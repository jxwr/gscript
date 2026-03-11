package runtime

import (
	"testing"
)

func TestBytesNew(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.new()
		buf.write("hello")
		buf.write(" world")
		result := buf.toString()
		length := buf.len()
	`)

	if interp.GetGlobal("result").Str() != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("length").Int() != 11 {
		t.Errorf("expected length=11, got %v", interp.GetGlobal("length"))
	}
}

func TestBytesFromString(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.fromString("hello")
		result := buf.toString()
		length := buf.len()
	`)

	if interp.GetGlobal("result").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("length").Int() != 5 {
		t.Errorf("expected length=5, got %v", interp.GetGlobal("length"))
	}
}

func TestBytesWriteByte(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.new()
		buf.writeByte(65)
		buf.writeByte(66)
		buf.writeByte(67)
		result := buf.toString()
	`)

	if interp.GetGlobal("result").Str() != "ABC" {
		t.Errorf("expected 'ABC', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesToHex(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		result := bytes.toHex("hello")
	`)

	if interp.GetGlobal("result").Str() != "68656c6c6f" {
		t.Errorf("expected '68656c6c6f', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesFromHex(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.fromHex("68656c6c6f")
		result := buf.toString()
	`)

	if interp.GetGlobal("result").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesBufferToHex(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.fromString("AB")
		result := buf.toHex()
	`)

	if interp.GetGlobal("result").Str() != "4142" {
		t.Errorf("expected '4142', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesReset(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.new()
		buf.write("hello")
		buf.reset()
		result := buf.toString()
		length := buf.len()
	`)

	if interp.GetGlobal("result").Str() != "" {
		t.Errorf("expected empty string after reset, got '%s'", interp.GetGlobal("result").Str())
	}
	if interp.GetGlobal("length").Int() != 0 {
		t.Errorf("expected length=0 after reset, got %v", interp.GetGlobal("length"))
	}
}

func TestBytesBufferBytes(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.fromString("AB")
		result := buf.bytes()
	`)

	v := interp.GetGlobal("result").Table()
	if v.Length() != 2 {
		t.Errorf("expected 2 bytes, got %d", v.Length())
	}
	if v.RawGet(IntValue(1)).Int() != 65 {
		t.Errorf("expected byte[1]=65, got %v", v.RawGet(IntValue(1)))
	}
	if v.RawGet(IntValue(2)).Int() != 66 {
		t.Errorf("expected byte[2]=66, got %v", v.RawGet(IntValue(2)))
	}
}

func TestBytesReadByte(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.fromString("ABC")
		a := buf.readByte(1)
		b := buf.readByte(2)
		c := buf.readByte(4)
	`)

	if interp.GetGlobal("a").Int() != 65 {
		t.Errorf("expected 65, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 66 {
		t.Errorf("expected 66, got %v", interp.GetGlobal("b"))
	}
	if !interp.GetGlobal("c").IsNil() {
		t.Errorf("expected nil for out of range, got %v", interp.GetGlobal("c"))
	}
}

func TestBytesReadString(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.fromString("hello world")
		result := buf.readString(1, 5)
	`)

	if interp.GetGlobal("result").Str() != "hello" {
		t.Errorf("expected 'hello', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesXor(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		result := bytes.xor("ab", "cd")
	`)

	v := interp.GetGlobal("result").Str()
	// 'a' ^ 'c' = 0x61 ^ 0x63 = 0x02
	// 'b' ^ 'd' = 0x62 ^ 0x64 = 0x06
	if v[0] != 2 || v[1] != 6 {
		t.Errorf("XOR result incorrect: got %v %v", v[0], v[1])
	}
}

func TestBytesCompare(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		a := bytes.compare("abc", "abd")
		b := bytes.compare("abc", "abc")
		c := bytes.compare("abd", "abc")
	`)

	if interp.GetGlobal("a").Int() != -1 {
		t.Errorf("expected -1, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 0 {
		t.Errorf("expected 0, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 1 {
		t.Errorf("expected 1, got %v", interp.GetGlobal("c"))
	}
}

func TestBytesRepeat(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		result := bytes.repeat("ab", 3)
	`)

	if interp.GetGlobal("result").Str() != "ababab" {
		t.Errorf("expected 'ababab', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesConcat(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		result := bytes.concat("hello", " ", "world")
	`)

	if interp.GetGlobal("result").Str() != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", interp.GetGlobal("result").Str())
	}
}

func TestBytesWriteInt(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.new()
		buf.writeInt8(42)
		length := buf.len()
		b := buf.readByte(1)
	`)

	if interp.GetGlobal("length").Int() != 1 {
		t.Errorf("expected length=1, got %v", interp.GetGlobal("length"))
	}
	if interp.GetGlobal("b").Int() != 42 {
		t.Errorf("expected byte=42, got %v", interp.GetGlobal("b"))
	}
}

func TestBytesWriteInt16(t *testing.T) {
	interp := New()
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	execOnInterp(t, interp, `
		buf := bytes.new()
		buf.writeInt16(256)
		length := buf.len()
		lo := buf.readByte(1)
		hi := buf.readByte(2)
	`)

	if interp.GetGlobal("length").Int() != 2 {
		t.Errorf("expected length=2, got %v", interp.GetGlobal("length"))
	}
	// 256 in little-endian: lo=0, hi=1
	if interp.GetGlobal("lo").Int() != 0 {
		t.Errorf("expected lo=0, got %v", interp.GetGlobal("lo"))
	}
	if interp.GetGlobal("hi").Int() != 1 {
		t.Errorf("expected hi=1, got %v", interp.GetGlobal("hi"))
	}
}
