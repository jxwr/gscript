package methodjit

import "testing"

func TestFieldSvalsCSE_ReusesSameTableAndShape(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_cse")
	s0 := &Instr{ID: fn.newValueID(), Op: OpFieldSvals, Type: TypeInt, Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	x := &Instr{ID: fn.newValueID(), Op: OpFieldLoad, Type: TypeInt, Args: []*Value{s0.Value()}, Aux: 0, Block: b}
	s1 := &Instr{ID: fn.newValueID(), Op: OpFieldSvals, Type: TypeInt, Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	y := &Instr{ID: fn.newValueID(), Op: OpFieldLoad, Type: TypeInt, Args: []*Value{s1.Value()}, Aux: 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{x.Value(), y.Value()}, Block: b}
	b.Instrs = []*Instr{obj, s0, x, s1, y, ret}

	out, err := FieldSvalsCSEPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsCSEPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after CSE: %v\n%s", errs, Print(out))
	}
	if s1.Op != OpNop {
		t.Fatalf("duplicate svals was not removed:\n%s", Print(out))
	}
	if y.Args[0].ID != s0.ID {
		t.Fatalf("field load did not reuse first svals:\n%s", Print(out))
	}
}

func TestFieldSvalsCSE_KeepsDuplicateAfterSameTableMutation(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_cse_barrier")
	s0 := &Instr{ID: fn.newValueID(), Op: OpFieldSvals, Type: TypeInt, Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	store := &Instr{ID: fn.newValueID(), Op: OpTableArrayStore, Type: TypeUnknown, Args: []*Value{obj.Value(), one.Value(), one.Value(), one.Value(), one.Value()}, Block: b}
	s1 := &Instr{ID: fn.newValueID(), Op: OpFieldSvals, Type: TypeInt, Args: []*Value{obj.Value()}, Aux: 42, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{s0.Value(), s1.Value()}, Block: b}
	b.Instrs = []*Instr{obj, s0, one, store, s1, ret}

	out, err := FieldSvalsCSEPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsCSEPass: %v", err)
	}
	if s1.Op != OpFieldSvals {
		t.Fatalf("svals after same-table mutation should remain:\n%s", Print(out))
	}
}
