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
	"sync"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

// Struct layout constants for vm.Closure and vm.FuncProto.
// Verified at init time via unsafe.Offsetof.
var (
	vmClosureOffProto          int // vm.Closure.Proto offset (should be 0)
	vmClosureOffUpvalues       int // vm.Closure.Upvalues offset (should be 8)
	vmClosureOffInlineUpvalue0 int // vm.Closure.inlineUpvalue[0] offset

	funcProtoOffCompiledCodePtr        int // vm.FuncProto.CompiledCodePtr offset
	funcProtoOffDirectEntryPtr         int // vm.FuncProto.DirectEntryPtr offset
	funcProtoOffTier2DirectEntryPtr    int // vm.FuncProto.Tier2DirectEntryPtr offset
	funcProtoOffTier2LeafEntryPtr      int // vm.FuncProto.Tier2LeafEntryPtr offset
	funcProtoOffDirectEntryVersion     int // vm.FuncProto.DirectEntryVersion offset
	funcProtoOffTier2NumericEntryPtr   int // vm.FuncProto.Tier2NumericEntryPtr offset
	funcProtoOffTier2TypedEntryPtr     int // vm.FuncProto.Tier2TypedEntryPtr offset
	funcProtoOffTier2TypedEntryABI     int // vm.FuncProto.Tier2TypedEntryABI offset
	funcProtoOffConstants              int // vm.FuncProto.Constants offset (slice header)
	funcProtoOffFieldCache             int // vm.FuncProto.FieldCache offset (slice header)
	funcProtoOffFieldPolyCache         int // vm.FuncProto.FieldPolyCache offset (slice header)
	funcProtoOffTableStringKeyCache    int // vm.FuncProto.TableStringKeyCache offset (slice header)
	funcProtoOffMaxStack               int // vm.FuncProto.MaxStack offset
	funcProtoOffNumParams              int // vm.FuncProto.NumParams offset
	funcProtoOffIsVarArg               int // vm.FuncProto.IsVarArg offset
	funcProtoOffGlobalValCachePtr      int // vm.FuncProto.GlobalValCachePtr offset
	funcProtoOffTier2GlobalCachePtr    int // vm.FuncProto.Tier2GlobalCachePtr offset
	funcProtoOffTier2GlobalCacheGenPtr int // vm.FuncProto.Tier2GlobalCacheGenPtr offset
	funcProtoOffTier2GlobalIndexPtr    int // vm.FuncProto.Tier2GlobalIndexPtr offset
	funcProtoOffCallCount              int // vm.FuncProto.CallCount offset
	funcProtoOffTier2Promoted          int // vm.FuncProto.Tier2Promoted offset
	funcProtoOffLeafNoCall             int // vm.FuncProto.LeafNoCall offset
	funcProtoOffTier2LeafNoCall        int // vm.FuncProto.Tier2LeafNoCall offset
	funcProtoOffNoGlobalOps            int // vm.FuncProto.NoGlobalOps offset
)

func init() {
	var cl vm.Closure
	var proto vm.FuncProto

	vmClosureOffProto = int(unsafe.Offsetof(cl.Proto))
	vmClosureOffUpvalues = int(unsafe.Offsetof(cl.Upvalues))
	vmClosureOffInlineUpvalue0 = vm.ClosureInlineUpvalue0Offset()

	funcProtoOffCompiledCodePtr = int(unsafe.Offsetof(proto.CompiledCodePtr))
	funcProtoOffDirectEntryPtr = int(unsafe.Offsetof(proto.DirectEntryPtr))
	funcProtoOffTier2DirectEntryPtr = int(unsafe.Offsetof(proto.Tier2DirectEntryPtr))
	funcProtoOffTier2LeafEntryPtr = int(unsafe.Offsetof(proto.Tier2LeafEntryPtr))
	funcProtoOffDirectEntryVersion = int(unsafe.Offsetof(proto.DirectEntryVersion))
	funcProtoOffTier2NumericEntryPtr = int(unsafe.Offsetof(proto.Tier2NumericEntryPtr))
	funcProtoOffTier2TypedEntryPtr = int(unsafe.Offsetof(proto.Tier2TypedEntryPtr))
	funcProtoOffTier2TypedEntryABI = int(unsafe.Offsetof(proto.Tier2TypedEntryABI))
	funcProtoOffConstants = int(unsafe.Offsetof(proto.Constants))
	funcProtoOffFieldCache = int(unsafe.Offsetof(proto.FieldCache))
	funcProtoOffFieldPolyCache = int(unsafe.Offsetof(proto.FieldPolyCache))
	funcProtoOffTableStringKeyCache = int(unsafe.Offsetof(proto.TableStringKeyCache))
	funcProtoOffMaxStack = int(unsafe.Offsetof(proto.MaxStack))
	funcProtoOffNumParams = int(unsafe.Offsetof(proto.NumParams))
	funcProtoOffIsVarArg = int(unsafe.Offsetof(proto.IsVarArg))
	funcProtoOffGlobalValCachePtr = int(unsafe.Offsetof(proto.GlobalValCachePtr))
	funcProtoOffTier2GlobalCachePtr = int(unsafe.Offsetof(proto.Tier2GlobalCachePtr))
	funcProtoOffTier2GlobalCacheGenPtr = int(unsafe.Offsetof(proto.Tier2GlobalCacheGenPtr))
	funcProtoOffTier2GlobalIndexPtr = int(unsafe.Offsetof(proto.Tier2GlobalIndexPtr))
	funcProtoOffCallCount = int(unsafe.Offsetof(proto.CallCount))
	funcProtoOffTier2Promoted = int(unsafe.Offsetof(proto.Tier2Promoted))
	funcProtoOffLeafNoCall = int(unsafe.Offsetof(proto.LeafNoCall))
	funcProtoOffTier2LeafNoCall = int(unsafe.Offsetof(proto.Tier2LeafNoCall))
	funcProtoOffNoGlobalOps = int(unsafe.Offsetof(proto.NoGlobalOps))
}

