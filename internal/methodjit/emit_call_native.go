//go:build darwin && arm64

// emit_call_native.go implements native ARM64 BLR calls for the Tier 2 Method JIT.
//
// When Tier 2 encounters OpCall, instead of exiting to Go (emitCallExit ~80ns),
// it emits a native BLR sequence (~10ns) identical to Tier 1's tier1_call.go.
// The key difference: Tier 2 must spill/reload SSA register allocations (GPR X20-X23,
// FPR D4-D11) around the BLR since the callee is free to use the same registers.
//
// Native call sequence:
//   1. Store function value and arguments to the VM register file
//   2. Spill ALL live SSA registers (GPR + FPR) to their home slots
//   3. Type check: is the function a compiled VMClosure?
//   4. Load DirectEntryPtr; if zero (uncompiled), fall to slow path
//   5. Bounds check: callee register window fits in register file
//   6. Increment callee's CallCount (for tiering)
//   7. Save caller state on native stack (X26, X27, FP, LR, CallMode, etc.)
//   8. Copy args to callee register window
//   9. Set up callee context, BLR to callee's direct entry
//  10. Restore caller state from stack
//  11. Check callee exit code
//  12. Reload ALL live SSA registers from home slots
//  13. Store result to SSA value's home
//
// Slow path: falls back to emitCallExit (exit-resume via Go).

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

const (
	// Tier 2 direct/self entries use a full 128-byte frame. executeTier2
	// reserves native stack budget before entering JIT code, so this can be
	// higher than the no-reserve emergency limit while still avoiding Go stack
	// guard corruption.
	maxNativeCallDepth = 128

	// Raw-int self calls use a 64-byte caller shim plus a 16-byte numeric
	// callee frame, much smaller than the boxed direct-entry frame. Keep this
	// separate so Ackermann-style raw recursion does not bounce through
	// ExitCallExit at the generic boxed-call depth boundary.
	maxRawSelfCallDepth = 512

	// Raw-int self BL remains behind a kill switch, but the v1 entry,
	// resume, fallback, and return contract is now wired through
	// emitCallNativeRawIntSelf.
	enableNumericSelfBL = true
)

const (
	rawSelfFrameSize = 48
	rawSelfRegsOff   = 0
	rawSelfArgsOff   = 8
)

