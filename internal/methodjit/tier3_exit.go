//go:build darwin && arm64

// tier3_exit.go implements exit-resume for the Tier 3 register-allocated emitter.
// When the JIT encounters an operation it cannot compile natively (calls,
// globals, tables, etc.), it flushes register-resident values to memory,
// exits to Go with state in ExecContext, and resumes via deferred entry points.
//
// This mirrors tier2_exit.go but adds register flushing before exits and
// register reloading after resumes.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// t3FlushRegsToMemory stores all active register-resident values to their
// memory slots. Called before any exit to ensure Go code can read all values.
func (tc *tier3Context) t3FlushRegsToMemory() {
	for valueID := range tc.activeRegs {
		pr, ok := tc.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		slot, slotOk := tc.slotMap[valueID]
		if !slotOk {
			continue
		}
		// Only flush if not already written through (cross-block values
		// are written on every store).
		if !tc.crossBlockLive[valueID] {
			tc.asm.STR(jit.Reg(pr.Reg), mRegRegs, t2SlotOffset(slot))
		}
	}
}

// --- Call exit ---

func (tc *tier3Context) t3EmitCallExit(instr *Instr) {
	asm := tc.asm

	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}

	tc.t3FlushRegsToMemory()

	// Store function value to regs[funcSlot].
	if len(instr.Args) > 0 {
		src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(funcSlot))
	}

	// Store arguments to regs[funcSlot+1..funcSlot+nArgs].
	for i := 1; i < len(instr.Args); i++ {
		src := tc.t3LoadValue(jit.X0, instr.Args[i].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(funcSlot+i))
	}

	// Write call descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(funcSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallSlot)
	asm.LoadImm64(jit.X0, int64(nArgs))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNArgs)
	asm.LoadImm64(jit.X0, int64(nRets))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNRets)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallID)

	// Exit.
	asm.LoadImm64(jit.X0, ExitCallExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t3_exit")

	// Resume label: load call result from regs[funcSlot].
	continueLabel := fmt.Sprintf("t3_call_continue_%d", instr.ID)
	asm.Label(continueLabel)
	asm.LDR(jit.X0, mRegRegs, t2SlotOffset(funcSlot))
	tc.t3StoreValue(jit.X0, instr.ID)

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Global exit ---

func (tc *tier3Context) t3EmitGlobalExit(instr *Instr) {
	asm := tc.asm

	tc.t3FlushRegsToMemory()

	resultSlot := tc.slotMap[instr.ID]
	constIdx := int(instr.Aux)

	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalSlot)
	asm.LoadImm64(jit.X0, int64(constIdx))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalConst)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalExitID)

	asm.LoadImm64(jit.X0, ExitGlobalExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t3_exit")

	continueLabel := fmt.Sprintf("t3_global_continue_%d", instr.ID)
	asm.Label(continueLabel)
	asm.LDR(jit.X0, mRegRegs, t2SlotOffset(resultSlot))
	tc.t3StoreValue(jit.X0, instr.ID)

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Table exit ---

func (tc *tier3Context) t3EmitTableExit(instr *Instr) {
	asm := tc.asm

	tc.t3FlushRegsToMemory()

	var tableOp int64
	switch instr.Op {
	case OpNewTable:
		tableOp = 0
	case OpGetTable:
		tableOp = 1
	case OpSetTable:
		tableOp = 2
	case OpGetField:
		tableOp = 3
	case OpSetField:
		tableOp = 4
	}

	resultSlot := tc.slotMap[instr.ID]

	asm.LoadImm64(jit.X0, tableOp)
	asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)

	if len(instr.Args) > 0 {
		src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(resultSlot))
	}
	if len(instr.Args) > 1 {
		keySlot := resultSlot + 1
		src := tc.t3LoadValue(jit.X0, instr.Args[1].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(keySlot))
		asm.LoadImm64(jit.X0, int64(keySlot))
		asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
	}
	if len(instr.Args) > 2 {
		valSlot := resultSlot + 2
		src := tc.t3LoadValue(jit.X0, instr.Args[2].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(valSlot))
		asm.LoadImm64(jit.X0, int64(valSlot))
		asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
	}

	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
	asm.LoadImm64(jit.X0, instr.Aux2)
	asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t3_exit")

	continueLabel := fmt.Sprintf("t3_table_continue_%d", instr.ID)
	asm.Label(continueLabel)
	asm.LDR(jit.X0, mRegRegs, t2SlotOffset(resultSlot))
	tc.t3StoreValue(jit.X0, instr.ID)

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Op exit (generic unsupported ops) ---

func (tc *tier3Context) t3EmitOpExit(instr *Instr) {
	asm := tc.asm

	tc.t3FlushRegsToMemory()

	resultSlot := tc.slotMap[instr.ID]

	asm.LoadImm64(jit.X0, int64(instr.Op))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitOp)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitSlot)

	if len(instr.Args) > 0 {
		argSlot := resultSlot + 1
		src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(argSlot))
		asm.LoadImm64(jit.X0, int64(argSlot))
		asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)
	} else {
		asm.LoadImm64(jit.X0, 0)
		asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)
	}
	if len(instr.Args) > 1 {
		argSlot := resultSlot + 2
		src := tc.t3LoadValue(jit.X0, instr.Args[1].ID)
		if src != jit.X0 {
			asm.MOVreg(jit.X0, src)
		}
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(argSlot))
		asm.LoadImm64(jit.X0, int64(argSlot))
		asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)
	} else {
		asm.LoadImm64(jit.X0, 0)
		asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)
	}

	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitID)

	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t3_exit")

	continueLabel := fmt.Sprintf("t3_op_continue_%d", instr.ID)
	asm.Label(continueLabel)
	asm.LDR(jit.X0, mRegRegs, t2SlotOffset(resultSlot))
	tc.t3StoreValue(jit.X0, instr.ID)

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}
