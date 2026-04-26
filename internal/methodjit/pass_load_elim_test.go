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
// is NOT forwarded to the earlier GetField. Instead, store-to-load
// forwarding kicks in: the GetField is forwarded to the stored value.
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
	// Store to the same field — kills the gf1 entry, records val
	sf := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeAny,
		Args: []*Value{obj.Value(), val.Value()}, Aux: 42, Block: b}
	// Second load — NOT forwarded to gf1, but forwarded to val via store-to-load
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

	// gf2 should NOT be forwarded to gf1 (SetField killed that entry).
	// Instead, store-to-load forwarding replaces gf2 with val.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Args[0].ID != gf1.ID {
				t.Errorf("expected add.Args[0] = gf1 (v%d), got v%d",
					gf1.ID, instr.Args[0].ID)
			}
			if instr.Args[1].ID == gf1.ID {
				t.Errorf("add.Args[1] should NOT be gf1 (v%d) — SetField kill failed",
					gf1.ID)
			}
			if instr.Args[1].ID != val.ID {
				t.Errorf("expected add.Args[1] = val (v%d) via store-to-load forwarding, got v%d",
					val.ID, instr.Args[1].ID)
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

// TestLoadElim_StoreToLoadForwarding verifies that after SetField(obj, field, val),
// a subsequent GetField(obj, field) is forwarded to val (the stored value)
// rather than reloading from memory. After DCE, the GetField should be eliminated.
func TestLoadElim_StoreToLoadForwarding(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "store_to_load_fwd"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	// obj = some table
	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	// val = 3.14
	val := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b}
	// SetField(obj, 42, val)
	sf := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeAny,
		Args: []*Value{obj.Value(), val.Value()}, Aux: 42, Block: b}
	// gf = GetField(obj, 42) — should be forwarded to val
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// return gf
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gf.Value()}, Block: b}

	b.Instrs = []*Instr{obj, val, sf, gf, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// After store-to-load forwarding, the return should reference val, not gf.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == ret.ID {
			if instr.Args[0].ID != val.ID {
				t.Errorf("expected ret.Args[0] to reference val (v%d), got v%d",
					val.ID, instr.Args[0].ID)
			}
		}
	}

	// After DCE, the GetField should be eliminated (no remaining uses).
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
	if getFieldCount != 0 {
		t.Errorf("expected 0 GetField after DCE, got %d", getFieldCount)
	}
}

// TestLoadElim_GuardTypeCSE verifies that redundant GuardType instructions
// on the same (value, type) pair are eliminated. When a value is guarded for
// the same type multiple times within a block, only the first guard is kept.
func TestLoadElim_GuardTypeCSE(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "guard_type_cse"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	// obj = table
	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	// v = GetField(obj, 42)
	v := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// guard1 = GuardType(v, TypeFloat) — first guard
	guard1 := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{v.Value()}, Aux: int64(TypeFloat), Block: b}
	// use1 = AddFloat(guard1, guard1)
	use1 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat,
		Args: []*Value{guard1.Value(), guard1.Value()}, Block: b}
	// guard2 = GuardType(v, TypeFloat) — REDUNDANT, same value, same type
	guard2 := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{v.Value()}, Aux: int64(TypeFloat), Block: b}
	// use2 = MulFloat(guard2, use1)
	use2 := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat,
		Args: []*Value{guard2.Value(), use1.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{use2.Value()}, Block: b}

	b.Instrs = []*Instr{obj, v, guard1, use1, guard2, use2, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// After guard CSE, use2's first arg should reference guard1, not guard2.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == use2.ID {
			if instr.Args[0].ID != guard1.ID {
				t.Errorf("expected use2.Args[0] = guard1 (v%d), got v%d",
					guard1.ID, instr.Args[0].ID)
			}
		}
	}

	// The redundant guard2 should have been converted to Nop.
	guard2Alive := false
	for _, instr := range result.Entry.Instrs {
		if instr.ID == guard2.ID && instr.Op == OpGuardType {
			guard2Alive = true
		}
	}
	if guard2Alive {
		t.Error("redundant guard2 should have been converted to Nop, but is still OpGuardType")
	}

	// After DCE, only one GuardType should remain.
	result, err = DCEPass(result)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	guardCount := 0
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpGuardType {
			guardCount++
		}
	}
	if guardCount != 1 {
		t.Errorf("expected 1 GuardType after DCE, got %d", guardCount)
	}
}

