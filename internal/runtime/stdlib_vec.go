package runtime

import (
	"fmt"
	"math"
)

// --------------------------------------------------------------------------
// Vec2 metatable (shared across all vec2 instances)
// --------------------------------------------------------------------------

func newVec2Meta() *Table {
	mt := NewTable()

	// Helper to extract x, y from a vec2 table
	getXY := func(v Value) (float64, float64) {
		tbl := v.Table()
		return toFloat(tbl.RawGet(StringValue("x"))), toFloat(tbl.RawGet(StringValue("y")))
	}

	// __add: vec2 + vec2
	mt.RawSet(StringValue("__add"), FunctionValue(&GoFunction{
		Name: "vec2.__add",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec2.__add requires 2 arguments")
			}
			ax, ay := getXY(args[0])
			bx, by := getXY(args[1])
			return []Value{makeVec2Value(ax+bx, ay+by)}, nil
		},
	}))

	// __sub: vec2 - vec2
	mt.RawSet(StringValue("__sub"), FunctionValue(&GoFunction{
		Name: "vec2.__sub",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec2.__sub requires 2 arguments")
			}
			ax, ay := getXY(args[0])
			bx, by := getXY(args[1])
			return []Value{makeVec2Value(ax-bx, ay-by)}, nil
		},
	}))

	// __mul: vec2 * scalar or scalar * vec2
	mt.RawSet(StringValue("__mul"), FunctionValue(&GoFunction{
		Name: "vec2.__mul",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec2.__mul requires 2 arguments")
			}
			a, b := args[0], args[1]
			if a.IsTable() && (b.IsNumber() || b.IsInt()) {
				ax, ay := getXY(a)
				s := toFloat(b)
				return []Value{makeVec2Value(ax*s, ay*s)}, nil
			}
			if (a.IsNumber() || a.IsInt()) && b.IsTable() {
				s := toFloat(a)
				bx, by := getXY(b)
				return []Value{makeVec2Value(s*bx, s*by)}, nil
			}
			return nil, fmt.Errorf("vec2.__mul: unsupported operand types")
		},
	}))

	// __div: vec2 / scalar
	mt.RawSet(StringValue("__div"), FunctionValue(&GoFunction{
		Name: "vec2.__div",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec2.__div requires 2 arguments")
			}
			ax, ay := getXY(args[0])
			s := toFloat(args[1])
			if s == 0 {
				return nil, fmt.Errorf("vec2.__div: division by zero")
			}
			return []Value{makeVec2Value(ax/s, ay/s)}, nil
		},
	}))

	// __unm: -vec2
	mt.RawSet(StringValue("__unm"), FunctionValue(&GoFunction{
		Name: "vec2.__unm",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("vec2.__unm requires 1 argument")
			}
			ax, ay := getXY(args[0])
			return []Value{makeVec2Value(-ax, -ay)}, nil
		},
	}))

	// __eq: vec2 == vec2
	mt.RawSet(StringValue("__eq"), FunctionValue(&GoFunction{
		Name: "vec2.__eq",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec2.__eq requires 2 arguments")
			}
			ax, ay := getXY(args[0])
			bx, by := getXY(args[1])
			return []Value{BoolValue(ax == bx && ay == by)}, nil
		},
	}))

	return mt
}

// --------------------------------------------------------------------------
// Vec3 metatable (shared across all vec3 instances)
// --------------------------------------------------------------------------

