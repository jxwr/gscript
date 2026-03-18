//go:build darwin && arm64

package jit

import (
	"testing"
)

// countNOPsAfterLoop counts NOP instructions after the LOOP marker.
func countNOPsAfterLoop(f *SSAFunc) int {
	count := 0
	loopSeen := false
	for _, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopSeen = true
			continue
		}
		if loopSeen && inst.Op == SSA_NOP {
			count++
		}
	}
	return count
}

func TestCSE_DuplicateMulInt(t *testing.T) {
	// Two identical MUL_INT(ref0, ref1) instructions.
	// The second should become NOP and all references should be updated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOAD_SLOT y
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
			// 2: LOOP
			{Op: SSA_LOOP},
			// 3: MUL_INT x*y (first)
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 2},
			// 4: MUL_INT x*y (duplicate)
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 3},
			// 5: ADD_INT (first_mul + dup_mul) — uses both results
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 3, Arg2: 4, Slot: 4},
			// 6: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 5, Arg2: 0},
		},
	}

	result := CSE(f)

	// Instruction 4 (duplicate MUL_INT) should be NOP
	if result.Insts[4].Op != SSA_NOP {
		t.Errorf("inst 4: expected SSA_NOP, got %d", result.Insts[4].Op)
	}

	// Instruction 3 (first MUL_INT) should still be MUL_INT
	if result.Insts[3].Op != SSA_MUL_INT {
		t.Errorf("inst 3: expected SSA_MUL_INT, got %d", result.Insts[3].Op)
	}

	// Instruction 5 (ADD_INT) should now reference inst 3 for BOTH args
	// (was Arg1=3, Arg2=4; after CSE, Arg2 should be rewritten from 4 to 3)
	if result.Insts[5].Arg2 != 3 {
		t.Errorf("inst 5 Arg2: expected 3 (rewritten from 4), got %d", result.Insts[5].Arg2)
	}
}

func TestCSE_DifferentArgsMulInt(t *testing.T) {
	// Two MUL_INT with different operands — both should be kept.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOAD_SLOT y
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
			// 2: LOAD_SLOT z
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
			// 3: LOOP
			{Op: SSA_LOOP},
			// 4: MUL_INT x*y
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 3},
			// 5: MUL_INT x*z (different arg2)
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 2, Slot: 4},
			// 6: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 4, Arg2: 5},
		},
	}

	result := CSE(f)

	// Both MUL_INTs should remain (not NOP'd)
	if result.Insts[4].Op != SSA_MUL_INT {
		t.Errorf("inst 4: expected SSA_MUL_INT, got %d", result.Insts[4].Op)
	}
	if result.Insts[5].Op != SSA_MUL_INT {
		t.Errorf("inst 5: expected SSA_MUL_INT, got %d", result.Insts[5].Op)
	}
}

func TestCSE_DuplicateConstants(t *testing.T) {
	// Two CONST_INT with the same value should be deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOOP
			{Op: SSA_LOOP},
			// 1: CONST_INT 42
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: 0},
			// 2: CONST_INT 42 (duplicate)
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: 1},
			// 3: ADD_INT using both constants
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 2, Slot: 2},
			// 4: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 3, Arg2: 1},
		},
	}

	result := CSE(f)

	// Instruction 2 (duplicate CONST_INT 42) should be NOP
	if result.Insts[2].Op != SSA_NOP {
		t.Errorf("inst 2: expected SSA_NOP, got %d", result.Insts[2].Op)
	}

	// Instruction 3 (ADD_INT) Arg2 should be rewritten from 2 to 1
	if result.Insts[3].Arg2 != 1 {
		t.Errorf("inst 3 Arg2: expected 1 (rewritten from 2), got %d", result.Insts[3].Arg2)
	}
}

func TestCSE_SideEffectingOpsNotDeduplicated(t *testing.T) {
	// GUARD_TYPE instructions with same args must NOT be deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOOP
			{Op: SSA_LOOP},
			// 2: GUARD_TYPE (first)
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 0, AuxInt: 1},
			// 3: GUARD_TYPE (same args — must NOT be deduplicated)
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 0, AuxInt: 1},
			// 4: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 0, Arg2: 0},
		},
	}

	result := CSE(f)

	// Both GUARD_TYPE should remain
	if result.Insts[2].Op != SSA_GUARD_TYPE {
		t.Errorf("inst 2: expected SSA_GUARD_TYPE, got %d", result.Insts[2].Op)
	}
	if result.Insts[3].Op != SSA_GUARD_TYPE {
		t.Errorf("inst 3: expected SSA_GUARD_TYPE, got %d", result.Insts[3].Op)
	}
}

