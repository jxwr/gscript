// pass_load_elim_test.go tests the block-local load elimination pass.
// Tests build IR manually with GetField/SetField/Call patterns and verify
// that redundant loads are eliminated while necessary loads are preserved.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestLoadElimination_BasicRedundant verifies that a second GetField on the
// same (obj, field) is eliminated: its uses are replaced with the first
// GetField's value, and after DCE only one GetField remains.
func TestLoadElimination_BasicRedundant(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "basic_redundant"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	// obj = some table param
	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	// gf1 = GetField(obj, field 42)
	gf1 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// gf2 = GetField(obj, field 42) — redundant
	gf2 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// use both values: return gf1 + gf2
	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{gf1.Value(), gf2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}

	b.Instrs = []*Instr{obj, gf1, gf2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// After load elimination, the add's second arg should point to gf1, not gf2.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Args[1].ID != gf1.ID {
				t.Errorf("expected add.Args[1] to reference gf1 (v%d), got v%d",
					gf1.ID, instr.Args[1].ID)
			}
		}
	}

	// After DCE, gf2 should be removed (no uses remain).
	result, err = DCEPass(result)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	getFieldCount := 0
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpGetField {
			getFieldCount++
		}
	}
	if getFieldCount != 1 {
		t.Errorf("expected 1 GetField after DCE, got %d", getFieldCount)
	}
}

// TestLoadElimination_DifferentFields verifies that two GetField ops on the
// same object but DIFFERENT fields are both preserved (no elimination).
func TestLoadElimination_DifferentFields(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "different_fields"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	gf1 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b} // field 42
	gf2 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 99, Block: b} // field 99
	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{gf1.Value(), gf2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}

	b.Instrs = []*Instr{obj, gf1, gf2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// Both GetFields should remain, no use-replacement should have happened.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Args[0].ID != gf1.ID {
				t.Errorf("expected add.Args[0] = gf1 (v%d), got v%d", gf1.ID, instr.Args[0].ID)
			}
			if instr.Args[1].ID != gf2.ID {
				t.Errorf("expected add.Args[1] = gf2 (v%d), got v%d", gf2.ID, instr.Args[1].ID)
			}
		}
	}
}

// TestLoadElimination_SetFieldKill verifies that a SetField on the same
// (obj, field) invalidates the cached GetField, so a subsequent GetField
// is NOT eliminated.
func TestLoadElimination_SetFieldKill(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "setfield_kill"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 100, Block: b}

	// First load
	gf1 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// Store to the same field — kills the available entry
	sf := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeAny,
		Args: []*Value{obj.Value(), val.Value()}, Aux: 42, Block: b}
	// Second load — should NOT be eliminated (field was overwritten)
	gf2 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}

	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{gf1.Value(), gf2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}

	b.Instrs = []*Instr{obj, val, gf1, sf, gf2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// gf2 should NOT have been replaced — it still has its own uses.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Args[1].ID != gf2.ID {
				t.Errorf("expected add.Args[1] = gf2 (v%d), got v%d — SetField kill failed",
					gf2.ID, instr.Args[1].ID)
			}
		}
	}
}

// TestLoadElimination_CallKill verifies that a call clears all available
// entries, preventing elimination of a GetField after the call.
func TestLoadElimination_CallKill(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "call_kill"},
		NumRegs: 2,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	callee := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFunction, Aux: 1, Block: b}

	// First load
	gf1 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// Call — kills everything
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny,
		Args: []*Value{callee.Value()}, Aux: 1, Block: b}
	// Second load — should NOT be eliminated (call could have mutated the table)
	gf2 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}

	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{gf1.Value(), gf2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}

	b.Instrs = []*Instr{obj, callee, gf1, call, gf2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// gf2 should NOT have been replaced.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Args[1].ID != gf2.ID {
				t.Errorf("expected add.Args[1] = gf2 (v%d), got v%d — call kill failed",
					gf2.ID, instr.Args[1].ID)
			}
		}
	}
}

// TestLoadElimination_DifferentObjects verifies that two GetField ops on
// DIFFERENT objects but the same field Aux are both preserved.
func TestLoadElimination_DifferentObjects(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "different_objects"},
		NumRegs: 2,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj1 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	obj2 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 1, Block: b}

	gf1 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj1.Value()}, Aux: 42, Block: b} // obj1.field42
	gf2 := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj2.Value()}, Aux: 42, Block: b} // obj2.field42

	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{gf1.Value(), gf2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}

	b.Instrs = []*Instr{obj1, obj2, gf1, gf2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// Both GetFields reference different objects — no elimination.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Args[0].ID != gf1.ID {
				t.Errorf("expected add.Args[0] = gf1 (v%d), got v%d", gf1.ID, instr.Args[0].ID)
			}
			if instr.Args[1].ID != gf2.ID {
				t.Errorf("expected add.Args[1] = gf2 (v%d), got v%d", gf2.ID, instr.Args[1].ID)
			}
		}
	}
}
