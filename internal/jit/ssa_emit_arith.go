//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// Per-instruction emission: integer arithmetic
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitIntArith(ref SSARef, inst *SSAInst, op func(*Assembler, Reg, Reg, Reg)) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	dst := ec.getIntDst(ref, inst, X2)
	op(ec.asm, dst, a1, a2)
	ec.spillInt(ref, inst, dst)
}

func (ec *emitCtx) emitModInt(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	a2 := ec.resolveIntRef(inst.Arg2, X1)
	dst := ec.getIntDst(ref, inst, X2)
	// a % b = a - (a / b) * b
	ec.asm.SDIV(X3, a1, a2)     // X3 = a / b
	ec.asm.MSUB(dst, X3, a2, a1) // dst = a - X3 * b
	ec.spillInt(ref, inst, dst)
}

func (ec *emitCtx) emitNegInt(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	dst := ec.getIntDst(ref, inst, X1)
	ec.asm.NEG(dst, a1)
	ec.spillInt(ref, inst, dst)
}

// spillInt: if the dst register is a scratch register (not allocated),
// store the result back to memory.
func (ec *emitCtx) spillInt(ref SSARef, inst *SSAInst, dst Reg) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	// When an int value is written to a slot, remove any stale float tracking.
	// This prevents the float store-back from overwriting the slot with an old
	// float value after an int operation has updated it (e.g., quicksort swap
	// where slot 10 alternates between arr[j] (float) and i+1 (int)).
	delete(ec.floatSlotReg, slot)
	delete(ec.floatWrittenSlots, slot)
	if reg, ok := ec.regMap.IntReg(slot); ok && reg == dst {
		return // already in allocated register, no spill needed
	}
	// dst is scratch — store back to memory (NaN-boxed)
	EmitBoxIntFast(ec.asm, dst, dst, regTagInt)
	ec.asm.STR(dst, regRegs, slot*ValueSize)
}

// ────────────────────────────────────────────────────────────────────────────
// Per-instruction emission: float arithmetic
// ────────────────────────────────────────────────────────────────────────────

func (ec *emitCtx) emitFloatArith(ref SSARef, inst *SSAInst, op func(*Assembler, FReg, FReg, FReg)) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	a2 := ec.resolveFloatRef(inst.Arg2, D1)
	dst := ec.getFloatDst(ref, inst, D2)
	op(ec.asm, dst, a1, a2)
	ec.spillFloat(ref, inst, dst)
}

func (ec *emitCtx) emitNegFloat(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveFloatRef(inst.Arg1, D0)
	dst := ec.getFloatDst(ref, inst, D1)
	// FNEGd: Dd = -Dn. ARM64 encoding: 0|00|11110|01|1|00001|10000|Rn|Rd
	// Not in our assembler yet — emit manually
	ec.asm.emit(0x1E614000 | uint32(a1)<<5 | uint32(dst))
	ec.spillFloat(ref, inst, dst)
}

func (ec *emitCtx) emitFMADD(ref SSARef, inst *SSAInst) {
	// FMADD: dst = Arg1 * Arg2 + AuxInt(ref)
	// ARM64 FMADDd(rd, rn, rm, ra) = ra + rn * rm
	a := ec.resolveFloatRef(inst.Arg1, D0)
	b := ec.resolveFloatRef(inst.Arg2, D1)
	c := ec.resolveFloatRef(SSARef(inst.AuxInt), D3) // addend
	dst := ec.getFloatDst(ref, inst, D2)
	ec.asm.FMADDd(dst, a, b, c)
	ec.spillFloat(ref, inst, dst)
}

func (ec *emitCtx) emitFMSUB(ref SSARef, inst *SSAInst) {
	// FMSUB: dst = AuxInt(ref) - Arg1 * Arg2
	// ARM64 FMSUBd(rd, rn, rm, ra) = ra - rn * rm
	a := ec.resolveFloatRef(inst.Arg1, D0)
	b := ec.resolveFloatRef(inst.Arg2, D1)
	c := ec.resolveFloatRef(SSARef(inst.AuxInt), D3) // minuend
	dst := ec.getFloatDst(ref, inst, D2)
	ec.asm.FMSUBd(dst, a, b, c)
	ec.spillFloat(ref, inst, dst)
}

// emitBoxIntAsFloat: SSA_BOX_INT used as int→float conversion
func (ec *emitCtx) emitBoxIntAsFloat(ref SSARef, inst *SSAInst) {
	a1 := ec.resolveIntRef(inst.Arg1, X0)
	dst := ec.getFloatDst(ref, inst, D0)
	// SCVTF: convert signed int64 to float64
	ec.asm.SCVTF(dst, a1)
	ec.spillFloat(ref, inst, dst)
}

// spillFloat: if the dst FPR is scratch, store back to memory.
// Also tracks the slot→register mapping for the store-back.
func (ec *emitCtx) spillFloat(ref SSARef, inst *SSAInst, dst FReg) {
	slot := int(inst.Slot)
	if slot < 0 {
		return
	}
	// Track which register holds this slot's current value
	ec.floatSlotReg[slot] = dst
	// Mark this slot as last-written by float, so int store-back skips it.
	ec.floatWrittenSlots[slot] = true
	if freg, ok := ec.regMap.FloatRefReg(ref); ok && freg == dst {
		return // already in allocated register
	}
	if freg, ok := ec.regMap.FloatReg(slot); ok && freg == dst {
		return // already in allocated register
	}
	// dst is scratch — store back to memory (raw float bits = NaN-boxed float)
	ec.asm.FSTRd(dst, regRegs, slot*ValueSize)
}
