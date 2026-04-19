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
	const maxNativeCallDepth = 48
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X3, maxNativeCallDepth)
	asm.BCond(jit.CondGE, slowLabel)

	// Load function value from regs[funcSlot].
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))

	// --- R108: Monomorphic call IC fast path ---
	// Allocate this call site's cache slot (2 × uint64).
	icIdx := ec.nextCallCacheIndex
	ec.nextCallCacheIndex++
	cacheOff := icIdx * 16 // 2 uint64 per slot
	icHitLabel := ec.uniqueLabel("t2call_ic_hit")
	icDoneLabel := ec.uniqueLabel("t2call_ic_done")

	// Load cache base + cached closure value + compare.
	asm.LDR(jit.X3, mRegCtx, execCtxOffTier2CallCache) // X3 = &CallCache[0]
	asm.LDR(jit.X4, jit.X3, cacheOff)                   // X4 = cached boxed value
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
	asm.CBZ(jit.X2, slowLabel)                           // not compiled -> slow

	// R108: update IC cache with the boxed closure value (reload from
	// memory since X0 now holds the raw ptr) and the direct entry addr.
	asm.LDR(jit.X4, mRegRegs, slotOffset(funcSlot)) // re-load boxed value
	asm.STR(jit.X4, jit.X3, cacheOff)                // cache[0] = boxed value
	asm.STR(jit.X2, jit.X3, cacheOff+8)              // cache[1] = direct entry
	asm.B(icDoneLabel)

	// --- IC Hit: re-derive X0=*Closure and X1=*Proto ---
	// X0 still holds the boxed closure value (matched cache).
	// X2 already holds the cached direct-entry addr (from cache CMP setup).
	asm.Label(icHitLabel)
	asm.LDR(jit.X2, jit.X3, cacheOff+8)        // X2 = cached direct entry
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)     // X0 = *Closure
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)  // X1 = *Proto (needed downstream)

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

	// Step 7: Save caller state on stack (64 bytes, 16-byte aligned).
	// R111: for a static self-call, CallMode and GlobalCache are
	// invariant (same proto → same GlobalCache; CallMode stays 1).
	// Skip saving these two fields (4 insns: 2 LDR + 2 STR).
	asm.SUBimm(jit.SP, jit.SP, 64)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STP(mRegRegs, mRegConsts, jit.SP, 16)
	staticSelf := ec.fn != nil && ec.fn.Proto != nil && ec.fn.Proto.HasSelfCalls && ec.isStaticSelfCall(instr)
	if !staticSelf {
		asm.LDR(jit.X3, mRegCtx, execCtxOffCallMode)
		asm.STR(jit.X3, jit.SP, 32)
	}
	// Save caller's ClosurePtr (always — closure instance may differ).
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X3, jit.SP, 40)
	if !staticSelf {
		asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
		asm.STR(jit.X3, jit.SP, 48)
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

	// R111: skip CallMode/GlobalCache setup on static self-call
	// (caller already has CallMode=1 — we'll reach here via t2_direct_entry
	// or the caller-side setup — and GlobalCache is per-proto so unchanged).
	if !staticSelf {
		// Set CallMode = 1 (direct call).
		asm.MOVimm16(jit.X3, 1)
		asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)

		// Load callee's GlobalValCache from Proto.
		asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	}

	// Increment NativeCallDepth.
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	// R40: Self-call fast path via HasSelfCalls flag.
	// R110: If we can STATICALLY prove this particular OpCall is a self-call
	// (fn arg is OpGetGlobal of our own proto's name), skip the runtime
	// Proto compare entirely and BL direct to t2_self_entry.
	// R137 Layer 4 (caller side): numeric BL enters pass-2 which returns
	// RAW int in X0 via num_epilogue (no BRV write). Post-BL consumes
	// raw X0, boxes for regs[funcSlot] memory safety, storeRawInt into
	// the Call's SSA home. Gated on funcSlot >= numericParamCount so
	// pass-2 LoadSlot of a numeric-param slot doesn't read a NaN-boxed
	// value as raw. When that gate fails, fall through to normal BL/BLR
	// path (pass-1 body, writes BRV normally).
	usedNumericBL := false
	if ec.isNumericStaticSelfCall(instr) && funcSlot >= ec.numericParamCount {
		nParams := ec.fn.Proto.NumParams
		ec.emitNumericArgsInRegs(instr, nParams)
		switch nParams {
		case 1:
			asm.BL("t2_numeric_self_entry_1")
		case 2:
			asm.BL("t2_numeric_self_entry_2")
		case 3:
			asm.BL("t2_numeric_self_entry_3")
		case 4:
			asm.BL("t2_numeric_self_entry_4")
		}
		usedNumericBL = true
	} else if ec.fn != nil && ec.fn.Proto != nil && ec.fn.Proto.HasSelfCalls {
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
	if !staticSelf {
		asm.LDR(jit.X3, jit.SP, 32)
		asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	}
	asm.LDR(jit.X3, jit.SP, 40)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	if !staticSelf {
		asm.LDR(jit.X3, jit.SP, 48)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	}
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 64)

	// Restore ctx pointers.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	// Step 11: Check callee exit code.
	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X3, exitHandleLabel)

	// R143: save compile-time rawIntRegs state BEFORE the post-BL
	// block. The post-BL emit calls emitReloadSelectiveForCall (which
	// clears rawIntRegs entries for reloaded values) and storeRawInt
	// (which adds entries). These mutations persist into the emitter's
	// state and leak into the compile of the mutually-exclusive exit-
	// handler below (emitCallExitFallback → emitStoreAllActiveRegs).
	// That leak corrupted v23's stored memory to RAW at ack's v28 exit
	// path, which was then read as RAW at v29's tail-call arg staging —
	// the root cause of R136-R142's ack hang.
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}

	if usedNumericBL {
		// R137 Layer 4 consume: X0 holds raw int64 from callee's
		// num_epilogue. Box via scratch + STR to regs[funcSlot] for
		// Lua-convention / deopt safety (preserves X0). Reload live
		// SSA regs (preserves X0). storeRawInt writes raw X0 to the
		// Call's SSA home and marks rawIntRegs so downstream arith
		// and GuardType(TypeInt) pass through.
		jit.EmitBoxIntFast(asm, jit.X1, jit.X0, mRegTagInt)
		asm.STR(jit.X1, mRegRegs, slotOffset(funcSlot))
		ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
		ec.storeRawInt(jit.X0, instr.ID)
	} else {
		// Normal return: read NaN-boxed result from BaselineReturnValue.
		asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
		// Store result to regs[funcSlot] (overwrites the function slot, Lua convention).
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))

		// Step 12: Reload only live SSA registers from memory.
		ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)

		// Step 13: Store result into the SSA value's home.
		asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
		ec.storeResultNB(jit.X0, instr.ID)
	}

	asm.B(doneLabel)

	// --- Callee exited mid-execution (deopt/op-exit within callee) ---
	// Both callee-exit and slow-path share a single exit-resume sequence.
	// R143: restore pre-post-BL rawIntRegs so emitStoreAllActiveRegs
	// correctly boxes values that were raw before the post-BL mutations.
	// After emitCallExitFallback returns, ec.rawIntRegs reflects the
	// exit-path's final state (emitReloadAllActiveRegs cleared everything,
	// storeResultNB set activeRegs only) — that's the correct compile
	// state for downstream emit because the exit-path + normal-path
	// converge with the same runtime NaN-boxed invariant at doneLabel.
	asm.Label(exitHandleLabel)
	asm.Label(slowLabel)
	ec.rawIntRegs = savedRawIntRegs
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)

	// --- Done: merge point for native and slow paths ---
	asm.Label(doneLabel)
}

