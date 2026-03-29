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
	arg1Slot := int64(0)
	arg2Slot := int64(0)
	if len(instr.Args) > 0 {
		// Store arg1 value to its memory home before exiting.
		a1Slot, ok := ec.slotMap[instr.Args[0].ID]
		if ok {
			arg1Slot = int64(a1Slot)
		}
	}
	if len(instr.Args) > 1 {
		a2Slot, ok := ec.slotMap[instr.Args[1].ID]
		if ok {
			arg2Slot = int64(a2Slot)
		}
	}

	// Store all active register-resident values to memory so the Go
	// handler can read correct values from the register file.
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
	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("deopt_epilogue")

	// Continue label: the resume entry jumps here after Go handles the op.
	continueLabel := fmt.Sprintf("op_continue_%d", instr.ID)
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
	})
}
