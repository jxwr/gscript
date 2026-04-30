package methodjit

// LoopRegionVersioningPass recognizes single-entry natural loops whose
// preheader carries typed table-array facts and whose header branch proves a
// key is below the table-array len on every continuing iteration.
//
// This first stage does not clone CFG blocks or introduce new deopt points. It
// versions the loop by reusing already-hoisted preheader guards:
//
//	preheader:
//	  hdr  = TableArrayHeader(t)  // table/metatable/kind guard
//	  len  = TableArrayLen(hdr)
//	  data = TableArrayData(hdr)
//	header:
//	  cond = key < len
//	  Branch cond -> body, exit
//	body:
//	  TableArrayLoad(data, len, key)
//	  TableArrayStore(t, data, len, key, value)
//
// The continuing path of OpTableArrayStore is structural-preserving: it writes
// an existing typed-array slot and does not change table kind, backing data, or
// len. Any miss exits before native execution continues, so the preheader facts
// remain valid inside the region.
func LoopRegionVersioningPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	fn.TableArrayUpperBoundSafe = nil
	fn.LoopTableArrayFacts = nil

	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return fn, nil
	}

	dom := computeDominators(fn)
	preheaders := computeLoopPreheaders(fn, li)
	safe := make(map[int]bool)
	accessFacts := make(map[int]LoopTableArrayFact)

	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		preheaderID, ok := preheaders[header.ID]
		if !ok {
			functionRemarks(fn).Add("LoopRegionVersioning", "missed", header.ID, 0, OpNop,
				"loop has no single-entry preheader")
			continue
		}
		preheader := findBlockByID(fn, preheaderID)
		if preheader == nil {
			continue
		}

		guard, guardedSucc := tableArrayLoopUpperGuard(li, header)
		if guard == nil || guardedSucc == nil {
			functionRemarks(fn).Add("LoopRegionVersioning", "missed", header.ID, 0, OpNop,
				"loop header has no key < len branch guard")
			continue
		}
		if len(guard.Args) < 2 || guard.Args[0] == nil || guard.Args[1] == nil {
			continue
		}

		regionFacts := collectLoopRegionTableArrayFacts(preheader)
		if len(regionFacts) == 0 {
			functionRemarks(fn).Add("LoopRegionVersioning", "missed", preheader.ID, 0, OpTableArrayHeader,
				"preheader has no complete table-array header/len/data fact")
			continue
		}
		if hazard, ok := loopRegionStructuralHazard(fn, li.headerBlocks[header.ID]); ok {
			hazardBlockID := header.ID
			if hazard != nil && hazard.Block != nil {
				hazardBlockID = hazard.Block.ID
			}
			hazardID, hazardOp := 0, OpNop
			if hazard != nil {
				hazardID, hazardOp = hazard.ID, hazard.Op
			}
			functionRemarks(fn).Add("LoopRegionVersioning", "missed", hazardBlockID, hazardID, hazardOp,
				"loop contains structural table mutation or call")
			continue
		}

		key, length := guard.Args[0], guard.Args[1]
		for _, block := range fn.Blocks {
			if !li.headerBlocks[header.ID][block.ID] || block == header {
				continue
			}
			if !dom.dominates(guardedSucc.ID, block.ID) {
				continue
			}
			for _, instr := range block.Instrs {
				fact, ok := loopRegionAccessFact(header.ID, preheader.ID, instr, regionFacts, key, length)
				if !ok {
					continue
				}
				safe[instr.ID] = true
				accessFacts[instr.ID] = fact
				functionRemarks(fn).Add("LoopRegionVersioning", "changed", block.ID, instr.ID, instr.Op,
					"preheader table-array fact and loop header guard prove access upper bound")
			}
		}
	}

	if len(safe) == 0 {
		return fn, nil
	}
	fn.TableArrayUpperBoundSafe = safe
	fn.LoopTableArrayFacts = accessFacts
	return fn, nil
}

// TableArrayBoundsCheckHoistPass is kept as the compatibility entry point for
// older tests and diagnostics. The implementation is now the first loop-region
// versioning stage.
func TableArrayBoundsCheckHoistPass(fn *Function) (*Function, error) {
	return LoopRegionVersioningPass(fn)
}

type loopRegionTableArrayFact struct {
	table    *Value
	headerID int
	length   *Value
	data     *Value
	kind     int64
}

