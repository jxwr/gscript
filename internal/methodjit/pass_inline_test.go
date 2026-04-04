// pass_inline_test.go tests the function inlining pass.
// Tests compile GScript source with multiple functions, build the caller's IR,
// run the inline pass, and verify that small callees are inlined (OpCall removed)
// while large or recursive callees are left as calls.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// buildInlineTestIR compiles source and builds caller IR + inline config.
func buildInlineTestIR(t *testing.T, src, callerName string) (*Function, InlineConfig) {
	t.Helper()
	proto := compileTop(t, src)

	globals := make(map[string]*vm.FuncProto)
	var callerProto *vm.FuncProto
	for _, p := range proto.Protos {
		globals[p.Name] = p
		if p.Name == callerName {
			callerProto = p
		}
	}
	if callerProto == nil {
		t.Fatalf("function %q not found in compiled protos", callerName)
	}

	fn := BuildGraph(callerProto)
	config := InlineConfig{
		Globals: globals,
		MaxSize: 30,
	}
	return fn, config
}

// runVMFunc executes the named function from the source via the VM interpreter.
func runVMFunc(t *testing.T, src, funcName string, args []runtime.Value) []runtime.Value {
	t.Helper()
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	proto := compileTop(t, src)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	fnVal := v.GetGlobal(funcName)
	if fnVal.IsNil() {
		t.Fatalf("function %q not found in globals", funcName)
	}

	results, err := v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("VM call error: %v", err)
	}
	return results
}

// countOp counts the number of instructions with the given op in all blocks.
func countOp(fn *Function, op Op) int {
	count := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == op {
				count++
			}
		}
	}
	return count
}

// TestInline_TrivialFunction tests inlining of `func add(a,b){return a+b}` into
// a caller that uses add(x, 1). After inlining, no OpCall should remain.
func TestInline_TrivialFunction(t *testing.T) {
	src := `
func add(a, b) {
	return a + b
}
func f(x) {
	return add(x, 1)
}
`
	fn, config := buildInlineTestIR(t, src, "f")
	t.Logf("Before inline:\n%s", Print(fn))

	// Verify there is an OpCall before inlining.
	if countOp(fn, OpCall) == 0 {
		t.Fatal("expected at least one OpCall before inlining")
	}

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// After inlining, no OpCall should remain (add was inlined).
	if n := countOp(result, OpCall); n != 0 {
		t.Errorf("expected 0 OpCall after inlining, got %d", n)
	}

	// Should have an OpAdd (from the inlined add body).
	if countOp(result, OpAdd) == 0 {
		t.Error("expected OpAdd from inlined function body")
	}

	// Validate structural integrity.
	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}

// TestInline_SpectralNorm_A tests inlining a function like spectral_norm's A(i,j).
func TestInline_SpectralNorm_A(t *testing.T) {
	src := `
func A(i, j) {
	return 1.0 / ((i+j)*(i+j+1)/2 + i + 1)
}
func f(i) {
	s := 0
	for j := 0; j < 10; j++ {
		s = s + A(i, j)
	}
	return s
}
`
	fn, config := buildInlineTestIR(t, src, "f")
	t.Logf("Before inline:\n%s", Print(fn))

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// A is small, should be inlined.
	if n := countOp(result, OpCall); n != 0 {
		t.Errorf("expected 0 OpCall after inlining A, got %d", n)
	}

	// Should have a Div from A's body.
	if countOp(result, OpDiv) == 0 {
		t.Error("expected OpDiv from inlined A body")
	}

	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}

// TestInline_TooLarge tests that a callee exceeding the bytecode budget is NOT inlined.
func TestInline_TooLarge(t *testing.T) {
	// We'll set MaxSize to a very small value to force the callee to be "too large".
	src := `
func big(a, b) {
	x := a + b
	y := x * a
	z := y - b
	w := z + x
	return w + y + z
}
func f(x) {
	return big(x, 1)
}
`
	fn, config := buildInlineTestIR(t, src, "f")
	// Set a very small max size so "big" won't be inlined.
	config.MaxSize = 3
	t.Logf("Before inline:\n%s", Print(fn))

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// big has more than 3 bytecodes, so it should NOT be inlined.
	if n := countOp(result, OpCall); n == 0 {
		t.Error("expected OpCall to remain (callee too large)")
	}
}

// TestInline_Recursive tests that recursive functions are NOT inlined.
func TestInline_Recursive(t *testing.T) {
	src := `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}
func f(x) {
	return fib(x)
}
`
	fn, config := buildInlineTestIR(t, src, "f")
	t.Logf("Before inline:\n%s", Print(fn))

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// fib is recursive (it calls itself), so it should NOT be inlined.
	if n := countOp(result, OpCall); n == 0 {
		t.Error("expected OpCall to remain (recursive function should not be inlined)")
	}
}

