package runtime

import (
	"testing"
)

// ==================================================================
// Control flow edge cases
// ==================================================================

// --- if/elseif/else chains ---

func TestIfElseIfElseFullChain(t *testing.T) {
	tests := []struct {
		val    int
		expect int
	}{
		{5, 1},   // x < 10
		{15, 2},  // x < 20
		{25, 3},  // x < 30
		{35, 4},  // else
	}
	for _, tt := range tests {
		v := getGlobal(t, `
			x := `+itoa(tt.val)+`
			result := 0
			if x < 10 {
				result = 1
			} elseif x < 20 {
				result = 2
			} elseif x < 30 {
				result = 3
			} else {
				result = 4
			}
		`, "result")
		if v.Int() != int64(tt.expect) {
			t.Errorf("x=%d: expected %d, got %v", tt.val, tt.expect, v)
		}
	}
}

func TestIfMultipleElseIf(t *testing.T) {
	v := getGlobal(t, `
		x := 50
		result := "other"
		if x == 10 {
			result = "ten"
		} elseif x == 20 {
			result = "twenty"
		} elseif x == 30 {
			result = "thirty"
		} elseif x == 40 {
			result = "forty"
		} elseif x == 50 {
			result = "fifty"
		}
	`, "result")
	if v.Str() != "fifty" {
		t.Errorf("expected 'fifty', got %v", v)
	}
}

func TestIfNoMatchNoElse(t *testing.T) {
	v := getGlobal(t, `
		result := "unchanged"
		if false {
			result = "changed"
		} elseif false {
			result = "also changed"
		}
	`, "result")
	if v.Str() != "unchanged" {
		t.Errorf("expected 'unchanged', got %v", v)
	}
}

// --- Nested if statements ---

func TestIfNestedConditions(t *testing.T) {
	v := getGlobal(t, `
		x := 5
		y := 10
		result := "none"
		if x > 0 {
			if y > 5 {
				result = "both"
			} else {
				result = "x only"
			}
		}
	`, "result")
	if v.Str() != "both" {
		t.Errorf("expected 'both', got %v", v)
	}
}

// --- For numeric: step values ---

func TestForNumericStepByTwo(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 0; i < 10; i += 2 {
			sum += i
		}
		result := sum
	`, "result")
	// 0+2+4+6+8 = 20
	if !v.IsInt() || v.Int() != 20 {
		t.Errorf("expected 20, got %v", v)
	}
}

func TestForNumericStepByThree(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 0; i < 15; i += 3 {
			sum += i
		}
		result := sum
	`, "result")
	// 0+3+6+9+12 = 30
	if !v.IsInt() || v.Int() != 30 {
		t.Errorf("expected 30, got %v", v)
	}
}

func TestForNumericCountdown(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 5; i > 0; i-- {
			sum += i
		}
		result := sum
	`, "result")
	// 5+4+3+2+1 = 15
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

func TestForNumericNegativeStep(t *testing.T) {
	v := getGlobal(t, `
		count := 0
		for i := 10; i >= 0; i -= 3 {
			count++
		}
		result := count
	`, "result")
	// i: 10, 7, 4, 1 -> 4 iterations (next would be -2)
	if !v.IsInt() || v.Int() != 4 {
		t.Errorf("expected 4, got %v", v)
	}
}

func TestForNumericNoIteration(t *testing.T) {
	v := getGlobal(t, `
		count := 0
		for i := 10; i < 5; i++ {
			count++
		}
		result := count
	`, "result")
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

// --- For while-style ---

func TestForWhileCountdown(t *testing.T) {
	v := getGlobal(t, `
		n := 10
		count := 0
		for n > 0 {
			n = n - 1
			count++
		}
		result := count
	`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected 10, got %v", v)
	}
}

func TestForWhileFalseInitially(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		for false {
			result = 1
		}
	`, "result")
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

// --- For infinite loop with break ---

func TestForInfiniteLoopBreakCondition(t *testing.T) {
	v := getGlobal(t, `
		result := ""
		i := 0
		for {
			i++
			if i > 3 {
				break
			}
			result = result .. tostring(i)
		}
	`, "result")
	if v.Str() != "123" {
		t.Errorf("expected '123', got %q", v.Str())
	}
}

// --- Break in nested loops ---

func TestBreakInNestedLoop(t *testing.T) {
	v := getGlobal(t, `
		result := 0
		for i := 0; i < 5; i++ {
			for j := 0; j < 5; j++ {
				if j >= 2 {
					break
				}
				result++
			}
		}
	`, "result")
	// Each outer iteration, inner runs 2 times (j=0,1), so 5*2=10
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected 10, got %v", v)
	}
}

