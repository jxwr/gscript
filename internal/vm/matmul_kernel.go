package vm

import "github.com/gscript/gscript/internal/runtime"

func (vm *VM) tryRunMatmulWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 || !vm.noGlobalLock || !isNestedMatmulProto(cl.Proto) {
		return false, nil, nil
	}
	if !args[0].IsTable() || !args[1].IsTable() || !args[2].IsNumber() {
		return false, nil, nil
	}
	nn := args[2].Number()
	n64 := int64(nn)
	if float64(n64) != nn || n64 < 0 || int64(int(n64)) != n64 {
		return false, nil, nil
	}
	n := int(n64)
	if n == 0 {
		return true, []runtime.Value{runtime.TableValue(runtime.NewTable())}, nil
	}
	aRows, ok := args[0].Table().PlainFloatMatrixRowsForNumericKernel(n, n)
	if !ok {
		return false, nil, nil
	}
	bRows, ok := args[1].Table().PlainFloatMatrixRowsForNumericKernel(n, n)
	if !ok {
		return false, nil, nil
	}

	c := runtime.NewDenseMatrix(n, n)
	cFlat, cStride, ok := c.DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil, nil
	}

	bTransposed := vm.wholeCallFloatScratch(n * n)
	for j := 0; j < n; j++ {
		col := bTransposed[j*n : (j+1)*n]
		for k := 0; k < n; k++ {
			col[k] = bRows[k][j]
		}
	}

	for i := 0; i < n; i++ {
		aRow := aRows[i]
		cRow := cFlat[i*cStride : i*cStride+n]
		j := 0
		for ; j+3 < n; j += 4 {
			bCol0 := bTransposed[j*n : (j+1)*n]
			bCol1 := bTransposed[(j+1)*n : (j+2)*n]
			bCol2 := bTransposed[(j+2)*n : (j+3)*n]
			bCol3 := bTransposed[(j+3)*n : (j+4)*n]
			sum0 := 0.0
			sum1 := 0.0
			sum2 := 0.0
			sum3 := 0.0
			for k := 0; k < n; k++ {
				av := aRow[k]
				sum0 += av * bCol0[k]
				sum1 += av * bCol1[k]
				sum2 += av * bCol2[k]
				sum3 += av * bCol3[k]
			}
			cRow[j] = sum0
			cRow[j+1] = sum1
			cRow[j+2] = sum2
			cRow[j+3] = sum3
		}
		for ; j < n; j++ {
			bCol := bTransposed[j*n : (j+1)*n]
			sum := 0.0
			for k := 0; k < n; k++ {
				sum += aRow[k] * bCol[k]
			}
			cRow[j] = sum
		}
	}
	return true, []runtime.Value{runtime.TableValue(c)}, nil
}

func IsNestedMatmulKernelProto(p *FuncProto) bool {
	return isNestedMatmulProto(p)
}

func isNestedMatmulProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 3 || p.IsVarArg || len(p.Constants) != 1 || !numberConst(p.Constants[0], 0.0) {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_NEWTABLE, 3, 0, 0),
		EncodeAsBx(OP_LOADINT, 4, 0),
		EncodeABC(OP_MOVE, 8, 2, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_SUB, 5, 8, 9),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeAsBx(OP_FORPREP, 4, 33),
		EncodeABC(OP_NEWTABLE, 8, 0, 0),
		EncodeABC(OP_MOVE, 10, 7, 0),
		EncodeABC(OP_GETTABLE, 9, 0, 10),
		EncodeAsBx(OP_LOADINT, 10, 0),
		EncodeABC(OP_MOVE, 14, 2, 0),
		EncodeAsBx(OP_LOADINT, 15, 1),
		EncodeABC(OP_SUB, 11, 14, 15),
		EncodeAsBx(OP_LOADINT, 12, 1),
		EncodeAsBx(OP_FORPREP, 10, 20),
		EncodeABx(OP_LOADK, 14, 0),
		EncodeAsBx(OP_LOADINT, 15, 0),
		EncodeABC(OP_MOVE, 19, 2, 0),
		EncodeAsBx(OP_LOADINT, 20, 1),
		EncodeABC(OP_SUB, 16, 19, 20),
		EncodeAsBx(OP_LOADINT, 17, 1),
		EncodeAsBx(OP_FORPREP, 15, 9),
		EncodeABC(OP_MOVE, 22, 18, 0),
		EncodeABC(OP_GETTABLE, 21, 9, 22),
		EncodeABC(OP_MOVE, 24, 18, 0),
		EncodeABC(OP_GETTABLE, 23, 1, 24),
		EncodeABC(OP_MOVE, 24, 13, 0),
		EncodeABC(OP_GETTABLE, 22, 23, 24),
		EncodeABC(OP_MUL, 20, 21, 22),
		EncodeABC(OP_ADD, 19, 14, 20),
		EncodeABC(OP_MOVE, 14, 19, 0),
		EncodeAsBx(OP_FORLOOP, 15, -10),
		EncodeABC(OP_MOVE, 18, 14, 0),
		EncodeABC(OP_MOVE, 19, 13, 0),
		EncodeABC(OP_SETTABLE, 8, 19, 18),
		EncodeAsBx(OP_FORLOOP, 10, -21),
		EncodeABC(OP_MOVE, 16, 8, 0),
		EncodeABC(OP_MOVE, 17, 7, 0),
		EncodeABC(OP_SETTABLE, 3, 17, 16),
		EncodeAsBx(OP_FORLOOP, 4, -34),
		EncodeABC(OP_MOVE, 13, 3, 0),
		EncodeABC(OP_RETURN, 13, 2, 0),
	})
}
