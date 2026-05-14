package methodjit

import "testing"

func TestGuardFieldCalleePassFusesSingleUseFixedShapeLoad(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("guard_field_callee")
	load := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 3, Block: b}
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardCalleeProto, Type: TypeAny,
		Args: []*Value{load.Value()}, Aux: 1234, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{guard.Value()}, Block: b}
	b.Instrs = []*Instr{obj, load, guard, ret}

	out, err := GuardFieldCalleePass(fn)
	if err != nil {
		t.Fatalf("GuardFieldCalleePass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after pass: %v\n%s", errs, Print(out))
	}
	if countOpHelper(out, OpGetField) != 0 {
		t.Fatalf("single-use field load should be removed:\n%s", Print(out))
	}
	if guard.Op != OpGuardFieldCalleeProto || len(guard.Args) != 1 || guard.Args[0].ID != obj.ID || guard.Aux != 1234 || guard.Aux2 != load.Aux2 {
		t.Fatalf("guard not fused correctly:\n%s", Print(out))
	}
}

func TestGuardFieldCalleePassKeepsSharedLoad(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("guard_field_callee_shared")
	load := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 3, Block: b}
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardCalleeProto, Type: TypeAny,
		Args: []*Value{load.Value()}, Aux: 1234, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{guard.Value(), load.Value()}, Block: b}
	b.Instrs = []*Instr{obj, load, guard, ret}

	out, err := GuardFieldCalleePass(fn)
	if err != nil {
		t.Fatalf("GuardFieldCalleePass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after pass: %v\n%s", errs, Print(out))
	}
	if countOpHelper(out, OpGetField) != 1 {
		t.Fatalf("shared field load should remain:\n%s", Print(out))
	}
	if guard.Op != OpGuardFieldCalleeProto {
		t.Fatalf("callee guard should still be fused:\n%s", Print(out))
	}
}
