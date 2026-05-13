package methodjit

import (
	"fmt"
	"sort"
)

// FieldSvalsLowerPass turns repeated monomorphic fixed-shape field reads into
// a shared guard-backed svals pointer plus direct indexed loads:
//
//	a = GetField(obj.x)  -> s = FieldSvals(obj, shape)
//	b = GetField(obj.y)     a = FieldLoad(s, xidx)
//	                        b = FieldLoad(s, yidx)
//
// The pass is intentionally generic: it keys only on the runtime shape id and
// field index already attached by feedback/fixed-shape analysis. It does not
// inspect benchmark names or field-name literals.
func FieldSvalsLowerPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for i := 0; i < 3; i++ {
		if !crossBlockFieldSvalsLower(fn) {
			break
		}
		relinkValueDefs(fn)
	}
	changed := false
	for _, block := range fn.Blocks {
		if block == nil || len(block.Instrs) == 0 {
			continue
		}
		eligible := fieldSvalsLowerEligibleKeys(block)
		cache := make(map[fieldSvalsLowerKey]*Instr)
		newInstrs := make([]*Instr, 0, len(block.Instrs))
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			if fieldSvalsGlobalBarrier(instr) {
				cache = make(map[fieldSvalsLowerKey]*Instr)
				newInstrs = append(newInstrs, instr)
				continue
			}
			if tableID, ok := fieldSvalsMutationTableID(instr); ok {
				for key := range cache {
					if key.tableID == tableID {
						delete(cache, key)
					}
				}
			}
			if fieldSvalsStoreLowerable(instr) {
				shapeID := uint32(instr.Aux2 >> 32)
				fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))
				key := fieldSvalsLowerKey{tableID: instr.Args[0].ID, shapeID: shapeID}
				if svals := cache[key]; svals != nil {
					instr.Op = OpFieldStore
					instr.Type = TypeUnknown
					instr.Args = []*Value{svals.Value(), instr.Args[1]}
					instr.Aux = int64(fieldIdx)
					instr.Aux2 = 0
					newInstrs = append(newInstrs, instr)
					changed = true
					functionRemarks(fn).Add("FieldSvalsLower", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("lowered fixed-shape field store via shared svals v%d field %d", svals.ID, fieldIdx))
					continue
				}
			}
			if !fieldSvalsLowerable(instr) {
				newInstrs = append(newInstrs, instr)
				continue
			}
			shapeID := uint32(instr.Aux2 >> 32)
			fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))
			key := fieldSvalsLowerKey{tableID: instr.Args[0].ID, shapeID: shapeID}
			if !eligible[key] {
				newInstrs = append(newInstrs, instr)
				continue
			}
			svals := cache[key]
			if svals == nil {
				svals = emitIRInstr(fn, block, OpFieldSvals, TypeInt, []*Value{instr.Args[0]}, int64(shapeID), 0)
				svals.copySourceFrom(instr)
				cache[key] = svals
				newInstrs = append(newInstrs, svals)
				functionRemarks(fn).Add("FieldSvalsLower", "changed", block.ID, svals.ID, svals.Op,
					fmt.Sprintf("created shared svals pointer for table v%d shape %d", key.tableID, key.shapeID))
			}
			if instr.Op == OpGetFieldNumToFloat {
				instr.Op = OpFieldLoadNumToFloat
			} else {
				instr.Op = OpFieldLoad
			}
			instr.Args = []*Value{svals.Value()}
			instr.Aux = int64(fieldIdx)
			instr.Aux2 = 0
			newInstrs = append(newInstrs, instr)
			changed = true
			functionRemarks(fn).Add("FieldSvalsLower", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("lowered fixed-shape field load via shared svals v%d field %d", svals.ID, fieldIdx))
		}
		block.Instrs = newInstrs
	}
	if !changed {
		return fn, nil
	}
	return fn, nil
}

