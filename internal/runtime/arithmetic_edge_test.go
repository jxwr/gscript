package runtime

import (
	"math"
	"testing"
)

// ==================================================================
// Arithmetic edge cases (beyond what arithmetic_test.go covers)
// ==================================================================

// --- Chained operations ---

func TestArithChainedAddSub(t *testing.T) {
	v := getGlobal(t, `result := 10 + 5 - 3 + 2 - 1`, "result")
	if !v.IsInt() || v.Int() != 13 {
		t.Errorf("expected 13, got %v", v)
	}
}

func TestArithChainedMulDiv(t *testing.T) {
	v := getGlobal(t, `result := 100 / 10 * 3 / 5`, "result")
	// 100/10=10, 10*3=30, 30/5=6
	if !v.IsInt() || v.Int() != 6 {
		t.Errorf("expected 6, got %v", v)
	}
}

func TestArithComplexPrecedence(t *testing.T) {
	v := getGlobal(t, `result := 2 + 3 * 4 - 6 / 2`, "result")
	// 2 + 12 - 3.0 = 11.0 (6/2=3 int, but let's check)
	// 6/2 = 3 (exact), so all int: 2 + 12 - 3 = 11
	if v.Int() != 11 && v.Number() != 11.0 {
		t.Errorf("expected 11, got %v", v)
	}
}

func TestArithNestedParentheses(t *testing.T) {
	v := getGlobal(t, `result := ((2 + 3) * (4 - 1)) ** 2`, "result")
	// (5 * 3)^2 = 15^2 = 225
	if !v.IsInt() || v.Int() != 225 {
		t.Errorf("expected 225, got %v", v)
	}
}

// --- Unary minus edge cases ---

func TestArithUnaryMinusFloat(t *testing.T) {
	v := getGlobal(t, `result := -3.14`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-(-3.14)) > 1e-15 {
		t.Errorf("expected -3.14, got %v", v)
	}
}

func TestArithDoubleNegation(t *testing.T) {
	v := getGlobal(t, `
		x := -5
		result := -x
	`, "result")
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

func TestArithUnaryMinusInExpr(t *testing.T) {
	v := getGlobal(t, `result := 10 + -3`, "result")
	if !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected 7, got %v", v)
	}
}

// --- Power operator edge cases ---

func TestPowLargeExponent(t *testing.T) {
	v := getGlobal(t, `result := 2 ** 20`, "result")
	if !v.IsInt() || v.Int() != 1048576 {
		t.Errorf("expected 1048576, got %v", v)
	}
}

func TestPowFloatBase(t *testing.T) {
	v := getGlobal(t, `result := 2.0 ** 3`, "result")
	if !v.IsFloat() || v.Float() != 8.0 {
		t.Errorf("expected 8.0, got %v", v)
	}
}

func TestPowFloatExponent(t *testing.T) {
	v := getGlobal(t, `result := 4 ** 0.5`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-2.0) > 1e-10 {
		t.Errorf("expected 2.0, got %v", v)
	}
}

// --- Modulo edge cases ---

func TestModFloat(t *testing.T) {
	v := getGlobal(t, `result := 10.5 % 3`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-1.5) > 1e-10 {
		t.Errorf("expected 1.5, got %v", v)
	}
}

func TestModLargeNumbers(t *testing.T) {
	v := getGlobal(t, `result := 1000000007 % 1000000`, "result")
	if !v.IsInt() || v.Int() != 7 {
		t.Errorf("expected 7, got %v", v)
	}
}

// --- Division edge cases ---

func TestDivFloatByFloat(t *testing.T) {
	v := getGlobal(t, `result := 1.0 / 0.5`, "result")
	if !v.IsFloat() || v.Float() != 2.0 {
		t.Errorf("expected 2.0, got %v", v)
	}
}

func TestDivIntByFloat(t *testing.T) {
	v := getGlobal(t, `result := 10 / 3.0`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-10.0/3.0) > 1e-10 {
		t.Errorf("expected ~3.333, got %v", v)
	}
}

