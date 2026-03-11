package runtime

import (
	"testing"
)

// ==================================================================
// Bit32 library tests
// ==================================================================
// Note: GScript does not support hex literals, so we use decimal values.

func TestBit32Band(t *testing.T) {
	// 0xFF=255, 0x0F=15
	interp := runProgram(t, `
		result := bit32.band(255, 15)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

func TestBit32Band_multiple(t *testing.T) {
	// 255 & 15 & 7 = 7
	interp := runProgram(t, `
		result := bit32.band(255, 15, 7)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 7 {
		t.Errorf("expected 7, got %v", v)
	}
}

func TestBit32Bor(t *testing.T) {
	// 0xF0=240, 0x0F=15, 240|15=255
	interp := runProgram(t, `
		result := bit32.bor(240, 15)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 255 {
		t.Errorf("expected 255, got %v", v)
	}
}

func TestBit32Bxor(t *testing.T) {
	// 255 ^ 15 = 240
	interp := runProgram(t, `
		result := bit32.bxor(255, 15)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 240 {
		t.Errorf("expected 240, got %v", v)
	}
}

func TestBit32Bnot(t *testing.T) {
	interp := runProgram(t, `
		result := bit32.bnot(0)
	`)
	v := interp.GetGlobal("result")
	// ^uint32(0) = 4294967295
	if v.Int() != 4294967295 {
		t.Errorf("expected 4294967295, got %v", v)
	}
}

func TestBit32Bnot_value(t *testing.T) {
	// ^uint32(255) = 4294967040 (0xFFFFFF00)
	interp := runProgram(t, `
		result := bit32.bnot(255)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 4294967040 {
		t.Errorf("expected 4294967040, got %v", v)
	}
}

func TestBit32Lshift(t *testing.T) {
	interp := runProgram(t, `
		result := bit32.lshift(1, 4)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 16 {
		t.Errorf("expected 16, got %v", v)
	}
}

func TestBit32Rshift(t *testing.T) {
	interp := runProgram(t, `
		result := bit32.rshift(16, 4)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 1 {
		t.Errorf("expected 1, got %v", v)
	}
}

func TestBit32Arshift(t *testing.T) {
	// 2147483648 = 0x80000000
	interp := runProgram(t, `
		result := bit32.arshift(2147483648, 4)
	`)
	v := interp.GetGlobal("result")
	// int32(-2147483648) >> 4 = -134217728
	var expected int64 = -134217728
	if v.Int() != expected {
		t.Errorf("expected %d, got %d", expected, v.Int())
	}
}

func TestBit32Test(t *testing.T) {
	interp := runProgram(t, `
		a := bit32.test(10, 1)
		b := bit32.test(10, 2)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	// 10 = 0b1010, bit 1 is set, bit 2 is not set
	if !a.Bool() {
		t.Errorf("expected true for bit 1 of 10")
	}
	if b.Bool() {
		t.Errorf("expected false for bit 2 of 10")
	}
}

func TestBit32Set(t *testing.T) {
	interp := runProgram(t, `
		result := bit32.set(0, 3)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 8 {
		t.Errorf("expected 8, got %v", v)
	}
}

func TestBit32Clear(t *testing.T) {
	// clear bit 0 of 255 => 254
	interp := runProgram(t, `
		result := bit32.clear(255, 0)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 254 {
		t.Errorf("expected 254, got %v", v)
	}
}

func TestBit32Toggle(t *testing.T) {
	interp := runProgram(t, `
		a := bit32.toggle(0, 3)
		b := bit32.toggle(8, 3)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if a.Int() != 8 {
		t.Errorf("expected 8, got %v", a)
	}
	if b.Int() != 0 {
		t.Errorf("expected 0, got %v", b)
	}
}

func TestBit32Extract(t *testing.T) {
	// extract 4 bits starting at bit 8 from 65280 (0xFF00)
	interp := runProgram(t, `
		result := bit32.extract(65280, 8, 4)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 15 {
		t.Errorf("expected 15, got %v", v)
	}
}

func TestBit32Replace(t *testing.T) {
	// replace 4 bits at bit 8 in 65280 (0xFF00) with 10 (0xA) => 0xFA00 = 64000
	interp := runProgram(t, `
		result := bit32.replace(65280, 10, 8, 4)
	`)
	v := interp.GetGlobal("result")
	if v.Int() != 64000 {
		t.Errorf("expected 64000 (0xFA00), got %v (0x%X)", v, v.Int())
	}
}

func TestBit32Countbits(t *testing.T) {
	// 4294967295 = 0xFFFFFFFF
	interp := runProgram(t, `
		a := bit32.countbits(0)
		b := bit32.countbits(255)
		c := bit32.countbits(4294967295)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != 0 {
		t.Errorf("expected 0, got %v", a)
	}
	if b.Int() != 8 {
		t.Errorf("expected 8, got %v", b)
	}
	if c.Int() != 32 {
		t.Errorf("expected 32, got %v", c)
	}
}

func TestBit32Highbit(t *testing.T) {
	// 2147483648 = 0x80000000
	interp := runProgram(t, `
		a := bit32.highbit(0)
		b := bit32.highbit(1)
		c := bit32.highbit(2147483648)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	c := interp.GetGlobal("c")
	if a.Int() != -1 {
		t.Errorf("expected -1 for 0, got %v", a)
	}
	if b.Int() != 0 {
		t.Errorf("expected 0 for 1, got %v", b)
	}
	if c.Int() != 31 {
		t.Errorf("expected 31, got %v", c)
	}
}

func TestBit32ToHex(t *testing.T) {
	interp := runProgram(t, `
		a := bit32.toHex(255)
		b := bit32.toHex(255, 8)
	`)
	a := interp.GetGlobal("a")
	b := interp.GetGlobal("b")
	if a.Str() != "0xFF" {
		t.Errorf("expected '0xFF', got '%s'", a.Str())
	}
	if b.Str() != "0x000000FF" {
		t.Errorf("expected '0x000000FF', got '%s'", b.Str())
	}
}
