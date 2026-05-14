//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestFieldPolyShapeCasesOrdersByObservationCount(t *testing.T) {
	facts := []FixedShapeTableFact{
		{
			ShapeID:          11,
			ObservationCount: 2,
			FieldNames:       []string{"step"},
			FieldTypes:       map[string]Type{"step": TypeFunction},
		},
		{
			ShapeID:          12,
			ObservationCount: 9,
			FieldNames:       []string{"step"},
			FieldTypes:       map[string]Type{"step": TypeFunction},
		},
		{
			ShapeID:          13,
			ObservationCount: 5,
			FieldNames:       []string{"step"},
			FieldTypes:       map[string]Type{"step": TypeFunction},
		},
	}
	cases, typ := fieldPolyShapeCases(facts, "step")
	if typ != TypeFunction {
		t.Fatalf("type=%s want function", typ)
	}
	if len(cases) != 3 {
		t.Fatalf("cases=%d want 3", len(cases))
	}
	if cases[0].ShapeID != 12 || cases[1].ShapeID != 13 || cases[2].ShapeID != 11 {
		t.Fatalf("shape order=%d,%d,%d want 12,13,11", cases[0].ShapeID, cases[1].ShapeID, cases[2].ShapeID)
	}
}

func TestFixedShapeReturnFact_BinaryTreeShape(t *testing.T) {
	top := compileProto(t, `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}
result := makeTree(2)
`)
	makeTree := findProtoByName(top, "makeTree")
	if makeTree == nil {
		t.Fatal("makeTree proto missing")
	}

	fact, ok := AnalyzeFixedShapeReturnFact(makeTree)
	if !ok {
		t.Fatal("expected makeTree to return a fixed-shape table")
	}
	if fact.ShapeID != 0 {
		t.Fatalf("makeTree has leaf/interior physical shapes, so shape ID should be 0, got %d", fact.ShapeID)
	}
	if len(fact.FieldNames) != 2 || fact.FieldNames[0] != "left" || fact.FieldNames[1] != "right" {
		t.Fatalf("unexpected fixed shape fields: %#v", fact.FieldNames)
	}
	if len(fact.FieldValueIDs) != 0 {
		t.Fatalf("interprocedural return fact should not expose callee SSA values: %#v", fact.FieldValueIDs)
	}
	for _, name := range []string{"left", "right"} {
		fieldFact, ok := fact.FieldFacts[name]
		if !ok {
			t.Fatalf("missing field fact for %q: %#v", name, fact.FieldFacts)
		}
		if fieldFact.Kind != FixedShapeFieldUnknown || !fieldFact.MaybeNil || !fieldFact.MaybeMaterialized {
			t.Fatalf("%s field fact=%#v, want unknown maybe-nil maybe-materialized", name, fieldFact)
		}
	}
}

func TestFixedShapeReturnFact_EmptyLeafFieldsAreMaybeNil(t *testing.T) {
	top := compileProto(t, `
func makeTree(depth) {
    if depth == 0 {
        return {}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}
result := makeTree(2)
`)
	makeTree := findProtoByName(top, "makeTree")
	if makeTree == nil {
		t.Fatal("makeTree proto missing")
	}

	fact, ok := AnalyzeFixedShapeReturnFact(makeTree)
	if !ok {
		t.Fatal("expected empty-leaf makeTree to return a fixed-shape table fact")
	}
	if fact.ShapeID != 0 {
		t.Fatalf("empty leaf/interior physical shapes should force shape ID 0, got %d", fact.ShapeID)
	}
	for _, name := range []string{"left", "right"} {
		fieldFact, ok := fact.FieldFacts[name]
		if !ok {
			t.Fatalf("missing field fact for %q: %#v", name, fact.FieldFacts)
		}
		if fieldFact.Kind != FixedShapeFieldUnknown || !fieldFact.MaybeNil || !fieldFact.MaybeMaterialized {
			t.Fatalf("%s field fact=%#v, want unknown maybe-nil maybe-materialized", name, fieldFact)
		}
	}
}

func TestFixedShapeReturnFact_RejectsMismatchedShapes(t *testing.T) {
	top := compileProto(t, `
func maybePair(flag) {
    if flag {
        return {left: 1, right: 2}
    }
    return {left: 1, value: 2}
}
result := maybePair(true)
`)
	maybePair := findProtoByName(top, "maybePair")
	if maybePair == nil {
		t.Fatal("maybePair proto missing")
	}
	if fact, ok := AnalyzeFixedShapeReturnFact(maybePair); ok {
		t.Fatalf("expected mismatched return shapes to be rejected, got %#v", fact)
	}
}

