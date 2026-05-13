package methodjit

import "github.com/gscript/gscript/internal/vm"

const callResultRangeGuardMinCount uint32 = 4

// CallResultRangeGuardPass turns mature call-result range feedback into an
// explicit GuardIntRange. RangeAnalysis can then consume the guarded value
// without trusting profile data unconditionally.
func CallResultRangeGuardPass(fn *Function) (*Function, error) {
	if fn == nil || fn.Proto == nil || fn.Proto.CallSiteFeedback == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for i := 0; i < len(block.Instrs); i++ {
			instr := block.Instrs[i]
			if instr == nil || !callResultRangeGuardCandidate(instr) {
				continue
			}
			if !instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
				continue
			}
			fb := fn.Proto.CallSiteFeedback[instr.SourcePC]
			min, max, ok := stableCallResultRange(fb)
			if !ok {
				continue
			}
			guard := &Instr{
				ID:          fn.newValueID(),
				Op:          OpGuardIntRange,
				Type:        TypeInt,
				Args:        []*Value{instr.Value()},
				Aux:         min,
				Aux2:        max,
				Block:       block,
				HasSource:   instr.HasSource,
				SourceProto: instr.SourceProto,
				SourcePC:    instr.SourcePC,
				SourceLine:  instr.SourceLine,
			}
			block.Instrs = append(block.Instrs[:i+1], append([]*Instr{guard}, block.Instrs[i+1:]...)...)
			replaceValueUses(fn, instr.ID, guard.Value(), guard.ID)
			i++
			functionRemarks(fn).Add("CallResultRangeGuard", "changed", block.ID, guard.ID, guard.Op,
				"guarded profiled call result range")
		}
	}
	return fn, nil
}

func callResultRangeGuardCandidate(instr *Instr) bool {
	switch instr.Op {
	case OpCall, OpCallFloor, OpFieldCallFloor:
		return instr.Type == TypeInt
	default:
		return false
	}
}

func stableCallResultRange(fb vm.CallSiteFeedback) (int64, int64, bool) {
	if fb.Count < callResultRangeGuardMinCount || fb.ResultRange.Count < callResultRangeGuardMinCount ||
		fb.Flags&vm.CallSiteArityPolymorphic != 0 {
		return 0, 0, false
	}
	return fb.ResultRange.StableRange()
}
