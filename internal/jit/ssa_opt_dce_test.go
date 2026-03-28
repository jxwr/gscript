//go:build darwin && arm64

package jit

import (
	"testing"
)

func TestDCE_RemovesDeadArithmetic(t *testing.T) {
	// Dead arithmetic chain: CONST + MUL + ADD where the result is unused.
	// LOAD_GLOBAL/LOAD_FIELD are NOT removable (they write to VM memory).
	f := &SSAFunc{
		Insts: []SSAInst{
			// Pre-loop
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},  // 0
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0},   // 1
			{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: 2},         // 2
			{Op: SSA_LOOP},                                    // 3
			// Loop body — dead arithmetic
			{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: 100, Slot: -1},       // 4: dead
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 4, Arg2: 4, Slot: 10},   // 5: dead (uses 4)
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 5, Arg2: 4, Slot: 11},   // 6: dead (uses 5, 4)
			// Live arithmetic
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 1, Slot: -1},              // 7: alive
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 7, Slot: 0},          // 8: alive
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 8, Arg2: 1, AuxInt: -1},       // 9: guard
		},
		LoopIdx: 3,
	}

	f = DCE(f)

	// Dead arithmetic should be NOP'd
	if f.Insts[4].Op != SSA_NOP {
		t.Errorf("inst[4] CONST_FLOAT should be NOP, got %v", ssaOpString(f.Insts[4].Op))
	}
	if f.Insts[5].Op != SSA_NOP {
		t.Errorf("inst[5] MUL_FLOAT should be NOP, got %v", ssaOpString(f.Insts[5].Op))
	}
	if f.Insts[6].Op != SSA_NOP {
		t.Errorf("inst[6] ADD_FLOAT should be NOP, got %v", ssaOpString(f.Insts[6].Op))
	}
	// Live instructions survive
	if f.Insts[7].Op != SSA_CONST_INT {
		t.Errorf("inst[7] CONST_INT should survive, got %v", ssaOpString(f.Insts[7].Op))
	}
	if f.Insts[8].Op != SSA_ADD_INT {
		t.Errorf("inst[8] ADD_INT should survive, got %v", ssaOpString(f.Insts[8].Op))
	}
}

func TestDCE_PreservesUsedInstructions(t *testing.T) {
	// All instructions are used — DCE should not remove anything.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},  // 0
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0},   // 1
			{Op: SSA_GUARD_TYPE, Arg1: 0, AuxInt: 2},         // 2
			{Op: SSA_LOOP},                                    // 3
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 1, Slot: -1}, // 4
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 4, Slot: 0}, // 5: uses 1, 4
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 5, Arg2: 1, AuxInt: -1}, // 6: uses 5, 1
		},
		LoopIdx: 3,
	}

	f = DCE(f)

	for i, inst := range f.Insts {
		if inst.Op == SSA_NOP && i != 0 { // LOAD_SLOT at 0 might be dead if not in snapshot
			// Actually LOAD_SLOT[0] is used by UNBOX[1] and GUARD[2], so alive
		}
	}
	// CONST_INT[4] is used by ADD_INT[5], alive
	if f.Insts[4].Op != SSA_CONST_INT {
		t.Errorf("inst[4] should survive, got %v", ssaOpString(f.Insts[4].Op))
	}
	// ADD_INT[5] is used by LE_INT[6], alive
	if f.Insts[5].Op != SSA_ADD_INT {
		t.Errorf("inst[5] should survive, got %v", ssaOpString(f.Insts[5].Op))
	}
}

func TestDCE_PreservesStoreField(t *testing.T) {
	// STORE_FIELD has side effects — must NOT be removed even if result is unused.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOOP},                                                      // 0
			{Op: SSA_LOAD_GLOBAL, Type: SSATypeTable, Slot: 5, AuxInt: 0},      // 1: used by STORE_FIELD
			{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: 0, Slot: -1},     // 2: used by STORE_FIELD
			{Op: SSA_STORE_FIELD, Type: SSATypeUnknown, Arg1: 1, Arg2: 2, Slot: 5, AuxInt: 0}, // 3: side effect
		},
		LoopIdx: 0,
	}

	f = DCE(f)

	if f.Insts[3].Op != SSA_STORE_FIELD {
		t.Errorf("STORE_FIELD should survive, got %v", ssaOpString(f.Insts[3].Op))
	}
	// LOAD_GLOBAL and CONST_FLOAT are used by STORE_FIELD, so they survive too
	if f.Insts[1].Op != SSA_LOAD_GLOBAL {
		t.Errorf("LOAD_GLOBAL used by STORE_FIELD should survive, got %v", ssaOpString(f.Insts[1].Op))
	}
	if f.Insts[2].Op != SSA_CONST_FLOAT {
		t.Errorf("CONST_FLOAT used by STORE_FIELD should survive, got %v", ssaOpString(f.Insts[2].Op))
	}
}

func TestDCE_ChainsOfDeadCode(t *testing.T) {
	// A → B → C where C is dead. Should remove C, then B, then A.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOOP},                                                   // 0
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: -1},     // 1: dead (used only by 2)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 1, Slot: 5}, // 2: dead (used only by 3)
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 2, Arg2: 2, Slot: 6}, // 3: dead (no users)
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 1, Slot: -1},     // 4: alive (used by 5)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 4, Arg2: 4, AuxInt: -1}, // 5: guard (non-removable)
		},
		LoopIdx: 0,
	}

	f = DCE(f)

	if f.Insts[1].Op != SSA_NOP {
		t.Errorf("inst[1] should be dead, got %v", ssaOpString(f.Insts[1].Op))
	}
	if f.Insts[2].Op != SSA_NOP {
		t.Errorf("inst[2] should be dead, got %v", ssaOpString(f.Insts[2].Op))
	}
	if f.Insts[3].Op != SSA_NOP {
		t.Errorf("inst[3] should be dead, got %v", ssaOpString(f.Insts[3].Op))
	}
	if f.Insts[4].Op != SSA_CONST_INT {
		t.Errorf("inst[4] should survive, got %v", ssaOpString(f.Insts[4].Op))
	}
}

func TestDCE_SnapshotKeepsAlive(t *testing.T) {
	// An instruction referenced by a snapshot must NOT be removed.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOOP},                                                 // 0
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 99, Slot: -1},   // 1: no SSA users, but in snapshot
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: -1},   // 2: no users, NOT in snapshot → dead
		},
		Snapshots: []Snapshot{
			{PC: 0, Entries: []SnapEntry{{Slot: 5, Ref: 1, Type: SSATypeInt}}},
		},
		LoopIdx: 0,
	}

	f = DCE(f)

	if f.Insts[1].Op != SSA_CONST_INT {
		t.Errorf("inst[1] referenced by snapshot should survive, got %v", ssaOpString(f.Insts[1].Op))
	}
	if f.Insts[2].Op != SSA_NOP {
		t.Errorf("inst[2] not in snapshot should be dead, got %v", ssaOpString(f.Insts[2].Op))
	}
}
