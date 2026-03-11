package runtime

import (
	"fmt"
	"math"
	"strings"
)

// --------------------------------------------------------------------------
// Color metatable (shared across all color instances)
// --------------------------------------------------------------------------

var colorMeta *Table

func newColorMeta() *Table {
	mt := NewTable()

	getRGBA := func(v Value) (float64, float64, float64, float64) {
		tbl := v.Table()
		return toFloat(tbl.RawGet(StringValue("r"))),
			toFloat(tbl.RawGet(StringValue("g"))),
			toFloat(tbl.RawGet(StringValue("b"))),
			toFloat(tbl.RawGet(StringValue("a")))
	}

	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}

	// __add: component-wise add (clamped to 1)
	mt.RawSet(StringValue("__add"), FunctionValue(&GoFunction{
		Name: "color.__add",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("color.__add requires 2 arguments")
			}
			r1, g1, b1, a1 := getRGBA(args[0])
			r2, g2, b2, a2 := getRGBA(args[1])
			return []Value{makeColorValue(
				clamp01(r1+r2),
				clamp01(g1+g2),
				clamp01(b1+b2),
				clamp01(a1+a2),
			)}, nil
		},
	}))

	// __mul: scale by float or component-wise multiply by color
	mt.RawSet(StringValue("__mul"), FunctionValue(&GoFunction{
		Name: "color.__mul",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("color.__mul requires 2 arguments")
			}
			a, b := args[0], args[1]
			if a.IsTable() && (b.IsNumber() || b.IsInt()) {
				r, g, bl, al := getRGBA(a)
				s := toFloat(b)
				return []Value{makeColorValue(r*s, g*s, bl*s, al)}, nil
			}
			if (a.IsNumber() || a.IsInt()) && b.IsTable() {
				s := toFloat(a)
				r, g, bl, al := getRGBA(b)
				return []Value{makeColorValue(s*r, s*g, s*bl, al)}, nil
			}
			// Component-wise multiply of two colors
			if a.IsTable() && b.IsTable() {
				r1, g1, b1, a1 := getRGBA(a)
				r2, g2, b2, a2 := getRGBA(b)
				return []Value{makeColorValue(r1*r2, g1*g2, b1*b2, a1*a2)}, nil
			}
			return nil, fmt.Errorf("color.__mul: unsupported operand types")
		},
	}))

	// __eq: compare r,g,b,a
	mt.RawSet(StringValue("__eq"), FunctionValue(&GoFunction{
		Name: "color.__eq",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("color.__eq requires 2 arguments")
			}
			r1, g1, b1, a1 := getRGBA(args[0])
			r2, g2, b2, a2 := getRGBA(args[1])
			return []Value{BoolValue(r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2)}, nil
		},
	}))

	return mt
}

// --------------------------------------------------------------------------
// Internal helper to create color table values
// --------------------------------------------------------------------------

func makeColorValue(r, g, b, a float64) Value {
	t := NewTable()
	t.RawSet(StringValue("r"), FloatValue(r))
	t.RawSet(StringValue("g"), FloatValue(g))
	t.RawSet(StringValue("b"), FloatValue(b))
	t.RawSet(StringValue("a"), FloatValue(a))
	t.RawSet(StringValue("_type"), StringValue("color"))
	t.SetMetatable(colorMeta)
	return TableValue(t)
}

func isColorValue(v Value) bool {
	if !v.IsTable() {
		return false
	}
	ty := v.Table().RawGet(StringValue("_type"))
	return ty.IsString() && ty.Str() == "color"
}

// --------------------------------------------------------------------------
// HSV/HSL conversion helpers
// --------------------------------------------------------------------------

