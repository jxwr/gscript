package methodjit

// TableArrayStoreLowerPass rewrites typed SetTable sites to reuse split
// typed-array facts produced by earlier loads in the same block.
//
// The replacement store is in-bounds-only on the native path: it checks key
// and value compatibility before writing, then precise-deopts on miss so the
// interpreter replays the original SETTABLE. That makes the continuing path
// structural-preserving and allows swap/reverse/partition blocks to keep using
// the same table data pointer across multiple stores.
func TableArrayStoreLowerPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		lowerTableArrayStoresInBlock(fn, block)
	}
	return fn, nil
}

type tableArrayStoreFact struct {
	data *Value
	len  *Value
	kind int64
}

type tableArrayHeaderFact struct {
	table *Value
	kind  int64
}

func lowerTableArrayStoresInBlock(fn *Function, block *Block) {
	if block == nil {
		return
	}
	headers := make(map[int]tableArrayHeaderFact)
	lens := make(map[tableArrayDerivedKey]*Value)
	datas := make(map[tableArrayDerivedKey]*Value)
	facts := make(map[tableArrayHeaderKey]tableArrayStoreFact)

	refreshFact := func(headerID int, kind int64) {
		h, ok := headers[headerID]
		if !ok || h.table == nil || h.kind != kind {
			return
		}
		key := tableArrayDerivedKey{headerID: headerID, kind: kind}
		lenVal := lens[key]
		dataVal := datas[key]
		if lenVal == nil || dataVal == nil {
			return
		}
		factKey := tableArrayHeaderKey{objID: h.table.ID, kind: kind}
		if _, exists := facts[factKey]; exists {
			return
		}
		facts[factKey] = tableArrayStoreFact{
			data: dataVal,
			len:  lenVal,
			kind: kind,
		}
	}

	clearAll := func() {
		headers = make(map[int]tableArrayHeaderFact)
		lens = make(map[tableArrayDerivedKey]*Value)
		datas = make(map[tableArrayDerivedKey]*Value)
		facts = make(map[tableArrayHeaderKey]tableArrayStoreFact)
	}
	clearTable := func(tableID int) {
		for k := range facts {
			if k.objID == tableID {
				delete(facts, k)
			}
		}
	}

	for _, instr := range block.Instrs {
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpTableArrayHeader:
			if len(instr.Args) >= 1 && instr.Args[0] != nil && tableArrayLowerableKind(instr.Aux) {
				headers[instr.ID] = tableArrayHeaderFact{table: instr.Args[0], kind: instr.Aux}
				refreshFact(instr.ID, instr.Aux)
			}

		case OpTableArrayLen:
			if len(instr.Args) >= 1 && instr.Args[0] != nil {
				headerID := instr.Args[0].ID
				lens[tableArrayDerivedKey{headerID: headerID, kind: instr.Aux}] = instr.Value()
				refreshFact(headerID, instr.Aux)
			}

		case OpTableArrayData:
			if len(instr.Args) >= 1 && instr.Args[0] != nil {
				headerID := instr.Args[0].ID
				datas[tableArrayDerivedKey{headerID: headerID, kind: instr.Aux}] = instr.Value()
				refreshFact(headerID, instr.Aux)
			}

		case OpSetTable:
			if len(instr.Args) < 3 || instr.Args[0] == nil || !tableArrayLowerableKind(instr.Aux2) {
				if len(instr.Args) >= 1 && instr.Args[0] != nil {
					clearTable(instr.Args[0].ID)
				}
				continue
			}
			key := tableArrayHeaderKey{objID: instr.Args[0].ID, kind: instr.Aux2}
			fact, ok := facts[key]
			if !ok || fact.data == nil || fact.len == nil {
				clearTable(instr.Args[0].ID)
				continue
			}
			instr.Op = OpTableArrayStore
			instr.Args = []*Value{instr.Args[0], fact.data, fact.len, instr.Args[1], instr.Args[2]}
			instr.Aux = fact.kind
			instr.Aux2 = 0
			instr.Type = TypeUnknown
			functionRemarks(fn).Add("TableArrayStoreLower", "changed", block.ID, instr.ID, instr.Op,
				"reused typed array data/len for checked in-bounds store")

		case OpTableArrayStore:
			// This store is structural-preserving on its continuing path.
			continue

		case OpAppend, OpSetList:
			if len(instr.Args) >= 1 && instr.Args[0] != nil {
				clearTable(instr.Args[0].ID)
			}

		case OpCall, OpSelf:
			clearAll()
		}
	}
}
