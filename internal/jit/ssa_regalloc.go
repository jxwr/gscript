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

// RegMap holds the complete register allocation for an SSA function.
// It maps VM slots to both integer and float ARM64 registers.
type RegMap struct {
	Int   *slotAlloc      // integer slot → X20-X24
	Float *floatSlotAlloc // float slot → D4-D11
}

// AllocateRegisters performs register allocation for the given SSAFunc.
// This is a standalone pass: SSAFunc in, RegMap out. No codegen.
func AllocateRegisters(f *SSAFunc) *RegMap {
	return &RegMap{
		Int:   newSlotAlloc(f),
		Float: newFloatSlotAlloc(f),
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

// IsAllocated returns true if the slot has either an int or float register.
func (rm *RegMap) IsAllocated(slot int) bool {
	_, intOk := rm.Int.getReg(slot)
	_, floatOk := rm.Float.getReg(slot)
	return intOk || floatOk
}
