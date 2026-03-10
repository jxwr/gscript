package runtime

import (
	"math"
	"strings"
	"testing"
)

// ==================================================================
// Error handling tests
// ==================================================================

func TestPcall_success(t *testing.T) {
	interp := runProgram(t, `
		func add(a, b) {
			return a + b
		}
		ok, result := pcall(add, 1, 2)
	`)
	ok := interp.GetGlobal("ok")
	result := interp.GetGlobal("result")
	if !ok.Bool() {
		t.Errorf("pcall should return true on success, got %v", ok)
	}
	if !result.IsInt() || result.Int() != 3 {
		t.Errorf("expected 3, got %v", result)
	}
}

func TestPcall_error_string(t *testing.T) {
	interp := runProgram(t, `
		func fail() {
			error("something went wrong")
		}
		ok, msg := pcall(fail)
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("pcall should return false on error")
	}
	if msg.Str() != "something went wrong" {
		t.Errorf("expected 'something went wrong', got '%v'", msg)
	}
}

func TestPcall_error_value(t *testing.T) {
	interp := runProgram(t, `
		func fail() {
			error(42)
		}
		ok, val := pcall(fail)
	`)
	ok := interp.GetGlobal("ok")
	val := interp.GetGlobal("val")
	if ok.Truthy() {
		t.Errorf("pcall should return false on error")
	}
	if !val.IsInt() || val.Int() != 42 {
		t.Errorf("expected 42, got %v", val)
	}
}

func TestPcall_runtime_error(t *testing.T) {
	interp := runProgram(t, `
		func fail() {
			x := nil
			return x.foo
		}
		ok, msg := pcall(fail)
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("pcall should return false on runtime error")
	}
	if !msg.IsString() || msg.Str() == "" {
		t.Errorf("expected error message, got %v", msg)
	}
}

func TestXpcall(t *testing.T) {
	interp := runProgram(t, `
		func fail() {
			error("oops")
		}
		func handler(err) {
			return "handled: " .. err
		}
		ok, msg := xpcall(fail, handler)
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("xpcall should return false on error")
	}
	if msg.Str() != "handled: oops" {
		t.Errorf("expected 'handled: oops', got '%v'", msg)
	}
}

func TestXpcall_success(t *testing.T) {
	interp := runProgram(t, `
		func good() {
			return 10, 20
		}
		func handler(err) {
			return "bad"
		}
		ok, a, b := xpcall(good, handler)
	`)
	ok := interp.GetGlobal("ok")
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if !ok.Bool() {
		t.Errorf("xpcall should return true on success")
	}
	if a.Int() != 10 || b.Int() != 20 {
		t.Errorf("expected 10, 20 got %v, %v", a, b)
	}
}

func TestAssert_pass(t *testing.T) {
	interp := runProgram(t, `
		x := assert(42, "should not fail")
	`)
	x := interp.GetGlobal("x")
	if !x.IsInt() || x.Int() != 42 {
		t.Errorf("assert should return its first arg on success, got %v", x)
	}
}

func TestAssert_fail(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(assert, false, "my message")
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("assert(false) should error")
	}
	if msg.Str() != "my message" {
		t.Errorf("expected 'my message', got '%v'", msg)
	}
}

func TestAssert_fail_default_msg(t *testing.T) {
	interp := runProgram(t, `
		ok, msg := pcall(assert, nil)
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("assert(nil) should error")
	}
	if msg.Str() != "assertion failed" {
		t.Errorf("expected 'assertion failed', got '%v'", msg)
	}
}

func TestErrorObject(t *testing.T) {
	interp := runProgram(t, `
		errTbl := {code: 404, msg: "not found"}
		func fail() {
			error(errTbl)
		}
		ok, val := pcall(fail)
	`)
	ok := interp.GetGlobal("ok")
	val := interp.GetGlobal("val")
	if ok.Truthy() {
		t.Errorf("pcall should return false")
	}
	if !val.IsTable() {
		t.Errorf("expected table error value, got %s", val.TypeName())
	}
	code := val.Table().RawGet(StringValue("code"))
	if code.Int() != 404 {
		t.Errorf("expected code 404, got %v", code)
	}
}