func TestCSE_NoDuplicates(t *testing.T) {
	// No duplicates — nothing should change.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOAD_SLOT y
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
			// 2: LOOP
			{Op: SSA_LOOP},
			// 3: ADD_INT x+y
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 2},
			// 4: SUB_INT x-y (different op)
			{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 3},
			// 5: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 3, Arg2: 4},
		},
	}

	result := CSE(f)

	// No NOP should be introduced after LOOP
	nops := countNOPsAfterLoop(result)
	if nops != 0 {
		t.Errorf("expected 0 NOPs after LOOP, got %d", nops)
	}
}

func TestCSE_DependencyChain(t *testing.T) {
	// Chain of dependencies:
	//   inst3: A = X * Y
	//   inst4: B = A + Z
	//   inst5: C = X * Y   (duplicate of A → should become NOP)
	//   inst6: D = C + Z   (after rewrite, C→A, so D = A + Z = same as B → also NOP)
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOAD_SLOT y
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
			// 2: LOAD_SLOT z
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
			// 3: LOOP
			{Op: SSA_LOOP},
			// 4: A = MUL_INT(x, y)
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 3},
			// 5: B = ADD_INT(A, z)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 4, Arg2: 2, Slot: 4},
			// 6: C = MUL_INT(x, y) — duplicate of A
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1, Slot: 5},
			// 7: D = ADD_INT(C, z) — after rewrite C→A, becomes ADD_INT(A, z) = same as B
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 6, Arg2: 2, Slot: 6},
			// 8: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 7, Arg2: 0},
		},
	}

	result := CSE(f)

	// inst6 (C = MUL_INT(x,y)) should be NOP (duplicate of inst4)
	if result.Insts[6].Op != SSA_NOP {
		t.Errorf("inst 6 (duplicate MUL): expected SSA_NOP, got %d", result.Insts[6].Op)
	}

	// inst7 (D = ADD_INT(C, z)) after rewrite becomes ADD_INT(A, z) = same as inst5
	// So inst7 should also be NOP
	if result.Insts[7].Op != SSA_NOP {
		t.Errorf("inst 7 (duplicate ADD after rewrite): expected SSA_NOP, got %d", result.Insts[7].Op)
	}

	// inst4 (A = MUL_INT) and inst5 (B = ADD_INT) should remain
	if result.Insts[4].Op != SSA_MUL_INT {
		t.Errorf("inst 4 (original MUL): expected SSA_MUL_INT, got %d", result.Insts[4].Op)
	}
	if result.Insts[5].Op != SSA_ADD_INT {
		t.Errorf("inst 5 (original ADD): expected SSA_ADD_INT, got %d", result.Insts[5].Op)
	}

	// The LE_INT at inst8 should have Arg1 rewritten from 7 to 5
	// (D was replaced by B)
	if result.Insts[8].Arg1 != 5 {
		t.Errorf("inst 8 Arg1: expected 5 (rewritten from 7), got %d", result.Insts[8].Arg1)
	}
}

func TestCSE_LoadSlotNotDeduplicated(t *testing.T) {
	// Two LOAD_SLOT from the same slot must NOT be deduplicated
	// because the slot may be modified between loads.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOOP
			{Op: SSA_LOOP},
			// 1: LOAD_SLOT slot 0
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 2: LOAD_SLOT slot 0 (same slot — must NOT be deduplicated)
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 3: ADD_INT
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 2, Slot: 1},
			// 4: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 3, Arg2: 1},
		},
	}

	result := CSE(f)

	// Both LOAD_SLOT should remain
	if result.Insts[1].Op != SSA_LOAD_SLOT {
		t.Errorf("inst 1: expected SSA_LOAD_SLOT, got %d", result.Insts[1].Op)
	}
	if result.Insts[2].Op != SSA_LOAD_SLOT {
		t.Errorf("inst 2: expected SSA_LOAD_SLOT, got %d", result.Insts[2].Op)
	}
}

