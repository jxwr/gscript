//go:build darwin && arm64

package tests_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	gs "github.com/gscript/gscript/gscript"
)

// assertVMEqualsJIT runs the same source in bytecode-VM mode (no JIT) and in
// JIT mode, then compares the named global variables. The JIT execution is
// wrapped in a goroutine with a timeout to catch hangs.
func assertVMEqualsJIT(t *testing.T, src string, vars ...string) {
	t.Helper()

	// --- VM (bytecode interpreter, no JIT) ---
	vmInstance := gs.New(gs.WithVM())
	if err := vmInstance.Exec(src); err != nil {
		t.Fatalf("VM exec error: %v", err)
	}

	// --- JIT (with trace compilation) ---
	type jitResult struct {
		vm       *gs.VM
		err      error
		panicVal interface{}
	}
	done := make(chan jitResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- jitResult{panicVal: r}
			}
		}()
		jitVM := gs.New(gs.WithJIT())
		err := jitVM.Exec(src)
		done <- jitResult{vm: jitVM, err: err}
	}()

	var jitInstance *gs.VM
	select {
	case res := <-done:
		if res.panicVal != nil {
			t.Fatalf("JIT panic: %v", res.panicVal)
		}
		if res.err != nil {
			t.Fatalf("JIT exec error: %v", res.err)
		}
		jitInstance = res.vm
	case <-time.After(5 * time.Second):
		t.Fatal("JIT execution hung (timeout)")
		return
	}

	// --- Compare specified globals ---
	for _, varName := range vars {
		vmVal, vmErr := vmInstance.Get(varName)
		jitVal, jitErr := jitInstance.Get(varName)
		if vmErr != nil {
			t.Fatalf("VM Get(%q) error: %v", varName, vmErr)
		}
		if jitErr != nil {
			t.Fatalf("JIT Get(%q) error: %v", varName, jitErr)
		}

		if !valuesEqual(vmVal, jitVal) {
			t.Errorf("var %q: VM=%v (%T), JIT=%v (%T)", varName, vmVal, vmVal, jitVal, jitVal)
		}
	}
}

// valuesEqual compares two values with tolerance for floats.
func valuesEqual(a, b interface{}) bool {
	fa, aIsFloat := toFloat(a)
	fb, bIsFloat := toFloat(b)
	if aIsFloat && bIsFloat {
		if fa == fb {
			return true
		}
		// Relative tolerance for floating point
		diff := math.Abs(fa - fb)
		mag := math.Max(math.Abs(fa), math.Abs(fb))
		if mag == 0 {
			return diff < 1e-12
		}
		return diff/mag < 1e-9
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

// ============================================================================
// Invariant 1: Value correctness (single trace execution)
// ============================================================================

func TestInv1_IntArith(t *testing.T) {
	assertVMEqualsJIT(t, `result := 0; for i:=1;i<=100;i++ { result = result + i }`, "result")
}

func TestInv1_FloatArith(t *testing.T) {
	assertVMEqualsJIT(t, `result := 0.0; for i:=1;i<=100;i++ { result = result + 0.01 * i }`, "result")
}

func TestInv1_IntMul(t *testing.T) {
	assertVMEqualsJIT(t, `result := 1; for i:=1;i<=10;i++ { result = result * i }`, "result")
}

func TestInv1_FloatDiv(t *testing.T) {
	assertVMEqualsJIT(t, `result := 1024.0; for i:=1;i<=10;i++ { result = result / 2.0 }`, "result")
}

func TestInv1_IntMod(t *testing.T) {
	assertVMEqualsJIT(t, `result := 0; for i:=1;i<=100;i++ { result = result + i % 7 }`, "result")
}

func TestInv1_MixedIntFloat(t *testing.T) {
	assertVMEqualsJIT(t, `result := 0.0; for i:=1;i<=50;i++ { result = result + i * 0.1 }`, "result")
}

func TestInv1_Negation(t *testing.T) {
	assertVMEqualsJIT(t, `result := 0; for i:=1;i<=10;i++ { result = result + (-i) }`, "result")
}

func TestInv1_FloatMulAccum(t *testing.T) {
	assertVMEqualsJIT(t, `result := 1.0; for i:=1;i<=20;i++ { result = result * 1.05 }`, "result")
}

// ============================================================================
// Invariant 2: Memory consistency at exit points
// ============================================================================

// Loop-done: all modified slots written back correctly

func TestInv2_LoopDone_SingleVar(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0; for i:=1;i<=100;i++ { s=s+i }; result:=s`, "result")
}

func TestInv2_LoopDone_MultiVar(t *testing.T) {
	assertVMEqualsJIT(t, `a:=0; b:=0; for i:=1;i<=50;i++ { a=a+i; b=b+i*i }; ra:=a; rb:=b`, "ra", "rb")
}

func TestInv2_LoopDone_FloatVar(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0.0; for i:=1;i<=100;i++ { s=s+0.5 }; result:=s`, "result")
}

// Side-exit: memory correct at the guard's bytecode PC

func TestInv2_SideExit_SimpleIf(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=0;i<100;i++ { if i==50 { c=c+1 } }; result:=c`, "result")
}

func TestInv2_SideExit_MultipleExits(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=0;i<100;i++ { if i==20 || i==40 || i==60 || i==80 { c=c+1 } }; result:=c`, "result")
}

