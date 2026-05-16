package vm

import "github.com/gscript/gscript/internal/runtime"

func (vm *VM) tryRunMatmulWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || !hotWholeCallKernelRecognized(cl.Proto, wholeCallKernelNestedMatmul) {
		return false, nil, nil
	}
	return vm.runMatmulWholeCallKernel(cl, args)
}

func (vm *VM) runMatmulWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 || !vm.noGlobalLock {
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
	if handled, results := vm.runDenseMatmulWholeCallKernel(args[0].Table(), args[1].Table(), n); handled {
		return true, results, nil
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
		for ; j+7 < n; j += 8 {
			bCol0 := bTransposed[j*n : (j+1)*n]
			bCol1 := bTransposed[(j+1)*n : (j+2)*n]
			bCol2 := bTransposed[(j+2)*n : (j+3)*n]
			bCol3 := bTransposed[(j+3)*n : (j+4)*n]
			bCol4 := bTransposed[(j+4)*n : (j+5)*n]
			bCol5 := bTransposed[(j+5)*n : (j+6)*n]
			bCol6 := bTransposed[(j+6)*n : (j+7)*n]
			bCol7 := bTransposed[(j+7)*n : (j+8)*n]
			sum0 := 0.0
			sum1 := 0.0
			sum2 := 0.0
			sum3 := 0.0
			sum4 := 0.0
			sum5 := 0.0
			sum6 := 0.0
			sum7 := 0.0
			for k := 0; k < n; k++ {
				av := aRow[k]
				sum0 += av * bCol0[k]
				sum1 += av * bCol1[k]
				sum2 += av * bCol2[k]
				sum3 += av * bCol3[k]
				sum4 += av * bCol4[k]
				sum5 += av * bCol5[k]
				sum6 += av * bCol6[k]
				sum7 += av * bCol7[k]
			}
			cRow[j] = sum0
			cRow[j+1] = sum1
			cRow[j+2] = sum2
			cRow[j+3] = sum3
			cRow[j+4] = sum4
			cRow[j+5] = sum5
			cRow[j+6] = sum6
			cRow[j+7] = sum7
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

func (vm *VM) runDenseMatmulWholeCallKernel(aTable, bTable *runtime.Table, n int) (bool, []runtime.Value) {
	aFlat, aStride, ok := aTable.DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil
	}
	bFlat, bStride, ok := bTable.DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil
	}
	c := runtime.NewDenseMatrix(n, n)
	cFlat, cStride, ok := c.DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil
	}
	bTransposed := vm.wholeCallFloatScratch(n * n)
	for j := 0; j < n; j++ {
		col := bTransposed[j*n : (j+1)*n]
		for k := 0; k < n; k++ {
			col[k] = bFlat[k*bStride+j]
		}
	}
	for i := 0; i < n; i++ {
		aRow := aFlat[i*aStride : i*aStride+n]
		cRow := cFlat[i*cStride : i*cStride+n]
		j := 0
		for ; j+7 < n; j += 8 {
			bCol0 := bTransposed[j*n : (j+1)*n]
			bCol1 := bTransposed[(j+1)*n : (j+2)*n]
			bCol2 := bTransposed[(j+2)*n : (j+3)*n]
			bCol3 := bTransposed[(j+3)*n : (j+4)*n]
			bCol4 := bTransposed[(j+4)*n : (j+5)*n]
			bCol5 := bTransposed[(j+5)*n : (j+6)*n]
			bCol6 := bTransposed[(j+6)*n : (j+7)*n]
			bCol7 := bTransposed[(j+7)*n : (j+8)*n]
			sum0 := 0.0
			sum1 := 0.0
			sum2 := 0.0
			sum3 := 0.0
			sum4 := 0.0
			sum5 := 0.0
			sum6 := 0.0
			sum7 := 0.0
			for k := 0; k < n; k++ {
				av := aRow[k]
				sum0 += av * bCol0[k]
				sum1 += av * bCol1[k]
				sum2 += av * bCol2[k]
				sum3 += av * bCol3[k]
				sum4 += av * bCol4[k]
				sum5 += av * bCol5[k]
				sum6 += av * bCol6[k]
				sum7 += av * bCol7[k]
			}
			cRow[j] = sum0
			cRow[j+1] = sum1
			cRow[j+2] = sum2
			cRow[j+3] = sum3
			cRow[j+4] = sum4
			cRow[j+5] = sum5
			cRow[j+6] = sum6
			cRow[j+7] = sum7
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
	return true, []runtime.Value{runtime.TableValue(c)}
}

func (vm *VM) runDenseMatmulTransposedWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if cl == nil || cl.Proto == nil || len(args) != 4 || !vm.noGlobalLock {
		return false, nil
	}
	if !args[0].IsTable() || !args[1].IsTable() || !args[2].IsTable() || !args[3].IsNumber() {
		return false, nil
	}
	nn := args[3].Number()
	n64 := int64(nn)
	if float64(n64) != nn || n64 < 0 || int64(int(n64)) != n64 {
		return false, nil
	}
	n := int(n64)
	aFlat, aStride, ok := args[0].Table().DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil
	}
	btFlat, btStride, ok := args[1].Table().DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil
	}
	cFlat, cStride, ok := args[2].Table().DenseFloatMatrixForNumericKernel(n, n)
	if !ok {
		return false, nil
	}
	for i := 0; i < n; i++ {
		aRow := aFlat[i*aStride : i*aStride+n]
		cRow := cFlat[i*cStride : i*cStride+n]
		j := 0
		for ; j+3 < n; j += 4 {
			btRow0 := btFlat[j*btStride : j*btStride+n]
			btRow1 := btFlat[(j+1)*btStride : (j+1)*btStride+n]
			btRow2 := btFlat[(j+2)*btStride : (j+2)*btStride+n]
			btRow3 := btFlat[(j+3)*btStride : (j+3)*btStride+n]
			sum0 := 0.0
			sum1 := 0.0
			sum2 := 0.0
			sum3 := 0.0
			for k := 0; k < n; k++ {
				av := aRow[k]
				sum0 += av * btRow0[k]
				sum1 += av * btRow1[k]
				sum2 += av * btRow2[k]
				sum3 += av * btRow3[k]
			}
			cRow[j] = sum0
			cRow[j+1] = sum1
			cRow[j+2] = sum2
			cRow[j+3] = sum3
		}
		for ; j < n; j++ {
			btRow := btFlat[j*btStride : j*btStride+n]
			sum := 0.0
			k := 0
			for ; k+3 < n; k += 4 {
				sum += aRow[k] * btRow[k]
				sum += aRow[k+1] * btRow[k+1]
				sum += aRow[k+2] * btRow[k+2]
				sum += aRow[k+3] * btRow[k+3]
			}
			for ; k < n; k++ {
				sum += aRow[k] * btRow[k]
			}
			cRow[j] = sum
		}
	}
	return true, nil
}

