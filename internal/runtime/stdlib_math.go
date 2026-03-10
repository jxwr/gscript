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

	// math.tointeger(x)
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

	return t
}