func hsvToRGB(h, s, v float64) (float64, float64, float64) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c

	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func rgbToHSV(r, g, b float64) (float64, float64, float64) {
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	delta := max - min

	var h float64
	if delta == 0 {
		h = 0
	} else if max == r {
		h = 60 * math.Mod((g-b)/delta, 6)
	} else if max == g {
		h = 60 * ((b-r)/delta + 2)
	} else {
		h = 60 * ((r-g)/delta + 4)
	}
	if h < 0 {
		h += 360
	}

	var s float64
	if max != 0 {
		s = delta / max
	}

	return h, s, max
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2

	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func rgbToHSL(r, g, b float64) (float64, float64, float64) {
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l := (max + min) / 2
	delta := max - min

	if delta == 0 {
		return 0, 0, l
	}

	var s float64
	if l <= 0.5 {
		s = delta / (max + min)
	} else {
		s = delta / (2 - max - min)
	}

	var h float64
	if max == r {
		h = 60 * math.Mod((g-b)/delta, 6)
	} else if max == g {
		h = 60 * ((b-r)/delta + 2)
	} else {
		h = 60 * ((r-g)/delta + 4)
	}
	if h < 0 {
		h += 360
	}

	return h, s, l
}

// parseHexByte parses a two-character hex string into a byte value.
func parseHexByte(s string) (uint8, bool) {
	if len(s) != 2 {
		return 0, false
	}
	var val uint8
	for _, c := range s {
		val <<= 4
		switch {
		case c >= '0' && c <= '9':
			val |= uint8(c - '0')
		case c >= 'a' && c <= 'f':
			val |= uint8(c-'a') + 10
		case c >= 'A' && c <= 'F':
			val |= uint8(c-'A') + 10
		default:
			return 0, false
		}
	}
	return val, true
}

// --------------------------------------------------------------------------
// buildColorLib creates the "color" standard library table.
// --------------------------------------------------------------------------

func buildColorLib() *Table {
	colorMeta = newColorMeta()

	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "color." + name,
			Fn:   fn,
		}))
	}

	getRGBA := func(v Value) (float64, float64, float64, float64) {
		tbl := v.Table()
		return toFloat(tbl.RawGet(StringValue("r"))),
			toFloat(tbl.RawGet(StringValue("g"))),
			toFloat(tbl.RawGet(StringValue("b"))),
			toFloat(tbl.RawGet(StringValue("a")))
	}

	// ----------------------------------------------------------------
	// Constructors
	// ----------------------------------------------------------------

	// color.new(r, g, b [, a])  -- r,g,b,a in [0, 1]
	set("new", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'color.new': expected at least 3 arguments")
		}
		r := toFloat(args[0])
		g := toFloat(args[1])
		b := toFloat(args[2])
		a := 1.0
		if len(args) >= 4 {
			a = toFloat(args[3])
		}
		return []Value{makeColorValue(r, g, b, a)}, nil
	})

	// color.rgb(r, g, b) -- r,g,b in [0, 255]
	set("rgb", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'color.rgb': expected 3 arguments")
		}
		r := toFloat(args[0]) / 255.0
		g := toFloat(args[1]) / 255.0
		b := toFloat(args[2]) / 255.0
		return []Value{makeColorValue(r, g, b, 1.0)}, nil
	})

	// color.rgba(r, g, b, a) -- r,g,b,a in [0, 255]
	set("rgba", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("bad argument to 'color.rgba': expected 4 arguments")
		}
		r := toFloat(args[0]) / 255.0
		g := toFloat(args[1]) / 255.0
		b := toFloat(args[2]) / 255.0
		a := toFloat(args[3]) / 255.0
		return []Value{makeColorValue(r, g, b, a)}, nil
	})

	// color.fromHex(hexStr) -- parses "#RGB", "#RRGGBB", "#RRGGBBAA"
	set("fromHex", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsString() {
			return []Value{NilValue(), StringValue("bad argument to 'color.fromHex': expected string")}, nil
		}
		hex := args[0].Str()
		hex = strings.TrimPrefix(hex, "#")

		var r, g, b, a uint8
		a = 255

		switch len(hex) {
		case 3: // RGB
			rb, ok1 := parseHexByte(string(hex[0]) + string(hex[0]))
			gb, ok2 := parseHexByte(string(hex[1]) + string(hex[1]))
			bb, ok3 := parseHexByte(string(hex[2]) + string(hex[2]))
			if !ok1 || !ok2 || !ok3 {
				return []Value{NilValue(), StringValue("invalid hex color: " + args[0].Str())}, nil
			}
			r, g, b = rb, gb, bb
		case 6: // RRGGBB
			rb, ok1 := parseHexByte(hex[0:2])
			gb, ok2 := parseHexByte(hex[2:4])
			bb, ok3 := parseHexByte(hex[4:6])
			if !ok1 || !ok2 || !ok3 {
				return []Value{NilValue(), StringValue("invalid hex color: " + args[0].Str())}, nil
			}
			r, g, b = rb, gb, bb
		case 8: // RRGGBBAA
			rb, ok1 := parseHexByte(hex[0:2])
			gb, ok2 := parseHexByte(hex[2:4])
			bb, ok3 := parseHexByte(hex[4:6])
			ab, ok4 := parseHexByte(hex[6:8])
			if !ok1 || !ok2 || !ok3 || !ok4 {
				return []Value{NilValue(), StringValue("invalid hex color: " + args[0].Str())}, nil
			}
			r, g, b, a = rb, gb, bb, ab
		default:
			return []Value{NilValue(), StringValue("invalid hex color format: " + args[0].Str())}, nil
		}

		return []Value{makeColorValue(
			float64(r)/255.0,
			float64(g)/255.0,
			float64(b)/255.0,
			float64(a)/255.0,
		)}, nil
	})

	// color.toHex(c) -> "#RRGGBB"
	set("toHex", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'color.toHex'")
		}
		r, g, b, _ := getRGBA(args[0])
		ri := uint8(math.Round(r * 255))
		gi := uint8(math.Round(g * 255))
		bi := uint8(math.Round(b * 255))
		hex := fmt.Sprintf("#%02X%02X%02X", ri, gi, bi)
		return []Value{StringValue(hex)}, nil
	})

	// ----------------------------------------------------------------
	// HSV conversions
	// ----------------------------------------------------------------

	// color.fromHSV(h, s, v) -- h in [0,360], s,v in [0,1]
	set("fromHSV", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'color.fromHSV'")
		}
		h := toFloat(args[0])
		s := toFloat(args[1])
		v := toFloat(args[2])
		r, g, b := hsvToRGB(h, s, v)
		return []Value{makeColorValue(r, g, b, 1.0)}, nil
	})

	// color.toHSV(c) -> h, s, v
	set("toHSV", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'color.toHSV'")
		}
		r, g, b, _ := getRGBA(args[0])
		h, s, v := rgbToHSV(r, g, b)
		return []Value{FloatValue(h), FloatValue(s), FloatValue(v)}, nil
	})

	// ----------------------------------------------------------------
	// HSL conversions
	// ----------------------------------------------------------------

	// color.fromHSL(h, s, l) -- h in [0,360], s,l in [0,1]
	set("fromHSL", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'color.fromHSL'")
		}
		h := toFloat(args[0])
		s := toFloat(args[1])
		l := toFloat(args[2])
		r, g, b := hslToRGB(h, s, l)
		return []Value{makeColorValue(r, g, b, 1.0)}, nil
	})

	// color.toHSL(c) -> h, s, l
	set("toHSL", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'color.toHSL'")
		}
		r, g, b, _ := getRGBA(args[0])
		h, s, l := rgbToHSL(r, g, b)
		return []Value{FloatValue(h), FloatValue(s), FloatValue(l)}, nil
	})

	// ----------------------------------------------------------------
	// Interpolation / manipulation
	// ----------------------------------------------------------------

	// color.lerp(c1, c2, t) -- linear interpolation in RGB space
	set("lerp", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'color.lerp'")
		}
		r1, g1, b1, a1 := getRGBA(args[0])
		r2, g2, b2, a2 := getRGBA(args[1])
		t := toFloat(args[2])
		return []Value{makeColorValue(
			r1+(r2-r1)*t,
			g1+(g2-g1)*t,
			b1+(b2-b1)*t,
			a1+(a2-a1)*t,
		)}, nil
	})

	// color.mix -- alias for lerp
	set("mix", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'color.mix'")
		}
		r1, g1, b1, a1 := getRGBA(args[0])
		r2, g2, b2, a2 := getRGBA(args[1])
		t := toFloat(args[2])
		return []Value{makeColorValue(
			r1+(r2-r1)*t,
			g1+(g2-g1)*t,
			b1+(b2-b1)*t,
			a1+(a2-a1)*t,
		)}, nil
	})

	// color.darken(c, amount) -- reduce brightness by amount (0-1)
	set("darken", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'color.darken'")
		}
		r, g, b, a := getRGBA(args[0])
		amount := toFloat(args[1])
		factor := 1.0 - amount
		return []Value{makeColorValue(r*factor, g*factor, b*factor, a)}, nil
	})

	// color.lighten(c, amount) -- increase brightness
	set("lighten", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'color.lighten'")
		}
		r, g, b, a := getRGBA(args[0])
		amount := toFloat(args[1])
		return []Value{makeColorValue(
			r+(1-r)*amount,
			g+(1-g)*amount,
			b+(1-b)*amount,
			a,
		)}, nil
	})

	// color.alpha(c, a) -- same color with new alpha
	set("alpha", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'color.alpha'")
		}
		r, g, b, _ := getRGBA(args[0])
		newA := toFloat(args[1])
		return []Value{makeColorValue(r, g, b, newA)}, nil
	})

	// color.withAlpha -- alias for alpha
	set("withAlpha", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'color.withAlpha'")
		}
		r, g, b, _ := getRGBA(args[0])
		newA := toFloat(args[1])
		return []Value{makeColorValue(r, g, b, newA)}, nil
	})

	// color.invert(c)
	set("invert", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'color.invert'")
		}
		r, g, b, a := getRGBA(args[0])
		return []Value{makeColorValue(1-r, 1-g, 1-b, a)}, nil
	})

	// color.grayscale(c) -- using luminance weights
	set("grayscale", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'color.grayscale'")
		}
		r, g, b, a := getRGBA(args[0])
		lum := 0.2126*r + 0.7152*g + 0.0722*b
		return []Value{makeColorValue(lum, lum, lum, a)}, nil
	})

	// color.isColor(v)
	set("isColor", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(isColorValue(args[0]))}, nil
	})

	// ----------------------------------------------------------------
	// Named color constants
	// ----------------------------------------------------------------
	t.RawSet(StringValue("RED"), makeColorValue(1, 0, 0, 1))
	t.RawSet(StringValue("GREEN"), makeColorValue(0, 1, 0, 1))
	t.RawSet(StringValue("BLUE"), makeColorValue(0, 0, 1, 1))
	t.RawSet(StringValue("WHITE"), makeColorValue(1, 1, 1, 1))
	t.RawSet(StringValue("BLACK"), makeColorValue(0, 0, 0, 1))
	t.RawSet(StringValue("YELLOW"), makeColorValue(1, 1, 0, 1))
	t.RawSet(StringValue("CYAN"), makeColorValue(0, 1, 1, 1))
	t.RawSet(StringValue("MAGENTA"), makeColorValue(1, 0, 1, 1))
	t.RawSet(StringValue("ORANGE"), makeColorValue(1, 0.5, 0, 1))
	t.RawSet(StringValue("PURPLE"), makeColorValue(0.5, 0, 0.5, 1))
	t.RawSet(StringValue("TRANSPARENT"), makeColorValue(0, 0, 0, 0))

	return t
}
