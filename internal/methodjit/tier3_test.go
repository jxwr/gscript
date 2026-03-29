//go:build darwin && arm64

// tier3_test.go tests the Tier 3 register-allocated ARM64 code emitter.
// Tier 3 = Tier 2 pipeline + register allocation. Every test case must produce
// the same result as the VM interpreter and the IR interpreter.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// --- Helpers ---

// tier3Pipeline runs the full Tier 3 pipeline: BuildGraph -> TypeSpec -> ConstProp -> DCE -> AllocateRegisters -> Tier3Compile.
func tier3Pipeline(t *testing.T, src string) (*Tier2CompiledFunc, *Function) {
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

	alloc := AllocateRegisters(fn)
	cf, err := Tier3Compile(fn, alloc)
	if err != nil {
		t.Logf("IR:\n%s", Print(fn))
		t.Fatalf("Tier3Compile failed: %v", err)
	}
	return cf, fn
}

// tier3Check compiles, runs via IR interpreter, Tier 2 JIT, and Tier 3 JIT,
// and asserts all produce equal results.
func tier3Check(t *testing.T, src string, args []runtime.Value) {
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

	// Run through Tier 3 JIT.
	cf, _ := tier3Pipeline(t, src)
	jitResult, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Tier3 execute failed: %v", err)
	}

	// Compare Tier 3 vs interpreter.
	if len(interpResult) == 0 {
		interpResult = []runtime.Value{runtime.NilValue()}
	}
	if len(jitResult) == 0 {
		jitResult = []runtime.Value{runtime.NilValue()}
	}
	assertValuesEqual(t, "tier3 vs interp", jitResult[0], interpResult[0])

	// Also compare with VM.
	vmResult := runVM(t, src, args)
	if len(vmResult) > 0 {
		assertValuesEqual(t, "tier3 vs VM", jitResult[0], vmResult[0])
	}
}

// --- Correctness Tests ---

func TestTier3_ConstInt(t *testing.T) {
	tier3Check(t, `func f() { return 42 }`, nil)
}

func TestTier3_ConstFloat(t *testing.T) {
	tier3Check(t, `func f() { return 3.14 }`, nil)
}

func TestTier3_ConstBool(t *testing.T) {
	tier3Check(t, `func f() { return true }`, nil)
}

func TestTier3_ConstNil(t *testing.T) {
	tier3Check(t, `func f() { return nil }`, nil)
}

func TestTier3_AddInt(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a + b }`, []runtime.Value{intArg(3), intArg(4)})
}

func TestTier3_SubInt(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a - b }`, []runtime.Value{intArg(10), intArg(3)})
}

func TestTier3_MulInt(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a * b }`, []runtime.Value{intArg(6), intArg(7)})
}

func TestTier3_ModInt(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a % b }`, []runtime.Value{intArg(17), intArg(5)})
}

func TestTier3_NegInt(t *testing.T) {
	tier3Check(t, `func f(a) { return -a }`, []runtime.Value{intArg(42)})
}

func TestTier3_AddFloat(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a + b }`, []runtime.Value{floatArg(1.5), floatArg(2.5)})
}

func TestTier3_SubFloat(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a - b }`, []runtime.Value{floatArg(5.0), floatArg(2.0)})
}

func TestTier3_MulFloat(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a * b }`, []runtime.Value{floatArg(3.0), floatArg(4.0)})
}

func TestTier3_DivFloat(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a / b }`, []runtime.Value{floatArg(10.0), floatArg(3.0)})
}

func TestTier3_DivInt(t *testing.T) {
	tier3Check(t, `func f(a, b) { return a / b }`, []runtime.Value{intArg(10), intArg(3)})
}

func TestTier3_Not(t *testing.T) {
	tier3Check(t, `func f(a) { return !a }`, []runtime.Value{intArg(0)})
	tier3Check(t, `func f(a) { return !a }`, []runtime.Value{intArg(1)})
}

func TestTier3_IfElse(t *testing.T) {
	src := `
func f(n) {
	if n < 2 {
		return n
	} else {
		return n * 2
	}
}
`
	tier3Check(t, src, []runtime.Value{intArg(1)})
	tier3Check(t, src, []runtime.Value{intArg(5)})
}

func TestTier3_ForLoop(t *testing.T) {
	src := `
func f(n) {
	sum := 0
	for i := 1; i <= n; i = i + 1 {
		sum = sum + i
	}
	return sum
}
`
	tier3Check(t, src, []runtime.Value{intArg(10)})
	tier3Check(t, src, []runtime.Value{intArg(100)})
}

func TestTier3_Comparison(t *testing.T) {
	src := `func f(a, b) { if a == b { return 1 } else { return 0 } }`
	tier3Check(t, src, []runtime.Value{intArg(5), intArg(5)})
	tier3Check(t, src, []runtime.Value{intArg(5), intArg(6)})
}

func TestTier3_LtInt(t *testing.T) {
	src := `func f(a, b) { if a < b { return 1 } else { return 0 } }`
	tier3Check(t, src, []runtime.Value{intArg(3), intArg(5)})
	tier3Check(t, src, []runtime.Value{intArg(5), intArg(3)})
}

func TestTier3_LeInt(t *testing.T) {
	src := `func f(a, b) { if a <= b { return 1 } else { return 0 } }`
	tier3Check(t, src, []runtime.Value{intArg(5), intArg(5)})
	tier3Check(t, src, []runtime.Value{intArg(6), intArg(5)})
}

func TestTier3_Identity(t *testing.T) {
	src := `func f(a) { return a }`
	tier3Check(t, src, []runtime.Value{intArg(99)})
	tier3Check(t, src, []runtime.Value{floatArg(3.14)})
}

func TestTier3_NestedBranch(t *testing.T) {
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
	tier3Check(t, src, []runtime.Value{intArg(-5)})
	tier3Check(t, src, []runtime.Value{intArg(5)})
	tier3Check(t, src, []runtime.Value{intArg(15)})
}

func TestTier3_SumLoop(t *testing.T) {
	src := `
func f(n) {
	sum := 0
	for i := 1; i <= n; i = i + 1 {
		sum = sum + i
	}
	return sum
}
`
	tier3Check(t, src, []runtime.Value{intArg(10000)})
}

func TestTier3_Sum10000(t *testing.T) {
	// Key test: sum(10000) must produce the correct result.
	src := `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	tier3Check(t, src, []runtime.Value{intArg(10000)})
}

// --- Benchmarks: Tier 3 vs Tier 2 ---

func BenchmarkTier3_Sum100(b *testing.B) {
	src := `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Tier3Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Tier3Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(100)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier2_Sum100(b *testing.B) {
	src := `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	cf, err := Tier2Compile(fn)
	if err != nil {
		b.Fatalf("Tier2Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(100)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier3_Sum10000(b *testing.B) {
	src := `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Tier3Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Tier3Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(10000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier2_Sum10000(b *testing.B) {
	src := `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	cf, err := Tier2Compile(fn)
	if err != nil {
		b.Fatalf("Tier2Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(10000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier3_Add(b *testing.B) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Tier3Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Tier3Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier2_Add(b *testing.B) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	cf, err := Tier2Compile(fn)
	if err != nil {
		b.Fatalf("Tier2Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}