func IsNestedMatmulKernelProto(p *FuncProto) bool {
	return cachedWholeCallKernelRecognized(p, wholeCallKernelNestedMatmul)
}

func isNestedMatmulProto(p *FuncProto) bool {
	if isDenseMatmulProto(p) || isDenseUnroll2MatmulProto(p) || isDenseSplit2MatmulProto(p) {
		return true
	}
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

func isDenseMatmulProto(p *FuncProto) bool {
	if !hasDenseMatmulConstants(p, 26, 51) {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABx(OP_GETGLOBAL, 4, 0),
		EncodeABC(OP_GETFIELD, 3, 4, 1),
		EncodeABC(OP_MOVE, 4, 2, 0),
		EncodeABC(OP_MOVE, 5, 2, 0),
		EncodeABC(OP_CALL, 3, 3, 2),
		EncodeAsBx(OP_LOADINT, 4, 0),
		EncodeABC(OP_MOVE, 8, 2, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_SUB, 5, 8, 9),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeAsBx(OP_FORPREP, 4, 37),
		EncodeAsBx(OP_LOADINT, 8, 0),
		EncodeABC(OP_MOVE, 12, 2, 0),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_SUB, 9, 12, 13),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 30),
		EncodeABx(OP_LOADK, 12, 2),
		EncodeAsBx(OP_LOADINT, 13, 0),
		EncodeABC(OP_MOVE, 17, 2, 0),
		EncodeAsBx(OP_LOADINT, 18, 1),
		EncodeABC(OP_SUB, 14, 17, 18),
		EncodeAsBx(OP_LOADINT, 15, 1),
		EncodeAsBx(OP_FORPREP, 13, 15),
		EncodeABx(OP_GETGLOBAL, 20, 0),
		EncodeABC(OP_GETFIELD, 19, 20, 3),
		EncodeABC(OP_MOVE, 20, 0, 0),
		EncodeABC(OP_MOVE, 21, 7, 0),
		EncodeABC(OP_MOVE, 22, 16, 0),
		EncodeABC(OP_CALL, 19, 4, 2),
		EncodeABx(OP_GETGLOBAL, 21, 0),
		EncodeABC(OP_GETFIELD, 20, 21, 3),
		EncodeABC(OP_MOVE, 21, 1, 0),
		EncodeABC(OP_MOVE, 22, 16, 0),
		EncodeABC(OP_MOVE, 23, 11, 0),
		EncodeABC(OP_CALL, 20, 4, 2),
		EncodeABC(OP_MUL, 18, 19, 20),
		EncodeABC(OP_ADD, 17, 12, 18),
		EncodeABC(OP_MOVE, 12, 17, 0),
		EncodeAsBx(OP_FORLOOP, 13, -16),
		EncodeABx(OP_GETGLOBAL, 17, 0),
		EncodeABC(OP_GETFIELD, 16, 17, 4),
		EncodeABC(OP_MOVE, 17, 3, 0),
		EncodeABC(OP_MOVE, 18, 7, 0),
		EncodeABC(OP_MOVE, 19, 11, 0),
		EncodeABC(OP_MOVE, 20, 12, 0),
		EncodeABC(OP_CALL, 16, 5, 1),
		EncodeAsBx(OP_FORLOOP, 8, -31),
		EncodeAsBx(OP_FORLOOP, 4, -38),
		EncodeABC(OP_MOVE, 13, 3, 0),
		EncodeABC(OP_RETURN, 13, 2, 0),
	})
}

