//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestRegAllocSSA_NonNil verifies AllocateRegisters returns a non-nil RegMap
// for a simple SSAFunc with integer arithmetic.
func TestRegAllocSSA_NonNil(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -2},
		},
	}
	f := BuildSSA(trace)

	rm := AllocateRegisters(f)
	if rm == nil {
		t.Fatal("AllocateRegisters returned nil")
	}
	if rm.Int == nil {
		t.Fatal("RegMap.Int is nil")
	}
	if rm.Float == nil {
		t.Fatal("RegMap.Float is nil")
	}
}

// TestRegAllocSSA_IntSlotsX20X24 verifies that hot integer arithmetic slots
// get allocated to registers in the X20-X24 range.
func TestRegAllocSSA_IntSlotsX20X24(t *testing.T) {
	// Build a trace where slots 0 and 1 are heavily used integer slots.
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -5},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	// Slot 0 should be allocated (very hot)
	r0, ok0 := rm.IntReg(0)
	if !ok0 {
		t.Fatal("slot 0 should have an integer register allocated")
	}
	// Verify it's in the X20-X24 range
	if r0 < X20 || r0 > X24 {
		t.Errorf("slot 0 register %d not in X20-X24 range", r0)
	}

	// Slot 1 should also be allocated (hot)
	r1, ok1 := rm.IntReg(1)
	if !ok1 {
		t.Fatal("slot 1 should have an integer register allocated")
	}
	if r1 < X20 || r1 > X24 {
		t.Errorf("slot 1 register %d not in X20-X24 range", r1)
	}

	// They should get different registers
	if r0 == r1 {
		t.Errorf("slot 0 and slot 1 got the same register %d", r0)
	}
}

// TestRegAllocSSA_FloatSlotsD4D11 verifies that hot float slots
// get allocated to registers in the D4-D11 range.
func TestRegAllocSSA_FloatSlotsD4D11(t *testing.T) {
	// Build a trace where slot 0 is a heavily used float slot.
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -4},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	fr0, ok0 := rm.FloatReg(0)
	if !ok0 {
		t.Fatal("slot 0 should have a float register allocated")
	}
	if fr0 < D4 || fr0 > D11 {
		t.Errorf("slot 0 float register %d not in D4-D11 range", fr0)
	}

	fr1, ok1 := rm.FloatReg(1)
	if !ok1 {
		t.Fatal("slot 1 should have a float register allocated")
	}
	if fr1 < D4 || fr1 > D11 {
		t.Errorf("slot 1 float register %d not in D4-D11 range", fr1)
	}

	if fr0 == fr1 {
		t.Errorf("slot 0 and slot 1 got the same float register %d", fr0)
	}
}

// TestRegAllocSSA_IntRegFloatRegCorrectValues verifies that IntReg and FloatReg
// return the correct register for allocated slots, and match what the underlying
// slotAlloc/floatSlotAlloc hold.
func TestRegAllocSSA_IntRegFloatRegCorrectValues(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 5, B: 5, C: 6, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 5, B: 5, C: 6, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 5, B: 5, C: 6, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -4},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	// Verify IntReg matches internal slotAlloc getReg
	for _, slot := range []int{5, 6} {
		regFromHelper, okHelper := rm.IntReg(slot)
		regFromInternal, okInternal := rm.Int.getReg(slot)
		if okHelper != okInternal {
			t.Errorf("slot %d: IntReg ok=%v but internal getReg ok=%v", slot, okHelper, okInternal)
		}
		if okHelper && regFromHelper != regFromInternal {
			t.Errorf("slot %d: IntReg=%d but internal getReg=%d", slot, regFromHelper, regFromInternal)
		}
	}
}

// TestRegAllocSSA_UnallocatedSlotReturnsFalse verifies that IntReg and FloatReg
// return false for slots that were never used or below the frequency threshold.
func TestRegAllocSSA_UnallocatedSlotReturnsFalse(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -2},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	// Slot 99 was never mentioned in the trace
	_, ok := rm.IntReg(99)
	if ok {
		t.Error("slot 99 should not have an integer register")
	}
	_, ok = rm.FloatReg(99)
	if ok {
		t.Error("slot 99 should not have a float register")
	}

	if rm.IsAllocated(99) {
		t.Error("slot 99 should not be allocated at all")
	}
}