func TestFixedShapeTableFactsPass_AnnotatesCallResultGetField(t *testing.T) {
	top := compileProto(t, `
func makePair(x, y) {
    return {left: x + 1, right: y}
}
func usePair() {
    p := makePair(1, 2)
    return p.left
}
result := usePair()
`)
	makePair := findProtoByName(top, "makePair")
	usePair := findProtoByName(top, "usePair")
	if makePair == nil || usePair == nil {
		t.Fatalf("expected makePair and usePair protos, got makePair=%v usePair=%v", makePair != nil, usePair != nil)
	}
	fn := BuildGraph(usePair)
	out, err := FixedShapeTableFactsPass(map[string]*vm.FuncProto{"makePair": makePair})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPass: %v", err)
	}

	var sawCallFact, sawAnnotatedGet bool
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				if _, ok := out.FixedShapeTables[instr.ID]; ok && instr.Type == TypeTable {
					sawCallFact = true
				}
			case OpGetField:
				if instr.Aux2 != 0 {
					shapeID := uint32(uint64(instr.Aux2) >> 32)
					fieldIdx := uint32(instr.Aux2)
					if shapeID == 0 || fieldIdx != 0 {
						t.Fatalf("unexpected Aux2 shape/index: shapeID=%d fieldIdx=%d", shapeID, fieldIdx)
					}
					sawAnnotatedGet = true
				}
			}
		}
	}
	if !sawCallFact {
		t.Fatal("expected OpCall result to carry a fixed-shape table fact")
	}
	if !sawAnnotatedGet {
		t.Fatal("expected GetField(p.left) to receive prefilled shape metadata")
	}
}

func TestFixedShapeTableFactsPass_PropagatesNestedArrayElementFacts(t *testing.T) {
	proto := &vm.FuncProto{
		Name:      "nested_array_fact",
		NumParams: 1,
		Constants: []runtime.Value{
			runtime.StringValue("lines"),
		},
	}
	fn := &Function{Proto: proto, NumRegs: 4}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	lines := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Aux: 0, Args: []*Value{obj.Value()}, Block: b}
	load := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeAny, Args: []*Value{lines.Value(), key.Value()}, Block: b}
	store := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{lines.Value(), key.Value(), load.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{load.Value()}, Block: b}
	b.Instrs = []*Instr{obj, key, lines, load, store, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	out, err := FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
		ArgFacts: map[int]FixedShapeTableFact{
			0: {
				ShapeID:    77,
				FieldNames: []string{"lines"},
				FieldTypes: map[string]Type{"lines": TypeTable},
				FieldTableFacts: map[string]FixedShapeTableFact{
					"lines": {
						ArrayElementType: TypeInt,
						Guarded:          true,
					},
				},
				Guarded: true,
			},
		},
		EntryGuardedArgs: true,
	})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPass: %v", err)
	}
	if lines.Type != TypeTable {
		t.Fatalf("nested field type = %s, want table\n%s", lines.Type, Print(out))
	}
	if load.Aux2 != int64(vm.FBKindInt) || load.Type != TypeInt {
		t.Fatalf("nested array load not annotated: aux2=%d type=%s\n%s", load.Aux2, load.Type, Print(out))
	}
	if store.Aux2 != int64(vm.FBKindInt) {
		t.Fatalf("nested array store not annotated: aux2=%d\n%s", store.Aux2, Print(out))
	}
}

func TestFixedShapeTableFactsPass_ForwardsReturnedParamField(t *testing.T) {
	top := compileProto(t, `
func makePair(x, y) {
    return {left: x, right: y}
}
func usePair() {
    p := makePair(10, 20)
    return p.left
}
result := usePair()
`)
	makePair := findProtoByName(top, "makePair")
	usePair := findProtoByName(top, "usePair")
	if makePair == nil || usePair == nil {
		t.Fatalf("expected makePair and usePair protos, got makePair=%v usePair=%v", makePair != nil, usePair != nil)
	}
	fn := BuildGraph(usePair)
	out, err := FixedShapeTableFactsPass(map[string]*vm.FuncProto{"makePair": makePair})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPass: %v", err)
	}

	sawForwardedGet := false
	sawReturnConst10 := false
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNop {
				sawForwardedGet = true
			}
			if instr.Op == OpReturn && len(instr.Args) == 1 && instr.Args[0] != nil {
				def := instr.Args[0].Def
				if def != nil && def.Op == OpConstInt && def.Aux == 10 {
					sawReturnConst10 = true
				}
			}
		}
	}
	if !sawForwardedGet {
		t.Fatal("expected returned param field read to be replaced with Nop")
	}
	if !sawReturnConst10 {
		t.Fatalf("expected return to use forwarded constant 10\nIR:\n%s", Print(out))
	}
}

