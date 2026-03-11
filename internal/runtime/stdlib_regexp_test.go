package runtime

import (
	"testing"
)

// ==================================================================
// Regexp library tests
// ==================================================================

func TestRegexpMatch(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.match("^hello", "hello world")
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
}

func TestRegexpMatch_noMatch(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.match("^world", "hello world")
	`)
	v := interp.GetGlobal("result")
	if !v.IsBool() || v.Bool() {
		t.Errorf("expected false, got %v", v)
	}
}

func TestRegexpFind(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.find("[0-9]+", "hello123world")
	`)
	v := interp.GetGlobal("result")
	if !v.IsString() || v.Str() != "123" {
		t.Errorf("expected '123', got %v", v)
	}
}

func TestRegexpFind_noMatch(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.find("[0-9]+", "hello world")
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestRegexpFindAll(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.findAll("[0-9]+", "a1b22c333")
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 matches, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "1" {
		t.Errorf("expected '1', got '%s'", tbl.RawGet(IntValue(1)).Str())
	}
	if tbl.RawGet(IntValue(2)).Str() != "22" {
		t.Errorf("expected '22', got '%s'", tbl.RawGet(IntValue(2)).Str())
	}
	if tbl.RawGet(IntValue(3)).Str() != "333" {
		t.Errorf("expected '333', got '%s'", tbl.RawGet(IntValue(3)).Str())
	}
}

func TestRegexpFindAll_limit(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.findAll("[0-9]+", "a1b22c333", 2)
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 2 {
		t.Errorf("expected 2 matches, got %d", tbl.Length())
	}
}

func TestRegexpReplace(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.replace("[0-9]+", "a1b22c333", "X")
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "aXb22c333" {
		t.Errorf("expected 'aXb22c333', got '%s'", v.Str())
	}
}

func TestRegexpReplaceAll(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.replaceAll("[0-9]+", "a1b22c333", "X")
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "aXbXcX" {
		t.Errorf("expected 'aXbXcX', got '%s'", v.Str())
	}
}

func TestRegexpSplit(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.split(",\\s*", "a, b, c")
	`)
	tbl := interp.GetGlobal("result").Table()
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

func TestRegexpSplit_limit(t *testing.T) {
	interp := runProgram(t, `
		result := regexp.split(",", "a,b,c,d", 3)
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 parts, got %d", tbl.Length())
	}
}

func TestRegexpCompile(t *testing.T) {
	interp := runProgram(t, `
		re, err := regexp.compile("[0-9]+")
		result := re.match("hello123")
		pat := re.pattern
	`)
	if interp.GetGlobal("err").IsNil() == false && interp.GetGlobal("err").Str() != "" {
		t.Errorf("expected no error, got %v", interp.GetGlobal("err"))
	}
	v := interp.GetGlobal("result")
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}
	pat := interp.GetGlobal("pat")
	if pat.Str() != "[0-9]+" {
		t.Errorf("expected '[0-9]+', got '%s'", pat.Str())
	}
}

func TestRegexpCompile_invalid(t *testing.T) {
	interp := runProgram(t, `
		re, err := regexp.compile("[invalid")
	`)
	re := interp.GetGlobal("re")
	err := interp.GetGlobal("err")
	if !re.IsNil() {
		t.Errorf("expected nil for invalid pattern, got %v", re)
	}
	if !err.IsString() || err.Str() == "" {
		t.Errorf("expected error message, got %v", err)
	}
}

func TestRegexpMustCompile(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("[0-9]+")
		result := re.find("abc456def")
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "456" {
		t.Errorf("expected '456', got '%s'", v.Str())
	}
}

func TestRegexpReObject_findSubmatch(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("(\\w+)@(\\w+)")
		result := re.findSubmatch("user@host")
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.RawGet(IntValue(1)).Str() != "user@host" {
		t.Errorf("expected 'user@host' at [1], got '%s'", tbl.RawGet(IntValue(1)).Str())
	}
	if tbl.RawGet(IntValue(2)).Str() != "user" {
		t.Errorf("expected 'user' at [2], got '%s'", tbl.RawGet(IntValue(2)).Str())
	}
	if tbl.RawGet(IntValue(3)).Str() != "host" {
		t.Errorf("expected 'host' at [3], got '%s'", tbl.RawGet(IntValue(3)).Str())
	}
}

func TestRegexpReObject_findSubmatch_noMatch(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("(\\d+)-(\\d+)")
		result := re.findSubmatch("no match here")
	`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestRegexpReObject_findAll(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("\\d+")
		result := re.findAll("a1b22c333")
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 matches, got %d", tbl.Length())
	}
}

func TestRegexpReObject_findAllSubmatch(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("(\\w)(\\d)")
		result := re.findAllSubmatch("a1b2c3")
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 matches, got %d", tbl.Length())
	}
	// First match: "a1" with captures "a" and "1"
	first := tbl.RawGet(IntValue(1)).Table()
	if first.RawGet(IntValue(1)).Str() != "a1" {
		t.Errorf("expected 'a1', got '%s'", first.RawGet(IntValue(1)).Str())
	}
	if first.RawGet(IntValue(2)).Str() != "a" {
		t.Errorf("expected 'a', got '%s'", first.RawGet(IntValue(2)).Str())
	}
	if first.RawGet(IntValue(3)).Str() != "1" {
		t.Errorf("expected '1', got '%s'", first.RawGet(IntValue(3)).Str())
	}
}

func TestRegexpReObject_replace(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("\\d+")
		result := re.replace("a1b22c333", "X")
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "aXb22c333" {
		t.Errorf("expected 'aXb22c333', got '%s'", v.Str())
	}
}

func TestRegexpReObject_replaceAll(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("\\d+")
		result := re.replaceAll("a1b22c333", "X")
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "aXbXcX" {
		t.Errorf("expected 'aXbXcX', got '%s'", v.Str())
	}
}

func TestRegexpReObject_split(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile(",\\s*")
		result := re.split("a, b, c")
	`)
	tbl := interp.GetGlobal("result").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 parts, got %d", tbl.Length())
	}
}

func TestRegexpReObject_numSubexp(t *testing.T) {
	interp := runProgram(t, `
		re := regexp.mustCompile("(\\w+)@(\\w+)\\.(\\w+)")
		result := re.numSubexp()
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 3 {
		t.Errorf("expected 3, got %v", v)
	}
}
