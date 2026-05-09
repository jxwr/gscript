package methodjit

const (
	nestedLoopParamRangeMax int64 = 1 << 20
	singleLoopParamRangeMax int64 = 1 << 30
)

// LoopBoundRangeGuardPass adds a narrow entry range guard for integer
// parameters used as loop bounds. The guard feeds RangeAnalysis, which can then
// prove loop-bound-derived arithmetic fits in the int48 payload range and skip
// per-op overflow checks in hot numeric/table-building kernels. Guard misses
// deopt to the interpreter, preserving correctness for wider inputs.
func LoopBoundRangeGuardPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	if !computeLoopInfo(fn).hasLoops() {
		return fn, nil
	}
	boundParamMax := loopBoundParamMax(fn)
	if len(boundParamMax) == 0 {
		functionRemarks(fn).Add("LoopBoundRangeGuard", "missed", 0, 0, OpGuardIntRange,
			"loop had no parameter-derived loop bound")
		return fn, nil
	}

	changed := false
	for _, block := range fn.Blocks {
		if block != fn.Entry {
			continue
		}
		for i := 0; i < len(block.Instrs); i++ {
			instr := block.Instrs[i]
			slot, ok := intParamTypeGuardSlot(fn, instr)
			max, shouldGuard := boundParamMax[slot]
			if !ok || !shouldGuard {
				continue
			}
			if nextIsRangeGuard(block, i, instr.ID, max) {
				continue
			}

			guard := &Instr{
				ID:          fn.newValueID(),
				Op:          OpGuardIntRange,
				Type:        TypeInt,
				Args:        []*Value{instr.Value()},
				Aux:         0,
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
			functionRemarks(fn).Add("LoopBoundRangeGuard", "changed", block.ID, guard.ID, guard.Op,
				"guarded loop-bound int parameter for range analysis")
			changed = true
			i++
		}
	}
	if !changed {
		functionRemarks(fn).Add("LoopBoundRangeGuard", "missed", 0, 0, OpGuardIntRange,
			"loop had no integer parameter type guard")
	}
	return fn, nil
}

func intParamTypeGuardSlot(fn *Function, instr *Instr) (int, bool) {
	if instr == nil || instr.Op != OpGuardType || instr.Type != TypeInt || Type(instr.Aux) != TypeInt || len(instr.Args) == 0 {
		return 0, false
	}
	arg := instr.Args[0]
	if arg == nil || arg.Def == nil || arg.Def.Op != OpLoadSlot {
		return 0, false
	}
	slot := int(arg.Def.Aux)
	if fn == nil || fn.Proto == nil || slot < 0 || slot >= fn.Proto.NumParams {
		return 0, false
	}
	return slot, true
}

func loopBoundParamMax(fn *Function) map[int]int64 {
	slots := make(map[int]int64)
	if fn == nil || fn.Proto == nil {
		return slots
	}
	li := computeLoopInfo(fn)
	nest := loopNest(li)
	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		cond := loopHeaderBranchCond(header)
		if cond == nil || len(cond.Args) < 2 {
			continue
		}
		switch cond.Op {
		case OpLt, OpLtInt, OpLe, OpLeInt:
		default:
			continue
		}
		max := singleLoopParamRangeMax
		if nest[header.ID] != -1 {
			max = nestedLoopParamRangeMax
		}
		collectParamSlotsInBoundExpr(fn, cond.Args[0], slots, max, make(map[int]bool))
		collectParamSlotsInBoundExpr(fn, cond.Args[1], slots, max, make(map[int]bool))
	}
	return slots
}

func collectParamSlotsInBoundExpr(fn *Function, v *Value, slots map[int]int64, max int64, seen map[int]bool) {
	if fn == nil || fn.Proto == nil || v == nil || v.Def == nil {
		return
	}
	instr := v.Def
	if seen[instr.ID] {
		return
	}
	seen[instr.ID] = true

	if instr.Op == OpLoadSlot {
		slot := int(instr.Aux)
		if slot >= 0 && slot < fn.Proto.NumParams {
			if existing, ok := slots[slot]; !ok || max < existing {
				slots[slot] = max
			}
		}
		return
	}

	switch instr.Op {
	case OpGuardType, OpGuardIntRange, OpPhi,
		OpAdd, OpAddInt, OpSub, OpSubInt, OpMul, OpMulInt,
		OpDiv, OpDivIntExact, OpNegInt, OpUnm:
	default:
		return
	}
	for _, arg := range instr.Args {
		collectParamSlotsInBoundExpr(fn, arg, slots, max, seen)
	}
}

func nextIsRangeGuard(block *Block, idx int, sourceID int, max int64) bool {
	if block == nil || idx+1 >= len(block.Instrs) {
		return false
	}
	next := block.Instrs[idx+1]
	return next != nil &&
		next.Op == OpGuardIntRange &&
		len(next.Args) == 1 &&
		next.Args[0] != nil &&
		next.Args[0].ID == sourceID &&
		next.Aux == 0 &&
		next.Aux2 == max
}

func replaceUsesAfterGuard(fn *Function, oldID int, newInstr *Instr, skipID int) {
	newVal := newInstr.Value()
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.ID == skipID {
				continue
			}
			for i, arg := range instr.Args {
				if arg != nil && arg.ID == oldID {
					instr.Args[i] = newVal
				}
			}
		}
	}
}
