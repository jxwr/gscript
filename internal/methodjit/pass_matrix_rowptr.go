package methodjit

type matrixRowKey struct {
	flatID   int
	strideID int
	rowID    int
}

// MatrixRowPtrFactoringPass rewrites repeated MatrixLoadFAt/MatrixStoreFAt
// accesses to the same dense-matrix row into one MatrixRowPtr plus row-relative
// loads/stores. This is generic row strength reduction for matrix-heavy code:
//
//	load(flat, stride, i, c0)
//	load(flat, stride, i, c1)
//
// becomes:
//
//	row = MatrixRowPtr(flat, stride, i)
//	load(row, c0)
//	load(row, c1)
//
// Single-use rows stay in the compact Matrix*FAt form because that emits one
// MADD per access and is cheaper than materializing a row pointer.
func MatrixRowPtrFactoringPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		counts := make(map[matrixRowKey]int)
		for _, instr := range block.Instrs {
			if key, ok := matrixRowAccessKey(instr); ok {
				counts[key]++
			}
		}
		if len(counts) == 0 {
			continue
		}
		hasMultiUse := false
		for _, n := range counts {
			if n >= 2 {
				hasMultiUse = true
				break
			}
		}
		if !hasMultiUse {
			continue
		}

		rowPtrs := make(map[matrixRowKey]*Instr)
		newInstrs := make([]*Instr, 0, len(block.Instrs)+len(counts))
		for _, instr := range block.Instrs {
			key, ok := matrixRowAccessKey(instr)
			if !ok || counts[key] < 2 {
				newInstrs = append(newInstrs, instr)
				continue
			}
			row := rowPtrs[key]
			if row == nil {
				row = emitIRInstr(fn, block, OpMatrixRowPtr, TypeInt,
					[]*Value{instr.Args[0], instr.Args[1], instr.Args[2]}, 0, 0)
				row.copySourceFrom(instr)
				rowPtrs[key] = row
				newInstrs = append(newInstrs, row)
				functionRemarks(fn).Add("MatrixRowPtrFactoring", "changed", block.ID, row.ID, OpMatrixRowPtr,
					"factored repeated dense-matrix row address")
			}
			switch instr.Op {
			case OpMatrixLoadFAt:
				if col, ok := constIntFromValue(instr.Args[3]); ok && col >= 0 && col <= 4095 {
					instr.Op = OpMatrixLoadFRowConst
					instr.Args = []*Value{row.Value()}
					instr.Aux = col
				} else {
					instr.Op = OpMatrixLoadFRow
					instr.Args = []*Value{row.Value(), instr.Args[3]}
				}
			case OpMatrixStoreFAt:
				if col, ok := constIntFromValue(instr.Args[3]); ok && col >= 0 && col <= 4095 {
					instr.Op = OpMatrixStoreFRowConst
					instr.Args = []*Value{row.Value(), instr.Args[4]}
					instr.Aux = col
				} else {
					instr.Op = OpMatrixStoreFRow
					instr.Args = []*Value{row.Value(), instr.Args[3], instr.Args[4]}
				}
			}
			newInstrs = append(newInstrs, instr)
		}
		block.Instrs = newInstrs
	}
	return fn, nil
}

func matrixRowAccessKey(instr *Instr) (matrixRowKey, bool) {
	if instr == nil {
		return matrixRowKey{}, false
	}
	switch instr.Op {
	case OpMatrixLoadFAt:
		if len(instr.Args) < 4 {
			return matrixRowKey{}, false
		}
	case OpMatrixStoreFAt:
		if len(instr.Args) < 5 {
			return matrixRowKey{}, false
		}
	default:
		return matrixRowKey{}, false
	}
	return matrixRowKey{
		flatID:   instr.Args[0].ID,
		strideID: instr.Args[1].ID,
		rowID:    instr.Args[2].ID,
	}, true
}
