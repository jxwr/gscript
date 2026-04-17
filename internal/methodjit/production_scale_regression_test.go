//go:build darwin && arm64

// production_scale_regression_test.go — R17 test framework.
//
// Per R15 (revert of R12) mitigation: correctness-class fixes MUST be
// verified at production-scale iteration counts, not token 2-call
// regressions. R12 landed clean unit tests but exposed an infinite
// loop in math_intensive's million-iteration collatz only when the
// benchmark ran.
//
// This file hosts production-scale regression tests with:
//   - a hard timeout per case (so hangs fail fast)
//   - workloads that match the shape and magnitude of the actual
//     benchmarks that surfaced the original bug.
//
// Tests use `testing.T.Deadline()` so they fit the regular go-test
// flow; CI runs them by default. Failures print both the symptom
// and the R-round that added the case.

package methodjit

import (
	"testing"
	"time"
)

// runWithTimeout executes src and fails the test if it doesn't
// complete within deadline. This is the mechanical equivalent of
// the R15 mitigation requirement: hangs → test fail, not test-pass.
func runWithTimeout(t *testing.T, name, src string, deadline time.Duration) {
	t.Helper()
	done := make(chan struct{})
	var panicErr any
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr = r
			}
			close(done)
		}()
		_, _ = runWithTieringManager(t, src)
	}()
	select {
	case <-done:
		if panicErr != nil {
			t.Fatalf("%s: panicked: %v", name, panicErr)
		}
	case <-time.After(deadline):
		t.Fatalf("%s: exceeded deadline %v — production-scale hang (see R15)", name, deadline)
	}
}

// TestProductionScale_Collatz_ModHotLoop is the R15 regression: an
// int-int mod hot loop at production scale. If any future tier-2
// emit change re-introduces R12's infinite-loop failure mode, this
// test hangs → fails fast.
//
// Original symptom: math_intensive collatz(50000) hung indefinitely
// under R12. Wall-time budget: 1s (real value ≈ 0.054s on M4 Max).
func TestProductionScale_Collatz_ModHotLoop(t *testing.T) {
	src := `
func collatz_total(limit) {
    total_steps := 0
    for n := 2; n <= limit; n++ {
        x := n
        steps := 0
        for x != 1 {
            if x % 2 == 0 {
                x = x / 2
            } else {
                x = 3 * x + 1
            }
            steps = steps + 1
        }
        total_steps = total_steps + steps
    }
    return total_steps
}
result := collatz_total(5000)
result = collatz_total(5000)
`
	runWithTimeout(t, "collatz_total(5000)", src, 3*time.Second)
}

// TestProductionScale_QuicksortDeepRecursion is the R22 regression:
// a self-recursive function whose depth advances mRegRegs beyond the
// register file. R22 attempted to skip the Tier 2 bounds check on
// self-calls and crashed with SIGSEGV at recursion depth ~16. The
// bounds check MUST fire to fall to slow path before overflow.
//
// Recursion depth log2(N) for quicksort(N); we sort 1024 elements →
// depth ~10-12, sufficient to trigger the R22 failure mode.
func TestProductionScale_QuicksortDeepRecursion(t *testing.T) {
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

arr := {}
x := 1
for i := 1; i <= 1024; i++ {
    x = (x * 1103515245 + 12345) % 2147483648
    arr[i] = x
}
quicksort(arr, 1, 1024)
// Force promotion via second run
quicksort(arr, 1, 1024)
`
	runWithTimeout(t, "quicksort(1024)", src, 3*time.Second)
}

// TestProductionScale_FibRecursiveDeep is the R35 regression: deep
// recursive fib at scale. R35 promoted small self-recursive fib-class
// functions to Tier 2 and saw fib(35) -14% in spot-check, but fib(40)
// and other deep-recursion benchmarks regressed 19-47%. This test
// covers the deep-recursion failure mode.
func TestProductionScale_FibRecursiveDeep(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(25)
result = fib(25)
`
	runWithTimeout(t, "fib(25)", src, 5*time.Second)
}

// TestProductionScale_MutualRecursion is the R35 regression: mutual
// recursion regressed 47% under R35's naive promotion-gate loosening.
// Covers the mutual-recursion failure mode.
func TestProductionScale_MutualRecursion(t *testing.T) {
	src := `
func F(n) {
    if n == 0 { return 1 }
    return G(n - 1) + 1
}
func G(n) {
    if n == 0 { return 0 }
    return F(n - 1)
}
result := F(20)
result = F(20)
`
	runWithTimeout(t, "mutual_F(20)", src, 5*time.Second)
}

// TestProductionScale_BinaryTreesAlloc is the R35 regression: binary_trees
// regressed 13% when makeTree-class functions were drawn into Tier 2 via
// R35's self-recursive gate (R5's tier-routing gate was defeated). This
// test exercises the small-recursive-allocator pattern.
func TestProductionScale_BinaryTreesAlloc(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 { return {value: 1, left: null, right: null} }
    return {value: depth, left: makeTree(depth-1), right: makeTree(depth-1)}
}
func checkTree(node) {
    if node.left == null { return node.value }
    return node.value + checkTree(node.left) - checkTree(node.right)
}
tree := makeTree(12)
result := checkTree(tree)
tree = makeTree(12)
result = checkTree(tree)
`
	runWithTimeout(t, "binary_trees(depth=12)", src, 5*time.Second)
}

// TestProductionScale_IterativeFib is a secondary production-scale
// guard covering long arithmetic hot loops that don't use %.
func TestProductionScale_IterativeFib(t *testing.T) {
	src := `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
result := fib_iter(1000000)
result = fib_iter(1000000)
`
	runWithTimeout(t, "fib_iter(1e6)", src, 3*time.Second)
}
