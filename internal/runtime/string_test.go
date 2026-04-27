package runtime

import (
	"strings"
	"testing"
)

// ==================================================================
// Comprehensive string tests
// ==================================================================

// --- string.sub edge cases ---

func TestStringSubBasic(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", 2, 4)`, "result")
	if v.Str() != "ell" {
		t.Errorf("expected 'ell', got %q", v.Str())
	}
}

func TestStringSubToEnd(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", 2)`, "result")
	if v.Str() != "ello" {
		t.Errorf("expected 'ello', got %q", v.Str())
	}
}

func TestStringSubNegativeStart(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", -3)`, "result")
	if v.Str() != "llo" {
		t.Errorf("expected 'llo', got %q", v.Str())
	}
}

func TestStringSubNegativeEnd(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", 1, -2)`, "result")
	if v.Str() != "hell" {
		t.Errorf("expected 'hell', got %q", v.Str())
	}
}

func TestStringSubEmptyResult(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", 3, 2)`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

func TestStringSubWholeString(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", 1)`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

func TestStringSubOutOfRange(t *testing.T) {
	v := getGlobal(t, `result := string.sub("hello", 1, 100)`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

// --- string.format ---

func TestStringFormatD(t *testing.T) {
	v := getGlobal(t, `result := string.format("%d", 42)`, "result")
	if v.Str() != "42" {
		t.Errorf("expected '42', got %q", v.Str())
	}
}

func TestStringFormatS(t *testing.T) {
	v := getGlobal(t, `result := string.format("%s", "hello")`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

func TestStringFormatF(t *testing.T) {
	v := getGlobal(t, `result := string.format("%f", 3.14)`, "result")
	if !strings.HasPrefix(v.Str(), "3.14") {
		t.Errorf("expected string starting with '3.14', got %q", v.Str())
	}
}

func TestStringFormatX(t *testing.T) {
	v := getGlobal(t, `result := string.format("%x", 255)`, "result")
	if v.Str() != "ff" {
		t.Errorf("expected 'ff', got %q", v.Str())
	}
}

func TestStringFormatXUpper(t *testing.T) {
	v := getGlobal(t, `result := string.format("%X", 255)`, "result")
	if v.Str() != "FF" {
		t.Errorf("expected 'FF', got %q", v.Str())
	}
}

func TestStringFormatPercent(t *testing.T) {
	v := getGlobal(t, `result := string.format("%%")`, "result")
	if v.Str() != "%" {
		t.Errorf("expected '%%', got %q", v.Str())
	}
}

func TestStringFormatPadded(t *testing.T) {
	v := getGlobal(t, `result := string.format("%05d", 42)`, "result")
	if v.Str() != "00042" {
		t.Errorf("expected '00042', got %q", v.Str())
	}
}

func TestStringFormatPaddedNegative(t *testing.T) {
	v := getGlobal(t, `result := string.format("%05d", -42)`, "result")
	if v.Str() != "-0042" {
		t.Errorf("expected '-0042', got %q", v.Str())
	}
}

func TestStringFormatMultiArgs(t *testing.T) {
	v := getGlobal(t, `result := string.format("hello %s, you are %d", "world", 42)`, "result")
	if v.Str() != "hello world, you are 42" {
		t.Errorf("expected 'hello world, you are 42', got %q", v.Str())
	}
}

func TestStringFormatQ(t *testing.T) {
	v := getGlobal(t, `result := string.format("%q", "hello")`, "result")
	if v.Str() != `"hello"` {
		t.Errorf("expected '\"hello\"', got %q", v.Str())
	}
}

func TestStringFormatC(t *testing.T) {
	v := getGlobal(t, `result := string.format("%c", 65)`, "result")
	if v.Str() != "A" {
		t.Errorf("expected 'A', got %q", v.Str())
	}
}

func TestStringFormatG(t *testing.T) {
	v := getGlobal(t, `result := string.format("%g", 3.14)`, "result")
	if v.Str() != "3.14" {
		t.Errorf("expected '3.14', got %q", v.Str())
	}
}

// --- string.find ---

func TestStringFindPlain(t *testing.T) {
	interp := runProgram(t, `s, e := string.find("hello world", "world", 1, true)`)
	s := interp.GetGlobal("s")
	e := interp.GetGlobal("e")
	if s.Int() != 7 || e.Int() != 11 {
		t.Errorf("expected 7,11 got %v,%v", s, e)
	}
}

func TestStringFindNotFound(t *testing.T) {
	v := getGlobal(t, `result := string.find("hello", "xyz", 1, true)`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestStringFindPattern(t *testing.T) {
	interp := runProgram(t, `s, e := string.find("hello123", "%d+")`)
	s := interp.GetGlobal("s")
	e := interp.GetGlobal("e")
	if s.Int() != 6 || e.Int() != 8 {
		t.Errorf("expected 6,8 got %v,%v", s, e)
	}
}

func TestStringFindWithInit(t *testing.T) {
	interp := runProgram(t, `s, e := string.find("abcabc", "bc", 3, true)`)
	s := interp.GetGlobal("s")
	e := interp.GetGlobal("e")
	if s.Int() != 5 || e.Int() != 6 {
		t.Errorf("expected 5,6 got %v,%v", s, e)
	}
}

// --- string.match ---

func TestStringMatchBasic(t *testing.T) {
	v := getGlobal(t, `result := string.match("hello123", "%d+")`, "result")
	if v.Str() != "123" {
		t.Errorf("expected '123', got %q", v.Str())
	}
}

func TestStringMatchCaptures(t *testing.T) {
	interp := runProgram(t, `y, m, d := string.match("2026-03-10", "(%d+)-(%d+)-(%d+)")`)
	if interp.GetGlobal("y").Str() != "2026" {
		t.Errorf("expected '2026', got %q", interp.GetGlobal("y").Str())
	}
	if interp.GetGlobal("m").Str() != "03" {
		t.Errorf("expected '03', got %q", interp.GetGlobal("m").Str())
	}
	if interp.GetGlobal("d").Str() != "10" {
		t.Errorf("expected '10', got %q", interp.GetGlobal("d").Str())
	}
}

func TestStringMatchNoMatch(t *testing.T) {
	v := getGlobal(t, `result := string.match("hello", "%d+")`, "result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

// --- string.gmatch ---

func TestStringGmatchBasic(t *testing.T) {
	interp := runProgram(t, `
		result := {}
		i := 1
		for w := range string.gmatch("hello world foo", "%a+") {
			result[i] = w
			i = i + 1
		}
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 matches, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "hello" {
		t.Errorf("expected 'hello', got %q", tbl.RawGet(IntValue(1)).Str())
	}
	if tbl.RawGet(IntValue(2)).Str() != "world" {
		t.Errorf("expected 'world', got %q", tbl.RawGet(IntValue(2)).Str())
	}
	if tbl.RawGet(IntValue(3)).Str() != "foo" {
		t.Errorf("expected 'foo', got %q", tbl.RawGet(IntValue(3)).Str())
	}
}

// --- string.gsub ---

func TestStringGsubBasic(t *testing.T) {
	interp := runProgram(t, `result, count := string.gsub("hello world", "o", "0")`)
	if interp.GetGlobal("result").Str() != "hell0 w0rld" {
		t.Errorf("expected 'hell0 w0rld', got %q", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected count=2, got %v", interp.GetGlobal("count"))
	}
}

func TestStringGsubMaxReplace(t *testing.T) {
	interp := runProgram(t, `result, count := string.gsub("aaa", "a", "b", 2)`)
	if interp.GetGlobal("result").Str() != "bba" {
		t.Errorf("expected 'bba', got %q", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected count=2, got %v", interp.GetGlobal("count"))
	}
}

func TestStringGsubNoMatch(t *testing.T) {
	interp := runProgram(t, `result, count := string.gsub("hello", "z", "x")`)
	if interp.GetGlobal("result").Str() != "hello" {
		t.Errorf("expected 'hello', got %q", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("count").Int() != 0 {
		t.Errorf("expected count=0, got %v", interp.GetGlobal("count"))
	}
}

// --- string.byte ---

func TestStringByteDefault(t *testing.T) {
	v := getGlobal(t, `result := string.byte("A")`, "result")
	if v.Int() != 65 {
		t.Errorf("expected 65, got %v", v)
	}
}

func TestStringByteRange(t *testing.T) {
	interp := runProgram(t, `a, b, c := string.byte("abc", 1, 3)`)
	if interp.GetGlobal("a").Int() != 97 {
		t.Errorf("expected a=97, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 98 {
		t.Errorf("expected b=98, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 99 {
		t.Errorf("expected c=99, got %v", interp.GetGlobal("c"))
	}
}

// --- string.char ---

func TestStringCharBasic(t *testing.T) {
	v := getGlobal(t, `result := string.char(65, 66, 67)`, "result")
	if v.Str() != "ABC" {
		t.Errorf("expected 'ABC', got %q", v.Str())
	}
}

func TestStringCharSingle(t *testing.T) {
	v := getGlobal(t, `result := string.char(72)`, "result")
	if v.Str() != "H" {
		t.Errorf("expected 'H', got %q", v.Str())
	}
}

// --- string.rep ---

func TestStringRepBasic(t *testing.T) {
	v := getGlobal(t, `result := string.rep("ab", 3)`, "result")
	if v.Str() != "ababab" {
		t.Errorf("expected 'ababab', got %q", v.Str())
	}
}

func TestStringRepWithSep(t *testing.T) {
	v := getGlobal(t, `result := string.rep("ab", 3, ",")`, "result")
	if v.Str() != "ab,ab,ab" {
		t.Errorf("expected 'ab,ab,ab', got %q", v.Str())
	}
}

func TestStringRepZero(t *testing.T) {
	v := getGlobal(t, `result := string.rep("ab", 0)`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

// --- string.reverse ---

func TestStringReverseBasic(t *testing.T) {
	v := getGlobal(t, `result := string.reverse("hello")`, "result")
	if v.Str() != "olleh" {
		t.Errorf("expected 'olleh', got %q", v.Str())
	}
}

func TestStringReverseEmpty(t *testing.T) {
	v := getGlobal(t, `result := string.reverse("")`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

// --- string.upper / string.lower ---

func TestStringUpperLower(t *testing.T) {
	interp := runProgram(t, `
		u := string.upper("hello")
		l := string.lower("WORLD")
	`)
	if interp.GetGlobal("u").Str() != "HELLO" {
		t.Errorf("expected 'HELLO', got %q", interp.GetGlobal("u").Str())
	}
	if interp.GetGlobal("l").Str() != "world" {
		t.Errorf("expected 'world', got %q", interp.GetGlobal("l").Str())
	}
}

// --- string.len ---

func TestStringLenBasic(t *testing.T) {
	v := getGlobal(t, `result := string.len("hello")`, "result")
	if v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

func TestStringLenEmpty(t *testing.T) {
	v := getGlobal(t, `result := string.len("")`, "result")
	if v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

// --- String length with # ---

func TestStringLenOperator(t *testing.T) {
	v := getGlobal(t, `result := #"hello"`, "result")
	if v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

func TestStringLenOperatorEmpty(t *testing.T) {
	v := getGlobal(t, `result := #""`, "result")
	if v.Int() != 0 {
		t.Errorf("expected 0, got %v", v)
	}
}

// --- String method call syntax ---

func TestStringMethodUpper(t *testing.T) {
	v := getGlobal(t, `result := ("hello"):upper()`, "result")
	if v.Str() != "HELLO" {
		t.Errorf("expected 'HELLO', got %q", v.Str())
	}
}

func TestStringMethodLower(t *testing.T) {
	v := getGlobal(t, `result := ("HELLO"):lower()`, "result")
	if v.Str() != "hello" {
		t.Errorf("expected 'hello', got %q", v.Str())
	}
}

func TestStringMethodLen(t *testing.T) {
	v := getGlobal(t, `result := ("hello"):len()`, "result")
	if v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

func TestStringMethodRep(t *testing.T) {
	v := getGlobal(t, `result := ("ab"):rep(3)`, "result")
	if v.Str() != "ababab" {
		t.Errorf("expected 'ababab', got %q", v.Str())
	}
}

func TestStringMethodReverse(t *testing.T) {
	v := getGlobal(t, `result := ("hello"):reverse()`, "result")
	if v.Str() != "olleh" {
		t.Errorf("expected 'olleh', got %q", v.Str())
	}
}

func TestStringMethodSub(t *testing.T) {
	v := getGlobal(t, `result := ("hello"):sub(2, 4)`, "result")
	if v.Str() != "ell" {
		t.Errorf("expected 'ell', got %q", v.Str())
	}
}

// --- string.split ---

func TestStringSplitBasic(t *testing.T) {
	interp := runProgram(t, `parts := string.split("a,b,c", ",")`)
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
	if tbl.RawGet(IntValue(3)).Str() != "c" {
		t.Errorf("expected 'c', got %q", tbl.RawGet(IntValue(3)).Str())
	}
}

// --- String comparison ---

func TestStringComparisonLess(t *testing.T) {
	v := getGlobal(t, `result := "abc" < "abd"`, "result")
	if !v.Bool() {
		t.Errorf("expected true")
	}
}

func TestStringComparisonEqual(t *testing.T) {
	v := getGlobal(t, `result := "abc" == "abc"`, "result")
	if !v.Bool() {
		t.Errorf("expected true")
	}
}

func TestStringComparisonGreater(t *testing.T) {
	v := getGlobal(t, `result := "z" > "a"`, "result")
	if !v.Bool() {
		t.Errorf("expected true")
	}
}

// --- Empty string edge cases ---

func TestEmptyStringConcat(t *testing.T) {
	v := getGlobal(t, `result := "" .. ""`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}

func TestEmptyStringSub(t *testing.T) {
	v := getGlobal(t, `result := string.sub("", 1)`, "result")
	if v.Str() != "" {
		t.Errorf("expected empty string, got %q", v.Str())
	}
}
