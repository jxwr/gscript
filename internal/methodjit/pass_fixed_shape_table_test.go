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
    return {left: x, right: y}
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
