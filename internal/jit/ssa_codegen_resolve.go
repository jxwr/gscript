//go:build darwin && arm64

package jit

// getSlotReg returns the ARM64 register for an SSA ref based on its VM slot.
// If the slot is allocated, returns the allocated register.
// Otherwise returns the scratch register.
func getSlotReg(regMap *RegMap, sm *ssaSlotMapper, ref SSARef, slot int, scratch Reg) Reg {
	if slot >= 0 {
		if r, ok := regMap.IntReg(slot); ok {
			return r
		}
	}
	return scratch
}

// spillIfNotAllocated stores a computed value to memory if its slot is not allocated
// to a physical register. This ensures the value survives across instructions that
// clobber scratch registers.
func spillIfNotAllocated(asm *Assembler, regMap *RegMap, slot int, valReg Reg) {
	if slot < 0 {
		return
	}
	if _, ok := regMap.IntReg(slot); ok {
		return // already in a register, no spill needed
	}
	// Box the raw int and store as NaN-boxed value
	off := slot * ValueSize
	if off <= 32760 {
		EmitBoxInt(asm, X9, valReg, X8)
		asm.STR(X9, regRegs, off)
	}
}

// resolveSSARefSlot returns the ARM64 register holding the value for an SSA ref.
// Uses slot-based allocation: looks up the ref's VM slot, then the slot's register.
func resolveSSARefSlot(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, scratch Reg) Reg {
	if int(ref) >= len(f.Insts) {
		asm.MOVreg(scratch, XZR)
		return scratch
	}

	// Check if this ref has a known slot, and that slot is allocated
	slot := sm.getSlotForRef(ref)
	if slot >= 0 {
		if r, ok := regMap.IntReg(slot); ok {
			return r
		}
		// Slot is known but not allocated → load NaN-boxed value and unbox int
		asm.LDR(scratch, regRegs, slot*ValueSize)
		EmitUnboxInt(asm, scratch, scratch)
		return scratch
	}

	// No slot known → rematerialize based on instruction type
	inst := &f.Insts[ref]
	switch inst.Op {
	case SSA_CONST_INT:
		asm.LoadImm64(scratch, inst.AuxInt)
		return scratch
	case SSA_UNBOX_INT:
		if int(inst.Arg1) < len(f.Insts) {
			loadInst := &f.Insts[inst.Arg1]
			if loadInst.Op == SSA_LOAD_SLOT {
				s := int(loadInst.Slot)
				if r, ok := regMap.IntReg(s); ok {
					return r
				}
				asm.LDR(scratch, regRegs, s*ValueSize)
				EmitUnboxInt(asm, scratch, scratch)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if r, ok := regMap.IntReg(s); ok {
			return r
		}
		asm.LDR(scratch, regRegs, s*ValueSize)
		EmitUnboxInt(asm, scratch, scratch)
		return scratch
	case SSA_LOAD_FIELD, SSA_LOAD_ARRAY, SSA_LOAD_GLOBAL:
		s := int(inst.Slot)
		if s >= 0 {
			if r, ok := regMap.IntReg(s); ok {
				return r
			}
			asm.LDR(scratch, regRegs, s*ValueSize)
			EmitUnboxInt(asm, scratch, scratch)
			return scratch
		}
	}

	asm.MOVreg(scratch, XZR)
	return scratch
}

// emitSlotStoreBack writes modified allocated slot values back to memory.
// Only slots that were actually written by the loop body are stored back.
// Writing unmodified slots (e.g., table references) would corrupt their type.
// Dead-guard slots (WBR slots whose pre-loop guards were eliminated) are skipped:
// their registers hold stale values that would corrupt multi-type slots in memory.
func emitSlotStoreBack(asm *Assembler, regMap *RegMap, sm *ssaSlotMapper, writtenSlots map[int]bool, floatRefSpill map[int]FReg, deadGuardSlots map[int]bool) {
	// Integer register writeback: box raw int → NaN-boxed IntValue
	for slot, armReg := range regMap.Int.slotToReg {
		if !writtenSlots[slot] || deadGuardSlots[slot] {
			continue
		}
		// P2: Skip int store-back for slots that are also in the float register map.
		// These are multi-type slots (used for both int and float/table across the
		// loop body). Writing the stale int register value would corrupt the
		// current float/table value in memory.
		if _, hasFloat := regMap.Float.slotToReg[slot]; hasFloat {
			continue
		}
		if _, hasFloatRef := floatRefSpill[slot]; hasFloatRef {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			EmitBoxInt(asm, X0, armReg, X1)
			asm.STR(X0, regRegs, off)
		}

		if a3, ok := sm.forloopA3[slot]; ok {
			off3 := a3 * ValueSize
			if off3 <= 32760 {
				EmitBoxInt(asm, X0, armReg, X1)
				asm.STR(X0, regRegs, off3)
			}
		}
	}
	// Float D-register writeback: float bits ARE the NaN-boxed value.
	// Just FSTRd directly — no tag needed.
	for slot, dreg := range regMap.Float.slotToReg {
		if !writtenSlots[slot] || deadGuardSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.FSTRd(dreg, regRegs, off)
		}
	}
	// Ref-level float spill: written float slots NOT in Float.slotToReg
	// but allocated to ref-level D registers. These slots need explicit
	// store-back because the loop body may skip memory writes when a
	// ref-level D register is available.
	for slot, dreg := range floatRefSpill {
		if !writtenSlots[slot] || deadGuardSlots[slot] {
			continue
		}
		off := slot * ValueSize
		if off <= 32760 {
			asm.FSTRd(dreg, regRegs, off)
		}
	}
}

// resolveFloatRef returns the FReg holding a float SSA ref's value.
// If the ref's slot is allocated to a D register, returns that register (no load).
// Otherwise loads from memory or rematerializes the constant into scratch.
func resolveFloatRef(asm *Assembler, f *SSAFunc, ref SSARef, regMap *RegMap, sm *ssaSlotMapper, scratch FReg) FReg {
	if int(ref) >= len(f.Insts) {
		return scratch
	}
	inst := &f.Insts[ref]

	// Constant rematerialization
	if inst.Op == SSA_CONST_FLOAT {
		asm.LoadImm64(X0, inst.AuxInt)
		asm.FMOVtoFP(scratch, X0)
		return scratch
	}

	// Check slot allocation
	slot := sm.getSlotForRef(ref)
	if slot >= 0 {
		if dreg, ok := regMap.FloatReg(slot); ok {
			return dreg // already in D register
		}
		// NaN-boxing: float IS the raw value bits, so FLDRd loads correctly
		asm.FLDRd(scratch, regRegs, slot*ValueSize)
		return scratch
	}

	// Fallback: UNBOX_FLOAT / LOAD_SLOT / table load ops
	switch inst.Op {
	case SSA_UNBOX_FLOAT:
		if int(inst.Arg1) < len(f.Insts) {
			li := &f.Insts[inst.Arg1]
			if li.Op == SSA_LOAD_SLOT {
				s := int(li.Slot)
				if dreg, ok := regMap.FloatReg(s); ok {
					return dreg
				}
				asm.FLDRd(scratch, regRegs, s*ValueSize)
				return scratch
			}
		}
	case SSA_LOAD_SLOT:
		s := int(inst.Slot)
		if s >= 0 {
			if dreg, ok := regMap.FloatReg(s); ok {
				return dreg
			}
			asm.FLDRd(scratch, regRegs, s*ValueSize)
			return scratch
		}
	case SSA_LOAD_FIELD, SSA_LOAD_ARRAY, SSA_LOAD_GLOBAL:
		// Table/field/global loads write their result to memory at regs[slot*ValueSize].
		// The float value is already there (NaN-boxed float bits = raw float bits).
		s := int(inst.Slot)
		if s >= 0 {
			if dreg, ok := regMap.FloatReg(s); ok {
				return dreg
			}
			asm.FLDRd(scratch, regRegs, s*ValueSize)
			return scratch
		}
	}
	return scratch
}

// getFloatSlotReg returns the allocated D register for a slot, or scratch.
func getFloatSlotReg(regMap *RegMap, slot int, scratch FReg) FReg {
	if slot >= 0 {
		if dreg, ok := regMap.FloatReg(slot); ok {
			return dreg
		}
	}
	return scratch
}

// storeFloatResult stores a float result. If the slot is allocated to a D register,
// moves the value there (deferred writeback at loop exit). Otherwise writes to memory.
func storeFloatResult(asm *Assembler, regMap *RegMap, slot int, src FReg) {
	if slot < 0 {
		return
	}
	if dreg, ok := regMap.FloatReg(slot); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return // stays in register, written back at exit
	}
	// Not allocated — write float to memory.
	// NaN-boxing: float bits ARE the NaN-boxed value, so FSTRd is correct.
	asm.FSTRd(src, regRegs, slot*ValueSize)
}
// getFloatRefReg returns the D register for an SSA ref (ref-level allocation),
// falling back to the slot-level allocation, or scratch.
func getFloatRefReg(regMap *RegMap, ref SSARef, slot int, scratch FReg) FReg {
	// Ref-level first
	if dreg, ok := regMap.FloatRefReg(ref); ok {
		return dreg
	}
	// Slot-level fallback
	if slot >= 0 {
		if dreg, ok := regMap.FloatReg(slot); ok {
			return dreg
		}
	}
	return scratch
}

// storeFloatResultRef stores a float result using ref-level allocation.
// If the ref has a D register, moves the value there. If the slot has a D register
// (slot-level fallback), moves there. Otherwise writes to memory.
func storeFloatResultRef(asm *Assembler, regMap *RegMap, ref SSARef, slot int, src FReg) {
	if slot < 0 {
		return
	}
	// Check ref-level allocation
	if dreg, ok := regMap.FloatRefReg(ref); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return // stays in register, written back by floatRefSpill at exit
	}
	// Slot-level fallback
	if dreg, ok := regMap.FloatReg(slot); ok {
		if dreg != src {
			asm.FMOVd(dreg, src)
		}
		return
	}
	// Not allocated — write data to memory
	asm.FSTRd(src, regRegs, slot*ValueSize)
}
