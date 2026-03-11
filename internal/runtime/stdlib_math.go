package runtime

import (
	"fmt"
	"math"
	"math/rand"
)

// buildMathLib creates the "math" standard library table.
func buildMathLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "math." + name,
			Fn:   fn,
		}))
	}

	// Constants
	t.RawSet(StringValue("pi"), FloatValue(math.Pi))
	t.RawSet(StringValue("huge"), FloatValue(math.Inf(1)))
	t.RawSet(StringValue("maxinteger"), IntValue(math.MaxInt64))
	t.RawSet(StringValue("mininteger"), IntValue(math.MinInt64))

	// math.abs(x)
	set("abs", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.abs'")
		}
		if args[0].IsInt() {
			v := args[0].Int()
			if v < 0 {
				v = -v
			}
			return []Value{IntValue(v)}, nil
		}
		return []Value{FloatValue(math.Abs(toFloat(args[0])))}, nil
	})

	// math.ceil(x) -> int
	set("ceil", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.ceil'")
		}
		if args[0].IsInt() {
			return []Value{args[0]}, nil
		}
		return []Value{IntValue(int64(math.Ceil(toFloat(args[0]))))}, nil
	})

	// math.floor(x) -> int
	set("floor", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.floor'")
		}
		if args[0].IsInt() {
			return []Value{args[0]}, nil
		}
		return []Value{IntValue(int64(math.Floor(toFloat(args[0]))))}, nil
	})

	// math.sqrt(x)
	set("sqrt", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.sqrt'")
		}
		return []Value{FloatValue(math.Sqrt(toFloat(args[0])))}, nil
	})

	// math.sin(x)
	set("sin", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.sin'")
		}
		return []Value{FloatValue(math.Sin(toFloat(args[0])))}, nil
	})

	// math.cos(x)
	set("cos", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.cos'")
		}
		return []Value{FloatValue(math.Cos(toFloat(args[0])))}, nil
	})

	// math.tan(x)
	set("tan", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.tan'")
		}
		return []Value{FloatValue(math.Tan(toFloat(args[0])))}, nil
	})

	// math.asin(x)
	set("asin", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.asin'")
		}
		return []Value{FloatValue(math.Asin(toFloat(args[0])))}, nil
	})

	// math.acos(x)
	set("acos", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.acos'")
		}
		return []Value{FloatValue(math.Acos(toFloat(args[0])))}, nil
	})

	// math.atan(y [, x])
	set("atan", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.atan'")
		}
		y := toFloat(args[0])
		if len(args) >= 2 {
			x := toFloat(args[1])
			return []Value{FloatValue(math.Atan2(y, x))}, nil
		}
		return []Value{FloatValue(math.Atan(y))}, nil
	})

	// math.exp(x)
	set("exp", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.exp'")
		}
		return []Value{FloatValue(math.Exp(toFloat(args[0])))}, nil
	})

	// math.log(x [, base])
	set("log", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.log'")
		}
		x := toFloat(args[0])
		if len(args) >= 2 {
			base := toFloat(args[1])
			return []Value{FloatValue(math.Log(x) / math.Log(base))}, nil
		}
		return []Value{FloatValue(math.Log(x))}, nil
	})

	// math.max(x, ...)
	set("max", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.max'")
		}
		best := args[0]
		for _, v := range args[1:] {
			lt, ok := best.lessThan(v)
			if ok && lt {
				best = v
			}
		}
		return []Value{best}, nil
	})

	// math.min(x, ...)
	set("min", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.min'")
		}
		best := args[0]
		for _, v := range args[1:] {
			lt, ok := v.lessThan(best)
			if ok && lt {
				best = v
			}
		}
		return []Value{best}, nil
	})

	// math.fmod(x, y)
	set("fmod", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'math.fmod'")
		}
		return []Value{FloatValue(math.Mod(toFloat(args[0]), toFloat(args[1])))}, nil
	})

	// math.modf(x) -> int, frac
	set("modf", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.modf'")
		}
		i, f := math.Modf(toFloat(args[0]))
		return []Value{FloatValue(i), FloatValue(f)}, nil
	})

	// math.pow(x, y)  -- same as x ** y
	set("pow", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'math.pow'")
		}
		return []Value{FloatValue(math.Pow(toFloat(args[0]), toFloat(args[1])))}, nil
	})

	// math.random([m [, n]])
	set("random", func(args []Value) ([]Value, error) {
		if len(args) == 0 {
			return []Value{FloatValue(rand.Float64())}, nil
		}
		if len(args) == 1 {
			m := toInt(args[0])
			if m < 1 {
				return nil, fmt.Errorf("bad argument #1 to 'math.random' (interval is empty)")
			}
			return []Value{IntValue(rand.Int63n(m) + 1)}, nil
		}
		m := toInt(args[0])
		n := toInt(args[1])
		if m > n {
			return nil, fmt.Errorf("bad argument #2 to 'math.random' (interval is empty)")
		}
		return []Value{IntValue(m + rand.Int63n(n-m+1))}, nil
	})

	// math.randomseed(x)
	set("randomseed", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.randomseed'")
		}
		rand.Seed(toInt(args[0]))
		return nil, nil
	})

	// math.type(x) -> "integer" | "float" | false
	set("type", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		v := args[0]
		if v.IsInt() {
			return []Value{StringValue("integer")}, nil
		}
		if v.IsFloat() {
			return []Value{StringValue("float")}, nil
		}
		return []Value{BoolValue(false)}, nil
	})

	// math.tointeger(x) -- convert float to int (exact), nil if not exact
	set("tointeger", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{NilValue()}, nil
		}
		v := args[0]
		if v.IsInt() {
			return []Value{v}, nil
		}
		if v.IsFloat() {
			f := v.Float()
			if floatIsInt(f) {
				return []Value{IntValue(int64(f))}, nil
			}
		}
		return []Value{NilValue()}, nil
	})

	// math.clamp(x, min, max) -- clamp x to [min, max] range
	set("clamp", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'math.clamp' (3 arguments expected)")
		}
		x := toFloat(args[0])
		mn := toFloat(args[1])
		mx := toFloat(args[2])
		if x < mn {
			x = mn
		} else if x > mx {
			x = mx
		}
		// If all args are ints, return int
		if args[0].IsInt() && args[1].IsInt() && args[2].IsInt() {
			return []Value{IntValue(int64(x))}, nil
		}
		return []Value{FloatValue(x)}, nil
	})

	// math.lerp(a, b, t) -- linear interpolation: a + (b-a)*t
	set("lerp", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'math.lerp' (3 arguments expected)")
		}
		a := toFloat(args[0])
		b := toFloat(args[1])
		t := toFloat(args[2])
		return []Value{FloatValue(a + (b-a)*t)}, nil
	})

	// math.sign(x) -- -1, 0, or 1
	set("sign", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.sign'")
		}
		x := toFloat(args[0])
		if x > 0 {
			return []Value{IntValue(1)}, nil
		} else if x < 0 {
			return []Value{IntValue(-1)}, nil
		}
		return []Value{IntValue(0)}, nil
	})

	// math.round(x [, n]) -- round to n decimal places (n=0 rounds to integer)
	set("round", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.round'")
		}
		x := toFloat(args[0])
		n := int64(0)
		if len(args) >= 2 {
			n = toInt(args[1])
		}
		if n == 0 {
			return []Value{IntValue(int64(math.Round(x)))}, nil
		}
		factor := math.Pow(10, float64(n))
		return []Value{FloatValue(math.Round(x*factor) / factor)}, nil
	})

	// math.trunc(x) -- truncate toward zero
	set("trunc", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.trunc'")
		}
		if args[0].IsInt() {
			return []Value{args[0]}, nil
		}
		return []Value{IntValue(int64(math.Trunc(toFloat(args[0]))))}, nil
	})

	// math.hypot(x, y) -- sqrt(x*x + y*y)
	set("hypot", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'math.hypot'")
		}
		return []Value{FloatValue(math.Hypot(toFloat(args[0]), toFloat(args[1])))}, nil
	})

	// math.isnan(x) -> bool
	set("isnan", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.isnan'")
		}
		return []Value{BoolValue(math.IsNaN(toFloat(args[0])))}, nil
	})

	// math.isinf(x) -> bool
	set("isinf", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument #1 to 'math.isinf'")
		}
		return []Value{BoolValue(math.IsInf(toFloat(args[0]), 0))}, nil
	})

	return t
}
