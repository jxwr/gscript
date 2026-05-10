//go:build darwin && arm64

// emit_call_native.go implements native ARM64 BLR calls for the Tier 2 Method JIT.
//
// When Tier 2 encounters OpCall, instead of exiting to Go (emitCallExit ~80ns),
// it emits a native BLR sequence (~10ns) identical to Tier 1's tier1_call.go.
// The key difference: Tier 2 must spill/reload live SSA register allocations
// around the BLR since the callee is free to use the same allocatable registers.
//
// Native call sequence:
//   1. Store function value and arguments to the VM register file
//   2. Spill ALL live SSA registers (GPR + FPR) to their home slots
//   3. Type check: is the function a compiled VMClosure?
//   4. Resolve a direct entry; if no DirectEntryPtr/Tier2DirectEntryPtr is
//      published, fall to slow path
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
	"sort"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	gruntime "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const (
	// Tier 2 direct/self entries use a full 128-byte frame. executeTier2
	// reserves native stack budget before entering JIT code, so this can be
	// higher than the no-reserve emergency limit while still avoiding Go stack
	// guard corruption.
	maxNativeCallDepth = 128

	// Raw-int self calls use an args-only caller shim plus a 16-byte numeric
	// callee frame, much smaller than the boxed direct-entry frame. Multi-arg
	// raw self calls bound native recursion with RawSelfRegsEnd, so they do
	// not need per-call NativeCallDepth traffic.
	maxRawSelfCallDepth = 512

	// Raw-int self BL remains behind a kill switch, but the v1 entry,
	// resume, fallback, and return contract is now wired through
	// emitCallNativeRawIntSelf.
	enableNumericSelfBL = true
)

const (
	rawPeerFrameSize   = 80
	rawPeerRegsOff     = 0
	rawPeerConstsOff   = 8
	rawPeerFuncOff     = 16
	rawPeerArgsOff     = 24
	rawPeerClosureOff  = 56
	rawPeerCallModeOff = 64
)

const (
	typedSelfSavedCallModeOff = 0
	typedSelfArgsOff          = 8
)

const (
	tier2CallCacheWays        = 4
	tier2CallCacheWordsPerWay = 4
	tier2CallCacheWayBytes    = tier2CallCacheWordsPerWay * 8
	tier2CallCacheStrideWords = tier2CallCacheWays * tier2CallCacheWordsPerWay
	tier2CallCacheStrideBytes = tier2CallCacheStrideWords * 8
)

func rawSelfFrameSizeFor(nParams int) int {
	return rawSelfFrameSizeForLive(nParams, 0)
}

func rawSelfFrameSizeForLive(nParams, nLiveRaw int) int {
	size := rawSelfLiveSpillsOff(nParams) + nLiveRaw*jit.ValueSize
	return (size + 15) &^ 15
}

func rawSelfLiveSpillsOff(nParams int) int {
	return 0
}

func typedSelfFrameSizeFor(nArgs int) int {
	size := typedSelfArgsOff + nArgs*jit.ValueSize
	return (size + 15) &^ 15
}

type rawSelfLiveSpill struct {
	valueID  int
	reg      jit.Reg
	slot     int
	stackOff int
}

type callCalleeFlagSpec struct {
	protos        []*vm.FuncProto
	knownLeaf     bool
	knownNoGlobal bool
}

// emitCallNative emits a native BLR call sequence for OpCall in Tier 2.
// Uses selective spill/reload of SSA registers around the BLR: only registers
// that are actually live across the call point are saved/restored. Falls back
// to emitCallExit on the slow path (non-closure, uncompiled, overflow, etc.).
func (ec *emitContext) emitCallNative(instr *Instr) {
	asm := ec.asm

	desc := callExitDescriptorFromInstr(instr)
	funcSlot := desc.slot
	nArgs := desc.nArgs
	nRets := desc.nRets
	noDepthCallee := ec.staticNoDepthCallee(instr)

	// Step 1: Store the function value and arguments to the VM register file.
	// This must happen BEFORE spilling, since resolveValueNB may read from
	// SSA registers that we're about to spill.
	ec.emitStoreCallFrameArgs(instr, funcSlot)

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

	// Step 3: Load function value from regs[funcSlot]. The native-depth guard
	// runs after callee resolution, where LeafNoCall can skip it entirely.
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))

	// --- Polymorphic call IC fast path ---
	// Allocate this call site's cache slot (4 ways × 4 uint64).
	icIdx := ec.nextCallCacheIndex
	ec.nextCallCacheIndex++
	ec.recordCallCachePC(icIdx, instr.SourcePC)
	cacheOff := icIdx * tier2CallCacheStrideBytes
	icDoneLabel := ec.uniqueLabel("t2call_ic_done")

	// X3 = &CallCache[site][0].
	asm.LDR(jit.X3, mRegCtx, execCtxOffTier2CallCache)
	if cacheOff > 0 {
		if cacheOff <= 4095 {
			asm.ADDimm(jit.X3, jit.X3, uint16(cacheOff))
		} else {
			asm.LoadImm64(jit.X4, int64(cacheOff))
			asm.ADDreg(jit.X3, jit.X3, jit.X4)
		}
	}
	icHitLabels := make([]string, tier2CallCacheWays)
	for way := 0; way < tier2CallCacheWays; way++ {
		icHitLabels[way] = ec.uniqueLabel("t2call_ic_hit")
		wayOff := way * tier2CallCacheWayBytes
		asm.LDR(jit.X4, jit.X3, wayOff+baselineCallCacheBoxedOff)
		asm.CMPreg(jit.X0, jit.X4)
		asm.BCond(jit.CondEQ, icHitLabels[way])
	}

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

	// Update IC cache with the boxed closure value (reload from
	// memory since X0 now holds the raw ptr), direct entry, proto, and entry
	// publication version.
	icUpdateWayLabels := make([]string, tier2CallCacheWays)
	for way := 0; way < tier2CallCacheWays; way++ {
		icUpdateWayLabels[way] = ec.uniqueLabel("t2call_ic_update")
		wayOff := way * tier2CallCacheWayBytes
		asm.LDR(jit.X4, jit.X3, wayOff+baselineCallCacheBoxedOff)
		asm.CBZ(jit.X4, icUpdateWayLabels[way])
	}
	asm.B(icUpdateWayLabels[0])
	for way := 0; way < tier2CallCacheWays; way++ {
		wayOff := way * tier2CallCacheWayBytes
		asm.Label(icUpdateWayLabels[way])
		asm.LDR(jit.X4, mRegRegs, slotOffset(funcSlot)) // re-load boxed value
		asm.STR(jit.X4, jit.X3, wayOff+baselineCallCacheBoxedOff)
		ec.emitTaggedLeafEntryIfAvailable(jit.X1, jit.X2, jit.X4)
		asm.STR(jit.X2, jit.X3, wayOff+baselineCallCacheEntryOff)
		asm.STR(jit.X1, jit.X3, wayOff+baselineCallCacheProtoOff)
		asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryVersion)
		asm.STR(jit.X4, jit.X3, wayOff+baselineCallCacheVersionOff)
		asm.B(icDoneLabel)
	}

	// --- IC Hit: validate direct-entry version, refreshing entry on change. ---
	// X0 still holds the boxed closure value (matched cache).
	for way := 0; way < tier2CallCacheWays; way++ {
		wayOff := way * tier2CallCacheWayBytes
		asm.Label(icHitLabels[way])
		asm.LDR(jit.X2, jit.X3, wayOff+baselineCallCacheEntryOff)   // X2 = cached direct entry
		asm.LDR(jit.X1, jit.X3, wayOff+baselineCallCacheProtoOff)   // X1 = cached *Proto
		asm.LDR(jit.X4, jit.X3, wayOff+baselineCallCacheVersionOff) // X4 = cached entry version
		asm.LDR(jit.X5, jit.X1, funcProtoOffDirectEntryVersion)
		icVersionOKLabel := ec.uniqueLabel("t2call_ic_version_ok")
		asm.CMPreg(jit.X4, jit.X5)
		asm.BCond(jit.CondEQ, icVersionOKLabel)
		asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryPtr)
		icHaveEntryLabel := ec.uniqueLabel("t2call_ic_have_entry")
		asm.CBNZ(jit.X4, icHaveEntryLabel)
		asm.LDR(jit.X4, jit.X1, funcProtoOffTier2DirectEntryPtr)
		// DirectEntryPtr can be cleared when a baseline/native caller disables
		// generic BLR after an exit. Tier 2 ICs may still use the separate Tier 2
		// entry while it is published, but must not keep a stale entry after both
		// published entry pointers have been cleared.
		asm.CBZ(jit.X4, slowLabel)
		asm.Label(icHaveEntryLabel)
		asm.MOVreg(jit.X2, jit.X4)
		ec.emitTaggedLeafEntryIfAvailable(jit.X1, jit.X2, jit.X4)
		asm.STR(jit.X2, jit.X3, wayOff+baselineCallCacheEntryOff)
		asm.STR(jit.X5, jit.X3, wayOff+baselineCallCacheVersionOff)
		asm.Label(icVersionOKLabel)
		jit.EmitExtractPtr(asm, jit.X0, jit.X0) // X0 = *Closure
		asm.B(icDoneLabel)
	}

	asm.Label(icDoneLabel)
	ec.emitDecodeTaggedPeerEntry(jit.X2, jit.X5)

	if noDepthCallee != nil {
		asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(noDepthCallee))))
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondNE, slowLabel)
	}
	flagSpec := ec.callCalleeFlagSpec(instr)
	if noDepthCallee == nil {
		ec.emitGuardCalleeProtoSet(flagSpec.protos, slowLabel)
	}

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

	// Step 6: Increment callee's CallCount until Tier 2 is installed. Once a
	// callee has a Tier 2 entry, the hot native peer-call path no longer needs
	// to feed promotion counters on every call.
	// X0 = *vm.Closure, X1 = *FuncProto, X2 = DirectEntryPtr.
	skipCallCountLabel := ec.uniqueLabel("t2call_skip_callcount")
	asm.LDR(jit.X3, jit.X1, funcProtoOffTier2DirectEntryPtr)
	asm.CBNZ(jit.X3, skipCallCountLabel)
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	// If at Tier 2 threshold, fall to slow path to trigger compilation.
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel)
	asm.Label(skipCallCountLabel)

	staticSelf := ec.fn != nil && ec.fn.Proto != nil && ec.fn.Proto.HasSelfCalls && ec.isStaticSelfCall(instr)
	stackSlowLabel := ec.uniqueLabel("t2call_stack_slow")
	knownLeafCall := noDepthCallee != nil || flagSpec.knownLeaf
	knownNoGlobalCall := staticSelf || flagSpec.knownNoGlobal
	dynamicCalleeFlags := (noDepthCallee == nil && !knownLeafCall) || (!staticSelf && !knownNoGlobalCall)

	// Step 7: Save caller state on stack (128 bytes, 16-byte aligned).
	// R111: for a static self-call, GlobalCache is invariant
	// (same proto → same GlobalCache), so skip saving that field.
	// CallMode cannot be skipped: top-level Tier 2 enters with CallMode=0,
	// while a BL/BLR callee must return through the direct epilogue.
	asm.SUBimm(jit.SP, jit.SP, 128)
	if dynamicCalleeFlags {
		knownFlags := uint16(0)
		if knownLeafCall {
			knownFlags |= 1
		}
		if knownNoGlobalCall {
			knownFlags |= 2
		}
		asm.MOVimm16(jit.X6, knownFlags)
		if noDepthCallee == nil && !knownLeafCall {
			asm.LDRB(jit.X4, jit.X1, funcProtoOffLeafNoCall)
			asm.ORRreg(jit.X6, jit.X6, jit.X4)
		}
		if !staticSelf && !knownNoGlobalCall {
			asm.LDRB(jit.X4, jit.X1, funcProtoOffNoGlobalOps)
			asm.LSLimm(jit.X4, jit.X4, 1)
			asm.ORRreg(jit.X6, jit.X6, jit.X4)
		}
		asm.STR(jit.X6, jit.SP, 120)
	}
	if noDepthCallee == nil && !knownLeafCall {
		depthOKLabel := ec.uniqueLabel("t2call_depth_ok")
		asm.TBNZ(jit.X6, 0, depthOKLabel)
		asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
		asm.CMPimm(jit.X3, maxNativeCallDepth)
		asm.BCond(jit.CondGE, stackSlowLabel)
		asm.Label(depthOKLabel)
	}
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STP(mRegRegs, mRegConsts, jit.SP, 16)
	ec.emitLoadCallMode(jit.X3)
	asm.STR(jit.X3, jit.SP, 32)
	// Save caller's ClosurePtr (always — closure instance may differ).
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X3, jit.SP, 40)
	if !staticSelf {
		if !knownNoGlobalCall {
			skipSaveGlobalsLabel := ec.uniqueLabel("t2call_skip_save_globals")
			asm.TBNZ(jit.X6, 1, skipSaveGlobalsLabel)
			asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
			asm.STR(jit.X3, jit.SP, 48)
			asm.LDR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
			asm.STR(jit.X3, jit.SP, 56)
			asm.LDR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
			asm.STR(jit.X3, jit.SP, 64)
			asm.LDR(jit.X3, mRegCtx, execCtxOffTier2GlobalIndex)
			asm.STR(jit.X3, jit.SP, 72)
			asm.Label(skipSaveGlobalsLabel)
		}
	}
	// Keep the callee closure pointer for ExitNativeCallExit. If the callee
	// returns through an exit-resume path, caller state is restored before Go
	// sees the exit, so the raw closure pointer must survive independently.
	asm.STR(jit.X0, jit.SP, 112)

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

	// Set CallMode. Tagged call-IC entries use the Tier 2-only boxed leaf ABI
	// that returns the normal boxed result in X0.
	ec.emitStoreCallMode(jit.X5)

	// R111: skip GlobalCache setup on static self-call (per-proto invariant).
	if !staticSelf {
		if !knownNoGlobalCall {
			skipSetupGlobalsLabel := ec.uniqueLabel("t2call_skip_setup_globals")
			asm.TBNZ(jit.X6, 1, skipSetupGlobalsLabel)
			// Load callee's GlobalValCache from Proto.
			asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
			asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
			asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalCachePtr)
			asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
			asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalCacheGenPtr)
			asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
			asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalIndexPtr)
			asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalIndex)
			asm.Label(skipSetupGlobalsLabel)
		}
		asm.LDR(jit.X3, jit.X1, funcProtoOffFieldCache)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineFieldCache)
		asm.LDR(jit.X3, jit.X1, funcProtoOffFieldPolyCache)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineFieldPolyCache)
		asm.LDR(jit.X3, jit.X1, funcProtoOffTableStringKeyCache)
		asm.STR(jit.X3, mRegCtx, execCtxOffBaselineTableStringKeyCache)
	}

	// Increment NativeCallDepth unless the guarded callee is a leaf.
	if noDepthCallee == nil && !knownLeafCall {
		skipDepthIncLabel := ec.uniqueLabel("t2call_skip_depth_inc")
		asm.TBNZ(jit.X6, 0, skipDepthIncLabel)
		asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
		asm.ADDimm(jit.X3, jit.X3, 1)
		asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
		asm.Label(skipDepthIncLabel)
	}

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

	// Decrement NativeCallDepth unless the guarded callee is a leaf.
	if noDepthCallee == nil && !knownLeafCall {
		skipDepthDecLabel := ec.uniqueLabel("t2call_skip_depth_dec")
		asm.LDR(jit.X6, jit.SP, 120)
		asm.TBNZ(jit.X6, 0, skipDepthDecLabel)
		asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
		asm.SUBimm(jit.X3, jit.X3, 1)
		asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
		asm.Label(skipDepthDecLabel)
	}
	ec.emitLoadCallMode(jit.X8)

	// Snapshot callee exit metadata only on the cold exit path. Successful
	// native peer calls are the hot path and do not need resume metadata.
	skipExitSnapshotLabel := ec.uniqueLabel("t2call_skip_exit_snapshot")
	asm.MOVimm16(jit.X7, 0)
	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBZ(jit.X3, skipExitSnapshotLabel)
	asm.MOVimm16(jit.X7, 1)
	ec.emitPushNativeCallExitFrameIfNested(jit.X3, jit.X4, jit.X5, jit.X6)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCalleeExitCode)
	asm.LDR(jit.X3, mRegCtx, execCtxOffResumeNumericPass)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCalleeResumePass)
	asm.LDR(jit.X3, mRegCtx, execCtxOffExitResumePC)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCalleeResumePC)
	asm.LDR(jit.X3, jit.SP, 112)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.Label(skipExitSnapshotLabel)

	// Step 10: Restore caller state.
	asm.LDP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, jit.SP, 32)
	ec.emitStoreCallMode(jit.X3)
	asm.LDR(jit.X3, jit.SP, 40)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	if !staticSelf {
		if !knownNoGlobalCall {
			skipRestoreGlobalsLabel := ec.uniqueLabel("t2call_skip_restore_globals")
			asm.LDR(jit.X6, jit.SP, 120)
			asm.TBNZ(jit.X6, 1, skipRestoreGlobalsLabel)
			asm.LDR(jit.X3, jit.SP, 48)
			asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
			asm.LDR(jit.X3, jit.SP, 56)
			asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
			asm.LDR(jit.X3, jit.SP, 64)
			asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
			asm.LDR(jit.X3, jit.SP, 72)
			asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalIndex)
			asm.Label(skipRestoreGlobalsLabel)
		}
		if ec.fn != nil && ec.fn.Proto != nil {
			asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(ec.fn.Proto))))
			asm.LDR(jit.X4, jit.X3, funcProtoOffFieldCache)
			asm.STR(jit.X4, mRegCtx, execCtxOffBaselineFieldCache)
			asm.LDR(jit.X4, jit.X3, funcProtoOffFieldPolyCache)
			asm.STR(jit.X4, mRegCtx, execCtxOffBaselineFieldPolyCache)
			asm.LDR(jit.X4, jit.X3, funcProtoOffTableStringKeyCache)
			asm.STR(jit.X4, mRegCtx, execCtxOffBaselineTableStringKeyCache)
		}
	}
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 128)

	// Restore ctx pointers.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	// Step 11: Check callee exit code.
	asm.CBNZ(jit.X7, exitHandleLabel)

	// R143: save representation state BEFORE the post-BL emit. Reloading
	// selective values normalizes boxed homes, and the mutually-exclusive
	// exit/fallback emit paths must still see the pre-call representation.
	savedReprs := ec.snapshotValueReprs()

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)

	resultReadyLabel := ec.uniqueLabel("t2call_result_ready")
	if nRets > 0 {
		asm.CMPimm(jit.X8, callModeLeafX0)
		asm.BCond(jit.CondEQ, resultReadyLabel)
		asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
		asm.Label(resultReadyLabel)
		ec.storeResultNB(jit.X0, instr.ID)
	}
	postSuccessReprs := ec.snapshotValueReprs()

	asm.B(doneLabel)

	if noDepthCallee == nil && !knownLeafCall {
		asm.Label(stackSlowLabel)
		asm.ADDimm(jit.SP, jit.SP, 128)
		asm.B(slowLabel)
	}

	// --- Callee exited mid-execution (deopt/op-exit within callee) ---
	// Return to Go with enough metadata to resume the callee's own
	// exit-resume loop. This avoids replaying the call from the beginning
	// after the callee may already have mutated visible state.
	asm.Label(exitHandleLabel)
	ec.emitRequireNativeCalleeTier2Only(slowLabel)
	ec.restoreValueReprSnapshot(savedReprs)
	ec.emitNativeCallExit(instr, funcSlot, nArgs, nRets, calleeBaseOff)

	// Slow path: no native entry was taken, so the normal caller-side
	// call-exit fallback executes the call exactly once through the VM.
	asm.Label(slowLabel)
	ec.restoreValueReprSnapshot(savedReprs)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postSuccessReprs)
	ec.restoreValueReprSnapshot(postSuccessReprs)

	// --- Done: merge point for native and slow paths ---
	asm.Label(doneLabel)
}

