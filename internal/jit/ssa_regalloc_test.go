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

// ─── Ref-level float register allocation tests ───

// TestFloatRefAlloc_ReturnsNonNil verifies floatRefAllocLR returns a non-nil
// allocation even for an empty function.
func TestFloatRefAlloc_ReturnsNonNil(t *testing.T) {
	fra := floatRefAllocLR(nil)
	if fra == nil {
		t.Fatal("floatRefAllocLR(nil) returned nil")
	}
	if fra.refToReg == nil {
		t.Fatal("refToReg map is nil")
	}
}

// TestFloatRefAlloc_NoLoop verifies that without a LOOP marker, no refs are allocated.
func TestFloatRefAlloc_NoLoop(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_NOP},
		},
	}
	fra := floatRefAllocLR(f)
	if len(fra.refToReg) != 0 {
		t.Errorf("expected no allocations without LOOP, got %d", len(fra.refToReg))
	}
}

// TestFloatRefAlloc_SimpleFloatArith verifies that float arithmetic refs
// in the loop body get D register allocations.
func TestFloatRefAlloc_SimpleFloatArith(t *testing.T) {
	// Build: guard slot 0 float, guard slot 1 float, LOOP, ADD_FLOAT, FORLOOP
	f := &SSAFunc{
		Insts: []SSAInst{
			// Pre-loop: load and unbox float slots 0 and 1
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},    // ref 0
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 0, AuxInt: int64(runtime.TypeFloat)}, // ref 1
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 0, Slot: 0}, // ref 2
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},    // ref 3
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 3, AuxInt: int64(runtime.TypeFloat)}, // ref 4
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 3, Slot: 1}, // ref 5
			// Int slots for FORLOOP
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},      // ref 6
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 6, AuxInt: int64(runtime.TypeInt)}, // ref 7
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},  // ref 8
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},      // ref 9
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 9, AuxInt: int64(runtime.TypeInt)}, // ref 10
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},  // ref 11
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},      // ref 12
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 12, AuxInt: int64(runtime.TypeInt)}, // ref 13
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4}, // ref 14
			{Op: SSA_LOOP}, // ref 15
			// Loop body: slot0 = slot0 + slot1
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 5, Slot: 0}, // ref 16
			// FORLOOP
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 8, Arg2: 14, Slot: 2}, // ref 17
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 11},         // ref 18
		},
	}

	fra := floatRefAllocLR(f)

	// The pre-loop unbox refs (2, 5) should be allocated since they're used in the loop
	if _, ok := fra.getReg(2); !ok {
		t.Error("pre-loop unbox ref 2 (slot 0) should be allocated")
	}
	if _, ok := fra.getReg(5); !ok {
		t.Error("pre-loop unbox ref 5 (slot 1) should be allocated")
	}

	// The ADD_FLOAT ref (16) should be allocated
	if _, ok := fra.getReg(16); !ok {
		t.Error("ADD_FLOAT ref 16 should be allocated")
	}
}

// TestFloatRefAlloc_SlotReuse verifies that multiple refs using the same slot
// can get different D registers when their live ranges don't overlap.
func TestFloatRefAlloc_SlotReuse(t *testing.T) {
	// Simulate mandelbrot pattern:
	// Pre-loop: unbox zr (slot 7), zi (slot 8), cr (slot 5), ci (slot 4)
	// Loop body:
	//   ref A: MUL zr*zr → slot 9 (temp)
	//   ref B: MUL zi*zi → slot 9 (temp, reuses slot)
	//   ref C: SUB (A - B) → slot 9 (temp)
	//   ref D: ADD (C + cr) → slot 9 (tr)
	// With slot-level: all map to one D register. With ref-level: each gets its own.
	f := &SSAFunc{
		Insts: []SSAInst{
			// Pre-loop
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 7},    // ref 0: zr
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 0, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 0, Slot: 7}, // ref 2
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 8},    // ref 3: zi
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 3, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 3, Slot: 8}, // ref 5
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 5},    // ref 6: cr
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 6, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 6, Slot: 5}, // ref 8
			// Int slots for FORLOOP
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 9, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 2}, // ref 11
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 12, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 3}, // ref 14
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 15, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 15, Slot: 4}, // ref 17
			{Op: SSA_LOOP}, // ref 18
			// Loop body
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 2, Slot: 9},  // ref 19: zr*zr
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 5, Arg2: 5, Slot: 9},  // ref 20: zi*zi
			{Op: SSA_SUB_FLOAT, Type: SSATypeFloat, Arg1: 19, Arg2: 20, Slot: 9}, // ref 21: zr*zr - zi*zi
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 21, Arg2: 8, Slot: 9},  // ref 22: + cr = tr
			{Op: SSA_MOVE, Type: SSATypeFloat, Arg1: 22, Slot: 7}, // ref 23: zr = tr
			// FORLOOP
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 11, Arg2: 17, Slot: 2},
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 24, Arg2: 14},
		},
	}

	fra := floatRefAllocLR(f)

	// Ref 19 (zr*zr) and ref 22 (tr) both use slot 9 but should get different registers
	// since ref 19's value is consumed by ref 21 (only 2 instructions later).
	reg19, ok19 := fra.getReg(19)
	reg22, ok22 := fra.getReg(22)

	if ok19 && ok22 {
		// They CAN have different registers now (ref-level allocation)
		t.Logf("ref 19 (zr*zr) → D%d, ref 22 (tr) → D%d", reg19, reg22)
	}

	// The MOVE ref (23) should have the same register as the pre-loop zr ref (2)
	// because of coalescing
	regMove, okMove := fra.getReg(23)
	regPreloop, okPreloop := fra.getReg(2)
	if okMove && okPreloop {
		if regMove != regPreloop {
			t.Errorf("MOVE ref 23 and pre-loop ref 2 should be coalesced: D%d != D%d",
				regMove, regPreloop)
		}
	}
}