func TestInv2_SideExit_ModCondition(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=0;i<100;i++ { if i%7==0 { c=c+1 } }; result:=c`, "result")
}

func TestInv2_SideExit_LTCondition(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=0;i<100;i++ { if i>75 { c=c+1 } }; result:=c`, "result")
}

// ============================================================================
// Invariant 3: Control flow correctness
// ============================================================================

// Break exits loop correctly

func TestInv3_Break_Simple(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=1;i<=1000;i++ { if i>10 { break }; c=c+1 }; result:=c`, "result")
}

func TestInv3_Break_FloatCondition(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; s:=0.0; for i:=1;i<=1000;i++ { s=s+0.1; if s>5.0 { break }; c=c+1 }; result:=c`, "result")
}

func TestInv3_Break_InNestedLoop(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=1;i<=10;i++ { for j:=1;j<=100;j++ { if j>i*3 { break } }; c=c+1 }; result:=c`, "result")
}

// Nested loops

func TestInv3_NestedLoop_Simple(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0; for i:=1;i<=10;i++ { for j:=1;j<=10;j++ { s=s+1 } }; result:=s`, "result")
}

func TestInv3_NestedLoop_DependentBounds(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0; for i:=1;i<=10;i++ { for j:=1;j<=i;j++ { s=s+1 } }; result:=s`, "result")
}

func TestInv3_NestedLoop_FloatInner(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0.0; for i:=1;i<=5;i++ { for j:=1;j<=5;j++ { s=s+i*0.1+j*0.01 } }; result:=s`, "result")
}

// ============================================================================
// Invariant 4: Accumulated correctness (enter-exit-reenter cycles)
// ============================================================================

func TestInv4_Reenter_CounterIncrement(t *testing.T) {
	// trace records "i%10!=0" path. Each time i%10==0, side-exit -> interpreter does c++ -> re-enter
	assertVMEqualsJIT(t, `c:=0; for i:=0;i<100;i++ { if i%10==0 { c=c+1 } }; result:=c`, "result")
}

func TestInv4_Reenter_AccumPlusCondition(t *testing.T) {
	// mix: trace does accumulation, side-exits on condition, interpreter does different accumulation
	assertVMEqualsJIT(t, `a:=0; b:=0; for i:=1;i<=100;i++ { a=a+i; if i%20==0 { b=b+a } }; ra:=a; rb:=b`, "ra", "rb")
}

func TestInv4_Reenter_FibonacciLike(t *testing.T) {
	// loop-carried values that change every iteration
	assertVMEqualsJIT(t, `a:=0; b:=1; for i:=1;i<=30;i++ { c:=a+b; a=b; b=c }; result:=b`, "result")
}

