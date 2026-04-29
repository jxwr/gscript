package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// FixedShapeTableFact describes a table SSA value whose hidden-class shape is
// statically known. FieldValueIDs is populated only for constructors in the
// current Function; call-return facts intentionally leave values unknown.
type FixedShapeTableFact struct {
	ShapeID       uint32
	FieldNames    []string
	FieldValueIDs map[string]int
}

func (f FixedShapeTableFact) fieldIndex(name string) (int, bool) {
	for i, field := range f.FieldNames {
		if field == name {
			return i, true
		}
	}
	return -1, false
}

func (f FixedShapeTableFact) sameShape(other FixedShapeTableFact) bool {
	if len(f.FieldNames) != len(other.FieldNames) {
		return false
	}
	for i := range f.FieldNames {
		if f.FieldNames[i] != other.FieldNames[i] {
			return false
		}
	}
	return true
}

// FixedShapeTableFactsPass records fixed-shape table facts and uses
// interprocedural return facts from stable global callees to prefill GetField
// shape-cache metadata. It deliberately leaves runtime shape guards intact.
func FixedShapeTableFactsPass(globals map[string]*vm.FuncProto) PassFunc {
	return func(fn *Function) (*Function, error) {
		if fn == nil || len(fn.Blocks) == 0 {
			return fn, nil
		}
		facts := inferLocalFixedShapeTables(fn)
		if len(facts) == 0 {
			facts = make(map[int]FixedShapeTableFact)
		}

		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op != OpCall {
					continue
				}
				_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
				if callee == nil {
					continue
				}
				fact, ok := AnalyzeFixedShapeReturnFact(callee)
				if !ok {
					continue
				}
				facts[instr.ID] = fact
				if instr.Type == TypeAny || instr.Type == TypeUnknown {
					instr.Type = TypeTable
				}
				functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("call result carries fixed table shape %v", fact.FieldNames))
			}
		}

		if len(facts) == 0 {
			return fn, nil
		}
		fn.FixedShapeTables = facts
		annotateFixedShapeGetFields(fn, facts)
		return fn, nil
	}
}

// AnalyzeFixedShapeReturnFact reports whether every non-empty return in proto
// returns a freshly allocated table with the same ordered static string fields.
func AnalyzeFixedShapeReturnFact(proto *vm.FuncProto) (FixedShapeTableFact, bool) {
	if proto == nil {
		return FixedShapeTableFact{}, false
	}
	fn := BuildGraph(proto)
	if fn == nil || fn.Unpromotable {
		return FixedShapeTableFact{}, false
	}
	facts := inferLocalFixedShapeTables(fn)
	var out FixedShapeTableFact
	seenReturn := false
	seenEmpty := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpReturn {
				continue
			}
			if len(instr.Args) != 1 || instr.Args[0] == nil {
				return FixedShapeTableFact{}, false
			}
			fact, ok := facts[instr.Args[0].ID]
			if !ok {
				return FixedShapeTableFact{}, false
			}
			if len(fact.FieldNames) == 0 {
				seenEmpty = true
				seenReturn = true
				continue
			}
			if !seenReturn {
				out = withoutFieldValues(fact)
				seenReturn = true
				continue
			}
			if len(out.FieldNames) != 0 && !out.sameShape(fact) {
				return FixedShapeTableFact{}, false
			}
			if len(out.FieldNames) == 0 {
				out = withoutFieldValues(fact)
			}
		}
	}
	if !seenReturn {
		return FixedShapeTableFact{}, false
	}
	if seenEmpty {
		out.ShapeID = 0
	}
	return out, true
}

func withoutFieldValues(fact FixedShapeTableFact) FixedShapeTableFact {
	return FixedShapeTableFact{
		ShapeID:    fact.ShapeID,
		FieldNames: append([]string(nil), fact.FieldNames...),
	}
}

func inferLocalFixedShapeTables(fn *Function) map[int]FixedShapeTableFact {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	out := make(map[int]FixedShapeTableFact)
	for _, block := range fn.Blocks {
		allocFields := make(map[int][]string)
		allocValues := make(map[int]map[string]int)
		killed := make(map[int]bool)
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpNewTable:
				allocFields[instr.ID] = nil
				allocValues[instr.ID] = make(map[string]int)
				out[instr.ID] = FixedShapeTableFact{}
			case OpSetField:
				if len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
					continue
				}
				allocID := instr.Args[0].ID
				if _, ok := allocValues[allocID]; !ok || killed[allocID] {
					continue
				}
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" || fixedShapeContainsString(allocFields[allocID], name) {
					killed[allocID] = true
					delete(out, allocID)
					continue
				}
				allocFields[allocID] = append(allocFields[allocID], name)
				allocValues[allocID][name] = instr.Args[1].ID
				out[allocID] = FixedShapeTableFact{
					ShapeID:       runtime.GetShapeID(allocFields[allocID]),
					FieldNames:    append([]string(nil), allocFields[allocID]...),
					FieldValueIDs: cloneStringIntMap(allocValues[allocID]),
				}
			case OpSetTable, OpAppend, OpSetList:
				if len(instr.Args) == 0 || instr.Args[0] == nil {
					continue
				}
				allocID := instr.Args[0].ID
				if _, ok := allocValues[allocID]; ok {
					killed[allocID] = true
					delete(out, allocID)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func annotateFixedShapeGetFields(fn *Function, facts map[int]FixedShapeTableFact) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetField || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			if instr.Aux2 != 0 {
				continue
			}
			fact, ok := facts[instr.Args[0].ID]
			if !ok || fact.ShapeID == 0 {
				continue
			}
			name := fieldNameFromAux(fn, instr.Aux)
			if name == "" {
				continue
			}
			idx, ok := fact.fieldIndex(name)
			if !ok {
				continue
			}
			instr.Aux2 = int64(fact.ShapeID)<<32 | int64(uint32(idx))
			functionRemarks(fn).Add("FixedShapeTableFacts", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("prefilled fixed-shape field cache for %q", name))
		}
	}
}

func fixedShapeContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cloneStringIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