func (ec *emitContext) emitRequireNativeCalleeTier2Only(slowLabel string) {
	asm := ec.asm
	// This predicate is only needed after a callee exit. Keeping it out of
	// the successful call path avoids per-call DirectEntryPtr traffic.
	asm.LDR(jit.X0, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.CBZ(jit.X0, slowLabel)
	asm.LDR(jit.X0, jit.X0, vmClosureOffProto)
	asm.LDR(jit.X0, jit.X0, funcProtoOffDirectEntryPtr)
	asm.CBNZ(jit.X0, slowLabel)
	asm.MOVimm16(jit.X0, 1)
	asm.STR(jit.X0, mRegCtx, execCtxOffNativeCalleeTier2Only)
}

func (ec *emitContext) emitNativeCallExit(instr *Instr, funcSlot, nArgs, nRets, calleeBaseOff int) {
	ec.emitStoreNativeCallExitDescriptor(callExitDescriptor{
		slot:    funcSlot,
		nArgs:   nArgs,
		nRets:   nRets,
		instrID: instr.ID,
	}, calleeBaseOff)
	ec.emitCallProtocolExitToGo(ExitNativeCallExit)
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
	desc := callExitDescriptorFromInstr(instr)
	funcSlot := desc.slot
	nArgs := desc.nArgs
	nRets := desc.nRets

	ec.emitStoreCallFrameArgs(instr, funcSlot)

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillSelectiveForCall(liveGPRs, liveFPRs)

	slowLabel := ec.uniqueLabel("t2self_slow")
	doneLabel := ec.uniqueLabel("t2self_done")
	exitHandleLabel := ec.uniqueLabel("t2self_callee_exit")

	savedReprs := ec.snapshotValueReprs()

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
	ec.emitLoadCallMode(jit.X3)
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
	ec.emitStoreCallMode(jit.X3)

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
	ec.emitStoreCallMode(jit.X3)
	asm.LDR(jit.X3, jit.SP, 40)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 64)
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X3, exitHandleLabel)

	if nRets > 0 {
		asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	}

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)

	if nRets > 0 {
		asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
		ec.storeResultNB(jit.X0, instr.ID)
	}
	postSuccessReprs := ec.snapshotValueReprs()
	asm.B(doneLabel)

	asm.Label(exitHandleLabel)
	asm.Label(slowLabel)
	ec.restoreValueReprSnapshot(savedReprs)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postSuccessReprs)
	ec.restoreValueReprSnapshot(postSuccessReprs)

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
	nRets := callResultCountFromAux2(instr.Aux2)
	nParams := ec.fn.Proto.NumParams
	if nArgs != nParams || nParams < 1 || nParams > 4 {
		ec.emitCallNativeStaticSelfFast(instr)
		return
	}

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)

	preCallSlowLabel := ec.uniqueLabel("t2rawself_slow")
	exitLabel := ec.uniqueLabel("t2rawself_exit")
	fallbackLabel := ec.uniqueLabel("t2rawself_fallback")
	doneLabel := ec.uniqueLabel("t2rawself_done")

	preReprs := ec.snapshotValueReprs()
	rawLiveSpills := ec.rawSelfLiveSpills(liveGPRs, nParams)
	boxedLiveGPRs := liveGPRs
	if len(rawLiveSpills) > 0 {
		boxedLiveGPRs = cloneBoolMap(liveGPRs)
		for _, spill := range rawLiveSpills {
			delete(boxedLiveGPRs, spill.valueID)
		}
	}
	boxedRawReloads := preReprs.rawIntSubset(boxedLiveGPRs)
	if len(boxedLiveGPRs) > 0 || len(liveFPRs) > 0 {
		ec.emitSpillSelectiveForCall(boxedLiveGPRs, liveFPRs)
	}

	// Raw-call frame:
	//   0..       raw live GPR spills that survive the BL on the success path
	//
	// The caller's own entry frame already owns FP/LR, and raw-int self calls
	// stay within one proto/closure/constant domain. The callee base is always
	// caller base + calleeBaseOff, so the successful and callee-exit paths
	// restore mRegRegs with offset arithmetic instead of saving it in the shim
	// frame. Successful raw calls keep ctx.Regs lazy; numeric exit epilogues and
	// raw-call fallback paths publish the current base and materialize raw live
	// spills into boxed VM homes before Go observes the context. Pre-call
	// fallback rebuilds args directly from X0..X3, while callee exits use the
	// native-call-exit descriptor and no longer need saved raw args to replay
	// the call. The boxed function operand is rebuilt from BaselineClosurePtr;
	// static self recursion cannot change closure identity while this native
	// frame is executing. Numeric entries return a status in X16 (0 = success,
	// non-zero = ctx.ExitCode), so raw self calls leave ctx.CallMode unchanged
	// and avoid the per-call ExitCode load on success.
	rawFrameSize := rawSelfFrameSizeForLive(nParams, len(rawLiveSpills))

	ec.emitNumericArgsInRegs(instr, nParams)
	ec.emitAllocRawSelfFrame(rawFrameSize)
	ec.emitSaveRawSelfLiveSpills(rawLiveSpills)

	calleeBaseOff := ec.nextSlot * jit.ValueSize
	calleeFrameBytes := ec.nextSlot * jit.ValueSize
	useRawSelfRegsBudget := nParams >= 2

	if !useRawSelfRegsBudget {
		asm.LDR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
		asm.CMPimm(jit.X7, maxRawSelfCallDepth)
		asm.BCond(jit.CondGE, preCallSlowLabel)
	}

	if calleeBaseOff+calleeFrameBytes <= 4095 {
		asm.ADDimm(jit.X8, mRegRegs, uint16(calleeBaseOff+calleeFrameBytes))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff+calleeFrameBytes))
		asm.ADDreg(jit.X8, mRegRegs, jit.X8)
	}
	if useRawSelfRegsBudget {
		asm.LDR(jit.X9, mRegCtx, execCtxOffRawSelfRegsEnd)
	} else {
		asm.LDR(jit.X9, mRegCtx, execCtxOffRegsEnd)
	}
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondHI, preCallSlowLabel)

	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X8)
	}

	if !useRawSelfRegsBudget {
		asm.ADDimm(jit.X7, jit.X7, 1)
		asm.STR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
	}

	asm.BL(fmt.Sprintf("t2_numeric_self_entry_%d", nParams))

	if !useRawSelfRegsBudget {
		asm.LDR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
		asm.SUBimm(jit.X7, jit.X7, 1)
		asm.STR(jit.X7, mRegCtx, execCtxOffNativeCallDepth)
	}

	asm.CBNZ(jit.X16, exitLabel)

	ec.emitRestoreRawSelfCallerRegsFromCalleeBase(calleeBaseOff)
	ec.emitReloadRawSelfLiveSpills(rawLiveSpills)
	ec.emitFreeRawSelfFrame(rawFrameSize)
	ec.emitReloadSelectiveForCall(boxedLiveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(boxedRawReloads)
	ec.restoreValueReprSnapshot(preReprs)
	ec.storeRawInt(jit.X0, instr.ID)
	postReprs := ec.snapshotValueReprs()
	asm.B(doneLabel)

	asm.Label(exitLabel)
	ec.emitPushNativeCallExitFrameIfNested(jit.X7, jit.X8, jit.X9, jit.X10)
	asm.LDR(jit.X7, mRegCtx, execCtxOffExitCode)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCalleeExitCode)
	asm.LDR(jit.X7, mRegCtx, execCtxOffResumeNumericPass)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCalleeResumePass)
	asm.LDR(jit.X7, mRegCtx, execCtxOffExitResumePC)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCalleeResumePC)
	asm.LDR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.MOVimm16(jit.X7, 1)
	asm.STR(jit.X7, mRegCtx, execCtxOffNativeCalleeTier2Only)
	ec.emitRestoreRawSelfCallerRegsFromCalleeBase(calleeBaseOff)
	ec.emitPublishRawSelfCallerState()
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeRawSelfLiveSpills(rawLiveSpills)
	ec.emitMaterializeRawIntSelfFunctionFromSelfClosure(funcSlot)
	ec.emitFreeRawSelfFrame(rawFrameSize)
	ec.emitNativeCallExit(instr, funcSlot, nArgs, nRets, calleeBaseOff)

	asm.Label(preCallSlowLabel)
	ec.emitPublishRawSelfCallerState()

	asm.Label(fallbackLabel)
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeRawSelfLiveSpills(rawLiveSpills)
	ec.emitMaterializeRawIntSelfCallFrameFromArgRegs(funcSlot, nArgs)
	ec.emitFreeRawSelfFrame(rawFrameSize)
	ec.emitRawIntSelfCallExitResume(instr, funcSlot, nArgs, nRets, preReprs, liveGPRs, liveFPRs)
	ec.restoreValueReprSnapshot(postReprs)

	asm.Label(doneLabel)
}