// NaN-boxing pointer sub-type constants for ARM64 type checks.
const (
	nbPtrSubShift     = 44
	nbPtrSubVMClosure = 8 // ptrSubVMClosure = 8 << 44
)

const (
	baselineCallCacheStride      = 4
	baselineCallCacheBoxedOff    = 0
	baselineCallCacheEntryOff    = 8
	baselineCallCacheProtoOff    = 16
	baselineCallCacheVersionOff  = 24
	baselineCallCacheStrideBytes = baselineCallCacheStride * 8
)

// mRegSelfClosure caches the NaN-boxed closure value of the current function
// in callee-saved register X21. At CALL sites, comparing R(A) directly with
// X21 detects self-calls in 2 instructions instead of ~14.
const mRegSelfClosure = jit.X21

// nbClosureTagBits is the NaN-boxing tag for a VMClosure pointer:
// 0xFFFF800000000000 = NB_TagPtr | (ptrSubVMClosure << nbPtrSubShift).
const nbClosureTagBits = ^int64(1<<47 - 1)

type accumulatorClosureFastPath struct {
	proto      *vm.FuncProto
	valueUpval int
	deltaKind  accumulatorDeltaKind
	delta      int64
	deltaUpval int
}

type simpleClosureExprFastPath struct {
	proto *vm.FuncProto
	expr  simpleClosureExpr
}

type immediateClosureFactoryFastPath struct {
	proto      *vm.FuncProto
	expr       simpleClosureExpr
	upvalSlots []int
}

type simpleClosureExprKind uint8

const (
	simpleClosureExprParam simpleClosureExprKind = iota
	simpleClosureExprIntConst
	simpleClosureExprUpval
	simpleClosureExprAdd
	simpleClosureExprMul
)

type simpleClosureExpr struct {
	kind  simpleClosureExprKind
	value int64
	upval int
	left  *simpleClosureExpr
	right *simpleClosureExpr
}

type accumulatorDeltaKind uint8

const (
	accumulatorDeltaConst accumulatorDeltaKind = iota
	accumulatorDeltaUpval
)

