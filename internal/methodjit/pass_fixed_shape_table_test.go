//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

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
