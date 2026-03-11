package runtime

import (
	"math"
	"testing"
)

// ==================================================================
// Exhaustive arithmetic tests
// ==================================================================

// --- Integer arithmetic ---

func TestArithIntNegatives(t *testing.T) {
	v := getGlobal(t, `result := -3 + -5`, "result")
	if !v.IsInt() || v.Int() != -8 {
		t.Errorf("expected int -8, got %v", v)
	}
}

func TestArithIntSubNeg(t *testing.T) {
	v := getGlobal(t, `result := 5 - 10`, "result")
	if !v.IsInt() || v.Int() != -5 {
		t.Errorf("expected int -5, got %v", v)
	}
}

func TestArithIntMulNeg(t *testing.T) {
	v := getGlobal(t, `result := -3 * 7`, "result")
	if !v.IsInt() || v.Int() != -21 {
		t.Errorf("expected int -21, got %v", v)
	}
}

func TestArithIntLargeNumbers(t *testing.T) {
	v := getGlobal(t, `result := 1000000 * 1000000`, "result")
	if !v.IsInt() || v.Int() != 1000000000000 {
		t.Errorf("expected int 1000000000000, got %v", v)
	}
}

// --- Float arithmetic ---

func TestArithFloatPrecision(t *testing.T) {
	v := getGlobal(t, `result := 1.0 / 3.0`, "result")
	if !v.IsFloat() {
		t.Errorf("expected float, got %v", v)
	}
	expected := 1.0 / 3.0
	if math.Abs(v.Float()-expected) > 1e-15 {
		t.Errorf("expected %v, got %v", expected, v.Float())
	}
}

func TestArithFloatSmallNumbers(t *testing.T) {
	v := getGlobal(t, `result := 0.001 * 0.001`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-0.000001) > 1e-15 {
		t.Errorf("expected 0.000001, got %v", v)
	}
}

func TestArithFloatLargeNumbers(t *testing.T) {
	v := getGlobal(t, `result := 1e100 + 1e100`, "result")
	if !v.IsFloat() || v.Float() != 2e100 {
		t.Errorf("expected 2e100, got %v", v)
	}
}

// --- Mixed int/float ---

func TestArithMixedIntFloatAdd(t *testing.T) {
	v := getGlobal(t, `result := 1 + 1.0`, "result")
	if !v.IsFloat() || v.Float() != 2.0 {
		t.Errorf("expected float 2.0, got %v", v)
	}
}

func TestArithMixedIntFloatMul(t *testing.T) {
	v := getGlobal(t, `result := 3 * 2.5`, "result")
	if !v.IsFloat() || v.Float() != 7.5 {
		t.Errorf("expected float 7.5, got %v", v)
	}
}

func TestArithIntDivExact(t *testing.T) {
	v := getGlobal(t, `result := 6 / 2`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected int 3, got %v", v)
	}
}

func TestArithIntDivNotExact(t *testing.T) {
	v := getGlobal(t, `result := 7 / 2`, "result")
	if !v.IsFloat() || v.Float() != 3.5 {
		t.Errorf("expected float 3.5, got %v", v)
	}
}

// --- Power operator ---

func TestPow2To0(t *testing.T) {
	v := getGlobal(t, `result := 2 ** 0`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected int 1, got %v", v)
	}
}

func TestPow2ToNeg1(t *testing.T) {
	v := getGlobal(t, `result := 2 ** -1`, "result")
	if !v.IsFloat() || v.Float() != 0.5 {
		t.Errorf("expected float 0.5, got %v", v)
	}
}

func TestPow0To0(t *testing.T) {
	v := getGlobal(t, `result := 0 ** 0`, "result")
	// 0^0 = 1 by convention
	if v.Int() != 1 && v.Number() != 1.0 {
		t.Errorf("expected 1, got %v", v)
	}
}

func TestPow2To10(t *testing.T) {
	v := getGlobal(t, `result := 2 ** 10`, "result")
	if !v.IsInt() || v.Int() != 1024 {
		t.Errorf("expected int 1024, got %v", v)
	}
}

// --- Modulo ---

func TestModPositive(t *testing.T) {
	v := getGlobal(t, `result := 10 % 3`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected int 1, got %v", v)
	}
}

func TestModNegativeDividend(t *testing.T) {
	v := getGlobal(t, `result := -7 % 3`, "result")
	// Go semantics: -7 % 3 = -1
	if !v.IsInt() || v.Int() != -1 {
		t.Errorf("expected int -1, got %v", v)
	}
}

func TestModNegativeDivisor(t *testing.T) {
	v := getGlobal(t, `result := 7 % -3`, "result")
	// Go semantics: 7 % -3 = 1
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected int 1, got %v", v)
	}
}

// --- String-to-number coercion in arithmetic ---

