package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const tier2FeedbackArrayHint = 1024
const tier2FeedbackOuterLoopArrayHint = 64 * 1024

type tablePreallocHint struct {
	arrayHint int64
	kind      runtime.ArrayKind
	mixed     bool
}

// TablePreallocHintPass annotates empty table allocations that feed observed
// integer-key stores. The hints are consumed by the existing NewTable exit
// path, so allocation remains in Go while Tier 2 can use pre-sized and, when
// feedback is monomorphic scalar, typed-array append stores until capacity is
// exhausted.
func TablePreallocHintPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	candidates := make(map[int]tablePreallocHint)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetTable || len(instr.Args) == 0 {
				continue
			}
			if instr.Aux2 == 0 || instr.Aux2 == int64(vm.FBKindPolymorphic) {
				continue
			}
			tbl := instr.Args[0]
			if tbl == nil || tbl.Def == nil || tbl.Def.Op != OpNewTable {
				continue
			}
			hint := candidates[tbl.Def.ID]
			arrayHint := int64(tier2FeedbackArrayHint)
			if li != nil && tbl.Def.Block != nil && li.loopBlocks[block.ID] && !li.loopBlocks[tbl.Def.Block.ID] {
				arrayHint = tier2FeedbackOuterLoopArrayHint
			}
			if arrayHint > hint.arrayHint {
				hint.arrayHint = arrayHint
			}
			if kind, ok := setTableArrayKindHint(instr); ok {
				if hint.kind == runtime.ArrayMixed {
					hint.kind = kind
				} else if hint.kind != kind {
					hint.mixed = true
				}
			} else {
				hint.mixed = true
			}
			candidates[tbl.Def.ID] = hint
		}
	}
	if len(candidates) == 0 {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpNewTable || instr.Aux != 0 {
				continue
			}
			hint, ok := candidates[instr.ID]
			if !ok {
				continue
			}
			instr.Aux = hint.arrayHint
			if !hint.mixed && hint.kind != runtime.ArrayMixed {
				instr.Aux2 = packNewTableAux2(instr.Aux2, hint.kind)
			}
		}
	}
	return fn, nil
}

func setTableArrayKindHint(instr *Instr) (runtime.ArrayKind, bool) {
	switch instr.Aux2 {
	case int64(vm.FBKindInt):
		return runtime.ArrayInt, true
	case int64(vm.FBKindFloat):
		return runtime.ArrayFloat, true
	case int64(vm.FBKindBool):
		return runtime.ArrayBool, true
	case int64(vm.FBKindMixed):
		return runtime.ArrayMixed, false
	}
	if len(instr.Args) < 3 || instr.Args[2] == nil || instr.Args[2].Def == nil {
		return runtime.ArrayMixed, false
	}
	switch instr.Args[2].Def.Type {
	case TypeInt:
		return runtime.ArrayInt, true
	case TypeFloat:
		return runtime.ArrayFloat, true
	case TypeBool:
		return runtime.ArrayBool, true
	default:
		return runtime.ArrayMixed, false
	}
}
