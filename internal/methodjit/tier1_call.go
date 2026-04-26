//go:build darwin && arm64

// tier1_call.go emits ARM64 templates for native function calls in the Tier 1
// baseline compiler. Instead of exiting to Go for every OP_CALL (exit-resume),
// this emits a native BLR sequence that calls the callee's compiled code
// directly when the callee is a compiled vm.Closure.
//
// The native call sequence:
//   1. Load the function value from the register file
//   2. Type-check: must be a vm.Closure (ptrSubVMClosure = 8)
//   3. Load Proto.CompiledCodePtr; if zero, fall to slow path
//   4. Save caller state on stack (X26, X27, X29, X30)
//   5. Copy arguments from caller's registers to callee's register window
//   6. Set up callee's context (Regs, Constants, ClosurePtr)
//   7. BLR to callee's direct entry point
//   8. Restore caller state from stack
//   9. Check callee exit code (0 = normal return)
//  10. Move return value to destination register
//
// Supports variable-return (C=0) and variable-arg (B=0) calls natively
// by reading/writing Top via TopPtr in ExecContext.
//
// Falls back to the existing exit-resume path (slow path) for:
//   - GoFunctions
//   - Uncompiled closures (CompiledCodePtr == 0)
//   - Non-function values
//   - Register file overflow (callee window exceeds allocated regs)

package methodjit