func collectLoopRegionTableArrayFacts(preheader *Block) []loopRegionTableArrayFact {
	if preheader == nil {
		return nil
	}
	headers := make(map[int]tableArrayHeaderFact)
	lens := make(map[tableArrayDerivedKey]*Value)
	datas := make(map[tableArrayDerivedKey]*Value)

	for _, instr := range preheader.Instrs {
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpTableArrayHeader:
			if len(instr.Args) >= 1 && instr.Args[0] != nil && tableArrayLowerableKind(instr.Aux) {
				headers[instr.ID] = tableArrayHeaderFact{table: instr.Args[0], kind: instr.Aux}
			}
		case OpTableArrayLen:
			if len(instr.Args) >= 1 && instr.Args[0] != nil && tableArrayLowerableKind(instr.Aux) {
				lens[tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}] = instr.Value()
			}
		case OpTableArrayData:
			if len(instr.Args) >= 1 && instr.Args[0] != nil && tableArrayLowerableKind(instr.Aux) {
				datas[tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}] = instr.Value()
			}
		}
	}

	facts := make([]loopRegionTableArrayFact, 0, len(headers))
	for headerID, header := range headers {
		key := tableArrayDerivedKey{headerID: headerID, kind: header.kind}
		length := lens[key]
		data := datas[key]
		if header.table == nil || length == nil || data == nil {
			continue
		}
		facts = append(facts, loopRegionTableArrayFact{
			table:    header.table,
			headerID: headerID,
			length:   length,
			data:     data,
			kind:     header.kind,
		})
	}
	return facts
}

func loopRegionStructuralHazard(fn *Function, body map[int]bool) (*Instr, bool) {
	if fn == nil || body == nil {
		return nil, true
	}
	for _, block := range fn.Blocks {
		if !body[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpCall, OpSelf, OpSetTable, OpSetField, OpAppend, OpSetList, OpTableBoolArrayFill:
				return instr, true
			}
		}
	}
	return nil, false
}

func loopRegionAccessFact(headerID, preheaderID int, instr *Instr, facts []loopRegionTableArrayFact, key, length *Value) (LoopTableArrayFact, bool) {
	if instr == nil || key == nil || length == nil {
		return LoopTableArrayFact{}, false
	}
	switch instr.Op {
	case OpTableArrayLoad:
		if len(instr.Args) < 3 || instr.Args[0] == nil || instr.Args[1] == nil || instr.Args[2] == nil {
			return LoopTableArrayFact{}, false
		}
		if instr.Args[1].ID != length.ID || instr.Args[2].ID != key.ID {
			return LoopTableArrayFact{}, false
		}
		for _, fact := range facts {
			if fact.kind == instr.Aux && fact.length.ID == instr.Args[1].ID && fact.data.ID == instr.Args[0].ID {
				return makeLoopTableArrayFact(headerID, preheaderID, instr, fact, key), true
			}
		}
	case OpTableArrayStore:
		if len(instr.Args) < 5 || instr.Args[0] == nil || instr.Args[1] == nil ||
			instr.Args[2] == nil || instr.Args[3] == nil {
			return LoopTableArrayFact{}, false
		}
		if instr.Args[2].ID != length.ID || instr.Args[3].ID != key.ID {
			return LoopTableArrayFact{}, false
		}
		for _, fact := range facts {
			if fact.kind == instr.Aux &&
				fact.table.ID == instr.Args[0].ID &&
				fact.length.ID == instr.Args[2].ID &&
				fact.data.ID == instr.Args[1].ID {
				return makeLoopTableArrayFact(headerID, preheaderID, instr, fact, key), true
			}
		}
	}
	return LoopTableArrayFact{}, false
}

func makeLoopTableArrayFact(headerID, preheaderID int, instr *Instr, fact loopRegionTableArrayFact, key *Value) LoopTableArrayFact {
	tableID, tableHeaderID, lenID, dataID, keyID := -1, -1, -1, -1, -1
	if fact.table != nil {
		tableID = fact.table.ID
	}
	tableHeaderID = fact.headerID
	if fact.length != nil {
		lenID = fact.length.ID
	}
	if fact.data != nil {
		dataID = fact.data.ID
	}
	if key != nil {
		keyID = key.ID
	}
	return LoopTableArrayFact{
		HeaderBlockID:    headerID,
		PreheaderBlockID: preheaderID,
		TableID:          tableID,
		TableHeaderID:    tableHeaderID,
		LenID:            lenID,
		DataID:           dataID,
		KeyID:            keyID,
		Kind:             fact.kind,
		AccessOp:         instr.Op,
	}
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
	_, ok := loopRegionStructuralHazard(fn, body)
	return ok
}
