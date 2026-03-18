//go:build darwin && arm64

package jit

import "testing"

// helper: build a minimal SSAFunc from a slice of instructions.
func mkSSAFunc(insts ...SSAInst) *SSAFunc {
	return &SSAFunc{Insts: insts}
}

// TestLivenessSimpleAddInt verifies that an ADD_INT after SSA_LOOP with a
// positive Slot appears in WrittenSlots as SSATypeInt.
func TestLivenessSimpleAddInt(t *testing.T) {
	f := mkSSAFunc(
		// Pre-loop: LOAD_SLOT + UNBOX_INT (should NOT appear in written slots)
		SSAInst{Op: SSA_LOAD_SLOT, Slot: 0, Type: SSATypeUnknown},
		SSAInst{Op: SSA_UNBOX_INT, Slot: 0, Type: SSATypeInt, Arg1: 0},
		SSAInst{Op: SSA_LOAD_SLOT, Slot: 1, Type: SSATypeUnknown},
		SSAInst{Op: SSA_UNBOX_INT, Slot: 1, Type: SSATypeInt, Arg1: 2},
		// Loop marker
		SSAInst{Op: SSA_LOOP},
		// Loop body: ADD_INT writes to slot 0
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt, Arg1: 1, Arg2: 3},
		// Loop exit check
		SSAInst{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 5, Arg2: 3},
	)
	li := AnalyzeLiveness(f)
	if !li.WrittenSlots[0] {
		t.Errorf("expected slot 0 in WrittenSlots (ADD_INT writes to it)")
	}
	if li.SlotTypes[0] != SSATypeInt {
		t.Errorf("expected slot 0 type SSATypeInt, got %v", li.SlotTypes[0])
	}
}

// TestLivenessPreLoopExcluded verifies that instructions before SSA_LOOP are
// not included in WrittenSlots.
func TestLivenessPreLoopExcluded(t *testing.T) {
	f := mkSSAFunc(
		// Pre-loop: ADD_INT that writes to slot 5
		SSAInst{Op: SSA_ADD_INT, Slot: 5, Type: SSATypeInt, Arg1: 0, Arg2: 0},
		// Loop marker
		SSAInst{Op: SSA_LOOP},
		// Loop body: only writes to slot 0
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt, Arg1: 0, Arg2: 0},
	)
	li := AnalyzeLiveness(f)
	if li.WrittenSlots[5] {
		t.Errorf("slot 5 should NOT be in WrittenSlots (before SSA_LOOP)")
	}
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots (after SSA_LOOP)")
	}
}

// TestLivenessGuardsExcluded verifies that guard ops are not in WrittenSlots.
func TestLivenessGuardsExcluded(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		// Guards inside loop body (e.g., from inner conditional)
		SSAInst{Op: SSA_GUARD_TYPE, Slot: 3, Type: SSATypeInt},
		SSAInst{Op: SSA_GUARD_NNIL, Slot: 4},
		SSAInst{Op: SSA_GUARD_NOMETA, Slot: 5},
		SSAInst{Op: SSA_GUARD_TRUTHY, Slot: 6},
		// Only this should appear
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
	)
	li := AnalyzeLiveness(f)
	for _, slot := range []int{3, 4, 5, 6} {
		if li.WrittenSlots[slot] {
			t.Errorf("guard slot %d should NOT be in WrittenSlots", slot)
		}
	}
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots (ADD_INT)")
	}
}

// TestLivenessLoadSlotExcluded verifies that LOAD_SLOT (reads, not writes)
// is not in WrittenSlots.
func TestLivenessLoadSlotExcluded(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_LOAD_SLOT, Slot: 2, Type: SSATypeUnknown},
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt, Arg1: 1},
	)
	li := AnalyzeLiveness(f)
	if li.WrittenSlots[2] {
		t.Errorf("slot 2 should NOT be in WrittenSlots (LOAD_SLOT is a read)")
	}
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots")
	}
}

// TestLivenessNOPSkipped verifies that NOP'd instructions don't contribute.
func TestLivenessNOPSkipped(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		// NOP'd ADD_INT (dead code elimination replaced it)
		SSAInst{Op: SSA_NOP, Slot: 7, Type: SSATypeInt},
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
	)
	li := AnalyzeLiveness(f)
	if li.WrittenSlots[7] {
		t.Errorf("slot 7 should NOT be in WrittenSlots (NOP'd instruction)")
	}
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots")
	}
}

