package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const tier2FeedbackArrayHint = 1024
const tier2FeedbackOuterLoopArrayHint = 64 * 1024
const tier2MaxFeedbackArrayHint = 1 << 20

type tablePreallocHint struct {
	arrayHint int64
	kind      runtime.ArrayKind
	mixed     bool
}

func (h *tablePreallocHint) observeArrayHint(hint int64) {
	if hint > tier2MaxFeedbackArrayHint {
		hint = tier2MaxFeedbackArrayHint
	}
	if hint > h.arrayHint {
		h.arrayHint = hint
	}
}

func (h *tablePreallocHint) observeIntKeyFeedback(feedback vm.TableKeyFeedback) {
	if !feedback.HasIntKey {
		return
	}
	h.observeArrayHint(int64(feedback.MaxIntKey) + 1)
}

// TablePreallocHintPass annotates empty table allocations that feed observed
// integer-key stores. Feedback is preferred, but local IR value types can also
// seed dense typed tables before feedback is available. The hints are consumed
// by the existing NewTable exit path, so allocation remains in Go while Tier 2
// can use pre-sized and typed-array append stores until capacity is exhausted.
func TablePreallocHintPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	defs := tablePreallocDefs(fn)
	candidates := make(map[int]tablePreallocHint)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetTable || len(instr.Args) == 0 {
				continue
			}
			if instr.Aux2 == int64(vm.FBKindPolymorphic) {
				continue
			}
			tbl := instr.Args[0]
			tblDef := tablePreallocValueDef(tbl, defs)
			if tblDef == nil || tblDef.Op != OpNewTable {
				continue
			}
			kind, hasKind := setTableArrayKindHint(instr, defs)
			if !hasKind && instr.Aux2 == 0 {
				continue
			}
			hint := candidates[tblDef.ID]
			arrayHint := int64(tier2FeedbackArrayHint)
			if li != nil && tblDef.Block != nil && li.loopBlocks[block.ID] && !li.loopBlocks[tblDef.Block.ID] {
				arrayHint = tier2FeedbackOuterLoopArrayHint
			}
			hint.observeArrayHint(arrayHint)
			if fn.Proto != nil && fn.Proto.TableKeyFeedback != nil && instr.HasSource && instr.SourcePC >= 0 && instr.SourcePC < len(fn.Proto.TableKeyFeedback) {
				hint.observeIntKeyFeedback(fn.Proto.TableKeyFeedback[instr.SourcePC])
			}
			if hasKind {
				if hint.kind == runtime.ArrayMixed {
					hint.kind = kind
				} else if hint.kind != kind {
					hint.mixed = true
				}
			} else {
				hint.mixed = true
			}
			candidates[tblDef.ID] = hint
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
	annotateLocalTableArrayKinds(fn, candidates)
	return fn, nil
}

func annotateLocalTableArrayKinds(fn *Function, candidates map[int]tablePreallocHint) {
	defs := tablePreallocDefs(fn)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || (instr.Op != OpGetTable && instr.Op != OpSetTable) || instr.Aux2 != 0 || len(instr.Args) == 0 {
				continue
			}
			tbl := instr.Args[0]
			tblDef := tablePreallocValueDef(tbl, defs)
			if tblDef == nil {
				continue
			}
			hint, ok := candidates[tblDef.ID]
			if !ok || hint.mixed || hint.kind == runtime.ArrayMixed {
				continue
			}
			if fbKind, ok := arrayKindToFBKind(hint.kind); ok {
				instr.Aux2 = int64(fbKind)
			}
		}
	}
}

func tablePreallocDefs(fn *Function) map[int]*Instr {
	defs := make(map[int]*Instr)
	if fn == nil {
		return defs
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && !instr.Op.IsTerminator() {
				defs[instr.ID] = instr
			}
		}
	}
	return defs
}

func tablePreallocValueDef(v *Value, defs map[int]*Instr) *Instr {
	if v == nil {
		return nil
	}
	if v.Def != nil {
		return v.Def
	}
	return defs[v.ID]
}

func arrayKindToFBKind(kind runtime.ArrayKind) (uint8, bool) {
	switch kind {
	case runtime.ArrayInt:
		return vm.FBKindInt, true
	case runtime.ArrayFloat:
		return vm.FBKindFloat, true
	case runtime.ArrayBool:
		return vm.FBKindBool, true
	default:
		return 0, false
	}
}

func setTableArrayKindHint(instr *Instr, defs map[int]*Instr) (runtime.ArrayKind, bool) {
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
	if len(instr.Args) < 3 {
		return runtime.ArrayMixed, false
	}
	valDef := tablePreallocValueDef(instr.Args[2], defs)
	if valDef == nil {
		return runtime.ArrayMixed, false
	}
	switch valDef.Type {
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
