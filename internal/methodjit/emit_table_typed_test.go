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

// TestKindSpecialize_IntArray tests kind-specialized GetTable on ArrayInt.
func TestKindSpecialize_IntArray(t *testing.T) {
	src := `
func sum_ints(n) {
    arr := {10, 20, 30, 40, 50}
    total := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 5; j++ {
            total = total + arr[j]
        }
    }
    return total
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = sum_ints(10)
}
`
	compareTier2Result(t, src, "result")
}

// TestKindSpecialize_Sieve tests kind-specialized GetTable/SetTable on ArrayBool.
func TestKindSpecialize_Sieve(t *testing.T) {
	src := `
func sieve(n) {
    is_prime := {}
    for i := 0; i <= n; i++ {
        is_prime[i] = true
    }
    count := 0
    for i := 2; i <= n; i++ {
        if is_prime[i] {
            count = count + 1
            for j := i + i; j <= n; j = j + i {
                is_prime[j] = false
            }
        }
    }
    return count
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = sieve(1000)
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

func TestTier2_SetTableArrayMixedAppend(t *testing.T) {
	src := `
func mixed_append(n) {
    arr := {"seed"}
    x := 42
    for i := 2; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr[n]
}
mixed_append(50)
result := mixed_append(50)
`
	compareTier2Result(t, src, "result")
}
