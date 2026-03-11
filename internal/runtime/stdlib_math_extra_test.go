package runtime

import (
	"math"
	"testing"
)

func TestMathClamp(t *testing.T) {
	interp := runProgram(t, `
		a := math.clamp(5, 1, 10)
		b := math.clamp(-5, 0, 10)
		c := math.clamp(15, 0, 10)
	`)
	if interp.GetGlobal("a").Int() != 5 {
		t.Errorf("expected 5, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 0 {
		t.Errorf("expected 0, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 10 {
		t.Errorf("expected 10, got %v", interp.GetGlobal("c"))
	}
}

func TestMathClamp_float(t *testing.T) {
	interp := runProgram(t, `
		a := math.clamp(1.5, 0.0, 1.0)
	`)
	v := interp.GetGlobal("a").Number()
	if v != 1.0 {
		t.Errorf("expected 1.0, got %v", v)
	}
}

func TestMathLerp(t *testing.T) {
	interp := runProgram(t, `
		a := math.lerp(0, 10, 0.5)
		b := math.lerp(0, 10, 0.0)
		c := math.lerp(0, 10, 1.0)
	`)
	if interp.GetGlobal("a").Number() != 5.0 {
		t.Errorf("expected 5.0, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Number() != 0.0 {
		t.Errorf("expected 0.0, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Number() != 10.0 {
		t.Errorf("expected 10.0, got %v", interp.GetGlobal("c"))
	}
}

func TestMathSign(t *testing.T) {
	interp := runProgram(t, `
		a := math.sign(5)
		b := math.sign(-3.14)
		c := math.sign(0)
	`)
	if interp.GetGlobal("a").Int() != 1 {
		t.Errorf("expected 1, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != -1 {
		t.Errorf("expected -1, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 0 {
		t.Errorf("expected 0, got %v", interp.GetGlobal("c"))
	}
}

func TestMathRound(t *testing.T) {
	interp := runProgram(t, `
		a := math.round(3.7)
		b := math.round(3.3)
		c := math.round(3.14159, 2)
	`)
	if interp.GetGlobal("a").Int() != 4 {
		t.Errorf("expected 4, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != 3 {
		t.Errorf("expected 3, got %v", interp.GetGlobal("b"))
	}
	v := interp.GetGlobal("c").Number()
	if math.Abs(v-3.14) > 1e-10 {
		t.Errorf("expected 3.14, got %v", v)
	}
}

func TestMathTrunc(t *testing.T) {
	interp := runProgram(t, `
		a := math.trunc(3.7)
		b := math.trunc(-3.7)
		c := math.trunc(5)
	`)
	if interp.GetGlobal("a").Int() != 3 {
		t.Errorf("expected 3, got %v", interp.GetGlobal("a"))
	}
	if interp.GetGlobal("b").Int() != -3 {
		t.Errorf("expected -3, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 5 {
		t.Errorf("expected 5, got %v", interp.GetGlobal("c"))
	}
}

func TestMathHypot(t *testing.T) {
	interp := runProgram(t, `
		a := math.hypot(3, 4)
	`)
	v := interp.GetGlobal("a").Number()
	if math.Abs(v-5.0) > 1e-10 {
		t.Errorf("expected 5.0, got %v", v)
	}
}

func TestMathIsNaN(t *testing.T) {
	// Test isnan using math.huge - math.huge which produces NaN
	interp := runProgram(t, `
		nan := math.huge - math.huge
		a := math.isnan(nan)
		b := math.isnan(42)
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for NaN")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for 42")
	}
}

func TestMathIsInf(t *testing.T) {
	interp := runProgram(t, `
		a := math.isinf(math.huge)
		b := math.isinf(42)
	`)
	if !interp.GetGlobal("a").Bool() {
		t.Errorf("expected true for math.huge")
	}
	if interp.GetGlobal("b").Bool() {
		t.Errorf("expected false for 42")
	}
}

func TestMathTointeger_extra(t *testing.T) {
	interp := runProgram(t, `
		a := math.tointeger(5.0)
		b := math.tointeger(5.5)
		c := math.tointeger(42)
	`)
	if interp.GetGlobal("a").Int() != 5 {
		t.Errorf("expected 5, got %v", interp.GetGlobal("a"))
	}
	if !interp.GetGlobal("b").IsNil() {
		t.Errorf("expected nil for 5.5, got %v", interp.GetGlobal("b"))
	}
	if interp.GetGlobal("c").Int() != 42 {
		t.Errorf("expected 42, got %v", interp.GetGlobal("c"))
	}
}