var (
	accumulatorClosureProgramFastPathsMu sync.RWMutex
	accumulatorClosureProgramFastPaths   = make(map[*vm.FuncProto][]accumulatorClosureFastPath)

	simpleClosureExprProgramFastPathsMu sync.RWMutex
	simpleClosureExprProgramFastPaths   = make(map[*vm.FuncProto][]simpleClosureExprFastPath)

	immediateClosureFactoryProgramFastPathsMu sync.RWMutex
	immediateClosureFactoryProgramFastPaths   = make(map[*vm.FuncProto][]immediateClosureFactoryFastPath)
)

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

	emitBaselineSelfTailNoReturnFastPath(asm, inst, pc, callerProto, slowLabel)

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
	callICOff := pc * baselineCallCacheStrideBytes
	if useCallIC {
		// Monomorphic CALL IC for stable non-self closures. This keeps mutual
		// and cross-recursive calls on the direct-entry path. Hits still validate
		// FuncProto.DirectEntryVersion, and promoted Tier 2 callees fall back
		// through VM dispatch because baseline native-call exits use the Tier 1
		// exit protocol. If a site allocates fresh closures of the same proto,
		// the boxed value misses but a secondary proto/version hit below can
		// still reuse the cached direct entry.
		callICHitLabel = nextLabel("call_ic_hit")
		callICDoneLabel = nextLabel("call_ic_done")
		asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineCallCache)
		asm.LDR(jit.X4, jit.X3, callICOff+baselineCallCacheBoxedOff) // cached boxed closure
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
	if useCallIC {
		asm.MOVreg(jit.X4, jit.X0) // keep boxed closure for the call IC fill path
	}
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)

	// Load Proto
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)

	if b == 1 && c == 2 {
		emitBaselineAccumulatorClosureFastPath(asm, callerProto, slowLabel, doneLabel, a)
	}
	if b == 2 && c == 2 {
		emitBaselineImmediateClosureFactoryFastPath(asm, callerProto, pc, a)
	}
	if b == 2 && c == 2 {
		emitBaselineSimpleClosureExprFastPath(asm, callerProto, doneLabel, a)
	}

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
	asm.LDRB(jit.X2, jit.X1, funcProtoOffTier2Promoted)
	asm.CBNZ(jit.X2, slowLabel) // Tier 2 direct entries use a different exit protocol.
	asm.LDR(jit.X2, jit.X1, funcProtoOffDirectEntryPtr)
	asm.CBZ(jit.X2, slowLabel) // not compiled -> slow
	if useCallIC {
		callICFillLabel := nextLabel("call_ic_fill")
		callICProtoVersionOKLabel := nextLabel("call_ic_proto_version_ok")

		asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineCallCache)
		asm.LDR(jit.X5, jit.X3, callICOff+baselineCallCacheProtoOff)
		asm.CMPreg(jit.X5, jit.X1)
		asm.BCond(jit.CondNE, callICFillLabel)
		asm.LDR(jit.X6, jit.X3, callICOff+baselineCallCacheVersionOff)
		asm.LDR(jit.X5, jit.X1, funcProtoOffDirectEntryVersion)
		asm.CMPreg(jit.X6, jit.X5)
		asm.BCond(jit.CondEQ, callICProtoVersionOKLabel)

		asm.Label(callICFillLabel)
		asm.LDR(jit.X5, jit.X1, funcProtoOffDirectEntryVersion)
		asm.STP(jit.X4, jit.X2, jit.X3, callICOff+baselineCallCacheBoxedOff) // boxed closure, direct entry
		asm.STP(jit.X1, jit.X5, jit.X3, callICOff+baselineCallCacheProtoOff) // *vm.FuncProto, entry version
		asm.B(callICDoneLabel)

		asm.Label(callICProtoVersionOKLabel)
		asm.LDR(jit.X2, jit.X3, callICOff+baselineCallCacheEntryOff)
		asm.B(callICDoneLabel)

		asm.Label(callICHitLabel)
		asm.LDR(jit.X1, jit.X3, callICOff+baselineCallCacheProtoOff)   // cached *FuncProto
		asm.LDR(jit.X2, jit.X3, callICOff+baselineCallCacheEntryOff)   // cached DirectEntryPtr
		asm.LDR(jit.X4, jit.X3, callICOff+baselineCallCacheVersionOff) // cached DirectEntryVersion
		asm.LDR(jit.X5, jit.X1, funcProtoOffDirectEntryVersion)
		callICVersionOKLabel := nextLabel("call_ic_version_ok")
		asm.CMPreg(jit.X4, jit.X5)
		asm.BCond(jit.CondEQ, callICVersionOKLabel)
		asm.LDRB(jit.X4, jit.X1, funcProtoOffTier2Promoted)
		asm.CBNZ(jit.X4, slowLabel)
		asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryPtr)
		asm.CBZ(jit.X4, slowLabel)
		asm.MOVreg(jit.X2, jit.X4)
		asm.STR(jit.X2, jit.X3, callICOff+baselineCallCacheEntryOff)
		asm.STR(jit.X5, jit.X3, callICOff+baselineCallCacheVersionOff)
		asm.Label(callICVersionOKLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0) // X0 = *vm.Closure

		asm.Label(callICDoneLabel)
		if b == 2 && c == 2 {
			emitBaselineSimpleClosureExprFastPath(asm, callerProto, doneLabel, a)
		}
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

func emitBaselineAccumulatorClosureFastPath(asm *jit.Assembler, callerProto *vm.FuncProto, slowLabel, doneLabel string, dstSlot int) {
	fastPaths := accumulatorClosureFastPathsForProto(callerProto)
	if len(fastPaths) == 0 {
		return
	}
	missLabel := nextLabel("accum_closure_miss")
	for _, fast := range fastPaths {
		nextFastLabel := nextLabel("accum_closure_next")
		asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(fast.proto))))
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondNE, nextFastLabel)

		emitLoadClosureUpvalueRef(asm, jit.X0, fast.valueUpval, len(fast.proto.Upvalues), jit.X6, jit.X2, jit.X3, missLabel)
		asm.LDR(jit.X4, jit.X6, 0) // boxed current value

		switch fast.deltaKind {
		case accumulatorDeltaConst:
			floatLabel := nextLabel("accum_closure_float")
			emitCheckIsInt(asm, jit.X4, jit.X5)
			asm.BCond(jit.CondNE, floatLabel)
			jit.EmitUnboxInt(asm, jit.X4, jit.X4)
			if fast.delta >= 0 && fast.delta <= 4095 {
				asm.ADDimm(jit.X4, jit.X4, uint16(fast.delta))
			} else if fast.delta < 0 && fast.delta >= -4095 {
				asm.SUBimm(jit.X4, jit.X4, uint16(-fast.delta))
			} else {
				asm.LoadImm64(jit.X5, fast.delta)
				asm.ADDreg(jit.X4, jit.X4, jit.X5)
			}
			emitStoreAccumulatorIntResult(asm, dstSlot, doneLabel, jit.X6)
			asm.Label(floatLabel)
			emitFloatValueOrMiss(asm, jit.D0, jit.X4, jit.X5, missLabel)
			asm.LoadImm64(jit.X5, fast.delta)
			asm.SCVTF(jit.D1, jit.X5)
			asm.FADDd(jit.D0, jit.D0, jit.D1)
			asm.FMOVtoGP(jit.X4, jit.D0)
			asm.STR(jit.X4, jit.X6, 0)
			storeSlot(asm, dstSlot, jit.X4)
			asm.B(doneLabel)
		case accumulatorDeltaUpval:
			emitLoadClosureUpvalueRef(asm, jit.X0, fast.deltaUpval, len(fast.proto.Upvalues), jit.X2, jit.X5, jit.X3, missLabel)
			asm.LDR(jit.X7, jit.X2, 0) // boxed delta value
			floatLabel := nextLabel("accum_closure_float")
			mixedFloatLabel := nextLabel("accum_closure_mixed_float")
			emitCheckIsInt(asm, jit.X4, jit.X5)
			asm.BCond(jit.CondNE, floatLabel)
			emitCheckIsInt(asm, jit.X7, jit.X5)
			asm.BCond(jit.CondNE, mixedFloatLabel)
			jit.EmitUnboxInt(asm, jit.X4, jit.X4)
			jit.EmitUnboxInt(asm, jit.X7, jit.X7)
			asm.ADDreg(jit.X4, jit.X4, jit.X7)
			emitStoreAccumulatorIntResult(asm, dstSlot, doneLabel, jit.X6)
			asm.Label(mixedFloatLabel)
			asm.SBFX(jit.X5, jit.X4, 0, 48)
			asm.SCVTF(jit.D0, jit.X5)
			emitFloatValueOrMiss(asm, jit.D1, jit.X7, jit.X5, missLabel)
			asm.FADDd(jit.D0, jit.D0, jit.D1)
			asm.FMOVtoGP(jit.X4, jit.D0)
			asm.STR(jit.X4, jit.X6, 0)
			storeSlot(asm, dstSlot, jit.X4)
			asm.B(doneLabel)
			asm.Label(floatLabel)
			emitFloatValueOrMiss(asm, jit.D0, jit.X4, jit.X5, missLabel)
			emitToFloatNumberOrMiss(asm, jit.D1, jit.X7, jit.X5, missLabel)
			asm.FADDd(jit.D0, jit.D0, jit.D1)
			asm.FMOVtoGP(jit.X4, jit.D0)
			asm.STR(jit.X4, jit.X6, 0)
			storeSlot(asm, dstSlot, jit.X4)
			asm.B(doneLabel)
		default:
			asm.B(missLabel)
		}

		asm.Label(nextFastLabel)
	}
	asm.Label(missLabel)
}