func TestFixedShapeTableFactsPass_ForwardsAlwaysNilField(t *testing.T) {
	top := compileProto(t, `
func makeNilPair() {
    return {}
}
func usePair() {
    p := makeNilPair()
    return p.left
}
result := usePair()
`)
	makeNilPair := findProtoByName(top, "makeNilPair")
	usePair := findProtoByName(top, "usePair")
	if makeNilPair == nil || usePair == nil {
		t.Fatalf("expected makeNilPair and usePair protos, got makeNilPair=%v usePair=%v", makeNilPair != nil, usePair != nil)
	}
	fn := BuildGraph(usePair)
	out, err := FixedShapeTableFactsPass(map[string]*vm.FuncProto{"makeNilPair": makeNilPair})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPass: %v", err)
	}

	sawConstNil := false
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpConstNil && instr.Type == TypeNil {
				sawConstNil = true
			}
		}
	}
	if !sawConstNil {
		t.Fatalf("expected returned nil field read to become ConstNil\nIR:\n%s", Print(out))
	}
}

func TestFixedShapeTableFactsPass_DoesNotForwardAfterReturnedObjectMutation(t *testing.T) {
	top := compileProto(t, `
func makePair(x, y) {
    return {left: x, right: y}
}
func usePair() {
    p := makePair(10, 20)
    p.left = 30
    return p.left
}
result := usePair()
`)
	makePair := findProtoByName(top, "makePair")
	usePair := findProtoByName(top, "usePair")
	if makePair == nil || usePair == nil {
		t.Fatalf("expected makePair and usePair protos, got makePair=%v usePair=%v", makePair != nil, usePair != nil)
	}
	fn := BuildGraph(usePair)
	out, err := FixedShapeTableFactsPass(map[string]*vm.FuncProto{"makePair": makePair})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPass: %v", err)
	}

	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpReturn && len(instr.Args) == 1 && instr.Args[0] != nil {
				def := instr.Args[0].Def
				if def != nil && def.Op == OpConstInt && def.Aux == 10 {
					t.Fatalf("mutated returned object field was incorrectly forwarded to original arg\nIR:\n%s", Print(out))
				}
			}
		}
	}
}

func TestFixedShapeTableFactsPass_SeedsGuardedArgumentFactInCallee(t *testing.T) {
	top := compileProto(t, `
func makePair(x, y) {
    return {left: x, right: y}
}
func walk(pair) {
    return pair.left + pair.right
}
func driver() {
    return walk(makePair(10, 20))
}
result := driver()
`)
	makePair := findProtoByName(top, "makePair")
	walk := findProtoByName(top, "walk")
	driver := findProtoByName(top, "driver")
	if makePair == nil || walk == nil || driver == nil {
		t.Fatalf("expected makePair, walk, and driver protos, got makePair=%v walk=%v driver=%v",
			makePair != nil, walk != nil, driver != nil)
	}
	globals := map[string]*vm.FuncProto{
		"makePair": makePair,
		"walk":     walk,
		"driver":   driver,
	}
	argFacts := inferGuardedFixedShapeArgFactsForProto(walk, globals)
	if len(argFacts) != 1 {
		t.Fatalf("expected one guarded arg fact for walk(pair), got %#v", argFacts)
	}
	if fact := argFacts[0]; !fact.Guarded || fact.ShapeID == 0 || len(fact.FieldFacts) != 0 {
		t.Fatalf("unexpected guarded arg fact: %#v", fact)
	}

	fn := BuildGraph(walk)
	out, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals:      globals,
		InlineMaxSize:      1,
		FixedShapeArgFacts: argFacts,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(walk): %v", err)
	}

	annotated := 0
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetField {
				continue
			}
			if instr.Aux2 == 0 {
				t.Fatalf("callee GetField was not annotated with guarded argument shape\nIR:\n%s", Print(out))
			}
			annotated++
		}
	}
	if annotated != 2 {
		t.Fatalf("expected two annotated callee field reads, got %d\nIR:\n%s", annotated, Print(out))
	}
	if out.FixedShapeArgFacts == nil || !out.FixedShapeArgFacts[0].Guarded {
		t.Fatalf("callee did not retain guarded argument fact metadata: %#v", out.FixedShapeArgFacts)
	}
}

