package runtime

import (
	"math"
	"testing"
)

// runWithColor creates an interpreter with both vec and color libs.
func runWithColor(t *testing.T, src string) *Interpreter {
	t.Helper()
	return runWithVec(t, src) // runWithVec already registers both vec and color
}

// ==================================================================
// Color constructor tests
// ==================================================================

func TestColorNew(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(0.5, 0.6, 0.7)
		rr := c.r
		rg := c.g
		rb := c.b
		ra := c.a
		rt := c._type
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.5, 1e-10) {
		t.Errorf("expected r=0.5, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0.6, 1e-10) {
		t.Errorf("expected g=0.6, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.7, 1e-10) {
		t.Errorf("expected b=0.7, got %v", interp.GetGlobal("rb"))
	}
	if interp.GetGlobal("ra").Number() != 1 {
		t.Errorf("expected a=1 (default), got %v", interp.GetGlobal("ra"))
	}
	if interp.GetGlobal("rt").Str() != "color" {
		t.Errorf("expected _type='color', got %v", interp.GetGlobal("rt"))
	}
}

func TestColorNewWithAlpha(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(0.1, 0.2, 0.3, 0.4)
		ra := c.a
	`)
	if !floatClose(interp.GetGlobal("ra").Number(), 0.4, 1e-10) {
		t.Errorf("expected a=0.4, got %v", interp.GetGlobal("ra"))
	}
}

func TestColorRGB(t *testing.T) {
	interp := runWithColor(t, `
		c := color.rgb(255, 128, 0)
		rr := c.r
		rg := c.g
		rb := c.b
		ra := c.a
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 128.0/255.0, 1e-3) {
		t.Errorf("expected g~0.502, got %v", interp.GetGlobal("rg"))
	}
	if interp.GetGlobal("rb").Number() != 0 {
		t.Errorf("expected b=0, got %v", interp.GetGlobal("rb"))
	}
	if interp.GetGlobal("ra").Number() != 1 {
		t.Errorf("expected a=1, got %v", interp.GetGlobal("ra"))
	}
}

func TestColorRGBA(t *testing.T) {
	interp := runWithColor(t, `
		c := color.rgba(255, 0, 0, 128)
		rr := c.r
		ra := c.a
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0")
	}
	if !floatClose(interp.GetGlobal("ra").Number(), 128.0/255.0, 1e-3) {
		t.Errorf("expected a~0.502, got %v", interp.GetGlobal("ra"))
	}
}

// ==================================================================
// Color fromHex / toHex
// ==================================================================

func TestColorFromHex6(t *testing.T) {
	interp := runWithColor(t, `
		c := color.fromHex("#FF0000")
		rr := c.r
		rg := c.g
		rb := c.b
		ra := c.a
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if interp.GetGlobal("rg").Number() != 0 {
		t.Errorf("expected g=0")
	}
	if interp.GetGlobal("rb").Number() != 0 {
		t.Errorf("expected b=0")
	}
	if interp.GetGlobal("ra").Number() != 1 {
		t.Errorf("expected a=1")
	}
}

func TestColorFromHex3(t *testing.T) {
	interp := runWithColor(t, `
		c := color.fromHex("#F00")
		rr := c.r
		rg := c.g
		rb := c.b
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if interp.GetGlobal("rg").Number() != 0 {
		t.Errorf("expected g=0")
	}
	if interp.GetGlobal("rb").Number() != 0 {
		t.Errorf("expected b=0")
	}
}

func TestColorFromHex8(t *testing.T) {
	interp := runWithColor(t, `
		c := color.fromHex("#FF000080")
		rr := c.r
		ra := c.a
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("ra").Number(), 128.0/255.0, 1e-3) {
		t.Errorf("expected a~0.502, got %v", interp.GetGlobal("ra"))
	}
}

func TestColorFromHexInvalid(t *testing.T) {
	interp := runWithColor(t, `
		c, err := color.fromHex("invalid")
	`)
	c := interp.GetGlobal("c")
	errMsg := interp.GetGlobal("err")
	if !c.IsNil() {
		t.Errorf("expected nil for invalid hex, got %v", c)
	}
	if !errMsg.IsString() || errMsg.Str() == "" {
		t.Errorf("expected error message, got %v", errMsg)
	}
}

func TestColorToHex(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		result := color.toHex(c)
	`)
	v := interp.GetGlobal("result")
	if v.Str() != "#FF0000" {
		t.Errorf("expected '#FF0000', got '%v'", v.Str())
	}
}

// ==================================================================
// Color HSV
// ==================================================================

func TestColorFromHSV(t *testing.T) {
	interp := runWithColor(t, `
		c := color.fromHSV(0, 1, 1)
		rr := c.r
		rg := c.g
		rb := c.b
	`)
	// HSV(0, 1, 1) = pure red
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0, 1e-10) {
		t.Errorf("expected g=0, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0, 1e-10) {
		t.Errorf("expected b=0, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorToHSV(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		h, s, v := color.toHSV(c)
	`)
	h := interp.GetGlobal("h").Number()
	s := interp.GetGlobal("s").Number()
	v := interp.GetGlobal("v").Number()
	if !floatClose(h, 0, 1e-10) {
		t.Errorf("expected h=0, got %v", h)
	}
	if !floatClose(s, 1, 1e-10) {
		t.Errorf("expected s=1, got %v", s)
	}
	if !floatClose(v, 1, 1e-10) {
		t.Errorf("expected v=1, got %v", v)
	}
}

