//go:build darwin && arm64

package jit

import "testing"

func TestLoadElim_DuplicateLoad(t *testing.T) {
	// Two LOAD_FIELD with same table ref, same field, same slot should eliminate the second
	tblRef := SSARef(0) // dummy table ref

	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Slot: 1, Type: SSATypeTable},                          // 0: table
			{Op: SSA_LOOP},                                                              // 1
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 2: load t.x -> slot 2
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 3: load t.x -> slot 2 (same slot)
			{Op: SSA_ADD_FLOAT, Arg1: SSARef(2), Arg2: SSARef(3), Slot: 4, Type: SSATypeFloat}, // 4: use both
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(0), AuxInt: -1},                     // 5: loop exit
		},
		LoopIdx: 1,
	}

	f = LoadElimination(f)

	// Second LOAD_FIELD should be eliminated (NOP) since same slot
	if f.Insts[3].Op != SSA_NOP {
		t.Errorf("expected second LOAD_FIELD to be NOP, got %s", ssaOpString(f.Insts[3].Op))
	}

	// ADD_FLOAT should now use ref 2 for both args (the first LOAD_FIELD)
	add := &f.Insts[4]
	if add.Arg1 != SSARef(2) || add.Arg2 != SSARef(2) {
		t.Errorf("expected ADD args=(2,2), got (%d,%d)", add.Arg1, add.Arg2)
	}
}

func TestLoadElim_DifferentSlotNotEliminated(t *testing.T) {
	// Two LOAD_FIELD with same field but DIFFERENT slots should NOT be eliminated
	tblRef := SSARef(0)

	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Slot: 1, Type: SSATypeTable},                          // 0: table
			{Op: SSA_LOOP},                                                              // 1
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 2: load t.x -> slot 2
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 3, Type: SSATypeFloat}, // 3: load t.x -> slot 3 (different)
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(0), AuxInt: -1},             // 4: loop exit
		},
		LoopIdx: 1,
	}

	f = LoadElimination(f)

	// Both should survive (different destination slots)
	if f.Insts[2].Op != SSA_LOAD_FIELD {
		t.Errorf("first LOAD_FIELD should survive")
	}
	if f.Insts[3].Op != SSA_LOAD_FIELD {
		t.Errorf("second LOAD_FIELD should survive (different slot)")
	}
}

func TestLoadElim_StoreInvalidates(t *testing.T) {
	// STORE_FIELD to ANY field of same table should invalidate cached loads
	tblRef := SSARef(0)

	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Slot: 1, Type: SSATypeTable},                          // 0: table
			{Op: SSA_CONST_FLOAT, AuxInt: 42, Slot: -1, Type: SSATypeFloat},            // 1: value
			{Op: SSA_LOOP},                                                              // 2
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 3: load t.x
			{Op: SSA_STORE_FIELD, Arg1: tblRef, Arg2: SSARef(1), AuxInt: 1, Slot: 1},   // 4: store t.y = val
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 5: load t.x again
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(0), AuxInt: -1},             // 6: loop exit
		},
		LoopIdx: 2,
	}

	f = LoadElimination(f)

	// Second LOAD_FIELD should NOT be eliminated (store to same table invalidated cache)
	if f.Insts[5].Op != SSA_LOAD_FIELD {
		t.Errorf("expected LOAD_FIELD to survive after STORE_FIELD to same table, got %s", ssaOpString(f.Insts[5].Op))
	}
}

func TestLoadElim_CallInvalidates(t *testing.T) {
	// CALL between two LOAD_FIELDs should prevent elimination
	tblRef := SSARef(0)

	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Slot: 1, Type: SSATypeTable},                          // 0: table
			{Op: SSA_LOOP},                                                              // 1
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 2: load t.x
			{Op: SSA_CALL, Slot: 5, PC: 10},                                            // 3: function call
			{Op: SSA_LOAD_FIELD, Arg1: tblRef, AuxInt: 0, Slot: 2, Type: SSATypeFloat}, // 4: load t.x again
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(0), AuxInt: -1},             // 5: loop exit
		},
		LoopIdx: 1,
	}

	f = LoadElimination(f)

	// Second LOAD_FIELD should NOT be eliminated (call might have modified the table)
	if f.Insts[4].Op != SSA_LOAD_FIELD {
		t.Errorf("expected LOAD_FIELD to survive after CALL, got %s", ssaOpString(f.Insts[4].Op))
	}
}

func TestLoadElim_DifferentTables(t *testing.T) {
	// LOAD_FIELD from different tables should NOT eliminate

	f := &SSAFunc{
		Insts: []SSAInst{
			{Op: SSA_LOAD_SLOT, Slot: 1, Type: SSATypeTable},                          // 0: table1
			{Op: SSA_LOAD_SLOT, Slot: 2, Type: SSATypeTable},                          // 1: table2
			{Op: SSA_LOOP},                                                              // 2
			{Op: SSA_LOAD_FIELD, Arg1: SSARef(0), AuxInt: 0, Slot: 3, Type: SSATypeFloat}, // 3: load t1.x
			{Op: SSA_LOAD_FIELD, Arg1: SSARef(1), AuxInt: 0, Slot: 3, Type: SSATypeFloat}, // 4: load t2.x (different table)
			{Op: SSA_LE_INT, Arg1: SSARef(0), Arg2: SSARef(0), AuxInt: -1},                // 5: loop exit
		},
		LoopIdx: 2,
	}

	f = LoadElimination(f)

	// Both should survive (different tables)
	if f.Insts[3].Op != SSA_LOAD_FIELD {
		t.Errorf("first LOAD_FIELD should survive")
	}
	if f.Insts[4].Op != SSA_LOAD_FIELD {
		t.Errorf("second LOAD_FIELD should survive (different table)")
	}
}

func TestLoadElim_Correctness(t *testing.T) {
	// End-to-end: repeated field access in loop body (same slot)
	src := `
func f(tbl) {
    s := 0.0
    for i := 1; i <= 100; i++ {
        s = s + tbl.x + tbl.x
    }
    return s
}
t := {x: 1.5}
result = f(t)`
	vmResult := runVMGetFloat(t, src, "result")
	jitResult := runJITGetFloat(t, src, "result")
	if !floatClose(vmResult, jitResult, 1e-6) {
		t.Errorf("VM=%f, JIT=%f — mismatch", vmResult, jitResult)
	}
	expected := 300.0 // 100 * (1.5 + 1.5) = 300
	if !floatClose(vmResult, expected, 1e-6) {
		t.Errorf("VM result %f != expected %f", vmResult, expected)
	}
}

func TestLoadElim_NBodyCorrectness(t *testing.T) {
	// nbody-like pattern: load+store+load of different fields on same table
	src := `
func f() {
    b := {x: 0.0, vx: 0.1}
    for step := 1; step <= 100; step++ {
        b.vx = b.vx - 0.001
        b.x = b.x + b.vx
    }
    return b.x
}
result = f()`
	vmResult := runVMGetFloat(t, src, "result")
	jitResult := runJITGetFloat(t, src, "result")
	if !floatClose(vmResult, jitResult, 1e-6) {
		t.Errorf("VM=%f, JIT=%f — mismatch", vmResult, jitResult)
	}
}
