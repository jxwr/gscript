//go:build darwin && arm64

// tier3_emit.go implements per-instruction ARM64 emission for the Tier 3
// register-allocated emitter. Values with physical register assignments
// are loaded/stored via registers; spilled values fall back to memory.
//
// Key helpers:
//   t3LoadValue: if value has active register, returns it; else loads from memory
//   t3StoreValue: if value has register, stores to register + memory if cross-block
//
// All values are NaN-boxed in registers (same as memory), keeping the
// emission logic identical to Tier 2 except for the load/store paths.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// --- Register-aware load/store ---

// t3LoadValue loads a NaN-boxed value into a GPR. If the value has an active
// register allocation, returns that register directly (no LDR emitted).
// Otherwise loads from the memory slot into scratchReg.
func (tc *tier3Context) t3LoadValue(scratchReg jit.Reg, valueID int) jit.Reg {
	// Check if the value is in an active register.
	if tc.activeRegs[valueID] {
		if pr, ok := tc.alloc.ValueRegs[valueID]; ok && !pr.IsFloat {
			return jit.Reg(pr.Reg)
		}
	}
	// Fallback: load from memory slot.
	slot, ok := tc.slotMap[valueID]
	if !ok {
		return scratchReg
	}
	tc.asm.LDR(scratchReg, mRegRegs, t2SlotOffset(slot))
	return scratchReg
}

// t3StoreValue stores a NaN-boxed value from srcReg to the value's destination.
// If the value has a register allocation, moves to that register and marks active.
// If the value is cross-block live, also writes through to memory.
// Otherwise, stores only to memory.
func (tc *tier3Context) t3StoreValue(srcReg jit.Reg, valueID int) {
	pr, ok := tc.alloc.ValueRegs[valueID]
	if ok && !pr.IsFloat {
		dstReg := jit.Reg(pr.Reg)
		if srcReg != dstReg {
			tc.asm.MOVreg(dstReg, srcReg)
		}
		tc.activeRegs[valueID] = true

		// Write through to memory if value is used across blocks.
		if tc.crossBlockLive[valueID] {
			slot, slotOk := tc.slotMap[valueID]
			if slotOk {
				tc.asm.STR(dstReg, mRegRegs, t2SlotOffset(slot))
			}
		}
		return
	}
	// No register: store to memory.
	slot, slotOk := tc.slotMap[valueID]
	if slotOk {
		tc.asm.STR(srcReg, mRegRegs, t2SlotOffset(slot))
	}
}

// --- Instruction dispatch ---

