//go:build darwin && arm64

// emit_table_typed_test.go tests Tier 2 correctness for typed array
// (bool, float) GetTable/SetTable operations. These establish a correctness
// baseline — the current JIT handles these via exit-resume fallback.
// Native ARM64 paths added later must preserve these results.

package methodjit

import "testing"

func TestTier2_GetTableArrayBool(t *testing.T) {
	src := `
func count_true(n) {
    arr := {true, false, true, true, false}
    count := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 5; j++ {
            if arr[j] {
                count = count + 1
            }
        }
    }
    return count
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = count_true(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_SetTableArrayBool(t *testing.T) {
	src := `
func toggle_bools(n) {
    arr := {true, false, true}
    for i := 1; i <= n; i++ {
        for j := 1; j <= 3; j++ {
            if arr[j] {
                arr[j] = false
            } else {
                arr[j] = true
            }
        }
    }
    count := 0
    for j := 1; j <= 3; j++ {
        if arr[j] { count = count + 1 }
    }
    return count
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = toggle_bools(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_GetTableArrayFloat(t *testing.T) {
	src := `
func sum_floats(n) {
    arr := {1.5, 2.5, 3.5, 4.5}
    total := 0.0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 4; j++ {
            total = total + arr[j]
        }
    }
    return total
}
result := 0.0
for iter := 1; iter <= 5; iter++ {
    result = sum_floats(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_SetTableArrayFloat(t *testing.T) {
	src := `
func scale_floats(n) {
    arr := {1.0, 2.0, 3.0}
    for i := 1; i <= n; i++ {
        for j := 1; j <= 3; j++ {
            arr[j] = arr[j] * 1.1
        }
    }
    sum := 0.0
    for j := 1; j <= 3; j++ {
        sum = sum + arr[j]
    }
    return sum
}
result := 0.0
for iter := 1; iter <= 5; iter++ {
    result = scale_floats(10)
}
`
	compareTier2Result(t, src, "result")
}
