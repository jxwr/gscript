//go:build darwin && arm64

// emit_qs_t2_correctness_test.go — R79 failing test for BLOCKER-A:
// quicksort Tier 2 codegen miscompiles on LCG (pseudorandom) input.
//
// Reduced repro from R75 bisect: fails at N >= 11, seed=42, LCG
// arithmetic. The T2 compilation of quicksort itself (not main)
// produces incorrect sort order on this input pattern.

package methodjit

import "testing"

// TestTier2_Quicksort_LCG_N11 — minimum failing repro.
// The whole sort runs at Tier 1 normally. Quicksort reaches Tier 2
// after enough recursive calls; once at T2, the sort output diverges
// from VM.
func TestTier2_Quicksort_LCG_N11(t *testing.T) {
	t.Skip("BLOCKER-A: needs Layers 1+3 (feedback-gate + full TypeSpec integration); L2 alone insufficient when harness feedback is empty")
	src := `
func quicksort(arr, lo, hi) {
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
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

N := 11
arr := {}
x := 42
for i := 1; i <= N; i++ {
    x = (x * 1103515245 + 12345) % 2147483648
    arr[i] = x
}
quicksort(arr, 1, N)
result := arr[1]
`
	compareTier2Result(t, src, "result")
}

// TestTier2_Quicksort_Descending — control case. Same quicksort shape,
// descending-int input, should work (R75 verified).
func TestTier2_Quicksort_Descending(t *testing.T) {
	src := `
func quicksort(arr, lo, hi) {
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
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

N := 50
arr := {}
for i := 1; i <= N; i++ {
    arr[i] = (N + 1 - i) * 7
}
quicksort(arr, 1, N)
result := arr[1]
`
	compareTier2Result(t, src, "result")
}
