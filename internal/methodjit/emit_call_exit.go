//go:build darwin && arm64

// emit_call_exit.go implements call-exit and global-exit for the Method JIT.
//
// Call-exit (ExitCode=3): When the JIT encounters OpCall, it exits to Go
// which executes the call via the VM, then re-enters the JIT at a resume point.
//
// Global-exit (ExitCode=4): When the JIT encounters OpGetGlobal, it exits to
// Go which resolves the global variable, then re-enters the JIT.
//
// Both use the same pattern:
//   1. Store all register-resident values to memory.
//   2. Write descriptor to ExecContext.
//   3. Set ExitCode and return to Go via deopt_epilogue.
//   4. Go-side performs the operation (call or global lookup).
//   5. Go-side re-enters the JIT at the resume label.
//   6. Resume: re-init pinned registers, reload all values, load result.
//
// The resume mechanism uses callJIT(resumeAddr, ctxPtr): the trampoline
// takes a code pointer, so we can jump to any point in the native code.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// callExitResumeLabel returns the assembler label name for a call-exit
// resume point in the normal (non-numeric) pass.
// R128: for numeric pass, use callExitResumeLabelForPass(id, true).
func callExitResumeLabel(instrID int) string {
	return callExitResumeLabelForPass(instrID, false)
}

func (ec *emitContext) emitSetResumeNumericPass() {
	if ec.numericMode {
		ec.asm.MOVimm16(jit.X0, 1)
	} else {
		ec.asm.MOVimm16(jit.X0, 0)
	}
	ec.asm.STR(jit.X0, mRegCtx, execCtxOffResumeNumericPass)
}

