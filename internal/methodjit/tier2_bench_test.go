//go:build darwin && arm64

// tier2_bench_test.go benchmarks all execution tiers for direct comparison:
//   - VM interpreter (BenchmarkVMSum_*)
//   - Tier 1 baseline JIT via full VM + engine (BenchmarkTier1_Sum*)
//   - Tier 2 optimizing JIT with regalloc (BenchmarkTier2Reg_Sum*)
//
// The Sum benchmark is the primary workload: sum(N) = 1+2+...+N.
// It exercises integer arithmetic and loop control flow.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const benchSumSrc = `func sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`

// ---------------------------------------------------------------------------
// Tier 2 regalloc benchmarks (IR pipeline + register allocation)
// ---------------------------------------------------------------------------

func tier2RegPipelineB(b *testing.B, src string) *CompiledFunction {
	b.Helper()
	proto := compileFunctionB(b, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	return cf
}

func BenchmarkTier2Reg_Sum100(b *testing.B) {
	cf := tier2RegPipelineB(b, benchSumSrc)
	args := []runtime.Value{runtime.IntValue(100)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier2Reg_Sum1000(b *testing.B) {
	cf := tier2RegPipelineB(b, benchSumSrc)
	args := []runtime.Value{runtime.IntValue(1000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkTier2Reg_Sum10000(b *testing.B) {
	cf := tier2RegPipelineB(b, benchSumSrc)
	args := []runtime.Value{runtime.IntValue(10000)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

// ---------------------------------------------------------------------------
// Tier 1 benchmarks: baseline JIT (1:1 bytecode templates)
// ---------------------------------------------------------------------------

func tier1BenchHelper(b *testing.B, src string, args []runtime.Value) {
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
		b.Fatal("no named function found")
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

func BenchmarkTier1_Sum100(b *testing.B) {
	tier1BenchHelper(b, benchSumSrc, intArgs(100))
}

func BenchmarkTier1_Sum1000(b *testing.B) {
	tier1BenchHelper(b, benchSumSrc, intArgs(1000))
}

func BenchmarkTier1_Sum10000(b *testing.B) {
	tier1BenchHelper(b, benchSumSrc, intArgs(10000))
}

// ---------------------------------------------------------------------------
// VM interpreter benchmarks (for comparison)
// ---------------------------------------------------------------------------

func BenchmarkVMSum_100(b *testing.B) {
	benchVM(b, benchSumSrc, intArgs(100))
}

func BenchmarkVMSum_1000(b *testing.B) {
	benchVM(b, benchSumSrc, intArgs(1000))
}

func BenchmarkVMSum_10000(b *testing.B) {
	benchVM(b, benchSumSrc, intArgs(10000))
}
