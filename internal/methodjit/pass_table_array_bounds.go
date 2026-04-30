package methodjit

// TableArrayBoundsCheckHoistPass records typed table-array loads whose upper
// bounds check is dominated by the enclosing loop header guard.
//
// The recognized shape is intentionally narrow and structural:
//
//	header:
//	  cond = key < len
//	  Branch cond -> body, exit
//	body dominated by the true successor:
//	  TableArrayLoad(data, len, key)
//
// If the loop body has calls or table mutations, the pass declines the loop so
// the load keeps its own dynamic bounds check. The negative-key check is handled
// separately by RangeAnalysis via Function.IntNonNegative.
func TableArrayBoundsCheckHoistPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		fn.TableArrayUpperBoundSafe = nil
		return fn, nil
	}

	dom := computeDominators(fn)
	safe := make(map[int]bool)
	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		guard, guardedSucc := tableArrayLoopUpperGuard(li, header)
		if guard == nil || guardedSucc == nil {
			continue
		}
		if loopMayMutateTablesOrCall(fn, li.headerBlocks[header.ID]) {
			functionRemarks(fn).Add("TableArrayBoundsHoist", "missed", header.ID, guard.ID, guard.Op,
				"loop contains a call or table mutation")
			continue
		}
		key, length := guard.Args[0], guard.Args[1]
		if key == nil || length == nil {
			continue
		}
		for _, block := range fn.Blocks {
			if !li.headerBlocks[header.ID][block.ID] || block == header {
				continue
			}
			if !dom.dominates(guardedSucc.ID, block.ID) {
				continue
			}
			for _, instr := range block.Instrs {
				if instr.Op != OpTableArrayLoad || len(instr.Args) < 3 {
					continue
				}
				if instr.Args[1] == nil || instr.Args[2] == nil {
					continue
				}
				if instr.Args[1].ID != length.ID || instr.Args[2].ID != key.ID {
					continue
				}
				safe[instr.ID] = true
				functionRemarks(fn).Add("TableArrayBoundsHoist", "changed", block.ID, instr.ID, instr.Op,
					"loop header guard proves table-array key is below len")
			}
		}
	}
	if len(safe) == 0 {
		fn.TableArrayUpperBoundSafe = nil
		return fn, nil
	}
	fn.TableArrayUpperBoundSafe = safe
	return fn, nil
}

func tableArrayLoopUpperGuard(li *loopInfo, header *Block) (*Instr, *Block) {
	if header == nil || len(header.Instrs) == 0 || len(header.Succs) < 2 {
		return nil, nil
	}
	term := header.Instrs[len(header.Instrs)-1]
	if term.Op != OpBranch || len(term.Args) == 0 || term.Args[0] == nil || term.Args[0].Def == nil {
		return nil, nil
	}
	cond := term.Args[0].Def
	if cond.Op != OpLtInt || len(cond.Args) < 2 {
		return nil, nil
	}
	body := li.headerBlocks[header.ID]
	if body == nil {
		return nil, nil
	}
	trueSucc, falseSucc := header.Succs[0], header.Succs[1]
	if body[trueSucc.ID] && !body[falseSucc.ID] {
		return cond, trueSucc
	}
	if !body[trueSucc.ID] && body[falseSucc.ID] {
		return nil, nil
	}
	return nil, nil
}

func loopMayMutateTablesOrCall(fn *Function, body map[int]bool) bool {
	if fn == nil || body == nil {
		return true
	}
	for _, block := range fn.Blocks {
		if !body[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall, OpSelf, OpSetTable, OpSetField, OpAppend, OpSetList:
				return true
			}
		}
	}
	return false
}