func newVec3Meta() *Table {
	mt := NewTable()

	getXYZ := func(v Value) (float64, float64, float64) {
		tbl := v.Table()
		return toFloat(tbl.RawGet(StringValue("x"))),
			toFloat(tbl.RawGet(StringValue("y"))),
			toFloat(tbl.RawGet(StringValue("z")))
	}

	// __add
	mt.RawSet(StringValue("__add"), FunctionValue(&GoFunction{
		Name: "vec3.__add",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec3.__add requires 2 arguments")
			}
			ax, ay, az := getXYZ(args[0])
			bx, by, bz := getXYZ(args[1])
			return []Value{makeVec3Value(ax+bx, ay+by, az+bz)}, nil
		},
	}))

	// __sub
	mt.RawSet(StringValue("__sub"), FunctionValue(&GoFunction{
		Name: "vec3.__sub",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec3.__sub requires 2 arguments")
			}
			ax, ay, az := getXYZ(args[0])
			bx, by, bz := getXYZ(args[1])
			return []Value{makeVec3Value(ax-bx, ay-by, az-bz)}, nil
		},
	}))

	// __mul
	mt.RawSet(StringValue("__mul"), FunctionValue(&GoFunction{
		Name: "vec3.__mul",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec3.__mul requires 2 arguments")
			}
			a, b := args[0], args[1]
			if a.IsTable() && (b.IsNumber() || b.IsInt()) {
				ax, ay, az := getXYZ(a)
				s := toFloat(b)
				return []Value{makeVec3Value(ax*s, ay*s, az*s)}, nil
			}
			if (a.IsNumber() || a.IsInt()) && b.IsTable() {
				s := toFloat(a)
				bx, by, bz := getXYZ(b)
				return []Value{makeVec3Value(s*bx, s*by, s*bz)}, nil
			}
			return nil, fmt.Errorf("vec3.__mul: unsupported operand types")
		},
	}))

	// __div
	mt.RawSet(StringValue("__div"), FunctionValue(&GoFunction{
		Name: "vec3.__div",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec3.__div requires 2 arguments")
			}
			ax, ay, az := getXYZ(args[0])
			s := toFloat(args[1])
			if s == 0 {
				return nil, fmt.Errorf("vec3.__div: division by zero")
			}
			return []Value{makeVec3Value(ax/s, ay/s, az/s)}, nil
		},
	}))

	// __unm
	mt.RawSet(StringValue("__unm"), FunctionValue(&GoFunction{
		Name: "vec3.__unm",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("vec3.__unm requires 1 argument")
			}
			ax, ay, az := getXYZ(args[0])
			return []Value{makeVec3Value(-ax, -ay, -az)}, nil
		},
	}))

	// __eq
	mt.RawSet(StringValue("__eq"), FunctionValue(&GoFunction{
		Name: "vec3.__eq",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("vec3.__eq requires 2 arguments")
			}
			ax, ay, az := getXYZ(args[0])
			bx, by, bz := getXYZ(args[1])
			return []Value{BoolValue(ax == bx && ay == by && az == bz)}, nil
		},
	}))

	return mt
}

// --------------------------------------------------------------------------
// Internal helpers to create vec2/vec3 table values
// --------------------------------------------------------------------------

// Shared metatables (created once per buildVecLib call)
var (
	vec2Meta *Table
	vec3Meta *Table
)

func makeVec2Value(x, y float64) Value {
	t := NewTable()
	t.RawSet(StringValue("x"), FloatValue(x))
	t.RawSet(StringValue("y"), FloatValue(y))
	t.RawSet(StringValue("_type"), StringValue("vec2"))
	t.SetMetatable(vec2Meta)
	return TableValue(t)
}

func makeVec3Value(x, y, z float64) Value {
	t := NewTable()
	t.RawSet(StringValue("x"), FloatValue(x))
	t.RawSet(StringValue("y"), FloatValue(y))
	t.RawSet(StringValue("z"), FloatValue(z))
	t.RawSet(StringValue("_type"), StringValue("vec3"))
	t.SetMetatable(vec3Meta)
	return TableValue(t)
}

// isVec2 checks if a value is a vec2 table (has _type == "vec2").
func isVec2(v Value) bool {
	if !v.IsTable() {
		return false
	}
	ty := v.Table().RawGet(StringValue("_type"))
	return ty.IsString() && ty.Str() == "vec2"
}

// isVec3 checks if a value is a vec3 table (has _type == "vec3").
func isVec3(v Value) bool {
	if !v.IsTable() {
		return false
	}
	ty := v.Table().RawGet(StringValue("_type"))
	return ty.IsString() && ty.Str() == "vec3"
}

// --------------------------------------------------------------------------
// buildVecLib creates the "vec" standard library table.
// --------------------------------------------------------------------------

