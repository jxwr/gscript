package runtime

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
)

// runWithVec parses and executes source code with vec and color libs pre-registered.
func runWithVec(t *testing.T, src string) *Interpreter {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	interp := New()
	interp.globals.Define("vec", TableValue(buildVecLib()))
	interp.globals.Define("color", TableValue(buildColorLib()))
	if err := interp.Exec(prog); err != nil {
		t.Fatalf("exec error: %v", err)
	}
	return interp
}

// floatClose returns true if a and b are within epsilon of each other.
func floatClose(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// ==================================================================
// Vec2 constructor tests
// ==================================================================

func TestVec2Create(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(3, 4)
		rx := v.x
		ry := v.y
		rt := v._type
	`)
	rx := interp.GetGlobal("rx")
	ry := interp.GetGlobal("ry")
	rt := interp.GetGlobal("rt")
	if rx.Number() != 3 {
		t.Errorf("expected x=3, got %v", rx)
	}
	if ry.Number() != 4 {
		t.Errorf("expected y=4, got %v", ry)
	}
	if rt.Str() != "vec2" {
		t.Errorf("expected _type='vec2', got %v", rt)
	}
}

func TestVec2Zero(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.zero2()
		rx := v.x
		ry := v.y
	`)
	if interp.GetGlobal("rx").Number() != 0 {
		t.Errorf("expected x=0")
	}
	if interp.GetGlobal("ry").Number() != 0 {
		t.Errorf("expected y=0")
	}
}

func TestVec2One(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.one2()
		rx := v.x
		ry := v.y
	`)
	if interp.GetGlobal("rx").Number() != 1 {
		t.Errorf("expected x=1")
	}
	if interp.GetGlobal("ry").Number() != 1 {
		t.Errorf("expected y=1")
	}
}

func TestVec2Up(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.up()
		rx := v.x
		ry := v.y
	`)
	if interp.GetGlobal("rx").Number() != 0 {
		t.Errorf("expected x=0")
	}
	if interp.GetGlobal("ry").Number() != 1 {
		t.Errorf("expected y=1")
	}
}