func emitStoreAccumulatorIntResult(asm *jit.Assembler, dstSlot int, doneLabel string, valueRefReg jit.Reg) {
	overflowLabel := nextLabel("accum_closure_int_overflow")
	asm.SBFX(jit.X5, jit.X4, 0, 48)
	asm.CMPreg(jit.X5, jit.X4)
	asm.BCond(jit.CondNE, overflowLabel)
	jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
	asm.STR(jit.X4, valueRefReg, 0)
	storeSlot(asm, dstSlot, jit.X4)
	asm.B(doneLabel)

	asm.Label(overflowLabel)
	asm.SCVTF(jit.D0, jit.X4)
	asm.FMOVtoGP(jit.X4, jit.D0)
	asm.STR(jit.X4, valueRefReg, 0)
	storeSlot(asm, dstSlot, jit.X4)
	asm.B(doneLabel)
}

func emitFloatValueOrMiss(asm *jit.Assembler, fpReg jit.FReg, gpReg, scratch jit.Reg, missLabel string) {
	jit.EmitIsTagged(asm, gpReg, scratch)
	asm.BCond(jit.CondEQ, missLabel)
	asm.FMOVtoFP(fpReg, gpReg)
}

func emitToFloatNumberOrMiss(asm *jit.Assembler, fpReg jit.FReg, gpReg, scratch jit.Reg, missLabel string) {
	isIntLabel := nextLabel("number_to_float_int")
	doneLabel := nextLabel("number_to_float_done")

	emitCheckIsInt(asm, gpReg, scratch)
	asm.BCond(jit.CondEQ, isIntLabel)
	jit.EmitIsTagged(asm, gpReg, scratch)
	asm.BCond(jit.CondEQ, missLabel)
	asm.FMOVtoFP(fpReg, gpReg)
	asm.B(doneLabel)

	asm.Label(isIntLabel)
	asm.SBFX(scratch, gpReg, 0, 48)
	asm.SCVTF(fpReg, scratch)

	asm.Label(doneLabel)
}

func emitBaselineSimpleClosureExprFastPath(asm *jit.Assembler, callerProto *vm.FuncProto, doneLabel string, callSlot int) {
	fastPaths := simpleClosureExprFastPathsForProto(callerProto)
	if len(fastPaths) == 0 {
		return
	}
	missLabel := nextLabel("simple_closure_expr_miss")
	for _, fast := range fastPaths {
		nextFastLabel := nextLabel("simple_closure_expr_next")
		asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(fast.proto))))
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondNE, nextFastLabel)

		emitSimpleClosureExprValue(asm, fast.expr, callSlot+1, len(fast.proto.Upvalues), jit.X4, jit.X7, jit.X5, jit.X6, missLabel)
		jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
		storeSlot(asm, callSlot, jit.X4)
		asm.B(doneLabel)

		asm.Label(nextFastLabel)
	}
	asm.Label(missLabel)
}

