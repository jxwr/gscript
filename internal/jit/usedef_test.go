//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestUseDef_SimpleChain tests a simple LOAD_SLOT → UNBOX_INT → ADD_INT → guard chain.
func TestUseDef_SimpleChain(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT slot=0
			{Op: SSA_LOAD_SLOT, Type: SSATypeUnknown, Arg1: SSARefNone, Arg2: SSARefNone, Slot: 0},
			// 1: GUARD_TYPE ref=0
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 0, Arg2: SSARefNone, AuxInt: int64(runtime.TypeInt)},
			// 2: UNBOX_INT ref=0
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Arg2: SSARefNone, Slot: 0},
			// 3: LOAD_SLOT slot=1
			{Op: SSA_LOAD_SLOT, Type: SSATypeUnknown, Arg1: SSARefNone, Arg2: SSARefNone, Slot: 1},
			// 4: GUARD_TYPE ref=3
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 3, Arg2: SSARefNone, AuxInt: int64(runtime.TypeInt)},
			// 5: UNBOX_INT ref=3
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 3, Arg2: SSARefNone, Slot: 1},
			// 6: LOOP
			{Op: SSA_LOOP, Arg1: SSARefNone, Arg2: SSARefNone},
			// 7: ADD_INT ref=2, ref=5
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 2, Arg2: 5, Slot: 0},
			// 8: LE_INT ref=7, ref=5 (guard: loop exit)
			{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: 7, Arg2: 5},
		},
	}

	ud := BuildUseDef(f)

	// Instruction 0 (LOAD_SLOT) should be used by: 1 (GUARD_TYPE), 2 (UNBOX_INT)
	if ud.UserCount(0) != 2 {
		t.Errorf("inst 0 UserCount = %d, want 2", ud.UserCount(0))
	}

	// Instruction 2 (UNBOX_INT) should be used by: 7 (ADD_INT)
	if ud.UserCount(2) != 1 {
		t.Errorf("inst 2 UserCount = %d, want 1", ud.UserCount(2))
	}

	// Instruction 5 (UNBOX_INT) should be used by: 7 (ADD_INT), 8 (LE_INT)
	if ud.UserCount(5) != 2 {
		t.Errorf("inst 5 UserCount = %d, want 2", ud.UserCount(5))
	}

	// Instruction 7 (ADD_INT) should be used by: 8 (LE_INT)
	if ud.UserCount(7) != 1 {
		t.Errorf("inst 7 UserCount = %d, want 1", ud.UserCount(7))
	}

	// Instruction 8 (LE_INT) should have no users
	if ud.HasUsers(8) {
		t.Errorf("inst 8 should have no users")
	}

	// Verify actual user refs
	users0 := ud.Users[0]
	if len(users0) != 2 || users0[0] != 1 || users0[1] != 2 {
		t.Errorf("inst 0 users = %v, want [1 2]", users0)
	}
}

// TestUseDef_SSARefNone verifies that SSARefNone operands don't create edges.
func TestUseDef_SSARefNone(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: CONST_INT (no operands)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 42},
			// 1: CONST_INT (no operands)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 10},
			// 2: ADD_INT ref=0, ref=1
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 1},
		},
	}

	ud := BuildUseDef(f)

	// Instruction 0 should be used by 2 (via Arg1)
	if ud.UserCount(0) != 1 {
		t.Errorf("inst 0 UserCount = %d, want 1", ud.UserCount(0))
	}
	// Instruction 1 should be used by 2 (via Arg2)
	if ud.UserCount(1) != 1 {
		t.Errorf("inst 1 UserCount = %d, want 1", ud.UserCount(1))
	}
	// Instruction 2 has no users
	if ud.HasUsers(2) {
		t.Errorf("inst 2 should have no users")
	}
}