import (
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

// Struct layout constants for vm.Closure and vm.FuncProto.
// Verified at init time via unsafe.Offsetof.
var (
	vmClosureOffProto    int // vm.Closure.Proto offset (should be 0)
	vmClosureOffUpvalues int // vm.Closure.Upvalues offset (should be 8)

	funcProtoOffCompiledCodePtr        int // vm.FuncProto.CompiledCodePtr offset
	funcProtoOffDirectEntryPtr         int // vm.FuncProto.DirectEntryPtr offset
	funcProtoOffTier2DirectEntryPtr    int // vm.FuncProto.Tier2DirectEntryPtr offset
	funcProtoOffTier2NumericEntryPtr   int // vm.FuncProto.Tier2NumericEntryPtr offset
	funcProtoOffConstants              int // vm.FuncProto.Constants offset (slice header)
	funcProtoOffMaxStack               int // vm.FuncProto.MaxStack offset
	funcProtoOffNumParams              int // vm.FuncProto.NumParams offset
	funcProtoOffIsVarArg               int // vm.FuncProto.IsVarArg offset
	funcProtoOffGlobalValCachePtr      int // vm.FuncProto.GlobalValCachePtr offset
	funcProtoOffTier2GlobalCachePtr    int // vm.FuncProto.Tier2GlobalCachePtr offset
	funcProtoOffTier2GlobalCacheGenPtr int // vm.FuncProto.Tier2GlobalCacheGenPtr offset
	funcProtoOffCallCount              int // vm.FuncProto.CallCount offset
)

func init() {
	var cl vm.Closure
	var proto vm.FuncProto

	vmClosureOffProto = int(unsafe.Offsetof(cl.Proto))
	vmClosureOffUpvalues = int(unsafe.Offsetof(cl.Upvalues))

	funcProtoOffCompiledCodePtr = int(unsafe.Offsetof(proto.CompiledCodePtr))
	funcProtoOffDirectEntryPtr = int(unsafe.Offsetof(proto.DirectEntryPtr))
	funcProtoOffTier2DirectEntryPtr = int(unsafe.Offsetof(proto.Tier2DirectEntryPtr))
	funcProtoOffTier2NumericEntryPtr = int(unsafe.Offsetof(proto.Tier2NumericEntryPtr))
	funcProtoOffConstants = int(unsafe.Offsetof(proto.Constants))
	funcProtoOffMaxStack = int(unsafe.Offsetof(proto.MaxStack))
	funcProtoOffNumParams = int(unsafe.Offsetof(proto.NumParams))
	funcProtoOffIsVarArg = int(unsafe.Offsetof(proto.IsVarArg))
	funcProtoOffGlobalValCachePtr = int(unsafe.Offsetof(proto.GlobalValCachePtr))
	funcProtoOffTier2GlobalCachePtr = int(unsafe.Offsetof(proto.Tier2GlobalCachePtr))
	funcProtoOffTier2GlobalCacheGenPtr = int(unsafe.Offsetof(proto.Tier2GlobalCacheGenPtr))
	funcProtoOffCallCount = int(unsafe.Offsetof(proto.CallCount))
}

// NaN-boxing pointer sub-type constants for ARM64 type checks.
const (
	nbPtrSubShift     = 44
	nbPtrSubVMClosure = 8 // ptrSubVMClosure = 8 << 44
)

// mRegSelfClosure caches the NaN-boxed closure value of the current function
// in callee-saved register X21. At CALL sites, comparing R(A) directly with
// X21 detects self-calls in 2 instructions instead of ~14.
const mRegSelfClosure = jit.X21

// nbClosureTagBits is the NaN-boxing tag for a VMClosure pointer:
// 0xFFFF800000000000 = NB_TagPtr | (ptrSubVMClosure << nbPtrSubShift).
const nbClosureTagBits = ^int64(1<<47 - 1)

// emitBaselineNativeCall emits a native ARM64 call sequence for OP_CALL.
// For compiled vm.Closure targets, this uses BLR instead of exit-resume.
// For all other cases, falls through to the slow path (exit-resume).
//
// Parameters:
//   - asm: the assembler
//   - inst: the OP_CALL instruction
//   - pc: the bytecode PC of this instruction
//   - callerProto: the caller's FuncProto (for MaxStack)
func emitBaselineNativeCall(asm *jit.Assembler, inst uint32, pc int, callerProto *vm.FuncProto) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	// B=0 (variable args) requires reading Top at runtime.
	// Only use native BLR for B=0 if TopPtr is available.
	// Falls to slow path if the BLR checks fail.
	nArgs := b - 1 // B>0: fixed arg count
	nRets := c - 1 // C>0: fixed return count; C=0: variable returns
	varArgs := b == 0
	varRets := c == 0

	slowLabel := nextLabel("call_slow")
	doneLabel := nextLabel("call_done")
	exitHandleLabel := nextLabel("call_callee_exited")

	// Precompute callee base offset (bytes) from caller's register base.
	maxStack := callerProto.MaxStack
	calleeBaseOff := maxStack * 8

	// 0. Check NativeCallDepth limit (prevent native stack overflow)
	const maxNativeCallDepth = 48
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X3, maxNativeCallDepth)
	asm.BCond(jit.CondGE, slowLabel) // too deep → exit-resume

	// 1. Load function value from regs[A]
	loadSlot(asm, jit.X0, a)

	// Fast self-call check: compare NaN-boxed R(A) with cached self-closure value.
	// If they match, skip the entire type check + pointer extraction + proto comparison
	// sequence (~10-14 instructions saved per self-call).
	selfCallFastLabel := nextLabel("self_call_fast")
	asm.CMPreg(jit.X0, mRegSelfClosure)
	asm.BCond(jit.CondEQ, selfCallFastLabel)

	useCallIC := !isBaselineStaticSelfCall(callerProto, pc, a)
	callICHitLabel := ""
	callICDoneLabel := ""
	callICOff := pc * 32 // 4 uint64 entries per bytecode PC
	if useCallIC {
		// Monomorphic CALL IC for stable non-self closures. This keeps mutual
		// and cross-recursive calls on the direct-entry path without repeating
		// the closure tag/proto/direct-entry lookup sequence at every call site.
		callICHitLabel = nextLabel("call_ic_hit")
		callICDoneLabel = nextLabel("call_ic_done")
		asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineCallCache)
		asm.LDR(jit.X4, jit.X3, callICOff) // cached boxed closure
		asm.CMPreg(jit.X0, jit.X4)
		asm.BCond(jit.CondEQ, callICHitLabel)
	}

	// 2. Type-check: must be ptr (0xFFFF) with sub-type = 8 (VMClosure)
	asm.LSRimm(jit.X1, jit.X0, 48)
	asm.MOVimm16(jit.X2, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)

	// Check sub-type == 8
	asm.LSRimm(jit.X1, jit.X0, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X2, 0xF)
	asm.ANDreg(jit.X1, jit.X1, jit.X2)
	asm.CMPimm(jit.X1, nbPtrSubVMClosure)
	asm.BCond(jit.CondNE, slowLabel)

	// 3. Extract raw pointer -> X0 = *vm.Closure
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)

	// Load Proto
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)

	// Self-call detection: compare callee proto with callerProto.
	// If equal → self-call path (BL self_call_entry, lightweight save).
	// If not equal → normal path (BLR X2, full save).
	//
	// NOTE: X20 is intentionally NOT used as a flag across BLR/BL. direct_entry
	// and self_call_entry only save FP+LR, so the callee freely overwrites X20
	// for its own call sites. Using X20 to select the restore path after BLR
	// would therefore read the callee's last X20 value, not the caller's — causing
	// wrong-frame-size restores and goroutine stack corruption. The fix is to give
	// each path (normal and self-call) its own complete save/call/restore sequence
	// with no shared flag register needed.
	selfCallExecLabel := nextLabel("self_call_exec")
	asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(callerProto))))
	asm.CMPreg(jit.X1, jit.X3)
	asm.BCond(jit.CondEQ, selfCallExecLabel)

	// -----------------------------------------------------------------------
	// Normal path: callee is a different function.
	// X0 = *vm.Closure, X1 = *FuncProto, X2 = DirectEntryPtr (loaded below)
	// -----------------------------------------------------------------------
	asm.LDR(jit.X2, jit.X1, funcProtoOffDirectEntryPtr)
	asm.CBZ(jit.X2, slowLabel) // not compiled -> slow
	if useCallIC {
		asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineCallCache)
		loadSlot(asm, jit.X4, a)
		asm.STR(jit.X4, jit.X3, callICOff)    // boxed closure
		asm.STR(jit.X2, jit.X3, callICOff+8)  // direct entry
		asm.STR(jit.X0, jit.X3, callICOff+16) // *vm.Closure
		asm.STR(jit.X1, jit.X3, callICOff+24) // *vm.FuncProto
		asm.B(callICDoneLabel)

		asm.Label(callICHitLabel)
		asm.LDR(jit.X1, jit.X3, callICOff+24) // cached *FuncProto
		asm.LDR(jit.X2, jit.X3, callICOff+8)  // cached DirectEntryPtr

		asm.Label(callICDoneLabel)
		asm.LDR(jit.X0, jit.X3, callICOff+16) // cached *vm.Closure
	}

	// Bounds check: verify callee's register window fits in the register file.
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

	// Increment callee's CallCount so the TieringManager can promote it to Tier 2.
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel) // exactly at threshold → trigger Tier 2 via slow path

	// 4-N. Normal save (96 bytes, 16-byte aligned)
	asm.SUBimm(jit.SP, jit.SP, 96)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.STR(jit.X3, jit.SP, 32)
	// Save caller's ClosurePtr, GlobalCache, and GlobalCachedGen
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X3, jit.SP, 40)
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.STR(jit.X3, jit.SP, 48)
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCachedGen)
	asm.STR(jit.X3, jit.SP, 56)
	// Save caller's NaN-boxed self-closure cache (X21)
	asm.STR(mRegSelfClosure, jit.SP, 64)
	// Save caller's pinned R(0) (X22)
	asm.STR(mRegR0, jit.SP, 72)

	// 5-N. Copy args to callee register window (normal path)
	if varArgs {
		asm.LDR(jit.X3, mRegCtx, execCtxOffTopPtr)
		asm.LDR(jit.X3, jit.X3, 0)
		asm.LSLimm(jit.X3, jit.X3, 3)
		asm.LDR(jit.X4, mRegCtx, execCtxOffRegsBase)
		asm.ADDreg(jit.X3, jit.X3, jit.X4)
		argStartOff := slotOff(a + 1)
		if argStartOff <= 4095 {
			asm.ADDimm(jit.X5, mRegRegs, uint16(argStartOff))
		} else {
			asm.LoadImm64(jit.X5, int64(argStartOff))
			asm.ADDreg(jit.X5, mRegRegs, jit.X5)
		}
		copyLabel := nextLabel("call_vararg_copy")
		copyDoneLabel := nextLabel("call_vararg_done")
		if calleeBaseOff <= 4095 {
			asm.ADDimm(jit.X6, mRegRegs, uint16(calleeBaseOff))
		} else {
			asm.LoadImm64(jit.X6, int64(calleeBaseOff))
			asm.ADDreg(jit.X6, mRegRegs, jit.X6)
		}
		asm.Label(copyLabel)
		asm.CMPreg(jit.X5, jit.X3)
		asm.BCond(jit.CondHS, copyDoneLabel)
		asm.LDR(jit.X4, jit.X5, 0)
		asm.STR(jit.X4, jit.X6, 0)
		asm.ADDimm(jit.X5, jit.X5, 8)
		asm.ADDimm(jit.X6, jit.X6, 8)
		asm.B(copyLabel)
		asm.Label(copyDoneLabel)
	} else {
		for i := 0; i < nArgs; i++ {
			srcOff := slotOff(a + 1 + i)
			dstOff := calleeBaseOff + i*8
			asm.LDR(jit.X3, mRegRegs, srcOff)
			asm.STR(jit.X3, mRegRegs, dstOff)
		}
	}

	// 6-N. Normal setup: advance mRegRegs, reload Constants, set ClosurePtr/GlobalCache
	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X3, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X3)
	}
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.LDR(mRegConsts, jit.X1, funcProtoOffConstants)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.MOVimm16(jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.MOVimm16(jit.X3, 0)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCachedGen)

	// 7-N. Increment NativeCallDepth, BLR X2, decrement
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.MOVreg(jit.X0, mRegCtx)
	asm.BLR(jit.X2)
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	// 8-N. Normal restore (96-byte frame)
	restoreDoneLabel := nextLabel("restore_done")
	asm.LDP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, jit.SP, 32)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.LDR(jit.X3, jit.SP, 40)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LDR(jit.X3, jit.SP, 48)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.LDR(jit.X3, jit.SP, 56)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCachedGen)
	asm.LDR(mRegSelfClosure, jit.SP, 64)
	asm.LDR(mRegR0, jit.SP, 72)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 96)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants) // sync X27 back to ctx
	asm.B(restoreDoneLabel)

	// -----------------------------------------------------------------------
	// Self-call path: callee proto == callerProto (or fast-path X0 == X21).
	// Uses BL self_call_entry (PC-relative) instead of BLR X2.
	// selfCallFastLabel: X0 == mRegSelfClosure; X1 not yet loaded → load now.
	// selfCallExecLabel: X1 = callerProto (either loaded or from proto compare).
	// -----------------------------------------------------------------------
	asm.Label(selfCallFastLabel)
	asm.LoadImm64(jit.X1, int64(uintptr(unsafe.Pointer(callerProto))))
	// fall through to selfCallExecLabel

	asm.Label(selfCallExecLabel)

	// Check DirectEntryPtr: if handleNativeCallExit cleared it (set to 0 because
	// the callee had op-exits), fall to the slow exit-resume path. Without this
	// check, self-calls bypass the DirectEntryPtr guard, causing deeply-nested
	// handleNativeCallExit → executeInner chains that overflow the goroutine stack.
	// X1 = callerProto (set by selfCallFastLabel or by the proto comparison above).
	asm.LDR(jit.X3, jit.X1, funcProtoOffDirectEntryPtr)
	asm.CBZ(jit.X3, slowLabel) // DirectEntryPtr=0 → slow path

	// Bounds check (self-call: compile-time constant totalNeeded)
	selfCallTotalNeeded := int64(calleeBaseOff + maxStack*8)
	asm.LoadImm64(jit.X3, selfCallTotalNeeded)
	asm.ADDreg(jit.X3, jit.X3, mRegRegs)
	asm.LDR(jit.X4, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondHI, slowLabel)

	// Increment CallCount so Tier 2 promotion can still happen.
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel)

	// 4-S. Self-call save (48 bytes, 16-byte aligned)
	asm.SUBimm(jit.SP, jit.SP, 48)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STR(mRegRegs, jit.SP, 16)
	asm.LDR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.STR(jit.X3, jit.SP, 24)
	asm.STR(mRegR0, jit.SP, 32)

	// 5-S. Copy args to callee register window (self-call path)
	if varArgs {
		asm.LDR(jit.X3, mRegCtx, execCtxOffTopPtr)
		asm.LDR(jit.X3, jit.X3, 0)
		asm.LSLimm(jit.X3, jit.X3, 3)
		asm.LDR(jit.X4, mRegCtx, execCtxOffRegsBase)
		asm.ADDreg(jit.X3, jit.X3, jit.X4)
		argStartOff := slotOff(a + 1)
		if argStartOff <= 4095 {
			asm.ADDimm(jit.X5, mRegRegs, uint16(argStartOff))
		} else {
			asm.LoadImm64(jit.X5, int64(argStartOff))
			asm.ADDreg(jit.X5, mRegRegs, jit.X5)
		}
		scCopyLabel := nextLabel("sc_vararg_copy")
		scCopyDoneLabel := nextLabel("sc_vararg_done")
		if calleeBaseOff <= 4095 {
			asm.ADDimm(jit.X6, mRegRegs, uint16(calleeBaseOff))
		} else {
			asm.LoadImm64(jit.X6, int64(calleeBaseOff))
			asm.ADDreg(jit.X6, mRegRegs, jit.X6)
		}
		asm.Label(scCopyLabel)
		asm.CMPreg(jit.X5, jit.X3)
		asm.BCond(jit.CondHS, scCopyDoneLabel)
		asm.LDR(jit.X4, jit.X5, 0)
		asm.STR(jit.X4, jit.X6, 0)
		asm.ADDimm(jit.X5, jit.X5, 8)
		asm.ADDimm(jit.X6, jit.X6, 8)
		asm.B(scCopyLabel)
		asm.Label(scCopyDoneLabel)
	} else {
		for i := 0; i < nArgs; i++ {
			srcOff := slotOff(a + 1 + i)
			dstOff := calleeBaseOff + i*8
			asm.LDR(jit.X3, mRegRegs, srcOff)
			asm.STR(jit.X3, mRegRegs, dstOff)
		}
	}

	// 6-S. Self-call setup: only advance mRegRegs and set CallMode.
	// No ctx.Regs flush here — lazily flushed at op-exit (emitBaselineOpExitCommon).
	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X3, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X3)
	}
	asm.MOVimm16(jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)

	// 7-S. Increment NativeCallDepth, BL self_call_entry, decrement
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.BL("self_call_entry")
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	// 8-S. Self-call restore (48-byte frame)
	asm.LDR(mRegRegs, jit.SP, 16)
	asm.LDR(jit.X3, jit.SP, 24)
	asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.LDR(mRegR0, jit.SP, 32)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 48)
	// fall through to restoreDoneLabel

	asm.Label(restoreDoneLabel)
	// Restore context pointers
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)

	// 9. Check callee exit code
	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X3, exitHandleLabel)

	// 10. Normal return: result -> regs[A]
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	storeSlot(asm, a, jit.X0)
	if varRets {
		// C=0: update *TopPtr = absSlot + 1
		// absSlot = (mRegRegs - RegsBase) / 8 + a
		asm.LDR(jit.X1, mRegCtx, execCtxOffRegsBase)
		asm.SUBreg(jit.X1, mRegRegs, jit.X1) // X1 = mRegRegs - RegsBase (bytes)
		asm.LSRimm(jit.X1, jit.X1, 3)        // X1 = base (slots)
		asm.ADDimm(jit.X1, jit.X1, uint16(a+1))
		asm.LDR(jit.X2, mRegCtx, execCtxOffTopPtr)
		asm.STR(jit.X1, jit.X2, 0) // *TopPtr = base + a + 1
	} else if nRets > 1 {
		asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
		for i := 1; i < nRets; i++ {
			asm.STR(jit.X1, mRegRegs, slotOff(a+i))
		}
	}
	asm.B(doneLabel)

	// Callee exited mid-execution (op-exit). Fall back to Go handler.
	// No flush needed for pinned R(0) — storeSlot always keeps memory in sync.
	asm.Label(exitHandleLabel)
	asm.LoadImm64(jit.X0, ExitNativeCallExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.LoadImm64(jit.X0, int64(a))
	asm.STR(jit.X0, mRegCtx, execCtxOffNativeCallA)
	asm.LoadImm64(jit.X0, int64(b))
	asm.STR(jit.X0, mRegCtx, execCtxOffNativeCallB)
	asm.LoadImm64(jit.X0, int64(c))
	asm.STR(jit.X0, mRegCtx, execCtxOffNativeCallC)
	asm.LoadImm64(jit.X0, int64(calleeBaseOff))
	asm.STR(jit.X0, mRegCtx, execCtxOffNativeCalleeBaseOff)
	asm.LoadImm64(jit.X0, int64(pc+1))
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselinePC)
	asm.LDR(jit.X0, mRegCtx, execCtxOffCallMode)
	asm.CBNZ(jit.X0, "direct_exit")
	asm.B("baseline_exit")

	// Slow path: fall back to exit-resume
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_CALL, pc, a, b, c)

	asm.Label(doneLabel)
}

