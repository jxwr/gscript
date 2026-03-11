package runtime

import (
	"strings"
	"testing"
)

// ==================================================================
// Type system tests
// ==================================================================

// --- type() builtin ---

func TestTypeOfNil(t *testing.T) {
	v := getGlobal(t, `result := type(nil)`, "result")
	if v.Str() != "nil" {
		t.Errorf("expected 'nil', got %v", v)
	}
}

func TestTypeOfBool(t *testing.T) {
	v := getGlobal(t, `result := type(true)`, "result")
	if v.Str() != "boolean" {
		t.Errorf("expected 'boolean', got %v", v)
	}
}

func TestTypeOfInt(t *testing.T) {
	v := getGlobal(t, `result := type(42)`, "result")
	if v.Str() != "number" {
		t.Errorf("expected 'number', got %v", v)
	}
}

func TestTypeOfFloat(t *testing.T) {
	v := getGlobal(t, `result := type(3.14)`, "result")
	if v.Str() != "number" {
		t.Errorf("expected 'number', got %v", v)
	}
}

func TestTypeOfString(t *testing.T) {
	v := getGlobal(t, `result := type("hello")`, "result")
	if v.Str() != "string" {
		t.Errorf("expected 'string', got %v", v)
	}
}

func TestTypeOfTable(t *testing.T) {
	v := getGlobal(t, `result := type({})`, "result")
	if v.Str() != "table" {
		t.Errorf("expected 'table', got %v", v)
	}
}

func TestTypeOfFunction(t *testing.T) {
	v := getGlobal(t, `result := type(func() {})`, "result")
	if v.Str() != "function" {
		t.Errorf("expected 'function', got %v", v)
	}
}

func TestTypeOfCoroutineValue(t *testing.T) {
	v := getGlobal(t, `
		co := coroutine.create(func() {})
		result := type(co)
	`, "result")
	if v.Str() != "coroutine" {
		t.Errorf("expected 'coroutine', got %v", v)
	}
}

// --- math.type() ---

func TestMathTypeInteger(t *testing.T) {
	v := getGlobal(t, `result := math.type(42)`, "result")
	if v.Str() != "integer" {
		t.Errorf("expected 'integer', got %v", v)
	}
}

func TestMathTypeFloat(t *testing.T) {
	v := getGlobal(t, `result := math.type(3.14)`, "result")
	if v.Str() != "float" {
		t.Errorf("expected 'float', got %v", v)
	}
}

func TestMathTypeNonNumber(t *testing.T) {
	v := getGlobal(t, `result := math.type("hello")`, "result")
	if v.Truthy() {
		t.Errorf("expected false for non-number, got %v", v)
	}
}

// --- tostring() ---

func TestTostringNil(t *testing.T) {
	v := getGlobal(t, `result := tostring(nil)`, "result")
	if v.Str() != "nil" {
		t.Errorf("expected 'nil', got %v", v)
	}
}

func TestTostringBoolTrue(t *testing.T) {
	v := getGlobal(t, `result := tostring(true)`, "result")
	if v.Str() != "true" {
		t.Errorf("expected 'true', got %v", v)
	}
}

func TestTostringBoolFalse(t *testing.T) {
	v := getGlobal(t, `result := tostring(false)`, "result")
	if v.Str() != "false" {
		t.Errorf("expected 'false', got %v", v)
	}
}

func TestTostringInt(t *testing.T) {
	v := getGlobal(t, `result := tostring(42)`, "result")
	if v.Str() != "42" {
		t.Errorf("expected '42', got %v", v)
	}
}

func TestTostringFloat(t *testing.T) {
	v := getGlobal(t, `result := tostring(3.14)`, "result")
	if v.Str() != "3.14" {
		t.Errorf("expected '3.14', got %v", v)
	}
}

func TestTostringString(t *testing.T) {
	v := getGlobal(t, `result := tostring("hello")`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %v", v)
	}
}

func TestTostringTable(t *testing.T) {
	v := getGlobal(t, `result := tostring({})`, "result")
	if !strings.HasPrefix(v.Str(), "table:") {
		t.Errorf("expected 'table:...', got %v", v)
	}
}

func TestTostringFunction(t *testing.T) {
	v := getGlobal(t, `result := tostring(func() {})`, "result")
	if !strings.HasPrefix(v.Str(), "function:") {
		t.Errorf("expected 'function:...', got %v", v)
	}
}

// --- tonumber() ---