// TestUseDef_SlotDefs verifies that SlotDefs maps slots to their defining instructions.
func TestUseDef_SlotDefs(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT slot=0
			{Op: SSA_LOAD_SLOT, Type: SSATypeUnknown, Arg1: SSARefNone, Arg2: SSARefNone, Slot: 0},
			// 1: UNBOX_INT ref=0, slot=0
			{Op: SSA_UNBOX_INT, Type: SSATypeInt, Arg1: 0, Arg2: SSARefNone, Slot: 0},
			// 2: CONST_INT slot=1
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 100, Slot: 1},
			// 3: ADD_INT ref=1, ref=2 → slot=0
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 1, Arg2: 2, Slot: 0},
			// 4: MOVE ref=3 → slot=2
			{Op: SSA_MOVE, Type: SSATypeInt, Arg1: 3, Arg2: SSARefNone, Slot: 2},
		},
	}

	ud := BuildUseDef(f)

	// Slot 0 should be defined by: 0 (LOAD_SLOT), 1 (UNBOX_INT), 3 (ADD_INT)
	defs0 := ud.SlotDefs[0]
	if len(defs0) != 3 {
		t.Fatalf("slot 0 defs count = %d, want 3; defs=%v", len(defs0), defs0)
	}
	if defs0[0] != 0 || defs0[1] != 1 || defs0[2] != 3 {
		t.Errorf("slot 0 defs = %v, want [0 1 3]", defs0)
	}

	// Slot 1 should be defined by: 2 (CONST_INT)
	defs1 := ud.SlotDefs[1]
	if len(defs1) != 1 || defs1[0] != 2 {
		t.Errorf("slot 1 defs = %v, want [2]", defs1)
	}

	// Slot 2 should be defined by: 4 (MOVE)
	defs2 := ud.SlotDefs[2]
	if len(defs2) != 1 || defs2[0] != 4 {
		t.Errorf("slot 2 defs = %v, want [4]", defs2)
	}
}

// TestUseDef_HasUsersAndUserCount tests HasUsers/UserCount on various instructions.
func TestUseDef_HasUsersAndUserCount(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: CONST_INT
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 1},
			// 1: CONST_INT (unused)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 2},
			// 2: ADD_INT ref=0, ref=0 (uses 0 twice)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0},
			// 3: SUB_INT ref=2, ref=0
			{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: 2, Arg2: 0},
		},
	}

	ud := BuildUseDef(f)

	// Instruction 0: used by 2 (Arg1), 2 (Arg2), 3 (Arg2) = 3 users
	if !ud.HasUsers(0) {
		t.Error("inst 0 should have users")
	}
	if ud.UserCount(0) != 3 {
		t.Errorf("inst 0 UserCount = %d, want 3", ud.UserCount(0))
	}

	// Instruction 1: unused
	if ud.HasUsers(1) {
		t.Error("inst 1 should have no users")
	}
	if ud.UserCount(1) != 0 {
		t.Errorf("inst 1 UserCount = %d, want 0", ud.UserCount(1))
	}

	// Instruction 2: used by 3 (Arg1)
	if ud.UserCount(2) != 1 {
		t.Errorf("inst 2 UserCount = %d, want 1", ud.UserCount(2))
	}

	// Instruction 3: no users
	if ud.HasUsers(3) {
		t.Error("inst 3 should have no users")
	}
}

