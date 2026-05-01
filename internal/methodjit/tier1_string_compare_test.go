//go:build darwin && arm64

// tier1_string_compare_test.go tests OP_LT/OP_LE with string operands.
//
// Before the fix, emitBaselineLT/LE fell through to the float fallback when
// operands were strings (NaN-boxed pointers). FCMPd on pointer bits is always
// "unordered", so the conditional branch was never taken — string sorts
// silently produced wrong results.
//
// These tests sort strings via VM and JIT, then compare final ordering.

package methodjit

import "testing"

func TestTier1_StringLT_BubbleSort(t *testing.T) {
	compareVMvsJIT(t, `
func sort_strings() {
    arr := {}
    arr[1] = "c"
    arr[2] = "a"
    arr[3] = "d"
    arr[4] = "b"
    n := 4
    for i := 1; i <= n - 1; i++ {
        for j := 1; j <= n - i; j++ {
            if arr[j] > arr[j + 1] {
                t := arr[j]
                arr[j] = arr[j + 1]
                arr[j + 1] = t
            }
        }
    }
    return arr[1] .. arr[2] .. arr[3] .. arr[4]
}
result := ""
for k := 1; k <= 200; k++ { result = sort_strings() }
`, "result")
}

func TestTier1_StringLT_Direct(t *testing.T) {
	compareVMvsJIT(t, `
func cmp_lt() {
    a := "apple"
    b := "banana"
    if a < b { return 1 }
    return 0
}
result := 0
for i := 1; i <= 200; i++ { result = cmp_lt() }
`, "result")
}

func TestTier1_StringLT_Equal(t *testing.T) {
	compareVMvsJIT(t, `
func cmp_lt_eq() {
    a := "same"
    b := "same"
    if a < b { return 1 }
    return 0
}
result := -1
for i := 1; i <= 200; i++ { result = cmp_lt_eq() }
`, "result")
}

func TestTier1_StringEQ_DistinctBacking(t *testing.T) {
	compareVMvsJIT(t, `
func cmp_eq() {
    a := "err" .. "or"
    if a == "error" { return 1 }
    return 0
}
result := -1
for i := 1; i <= 200; i++ { result = cmp_eq() }
`, "result")
}

func TestTier1_StringNE_DistinctBacking(t *testing.T) {
	compareVMvsJIT(t, `
func cmp_ne() {
    a := "err" .. "or"
    if a != "error" { return 1 }
    return 0
}
result := -1
for i := 1; i <= 200; i++ { result = cmp_ne() }
`, "result")
}

func TestTier1_StringGT(t *testing.T) {
	// a > b is compiled as OP_LT with operands swapped.
	compareVMvsJIT(t, `
func cmp_gt() {
    a := "zebra"
    b := "apple"
    if a > b { return 1 }
    return 0
}
result := 0
for i := 1; i <= 200; i++ { result = cmp_gt() }
`, "result")
}

func TestTier1_StringLE(t *testing.T) {
	compareVMvsJIT(t, `
func cmp_le() {
    a := "abc"
    b := "abc"
    if a <= b { return 1 }
    return 0
}
result := 0
for i := 1; i <= 200; i++ { result = cmp_le() }
`, "result")
}

func TestTier1_StringLE_Strict(t *testing.T) {
	compareVMvsJIT(t, `
func cmp_le_strict() {
    a := "abd"
    b := "abc"
    if a <= b { return 1 }
    return 0
}
result := -1
for i := 1; i <= 200; i++ { result = cmp_le_strict() }
`, "result")
}

func TestTier1_StringGE(t *testing.T) {
	// a >= b is compiled as OP_LE with operands swapped.
	compareVMvsJIT(t, `
func cmp_ge() {
    a := "mango"
    b := "apple"
    if a >= b { return 1 }
    return 0
}
result := 0
for i := 1; i <= 200; i++ { result = cmp_ge() }
`, "result")
}

func TestTier1_StringSort_Larger(t *testing.T) {
	// Mimic string_bench's compare test with a smaller array.
	compareVMvsJIT(t, `
func sort_keys() {
    arr := {}
    for i := 1; i <= 50; i++ {
        arr[i] = string.format("key_%05d", (i * 7) % 50)
    }
    n := #arr
    for i := 1; i <= n - 1; i++ {
        for j := 1; j <= n - i; j++ {
            if arr[j] > arr[j + 1] {
                t := arr[j]
                arr[j] = arr[j + 1]
                arr[j + 1] = t
            }
        }
    }
    return arr[1] .. "|" .. arr[n]
}
result := ""
for k := 1; k <= 20; k++ { result = sort_keys() }
`, "result")
}

// Mixed int + string comparisons must never fall into the string slow path
// when both operands are ints. This regression-protects the fast path.
func TestTier1_IntLT_StillWorks(t *testing.T) {
	compareVMvsJIT(t, `
func int_lt() {
    sum := 0
    for i := 1; i <= 100; i++ {
        if i < 50 { sum = sum + 1 }
    }
    return sum
}
result := 0
for k := 1; k <= 200; k++ { result = int_lt() }
`, "result")
}

func TestTier1_FloatLT_StillWorks(t *testing.T) {
	compareVMvsJIT(t, `
func float_lt() {
    a := 1.5
    b := 2.5
    if a < b { return 42 }
    return 0
}
result := 0
for k := 1; k <= 200; k++ { result = float_lt() }
`, "result")
}