func TestVec2Right(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.right()
		rx := v.x
		ry := v.y
	`)
	if interp.GetGlobal("rx").Number() != 1 {
		t.Errorf("expected x=1")
	}
	if interp.GetGlobal("ry").Number() != 0 {
		t.Errorf("expected y=0")
	}
}

// ==================================================================
// Vec2 operator tests
// ==================================================================

func TestVec2Add(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(1, 2)
		v2 := vec.vec2(3, 4)
		v3 := v1 + v2
		rx := v3.x
		ry := v3.y
	`)
	if interp.GetGlobal("rx").Number() != 4 {
		t.Errorf("expected x=4, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 6 {
		t.Errorf("expected y=6, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2Sub(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(5, 7)
		v2 := vec.vec2(2, 3)
		v3 := v1 - v2
		rx := v3.x
		ry := v3.y
	`)
	if interp.GetGlobal("rx").Number() != 3 {
		t.Errorf("expected x=3, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 4 {
		t.Errorf("expected y=4, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2MulScalar(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(3, 4)
		v2 := v1 * 2
		rx := v2.x
		ry := v2.y
	`)
	if interp.GetGlobal("rx").Number() != 6 {
		t.Errorf("expected x=6, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 8 {
		t.Errorf("expected y=8, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2ScalarMul(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(3, 4)
		v2 := 2 * v1
		rx := v2.x
		ry := v2.y
	`)
	if interp.GetGlobal("rx").Number() != 6 {
		t.Errorf("expected x=6, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 8 {
		t.Errorf("expected y=8, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2DivScalar(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(6, 8)
		v2 := v1 / 2
		rx := v2.x
		ry := v2.y
	`)
	if interp.GetGlobal("rx").Number() != 3 {
		t.Errorf("expected x=3, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 4 {
		t.Errorf("expected y=4, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2Unm(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(3, -4)
		v2 := -v1
		rx := v2.x
		ry := v2.y
	`)
	if interp.GetGlobal("rx").Number() != -3 {
		t.Errorf("expected x=-3, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 4 {
		t.Errorf("expected y=4, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2Eq(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(3, 4)
		v2 := vec.vec2(3, 4)
		v3 := vec.vec2(1, 2)
		r1 := v1 == v2
		r2 := v1 == v3
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected v1 == v2 to be true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected v1 == v3 to be false")
	}
}

// ==================================================================
// Vec2 utility function tests
// ==================================================================

func TestVec2Dot(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(1, 2)
		v2 := vec.vec2(3, 4)
		result := vec.dot2(v1, v2)
	`)
	// 1*3 + 2*4 = 11
	v := interp.GetGlobal("result")
	if v.Number() != 11 {
		t.Errorf("expected dot=11, got %v", v)
	}
}

func TestVec2Length(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(3, 4)
		result := vec.length2(v)
	`)
	v := interp.GetGlobal("result")
	if !floatClose(v.Number(), 5.0, 1e-10) {
		t.Errorf("expected length=5, got %v", v)
	}
}

func TestVec2LengthSq(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(3, 4)
		result := vec.lengthSq2(v)
	`)
	v := interp.GetGlobal("result")
	if v.Number() != 25 {
		t.Errorf("expected lengthSq=25, got %v", v)
	}
}

func TestVec2Normalize(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(3, 4)
		n := vec.normalize2(v)
		rx := n.x
		ry := n.y
	`)
	rx := interp.GetGlobal("rx").Number()
	ry := interp.GetGlobal("ry").Number()
	if !floatClose(rx, 3.0/5.0, 1e-10) {
		t.Errorf("expected x=0.6, got %v", rx)
	}
	if !floatClose(ry, 4.0/5.0, 1e-10) {
		t.Errorf("expected y=0.8, got %v", ry)
	}
}

func TestVec2Angle(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(1, 0)
		result := vec.angle2(v)
	`)
	v := interp.GetGlobal("result")
	if !floatClose(v.Number(), 0, 1e-10) {
		t.Errorf("expected angle=0, got %v", v)
	}
}

func TestVec2Rotate(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(1, 0)
		r := vec.rotate2(v, math.pi / 2)
		rx := r.x
		ry := r.y
	`)
	rx := interp.GetGlobal("rx").Number()
	ry := interp.GetGlobal("ry").Number()
	if !floatClose(rx, 0, 1e-10) {
		t.Errorf("expected x~0, got %v", rx)
	}
	if !floatClose(ry, 1, 1e-10) {
		t.Errorf("expected y~1, got %v", ry)
	}
}

func TestVec2Lerp(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(0, 0)
		v2 := vec.vec2(10, 20)
		v3 := vec.lerp2(v1, v2, 0.5)
		rx := v3.x
		ry := v3.y
	`)
	if interp.GetGlobal("rx").Number() != 5 {
		t.Errorf("expected x=5, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 10 {
		t.Errorf("expected y=10, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2Dist(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(0, 0)
		v2 := vec.vec2(3, 4)
		result := vec.dist2(v1, v2)
	`)
	v := interp.GetGlobal("result")
	if !floatClose(v.Number(), 5, 1e-10) {
		t.Errorf("expected dist=5, got %v", v)
	}
}

func TestVec2DistSq(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec2(0, 0)
		v2 := vec.vec2(3, 4)
		result := vec.distSq2(v1, v2)
	`)
	v := interp.GetGlobal("result")
	if v.Number() != 25 {
		t.Errorf("expected distSq=25, got %v", v)
	}
}

func TestVec2Reflect(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(1, -1)
		n := vec.vec2(0, 1)
		r := vec.reflect2(v, n)
		rx := r.x
		ry := r.y
	`)
	rx := interp.GetGlobal("rx").Number()
	ry := interp.GetGlobal("ry").Number()
	// reflect(v, n) = v - 2*dot(v,n)*n
	// dot(1,-1, 0,1) = -1
	// v - 2*(-1)*(0,1) = (1,-1) - (0,-2) = (1,1)
	if !floatClose(rx, 1, 1e-10) {
		t.Errorf("expected rx=1, got %v", rx)
	}
	if !floatClose(ry, 1, 1e-10) {
		t.Errorf("expected ry=1, got %v", ry)
	}
}

func TestVec2Perp(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(3, 4)
		p := vec.perp2(v)
		rx := p.x
		ry := p.y
	`)
	if interp.GetGlobal("rx").Number() != -4 {
		t.Errorf("expected x=-4, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 3 {
		t.Errorf("expected y=3, got %v", interp.GetGlobal("ry"))
	}
}

func TestVec2IsVec2(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(1, 2)
		r1 := vec.isVec2(v)
		r2 := vec.isVec2(42)
		r3 := vec.isVec2({x: 1, y: 2})
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected isVec2(vec2) to be true")
	}
	if interp.GetGlobal("r2").Truthy() {
		t.Errorf("expected isVec2(42) to be false")
	}
	if interp.GetGlobal("r3").Truthy() {
		t.Errorf("expected isVec2({x:1,y:2}) to be false (no _type)")
	}
}

func TestVec2Clamp(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec2(5, -3)
		clamped := vec.clamp2(v, 0, 4)
		rx := clamped.x
		ry := clamped.y
	`)
	if interp.GetGlobal("rx").Number() != 4 {
		t.Errorf("expected x=4, got %v", interp.GetGlobal("rx"))
	}
	if interp.GetGlobal("ry").Number() != 0 {
		t.Errorf("expected y=0, got %v", interp.GetGlobal("ry"))
	}
}

// ==================================================================
// Vec3 tests
// ==================================================================

func TestVec3Create(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec3(1, 2, 3)
		rx := v.x
		ry := v.y
		rz := v.z
		rt := v._type
	`)
	if interp.GetGlobal("rx").Number() != 1 {
		t.Errorf("expected x=1")
	}
	if interp.GetGlobal("ry").Number() != 2 {
		t.Errorf("expected y=2")
	}
	if interp.GetGlobal("rz").Number() != 3 {
		t.Errorf("expected z=3")
	}
	if interp.GetGlobal("rt").Str() != "vec3" {
		t.Errorf("expected _type='vec3'")
	}
}

func TestVec3Zero(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.zero3()
		rx := v.x
		ry := v.y
		rz := v.z
	`)
	if interp.GetGlobal("rx").Number() != 0 || interp.GetGlobal("ry").Number() != 0 || interp.GetGlobal("rz").Number() != 0 {
		t.Errorf("expected zero3 to be (0,0,0)")
	}
}

func TestVec3One(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.one3()
		rx := v.x
		ry := v.y
		rz := v.z
	`)
	if interp.GetGlobal("rx").Number() != 1 || interp.GetGlobal("ry").Number() != 1 || interp.GetGlobal("rz").Number() != 1 {
		t.Errorf("expected one3 to be (1,1,1)")
	}
}

func TestVec3Add(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(1, 2, 3)
		v2 := vec.vec3(4, 5, 6)
		v3 := v1 + v2
		rx := v3.x
		ry := v3.y
		rz := v3.z
	`)
	if interp.GetGlobal("rx").Number() != 5 {
		t.Errorf("expected x=5")
	}
	if interp.GetGlobal("ry").Number() != 7 {
		t.Errorf("expected y=7")
	}
	if interp.GetGlobal("rz").Number() != 9 {
		t.Errorf("expected z=9")
	}
}

func TestVec3Sub(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(5, 7, 9)
		v2 := vec.vec3(1, 2, 3)
		v3 := v1 - v2
		rx := v3.x
		ry := v3.y
		rz := v3.z
	`)
	if interp.GetGlobal("rx").Number() != 4 {
		t.Errorf("expected x=4")
	}
	if interp.GetGlobal("ry").Number() != 5 {
		t.Errorf("expected y=5")
	}
	if interp.GetGlobal("rz").Number() != 6 {
		t.Errorf("expected z=6")
	}
}

