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

func (h *tablePreallocHint) observeIntKeyFeedback(feedback vm.TableKeyFeedback, allowLargeLoopHeadroom bool) {
	if !feedback.HasIntKey {
		return
	}
	needed := int64(feedback.MaxIntKey) + 1
	if allowLargeLoopHeadroom && needed >= tier2FeedbackOuterLoopArrayHint {
		needed += needed / 2
	}
	h.observeArrayHint(needed)
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
	globalNewTables := tablePreallocGlobalNewTables(fn, defs)
	candidates := make(map[int]tablePreallocHint)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetTable || len(instr.Args) == 0 {
				continue
			}
			forceMixed := setTableHasPolymorphicKindFeedback(fn, instr)
			tbl := instr.Args[0]
			tblDef, globalBacked := tablePreallocTableDef(tbl, defs, globalNewTables)
			if tblDef == nil || tblDef.Op != OpNewTable {
				continue
			}
			kind, hasKind := runtime.ArrayMixed, false
			if !forceMixed {
				kind, hasKind = setTableArrayKindHint(instr, defs)
			}
			hasMixedValue := setTableMixedArrayValueHint(instr, defs)
			if !forceMixed && !hasKind && instr.Aux2 == 0 && !hasMixedValue {
				continue
			}
			hint := candidates[tblDef.ID]
			arrayHint := int64(tier2FeedbackArrayHint)
			largeLoopBuilder := false
			if li != nil && tblDef.Block != nil && li.loopBlocks[block.ID] && !li.loopBlocks[tblDef.Block.ID] {
				if !globalBacked {
					arrayHint = tier2FeedbackOuterLoopArrayHint
				}
				largeLoopBuilder = true
			}
			hint.observeArrayHint(arrayHint)
			if fn.Proto != nil && fn.Proto.TableKeyFeedback != nil && instr.HasSource && instr.SourcePC >= 0 && instr.SourcePC < len(fn.Proto.TableKeyFeedback) {
				hint.observeIntKeyFeedback(fn.Proto.TableKeyFeedback[instr.SourcePC], largeLoopBuilder)
			}
			if forceMixed || hasMixedValue {
				hint.mixed = true
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
	annotateLocalTableArrayKinds(fn, candidates, globalNewTables)
	return fn, nil
}

func annotateLocalTableArrayKinds(fn *Function, candidates map[int]tablePreallocHint, globalNewTables map[int64]*Instr) {
	defs := tablePreallocDefs(fn)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || (instr.Op != OpGetTable && instr.Op != OpSetTable) || instr.Aux2 != 0 || len(instr.Args) == 0 {
				continue
			}
			tbl := instr.Args[0]
			tblDef, _ := tablePreallocTableDef(tbl, defs, globalNewTables)
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

func tablePreallocGlobalNewTables(fn *Function, defs map[int]*Instr) map[int64]*Instr {
	if fn == nil {
		return nil
	}
	type globalTableCandidate struct {
		tbl       *Instr
		ambiguous bool
	}
	candidates := make(map[int64]globalTableCandidate)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetGlobal || len(instr.Args) == 0 {
				continue
			}
			global := instr.Aux
			valDef := tablePreallocValueDef(instr.Args[0], defs)
			prev, seen := candidates[global]
			if valDef == nil || valDef.Op != OpNewTable {
				candidates[global] = globalTableCandidate{ambiguous: true}
				continue
			}
			if seen && (prev.ambiguous || prev.tbl == nil || prev.tbl.ID != valDef.ID) {
				candidates[global] = globalTableCandidate{ambiguous: true}
				continue
			}
			candidates[global] = globalTableCandidate{tbl: valDef}
		}
	}
	out := make(map[int64]*Instr)
	for global, candidate := range candidates {
		if !candidate.ambiguous && candidate.tbl != nil {
			out[global] = candidate.tbl
		}
	}
	return out
}

func tablePreallocTableDef(v *Value, defs map[int]*Instr, globalNewTables map[int64]*Instr) (*Instr, bool) {
	def := tablePreallocValueDef(v, defs)
	if def == nil || def.Op != OpGetGlobal {
		return def, false
	}
	return globalNewTables[def.Aux], true
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

func setTableHasPolymorphicKindFeedback(fn *Function, instr *Instr) bool {
	if instr == nil {
		return false
	}
	if instr.Aux2 == int64(vm.FBKindPolymorphic) {
		return true
	}
	if fn == nil || fn.Proto == nil || fn.Proto.Feedback == nil || !instr.HasSource {
		return false
	}
	if instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.Feedback) {
		return false
	}
	return fn.Proto.Feedback[instr.SourcePC].Kind == vm.FBKindPolymorphic
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

func setTableMixedArrayValueHint(instr *Instr, defs map[int]*Instr) bool {
	if len(instr.Args) < 3 {
		return false
	}
	keyDef := tablePreallocValueDef(instr.Args[1], defs)
	if keyDef == nil || keyDef.Type != TypeInt {
		return false
	}
	valDef := tablePreallocValueDef(instr.Args[2], defs)
	if valDef == nil {
		return false
	}
	switch valDef.Type {
	case TypeTable, TypeString, TypeFunction:
		return true
	default:
		return false
	}
}
