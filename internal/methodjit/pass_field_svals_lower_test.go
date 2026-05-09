package methodjit

import "testing"

func TestFieldSvalsLower_SharedSvalsForRepeatedFixedShapeLoads(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_repeated")
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	sum := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{gx.Value(), one.Value()}, Block: b}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetFieldNumToFloat, Type: TypeFloat,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{sum.Value(), gy.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gx, one, sum, gy, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after lower: %v\n%s", errs, Print(out))
	}
	var svals *Instr
	for _, instr := range b.Instrs {
		if instr.Op == OpFieldSvals {
			svals = instr
		}
	}
	if svals == nil {
		t.Fatalf("expected shared FieldSvals:\n%s", Print(out))
	}
	if gx.Op != OpFieldLoad || gx.Args[0].ID != svals.ID || gx.Aux != 0 {
		t.Fatalf("first field load not lowered through shared svals:\n%s", Print(out))
	}
	if gy.Op != OpFieldLoadNumToFloat || gy.Args[0].ID != svals.ID || gy.Aux != 1 {
		t.Fatalf("numeric field load not lowered through shared svals:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_LeavesSingleLoadAsGetField(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_single")
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gx.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gx, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if gx.Op != OpGetField {
		t.Fatalf("single field load should remain GetField:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_LeavesAdjacentLoadsToEmitterCache(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_adjacent")
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gx.Value(), gy.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gx, gy, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if gx.Op != OpGetField || gy.Op != OpGetField {
		t.Fatalf("adjacent field loads should remain GetField for emitter cache:\n%s", Print(out))
	}
}