func TestVec3MulScalar(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(1, 2, 3)
		v2 := v1 * 3
		rx := v2.x
		ry := v2.y
		rz := v2.z
	`)
	if interp.GetGlobal("rx").Number() != 3 {
		t.Errorf("expected x=3")
	}
	if interp.GetGlobal("ry").Number() != 6 {
		t.Errorf("expected y=6")
	}
	if interp.GetGlobal("rz").Number() != 9 {
		t.Errorf("expected z=9")
	}
}

func TestVec3DivScalar(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(6, 9, 12)
		v2 := v1 / 3
		rx := v2.x
		ry := v2.y
		rz := v2.z
	`)
	if interp.GetGlobal("rx").Number() != 2 {
		t.Errorf("expected x=2")
	}
	if interp.GetGlobal("ry").Number() != 3 {
		t.Errorf("expected y=3")
	}
	if interp.GetGlobal("rz").Number() != 4 {
		t.Errorf("expected z=4")
	}
}

func TestVec3Unm(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(1, -2, 3)
		v2 := -v1
		rx := v2.x
		ry := v2.y
		rz := v2.z
	`)
	if interp.GetGlobal("rx").Number() != -1 {
		t.Errorf("expected x=-1")
	}
	if interp.GetGlobal("ry").Number() != 2 {
		t.Errorf("expected y=2")
	}
	if interp.GetGlobal("rz").Number() != -3 {
		t.Errorf("expected z=-3")
	}
}

func TestVec3Eq(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(1, 2, 3)
		v2 := vec.vec3(1, 2, 3)
		v3 := vec.vec3(1, 2, 4)
		r1 := v1 == v2
		r2 := v1 == v3
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected v1 == v2 to be true")
	}
	if interp.GetGlobal("r2").Bool() {
		t.Errorf("expected v1 == v3 to be false")
	}
}

func TestVec3Dot(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(1, 2, 3)
		v2 := vec.vec3(4, 5, 6)
		result := vec.dot3(v1, v2)
	`)
	// 1*4 + 2*5 + 3*6 = 32
	v := interp.GetGlobal("result")
	if v.Number() != 32 {
		t.Errorf("expected dot3=32, got %v", v)
	}
}