func TestBreakOnlyInnerLoop(t *testing.T) {
	v := getGlobal(t, `
		outer_count := 0
		for i := 0; i < 3; i++ {
			outer_count++
			for j := 0; j < 100; j++ {
				break
			}
		}
		result := outer_count
	`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}

// --- Continue in loops ---

func TestContinueSkipsRemainder(t *testing.T) {
	v := getGlobal(t, `
		result := ""
		for i := 1; i <= 5; i++ {
			if i == 3 {
				continue
			}
			result = result .. tostring(i)
		}
	`, "result")
	if v.Str() != "1245" {
		t.Errorf("expected '1245', got %q", v.Str())
	}
}

func TestContinueInNestedLoop(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 1; i <= 3; i++ {
			for j := 1; j <= 3; j++ {
				if j == 2 {
					continue
				}
				sum += j
			}
		}
		result := sum
	`, "result")
	// Each outer: j=1+j=3 = 4, three outers = 12
	if !v.IsInt() || v.Int() != 12 {
		t.Errorf("expected 12, got %v", v)
	}
}

func TestContinueWithCounter(t *testing.T) {
	v := getGlobal(t, `
		count := 0
		for i := 0; i < 10; i++ {
			if i % 3 == 0 {
				continue
			}
			count++
		}
		result := count
	`, "result")
	// Skip i=0,3,6,9 -> count 6
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected 6, got %v", v)
	}
}

// --- Return from nested function ---

func TestReturnFromNestedFunc(t *testing.T) {
	v := getGlobal(t, `
		func outer() {
			func inner() {
				return 42
			}
			return inner() + 8
		}
		result := outer()
	`, "result")
	if !v.IsInt() || v.Int() != 50 {
		t.Errorf("expected 50, got %v", v)
	}
}

func TestEarlyReturn(t *testing.T) {
	v := getGlobal(t, `
		func f(x) {
			if x < 0 {
				return -1
			}
			if x == 0 {
				return 0
			}
			return 1
		}
		result := f(-5) + f(0) + f(10)
	`, "result")
	// -1 + 0 + 1 = 0
	if !v.IsInt() || v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

func TestReturnFromLoop(t *testing.T) {
	v := getGlobal(t, `
		func findFirst(t, target) {
			for k, v := range t {
				if v == target {
					return k
				}
			}
			return nil
		}
		t := {10, 20, 30, 40, 50}
		result := findFirst(t, 30)
	`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}

// --- Truthiness edge cases ---

func TestTruthinessInLogicOps(t *testing.T) {
	tests := []struct {
		src    string
		expect string
	}{
		{`result := nil || "default"`, "default"},
		{`result := false || "default"`, "default"},
		{`result := 0 || "default"`, "0"},        // 0 is truthy
		{`result := "" || "default"`, ""},         // "" is truthy
	}
	for _, tt := range tests {
		v := getGlobal(t, tt.src, "result")
		if v.String() != tt.expect {
			t.Errorf("%s: expected %q, got %q", tt.src, tt.expect, v.String())
		}
	}
}

func TestTruthinessTableInCondition(t *testing.T) {
	v := getGlobal(t, `
		result := false
		t := {}
		if t {
			result = true
		}
	`, "result")
	if !v.Bool() {
		t.Errorf("table should be truthy")
	}
}

func TestTruthinessFunctionInCondition(t *testing.T) {
	v := getGlobal(t, `
		result := false
		f := func() {}
		if f {
			result = true
		}
	`, "result")
	if !v.Bool() {
		t.Errorf("function should be truthy")
	}
}

// --- Logic operators edge cases ---

func TestAndReturnsFalsy(t *testing.T) {
	v := getGlobal(t, `result := nil && 42`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestAndReturnsTruthy(t *testing.T) {
	v := getGlobal(t, `result := 1 && 2 && 3`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}

func TestOrReturnsFirst(t *testing.T) {
	v := getGlobal(t, `result := 1 || 2`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

func TestOrChain(t *testing.T) {
	v := getGlobal(t, `result := nil || false || 42`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestAndOrTernary(t *testing.T) {
	// Simulating: cond ? a : b using: cond && a || b
	v := getGlobal(t, `
		x := 10
		result := (x > 5) && "yes" || "no"
	`, "result")
	if v.Str() != "yes" {
		t.Errorf("expected 'yes', got %v", v)
	}
}

func TestAndOrTernaryFalse(t *testing.T) {
	v := getGlobal(t, `
		x := 3
		result := (x > 5) && "yes" || "no"
	`, "result")
	if v.Str() != "no" {
		t.Errorf("expected 'no', got %v", v)
	}
}

// --- For range over function iterator ---

func TestForRangeFunctionIterator(t *testing.T) {
	v := getGlobal(t, `
		func counter(n) {
			i := 0
			return func() {
				i = i + 1
				if i > n {
					return nil
				}
				return i
			}
		}
		sum := 0
		for v := range counter(5) {
			sum = sum + v
		}
		result := sum
	`, "result")
	// 1+2+3+4+5=15
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

// --- For range with key only ---

func TestForRangeKeyOnly(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20, 30}
		sum := 0
		for k := range t {
			sum += k
		}
		result := sum
	`, "result")
	// keys are 1,2,3 -> sum=6
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected 6, got %v", v)
	}
}

// helper to avoid importing strconv in tests
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
