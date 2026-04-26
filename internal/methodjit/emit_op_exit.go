//go:build darwin && arm64

// emit_op_exit.go emits ARM64 code for operations that cannot be compiled
// natively. Uses the same exit-resume pattern as call-exit: store registers,
// write op descriptor to ExecContext, exit to Go, Go performs the operation,
// resume JIT.
//
// The op descriptor tells the Go-side handler which operation to execute,
// and where to find/store operands in the VM register file:
//   OpExitOp:   the IR Op to execute
//   OpExitSlot: destination slot for the result
//   OpExitArg1: first operand slot (or constant index)
//   OpExitArg2: second operand slot (or constant index)
//   OpExitAux:  auxiliary data (constant pool index, etc.)
//   OpExitID:   instruction ID for resume address resolution

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// emitOpExit emits ARM64 code that exits to Go for an unsupported operation.
// The Go-side handler executes the operation and writes the result to the
// register file. The JIT resumes at the continue label after the exit.
//
// This is the universal fallback: any IR op can use op-exit, so functions
// are never rejected due to unsupported ops. The cost is a Go round-trip
// per execution of the unsupported op, but the function still benefits from
// native code for all its supported ops.
func (ec *emitContext) emitOpExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		// No home slot: shouldn't happen for ops that produce values,
		// but for side-effect-only ops (SetGlobal, SetUpval, etc.) we
		// assign a dummy slot. Use nextSlot to be safe.
		resultSlot = ec.nextSlot
		ec.slotMap[instr.ID] = resultSlot
		ec.nextSlot++
	}

	// Resolve arg slots. For ops with 0, 1, or 2 args, store the
	// home slot of each arg. The Go side reads the NaN-boxed values
	// from regs[base+argSlot].
	//
	// For 0-arg ops that carry extra aux data (e.g., OpVararg with Aux2=B),
	// Arg1 carries instr.Aux2 so the Go handler can access it.
	arg1Slot := int64(0)
	arg2Slot := int64(0)
	if len(instr.Args) > 0 {
		// Store arg1 value to its memory home before exiting.
		a1Slot, ok := ec.slotMap[instr.Args[0].ID]
		if ok {
			arg1Slot = int64(a1Slot)
		}
	} else {
		// No args: carry instr.Aux2 in Arg1 for ops that need it
		// (e.g., OpVararg uses Aux2 for the B count).
		arg1Slot = instr.Aux2
	}
	if len(instr.Args) > 1 {
		a2Slot, ok := ec.slotMap[instr.Args[1].ID]
		if ok {
			arg2Slot = int64(a2Slot)
		}
	}

	// Store all active register-resident values to memory so the Go
	// handler can read correct values from the register file.
	ec.recordExitResumeCheckSite(instr, ExitOpExit, []int{resultSlot}, exitResumeCheckOptions{})
	ec.emitStoreAllActiveRegs()

	// Write op descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(instr.Op))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitOp)

	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitSlot)

	asm.LoadImm64(jit.X0, arg1Slot)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)

	asm.LoadImm64(jit.X0, arg2Slot)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)

	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)

	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitID)

	// Set ExitCode = ExitOpExit and return to Go.
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label: the resume entry jumps here after Go handles the op.
	continueLabel := ec.passLabel(fmt.Sprintf("op_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory (Go may have modified the
	// register file).
	ec.emitReloadAllActiveRegs()

	// Load the result from the register file into the SSA value's home.
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

// emitConcatExit emits OpConcat through exit-resume with all concat operands.
// The compiler can fold a chain like a .. b .. c into one OpConcat with more
// than two args, so the generic two-arg op-exit descriptor is not sufficient.
func (ec *emitContext) emitConcatExit(instr *Instr) {
	asm := ec.asm

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		resultSlot = ec.nextSlot
		ec.slotMap[instr.ID] = resultSlot
		ec.nextSlot++
	}

	nArgs := len(instr.Args)
	tempBase := ec.nextSlot
	ec.nextSlot += nArgs

	for i, arg := range instr.Args {
		valReg := ec.resolveValueNB(arg.ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(tempBase+i))
	}

	ec.recordExitResumeCheckSite(instr, ExitOpExit, []int{resultSlot}, exitResumeCheckOptions{})
	ec.emitStoreAllActiveRegs()

	asm.LoadImm64(jit.X0, int64(instr.Op))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitOp)

	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitSlot)

	asm.LoadImm64(jit.X0, int64(tempBase))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)

	asm.LoadImm64(jit.X0, int64(nArgs))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)

	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)

	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitID)

	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	continueLabel := ec.passLabel(fmt.Sprintf("op_continue_%d", instr.ID))
	asm.Label(continueLabel)

	ec.emitReloadAllActiveRegs()

	asm.LDR(jit.X0, mRegRegs, slotOffset(resultSlot))
	ec.storeResultNB(jit.X0, instr.ID)

	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}

// emitSetListExit emits ARM64 code for OpSetList via exit-resume.
// OpSetList has variable args: Args[0]=table, Args[1..N]=values.
// Before exiting, stores all values to consecutive temp slots in the
// register file so the Go-side handler can read them sequentially.
//
// Op-exit descriptor:
//
//	OpExitArg1 = table slot
//	OpExitArg2 = start of consecutive value slots (temp base)
//	OpExitAux  = array start index (1-based, from Aux)
//	OpExitSlot = number of values (len(Args)-1)
func (ec *emitContext) emitSetListExit(instr *Instr) {
	asm := ec.asm

	// Allocate consecutive temp slots for the values.
	nValues := len(instr.Args) - 1
	tempBase := ec.nextSlot
	ec.nextSlot += nValues

	// Resolve and store each value to its temp slot.
	for i := 1; i < len(instr.Args); i++ {
		valReg := ec.resolveValueNB(instr.Args[i].ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(tempBase+i-1))
	}

	// Store all active register-resident values to memory.
	ec.recordExitResumeCheckSite(instr, ExitOpExit, nil, exitResumeCheckOptions{RequireTableInputs: true})
	ec.emitStoreAllActiveRegs()

	// Resolve table slot.
	tableSlot := int64(0)
	if len(instr.Args) > 0 {
		if s, ok := ec.slotMap[instr.Args[0].ID]; ok {
			tableSlot = int64(s)
		}
	}

	// Write op descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(instr.Op))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitOp)

	// Slot = nValues (re-purpose for count).
	asm.LoadImm64(jit.X0, int64(nValues))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitSlot)

	// Arg1 = table slot.
	asm.LoadImm64(jit.X0, tableSlot)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)

	// Arg2 = temp base slot (where consecutive values start).
	asm.LoadImm64(jit.X0, int64(tempBase))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)

	// Aux = array start index (1-based).
	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)

	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitID)

	// Set ExitCode = ExitOpExit and return to Go.
	ec.emitSetResumeNumericPass()
	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	if ec.numericMode {
		asm.B("num_deopt_epilogue")
	} else {
		asm.B("deopt_epilogue")
	}

	// Continue label.
	continueLabel := ec.passLabel(fmt.Sprintf("op_continue_%d", instr.ID))
	asm.Label(continueLabel)

	// Reload all active registers from memory.
	ec.emitReloadAllActiveRegs()

	// SetList is side-effect only (no result), but we still need to
	// proceed to the next instruction. No result to store.

	// Record for deferred resume entry generation.
	ec.callExitIDs = append(ec.callExitIDs, instr.ID)
	ec.deferredResumes = append(ec.deferredResumes, deferredResume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
		numericPass:   ec.numericMode,
	})
}