func TestVec3Cross(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(1, 0, 0)
		v2 := vec.vec3(0, 1, 0)
		v3 := vec.cross3(v1, v2)
		rx := v3.x
		ry := v3.y
		rz := v3.z
	`)
	// (1,0,0) x (0,1,0) = (0,0,1)
	if interp.GetGlobal("rx").Number() != 0 {
		t.Errorf("expected x=0")
	}
	if interp.GetGlobal("ry").Number() != 0 {
		t.Errorf("expected y=0")
	}
	if interp.GetGlobal("rz").Number() != 1 {
		t.Errorf("expected z=1")
	}
}

func TestVec3Length(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec3(1, 2, 2)
		result := vec.length3(v)
	`)
	v := interp.GetGlobal("result")
	if !floatClose(v.Number(), 3, 1e-10) {
		t.Errorf("expected length3=3, got %v", v)
	}
}

func TestVec3Normalize(t *testing.T) {
	interp := runWithVec(t, `
		v := vec.vec3(0, 0, 5)
		n := vec.normalize3(v)
		rx := n.x
		ry := n.y
		rz := n.z
	`)
	if !floatClose(interp.GetGlobal("rx").Number(), 0, 1e-10) {
		t.Errorf("expected x=0")
	}
	if !floatClose(interp.GetGlobal("ry").Number(), 0, 1e-10) {
		t.Errorf("expected y=0")
	}
	if !floatClose(interp.GetGlobal("rz").Number(), 1, 1e-10) {
		t.Errorf("expected z=1")
	}
}

func TestVec3Lerp(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(0, 0, 0)
		v2 := vec.vec3(10, 20, 30)
		v3 := vec.lerp3(v1, v2, 0.5)
		rx := v3.x
		ry := v3.y
		rz := v3.z
	`)
	if interp.GetGlobal("rx").Number() != 5 {
		t.Errorf("expected x=5")
	}
	if interp.GetGlobal("ry").Number() != 10 {
		t.Errorf("expected y=10")
	}
	if interp.GetGlobal("rz").Number() != 15 {
		t.Errorf("expected z=15")
	}
}

func TestVec3Dist(t *testing.T) {
	interp := runWithVec(t, `
		v1 := vec.vec3(0, 0, 0)
		v2 := vec.vec3(1, 2, 2)
		result := vec.dist3(v1, v2)
	`)
	v := interp.GetGlobal("result")
	if !floatClose(v.Number(), 3, 1e-10) {
		t.Errorf("expected dist3=3, got %v", v)
	}
}

func TestVec3IsVec3(t *testing.T) {
	interp := runWithVec(t, `
		v2 := vec.vec2(1, 2)
		v3 := vec.vec3(1, 2, 3)
		r1 := vec.isVec3(v3)
		r2 := vec.isVec3(v2)
		r3 := vec.isVec3(42)
	`)
	if !interp.GetGlobal("r1").Bool() {
		t.Errorf("expected isVec3(vec3) to be true")
	}
	if interp.GetGlobal("r2").Truthy() {
		t.Errorf("expected isVec3(vec2) to be false")
	}
	if interp.GetGlobal("r3").Truthy() {
		t.Errorf("expected isVec3(42) to be false")
	}
}