// t3EmitInstr dispatches a single IR instruction to the appropriate emitter.
func (tc *tier3Context) t3EmitInstr(instr *Instr, block *Block) {
	switch instr.Op {
	// --- Constants ---
	case OpConstInt:
		tc.t3EmitConstInt(instr)
	case OpConstFloat:
		tc.t3EmitConstFloat(instr)
	case OpConstBool:
		tc.t3EmitConstBool(instr)
	case OpConstNil:
		tc.t3EmitConstNil(instr)
	case OpConstString:
		tc.t3EmitOpExit(instr)

	// --- Slot access ---
	case OpLoadSlot:
		// Handled at block entry by t3LoadBlockLiveIns.
	case OpStoreSlot:
		tc.t3EmitStoreSlot(instr)

	// --- Type-specialized integer arithmetic ---
	case OpAddInt:
		tc.t3EmitIntBinOp(instr, "add")
	case OpSubInt:
		tc.t3EmitIntBinOp(instr, "sub")
	case OpMulInt:
		tc.t3EmitIntBinOp(instr, "mul")
	case OpModInt:
		tc.t3EmitIntBinOp(instr, "mod")
	case OpNegInt:
		tc.t3EmitNegInt(instr)

	// --- Type-specialized float arithmetic ---
	case OpAddFloat:
		tc.t3EmitFloatBinOp(instr, "add")
	case OpSubFloat:
		tc.t3EmitFloatBinOp(instr, "sub")
	case OpMulFloat:
		tc.t3EmitFloatBinOp(instr, "mul")
	case OpDivFloat:
		tc.t3EmitFloatBinOp(instr, "div")
	case OpNegFloat:
		tc.t3EmitNegFloat(instr)

	// --- Generic arithmetic ---
	case OpAdd:
		tc.t3EmitGenericArith(instr, "add")
	case OpSub:
		tc.t3EmitGenericArith(instr, "sub")
	case OpMul:
		tc.t3EmitGenericArith(instr, "mul")
	case OpDiv:
		tc.t3EmitGenericArith(instr, "div")
	case OpMod:
		tc.t3EmitGenericArith(instr, "mod")
	case OpUnm:
		tc.t3EmitGenericUnm(instr)
	case OpNot:
		tc.t3EmitNot(instr)

	// --- Type-specialized comparison ---
	case OpEqInt:
		tc.t3EmitIntCmp(instr, jit.CondEQ)
	case OpLtInt:
		tc.t3EmitIntCmp(instr, jit.CondLT)
	case OpLeInt:
		tc.t3EmitIntCmp(instr, jit.CondLE)
	case OpLtFloat:
		tc.t3EmitFloatCmp(instr, jit.CondLT)
	case OpLeFloat:
		tc.t3EmitFloatCmp(instr, jit.CondLE)

	// --- Generic comparison ---
	case OpEq:
		tc.t3EmitGenericEq(instr)
	case OpLt:
		tc.t3EmitGenericLt(instr)
	case OpLe:
		tc.t3EmitGenericLe(instr)

	// --- Control flow ---
	case OpJump:
		tc.t3EmitJump(instr, block)
	case OpBranch:
		tc.t3EmitBranch(instr, block)
	case OpReturn:
		tc.t3EmitReturn(instr)

	// --- Phi ---
	case OpPhi:
		// No code emitted here; phi moves happen in Jump/Branch emitters.

	// --- Type operations ---
	case OpBoxInt, OpBoxFloat, OpUnboxInt, OpUnboxFloat:
		if len(instr.Args) > 0 {
			src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
			tc.t3StoreValue(src, instr.ID)
		}

	// --- Guards ---
	case OpGuardType, OpGuardNonNil, OpGuardTruthy:
		if len(instr.Args) > 0 {
			src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
			tc.t3StoreValue(src, instr.ID)
		}

	// --- Len ---
	case OpLen:
		tc.t3EmitOpExit(instr)

	// --- Exit-resume ops ---
	case OpCall:
		tc.t3EmitCallExit(instr)
	case OpGetGlobal:
		tc.t3EmitGlobalExit(instr)
	case OpSetGlobal:
		tc.t3EmitOpExit(instr)
	case OpNewTable, OpGetTable, OpSetTable:
		tc.t3EmitTableExit(instr)
	case OpGetField, OpSetField:
		tc.t3EmitTableExit(instr)
	case OpSetList, OpAppend:
		tc.t3EmitOpExit(instr)
	case OpSelf:
		tc.t3EmitOpExit(instr)
	case OpClosure, OpClose:
		tc.t3EmitOpExit(instr)
	case OpGetUpval, OpSetUpval:
		tc.t3EmitOpExit(instr)
	case OpConcat, OpPow:
		tc.t3EmitOpExit(instr)
	case OpVararg, OpTestSet:
		tc.t3EmitOpExit(instr)
	case OpForPrep, OpForLoop:
		tc.t3EmitOpExit(instr)
	case OpTForCall, OpTForLoop:
		tc.t3EmitOpExit(instr)
	case OpGo, OpMakeChan, OpSend, OpRecv:
		tc.t3EmitOpExit(instr)

	case OpNop:
		// No-op.

	default:
		tc.t3EmitOpExit(instr)
	}
}

// --- Constant emission ---

