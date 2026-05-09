package methodjit

import "fmt"

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
			if fieldSvalsLowerBarrier(instr) {
				cache = make(map[fieldSvalsLowerKey]*Instr)
				newInstrs = append(newInstrs, instr)
				continue
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

type fieldSvalsLowerKey struct {
	tableID int
	shapeID uint32
}

func fieldSvalsLowerEligibleKeys(block *Block) map[fieldSvalsLowerKey]bool {
	eligible := make(map[fieldSvalsLowerKey]bool)
	seen := make(map[fieldSvalsLowerKey]bool)
	broken := make(map[fieldSvalsLowerKey]bool)
	for _, instr := range block.Instrs {
		if fieldSvalsLowerBarrier(instr) {
			seen = make(map[fieldSvalsLowerKey]bool)
			broken = make(map[fieldSvalsLowerKey]bool)
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
		if seen[key] && broken[key] {
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

func fieldSvalsLowerBarrier(instr *Instr) bool {
	if instr == nil {
		return true
	}
	if instr.Op.IsTerminator() {
		return true
	}
	switch instr.Op {
	case OpSetField:
		return !fieldSvalsSetFieldPreservesShape(instr)
	case OpSetTable, OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs,
		OpTableBoolArrayFill, OpTableIntArrayReversePrefix, OpTableIntArrayCopyPrefix,
		OpSetList, OpAppend, OpCall, OpResume, OpYield, OpSelf, OpSetGlobal, OpSetUpval:
		return true
	default:
		return false
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
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpSqrt, OpFloor, OpFMA, OpFMSUB, OpNumToFloat, OpGetFieldNumToFloat,
		OpFieldLoadNumToFloat, OpLen, OpLtInt, OpLeInt, OpEqInt, OpLtFloat, OpLeFloat:
		return true
	default:
		return v.Def.Type == TypeInt || v.Def.Type == TypeFloat || v.Def.Type == TypeBool || v.Def.Type == TypeString || v.Def.Type == TypeTable
	}
}
