//go:build darwin && arm64

// perf_test.go benchmarks the Method JIT against the VM interpreter.
// Measures native ARM64 code generation + execution vs bytecode interpretation
// for the same GScript functions. These micro-benchmarks quantify the speedup
// from compiling to native code for operations the Method JIT supports.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// --- Helper: compile + JIT a function, return CompiledFunction ---

func compileJIT(b *testing.B, src string) *CompiledFunction {
	b.Helper()
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	// Run optimization pipeline: TypeSpec → ConstProp → DCE
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Compile error: %v", err)
	}
	// Note: caller should defer cf.Code.Free() but in benchmarks
	// we let the GC handle it since b.Cleanup isn't always available.
	b.Cleanup(func() { cf.Code.Free() })
	return cf
}

// =====================================================================
// 1. Integer Addition: func f(a, b) { return a + b }
// =====================================================================

func BenchmarkMethodJIT_Add(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a + b }`)
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Add(b *testing.B) {
	benchVM(b, `func f(a, b) { return a + b }`, intArgs(3, 4))
}

// =====================================================================
// 2. Integer Subtraction: func f(a, b) { return a - b }
// =====================================================================

func BenchmarkMethodJIT_Sub(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a - b }`)
	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Sub(b *testing.B) {
	benchVM(b, `func f(a, b) { return a - b }`, intArgs(10, 3))
}

// =====================================================================
// 3. Integer Multiplication: func f(a, b) { return a * b }
// =====================================================================

func BenchmarkMethodJIT_Mul(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a * b }`)
	args := []runtime.Value{runtime.IntValue(6), runtime.IntValue(7)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Mul(b *testing.B) {
	benchVM(b, `func f(a, b) { return a * b }`, intArgs(6, 7))
}

// =====================================================================
// 4. Float Division: func f(a, b) { return a / b }
// =====================================================================

func BenchmarkMethodJIT_Div(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a / b }`)
	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Div(b *testing.B) {
	benchVM(b, `func f(a, b) { return a / b }`, intArgs(10, 3))
}

// =====================================================================
// 5. Float Addition: func f(a, b) { return a + b } with float args
// =====================================================================

func BenchmarkMethodJIT_FloatAdd(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { return a + b }`)
	args := []runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_FloatAdd(b *testing.B) {
	benchVM(b, `func f(a, b) { return a + b }`,
		[]runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)})
}

// =====================================================================
// 6. Mul chain: func f(a, b) { x := a * b; y := x * a; z := y * b; return z }
// =====================================================================

func BenchmarkMethodJIT_MulChain(b *testing.B) {
	cf := compileJIT(b, `func f(a, b) { x := a * b; y := x * a; z := y * b; return z }`)
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_MulChain(b *testing.B) {
	benchVM(b, `func f(a, b) { x := a * b; y := x * a; z := y * b; return z }`, intArgs(3, 4))
}

// =====================================================================
// 7. Branching: func f(n) { if n > 0 { return n * 2 } else { return 0 } }
// =====================================================================

func BenchmarkMethodJIT_Branch(b *testing.B) {
	cf := compileJIT(b, `func f(n) { if n > 0 { return n * 2 } else { return 0 } }`)
	args := []runtime.Value{runtime.IntValue(5)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Branch(b *testing.B) {
	benchVM(b, `func f(n) { if n > 0 { return n * 2 } else { return 0 } }`, intArgs(5))
}

// =====================================================================
// 8. Nested If: func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }
// =====================================================================

func BenchmarkMethodJIT_NestedIf(b *testing.B) {
	cf := compileJIT(b, `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`)
	args := []runtime.Value{runtime.IntValue(15)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_NestedIf(b *testing.B) {
	benchVM(b, `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`, intArgs(15))
}

// =====================================================================
// 9. For loop (sum 1..100): func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }
// =====================================================================

func BenchmarkMethodJIT_Sum100(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(100)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Sum100(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(100))
}

// =====================================================================
// 10. For loop (sum 1..1000)
// =====================================================================

func BenchmarkMethodJIT_Sum1000(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(1000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Sum1000(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(1000))
}

// =====================================================================
// 11. For loop (sum 1..10000)
// =====================================================================

func BenchmarkMethodJIT_Sum10000(b *testing.B) {
	cf := compileJIT(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(10000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Sum10000(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10000))
}

// =====================================================================
// 12. Return constant: func f() { return 42 }
// =====================================================================

func BenchmarkMethodJIT_RetConst(b *testing.B) {
	cf := compileJIT(b, `func f() { return 42 }`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(nil)
	}
}

func BenchmarkVM_RetConst(b *testing.B) {
	benchVM(b, `func f() { return 42 }`, nil)
}

// =====================================================================
// 13. Unary negate: func f(a) { return -a }
// =====================================================================

func BenchmarkMethodJIT_Neg(b *testing.B) {
	cf := compileJIT(b, `func f(a) { return -a }`)
	args := []runtime.Value{runtime.IntValue(42)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Neg(b *testing.B) {
	benchVM(b, `func f(a) { return -a }`, intArgs(42))
}

// =====================================================================
// 14. For loop (short): sum 1..10
// =====================================================================

func BenchmarkMethodJIT_Sum10(b *testing.B) {
	cf := compileJIT(b, `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	args := []runtime.Value{runtime.IntValue(10)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkVM_Sum10(b *testing.B) {
	benchVM(b, `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10))
}