// emitCallExit emits ARM64 code for an OpCall instruction using the call-exit
// mechanism. This replaces the previous emitDeopt for OpCall.
//
// Generated code structure:
//
//	[in-line] Store args, store regs, write descriptor, exit to Go
//	[in-line] Continue label (jumped to from resume entry)
//	...rest of function...
//	[at end] Resume entry: full prologue, load result, jump to continue label
//
// The resume entry is a complete function entry point with its own prologue,
// so callJIT can jump to it directly.
func (ec *emitContext) emitCallExit(instr *Instr) {
	asm := ec.asm

	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}

	// Store the function value to regs[funcSlot].
	if len(instr.Args) > 0 {
		fnReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if fnReg != jit.X0 {
			asm.MOVreg(jit.X0, fnReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))
	}

	// Store each argument to regs[funcSlot+1], regs[funcSlot+2], ...
	for i := 1; i < len(instr.Args); i++ {
		argReg := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if argReg != jit.X0 {
			asm.MOVreg(jit.X0, argReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot+i))
	}

	// Store all active register-resident values to memory.
	ec.recordExitResumeCheckSite(instr, ExitCallExit, callExitModifiedSlots(funcSlot, nRets), exitResumeCheckOptions{RequireCallFunc: true})
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

	// Continue label: the resume entry jumps here after reloading state.
	continueLabel := ec.passLabel(fmt.Sprintf("call_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory (the call may have changed
	// shared state, and the function slot now contains the result).
	ec.emitReloadAllActiveRegs()

	// Load call result from regs[funcSlot] into the SSA value's home.
	resultSlot := funcSlot
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record this call for deferred resume entry generation.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

// emitGetGlobalNative emits ARM64 code for OpGetGlobal with an inline value
// cache. On cache hit (~5 ARM64 instructions), the cached NaN-boxed value is
// loaded directly from CompiledFunction.GlobalCache[cacheIdx]. On cache miss
// (first access or generation mismatch after SetGlobal), falls through to the
// existing exit-resume path which populates the cache in Go.
//
// Each GetGlobal instruction gets a unique cache index (0, 1, 2, ...) assigned
// at emission time. The generation counter (shared with Tier 1's globalCacheGen)
// is checked before reading the cache: if SetGlobal has incremented it since
// the cache was last populated, the cache is invalidated.
//
// ARM64 fast path:
//  1. Load genPtr from ExecContext, CBZ → slow
//  2. Load current gen, load cached gen, CMP → slow if mismatch
//  3. Load cache pointer, CBZ → slow
//  4. Load cached value at [cache + cacheIdx*8], CBZ → slow (uncached)
//  5. Store result to SSA home
func (ec *emitContext) emitGetGlobalNative(instr *Instr) {
	asm := ec.asm

	if ec.numericMode && ec.isSelfGlobal(instr) {
		ec.emitBoxCurrentClosure(jit.X0, jit.X1)
		ec.storeResultNB(jit.X0, instr.ID)
		return
	}

	slowLabel := ec.uniqueLabel("getglobal_slow")
	doneLabel := ec.uniqueLabel("getglobal_done")

	if ec.supportsIndexedGlobalGetProtocol() {
		indexFallbackLabel := ec.uniqueLabel("getglobal_index_fallback")
		ec.emitIndexedGetGlobalFast(instr, indexFallbackLabel, doneLabel)
		asm.Label(indexFallbackLabel)
	}

	// Assign a cache index for this GetGlobal instruction.
	cacheIdx := ec.nextGlobalCacheIndex
	ec.nextGlobalCacheIndex++
	ec.globalCacheConsts = append(ec.globalCacheConsts, int(instr.Aux))

	// --- Fast path: check generation, then load from cache ---

	// Check generation: *genPtr == *cachedGen?
	asm.LDR(jit.X0, mRegCtx, execCtxOffTier2GlobalGenPtr)
	asm.CBZ(jit.X0, slowLabel) // no gen pointer → cache not set up
	asm.LDR(jit.X1, jit.X0, 0) // X1 = current gen (*genPtr)

	asm.LDR(jit.X0, mRegCtx, execCtxOffTier2GlobalCacheGen)
	asm.CBZ(jit.X0, slowLabel) // no cachedGen pointer
	asm.LDR(jit.X2, jit.X0, 0) // X2 = cached gen (*cachedGen)

	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel) // gen mismatch → cache invalid

	// Load cache pointer.
	asm.LDR(jit.X0, mRegCtx, execCtxOffTier2GlobalCache)
	asm.CBZ(jit.X0, slowLabel) // no cache allocated

	// Load cached value at GlobalCache[cacheIdx].
	cacheOff := cacheIdx * 8 // each entry is 8 bytes (uint64)
	if cacheOff < 4096 {
		asm.LDR(jit.X1, jit.X0, cacheOff)
	} else {
		asm.LoadImm64(jit.X1, int64(cacheOff))
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
		asm.LDR(jit.X1, jit.X0, 0)
	}

	// If zero (cache miss / not yet populated), go to slow path.
	asm.CBZ(jit.X1, slowLabel)

	// Cache hit! Store to result.
	ec.storeResultNB(jit.X1, instr.ID)
	asm.B(doneLabel)

	// --- Slow path: exit-resume to Go, which populates the cache ---
	asm.Label(slowLabel)

	// Write cache index to ExecContext so the Go handler can populate the cache.
	asm.LoadImm64(jit.X0, int64(cacheIdx))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalCacheIdx)

	// Save rawIntRegs before slow path emission — emitGlobalExitInner calls
	// emitReloadAllActiveRegs which clears rawIntRegs entries. The slow path
	// code is correct (after Go execution, values ARE NaN-boxed), but the
	// build-time state must be preserved for subsequent fast-path instructions.
	savedRawIntRegs := make(map[int]bool, len(ec.rawIntRegs))
	for k, v := range ec.rawIntRegs {
		savedRawIntRegs[k] = v
	}
	ec.emitGlobalExitInner(instr)
	// The slow path reloads active registers from memory, where raw-int
	// values are stored boxed. Rebuild the raw register state before
	// merging back into the fast path.
	ec.emitUnboxRawIntRegs(savedRawIntRegs)
	ec.rawIntRegs = savedRawIntRegs

	asm.Label(doneLabel)
}

