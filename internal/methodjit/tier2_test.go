//go:build darwin && arm64

// tier2_test.go tests the Tier 2 memory-to-memory ARM64 code emitter.
// For every test case, both the IR interpreter (oracle) and Tier 2 JIT
// must produce identical results. Additionally, the Tier 2 result must
// match the VM interpreter.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// --- Helpers ---

// tier2Pipeline runs the full Tier 2 pipeline: BuildGraph -> TypeSpec -> ConstProp -> DCE -> Tier2Compile.
func tier2Pipeline(t *testing.T, src string) (*Tier2CompiledFunc, *Function) {
	t.Helper()
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	errs := Validate(fn)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Logf("validate: %v", e)
		}
		t.Fatal("IR validation failed before passes")
	}
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)

	cf, err := Tier2Compile(fn)
	if err != nil {
		t.Logf("IR:\n%s", Print(fn))
		t.Fatalf("Tier2Compile failed: %v", err)
	}
	return cf, fn
}

// tier2Run executes a Tier 2 compiled function and returns the result.
func tier2Run(t *testing.T, cf *Tier2CompiledFunc, args ...runtime.Value) []runtime.Value {
	t.Helper()
	results, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Tier2 execute failed: %v", err)
	}
	return results
}

// tier2Check compiles, runs via IR interpreter and Tier 2 JIT, and asserts equal results.
func tier2Check(t *testing.T, src string, args []runtime.Value) {
	t.Helper()

	// Build IR and run interpreter (oracle).
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)

	interpResult, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}

	// Run through Tier 2 JIT.
	cf, _ := tier2Pipeline(t, src)
	jitResult := tier2Run(t, cf, args...)

	// Compare.
	if len(interpResult) == 0 && len(jitResult) == 0 {
		return
	}
	if len(interpResult) == 0 {
		interpResult = []runtime.Value{runtime.NilValue()}
	}
	if len(jitResult) == 0 {
		jitResult = []runtime.Value{runtime.NilValue()}
	}
	assertValuesEqual(t, "tier2 vs interp", jitResult[0], interpResult[0])

	// Also compare with VM.
	vmResult := runVM(t, src, args)
	if len(vmResult) > 0 {
		assertValuesEqual(t, "tier2 vs VM", jitResult[0], vmResult[0])
	}
}

func intArg(v int64) runtime.Value  { return runtime.IntValue(v) }
func floatArg(v float64) runtime.Value { return runtime.FloatValue(v) }

// --- Test Cases ---

func TestTier2_ConstInt(t *testing.T) {
	src := `func f() { return 42 }`
	tier2Check(t, src, nil)
}

func TestTier2_ConstFloat(t *testing.T) {
	src := `func f() { return 3.14 }`
	tier2Check(t, src, nil)
}

func TestTier2_ConstBool(t *testing.T) {
	src := `func f() { return true }`
	tier2Check(t, src, nil)
}

func TestTier2_ConstNil(t *testing.T) {
	src := `func f() { return nil }`
	tier2Check(t, src, nil)
}

func TestTier2_AddInt(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	tier2Check(t, src, []runtime.Value{intArg(3), intArg(4)})
}

func TestTier2_SubInt(t *testing.T) {
	src := `func f(a, b) { return a - b }`
	tier2Check(t, src, []runtime.Value{intArg(10), intArg(3)})
}

func TestTier2_MulInt(t *testing.T) {
	src := `func f(a, b) { return a * b }`
	tier2Check(t, src, []runtime.Value{intArg(6), intArg(7)})
}

func TestTier2_ModInt(t *testing.T) {
	src := `func f(a, b) { return a % b }`
	tier2Check(t, src, []runtime.Value{intArg(17), intArg(5)})
}

func TestTier2_NegInt(t *testing.T) {
	src := `func f(a) { return -a }`
	tier2Check(t, src, []runtime.Value{intArg(42)})
}

func TestTier2_AddFloat(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	tier2Check(t, src, []runtime.Value{floatArg(1.5), floatArg(2.5)})
}

func TestTier2_SubFloat(t *testing.T) {
	src := `func f(a, b) { return a - b }`
	tier2Check(t, src, []runtime.Value{floatArg(5.0), floatArg(2.0)})
}

func TestTier2_MulFloat(t *testing.T) {
	src := `func f(a, b) { return a * b }`
	tier2Check(t, src, []runtime.Value{floatArg(3.0), floatArg(4.0)})
}

func TestTier2_DivFloat(t *testing.T) {
	src := `func f(a, b) { return a / b }`
	tier2Check(t, src, []runtime.Value{floatArg(10.0), floatArg(3.0)})
}

func TestTier2_DivInt(t *testing.T) {
	src := `func f(a, b) { return a / b }`
	tier2Check(t, src, []runtime.Value{intArg(10), intArg(3)})
}

func TestTier2_Not(t *testing.T) {
	src := `func f(a) { return !a }`
	tier2Check(t, src, []runtime.Value{intArg(0)})
	tier2Check(t, src, []runtime.Value{intArg(1)})
}

func TestTier2_IfElse(t *testing.T) {
	src := `
func f(n) {
	if n < 2 {
		return n
	} else {
		return n * 2
	}
}
`
	tier2Check(t, src, []runtime.Value{intArg(1)})
	tier2Check(t, src, []runtime.Value{intArg(5)})
}