func TestCSE_LoadFieldNotDeduplicated(t *testing.T) {
	// LOAD_FIELD must not be deduplicated (reads mutable memory).
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT (table)
			{Op: SSA_LOAD_SLOT, Type: SSATypeTable, Slot: 0},
			// 1: LOOP
			{Op: SSA_LOOP},
			// 2: LOAD_FIELD
			{Op: SSA_LOAD_FIELD, Type: SSATypeUnknown, Arg1: 0, AuxInt: 1},
			// 3: LOAD_FIELD (same — must NOT be deduplicated)
			{Op: SSA_LOAD_FIELD, Type: SSATypeUnknown, Arg1: 0, AuxInt: 1},
			// 4: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 2, Arg2: 3},
		},
	}

	result := CSE(f)

	if result.Insts[2].Op != SSA_LOAD_FIELD {
		t.Errorf("inst 2: expected SSA_LOAD_FIELD, got %d", result.Insts[2].Op)
	}
	if result.Insts[3].Op != SSA_LOAD_FIELD {
		t.Errorf("inst 3: expected SSA_LOAD_FIELD, got %d", result.Insts[3].Op)
	}
}

func TestCSE_FloatArithmetic(t *testing.T) {
	// Duplicate MUL_FLOAT should be deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x (float)
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 0},
			// 1: LOAD_SLOT y (float)
			{Op: SSA_LOAD_SLOT, Type: SSATypeFloat, Slot: 1},
			// 2: LOOP
			{Op: SSA_LOOP},
			// 3: MUL_FLOAT x*y
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 2},
			// 4: MUL_FLOAT x*y (duplicate)
			{Op: SSA_MUL_FLOAT, Type: SSATypeFloat, Arg1: 0, Arg2: 1, Slot: 3},
			// 5: ADD_FLOAT
			{Op: SSA_ADD_FLOAT, Type: SSATypeFloat, Arg1: 3, Arg2: 4, Slot: 4},
			// 6: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 5, Arg2: 0},
		},
	}

	result := CSE(f)

	// inst4 (duplicate MUL_FLOAT) should be NOP
	if result.Insts[4].Op != SSA_NOP {
		t.Errorf("inst 4: expected SSA_NOP, got %d", result.Insts[4].Op)
	}

	// inst5 (ADD_FLOAT) Arg2 should be rewritten from 4 to 3
	if result.Insts[5].Arg2 != 3 {
		t.Errorf("inst 5 Arg2: expected 3 (rewritten from 4), got %d", result.Insts[5].Arg2)
	}
}

func TestCSE_StoreArrayAuxIntRewritten(t *testing.T) {
	// STORE_ARRAY uses AuxInt as SSARef for the value.
	// CSE must rewrite AuxInt when the referenced instruction is deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOAD_SLOT table
			{Op: SSA_LOAD_SLOT, Type: SSATypeTable, Slot: 1},
			// 2: LOAD_SLOT key
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 2},
			// 3: LOOP
			{Op: SSA_LOOP},
			// 4: MUL_INT x*x
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0, Slot: 3},
			// 5: MUL_INT x*x (duplicate)
			{Op: SSA_MUL_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0, Slot: 4},
			// 6: STORE_ARRAY table[key] = duplicate_result (AuxInt = ref to inst 5)
			{Op: SSA_STORE_ARRAY, Type: SSATypeUnknown, Arg1: 1, Arg2: 2, AuxInt: 5},
			// 7: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 4, Arg2: 0},
		},
	}

	result := CSE(f)

	// inst5 should be NOP (duplicate MUL)
	if result.Insts[5].Op != SSA_NOP {
		t.Errorf("inst 5: expected SSA_NOP, got %d", result.Insts[5].Op)
	}

	// STORE_ARRAY AuxInt should be rewritten from 5 to 4
	if result.Insts[6].AuxInt != 4 {
		t.Errorf("STORE_ARRAY AuxInt: expected 4 (rewritten from 5), got %d", result.Insts[6].AuxInt)
	}
}

func TestCSE_PreLoopReferencesRewritten(t *testing.T) {
	// References in pre-loop instructions should also be rewritten.
	// This tests that the rewrite pass covers ALL instructions.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOOP
			{Op: SSA_LOOP},
			// 2: CONST_INT 42
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: 1},
			// 3: CONST_INT 42 (duplicate)
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: 2},
			// 4: ADD_INT using duplicate constant
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 3, Slot: 3},
			// 5: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 4, Arg2: 2},
		},
	}

	result := CSE(f)

	// inst3 (duplicate CONST_INT 42) should be NOP
	if result.Insts[3].Op != SSA_NOP {
		t.Errorf("inst 3: expected SSA_NOP, got %d", result.Insts[3].Op)
	}

	// inst4 Arg2 should be rewritten from 3 to 2
	if result.Insts[4].Arg2 != 2 {
		t.Errorf("inst 4 Arg2: expected 2 (rewritten from 3), got %d", result.Insts[4].Arg2)
	}
}

