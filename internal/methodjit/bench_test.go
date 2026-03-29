// bench_test.go benchmarks the IR interpreter against the VM bytecode interpreter.
// It measures both correctness (IR vs VM produce identical results) and performance
// (how much slower the IR interpreter is compared to the VM).

package methodjit

import (
	"testing"
	"time"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// compileFunctionB is the benchmark variant of compileFunction (uses *testing.B).
func compileFunctionB(b *testing.B, src string) *vm.FuncProto {
	b.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		b.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		b.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		b.Fatalf("compile error: %v", err)
	}
	if len(proto.Protos) > 0 {
		return proto.Protos[0]
	}
	return proto
}

// compileTopB is the benchmark variant of compileTop (uses *testing.B).
func compileTopB(b *testing.B, src string) *vm.FuncProto {
	b.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		b.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		b.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		b.Fatalf("compile error: %v", err)
	}
	return proto
}

// runVMB executes a function via the VM interpreter (benchmark variant).
func runVMB(b *testing.B, src string, args []runtime.Value) []runtime.Value {
	b.Helper()
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	proto := compileTopB(b, src)
	_, err := v.Execute(proto)
	if err != nil {
		b.Fatalf("VM execute top-level error: %v", err)
	}

	var fnName string
	for _, p := range proto.Protos {
		if p.Name != "" {
			fnName = p.Name
			break
		}
	}
	if fnName == "" {
		b.Fatal("no named function found in proto")
	}

	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		b.Fatalf("function %q not found in globals", fnName)
	}

	results, err := v.CallValue(fnVal, args)
	if err != nil {
		b.Fatalf("VM call error: %v", err)
	}
	return results
}

// intArgs builds a []runtime.Value from int64 arguments.
func intArgs(vals ...int64) []runtime.Value {
	args := make([]runtime.Value, len(vals))
	for i, v := range vals {
		args[i] = runtime.IntValue(v)
	}
	return args
}

// ---------- Correctness + timing comparison ----------