func TestFixedShapeTableFactsPass_AnnotatesSetFieldFromGenericArithmetic(t *testing.T) {
	top := compileProto(t, `
func step(a, tick) {
    a.queue = (a.queue + tick + a.id) % 211
    a.bytes = a.bytes + a.queue * 13 + tick
    return a.bytes
}
result := step({queue: 1, id: 2, bytes: 3}, 4)
`)
	step := findProtoByName(top, "step")
	if step == nil {
		t.Fatal("expected step proto")
	}
	fields := []string{"queue", "id", "bytes"}
	fn := BuildGraph(step)
	out, err := FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
		ArgFacts: map[int]FixedShapeTableFact{
			0: {
				ShapeID:    runtime.GetShapeID(fields),
				FieldNames: fields,
				FieldTypes: map[string]Type{
					"queue": TypeInt,
					"id":    TypeInt,
					"bytes": TypeInt,
				},
			},
		},
	})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPassWith: %v", err)
	}

	setFields := 0
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpSetField {
				continue
			}
			setFields++
			if instr.Aux2 == 0 {
				t.Fatalf("SetField was not annotated with guarded shape cache:\n%s", Print(out))
			}
		}
	}
	if setFields != 2 {
		t.Fatalf("expected two SetField ops, got %d\n%s", setFields, Print(out))
	}
}

func TestFixedShapeTableFactsPass_EntryGuardedArgumentFactRecordsGuardMetadata(t *testing.T) {
	top := compileProto(t, `
func makePair(x, y) {
    return {left: x, right: y}
}
func walk(pair) {
    return pair.left - pair.right
}
func driver() {
    return walk(makePair(10, 20))
}
result := driver()
`)
	makePair := findProtoByName(top, "makePair")
	walk := findProtoByName(top, "walk")
	driver := findProtoByName(top, "driver")
	if makePair == nil || walk == nil || driver == nil {
		t.Fatalf("expected makePair, walk, and driver protos, got makePair=%v walk=%v driver=%v",
			makePair != nil, walk != nil, driver != nil)
	}
	globals := map[string]*vm.FuncProto{
		"makePair": makePair,
		"walk":     walk,
		"driver":   driver,
	}
	argFacts := inferGuardedFixedShapeArgFactsForProto(walk, globals)
	if len(argFacts) != 1 {
		t.Fatalf("expected one guarded arg fact for walk(pair), got %#v", argFacts)
	}

	fn := BuildGraph(walk)
	out, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals:         globals,
		InlineMaxSize:         1,
		FixedShapeArgFacts:    argFacts,
		FixedShapeEntryGuards: true,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(walk): %v", err)
	}

	fact, ok := out.FixedShapeEntryGuards[0]
	if !ok {
		t.Fatalf("callee did not record fixed-shape entry guard metadata: %#v", out.FixedShapeEntryGuards)
	}
	if !fact.Guarded || !fact.EntryGuarded || fact.ShapeID == 0 || len(fact.FieldFacts) != 0 {
		t.Fatalf("unexpected entry guard fact: %#v", fact)
	}
	if got := out.FixedShapeArgFacts[0]; !got.EntryGuarded || got.ShapeID != fact.ShapeID {
		t.Fatalf("arg fact did not retain entry-guard strength: %#v vs %#v", got, fact)
	}
}