// TestInline_Correctness verifies that inlined results match VM execution.
func TestInline_Correctness(t *testing.T) {
	src := `
func add(a, b) {
	return a + b
}
func f(x) {
	return add(x, 1)
}
`
	// Run via VM to get ground truth.
	vmResults := runVMFunc(t, src, "f", []runtime.Value{runtime.IntValue(10)})
	if len(vmResults) == 0 {
		t.Fatal("VM returned no results")
	}
	vmResult := vmResults[0]

	// Run via IR interpreter after inlining.
	fn, config := buildInlineTestIR(t, src, "f")
	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}

	// The inlined IR should execute correctly.
	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(10)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	if len(irResults) == 0 {
		t.Fatal("IR interpreter returned no results")
	}

	assertValuesEqual(t, "f(10) = add(10, 1)", irResults[0], vmResult)
}

// TestInline_Pipeline tests that InlinePass integrates with the pipeline.
func TestInline_Pipeline(t *testing.T) {
	src := `
func add(a, b) {
	return a + b
}
func f(x) {
	return add(x, 1)
}
`
	fn, config := buildInlineTestIR(t, src, "f")

	p := NewPipeline()
	p.Add("Inline", InlinePassWith(config))
	p.SetValidator(Validate)

	result, err := p.Run(fn)
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	// After inlining through the pipeline, no OpCall should remain.
	if n := countOp(result, OpCall); n != 0 {
		t.Errorf("expected 0 OpCall after pipeline inline, got %d", n)
	}
}

// TestInline_MultipleCallSites tests inlining when the same function is called
// multiple times in the caller.
func TestInline_MultipleCallSites(t *testing.T) {
	src := `
func add(a, b) {
	return a + b
}
func f(x, y) {
	return add(x, 1) + add(y, 2)
}
`
	fn, config := buildInlineTestIR(t, src, "f")
	t.Logf("Before inline:\n%s", Print(fn))

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// Both calls to add should be inlined.
	if n := countOp(result, OpCall); n != 0 {
		t.Errorf("expected 0 OpCall after inlining both calls, got %d", n)
	}

	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}

// TestInline_TrivialInLoopPhiRewrite verifies that when a call inside a loop body
// is inlined, the loop header phi that references the old call result ID is
// properly rewritten to the inlined result. This is the regression test for the
// bug where rewriteValueRefs only scanned the current block, leaving cross-block
// phi references pointing to the dead call ID.
func TestInline_TrivialInLoopPhiRewrite(t *testing.T) {
	src := `
func add_xy(a, b) {
	return a + b
}
func sum_inline(n) {
	total := 0.0
	for i := 1; i <= n; i++ {
		total = add_xy(total, i * 1.0)
	}
	return total
}
`
	fn, config := buildInlineTestIR(t, src, "sum_inline")
	t.Logf("Before inline:\n%s", Print(fn))

	// Verify there is an OpCall before inlining.
	if countOp(fn, OpCall) == 0 {
		t.Fatal("expected at least one OpCall before inlining")
	}

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// After inlining, no OpCall should remain.
	if n := countOp(result, OpCall); n != 0 {
		t.Errorf("expected 0 OpCall after inlining, got %d", n)
	}

	// Validate structural integrity (catches orphan references).
	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}

	// Verify that all phi arguments reference live values (not the dead call ID).
	// Collect all defined value IDs.
	definedIDs := make(map[int]bool)
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			definedIDs[instr.ID] = true
		}
	}
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi {
				for i, arg := range instr.Args {
					if arg != nil && !definedIDs[arg.ID] {
						// Check if arg is a parameter (LoadSlot) which may not be in definedIDs
						// but is a valid function input. Skip those.
						if arg.Def != nil && arg.Def.Op == OpLoadSlot {
							continue
						}
						// Also skip constants
						if arg.Def != nil && (arg.Def.Op == OpConstInt || arg.Def.Op == OpConstFloat || arg.Def.Op == OpConstNil) {
							continue
						}
						t.Errorf("phi v%d arg[%d] references undefined v%d (dead call ID not rewritten)",
							instr.ID, i, arg.ID)
					}
				}
			}
		}
	}

	// Run the IR interpreter to verify correctness.
	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(100)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	if len(irResults) == 0 {
		t.Fatal("IR interpreter returned no results")
	}

	// Expected: sum(1..100) = 5050.0
	vmResults := runVMFunc(t, src, "sum_inline", []runtime.Value{runtime.IntValue(100)})
	if len(vmResults) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "sum_inline(100)", irResults[0], vmResults[0])
}

// TestInline_CorrectnessSpectralA verifies inlined spectral_norm A matches VM.
func TestInline_CorrectnessSpectralA(t *testing.T) {
	src := `
func A(i, j) {
	return 1.0 / ((i+j)*(i+j+1)/2 + i + 1)
}
func f(i, j) {
	return A(i, j)
}
`
	vmResults := runVMFunc(t, src, "f", []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)})
	if len(vmResults) == 0 {
		t.Fatal("VM returned no results")
	}

	fn, config := buildInlineTestIR(t, src, "f")
	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}

	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	if len(irResults) == 0 {
		t.Fatal("IR interpreter returned no results")
	}

	assertValuesEqual(t, "A(3,4)", irResults[0], vmResults[0])
}