// emitCallNative emits a native BLR call sequence for OpCall in Tier 2.
// Uses selective spill/reload of SSA registers around the BLR: only registers
// that are actually live across the call point are saved/restored. Falls back
// to emitCallExit on the slow path (non-closure, uncompiled, overflow, etc.).
func (ec *emitContext) emitCallNative(instr *Instr) {
	asm := ec.asm

	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}

	// Step 1: Store the function value and arguments to the VM register file.
	// This must happen BEFORE spilling, since resolveValueNB may read from
	// SSA registers that we're about to spill.
	if len(instr.Args) > 0 {
		fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if fnReg != jit.X0 {
			asm.MOVreg(jit.X0, fnReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	}
	for i := 1; i < len(instr.Args); i++ {
		argReg := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if argReg != jit.X0 {
			asm.MOVreg(jit.X0, argReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	// Step 2: Selectively spill only registers that are LIVE across this call.
	// A value is live across the call if it's used by any instruction after the
	// call in the same block, or is used by a phi in a successor block.
	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillSelectiveForCall(liveGPRs, liveFPRs)

	// Labels for the native call path.
	slowLabel := ec.uniqueLabel("t2call_slow")
	doneLabel := ec.uniqueLabel("t2call_done")
	exitHandleLabel := ec.uniqueLabel("t2call_callee_exit")

	// Callee base offset: past ALL Tier 2 slots (NumRegs + temp slots).
	// This prevents the callee's register window from clobbering our SSA temp slots.
	calleeBaseOff := ec.nextSlot * jit.ValueSize

	// Step 3: Check NativeCallDepth limit.
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X3, maxNativeCallDepth)
	asm.BCond(jit.CondGE, slowLabel)

	// Load function value from regs[funcSlot].
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))

	// --- R108: Monomorphic call IC fast path ---
	// Allocate this call site's cache slot (4 × uint64).
	icIdx := ec.nextCallCacheIndex
	ec.nextCallCacheIndex++
	cacheOff := icIdx * 32 // 4 uint64 per slot
	icHitLabel := ec.uniqueLabel("t2call_ic_hit")
	icDoneLabel := ec.uniqueLabel("t2call_ic_done")

	// Load cache base + cached closure value + compare.
	asm.LDR(jit.X3, mRegCtx, execCtxOffTier2CallCache) // X3 = &CallCache[0]
	asm.LDR(jit.X4, jit.X3, cacheOff)                  // X4 = cached boxed value
	asm.CMPreg(jit.X0, jit.X4)
	asm.BCond(jit.CondEQ, icHitLabel)

	// --- IC Miss: original type check + proto load path ---
	// Type check: must be ptr (0xFFFF) with sub-type = 8 (VMClosure).
	asm.LSRimm(jit.X1, jit.X0, 48)
	asm.MOVimm16(jit.X2, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)

	// Check sub-type == 8.
	asm.LSRimm(jit.X1, jit.X0, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X2, 0xF)
	asm.ANDreg(jit.X1, jit.X1, jit.X2)
	asm.CMPimm(jit.X1, nbPtrSubVMClosure)
	asm.BCond(jit.CondNE, slowLabel)

	// Step 4: Extract raw pointer -> X0 = *vm.Closure.
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)

	// Load Proto, DirectEntryPtr.
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)          // X1 = *FuncProto
	asm.LDR(jit.X2, jit.X1, funcProtoOffDirectEntryPtr) // X2 = DirectEntryPtr
	missHaveEntryLabel := ec.uniqueLabel("t2call_miss_have_entry")
	asm.CBNZ(jit.X2, missHaveEntryLabel)
	asm.LDR(jit.X2, jit.X1, funcProtoOffTier2DirectEntryPtr)
	asm.Label(missHaveEntryLabel)
	asm.CBZ(jit.X2, slowLabel) // not compiled -> slow

	// R108: update IC cache with the boxed closure value (reload from
	// memory since X0 now holds the raw ptr) and the direct entry addr.
	asm.LDR(jit.X4, mRegRegs, slotOffset(funcSlot)) // re-load boxed value
	asm.STR(jit.X4, jit.X3, cacheOff)               // cache[0] = boxed value
	asm.STR(jit.X2, jit.X3, cacheOff+8)             // cache[1] = direct entry
	asm.STR(jit.X0, jit.X3, cacheOff+16)            // cache[2] = *Closure
	asm.STR(jit.X1, jit.X3, cacheOff+24)            // cache[3] = *Proto
	asm.B(icDoneLabel)

	// --- IC Hit: recover cached raw pointers and refresh direct entry. ---
	// X0 still holds the boxed closure value (matched cache).
	asm.Label(icHitLabel)
	asm.LDR(jit.X2, jit.X3, cacheOff+8)  // X2 = cached direct entry
	asm.LDR(jit.X0, jit.X3, cacheOff+16) // X0 = *Closure
	asm.LDR(jit.X1, jit.X3, cacheOff+24) // X1 = *Proto
	asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryPtr)
	icRefreshLabel := ec.uniqueLabel("t2call_ic_refresh")
	asm.CBNZ(jit.X4, icRefreshLabel)
	asm.LDR(jit.X4, jit.X1, funcProtoOffTier2DirectEntryPtr)
	// DirectEntryPtr can be cleared when a baseline/native caller disables
	// generic BLR after an exit. Tier 2 ICs may still use the separate Tier 2
	// entry while it is published, but must not keep a stale entry after both
	// published entry pointers have been cleared.
	asm.CBZ(jit.X4, slowLabel)
	asm.Label(icRefreshLabel)
	asm.CMPreg(jit.X2, jit.X4)
	asm.BCond(jit.CondEQ, icDoneLabel)
	asm.MOVreg(jit.X2, jit.X4)
	asm.STR(jit.X2, jit.X3, cacheOff+8)

	asm.Label(icDoneLabel)

	// Step 5: Bounds check: callee register window fits in register file.
	asm.LDR(jit.X3, jit.X1, funcProtoOffMaxStack) // X3 = calleeMaxStack (int)
	asm.LSLimm(jit.X3, jit.X3, 3)                 // X3 = calleeMaxStack * 8
	if calleeBaseOff <= 4095 {
		asm.ADDimm(jit.X3, jit.X3, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X4, int64(calleeBaseOff))
		asm.ADDreg(jit.X3, jit.X3, jit.X4)
	}
	asm.ADDreg(jit.X3, jit.X3, mRegRegs) // X3 = mRegRegs + calleeBaseOff + calleeMaxStack*8
	asm.LDR(jit.X4, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondHI, slowLabel) // unsigned greater than -> slow path

	// Step 6: Increment callee's CallCount for tiering.
	// X0 = *vm.Closure, X1 = *FuncProto, X2 = DirectEntryPtr.
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	// If at Tier 2 threshold, fall to slow path to trigger compilation.
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel)

	// Step 7: Save caller state on stack (80 bytes, 16-byte aligned).
	// R111: for a static self-call, GlobalCache is invariant
	// (same proto → same GlobalCache), so skip saving that field.
	// CallMode cannot be skipped: top-level Tier 2 enters with CallMode=0,
	// while a BL/BLR callee must return through the direct epilogue.
	asm.SUBimm(jit.SP, jit.SP, 80)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STP(mRegRegs, mRegConsts, jit.SP, 16)
	staticSelf := ec.fn != nil && ec.fn.Proto != nil && ec.fn.Proto.HasSelfCalls && ec.isStaticSelfCall(instr)
	asm.LDR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.STR(jit.X3, jit.SP, 32)
	// Save caller's ClosurePtr (always — closure instance may differ).
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X3, jit.SP, 40)
	if !staticSelf {
		asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
		asm.STR(jit.X3, jit.SP, 48)
		asm.LDR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
		asm.STR(jit.X3, jit.SP, 56)
		asm.LDR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
		asm.STR(jit.X3, jit.SP, 64)
	}

	// Step 8: Copy args to callee register window.
	for i := 0; i < nArgs; i++ {
		srcOff := slotOffset(funcSlot + 1 + i)
		dstOff := calleeBaseOff + i*jit.ValueSize
		asm.LDR(jit.X3, mRegRegs, srcOff)
		asm.STR(jit.X3, mRegRegs, dstOff)
	}

	// Step 9: Set up callee context and BLR.
	// Advance mRegRegs to callee base.
	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X3, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X3)
	}
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)

	// Load callee's constants.
	asm.LDR(mRegConsts, jit.X1, funcProtoOffConstants)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	// Set callee's ClosurePtr.
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr)

	// Set CallMode = 1 (direct call).
	asm.MOVimm16(jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)

	// R111: skip GlobalCache setup on static self-call (per-proto invariant).
	if !staticSelf {
		// Load callee's GlobalValCache from Proto.
		asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
		asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalCachePtr)
		asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
		asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalCacheGenPtr)
		asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
	}

	// Increment NativeCallDepth.
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	// R40/R110: self-call fast path via HasSelfCalls. Statically proven
	// raw-int self calls are routed before this function to the dedicated
	// emitCallNativeRawIntSelf protocol; the generic path always keeps the
	// boxed VM call/return ABI.
	if ec.fn != nil && ec.fn.Proto != nil && ec.fn.Proto.HasSelfCalls {
		asm.MOVreg(jit.X0, mRegCtx)
		if ec.isStaticSelfCall(instr) {
			// R110: static self-call — 1 insn.
			asm.BL("t2_self_entry")
		} else {
			selfCallLabel := ec.uniqueLabel("t2call_do_self")
			afterBlLabel := ec.uniqueLabel("t2call_after_bl")
			// X1 still holds *FuncProto from step 4 load.
			asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(ec.fn.Proto))))
			asm.CMPreg(jit.X1, jit.X3)
			asm.BCond(jit.CondEQ, selfCallLabel)
			// Non-self: original BLR path.
			asm.BLR(jit.X2)
			asm.B(afterBlLabel)
			// Self-call: PC-relative BL to lightweight entry
			// (t2_self_entry skips 4 redundant setup insns vs t2_direct_entry).
			asm.Label(selfCallLabel)
			asm.BL("t2_self_entry")
			asm.Label(afterBlLabel)
		}
	} else {
		asm.MOVreg(jit.X0, mRegCtx)
		asm.BLR(jit.X2)
	}

	// Decrement NativeCallDepth.
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	// Step 10: Restore caller state.
	asm.LDP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, jit.SP, 32)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.LDR(jit.X3, jit.SP, 40)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	if !staticSelf {
		asm.LDR(jit.X3, jit.SP, 48)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
		asm.LDR(jit.X3, jit.SP, 56)
		asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
		asm.LDR(jit.X3, jit.SP, 64)
		asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
	}
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 80)

	// Restore ctx pointers.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	// Step 11: Check callee exit code.
	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X3, exitHandleLabel)

	// R143: save rawIntRegs BEFORE the post-BL emit — emitReloadSelectiveForCall
	// clears entries, which would leak into the mutually-exclusive exit-
	// handler compile below (emitStoreAllActiveRegs sees wrong state).
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}

	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)

	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)
	postSuccessRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		postSuccessRawIntRegs[k] = v
	}

	asm.B(doneLabel)

	// --- Callee exited mid-execution (deopt/op-exit within callee) ---
	// Both callee-exit and slow-path share a single exit-resume sequence.
	// R143 restore pre-post-BL rawIntRegs so emitStoreAllActiveRegs
	// correctly boxes values that WERE raw before the post-BL reload.
	asm.Label(exitHandleLabel)
	asm.Label(slowLabel)
	ec.rawIntRegs = savedRawIntRegs
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postSuccessRawIntRegs)
	ec.rawIntRegs = postSuccessRawIntRegs

	// --- Done: merge point for native and slow paths ---
	asm.Label(doneLabel)
}