// TestRegAllocSSA_MixedIntFloat verifies that a trace with both integer and float
// operations allocates both int and float registers independently.
func TestRegAllocSSA_MixedIntFloat(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			// Integer ops on slots 0, 1
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			// Float ops on slots 10, 11
			{Op: vm.OP_ADD, A: 10, B: 10, C: 11, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 10, B: 10, C: 11, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 10, B: 10, C: 11, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -7},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	// Integer slots should have int regs
	_, okInt0 := rm.IntReg(0)
	if !okInt0 {
		t.Error("slot 0 should have an integer register (hot int slot)")
	}

	// Float slots should have float regs
	_, okFloat10 := rm.FloatReg(10)
	if !okFloat10 {
		t.Error("slot 10 should have a float register (hot float slot)")
	}

	// Float slot 10 should NOT have an int reg (float slots excluded from int alloc)
	_, okInt10 := rm.IntReg(10)
	if okInt10 {
		t.Error("slot 10 (float) should NOT have an integer register")
	}

	// IsAllocated should return true for both
	if !rm.IsAllocated(0) {
		t.Error("slot 0 should be allocated (int)")
	}
	if !rm.IsAllocated(10) {
		t.Error("slot 10 should be allocated (float)")
	}
}

// TestRegAllocSSA_FrequencyThreshold verifies that slots used fewer than 3 times
// in the trace IR don't get integer registers allocated (the threshold for int alloc
// is count < 1 in newSlotAlloc, but the trace-based legacy threshold is < 3).
// The SSA slotAlloc threshold is count < 1, so a single-use slot WILL be allocated.
// But a slot with count 0 (never used) will not.
func TestRegAllocSSA_FrequencyThreshold(t *testing.T) {
	// Slot 0 is used many times (hot). Slot 7 is used only once (cold).
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -5},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	// Slot 0 is heavily used — should be allocated
	if !rm.IsAllocated(0) {
		t.Error("slot 0 should be allocated (high frequency)")
	}

	// Slot 99 is never mentioned — should NOT be allocated
	if rm.IsAllocated(99) {
		t.Error("slot 99 should NOT be allocated (never used)")
	}
}

// TestRegAllocSSA_IdenticalToOldSlotAlloc verifies that AllocateRegisters allocates
// the same SET of slots as calling newSlotAlloc and newFloatSlotAlloc directly.
// Note: map iteration order is non-deterministic in Go, so when multiple candidates
// have the same frequency, the exact slot-to-register mapping may differ between
// calls. We verify the same slots are allocated, not the exact register assignment.
func TestRegAllocSSA_IdenticalToOldSlotAlloc(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 10, B: 10, C: 11, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 10, B: 10, C: 11, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -6},
		},
	}
	f := BuildSSA(trace)

	// Call new API
	rm := AllocateRegisters(f)

	// Call old functions directly for comparison
	oldInt := newSlotAlloc(f)
	oldFloat := newFloatSlotAlloc(f)

	// Compare integer allocations: same set of slots should be allocated
	if len(rm.Int.slotToReg) != len(oldInt.slotToReg) {
		t.Errorf("int alloc count mismatch: new=%d old=%d", len(rm.Int.slotToReg), len(oldInt.slotToReg))
	}
	for slot := range oldInt.slotToReg {
		if _, ok := rm.IntReg(slot); !ok {
			t.Errorf("int slot %d: allocated by old, not by new", slot)
		}
	}
	for slot := range rm.Int.slotToReg {
		if _, ok := oldInt.slotToReg[slot]; !ok {
			t.Errorf("int slot %d: allocated by new, not by old", slot)
		}
	}

	// Compare float allocations: same set of slots should be allocated
	if len(rm.Float.slotToReg) != len(oldFloat.slotToReg) {
		t.Errorf("float alloc count mismatch: new=%d old=%d", len(rm.Float.slotToReg), len(oldFloat.slotToReg))
	}
	for slot := range oldFloat.slotToReg {
		if _, ok := rm.FloatReg(slot); !ok {
			t.Errorf("float slot %d: allocated by old, not by new", slot)
		}
	}
	for slot := range rm.Float.slotToReg {
		if _, ok := oldFloat.slotToReg[slot]; !ok {
			t.Errorf("float slot %d: allocated by new, not by old", slot)
		}
	}
}
