package methodjit

import "github.com/gscript/gscript/internal/vm"

// TableArrayStoreLoopVersionPass creates loop-scoped typed-array facts for safe
// local typed-table mutation loops, then lowers typed SetTable sites to checked
// OpTableArrayStore. Bool loops still require a dominating fill because nil
// bool writes have sentinel semantics. Numeric loops may grow within the
// preallocated typed backing and deopt on misses; the emitter carries the
// updated length register across those in-capacity appends.
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
		candidates := tableArrayStoreLoopCandidatesFor(fn, body)
		if len(candidates) == 0 {
			continue
		}
		for _, cand := range candidates {
			nestedBuilder := tableArrayStoreLoopNestedBuilder(li, body, cand)
			if !tableArrayStoreLoopHasLengthSeed(fn, dom, preheader, cand, nestedBuilder) {
				functionRemarks(fn).Add("TableArrayStoreLoopVersion", "missed", header.ID, 0, OpTableArrayStore,
					"typed mutation loop has no dominating length seed")
				continue
			}
			header, data, length := insertTableArrayStoreLoopFacts(fn, preheader, cand)
			for _, store := range cand.stores {
				if len(store.Args) < 3 {
					continue
				}
				store.Op = OpTableArrayStore
				store.Args = []*Value{cand.table, data, length, store.Args[1], store.Args[2], header}
				store.Aux = cand.kind
				store.Aux2 = tableArrayStoreLoopFlags(cand, nestedBuilder)
				store.Type = TypeUnknown
				functionRemarks(fn).Add("TableArrayStoreLoopVersion", "changed", store.Block.ID, store.ID, store.Op,
					"lowered local typed table mutation loop store to checked raw array store")
			}
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

func tableArrayStoreLoopCandidatesFor(fn *Function, body map[int]bool) []tableArrayStoreLoopCandidate {
	if fn == nil || body == nil {
		return nil
	}
	candidates := make(map[int]*tableArrayStoreLoopCandidate)
	var order []int
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
					return nil
				}
				tableID := instr.Args[0].ID
				cand := candidates[tableID]
				if cand == nil {
					if !tableArrayStoreLoopLocalTypedTable(instr.Args[0], instr.Aux2, body) {
						return nil
					}
					cand = &tableArrayStoreLoopCandidate{table: instr.Args[0], kind: instr.Aux2}
					candidates[tableID] = cand
					order = append(order, tableID)
				} else if cand.kind != instr.Aux2 {
					return nil
				}
				cand.stores = append(cand.stores, instr)
			case OpTableArrayStore:
				// Existing checked stores are structural-preserving, but this
				// pass is only responsible for pure SetTable mutation loops.
				return nil
			case OpCall, OpSelf, OpSetField, OpAppend, OpSetList, OpTableBoolArrayFill:
				return nil
			}
		}
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]tableArrayStoreLoopCandidate, 0, len(order))
	for _, tableID := range order {
		cand := candidates[tableID]
		if cand != nil && cand.table != nil && len(cand.stores) > 0 {
			out = append(out, *cand)
		}
	}
	return out
}

func tableArrayStoreLoopCandidateFor(fn *Function, body map[int]bool) (tableArrayStoreLoopCandidate, bool) {
	candidates := tableArrayStoreLoopCandidatesFor(fn, body)
	if len(candidates) != 1 {
		return tableArrayStoreLoopCandidate{}, false
	}
	return candidates[0], true
}

func tableArrayStoreLoopKind(kind int64) bool {
	return kind == int64(vm.FBKindInt) || kind == int64(vm.FBKindFloat) || kind == int64(vm.FBKindBool)
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

func tableArrayStoreLoopHasLengthSeed(fn *Function, dom *domInfo, preheader *Block, cand tableArrayStoreLoopCandidate, nestedBuilder bool) bool {
	if fn == nil || dom == nil || preheader == nil || cand.table == nil {
		return false
	}
	if cand.kind != int64(vm.FBKindBool) {
		return tableArrayStoreLoopNumericHasLargeTypedPrealloc(cand) ||
			(nestedBuilder && tableArrayStoreLoopNumericHasTypedPrealloc(cand))
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

func tableArrayStoreLoopNumericHasLargeTypedPrealloc(cand tableArrayStoreLoopCandidate) bool {
	if !tableArrayStoreLoopNumericHasTypedPrealloc(cand) {
		return false
	}
	return cand.table.Def.Aux > tier2FeedbackOuterLoopArrayHint
}

func tableArrayStoreLoopNumericHasTypedPrealloc(cand tableArrayStoreLoopCandidate) bool {
	if cand.table == nil || cand.table.Def == nil || cand.table.Def.Op != OpNewTable {
		return false
	}
	if cand.kind != int64(vm.FBKindInt) && cand.kind != int64(vm.FBKindFloat) {
		return false
	}
	return cand.table.Def.Aux > 0
}

func tableArrayStoreLoopFlags(cand tableArrayStoreLoopCandidate, nestedBuilder bool) int64 {
	if tableArrayStoreLoopNumericHasLargeTypedPrealloc(cand) ||
		(nestedBuilder && tableArrayStoreLoopNumericHasTypedPrealloc(cand)) {
		return tableArrayStoreFlagAllowGrow
	}
	return 0
}

func tableArrayStoreLoopNestedBuilder(li *loopInfo, body map[int]bool, cand tableArrayStoreLoopCandidate) bool {
	if li == nil || body == nil || cand.table == nil || cand.table.Def == nil || cand.table.Def.Block == nil {
		return false
	}
	defBlockID := cand.table.Def.Block.ID
	return li.loopBlocks[defBlockID] && !body[defBlockID]
}

func insertTableArrayStoreLoopFacts(fn *Function, preheader *Block, cand tableArrayStoreLoopCandidate) (*Value, *Value, *Value) {
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
	return header.Value(), data.Value(), length.Value()
}