// TestUseDef_IsDeadCode verifies dead code detection.
func TestUseDef_IsDeadCode(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: CONST_INT (used by ADD)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 1},
			// 1: CONST_INT (unused — dead code)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 2},
			// 2: ADD_INT ref=0, ref=0 (unused — dead code)
			{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0},
			// 3: GUARD_TYPE ref=0 (side-effecting — never dead)
			{Op: SSA_GUARD_TYPE, Type: SSATypeInt, Arg1: 0, Arg2: SSARefNone},
			// 4: STORE_SLOT ref=0 (side-effecting — never dead)
			{Op: SSA_STORE_SLOT, Type: SSATypeInt, Arg1: 0, Arg2: SSARefNone, Slot: 5},
			// 5: SIDE_EXIT (side-effecting — never dead)
			{Op: SSA_SIDE_EXIT, Arg1: SSARefNone, Arg2: SSARefNone},
			// 6: CALL (side-effecting — never dead)
			{Op: SSA_CALL, Arg1: SSARefNone, Arg2: SSARefNone},
			// 7: CALL_SELF (side-effecting — never dead)
			{Op: SSA_CALL_SELF, Arg1: SSARefNone, Arg2: SSARefNone},
			// 8: SUB_INT ref=0, ref=0 (unused — dead code)
			{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: 0, Arg2: 0},
		},
	}

	ud := BuildUseDef(f)

	tests := []struct {
		ref  SSARef
		dead bool
		desc string
	}{
		{0, false, "CONST_INT used by GUARD_TYPE and STORE_SLOT"},
		{1, true, "CONST_INT unused"},
		{2, true, "ADD_INT unused"},
		{3, false, "GUARD_TYPE is side-effecting"},
		{4, false, "STORE_SLOT is side-effecting"},
		{5, false, "SIDE_EXIT is side-effecting"},
		{6, false, "CALL is side-effecting"},
		{7, false, "CALL_SELF is side-effecting"},
		{8, true, "SUB_INT unused"},
	}

	for _, tt := range tests {
		got := ud.IsDeadCode(tt.ref, f)
		if got != tt.dead {
			t.Errorf("IsDeadCode(%d) = %v, want %v (%s)", tt.ref, got, tt.dead, tt.desc)
		}
	}
}

// TestUseDef_StoreArrayAuxInt verifies that SSA_STORE_ARRAY's AuxInt (value ref) is tracked.
func TestUseDef_StoreArrayAuxInt(t *testing.T) {
	f := &SSAFunc{
		Insts: []SSAInst{
			// 0: LOAD_SLOT (table)
			{Op: SSA_LOAD_SLOT, Type: SSATypeTable, Arg1: SSARefNone, Arg2: SSARefNone, Slot: 0},
			// 1: CONST_INT (key)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 1},
			// 2: CONST_INT (value to store)
			{Op: SSA_CONST_INT, Type: SSATypeInt, Arg1: SSARefNone, Arg2: SSARefNone, AuxInt: 42},
			// 3: STORE_ARRAY table=0, key=1, value=2 (value in AuxInt)
			{Op: SSA_STORE_ARRAY, Arg1: 0, Arg2: 1, AuxInt: int64(2)},
		},
	}

	ud := BuildUseDef(f)

	// Instruction 2 (the value) should be used by instruction 3 (STORE_ARRAY via AuxInt)
	if ud.UserCount(2) != 1 {
		t.Errorf("inst 2 UserCount = %d, want 1 (from STORE_ARRAY AuxInt)", ud.UserCount(2))
	}
	if len(ud.Users[2]) != 1 || ud.Users[2][0] != 3 {
		t.Errorf("inst 2 users = %v, want [3]", ud.Users[2])
	}
}

// TestUseDef_WithBuildSSA tests use/def with real BuildSSA output.
func TestUseDef_WithBuildSSA(t *testing.T) {
	// Simple loop: sum = sum + i; FORLOOP
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	f := BuildSSA(trace)
	ud := BuildUseDef(f)

	// Basic sanity: every Arg1/Arg2 >= 0 should appear in Users
	for i, inst := range f.Insts {
		if inst.Arg1 >= 0 && inst.Arg1 != SSARefNone {
			found := false
			for _, u := range ud.Users[inst.Arg1] {
				if u == SSARef(i) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("inst %d uses Arg1=%d but not in Users[%d]", i, inst.Arg1, inst.Arg1)
			}
		}
		if inst.Arg2 >= 0 && inst.Arg2 != SSARefNone {
			found := false
			for _, u := range ud.Users[inst.Arg2] {
				if u == SSARef(i) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("inst %d uses Arg2=%d but not in Users[%d]", i, inst.Arg2, inst.Arg2)
			}
		}
	}

	// The SSA should have at least one instruction with users
	anyUsers := false
	for i := range f.Insts {
		if ud.HasUsers(SSARef(i)) {
			anyUsers = true
			break
		}
	}
	if !anyUsers {
		t.Error("BuildSSA output has no instructions with users — something is wrong")
	}
}