func TestTier2_ForLoop(t *testing.T) {
	src := `
func f(n) {
	sum := 0
	for i := 1; i <= n; i = i + 1 {
		sum = sum + i
	}
	return sum
}
`
	tier2Check(t, src, []runtime.Value{intArg(10)})
	tier2Check(t, src, []runtime.Value{intArg(100)})
}

func TestTier2_Comparison(t *testing.T) {
	src := `func f(a, b) { if a == b { return 1 } else { return 0 } }`
	tier2Check(t, src, []runtime.Value{intArg(5), intArg(5)})
	tier2Check(t, src, []runtime.Value{intArg(5), intArg(6)})
}

func TestTier2_LtInt(t *testing.T) {
	src := `func f(a, b) { if a < b { return 1 } else { return 0 } }`
	tier2Check(t, src, []runtime.Value{intArg(3), intArg(5)})
	tier2Check(t, src, []runtime.Value{intArg(5), intArg(3)})
}

func TestTier2_LeInt(t *testing.T) {
	src := `func f(a, b) { if a <= b { return 1 } else { return 0 } }`
	tier2Check(t, src, []runtime.Value{intArg(5), intArg(5)})
	tier2Check(t, src, []runtime.Value{intArg(6), intArg(5)})
}

func TestTier2_Identity(t *testing.T) {
	// Simple passthrough: return the argument.
	src := `func f(a) { return a }`
	tier2Check(t, src, []runtime.Value{intArg(99)})
	tier2Check(t, src, []runtime.Value{floatArg(3.14)})
}

func TestTier2_MultipleReturns(t *testing.T) {
	// The IR only returns one value from slot 0. Test that.
	src := `func f(a, b) { return a + b }`
	tier2Check(t, src, []runtime.Value{intArg(100), intArg(200)})
}

func TestTier2_SumLoop(t *testing.T) {
	// More complex loop: sum 1 to 10000.
	src := `
func f(n) {
	sum := 0
	for i := 1; i <= n; i = i + 1 {
		sum = sum + i
	}
	return sum
}
`
	tier2Check(t, src, []runtime.Value{intArg(10000)})
}

func TestTier2_NestedBranch(t *testing.T) {
	src := `
func f(a) {
	if a > 0 {
		if a > 10 {
			return 3
		} else {
			return 2
		}
	} else {
		return 1
	}
}
`
	tier2Check(t, src, []runtime.Value{intArg(-5)})
	tier2Check(t, src, []runtime.Value{intArg(5)})
	tier2Check(t, src, []runtime.Value{intArg(15)})
}

// --- Raw float mode tests ---
// These test that type-specialized float operations keep values in FPRs.

func TestTier2_FloatChainedOps(t *testing.T) {
	// Chained float ops: (a + b) * c - d. Tests that intermediate results
	// stay in FPRs across multiple operations without GPR round-trips.
	src := `func f(a, b, c, d) { return (a + b) * c - d }`
	tier2Check(t, src, []runtime.Value{
		floatArg(1.5), floatArg(2.5), floatArg(3.0), floatArg(1.0),
	})
}

func TestTier2_FloatNeg(t *testing.T) {
	src := `func f(a) { return -a }`
	tier2Check(t, src, []runtime.Value{floatArg(3.14)})
}

func TestTier2_FloatCompare(t *testing.T) {
	src := `func f(a, b) { if a < b { return 1 } else { return 0 } }`
	tier2Check(t, src, []runtime.Value{floatArg(1.5), floatArg(2.5)})
	tier2Check(t, src, []runtime.Value{floatArg(3.5), floatArg(2.5)})
}

func TestTier2_FloatLoop(t *testing.T) {
	// Float accumulation loop: tests float phis in loop headers.
	src := `
func f(n) {
	sum := 0.0
	for i := 0; i < n; i = i + 1 {
		sum = sum + 1.5
	}
	return sum
}
`
	tier2Check(t, src, []runtime.Value{intArg(0)})
	tier2Check(t, src, []runtime.Value{intArg(1)})
	tier2Check(t, src, []runtime.Value{intArg(4)})
	tier2Check(t, src, []runtime.Value{intArg(100)})
}

func TestTier2_FloatConstExpr(t *testing.T) {
	// Float constant expression: tests emitConstFloat raw mode.
	src := `func f() { return 3.14 + 2.86 }`
	tier2Check(t, src, nil)
}

func TestTier2_FloatDiv(t *testing.T) {
	// Float division with type-specialized operands.
	src := `func f(a, b) { return a / b }`
	tier2Check(t, src, []runtime.Value{floatArg(10.0), floatArg(3.0)})
	tier2Check(t, src, []runtime.Value{floatArg(7.5), floatArg(2.5)})
}

func TestTier2_FloatMandelbrotSmall(t *testing.T) {
	// Miniature mandelbrot: exercises float mul, sub, add, compare in a loop.
	src := `
func f(size) {
	count := 0
	for y := 0; y < size; y = y + 1 {
		ci := 2.0 * y / size - 1.0
		for x := 0; x < size; x = x + 1 {
			cr := 2.0 * x / size - 1.5
			zr := 0.0
			zi := 0.0
			escaped := false
			for iter := 0; iter < 50; iter = iter + 1 {
				tr := zr * zr - zi * zi + cr
				ti := 2.0 * zr * zi + ci
				zr = tr
				zi = ti
				if zr * zr + zi * zi > 4.0 {
					escaped = true
					break
				}
			}
			if !escaped { count = count + 1 }
		}
	}
	return count
}
`
	tier2Check(t, src, []runtime.Value{intArg(3)})
}