// emitCallNativeStaticSelfFast emits the boxed-value self-call path for a
// statically proven recursive call. It keeps the same public contract as the
// generic native call path (boxed args/results in the VM register file,
// ExitCode checked after return), but skips closure type checks, the
// monomorphic call IC, proto/direct-entry loads, global-cache switching, and
// the full callee-save frame on the recursive entry.
func (ec *emitContext) emitCallNativeStaticSelfFast(instr *Instr) {
	if ec.fn == nil || ec.fn.Proto == nil || !ec.fn.Proto.HasSelfCalls || !ec.isStaticSelfCall(instr) {
		ec.emitCallNative(instr)
		return
	}

	asm := ec.asm
	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}

	if len(instr.Args) > 0 {
		fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if fnReg != jit.X0 {
			asm.MOVreg(jit.X0, fnReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	}
	for i := 1; i < len(instr.Args); i++ {
		argReg := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if argReg != jit.X0 {
			asm.MOVreg(jit.X0, argReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillSelectiveForCall(liveGPRs, liveFPRs)

	slowLabel := ec.uniqueLabel("t2self_slow")
	doneLabel := ec.uniqueLabel("t2self_done")
	exitHandleLabel := ec.uniqueLabel("t2self_callee_exit")

	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}

	calleeBaseOff := ec.nextSlot * jit.ValueSize

	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X3, maxNativeCallDepth)
	asm.BCond(jit.CondGE, slowLabel)

	calleeFrameBytes := ec.nextSlot * jit.ValueSize
	if calleeBaseOff+calleeFrameBytes <= 4095 {
		asm.ADDimm(jit.X3, mRegRegs, uint16(calleeBaseOff+calleeFrameBytes))
	} else {
		asm.LoadImm64(jit.X3, int64(calleeBaseOff+calleeFrameBytes))
		asm.ADDreg(jit.X3, mRegRegs, jit.X3)
	}
	asm.LDR(jit.X4, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondHI, slowLabel)

	asm.SUBimm(jit.SP, jit.SP, 64)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.STR(jit.X3, jit.SP, 32)
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X3, jit.SP, 40)

	for i := 0; i < nArgs; i++ {
		srcOff := slotOffset(funcSlot + 1 + i)
		dstOff := calleeBaseOff + i*jit.ValueSize
		asm.LDR(jit.X3, mRegRegs, srcOff)
		asm.STR(jit.X3, mRegRegs, dstOff)
	}

	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X3, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X3)
	}
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.MOVimm16(jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)

	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	asm.MOVreg(jit.X0, mRegCtx)
	asm.BL("t2_self_entry")

	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	asm.LDP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, jit.SP, 32)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.LDR(jit.X3, jit.SP, 40)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 64)
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X3, exitHandleLabel)

	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)

	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)
	postSuccessRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		postSuccessRawIntRegs[k] = v
	}
	asm.B(doneLabel)

	asm.Label(exitHandleLabel)
	asm.Label(slowLabel)
	ec.rawIntRegs = savedRawIntRegs
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postSuccessRawIntRegs)
	ec.rawIntRegs = postSuccessRawIntRegs

	asm.Label(doneLabel)
}

