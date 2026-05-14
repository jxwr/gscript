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

func TestFieldSvalsLower_ReusesAcrossNonNilExistingSetField(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_setfield")
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), one.Value()}, Aux: 3, Aux2: int64(42)<<32 | 2, Block: b}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gx.Value(), gy.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gx, one, set, gy, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after lower: %v\n%s", errs, Print(out))
	}
	var svalsCount int
	var svalsID int
	for _, instr := range b.Instrs {
		if instr.Op == OpFieldSvals {
			svalsCount++
			svalsID = instr.ID
		}
	}
	if svalsCount != 1 {
		t.Fatalf("expected one shared FieldSvals across preserving SetField, got %d\n%s", svalsCount, Print(out))
	}
	if gx.Op != OpFieldLoad || gy.Op != OpFieldLoad || gx.Args[0].ID != svalsID || gy.Args[0].ID != svalsID {
		t.Fatalf("loads did not share FieldSvals across SetField:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_LowersStoreThroughSharedSvals(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_store")
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	sum := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{gx.Value(), one.Value()}, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), sum.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gy.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gx, one, sum, set, gy, ret}

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
	if set.Op != OpFieldStore || len(set.Args) != 2 || set.Args[0].ID != svals.ID || set.Args[1].ID != sum.ID || set.Aux != 0 {
		t.Fatalf("SetField was not lowered to FieldStore through shared svals:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_NilSetFieldRemainsBarrier(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_nil_setfield")
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	nilv := &Instr{ID: fn.newValueID(), Op: OpConstNil, Type: TypeNil, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), nilv.Value()}, Aux: 3, Aux2: int64(42)<<32 | 2, Block: b}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gx.Value(), gy.Value()}, Block: b}
	b.Instrs = []*Instr{obj, gx, nilv, set, gy, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if gx.Op != OpGetField || gy.Op != OpGetField {
		t.Fatalf("nil SetField should remain a barrier:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_GenericSetTableBreaksRawSvalsReuse(t *testing.T) {
	fn, b, obj := newFieldNumFusionFn("field_svals_settable_barrier")
	other := &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Block: b}
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
		Args: []*Value{other.Value(), one.Value(), one.Value()}, Block: b}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gx.Value(), gy.Value(), set.Value()}, Block: b}
	b.Instrs = []*Instr{obj, other, gx, one, set, gy, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if gx.Op != OpGetField || gy.Op != OpGetField {
		t.Fatalf("generic SetTable may exit; FieldSvals must not be reused across it:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_CrossBlockDominatedLoads(t *testing.T) {
	fn, b0, obj := newFieldNumFusionFn("field_svals_cross_block")
	b1 := &Block{ID: 1}
	b2 := &Block{ID: 2}
	b3 := &Block{ID: 3}
	b0.Succs = []*Block{b1, b2}
	b1.Preds = []*Block{b0}
	b2.Preds = []*Block{b0}
	b1.Succs = []*Block{b3}
	b2.Succs = []*Block{b3}
	b3.Preds = []*Block{b1, b2}
	fn.Blocks = []*Block{b0, b1, b2, b3}

	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: b0}
	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b0}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b1}
	gz := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 3, Aux2: int64(42)<<32 | 2, Block: b2}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gx.Value(), gy.Value(), gz.Value()}, Block: b3}
	br := &Instr{ID: fn.newValueID(), Op: OpBranch, Args: []*Value{cond.Value()}, Block: b0}
	j1 := &Instr{ID: fn.newValueID(), Op: OpJump, Block: b1}
	j2 := &Instr{ID: fn.newValueID(), Op: OpJump, Block: b2}
	b0.Instrs = []*Instr{obj, cond, gx, br}
	b1.Instrs = []*Instr{gy, j1}
	b2.Instrs = []*Instr{gz, j2}
	b3.Instrs = []*Instr{ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after lower: %v\n%s", errs, Print(out))
	}
	var svals *Instr
	for _, instr := range b0.Instrs {
		if instr.Op == OpFieldSvals {
			svals = instr
		}
	}
	if svals == nil {
		t.Fatalf("missing cross-block FieldSvals:\n%s", Print(out))
	}
	if gx.Op != OpFieldLoad || gy.Op != OpFieldLoad || gz.Op != OpFieldLoad ||
		gx.Args[0].ID != svals.ID || gy.Args[0].ID != svals.ID || gz.Args[0].ID != svals.ID {
		t.Fatalf("loads did not share cross-block FieldSvals:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_CrossBlockDominatedShapePreservingStore(t *testing.T) {
	fn, b0, obj := newFieldNumFusionFn("field_svals_cross_block_store")
	b1 := &Block{ID: 1}
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}
	fn.Blocks = []*Block{b0, b1}

	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b0}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b0}
	localSet := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), one.Value()}, Aux: 4, Aux2: int64(42)<<32 | 3, Block: b0}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b0}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: b0}
	set := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), gx.Value()}, Aux: 3, Aux2: int64(42)<<32 | 2, Block: b1}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gy.Value()}, Block: b1}
	b0.Instrs = []*Instr{obj, gx, one, localSet, gy, jump}
	b1.Instrs = []*Instr{set, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("invalid IR after lower: %v\n%s", errs, Print(out))
	}
	var svals *Instr
	for _, instr := range b0.Instrs {
		if instr.Op == OpFieldSvals {
			svals = instr
		}
	}
	if svals == nil {
		t.Fatalf("missing FieldSvals:\n%s", Print(out))
	}
	if set.Op != OpFieldStore || len(set.Args) != 2 || set.Args[0].ID != svals.ID || set.Args[1].ID != gx.ID || set.Aux != 2 {
		t.Fatalf("dominated shape-preserving SetField was not lowered through existing svals:\n%s", Print(out))
	}
}

func TestFieldSvalsLower_CrossBlockCallBlocksExistingSvalsReuse(t *testing.T) {
	fn, b0, obj := newFieldNumFusionFn("field_svals_cross_block_call_barrier")
	b1 := &Block{ID: 1}
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}
	fn.Blocks = []*Block{b0, b1}

	gx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 1, Aux2: int64(42)<<32 | 0, Block: b0}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b0}
	localSet := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), one.Value()}, Aux: 4, Aux2: int64(42)<<32 | 3, Block: b0}
	gy := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeInt,
		Args: []*Value{obj.Value()}, Aux: 2, Aux2: int64(42)<<32 | 1, Block: b0}
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny, Block: b0}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: b0}
	set := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{obj.Value(), gx.Value()}, Aux: 3, Aux2: int64(42)<<32 | 2, Block: b1}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{gy.Value()}, Block: b1}
	b0.Instrs = []*Instr{obj, gx, one, localSet, gy, call, jump}
	b1.Instrs = []*Instr{set, ret}

	out, err := FieldSvalsLowerPass(fn)
	if err != nil {
		t.Fatalf("FieldSvalsLowerPass: %v", err)
	}
	if set.Op != OpSetField {
		t.Fatalf("call barrier should block cross-block svals reuse:\n%s", Print(out))
	}
}