func emitBaselineImmediateClosureFactoryFastPath(asm *jit.Assembler, callerProto *vm.FuncProto, pc, factoryCallSlot int) {
	fastPaths := immediateClosureFactoryFastPathsForProto(callerProto)
	if len(fastPaths) == 0 || pc+4 >= len(callerProto.Code) {
		return
	}
	moveCallee := callerProto.Code[pc+1]
	moveArg := callerProto.Code[pc+2]
	callClosure := callerProto.Code[pc+3]
	if vm.DecodeOp(moveCallee) != vm.OP_MOVE ||
		vm.DecodeB(moveCallee) != factoryCallSlot ||
		vm.DecodeOp(moveArg) != vm.OP_MOVE ||
		vm.DecodeOp(callClosure) != vm.OP_CALL ||
		vm.DecodeB(callClosure) != 2 ||
		vm.DecodeC(callClosure) != 2 ||
		vm.DecodeA(callClosure) != vm.DecodeA(moveCallee) ||
		vm.DecodeA(moveArg) != vm.DecodeA(callClosure)+1 {
		return
	}

	resultSlot := vm.DecodeA(callClosure)
	argSrcSlot := vm.DecodeB(moveArg)
	missLabel := nextLabel("immediate_closure_factory_miss")
	for _, fast := range fastPaths {
		nextFastLabel := nextLabel("immediate_closure_factory_next")
		asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(fast.proto))))
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondNE, nextFastLabel)

		emitImmediateClosureFactoryExprValue(asm, fast.expr, argSrcSlot, factoryCallSlot+1, fast.upvalSlots, jit.X4, jit.X7, jit.X5, missLabel)
		jit.EmitBoxIntFast(asm, jit.X4, jit.X4, mRegTagInt)
		storeSlot(asm, resultSlot, jit.X4)
		asm.B(pcLabel(pc + 4))

		asm.Label(nextFastLabel)
	}
	asm.Label(missLabel)
}

func emitSimpleClosureExprValue(asm *jit.Assembler, expr simpleClosureExpr, argSlot, upvalCount int, dst, rhs, tagScratch, refScratch jit.Reg, missLabel string) {
	switch expr.kind {
	case simpleClosureExprParam:
		loadSlot(asm, dst, argSlot)
		emitCheckIsInt(asm, dst, tagScratch)
		asm.BCond(jit.CondNE, missLabel)
		jit.EmitUnboxInt(asm, dst, dst)
	case simpleClosureExprIntConst:
		asm.LoadImm64(dst, expr.value)
	case simpleClosureExprUpval:
		emitLoadClosureUpvalueRef(asm, jit.X0, expr.upval, upvalCount, refScratch, rhs, tagScratch, missLabel)
		asm.LDR(dst, refScratch, 0)
		emitCheckIsInt(asm, dst, tagScratch)
		asm.BCond(jit.CondNE, missLabel)
		jit.EmitUnboxInt(asm, dst, dst)
	case simpleClosureExprAdd, simpleClosureExprMul:
		if expr.left == nil || expr.right == nil {
			asm.B(missLabel)
			return
		}
		emitSimpleClosureExprValue(asm, *expr.left, argSlot, upvalCount, dst, rhs, tagScratch, refScratch, missLabel)
		emitSimpleClosureExprValue(asm, *expr.right, argSlot, upvalCount, rhs, dst, tagScratch, refScratch, missLabel)
		switch expr.kind {
		case simpleClosureExprAdd:
			asm.ADDreg(dst, dst, rhs)
		case simpleClosureExprMul:
			asm.MUL(dst, dst, rhs)
		}
		asm.SBFX(tagScratch, dst, 0, 48)
		asm.CMPreg(tagScratch, dst)
		asm.BCond(jit.CondNE, missLabel)
	default:
		asm.B(missLabel)
	}
}

func emitImmediateClosureFactoryExprValue(asm *jit.Assembler, expr simpleClosureExpr, argSlot, factoryArgBase int, upvalSlots []int, dst, rhs, tagScratch jit.Reg, missLabel string) {
	switch expr.kind {
	case simpleClosureExprParam:
		loadSlot(asm, dst, argSlot)
		emitCheckIsInt(asm, dst, tagScratch)
		asm.BCond(jit.CondNE, missLabel)
		jit.EmitUnboxInt(asm, dst, dst)
	case simpleClosureExprIntConst:
		asm.LoadImm64(dst, expr.value)
	case simpleClosureExprUpval:
		if expr.upval < 0 || expr.upval >= len(upvalSlots) {
			asm.B(missLabel)
			return
		}
		loadSlot(asm, dst, factoryArgBase+upvalSlots[expr.upval])
		emitCheckIsInt(asm, dst, tagScratch)
		asm.BCond(jit.CondNE, missLabel)
		jit.EmitUnboxInt(asm, dst, dst)
	case simpleClosureExprAdd, simpleClosureExprMul:
		if expr.left == nil || expr.right == nil {
			asm.B(missLabel)
			return
		}
		emitImmediateClosureFactoryExprValue(asm, *expr.left, argSlot, factoryArgBase, upvalSlots, dst, rhs, tagScratch, missLabel)
		emitImmediateClosureFactoryExprValue(asm, *expr.right, argSlot, factoryArgBase, upvalSlots, rhs, dst, tagScratch, missLabel)
		switch expr.kind {
		case simpleClosureExprAdd:
			asm.ADDreg(dst, dst, rhs)
		case simpleClosureExprMul:
			asm.MUL(dst, dst, rhs)
		}
		asm.SBFX(tagScratch, dst, 0, 48)
		asm.CMPreg(tagScratch, dst)
		asm.BCond(jit.CondNE, missLabel)
	default:
		asm.B(missLabel)
	}
}