// TestLivenessStopAtSideExit verifies that instructions after SSA_SIDE_EXIT
// are NOT included in WrittenSlots (they are unreachable).
func TestLivenessStopAtSideExit(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
		SSAInst{Op: SSA_SIDE_EXIT, PC: 10},
		// These are unreachable:
		SSAInst{Op: SSA_ADD_INT, Slot: 8, Type: SSATypeInt},
		SSAInst{Op: SSA_MUL_INT, Slot: 9, Type: SSATypeInt},
	)
	li := AnalyzeLiveness(f)
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots (before SIDE_EXIT)")
	}
	if li.WrittenSlots[8] {
		t.Errorf("slot 8 should NOT be in WrittenSlots (after SIDE_EXIT)")
	}
	if li.WrittenSlots[9] {
		t.Errorf("slot 9 should NOT be in WrittenSlots (after SIDE_EXIT)")
	}
}

// TestLivenessFloatTypes verifies that float arithmetic slots are recorded
// with SSATypeFloat in SlotTypes.
func TestLivenessFloatTypes(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_ADD_FLOAT, Slot: 0, Type: SSATypeFloat},
		SSAInst{Op: SSA_MUL_FLOAT, Slot: 1, Type: SSATypeFloat},
		SSAInst{Op: SSA_DIV_FLOAT, Slot: 2, Type: SSATypeFloat},
		SSAInst{Op: SSA_SUB_FLOAT, Slot: 3, Type: SSATypeFloat},
		SSAInst{Op: SSA_NEG_FLOAT, Slot: 4, Type: SSATypeFloat},
	)
	li := AnalyzeLiveness(f)
	for slot := 0; slot <= 4; slot++ {
		if !li.WrittenSlots[slot] {
			t.Errorf("expected slot %d in WrittenSlots", slot)
		}
		if li.SlotTypes[slot] != SSATypeFloat {
			t.Errorf("expected slot %d type SSATypeFloat, got %v", slot, li.SlotTypes[slot])
		}
	}
}

// TestLivenessMixedIntFloat verifies correct type tracking in a loop body
// that has both int and float operations.
func TestLivenessMixedIntFloat(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		// Int operations
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
		SSAInst{Op: SSA_SUB_INT, Slot: 1, Type: SSATypeInt},
		// Float operations
		SSAInst{Op: SSA_MUL_FLOAT, Slot: 2, Type: SSATypeFloat},
		SSAInst{Op: SSA_DIV_FLOAT, Slot: 3, Type: SSATypeFloat},
		// MOVE copies (inherits source type)
		SSAInst{Op: SSA_MOVE, Slot: 4, Type: SSATypeInt},
		// CONST inside loop body
		SSAInst{Op: SSA_CONST_INT, Slot: 5, Type: SSATypeInt, AuxInt: 42},
		SSAInst{Op: SSA_CONST_FLOAT, Slot: 6, Type: SSATypeFloat, AuxInt: 0},
	)
	li := AnalyzeLiveness(f)

	expectedTypes := map[int]SSAType{
		0: SSATypeInt,
		1: SSATypeInt,
		2: SSATypeFloat,
		3: SSATypeFloat,
		4: SSATypeInt,
		5: SSATypeInt,
		6: SSATypeFloat,
	}
	for slot, wantType := range expectedTypes {
		if !li.WrittenSlots[slot] {
			t.Errorf("expected slot %d in WrittenSlots", slot)
		}
		if li.SlotTypes[slot] != wantType {
			t.Errorf("slot %d: expected type %v, got %v", slot, wantType, li.SlotTypes[slot])
		}
	}
}

