//go:build darwin && arm64

package jit

import (
	"testing"
)

// helper: find the index of the SSA_LOOP marker in a function.
func findLoopIndex(f *SSAFunc) int {
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			return i
		}
	}
	return -1
}

func TestConstHoist_MovesConstsBeforeLoop(t *testing.T) {
	// Setup: GUARD, LOOP, CONST_INT(42), ADD_INT using that const
	// Slot=-1 means pool constant (not bound to VM slot) — safe to hoist.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone},
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 1}, // uses GUARD(0) and CONST(2)
		},
	}

	result := ConstHoist(f)

	loopIdx := findLoopIndex(result)
	if loopIdx < 0 {
		t.Fatal("LOOP marker missing after ConstHoist")
	}

	// The CONST_INT should now be before LOOP
	constIdx := -1
	for i, inst := range result.Insts {
		if inst.Op == SSA_CONST_INT && inst.AuxInt == 42 {
			constIdx = i
			break
		}
	}
	if constIdx < 0 {
		t.Fatal("CONST_INT(42) missing after ConstHoist")
	}
	if constIdx >= loopIdx {
		t.Errorf("CONST_INT at index %d should be before LOOP at index %d", constIdx, loopIdx)
	}
}

func TestConstHoist_UpdatesRefsAfterReindex(t *testing.T) {
	// Setup: LOAD_SLOT(0), LOOP(1), CONST_INT(2), ADD_INT(3) refs LOAD(0)+CONST(2)
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},       // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                        // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 10, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 2
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 1},                            // idx 3: uses LOAD(0)+CONST(2)
		},
	}

	result := ConstHoist(f)

	// After hoisting: LOAD_SLOT(0), CONST_INT(1), LOOP(2), ADD_INT(3)
	// The ADD_INT should now reference CONST_INT at index 1 (was 2)
	loopIdx := findLoopIndex(result)
	if loopIdx != 2 {
		t.Fatalf("expected LOOP at index 2, got %d", loopIdx)
	}

	// The ADD_INT should be at index 3
	addInst := result.Insts[3]
	if addInst.Op != SSA_ADD_INT {
		t.Fatalf("expected ADD_INT at index 3, got op %d", addInst.Op)
	}
	// Arg1 should still point to LOAD_SLOT (index 0 unchanged)
	if addInst.Arg1 != 0 {
		t.Errorf("ADD_INT.Arg1 = %d, want 0 (LOAD_SLOT)", addInst.Arg1)
	}
	// Arg2 should now point to CONST_INT (moved from index 2 to index 1)
	if addInst.Arg2 != 1 {
		t.Errorf("ADD_INT.Arg2 = %d, want 1 (CONST_INT after hoist)", addInst.Arg2)
	}
}

func TestConstHoist_NonConstStaysInLoop(t *testing.T) {
	// Verify that non-constant instructions remain in the loop body
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},           // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                            // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 5, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 2 (should hoist)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 1},                                // idx 3 (should stay)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 3, Arg2: 0},                                         // idx 4 (should stay)
		},
	}

	result := ConstHoist(f)
	loopIdx := findLoopIndex(result)

	// ADD_INT and LE_INT must be after LOOP
	for i, inst := range result.Insts {
		if (inst.Op == SSA_ADD_INT || inst.Op == SSA_LE_INT) && i <= loopIdx {
			t.Errorf("op %d at index %d should be after LOOP at %d", inst.Op, i, loopIdx)
		}
	}
}

func TestConstHoist_NoConstants_NoOp(t *testing.T) {
	// When there are no constants in the loop body, nothing should change
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0, Slot: 1},
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 2, Arg2: 0},
		},
	}

	result := ConstHoist(f)

	// Everything should stay the same
	if len(result.Insts) != len(f.Insts) {
		t.Fatalf("instruction count changed: %d -> %d", len(f.Insts), len(result.Insts))
	}
	for i, inst := range result.Insts {
		if inst.Op != f.Insts[i].Op {
			t.Errorf("inst[%d].Op = %d, want %d", i, inst.Op, f.Insts[i].Op)
		}
	}
}