// emitDirectEntryPrologue emits the lightweight direct entry point for native BLR
// calls. This is placed after the normal prologue and before the first bytecode.
// It only saves FP+LR (16 bytes) and reloads pinned registers from ctx.
func emitDirectEntryPrologue(asm *jit.Assembler) {
	asm.Label("direct_entry")
	// Save FP+LR with pre-index (SP -= 16)
	asm.STPpre(jit.X29, jit.X30, jit.SP, -16)
	asm.ADDimm(jit.X29, jit.SP, 0) // FP = SP

	// Set up pinned registers from ctx (X0 = ctx, set by caller)
	asm.MOVreg(mRegCtx, jit.X0)                       // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)        // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
	// X24 (tagInt) and X25 (tagBool) are callee-saved, preserved from caller.

	// Cache NaN-boxed self-closure for fast self-call detection.
	asm.LDR(mRegSelfClosure, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LoadImm64(jit.X3, nbClosureTagBits)
	asm.ORRreg(mRegSelfClosure, mRegSelfClosure, jit.X3)

	// Pin R(0): load from callee's register window.
	asm.LDR(mRegR0, mRegRegs, 0)

	// Jump to first bytecode.
	asm.B("pc_0")
}

func isBaselineStaticSelfCall(proto *vm.FuncProto, callPC, callA int) bool {
	if proto == nil || callPC <= 0 || callPC >= len(proto.Code) {
		return false
	}
	for pc := callPC - 1; pc >= 0; pc-- {
		inst := proto.Code[pc]
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		if op == vm.OP_GETGLOBAL && a == callA {
			bx := vm.DecodeBx(inst)
			return bx >= 0 && bx < len(proto.Constants) && proto.Constants[bx].IsString() && proto.Constants[bx].Str() == proto.Name
		}
		if baselineOpWritesSlot(op) && a == callA {
			return false
		}
	}
	return false
}

func baselineOpWritesSlot(op vm.Opcode) bool {
	switch op {
	case vm.OP_JMP, vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_SETGLOBAL,
		vm.OP_SETUPVAL, vm.OP_CLOSE, vm.OP_RETURN, vm.OP_TFORLOOP,
		vm.OP_GO, vm.OP_SEND:
		return false
	default:
		return true
	}
}

// emitSelfCallEntryPrologue emits a lightweight entry point used only by
// self-call BL instructions. For self-calls, the caller and callee are the
// same function, so:
//   - X19 (mRegCtx) is already set (same context)
//   - X26 (mRegRegs) was already updated by the caller's step 6
//   - X27 (mRegConsts) is preserved (same proto → same constants)
//
// This avoids the MOVreg X19,X0 and the two LDR for Regs/Constants that
// the normal direct_entry prologue performs.
func emitSelfCallEntryPrologue(asm *jit.Assembler) {
	asm.Label("self_call_entry")
	// Save FP+LR with pre-index (SP -= 16)
	asm.STPpre(jit.X29, jit.X30, jit.SP, -16)
	asm.ADDimm(jit.X29, jit.SP, 0) // FP = SP
	// Skip: MOVreg X19, X0 — X19 already holds ctx for self-call
	// Skip: LDR X26 from ctx.Regs — already set by caller's step 6
	// Skip: LDR X27 from ctx.Constants — same function, preserved

	// Pin R(0): load from callee's register window.
	// For fixed-arg self-calls, X22 was already set by the caller's arg copy,
	// but we reload for safety (covers vararg self-calls).
	asm.LDR(mRegR0, mRegRegs, 0)

	asm.B("pc_0")
}

// emitDirectExitEpilogue emits the direct exit path for functions entered via
// native BLR. RETURN jumps here when CallMode == 1.
func emitDirectExitEpilogue(asm *jit.Assembler) {
	asm.Label("direct_epilogue")
	asm.MOVimm16(jit.X0, 0) // ExitNormal
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	asm.Label("direct_exit")
	// Restore FP+LR with post-index (SP += 16)
	asm.LDPpost(jit.X29, jit.X30, jit.SP, 16)
	asm.RET()
}