func (ec *emitContext) rawSelfLiveSpills(gprLive map[int]bool, nParams int) []rawSelfLiveSpill {
	if len(gprLive) == 0 {
		return nil
	}
	ids := make([]int, 0, len(gprLive))
	for valueID := range gprLive {
		if ec.valueReprOf(valueID) != valueReprRawInt {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		if _, active := ec.activeRegs[valueID]; !active {
			continue
		}
		if _, ok := ec.slotMap[valueID]; !ok {
			continue
		}
		ids = append(ids, valueID)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Ints(ids)
	spills := make([]rawSelfLiveSpill, 0, len(ids))
	stackOff := rawSelfLiveSpillsOff(nParams)
	for _, valueID := range ids {
		pr := ec.alloc.ValueRegs[valueID]
		spills = append(spills, rawSelfLiveSpill{
			valueID:  valueID,
			reg:      jit.Reg(pr.Reg),
			slot:     ec.slotMap[valueID],
			stackOff: stackOff,
		})
		stackOff += jit.ValueSize
	}
	return spills
}

func (ec *emitContext) emitRestoreRawSelfCallerRegsFromCalleeBase(calleeBaseOff int) {
	asm := ec.asm
	if calleeBaseOff <= 4095 {
		asm.SUBimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.SUBreg(mRegRegs, mRegRegs, jit.X8)
	}
}

func (ec *emitContext) emitAllocRawSelfFrame(rawFrameSize int) {
	if rawFrameSize > 0 {
		ec.asm.SUBimm(jit.SP, jit.SP, uint16(rawFrameSize))
	}
}

func (ec *emitContext) emitFreeRawSelfFrame(rawFrameSize int) {
	if rawFrameSize > 0 {
		ec.asm.ADDimm(jit.SP, jit.SP, uint16(rawFrameSize))
	}
}

func (ec *emitContext) emitSaveRawSelfLiveSpills(spills []rawSelfLiveSpill) {
	for _, spill := range spills {
		ec.asm.STR(spill.reg, jit.SP, spill.stackOff)
	}
}

func (ec *emitContext) emitReloadRawSelfLiveSpills(spills []rawSelfLiveSpill) {
	for _, spill := range spills {
		ec.asm.LDR(spill.reg, jit.SP, spill.stackOff)
	}
}

func (ec *emitContext) emitMaterializeRawSelfLiveSpills(spills []rawSelfLiveSpill) {
	for _, spill := range spills {
		ec.asm.LDR(jit.X10, jit.SP, spill.stackOff)
		jit.EmitBoxIntFast(ec.asm, jit.X10, jit.X10, mRegTagInt)
		ec.asm.STR(jit.X10, mRegRegs, slotOffset(spill.slot))
		ec.emitExitResumeCheckShadowStoreGPR(spill.slot, jit.X10)
	}
}

func (ec *emitContext) emitRestoreRawSelfCallerStateFromCalleeBase(calleeBaseOff int) {
	ec.emitRestoreRawSelfCallerRegsFromCalleeBase(calleeBaseOff)
	ec.emitPublishRawSelfCallerState()
}

func (ec *emitContext) emitPublishRawSelfCallerState() {
	ec.asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
}

func (ec *emitContext) emitBoxCurrentClosure(dst, scratch jit.Reg) {
	ec.asm.LDR(dst, mRegCtx, execCtxOffBaselineClosurePtr)
	ec.asm.UBFX(dst, dst, 0, 44)
	ec.asm.LoadImm64(scratch, nbClosureTagBits)
	ec.asm.ORRreg(dst, dst, scratch)
}

func (ec *emitContext) emitMaterializeRawIntSelfFunctionFromSelfClosure(funcSlot int) {
	asm := ec.asm
	ec.emitBoxCurrentClosure(jit.X10, jit.X11)
	asm.STR(jit.X10, mRegRegs, slotOffset(funcSlot))
	ec.emitExitResumeCheckShadowStoreGPR(funcSlot, jit.X10)
}

func (ec *emitContext) emitMaterializeRawIntSelfCallFrameFromArgRegs(funcSlot, nArgs int) {
	asm := ec.asm
	ec.emitMaterializeRawIntSelfFunctionFromSelfClosure(funcSlot)
	for i := 0; i < nArgs; i++ {
		argReg := jit.Reg(int(jit.X0) + i)
		jit.EmitBoxIntFast(asm, jit.X10, argReg, mRegTagInt)
		asm.STR(jit.X10, mRegRegs, slotOffset(funcSlot+1+i))
		ec.emitExitResumeCheckShadowStoreGPR(funcSlot+1+i, jit.X10)
	}
}

func (ec *emitContext) emitRawIntSelfCallExitResume(instr *Instr, funcSlot, nArgs, nRets int, preReprs valueReprSnapshot, liveGPRs, liveFPRs map[int]bool) {
	asm := ec.asm

	ec.recordExitResumeCheckSiteWithLive(
		instr,
		ExitCallExit,
		ec.exitResumeCheckLiveSlots(liveGPRs, liveFPRs),
		callExitModifiedSlots(funcSlot, nRets),
		exitResumeCheckOptions{RequireCallFunc: true, RequireRawIntArgs: true},
	)

	ec.emitStoreCallExitDescriptor(callExitDescriptor{
		slot:    funcSlot,
		nArgs:   nArgs,
		nRets:   nRets,
		instrID: instr.ID,
	})
	ec.emitCallProtocolExitToGo(ExitCallExit)

	continueLabel := ec.passLabel(fmt.Sprintf("call_continue_%d", instr.ID))
	asm.Label(continueLabel)

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(preReprs)
	ec.restoreValueReprSnapshot(preReprs)
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

func (ec *emitContext) emitCallNativeRawIntPeerIfEligible(instr *Instr) bool {
	callee := ec.rawIntPeerCallee(instr)
	if callee == nil {
		return false
	}
	nArgs := len(instr.Args) - 1
	nRets := callResultCountFromAux2(instr.Aux2)
	if nRets != 1 || nArgs != callee.NumParams || nArgs < 1 || nArgs > 4 {
		return false
	}

	asm := ec.asm
	funcSlot := int(instr.Aux)
	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillTypedPeerLiveForSuccess(liveFPRs)

	fallbackLabel := ec.uniqueLabel("t2rawpeer_fallback")
	exitLabel := ec.uniqueLabel("t2rawpeer_exit")
	materializeLabel := ec.uniqueLabel("t2rawpeer_materialize")
	doneLabel := ec.uniqueLabel("t2rawpeer_done")
	preReprs := ec.snapshotValueReprs()
	leafCallee := rawIntPeerLeafCallee(callee)

	asm.SUBimm(jit.SP, jit.SP, rawPeerFrameSize)
	if !leafCallee {
		asm.STR(mRegRegs, jit.SP, rawPeerRegsOff)
		asm.STR(mRegConsts, jit.SP, rawPeerConstsOff)
		asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
		asm.STR(jit.X8, jit.SP, rawPeerClosureOff)
	}

	fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X6)
	if fnReg != jit.X6 {
		asm.MOVreg(jit.X6, fnReg)
	}
	asm.STR(jit.X6, jit.SP, rawPeerFuncOff)

	ec.emitNumericArgsInRegs(instr, nArgs)
	for i := 0; i < nArgs; i++ {
		argReg := jit.Reg(int(jit.X0) + i)
		asm.STR(argReg, jit.SP, rawPeerArgsOff+i*jit.ValueSize)
	}

	// Guard the static callee identity. Stable globals make this hot-path
	// predictable, but the guard keeps rebinding and cache invalidation safe.
	asm.LSRimm(jit.X7, jit.X6, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X8, int64((jit.NB_TagPtrShr48<<4)|nbPtrSubVMClosure))
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondNE, fallbackLabel)
	jit.EmitExtractPtr(asm, jit.X7, jit.X6)
	if !leafCallee {
		asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
	}
	asm.LDR(jit.X7, jit.X7, vmClosureOffProto)
	asm.LoadImm64(jit.X8, int64(uintptr(unsafe.Pointer(callee))))
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondNE, fallbackLabel)
	asm.LDR(jit.X16, jit.X7, funcProtoOffTier2NumericEntryPtr)
	asm.CBZ(jit.X16, fallbackLabel)

	if !leafCallee {
		asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		asm.CMPimm(jit.X8, maxNativeCallDepth)
		asm.BCond(jit.CondGE, fallbackLabel)
	}

	calleeBaseOff := ec.nextSlot * jit.ValueSize
	asm.LoadImm64(jit.X8, int64(callee.MaxStack*jit.ValueSize))
	if calleeBaseOff <= 4095 {
		asm.ADDimm(jit.X8, jit.X8, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X9, int64(calleeBaseOff))
		asm.ADDreg(jit.X8, jit.X8, jit.X9)
	}
	asm.ADDreg(jit.X8, jit.X8, mRegRegs)
	asm.LDR(jit.X9, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondHI, fallbackLabel)

	// The raw peer path only exists after the callee has published a Tier 2
	// numeric entry, so this hot call no longer needs to feed tiering counters.
	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X8)
	}
	asm.LDR(mRegConsts, jit.X7, funcProtoOffConstants)

	if !leafCallee {
		asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		asm.ADDimm(jit.X8, jit.X8, 1)
		asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		if callee.NumParams >= 2 {
			ec.emitSetRawSelfRegsEnd(mRegRegs, callee.MaxStack, jit.X8, jit.X9)
		}
	}
	asm.BLR(jit.X16)
	if !leafCallee {
		asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		asm.SUBimm(jit.X8, jit.X8, 1)
		asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	}

	asm.CBNZ(jit.X16, exitLabel)

	// Numeric entries return raw int64 in X0 on ExitNormal. Boxed fallback
	// results are handled by emitRawIntPeerCallExitResume.
	if leafCallee {
		ec.emitRestoreRawPeerLeafCallerRegs(calleeBaseOff)
	} else {
		ec.emitRestoreRawPeerCallerState()
	}
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(preReprs)
	ec.restoreValueReprSnapshot(preReprs)
	ec.storeRawInt(jit.X0, instr.ID)
	postReprs := ec.snapshotValueReprs()
	asm.B(doneLabel)

	asm.Label(exitLabel)
	if leafCallee {
		ec.emitRestoreRawPeerLeafCallerRegs(calleeBaseOff)
	} else {
		ec.emitRestoreRawPeerCallerState()
	}
	asm.B(materializeLabel)

	asm.Label(fallbackLabel)
	if !leafCallee {
		ec.emitRestoreRawPeerCallerState()
	}
	asm.Label(materializeLabel)
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeRawIntPeerCallFrame(funcSlot, nArgs)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitRawIntPeerCallExitResume(instr, funcSlot, nArgs, nRets, preReprs, liveGPRs, liveFPRs)
	ec.restoreValueReprSnapshot(postReprs)

	asm.Label(doneLabel)
	return true
}