func emitLoadClosureUpvalueRef(asm *jit.Assembler, closureReg jit.Reg, upval, upvalCount int, dstRefReg, upvalReg, dataReg jit.Reg, slowLabel string) {
	if upvalCount == 1 && upval == 0 {
		asm.LDR(upvalReg, closureReg, vmClosureOffInlineUpvalue0)
	} else {
		asm.LDR(dataReg, closureReg, vmClosureOffUpvalues)
		asm.CBZ(dataReg, slowLabel)
		asm.LDR(upvalReg, dataReg, upval*8)
	}
	asm.CBZ(upvalReg, slowLabel)
	asm.LDR(dstRefReg, upvalReg, 0)
	asm.CBZ(dstRefReg, slowLabel)
}

func registerAccumulatorClosureFastPaths(root *vm.FuncProto) {
	if root == nil {
		return
	}
	fastPaths := collectAccumulatorClosureFastPaths(root)
	exprFastPaths := collectSimpleClosureExprFastPaths(root)
	factoryFastPaths := collectImmediateClosureFactoryFastPaths(root)
	if len(fastPaths) == 0 && len(exprFastPaths) == 0 && len(factoryFastPaths) == 0 {
		return
	}
	protos := collectProtoTree(root)
	accumulatorClosureProgramFastPathsMu.Lock()
	for _, proto := range protos {
		if len(fastPaths) != 0 {
			accumulatorClosureProgramFastPaths[proto] = fastPaths
		}
	}
	accumulatorClosureProgramFastPathsMu.Unlock()
	simpleClosureExprProgramFastPathsMu.Lock()
	for _, proto := range protos {
		if len(exprFastPaths) != 0 {
			simpleClosureExprProgramFastPaths[proto] = exprFastPaths
		}
	}
	simpleClosureExprProgramFastPathsMu.Unlock()
	immediateClosureFactoryProgramFastPathsMu.Lock()
	for _, proto := range protos {
		if len(factoryFastPaths) != 0 {
			immediateClosureFactoryProgramFastPaths[proto] = factoryFastPaths
		}
	}
	immediateClosureFactoryProgramFastPathsMu.Unlock()
}

func accumulatorClosureFastPathsForProto(proto *vm.FuncProto) []accumulatorClosureFastPath {
	if proto == nil {
		return nil
	}
	accumulatorClosureProgramFastPathsMu.RLock()
	if fastPaths := accumulatorClosureProgramFastPaths[proto]; len(fastPaths) != 0 {
		accumulatorClosureProgramFastPathsMu.RUnlock()
		return fastPaths
	}
	accumulatorClosureProgramFastPathsMu.RUnlock()
	return collectAccumulatorClosureFastPaths(proto)
}

func simpleClosureExprFastPathsForProto(proto *vm.FuncProto) []simpleClosureExprFastPath {
	if proto == nil {
		return nil
	}
	simpleClosureExprProgramFastPathsMu.RLock()
	if fastPaths := simpleClosureExprProgramFastPaths[proto]; len(fastPaths) != 0 {
		simpleClosureExprProgramFastPathsMu.RUnlock()
		return fastPaths
	}
	simpleClosureExprProgramFastPathsMu.RUnlock()
	return collectSimpleClosureExprFastPaths(proto)
}

func immediateClosureFactoryFastPathsForProto(proto *vm.FuncProto) []immediateClosureFactoryFastPath {
	if proto == nil {
		return nil
	}
	immediateClosureFactoryProgramFastPathsMu.RLock()
	if fastPaths := immediateClosureFactoryProgramFastPaths[proto]; len(fastPaths) != 0 {
		immediateClosureFactoryProgramFastPathsMu.RUnlock()
		return fastPaths
	}
	immediateClosureFactoryProgramFastPathsMu.RUnlock()
	return collectImmediateClosureFactoryFastPaths(proto)
}

func collectProtoTree(root *vm.FuncProto) []*vm.FuncProto {
	seen := make(map[*vm.FuncProto]bool)
	var out []*vm.FuncProto
	var walk func(*vm.FuncProto)
	walk = func(proto *vm.FuncProto) {
		if proto == nil || seen[proto] {
			return
		}
		seen[proto] = true
		out = append(out, proto)
		for _, child := range proto.Protos {
			walk(child)
		}
	}
	walk(root)
	return out
}

func collectAccumulatorClosureFastPaths(root *vm.FuncProto) []accumulatorClosureFastPath {
	seen := make(map[*vm.FuncProto]bool)
	var out []accumulatorClosureFastPath
	var walk func(*vm.FuncProto)
	walk = func(proto *vm.FuncProto) {
		if proto == nil || seen[proto] {
			return
		}
		seen[proto] = true
		if fast, ok := accumulatorClosurePattern(proto); ok {
			out = append(out, fast)
		}
		for _, child := range proto.Protos {
			walk(child)
		}
	}
	walk(root)
	return out
}

