package methodjit

// TableArrayStaticBoundsPass marks typed array loads as bounds-safe when the
// table comes from a dominating SetList construction and RangeAnalysis proves
// the key stays inside the constructed array length.
func TableArrayStaticBoundsPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	facts := collectStaticTableLenFacts(fn)
	dom := computeDominators(fn)
	li := computeLoopInfo(fn)
	order := blockInstructionOrder(fn)
	lenGuards := collectDominatingTableArrayLenLowerGuards(fn)
	keyUpperGuards := collectDominatingKeyUpperGuards(fn)
	headers := make(map[int]int)
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpTableArrayHeader || len(instr.Args) < 1 || instr.Args[0] == nil {
				continue
			}
			headers[instr.ID] = instr.Args[0].ID
		}
	}
	if len(headers) == 0 && len(lenGuards) == 0 {
		return fn, nil
	}

	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpTableArrayLoad || len(instr.Args) < 3 ||
				instr.Args[1] == nil || instr.Args[2] == nil || instr.Args[1].Def == nil {
				continue
			}
			keyNonNegative, keyMax, keyMaxKnown := tableArrayStaticKeyBounds(fn, li, instr.Args[2], keyUpperGuards, dom, block.ID)
			lenInstr := instr.Args[1].Def
			if lenInstr.Op != OpTableArrayLen || len(lenInstr.Args) < 1 || lenInstr.Args[0] == nil {
				continue
			}

			if tableID, ok := headers[lenInstr.Args[0].ID]; ok {
				if fact, ok := staticTableLenFactForLen(facts[tableID], dom, order, block.ID, instr.ID); ok && fact.length >= 0 {
					if keyNonNegative {
						markTableArrayLowerBoundSafe(fn, instr)
					}
					if keyNonNegative && keyMaxKnown && keyMax <= fact.length {
						markTableArrayUpperBoundSafe(fn, instr)
						functionRemarks(fn).Add("TableArrayStaticBounds", "changed", block.ID, instr.ID, instr.Op,
							"static SetList length and key range prove table-array bounds")
						continue
					}
				}
			}

			if maxSafe, ok := dominatingTableArrayLenGuardMaxSafe(lenGuards[lenInstr.ID], dom, order, block.ID, instr.ID); ok && keyNonNegative {
				markTableArrayLowerBoundSafe(fn, instr)
				if keyMaxKnown && keyMax <= maxSafe {
					markTableArrayUpperBoundSafe(fn, instr)
					functionRemarks(fn).Add("TableArrayStaticBounds", "changed", block.ID, instr.ID, instr.Op,
						"dominating array-len guard and key range prove table-array bounds")
				}
			}
		}
	}
	return fn, nil
}

type tableArrayLenGuardFact struct {
	blockID int
	instrID int
	maxSafe int64
}

type keyUpperGuardFact struct {
	trueBlockID int
	max         int64
}

func tableArrayStaticKeyBounds(fn *Function, li *loopInfo, key *Value, guards map[int][]keyUpperGuardFact, dom *domInfo, blockID int) (bool, int64, bool) {
	if key == nil {
		return false, 0, false
	}
	nonNegative := false
	var max int64
	maxKnown := false
	if r, ok := fn.IntRanges[key.ID]; ok && r.known {
		nonNegative = r.min >= 0
		max = r.max
		maxKnown = true
	}
	if c, ok := constIntFromValue(key); ok {
		nonNegative = c >= 0
		if !maxKnown || c < max {
			max = c
			maxKnown = true
		}
	}
	if fn.IntNonNegative != nil && fn.IntNonNegative[key.ID] {
		nonNegative = true
	}
	if tableArrayKeyNonNegativeFromInduction(li, key) {
		nonNegative = true
	}
	if guardMax, ok := dominatingKeyUpperGuardMax(guards[key.ID], dom, blockID); ok {
		if !maxKnown || guardMax < max {
			max = guardMax
			maxKnown = true
		}
	}
	return nonNegative, max, maxKnown
}

func tableArrayKeyNonNegativeFromInduction(li *loopInfo, key *Value) bool {
	if li == nil || key == nil || key.Def == nil || !key.Def.Type.isIntegerLike() {
		return false
	}
	if step, phi := tableArrayForwardStepWithPhi(key.Def); step >= 0 && phi != nil && phi.Block != nil {
		if ind, ok := analyzeForwardInduction(phi, li); ok && ind.step >= 0 && ind.init.min >= 0 {
			return true
		}
	}
	return false
}

func tableArrayForwardStepWithPhi(instr *Instr) (int64, *Instr) {
	if instr == nil || len(instr.Args) < 2 {
		return 0, nil
	}
	for _, arg := range instr.Args {
		if arg == nil || arg.Def == nil || arg.Def.Op != OpPhi {
			continue
		}
		step, ok := forwardStepFromPhi(instr, arg.ID)
		if !ok {
			continue
		}
		return step, arg.Def
	}
	return 0, nil
}

func collectDominatingTableArrayLenLowerGuards(fn *Function) map[int][]tableArrayLenGuardFact {
	out := make(map[int][]tableArrayLenGuardFact)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpGuardTruthy || len(instr.Args) < 1 || instr.Args[0] == nil || instr.Args[0].Def == nil {
				continue
			}
			cond := instr.Args[0].Def
			lenID, maxSafe, ok := tableArrayLenLowerGuard(cond)
			if !ok {
				continue
			}
			out[lenID] = append(out[lenID], tableArrayLenGuardFact{
				blockID: block.ID,
				instrID: instr.ID,
				maxSafe: maxSafe,
			})
		}
	}
	return out
}