func isDenseUnroll2MatmulProto(p *FuncProto) bool {
	if !hasDenseMatmulConstants(p, 23, 91) {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABx(OP_GETGLOBAL, 4, 0),
		EncodeABC(OP_GETFIELD, 3, 4, 1),
		EncodeABC(OP_MOVE, 4, 2, 0),
		EncodeABC(OP_MOVE, 5, 2, 0),
		EncodeABC(OP_CALL, 3, 3, 2),
		EncodeAsBx(OP_LOADINT, 4, 0),
		EncodeABC(OP_MOVE, 8, 2, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_SUB, 5, 8, 9),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeAsBx(OP_FORPREP, 4, 77),
		EncodeAsBx(OP_LOADINT, 8, 0),
		EncodeABC(OP_MOVE, 12, 2, 0),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_SUB, 9, 12, 13),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 70),
		EncodeABx(OP_LOADK, 12, 2),
		EncodeAsBx(OP_LOADINT, 13, 0),
		EncodeAsBx(OP_LOADINT, 15, 1),
		EncodeABC(OP_ADD, 14, 13, 15),
		EncodeABC(OP_LT, 0, 14, 2),
		EncodeAsBx(OP_JMP, 0, 36),
		EncodeABx(OP_GETGLOBAL, 17, 0),
		EncodeABC(OP_GETFIELD, 16, 17, 3),
		EncodeABC(OP_MOVE, 17, 0, 0),
		EncodeABC(OP_MOVE, 18, 7, 0),
		EncodeABC(OP_MOVE, 19, 13, 0),
		EncodeABC(OP_CALL, 16, 4, 2),
		EncodeABx(OP_GETGLOBAL, 18, 0),
		EncodeABC(OP_GETFIELD, 17, 18, 3),
		EncodeABC(OP_MOVE, 18, 1, 0),
		EncodeABC(OP_MOVE, 19, 13, 0),
		EncodeABC(OP_MOVE, 20, 11, 0),
		EncodeABC(OP_CALL, 17, 4, 2),
		EncodeABC(OP_MUL, 15, 16, 17),
		EncodeABC(OP_ADD, 14, 12, 15),
		EncodeABC(OP_MOVE, 12, 14, 0),
		EncodeABx(OP_GETGLOBAL, 17, 0),
		EncodeABC(OP_GETFIELD, 16, 17, 3),
		EncodeABC(OP_MOVE, 17, 0, 0),
		EncodeABC(OP_MOVE, 18, 7, 0),
		EncodeAsBx(OP_LOADINT, 20, 1),
		EncodeABC(OP_ADD, 19, 13, 20),
		EncodeABC(OP_CALL, 16, 4, 2),
		EncodeABx(OP_GETGLOBAL, 18, 0),
		EncodeABC(OP_GETFIELD, 17, 18, 3),
		EncodeABC(OP_MOVE, 18, 1, 0),
		EncodeAsBx(OP_LOADINT, 20, 1),
		EncodeABC(OP_ADD, 19, 13, 20),
		EncodeABC(OP_MOVE, 20, 11, 0),
		EncodeABC(OP_CALL, 17, 4, 2),
		EncodeABC(OP_MUL, 15, 16, 17),
		EncodeABC(OP_ADD, 14, 12, 15),
		EncodeABC(OP_MOVE, 12, 14, 0),
		EncodeAsBx(OP_LOADINT, 15, 2),
		EncodeABC(OP_ADD, 14, 13, 15),
		EncodeABC(OP_MOVE, 13, 14, 0),
		EncodeAsBx(OP_JMP, 0, -40),
		EncodeABC(OP_LT, 0, 13, 2),
		EncodeAsBx(OP_JMP, 0, 19),
		EncodeABx(OP_GETGLOBAL, 17, 0),
		EncodeABC(OP_GETFIELD, 16, 17, 3),
		EncodeABC(OP_MOVE, 17, 0, 0),
		EncodeABC(OP_MOVE, 18, 7, 0),
		EncodeABC(OP_MOVE, 19, 13, 0),
		EncodeABC(OP_CALL, 16, 4, 2),
		EncodeABx(OP_GETGLOBAL, 18, 0),
		EncodeABC(OP_GETFIELD, 17, 18, 3),
		EncodeABC(OP_MOVE, 18, 1, 0),
		EncodeABC(OP_MOVE, 19, 13, 0),
		EncodeABC(OP_MOVE, 20, 11, 0),
		EncodeABC(OP_CALL, 17, 4, 2),
		EncodeABC(OP_MUL, 15, 16, 17),
		EncodeABC(OP_ADD, 14, 12, 15),
		EncodeABC(OP_MOVE, 12, 14, 0),
		EncodeAsBx(OP_LOADINT, 15, 1),
		EncodeABC(OP_ADD, 14, 13, 15),
		EncodeABC(OP_MOVE, 13, 14, 0),
		EncodeAsBx(OP_JMP, 0, -21),
		EncodeABx(OP_GETGLOBAL, 15, 0),
		EncodeABC(OP_GETFIELD, 14, 15, 4),
		EncodeABC(OP_MOVE, 15, 3, 0),
		EncodeABC(OP_MOVE, 16, 7, 0),
		EncodeABC(OP_MOVE, 17, 11, 0),
		EncodeABC(OP_MOVE, 18, 12, 0),
		EncodeABC(OP_CALL, 14, 5, 1),
		EncodeAsBx(OP_FORLOOP, 8, -71),
		EncodeAsBx(OP_FORLOOP, 4, -78),
		EncodeABC(OP_MOVE, 10, 3, 0),
		EncodeABC(OP_RETURN, 10, 2, 0),
	})
}