func TestInv4_Mandelbrot_Mini(t *testing.T) {
	// The hardest pattern: nested loop + float + break + accumulator across outer iterations
	src := `
count := 0
for y := 0; y < 5; y++ {
    for x := 0; x < 5; x++ {
        cr := (x * 2.0 / 5) - 1.5
        ci := (y * 2.0 / 5) - 1.0
        zr := 0.0
        zi := 0.0
        escaped := false
        for iter := 0; iter < 20; iter++ {
            zr2 := zr * zr
            zi2 := zi * zi
            if zr2 + zi2 > 4.0 {
                escaped = true
                break
            }
            zi = 2.0 * zr * zi + ci
            zr = zr2 - zi2 + cr
        }
        if !escaped {
            count = count + 1
        }
    }
}
result := count`
	assertVMEqualsJIT(t, src, "result")
}

func TestInv4_Sieve_Mini(t *testing.T) {
	// Array read + conditional + accumulator
	src := `
n := 100
is_prime := {}
for i := 2; i <= n; i++ { is_prime[i] = true }
for i := 2; i * i <= n; i++ {
    if is_prime[i] {
        j := i * i
        for j <= n {
            is_prime[j] = false
            j = j + i
        }
    }
}
count := 0
for i := 2; i <= n; i++ {
    if is_prime[i] { count = count + 1 }
}
result := count`
	assertVMEqualsJIT(t, src, "result")
}

func TestInv4_NBody_Mini(t *testing.T) {
	// Field access + float arithmetic + nested loop + accumulation
	src := `
bodies := {
    {x: 0.0, y: 0.0, vx: 0.0, vy: 0.0, mass: 10.0},
    {x: 1.0, y: 0.0, vx: 0.0, vy: 0.5, mass: 1.0},
}
for step := 1; step <= 50; step++ {
    n := #bodies
    for i := 1; i <= n; i++ {
        bi := bodies[i]
        for j := i + 1; j <= n; j++ {
            bj := bodies[j]
            dx := bi.x - bj.x
            dy := bi.y - bj.y
            dsq := dx*dx + dy*dy + 0.001
            dist := math.sqrt(dsq)
            mag := 0.01 / (dsq * dist)
            bi.vx = bi.vx - dx * bj.mass * mag
            bi.vy = bi.vy - dy * bj.mass * mag
            bj.vx = bj.vx + dx * bi.mass * mag
            bj.vy = bj.vy + dy * bi.mass * mag
        }
    }
    for i := 1; i <= n; i++ {
        b := bodies[i]
        b.x = b.x + 0.01 * b.vx
        b.y = b.y + 0.01 * b.vy
    }
}
result := bodies[2].x`
	assertVMEqualsJIT(t, src, "result")
}

// ============================================================================
// Invariant 5: Type safety
// ============================================================================

func TestInv5_IntGuard(t *testing.T) {
	// Loop computes with ints, trace should work
	assertVMEqualsJIT(t, `s:=0; for i:=1;i<=50;i++ { s=s+i }; result:=s`, "result")
}

func TestInv5_FloatGuard(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0.0; for i:=1;i<=50;i++ { s=s+1.5 }; result:=s`, "result")
}

func TestInv5_TableGuard(t *testing.T) {
	assertVMEqualsJIT(t, `t:={x:1}; s:=0; for i:=1;i<=50;i++ { s=s+t.x; t.x=t.x+1 }; result:=s`, "result")
}

func TestInv5_BoolGuard(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; flag:=true; for i:=1;i<=50;i++ { if flag { c=c+1 } }; result:=c`, "result")
}

// ============================================================================
// Stress tests (dimension combinations)
// ============================================================================

func TestStress_ManyIterations(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0; for i:=1;i<=100000;i++ { s=s+i }; result:=s`, "result")
}

func TestStress_ManySideExits(t *testing.T) {
	assertVMEqualsJIT(t, `c:=0; for i:=0;i<10000;i++ { if i%3==0 { c=c+1 } }; result:=c`, "result")
}

func TestStress_DeepNesting(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0; for i:=1;i<=10;i++ { for j:=1;j<=10;j++ { for k:=1;k<=10;k++ { s=s+1 } } }; result:=s`, "result")
}

func TestStress_FloatPrecision(t *testing.T) {
	assertVMEqualsJIT(t, `s:=0.0; for i:=1;i<=10000;i++ { s=s+0.0001 }; result:=s`, "result")
}

func TestStress_FunctionCallInLoop(t *testing.T) {
	assertVMEqualsJIT(t, `func double(x) { return x * 2 }; s:=0; for i:=1;i<=100;i++ { s=s+double(i) }; result:=s`, "result")
}