// emitOpCall dispatches OpCall to the regular or tail variant and
// applies the post-call invalidation of cross-block verification caches.
// Extracted from emit_dispatch.go to keep that file under rule 13's
// 1000-line cap.
func (ec *emitContext) emitOpCall(instr *Instr) {
	if ec.tailCallInstrs[instr.ID] {
		// R107: tail call — frame-replacing BR on the fast path. The
		// slow-path fallback (emitCallExitFallback) still produces a
		// normal return value, so we DO emit the following OpReturn:
		// on the fast path it's dead code (BR already transferred
		// control), on the slow path it correctly completes the call.
		ec.emitCallNativeTail(instr)
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

// emitCallNativeTail emits a tail-call variant of OpCall: when the Call's
// result is returned immediately (Call→Return pattern in the same block),
// we replace our stack frame with the callee's instead of stacking a new
// one. Eliminates caller frame save/restore + BLR/RET overhead, and stops
// stack growth for tail-recursive chains.
//
// Sequence:
//   1. Store fn + args to regs (same as emitCallNative step 1).
//   2. Closure type-check + resolve callee's DirectEntry + bounds check.
//   3. Copy args from regs[funcSlot+1..] to regs[0..nArgs-1] (tail window).
//   4. Set callee context: Constants, ClosurePtr, CallMode=1, GlobalCache.
//      Do NOT advance ctx.Regs (reuse current frame's register window).
//      Do NOT increment NativeCallDepth (we're replacing, not nesting).
//   5. Inline epilogue: restore callee-saved X19..X28, FP/LR, ADD sp.
//   6. X0 = X19 (restored ctx pointer).
//   7. BR X2 (tail jump to callee's direct entry).
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
	cacheOff := icIdx * 16
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
	asm.CBZ(jit.X2, slowLabel)

	// R108 cache update on successful miss path.
	asm.LDR(jit.X4, mRegRegs, slotOffset(funcSlot))
	asm.STR(jit.X4, jit.X3, cacheOff)
	asm.STR(jit.X2, jit.X3, cacheOff+8)
	asm.B(icDoneLabel)

	// --- IC Hit: re-derive X0=*Closure and X1=*Proto ---
	asm.Label(icHitLabel)
	asm.LDR(jit.X2, jit.X3, cacheOff+8)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)

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

	// Step 6: callee's direct entry expects X0 = ctx pointer. X19 was just
	// restored to our original ctx value — copy it to X0.
	asm.MOVreg(jit.X0, jit.X19)

	// Step 7: Tail jump to callee's direct entry (no link register update).
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
	for i := 1; i < len(instr.Args); i++ {
		argID := instr.Args[i].ID
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
		src0 := ec.resolveRawInt(instr.Args[1].ID, jit.X0)
		src1 := ec.resolveRawInt(instr.Args[2].ID, jit.X1)
		if src0 == jit.X1 && src1 == jit.X0 {
			asm.MOVreg(jit.X2, jit.X0)
			asm.MOVreg(jit.X0, jit.X1)
			asm.MOVreg(jit.X1, jit.X2)
			return
		}
		if src0 == jit.X1 {
			// Move arg0 out of X1 first to avoid clobber.
			asm.MOVreg(jit.X0, jit.X1)
			if src1 != jit.X1 {
				asm.MOVreg(jit.X1, src1)
			}
			return
		}
		if src0 != jit.X0 {
			asm.MOVreg(jit.X0, src0)
		}
		if src1 != jit.X1 {
			asm.MOVreg(jit.X1, src1)
		}
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

// qualifyForNumeric (R121) reports whether a proto is structurally eligible
// for the end-to-end numeric calling convention. This is the scaffolding
// predicate; R122+ uses it to decide whether to compile a numeric twin.
// Returns (ok, numParams). When ok is true, numParams is in [1, 4].
//
// Current criteria (R121): 1-4 params, no upvalues, no nested protos.
// Future tightening (R123): return-flow analysis proves int return.
func qualifyForNumeric(proto *vm.FuncProto) (bool, int) {
	if proto == nil {
		return false, 0
	}
	if proto.NumParams < 1 || proto.NumParams > 4 {
		return false, 0
	}
	if len(proto.Upvalues) != 0 {
		return false, 0
	}
	if len(proto.Protos) != 0 {
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
	asm.LoadImm64(jit.X0, ExitCallExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

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
//   1. It's currently active in a register, AND
//   2. It's used by any instruction AFTER the call in the same block, OR
//   3. It's used by a phi in a successor block (cross-block live).
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
		} else {
			ec.asm.STR(reg, mRegRegs, slotOffset(slot))
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