func isDenseSplit2MatmulProto(p *FuncProto) bool {
	if !hasDenseMatmulConstants(p, 25, 93) {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABx(OP_GETGLOBAL, 4, 0),
		EncodeABC(OP_GETFIELD, 3, 4, 1),
		EncodeABC(OP_MOVE, 4, 2, 0),
		EncodeABC(OP_MOVE, 5, 2, 0),
		EncodeABC(OP_CALL, 3, 3, 2),
		EncodeAsBx(OP_LOADINT, 4, 0),
		EncodeABC(OP_MOVE, 8, 2, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_SUB, 5, 8, 9),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeAsBx(OP_FORPREP, 4, 79),
		EncodeAsBx(OP_LOADINT, 8, 0),
		EncodeABC(OP_MOVE, 12, 2, 0),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_SUB, 9, 12, 13),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 72),
		EncodeABx(OP_LOADK, 12, 2),
		EncodeABx(OP_LOADK, 13, 2),
		EncodeAsBx(OP_LOADINT, 14, 0),
		EncodeAsBx(OP_LOADINT, 16, 1),
		EncodeABC(OP_ADD, 15, 14, 16),
		EncodeABC(OP_LT, 0, 15, 2),
		EncodeAsBx(OP_JMP, 0, 36),
		EncodeABx(OP_GETGLOBAL, 18, 0),
		EncodeABC(OP_GETFIELD, 17, 18, 3),
		EncodeABC(OP_MOVE, 18, 0, 0),
		EncodeABC(OP_MOVE, 19, 7, 0),
		EncodeABC(OP_MOVE, 20, 14, 0),
		EncodeABC(OP_CALL, 17, 4, 2),
		EncodeABx(OP_GETGLOBAL, 19, 0),
		EncodeABC(OP_GETFIELD, 18, 19, 3),
		EncodeABC(OP_MOVE, 19, 1, 0),
		EncodeABC(OP_MOVE, 20, 14, 0),
		EncodeABC(OP_MOVE, 21, 11, 0),
		EncodeABC(OP_CALL, 18, 4, 2),
		EncodeABC(OP_MUL, 16, 17, 18),
		EncodeABC(OP_ADD, 15, 12, 16),
		EncodeABC(OP_MOVE, 12, 15, 0),
		EncodeABx(OP_GETGLOBAL, 18, 0),
		EncodeABC(OP_GETFIELD, 17, 18, 3),
		EncodeABC(OP_MOVE, 18, 0, 0),
		EncodeABC(OP_MOVE, 19, 7, 0),
		EncodeAsBx(OP_LOADINT, 21, 1),
		EncodeABC(OP_ADD, 20, 14, 21),
		EncodeABC(OP_CALL, 17, 4, 2),
		EncodeABx(OP_GETGLOBAL, 19, 0),
		EncodeABC(OP_GETFIELD, 18, 19, 3),
		EncodeABC(OP_MOVE, 19, 1, 0),
		EncodeAsBx(OP_LOADINT, 21, 1),
		EncodeABC(OP_ADD, 20, 14, 21),
		EncodeABC(OP_MOVE, 21, 11, 0),
		EncodeABC(OP_CALL, 18, 4, 2),
		EncodeABC(OP_MUL, 16, 17, 18),
		EncodeABC(OP_ADD, 15, 13, 16),
		EncodeABC(OP_MOVE, 13, 15, 0),
		EncodeAsBx(OP_LOADINT, 16, 2),
		EncodeABC(OP_ADD, 15, 14, 16),
		EncodeABC(OP_MOVE, 14, 15, 0),
		EncodeAsBx(OP_JMP, 0, -40),
		EncodeABC(OP_ADD, 15, 12, 13),
		EncodeABC(OP_LT, 0, 14, 2),
		EncodeAsBx(OP_JMP, 0, 19),
		EncodeABx(OP_GETGLOBAL, 19, 0),
		EncodeABC(OP_GETFIELD, 18, 19, 3),
		EncodeABC(OP_MOVE, 19, 0, 0),
		EncodeABC(OP_MOVE, 20, 7, 0),
		EncodeABC(OP_MOVE, 21, 14, 0),
		EncodeABC(OP_CALL, 18, 4, 2),
		EncodeABx(OP_GETGLOBAL, 20, 0),
		EncodeABC(OP_GETFIELD, 19, 20, 3),
		EncodeABC(OP_MOVE, 20, 1, 0),
		EncodeABC(OP_MOVE, 21, 14, 0),
		EncodeABC(OP_MOVE, 22, 11, 0),
		EncodeABC(OP_CALL, 19, 4, 2),
		EncodeABC(OP_MUL, 17, 18, 19),
		EncodeABC(OP_ADD, 16, 15, 17),
		EncodeABC(OP_MOVE, 15, 16, 0),
		EncodeAsBx(OP_LOADINT, 17, 1),
		EncodeABC(OP_ADD, 16, 14, 17),
		EncodeABC(OP_MOVE, 14, 16, 0),
		EncodeAsBx(OP_JMP, 0, -21),
		EncodeABx(OP_GETGLOBAL, 17, 0),
		EncodeABC(OP_GETFIELD, 16, 17, 4),
		EncodeABC(OP_MOVE, 17, 3, 0),
		EncodeABC(OP_MOVE, 18, 7, 0),
		EncodeABC(OP_MOVE, 19, 11, 0),
		EncodeABC(OP_MOVE, 20, 15, 0),
		EncodeABC(OP_CALL, 16, 5, 1),
		EncodeAsBx(OP_FORLOOP, 8, -73),
		EncodeAsBx(OP_FORLOOP, 4, -80),
		EncodeABC(OP_MOVE, 10, 3, 0),
		EncodeABC(OP_RETURN, 10, 2, 0),
	})
}

