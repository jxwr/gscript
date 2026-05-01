package methodjit

// TableArrayStoreLowerPass rewrites typed SetTable sites to reuse split
// typed-array facts produced by earlier loads in the same block. Store paths
// that need table metadata also carry the guarded TableArrayHeader as an
// optional raw table-pointer ABI operand after the canonical five operands.
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

func lowerTableArrayStoresInBlock(fn *Function, block *Block) {
	if block == nil {
		return
	}
	facts := newTableArrayFactSet()

	for _, instr := range block.Instrs {
		if instr == nil {
			continue
		}
		switch instr.Op {
		case OpTableArrayHeader:
			if len(instr.Args) >= 1 && instr.Args[0] != nil && tableArrayLowerableKind(instr.Aux) {
				facts.RecordHeader(instr)
			}

		case OpTableArrayLen:
			facts.RecordLen(instr)

		case OpTableArrayData:
			facts.RecordData(instr)

		case OpSetTable:
			if len(instr.Args) < 3 || instr.Args[0] == nil || !tableArrayLowerableKind(instr.Aux2) {
				if len(instr.Args) >= 1 && instr.Args[0] != nil {
					facts.InvalidateTable(instr.Args[0].ID)
				}
				continue
			}
			fact, ok := facts.Complete(instr.Args[0].ID, instr.Aux2)
			if !ok || fact.data == nil || fact.len == nil {
				functionRemarks(fn).Add("TableArrayStoreLower", "missed", block.ID, instr.ID, instr.Op,
					"missing complete typed array fact")
				facts.InvalidateTable(instr.Args[0].ID)
				continue
			}
			storeArgs := []*Value{instr.Args[0], fact.data, fact.len, instr.Args[1], instr.Args[2]}
			if tableArrayStoreNeedsTablePtr(fact.kind, 0) {
				storeArgs = append(storeArgs, fact.header)
			}
			instr.Op = OpTableArrayStore
			instr.Args = storeArgs
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
				facts.InvalidateTable(instr.Args[0].ID)
			}

		case OpCall, OpSelf:
			facts.Reset()
		}
	}
}
