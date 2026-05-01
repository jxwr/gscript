package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
)

// FixedTableConstructorLoweringPass combines surviving fixed-field table
// constructors into one value-producing op after escape analysis has had a
// chance to scalar-replace the expanded NewTable+SetField form.
func FixedTableConstructorLoweringPass(fn *Function) (*Function, error) {
	if fn == nil || fn.Proto == nil {
		return fn, nil
	}
	if len(fn.FixedTableConstructors) > 0 {
		for _, block := range fn.Blocks {
			for i, instr := range block.Instrs {
				if instr == nil || instr.Op != OpNewTable {
					continue
				}
				fact, ok := fn.FixedTableConstructors[instr.ID]
				if !ok {
					continue
				}
				if lowerFixedTableConstructor2(fn, block, i, instr, fact) ||
					lowerFixedTableConstructorN(fn, block, i, instr, fact) {
					functionRemarks(fn).Add("FixedTableConstructorLowering", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("lowered fixed table constructor fields=%v", fact.FieldNames))
				}
			}
		}
	}
	lowerMaterializedTableConstructors(fn)
	return fn, nil
}

func lowerFixedTableConstructor2(fn *Function, block *Block, idx int, alloc *Instr, fact FixedTableConstructorFact) bool {
	if fn == nil || fn.Proto == nil || block == nil || alloc == nil {
		return false
	}
	if fact.Ctor2Index < 0 || fact.Ctor2Index >= len(fn.Proto.TableCtors2) {
		return false
	}
	ctor := fn.Proto.TableCtors2[fact.Ctor2Index]
	if ctor.Runtime.Key1 == ctor.Runtime.Key2 {
		return false
	}
	set1, pos, ok := nextFixedCtorSetField(block.Instrs, idx+1, alloc.ID, int64(ctor.Key1Const))
	if !ok {
		return false
	}
	set2, _, ok := nextFixedCtorSetField(block.Instrs, pos, alloc.ID, int64(ctor.Key2Const))
	if !ok {
		return false
	}
	if len(set1.Args) < 2 || set1.Args[1] == nil || len(set2.Args) < 2 || set2.Args[1] == nil {
		return false
	}

	alloc.Op = OpNewFixedTable
	alloc.Type = TypeTable
	alloc.Args = []*Value{set1.Args[1], set2.Args[1]}
	alloc.Aux = int64(fact.Ctor2Index)
	alloc.Aux2 = 2
	nopInstruction(set1)
	nopInstruction(set2)
	return true
}

func lowerFixedTableConstructorN(fn *Function, block *Block, idx int, alloc *Instr, fact FixedTableConstructorFact) bool {
	if fn == nil || fn.Proto == nil || block == nil || alloc == nil {
		return false
	}
	if fact.CtorNIndex < 0 || fact.CtorNIndex >= len(fn.Proto.TableCtorsN) {
		return false
	}
	ctor := fn.Proto.TableCtorsN[fact.CtorNIndex]
	if len(ctor.KeyConsts) == 0 || len(ctor.KeyConsts) != len(ctor.Runtime.Keys) {
		return false
	}
	values := make([]*Value, 0, len(ctor.KeyConsts))
	pos := idx + 1
	for _, keyConst := range ctor.KeyConsts {
		set, next, ok := nextFixedCtorSetField(block.Instrs, pos, alloc.ID, int64(keyConst))
		if !ok || len(set.Args) < 2 || set.Args[1] == nil {
			return false
		}
		values = append(values, set.Args[1])
		pos = next
	}

	alloc.Op = OpNewFixedTable
	alloc.Type = TypeTable
	alloc.Args = values
	alloc.Aux = int64(fact.CtorNIndex)
	alloc.Aux2 = int64(len(values))
	for i := idx + 1; i < pos; i++ {
		instr := block.Instrs[i]
		if instr != nil && instr.Op == OpSetField && len(instr.Args) > 0 && instr.Args[0] != nil && instr.Args[0].ID == alloc.ID {
			nopInstruction(instr)
		}
	}
	return true
}

