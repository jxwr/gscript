//go:build darwin && arm64

package jit

import "fmt"

// emitSSAIntArith emits integer arithmetic: ADD, SUB, MUL, MOD, NEG.
func emitSSAIntArith(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	switch inst.Op {
	case SSA_ADD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.ADDreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)
		// If this slot has a FORLOOP A+3 alias, copy to that register too
		if a3Slot, ok := sm.forloopA3[slot]; ok {
			if a3Reg, ok := regMap.IntReg(a3Slot); ok && a3Reg != dstReg {
				asm.MOVreg(a3Reg, dstReg)
			}
		}

	case SSA_SUB_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.SUBreg(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_MUL_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.MUL(dstReg, arg1Reg, arg2Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_MOD_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X1)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X2)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.LoadImm64(X9, int64(inst.PC))
		asm.CBZ(arg2Reg, "side_exit")
		asm.SDIV(X3, arg1Reg, arg2Reg)
		asm.MSUB(dstReg, X3, arg2Reg, arg1Reg)
		// Lua-style modulo: result has same sign as divisor
		doneLabel := fmt.Sprintf("mod_done_%d", ref)
		asm.CBZ(dstReg, doneLabel)
		asm.EORreg(X3, dstReg, arg2Reg)
		asm.CMPreg(X3, XZR)
		asm.BCond(CondGE, doneLabel)
		asm.ADDreg(dstReg, dstReg, arg2Reg)
		asm.Label(doneLabel)
		spillIfNotAllocated(asm, regMap, slot, dstReg)

	case SSA_NEG_INT:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		slot := sm.getSlotForRef(ref)
		dstReg := getSlotReg(regMap, sm, ref, slot, X0)
		asm.NEG(dstReg, arg1Reg)
		spillIfNotAllocated(asm, regMap, slot, dstReg)
	}
}

// emitSSAIntCompare emits integer comparison guards: EQ, LT, LE.
func emitSSAIntCompare(asm *Assembler, f *SSAFunc, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
	arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
	asm.LoadImm64(X9, int64(inst.PC))
	asm.CMPreg(arg1Reg, arg2Reg)
	switch inst.Op {
	case SSA_EQ_INT:
		asm.BCond(CondNE, "side_exit")
	case SSA_LT_INT:
		asm.BCond(CondGE, "side_exit")
	case SSA_LE_INT:
		asm.BCond(CondGT, "side_exit")
	}
}

// emitSSAFloatArith emits float arithmetic: ADD, SUB, MUL, DIV, FMADD, FMSUB.
func emitSSAFloatArith(asm *Assembler, f *SSAFunc, ref SSARef, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	slot := sm.getSlotForRef(ref)
	arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D1)
	arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D2)

	switch inst.Op {
	case SSA_FMADD, SSA_FMSUB:
		addendD := resolveFloatRef(asm, f, SSARef(inst.AuxInt), regMap, sm, D3)
		dstD := getFloatSlotReg(regMap, slot, D0)
		if inst.Op == SSA_FMADD {
			asm.FMADDd(dstD, arg1D, arg2D, addendD)
		} else {
			asm.FMSUBd(dstD, arg1D, arg2D, addendD)
		}
		storeFloatResult(asm, regMap, slot, dstD)
	default:
		dstD := getFloatSlotReg(regMap, slot, D0)
		switch inst.Op {
		case SSA_ADD_FLOAT:
			asm.FADDd(dstD, arg1D, arg2D)
		case SSA_SUB_FLOAT:
			asm.FSUBd(dstD, arg1D, arg2D)
		case SSA_MUL_FLOAT:
			asm.FMULd(dstD, arg1D, arg2D)
		case SSA_DIV_FLOAT:
			asm.FDIVd(dstD, arg1D, arg2D)
		}
		storeFloatResult(asm, regMap, slot, dstD)
	}
}

// emitSSAFloatCompare emits float comparison guards: LT, LE, GT.
func emitSSAFloatCompare(asm *Assembler, f *SSAFunc, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	asm.LoadImm64(X9, int64(inst.PC))
	arg1D := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
	arg2D := resolveFloatRef(asm, f, inst.Arg2, regMap, sm, D1)
	asm.FCMPd(arg1D, arg2D)
	switch inst.Op {
	case SSA_LT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGE, "side_exit")
		} else {
			asm.BCond(CondLT, "side_exit")
		}
	case SSA_LE_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondGT, "side_exit")
		} else {
			asm.BCond(CondLE, "side_exit")
		}
	case SSA_GT_FLOAT:
		if inst.AuxInt == 0 {
			asm.BCond(CondLE, "side_exit")
		} else {
			asm.BCond(CondGT, "side_exit")
		}
	}
}

// emitSSAIntrinsic emits inline code for SSA_INTRINSIC (sqrt, bxor, band).
func emitSSAIntrinsic(asm *Assembler, f *SSAFunc, inst *SSAInst, regMap *RegMap, sm *ssaSlotMapper) {
	dstSlot := int(inst.Slot)
	switch int(inst.AuxInt) {
	case IntrinsicSqrt:
		// math.sqrt(x): load float arg, FSQRT, store result
		// Arg1 is the input value ref (R(A+1) in original CALL)
		srcD := resolveFloatRef(asm, f, inst.Arg1, regMap, sm, D0)
		asm.FSQRTd(D1, srcD)
		// Store result to destination slot
		if dstSlot >= 0 {
			if dstDreg, ok := regMap.FloatReg(dstSlot); ok {
				asm.FMOVd(dstDreg, D1)
			} else {
				// With NaN-boxing, raw float64 bits ARE the NaN-boxed value.
				asm.FSTRd(D1, regRegs, dstSlot*ValueSize)
			}
		}
	case IntrinsicBxor:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.EORreg(X2, arg1Reg, arg2Reg)
		if dstSlot >= 0 {
			EmitBoxIntFast(asm, X5, X2, regTagInt)
			asm.STR(X5, regRegs, dstSlot*ValueSize)
		}
	case IntrinsicBand:
		arg1Reg := resolveSSARefSlot(asm, f, inst.Arg1, regMap, sm, X0)
		arg2Reg := resolveSSARefSlot(asm, f, inst.Arg2, regMap, sm, X1)
		asm.ANDreg(X2, arg1Reg, arg2Reg)
		if dstSlot >= 0 {
			EmitBoxIntFast(asm, X5, X2, regTagInt)
			asm.STR(X5, regRegs, dstSlot*ValueSize)
		}
	default:
		// Unknown intrinsic → side-exit
		asm.LoadImm64(X9, int64(inst.PC))
		asm.B("side_exit")
	}
}