func (tc *tier3Context) t3EmitConstInt(instr *Instr) {
	tc.asm.LoadImm64(jit.X0, instr.Aux)
	jit.EmitBoxIntFast(tc.asm, jit.X0, jit.X0, mRegTagInt)
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitConstFloat(instr *Instr) {
	tc.asm.LoadImm64(jit.X0, instr.Aux)
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitConstBool(instr *Instr) {
	if instr.Aux != 0 {
		tc.asm.ADDimm(jit.X0, mRegTagBool, 1)
	} else {
		tc.asm.MOVreg(jit.X0, mRegTagBool)
	}
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitConstNil(instr *Instr) {
	jit.EmitBoxNil(tc.asm, jit.X0)
	tc.t3StoreValue(jit.X0, instr.ID)
}

// --- Slot access ---

func (tc *tier3Context) t3EmitStoreSlot(instr *Instr) {
	if len(instr.Args) == 0 {
		return
	}
	src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	slot := int(instr.Aux)
	tc.asm.STR(src, mRegRegs, t2SlotOffset(slot))
}

// --- Type-specialized integer binary ops ---

func (tc *tier3Context) t3EmitIntBinOp(instr *Instr, op string) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	// Load both operands, unbox to raw int.
	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)

	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)

	// Compute.
	switch op {
	case "add":
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case "sub":
		asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case "mul":
		asm.MUL(jit.X0, jit.X0, jit.X1)
	case "mod":
		asm.SDIV(jit.X2, jit.X0, jit.X1)
		asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	}

	// Rebox and store.
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitNegInt(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm
	src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if src != jit.X0 {
		asm.MOVreg(jit.X0, src)
	}
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.NEG(jit.X0, jit.X0)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	tc.t3StoreValue(jit.X0, instr.ID)
}

// --- Type-specialized float binary ops ---

func (tc *tier3Context) t3EmitFloatBinOp(instr *Instr, op string) {
	if len(instr.Args) < 2 {
		return
	}
	tc.t3EmitFloatBinOpWithIntCheck(instr, op)
}

func (tc *tier3Context) t3EmitFloatBinOpWithIntCheck(instr *Instr, op string) {
	asm := tc.asm

	// Operand 1.
	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	lhsIntLabel := fmt.Sprintf("t3_fop_lint_%d", instr.ID)
	lhsDoneLabel := fmt.Sprintf("t3_fop_ldone_%d", instr.ID)
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, lhsIntLabel)
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.B(lhsDoneLabel)
	asm.Label(lhsIntLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.SCVTF(jit.D0, jit.X0)
	asm.Label(lhsDoneLabel)

	// Operand 2.
	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	rhsIntLabel := fmt.Sprintf("t3_fop_rint_%d", instr.ID)
	rhsDoneLabel := fmt.Sprintf("t3_fop_rdone_%d", instr.ID)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, rhsIntLabel)
	asm.FMOVtoFP(jit.D1, jit.X1)
	asm.B(rhsDoneLabel)
	asm.Label(rhsIntLabel)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.SCVTF(jit.D1, jit.X1)
	asm.Label(rhsDoneLabel)

	// Compute.
	switch op {
	case "add":
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case "sub":
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case "mul":
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	case "div":
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
	}

	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitNegFloat(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm
	src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if src != jit.X0 {
		asm.MOVreg(jit.X0, src)
	}
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t3StoreValue(jit.X0, instr.ID)
}

// --- Generic arithmetic ---

func (tc *tier3Context) t3EmitGenericArith(instr *Instr, op string) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}

	intPathLabel := fmt.Sprintf("t3_arith_int_%d", instr.ID)
	floatPathLabel := fmt.Sprintf("t3_arith_float_%d", instr.ID)
	doneLabel := fmt.Sprintf("t3_arith_done_%d", instr.ID)

	// Check lhs tag.
	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)
	// Check rhs tag.
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	// Both int: unbox, compute, rebox.
	asm.Label(intPathLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)

	switch op {
	case "add":
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
	case "sub":
		asm.SUBreg(jit.X0, jit.X0, jit.X1)
	case "mul":
		asm.MUL(jit.X0, jit.X0, jit.X1)
	case "mod":
		asm.SDIV(jit.X2, jit.X0, jit.X1)
		asm.MSUB(jit.X0, jit.X2, jit.X1, jit.X0)
	case "div":
		asm.SCVTF(jit.D0, jit.X0)
		asm.SCVTF(jit.D1, jit.X1)
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
		asm.FMOVtoGP(jit.X0, jit.D0)
		tc.t3StoreValue(jit.X0, instr.ID)
		asm.B(doneLabel)
	}

	if op != "div" {
		jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
		tc.t3StoreValue(jit.X0, instr.ID)
	}
	asm.B(doneLabel)

	// Float path.
	asm.Label(floatPathLabel)
	// Reload operands.
	lhs = tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs = tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}

	tc.t3ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t3_ga_lf_%d", instr.ID),
		fmt.Sprintf("t3_ga_ld_%d", instr.ID))
	tc.t3ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t3_ga_rf_%d", instr.ID),
		fmt.Sprintf("t3_ga_rd_%d", instr.ID))

	switch op {
	case "add":
		asm.FADDd(jit.D0, jit.D0, jit.D1)
	case "sub":
		asm.FSUBd(jit.D0, jit.D0, jit.D1)
	case "mul":
		asm.FMULd(jit.D0, jit.D0, jit.D1)
	case "div":
		asm.FDIVd(jit.D0, jit.D0, jit.D1)
	case "mod":
		asm.FDIVd(jit.D2, jit.D0, jit.D1)
		asm.FRINTMd(jit.D2, jit.D2)
		asm.FMSUBd(jit.D0, jit.D2, jit.D1, jit.D0)
	}

	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t3StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

