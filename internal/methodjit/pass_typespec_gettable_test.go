// pass_typespec_gettable_test.go — R87 test for GetTable→result-type inference.
//
// When OpGetTable has Aux2 set to a monomorphic FBKind (FBKindInt=2,
// FBKindFloat=3, FBKindBool=4), the runtime kind guard in
// emit_table_array.go deopts on mismatch, so the loaded value's IR type
// is determined. TypeSpec must use this to cascade specialization —
// e.g., OpLe(GetTable<int>, GetTable<int>) → OpLeInt.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestTypeSpec_GetTableInt_CascadesToOpLeInt builds:
//   t1 = GetTable t, k1  (Aux2 = FBKindInt)
//   t2 = GetTable t, k2  (Aux2 = FBKindInt)
//   r  = Le t1, t2
// After TypeSpec, r should become OpLeInt.
func TestTypeSpec_GetTableInt_CascadesToOpLeInt(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "gettable_leint"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	k1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	k2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}

	load1 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), k1.Value()}, Aux2: int64(vm.FBKindInt), Block: b}
	load2 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), k2.Value()}, Aux2: int64(vm.FBKindInt), Block: b}

	le := &Instr{ID: fn.newValueID(), Op: OpLe, Type: TypeAny,
		Args: []*Value{load1.Value(), load2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{le.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, k1, k2, load1, load2, le, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	var gotLe *Instr
	for _, instr := range result.Entry.Instrs {
		if instr.ID == le.ID {
			gotLe = instr
			break
		}
	}
	if gotLe == nil {
		t.Fatal("Le instruction not found after pass")
	}
	if gotLe.Op != OpLeInt {
		t.Errorf("expected OpLeInt after TypeSpec, got %s (Le stayed generic — GetTable Aux2 Kind not inferred)", gotLe.Op)
	}
}

// TestTypeSpec_GetTableFloat_CascadesToOpLeFloat — same as above but FBKindFloat.
func TestTypeSpec_GetTableFloat_CascadesToOpLeFloat(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "gettable_lefloat"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	k1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	k2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}

	load1 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), k1.Value()}, Aux2: int64(vm.FBKindFloat), Block: b}
	load2 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), k2.Value()}, Aux2: int64(vm.FBKindFloat), Block: b}

	le := &Instr{ID: fn.newValueID(), Op: OpLe, Type: TypeAny,
		Args: []*Value{load1.Value(), load2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{le.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, k1, k2, load1, load2, le, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	var gotLe *Instr
	for _, instr := range result.Entry.Instrs {
		if instr.ID == le.ID {
			gotLe = instr
			break
		}
	}
	if gotLe == nil {
		t.Fatal("Le instruction not found after pass")
	}
	if gotLe.Op != OpLeFloat {
		t.Errorf("expected OpLeFloat after TypeSpec, got %s", gotLe.Op)
	}
}

// TestTypeSpec_GetTableMixed_StaysGeneric — Aux2=FBKindMixed means the array
// can hold any type (it's the generic []Value backing), so no inference.
func TestTypeSpec_GetTableMixed_StaysGeneric(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "gettable_mixed"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	k1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	k2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}

	load1 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), k1.Value()}, Aux2: int64(vm.FBKindMixed), Block: b}
	load2 := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny,
		Args: []*Value{tbl.Value(), k2.Value()}, Aux2: int64(vm.FBKindMixed), Block: b}

	le := &Instr{ID: fn.newValueID(), Op: OpLe, Type: TypeAny,
		Args: []*Value{load1.Value(), load2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{le.Value()}, Block: b}
	b.Instrs = []*Instr{tbl, k1, k2, load1, load2, le, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	for _, instr := range result.Entry.Instrs {
		if instr.ID == le.ID && instr.Op != OpLe {
			t.Errorf("Le should stay generic for FBKindMixed, got %s", instr.Op)
		}
	}
}