// emitCallNativeRawIntSelf emits the v1 raw-int self-recursive ABI. It is a
// dedicated static-self path rather than another branch inside emitCallNative:
// args enter the callee as raw ints in X0..X3, success returns raw int in X0,
// and every fallback materializes a normal boxed VM call frame before
// ExitCallExit.
func (ec *emitContext) emitCallNativeRawIntSelf(instr *Instr) {
	if !enableNumericSelfBL || ec.fn == nil || ec.fn.Proto == nil || !ec.isNumericStaticSelfCall(instr) {
		ec.emitCallNativeStaticSelfFast(instr)
		return
	}

	asm := ec.asm
	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}
	nParams := ec.fn.Proto.NumParams
	if nArgs != nParams || nParams < 1 || nParams > 4 {
		ec.emitCallNativeStaticSelfFast(instr)
		return
	}

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	if len(liveGPRs) > 0 || len(liveFPRs) > 0 {
		ec.emitSpillSelectiveForCall(liveGPRs, liveFPRs)
	}

	slowLabel := ec.uniqueLabel("t2rawself_slow")
	exitLabel := ec.uniqueLabel("t2rawself_exit")
	doneLabel := ec.uniqueLabel("t2rawself_done")

	preRawIntRegs := cloneBoolMap(ec.rawIntRegs)

	// Raw-call frame:
	//   0       saved caller mRegRegs
	//   8..39   raw int args X0..X3
	//
	// The caller's own entry frame already owns FP/LR, and raw-int self calls
	// stay within one proto/closure/constant domain. We keep the callee base in
	// mRegRegs instead of round-tripping through ctx.Regs. Before any fallback
	// to Go, emitRestoreRawSelfCallerState writes the caller base back into
	// ctx.Regs so resume handlers still see the boxed VM ABI state. The boxed
	// function operand needed by VM fallback is rebuilt from BaselineClosurePtr;
	// static self recursion cannot change closure identity while this native
	// frame is executing. Numeric entries return through num_epilogue and do
	// not branch on CallMode, so raw self calls leave ctx.CallMode unchanged.
	asm.SUBimm(jit.SP, jit.SP, rawSelfFrameSize)
	asm.STR(mRegRegs, jit.SP, rawSelfRegsOff)

	ec.emitNumericArgsInRegs(instr, nParams)
	for i := 0; i < nParams; i++ {
		argReg := jit.Reg(int(jit.X0) + i)
		asm.STR(argReg, jit.SP, rawSelfArgsOff+i*jit.ValueSize)
	}

	calleeBaseOff := ec.nextSlot * jit.ValueSize

	asm.LDR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X7, maxRawSelfCallDepth)
	asm.BCond(jit.CondGE, slowLabel)

	calleeFrameBytes := ec.nextSlot * jit.ValueSize
	if calleeBaseOff+calleeFrameBytes <= 4095 {
		asm.ADDimm(jit.X7, mRegRegs, uint16(calleeBaseOff+calleeFrameBytes))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff+calleeFrameBytes))
		asm.ADDreg(jit.X7, mRegRegs, jit.X8)
	}
	asm.LDR(jit.X8, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondHI, slowLabel)

	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X7, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X7)
	}

	asm.LDR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X7, jit.X7, 1)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)

	asm.BL(fmt.Sprintf("t2_numeric_self_entry_%d", nParams))

	asm.LDR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X7, jit.X7, 1)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)

	asm.LDR(jit.X7, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X7, exitLabel)

	ec.emitRestoreRawSelfCallerState()
	asm.ADDimm(jit.SP, jit.SP, rawSelfFrameSize)
	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(preRawIntRegs)
	ec.rawIntRegs = cloneBoolMap(preRawIntRegs)
	ec.storeRawInt(jit.X0, instr.ID)
	postRawIntRegs := cloneBoolMap(ec.rawIntRegs)
	asm.B(doneLabel)

	asm.Label(exitLabel)
	asm.Label(slowLabel)
	ec.emitRestoreRawSelfCallerState()
	ec.rawIntRegs = cloneBoolMap(preRawIntRegs)
	ec.emitMaterializeRawIntSelfCallFrameFromSelfClosure(funcSlot, nArgs, rawSelfArgsOff)
	asm.ADDimm(jit.SP, jit.SP, rawSelfFrameSize)
	ec.emitRawIntSelfCallExitResume(instr, funcSlot, nArgs, nRets, preRawIntRegs, liveGPRs, liveFPRs)
	ec.rawIntRegs = postRawIntRegs

	asm.Label(doneLabel)
}

func (ec *emitContext) emitRestoreRawSelfCallerState() {
	asm := ec.asm
	asm.LDR(mRegRegs, jit.SP, rawSelfRegsOff)
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
}