// t3ConvertToFloat converts a NaN-boxed value in srcGPR to float in dstFPR.
func (tc *tier3Context) t3ConvertToFloat(srcGPR jit.Reg, dstFPR jit.FReg, scratch1, scratch2 jit.Reg, intLabel, doneLabel string) {
	asm := tc.asm
	asm.LSRimm(scratch1, srcGPR, 48)
	asm.MOVimm16(scratch2, jit.NB_TagIntShr48)
	asm.CMPreg(scratch1, scratch2)
	asm.BCond(jit.CondEQ, intLabel)
	asm.FMOVtoFP(dstFPR, srcGPR)
	asm.B(doneLabel)
	asm.Label(intLabel)
	jit.EmitUnboxInt(asm, srcGPR, srcGPR)
	asm.SCVTF(dstFPR, srcGPR)
	asm.Label(doneLabel)
}

func (tc *tier3Context) t3EmitGenericUnm(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm

	src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if src != jit.X0 {
		asm.MOVreg(jit.X0, src)
	}

	intLabel := fmt.Sprintf("t3_unm_int_%d", instr.ID)
	doneLabel := fmt.Sprintf("t3_unm_done_%d", instr.ID)

	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondEQ, intLabel)

	// Float path.
	asm.FMOVtoFP(jit.D0, jit.X0)
	asm.FNEGd(jit.D0, jit.D0)
	asm.FMOVtoGP(jit.X0, jit.D0)
	tc.t3StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Int path.
	asm.Label(intLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	asm.NEG(jit.X0, jit.X0)
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	tc.t3StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

func (tc *tier3Context) t3EmitNot(instr *Instr) {
	if len(instr.Args) < 1 {
		return
	}
	asm := tc.asm

	src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if src != jit.X0 {
		asm.MOVreg(jit.X0, src)
	}

	falseLabel := fmt.Sprintf("t3_not_false_%d", instr.ID)
	doneLabel := fmt.Sprintf("t3_not_done_%d", instr.ID)

	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel)

	asm.MOVreg(jit.X1, mRegTagBool)
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel)

	// Truthy -> false.
	asm.MOVreg(jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	// Falsy -> true.
	asm.Label(falseLabel)
	asm.ADDimm(jit.X0, mRegTagBool, 1)
	tc.t3StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

// --- Type-specialized comparison ---

func (tc *tier3Context) t3EmitIntCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)

	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)

	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, cond)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitFloatCmp(instr *Instr, cond jit.Cond) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	tc.t3ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t3_fcmp_li_%d", instr.ID),
		fmt.Sprintf("t3_fcmp_ld_%d", instr.ID))

	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	tc.t3ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t3_fcmp_ri_%d", instr.ID),
		fmt.Sprintf("t3_fcmp_rd_%d", instr.ID))

	asm.FCMPd(jit.D0, jit.D1)
	asm.CSET(jit.X0, cond)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)
}

// --- Generic comparison ---

func (tc *tier3Context) t3EmitGenericEq(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, jit.CondEQ)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)
}

func (tc *tier3Context) t3EmitGenericLt(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}

	intPathLabel := fmt.Sprintf("t3_lt_int_%d", instr.ID)
	floatPathLabel := fmt.Sprintf("t3_lt_float_%d", instr.ID)
	doneLabel := fmt.Sprintf("t3_lt_done_%d", instr.ID)

	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	asm.Label(intPathLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, jit.CondLT)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(floatPathLabel)
	lhs = tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs = tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	tc.t3ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t3_lt_fli_%d", instr.ID),
		fmt.Sprintf("t3_lt_fld_%d", instr.ID))
	tc.t3ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t3_lt_fri_%d", instr.ID),
		fmt.Sprintf("t3_lt_frd_%d", instr.ID))
	asm.FCMPd(jit.D0, jit.D1)
	asm.CSET(jit.X0, jit.CondLT)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

