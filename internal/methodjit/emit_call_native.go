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

	"github.com/gscript/gscript/internal/jit"
)

// emitCallNative emits a native BLR call sequence for OpCall in Tier 2.
// Uses spill/reload of SSA registers around the BLR. Falls back to
// emitCallExit on the slow path (non-closure, uncompiled, overflow, etc.).
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

	// Step 2: Spill ALL live SSA registers to memory (GPR + FPR).
	ec.emitSpillAllForCall()

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
	asm.LDR(jit.X1, jit.X0, vmClosureOffProto)     // X1 = *FuncProto
	asm.LDR(jit.X2, jit.X1, funcProtoOffDirectEntryPtr) // X2 = DirectEntryPtr
	asm.CBZ(jit.X2, slowLabel)                      // not compiled -> slow

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
	asm.SUBimm(jit.SP, jit.SP, 64)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.STP(mRegRegs, mRegConsts, jit.SP, 16)
	asm.LDR(jit.X3, mRegCtx, execCtxOffCallMode)
	asm.STR(jit.X3, jit.SP, 32)
	// Save caller's ClosurePtr and GlobalCache.
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.STR(jit.X3, jit.SP, 40)
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.STR(jit.X3, jit.SP, 48)

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

	// Load callee's GlobalValCache from Proto.
	asm.LDR(jit.X3, jit.X1, funcProtoOffGlobalValCachePtr)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)

	// Increment NativeCallDepth.
	asm.LDR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)
	asm.ADDimm(jit.X3, jit.X3, 1)
	asm.STR(jit.X3, mRegCtx, execCtxOffNativeCallDepth)

	// BLR to callee's direct entry.
	asm.MOVreg(jit.X0, mRegCtx)
	asm.BLR(jit.X2)

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
	asm.LDR(jit.X3, jit.SP, 48)
	asm.STR(jit.X3, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, 64)

	// Restore ctx pointers.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)

	// Step 11: Check callee exit code.
	asm.LDR(jit.X3, mRegCtx, execCtxOffExitCode)
	asm.CBNZ(jit.X3, exitHandleLabel)

	// Normal return: read result from BaselineReturnValue.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineReturnValue)
	// Store result to regs[funcSlot] (overwrites the function slot, Lua convention).
	asm.STR(jit.X0, mRegRegs, slotOffset(funcSlot))

	// Step 12: Reload ALL live SSA registers from memory.
	ec.emitReloadAllForCall()

	// Step 13: Store result into the SSA value's home.
	// The result is at regs[funcSlot], load it and store to SSA home.
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	asm.B(doneLabel)

	// --- Callee exited mid-execution (deopt/op-exit within callee) ---
	// Both callee-exit and slow-path share a single exit-resume sequence.
	asm.Label(exitHandleLabel)
	asm.Label(slowLabel)
	ec.emitCallExitFallback(instr, funcSlot, nArgs, nRets)

	// --- Done: merge point for native and slow paths ---
	asm.Label(doneLabel)
}

// emitCallExitFallback emits the exit-resume sequence for a CALL that couldn't
// take the native BLR path. This is identical to emitCallExit but without the
// arg-store (args were already stored in emitCallNative step 1) and without
// re-spilling (already spilled in step 2).
func (ec *emitContext) emitCallExitFallback(instr *Instr, funcSlot, nArgs, nRets int) {
	asm := ec.asm

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
	continueLabel := fmt.Sprintf("call_continue_%d", instr.ID)
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllForCall()

	// Load call result from regs[funcSlot].
	asm.LDR(jit.X0, mRegRegs, slotOffset(funcSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	// Record for deferred resume entry generation.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// emitSpillAllForCall writes ALL active register-resident values (GPR and FPR)
// to their memory home slots. Called before a native BLR to ensure the VM
// register file is fully up-to-date and callee-saved registers can be reused.
func (ec *emitContext) emitSpillAllForCall() {
	// Spill GPRs (same as emitStoreAllActiveRegs).
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
		if ec.rawIntRegs[valueID] {
			jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
			ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
		} else {
			ec.asm.STR(reg, mRegRegs, slotOffset(slot))
		}
	}

	// Spill FPRs: float bits ARE the NaN-boxed representation, so
	// FMOVtoGP gives us the correct NaN-boxed value for storage.
	for valueID := range ec.activeFPRegs {
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

// emitReloadAllForCall reloads ALL active register-resident values (GPR and FPR)
// from their memory home slots. Called after a native BLR or call-exit resume.
func (ec *emitContext) emitReloadAllForCall() {
	// Reload GPRs.
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
		delete(ec.rawIntRegs, valueID)
	}

	// Reload FPRs: load NaN-boxed from memory, move bits to FPR.
	// Since float bits ARE the NaN-boxed representation, we can use
	// FLDRd to load directly into the FPR.
	for valueID := range ec.activeFPRegs {
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