func hasDenseMatmulConstants(p *FuncProto, maxStack, codeLen int) bool {
	return p != nil && p.NumParams == 3 && !p.IsVarArg && p.MaxStack == maxStack && len(p.Code) == codeLen &&
		len(p.Constants) == 5 &&
		p.Constants[0].IsString() && p.Constants[0].Str() == "matrix" &&
		p.Constants[1].IsString() && p.Constants[1].Str() == "dense" &&
		numberConst(p.Constants[2], 0.0) &&
		p.Constants[3].IsString() && p.Constants[3].Str() == "getf" &&
		p.Constants[4].IsString() && p.Constants[4].Str() == "setf"
}

func isDenseMatmulTransposedProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 4 || p.IsVarArg || p.MaxStack != 26 || len(p.Code) != 45 ||
		len(p.Constants) != 4 ||
		!numberConst(p.Constants[0], 0.0) ||
		!p.Constants[1].IsString() || p.Constants[1].Str() != "matrix" ||
		!p.Constants[2].IsString() || p.Constants[2].Str() != "getf" ||
		!p.Constants[3].IsString() || p.Constants[3].Str() != "setf" {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeAsBx(OP_LOADINT, 4, 0),
		EncodeABC(OP_MOVE, 8, 3, 0),
		EncodeAsBx(OP_LOADINT, 9, 1),
		EncodeABC(OP_SUB, 5, 8, 9),
		EncodeAsBx(OP_LOADINT, 6, 1),
		EncodeAsBx(OP_FORPREP, 4, 37),
		EncodeAsBx(OP_LOADINT, 8, 0),
		EncodeABC(OP_MOVE, 12, 3, 0),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_SUB, 9, 12, 13),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeAsBx(OP_FORPREP, 8, 30),
		EncodeABx(OP_LOADK, 12, 0),
		EncodeAsBx(OP_LOADINT, 13, 0),
		EncodeABC(OP_MOVE, 17, 3, 0),
		EncodeAsBx(OP_LOADINT, 18, 1),
		EncodeABC(OP_SUB, 14, 17, 18),
		EncodeAsBx(OP_LOADINT, 15, 1),
		EncodeAsBx(OP_FORPREP, 13, 15),
		EncodeABx(OP_GETGLOBAL, 20, 1),
		EncodeABC(OP_GETFIELD, 19, 20, 2),
		EncodeABC(OP_MOVE, 20, 0, 0),
		EncodeABC(OP_MOVE, 21, 7, 0),
		EncodeABC(OP_MOVE, 22, 16, 0),
		EncodeABC(OP_CALL, 19, 4, 2),
		EncodeABx(OP_GETGLOBAL, 21, 1),
		EncodeABC(OP_GETFIELD, 20, 21, 2),
		EncodeABC(OP_MOVE, 21, 1, 0),
		EncodeABC(OP_MOVE, 22, 11, 0),
		EncodeABC(OP_MOVE, 23, 16, 0),
		EncodeABC(OP_CALL, 20, 4, 2),
		EncodeABC(OP_MUL, 18, 19, 20),
		EncodeABC(OP_ADD, 17, 12, 18),
		EncodeABC(OP_MOVE, 12, 17, 0),
		EncodeAsBx(OP_FORLOOP, 13, -16),
		EncodeABx(OP_GETGLOBAL, 17, 1),
		EncodeABC(OP_GETFIELD, 16, 17, 3),
		EncodeABC(OP_MOVE, 17, 2, 0),
		EncodeABC(OP_MOVE, 18, 7, 0),
		EncodeABC(OP_MOVE, 19, 11, 0),
		EncodeABC(OP_MOVE, 20, 12, 0),
		EncodeABC(OP_CALL, 16, 5, 1),
		EncodeAsBx(OP_FORLOOP, 8, -31),
		EncodeAsBx(OP_FORLOOP, 4, -38),
		EncodeABC(OP_RETURN, 0, 1, 0),
	})
}
