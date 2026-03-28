//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// resolveIntRef: get the GPR holding an SSA ref's int value.
// If the ref is in a register, returns that register.
// Otherwise loads from memory into scratch.
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) resolveIntRef(ref SSARef, scratch Reg) Reg {
	if ref == SSARefNone || int(ref) >= len(ec.f.Insts) {
		return scratch
	}
	inst := &ec.f.Insts[ref]
	slot := int(inst.Slot)

	// Check for constant values BEFORE slot-level allocation.
	// A CONST_INT shares a slot with other instructions. The slot-level GPR
	// may have been overwritten by a later instruction targeting the same slot.
	// Always reload from the immediate to guarantee correctness.
	if inst.Op == SSA_CONST_INT {
		ec.asm.LoadImm64(scratch, inst.AuxInt)
		return scratch
	}

	// Check if this ref's value is in regSelfExtra (X28) from a self-call result
	// that had no GPR allocation. X28 is saved/restored across self-calls.
	if ec.selfCallExtraRef == ref {
		return regSelfExtra
	}

	// Slot-level allocation
	if slot >= 0 {
		if reg, ok := ec.regMap.IntReg(slot); ok {
			return reg
		}
	}

	// Load from memory
	if slot >= 0 {
		ec.asm.LDR(scratch, regRegs, slot*ValueSize)
		EmitUnboxInt(ec.asm, scratch, scratch)
		return scratch
	}

	return scratch
}

// resolveFloatRef: get the FPR holding an SSA ref's float value.
func (ec *emitCtx) resolveFloatRef(ref SSARef, scratch FReg) FReg {
	if ref == SSARefNone || int(ref) >= len(ec.f.Insts) {
		return scratch
	}
	inst := &ec.f.Insts[ref]

	// Check ref-level float allocation
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		return freg
	}

	// Check for float constant BEFORE slot-level allocation.
	// A CONST_FLOAT shares a slot with other instructions (e.g., MUL_FLOAT).
	// The slot-level FPR may have been overwritten by a later instruction
	// targeting the same slot, destroying the constant value. Always reload
	// from the immediate to guarantee correctness.
	if inst.Op == SSA_CONST_FLOAT {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(scratch, X0)
		return scratch
	}

	slot := int(inst.Slot)
	// Check slot-level float allocation
	if slot >= 0 {
		if freg, ok := ec.regMap.FloatReg(slot); ok {
			return freg
		}
	}

	// Load from memory
	if slot >= 0 {
		ec.asm.FLDRd(scratch, regRegs, slot*ValueSize)
		return scratch
	}

	return scratch
}

// getIntDst: get the destination GPR for an SSA ref's result.
func (ec *emitCtx) getIntDst(ref SSARef, inst *SSAInst, scratch Reg) Reg {
	slot := int(inst.Slot)
	if slot >= 0 {
		if reg, ok := ec.regMap.IntReg(slot); ok {
			return reg
		}
	}
	return scratch
}

// getFloatDst: get the destination FPR for an SSA ref's result.
func (ec *emitCtx) getFloatDst(ref SSARef, inst *SSAInst, scratch FReg) FReg {
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		return freg
	}
	slot := int(inst.Slot)
	if slot >= 0 {
		if freg, ok := ec.regMap.FloatReg(slot); ok {
			return freg
		}
	}
	return scratch
}

// ────────────────────────────────────────────────────────────────────────────
// UNBOX_INT / UNBOX_FLOAT (in loop body)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitUnboxInt(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	if reg, ok := ec.regMap.IntReg(slot); ok {
		ec.asm.LDR(reg, regRegs, slot*ValueSize)
		EmitUnboxInt(ec.asm, reg, reg)
	}
}

func (ec *emitCtx) emitUnboxFloat(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		ec.asm.FLDRd(freg, regRegs, slot*ValueSize)
	} else if freg, ok := ec.regMap.FloatReg(slot); ok {
		ec.asm.FLDRd(freg, regRegs, slot*ValueSize)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CONST_INT / CONST_FLOAT (in loop body)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitConstInt(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	if reg, ok := ec.regMap.IntReg(slot); ok {
		ec.asm.LoadImm64(reg, inst.AuxInt)
	} else {
		// Store directly to memory as NaN-boxed int
		ec.asm.LoadImm64(X0, inst.AuxInt)
		EmitBoxIntFast(ec.asm, X0, X0, regTagInt)
		ec.asm.STR(X0, regRegs, slot*ValueSize)
	}
}

func (ec *emitCtx) emitConstFloat(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	// Always load into ref-level register if one is allocated (even for slot=-1 constants).
	if freg, ok := ec.regMap.FloatRefReg(ref); ok {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(freg, X0)
		if slot >= 0 {
			ec.floatSlotReg[slot] = freg
		}
		return
	}
	if slot < 0 {
		return
	}
	if freg, ok := ec.regMap.FloatReg(slot); ok {
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.FMOVtoFP(freg, X0)
		ec.floatSlotReg[slot] = freg
	} else {
		// Store directly to memory (raw float bits = NaN-boxed float)
		ec.asm.LoadImm64(X0, inst.AuxInt)
		ec.asm.STR(X0, regRegs, slot*ValueSize)
		delete(ec.floatSlotReg, slot) // value is in memory, not a register
	}
}

// ────────────────────────────────────────────────────────────────────────────
// MOVE instruction
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitMove(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

	if inst.Type == SSATypeFloat {
		src := ec.resolveFloatRef(inst.Arg1, D0)
		dst := ec.getFloatDst(ref, inst, D1)
		if src != dst {
			ec.asm.FMOVd(dst, src)
		}
		ec.spillFloat(ref, inst, dst)
	} else {
		src := ec.resolveIntRef(inst.Arg1, X0)
		dst := ec.getIntDst(ref, inst, X1)
		if src != dst {
			ec.asm.MOVreg(dst, src)
		}
		ec.spillInt(ref, inst, dst)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LOAD_SLOT (in loop body — reload from memory)
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitLoadSlot(ref SSARef, inst *SSAInst) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}

	if inst.Type == SSATypeFloat {
		dst := ec.getFloatDst(ref, inst, D0)
		ec.asm.FLDRd(dst, regRegs, slot*ValueSize)
	} else if inst.Type == SSATypeInt {
		dst := ec.getIntDst(ref, inst, X0)
		ec.asm.LDR(dst, regRegs, slot*ValueSize)
		EmitUnboxInt(ec.asm, dst, dst)
	}
}
