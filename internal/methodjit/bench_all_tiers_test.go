//go:build darwin && arm64

// bench_all_tiers_test.go provides unified benchmarks comparing all JIT tiers:
//   - VM interpreter (Tier 0)
//   - Tier 1 baseline JIT (1:1 bytecode templates via VM + BaselineJITEngine)
//   - Tier 2 optimizing JIT (IR pipeline + regalloc via Compile)
//
// Workloads: Sum(N), Add, Fib(10), Branch, FloatAdd
//
// All benchmark names use the "BenchmarkAll_" prefix to avoid collisions with
// existing benchmarks in other test files.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ---------------------------------------------------------------------------
// Source strings
// ---------------------------------------------------------------------------

const (
	allSrcSum      = `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	allSrcAdd      = `func f(a, b) { return a + b }`
	allSrcFib      = `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	allSrcBranch   = `func f(n) { if n > 10 { if n > 20 { return 3 } else { return 2 } } else { return 1 } }`
	allSrcFloatAdd = `func f(a, b) { return a + b }`
)

// ---------------------------------------------------------------------------
// Tier helpers (benchmark variants)
// ---------------------------------------------------------------------------

// allBenchVM sets up the VM once and calls the named function b.N times.
func allBenchVM(b *testing.B, src string, args []runtime.Value) {
	b.Helper()
	proto := compileTopB(b, src)
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	_, err := v.Execute(proto)
	if err != nil {
		b.Fatalf("VM execute error: %v", err)
	}

	var fnName string
	for _, p := range proto.Protos {
		if p.Name != "" {
			fnName = p.Name
			break
		}
	}
	if fnName == "" {
		b.Fatal("no named function")
	}
	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		b.Fatalf("function %q not found", fnName)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.CallValue(fnVal, args)
	}
}

// allBenchTier1 sets up the VM with baseline JIT engine and calls b.N times.
func allBenchTier1(b *testing.B, src string, args []runtime.Value) {
	b.Helper()
	proto := compileTopB(b, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()

	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)

	_, err := v.Execute(proto)
	if err != nil {
		b.Fatalf("execute error: %v", err)
	}

	var fnName string
	for _, p := range proto.Protos {
		if p.Name != "" {
			fnName = p.Name
			break
		}
	}
	if fnName == "" {
		b.Fatal("no named function")
	}
	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		b.Fatalf("function %q not found", fnName)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.CallValue(fnVal, args)
	}
}

// allBenchTier2 compiles through the Tier 2 optimizing pipeline (with regalloc)
// and executes b.N times.
func allBenchTier2(b *testing.B, src string, args []runtime.Value) {
	b.Helper()
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _, pipeErr := RunTier2Pipeline(fn, nil)
	if pipeErr != nil {
		b.Fatalf("pipeline: %v", pipeErr)
	}
	fn.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

// ---------------------------------------------------------------------------
// Sum(N) benchmarks -- N = 100, 1000, 10000
// ---------------------------------------------------------------------------

func BenchmarkAll_VM_Sum100(b *testing.B)    { allBenchVM(b, allSrcSum, intArgs(100)) }
func BenchmarkAll_T1_Sum100(b *testing.B)    { allBenchTier1(b, allSrcSum, intArgs(100)) }
func BenchmarkAll_T2_Sum100(b *testing.B)    { allBenchTier2(b, allSrcSum, intArgs(100)) }

func BenchmarkAll_VM_Sum1000(b *testing.B)   { allBenchVM(b, allSrcSum, intArgs(1000)) }
func BenchmarkAll_T1_Sum1000(b *testing.B)   { allBenchTier1(b, allSrcSum, intArgs(1000)) }
func BenchmarkAll_T2_Sum1000(b *testing.B)   { allBenchTier2(b, allSrcSum, intArgs(1000)) }

func BenchmarkAll_VM_Sum10000(b *testing.B)  { allBenchVM(b, allSrcSum, intArgs(10000)) }
func BenchmarkAll_T1_Sum10000(b *testing.B)  { allBenchTier1(b, allSrcSum, intArgs(10000)) }
func BenchmarkAll_T2_Sum10000(b *testing.B)  { allBenchTier2(b, allSrcSum, intArgs(10000)) }

// ---------------------------------------------------------------------------
// Add(a, b) -- single integer arithmetic
// ---------------------------------------------------------------------------

func BenchmarkAll_VM_Add(b *testing.B)  { allBenchVM(b, allSrcAdd, intArgs(3, 4)) }
func BenchmarkAll_T1_Add(b *testing.B)  { allBenchTier1(b, allSrcAdd, intArgs(3, 4)) }
func BenchmarkAll_T2_Add(b *testing.B)  { allBenchTier2(b, allSrcAdd, intArgs(3, 4)) }

// ---------------------------------------------------------------------------
// Fib(10) -- recursive calls
// ---------------------------------------------------------------------------

func BenchmarkAll_VM_Fib10(b *testing.B) { allBenchVM(b, allSrcFib, intArgs(10)) }
// Note: Tier 1 fib(10) has a known deep-recursion bug; included for comparison.
func BenchmarkAll_T1_Fib10(b *testing.B) { allBenchTier1(b, allSrcFib, intArgs(10)) }
// Note: Tier 2 standalone Execute does not support recursive calls (no call
// instruction). Fib(10) would require call-exit. Omitted intentionally.

// ---------------------------------------------------------------------------
// Branch -- if/else (no loops, no calls)
// ---------------------------------------------------------------------------

func BenchmarkAll_VM_Branch(b *testing.B)  { allBenchVM(b, allSrcBranch, intArgs(15)) }
func BenchmarkAll_T1_Branch(b *testing.B)  { allBenchTier1(b, allSrcBranch, intArgs(15)) }
func BenchmarkAll_T2_Branch(b *testing.B)  { allBenchTier2(b, allSrcBranch, intArgs(15)) }

// ---------------------------------------------------------------------------
// FloatAdd -- float arithmetic
// ---------------------------------------------------------------------------

var floatArgs = []runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)}

func BenchmarkAll_VM_FloatAdd(b *testing.B)  { allBenchVM(b, allSrcFloatAdd, floatArgs) }
func BenchmarkAll_T1_FloatAdd(b *testing.B)  { allBenchTier1(b, allSrcFloatAdd, floatArgs) }
func BenchmarkAll_T2_FloatAdd(b *testing.B)  { allBenchTier2(b, allSrcFloatAdd, floatArgs) }