func (ec *emitContext) emitBoxCurrentClosure(dst, scratch jit.Reg) {
	ec.asm.LDR(dst, mRegCtx, execCtxOffBaselineClosurePtr)
	ec.asm.UBFX(dst, dst, 0, 44)
	ec.asm.LoadImm64(scratch, nbClosureTagBits)
	ec.asm.ORRreg(dst, dst, scratch)
}

func (ec *emitContext) emitMaterializeRawIntSelfCallFrameFromSelfClosure(funcSlot, nArgs, rawArgOff int) {
	asm := ec.asm
	ec.emitBoxCurrentClosure(jit.X0, jit.X1)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	for i := 0; i < nArgs; i++ {
		asm.LDR(jit.X0, jit.SP, rawArgOff+i*jit.ValueSize)
		jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+1+i))
	}
}

func (ec *emitContext) emitRawIntSelfCallExitResume(instr *Instr, funcSlot, nArgs, nRets int, preRawIntRegs, liveGPRs, liveFPRs map[int]bool) {
	asm := ec.asm

	ec.recordExitResumeCheckSiteWithLive(
		instr,
		ExitCallExit,
		ec.exitResumeCheckLiveSlots(liveGPRs, liveFPRs),
		callExitModifiedSlots(funcSlot, nRets),
		exitResumeCheckOptions{RequireCallFunc: true, RequireRawIntArgs: true},
	)

	asm.LoadImm64(jit.X0, int64(funcSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallSlot)
	asm.LoadImm64(jit.X0, int64(nArgs))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNArgs)
	asm.LoadImm64(jit.X0, int64(nRets))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNRets)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitCallExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("call_continue_%d", instr.ID))
	asm.Label(continueLabel)

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(preRawIntRegs)
	ec.rawIntRegs = cloneBoolMap(preRawIntRegs)
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	resultIntLabel := ec.uniqueLabel("t2rawself_result_int")
	asm.LSRimm(jit.X1, jit.X0, 48)
	asm.MOVimm16(jit.X2, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondEQ, resultIntLabel)
	asm.LoadImm64(jit.X1, ExitDeopt)
	asm.STR(jit.X1, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}
	asm.Label(resultIntLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	ec.storeRawInt(jit.X0, instr.ID)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

// emitOpCall dispatches OpCall to the regular or tail variant and
// applies the post-call invalidation of cross-block verification caches.
// Extracted from emit_dispatch.go to keep that file under rule 13's
// 1000-line cap.
func (ec *emitContext) emitOpCall(instr *Instr) {
	if ec.numericMode && ec.tailCallInstrs[instr.ID] && ec.isNumericStaticSelfCall(instr) {
		ec.emitCallNativeNumericTail(instr)
	} else if !ec.tailCallInstrs[instr.ID] && ec.isNumericStaticSelfCall(instr) {
		ec.emitCallNativeRawIntSelf(instr)
	} else if ec.tailCallInstrs[instr.ID] && ec.isStaticSelfCall(instr) {
		ec.emitStaticSelfTailLoop(instr)
	} else if ec.isStaticSelfCall(instr) {
		ec.emitCallNativeStaticSelfFast(instr)
	} else if ec.tailCallInstrs[instr.ID] {
		// R107: tail call — frame-replacing BR on the fast path. The
		// slow-path fallback (emitCallExitFallback) still produces a
		// normal return value, so we DO emit the following OpReturn:
		// on the fast path it's dead code (BR already transferred
		// control), on the slow path it correctly completes the call.
		ec.emitCallNative(instr)
	} else {
		ec.emitCallNative(instr)
	}
	// Calls can modify any table's shape — invalidate verification caches.
	ec.shapeVerified = make(map[int]uint32)
	ec.tableVerified = make(map[int]bool)
	ec.kindVerified = make(map[int]uint16)
	ec.keysDirtyWritten = make(map[int]bool)
	ec.dmVerified = make(map[int]bool)
}

func (ec *emitContext) emitCallNativeNumericTail(instr *Instr) {
	asm := ec.asm
	slowLabel := ec.uniqueLabel("t2numtail_slow")

	if len(instr.Args) == 0 || ec.fn == nil || ec.fn.Proto == nil {
		asm.B(slowLabel)
	} else {
		ec.emitNumericArgsInRegs(instr, ec.fn.Proto.NumParams)
		asm.B(fmt.Sprintf("num_B%d", ec.fn.Entry.ID))
	}

	asm.Label(slowLabel)
	ec.emitCallNative(instr)
}

// emitStaticSelfTailLoop lowers a proven self tail-call into an in-frame loop.
// This avoids growing the native stack and also avoids the generic BR-to-direct
// tail path, whose context/slot protocol is too broad for recursive raw-int
// shapes. The preceding GetGlobal still runs, so cache misses and global exits
// happen before this point.
func (ec *emitContext) emitStaticSelfTailLoop(instr *Instr) {
	if ec.fn == nil || ec.fn.Proto == nil || ec.fn.Entry == nil {
		ec.emitCallNative(instr)
		return
	}
	nArgs := len(instr.Args) - 1
	if nArgs != ec.fn.Proto.NumParams || nArgs > 4 {
		ec.emitCallNative(instr)
		return
	}

	// Tail-call argument assignment is semantically parallel. Stage into
	// scratch registers that cannot be source homes for allocated SSA values
	// before overwriting parameter slots.
	scratch := []jit.Reg{jit.X4, jit.X5, jit.X6, jit.X7}
	for i := 0; i < nArgs; i++ {
		src := ec.resolveValueNB(instr.Args[1+i].ID, scratch[i])
		if src != scratch[i] {
			ec.asm.MOVreg(scratch[i], src)
		}
	}
	for i := 0; i < nArgs; i++ {
		ec.asm.STR(scratch[i], mRegRegs, slotOffset(i))
	}
	ec.asm.B(ec.blockLabelFor(ec.fn.Entry))
}

// emitCallNativeTail emits a tail-call variant of OpCall: when the Call's
// result is returned immediately (Call→Return pattern in the same block),
// we replace our stack frame with the callee's instead of stacking a new
// one. Eliminates caller frame save/restore + BLR/RET overhead, and stops
// stack growth for tail-recursive chains.
//
// Sequence:
//  1. Store fn + args to regs (same as emitCallNative step 1).
//  2. Closure type-check + resolve callee's DirectEntry + bounds check.
//  3. Copy args from regs[funcSlot+1..] to regs[0..nArgs-1] (tail window).
//  4. Set callee context: Constants, ClosurePtr, CallMode=1, GlobalCache.
//     Do NOT advance ctx.Regs (reuse current frame's register window).
//     Do NOT increment NativeCallDepth (we're replacing, not nesting).
//  5. Set X0 to the current ctx pointer, then inline our epilogue.
//  6. BR X2 (tail jump to callee's direct entry).
//
// Correctness: after step 5, LR is the CALLER-OF-CURRENT's return address
// (saved by our prologue at sp+0). After callee runs and does its own
// RET in its epilogue, it returns directly to caller-of-current, as
// required by TCO semantics.
//
// Slow-path fallback: emits the same emitCallExitFallback as emitCallNative
// for non-closure targets, uncompiled callees, or overflow cases.
func (ec *emitContext) emitCallNativeTail(instr *Instr) {
	asm := ec.asm

	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}

	// Step 1: Store fn + args to regs (same as emitCallNative).
	if len(instr.Args) > 0 {
		fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if fnReg != jit.X0 {
			asm.MOVreg(jit.X0, fnReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	}
	for i := 1; i < len(instr.Args); i++ {
		argReg := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if argReg != jit.X0 {
			asm.MOVreg(jit.X0, argReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	// For the slow-path fallback, still need all active regs in memory so
	// the Go-side handler can inspect them.
	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillSelectiveForCall(liveGPRs, liveFPRs)

	slowLabel := ec.uniqueLabel("t2tail_slow")

	// Step 2: Closure type check (ptr + sub-type 8), with R108 mono-IC
	// fast path.
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))

	icIdx := ec.nextCallCacheIndex
	ec.nextCallCacheIndex++
	cacheOff := icIdx * 32
	icHitLabel := ec.uniqueLabel("t2tail_ic_hit")
	icDoneLabel := ec.uniqueLabel("t2tail_ic_done")

	asm.LDR(jit.X3, mRegCtx, execCtxOffTier2CallCache)
	asm.LDR(jit.X4, jit.X3, cacheOff)
	asm.CMPreg(jit.X0, jit.X4)
	asm.BCond(jit.CondEQ, icHitLabel)

	// --- IC Miss: full type check + proto load ---
	asm.LSRimm(jit.X1, jit.X0, 48)
	asm.MOVimm16(jit.X2, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LSRimm(jit.X1, jit.X0, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X2, 0xF)
	asm.ANDreg(jit.X1, jit.X1, jit.X2)
	asm.CMPimm(jit.X1, nbPtrSubVMClosure)
	asm.BCond(jit.CondNE, slowLabel)

	// Extract raw pointer -> X0 = *vm.Closure.
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)

	// Load Proto (X1), DirectEntryPtr (X2).
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)
	asm.LDR(jit.X2, jit.X1, funcProtoOffDirectEntryPtr)
	tailMissHaveEntryLabel := ec.uniqueLabel("t2tail_miss_have_entry")
	asm.CBNZ(jit.X2, tailMissHaveEntryLabel)
	asm.LDR(jit.X2, jit.X1, funcProtoOffTier2DirectEntryPtr)
	asm.Label(tailMissHaveEntryLabel)
	asm.CBZ(jit.X2, slowLabel)

	// R108 cache update on successful miss path.
	asm.LDR(jit.X4, mRegRegs, slotOffset(funcSlot))
	asm.STR(jit.X4, jit.X3, cacheOff)
	asm.STR(jit.X2, jit.X3, cacheOff+8)
	asm.STR(jit.X0, jit.X3, cacheOff+16)
	asm.STR(jit.X1, jit.X3, cacheOff+24)
	asm.B(icDoneLabel)

	// --- IC Hit: recover cached raw pointers and refresh direct entry. ---
	asm.Label(icHitLabel)
	asm.LDR(jit.X2, jit.X3, cacheOff+8)
	asm.LDR(jit.X0, jit.X3, cacheOff+16)
	asm.LDR(jit.X1, jit.X3, cacheOff+24)
	asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryPtr)
	tailICRefreshLabel := ec.uniqueLabel("t2tail_ic_refresh")
	asm.CBNZ(jit.X4, tailICRefreshLabel)
	asm.LDR(jit.X4, jit.X1, funcProtoOffTier2DirectEntryPtr)
	asm.CBZ(jit.X4, slowLabel)
	asm.Label(tailICRefreshLabel)
	asm.CMPreg(jit.X2, jit.X4)
	asm.BCond(jit.CondEQ, icDoneLabel)
	asm.MOVreg(jit.X2, jit.X4)
	asm.STR(jit.X2, jit.X3, cacheOff+8)

	asm.Label(icDoneLabel)

	// Bounds check: callee window (at the TAIL base = 0) fits in register file.
	asm.LDR(jit.X3, jit.X1, funcProtoOffMaxStack)
	asm.LSLimm(jit.X3, jit.X3, 3)
	asm.ADDreg(jit.X3, jit.X3, mRegRegs)
	asm.LDR(jit.X4, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondHI, slowLabel)

	// CallCount increment for tiering.
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel)

	// Step 3: Copy args to tail window regs[0..nArgs-1]. Forward order is
	// safe because src = funcSlot+1+i > dst = i for all i >= 0.
	for i := 0; i < nArgs; i++ {
		srcOff := slotOffset(funcSlot + 1 + i)
		dstOff := slotOffset(i)
		if srcOff == dstOff {
			continue
		}
		asm.LDR(jit.X3, mRegRegs, srcOff)
		asm.STR(jit.X3, mRegRegs, dstOff)
	}

	// Step 4: Set callee context. ctx.Regs is UNCHANGED (reuse frame).
	asm.LDR(mRegConsts, jit.X1, funcProtoOffConstants)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr) // X0 = closure ptr
	asm.MOVimm16(jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	// Persist the (unchanged) mRegRegs back to ctx.Regs so callee's
	// direct-entry reload sees the correct base.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)

	// Step 5: Inline our own epilogue — restore callee-saved regs, FP/LR,
	// deallocate frame. Do NOT emit RET; we'll BR to callee instead.
	// X2 (direct entry addr) must survive; none of the LDP writes touch X2.
	// The callee direct entry expects X0=ctx. Capture it before restoring
	// X19, whose saved value belongs to our caller rather than this frame.
	asm.MOVreg(jit.X0, mRegCtx)
	if ec.useFPR {
		asm.FLDP(jit.D8, jit.D9, jit.SP, 96)
		asm.FLDP(jit.D10, jit.D11, jit.SP, 112)
	}
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))

	// Step 6: Tail jump to callee's direct entry (no link register update).
	asm.BR(jit.X2)

	// Slow-path fallback: falls back to the exit-resume path (which handles
	// return value normally — so the following OpReturn still runs correctly).
	asm.Label(slowLabel)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
}