func buildVecLib() *Table {
	// Create shared metatables
	vec2Meta = newVec2Meta()
	vec3Meta = newVec3Meta()

	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "vec." + name,
			Fn:   fn,
		}))
	}

	// ----------------------------------------------------------------
	// Vec2 constructors
	// ----------------------------------------------------------------

	// vec.vec2(x, y)
	set("vec2", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.vec2': expected 2 arguments")
		}
		return []Value{makeVec2Value(toFloat(args[0]), toFloat(args[1]))}, nil
	})

	// vec.zero2()
	set("zero2", func(args []Value) ([]Value, error) {
		return []Value{makeVec2Value(0, 0)}, nil
	})

	// vec.one2()
	set("one2", func(args []Value) ([]Value, error) {
		return []Value{makeVec2Value(1, 1)}, nil
	})

	// vec.up()
	set("up", func(args []Value) ([]Value, error) {
		return []Value{makeVec2Value(0, 1)}, nil
	})

	// vec.right()
	set("right", func(args []Value) ([]Value, error) {
		return []Value{makeVec2Value(1, 0)}, nil
	})

	// ----------------------------------------------------------------
	// Vec2 utilities
	// ----------------------------------------------------------------

	getXY := func(v Value) (float64, float64) {
		tbl := v.Table()
		return toFloat(tbl.RawGet(StringValue("x"))), toFloat(tbl.RawGet(StringValue("y")))
	}

	// vec.dot2(v1, v2)
	set("dot2", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.dot2'")
		}
		ax, ay := getXY(args[0])
		bx, by := getXY(args[1])
		return []Value{FloatValue(ax*bx + ay*by)}, nil
	})

	// vec.length2(v)
	set("length2", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.length2'")
		}
		x, y := getXY(args[0])
		return []Value{FloatValue(math.Sqrt(x*x + y*y))}, nil
	})

	// vec.lengthSq2(v)
	set("lengthSq2", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.lengthSq2'")
		}
		x, y := getXY(args[0])
		return []Value{FloatValue(x*x + y*y)}, nil
	})

	// vec.normalize2(v)
	set("normalize2", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.normalize2'")
		}
		x, y := getXY(args[0])
		l := math.Sqrt(x*x + y*y)
		if l == 0 {
			return []Value{makeVec2Value(0, 0)}, nil
		}
		return []Value{makeVec2Value(x/l, y/l)}, nil
	})

	// vec.angle2(v)
	set("angle2", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.angle2'")
		}
		x, y := getXY(args[0])
		return []Value{FloatValue(math.Atan2(y, x))}, nil
	})

	// vec.rotate2(v, angle)
	set("rotate2", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.rotate2'")
		}
		x, y := getXY(args[0])
		angle := toFloat(args[1])
		cos := math.Cos(angle)
		sin := math.Sin(angle)
		return []Value{makeVec2Value(x*cos-y*sin, x*sin+y*cos)}, nil
	})

	// vec.lerp2(v1, v2, t)
	set("lerp2", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'vec.lerp2'")
		}
		ax, ay := getXY(args[0])
		bx, by := getXY(args[1])
		t := toFloat(args[2])
		return []Value{makeVec2Value(ax+(bx-ax)*t, ay+(by-ay)*t)}, nil
	})

	// vec.dist2(v1, v2)
	set("dist2", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.dist2'")
		}
		ax, ay := getXY(args[0])
		bx, by := getXY(args[1])
		dx, dy := ax-bx, ay-by
		return []Value{FloatValue(math.Sqrt(dx*dx + dy*dy))}, nil
	})

	// vec.distSq2(v1, v2)
	set("distSq2", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.distSq2'")
		}
		ax, ay := getXY(args[0])
		bx, by := getXY(args[1])
		dx, dy := ax-bx, ay-by
		return []Value{FloatValue(dx*dx + dy*dy)}, nil
	})

	// vec.reflect2(v, normal)
	set("reflect2", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.reflect2'")
		}
		vx, vy := getXY(args[0])
		nx, ny := getXY(args[1])
		dot := vx*nx + vy*ny
		return []Value{makeVec2Value(vx-2*dot*nx, vy-2*dot*ny)}, nil
	})

	// vec.perp2(v) -> (-y, x)
	set("perp2", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.perp2'")
		}
		x, y := getXY(args[0])
		return []Value{makeVec2Value(-y, x)}, nil
	})

	// vec.clamp2(v, min, max)
	// min and max can be floats (clamp each component uniformly) or vec2 tables
	set("clamp2", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'vec.clamp2'")
		}
		vx, vy := getXY(args[0])

		var minX, minY, maxX, maxY float64
		if args[1].IsTable() {
			minX, minY = getXY(args[1])
		} else {
			minX = toFloat(args[1])
			minY = minX
		}
		if args[2].IsTable() {
			maxX, maxY = getXY(args[2])
		} else {
			maxX = toFloat(args[2])
			maxY = maxX
		}

		cx := math.Max(minX, math.Min(maxX, vx))
		cy := math.Max(minY, math.Min(maxY, vy))
		return []Value{makeVec2Value(cx, cy)}, nil
	})

	// vec.isVec2(v)
	set("isVec2", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(isVec2(args[0]))}, nil
	})

	// ----------------------------------------------------------------
	// Vec3 constructors
	// ----------------------------------------------------------------

	getXYZ := func(v Value) (float64, float64, float64) {
		tbl := v.Table()
		return toFloat(tbl.RawGet(StringValue("x"))),
			toFloat(tbl.RawGet(StringValue("y"))),
			toFloat(tbl.RawGet(StringValue("z")))
	}

	// vec.vec3(x, y, z)
	set("vec3", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'vec.vec3': expected 3 arguments")
		}
		return []Value{makeVec3Value(toFloat(args[0]), toFloat(args[1]), toFloat(args[2]))}, nil
	})

	// vec.zero3()
	set("zero3", func(args []Value) ([]Value, error) {
		return []Value{makeVec3Value(0, 0, 0)}, nil
	})

	// vec.one3()
	set("one3", func(args []Value) ([]Value, error) {
		return []Value{makeVec3Value(1, 1, 1)}, nil
	})

	// ----------------------------------------------------------------
	// Vec3 utilities
	// ----------------------------------------------------------------

	// vec.dot3(v1, v2)
	set("dot3", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.dot3'")
		}
		ax, ay, az := getXYZ(args[0])
		bx, by, bz := getXYZ(args[1])
		return []Value{FloatValue(ax*bx + ay*by + az*bz)}, nil
	})

	// vec.cross3(v1, v2)
	set("cross3", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.cross3'")
		}
		ax, ay, az := getXYZ(args[0])
		bx, by, bz := getXYZ(args[1])
		return []Value{makeVec3Value(
			ay*bz-az*by,
			az*bx-ax*bz,
			ax*by-ay*bx,
		)}, nil
	})

	// vec.length3(v)
	set("length3", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.length3'")
		}
		x, y, z := getXYZ(args[0])
		return []Value{FloatValue(math.Sqrt(x*x + y*y + z*z))}, nil
	})

	// vec.normalize3(v)
	set("normalize3", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("bad argument to 'vec.normalize3'")
		}
		x, y, z := getXYZ(args[0])
		l := math.Sqrt(x*x + y*y + z*z)
		if l == 0 {
			return []Value{makeVec3Value(0, 0, 0)}, nil
		}
		return []Value{makeVec3Value(x/l, y/l, z/l)}, nil
	})

	// vec.lerp3(v1, v2, t)
	set("lerp3", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("bad argument to 'vec.lerp3'")
		}
		ax, ay, az := getXYZ(args[0])
		bx, by, bz := getXYZ(args[1])
		t := toFloat(args[2])
		return []Value{makeVec3Value(
			ax+(bx-ax)*t,
			ay+(by-ay)*t,
			az+(bz-az)*t,
		)}, nil
	})

	// vec.dist3(v1, v2)
	set("dist3", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("bad argument to 'vec.dist3'")
		}
		ax, ay, az := getXYZ(args[0])
		bx, by, bz := getXYZ(args[1])
		dx, dy, dz := ax-bx, ay-by, az-bz
		return []Value{FloatValue(math.Sqrt(dx*dx + dy*dy + dz*dz))}, nil
	})

	// vec.isVec3(v)
	set("isVec3", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(isVec3(args[0]))}, nil
	})

	return t
}