func TestDivFloatByZeroError(t *testing.T) {
	err := runProgramExpectError(t, `result := 1.0 / 0.0`)
	if err == nil {
		t.Fatal("expected error for float division by zero")
	}
}

// --- String-to-number coercion additional tests ---

func TestStringCoercionDiv(t *testing.T) {
	v := getGlobal(t, `result := "10" / "2"`, "result")
	if !v.IsInt() || v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

func TestStringCoercionMod(t *testing.T) {
	v := getGlobal(t, `result := "10" % "3"`, "result")
	if !v.IsInt() || v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

func TestStringCoercionPow(t *testing.T) {
	v := getGlobal(t, `result := "2" ** "10"`, "result")
	if !v.IsInt() || v.Int() != 1024 {
		t.Errorf("expected 1024, got %v", v)
	}
}

func TestStringCoercionFloatMixed(t *testing.T) {
	v := getGlobal(t, `result := "1.5" + 2`, "result")
	if !v.IsFloat() || math.Abs(v.Float()-3.5) > 1e-15 {
		t.Errorf("expected 3.5, got %v", v)
	}
}

func TestStringCoercionUnaryMinus(t *testing.T) {
	v := getGlobal(t, `result := -"5"`, "result")
	if !v.IsInt() || v.Int() != -5 {
		t.Errorf("expected -5, got %v", v)
	}
}

// --- Arithmetic with variables ---

func TestArithAccumulator(t *testing.T) {
	v := getGlobal(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum += i
		}
		result := sum
	`, "result")
	if !v.IsInt() || v.Int() != 5050 {
		t.Errorf("expected 5050, got %v", v)
	}
}

func TestArithProduct(t *testing.T) {
	v := getGlobal(t, `
		prod := 1
		for i := 1; i <= 6; i++ {
			prod *= i
		}
		result := prod
	`, "result")
	if !v.IsInt() || v.Int() != 720 {
		t.Errorf("expected 720 (6!), got %v", v)
	}
}

// --- Compound assignment edge cases ---

func TestCompoundAssignOnTableField(t *testing.T) {
	v := getGlobal(t, `
		t := {x: 10}
		t.x += 5
		result := t.x
	`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

func TestCompoundAssignOnTableIndex(t *testing.T) {
	v := getGlobal(t, `
		t := {10, 20, 30}
		t[2] += 5
		result := t[2]
	`, "result")
	if !v.IsInt() || v.Int() != 25 {
		t.Errorf("expected 25, got %v", v)
	}
}

func TestIncDecOnTableField(t *testing.T) {
	v := getGlobal(t, `
		t := {n: 0}
		t.n++
		t.n++
		t.n++
		result := t.n
	`, "result")
	if !v.IsInt() || v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}

// --- Not operator edge cases ---

func TestNotNotTrue(t *testing.T) {
	v := getGlobal(t, `result := !!true`, "result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

func TestNotNil(t *testing.T) {
	v := getGlobal(t, `result := !nil`, "result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

func TestNotZero(t *testing.T) {
	v := getGlobal(t, `result := !0`, "result")
	// 0 is truthy, so !0 = false
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false (0 is truthy), got %v", v)
	}
}

func TestNotEmptyString(t *testing.T) {
	v := getGlobal(t, `result := !""`, "result")
	// "" is truthy, so !"" = false
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false ('' is truthy), got %v", v)
	}
}

// --- Error cases ---

func TestArithNilPlusOne(t *testing.T) {
	err := runProgramExpectError(t, `
		x := nil
		result := x + 1
	`)
	if err == nil {
		t.Fatal("expected error for nil + 1")
	}
}

func TestArithBoolArithmetic(t *testing.T) {
	err := runProgramExpectError(t, `result := true + 1`)
	if err == nil {
		t.Fatal("expected error for bool arithmetic")
	}
}

func TestArithTableArithmeticNoMetamethod(t *testing.T) {
	err := runProgramExpectError(t, `
		t := {}
		result := t + 1
	`)
	if err == nil {
		t.Fatal("expected error for table + int without metamethod")
	}
}