func TestStress_TableReadWrite(t *testing.T) {
	assertVMEqualsJIT(t, `a:={}; for i:=1;i<=100;i++ { a[i]=i*10 }; s:=0; for i:=1;i<=100;i++ { s=s+a[i] }; result:=s`, "result")
}

func TestStress_FieldReadWrite(t *testing.T) {
	assertVMEqualsJIT(t, `p:={x:0.0,y:0.0}; for i:=1;i<=100;i++ { p.x=p.x+0.1; p.y=p.y+0.2 }; rx:=p.x; ry:=p.y`, "rx", "ry")
}

func TestStress_MatmulMini(t *testing.T) {
	src := `
n := 5
a := {}; b := {}
for i := 0; i < n; i++ {
    ar := {}; br := {}
    for j := 0; j < n; j++ {
        ar[j] = (i*n+j+1.0)/(n*n)
        br[j] = (j*n+i+1.0)/(n*n)
    }
    a[i] = ar; b[i] = br
}
s := 0.0
for i := 0; i < n; i++ {
    for j := 0; j < n; j++ {
        v := 0.0
        for k := 0; k < n; k++ {
            v = v + a[i][k] * b[k][j]
        }
        s = s + v
    }
}
result := s`
	assertVMEqualsJIT(t, src, "result")
}

// ============================================================================
// CSE correctness — repeated subexpressions
// ============================================================================

func TestInv_CSE_RepeatedSubexpr(t *testing.T) {
	// x*x appears twice per iteration — CSE should deduplicate but result stays correct
	assertVMEqualsJIT(t, `s := 0.0; for i := 1; i <= 100; i++ { x := i * 0.1; s = s + x*x + x*x }`, "s")
}

func TestInv_CSE_IntRepeatedSubexpr(t *testing.T) {
	// Integer repeated subexpression
	assertVMEqualsJIT(t, `s := 0; for i := 1; i <= 100; i++ { s = s + i*i + i*i }`, "s")
}

// ============================================================================
// GETGLOBAL native compilation tests
// ============================================================================

func TestInv_GetGlobal_Native(t *testing.T) {
	// Global variable read in loop should work correctly
	assertVMEqualsJIT(t, `
        data := {10, 20, 30, 40, 50}
        s := 0
        for i := 1; i <= 5; i++ {
            s = s + data[i]
        }
        result := s`, "result")
}

func TestInv_GetGlobal_TableFieldRead(t *testing.T) {
	// Global table + field read in loop
	assertVMEqualsJIT(t, `
        obj := {x: 1.0, y: 2.0}
        s := 0.0
        for i := 1; i <= 50; i++ {
            s = s + obj.x + obj.y
        }
        result := s`, "result")
}

func TestInv_GetGlobal_TableFieldWrite(t *testing.T) {
	// Global table + field write in loop
	assertVMEqualsJIT(t, `
        obj := {x: 0.0}
        for i := 1; i <= 50; i++ {
            obj.x = obj.x + 0.1
        }
        result := obj.x`, "result")
}

func TestInv_GetGlobal_NestedTable(t *testing.T) {
	// Global table of tables + index + field access in loop
	assertVMEqualsJIT(t, `
        data := { {x: 1.0}, {x: 2.0} }
        s := 0.0
        for i := 1; i <= 50; i++ {
            s = s + data[1].x + data[2].x
        }
        result := s`, "result")
}

func TestInv_GetGlobal_NBody_Mini(t *testing.T) {
	// nbody-like pattern: global table + field access in loop
	assertVMEqualsJIT(t, `
        bodies := {
            {x: 0.0, vx: 0.1, mass: 10.0},
            {x: 1.0, vx: -0.1, mass: 1.0},
        }
        for step := 1; step <= 50; step++ {
            b1 := bodies[1]
            b2 := bodies[2]
            dx := b1.x - b2.x
            f := b2.mass / (dx * dx + 0.01)
            b1.vx = b1.vx - dx * f * 0.01
            b2.vx = b2.vx + dx * f * 0.01
            b1.x = b1.x + b1.vx * 0.01
            b2.x = b2.x + b2.vx * 0.01
        }
        result := bodies[1].x`, "result")
}