func TestFixedShapeTableFactsPass_SeedsGuardedArrayElementArgumentFactInCallee(t *testing.T) {
	top := compileProto(t, `
func makeDoc(i) {
    return {id: i, score: i + 1}
}

func buildDocs(n) {
    docs := {}
    for i := 1; i <= n; i++ {
        docs[i] = makeDoc(i)
    }
    return docs
}
func walk(docs, n) {
    total := 0
    for i := 1; i <= n; i++ {
        doc := docs[i]
        total = total + doc.id + doc.score
    }
    return total
}
func driver() {
    return walk(buildDocs(10), 10)
}
result := driver()
`)
	makeDoc := findProtoByName(top, "makeDoc")
	buildDocs := findProtoByName(top, "buildDocs")
	walk := findProtoByName(top, "walk")
	driver := findProtoByName(top, "driver")
	if makeDoc == nil || buildDocs == nil || walk == nil || driver == nil {
		t.Fatalf("expected makeDoc/buildDocs/walk/driver protos, got makeDoc=%v buildDocs=%v walk=%v driver=%v",
			makeDoc != nil, buildDocs != nil, walk != nil, driver != nil)
	}
	globals := map[string]*vm.FuncProto{
		"makeDoc":   makeDoc,
		"buildDocs": buildDocs,
		"walk":      walk,
		"driver":    driver,
	}

	elemFact, ok := AnalyzeFixedShapeArrayElementReturnFact(buildDocs, globals)
	if !ok {
		t.Fatal("expected buildDocs to return an array with fixed-shape elements")
	}
	if elemFact.ShapeID == 0 || len(elemFact.FieldNames) != 2 {
		t.Fatalf("unexpected array element fact: %#v", elemFact)
	}

	argFacts := inferGuardedFixedShapeArrayElementArgFactsForProto(walk, globals)
	if len(argFacts) != 1 {
		t.Fatalf("expected one guarded array element arg fact for walk(docs), got %#v", argFacts)
	}
	if fact := argFacts[0]; !fact.Guarded || fact.ShapeID == 0 || len(fact.FieldFacts) != 0 {
		t.Fatalf("unexpected guarded array element arg fact: %#v", fact)
	}

	fn := BuildGraph(walk)
	out, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals:                  globals,
		InlineMaxSize:                  1,
		FixedShapeArrayElementArgFacts: argFacts,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(walk): %v", err)
	}

	annotated := 0
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpGetField:
				if instr.Aux2 == 0 {
					t.Fatalf("array element GetField was not annotated with guarded shape\nIR:\n%s", Print(out))
				}
				annotated++
			case OpFieldLoad:
				annotated++
			}
		}
	}
	if annotated != 2 {
		t.Fatalf("expected two annotated array element field reads, got %d\nIR:\n%s", annotated, Print(out))
	}
	if got := countOps(out)[OpTableArrayLoad]; got == 0 {
		t.Fatalf("guarded array element fact should feed mixed array lowering:\n%s", Print(out))
	}
}

func TestFixedShapeTableFactsPass_SeedsProfiledArrayElementFieldRanges(t *testing.T) {
	top := compileProto(t, `
func walk(docs, n) {
    total := 0
    for i := 1; i <= n; i++ {
        doc := docs[i]
        total = total + doc.id * 3 + doc.score
    }
    return total
}
result := walk({}, 0)
`)
	walk := findProtoByName(top, "walk")
	if walk == nil {
		t.Fatal("expected walk proto")
	}
	walk.ArgArrayElementShapeFeedback = make(vm.ArgArrayElementShapeFeedbackVector, walk.NumParams)
	shapeFields := []string{"id", "score"}
	walk.ArgArrayElementShapeFeedback[0] = vm.ArgArrayElementShapeFeedback{
		Count:      4,
		ShapeID:    runtime.GetShapeID(shapeFields),
		FieldNames: shapeFields,
		FieldTypes: map[string]vm.FeedbackType{
			"id":    vm.FBInt,
			"score": vm.FBInt,
		},
		FieldRanges: map[string]vm.IntRangeFeedback{
			"id":    {Count: 4, Min: 1, Max: 4},
			"score": {Count: 4, Min: 2, Max: 5},
		},
	}
	argFacts := profiledFixedShapeArrayElementArgFactsForProto(walk)
	if len(argFacts) != 1 {
		t.Fatalf("expected profiled arg fact, got %#v", argFacts)
	}

	fn := BuildGraph(walk)
	out, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		FixedShapeArrayElementArgFacts: argFacts,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(walk): %v", err)
	}

	rangedFields := 0
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetField && instr.Op != OpFieldLoad {
				continue
			}
			if r, ok := out.IntRanges[instr.ID]; ok && r.known {
				rangedFields++
				if instr.Type != TypeInt {
					t.Fatalf("profiled range did not force int field type: %s", instr.Type)
				}
			} else if instr.Op == OpFieldLoad && instr.Type == TypeInt {
				rangedFields++
			}
		}
	}
	if rangedFields != 2 {
		t.Fatalf("expected two ranged field reads, got %d\nIR:\n%s", rangedFields, Print(out))
	}
}