func TestCSE_ConstantsDifferentValues(t *testing.T) {
	// CONST_INT with different values must NOT be deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOOP
			{Op: SSA_LOOP},
			// 1: CONST_INT 42
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 42, Slot: 0},
			// 2: CONST_INT 99 (different value)
			{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: 99, Slot: 1},
			// 3: ADD_INT
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 2, Slot: 2},
			// 4: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 3, Arg2: 1},
		},
	}

	result := CSE(f)

	// Both constants should remain
	if result.Insts[1].Op != SSA_CONST_INT {
		t.Errorf("inst 1: expected SSA_CONST_INT, got %d", result.Insts[1].Op)
	}
	if result.Insts[2].Op != SSA_CONST_INT {
		t.Errorf("inst 2: expected SSA_CONST_INT, got %d", result.Insts[2].Op)
	}
}

func TestCSE_UnboxDeduplicated(t *testing.T) {
	// UNBOX_INT with same Arg1 should be deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOOP
			{Op: SSA_LOOP},
			// 2: UNBOX_INT
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 1},
			// 3: UNBOX_INT (duplicate)
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Slot: 2},
			// 4: ADD_INT
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 2, Arg2: 3, Slot: 3},
			// 5: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 4, Arg2: 2},
		},
	}

	result := CSE(f)

	if result.Insts[3].Op != SSA_NOP {
		t.Errorf("inst 3: expected SSA_NOP, got %d", result.Insts[3].Op)
	}
	if result.Insts[4].Arg2 != 2 {
		t.Errorf("inst 4 Arg2: expected 2 (rewritten from 3), got %d", result.Insts[4].Arg2)
	}
}

func TestCSE_MoveNotDeduplicated(t *testing.T) {
	// MOVE instructions must NOT be deduplicated.
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT x
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			// 1: LOOP
			{Op: SSA_LOOP},
			// 2: MOVE
			{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 0, Slot: 1},
			// 3: MOVE (same args)
			{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 0, Slot: 2},
			// 4: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 2, Arg2: 3},
		},
	}

	result := CSE(f)

	if result.Insts[2].Op != SSA_MOVE {
		t.Errorf("inst 2: expected SSA_MOVE, got %d", result.Insts[2].Op)
	}
	if result.Insts[3].Op != SSA_MOVE {
		t.Errorf("inst 3: expected SSA_MOVE, got %d", result.Insts[3].Op)
	}
}

func TestCSE_LoadArrayNotDeduplicated(t *testing.T) {
	// LOAD_ARRAY must not be deduplicated (reads mutable memory).
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT (table)
			{Op: SSA_LOAD_SLOT, Type: SSATypeTable, Slot: 0},
			// 1: LOAD_SLOT (key)
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 1},
			// 2: LOOP
			{Op: SSA_LOOP},
			// 3: LOAD_ARRAY
			{Op: SSA_LOAD_ARRAY, Type: SSATypeUnknown, Arg1: 0, Arg2: 1},
			// 4: LOAD_ARRAY (same — must NOT be deduplicated)
			{Op: SSA_LOAD_ARRAY, Type: SSATypeUnknown, Arg1: 0, Arg2: 1},
			// 5: LE_INT (loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 3, Arg2: 4},
		},
	}

	result := CSE(f)

	if result.Insts[3].Op != SSA_LOAD_ARRAY {
		t.Errorf("inst 3: expected SSA_LOAD_ARRAY, got %d", result.Insts[3].Op)
	}
	if result.Insts[4].Op != SSA_LOAD_ARRAY {
		t.Errorf("inst 4: expected SSA_LOAD_ARRAY, got %d", result.Insts[4].Op)
	}
}

func TestCSE_NoLoopMarker(t *testing.T) {
	// If there's no LOOP marker, CSE should return the function unchanged.
	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Type: SSATypeInt, Slot: 0},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0, Slot: 1},
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0, Slot: 2},
		},
	}

	result := CSE(f)

	// Nothing should be NOP'd
	for i, inst := range result.Insts {
		if inst.Op == SSA_NOP {
			t.Errorf("inst %d: unexpected SSA_NOP", i)
		}
	}
}
