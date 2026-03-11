package runtime

import (
	"testing"
)

func TestStringTrim(t *testing.T) {
	interp := runProgram(t, `
		a := string.trim("  hello  ")
		b := string.trim("xxhelloxx", "x")
	`)
	if interp.GetGlobal("a").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestStringTrimLeft(t *testing.T) {
	interp := runProgram(t, `
		a := string.trimLeft("  hello  ")
		b := string.trimLeft("xxhello", "x")
	`)
	if interp.GetGlobal("a").Str() != "hello  " {
		t.Errorf("expected 'hello  ', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestStringTrimRight(t *testing.T) {
	interp := runProgram(t, `
		a := string.trimRight("  hello  ")
		b := string.trimRight("helloxx", "x")
	`)
	if interp.GetGlobal("a").Str() != "  hello" {
		t.Errorf("expected '  hello', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestStringSplitEmpty(t *testing.T) {
	interp := runProgram(t, `
		parts := string.split("abc", "")
	`)
	tbl := interp.GetGlobal("parts").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 parts, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected 'a', got '%s'", tbl.RawGet(IntValue(1)).Str())
	}
	if tbl.RawGet(IntValue(2)).Str() != "b" {
		t.Errorf("expected 'b', got '%s'", tbl.RawGet(IntValue(2)).Str())
	}
	if tbl.RawGet(IntValue(3)).Str() != "c" {
		t.Errorf("expected 'c', got '%s'", tbl.RawGet(IntValue(3)).Str())
	}
}

func TestStringHasPrefix(t *testing.T) {
	interp := runProgram(t, `
		a := string.hasPrefix("hello world", "hello")
		b := string.hasPrefix("hello world", "world")
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for hasPrefix 'hello'")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for hasPrefix 'world'")
	}
}

func TestStringHasSuffix(t *testing.T) {
	interp := runProgram(t, `
		a := string.hasSuffix("hello world", "world")
		b := string.hasSuffix("hello world", "hello")
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for hasSuffix 'world'")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for hasSuffix 'hello'")
	}
}

func TestStringContains(t *testing.T) {
	interp := runProgram(t, `
		a := string.contains("hello world", "lo wo")
		b := string.contains("hello world", "xyz")
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for contains 'lo wo'")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for contains 'xyz'")
	}
}

func TestStringCount(t *testing.T) {
	interp := runProgram(t, `
		a := string.count("hello", "l")
		b := string.count("banana", "na")
	`)
	if interp.GetGlobal("a").Int() != 2 {
		t.Errorf("expected 2, got %d", interp.GetGlobal("a").Int())
	}
	if interp.GetGlobal("b").Int() != 2 {
		t.Errorf("expected 2, got %d", interp.GetGlobal("b").Int())
	}
}

func TestStringReplaceAll(t *testing.T) {
	interp := runProgram(t, `
		result := string.replaceAll("hello world", "o", "0")
	`)
	if interp.GetGlobal("result").Str() != "hell0 w0rld" {
		t.Errorf("expected 'hell0 w0rld', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestStringJoin(t *testing.T) {
	interp := runProgram(t, `
		result := string.join({"a", "b", "c"}, ", ")
	`)
	if interp.GetGlobal("result").Str() != "a, b, c" {
		t.Errorf("expected 'a, b, c', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestStringTitle(t *testing.T) {
	interp := runProgram(t, `
		result := string.title("hello world foo")
	`)
	if interp.GetGlobal("result").Str() != "Hello World Foo" {
		t.Errorf("expected 'Hello World Foo', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestStringPadLeft(t *testing.T) {
	interp := runProgram(t, `
		a := string.padLeft("hi", 5)
		b := string.padLeft("hi", 5, "0")
	`)
	if interp.GetGlobal("a").Str() != "   hi" {
		t.Errorf("expected '   hi', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "000hi" {
		t.Errorf("expected '000hi', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestStringPadRight(t *testing.T) {
	interp := runProgram(t, `
		a := string.padRight("hi", 5)
		b := string.padRight("hi", 5, "0")
	`)
	if interp.GetGlobal("a").Str() != "hi   " {
		t.Errorf("expected 'hi   ', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "hi000" {
		t.Errorf("expected 'hi000', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestStringRepeatAlias(t *testing.T) {
	interp := runProgram(t, `
		a := string.repeat("ab", 3)
		b := string.repeat("x", 0)
	`)
	if interp.GetGlobal("a").Str() != "ababab" {
		t.Errorf("expected 'ababab', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "" {
		t.Errorf("expected '', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestStringIsNumeric(t *testing.T) {
	interp := runProgram(t, `
		a := string.isNumeric("123")
		b := string.isNumeric("3.14")
		c := string.isNumeric("-42")
		d := string.isNumeric("hello")
		e := string.isNumeric("")
		f := string.isNumeric("1e10")
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for '123'")
	}
	if !interp.GetGlobal("b").Bool() {
		t.Errorf("expected true for '3.14'")
	}
	if !interp.GetGlobal("c").Bool() {
		t.Errorf("expected true for '-42'")
	}
	if interp.GetGlobal("d").Bool() {
		t.Errorf("expected false for 'hello'")
	}
	if interp.GetGlobal("e").Bool() {
		t.Errorf("expected false for ''")
	}
	if !interp.GetGlobal("f").Bool() {
		t.Errorf("expected true for '1e10'")
	}
}