func nextFixedCtorSetField(instrs []*Instr, start, allocID int, constIdx int64) (*Instr, int, bool) {
	for i := start; i < len(instrs); i++ {
		instr := instrs[i]
		if instr == nil || instr.Op == OpNop {
			continue
		}
		if instr.Op != OpSetField || instr.Aux != constIdx || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[0].ID != allocID {
			return nil, i, false
		}
		return instr, i + 1, true
	}
	return nil, len(instrs), false
}

func nopInstruction(instr *Instr) {
	instr.Op = OpNop
	instr.Type = TypeUnknown
	instr.Args = nil
	instr.Aux = 0
	instr.Aux2 = 0
}

type materializedCtorUse struct {
	instr  *Instr
	block  *Block
	index  int
	argIdx int
}

type materializedCtorStore struct {
	use      materializedCtorUse
	keyConst int
	key      string
	value    *Value
}

type materializedCtorCandidate struct {
	alloc      *Instr
	allocBlock *Block
	allocIndex int
	stores     []materializedCtorStore
	escapes    []materializedCtorUse
}

func lowerMaterializedTableConstructors(fn *Function) {
	if fn == nil || fn.Proto == nil {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpNewTable {
				continue
			}
			cand, ok := findMaterializedCtorCandidate(fn, instr)
			if !ok {
				continue
			}
			if rewriteMaterializedCtor(fn, cand) {
				functionRemarks(fn).Add("FixedTableConstructorLowering", "changed", cand.allocBlock.ID, cand.alloc.ID, cand.alloc.Op,
					fmt.Sprintf("lowered materialized table constructor fields=%v", materializedCtorFieldNames(cand)))
			}
		}
	}
}

func findMaterializedCtorCandidate(fn *Function, alloc *Instr) (*materializedCtorCandidate, bool) {
	if fn == nil || fn.Proto == nil || alloc == nil || alloc.Op != OpNewTable {
		return nil, false
	}
	if alloc.Aux != 0 || alloc.Aux2 <= 2 || alloc.Aux2 > runtime.SmallFieldCap {
		return nil, false
	}
	positions := instructionPositions(fn)
	allocPos, ok := positions[alloc.ID]
	if !ok {
		return nil, false
	}
	cand := &materializedCtorCandidate{
		alloc:      alloc,
		allocBlock: allocPos.block,
		allocIndex: allocPos.index,
	}
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr == nil || instr.Op == OpNop {
				continue
			}
			for argIdx, arg := range instr.Args {
				if arg == nil || arg.ID != alloc.ID {
					continue
				}
				use := materializedCtorUse{instr: instr, block: block, index: i, argIdx: argIdx}
				if instr.Op == OpSetField && argIdx == 0 {
					keyConst := int(instr.Aux)
					if keyConst < 0 || keyConst >= len(fn.Proto.Constants) {
						return nil, false
					}
					keyVal := fn.Proto.Constants[keyConst]
					if !keyVal.IsString() || len(instr.Args) < 2 || instr.Args[1] == nil {
						return nil, false
					}
					cand.stores = append(cand.stores, materializedCtorStore{
						use:      use,
						keyConst: keyConst,
						key:      keyVal.Str(),
						value:    instr.Args[1],
					})
					continue
				}
				cand.escapes = append(cand.escapes, use)
			}
		}
	}
	if len(cand.stores) <= 2 || len(cand.stores) > runtime.SmallFieldCap || len(cand.escapes) == 0 {
		return nil, false
	}
	if materializedCtorHasDuplicateKeys(cand.stores) {
		return nil, false
	}
	if !materializedCtorUsesAreOrdered(fn, cand, positions) {
		return nil, false
	}
	return cand, true
}