// isNumericStaticSelfCall (R124) returns true when this OpCall can use
// the numeric self-call fast path: static-self (R110), proto qualifies
// for numeric (R121), all args are int-typed.
func (ec *emitContext) isNumericStaticSelfCall(instr *Instr) bool {
	if !ec.isStaticSelfCall(instr) {
		return false
	}
	ok, numParams := qualifyForNumeric(ec.fn.Proto)
	if !ok {
		return false
	}
	if len(instr.Args) != 1+numParams {
		return false
	}
	for i := 0; i < numParams; i++ {
		argID := instr.Args[1+i].ID
		if ec.hasReg(argID) && ec.rawIntRegs[argID] {
			continue
		}
		if ec.irTypes[argID] == TypeInt {
			continue
		}
		return false
	}
	return true
}

// emitNumericArgsInRegs (R124) materializes raw int64 args into X0..X(N-1)
// ahead of a BL t2_numeric_self_entry_N. Handles aliasing between arg
// sources and ABI registers.
func (ec *emitContext) emitNumericArgsInRegs(instr *Instr, nParams int) {
	asm := ec.asm
	if nParams == 1 {
		src0 := ec.resolveRawInt(instr.Args[1].ID, jit.X0)
		if src0 != jit.X0 {
			asm.MOVreg(jit.X0, src0)
		}
		return
	}
	if nParams == 2 {
		src0 := ec.resolveRawInt(instr.Args[1].ID, jit.X2)
		if src0 != jit.X2 {
			asm.MOVreg(jit.X2, src0)
		}
		src1 := ec.resolveRawInt(instr.Args[2].ID, jit.X3)
		if src1 != jit.X3 {
			asm.MOVreg(jit.X3, src1)
		}
		asm.MOVreg(jit.X0, jit.X2)
		asm.MOVreg(jit.X1, jit.X3)
		return
	}
	// nParams 3/4: conservative via X2/X3/X4/X5 scratch.
	scratchRegs := []jit.Reg{jit.X2, jit.X3, jit.X4, jit.X5}
	srcRegs := make([]jit.Reg, nParams)
	for i := 0; i < nParams; i++ {
		srcRegs[i] = ec.resolveRawInt(instr.Args[1+i].ID, scratchRegs[i])
		if srcRegs[i] != scratchRegs[i] {
			asm.MOVreg(scratchRegs[i], srcRegs[i])
		}
	}
	for i := 0; i < nParams; i++ {
		dst := jit.Reg(int(jit.X0) + i)
		asm.MOVreg(dst, scratchRegs[i])
	}
}