func (ec *emitContext) emitCallNativeTypedPeerIfEligible(instr *Instr) bool {
	if ec == nil || ec.fn == nil || instr == nil || ec.fn.CallABIs == nil {
		return false
	}
	desc, ok := ec.fn.CallABIs[instr.ID]
	if !ok || !desc.TypedPeer || desc.Callee == nil {
		return false
	}
	callee := desc.Callee
	nArgs := len(instr.Args) - 1
	nRets := callResultCountFromAux2(instr.Aux2)
	if nRets != 1 || nArgs != desc.NumArgs || nArgs != callee.NumParams || nArgs < 1 || nArgs > 4 {
		return false
	}
	if len(desc.ParamReps) != nArgs {
		return false
	}
	switch desc.ReturnRep {
	case SpecializedABIReturnRawInt, SpecializedABIReturnRawFloat, SpecializedABIReturnRawTablePtr:
	default:
		return false
	}

	asm := ec.asm
	funcSlot := int(instr.Aux)
	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillTypedPeerLiveForSuccess(liveFPRs)

	fallbackLabel := ec.uniqueLabel("t2typedpeer_fallback")
	exitLabel := ec.uniqueLabel("t2typedpeer_exit")
	doneLabel := ec.uniqueLabel("t2typedpeer_done")
	preReprs := ec.snapshotValueReprs()

	asm.SUBimm(jit.SP, jit.SP, rawPeerFrameSize)
	asm.STR(mRegRegs, jit.SP, rawPeerRegsOff)
	asm.STR(mRegConsts, jit.SP, rawPeerConstsOff)
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, jit.SP, rawPeerClosureOff)
	ec.emitLoadCallMode(jit.X8)
	asm.STR(jit.X8, jit.SP, rawPeerCallModeOff)

	fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X6)
	if fnReg != jit.X6 {
		asm.MOVreg(jit.X6, fnReg)
	}
	asm.STR(jit.X6, jit.SP, rawPeerFuncOff)
	ec.emitTypedPeerArgsInRegsAndSave(instr, desc, fallbackLabel)

	asm.LSRimm(jit.X7, jit.X6, uint8(nbPtrSubShift))
	asm.LoadImm64(jit.X8, int64((jit.NB_TagPtrShr48<<4)|nbPtrSubVMClosure))
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondNE, fallbackLabel)
	jit.EmitExtractPtr(asm, jit.X7, jit.X6)
	asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LDR(jit.X7, jit.X7, vmClosureOffProto)
	asm.LoadImm64(jit.X8, int64(uintptr(unsafe.Pointer(callee))))
	asm.CMPreg(jit.X7, jit.X8)
	asm.BCond(jit.CondNE, fallbackLabel)
	asm.LDR(jit.X16, jit.X7, funcProtoOffTier2TypedEntryPtr)
	asm.CBZ(jit.X16, fallbackLabel)

	asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X8, maxNativeCallDepth)
	asm.BCond(jit.CondGE, fallbackLabel)

	calleeBaseOff := ec.nextSlot * jit.ValueSize
	asm.LDR(jit.X8, jit.X7, funcProtoOffMaxStack)
	asm.LSLimm(jit.X8, jit.X8, 3)
	if calleeBaseOff <= 4095 {
		asm.ADDimm(jit.X8, jit.X8, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X9, int64(calleeBaseOff))
		asm.ADDreg(jit.X8, jit.X8, jit.X9)
	}
	asm.ADDreg(jit.X8, jit.X8, mRegRegs)
	asm.LDR(jit.X9, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondHI, fallbackLabel)

	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X8)
	}
	asm.LDR(mRegConsts, jit.X7, funcProtoOffConstants)
	asm.MOVimm16(jit.X8, callModeTypedSelf)
	ec.emitStoreCallMode(jit.X8)
	asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)

	asm.BLR(jit.X16)

	asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X8, jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.CBNZ(jit.X16, exitLabel)

	ec.emitRestoreTypedPeerCallerState()
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitReloadTypedPeerLiveForSuccess(liveFPRs)
	ec.restoreValueReprSnapshot(preReprs)
	switch desc.ReturnRep {
	case SpecializedABIReturnRawInt:
		ec.storeRawInt(jit.X0, instr.ID)
	case SpecializedABIReturnRawFloat:
		asm.FMOVtoFP(jit.D0, jit.X0)
		ec.storeRawFloat(jit.D0, instr.ID)
	case SpecializedABIReturnRawTablePtr:
		emitBoxTablePtr(asm, jit.X0, jit.X0, jit.X1)
		ec.storeResultNB(jit.X0, instr.ID)
	}
	postReprs := ec.snapshotValueReprs()
	asm.B(doneLabel)

	asm.Label(exitLabel)
	ec.emitPushNativeCallExitFrameIfNested(jit.X8, jit.X9, jit.X10, jit.X11)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitCode)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeExitCode)
	asm.LDR(jit.X8, mRegCtx, execCtxOffResumeNumericPass)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePass)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitResumePC)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePC)
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.MOVimm16(jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeTier2Only)
	ec.emitRestoreTypedPeerCallerState()
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitSpillSelectiveForCall(liveGPRs, nil)
	ec.emitMaterializeTypedPeerCallFrame(funcSlot, nArgs, desc)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitNativeCallExit(instr, funcSlot, nArgs, nRets, calleeBaseOff)
	ec.emitUnboxRawIntRegs(postReprs)
	ec.restoreValueReprSnapshot(postReprs)
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	ec.emitRestoreTypedPeerCallerState()
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeTypedPeerCallFrame(funcSlot, nArgs, desc)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postReprs)
	ec.restoreValueReprSnapshot(postReprs)

	asm.Label(doneLabel)
	return true
}

type fieldShapeTypedPeerCallCase struct {
	shapeID       int
	fieldIdx      int
	callee        *vm.FuncProto
	exactClosure  uintptr
	shapeEpochPtr uintptr
	shapeEpoch    uint64
	desc          CallABIDescriptor
}

func (ec *emitContext) fieldShapeTypedPeerCallCases(instr *Instr) []fieldShapeTypedPeerCallCase {
	if ec == nil || ec.fn == nil || instr == nil || (instr.Op != OpCall && instr.Op != OpCallFloor) || len(instr.Args) < 2 {
		return nil
	}
	calleeLoad := instr.Args[0].Def
	if calleeLoad == nil || calleeLoad.Op != OpGetField || len(calleeLoad.Args) == 0 || calleeLoad.Args[0] == nil {
		return nil
	}
	receiver := calleeLoad.Args[0]
	if instr.Args[1] == nil || instr.Args[1].ID != receiver.ID {
		return nil
	}
	nArgs := len(instr.Args) - 1
	if callResultCountFromAux2(instr.Aux2) != 1 || nArgs < 1 || nArgs > 4 {
		return nil
	}
	cases := ec.fn.FieldPolyShapeFacts[calleeLoad.ID]
	if len(cases) < 2 {
		return nil
	}
	out := make([]fieldShapeTypedPeerCallCase, 0, len(cases))
	var paramReps []SpecializedABIParamRep
	for _, c := range cases {
		if c.ShapeID == 0 || c.FieldIdx < 0 || c.VMProto == nil || c.VMProto.NumParams != nArgs {
			return nil
		}
		argFacts := map[int]FixedShapeTableFact{0: c.ReceiverFact}
		abi := AnalyzeTypedPeerABIWithArgFacts(c.VMProto, argFacts)
		if !abi.Eligible || len(abi.Params) != nArgs || abi.Params[0] != SpecializedABIParamRawTablePtr {
			return nil
		}
		switch abi.Return {
		case SpecializedABIReturnRawInt, SpecializedABIReturnRawFloat, SpecializedABIReturnRawTablePtr:
		default:
			return nil
		}
		if len(paramReps) == 0 {
			paramReps = append([]SpecializedABIParamRep(nil), abi.Params...)
		} else {
			for i, rep := range abi.Params {
				if paramReps[i] != rep {
					return nil
				}
			}
		}
		for i, rep := range abi.Params {
			switch rep {
			case SpecializedABIParamRawInt:
				if !callABIValueIsInt(instr.Args[1+i]) {
					return nil
				}
			case SpecializedABIParamRawTablePtr:
				if !callABIValueIsTable(instr.Args[1+i]) && i != 0 {
					return nil
				}
			default:
				return nil
			}
		}
		desc := CallABIDescriptor{
			Callee:    c.VMProto,
			NumArgs:   nArgs,
			NumRets:   1,
			TypedPeer: true,
			ParamReps: append([]SpecializedABIParamRep(nil), abi.Params...),
			ReturnRep: abi.Return,
			ArgFacts:  argFacts,
		}
		out = append(out, fieldShapeTypedPeerCallCase{
			shapeID:       int(c.ShapeID),
			fieldIdx:      c.FieldIdx,
			callee:        c.VMProto,
			exactClosure:  c.VMClosure,
			shapeEpochPtr: uintptr(gruntime.ShapeFieldMutationCountPtr(c.ShapeID, c.FieldIdx)),
			shapeEpoch:    gruntime.ShapeFieldMutationCount(c.ShapeID, c.FieldIdx),
			desc:          desc,
		})
	}
	return out
}

func (ec *emitContext) fieldShapeTypedPeerMethodCallCases(instr *Instr) []fieldShapeTypedPeerCallCase {
	if ec == nil || ec.fn == nil || instr == nil || instr.Op != OpFieldCallFloor || len(instr.Args) < 1 {
		return nil
	}
	nArgs := len(instr.Args)
	if callResultCountFromAux2(instr.Aux2) != 1 || nArgs < 1 || nArgs > 4 {
		return nil
	}
	cases := ec.fn.FieldPolyShapeFacts[instr.ID]
	if len(cases) < 2 {
		return nil
	}
	out := make([]fieldShapeTypedPeerCallCase, 0, len(cases))
	var paramReps []SpecializedABIParamRep
	for _, c := range cases {
		if c.ShapeID == 0 || c.FieldIdx < 0 || c.VMProto == nil || c.VMProto.NumParams != nArgs {
			return nil
		}
		argFacts := map[int]FixedShapeTableFact{0: c.ReceiverFact}
		abi := AnalyzeTypedPeerABIWithArgFacts(c.VMProto, argFacts)
		if !abi.Eligible || len(abi.Params) != nArgs || abi.Params[0] != SpecializedABIParamRawTablePtr {
			return nil
		}
		switch abi.Return {
		case SpecializedABIReturnRawInt, SpecializedABIReturnRawFloat:
		default:
			return nil
		}
		if len(paramReps) == 0 {
			paramReps = append([]SpecializedABIParamRep(nil), abi.Params...)
		} else {
			for i, rep := range abi.Params {
				if paramReps[i] != rep {
					return nil
				}
			}
		}
		for i, rep := range abi.Params {
			switch rep {
			case SpecializedABIParamRawInt:
				if !callABIValueIsInt(instr.Args[i]) {
					return nil
				}
			case SpecializedABIParamRawTablePtr:
				if i != 0 && !callABIValueIsTable(instr.Args[i]) {
					return nil
				}
			default:
				return nil
			}
		}
		out = append(out, fieldShapeTypedPeerCallCase{
			shapeID:       int(c.ShapeID),
			fieldIdx:      c.FieldIdx,
			callee:        c.VMProto,
			exactClosure:  c.VMClosure,
			shapeEpochPtr: uintptr(gruntime.ShapeFieldMutationCountPtr(c.ShapeID, c.FieldIdx)),
			shapeEpoch:    gruntime.ShapeFieldMutationCount(c.ShapeID, c.FieldIdx),
			desc: CallABIDescriptor{
				Callee:    c.VMProto,
				NumArgs:   nArgs,
				NumRets:   1,
				TypedPeer: true,
				ParamReps: append([]SpecializedABIParamRep(nil), abi.Params...),
				ReturnRep: abi.Return,
				ArgFacts:  argFacts,
			},
		})
	}
	return out
}

func fieldShapeTypedPeerCasesAllLeaf(cases []fieldShapeTypedPeerCallCase) bool {
	if len(cases) == 0 {
		return false
	}
	for _, c := range cases {
		if c.callee == nil || !c.callee.LeafNoCall {
			return false
		}
	}
	return true
}

