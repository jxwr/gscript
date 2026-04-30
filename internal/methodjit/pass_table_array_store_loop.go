package methodjit

import "github.com/gscript/gscript/internal/vm"

// TableArrayStoreLoopVersionPass creates loop-scoped typed-array facts for
// safe local bool-table mutation loops, then lowers typed SetTable sites to
// checked OpTableArrayStore. It is intentionally narrower than full loop
// versioning: the table must be a typed local NewTable allocated outside the
// loop, a dominating bool-fill must have established length, all table
// mutations in the loop must be same-table bool stores, and the function must
// not expose a metatable mutation surface.
func TableArrayStoreLoopVersionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	if !functionHasNoTableMetatableMutationSurface(fn) {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	if li == nil || !li.hasLoops() {
		return fn, nil
	}
	dom := computeDominators(fn)
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if header == nil || !li.loopHeaders[header.ID] {
			continue
		}
		body := li.headerBlocks[header.ID]
		preheader := tableArrayStoreLoopPreheader(header, body)
		if preheader == nil {
			continue
		}
		cand, ok := tableArrayStoreLoopCandidateFor(fn, body)
		if !ok {
			continue
		}
		if !tableArrayStoreLoopHasLengthSeed(fn, dom, preheader, cand) {
			functionRemarks(fn).Add("TableArrayStoreLoopVersion", "missed", header.ID, 0, OpTableArrayStore,
				"typed mutation loop has no dominating length seed")
			continue
		}
		data, length := insertTableArrayStoreLoopFacts(fn, preheader, cand)
		for _, store := range cand.stores {
			if len(store.Args) < 3 {
				continue
			}
			store.Op = OpTableArrayStore
			store.Args = []*Value{cand.table, data, length, store.Args[1], store.Args[2]}
			store.Aux = cand.kind
			store.Aux2 = 0
			store.Type = TypeUnknown
			functionRemarks(fn).Add("TableArrayStoreLoopVersion", "changed", store.Block.ID, store.ID, store.Op,
				"lowered local typed table mutation loop store to checked raw array store")
		}
	}
	return fn, nil
}

type tableArrayStoreLoopCandidate struct {
	table  *Value
	kind   int64
	stores []*Instr
}

func tableArrayStoreLoopPreheader(header *Block, body map[int]bool) *Block {
	if header == nil || body == nil {
		return nil
	}
	var preheader *Block
	for _, pred := range header.Preds {
		if pred == nil || body[pred.ID] {
			continue
		}
		if preheader != nil {
			return nil
		}
		preheader = pred
	}
	return preheader
}

func tableArrayStoreLoopCandidateFor(fn *Function, body map[int]bool) (tableArrayStoreLoopCandidate, bool) {
	var cand tableArrayStoreLoopCandidate
	if fn == nil || body == nil {
		return cand, false
	}
	for _, block := range fn.Blocks {
		if block == nil || !body[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpSetTable:
				if len(instr.Args) < 3 || instr.Args[0] == nil || !tableArrayStoreLoopKind(instr.Aux2) {
					return cand, false
				}
				if cand.table == nil {
					cand.table = instr.Args[0]
					cand.kind = instr.Aux2
					if !tableArrayStoreLoopLocalTypedTable(cand.table, cand.kind, body) {
						return cand, false
					}
				} else if instr.Args[0].ID != cand.table.ID || instr.Aux2 != cand.kind {
					return cand, false
				}
				cand.stores = append(cand.stores, instr)
			case OpTableArrayStore:
				// Existing checked stores are structural-preserving, but this
				// pass is only responsible for pure SetTable mutation loops.
				return cand, false
			case OpCall, OpSelf, OpSetField, OpAppend, OpSetList, OpTableBoolArrayFill:
				return cand, false
			}
		}
	}
	return cand, cand.table != nil && len(cand.stores) > 0
}

func tableArrayStoreLoopKind(kind int64) bool {
	return kind == int64(vm.FBKindBool)
}

func tableArrayStoreLoopLocalTypedTable(table *Value, kind int64, body map[int]bool) bool {
	if table == nil || table.Def == nil || table.Def.Op != OpNewTable || table.Def.Block == nil {
		return false
	}
	if body != nil && body[table.Def.Block.ID] {
		return false
	}
	_, arrayKind := unpackNewTableAux2(table.Def.Aux2)
	fbKind, ok := arrayKindToFBKind(arrayKind)
	return ok && int64(fbKind) == kind
}

func tableArrayStoreLoopHasLengthSeed(fn *Function, dom *domInfo, preheader *Block, cand tableArrayStoreLoopCandidate) bool {
	if fn == nil || dom == nil || preheader == nil || cand.table == nil {
		return false
	}
	if cand.kind != int64(vm.FBKindBool) {
		return false
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		if block.ID != preheader.ID && !dom.dominates(block.ID, preheader.ID) {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpTableBoolArrayFill || len(instr.Args) < 1 || instr.Args[0] == nil {
				continue
			}
			if instr.Args[0].ID == cand.table.ID {
				return true
			}
		}
	}
	return false
}

func insertTableArrayStoreLoopFacts(fn *Function, preheader *Block, cand tableArrayStoreLoopCandidate) (*Value, *Value) {
	header := &Instr{
		ID:    fn.newValueID(),
		Op:    OpTableArrayHeader,
		Type:  TypeInt,
		Args:  []*Value{cand.table},
		Aux:   cand.kind,
		Block: preheader,
	}
	length := &Instr{
		ID:    fn.newValueID(),
		Op:    OpTableArrayLen,
		Type:  TypeInt,
		Args:  []*Value{header.Value()},
		Aux:   cand.kind,
		Block: preheader,
	}
	data := &Instr{
		ID:    fn.newValueID(),
		Op:    OpTableArrayData,
		Type:  TypeInt,
		Args:  []*Value{header.Value()},
		Aux:   cand.kind,
		Block: preheader,
	}
	if len(cand.stores) > 0 {
		header.copySourceFrom(cand.stores[0])
		length.copySourceFrom(cand.stores[0])
		data.copySourceFrom(cand.stores[0])
	}
	insertAt := len(preheader.Instrs)
	if insertAt > 0 && preheader.Instrs[insertAt-1].Op.IsTerminator() {
		insertAt--
	}
	inserted := []*Instr{header, length, data}
	preheader.Instrs = append(preheader.Instrs[:insertAt], append(inserted, preheader.Instrs[insertAt:]...)...)
	return data.Value(), length.Value()
}