func collectSimpleClosureExprFastPaths(root *vm.FuncProto) []simpleClosureExprFastPath {
	seen := make(map[*vm.FuncProto]bool)
	var out []simpleClosureExprFastPath
	var walk func(*vm.FuncProto)
	walk = func(proto *vm.FuncProto) {
		if proto == nil || seen[proto] {
			return
		}
		seen[proto] = true
		if expr, ok := simpleClosureExprPattern(proto); ok {
			out = append(out, simpleClosureExprFastPath{proto: proto, expr: expr})
		}
		for _, child := range proto.Protos {
			walk(child)
		}
	}
	walk(root)
	return out
}

func collectImmediateClosureFactoryFastPaths(root *vm.FuncProto) []immediateClosureFactoryFastPath {
	seen := make(map[*vm.FuncProto]bool)
	var out []immediateClosureFactoryFastPath
	var walk func(*vm.FuncProto)
	walk = func(proto *vm.FuncProto) {
		if proto == nil || seen[proto] {
			return
		}
		seen[proto] = true
		if fast, ok := immediateClosureFactoryPattern(proto); ok {
			out = append(out, fast)
		}
		for _, child := range proto.Protos {
			walk(child)
		}
	}
	walk(root)
	return out
}

func accumulatorClosurePattern(proto *vm.FuncProto) (accumulatorClosureFastPath, bool) {
	if proto == nil || proto.NumParams != 0 || proto.IsVarArg || len(proto.Code) != 6 {
		return accumulatorClosureFastPath{}, false
	}
	if vm.DecodeOp(proto.Code[0]) != vm.OP_GETUPVAL ||
		vm.DecodeOp(proto.Code[2]) != vm.OP_ADD ||
		vm.DecodeOp(proto.Code[3]) != vm.OP_SETUPVAL ||
		vm.DecodeOp(proto.Code[4]) != vm.OP_GETUPVAL ||
		vm.DecodeOp(proto.Code[5]) != vm.OP_RETURN {
		return accumulatorClosureFastPath{}, false
	}
	uv := vm.DecodeB(proto.Code[0])
	if uv < 0 || uv >= len(proto.Upvalues) {
		return accumulatorClosureFastPath{}, false
	}
	if vm.DecodeB(proto.Code[3]) != uv || vm.DecodeB(proto.Code[4]) != uv {
		return accumulatorClosureFastPath{}, false
	}
	loadReg := vm.DecodeA(proto.Code[0])
	addDst := vm.DecodeA(proto.Code[2])
	addB := vm.DecodeB(proto.Code[2])
	addC := vm.DecodeC(proto.Code[2])
	if addDst != vm.DecodeA(proto.Code[3]) {
		return accumulatorClosureFastPath{}, false
	}
	retReg := vm.DecodeA(proto.Code[4])
	if vm.DecodeA(proto.Code[5]) != retReg || vm.DecodeB(proto.Code[5]) != 2 {
		return accumulatorClosureFastPath{}, false
	}

	fast := accumulatorClosureFastPath{proto: proto, valueUpval: uv}
	switch vm.DecodeOp(proto.Code[1]) {
	case vm.OP_LOADINT:
		constReg := vm.DecodeA(proto.Code[1])
		if !((addB == loadReg && addC == constReg) || (addB == constReg && addC == loadReg)) {
			return accumulatorClosureFastPath{}, false
		}
		fast.deltaKind = accumulatorDeltaConst
		fast.delta = int64(vm.DecodesBx(proto.Code[1]))
		return fast, true
	case vm.OP_GETUPVAL:
		deltaReg := vm.DecodeA(proto.Code[1])
		deltaUpval := vm.DecodeB(proto.Code[1])
		if deltaUpval < 0 || deltaUpval >= len(proto.Upvalues) {
			return accumulatorClosureFastPath{}, false
		}
		if !((addB == loadReg && addC == deltaReg) || (addB == deltaReg && addC == loadReg)) {
			return accumulatorClosureFastPath{}, false
		}
		fast.deltaKind = accumulatorDeltaUpval
		fast.deltaUpval = deltaUpval
		return fast, true
	default:
		return accumulatorClosureFastPath{}, false
	}
}

func simpleClosureExprPattern(proto *vm.FuncProto) (simpleClosureExpr, bool) {
	if proto == nil || proto.NumParams != 1 || proto.IsVarArg || len(proto.Code) < 2 || len(proto.Code) > 6 {
		return simpleClosureExpr{}, false
	}
	exprs := map[int]simpleClosureExpr{
		0: {kind: simpleClosureExprParam},
	}
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_RETURN {
			if pc != len(proto.Code)-1 || vm.DecodeB(inst) != 2 {
				return simpleClosureExpr{}, false
			}
			retReg := vm.DecodeA(inst)
			expr, ok := exprs[retReg]
			if !ok || simpleClosureExprCost(expr) > 6 {
				return simpleClosureExpr{}, false
			}
			return expr, true
		}
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_LOADINT:
			exprs[a] = simpleClosureExpr{kind: simpleClosureExprIntConst, value: int64(vm.DecodesBx(inst))}
		case vm.OP_GETUPVAL:
			uv := vm.DecodeB(inst)
			if uv < 0 || uv >= len(proto.Upvalues) {
				return simpleClosureExpr{}, false
			}
			exprs[a] = simpleClosureExpr{kind: simpleClosureExprUpval, upval: uv}
		case vm.OP_ADD, vm.OP_MUL:
			left, ok := exprs[vm.DecodeB(inst)]
			if !ok {
				return simpleClosureExpr{}, false
			}
			right, ok := exprs[vm.DecodeC(inst)]
			if !ok {
				return simpleClosureExpr{}, false
			}
			kind := simpleClosureExprAdd
			if op == vm.OP_MUL {
				kind = simpleClosureExprMul
			}
			leftCopy := left
			rightCopy := right
			exprs[a] = simpleClosureExpr{kind: kind, left: &leftCopy, right: &rightCopy}
		default:
			return simpleClosureExpr{}, false
		}
	}
	return simpleClosureExpr{}, false
}