// emitSetGlobalNative stores directly into VM.globalArray when the VM prepared
// an indexed global table with a matching structural version. If that protocol
// is unavailable or invalidated, it falls back to the existing OpExit path.
func (ec *emitContext) emitSetGlobalNative(instr *Instr) {
	if !ec.supportsIndexedGlobalSetProtocol() {
		ec.emitOpExit(instr)
		return
	}

	asm := ec.asm
	slowLabel := ec.uniqueLabel("setglobal_slow")
	doneLabel := ec.uniqueLabel("setglobal_done")

	ec.emitIndexedGlobalAddress(int(instr.Aux), slowLabel)

	if len(instr.Args) > 0 {
		valReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.STRreg(jit.X0, jit.X16, jit.X17)
	}

	// Native SetGlobal changes global values without an exit. Bump the shared
	// generation counter so Tier 1/Tier 2 value caches miss instead of reading
	// stale cached globals after this store.
	asm.LDR(jit.X1, mRegCtx, execCtxOffTier2GlobalGenPtr)
	asm.CBZ(jit.X1, doneLabel)
	asm.LDR(jit.X2, jit.X1, 0)
	asm.ADDimm(jit.X2, jit.X2, 1)
	asm.STR(jit.X2, jit.X1, 0)
	asm.B(doneLabel)

	asm.Label(slowLabel)
	ec.emitOpExit(instr)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitIndexedGetGlobalFast(instr *Instr, slowLabel, doneLabel string) {
	ec.emitIndexedGlobalAddress(int(instr.Aux), slowLabel)
	ec.asm.LDRreg(jit.X1, jit.X16, jit.X17)
	ec.storeResultNB(jit.X1, instr.ID)
	ec.asm.B(doneLabel)
}

func (ec *emitContext) emitIndexedGlobalAddress(constIdx int, slowLabel string) {
	asm := ec.asm
	asm.LDR(jit.X16, mRegCtx, execCtxOffTier2GlobalArray)
	asm.CBZ(jit.X16, slowLabel)
	asm.LDR(jit.X17, mRegCtx, execCtxOffTier2GlobalIndex)
	asm.CBZ(jit.X17, slowLabel)
	asm.LDR(jit.X0, mRegCtx, execCtxOffTier2GlobalVerPtr)
	asm.CBZ(jit.X0, slowLabel)
	asm.LDRW(jit.X1, jit.X0, 0)
	asm.LDR(jit.X2, mRegCtx, execCtxOffTier2GlobalVer)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)
	constOff := constIdx * 4
	if constOff < 4096*4 {
		asm.LDRW(jit.X17, jit.X17, constOff)
	} else {
		asm.LoadImm64(jit.X3, int64(constOff))
		asm.ADDreg(jit.X17, jit.X17, jit.X3)
		asm.LDRW(jit.X17, jit.X17, 0)
	}
	asm.LoadImm64(jit.X3, 0xFFFFFFFF)
	asm.CMPreg(jit.X17, jit.X3)
	asm.BCond(jit.CondEQ, slowLabel)
}

func (ec *emitContext) supportsIndexedGlobalGetProtocol() bool {
	return ec != nil && ec.fn != nil && ec.fn.Proto != nil
}

func (ec *emitContext) supportsIndexedGlobalSetProtocol() bool {
	return ec != nil && ec.fn != nil && fnSupportsNativeSetGlobalProtocol(ec.fn)
}

func (ec *emitContext) isSelfGlobal(instr *Instr) bool {
	if ec.fn == nil || ec.fn.Proto == nil || instr == nil {
		return false
	}
	constIdx := int(instr.Aux)
	if constIdx < 0 || constIdx >= len(ec.fn.Proto.Constants) {
		return false
	}
	kv := ec.fn.Proto.Constants[constIdx]
	return kv.IsString() && kv.Str() == ec.fn.Proto.Name
}

