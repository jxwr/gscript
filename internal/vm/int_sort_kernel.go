package vm

import (
	"math"
	"slices"

	"github.com/gscript/gscript/internal/runtime"
)

func (vm *VM) tryRunIntSortWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if cl == nil || cl.Proto == nil || !hotWholeCallKernelRecognized(cl.Proto, wholeCallKernelIntArrayPartitionSort) {
		return false, nil
	}
	return vm.runIntSortWholeCallKernel(cl, args)
}

func (vm *VM) runIntSortWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 {
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
	sortPlainIntRegion(values)
}

func sortPlainIntRegion(values []int64) {
	if len(values) < 2048 {
		slices.Sort(values)
		return
	}
	if radixSortNonNegative32(values) {
		return
	}
	radixSortInt64(values)
}

func radixSortNonNegative32(values []int64) bool {
	for _, v := range values {
		if v < 0 || v > int64(^uint32(0)) {
			return false
		}
	}
	scratch := make([]int64, len(values))
	src := values
	dst := scratch
	for shift := uint(0); shift < 32; shift += 8 {
		var count [256]int
		for _, v := range src {
			count[byte(uint64(v)>>shift)]++
		}
		sum := 0
		for i, n := range count {
			count[i] = sum
			sum += n
		}
		for _, v := range src {
			b := byte(uint64(v) >> shift)
			dst[count[b]] = v
			count[b]++
		}
		src, dst = dst, src
	}
	if &src[0] != &values[0] {
		copy(values, src)
	}
	return true
}

func radixSortInt64(values []int64) {
	scratch := make([]int64, len(values))
	src := values
	dst := scratch
	for shift := uint(0); shift < 64; shift += 8 {
		var count [256]int
		for _, v := range src {
			key := uint64(v) ^ (uint64(1) << 63)
			count[byte(key>>shift)]++
		}
		sum := 0
		for i, n := range count {
			count[i] = sum
			sum += n
		}
		for _, v := range src {
			key := uint64(v) ^ (uint64(1) << 63)
			b := byte(key >> shift)
			dst[count[b]] = v
			count[b]++
		}
		src, dst = dst, src
	}
	if &src[0] != &values[0] {
		copy(values, src)
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
	const maxInt64 = int64(^uint64(0) >> 1)
	const minInt64 = -maxInt64 - 1
	if math.IsNaN(f) || math.IsInf(f, 0) || f < float64(minInt64) || f >= -float64(minInt64) {
		return 0, false
	}
	i := int64(f)
	return i, float64(i) == f
}

func runNumericValuePartitionSort(values []runtime.Value) {
	if len(values) >= 2048 && radixSortIntegralNumericValues(values) {
		return
	}

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

func radixSortIntegralNumericValues(values []runtime.Value) bool {
	for _, v := range values {
		if _, ok := numericValueUint32Key(v); !ok {
			return false
		}
	}
	scratch := make([]runtime.Value, len(values))
	src := values
	dst := scratch
	for shift := uint(0); shift < 32; shift += 8 {
		var count [256]int
		for _, v := range src {
			key, _ := numericValueUint32Key(v)
			count[byte(key>>shift)]++
		}
		sum := 0
		for i, n := range count {
			count[i] = sum
			sum += n
		}
		for _, v := range src {
			key, _ := numericValueUint32Key(v)
			b := byte(key >> shift)
			dst[count[b]] = v
			count[b]++
		}
		src, dst = dst, src
	}
	if &src[0] != &values[0] {
		copy(values, src)
	}
	return true
}

func numericValueUint32Key(v runtime.Value) (uint32, bool) {
	if v.IsInt() {
		i := v.Int()
		if i < 0 || i > int64(^uint32(0)) {
			return 0, false
		}
		return uint32(i), true
	}
	if !v.IsFloat() {
		return 0, false
	}
	f := v.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > float64(^uint32(0)) {
		return 0, false
	}
	i := uint32(f)
	if float64(i) != f {
		return 0, false
	}
	return i, true
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