func immediateClosureFactoryPattern(proto *vm.FuncProto) (immediateClosureFactoryFastPath, bool) {
	if proto == nil || proto.NumParams == 0 || proto.IsVarArg || len(proto.Protos) == 0 || len(proto.Code) < 2 {
		return immediateClosureFactoryFastPath{}, false
	}
	if vm.DecodeOp(proto.Code[0]) != vm.OP_CLOSURE || vm.DecodeOp(proto.Code[1]) != vm.OP_RETURN {
		return immediateClosureFactoryFastPath{}, false
	}
	closureReg := vm.DecodeA(proto.Code[0])
	if vm.DecodeA(proto.Code[1]) != closureReg || vm.DecodeB(proto.Code[1]) != 2 {
		return immediateClosureFactoryFastPath{}, false
	}
	childIdx := vm.DecodeBx(proto.Code[0])
	if childIdx < 0 || childIdx >= len(proto.Protos) {
		return immediateClosureFactoryFastPath{}, false
	}
	for i := 2; i < len(proto.Code); i++ {
		op := vm.DecodeOp(proto.Code[i])
		if op != vm.OP_CLOSE && op != vm.OP_RETURN {
			return immediateClosureFactoryFastPath{}, false
		}
	}
	child := proto.Protos[childIdx]
	expr, ok := simpleClosureExprPattern(child)
	if !ok || len(child.Upvalues) == 0 {
		return immediateClosureFactoryFastPath{}, false
	}
	upvalSlots := make([]int, len(child.Upvalues))
	for i, desc := range child.Upvalues {
		if !desc.InStack || desc.Index < 0 || desc.Index >= proto.NumParams {
			return immediateClosureFactoryFastPath{}, false
		}
		upvalSlots[i] = desc.Index
	}
	return immediateClosureFactoryFastPath{proto: proto, expr: expr, upvalSlots: upvalSlots}, true
}

func simpleClosureExprCost(expr simpleClosureExpr) int {
	switch expr.kind {
	case simpleClosureExprParam, simpleClosureExprIntConst, simpleClosureExprUpval:
		return 1
	case simpleClosureExprAdd, simpleClosureExprMul:
		if expr.left == nil || expr.right == nil {
			return 1000
		}
		return 1 + simpleClosureExprCost(*expr.left) + simpleClosureExprCost(*expr.right)
	default:
		return 1000
	}
}

func emitBaselineSelfTailNoReturnFastPath(asm *jit.Assembler, inst uint32, pc int, callerProto *vm.FuncProto, slowLabel string) bool {
	if !isBaselineStaticSelfTailNoReturnCall(callerProto, inst, pc) {
		return false
	}
	a := vm.DecodeA(inst)
	nArgs := vm.DecodeB(inst) - 1
	fallthroughLabel := nextLabel("self_tail_fallthrough")

	loadSlot(asm, jit.X0, a)
	asm.CMPreg(jit.X0, mRegSelfClosure)
	asm.BCond(jit.CondNE, fallthroughLabel)

	// Preserve the existing tiering trigger: the threshold call exits through
	// Go so the TieringManager can attempt promotion. Calls above/below the
	// threshold stay in-frame and avoid the native-call stack entirely.
	asm.LoadImm64(jit.X1, int64(uintptr(unsafe.Pointer(callerProto))))
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel)

	scratch := []jit.Reg{jit.X4, jit.X5, jit.X6, jit.X7}
	for i := 0; i < nArgs; i++ {
		loadSlot(asm, scratch[i], a+1+i)
	}
	for i := 0; i < nArgs; i++ {
		storeSlot(asm, i, scratch[i])
	}
	asm.B(pcLabel(0))

	asm.Label(fallthroughLabel)
	return true
}

func isBaselineStaticSelfTailNoReturnCall(proto *vm.FuncProto, inst uint32, pc int) bool {
	if proto == nil || proto.IsVarArg || !baselineSelfTailNoReturnSafe(proto) {
		return false
	}
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	if b == 0 || c != 1 {
		return false
	}
	nArgs := b - 1
	if nArgs != proto.NumParams || nArgs > 4 {
		return false
	}
	if pc+1 >= len(proto.Code) {
		return false
	}
	next := proto.Code[pc+1]
	if vm.DecodeOp(next) != vm.OP_RETURN || vm.DecodeB(next) != 1 {
		return false
	}
	return isBaselineStaticSelfCall(proto, pc, a)
}

func baselineSelfTailNoReturnSafe(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_CLOSURE, vm.OP_CLOSE, vm.OP_GETUPVAL, vm.OP_SETUPVAL, vm.OP_VARARG:
			return false
		}
	}
	return true
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
