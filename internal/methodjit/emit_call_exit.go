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

// callExitResumeLabel returns the assembler label name for a call-exit resume point.
func callExitResumeLabel(instrID int) string {
	return fmt.Sprintf("call_resume_%d", instrID)
}

// emitCallExit emits ARM64 code for an OpCall instruction using the call-exit
// mechanism. This replaces the previous emitDeopt for OpCall.
//
// Generated code structure:
//   [in-line] Store args, store regs, write descriptor, exit to Go
//   [in-line] Continue label (jumped to from resume entry)
//   ...rest of function...
//   [at end] Resume entry: full prologue, load result, jump to continue label
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

	// Continue label: the resume entry jumps here after reloading state.
	continueLabel := fmt.Sprintf("call_continue_%d", instr.ID)
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
	})
}

// emitGlobalExit emits ARM64 code for an OpGetGlobal instruction using the
// global-exit mechanism. Instead of deopting the entire function, exits to
// Go which resolves the global, writes it to the register file, and resumes.
//
// OpGetGlobal: Aux = constant pool index for the global name.
// The result is stored in the SSA value's home slot.
func (ec *emitContext) emitGlobalExit(instr *Instr) {
	asm := ec.asm
	constIdx := int(instr.Aux)

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		ec.emitDeopt(instr)
		return
	}

	// Store all active register values to memory before exiting.
	ec.emitStoreAllActiveRegs()

	// Write global descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalSlot)
	asm.LoadImm64(jit.X0, int64(constIdx))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalConst)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalExitID)

	// Set ExitCode = ExitGlobalExit and return to Go.
	asm.LoadImm64(jit.X0, ExitGlobalExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label: the resume entry jumps here after reloading state.
	continueLabel := fmt.Sprintf("global_continue_%d", instr.ID)
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
	})
}

// deferredResume tracks a resume entry point that must be emitted after the
// epilogue. Each call-exit or global-exit generates a deferred resume.
type deferredResume struct {
	instrID       int    // instruction ID (for the resume label name)
	continueLabel string // label to jump to after prologue
}

// emitDeferredResumes emits all resume entry points after the epilogue.
// Each resume entry is a complete function entry point:
//   1. Full prologue (save callee-saved regs, set up stack frame)
//   2. Load pinned registers from ExecContext
//   3. Jump to the continue label (which reloads values and continues)
func (ec *emitContext) emitDeferredResumes() {
	for _, dr := range ec.deferredResumes {
		resumeLabel := callExitResumeLabel(dr.instrID)
		ec.asm.Label(resumeLabel)

		// Full prologue (identical to the main function entry).
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
// file is fully up-to-date before a call-exit.
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
		} else {
			ec.asm.STR(reg, mRegRegs, slotOffset(slot))
		}
	}
}

// emitReloadAllActiveRegs reloads all register-resident values from their
// memory home slots. Called at resume points after a call-exit.
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
}
