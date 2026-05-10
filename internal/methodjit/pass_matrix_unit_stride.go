package methodjit

type matrixUnitStrideKey struct {
	strideID int
}

// MatrixUnitStridePass speculates dense-vector style matrix accesses by
// guarding a proven runtime stride value to 1 and then exposing that fact as a
// constant to codegen. This is a generic guard+deopt lowering for any
// DenseMatrix with one-column/unit-stride rows; it is not tied to a benchmark.
func MatrixUnitStridePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		guards := make(map[matrixUnitStrideKey]*Instr)
		constOne := (*Instr)(nil)
		newInstrs := make([]*Instr, 0, len(block.Instrs))
		for _, instr := range block.Instrs {
			if matrixAccessCanUseUnitStride(fn, instr) {
				stride := instr.Args[1]
				key := matrixUnitStrideKey{strideID: stride.ID}
				guard := guards[key]
				if guard == nil {
					guard = emitIRInstr(fn, block, OpGuardIntRange, TypeInt, []*Value{stride}, 1, 1)
					guard.copySourceFrom(instr)
					guards[key] = guard
					newInstrs = append(newInstrs, guard)
					functionRemarks(fn).Add("MatrixUnitStride", "changed", block.ID, guard.ID, OpGuardIntRange,
						"guarded DenseMatrix stride to unit stride")
				}
				if constOne == nil {
					constOne = emitIRInstr(fn, block, OpConstInt, TypeInt, nil, 1, 0)
					constOne.copySourceFrom(instr)
					newInstrs = append(newInstrs, constOne)
				}
				instr.Args[1] = constOne.Value()
			}
			newInstrs = append(newInstrs, instr)
		}
		block.Instrs = newInstrs
	}
	return fn, nil
}

func matrixAccessCanUseUnitStride(fn *Function, instr *Instr) bool {
	if instr == nil {
		return false
	}
	switch instr.Op {
	case OpMatrixLoadFAt:
		if len(instr.Args) < 4 {
			return false
		}
	case OpMatrixStoreFAt:
		if len(instr.Args) < 5 {
			return false
		}
	default:
		return false
	}
	stride := instr.Args[1]
	if stride == nil || stride.Def == nil || stride.Def.Op != OpMatrixStride {
		return false
	}
	return matrixStrideValueHasStableUnitFeedback(fn, stride)
}

func matrixStrideValueHasStableUnitFeedback(fn *Function, stride *Value) bool {
	if fn == nil || fn.Proto == nil || stride == nil || stride.Def == nil || stride.Def.Op != OpMatrixStride || len(stride.Def.Args) == 0 {
		return false
	}
	param, ok := matrixParamSlot(stride.Def.Args[0])
	if !ok || param < 0 || param >= len(fn.Proto.ArgDenseMatrixStrideFeedback) {
		return false
	}
	observed, ok := fn.Proto.ArgDenseMatrixStrideFeedback[param].StableStride()
	return ok && observed == 1
}

func matrixParamSlot(v *Value) (int, bool) {
	for v != nil && v.Def != nil {
		switch v.Def.Op {
		case OpLoadSlot:
			return int(v.Def.Aux), true
		case OpGuardType, OpGuardIntRange:
			if len(v.Def.Args) == 0 {
				return 0, false
			}
			v = v.Def.Args[0]
		default:
			return 0, false
		}
	}
	return 0, false
}