// ==================================================================
// String library tests
// ==================================================================

func TestStringLen(t *testing.T) {
	interp := runProgram(t, `
		result := string.len("hello")
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 5 {
		t.Errorf("expected 5, got %v", v)
	}
}

func TestStringSub(t *testing.T) {
	tests := []struct {
		src    string
		expect string
	}{
		{`result := string.sub("hello", 2, 4)`, "ell"},
		{`result := string.sub("hello", 2)`, "ello"},
		{`result := string.sub("hello", -3)`, "llo"},
		{`result := string.sub("hello", 1, -2)`, "hell"},
		{`result := string.sub("hello", 3, 2)`, ""},
	}
	for _, tt := range tests {
		interp := runProgram(t, tt.src)
		v := interp.GetGlobal("result")
		if v.Str() != tt.expect {
			t.Errorf("%s: expected %q, got %q", tt.src, tt.expect, v.Str())
		}
	}
}

func TestStringUpper_Lower(t *testing.T) {
	interp := runProgram(t, `
		u := string.upper("hello")
		l := string.lower("WORLD")
	`)
	if interp.GetGlobal("u").Str() != "HELLO" {
		t.Errorf("expected HELLO")
	}
	if interp.GetGlobal("l").Str() != "world" {
		t.Errorf("expected world")
	}
}

func TestStringRep(t *testing.T) {
	interp := runProgram(t, `
		a := string.rep("ab", 3)
		b := string.rep("ab", 3, ",")
		c := string.rep("x", 0)
	`)
	if interp.GetGlobal("a").Str() != "ababab" {
		t.Errorf("expected ababab, got %s", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "ab,ab,ab" {
		t.Errorf("expected ab,ab,ab, got %s", interp.GetGlobal("b").Str())
	}
	if interp.GetGlobal("c").Str() != "" {
		t.Errorf("expected empty string, got %s", interp.GetGlobal("c").Str())
	}
}

func TestStringReverse(t *testing.T) {
	interp := runProgram(t, `result := string.reverse("hello")`)
	if interp.GetGlobal("result").Str() != "olleh" {
		t.Errorf("expected olleh, got %s", interp.GetGlobal("result").Str())
	}
}

func TestStringByte_Char(t *testing.T) {
	interp := runProgram(t, `
		b := string.byte("A")
		c := string.char(65, 66, 67)
	`)
	if interp.GetGlobal("b").Int() != 65 {
		t.Errorf("expected 65, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Str() != "ABC" {
		t.Errorf("expected ABC, got %s", interp.GetGlobal("c").Str())
	}
}

func TestStringFind_plain(t *testing.T) {
	interp := runProgram(t, `
		s, e := string.find("hello world", "world", 1, true)
	`)
	s := interp.GetGlobal("s")
	e := interp.GetGlobal("e")
	if s.Int() != 7 {
		t.Errorf("expected start=7, got %v", s)
	}
	if e.Int() != 11 {
		t.Errorf("expected end=11, got %v", e)
	}
}

func TestStringFind_not_found(t *testing.T) {
	interp := runProgram(t, `
		s := string.find("hello", "xyz", 1, true)
	`)
	if !interp.GetGlobal("s").IsNil() {
		t.Errorf("expected nil for not found, got %v", interp.GetGlobal("s"))
	}
}

func TestStringFind_pattern(t *testing.T) {
	interp := runProgram(t, `
		s, e := string.find("hello123", "%d+")
	`)
	s := interp.GetGlobal("s")
	e := interp.GetGlobal("e")
	if s.Int() != 6 {
		t.Errorf("expected start=6, got %v", s)
	}
	if e.Int() != 8 {
		t.Errorf("expected end=8, got %v", e)
	}
}

func TestStringFormat_basic(t *testing.T) {
	interp := runProgram(t, `
		result := string.format("hello %s, you are %d", "world", 42)
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "hello world, you are 42" {
		t.Errorf("expected 'hello world, you are 42', got '%s'", v.Str())
	}
}

func TestStringFormat_types(t *testing.T) {
	interp := runProgram(t, `
		a := string.format("%d", 42)
		b := string.format("%f", 3.14)
		c := string.format("%x", 255)
		d := string.format("%05d", 42)
		e := string.format("%%")
	`)
	if interp.GetGlobal("a").Str() != "42" {
		t.Errorf("%%d: expected 42, got %s", interp.GetGlobal("a").Str())
	}
	if !strings.HasPrefix(interp.GetGlobal("b").Str(), "3.14") {
		t.Errorf("%%f: expected 3.14..., got %s", interp.GetGlobal("b").Str())
	}
	if interp.GetGlobal("c").Str() != "ff" {
		t.Errorf("%%x: expected ff, got %s", interp.GetGlobal("c").Str())
	}
	if interp.GetGlobal("d").Str() != "00042" {
		t.Errorf("%%05d: expected 00042, got %s", interp.GetGlobal("d").Str())
	}
	if interp.GetGlobal("e").Str() != "%" {
		t.Errorf("%%%% expected %%, got %s", interp.GetGlobal("e").Str())
	}
}

func TestStringGsub(t *testing.T) {
	interp := runProgram(t, `
		result, count := string.gsub("hello world", "o", "0")
	`)
	if interp.GetGlobal("result").Str() != "hell0 w0rld" {
		t.Errorf("expected hell0 w0rld, got %s", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected count=2, got %v", interp.GetGlobal("count"))
	}
}

func TestStringGsub_limit(t *testing.T) {
	interp := runProgram(t, `
		result, count := string.gsub("aaa", "a", "b", 2)
	`)
	if interp.GetGlobal("result").Str() != "bba" {
		t.Errorf("expected bba, got %s", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("count").Int() != 2 {
		t.Errorf("expected count=2, got %v", interp.GetGlobal("count"))
	}
}

func TestStringGmatch(t *testing.T) {
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
		t.Errorf("expected 'hello', got '%s'", tbl.RawGet(IntValue(1)).Str())
	}
	if tbl.RawGet(IntValue(2)).Str() != "world" {
		t.Errorf("expected 'world', got '%s'", tbl.RawGet(IntValue(2)).Str())
	}
	if tbl.RawGet(IntValue(3)).Str() != "foo" {
		t.Errorf("expected 'foo', got '%s'", tbl.RawGet(IntValue(3)).Str())
	}
}

func TestStringMatch(t *testing.T) {
	interp := runProgram(t, `
		result := string.match("hello123world", "%d+")
	`)
	if interp.GetGlobal("result").Str() != "123" {
		t.Errorf("expected '123', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestStringMatch_captures(t *testing.T) {
	interp := runProgram(t, `
		y, m, d := string.match("2026-03-10", "(%d+)-(%d+)-(%d+)")
	`)
	if interp.GetGlobal("y").Str() != "2026" {
		t.Errorf("expected '2026', got '%s'", interp.GetGlobal("y").Str())
	}
	if interp.GetGlobal("m").Str() != "03" {
		t.Errorf("expected '03', got '%s'", interp.GetGlobal("m").Str())
	}
	if interp.GetGlobal("d").Str() != "10" {
		t.Errorf("expected '10', got '%s'", interp.GetGlobal("d").Str())
	}
}

func TestStringSplit(t *testing.T) {
	interp := runProgram(t, `
		parts := string.split("a,b,c", ",")
	`)
	tbl := interp.GetGlobal("parts").Table()
	if tbl.Length() != 3 {
		t.Errorf("expected 3 parts, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected 'a', got '%s'", tbl.RawGet(IntValue(1)).Str())
	}
}

func TestStringMethodSyntax(t *testing.T) {
	interp := runProgram(t, `
		result := ("hello"):upper()
	`)
	if interp.GetGlobal("result").Str() != "HELLO" {
		t.Errorf("expected HELLO, got %s", interp.GetGlobal("result").Str())
	}
}

// ==================================================================
// Table library tests
// ==================================================================

func TestTableInsert(t *testing.T) {
	interp := runProgram(t, `
		t := {1, 2, 3}
		table.insert(t, 4)
		table.insert(t, 2, 10)
	`)
	tbl := interp.GetGlobal("t").Table()
	if tbl.Length() != 5 {
		t.Errorf("expected length 5, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Int() != 1 {
		t.Errorf("expected t[1]=1")
	}
	if tbl.RawGet(IntValue(2)).Int() != 10 {
		t.Errorf("expected t[2]=10, got %v", tbl.RawGet(IntValue(2)))
	}
	if tbl.RawGet(IntValue(3)).Int() != 2 {
		t.Errorf("expected t[3]=2, got %v", tbl.RawGet(IntValue(3)))
	}
	if tbl.RawGet(IntValue(5)).Int() != 4 {
		t.Errorf("expected t[5]=4, got %v", tbl.RawGet(IntValue(5)))
	}
}

func TestTableRemove(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30, 40}
		removed := table.remove(t, 2)
	`)
	tbl := interp.GetGlobal("t").Table()
	removed := interp.GetGlobal("removed")
	if removed.Int() != 20 {
		t.Errorf("expected removed=20, got %v", removed)
	}
	if tbl.Length() != 3 {
		t.Errorf("expected length 3, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(2)).Int() != 30 {
		t.Errorf("expected t[2]=30, got %v", tbl.RawGet(IntValue(2)))
	}
}

func TestTableRemove_last(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		removed := table.remove(t)
	`)
	removed := interp.GetGlobal("removed")
	if removed.Int() != 30 {
		t.Errorf("expected removed=30, got %v", removed)
	}
	tbl := interp.GetGlobal("t").Table()
	if tbl.Length() != 2 {
		t.Errorf("expected length 2, got %d", tbl.Length())
	}
}

func TestTableConcat(t *testing.T) {
	interp := runProgram(t, `
		t := {"hello", "world", "foo"}
		a := table.concat(t, ", ")
		b := table.concat(t, "-", 1, 2)
	`)
	if interp.GetGlobal("a").Str() != "hello, world, foo" {
		t.Errorf("expected 'hello, world, foo', got '%s'", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "hello-world" {
		t.Errorf("expected 'hello-world', got '%s'", interp.GetGlobal("b").Str())
	}
}

func TestTableSort(t *testing.T) {
	interp := runProgram(t, `
		t := {3, 1, 4, 1, 5, 9}
		table.sort(t)
	`)
	tbl := interp.GetGlobal("t").Table()
	expected := []int64{1, 1, 3, 4, 5, 9}
	for i, exp := range expected {
		v := tbl.RawGet(IntValue(int64(i + 1)))
		if v.Int() != exp {
			t.Errorf("t[%d] = %v, expected %d", i+1, v, exp)
		}
	}
}

func TestTableSort_custom(t *testing.T) {
	interp := runProgram(t, `
		t := {3, 1, 4, 1, 5}
		table.sort(t, func(a, b) { return a > b })
	`)
	tbl := interp.GetGlobal("t").Table()
	expected := []int64{5, 4, 3, 1, 1}
	for i, exp := range expected {
		v := tbl.RawGet(IntValue(int64(i + 1)))
		if v.Int() != exp {
			t.Errorf("t[%d] = %v, expected %d", i+1, v, exp)
		}
	}
}

func TestTableUnpack(t *testing.T) {
	interp := runProgram(t, `
		a, b, c := table.unpack({10, 20, 30})
	`)
	if interp.GetGlobal("a").Int() != 10 {
		t.Errorf("expected a=10")
	}
	if interp.GetGlobal("b").Int() != 20 {
		t.Errorf("expected b=20")
	}
	if interp.GetGlobal("c").Int() != 30 {
		t.Errorf("expected c=30")
	}
}

func TestTablePack(t *testing.T) {
	interp := runProgram(t, `
		t := table.pack(10, 20, 30)
		n := t.n
	`)
	tbl := interp.GetGlobal("t").Table()
	if tbl.RawGet(IntValue(1)).Int() != 10 {
		t.Errorf("expected t[1]=10")
	}
	if interp.GetGlobal("n").Int() != 3 {
		t.Errorf("expected n=3, got %v", interp.GetGlobal("n"))
	}
}

func TestTableMove(t *testing.T) {
	interp := runProgram(t, `
		t := {1, 2, 3, 4, 5}
		table.move(t, 3, 5, 1)
	`)
	tbl := interp.GetGlobal("t").Table()
	if tbl.RawGet(IntValue(1)).Int() != 3 {
		t.Errorf("expected t[1]=3, got %v", tbl.RawGet(IntValue(1)))
	}
	if tbl.RawGet(IntValue(2)).Int() != 4 {
		t.Errorf("expected t[2]=4, got %v", tbl.RawGet(IntValue(2)))
	}
}

// ==================================================================
// Math library tests
// ==================================================================

func TestMathBasic(t *testing.T) {
	interp := runProgram(t, `
		a := math.abs(-5)
		b := math.abs(3.14)
	`)
	if interp.GetGlobal("a").Int() != 5 {
		t.Errorf("expected abs(-5)=5, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Number() != 3.14 {
		t.Errorf("expected abs(3.14)=3.14, got %v", interp.GetGlobal("b"))
	}
}

func TestMathFloorCeil(t *testing.T) {
	interp := runProgram(t, `
		f := math.floor(3.7)
		c := math.ceil(3.2)
	`)
	if interp.GetGlobal("f").Int() != 3 {
		t.Errorf("expected floor(3.7)=3, got %v", interp.GetGlobal("f"))
	}
	if interp.GetGlobal("c").Int() != 4 {
		t.Errorf("expected ceil(3.2)=4, got %v", interp.GetGlobal("c"))
	}
}

func TestMathSqrt(t *testing.T) {
	interp := runProgram(t, `result := math.sqrt(16)`)
	v := interp.GetGlobal("result")
	if v.Number() != 4.0 {
		t.Errorf("expected sqrt(16)=4, got %v", v)
	}
}

func TestMathTrig(t *testing.T) {
	interp := runProgram(t, `
		s := math.sin(0)
		c := math.cos(0)
	`)
	if interp.GetGlobal("s").Number() != 0 {
		t.Errorf("expected sin(0)=0, got %v", interp.GetGlobal("s"))
	}
	if interp.GetGlobal("c").Number() != 1 {
		t.Errorf("expected cos(0)=1, got %v", interp.GetGlobal("c"))
	}
}

func TestMathMinMax(t *testing.T) {
	interp := runProgram(t, `
		mx := math.max(1, 5, 3)
		mn := math.min(1, 5, 3)
	`)
	if interp.GetGlobal("mx").Int() != 5 {
		t.Errorf("expected max=5, got %v", interp.GetGlobal("mx"))
	}
	if interp.GetGlobal("mn").Int() != 1 {
		t.Errorf("expected min=1, got %v", interp.GetGlobal("mn"))
	}
}

func TestMathRandom(t *testing.T) {
	interp := runProgram(t, `
		math.randomseed(42)
		a := math.random()
		b := math.random(10)
		c := math.random(5, 10)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")

	if !a.IsFloat() || a.Number() < 0 || a.Number() >= 1 {
		t.Errorf("math.random() should return [0, 1), got %v", a)
	}
	if b.Int() < 1 || b.Int() > 10 {
		t.Errorf("math.random(10) should return [1, 10], got %v", b)
	}
	if c.Int() < 5 || c.Int() > 10 {
		t.Errorf("math.random(5, 10) should return [5, 10], got %v", c)
	}
}

func TestMathType(t *testing.T) {
	interp := runProgram(t, `
		a := math.type(42)
		b := math.type(3.14)
		c := math.type("hello")
	`)
	if interp.GetGlobal("a").Str() != "integer" {
		t.Errorf("expected 'integer', got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Str() != "float" {
		t.Errorf("expected 'float', got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Truthy() {
		t.Errorf("expected false for non-number, got %v", interp.GetGlobal("c"))
	}
}

func TestMathConstants(t *testing.T) {
	interp := runProgram(t, `
		pi := math.pi
		huge := math.huge
	`)
	pi := interp.GetGlobal("pi")
	huge := interp.GetGlobal("huge")
	if math.Abs(pi.Number()-math.Pi) > 1e-10 {
		t.Errorf("expected pi=%.15f, got %.15f", math.Pi, pi.Number())
	}
	if !math.IsInf(huge.Number(), 1) {
		t.Errorf("expected +Inf, got %v", huge)
	}
}

func TestMathExp_Log(t *testing.T) {
	interp := runProgram(t, `
		e := math.exp(1)
		l := math.log(math.exp(1))
		l10 := math.log(100, 10)
	`)
	e := interp.GetGlobal("e").Number()
	if math.Abs(e-math.E) > 1e-10 {
		t.Errorf("expected math.exp(1)=e, got %v", e)
	}
	l := interp.GetGlobal("l").Number()
	if math.Abs(l-1.0) > 1e-10 {
		t.Errorf("expected log(e)=1, got %v", l)
	}
	l10 := interp.GetGlobal("l10").Number()
	if math.Abs(l10-2.0) > 1e-10 {
		t.Errorf("expected log(100,10)=2, got %v", l10)
	}
}

func TestMathModf(t *testing.T) {
	interp := runProgram(t, `
		i, f := math.modf(3.75)
	`)
	i := interp.GetGlobal("i").Number()
	f := interp.GetGlobal("f").Number()
	if i != 3.0 {
		t.Errorf("expected int part=3, got %v", i)
	}
	if math.Abs(f-0.75) > 1e-10 {
		t.Errorf("expected frac part=0.75, got %v", f)
	}
}

// ==================================================================
// Pairs/ipairs tests
// ==================================================================

func TestIpairs(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20, 30}
		sum := 0
		for i, v := range ipairs(t) {
			sum = sum + v
		}
	`)
	sum := interp.GetGlobal("sum")
	if sum.Int() != 60 {
		t.Errorf("expected sum=60, got %v", sum)
	}
}

func TestPairs(t *testing.T) {
	interp := runProgram(t, `
		t := {a: 1, b: 2, c: 3}
		sum := 0
		for k, v := range pairs(t) {
			sum = sum + v
		}
	`)
	sum := interp.GetGlobal("sum")
	if sum.Int() != 6 {
		t.Errorf("expected sum=6, got %v", sum)
	}
}

func TestSelect(t *testing.T) {
	interp := runProgram(t, `
		count := select("#", 10, 20, 30)
		first := select(1, 10, 20, 30)
		second := select(2, 10, 20, 30)
	`)
	if interp.GetGlobal("count").Int() != 3 {
		t.Errorf("expected count=3, got %v", interp.GetGlobal("count"))
	}
	if interp.GetGlobal("first").Int() != 10 {
		t.Errorf("expected first=10, got %v", interp.GetGlobal("first"))
	}
	if interp.GetGlobal("second").Int() != 20 {
		t.Errorf("expected second=20, got %v", interp.GetGlobal("second"))
	}
}

func TestUnpack(t *testing.T) {
	interp := runProgram(t, `
		a, b, c := unpack({10, 20, 30})
	`)
	if interp.GetGlobal("a").Int() != 10 {
		t.Errorf("expected a=10")
	}
	if interp.GetGlobal("b").Int() != 20 {
		t.Errorf("expected b=20")
	}
	if interp.GetGlobal("c").Int() != 30 {
		t.Errorf("expected c=30")
	}
}

func TestNext(t *testing.T) {
	interp := runProgram(t, `
		t := {10, 20}
		k1, v1 := next(t, nil)
		k2, v2 := next(t, k1)
	`)
	k1 := interp.GetGlobal("k1")
	v1 := interp.GetGlobal("v1")
	if k1.Int() != 1 || v1.Int() != 10 {
		t.Errorf("expected k1=1,v1=10, got k1=%v,v1=%v", k1, v1)
	}
	k2 := interp.GetGlobal("k2")
	v2 := interp.GetGlobal("v2")
	if k2.Int() != 2 || v2.Int() != 20 {
		t.Errorf("expected k2=2,v2=20, got k2=%v,v2=%v", k2, v2)
	}
}

// ==================================================================
// OS library basic tests
// ==================================================================

func TestOsTime(t *testing.T) {
	interp := runProgram(t, `result := os.time()`)
	v := interp.GetGlobal("result")
	if !v.IsInt() || v.Int() <= 0 {
		t.Errorf("os.time() should return positive integer, got %v", v)
	}
}

func TestOsClock(t *testing.T) {
	interp := runProgram(t, `result := os.clock()`)
	v := interp.GetGlobal("result")
	if !v.IsFloat() || v.Number() < 0 {
		t.Errorf("os.clock() should return non-negative float, got %v", v)
	}
}

func TestOsGetenv(t *testing.T) {
	interp := runProgram(t, `result := os.getenv("PATH")`)
	v := interp.GetGlobal("result")
	if v.IsNil() {
		t.Errorf("os.getenv('PATH') should not be nil")
	}
}

func TestOsGetenv_missing(t *testing.T) {
	interp := runProgram(t, `result := os.getenv("__GSCRIPT_NONEXISTENT_VAR__")`)
	v := interp.GetGlobal("result")
	if !v.IsNil() {
		t.Errorf("os.getenv for missing var should be nil, got %v", v)
	}
}

// ==================================================================
// Error nesting: pcall inside pcall
// ==================================================================

func TestPcall_nested(t *testing.T) {
	interp := runProgram(t, `
		func inner() {
			error("inner error")
		}
		func outer() {
			ok, msg := pcall(inner)
			return ok, msg
		}
		ok, msg := outer()
	`)
	ok := interp.GetGlobal("ok")
	msg := interp.GetGlobal("msg")
	if ok.Truthy() {
		t.Errorf("expected false from pcall(inner)")
	}
	if msg.Str() != "inner error" {
		t.Errorf("expected 'inner error', got %v", msg)
	}
}

func TestPcall_with_args(t *testing.T) {
	interp := runProgram(t, `
		func div(a, b) {
			if b == 0 {
				error("division by zero")
			}
			return a / b
		}
		ok1, r1 := pcall(div, 10, 2)
		ok2, r2 := pcall(div, 10, 0)
	`)
	if !interp.GetGlobal("ok1").Bool() {
		t.Errorf("pcall(div, 10, 2) should succeed")
	}
	if interp.GetGlobal("r1").Int() != 5 {
		t.Errorf("expected 5, got %v", interp.GetGlobal("r1"))
	}
	if interp.GetGlobal("ok2").Truthy() {
		t.Errorf("pcall(div, 10, 0) should fail")
	}
	if interp.GetGlobal("r2").Str() != "division by zero" {
		t.Errorf("expected 'division by zero', got %v", interp.GetGlobal("r2"))
	}
}

// ==================================================================
// HTTP library tests
// ==================================================================

func TestHTTPLibRegistered(t *testing.T) {
	interp := runProgram(t, `
		result := type(http)
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "table" {
		t.Errorf("expected http to be 'table', got %s", v.Str())
	}
}

func TestHTTPLibFunctions(t *testing.T) {
	interp := runProgram(t, `
		a := type(http.listen)
		b := type(http.get)
		c := type(http.newRouter)
	`)
	if interp.GetGlobal("a").Str() != "function" {
		t.Errorf("expected http.listen to be 'function', got %s", interp.GetGlobal("a").Str())
	}
	if interp.GetGlobal("b").Str() != "function" {
		t.Errorf("expected http.get to be 'function', got %s", interp.GetGlobal("b").Str())
	}
	if interp.GetGlobal("c").Str() != "function" {
		t.Errorf("expected http.newRouter to be 'function', got %s", interp.GetGlobal("c").Str())
	}
}

func TestHTTPNewRouter(t *testing.T) {
	interp := runProgram(t, `
		router := http.newRouter()
		result := type(router)
		has_get := type(router.get)
		has_post := type(router.post)
		has_any := type(router.any)
		has_listen := type(router.listen)
	`)
	if interp.GetGlobal("result").Str() != "table" {
		t.Errorf("expected router to be 'table', got %s", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("has_get").Str() != "function" {
		t.Errorf("expected router.get to be 'function', got %s", interp.GetGlobal("has_get").Str())
	}
	if interp.GetGlobal("has_post").Str() != "function" {
		t.Errorf("expected router.post to be 'function', got %s", interp.GetGlobal("has_post").Str())
	}
	if interp.GetGlobal("has_any").Str() != "function" {
		t.Errorf("expected router.any to be 'function', got %s", interp.GetGlobal("has_any").Str())
	}
	if interp.GetGlobal("has_listen").Str() != "function" {
		t.Errorf("expected router.listen to be 'function', got %s", interp.GetGlobal("has_listen").Str())
	}
}

func TestHTTPRouterChaining(t *testing.T) {
	// Verify that router.get/post/any return the router for chaining
	interp := runProgram(t, `
		router := http.newRouter()
		r2 := router.get("/test", func(req, res) {})
		same := r2 == router
	`)
	if !interp.GetGlobal("same").Truthy() {
		t.Errorf("expected router.get to return the same router for chaining")
	}
}

func TestGoToGScript(t *testing.T) {
	// Test the goToGScript conversion function
	v := goToGScript(nil)
	if !v.IsNil() {
		t.Errorf("expected nil, got %v", v)
	}

	v = goToGScript(true)
	if !v.IsBool() || !v.Bool() {
		t.Errorf("expected true, got %v", v)
	}

	v = goToGScript(3.14)
	if !v.IsFloat() || v.Number() != 3.14 {
		t.Errorf("expected 3.14, got %v", v)
	}

	v = goToGScript("hello")
	if !v.IsString() || v.Str() != "hello" {
		t.Errorf("expected 'hello', got %v", v)
	}

	v = goToGScript([]interface{}{"a", "b"})
	if !v.IsTable() {
		t.Errorf("expected table, got %v", v.TypeName())
	}
	tbl := v.Table()
	if tbl.Length() != 2 {
		t.Errorf("expected length 2, got %d", tbl.Length())
	}
	if tbl.RawGet(IntValue(1)).Str() != "a" {
		t.Errorf("expected 'a' at index 1, got %v", tbl.RawGet(IntValue(1)))
	}

	v = goToGScript(map[string]interface{}{"key": "val"})
	if !v.IsTable() {
		t.Errorf("expected table, got %v", v.TypeName())
	}
	tbl = v.Table()
	if tbl.RawGet(StringValue("key")).Str() != "val" {
		t.Errorf("expected 'val' for key 'key', got %v", tbl.RawGet(StringValue("key")))
	}
}

func TestGScriptToGo(t *testing.T) {
	// Test the gscriptToGo conversion function
	if gscriptToGo(NilValue()) != nil {
		t.Errorf("expected nil")
	}

	if gscriptToGo(BoolValue(true)) != true {
		t.Errorf("expected true")
	}

	if gscriptToGo(IntValue(42)) != int64(42) {
		t.Errorf("expected 42")
	}

	if gscriptToGo(FloatValue(3.14)) != 3.14 {
		t.Errorf("expected 3.14")
	}

	if gscriptToGo(StringValue("hello")) != "hello" {
		t.Errorf("expected 'hello'")
	}

	// Array-like table
	tbl := NewTable()
	tbl.RawSet(IntValue(1), StringValue("a"))
	tbl.RawSet(IntValue(2), StringValue("b"))
	result := gscriptToGo(TableValue(tbl))
	arr, ok := result.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", result)
	}
	if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Errorf("expected [a, b], got %v", arr)
	}

	// Hash-like table
	tbl2 := NewTable()
	tbl2.RawSet(StringValue("key"), StringValue("val"))
	result2 := gscriptToGo(TableValue(tbl2))
	m, ok := result2.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result2)
	}
	if m["key"] != "val" {
		t.Errorf("expected key=val, got %v", m)
	}
}