func crossBlockFieldSvalsLower(fn *Function) bool {
	if fn == nil || len(fn.Blocks) == 0 {
		return false
	}
	dom := computeDominators(fn)
	if dom == nil {
		return false
	}
	groups := make(map[fieldSvalsLowerKey][]useSite)
	blockSet := make(map[fieldSvalsLowerKey]map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !fieldSvalsLowerable(instr) {
				continue
			}
			shapeID := uint32(instr.Aux2 >> 32)
			key := fieldSvalsLowerKey{tableID: instr.Args[0].ID, shapeID: shapeID}
			groups[key] = append(groups[key], useSite{block: block, instr: instr})
			if blockSet[key] == nil {
				blockSet[key] = make(map[int]bool)
			}
			blockSet[key][block.ID] = true
		}
	}
	changed := false
	keys := make([]fieldSvalsLowerKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		ai := fieldSvalsLowerDefOrder(fn, keys[i].tableID)
		aj := fieldSvalsLowerDefOrder(fn, keys[j].tableID)
		if ai.block != aj.block {
			return ai.block < aj.block
		}
		if ai.index != aj.index {
			return ai.index < aj.index
		}
		if keys[i].tableID != keys[j].tableID {
			return keys[i].tableID < keys[j].tableID
		}
		return keys[i].shapeID < keys[j].shapeID
	})
	for _, key := range keys {
		uses := groups[key]
		if len(uses) < 3 || len(blockSet[key]) < 2 {
			continue
		}
		def := valueDefByID(fn, key.tableID)
		if def == nil || def.Block == nil {
			continue
		}
		if !crossBlockFieldSvalsSafe(fn, dom, key, def.Block, uses) {
			continue
		}
		svals := &Instr{
			ID:    fn.newValueID(),
			Op:    OpFieldSvals,
			Type:  TypeInt,
			Args:  []*Value{def.Value()},
			Aux:   int64(key.shapeID),
			Block: def.Block,
		}
		svals.copySourceFrom(uses[0].instr)
		insertAfterInstr(def.Block, def, svals)
		functionRemarks(fn).Add("FieldSvalsLower", "changed", def.Block.ID, svals.ID, svals.Op,
			fmt.Sprintf("created cross-block shared svals pointer for table v%d shape %d", key.tableID, key.shapeID))
		for _, use := range uses {
			fieldIdx := int(int32(use.instr.Aux2 & 0xFFFFFFFF))
			if use.instr.Op == OpGetFieldNumToFloat {
				use.instr.Op = OpFieldLoadNumToFloat
			} else {
				use.instr.Op = OpFieldLoad
			}
			use.instr.Args = []*Value{svals.Value()}
			use.instr.Aux = int64(fieldIdx)
			use.instr.Aux2 = 0
			functionRemarks(fn).Add("FieldSvalsLower", "changed", use.block.ID, use.instr.ID, use.instr.Op,
				fmt.Sprintf("lowered cross-block fixed-shape field load via shared svals v%d field %d", svals.ID, fieldIdx))
		}
		changed = true
	}
	return changed
}

type fieldSvalsLowerOrder struct {
	block int
	index int
}

func fieldSvalsLowerDefOrder(fn *Function, id int) fieldSvalsLowerOrder {
	if fn == nil {
		return fieldSvalsLowerOrder{block: 1 << 30, index: 1 << 30}
	}
	for bi, block := range fn.Blocks {
		for ii, instr := range block.Instrs {
			if instr != nil && instr.ID == id {
				return fieldSvalsLowerOrder{block: bi, index: ii}
			}
		}
	}
	return fieldSvalsLowerOrder{block: 1 << 30, index: 1 << 30}
}

func valueDefByID(fn *Function, id int) *Instr {
	if fn == nil {
		return nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && instr.ID == id {
				return instr
			}
		}
	}
	return nil
}

func crossBlockFieldSvalsSafe(fn *Function, dom *domInfo, key fieldSvalsLowerKey, defBlock *Block, uses []useSite) bool {
	if fn == nil || dom == nil || defBlock == nil || len(uses) == 0 {
		return false
	}
	for _, use := range uses {
		if use.block == nil || use.instr == nil || !dom.dominates(defBlock.ID, use.block.ID) {
			return false
		}
	}
	for _, block := range fn.Blocks {
		if block == nil || !dom.dominates(defBlock.ID, block.ID) {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op.IsTerminator() {
				continue
			}
			if crossBlockFieldSvalsGlobalBarrier(instr) {
				return false
			}
			if tableID, ok := fieldSvalsMutationTableID(instr); ok && tableID == key.tableID {
				return false
			}
		}
	}
	return true
}

func crossBlockFieldSvalsGlobalBarrier(instr *Instr) bool {
	if instr == nil {
		return true
	}
	switch instr.Op {
	case OpCall, OpCallFloor, OpFieldCallFloor, OpResume, OpYield, OpSelf, OpSetGlobal, OpSetUpval,
		OpSetTable, OpSetList, OpAppend:
		return true
	default:
		return false
	}
}

func insertAfterInstr(block *Block, after, instr *Instr) {
	if block == nil || instr == nil {
		return
	}
	if after == nil {
		insertAtTopAfterPhis(block, instr)
		return
	}
	for i, cur := range block.Instrs {
		if cur == after {
			idx := i + 1
			block.Instrs = append(block.Instrs, nil)
			copy(block.Instrs[idx+1:], block.Instrs[idx:])
			block.Instrs[idx] = instr
			return
		}
	}
	insertBeforeTerminator(block, instr)
}

type fieldSvalsLowerKey struct {
	tableID int
	shapeID uint32
}