// emitGlobalExit emits ARM64 code for an OpGetGlobal instruction using the
// global-exit mechanism (no cache). Kept for fallback use; the normal path
// uses emitGetGlobalNative which adds an inline value cache.
func (ec *emitContext) emitGlobalExit(instr *Instr) {
	// Write a dummy cache index of -1 so the Go handler knows not to cache.
	ec.asm.LoadImm64(jit.X0, -1)
	ec.asm.STR(jit.X0, mRegCtx, execCtxOffGlobalCacheIdx)
	ec.emitGlobalExitInner(instr)
}

// emitGlobalExitInner emits the exit-resume body for OpGetGlobal. Shared by
// both emitGetGlobalNative (slow path) and emitGlobalExit (uncached fallback).
func (ec *emitContext) emitGlobalExitInner(instr *Instr) {
	asm := ec.asm
	constIdx := int(instr.Aux)

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	// Store all active register values to memory before exiting.
	ec.recordExitResumeCheckSite(instr, ExitGlobalExit, []int{resultSlot}, exitResumeCheckOptions{})
	ec.emitStoreAllActiveRegs()

	// Write global descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalSlot)
	asm.LoadImm64(jit.X0, int64(constIdx))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalConst)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalExitID)

	// Set ExitCode = ExitGlobalExit and return to Go.
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitGlobalExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label: the resume entry jumps here after reloading state.
	continueLabel := ec.passLabel(fmt.Sprintf("global_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// Load the global value from the register file into the SSA value's home.
	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record for deferred resume entry generation.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

// deferredResume tracks a resume entry point that must be emitted after the
// epilogue. Each call-exit or global-exit generates a deferred resume.
// R128: numericPass flag disambiguates pass-1 vs pass-2 resume entries so
// their resume labels don't collide.
type deferredResume struct {
	instrID       int    // instruction ID (for the resume label name)
	continueLabel string // label to jump to after prologue
	numericPass   bool   // true if from numeric (pass-2) body
}

// emitDeferredResumes emits all resume entry points after the epilogue.
// Each resume entry is a complete function entry point:
//  1. Pass-specific prologue (boxed pass uses full frame; numeric pass uses
//     the same thin frame as t2_numeric_self_entry_N)
//  2. Load pinned registers from ExecContext
//  3. Jump to the continue label (which reloads values and continues)
func (ec *emitContext) emitDeferredResumes() {
	for _, dr := range ec.deferredResumes {
		resumeLabel := callExitResumeLabelForPass(dr.instrID, dr.numericPass)
		ec.asm.Label(resumeLabel)

		if dr.numericPass {
			ec.asm.SUBimm(jit.SP, jit.SP, uint16(numericSelfEntryFrameSize))
			ec.asm.STP(jit.X29, jit.X30, jit.SP, 0)
			ec.asm.ADDimm(jit.X29, jit.SP, 0)
		} else {
			// Full prologue (identical to the main boxed function entry).
			ec.asm.SUBimm(jit.SP, jit.SP, uint16(frameSize))
			ec.asm.STP(jit.X29, jit.X30, jit.SP, 0)
			ec.asm.ADDimm(jit.X29, jit.SP, 0)
			ec.asm.STP(jit.X19, jit.X20, jit.SP, 16)
			ec.asm.STP(jit.X21, jit.X22, jit.SP, 32)
			ec.asm.STP(jit.X23, jit.X24, jit.SP, 48)
			ec.asm.STP(jit.X25, jit.X26, jit.SP, 64)
			ec.asm.STP(jit.X27, jit.X28, jit.SP, 80)
			if ec.useFPR {
				ec.asm.FSTP(jit.D8, jit.D9, jit.SP, 96)
				ec.asm.FSTP(jit.D10, jit.D11, jit.SP, 112)
			}
		}

		// Set up pinned registers from ExecContext (X0 = ctx ptr from trampoline).
		ec.asm.MOVreg(mRegCtx, jit.X0)
		ec.asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)
		ec.asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)
		ec.asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))
		ec.asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))

		// Jump to the continue label in the main code body.
		ec.asm.B(dr.continueLabel)
	}
}