func TestColorHSVRoundtrip(t *testing.T) {
	interp := runWithColor(t, `
		c1 := color.fromHSV(120, 0.5, 0.8)
		h, s, v := color.toHSV(c1)
	`)
	h := interp.GetGlobal("h").Number()
	s := interp.GetGlobal("s").Number()
	v := interp.GetGlobal("v").Number()
	if !floatClose(h, 120, 1e-6) {
		t.Errorf("expected h=120, got %v", h)
	}
	if !floatClose(s, 0.5, 1e-6) {
		t.Errorf("expected s=0.5, got %v", s)
	}
	if !floatClose(v, 0.8, 1e-6) {
		t.Errorf("expected v=0.8, got %v", v)
	}
}

// ==================================================================
// Color HSL
// ==================================================================

func TestColorFromHSL(t *testing.T) {
	interp := runWithColor(t, `
		c := color.fromHSL(0, 1, 0.5)
		rr := c.r
		rg := c.g
		rb := c.b
	`)
	// HSL(0, 1, 0.5) = pure red
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0, 1e-10) {
		t.Errorf("expected g=0, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0, 1e-10) {
		t.Errorf("expected b=0, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorToHSL(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		h, s, l := color.toHSL(c)
	`)
	h := interp.GetGlobal("h").Number()
	s := interp.GetGlobal("s").Number()
	l := interp.GetGlobal("l").Number()
	if !floatClose(h, 0, 1e-10) {
		t.Errorf("expected h=0, got %v", h)
	}
	if !floatClose(s, 1, 1e-10) {
		t.Errorf("expected s=1, got %v", s)
	}
	if !floatClose(l, 0.5, 1e-10) {
		t.Errorf("expected l=0.5, got %v", l)
	}
}

// ==================================================================
// Color interpolation / manipulation
// ==================================================================

func TestColorLerp(t *testing.T) {
	interp := runWithColor(t, `
		c1 := color.new(0, 0, 0, 1)
		c2 := color.new(1, 1, 1, 1)
		c3 := color.lerp(c1, c2, 0.5)
		rr := c3.r
		rg := c3.g
		rb := c3.b
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.5, 1e-10) {
		t.Errorf("expected r=0.5, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0.5, 1e-10) {
		t.Errorf("expected g=0.5, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.5, 1e-10) {
		t.Errorf("expected b=0.5, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorMix(t *testing.T) {
	interp := runWithColor(t, `
		c1 := color.new(1, 0, 0, 1)
		c2 := color.new(0, 0, 1, 1)
		c3 := color.mix(c1, c2, 0.5)
		rr := c3.r
		rb := c3.b
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.5, 1e-10) {
		t.Errorf("expected r=0.5, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.5, 1e-10) {
		t.Errorf("expected b=0.5, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorDarken(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0.8, 0.6, 1)
		d := color.darken(c, 0.5)
		rr := d.r
		rg := d.g
		rb := d.b
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.5, 1e-10) {
		t.Errorf("expected r=0.5, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0.4, 1e-10) {
		t.Errorf("expected g=0.4, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.3, 1e-10) {
		t.Errorf("expected b=0.3, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorLighten(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(0.4, 0.4, 0.4, 1)
		l := color.lighten(c, 0.5)
		rr := l.r
	`)
	// lighten: r + (1 - r) * amount = 0.4 + 0.6*0.5 = 0.7
	if !floatClose(interp.GetGlobal("rr").Number(), 0.7, 1e-10) {
		t.Errorf("expected r=0.7, got %v", interp.GetGlobal("rr"))
	}
}

func TestColorAlpha(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		c2 := color.alpha(c, 0.5)
		rr := c2.r
		ra := c2.a
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 1.0, 1e-10) {
		t.Errorf("expected r=1.0, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("ra").Number(), 0.5, 1e-10) {
		t.Errorf("expected a=0.5, got %v", interp.GetGlobal("ra"))
	}
}

func TestColorWithAlpha(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		c2 := color.withAlpha(c, 0.3)
		ra := c2.a
	`)
	if !floatClose(interp.GetGlobal("ra").Number(), 0.3, 1e-10) {
		t.Errorf("expected a=0.3, got %v", interp.GetGlobal("ra"))
	}
}

func TestColorInvert(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(0.2, 0.4, 0.6, 1)
		inv := color.invert(c)
		rr := inv.r
		rg := inv.g
		rb := inv.b
		ra := inv.a
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.8, 1e-10) {
		t.Errorf("expected r=0.8, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0.6, 1e-10) {
		t.Errorf("expected g=0.6, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.4, 1e-10) {
		t.Errorf("expected b=0.4, got %v", interp.GetGlobal("rb"))
	}
	if interp.GetGlobal("ra").Number() != 1 {
		t.Errorf("expected a=1 (preserved), got %v", interp.GetGlobal("ra"))
	}
}

func TestColorGrayscale(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		g := color.grayscale(c)
		rr := g.r
		rg := g.g
		rb := g.b
	`)
	// luminance weights: 0.2126*R + 0.7152*G + 0.0722*B
	// for (1,0,0): 0.2126
	lum := 0.2126
	rr := interp.GetGlobal("rr").Number()
	rg := interp.GetGlobal("rg").Number()
	rb := interp.GetGlobal("rb").Number()
	if !floatClose(rr, lum, 1e-4) {
		t.Errorf("expected r=%v, got %v", lum, rr)
	}
	if !floatClose(rg, lum, 1e-4) {
		t.Errorf("expected g=%v, got %v", lum, rg)
	}
	if !floatClose(rb, lum, 1e-4) {
		t.Errorf("expected b=%v, got %v", lum, rb)
	}
}

func TestColorIsColor(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(1, 0, 0, 1)
		r1 := color.isColor(c)
		r2 := color.isColor(42)
		r3 := color.isColor({r: 1, g: 0, b: 0, a: 1})
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected isColor(color) to be true")
	}
	if interp.GetGlobal("r2").Truthy() {
		t.Errorf("expected isColor(42) to be false")
	}
	if interp.GetGlobal("r3").Truthy() {
		t.Errorf("expected isColor(table without _type) to be false")
	}
}

// ==================================================================
// Named color constants
// ==================================================================

func TestColorConstants(t *testing.T) {
	interp := runWithColor(t, `
		rr := color.RED.r
		rg := color.RED.g
		rb := color.RED.b

		gr := color.GREEN.r
		gg := color.GREEN.g
		gb := color.GREEN.b

		br := color.BLUE.r
		bg := color.BLUE.g
		bb := color.BLUE.b

		wr := color.WHITE.r
		wg := color.WHITE.g
		wb := color.WHITE.b

		kr := color.BLACK.r
		kg := color.BLACK.g
		kb := color.BLACK.b

		ta := color.TRANSPARENT.a
	`)
	if interp.GetGlobal("rr").Number() != 1 || interp.GetGlobal("rg").Number() != 0 || interp.GetGlobal("rb").Number() != 0 {
		t.Errorf("RED is not (1,0,0)")
	}
	if interp.GetGlobal("gr").Number() != 0 || interp.GetGlobal("gg").Number() != 1 || interp.GetGlobal("gb").Number() != 0 {
		t.Errorf("GREEN is not (0,1,0)")
	}
	if interp.GetGlobal("br").Number() != 0 || interp.GetGlobal("bg").Number() != 0 || interp.GetGlobal("bb").Number() != 1 {
		t.Errorf("BLUE is not (0,0,1)")
	}
	if interp.GetGlobal("wr").Number() != 1 || interp.GetGlobal("wg").Number() != 1 || interp.GetGlobal("wb").Number() != 1 {
		t.Errorf("WHITE is not (1,1,1)")
	}
	if interp.GetGlobal("kr").Number() != 0 || interp.GetGlobal("kg").Number() != 0 || interp.GetGlobal("kb").Number() != 0 {
		t.Errorf("BLACK is not (0,0,0)")
	}
	if interp.GetGlobal("ta").Number() != 0 {
		t.Errorf("TRANSPARENT.a should be 0, got %v", interp.GetGlobal("ta"))
	}
}

func TestColorMoreConstants(t *testing.T) {
	interp := runWithColor(t, `
		yr := color.YELLOW.r
		yg := color.YELLOW.g
		yb := color.YELLOW.b

		cr := color.CYAN.r
		cg := color.CYAN.g
		cb := color.CYAN.b

		mr := color.MAGENTA.r
		mg := color.MAGENTA.g
		mb := color.MAGENTA.b

		or := color.ORANGE.r
		og := color.ORANGE.g
		ob := color.ORANGE.b

		pr := color.PURPLE.r
		pg := color.PURPLE.g
		pb := color.PURPLE.b
	`)
	if interp.GetGlobal("yr").Number() != 1 || interp.GetGlobal("yg").Number() != 1 || interp.GetGlobal("yb").Number() != 0 {
		t.Errorf("YELLOW is not (1,1,0)")
	}
	if interp.GetGlobal("cr").Number() != 0 || interp.GetGlobal("cg").Number() != 1 || interp.GetGlobal("cb").Number() != 1 {
		t.Errorf("CYAN is not (0,1,1)")
	}
	if interp.GetGlobal("mr").Number() != 1 || interp.GetGlobal("mg").Number() != 0 || interp.GetGlobal("mb").Number() != 1 {
		t.Errorf("MAGENTA is not (1,0,1)")
	}
	if interp.GetGlobal("or").Number() != 1 || !floatClose(interp.GetGlobal("og").Number(), 0.5, 1e-10) || interp.GetGlobal("ob").Number() != 0 {
		t.Errorf("ORANGE is not (1,0.5,0)")
	}
	if !floatClose(interp.GetGlobal("pr").Number(), 0.5, 1e-10) || interp.GetGlobal("pg").Number() != 0 || !floatClose(interp.GetGlobal("pb").Number(), 0.5, 1e-10) {
		t.Errorf("PURPLE is not (0.5,0,0.5)")
	}
}

// ==================================================================
// Color operator tests
// ==================================================================

func TestColorAdd(t *testing.T) {
	interp := runWithColor(t, `
		c1 := color.new(0.5, 0.3, 0.2, 1)
		c2 := color.new(0.3, 0.9, 0.5, 1)
		c3 := c1 + c2
		rr := c3.r
		rg := c3.g
		rb := c3.b
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.8, 1e-10) {
		t.Errorf("expected r=0.8, got %v", interp.GetGlobal("rr"))
	}
	// 0.3 + 0.9 = 1.2 -> clamped to 1.0
	if !floatClose(interp.GetGlobal("rg").Number(), 1.0, 1e-10) {
		t.Errorf("expected g=1.0 (clamped), got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.7, 1e-10) {
		t.Errorf("expected b=0.7, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorMulScalar(t *testing.T) {
	interp := runWithColor(t, `
		c := color.new(0.5, 0.3, 0.2, 1)
		c2 := c * 0.5
		rr := c2.r
		rg := c2.g
		rb := c2.b
	`)
	if !floatClose(interp.GetGlobal("rr").Number(), 0.25, 1e-10) {
		t.Errorf("expected r=0.25, got %v", interp.GetGlobal("rr"))
	}
	if !floatClose(interp.GetGlobal("rg").Number(), 0.15, 1e-10) {
		t.Errorf("expected g=0.15, got %v", interp.GetGlobal("rg"))
	}
	if !floatClose(interp.GetGlobal("rb").Number(), 0.1, 1e-10) {
		t.Errorf("expected b=0.1, got %v", interp.GetGlobal("rb"))
	}
}

func TestColorEq(t *testing.T) {
	interp := runWithColor(t, `
		c1 := color.new(1, 0, 0, 1)
		c2 := color.new(1, 0, 0, 1)
		c3 := color.new(0, 1, 0, 1)
		r1 := c1 == c2
		r2 := c1 == c3
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected c1 == c2 to be true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected c1 == c3 to be false")
	}
}

// Suppress the unused import warning for math package.
var _ = math.Abs