func TestConstHoist_NoLoop_NoOp(t *testing.T) {
	// If there's no LOOP marker, return unchanged
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 1, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0, Slot: 1},
		},
	}

	result := ConstHoist(f)

	if len(result.Insts) != 2 {
		t.Fatalf("instruction count changed: %d", len(result.Insts))
	}
}

func TestConstHoist_MixedIntAndFloat(t *testing.T) {
	// Both int and float constants should be hoisted (when Slot=-1)
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},              // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                                 // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 2, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},    // idx 2 (hoist)
			{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: 100, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 3 (hoist)
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 3, Slot: 1},                                 // idx 4: uses LOAD(0)+CONST_FLOAT(3)
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 4, Arg2: 3, Slot: 2},                                 // idx 5: uses ADD(4)+CONST_FLOAT(3)
		},
	}

	result := ConstHoist(f)
	loopIdx := findLoopIndex(result)

	// Both constants should be before LOOP
	constsBefore := 0
	for i, inst := range result.Insts {
		if i < loopIdx && (inst.Op == SSA_CONST_INT || inst.Op == SSA_CONST_FLOAT) {
			constsBefore++
		}
	}
	if constsBefore != 2 {
		t.Errorf("expected 2 constants before LOOP, got %d", constsBefore)
	}

	// After hoist, order should be: LOAD(0), CONST_INT(1), CONST_FLOAT(2), LOOP(3), ADD(4), MUL(5)
	// ADD should reference LOAD(0) and CONST_FLOAT(2)
	addIdx := -1
	for i, inst := range result.Insts {
		if inst.Op == SSA_ADD_FLOAT {
			addIdx = i
			break
		}
	}
	if addIdx < 0 {
		t.Fatal("ADD_FLOAT missing")
	}
	addInst := result.Insts[addIdx]
	if addInst.Arg1 != 0 {
		t.Errorf("ADD_FLOAT.Arg1 = %d, want 0 (LOAD_SLOT)", addInst.Arg1)
	}
	if addInst.Arg2 != 2 {
		t.Errorf("ADD_FLOAT.Arg2 = %d, want 2 (CONST_FLOAT after hoist)", addInst.Arg2)
	}

	// MUL should reference ADD(4) and CONST_FLOAT(2)
	mulIdx := -1
	for i, inst := range result.Insts {
		if inst.Op == SSA_MUL_FLOAT {
			mulIdx = i
			break
		}
	}
	if mulIdx < 0 {
		t.Fatal("MUL_FLOAT missing")
	}
	mulInst := result.Insts[mulIdx]
	if mulInst.Arg1 != SSARef(addIdx) {
		t.Errorf("MUL_FLOAT.Arg1 = %d, want %d (ADD_FLOAT)", mulInst.Arg1, addIdx)
	}
	if mulInst.Arg2 != 2 {
		t.Errorf("MUL_FLOAT.Arg2 = %d, want 2 (CONST_FLOAT after hoist)", mulInst.Arg2)
	}
}

func TestConstHoist_StoreArrayAuxIntRef(t *testing.T) {
	// SSA_STORE_ARRAY stores a value ref in AuxInt — must be updated on reindex
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeTable, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},         // idx 0: table
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                            // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 1, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},  // idx 2: key (hoist)
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 99, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 3: value (hoist)
			{Op: SSA_STORE_ARRAY, Type: SSATypeUnknown, Arg1: 0, Arg2: 2, AuxInt: 3, Slot: 0},             // idx 4: table[key]=value, AuxInt=valRef
		},
	}

	result := ConstHoist(f)

	// After hoist: LOAD(0), CONST(1), CONST(2), LOOP(3), STORE_ARRAY(4)
	// STORE_ARRAY.Arg1 should be 0 (LOAD unchanged)
	// STORE_ARRAY.Arg2 should be 1 (key const moved from 2->1)
	// STORE_ARRAY.AuxInt should be 2 (value const moved from 3->2)
	storeIdx := -1
	for i, inst := range result.Insts {
		if inst.Op == SSA_STORE_ARRAY {
			storeIdx = i
			break
		}
	}
	if storeIdx < 0 {
		t.Fatal("STORE_ARRAY missing")
	}
	storeInst := result.Insts[storeIdx]
	if storeInst.Arg1 != 0 {
		t.Errorf("STORE_ARRAY.Arg1 = %d, want 0", storeInst.Arg1)
	}
	if storeInst.Arg2 != 1 {
		t.Errorf("STORE_ARRAY.Arg2 = %d, want 1", storeInst.Arg2)
	}
	if SSARef(storeInst.AuxInt) != 2 {
		t.Errorf("STORE_ARRAY.AuxInt = %d, want 2", storeInst.AuxInt)
	}
}