type useSite struct {
	block *Block
	instr *Instr
}

func fieldSvalsLowerEligibleKeys(block *Block) map[fieldSvalsLowerKey]bool {
	eligible := make(map[fieldSvalsLowerKey]bool)
	seen := make(map[fieldSvalsLowerKey]bool)
	broken := make(map[fieldSvalsLowerKey]bool)
	hasStore := make(map[fieldSvalsLowerKey]bool)
	for _, instr := range block.Instrs {
		if !fieldSvalsStoreLowerable(instr) {
			continue
		}
		shapeID := uint32(instr.Aux2 >> 32)
		hasStore[fieldSvalsLowerKey{tableID: instr.Args[0].ID, shapeID: shapeID}] = true
	}
	for _, instr := range block.Instrs {
		if fieldSvalsGlobalBarrier(instr) {
			seen = make(map[fieldSvalsLowerKey]bool)
			broken = make(map[fieldSvalsLowerKey]bool)
			continue
		}
		if tableID, ok := fieldSvalsMutationTableID(instr); ok {
			for key := range seen {
				if key.tableID == tableID {
					delete(seen, key)
					delete(broken, key)
				}
			}
			continue
		}
		if !fieldSvalsLowerable(instr) {
			if !instrPreservesFieldSvalsCache(instr) {
				for key := range seen {
					broken[key] = true
				}
			}
			continue
		}
		shapeID := uint32(instr.Aux2 >> 32)
		key := fieldSvalsLowerKey{tableID: instr.Args[0].ID, shapeID: shapeID}
		if seen[key] && (broken[key] || hasStore[key]) {
			eligible[key] = true
		}
		seen[key] = true
	}
	return eligible
}

func fieldSvalsLowerable(instr *Instr) bool {
	if instr == nil || len(instr.Args) == 0 || instr.Args[0] == nil || instr.Aux2 == 0 {
		return false
	}
	switch instr.Op {
	case OpGetField, OpGetFieldNumToFloat:
	default:
		return false
	}
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))
	return shapeID != 0 && fieldIdx >= 0
}

func fieldSvalsStoreLowerable(instr *Instr) bool {
	if instr == nil || instr.Op != OpSetField || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil || instr.Aux2 == 0 {
		return false
	}
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))
	return shapeID != 0 && fieldIdx >= 0 && valueProvenNonNil(instr.Args[1])
}

func fieldSvalsGlobalBarrier(instr *Instr) bool {
	if instr == nil {
		return true
	}
	if instr.Op.IsTerminator() {
		return true
	}
	switch instr.Op {
	case OpSetField:
		return len(instr.Args) == 0 || instr.Args[0] == nil
	case OpSetTable, OpSetList, OpAppend:
		return true
	case OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs,
		OpTableBoolArrayFill, OpTableIntArrayReversePrefix, OpTableIntArrayCopyPrefix:
		return len(instr.Args) == 0 || instr.Args[0] == nil
	case OpCall, OpCallFloor, OpFieldCallFloor, OpResume, OpYield, OpSelf, OpSetGlobal, OpSetUpval:
		return true
	default:
		return false
	}
}

func fieldSvalsMutationTableID(instr *Instr) (int, bool) {
	if instr == nil || len(instr.Args) == 0 || instr.Args[0] == nil {
		return 0, false
	}
	switch instr.Op {
	case OpSetField:
		if fieldSvalsSetFieldPreservesShape(instr) {
			return 0, false
		}
		return instr.Args[0].ID, true
	case OpSetTable, OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs,
		OpTableBoolArrayFill, OpTableIntArrayReversePrefix, OpTableIntArrayCopyPrefix,
		OpSetList, OpAppend:
		return instr.Args[0].ID, true
	default:
		return 0, false
	}
}

func fieldSvalsSetFieldPreservesShape(instr *Instr) bool {
	if instr == nil || instr.Op != OpSetField || len(instr.Args) < 2 || instr.Aux2 == 0 {
		return false
	}
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))
	if shapeID == 0 || fieldIdx < 0 {
		return false
	}
	return valueProvenNonNil(instr.Args[1])
}

func valueProvenNonNil(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	switch v.Def.Op {
	case OpConstNil:
		return false
	case OpConstInt, OpConstFloat, OpConstBool, OpConstString,
		OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpSqrt, OpFloor, OpFMA, OpFMSUB, OpNumToFloat, OpGetFieldNumToFloat,
		OpFieldLoadNumToFloat, OpLen, OpLtInt, OpLeInt, OpEqInt, OpLtFloat, OpLeFloat, OpEqString:
		return true
	default:
		return v.Def.Type == TypeInt || v.Def.Type == TypeFloat || v.Def.Type == TypeBool || v.Def.Type == TypeString || v.Def.Type == TypeTable
	}
}