func TestLoadElim_GuardTypeTypedProducer(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "guard_typed_producer"},
		NumRegs: 0,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	a := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b}
	bb := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b}
	mul := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat,
		Args: []*Value{a.Value(), bb.Value()}, Block: b}
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{mul.Value()}, Aux: int64(TypeFloat), Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{guard.Value()}, Block: b}

	b.Instrs = []*Instr{a, bb, mul, guard, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	if ret.Args[0].ID != mul.ID {
		t.Fatalf("expected return to use MulFloat v%d directly, got v%d", mul.ID, ret.Args[0].ID)
	}
	if guard.Op != OpNop {
		t.Fatalf("expected proven GuardType to become Nop, got %s", guard.Op)
	}

	result, err = DCEPass(result)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpGuardType {
			t.Fatal("expected no GuardType after DCE")
		}
	}
}

func TestLoadElim_GuardTypeKeepsDynamicProducer(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "guard_dynamic_producer"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	field := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{field.Value()}, Aux: int64(TypeFloat), Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{guard.Value()}, Block: b}

	b.Instrs = []*Instr{obj, field, guard, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	if _, err := LoadEliminationPass(fn); err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	if guard.Op != OpGuardType {
		t.Fatalf("expected dynamic producer guard to remain, got %s", guard.Op)
	}
	if ret.Args[0].ID != guard.ID {
		t.Fatalf("expected return to keep guard v%d, got v%d", guard.ID, ret.Args[0].ID)
	}
}

// TestLoadElim_GuardTypeCSE_DifferentTypes verifies that GuardType instructions
// with the SAME value but DIFFERENT types are NOT eliminated — they guard for
// different conditions.
func TestLoadElim_GuardTypeCSE_DifferentTypes(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "guard_different_types"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	v := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// guard1 = GuardType(v, TypeFloat)
	guard1 := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{v.Value()}, Aux: int64(TypeFloat), Block: b}
	// guard2 = GuardType(v, TypeInt) — NOT redundant, different type
	guard2 := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeInt,
		Args: []*Value{v.Value()}, Aux: int64(TypeInt), Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn,
		Args: []*Value{guard1.Value(), guard2.Value()}, Block: b}

	b.Instrs = []*Instr{obj, v, guard1, guard2, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// Both guards should remain — different types.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == ret.ID {
			if instr.Args[0].ID != guard1.ID {
				t.Errorf("expected ret.Args[0] = guard1 (v%d), got v%d",
					guard1.ID, instr.Args[0].ID)
			}
			if instr.Args[1].ID != guard2.ID {
				t.Errorf("expected ret.Args[1] = guard2 (v%d), got v%d",
					guard2.ID, instr.Args[1].ID)
			}
		}
	}
}

// TestLoadElim_GuardTypeCSE_CallKill verifies that a call clears the guard
// available map, so a guard after a call is NOT eliminated even if it has
// the same (value, type) as a guard before the call.
func TestLoadElim_GuardTypeCSE_CallKill(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "guard_call_kill"},
		NumRegs: 2,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	callee := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFunction, Aux: 1, Block: b}
	v := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	// guard1 = GuardType(v, TypeFloat) — before call
	guard1 := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{v.Value()}, Aux: int64(TypeFloat), Block: b}
	// call — kills guard available map
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny,
		Args: []*Value{callee.Value()}, Aux: 1, Block: b}
	// guard2 = GuardType(v, TypeFloat) — NOT redundant (call could change type)
	guard2 := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat,
		Args: []*Value{v.Value()}, Aux: int64(TypeFloat), Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn,
		Args: []*Value{guard1.Value(), guard2.Value()}, Block: b}

	b.Instrs = []*Instr{obj, callee, v, guard1, call, guard2, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := LoadEliminationPass(fn)
	if err != nil {
		t.Fatalf("LoadEliminationPass error: %v", err)
	}

	// Both guards should remain — call kills availability.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == ret.ID {
			if instr.Args[0].ID != guard1.ID {
				t.Errorf("expected ret.Args[0] = guard1 (v%d), got v%d",
					guard1.ID, instr.Args[0].ID)
			}
			if instr.Args[1].ID != guard2.ID {
				t.Errorf("expected ret.Args[1] = guard2 (v%d), got v%d",
					guard2.ID, instr.Args[1].ID)
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