func (ec *emitContext) emitCallNativeFieldShapeTypedPeerIfEligible(instr *Instr) bool {
	cases := ec.fieldShapeTypedPeerCallCases(instr)
	if len(cases) < 2 {
		return false
	}
	nArgs := len(instr.Args) - 1
	nRets := callResultCountFromAux2(instr.Aux2)
	funcSlot := int(instr.Aux)
	asm := ec.asm

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillTypedPeerLiveForSuccess(liveFPRs)

	fallbackLabel := ec.uniqueLabel("t2fieldpeer_fallback")
	exitLabel := ec.uniqueLabel("t2fieldpeer_exit")
	doneLabel := ec.uniqueLabel("t2fieldpeer_done")
	preReprs := ec.snapshotValueReprs()
	calleeBaseOff := ec.nextSlot * jit.ValueSize
	argDesc := cases[0].desc
	argDesc.ArgFacts = nil
	allLeafCallees := fieldShapeTypedPeerCasesAllLeaf(cases)

	asm.SUBimm(jit.SP, jit.SP, rawPeerFrameSize)
	if !allLeafCallees {
		asm.STR(mRegRegs, jit.SP, rawPeerRegsOff)
		asm.STR(mRegConsts, jit.SP, rawPeerConstsOff)
	}
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, jit.SP, rawPeerClosureOff)
	ec.emitLoadCallMode(jit.X8)
	asm.STR(jit.X8, jit.SP, rawPeerCallModeOff)

	fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X6)
	if fnReg != jit.X6 {
		asm.MOVreg(jit.X6, fnReg)
	}
	asm.STR(jit.X6, jit.SP, rawPeerFuncOff)
	ec.emitTypedPeerArgsInRegsAndSave(instr, argDesc, fallbackLabel)

	asm.LDRW(jit.X9, jit.X0, jit.TableOffShapeID)
	for _, c := range cases {
		nextLabel := ec.uniqueLabel("t2fieldpeer_next")
		asm.LoadImm64(jit.X12, int64(c.shapeID))
		asm.CMPreg(jit.X9, jit.X12)
		asm.BCond(jit.CondNE, nextLabel)
		asm.LDR(jit.X6, jit.SP, rawPeerFuncOff)
		if c.exactClosure != 0 {
			asm.LoadImm64(jit.X8, nbClosureTagBits|int64(c.exactClosure))
			asm.CMPreg(jit.X6, jit.X8)
			asm.BCond(jit.CondNE, fallbackLabel)
			asm.LoadImm64(jit.X7, int64(c.exactClosure))
			asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
			asm.LoadImm64(jit.X7, int64(uintptr(unsafe.Pointer(c.callee))))
		} else {
			asm.LSRimm(jit.X7, jit.X6, uint8(nbPtrSubShift))
			asm.LoadImm64(jit.X8, int64((jit.NB_TagPtrShr48<<4)|nbPtrSubVMClosure))
			asm.CMPreg(jit.X7, jit.X8)
			asm.BCond(jit.CondNE, fallbackLabel)
			jit.EmitExtractPtr(asm, jit.X7, jit.X6)
			asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
			asm.LDR(jit.X7, jit.X7, vmClosureOffProto)
			asm.LoadImm64(jit.X8, int64(uintptr(unsafe.Pointer(c.callee))))
			asm.CMPreg(jit.X7, jit.X8)
			asm.BCond(jit.CondNE, fallbackLabel)
		}
		asm.LDR(jit.X16, jit.X7, funcProtoOffTier2TypedEntryPtr)
		asm.CBZ(jit.X16, fallbackLabel)

		if !c.callee.LeafNoCall {
			asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
			asm.CMPimm(jit.X8, maxNativeCallDepth)
			asm.BCond(jit.CondGE, fallbackLabel)
		}

		asm.LoadImm64(jit.X8, int64(c.callee.MaxStack*jit.ValueSize))
		if calleeBaseOff <= 4095 {
			asm.ADDimm(jit.X8, jit.X8, uint16(calleeBaseOff))
		} else {
			asm.LoadImm64(jit.X12, int64(calleeBaseOff))
			asm.ADDreg(jit.X8, jit.X8, jit.X12)
		}
		asm.ADDreg(jit.X8, jit.X8, mRegRegs)
		asm.LDR(jit.X12, mRegCtx, execCtxOffRegsEnd)
		asm.CMPreg(jit.X8, jit.X12)
		asm.BCond(jit.CondHI, fallbackLabel)

		if calleeBaseOff <= 4095 {
			asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
		} else {
			asm.LoadImm64(jit.X8, int64(calleeBaseOff))
			asm.ADDreg(mRegRegs, mRegRegs, jit.X8)
		}
		asm.LDR(mRegConsts, jit.X7, funcProtoOffConstants)
		asm.MOVimm16(jit.X8, callModeTypedSelf)
		ec.emitStoreCallMode(jit.X8)
		if !c.callee.LeafNoCall {
			asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
			asm.ADDimm(jit.X8, jit.X8, 1)
			asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		}

		asm.BLR(jit.X16)

		if !c.callee.LeafNoCall {
			asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
			asm.SUBimm(jit.X8, jit.X8, 1)
			asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		}
		asm.CBNZ(jit.X16, exitLabel)

		if c.callee.LeafNoCall {
			ec.emitRestoreTypedPeerLeafCallerState(calleeBaseOff)
		} else {
			ec.emitRestoreTypedPeerCallerState()
		}
		asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
		ec.emitReloadTypedPeerLiveForSuccess(liveFPRs)
		ec.restoreValueReprSnapshot(preReprs)
		switch c.desc.ReturnRep {
		case SpecializedABIReturnRawInt:
			jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		case SpecializedABIReturnRawFloat:
		case SpecializedABIReturnRawTablePtr:
			emitBoxTablePtr(asm, jit.X0, jit.X0, jit.X1)
		}
		ec.storeResultNB(jit.X0, instr.ID)
		asm.B(doneLabel)
		asm.Label(nextLabel)
	}
	postReprs := ec.snapshotValueReprs()
	asm.B(fallbackLabel)

	asm.Label(exitLabel)
	ec.emitPushNativeCallExitFrameIfNested(jit.X8, jit.X9, jit.X10, jit.X11)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitCode)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeExitCode)
	asm.LDR(jit.X8, mRegCtx, execCtxOffResumeNumericPass)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePass)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitResumePC)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePC)
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.MOVimm16(jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeTier2Only)
	if allLeafCallees {
		ec.emitRestoreTypedPeerLeafCallerState(calleeBaseOff)
	} else {
		ec.emitRestoreTypedPeerCallerState()
	}
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitSpillSelectiveForCall(liveGPRs, nil)
	ec.emitMaterializeTypedPeerCallFrame(funcSlot, nArgs, argDesc)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitNativeCallExit(instr, funcSlot, nArgs, nRets, calleeBaseOff)
	ec.emitUnboxRawIntRegs(postReprs)
	ec.restoreValueReprSnapshot(postReprs)
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	if allLeafCallees {
		ec.emitRestoreTypedPeerCallerModeClosureOnly()
	} else {
		ec.emitRestoreTypedPeerCallerState()
	}
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeTypedPeerCallFrame(funcSlot, nArgs, argDesc)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postReprs)
	ec.restoreValueReprSnapshot(postReprs)

	asm.Label(doneLabel)
	return true
}

func (ec *emitContext) emitOpCallFloor(instr *Instr) {
	ec.emitOpCall(instr)
	ec.emitFloorProjectionFromCallResult(instr)
}

func (ec *emitContext) emitOpFieldCallFloor(instr *Instr) {
	if !ec.emitFieldShapeMethodCallFloorNative(instr) {
		ec.emitDeopt(instr)
	}
}

func (ec *emitContext) emitFieldShapeMethodCallFloorNative(instr *Instr) bool {
	cases := ec.fieldShapeTypedPeerMethodCallCases(instr)
	if len(cases) < 2 {
		return false
	}
	nArgs := len(instr.Args)
	nRets := callResultCountFromAux2(instr.Aux2)
	funcSlot := int(instr.Aux)
	asm := ec.asm

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	ec.emitSpillTypedPeerLiveForSuccess(liveFPRs)

	fallbackLabel := ec.uniqueLabel("t2fieldmethod_fallback")
	callFallbackLabel := ec.uniqueLabel("t2fieldmethod_call_fallback")
	exitLabel := ec.uniqueLabel("t2fieldmethod_exit")
	doneLabel := ec.uniqueLabel("t2fieldmethod_done")
	preReprs := ec.snapshotValueReprs()
	calleeBaseOff := ec.nextSlot * jit.ValueSize
	argDesc := cases[0].desc
	argDesc.ArgFacts = nil
	allLeafCallees := fieldShapeTypedPeerCasesAllLeaf(cases)

	asm.SUBimm(jit.SP, jit.SP, rawPeerFrameSize)
	if !allLeafCallees {
		asm.STR(mRegRegs, jit.SP, rawPeerRegsOff)
		asm.STR(mRegConsts, jit.SP, rawPeerConstsOff)
	}
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, jit.SP, rawPeerClosureOff)
	ec.emitLoadCallMode(jit.X8)
	asm.STR(jit.X8, jit.SP, rawPeerCallModeOff)

	ec.emitTypedPeerArgsFromValuesInRegsAndSave(instr.Args, argDesc, fallbackLabel)
	asm.LDRW(jit.X9, jit.X0, jit.TableOffShapeID)
	for _, c := range cases {
		nextLabel := ec.uniqueLabel("t2fieldmethod_next")
		asm.LoadImm64(jit.X12, int64(c.shapeID))
		asm.CMPreg(jit.X9, jit.X12)
		asm.BCond(jit.CondNE, nextLabel)

		validateMethodLabel := ec.uniqueLabel("t2fieldmethod_validate")
		if c.exactClosure != 0 && c.shapeEpochPtr != 0 {
			asm.LoadImm64(jit.X8, int64(c.shapeEpochPtr))
			asm.LDR(jit.X8, jit.X8, 0)
			asm.LoadImm64(jit.X12, int64(c.shapeEpoch))
			asm.CMPreg(jit.X8, jit.X12)
			asm.BCond(jit.CondNE, validateMethodLabel)
			asm.LoadImm64(jit.X6, nbClosureTagBits|int64(c.exactClosure))
			asm.LoadImm64(jit.X7, int64(c.exactClosure))
			asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
			asm.LoadImm64(jit.X7, int64(uintptr(unsafe.Pointer(c.callee))))
			asm.B(validateMethodLabel + "_entry")
		}

		asm.Label(validateMethodLabel)
		asm.LDR(jit.X6, jit.X0, jit.TableOffSvals)
		asm.LDR(jit.X6, jit.X6, c.fieldIdx*jit.ValueSize)
		if c.exactClosure != 0 {
			asm.LoadImm64(jit.X8, nbClosureTagBits|int64(c.exactClosure))
			asm.CMPreg(jit.X6, jit.X8)
			asm.BCond(jit.CondNE, callFallbackLabel)
			asm.LoadImm64(jit.X7, int64(c.exactClosure))
			asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
			asm.LoadImm64(jit.X7, int64(uintptr(unsafe.Pointer(c.callee))))
		} else {
			asm.LSRimm(jit.X7, jit.X6, uint8(nbPtrSubShift))
			asm.LoadImm64(jit.X8, int64((jit.NB_TagPtrShr48<<4)|nbPtrSubVMClosure))
			asm.CMPreg(jit.X7, jit.X8)
			asm.BCond(jit.CondNE, callFallbackLabel)
			jit.EmitExtractPtr(asm, jit.X7, jit.X6)
			asm.STR(jit.X7, mRegCtx, execCtxOffBaselineClosurePtr)
			asm.LDR(jit.X7, jit.X7, vmClosureOffProto)
			asm.LoadImm64(jit.X8, int64(uintptr(unsafe.Pointer(c.callee))))
			asm.CMPreg(jit.X7, jit.X8)
			asm.BCond(jit.CondNE, callFallbackLabel)
		}
		asm.Label(validateMethodLabel + "_entry")
		asm.LDR(jit.X16, jit.X7, funcProtoOffTier2TypedEntryPtr)
		asm.CBZ(jit.X16, callFallbackLabel)

		if !c.callee.LeafNoCall {
			asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
			asm.CMPimm(jit.X8, maxNativeCallDepth)
			asm.BCond(jit.CondGE, callFallbackLabel)
		}

		asm.LoadImm64(jit.X8, int64(c.callee.MaxStack*jit.ValueSize))
		if calleeBaseOff <= 4095 {
			asm.ADDimm(jit.X8, jit.X8, uint16(calleeBaseOff))
		} else {
			asm.LoadImm64(jit.X12, int64(calleeBaseOff))
			asm.ADDreg(jit.X8, jit.X8, jit.X12)
		}
		asm.ADDreg(jit.X8, jit.X8, mRegRegs)
		asm.LDR(jit.X12, mRegCtx, execCtxOffRegsEnd)
		asm.CMPreg(jit.X8, jit.X12)
		asm.BCond(jit.CondHI, callFallbackLabel)

		if calleeBaseOff <= 4095 {
			asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
		} else {
			asm.LoadImm64(jit.X8, int64(calleeBaseOff))
			asm.ADDreg(mRegRegs, mRegRegs, jit.X8)
		}
		asm.LDR(mRegConsts, jit.X7, funcProtoOffConstants)
		asm.MOVimm16(jit.X8, callModeTypedSelf)
		ec.emitStoreCallMode(jit.X8)
		if !c.callee.LeafNoCall {
			asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
			asm.ADDimm(jit.X8, jit.X8, 1)
			asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		}

		asm.BLR(jit.X16)

		if !c.callee.LeafNoCall {
			asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
			asm.SUBimm(jit.X8, jit.X8, 1)
			asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
		}
		asm.CBNZ(jit.X16, exitLabel)

		if c.callee.LeafNoCall {
			ec.emitRestoreTypedPeerLeafCallerState(calleeBaseOff)
		} else {
			ec.emitRestoreTypedPeerCallerState()
		}
		asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
		ec.emitReloadTypedPeerLiveForSuccess(liveFPRs)
		ec.restoreValueReprSnapshot(preReprs)
		switch c.desc.ReturnRep {
		case SpecializedABIReturnRawInt:
			ec.storeRawInt(jit.X0, instr.ID)
		case SpecializedABIReturnRawFloat:
			asm.FMOVtoFP(jit.D0, jit.X0)
			asm.FRINTMd(jit.D0, jit.D0)
			asm.FCVTZS(jit.X0, jit.D0)
			ec.storeRawInt(jit.X0, instr.ID)
		default:
			asm.B(fallbackLabel)
		}
		asm.B(doneLabel)
		asm.Label(nextLabel)
	}
	postReprs := ec.snapshotValueReprs()
	asm.B(fallbackLabel)

	asm.Label(exitLabel)
	ec.emitPushNativeCallExitFrameIfNested(jit.X8, jit.X9, jit.X10, jit.X11)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitCode)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeExitCode)
	asm.LDR(jit.X8, mRegCtx, execCtxOffResumeNumericPass)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePass)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitResumePC)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePC)
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.UBFX(jit.X6, jit.X8, 0, 44)
	asm.LoadImm64(jit.X12, nbClosureTagBits)
	asm.ORRreg(jit.X6, jit.X6, jit.X12)
	asm.STR(jit.X6, jit.SP, rawPeerFuncOff)
	asm.MOVimm16(jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeTier2Only)
	if allLeafCallees {
		ec.emitRestoreTypedPeerLeafCallerState(calleeBaseOff)
	} else {
		ec.emitRestoreTypedPeerCallerState()
	}
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitSpillSelectiveForCall(liveGPRs, nil)
	ec.emitMaterializeTypedPeerCallFrame(funcSlot, nArgs, argDesc)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitNativeCallExit(instr, funcSlot, nArgs, nRets, calleeBaseOff)
	ec.emitFloorProjectionFromCallResult(instr)
	ec.emitUnboxRawIntRegs(postReprs)
	ec.restoreValueReprSnapshot(postReprs)
	asm.B(doneLabel)

	asm.Label(callFallbackLabel)
	asm.STR(jit.X6, jit.SP, rawPeerFuncOff)
	if allLeafCallees {
		ec.emitRestoreTypedPeerCallerModeClosureOnly()
	} else {
		ec.emitRestoreTypedPeerCallerState()
	}
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeTypedPeerCallFrame(funcSlot, nArgs, argDesc)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitFloorProjectionFromCallResult(instr)
	ec.emitUnboxRawIntRegs(postReprs)
	ec.restoreValueReprSnapshot(postReprs)
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	if allLeafCallees {
		ec.emitRestoreTypedPeerCallerModeClosureOnly()
	} else {
		ec.emitRestoreTypedPeerCallerState()
	}
	ec.restoreValueReprSnapshot(preReprs)
	asm.ADDimm(jit.SP, jit.SP, rawPeerFrameSize)
	ec.emitDeopt(instr)

	asm.Label(doneLabel)
	return true
}