// emitStoreAllActiveRegs writes all register-resident values (active in the
// current block) back to their memory home slots. This ensures the VM register
// file is fully up-to-date before a call/table/op exit.
func (ec *emitContext) emitStoreAllActiveRegs() {
	for valueID := range ec.activeRegs {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		reg := jit.Reg(pr.Reg)
		// If the register holds a raw int, box it before storing.
		if ec.rawIntRegs[valueID] {
			jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
			ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
			ec.emitExitResumeCheckShadowStoreGPR(slot, jit.X0)
		} else {
			ec.asm.STR(reg, mRegRegs, slotOffset(slot))
			ec.emitExitResumeCheckShadowStoreGPR(slot, reg)
		}
	}
	for valueID := range ec.activeFPRegs {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || !pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		ec.asm.FSTRd(jit.FReg(pr.Reg), mRegRegs, slotOffset(slot))
		ec.emitExitResumeCheckShadowStoreFPR(slot, jit.FReg(pr.Reg))
	}
}

func (ec *emitContext) emitExitResumeCheckShadowStoreGPR(slot int, src jit.Reg) {
	if ec.exitResumeCheck == nil {
		return
	}
	skip := ec.uniqueLabel("exitcheck_no_shadow")
	ec.asm.LDR(jit.X17, mRegCtx, execCtxOffExitResumeCheckShadow)
	ec.asm.CBZ(jit.X17, skip)
	ec.asm.STR(src, jit.X17, slotOffset(slot))
	ec.asm.Label(skip)
}

func (ec *emitContext) emitExitResumeCheckShadowStoreFPR(slot int, src jit.FReg) {
	if ec.exitResumeCheck == nil {
		return
	}
	skip := ec.uniqueLabel("exitcheck_no_shadow")
	ec.asm.LDR(jit.X17, mRegCtx, execCtxOffExitResumeCheckShadow)
	ec.asm.CBZ(jit.X17, skip)
	ec.asm.FSTRd(src, jit.X17, slotOffset(slot))
	ec.asm.Label(skip)
}

// emitReloadAllActiveRegs reloads all register-resident values from their
// memory home slots. Called at resume points after a call/table/op exit.
func (ec *emitContext) emitReloadAllActiveRegs() {
	for valueID := range ec.activeRegs {
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
		// After reload, registers hold NaN-boxed values (not raw).
		// Clear raw int tracking for this value.
		delete(ec.rawIntRegs, valueID)
	}
	for valueID := range ec.activeFPRegs {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || !pr.IsFloat {
			continue
		}
		slot, hasSlot := ec.slotMap[valueID]
		if !hasSlot {
			continue
		}
		ec.asm.FLDRd(jit.FReg(pr.Reg), mRegRegs, slotOffset(slot))
	}
}

// emitUnboxRawIntRegs emits ARM64 unbox instructions to convert NaN-boxed
// register values back to raw int form. Called after emitReloadAllActiveRegs
// on deopt fallback paths of native table/field operations, where the fast
// path leaves registers in raw-int form but the slow path (exit-resume)
// reloads them as NaN-boxed. Both paths must converge with the same register
// state, so the slow path unboxes to match the fast path's raw-int convention.
func (ec *emitContext) emitUnboxRawIntRegs(rawRegs map[int]bool) {
	for valueID, isRaw := range rawRegs {
		if !isRaw {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		if _, active := ec.activeRegs[valueID]; !active {
			continue
		}
		reg := jit.Reg(pr.Reg)
		jit.EmitUnboxInt(ec.asm, reg, reg)
	}
}