func (tc *tier3Context) t3EmitGenericLe(instr *Instr) {
	if len(instr.Args) < 2 {
		return
	}
	asm := tc.asm

	lhs := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs := tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}

	intPathLabel := fmt.Sprintf("t3_le_int_%d", instr.ID)
	floatPathLabel := fmt.Sprintf("t3_le_float_%d", instr.ID)
	doneLabel := fmt.Sprintf("t3_le_done_%d", instr.ID)

	asm.LSRimm(jit.X2, jit.X0, 48)
	asm.MOVimm16(jit.X3, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, floatPathLabel)

	asm.Label(intPathLabel)
	jit.EmitUnboxInt(asm, jit.X0, jit.X0)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)
	asm.CMPreg(jit.X0, jit.X1)
	asm.CSET(jit.X0, jit.CondLE)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)
	asm.B(doneLabel)

	asm.Label(floatPathLabel)
	lhs = tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if lhs != jit.X0 {
		asm.MOVreg(jit.X0, lhs)
	}
	rhs = tc.t3LoadValue(jit.X1, instr.Args[1].ID)
	if rhs != jit.X1 {
		asm.MOVreg(jit.X1, rhs)
	}
	tc.t3ConvertToFloat(jit.X0, jit.D0, jit.X2, jit.X3,
		fmt.Sprintf("t3_le_fli_%d", instr.ID),
		fmt.Sprintf("t3_le_fld_%d", instr.ID))
	tc.t3ConvertToFloat(jit.X1, jit.D1, jit.X2, jit.X3,
		fmt.Sprintf("t3_le_fri_%d", instr.ID),
		fmt.Sprintf("t3_le_frd_%d", instr.ID))
	asm.FCMPd(jit.D0, jit.D1)
	asm.CSET(jit.X0, jit.CondLE)
	asm.ORRreg(jit.X0, jit.X0, mRegTagBool)
	tc.t3StoreValue(jit.X0, instr.ID)

	asm.Label(doneLabel)
}

// --- Control flow ---

func (tc *tier3Context) t3EmitJump(instr *Instr, block *Block) {
	if len(block.Succs) == 0 {
		return
	}
	target := block.Succs[0]
	tc.t3EmitPhiMoves(block, target)
	tc.asm.B(fmt.Sprintf("t3_B%d", target.ID))
}

func (tc *tier3Context) t3EmitBranch(instr *Instr, block *Block) {
	if len(instr.Args) == 0 || len(block.Succs) < 2 {
		return
	}
	asm := tc.asm

	trueBlock := block.Succs[0]
	falseBlock := block.Succs[1]

	cond := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
	if cond != jit.X0 {
		asm.MOVreg(jit.X0, cond)
	}

	falseLabel := fmt.Sprintf("t3_br_false_%d", instr.ID)

	asm.LoadImm64(jit.X1, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel)

	asm.MOVreg(jit.X1, mRegTagBool)
	asm.CMPreg(jit.X0, jit.X1)
	asm.BCond(jit.CondEQ, falseLabel)

	// Truthy -> true block.
	tc.t3EmitPhiMoves(block, trueBlock)
	asm.B(fmt.Sprintf("t3_B%d", trueBlock.ID))

	// Falsy -> false block.
	asm.Label(falseLabel)
	tc.t3EmitPhiMoves(block, falseBlock)
	asm.B(fmt.Sprintf("t3_B%d", falseBlock.ID))
}

func (tc *tier3Context) t3EmitReturn(instr *Instr) {
	if len(instr.Args) > 0 {
		src := tc.t3LoadValue(jit.X0, instr.Args[0].ID)
		if src != jit.X0 {
			tc.asm.MOVreg(jit.X0, src)
		}
		tc.asm.STR(jit.X0, mRegRegs, 0) // slot 0
	}
	tc.asm.B("t3_epilogue")
}

// --- Phi moves ---

func (tc *tier3Context) t3EmitPhiMoves(fromBlock *Block, toBlock *Block) {
	predIdx := -1
	for i, pred := range toBlock.Preds {
		if pred == fromBlock {
			predIdx = i
			break
		}
	}
	if predIdx < 0 {
		return
	}

	for _, instr := range toBlock.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if predIdx >= len(instr.Args) {
			continue
		}
		srcArg := instr.Args[predIdx]
		if srcArg == nil {
			continue
		}

		// Load source value into X4 (scratch).
		src := tc.t3LoadValue(jit.X4, srcArg.ID)
		// Always store to phi's memory slot.
		slot, ok := tc.slotMap[instr.ID]
		if ok {
			tc.asm.STR(src, mRegRegs, t2SlotOffset(slot))
		}
	}
}

