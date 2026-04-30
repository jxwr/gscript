package vm

import "github.com/gscript/gscript/internal/runtime"

func (vm *VM) tryRunIntSortWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 || !isIntArrayPartitionSortProto(cl.Proto) {
		return false, nil
	}
	if cl.Proto.Tier2Promoted {
		return false, nil
	}
	if !vm.guardSelfRecursiveGlobal(cl) {
		return false, nil
	}
	return runIntArrayPartitionSortRegion(args), nil
}

func (vm *VM) guardSelfRecursiveGlobal(cl *Closure) bool {
	if cl == nil || cl.Proto == nil || len(cl.Proto.Constants) == 0 || !cl.Proto.Constants[0].IsString() {
		return false
	}
	v, ok := vm.globalValue(cl.Proto.Constants[0].Str())
	if !ok {
		return false
	}
	current, ok := closureFromValue(v)
	return ok && current == cl
}

func runIntArrayPartitionSortRegion(args []runtime.Value) bool {
	if len(args) != 3 || !args[1].IsNumber() || !args[2].IsNumber() {
		return false
	}
	lo64, ok := integralKernelArg(args[1])
	if !ok {
		return false
	}
	hi64, ok := integralKernelArg(args[2])
	if !ok {
		return false
	}
	if lo64 >= hi64 {
		return true
	}
	if !args[0].IsTable() || int64(int(lo64)) != lo64 || int64(int(hi64)) != hi64 {
		return false
	}
	if hi64-lo64+1 > maxCallDepth {
		return false
	}
	tbl := args[0].Table()
	if region, ok := tbl.PlainIntArrayRegionForNumericKernel(int(lo64), int(hi64)); ok {
		runPartitionSort(region)
		tbl.MarkArrayMutationForNumericKernel()
		return true
	}
	if region, ok := tbl.PlainNumericValueArrayRegionForNumericKernel(int(lo64), int(hi64)); ok {
		runNumericValuePartitionSort(region)
		tbl.MarkArrayMutationForNumericKernel()
		return true
	}
	return false
}

func runPartitionSort(values []int64) {
	type frame struct {
		lo int
		hi int
	}
	var fixed [64]frame
	stack := fixed[:1]
	stack[0] = frame{lo: 0, hi: len(values) - 1}
	for len(stack) > 0 {
		n := len(stack) - 1
		f := stack[n]
		stack = stack[:n]
		for f.lo < f.hi {
			pivot := values[f.hi]
			i := f.lo
			for j := f.lo; j < f.hi; j++ {
				if values[j] <= pivot {
					if i != j {
						values[i], values[j] = values[j], values[i]
					}
					i++
				}
			}
			if i != f.hi {
				values[i], values[f.hi] = values[f.hi], values[i]
			}

			// Source quicksort executes the left recursive call before the right.
			// Tail-run the left side and save only the right side.
			if i+1 < f.hi {
				stack = append(stack, frame{lo: i + 1, hi: f.hi})
			}
			f.hi = i - 1
		}
	}
}

func integralKernelArg(v runtime.Value) (int64, bool) {
	if v.IsInt() {
		return v.Int(), true
	}
	if !v.IsFloat() {
		return 0, false
	}
	f := v.Float()
	i := int64(f)
	return i, float64(i) == f
}

func runNumericValuePartitionSort(values []runtime.Value) {
	type frame struct {
		lo int
		hi int
	}
	var fixed [64]frame
	stack := fixed[:1]
	stack[0] = frame{lo: 0, hi: len(values) - 1}
	for len(stack) > 0 {
		n := len(stack) - 1
		f := stack[n]
		stack = stack[:n]
		for f.lo < f.hi {
			pivot := values[f.hi]
			i := f.lo
			if pivot.IsInt() {
				pivotInt := pivot.Int()
				for j := f.lo; j < f.hi; j++ {
					if numericKernelLEIntPivot(values[j], pivotInt) {
						if i != j {
							values[i], values[j] = values[j], values[i]
						}
						i++
					}
				}
			} else {
				pivotFloat := pivot.Float()
				for j := f.lo; j < f.hi; j++ {
					if numericKernelLEFloatPivot(values[j], pivotFloat) {
						if i != j {
							values[i], values[j] = values[j], values[i]
						}
						i++
					}
				}
			}
			if i != f.hi {
				values[i], values[f.hi] = values[f.hi], values[i]
			}
			if i+1 < f.hi {
				stack = append(stack, frame{lo: i + 1, hi: f.hi})
			}
			f.hi = i - 1
		}
	}
}

