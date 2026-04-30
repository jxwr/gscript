package vm

import "testing"

func TestIntSortKernelRecognizesStructuralPartitionSort(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
func partition_sort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t

    partition_sort(arr, lo, i - 1)
    partition_sort(arr, i + 1, hi)
}
`)
	defer vm.Close()
	if !isIntArrayPartitionSortProto(proto.Protos[0]) {
		t.Fatalf("structural partition-sort proto not recognized:\n%s", Disassemble(proto.Protos[0]))
	}
}

func TestIntSortKernelSortsPlainIntArray(t *testing.T) {
	globals := compileAndRun(t, `
func q(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    q(arr, lo, i - 1)
    q(arr, i + 1, hi)
}
arr := {}
arr[1] = 9
arr[2] = 3
arr[3] = 7
arr[4] = 1
arr[5] = 5
q(arr, 1, 5)
result := arr[1] * 10000 + arr[2] * 1000 + arr[3] * 100 + arr[4] * 10 + arr[5]
`)
	expectGlobalInt(t, globals, "result", 13579)
}

func TestIntSortKernelFallsBackWhenRecursiveGlobalRebound(t *testing.T) {
	globals := compileAndRun(t, `
func q(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    q(arr, lo, i - 1)
    q(arr, i + 1, hi)
}
orig := q
calls := 0
q = func(arr, lo, hi) {
    calls = calls + 1
}
arr := {}
arr[1] = 2
arr[2] = 1
orig(arr, 1, 2)
result := arr[1] * 10 + arr[2]
`)
	expectGlobalInt(t, globals, "calls", 2)
	expectGlobalInt(t, globals, "result", 12)
}

func TestIntSortKernelSortsMixedNumericArrayPreservingBoxes(t *testing.T) {
	globals := compileAndRun(t, `
func q(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    q(arr, lo, i - 1)
    q(arr, i + 1, hi)
}
arr := {}
arr[1] = 2
arr[2] = 1.5
arr[3] = 1
q(arr, 1, 3)
result := arr[1] + arr[2] + arr[3]
middle := arr[2]
t1 := math.type(arr[1])
t2 := math.type(arr[2])
t3 := math.type(arr[3])
`)
	expectGlobalFloat(t, globals, "result", 4.5)
	expectGlobalFloat(t, globals, "middle", 1.5)
	expectGlobalString(t, globals, "t1", "integer")
	expectGlobalString(t, globals, "t2", "float")
	expectGlobalString(t, globals, "t3", "integer")
}

func TestIntSortKernelKeepsMixedDuplicateBoxPermutation(t *testing.T) {
	globals := compileAndRun(t, `
func q(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    q(arr, lo, i - 1)
    q(arr, i + 1, hi)
}
arr := {}
arr[1] = 1.0
arr[2] = 0
arr[3] = 1
q(arr, 1, 3)
t1 := math.type(arr[1])
t2 := math.type(arr[2])
t3 := math.type(arr[3])
`)
	expectGlobalString(t, globals, "t1", "integer")
	expectGlobalString(t, globals, "t2", "float")
	expectGlobalString(t, globals, "t3", "integer")
}

func TestIntSortKernelKeepsBaseCaseSemantics(t *testing.T) {
	globals := compileAndRun(t, `
func q(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    q(arr, lo, i - 1)
    q(arr, i + 1, hi)
}
q(nil, 3, 2)
result := 1
`)
	expectGlobalInt(t, globals, "result", 1)
}

func TestIntSortKernelFallsBackForNonnumericMixedArray(t *testing.T) {
	err := compileAndRunExpectError(t, `
func q(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    q(arr, lo, i - 1)
    q(arr, i + 1, hi)
}
arr := {}
arr[1] = 2
arr[2] = "x"
arr[3] = 1
q(arr, 1, 3)
`)
	if err == nil {
		t.Fatal("expected fallback VM comparison error")
	}
}