// TestLivenessBuggyCase_NEG_FLOAT is the critical regression test.
// The old ssaWrittenSlots missed SSA_NEG_FLOAT entirely — the opcode was
// absent from the switch statement. This caused stale float values to persist
// in VM slots after the trace exited, leading to incorrect results.
//
// The new analysis MUST include SSA_NEG_FLOAT because it produces a value
// that modifies a VM slot.
func TestLivenessBuggyCase_NEG_FLOAT(t *testing.T) {
	f := mkSSAFunc(
		// Pre-loop setup
		SSAInst{Op: SSA_LOAD_SLOT, Slot: 0, Type: SSATypeUnknown},
		SSAInst{Op: SSA_UNBOX_FLOAT, Slot: 0, Type: SSATypeFloat, Arg1: 0},
		// Loop
		SSAInst{Op: SSA_LOOP},
		// NEG_FLOAT writing to slot 0 — this is the bug: old code missed it
		SSAInst{Op: SSA_NEG_FLOAT, Slot: 0, Type: SSATypeFloat, Arg1: 1},
		// Some other float work
		SSAInst{Op: SSA_ADD_FLOAT, Slot: 1, Type: SSATypeFloat, Arg1: 3, Arg2: 1},
	)
	li := AnalyzeLiveness(f)

	// The critical assertion: NEG_FLOAT's slot MUST be in WrittenSlots
	if !li.WrittenSlots[0] {
		t.Fatal("BUG REGRESSION: slot 0 (NEG_FLOAT target) missing from WrittenSlots")
	}
	if li.SlotTypes[0] != SSATypeFloat {
		t.Errorf("slot 0 should be SSATypeFloat, got %v", li.SlotTypes[0])
	}
	if !li.WrittenSlots[1] {
		t.Fatal("slot 1 (ADD_FLOAT target) missing from WrittenSlots")
	}
}

// TestLivenessBuggyCase_LOAD_ARRAY is another regression test.
// The old ssaWrittenSlots missed SSA_LOAD_ARRAY — a table read that writes
// its result to a VM slot. If a loop reads from a table and stores the result
// in a slot, that slot must be written back on exit.
func TestLivenessBuggyCase_LOAD_ARRAY(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		// LOAD_ARRAY writes to slot 5
		SSAInst{Op: SSA_LOAD_ARRAY, Slot: 5, Type: SSATypeUnknown, Arg1: 0, Arg2: 1},
		// INTRINSIC writes to slot 6
		SSAInst{Op: SSA_INTRINSIC, Slot: 6, Type: SSATypeInt, Arg1: 0},
		// TABLE_LEN writes to slot 7
		SSAInst{Op: SSA_TABLE_LEN, Slot: 7, Type: SSATypeInt, Arg1: 0},
		// LOAD_FIELD writes to slot 8
		SSAInst{Op: SSA_LOAD_FIELD, Slot: 8, Type: SSATypeUnknown, Arg1: 0},
	)
	li := AnalyzeLiveness(f)

	for _, slot := range []int{5, 6, 7, 8} {
		if !li.WrittenSlots[slot] {
			t.Errorf("BUG REGRESSION: slot %d missing from WrittenSlots", slot)
		}
	}
}

// TestLivenessBuggyCase_UNBOX verifies that UNBOX_INT / UNBOX_FLOAT inside
// the loop body are tracked. In the old code, these were completely ignored,
// which could cause issues when a slot is loaded+unboxed inside the loop.
func TestLivenessBuggyCase_UNBOX(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_UNBOX_INT, Slot: 2, Type: SSATypeInt, Arg1: 0},
		SSAInst{Op: SSA_UNBOX_FLOAT, Slot: 3, Type: SSATypeFloat, Arg1: 0},
		SSAInst{Op: SSA_BOX_INT, Slot: 4, Type: SSATypeInt, Arg1: 0},
		SSAInst{Op: SSA_BOX_FLOAT, Slot: 5, Type: SSATypeFloat, Arg1: 0},
	)
	li := AnalyzeLiveness(f)

	if !li.WrittenSlots[2] {
		t.Errorf("UNBOX_INT slot 2 should be in WrittenSlots")
	}
	if li.SlotTypes[2] != SSATypeInt {
		t.Errorf("UNBOX_INT slot 2 should be SSATypeInt, got %v", li.SlotTypes[2])
	}
	if !li.WrittenSlots[3] {
		t.Errorf("UNBOX_FLOAT slot 3 should be in WrittenSlots")
	}
	if li.SlotTypes[3] != SSATypeFloat {
		t.Errorf("UNBOX_FLOAT slot 3 should be SSATypeFloat, got %v", li.SlotTypes[3])
	}
	if !li.WrittenSlots[4] {
		t.Errorf("BOX_INT slot 4 should be in WrittenSlots")
	}
	if !li.WrittenSlots[5] {
		t.Errorf("BOX_FLOAT slot 5 should be in WrittenSlots")
	}
}