func (ec *emitContext) emitFloorProjectionFromCallResult(instr *Instr) {
	if instr == nil {
		return
	}
	asm := ec.asm
	valueID := instr.ID

	// OpCallFloor reuses the call's SSA id for the projected floor result.
	// Snapshot the current post-call representation before storing the raw int
	// back to the same id; routing this through the ordinary OpFloor protocol
	// would make the input and output self-referential.
	if ec.hasReg(valueID) && ec.valueReprOf(valueID) == valueReprRawInt {
		src := ec.physReg(valueID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		ec.storeRawInt(jit.X0, valueID)
		return
	}
	if ec.hasFPReg(valueID) {
		asm.FRINTMd(jit.D0, ec.physFPReg(valueID))
		asm.FCVTZS(jit.X0, jit.D0)
		ec.storeRawInt(jit.X0, valueID)
		return
	}

	srcReg := ec.resolveValueNB(valueID, jit.X0)
	if srcReg != jit.X0 {
		asm.MOVreg(jit.X0, srcReg)
	}

	floatLabel := ec.uniqueLabel("call_floor_float")
	deoptLabel := ec.uniqueLabel("call_floor_deopt")
	doneLabel := ec.uniqueLabel("call_floor_done")

	emitCheckIsInt(asm, jit.X0, jit.X2)
	asm.BCond(jit.CondNE, floatLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	ec.storeRawInt(jit.X0, valueID)
	asm.B(doneLabel)

	asm.Label(floatLabel)
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagNilShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondGE, deoptLabel)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FRINTMd(jit.D0, jit.D0)
	asm.FCVTZS(jit.X0, jit.D0)
	ec.storeRawInt(jit.X0, valueID)
	asm.B(doneLabel)

	asm.Label(deoptLabel)
	ec.emitDeopt(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitCallNativeTypedSelfIfEligible(instr *Instr) bool {
	if !ec.isTypedStaticSelfCall(instr) {
		return false
	}

	asm := ec.asm
	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := callResultCountFromAux2(instr.Aux2)
	abi := ec.typedSelfABI
	wantRets := 1
	if abi.Return == SpecializedABIReturnNone {
		wantRets = 0
	}
	if nRets != wantRets || nArgs != abi.NumParams || len(abi.Params) != nArgs {
		return false
	}

	liveGPRs, liveFPRs := ec.computeLiveAcrossCall(instr)
	preReprs := ec.snapshotValueReprs()
	ec.emitSpillSelectiveForCall(liveGPRs, liveFPRs)

	exitHandleLabel := ec.uniqueLabel("t2typedself_exit")
	fallbackLabel := ec.uniqueLabel("t2typedself_fallback")
	doneLabel := ec.uniqueLabel("t2typedself_done")
	frameSize := typedSelfFrameSizeFor(nArgs)

	// Keep only the data needed to reconstruct the public VM call frame on
	// fallback/exit. The success path passes typed args in X0..X3 and avoids
	// writing regs[funcSlot..] before the recursive BL.
	asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
	ec.emitLoadCallMode(jit.X8)
	asm.STR(jit.X8, jit.SP, typedSelfSavedCallModeOff)
	ec.emitTypedSelfArgsInRegsAndSave(instr, abi, fallbackLabel)

	calleeBaseOff := ec.nextSlot * jit.ValueSize
	calleeFrameBytes := ec.nextSlot * jit.ValueSize
	if calleeBaseOff+calleeFrameBytes <= 4095 {
		asm.ADDimm(jit.X8, mRegRegs, uint16(calleeBaseOff+calleeFrameBytes))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff+calleeFrameBytes))
		asm.ADDreg(jit.X8, mRegRegs, jit.X8)
	}
	asm.LDR(jit.X9, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X8, jit.X9)
	asm.BCond(jit.CondHI, fallbackLabel)

	asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.CMPimm(jit.X8, maxNativeCallDepth)
	asm.BCond(jit.CondGE, fallbackLabel)

	asm.MOVimm16(jit.X8, callModeTypedSelf)
	ec.emitStoreCallMode(jit.X8)

	if calleeBaseOff <= 4095 {
		asm.ADDimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.ADDreg(mRegRegs, mRegRegs, jit.X8)
	}

	asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X8, jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)

	asm.BL("t2_typed_self_entry")

	asm.LDR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)
	asm.SUBimm(jit.X8, jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCallDepth)

	if calleeBaseOff <= 4095 {
		asm.SUBimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.SUBreg(mRegRegs, mRegRegs, jit.X8)
	}
	asm.LDR(jit.X8, jit.SP, typedSelfSavedCallModeOff)
	ec.emitStoreCallMode(jit.X8)

	asm.LDR(jit.X8, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X8, exitHandleLabel)

	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(preReprs)
	ec.restoreValueReprSnapshot(preReprs)
	switch abi.Return {
	case SpecializedABIReturnNone:
		// CALL C=1: recursive side effects are complete and no result slot is
		// produced or consumed.
	case SpecializedABIReturnRawInt:
		ec.storeRawInt(jit.X0, instr.ID)
	case SpecializedABIReturnRawFloat:
		asm.FMOVtoFP(jit.D0, jit.X0)
		ec.storeRawFloat(jit.D0, instr.ID)
	case SpecializedABIReturnRawTablePtr:
		emitBoxTablePtr(asm, jit.X0, jit.X0, jit.X1)
		ec.storeResultNB(jit.X0, instr.ID)
	default:
		asm.B(fallbackLabel)
	}
	postSuccessReprs := ec.snapshotValueReprs()
	asm.B(doneLabel)

	asm.Label(exitHandleLabel)
	ec.emitPushNativeCallExitFrameIfNested(jit.X8, jit.X9, jit.X10, jit.X11)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitCode)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeExitCode)
	asm.LDR(jit.X8, mRegCtx, execCtxOffResumeNumericPass)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePass)
	asm.LDR(jit.X8, mRegCtx, execCtxOffExitResumePC)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeResumePC)
	asm.LDR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeClosurePtr)
	asm.MOVimm16(jit.X8, 1)
	asm.STR(jit.X8, mRegCtx, execCtxOffNativeCalleeTier2Only)
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeTypedSelfCallFrameFromStack(funcSlot, nArgs, abi)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	ec.emitNativeCallExit(instr, funcSlot, nArgs, nRets, calleeBaseOff)

	asm.Label(fallbackLabel)
	ec.restoreValueReprSnapshot(preReprs)
	ec.emitMaterializeTypedSelfCallFrameFromStack(funcSlot, nArgs, abi)
	asm.ADDimm(jit.SP, jit.SP, uint16(frameSize))
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)
	ec.emitUnboxRawIntRegs(postSuccessReprs)
	ec.restoreValueReprSnapshot(postSuccessReprs)

	asm.Label(doneLabel)
	return true
}

func (ec *emitContext) emitPushNativeCallExitFrameIfNested(tmpExit, tmpDepth, tmpFrame, tmpVal jit.Reg) {
	asm := ec.asm
	doneLabel := ec.uniqueLabel("native_exit_stack_done")
	overflowLabel := ec.uniqueLabel("native_exit_stack_overflow")

	asm.LDR(tmpExit, mRegCtx, execCtxOffExitCode)
	asm.CMPimm(tmpExit, ExitNativeCallExit)
	asm.BCond(jit.CondNE, doneLabel)

	asm.LDR(tmpDepth, mRegCtx, execCtxOffNativeCallExitStackDepth)
	asm.CMPimm(tmpDepth, maxNativeCallExitStackDepth)
	asm.BCond(jit.CondGE, overflowLabel)

	asm.LoadImm64(tmpFrame, int64(nativeCallExitFrameSize))
	asm.MUL(tmpFrame, tmpDepth, tmpFrame)
	if execCtxOffNativeCallExitStack <= 4095 {
		asm.ADDimm(tmpFrame, tmpFrame, uint16(execCtxOffNativeCallExitStack))
	} else {
		asm.LoadImm64(tmpVal, int64(execCtxOffNativeCallExitStack))
		asm.ADDreg(tmpFrame, tmpFrame, tmpVal)
	}
	asm.ADDreg(tmpFrame, mRegCtx, tmpFrame)

	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffCallSlot, nativeCallExitFrameOffCallSlot)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffCallNArgs, nativeCallExitFrameOffCallNArgs)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffCallNRets, nativeCallExitFrameOffCallNRets)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffCallID, nativeCallExitFrameOffCallID)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCallA, nativeCallExitFrameOffNativeCallA)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCallB, nativeCallExitFrameOffNativeCallB)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCallC, nativeCallExitFrameOffNativeCallC)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCalleeExitCode, nativeCallExitFrameOffNativeCalleeExitCode)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCalleeResumePass, nativeCallExitFrameOffNativeCalleeResumePass)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCalleeBaseOff, nativeCallExitFrameOffNativeCalleeBaseOff)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCalleeResumePC, nativeCallExitFrameOffNativeCalleeResumePC)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCalleeClosurePtr, nativeCallExitFrameOffNativeCalleeClosurePtr)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCalleeTier2Only, nativeCallExitFrameOffNativeCalleeTier2Only)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffNativeCallerClosurePtr, nativeCallExitFrameOffNativeCallerClosurePtr)
	ec.emitStoreNativeCallExitFrameField(tmpFrame, tmpVal, execCtxOffResumeNumericPass, nativeCallExitFrameOffResumeNumericPass)

	asm.ADDimm(tmpDepth, tmpDepth, 1)
	asm.STR(tmpDepth, mRegCtx, execCtxOffNativeCallExitStackDepth)
	asm.B(doneLabel)

	asm.Label(overflowLabel)
	asm.MOVimm16(tmpVal, 1)
	asm.STR(tmpVal, mRegCtx, execCtxOffNativeCallExitStackOverflow)

	asm.Label(doneLabel)
}

