package runtime

import (
	"testing"
)

// ==================================================================
// String operations edge cases (beyond what string_test.go covers)
// ==================================================================

// --- Concat edge cases ---

func TestConcatMultipleStrings(t *testing.T) {
	v := getGlobal(t, `result := "a" .. "b" .. "c" .. "d" .. "e"`, "result")
	if v.Str() != "abcde" {
		t.Errorf("expected 'abcde', got %q", v.Str())
	}
}

func TestConcatBoolToString(t *testing.T) {
	// Booleans cannot be concatenated
	err := runProgramExpectError(t, `result := "val: " .. true`)
	if err == nil {
		t.Fatal("expected error for concatenating boolean")
	}
}

func TestConcatNilToString(t *testing.T) {
	err := runProgramExpectError(t, `result := "val: " .. nil`)
	if err == nil {
		t.Fatal("expected error for concatenating nil")
	}
}

// --- String length edge cases ---

func TestStringLenWithSpaces(t *testing.T) {
	v := getGlobal(t, `result := #"  hello  "`, "result")
	if v.Int() != 9 {
		t.Errorf("expected 9, got %v", v)
	}
}

func TestStringLenSingleChar(t *testing.T) {
	v := getGlobal(t, `result := #"x"`, "result")
	if v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

// --- tostring edge cases ---

func TestTostringNegInt(t *testing.T) {
	v := getGlobal(t, `result := tostring(-42)`, "result")
	if v.Str() != "-42" {
		t.Errorf("expected '-42', got %q", v.Str())
	}
}

func TestTostringZero(t *testing.T) {
	v := getGlobal(t, `result := tostring(0)`, "result")
	if v.Str() != "0" {
		t.Errorf("expected '0', got %q", v.Str())
	}
}

func TestTostringFloatWholeNumber(t *testing.T) {
	v := getGlobal(t, `result := tostring(3.0)`, "result")
	// Should have decimal point to show it's float
	if v.Str() != "3.0" {
		t.Errorf("expected '3.0', got %q", v.Str())
	}
}

// --- tonumber edge cases ---

func TestTonumberNegativeString(t *testing.T) {
	v := getGlobal(t, `result := tonumber("-42")`, "result")
	if !v.IsInt() || v.Int() != -42 {
		t.Errorf("expected -42, got %v", v)
	}
}

func TestTonumberScientific(t *testing.T) {
	v := getGlobal(t, `result := tonumber("1e3")`, "result")
	if !v.IsFloat() || v.Float() != 1000.0 {
		t.Errorf("expected 1000.0, got %v", v)
	}
}

func TestTonumberEmptyString(t *testing.T) {
	v := getGlobal(t, `result := tonumber("")`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil for empty string, got %v", v)
	}
}

func TestTonumberTable(t *testing.T) {
	v := getGlobal(t, `result := tonumber({})`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil for table, got %v", v)
	}
}

// --- String method chaining ---

func TestStringMethodChaining(t *testing.T) {
	v := getGlobal(t, `result := ("hello"):upper():sub(1, 3)`, "result")
	if v.Str() != "HEL" {
		t.Errorf("expected 'HEL', got %q", v.Str())
	}
}

// --- string.find from init position ---

func TestStringFindFromMiddle(t *testing.T) {
	interp := runProgram(t, `s, e := string.find("hello hello", "hello", 2, true)`)
	s := interp.GetGlobal("s")
	e := interp.GetGlobal("e")
	if s.Int() != 7 || e.Int() != 11 {
		t.Errorf("expected 7,11, got %v,%v", s, e)
	}
}

func TestStringFindEmptyString(t *testing.T) {
	interp := runProgram(t, `s, e := string.find("hello", "", 1, true)`)
	s := interp.GetGlobal("s")
	if s.Int() != 1 {
		t.Errorf("expected start=1, got %v", s)
	}
}

// --- string.gsub with pattern ---

func TestStringGsubPattern(t *testing.T) {
	interp := runProgram(t, `result, count := string.gsub("hello 123 world 456", "%d+", "NUM")`)
	if interp.GetGlobal("result").Str() != "hello NUM world NUM" {
		t.Errorf("expected 'hello NUM world NUM', got %q", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected count=2, got %v", interp.GetGlobal("count"))
	}
}

// --- string.rep edge cases ---

func TestStringRepOne(t *testing.T) {
	v := getGlobal(t, `result := string.rep("hello", 1)`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

func TestStringRepNegative(t *testing.T) {
	v := getGlobal(t, `result := string.rep("hello", -1)`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string for negative rep, got %q", v.Str())
	}
}

// --- string.reverse edge cases ---

func TestStringReverseSingleChar(t *testing.T) {
	v := getGlobal(t, `result := string.reverse("x")`, "result")
	if v.Str() != "x" {
		t.Errorf("expected 'x', got %q", v.Str())
	}
}

func TestStringReversePalindrome(t *testing.T) {
	v := getGlobal(t, `result := string.reverse("racecar")`, "result")
	if v.Str() != "racecar" {
		t.Errorf("expected 'racecar', got %q", v.Str())
	}
}

// --- string.byte edge cases ---

func TestStringByteAtPosition(t *testing.T) {
	v := getGlobal(t, `result := string.byte("ABC", 2)`, "result")
	if v.Int() != 66 {
		t.Errorf("expected 66 (B), got %v", v)
	}
}

// --- string.char edge cases ---

func TestStringCharEmpty(t *testing.T) {
	v := getGlobal(t, `result := string.char()`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

func TestStringCharNewline(t *testing.T) {
	v := getGlobal(t, `result := string.char(10)`, "result")
	if v.Str() != "\n" {
		t.Errorf("expected newline, got %q", v.Str())
	}
}

// --- string.split edge cases ---

func TestStringSplitNoMatch(t *testing.T) {
	interp := runProgram(t, `parts := string.split("hello", ",")`)
	tbl := interp.GetGlobal("parts").Table()
	if tbl.Length() != 1 {
		t.Errorf("expected 1 part, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "hello" {
		t.Errorf("expected 'hello', got %q", tbl.RawGet(IntValue(1)).Str())
	}
}

func TestStringSplitMultiCharSep(t *testing.T) {
	interp := runProgram(t, `parts := string.split("a::b::c", "::")`)
	tbl := interp.GetGlobal("parts").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 parts, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected 'a', got %q", tbl.RawGet(IntValue(1)).Str())
	}
	if tbl.RawGet(IntValue(2)).Str() != "b" {
		t.Errorf("expected 'b', got %q", tbl.RawGet(IntValue(2)).Str())
	}
}

// --- String comparison edge cases ---

func TestStringCompareEmpty(t *testing.T) {
	v := getGlobal(t, `result := "" < "a"`, "result")
	if !v.Bool() {
		t.Errorf("empty string should be less than 'a'")
	}
}

func TestStringCompareCase(t *testing.T) {
	v := getGlobal(t, `result := "A" < "a"`, "result")
	if !v.Bool() {
		t.Errorf("'A' should be less than 'a' (ASCII order)")
	}
}

func TestStringCompareNotEqual(t *testing.T) {
	v := getGlobal(t, `result := "abc" != "def"`, "result")
	if !v.Bool() {
		t.Errorf("expected true")
	}
}

// --- String in concatenation with numbers ---

func TestConcatIntZero(t *testing.T) {
	v := getGlobal(t, `result := 0 .. ""`, "result")
	if v.Str() != "0" {
		t.Errorf("expected '0', got %q", v.Str())
	}
}

func TestConcatNegativeNumber(t *testing.T) {
	v := getGlobal(t, `result := "value: " .. -5`, "result")
	if v.Str() != "value: -5" {
		t.Errorf("expected 'value: -5', got %q", v.Str())
	}
}

// --- string.format edge cases ---

func TestStringFormatEmptyString(t *testing.T) {
	v := getGlobal(t, `result := string.format("")`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

func TestStringFormatNoSpecifiers(t *testing.T) {
	v := getGlobal(t, `result := string.format("hello world")`, "result")
	if v.Str() != "hello world" {
		t.Errorf("expected 'hello world', got %q", v.Str())
	}
}

func TestStringFormatNegativeInt(t *testing.T) {
	v := getGlobal(t, `result := string.format("%d", -42)`, "result")
	if v.Str() != "-42" {
		t.Errorf("expected '-42', got %q", v.Str())
	}
}

// --- string.match edge cases ---

func TestStringMatchAnchored(t *testing.T) {
	v := getGlobal(t, `result := string.match("hello123", "^%a+")`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

func TestStringMatchEndAnchor(t *testing.T) {
	v := getGlobal(t, `result := string.match("hello123", "%d+$")`, "result")
	if v.Str() != "123" {
		t.Errorf("expected '123', got %q", v.Str())
	}
}

// --- String used as table keys ---

func TestStringTableKey(t *testing.T) {
	v := getGlobal(t, `
		t := {}
		key := "mykey"
		t[key] = 42
		result := t[key]
	`, "result")
	if !v.IsInt() || v.Int() != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestStringDynamicFieldAccess(t *testing.T) {
	v := getGlobal(t, `
		t := {a: 1, b: 2, c: 3}
		key := "b"
		result := t[key]
	`, "result")
	if !v.IsInt() || v.Int() != 2 {
		t.Errorf("expected 2, got %v", v)
	}
}