// TestLivenessNegativeSlot verifies that instructions with Slot < 0
// (e.g., constants from the pool, not bound to a VM register) are excluded.
func TestLivenessNegativeSlot(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_CONST_INT, Slot: -1, Type: SSATypeInt, AuxInt: 99},
		SSAInst{Op: SSA_CONST_FLOAT, Slot: -1, Type: SSATypeFloat},
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
	)
	li := AnalyzeLiveness(f)
	// Slot -1 should not appear
	if li.WrittenSlots[-1] {
		t.Errorf("slot -1 should NOT be in WrittenSlots")
	}
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots")
	}
}

// TestLivenessNoLoop handles edge case: no SSA_LOOP marker at all.
// Should return empty WrittenSlots.
func TestLivenessNoLoop(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
		SSAInst{Op: SSA_ADD_INT, Slot: 1, Type: SSATypeInt},
	)
	li := AnalyzeLiveness(f)
	if len(li.WrittenSlots) != 0 {
		t.Errorf("expected empty WrittenSlots when no SSA_LOOP marker, got %v", li.WrittenSlots)
	}
}

// TestLivenessNeedsStoreBack verifies the NeedsStoreBack helper.
func TestLivenessNeedsStoreBack(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_ADD_INT, Slot: 3, Type: SSATypeInt},
		SSAInst{Op: SSA_MUL_FLOAT, Slot: 7, Type: SSATypeFloat},
	)
	li := AnalyzeLiveness(f)
	if !li.NeedsStoreBack(3) {
		t.Errorf("NeedsStoreBack(3) should return true")
	}
	if !li.NeedsStoreBack(7) {
		t.Errorf("NeedsStoreBack(7) should return true")
	}
	if li.NeedsStoreBack(0) {
		t.Errorf("NeedsStoreBack(0) should return false (not written)")
	}
	if li.NeedsStoreBack(99) {
		t.Errorf("NeedsStoreBack(99) should return false (not written)")
	}
}

// TestLivenessComparisonOpsExcluded verifies that comparison ops (EQ_INT,
// LT_INT, LE_INT, etc.) are NOT in WrittenSlots — they produce booleans
// for guards, not values for VM slots.
func TestLivenessComparisonOpsExcluded(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_ADD_INT, Slot: 0, Type: SSATypeInt},
		// Comparisons — these have Slot fields but don't write values to VM slots
		SSAInst{Op: SSA_EQ_INT, Slot: 0, Type: SSATypeBool},
		SSAInst{Op: SSA_LT_INT, Slot: 0, Type: SSATypeBool},
		SSAInst{Op: SSA_LE_INT, Slot: 0, Type: SSATypeBool},
		SSAInst{Op: SSA_LT_FLOAT, Slot: 0, Type: SSATypeBool},
		SSAInst{Op: SSA_LE_FLOAT, Slot: 0, Type: SSATypeBool},
		SSAInst{Op: SSA_GT_FLOAT, Slot: 0, Type: SSATypeBool},
	)
	li := AnalyzeLiveness(f)
	// Slot 0 should be in WrittenSlots only because of ADD_INT,
	// and its type should be SSATypeInt (not SSATypeBool from comparisons)
	if !li.WrittenSlots[0] {
		t.Errorf("slot 0 should be in WrittenSlots from ADD_INT")
	}
	if li.SlotTypes[0] != SSATypeInt {
		t.Errorf("slot 0 type should be SSATypeInt (from ADD_INT), got %v", li.SlotTypes[0])
	}
}

// TestLivenessStoreOpsExcluded verifies that STORE_SLOT, STORE_FIELD,
// STORE_ARRAY are NOT in WrittenSlots — they write to memory, not to a
// "produced value" for a VM slot.
func TestLivenessStoreOpsExcluded(t *testing.T) {
	f := mkSSAFunc(
		SSAInst{Op: SSA_LOOP},
		SSAInst{Op: SSA_STORE_SLOT, Slot: 10},
		SSAInst{Op: SSA_STORE_FIELD, Slot: 11},
		SSAInst{Op: SSA_STORE_ARRAY, Slot: 12},
	)
	li := AnalyzeLiveness(f)
	for _, slot := range []int{10, 11, 12} {
		if li.WrittenSlots[slot] {
			t.Errorf("slot %d should NOT be in WrittenSlots (store ops write to memory, not produce values)", slot)
		}
	}
}