// qualifyForNumeric reports whether a proto is eligible for the raw-int
// self-recursive ABI. The predicate delegates to AnalyzeSpecializedABI so the
// compiler, tests, and future metadata all use the same structural contract.
// Returns (ok, numParams). When ok is true, numParams is in [1, 4].
func qualifyForNumeric(proto *vm.FuncProto) (bool, int) {
	abi := AnalyzeSpecializedABI(proto)
	if !abi.Eligible || abi.Kind != SpecializedABIRawInt {
		return false, 0
	}
	return true, proto.NumParams
}

// isStaticSelfCall (R110) returns true when OpCall's function argument is
// an OpGetGlobal whose resolved constant-pool name matches the current
// function's proto name. In that case the target is (statically) our
// own proto, so the runtime Proto compare can be elided and we can BL
// t2_self_entry directly.
func (ec *emitContext) isStaticSelfCall(instr *Instr) bool {
	if ec.fn == nil || ec.fn.Proto == nil || instr == nil {
		return false
	}
	if len(instr.Args) == 0 || instr.Args[0] == nil || instr.Args[0].Def == nil {
		return false
	}
	def := instr.Args[0].Def
	if def.Op != OpGetGlobal {
		return false
	}
	globalIdx := int(def.Aux)
	constants := ec.fn.Proto.Constants
	if globalIdx < 0 || globalIdx >= len(constants) {
		return false
	}
	kv := constants[globalIdx]
	if !kv.IsString() {
		return false
	}
	return kv.Str() == ec.fn.Proto.Name
}