func TestStringCoercionAdd(t *testing.T) {
	v := getGlobal(t, `result := "10" + 5`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

func TestStringCoercionSub(t *testing.T) {
	v := getGlobal(t, `result := "20" - 8`, "result")
	if !v.IsInt() || v.Int() != 12 {
		t.Errorf("expected int 12, got %v", v)
	}
}

func TestStringCoercionBothStrings(t *testing.T) {
	v := getGlobal(t, `result := "3" * "4"`, "result")
	if !v.IsInt() || v.Int() != 12 {
		t.Errorf("expected int 12, got %v", v)
	}
}

func TestStringCoercionFloat(t *testing.T) {
	v := getGlobal(t, `result := "3.14" + 0`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-3.14) > 1e-15 {
		t.Errorf("expected float 3.14, got %v", v)
	}
}

func TestInvalidStringArithError(t *testing.T) {
	err := runProgramExpectError(t, `result := "hello" + 1`)
	if err == nil {
		t.Fatal("expected error for non-numeric string arithmetic")
	}
}

// --- Concat operator ---

func TestConcatStrings(t *testing.T) {
	v := getGlobal(t, `result := "hello" .. " " .. "world"`, "result")
	if v.Str() != "hello world" {
		t.Errorf("expected 'hello world', got %v", v)
	}
}

func TestConcatIntToString(t *testing.T) {
	v := getGlobal(t, `result := 1 .. 2`, "result")
	if !v.IsString() || v.Str() != "12" {
		t.Errorf("expected '12', got %v", v)
	}
}

func TestConcatFloatToString(t *testing.T) {
	v := getGlobal(t, `result := 1.5 .. "x"`, "result")
	if !v.IsString() || v.Str() != "1.5x" {
		t.Errorf("expected '1.5x', got %v", v)
	}
}

func TestConcatNumberAndString(t *testing.T) {
	v := getGlobal(t, `result := "count: " .. 42`, "result")
	if v.Str() != "count: 42" {
		t.Errorf("expected 'count: 42', got %v", v)
	}
}

// --- Comparison ---

func TestCompareIntLess(t *testing.T) {
	v := getGlobal(t, `result := 1 < 2`, "result")
	if !v.Bool() {
		t.Errorf("1 < 2 should be true")
	}
}

func TestCompareIntGreater(t *testing.T) {
	v := getGlobal(t, `result := 5 > 3`, "result")
	if !v.Bool() {
		t.Errorf("5 > 3 should be true")
	}
}

func TestCompareIntEqual(t *testing.T) {
	v := getGlobal(t, `result := 3 <= 3`, "result")
	if !v.Bool() {
		t.Errorf("3 <= 3 should be true")
	}
}

func TestCompareStringLess(t *testing.T) {
	v := getGlobal(t, `result := "abc" < "abd"`, "result")
	if !v.Bool() {
		t.Errorf(`"abc" < "abd" should be true`)
	}
}

func TestCompareStringGreater(t *testing.T) {
	v := getGlobal(t, `result := "z" > "a"`, "result")
	if !v.Bool() {
		t.Errorf(`"z" > "a" should be true`)
	}
}

func TestCompareMixedIntFloat(t *testing.T) {
	v := getGlobal(t, `result := 1 < 1.5`, "result")
	if !v.Bool() {
		t.Errorf("1 < 1.5 should be true")
	}
}

// --- Division by zero ---

func TestDivByZeroInt(t *testing.T) {
	err := runProgramExpectError(t, `result := 10 / 0`)
	if err == nil {
		t.Fatal("expected error for division by zero")
	}
}

func TestModByZero(t *testing.T) {
	err := runProgramExpectError(t, `result := 10 % 0`)
	if err == nil {
		t.Fatal("expected error for modulo by zero")
	}
}

// --- Compound assignment operators ---

func TestCompoundSubAssign(t *testing.T) {
	v := getGlobal(t, `
		x := 10
		x -= 3
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected int 7, got %v", v)
	}
}

func TestCompoundMulAssign(t *testing.T) {
	v := getGlobal(t, `
		x := 5
		x *= 3
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

func TestCompoundDivAssign(t *testing.T) {
	v := getGlobal(t, `
		x := 10
		x /= 2
		result := x
	`, "result")
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected int 5, got %v", v)
	}
}

// --- Operator precedence ---

func TestPrecedenceMulOverAdd(t *testing.T) {
	v := getGlobal(t, `result := 2 + 3 * 4`, "result")
	if !v.IsInt() || v.Int() != 14 {
		t.Errorf("expected 14, got %v", v)
	}
}

func TestPrecedenceParentheses(t *testing.T) {
	v := getGlobal(t, `result := (2 + 3) * 4`, "result")
	if !v.IsInt() || v.Int() != 20 {
		t.Errorf("expected 20, got %v", v)
	}
}

func TestPrecedenceUnaryMinus(t *testing.T) {
	v := getGlobal(t, `result := -2 * 3`, "result")
	if !v.IsInt() || v.Int() != -6 {
		t.Errorf("expected -6, got %v", v)
	}
}
