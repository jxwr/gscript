//go:build darwin && arm64

package jit

// SSA Register Allocator: standalone pass that maps VM slots to ARM64 registers.
//
// This is a pure analysis pass: SSAFunc in, RegMap out. No codegen.
//
// The allocator uses frequency-based heuristics to assign the hottest VM slots
// to physical registers:
//   - Integer slots → X20-X24 (up to 5, via slotAlloc)
//   - Float slots   → D4-D11  (up to 8, via floatSlotAlloc)
//
// The underlying allocation logic is identical to newSlotAlloc/newFloatSlotAlloc
// in ssa_codegen.go. This file wraps them in a clean pass interface.

// allocableRegs are the ARM64 registers available for trace integer register allocation.
// X24 is reserved for the NaN-boxing int tag constant.
var allocableRegs = []Reg{X20, X21, X22, X23}

const maxAllocRegs = 4

// RegMap holds the complete register allocation for an SSA function.
// It maps VM slots to both integer and float ARM64 registers.
// FloatRef provides ref-level allocation for individual SSA values.
type RegMap struct {
	Int      *slotAlloc      // integer slot → X20-X24
	Float    *floatSlotAlloc // float slot → D4-D11 (slot-level, used as fallback)
	FloatRef *floatRefAlloc  // float SSA ref → D4-D11 (ref-level, primary)
}

// AllocateRegisters performs register allocation for the given SSAFunc.
// This is a standalone pass: SSAFunc in, RegMap out. No codegen.
// Uses ref-level live-range allocation for floats (each SSA value gets its own
// D register assignment, enabling better register utilization when multiple
// temporaries share the same VM slot).
func AllocateRegisters(f *SSAFunc) *RegMap {
	fra := floatRefAllocLR(f)

	// Build a slot-level allocation from the ref-level allocation.
	// For store-back at loop exit, we need to know which D register holds the
	// LAST value of each slot. Walk instructions and keep the LAST ref's register.
	fa := &floatSlotAlloc{
		slotToReg: make(map[int]FReg),
		regToSlot: make(map[FReg]int),
	}

	if f != nil {
		// Find the last float-producing ref for each slot in the loop body
		loopIdx := -1
		for i, inst := range f.Insts {
			if inst.Op == SSA_LOOP {
				loopIdx = i
				break
			}
		}

		// For slots used before the loop: use the first ref's register
		// (needed for pre-loop loading)
		if loopIdx >= 0 {
			for i := 0; i <= loopIdx; i++ {
				ref := SSARef(i)
				inst := &f.Insts[i]
				if dreg, ok := fra.getReg(ref); ok {
					slot := int(inst.Slot)
					if slot >= 0 {
						fa.slotToReg[slot] = dreg
						fa.regToSlot[dreg] = slot
					}
				}
			}
		}

		// For slots in the loop body: use the LAST ref's register
		// This is the register that holds the slot's value at loop exit
		if loopIdx >= 0 {
			for i := loopIdx + 1; i < len(f.Insts); i++ {
				ref := SSARef(i)
				inst := &f.Insts[i]
				if dreg, ok := fra.getReg(ref); ok {
					slot := int(inst.Slot)
					if slot >= 0 {
						// Overwrite: last writer wins (correct for store-back)
						if oldSlot, hasOldSlot := fa.regToSlot[dreg]; hasOldSlot && oldSlot != slot {
							delete(fa.slotToReg, oldSlot)
						}
						fa.slotToReg[slot] = dreg
						fa.regToSlot[dreg] = slot
					}
				}
			}
		}
	}

	return &RegMap{
		Int:      newSlotAlloc(f),
		Float:    fa,
		FloatRef: fra,
	}
}

// IntReg returns the integer register for a VM slot, and whether it's allocated.
func (rm *RegMap) IntReg(slot int) (Reg, bool) {
	return rm.Int.getReg(slot)
}

// FloatReg returns the float register for a VM slot, and whether it's allocated.
func (rm *RegMap) FloatReg(slot int) (FReg, bool) {
	return rm.Float.getReg(slot)
}

// FloatRefReg returns the float register for an SSA ref, and whether it's allocated.
func (rm *RegMap) FloatRefReg(ref SSARef) (FReg, bool) {
	if rm.FloatRef == nil {
		return 0, false
	}
	return rm.FloatRef.getReg(ref)
}

// IsAllocated returns true if the slot has either an int or float register.
func (rm *RegMap) IsAllocated(slot int) bool {
	_, intOk := rm.Int.getReg(slot)
	_, floatOk := rm.Float.getReg(slot)
	return intOk || floatOk
}