func materializedCtorHasDuplicateKeys(stores []materializedCtorStore) bool {
	seen := make(map[string]struct{}, len(stores))
	for _, store := range stores {
		if _, ok := seen[store.key]; ok {
			return true
		}
		seen[store.key] = struct{}{}
	}
	return false
}

type instrPosition struct {
	block *Block
	index int
}

func instructionPositions(fn *Function) map[int]instrPosition {
	positions := make(map[int]instrPosition)
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr != nil {
				positions[instr.ID] = instrPosition{block: block, index: i}
			}
		}
	}
	return positions
}

func materializedCtorUsesAreOrdered(fn *Function, cand *materializedCtorCandidate, positions map[int]instrPosition) bool {
	dom := computeDominators(fn)
	for _, store := range cand.stores {
		if !instrStrictlyDominates(dom, cand.allocBlock, cand.allocIndex, store.use.block, store.use.index) {
			return false
		}
		for _, escape := range cand.escapes {
			if !instrStrictlyDominates(dom, store.use.block, store.use.index, escape.block, escape.index) {
				return false
			}
		}
	}
	escapeBlock := cand.escapes[0].block
	for _, escape := range cand.escapes[1:] {
		if escape.block != escapeBlock {
			return false
		}
	}
	if _, ok := positions[cand.alloc.ID]; !ok {
		return false
	}
	return true
}

func instrStrictlyDominates(dom *domInfo, aBlock *Block, aIndex int, bBlock *Block, bIndex int) bool {
	if aBlock == nil || bBlock == nil {
		return false
	}
	if aBlock.ID == bBlock.ID {
		return aIndex < bIndex
	}
	return dom != nil && dom.dominates(aBlock.ID, bBlock.ID)
}

func rewriteMaterializedCtor(fn *Function, cand *materializedCtorCandidate) bool {
	if fn == nil || fn.Proto == nil || cand == nil || len(cand.stores) == 0 || len(cand.escapes) == 0 {
		return false
	}
	escapeBlock := cand.escapes[0].block
	insertAt := cand.escapes[0].index
	for _, escape := range cand.escapes[1:] {
		if escape.index < insertAt {
			insertAt = escape.index
		}
	}
	if escapeBlock == nil || insertAt < 0 || insertAt > len(escapeBlock.Instrs) {
		return false
	}
	keys := make([]string, len(cand.stores))
	args := make([]*Value, len(cand.stores))
	for i, store := range cand.stores {
		keys[i] = store.key
		args[i] = store.value
	}
	ctorIdx := ensureFuncProtoTableCtorN(fn.Proto, keys)
	fixed := &Instr{
		ID:    fn.newValueID(),
		Op:    OpNewFixedTable,
		Type:  TypeTable,
		Args:  args,
		Aux:   int64(ctorIdx),
		Aux2:  int64(len(args)),
		Block: escapeBlock,
	}
	fixed.copySourceFrom(cand.alloc)
	escapeBlock.Instrs = append(escapeBlock.Instrs, nil)
	copy(escapeBlock.Instrs[insertAt+1:], escapeBlock.Instrs[insertAt:])
	escapeBlock.Instrs[insertAt] = fixed

	rewriteValueUses(fn, cand.alloc.ID, fixed.Value())
	for _, store := range cand.stores {
		nopInstruction(store.use.instr)
	}
	return true
}

func rewriteValueUses(fn *Function, oldID int, repl *Value) {
	if fn == nil || repl == nil {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			for i, arg := range instr.Args {
				if arg != nil && arg.ID == oldID {
					instr.Args[i] = repl
				}
			}
		}
	}
}

func materializedCtorFieldNames(cand *materializedCtorCandidate) []string {
	if cand == nil || len(cand.stores) == 0 {
		return nil
	}
	fields := make([]string, len(cand.stores))
	for i, store := range cand.stores {
		fields[i] = store.key
	}
	return fields
}