func TestConstHoist_ConstAlreadyBeforeLoop(t *testing.T) {
	// Constants already before LOOP should not be moved or duplicated
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 7, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 0: already pre-loop
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                              // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 2: in loop (should hoist)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 1},                                  // idx 3
		},
	}

	result := ConstHoist(f)
	loopIdx := findLoopIndex(result)

	// Should have 2 constants before LOOP, 0 after
	constsBefore := 0
	constsAfter := 0
	for i, inst := range result.Insts {
		if inst.Op == SSA_CONST_INT {
			if i < loopIdx {
				constsBefore++
			} else {
				constsAfter++
			}
		}
	}
	if constsBefore != 2 {
		t.Errorf("expected 2 constants before LOOP, got %d", constsBefore)
	}
	if constsAfter != 0 {
		t.Errorf("expected 0 constants after LOOP, got %d", constsAfter)
	}
}

func TestConstHoist_PreservesSSARefNone(t *testing.T) {
	// SSARefNone (-32768) should not be reindexed
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 1, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: 1, Slot: 0},
		},
	}

	result := ConstHoist(f)

	// After hoist: CONST_INT(0), LOOP(1), ADD_INT(2)
	addInst := result.Insts[2]
	if addInst.Arg1 != SSARefNone {
		t.Errorf("SSARefNone was incorrectly reindexed to %d", addInst.Arg1)
	}
}

func TestConstHoist_UpdatesSnapshotRefs(t *testing.T) {
	// Snapshots contain SSARef entries that must be remapped after hoisting
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},              // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                               // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 10, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 2 (hoist)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 1},                                   // idx 3
			{Op: SSA_SNAPSHOT, AuxInt: 0},                                                                      // idx 4: snapshot index 0
		},
		Snapshots: []Snapshot{
			{PC: 10, Entries: []SnapEntry{
				{Slot: 0, Ref: 0, Type: SSATypeInt}, // points to LOAD_SLOT(0)
				{Slot: 1, Ref: 3, Type: SSATypeInt}, // points to ADD_INT(3)
			}},
		},
	}

	result := ConstHoist(f)

	// After hoist: LOAD(0), CONST(1), LOOP(2), ADD(3), SNAPSHOT(4)
	// Snapshot entry for slot 0: ref should still be 0 (LOAD_SLOT unchanged)
	// Snapshot entry for slot 1: ref should be 3 (ADD_INT moved from idx 3 to idx 3, no change in this case)
	if len(result.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(result.Snapshots))
	}
	snap := result.Snapshots[0]
	if snap.Entries[0].Ref != 0 {
		t.Errorf("snapshot entry 0 ref = %d, want 0 (LOAD_SLOT)", snap.Entries[0].Ref)
	}
	if snap.Entries[1].Ref != 3 {
		t.Errorf("snapshot entry 1 ref = %d, want 3 (ADD_INT)", snap.Entries[1].Ref)
	}
}

