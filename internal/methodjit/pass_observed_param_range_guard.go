package methodjit

// ObservedParamRangeGuardPass adds entry range guards for integer parameters
// with stable runtime argument-range feedback. This gives RangeAnalysis concrete
// callsite-sized bounds for arithmetic derived from non-loop parameters while
// preserving semantics through normal guard deoptimization on mismatch.
func ObservedParamRangeGuardPass(fn *Function) (*Function, error) {
	if fn == nil || fn.Proto == nil || fn.Entry == nil || len(fn.Proto.ArgIntRangeFeedback) == 0 {
		return fn, nil
	}
	if specGuardKindSuppressed(fn, -1, "GuardIntRange") {
		functionRemarks(fn).Add("ObservedParamRangeGuard", "missed", 0, 0, OpGuardIntRange,
			"skipped globally suppressed int-range guard")
		return fn, nil
	}

	changed := false
	block := fn.Entry
	for i := 0; i < len(block.Instrs); i++ {
		instr := block.Instrs[i]
		slot, ok := intParamTypeGuardSlot(fn, instr)
		if !ok || slot >= len(fn.Proto.ArgIntRangeFeedback) {
			continue
		}
		rf := fn.Proto.ArgIntRangeFeedback[slot]
		min, max, stable := rf.StableRange()
		if !stable {
			continue
		}
		if rf.Count < observedParamRangeGuardMinCount && !canSingleObservationParamRangeGuard(fn, rf) {
			continue
		}
		if next := nextRangeGuardForSource(block, i, instr.ID); next != nil {
			if next.Aux <= min && next.Aux2 >= max && (next.Aux != min || next.Aux2 != max) {
				next.Aux = min
				next.Aux2 = max
				functionRemarks(fn).Add("ObservedParamRangeGuard", "changed", block.ID, next.ID, next.Op,
					"tightened observed int parameter range")
				changed = true
			}
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
		block.Instrs = append(block.Instrs, nil)
		copy(block.Instrs[i+2:], block.Instrs[i+1:])
		block.Instrs[i+1] = guard
		replaceUsesAfterGuard(fn, instr.ID, guard, guard.ID)
		functionRemarks(fn).Add("ObservedParamRangeGuard", "changed", block.ID, guard.ID, guard.Op,
			"guarded observed int parameter range")
		changed = true
		i++
	}
	if !changed {
		functionRemarks(fn).Add("ObservedParamRangeGuard", "missed", 0, 0, OpGuardIntRange,
			"no stable integer parameter range feedback")
	}
	return fn, nil
}

func canSingleObservationParamRangeGuard(fn *Function, rf interface {
	StableRange() (int64, int64, bool)
}) bool {
	if fn == nil {
		return false
	}
	min, max, stable := rf.StableRange()
	return stable && min == max && computeLoopInfo(fn).hasLoops()
}

func nextRangeGuardForSource(block *Block, idx int, sourceID int) *Instr {
	if block == nil || idx+1 >= len(block.Instrs) {
		return nil
	}
	next := block.Instrs[idx+1]
	if next == nil || next.Op != OpGuardIntRange || len(next.Args) != 1 || next.Args[0] == nil || next.Args[0].ID != sourceID {
		return nil
	}
	return next
}