func TestTonumberFromInt(t *testing.T) {
	v := getGlobal(t, `result := tonumber(42)`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestTonumberFromFloat(t *testing.T) {
	v := getGlobal(t, `result := tonumber(3.14)`, "result")
	if !v.IsFloat() || v.Float() != 3.14 {
		t.Errorf("expected float 3.14, got %v", v)
	}
}

func TestTonumberFromStringInt(t *testing.T) {
	v := getGlobal(t, `result := tonumber("42")`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

func TestTonumberFromStringFloat(t *testing.T) {
	v := getGlobal(t, `result := tonumber("3.14")`, "result")
	if !v.IsFloat() || v.Float() != 3.14 {
		t.Errorf("expected float 3.14, got %v", v)
	}
}

func TestTonumberFromInvalidString(t *testing.T) {
	v := getGlobal(t, `result := tonumber("hello")`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestTonumberFromNil(t *testing.T) {
	v := getGlobal(t, `result := tonumber(nil)`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestTonumberFromBool(t *testing.T) {
	v := getGlobal(t, `result := tonumber(true)`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil for bool->number, got %v", v)
	}
}

func TestTonumberFromStringWithSpaces(t *testing.T) {
	v := getGlobal(t, `result := tonumber("  10  ")`, "result")
	if !v.IsInt() || v.Int() != 10 {
		t.Errorf("expected int 10, got %v", v)
	}
}

// --- String coercion in arithmetic ---

func TestStringCoercionInAdd(t *testing.T) {
	v := getGlobal(t, `result := "10" + 5`, "result")
	if !v.IsInt() || v.Int() != 15 {
		t.Errorf("expected int 15, got %v", v)
	}
}

func TestStringCoercionInMul(t *testing.T) {
	v := getGlobal(t, `result := "6" * "7"`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected int 42, got %v", v)
	}
}

// --- Truthiness ---

func TestTruthinessNilIsFalsy(t *testing.T) {
	v := getGlobal(t, `
		result := false
		if nil {
			result = true
		}
	`, "result")
	if v.Bool() {
		t.Errorf("nil should be falsy")
	}
}

func TestTruthinessFalseIsFalsy(t *testing.T) {
	v := getGlobal(t, `
		result := false
		if false {
			result = true
		}
	`, "result")
	if v.Bool() {
		t.Errorf("false should be falsy")
	}
}

func TestTruthinessZeroIsTruthy(t *testing.T) {
	v := getGlobal(t, `
		result := false
		if 0 {
			result = true
		}
	`, "result")
	if !v.Bool() {
		t.Errorf("0 should be truthy (Lua semantics)")
	}
}

func TestTruthinessEmptyStringIsTruthy(t *testing.T) {
	v := getGlobal(t, `
		result := false
		if "" {
			result = true
		}
	`, "result")
	if !v.Bool() {
		t.Errorf("empty string should be truthy (Lua semantics)")
	}
}

func TestTruthinessTableIsTruthy(t *testing.T) {
	v := getGlobal(t, `
		result := false
		if {} {
			result = true
		}
	`, "result")
	if !v.Bool() {
		t.Errorf("table should be truthy")
	}
}

// --- Equality ---

func TestEqualityNilNil(t *testing.T) {
	v := getGlobal(t, `result := nil == nil`, "result")
	if !v.Bool() {
		t.Errorf("nil == nil should be true")
	}
}

func TestEqualityIntFloat(t *testing.T) {
	v := getGlobal(t, `result := 1 == 1.0`, "result")
	if !v.Bool() {
		t.Errorf("1 == 1.0 should be true")
	}
}

func TestEqualityStringString(t *testing.T) {
	v := getGlobal(t, `result := "a" == "a"`, "result")
	if !v.Bool() {
		t.Errorf(`"a" == "a" should be true`)
	}
}

func TestEqualityDifferentTypes(t *testing.T) {
	v := getGlobal(t, `result := 1 == "1"`, "result")
	if v.Bool() {
		t.Errorf(`1 == "1" should be false (different types)`)
	}
}

func TestTableIdentityNotValue(t *testing.T) {
	v := getGlobal(t, `
		a := {x: 1}
		b := {x: 1}
		result := a == b
	`, "result")
	if v.Bool() {
		t.Errorf("two different tables with same content should not be ==")
	}
}

func TestTableSameIdentity(t *testing.T) {
	v := getGlobal(t, `
		a := {x: 1}
		b := a
		result := a == b
	`, "result")
	if !v.Bool() {
		t.Errorf("same table reference should be ==")
	}
}

func TestEqualityFalseNotNil(t *testing.T) {
	v := getGlobal(t, `result := false == nil`, "result")
	if v.Bool() {
		t.Errorf("false == nil should be false")
	}
}

func TestNotEqualOperator(t *testing.T) {
	v := getGlobal(t, `result := 1 != 2`, "result")
	if !v.Bool() {
		t.Errorf("1 != 2 should be true")
	}
}