func TestBenchIRInterp(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		args  []runtime.Value
		iters int // number of iterations for timing
	}{
		{"add", `func f(a, b) { return a + b }`, intArgs(3, 4), 10000},
		{"sub", `func f(a, b) { return a - b }`, intArgs(10, 3), 10000},
		{"mul", `func f(a, b) { return a * b }`, intArgs(6, 7), 10000},
		{"fib_10", `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`, intArgs(10), 100},
		{"fib_20", `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`, intArgs(20), 10},
		{"sum_100", `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(100), 1000},
		{"sum_10000", `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10000), 100},
		{"nested_if", `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`, intArgs(15), 10000},
		{"mul_chain", `func f(a, b) { x := a * b; y := x * a; z := y * b; return z }`, intArgs(3, 4), 10000},
		{"if_true", `func f(n) { if n < 2 { return n } else { return n * 2 } }`, intArgs(1), 10000},
		{"if_false", `func f(n) { if n < 2 { return n } else { return n * 2 } }`, intArgs(5), 10000},
		{"for_loop_10", `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10), 10000},
		// NOTE: countdown `for n > 0 { n = n - 1 }` causes infinite loop in IR interpreter.
		// The while-loop pattern that reassigns a parameter variable is not handled correctly
		// by the graph builder's phi/SSA construction. Filed as known bug.
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proto := compileFunction(t, tc.src)
			fn := BuildGraph(proto)

			// Correctness: IR vs VM
			irResult, err := Interpret(fn, tc.args)
			if err != nil {
				t.Fatalf("IR interpreter error: %v", err)
			}
			vmResult := runVM(t, tc.src, tc.args)

			if len(irResult) != len(vmResult) {
				t.Fatalf("result count mismatch: IR=%d, VM=%d", len(irResult), len(vmResult))
			}
			for i := range irResult {
				if irResult[i].IsString() && vmResult[i].IsString() {
					if irResult[i].Str() != vmResult[i].Str() {
						t.Errorf("result[%d] mismatch: IR=%q, VM=%q", i, irResult[i].Str(), vmResult[i].Str())
					}
				} else {
					assertValuesEqual(t, tc.name, irResult[i], vmResult[i])
				}
			}
			t.Logf("CORRECT: IR=%v, VM=%v", irResult, vmResult)

			// Timing: IR interpreter
			start := time.Now()
			for i := 0; i < tc.iters; i++ {
				Interpret(fn, tc.args)
			}
			irTime := time.Since(start)

			// Timing: VM interpreter
			// Pre-create the VM and register the function once, then call repeatedly.
			globals := make(map[string]runtime.Value)
			v := vm.New(globals)
			defer v.Close()

			topProto := compileTop(t, tc.src)
			_, err = v.Execute(topProto)
			if err != nil {
				t.Fatalf("VM execute top-level error: %v", err)
			}

			var fnName string
			for _, p := range topProto.Protos {
				if p.Name != "" {
					fnName = p.Name
					break
				}
			}
			fnVal := v.GetGlobal(fnName)

			start = time.Now()
			for i := 0; i < tc.iters; i++ {
				v.CallValue(fnVal, tc.args)
			}
			vmTime := time.Since(start)

			irAvg := irTime / time.Duration(tc.iters)
			vmAvg := vmTime / time.Duration(tc.iters)
			ratio := float64(irTime) / float64(vmTime)

			t.Logf("IR: %v/op (%v total), VM: %v/op (%v total), ratio: %.2fx slower",
				irAvg, irTime, vmAvg, vmTime, ratio)
		})
	}
}

// ---------- Go benchmarks: IR interpreter ----------

func BenchmarkIRInterp_Add(b *testing.B) {
	proto := compileFunctionB(b, `func f(a, b) { return a + b }`)
	fn := BuildGraph(proto)
	args := intArgs(3, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

func BenchmarkIRInterp_Fib10(b *testing.B) {
	proto := compileFunctionB(b, `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`)
	fn := BuildGraph(proto)
	args := intArgs(10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

func BenchmarkIRInterp_Fib20(b *testing.B) {
	proto := compileFunctionB(b, `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`)
	fn := BuildGraph(proto)
	args := intArgs(20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

func BenchmarkIRInterp_Sum100(b *testing.B) {
	proto := compileFunctionB(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	fn := BuildGraph(proto)
	args := intArgs(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

func BenchmarkIRInterp_Sum10000(b *testing.B) {
	proto := compileFunctionB(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	fn := BuildGraph(proto)
	args := intArgs(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

func BenchmarkIRInterp_NestedIf(b *testing.B) {
	proto := compileFunctionB(b, `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`)
	fn := BuildGraph(proto)
	args := intArgs(15)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

func BenchmarkIRInterp_MulChain(b *testing.B) {
	proto := compileFunctionB(b, `func f(a, b) { x := a * b; y := x * a; z := y * b; return z }`)
	fn := BuildGraph(proto)
	args := intArgs(3, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

// NOTE: BenchmarkIRInterp_Countdown is omitted because the while-loop pattern
// `for n > 0 { n = n - 1 }` causes an infinite loop in the IR interpreter.
// The graph builder does not correctly construct phi nodes for parameter
// reassignment in while-style loops. See TestBenchIRInterp for details.

func BenchmarkIRInterp_ForLoop10(b *testing.B) {
	proto := compileFunctionB(b, `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	fn := BuildGraph(proto)
	args := intArgs(10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Interpret(fn, args)
	}
}

// ---------- Go benchmarks: VM interpreter ----------

func BenchmarkVMInterp_Add(b *testing.B) {
	benchVM(b, `func f(a, b) { return a + b }`, intArgs(3, 4))
}

func BenchmarkVMInterp_Fib10(b *testing.B) {
	benchVM(b, `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`, intArgs(10))
}

func BenchmarkVMInterp_Fib20(b *testing.B) {
	benchVM(b, `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`, intArgs(20))
}

func BenchmarkVMInterp_Sum100(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(100))
}

func BenchmarkVMInterp_Sum10000(b *testing.B) {
	benchVM(b, `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10000))
}

func BenchmarkVMInterp_NestedIf(b *testing.B) {
	benchVM(b, `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`, intArgs(15))
}

func BenchmarkVMInterp_MulChain(b *testing.B) {
	benchVM(b, `func f(a, b) { x := a * b; y := x * a; z := y * b; return z }`, intArgs(3, 4))
}

// NOTE: BenchmarkVMInterp_Countdown omitted (see IR countdown note above).

func BenchmarkVMInterp_ForLoop10(b *testing.B) {
	benchVM(b, `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`, intArgs(10))
}

// benchVM is a helper that sets up the VM once and calls the function b.N times.
func benchVM(b *testing.B, src string, args []runtime.Value) {
	b.Helper()

	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		b.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		b.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		b.Fatalf("compile error: %v", err)
	}

	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	_, err = v.Execute(proto)
	if err != nil {
		b.Fatalf("VM execute top-level error: %v", err)
	}

	var fnName string
	for _, p := range proto.Protos {
		if p.Name != "" {
			fnName = p.Name
			break
		}
	}
	if fnName == "" {
		b.Fatal("no named function found in proto")
	}

	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		b.Fatalf("function %q not found in globals", fnName)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.CallValue(fnVal, args)
	}
}
