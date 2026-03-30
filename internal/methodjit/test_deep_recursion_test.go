//go:build darwin && arm64

// test_deep_recursion_test.go verifies that deep recursion works correctly
// in the Tier 1 baseline JIT. The native BLR call path limits nesting depth
// via NativeCallDepth to prevent native stack overflow, falling to the
// exit-resume slow path (which goes through Go and triggers stack growth).

package methodjit

import (
	"fmt"
	"testing"
)

// TestDeepRecursionSimple verifies that a simple linear recursion at depth 900
// works correctly via the baseline JIT. This exercises the NativeCallDepth
// limit: the BLR path handles the first maxNativeCallDepth levels, then falls
// to the slow path for the rest.
func TestDeepRecursionSimple(t *testing.T) {
	compareVMvsJIT(t, `
func countdown(n) {
    if n <= 0 { return 0 }
    return countdown(n - 1)
}
result := countdown(900)
`, "result")
}

// TestQuicksortSmall verifies quicksort on 1000 elements with JIT.
func TestQuicksortSmall(t *testing.T) {
	compareVMvsJIT(t, `
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

func make_random_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr
}

func is_sorted(arr, n) {
    for i := 1; i < n; i++ {
        if arr[i] > arr[i + 1] { return false }
    }
    return true
}

N := 1000
arr := make_random_array(N, 42)
quicksort(arr, 1, N)
result := is_sorted(arr, N)
`, "result")
}

// TestDeepRecursionRegression is the main regression test for the
// NativeCallDepth fix. It tests deep recursion with various patterns.
func TestDeepRecursionRegression(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "linear_recursion_500",
			src: `
func f(n) {
    if n <= 0 { return 0 }
    return f(n - 1)
}
result := f(500)
`,
		},
		{
			name: "quicksort_5000",
			src: fmt.Sprintf(`
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

func make_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) %% 2147483648
        arr[i] = x
    }
    return arr
}

arr := make_array(5000, 42)
quicksort(arr, 1, 5000)
result := arr[1] <= arr[5000]
`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compareVMvsJIT(t, tt.src, "result")
		})
	}
}