func tableArrayLenLowerGuard(cond *Instr) (int, int64, bool) {
	if cond == nil || len(cond.Args) < 2 || cond.Args[0] == nil || cond.Args[1] == nil {
		return 0, 0, false
	}
	lhsConst, lhsIsConst := constIntFromValue(cond.Args[0])
	rhsConst, rhsIsConst := constIntFromValue(cond.Args[1])
	lhsLen := tableArrayLenValueID(cond.Args[0])
	rhsLen := tableArrayLenValueID(cond.Args[1])
	switch cond.Op {
	case OpLt, OpLtInt:
		if lhsIsConst && rhsLen != 0 {
			return rhsLen, lhsConst, true
		}
		if rhsIsConst && lhsLen != 0 {
			return lhsLen, satSub(rhsConst, 1), true
		}
	case OpLe, OpLeInt:
		if lhsIsConst && rhsLen != 0 {
			return rhsLen, satSub(lhsConst, 1), true
		}
		if rhsIsConst && lhsLen != 0 {
			return lhsLen, rhsConst, true
		}
	}
	return 0, 0, false
}

func tableArrayLenValueID(v *Value) int {
	if v == nil || v.Def == nil || v.Def.Op != OpTableArrayLen {
		return 0
	}
	return v.ID
}

func collectDominatingKeyUpperGuards(fn *Function) map[int][]keyUpperGuardFact {
	out := make(map[int][]keyUpperGuardFact)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		if block == nil || len(block.Instrs) == 0 {
			continue
		}
		term := block.Instrs[len(block.Instrs)-1]
		if term == nil || term.Op != OpBranch || len(term.Args) < 1 || term.Args[0] == nil || term.Args[0].Def == nil {
			continue
		}
		trueBlockID, ok := branchTrueBlockID(term)
		if !ok {
			continue
		}
		keyID, max, ok := keyUpperGuard(term.Args[0].Def)
		if !ok {
			continue
		}
		out[keyID] = append(out[keyID], keyUpperGuardFact{trueBlockID: trueBlockID, max: max})
	}
	return out
}

func branchTrueBlockID(branch *Instr) (int, bool) {
	if branch == nil || branch.Op != OpBranch {
		return 0, false
	}
	if branch.Aux != 0 {
		return int(branch.Aux), true
	}
	if branch.Block != nil && len(branch.Block.Succs) >= 1 && branch.Block.Succs[0] != nil {
		return branch.Block.Succs[0].ID, true
	}
	return 0, false
}

func keyUpperGuard(cond *Instr) (int, int64, bool) {
	if cond == nil || len(cond.Args) < 2 || cond.Args[0] == nil || cond.Args[1] == nil {
		return 0, 0, false
	}
	lhsConst, lhsIsConst := constIntFromValue(cond.Args[0])
	rhsConst, rhsIsConst := constIntFromValue(cond.Args[1])
	switch cond.Op {
	case OpLt, OpLtInt:
		if rhsIsConst && cond.Args[0].Def != nil && cond.Args[0].Def.Type.isIntegerLike() {
			return cond.Args[0].ID, satSub(rhsConst, 1), true
		}
		if lhsIsConst && cond.Args[1].Def != nil && cond.Args[1].Def.Type.isIntegerLike() {
			return cond.Args[1].ID, lhsConst, true
		}
	case OpLe, OpLeInt:
		if rhsIsConst && cond.Args[0].Def != nil && cond.Args[0].Def.Type.isIntegerLike() {
			return cond.Args[0].ID, rhsConst, true
		}
		if lhsIsConst && cond.Args[1].Def != nil && cond.Args[1].Def.Type.isIntegerLike() {
			return cond.Args[1].ID, satSub(lhsConst, 1), true
		}
	}
	return 0, 0, false
}

func dominatingKeyUpperGuardMax(facts []keyUpperGuardFact, dom *domInfo, blockID int) (int64, bool) {
	var best int64
	ok := false
	for _, fact := range facts {
		if dom == nil || !dom.dominates(fact.trueBlockID, blockID) {
			continue
		}
		if !ok || fact.max < best {
			best = fact.max
			ok = true
		}
	}
	return best, ok
}

func dominatingTableArrayLenGuardMaxSafe(facts []tableArrayLenGuardFact, dom *domInfo, order map[int]map[int]int, blockID, instrID int) (int64, bool) {
	var best int64
	ok := false
	for _, fact := range facts {
		if !tableArrayLenGuardDominates(fact, dom, order, blockID, instrID) {
			continue
		}
		if !ok || fact.maxSafe > best {
			best = fact.maxSafe
			ok = true
		}
	}
	return best, ok
}

func tableArrayLenGuardDominates(fact tableArrayLenGuardFact, dom *domInfo, order map[int]map[int]int, blockID, instrID int) bool {
	if fact.blockID == blockID {
		blockOrder, ok := order[blockID]
		if !ok {
			return false
		}
		return blockOrder[fact.instrID] < blockOrder[instrID]
	}
	return dom != nil && dom.dominates(fact.blockID, blockID)
}

func markTableArrayLowerBoundSafe(fn *Function, instr *Instr) {
	if fn.TableArrayLowerBoundSafe == nil {
		fn.TableArrayLowerBoundSafe = make(map[int]bool)
	}
	fn.TableArrayLowerBoundSafe[instr.ID] = true
}

func markTableArrayUpperBoundSafe(fn *Function, instr *Instr) {
	if fn.TableArrayUpperBoundSafe == nil {
		fn.TableArrayUpperBoundSafe = make(map[int]bool)
	}
	fn.TableArrayUpperBoundSafe[instr.ID] = true
}