// emitCallExitFallback emits the exit-resume sequence for a CALL that couldn't
// take the native BLR path. This is identical to emitCallExit but without the
// arg-store (args were already stored in emitCallNative step 1) and without
// re-spilling (already spilled in step 2).
//
// The fallback path also spills ALL active registers (not just live ones) because
// the Go-side exit handler may inspect any register in the register file.
func (ec *emitContext) emitCallExitFallback(instr *Instr, funcSlot, nArgs, nRets int) {
	asm := ec.asm

	// The selective spill from the native path only saved live-across-call values.
	// The Go-side handler needs all active registers in memory, so spill the rest.
	ec.recordExitResumeCheckSite(instr, ExitCallExit, callExitModifiedSlots(funcSlot, nRets), exitResumeCheckOptions{
		RequireCallFunc:   true,
		RequireRawIntArgs: ec.isNumericStaticSelfCall(instr),
	})
	ec.emitStoreAllActiveRegs()

	// Write call descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(funcSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallSlot)
	asm.LoadImm64(jit.X0, int64(nArgs))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNArgs)
	asm.LoadImm64(jit.X0, int64(nRets))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNRets)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallID)

	// Set ExitCode = ExitCallExit and return to Go.
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitCallExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label: the resume entry jumps here after Go handles the call.
	continueLabel := ec.passLabel(fmt.Sprintf("call_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Load call result from regs[funcSlot].
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record for deferred resume entry generation.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

// computeLiveAcrossCall returns the set of GPR and FPR value IDs that are live
// across a CALL instruction. A value is live across the call if:
//  1. It's currently active in a register, AND
//  2. It's used by any instruction AFTER the call in the same block, OR
//  3. It's used by a phi in a successor block (cross-block live).
//
// Typically only 1-3 registers are live across a call (e.g., fib(n) only has
// n live across each recursive call). This lets selective spill emit 1-3 STR
// instructions instead of 12 (4 GPRs + 8 FPRs).
func (ec *emitContext) computeLiveAcrossCall(callInstr *Instr) (gprLive map[int]bool, fprLive map[int]bool) {
	gprLive = make(map[int]bool)
	fprLive = make(map[int]bool)

	// Collect all value IDs used after the call in the same block.
	usedAfter := make(map[int]bool)
	block := callInstr.Block
	if block != nil {
		found := false
		for _, instr := range block.Instrs {
			if instr == callInstr {
				found = true
				continue
			}
			if !found {
				continue
			}
			for _, arg := range instr.Args {
				if arg != nil {
					usedAfter[arg.ID] = true
				}
			}
		}
	}

	// Check GPRs: is the active value used after the call or cross-block live?
	for valueID := range ec.activeRegs {
		if usedAfter[valueID] || ec.crossBlockLive[valueID] {
			gprLive[valueID] = true
		}
	}

	// Check FPRs: same criterion.
	for valueID := range ec.activeFPRegs {
		if usedAfter[valueID] || ec.crossBlockLive[valueID] {
			fprLive[valueID] = true
		}
	}

	return gprLive, fprLive
}

// emitSpillSelectiveForCall writes only the specified live register-resident
// values to their memory home slots. Called before a native BLR to save only
// registers that are actually needed after the call returns.
func (ec *emitContext) emitSpillSelectiveForCall(gprLive, fprLive map[int]bool) {
	for valueID := range gprLive {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		reg := jit.Reg(pr.Reg)
		if ec.rawIntRegs[valueID] {
			jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
			ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
			ec.emitExitResumeCheckShadowStoreGPR(slot, jit.X0)
		} else {
			ec.asm.STR(reg, mRegRegs, slotOffset(slot))
			ec.emitExitResumeCheckShadowStoreGPR(slot, reg)
		}
	}

	for valueID := range fprLive {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || !pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		fpr := jit.FReg(pr.Reg)
		ec.asm.FMOVtoGP(jit.X0, fpr)
		ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
		ec.emitExitResumeCheckShadowStoreGPR(slot, jit.X0)
	}
}

// emitReloadSelectiveForCall reloads only the specified live register-resident
// values from their memory home slots. Called after a native BLR to restore
// only registers that are needed after the call.
func (ec *emitContext) emitReloadSelectiveForCall(gprLive, fprLive map[int]bool) {
	for valueID := range gprLive {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		reg := jit.Reg(pr.Reg)
		ec.asm.LDR(reg, mRegRegs, slotOffset(slot))
		delete(ec.rawIntRegs, valueID)
	}

	for valueID := range fprLive {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || !pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		fpr := jit.FReg(pr.Reg)
		ec.asm.FLDRd(fpr, mRegRegs, slotOffset(slot))
	}
}

func cloneBoolMap(src map[int]bool) map[int]bool {
	dst := make(map[int]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