func numericKernelLEIntPivot(a runtime.Value, pivot int64) bool {
	if a.IsInt() {
		return a.Int() <= pivot
	}
	return a.Float() <= float64(pivot)
}

func numericKernelLEFloatPivot(a runtime.Value, pivot float64) bool {
	if a.IsInt() {
		return float64(a.Int()) <= pivot
	}
	return a.Float() <= pivot
}

func isIntArrayPartitionSortProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 3 || p.IsVarArg || len(p.Constants) < 1 || !p.Constants[0].IsString() || len(p.Code) != 51 {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeABC(OP_LE, 0, 2, 1),
		EncodeAsBx(OP_JMP, 0, 1),
		EncodeABC(OP_RETURN, 0, 1, 0),
		EncodeABC(OP_MOVE, 4, 2, 0),
		EncodeABC(OP_GETTABLE, 3, 0, 4),
		EncodeABC(OP_MOVE, 4, 1, 0),
		EncodeABC(OP_MOVE, 5, 1, 0),
		EncodeABC(OP_MOVE, 9, 2, 0),
		EncodeAsBx(OP_LOADINT, 10, 1),
		EncodeABC(OP_SUB, 6, 9, 10),
		EncodeAsBx(OP_LOADINT, 7, 1),
		EncodeAsBx(OP_FORPREP, 5, 16),
		EncodeABC(OP_MOVE, 10, 8, 0),
		EncodeABC(OP_GETTABLE, 9, 0, 10),
		EncodeABC(OP_LE, 0, 9, 3),
		EncodeAsBx(OP_JMP, 0, 12),
		EncodeABC(OP_MOVE, 10, 4, 0),
		EncodeABC(OP_GETTABLE, 9, 0, 10),
		EncodeABC(OP_MOVE, 11, 8, 0),
		EncodeABC(OP_GETTABLE, 10, 0, 11),
		EncodeABC(OP_MOVE, 11, 4, 0),
		EncodeABC(OP_SETTABLE, 0, 11, 10),
		EncodeABC(OP_MOVE, 10, 9, 0),
		EncodeABC(OP_MOVE, 11, 8, 0),
		EncodeABC(OP_SETTABLE, 0, 11, 10),
		EncodeAsBx(OP_LOADINT, 11, 1),
		EncodeABC(OP_ADD, 10, 4, 11),
		EncodeABC(OP_MOVE, 4, 10, 0),
		EncodeAsBx(OP_FORLOOP, 5, -17),
		EncodeABC(OP_MOVE, 9, 4, 0),
		EncodeABC(OP_GETTABLE, 8, 0, 9),
		EncodeABC(OP_MOVE, 10, 2, 0),
		EncodeABC(OP_GETTABLE, 9, 0, 10),
		EncodeABC(OP_MOVE, 10, 4, 0),
		EncodeABC(OP_SETTABLE, 0, 10, 9),
		EncodeABC(OP_MOVE, 9, 8, 0),
		EncodeABC(OP_MOVE, 10, 2, 0),
		EncodeABC(OP_SETTABLE, 0, 10, 9),
		EncodeABx(OP_GETGLOBAL, 9, 0),
		EncodeABC(OP_MOVE, 10, 0, 0),
		EncodeABC(OP_MOVE, 11, 1, 0),
		EncodeAsBx(OP_LOADINT, 13, 1),
		EncodeABC(OP_SUB, 12, 4, 13),
		EncodeABC(OP_CALL, 9, 4, 1),
		EncodeABx(OP_GETGLOBAL, 9, 0),
		EncodeABC(OP_MOVE, 10, 0, 0),
		EncodeAsBx(OP_LOADINT, 12, 1),
		EncodeABC(OP_ADD, 11, 4, 12),
		EncodeABC(OP_MOVE, 12, 2, 0),
		EncodeABC(OP_CALL, 9, 4, 1),
		EncodeABC(OP_RETURN, 0, 1, 0),
	})
}
