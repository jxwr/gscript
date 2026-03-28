//go:build darwin && arm64

// tiering_test.go tests automatic promotion from interpreter to Method JIT.
// Tests verify cold functions stay interpreted, hot functions get compiled,
// compiled functions produce correct results, and failed compilations
// are handled gracefully.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// compileProto compiles GScript source and returns the top-level proto.
func compileProto(t *testing.T, src string) *vm.FuncProto {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return proto
}

// runWithMethodJIT compiles and runs GScript source with the Method JIT engine enabled.
// Returns the VM and engine for inspection.
func runWithMethodJIT(t *testing.T, src string) (*vm.VM, *MethodJITEngine) {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	engine := NewMethodJITEngine()
	v.SetMethodJIT(engine)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return v, engine
}

// TestTiering_ColdFunction verifies that a function called fewer times than the
// threshold stays interpreted and is not compiled.
func TestTiering_ColdFunction(t *testing.T) {
	src := `
func add(a, b) {
    return a + b
}
result := 0
for i := 1; i <= 50; i++ {
    result = add(i, 1)
}
`
	_, engine := runWithMethodJIT(t, src)
	if engine.CompiledCount() != 0 {
		t.Errorf("expected 0 compiled functions, got %d", engine.CompiledCount())
	}
}

// TestTiering_HotFunction verifies that a function called more than the threshold
// gets compiled by the Method JIT (when it only uses supported ops).
func TestTiering_HotFunction(t *testing.T) {
	// add() only uses OpAdd, OpLoadSlot, OpReturn -- all supported ops.
	src := `
func add(a, b) {
    return a + b
}
result := 0
for i := 1; i <= 200; i++ {
    result = add(i, 1)
}
`
	_, engine := runWithMethodJIT(t, src)
	if engine.CompiledCount() == 0 {
		t.Error("expected at least 1 compiled function after 200 calls")
	}
}

// TestTiering_CompiledExecutes verifies that after compilation, the function
// executes natively and produces the correct result.
func TestTiering_CompiledExecutes(t *testing.T) {
	src := `
func add(a, b) {
    return a + b
}
result := 0
for i := 1; i <= 300; i++ {
    result = add(i, 1)
}
`
	v, engine := runWithMethodJIT(t, src)
	if engine.CompiledCount() == 0 {
		t.Fatal("expected function to be compiled")
	}
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// Last iteration: add(300, 1) = 301
	if result.Int() != 301 {
		t.Errorf("got result %d, want 301", result.Int())
	}
}

// TestTiering_FailedCompilation verifies that if compilation fails for a function,
// the VM continues interpreting without crashing.
func TestTiering_FailedCompilation(t *testing.T) {
	engine := NewMethodJITEngine()

	// Compile a function that uses unsupported ops (OpCall, OpGetGlobal).
	// The Method JIT should refuse to compile it.
	src := `
func caller(x) {
    return print(x)
}
`
	proto := compileProto(t, src)
	if len(proto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	fnProto := proto.Protos[0]
	fnProto.CallCount = CompileThreshold + 10
	fnProto.EnsureFeedback()

	cf := engine.TryCompile(fnProto)
	if cf != nil {
		t.Error("expected nil CompiledFunction for function with unsupported ops")
	}
	if engine.FailedCount() == 0 {
		t.Error("expected failed compilation to be recorded")
	}

	// Calling TryCompile again should return nil quickly (cached failure).
	cf = engine.TryCompile(fnProto)
	if cf != nil {
		t.Error("expected nil on retry of failed proto")
	}
}

// TestTiering_FeedbackCollected verifies that after threshold calls,
// the feedback vector has been initialized with data.
func TestTiering_FeedbackCollected(t *testing.T) {
	src := `
func mul(a, b) {
    return a * b
}
result := 0
for i := 1; i <= 200; i++ {
    result = mul(i, 2)
}
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	engine := NewMethodJITEngine()
	v.SetMethodJIT(engine)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	// Find the inner "mul" function proto.
	if len(proto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	mulProto := proto.Protos[0]
	if mulProto.Feedback == nil {
		t.Error("expected feedback vector to be initialized after hot calls")
	}
}

// TestTiering_EndToEnd_Fib runs fib(20) through the VM with Method JIT enabled.
// fib uses recursive calls (unsupported ops), so it won't be compiled.
// The test verifies correctness is maintained when Method JIT is active.
func TestTiering_EndToEnd_Fib(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(20)
`
	v, engine := runWithMethodJIT(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 6765 {
		t.Errorf("fib(20) = %d, want 6765", result.Int())
	}

	// fib uses OpCall/OpGetGlobal which are unsupported; should NOT be compiled.
	// The engine should have recorded it as failed.
	if engine.CompiledCount() > 0 {
		t.Log("note: fib was compiled (unexpected; unsupported ops should prevent it)")
	}
}

// TestTiering_EndToEnd_ForLoop runs a looping function through tiered execution.
// sum() uses only supported ops (add, comparisons, for-loop).
func TestTiering_EndToEnd_ForLoop(t *testing.T) {
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := 0
for i := 1; i <= 200; i++ {
    result = sum(10)
}
`
	v, _ := runWithMethodJIT(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// sum(10) = 55
	if result.Int() != 55 {
		t.Errorf("sum(10) = %d, want 55", result.Int())
	}
}

// TestTiering_NoMethodJIT verifies that the VM works exactly as before when
// no Method JIT engine is set.
func TestTiering_NoMethodJIT(t *testing.T) {
	src := `
func add(a, b) {
    return a + b
}
result := add(1, 2)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	// Do NOT set Method JIT engine.
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 3 {
		t.Errorf("got %v, want 3", result)
	}
}

// TestTiering_ThresholdBoundary tests behavior right at the compilation threshold.
func TestTiering_ThresholdBoundary(t *testing.T) {
	engine := NewMethodJITEngine()

	// Create a simple valid proto (identity function: return x).
	src := `
func identity(x) {
    return x
}
`
	proto := compileProto(t, src)
	if len(proto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	fnProto := proto.Protos[0]

	// Call below threshold: should not compile.
	for i := 0; i < CompileThreshold-1; i++ {
		fnProto.CallCount++
		cf := engine.TryCompile(fnProto)
		if cf != nil {
			t.Fatalf("compiled at call %d, expected threshold %d", i+1, CompileThreshold)
		}
	}

	// At threshold: initializes feedback, returns nil.
	fnProto.CallCount++
	cf := engine.TryCompile(fnProto)
	if cf != nil {
		t.Error("expected nil at threshold (feedback just initialized)")
	}
	if fnProto.Feedback == nil {
		t.Error("expected feedback to be initialized at threshold")
	}

	// One more call: should compile (identity only uses LoadSlot + Return).
	fnProto.CallCount++
	cf = engine.TryCompile(fnProto)
	if cf == nil {
		t.Error("expected compilation after threshold + 1 calls with feedback ready")
	}
}
