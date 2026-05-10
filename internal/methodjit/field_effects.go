package methodjit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gscript/gscript/internal/vm"
)

// FieldEffectSummary records conservative static field writes for fixed
// parameter tables. It is intentionally small: consumers may use known writes
// to prove that a nearby field read/length cannot be invalidated by a native
// typed-peer callee, while unknown mutations keep the optimization disabled.
type FieldEffectSummary struct {
	ParamWrites          map[int]map[string]bool
	UnknownParamMutation map[int]bool
	HasCall              bool
}

func SummarizeFieldEffects(proto *vm.FuncProto) FieldEffectSummary {
	s := FieldEffectSummary{
		ParamWrites:          make(map[int]map[string]bool),
		UnknownParamMutation: make(map[int]bool),
	}
	fn := BuildGraph(proto)
	if fn == nil || fn.Proto == nil {
		return s
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpSetField:
				param, ok := fieldEffectParamBase(instr)
				if !ok {
					continue
				}
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" {
					s.UnknownParamMutation[param] = true
					continue
				}
				if s.ParamWrites[param] == nil {
					s.ParamWrites[param] = make(map[string]bool)
				}
				s.ParamWrites[param][name] = true
			case OpSetTable, OpSetList, OpAppend, OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs,
				OpTableBoolArrayFill, OpTableIntArrayReversePrefix, OpTableIntArrayCopyPrefix:
				if param, ok := fieldEffectParamBase(instr); ok {
					s.UnknownParamMutation[param] = true
				}
			case OpCall, OpCallFloor, OpFieldCallFloor, OpResume, OpYield, OpSelf, OpGo, OpSend, OpRecv:
				s.HasCall = true
			}
		}
	}
	return s
}

func fieldEffectParamBase(instr *Instr) (int, bool) {
	if instr == nil || len(instr.Args) == 0 || instr.Args[0] == nil || instr.Args[0].Def == nil {
		return 0, false
	}
	base := instr.Args[0].Def
	if base.Op != OpLoadSlot || base.Aux < 0 {
		return 0, false
	}
	return int(base.Aux), true
}

func (s FieldEffectSummary) WritesParamField(param int, field string) bool {
	return s.ParamWrites[param] != nil && s.ParamWrites[param][field]
}

func (s FieldEffectSummary) ParamMutationKnown(param int) bool {
	return !s.UnknownParamMutation[param] && !s.HasCall
}

func (s FieldEffectSummary) FormatParam(param int) string {
	fields := make([]string, 0, len(s.ParamWrites[param]))
	for field := range s.ParamWrites[param] {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	state := "known"
	if !s.ParamMutationKnown(param) {
		state = "unknown"
	}
	if len(fields) == 0 {
		return fmt.Sprintf("p%d:%s:none", param, state)
	}
	return fmt.Sprintf("p%d:%s:%s", param, state, strings.Join(fields, "|"))
}
