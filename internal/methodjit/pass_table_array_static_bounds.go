package methodjit

// TableArrayStaticBoundsPass marks typed array loads as bounds-safe when the
// table comes from a dominating SetList construction and RangeAnalysis proves
// the key stays inside the constructed array length.
func TableArrayStaticBoundsPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 || len(fn.IntRanges) == 0 {
		return fn, nil
	}
	facts := collectStaticTableLenFacts(fn)
	if len(facts) == 0 {
		return fn, nil
	}
	dom := computeDominators(fn)
	order := blockInstructionOrder(fn)
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
	if len(headers) == 0 {
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
			lenInstr := instr.Args[1].Def
			if lenInstr.Op != OpTableArrayLen || len(lenInstr.Args) < 1 || lenInstr.Args[0] == nil {
				continue
			}
			tableID, ok := headers[lenInstr.Args[0].ID]
			if !ok {
				continue
			}
			fact, ok := staticTableLenFactForLen(facts[tableID], dom, order, block.ID, instr.ID)
			if !ok || fact.length < 0 {
				continue
			}
			keyRange, ok := fn.IntRanges[instr.Args[2].ID]
			if !ok || !keyRange.known {
				continue
			}
			if keyRange.min >= 0 {
				if fn.TableArrayLowerBoundSafe == nil {
					fn.TableArrayLowerBoundSafe = make(map[int]bool)
				}
				fn.TableArrayLowerBoundSafe[instr.ID] = true
			}
			if keyRange.min >= 0 && keyRange.max <= fact.length {
				if fn.TableArrayUpperBoundSafe == nil {
					fn.TableArrayUpperBoundSafe = make(map[int]bool)
				}
				fn.TableArrayUpperBoundSafe[instr.ID] = true
				functionRemarks(fn).Add("TableArrayStaticBounds", "changed", block.ID, instr.ID, instr.Op,
					"static SetList length and key range prove table-array bounds")
			}
		}
	}
	return fn, nil
}