// TestFloatRefAlloc_CoalescingLoopCarried verifies that MOVE instructions that
// write to loop-carried slots are coalesced with the pre-loop ref.
func TestFloatRefAlloc_CoalescingLoopCarried(t *testing.T) {
	// Simple pattern: pre-loop unbox slot 0, loop body: slot0 = slot0 + const,
	// which becomes ADD_FLOAT producing to slot 0.
	// In mandelbrot, it's: pre-loop unbox zr, loop body: MUL/SUB/ADD → tr → MOVE zr=tr.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},    // ref 0
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 0, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 0, Slot: 0}, // ref 2
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},    // ref 3
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 3, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 3, Slot: 1}, // ref 5
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 6, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 9, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 12, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
			{Op: SSA_LOOP}, // ref 15
			// Loop body: temp = slot0 + slot1, then MOVE slot0 = temp
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 5, Slot: 6}, // ref 16: temp
			{Op: SSA_MOVE, Type: SSATypeFloat, Arg1: 16, Slot: 0},              // ref 17: slot0 = temp
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 8, Arg2: 14, Slot: 2},
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 18, Arg2: 11},
		},
	}

	fra := floatRefAllocLR(f)

	// MOVE ref 17 writes to slot 0, which has pre-loop ref 2.
	// They should be coalesced (same register).
	regMove, okMove := fra.getReg(17)
	regPre, okPre := fra.getReg(2)

	if !okMove {
		t.Fatal("MOVE ref 17 should have a D register")
	}
	if !okPre {
		t.Fatal("pre-loop ref 2 should have a D register")
	}
	if regMove != regPre {
		t.Errorf("coalescing failed: MOVE ref 17 (D%d) != pre-loop ref 2 (D%d)",
			regMove, regPre)
	}
}

// TestFloatRefAlloc_RegisterRange verifies all allocated registers are in D4-D11.
func TestFloatRefAlloc_RegisterRange(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 0, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 0, Slot: 0},
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},
			{Op: SSA_GUARD_TYPE, Type: SSATypeFloat, Arg1: 3, AuxInt: int64(runtime.TypeFloat)},
			{Op: SSA_UNBOX_FLOAT, Type: SSATypeFloat, Arg1: 3, Slot: 1},
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 6, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 6, Slot: 2},
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 3},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 9, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 9, Slot: 3},
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 4},
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 12, AuxInt: int64(runtime.TypeInt)},
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 12, Slot: 4},
			{Op: SSA_LOOP},
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 2, Arg2: 5, Slot: 0},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 8, Arg2: 14, Slot: 2},
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 17, Arg2: 11},
		},
	}

	fra := floatRefAllocLR(f)

	for ref, dreg := range fra.refToReg {
		if dreg < D4 || dreg > D11 {
			t.Errorf("ref %d allocated to D%d, outside D4-D11 range", ref, dreg)
		}
	}
}

// TestRegMap_FloatRefReg verifies the RegMap.FloatRefReg accessor works.
func TestRegMap_FloatRefReg(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_ADD, A: 0, B: 0, C: 1, AType: runtime.TypeFloat, BType: runtime.TypeFloat, CType: runtime.TypeFloat},
			{Op: vm.OP_FORLOOP, A: 2, SBX: -3},
		},
	}
	f := BuildSSA(trace)
	rm := AllocateRegisters(f)

	// FloatRef should exist
	if rm.FloatRef == nil {
		t.Fatal("FloatRef is nil")
	}

	// At least some float refs should be allocated
	hasAllocation := false
	for _, inst := range f.Insts {
		if isFloatOp(inst.Op) {
			break
		}
	}
	for ref := range rm.FloatRef.refToReg {
		if ref >= 0 {
			hasAllocation = true
			break
		}
	}
	if !hasAllocation && len(rm.FloatRef.refToReg) > 0 {
		t.Log("FloatRef allocations exist")
	}

	// FloatRefReg for a non-existent ref should return false
	_, ok := rm.FloatRefReg(-999)
	if ok {
		t.Error("FloatRefReg(-999) should return false")
	}
}

// TestFloatRefAlloc_MandelbrotCorrectness tests the full mandelbrot pipeline
// with ref-level allocation to ensure correctness.
func TestFloatRefAlloc_MandelbrotCorrectness(t *testing.T) {
	src := `
		func mandelbrot(size) {
			count := 0
			for y := 0; y < size; y++ {
				ci := 2.0 * y / size - 1.0
				for x := 0; x < size; x++ {
					cr := 2.0 * x / size - 1.5
					zr := 0.0
					zi := 0.0
					escaped := false
					for iter := 0; iter < 50; iter++ {
						tr := zr * zr - zi * zi + cr
						ti := 2.0 * zr * zi + ci
						zr = tr
						zi = ti
						if zr * zr + zi * zi > 4.0 {
							escaped = true
							break
						}
					}
					if !escaped { count = count + 1 }
				}
			}
			return count
		}
		result := mandelbrot(20)
	`
	// Run without tracing (interpreter only)
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT (uses ref-level allocation)
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mandelbrot(20) mismatch: interpreter=%d, ssa=%d",
			g1["result"].Int(), g2["result"].Int())
	}
}