func (ec *emitContext) emitStoreNativeCallExitFrameField(frameReg, tmpReg jit.Reg, ctxOff, frameOff int) {
	ec.asm.LDR(tmpReg, mRegCtx, ctxOff)
	ec.asm.STR(tmpReg, frameReg, frameOff)
}

func (ec *emitContext) emitTypedSelfArgsInRegsAndSave(instr *Instr, abi TypedSelfABI, fallbackLabel string) {
	asm := ec.asm
	for i, rep := range abi.Params {
		dst := jit.Reg(int(jit.X0) + i)
		arg := instr.Args[1+i]
		switch rep {
		case SpecializedABIParamRawInt:
			src := ec.resolveRawInt(arg.ID, dst)
			if src != dst {
				asm.MOVreg(dst, src)
			}
			asm.STR(dst, jit.SP, typedSelfArgsOff+i*jit.ValueSize)
		case SpecializedABIParamRawTablePtr:
			src := ec.resolveValueNB(arg.ID, dst)
			if src != dst {
				asm.MOVreg(dst, src)
			}
			asm.STR(dst, jit.SP, typedSelfArgsOff+i*jit.ValueSize)
			if ec.irTypes[arg.ID] != TypeTable {
				jit.EmitCheckIsTableFull(asm, dst, jit.X6, jit.X7, fallbackLabel)
			}
			jit.EmitExtractPtr(asm, dst, dst)
			if fact, ok := ec.entryShapeGuards[i]; ok && fact.ShapeID != 0 {
				asm.LDRW(jit.X6, dst, jit.TableOffShapeID)
				asm.LoadImm64(jit.X7, int64(fact.ShapeID))
				asm.CMPreg(jit.X6, jit.X7)
				asm.BCond(jit.CondNE, fallbackLabel)
			}
		}
	}
}

func (ec *emitContext) emitMaterializeTypedSelfCallFrameFromStack(funcSlot, nArgs int, abi TypedSelfABI) {
	asm := ec.asm
	ec.emitBoxCurrentClosure(jit.X0, jit.X1)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	for i := 0; i < nArgs; i++ {
		asm.LDR(jit.X0, jit.SP, typedSelfArgsOff+i*jit.ValueSize)
		if i < len(abi.Params) && abi.Params[i] == SpecializedABIParamRawInt {
			jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+1+i))
	}
}

func rawIntPeerLeafCallee(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	// Leaf raw-int kernels cannot recursively grow the native BLR chain. They
	// still use one numeric entry frame, but repeated loop calls do not stack,
	// so the per-call NativeCallDepth load/store traffic is unnecessary.
	for _, inst := range proto.Code {
		if vm.DecodeOp(inst) == vm.OP_CALL {
			return false
		}
	}
	return true
}

func (ec *emitContext) staticNoDepthCallee(instr *Instr) *vm.FuncProto {
	if ec == nil || instr == nil || ec.fn == nil {
		return nil
	}
	if ec.tailCallInstrs[instr.ID] || ec.isStaticSelfCall(instr) {
		return nil
	}
	_, callee := resolveCallee(instr, ec.fn, InlineConfig{Globals: ec.fn.Globals})
	if callee == nil {
		if feedbackCallee, ok := callABIFeedbackCalleeProto(ec.fn, instr); ok {
			callee = feedbackCallee
		}
	}
	if !rawIntPeerLeafCallee(callee) {
		return nil
	}
	return callee
}

func (ec *emitContext) callCalleeFlagSpec(instr *Instr) callCalleeFlagSpec {
	protos := ec.callCalleeFeedbackProtos(instr)
	if len(protos) == 0 {
		return callCalleeFlagSpec{}
	}
	allLeaf := true
	allNoGlobal := true
	for _, proto := range protos {
		if proto == nil {
			return callCalleeFlagSpec{}
		}
		if !proto.LeafNoCall {
			allLeaf = false
		}
		if !proto.NoGlobalOps {
			allNoGlobal = false
		}
	}
	if !allLeaf && !allNoGlobal {
		return callCalleeFlagSpec{}
	}
	return callCalleeFlagSpec{
		protos:        protos,
		knownLeaf:     allLeaf,
		knownNoGlobal: allNoGlobal,
	}
}

func (ec *emitContext) callCalleeFeedbackProtos(instr *Instr) []*vm.FuncProto {
	if protos := ec.callCalleeFieldShapeProtos(instr); len(protos) > 0 {
		return protos
	}
	if ec == nil || ec.fn == nil || ec.fn.Proto == nil || instr == nil || instr.Op != OpCall ||
		!instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(ec.fn.Proto.CallSiteFeedback) {
		return nil
	}
	fb := ec.fn.Proto.CallSiteFeedback[instr.SourcePC]
	if fb.Count < wholeCallKernelMinStableObservations ||
		fb.Flags&vm.CallSiteArityPolymorphic != 0 ||
		int(fb.NArgs) != len(instr.Args)-1 ||
		fb.ResultArity != uint8(instr.Aux2) {
		return nil
	}
	if fb.Flags&vm.CallSiteCalleePolymorphic == 0 {
		if callee, ok := fb.StableCalleeVMProto(); ok && callee != nil {
			return []*vm.FuncProto{callee}
		}
		return nil
	}
	return fb.MaturePolymorphicVMProtos(wholeCallKernelMinStableObservations, len(instr.Args)-1, uint8(instr.Aux2))
}

func (ec *emitContext) callCalleeFieldShapeProtos(instr *Instr) []*vm.FuncProto {
	if ec == nil {
		return nil
	}
	return fieldShapeCalleeProtos(ec.fn, instr)
}

func (ec *emitContext) emitGuardCalleeProtoSet(protos []*vm.FuncProto, slowLabel string) {
	if ec == nil || len(protos) == 0 {
		return
	}
	asm := ec.asm
	okLabel := ec.uniqueLabel("t2call_feedback_proto_ok")
	for _, proto := range protos {
		if proto == nil {
			continue
		}
		asm.LoadImm64(jit.X3, int64(uintptr(unsafe.Pointer(proto))))
		asm.CMPreg(jit.X1, jit.X3)
		asm.BCond(jit.CondEQ, okLabel)
	}
	asm.B(slowLabel)
	asm.Label(okLabel)
}

func (ec *emitContext) recordCallCachePC(cacheIndex, pc int) {
	if ec == nil || cacheIndex < 0 {
		return
	}
	for len(ec.callCachePCs) <= cacheIndex {
		ec.callCachePCs = append(ec.callCachePCs, -1)
	}
	ec.callCachePCs[cacheIndex] = pc
}

func (ec *emitContext) rawIntPeerCallee(instr *Instr) *vm.FuncProto {
	if instr == nil || ec.fn == nil || instr.Type != TypeInt {
		return nil
	}
	if !ec.inLoopBlock() && !(ec.numericMode && ec.rawIntSelfABI.Eligible) {
		return nil
	}
	if ec.tailCallInstrs[instr.ID] || ec.isStaticSelfCall(instr) {
		return nil
	}
	if len(instr.Args) < 2 || ec.fn.CallABIs == nil {
		return nil
	}
	desc, ok := ec.fn.CallABIs[instr.ID]
	if !ok || desc.Callee == nil || !desc.RawIntReturn || desc.NumRets != 1 {
		return nil
	}
	callee := desc.Callee
	ok, numParams := qualifyForNumeric(callee)
	if !ok || desc.NumArgs != numParams || len(desc.RawIntParams) != numParams || len(instr.Args) != 1+numParams {
		return nil
	}
	for i := 0; i < numParams; i++ {
		if !desc.RawIntParams[i] {
			return nil
		}
		argID := instr.Args[1+i].ID
		if ec.hasReg(argID) && ec.valueReprOf(argID) == valueReprRawInt {
			continue
		}
		if ec.irTypes[argID] == TypeInt {
			continue
		}
		return nil
	}
	return callee
}

func (ec *emitContext) emitRestoreRawPeerCallerState() {
	asm := ec.asm
	asm.LDR(mRegRegs, jit.SP, rawPeerRegsOff)
	asm.LDR(mRegConsts, jit.SP, rawPeerConstsOff)
	asm.LDR(jit.X8, jit.SP, rawPeerClosureOff)
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.STR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
}

func (ec *emitContext) emitRestoreTypedPeerCallerState() {
	ec.emitRestoreRawPeerCallerState()
	ec.asm.LDR(jit.X8, jit.SP, rawPeerCallModeOff)
	ec.emitStoreCallMode(jit.X8)
}

func (ec *emitContext) emitSpillTypedPeerLiveForSuccess(liveFPRs map[int]bool) {
	// The published typed-peer callee entry uses the full native frame and
	// preserves allocatable GPRs. Success paths only need to protect live FPRs;
	// exit/fallback paths still materialize GPR live values before returning
	// through Go.
	ec.emitSpillSelectiveForCall(nil, liveFPRs)
}

func (ec *emitContext) emitReloadTypedPeerLiveForSuccess(liveFPRs map[int]bool) {
	ec.emitReloadSelectiveForCall(nil, liveFPRs)
}

func (ec *emitContext) emitRestoreTypedPeerCallerModeClosureOnly() {
	asm := ec.asm
	asm.LDR(jit.X8, jit.SP, rawPeerClosureOff)
	asm.STR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LDR(jit.X8, jit.SP, rawPeerCallModeOff)
	ec.emitStoreCallMode(jit.X8)
}

func (ec *emitContext) emitRestoreTypedPeerLeafCallerState(calleeBaseOff int) {
	asm := ec.asm
	ec.emitRestoreRawPeerLeafCallerRegs(calleeBaseOff)
	asm.LDR(jit.X8, jit.SP, rawPeerClosureOff)
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.STR(jit.X8, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LDR(jit.X8, jit.SP, rawPeerCallModeOff)
	ec.emitStoreCallMode(jit.X8)
}

func (ec *emitContext) emitRestoreRawPeerLeafCallerRegs(calleeBaseOff int) {
	asm := ec.asm
	if calleeBaseOff <= 4095 {
		asm.SUBimm(mRegRegs, mRegRegs, uint16(calleeBaseOff))
	} else {
		asm.LoadImm64(jit.X8, int64(calleeBaseOff))
		asm.SUBreg(mRegRegs, mRegRegs, jit.X8)
	}
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)
}

func (ec *emitContext) emitMaterializeRawIntPeerCallFrame(funcSlot, nArgs int) {
	asm := ec.asm
	asm.LDR(jit.X0, jit.SP, rawPeerFuncOff)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	for i := 0; i < nArgs; i++ {
		asm.LDR(jit.X0, jit.SP, rawPeerArgsOff+i*jit.ValueSize)
		jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+1+i))
	}
}

func (ec *emitContext) emitTypedPeerArgsInRegsAndSave(instr *Instr, desc CallABIDescriptor, fallbackLabel string) {
	if instr == nil || len(instr.Args) < 1 {
		ec.asm.B(fallbackLabel)
		return
	}
	ec.emitTypedPeerArgsFromValuesInRegsAndSave(instr.Args[1:], desc, fallbackLabel)
}

func (ec *emitContext) emitTypedPeerArgsFromValuesInRegsAndSave(args []*Value, desc CallABIDescriptor, fallbackLabel string) {
	asm := ec.asm
	for i, rep := range desc.ParamReps {
		if i >= len(args) || args[i] == nil {
			asm.B(fallbackLabel)
			return
		}
		dst := jit.Reg(int(jit.X0) + i)
		arg := args[i]
		switch rep {
		case SpecializedABIParamRawInt:
			src := ec.resolveRawInt(arg.ID, dst)
			if src != dst {
				asm.MOVreg(dst, src)
			}
			asm.STR(dst, jit.SP, rawPeerArgsOff+i*jit.ValueSize)
		case SpecializedABIParamRawTablePtr:
			src := ec.resolveValueNB(arg.ID, dst)
			if src != dst {
				asm.MOVreg(dst, src)
			}
			asm.STR(dst, jit.SP, rawPeerArgsOff+i*jit.ValueSize)
			if ec.irTypes[arg.ID] != TypeTable {
				jit.EmitCheckIsTableFull(asm, dst, jit.X6, jit.X7, fallbackLabel)
			}
			jit.EmitExtractPtr(asm, dst, dst)
			if fact, ok := desc.ArgFacts[i]; ok && fact.ShapeID != 0 {
				asm.LDRW(jit.X6, dst, jit.TableOffShapeID)
				asm.LoadImm64(jit.X7, int64(fact.ShapeID))
				asm.CMPreg(jit.X6, jit.X7)
				asm.BCond(jit.CondNE, fallbackLabel)
			}
		default:
			asm.B(fallbackLabel)
		}
	}
}