func TestFixedShapeTableFactsPass_FieldSvalsLoadCarriesNestedArrayFact(t *testing.T) {
	shapeFields := []string{"lines"}
	shapeID := runtime.GetShapeID(shapeFields)
	fn := &Function{
		Proto: &vm.FuncProto{NumParams: 1},
	}
	block := &Block{ID: 0}
	arg := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: block}
	svals := &Instr{ID: 2, Op: OpFieldSvals, Type: TypeInt, Args: []*Value{arg.Value()}, Aux: int64(shapeID), Block: block}
	lines := &Instr{ID: 3, Op: OpFieldLoad, Type: TypeAny, Args: []*Value{svals.Value()}, Aux: 0, Block: block}
	idx := &Instr{ID: 4, Op: OpConstInt, Type: TypeInt, Aux: 1, Block: block}
	item := &Instr{ID: 5, Op: OpGetTable, Type: TypeAny, Args: []*Value{lines.Value(), idx.Value()}, Block: block}
	block.Instrs = []*Instr{arg, svals, lines, idx, item}
	fn.Entry = block
	fn.Blocks = []*Block{block}

	out, err := FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
		ArgFacts: map[int]FixedShapeTableFact{
			0: {
				ShapeID:    shapeID,
				FieldNames: append([]string(nil), shapeFields...),
				FieldTableFacts: map[string]FixedShapeTableFact{
					"lines": {
						ArrayElementType: TypeInt,
						Guarded:          true,
					},
				},
				Guarded: true,
			},
		},
	})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPassWith: %v", err)
	}
	if lines.Type != TypeTable {
		t.Fatalf("field load should carry nested table type, got %s\nIR:\n%s", lines.Type, Print(out))
	}
	if item.Aux2 == 0 || item.Type != TypeInt {
		t.Fatalf("nested array element fact should annotate gettable, aux2=%d type=%s\nIR:\n%s", item.Aux2, item.Type, Print(out))
	}
}

func TestFixedShapeTableFactsPass_FieldSvalsUsesPolymorphicShapeCatalog(t *testing.T) {
	shapeFields := []string{"lines"}
	shapeID := runtime.GetShapeID(shapeFields)
	fn := &Function{Proto: &vm.FuncProto{NumParams: 1}}
	block := &Block{ID: 0}
	recv := &Instr{ID: 1, Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: block}
	svals := &Instr{ID: 2, Op: OpFieldSvals, Type: TypeInt, Args: []*Value{recv.Value()}, Aux: int64(shapeID), Block: block}
	lines := &Instr{ID: 3, Op: OpFieldLoad, Type: TypeAny, Args: []*Value{svals.Value()}, Aux: 0, Block: block}
	idx := &Instr{ID: 4, Op: OpConstInt, Type: TypeInt, Aux: 1, Block: block}
	item := &Instr{ID: 5, Op: OpGetTable, Type: TypeAny, Args: []*Value{lines.Value(), idx.Value()}, Block: block}
	block.Instrs = []*Instr{recv, svals, lines, idx, item}
	fn.Entry = block
	fn.Blocks = []*Block{block}
	fn.FieldPolyShapeFacts = map[int][]FieldPolyShapeCase{
		99: {
			{
				ShapeID: shapeID,
				ReceiverFact: FixedShapeTableFact{
					ShapeID:    shapeID,
					FieldNames: append([]string(nil), shapeFields...),
					FieldTableFacts: map[string]FixedShapeTableFact{
						"lines": {
							ArrayElementType: TypeInt,
							Guarded:          true,
						},
					},
					Guarded: true,
				},
			},
		},
	}
	recordFieldPolyShapeCatalog(fn, fn.FieldPolyShapeFacts[99])

	out, err := FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{})(fn)
	if err != nil {
		t.Fatalf("FixedShapeTableFactsPassWith: %v", err)
	}
	if lines.Type != TypeTable || item.Aux2 == 0 || item.Type != TypeInt {
		t.Fatalf("polymorphic shape catalog should feed field-load nested array facts, lines=%s aux2=%d item=%s\nIR:\n%s",
			lines.Type, item.Aux2, item.Type, Print(out))
	}
}
