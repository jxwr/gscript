package methodjit

import "github.com/gscript/gscript/internal/vm"

const callResultRangeGuardMinCount uint32 = 4
const callFloorSpecRangeMin int64 = -1 << 31
const callFloorSpecRangeMax int64 = 1<<31 - 1

// CallResultRangeGuardPass turns mature call-result range feedback into an
// explicit GuardIntRange. For floor-projected calls with stable callee facts
// but no mature result range yet, it may add a conservative int32 guard so
// first-entry Tier 2 can still specialize while guard/deopt preserves semantics.
// RangeAnalysis can then consume the guarded value without trusting profile
// data unconditionally.
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
			if specGuardKindSuppressed(fn, instr.SourcePC, "GuardIntRange") {
				functionRemarks(fn).Add("CallResultRangeGuard", "missed", block.ID, instr.ID, instr.Op,
					"skipped suppressed int-range guard")
				continue
			}
			fb := fn.Proto.CallSiteFeedback[instr.SourcePC]
			min, max, reason, ok := callResultGuardRange(fn, instr, fb)
			if !ok || nextInstrIsSameIntRangeGuard(block, i, instr.ID, min, max) {
				continue
			}
			guard := newCallResultRangeGuard(fn, block, instr, min, max)
			block.Instrs = append(block.Instrs[:i+1], append([]*Instr{guard}, block.Instrs[i+1:]...)...)
			replaceValueUses(fn, instr.ID, guard.Value(), guard.ID)
			i++
			functionRemarks(fn).Add("CallResultRangeGuard", "changed", block.ID, guard.ID, guard.Op,
				reason)
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

func callResultGuardRange(fn *Function, instr *Instr, fb vm.CallSiteFeedback) (int64, int64, string, bool) {
	if min, max, ok := stableCallResultRange(fb); ok {
		return min, max, "guarded profiled call result range", true
	}
	if callFloorSpeculativeNarrowRangeCandidate(fn, instr, fb) {
		return callFloorSpecRangeMin, callFloorSpecRangeMax, "guarded speculative floor-call int32 result range", true
	}
	return 0, 0, "", false
}

func callFloorSpeculativeNarrowRangeCandidate(fn *Function, instr *Instr, fb vm.CallSiteFeedback) bool {
	if instr == nil || instr.Type != TypeInt || fb.Flags&vm.CallSiteArityPolymorphic != 0 {
		return false
	}
	switch instr.Op {
	case OpCallFloor:
		if fn != nil && fn.CallABIs != nil {
			if desc, ok := fn.CallABIs[instr.ID]; ok && desc.ReturnRep != SpecializedABIReturnNone {
				return true
			}
		}
		_, _, nativeOK := fb.StableCalleeNativeIdentity()
		_, vmOK := fb.StableCalleeVMProto()
		return nativeOK || vmOK
	case OpFieldCallFloor:
		return fn != nil && len(fn.FieldPolyShapeFacts[instr.ID]) > 0
	default:
		return false
	}
}

func newCallResultRangeGuard(fn *Function, block *Block, instr *Instr, min, max int64) *Instr {
	return &Instr{
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
}

func nextInstrIsSameIntRangeGuard(block *Block, idx int, valueID int, min, max int64) bool {
	if block == nil || idx+1 >= len(block.Instrs) {
		return false
	}
	next := block.Instrs[idx+1]
	return next != nil && next.Op == OpGuardIntRange && len(next.Args) == 1 &&
		next.Args[0] != nil && next.Args[0].ID == valueID &&
		next.Aux == min && next.Aux2 == max
}