func (ec *emitContext) emitMaterializeTypedPeerCallFrame(funcSlot, nArgs int, desc CallABIDescriptor) {
	asm := ec.asm
	asm.LDR(jit.X0, jit.SP, rawPeerFuncOff)
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	for i := 0; i < nArgs; i++ {
		asm.LDR(jit.X0, jit.SP, rawPeerArgsOff+i*jit.ValueSize)
		if i < len(desc.ParamReps) && desc.ParamReps[i] == SpecializedABIParamRawInt {
			jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+1+i))
	}
}

func (ec *emitContext) emitRawIntPeerCallExitResume(instr *Instr, funcSlot, nArgs, nRets int, preReprs valueReprSnapshot, liveGPRs, liveFPRs map[int]bool) {
	asm := ec.asm

	ec.recordExitResumeCheckSiteWithLive(
		instr,
		ExitCallExit,
		ec.exitResumeCheckLiveSlots(liveGPRs, liveFPRs),
		callExitModifiedSlots(funcSlot, nRets),
		exitResumeCheckOptions{RequireCallFunc: true, RequireRawIntArgs: true},
	)

	ec.emitStoreCallExitDescriptor(callExitDescriptor{
		slot:    funcSlot,
		nArgs:   nArgs,
		nRets:   nRets,
		instrID: instr.ID,
	})
	ec.emitCallProtocolExitToGo(ExitCallExit)

	continueLabel := ec.passLabel(fmt.Sprintf("call_continue_%d", instr.ID))
	asm.Label(continueLabel)

	ec.emitReloadSelectiveForCall(liveGPRs, liveFPRs)
	ec.emitUnboxRawIntRegs(preReprs)
	ec.restoreValueReprSnapshot(preReprs)
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	resultIntLabel := ec.uniqueLabel("t2rawpeer_result_int")
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
	} else if ec.emitProtocolConstCallIfEligible(instr) {
	} else if ec.emitWholeCallKernelOpExitIfEligible(instr) {
	} else if ec.emitCallNativeRawIntPeerIfEligible(instr) {
	} else if ec.emitCallNativeFieldShapeTypedPeerIfEligible(instr) {
	} else if ec.emitCallNativeTypedPeerIfEligible(instr) {
	} else if ec.emitCallNativeTypedSelfIfEligible(instr) {
	} else if ec.isStaticSelfCall(instr) && !ec.tailCallInstrs[instr.ID] && callResultCountFromAux2(instr.Aux2) > 0 && !ec.nativeCallReplaySafe {
		ec.emitCallExit(instr)
	} else if ec.tailCallInstrs[instr.ID] && ec.isStaticSelfCall(instr) && !ec.hasEntryShapeGuards() {
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

func (ec *emitContext) invalidateCallClobberedFactsAfterResume() {
	ec.shapeVerified = make(map[int]uint32)
	ec.tableVerified = make(map[int]bool)
	ec.kindVerified = make(map[int]uint16)
	ec.keysDirtyWritten = make(map[int]bool)
	ec.dmVerified = make(map[int]bool)
	for valueID := range ec.activeRegs {
		if ec.valueReprOf(valueID) == valueReprRawDataPtr {
			ec.clearValueRepr(valueID)
		}
	}
}

func (ec *emitContext) emitCallNativeNumericTail(instr *Instr) {
	asm := ec.asm
	slowLabel := ec.uniqueLabel("t2numtail_slow")

	entryLabel, hasEntry := ec.entryBlockLabelOK()
	if len(instr.Args) == 0 || ec.fn == nil || ec.fn.Proto == nil || !hasEntry {
		asm.B(slowLabel)
	} else {
		ec.emitNumericArgsInRegs(instr, ec.fn.Proto.NumParams)
		asm.B(entryLabel)
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
	nRets := callResultCountFromAux2(instr.Aux2)

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
	ec.recordCallCachePC(icIdx, instr.SourcePC)
	cacheOff := icIdx * tier2CallCacheStrideBytes
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

	// Cache update on successful miss path.
	asm.LDR(jit.X4, mRegRegs, slotOffset(funcSlot))
	asm.STR(jit.X4, jit.X3, cacheOff)
	asm.STR(jit.X2, jit.X3, cacheOff+8)
	asm.STR(jit.X1, jit.X3, cacheOff+16)
	asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryVersion)
	asm.STR(jit.X4, jit.X3, cacheOff+24)
	asm.B(icDoneLabel)

	// --- IC Hit: validate direct-entry version, refreshing entry on change. ---
	asm.Label(icHitLabel)
	asm.LDR(jit.X2, jit.X3, cacheOff+8)
	asm.LDR(jit.X1, jit.X3, cacheOff+16)
	asm.LDR(jit.X4, jit.X3, cacheOff+24)
	asm.LDR(jit.X5, jit.X1, funcProtoOffDirectEntryVersion)
	tailICVersionOKLabel := ec.uniqueLabel("t2tail_ic_version_ok")
	asm.CMPreg(jit.X4, jit.X5)
	asm.BCond(jit.CondEQ, tailICVersionOKLabel)
	asm.LDR(jit.X4, jit.X1, funcProtoOffDirectEntryPtr)
	tailICHaveEntryLabel := ec.uniqueLabel("t2tail_ic_have_entry")
	asm.CBNZ(jit.X4, tailICHaveEntryLabel)
	asm.LDR(jit.X4, jit.X1, funcProtoOffTier2DirectEntryPtr)
	asm.CBZ(jit.X4, slowLabel)
	asm.Label(tailICHaveEntryLabel)
	asm.MOVreg(jit.X2, jit.X4)
	asm.STR(jit.X2, jit.X3, cacheOff+8)
	asm.STR(jit.X5, jit.X3, cacheOff+24)
	asm.Label(tailICVersionOKLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)

	asm.Label(icDoneLabel)

	// Bounds check: callee window (at the TAIL base = 0) fits in register file.
	asm.LDR(jit.X3, jit.X1, funcProtoOffMaxStack)
	asm.LSLimm(jit.X3, jit.X3, 3)
	asm.ADDreg(jit.X3, jit.X3, mRegRegs)
	asm.LDR(jit.X4, mRegCtx, execCtxOffRegsEnd)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondHI, slowLabel)

	// CallCount increment for tiering. A published Tier 2 entry means the
	// callee no longer needs hot tail calls to feed promotion counters.
	skipTailCallCountLabel := ec.uniqueLabel("t2tail_skip_callcount")
	asm.LDR(jit.X3, jit.X1, funcProtoOffTier2DirectEntryPtr)
	asm.CBNZ(jit.X3, skipTailCallCountLabel)
	asm.LDR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, jit.X1, funcProtoOffCallCount)
	asm.CMPimm(jit.X3, tmDefaultTier2Threshold)
	asm.BCond(jit.CondEQ, slowLabel)
	asm.Label(skipTailCallCountLabel)

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
	ec.emitStoreCallMode(jit.X3)
	asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalCachePtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCache)
	asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalCacheGenPtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalCacheGen)
	asm.LDR(jit.X3, jit.X1, funcProtoOffTier2GlobalIndexPtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffTier2GlobalIndex)
	asm.LDR(jit.X3, jit.X1, funcProtoOffFieldCache)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineFieldCache)
	asm.LDR(jit.X3, jit.X1, funcProtoOffFieldPolyCache)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineFieldPolyCache)
	asm.LDR(jit.X3, jit.X1, funcProtoOffTableStringKeyCache)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineTableStringKeyCache)
	// Persist the (unchanged) mRegRegs back to ctx.Regs so callee's
	// direct-entry reload sees the correct base.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)

	// Step 5: Inline our own epilogue — restore callee-saved regs, FP/LR,
	// deallocate frame. Do NOT emit RET; we'll BR to callee instead.
	// X2 (direct entry addr) must survive; none of the LDP writes touch X2.
	// The callee direct entry expects X0=ctx. Capture it before restoring
	// X19, whose saved value belongs to our caller rather than this frame.
	asm.MOVreg(jit.X0, mRegCtx)
	ec.emitRestoreCalleeSavedFPRs()
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
	abi := ec.rawIntSelfABI
	if !abi.Eligible {
		abi = AnalyzeRawIntSelfABI(ec.fn.Proto)
	}
	if !abi.Eligible {
		return false
	}
	numParams := abi.NumParams
	if len(instr.Args) != 1+numParams {
		return false
	}
	for i := 0; i < numParams; i++ {
		argID := instr.Args[1+i].ID
		if ec.hasReg(argID) && ec.valueReprOf(argID) == valueReprRawInt {
			continue
		}
		if ec.irTypes[argID] == TypeInt {
			continue
		}
		return false
	}
	return true
}

func (ec *emitContext) isTypedStaticSelfCall(instr *Instr) bool {
	if ec.numericMode || !ec.isStaticSelfCall(instr) {
		return false
	}
	abi := ec.typedSelfABI
	if !abi.Eligible {
		return false
	}
	if len(instr.Args) != 1+abi.NumParams || len(abi.Params) != abi.NumParams {
		return false
	}
	if ec.tailCallInstrs[instr.ID] {
		return false
	}
	for i, rep := range abi.Params {
		argID := instr.Args[1+i].ID
		switch rep {
		case SpecializedABIParamRawInt:
			if ec.hasReg(argID) && ec.valueReprOf(argID) == valueReprRawInt {
				continue
			}
			if ec.irTypes[argID] == TypeInt {
				continue
			}
			return false
		case SpecializedABIParamRawTablePtr:
			if ec.irTypes[argID] == TypeTable {
				continue
			}
			if (ec.irTypes[argID] == TypeAny || ec.irTypes[argID] == TypeUnknown) &&
				typedSelfCallArgSlotMatches(ec.fn.Proto, instr.SourcePC, i, SpecializedABIParamRawTablePtr) {
				continue
			}
			if ec.irTypes[argID] != TypeTable {
				return false
			}
		default:
			return false
		}
	}
	switch abi.Return {
	case SpecializedABIReturnNone:
		return callResultCountFromAux2(instr.Aux2) == 0
	case SpecializedABIReturnRawInt:
		return instr.Type == TypeInt
	case SpecializedABIReturnRawFloat:
		return instr.Type == TypeFloat
	case SpecializedABIReturnRawTablePtr:
		return instr.Type == TypeTable
	default:
		return false
	}
}

// emitNumericArgsInRegs (R124) materializes raw int64 args into X0..X(N-1)
// ahead of a BL t2_numeric_self_entry_N. Allocated SSA GPRs are X20-X23/X28,
// so using the destination ABI register as the load/unbox scratch cannot
// clobber another live raw argument source.
func (ec *emitContext) emitNumericArgsInRegs(instr *Instr, nParams int) {
	asm := ec.asm
	for i := 0; i < nParams; i++ {
		dst := jit.Reg(int(jit.X0) + i)
		src := ec.resolveRawInt(instr.Args[1+i].ID, dst)
		if src != dst {
			asm.MOVreg(dst, src)
		}
	}
}

// qualifyForNumeric reports whether a proto is eligible for the raw-int
// self-recursive ABI. The predicate delegates to AnalyzeSpecializedABI so the
// compiler, tests, and future metadata all use the same structural contract.
// Returns (ok, numParams). When ok is true, numParams is in [1, 4].
func qualifyForNumeric(proto *vm.FuncProto) (bool, int) {
	abi := AnalyzeRawIntSelfABI(proto)
	if !abi.Eligible {
		return false, 0
	}
	return true, abi.NumParams
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
	ec.emitStoreCallExitDescriptor(callExitDescriptor{
		slot:    funcSlot,
		nArgs:   nArgs,
		nRets:   nRets,
		instrID: instr.ID,
	})

	// Set ExitCode = ExitCallExit and return to Go.
	ec.emitCallProtocolExitToGo(ExitCallExit)

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
// instructions instead of spilling the full allocatable GPR/FPR pools.
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

	liveOut := map[int]bool(nil)
	if callInstr.Block != nil {
		liveOut = ec.blockLiveOut[callInstr.Block.ID]
	}

	// Check GPRs: is the active value used after the call or live out of
	// this block? blockLiveOut is point-bounded; crossBlockLive is too broad
	// for values carried into this block and already consumed before the call.
	for valueID := range ec.activeRegs {
		if usedAfter[valueID] || liveOut[valueID] {
			gprLive[valueID] = true
		}
	}

	// Check FPRs: same criterion.
	for valueID := range ec.activeFPRegs {
		if usedAfter[valueID] || liveOut[valueID] {
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
		ec.emitStoreGPRValueAsBoxed(valueID, reg, slot)
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
		ec.emitReloadGPRValueFromBoxed(valueID, reg, slot)
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