func TestConstHoist_UpdatesSnapshotRefsToConst(t *testing.T) {
	// Snapshot that references a constant being hoisted
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},               // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                                // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},  // idx 2 (hoist)
			{Op: SSA_SNAPSHOT, AuxInt: 0},                                                                       // idx 3: snapshot index 0
		},
		Snapshots: []Snapshot{
			{PC: 10, Entries: []SnapEntry{
				{Slot: 0, Ref: 2, Type: SSATypeInt}, // points to CONST_INT(2)
			}},
		},
	}

	result := ConstHoist(f)

	// After hoist: LOAD(0), CONST(1), LOOP(2), SNAPSHOT(3)
	// Snapshot entry for slot 0: ref was 2 (CONST), now at index 1
	snap := result.Snapshots[0]
	if snap.Entries[0].Ref != 1 {
		t.Errorf("snapshot entry 0 ref = %d, want 1 (CONST_INT after hoist)", snap.Entries[0].Ref)
	}
}

func TestConstHoist_UpdatesFMADDAuxIntRef(t *testing.T) {
	// FMADD/FMSUB use AuxInt as a third operand ref — must be remapped
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},                  // idx 0
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1, Arg1: SSARefNone, Arg2: SSARefNone},                  // idx 1
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                                     // idx 2
			{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: 100, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 3 (hoist)
			{Op: SSA_FMADD, Type: SSATypeFloat, Arg1: 0, Arg2: 1, AuxInt: 3, Slot: 2},                             // idx 4: a*b+c where c=CONST(3)
		},
	}

	result := ConstHoist(f)

	// After hoist: LOAD(0), LOAD(1), CONST(2), LOOP(3), FMADD(4)
	// FMADD.AuxInt should be 2 (CONST moved from 3->2)
	fmaddIdx := -1
	for i, inst := range result.Insts {
		if inst.Op == SSA_FMADD {
			fmaddIdx = i
			break
		}
	}
	if fmaddIdx < 0 {
		t.Fatal("FMADD missing")
	}
	fmadd := result.Insts[fmaddIdx]
	if SSARef(fmadd.AuxInt) != 2 {
		t.Errorf("FMADD.AuxInt = %d, want 2 (CONST_FLOAT after hoist)", fmadd.AuxInt)
	}
}

func TestConstHoist_UpdatesLoopIdx(t *testing.T) {
	// LoopIdx on the SSAFunc struct should be updated after hoisting
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},                  // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                                   // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 5, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1},      // idx 2 (hoist)
			{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: 7, Arg1: SSARefNone, Arg2: SSARefNone, Slot: -1}, // idx 3 (hoist)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 1},                                       // idx 4
		},
		LoopIdx: 1,
	}

	result := ConstHoist(f)

	// After hoist: LOAD(0), CONST_INT(1), CONST_FLOAT(2), LOOP(3), ADD_INT(4)
	// LoopIdx should be 3
	if result.LoopIdx != 3 {
		t.Errorf("LoopIdx = %d, want 3", result.LoopIdx)
	}
}

func TestConstHoist_SlotBoundConstNotHoisted(t *testing.T) {
	// Constants with Slot >= 0 (bound to a VM slot) must NOT be hoisted.
	// They need to stay in the loop body so emitConstInt writes to the slot,
	// which is required for correct interpreter state after side-exit.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0, Arg1: SSARefNone, Arg2: SSARefNone},              // idx 0
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},                                               // idx 1
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 20, Arg1: SSARefNone, Arg2: SSARefNone, Slot: 5},  // idx 2: bound to slot 5, NOT hoisted
			{Op: SSA_EQ_INT, Type: SSATypeBool, Arg1: 0, Arg2: 2, AuxInt: 0},                                 // idx 3
		},
	}

	result := ConstHoist(f)

	// Nothing should be hoisted (only slot-bound constants in loop body)
	if len(result.Insts) != len(f.Insts) {
		t.Fatalf("instruction count changed: %d -> %d", len(f.Insts), len(result.Insts))
	}
	loopIdx := findLoopIndex(result)
	// CONST_INT should still be after LOOP
	for i, inst := range result.Insts {
		if inst.Op == SSA_CONST_INT && i <= loopIdx {
			t.Errorf("slot-bound CONST_INT was incorrectly hoisted to index %d (LOOP at %d)", i, loopIdx)
		}
	}
}
