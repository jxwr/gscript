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

func TestInlinePureNumericPolicy_AcceptsNumericHelper(t *testing.T) {
	src := `
func A(i, j) {
	return 1.0 / ((i+j)*(i+j+1)/2 + i + 1)
}
func f(i, j) {
	return A(i, j)
}
`
	fn, config := buildInlineTestIR(t, src, "f")
	config.RequirePureNumeric = true

	result, err := InlinePassWith(config)(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	if n := countOp(result, OpCall); n != 0 {
		t.Fatalf("expected pure numeric helper to inline, got %d residual calls\nIR:\n%s", n, Print(result))
	}
}

func TestInlinePureNumericPolicy_RejectsSideEffectEscapeAndMultiReturn(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "side_effect_call",
			src: `
func helper(x) {
	print(x)
	return x + 1
}
func f(x) { return helper(x) }
`,
		},
		{
			name: "escaping_table",
			src: `
func helper(x) {
	return {value: x}
}
func f(x) { return helper(x) }
`,
		},
		{
			name: "multi_return",
			src: `
func helper(x) {
	return x, x + 1
}
func f(x) { return helper(x) }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, config := buildInlineTestIR(t, tt.src, "f")
			config.RequirePureNumeric = true
			result, err := InlinePassWith(config)(fn)
			if err != nil {
				t.Fatalf("InlinePass error: %v", err)
			}
			if n := countOp(result, OpCall); n == 0 {
				t.Fatalf("pure numeric policy should reject %s\nIR:\n%s", tt.name, Print(result))
			}
			if errs := Validate(result); len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("validation error: %v", e)
				}
			}
		})
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

func TestInline_PureNumericLoopCalleeInsideCallerLoop(t *testing.T) {
	src := `
func gcd(a, b) {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}
func sum_gcd(n) {
	total := 0
	for i := 1; i <= n; i++ {
		total = total + gcd(i * 7 + 13, i * 11 + 3)
	}
	return total
}
`
	fn, config := buildInlineTestIR(t, src, "sum_gcd")
	config.MaxSize = 40

	result, err := InlinePassWith(config)(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	if n := countOp(result, OpCall); n != 0 {
		t.Fatalf("expected pure numeric loop callee to inline inside caller loop, got %d calls\nIR:\n%s", n, Print(result))
	}
	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}

	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	vmResults := runVMFunc(t, src, "sum_gcd", []runtime.Value{runtime.IntValue(40)})
	if len(irResults) == 0 || len(vmResults) == 0 {
		t.Fatalf("empty results: IR=%v VM=%v", irResults, vmResults)
	}
	assertValuesEqual(t, "sum_gcd(40)", irResults[0], vmResults[0])
}

func TestInline_LoopCalleeInsideCallerLoopRejectsImpureNumeric(t *testing.T) {
	src := `
func sum_table(t, n) {
	total := 0
	for i := 1; i <= n; i++ {
		total = total + t[i]
	}
	return total
}
func driver(t, n) {
	total := 0
	for i := 1; i <= n; i++ {
		total = total + sum_table(t, i)
	}
	return total
}
`
	fn, config := buildInlineTestIR(t, src, "driver")
	config.MaxSize = 40

	result, err := InlinePassWith(config)(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	if n := countOp(result, OpCall); n == 0 {
		t.Fatalf("expected table-reading loop callee to remain behind call boundary\nIR:\n%s", Print(result))
	}
}

func TestInline_LoopCalleeInsideCallerLoopRejectsOverflowVersionedRecurrence(t *testing.T) {
	src := `
func fib_iter(n) {
	a := 0
	b := 1
	for i := 0; i < n; i++ {
		t := a + b
		a = b
		b = t
	}
	return a
}
func bench(n, reps) {
	result := 0
	for r := 1; r <= reps; r++ {
		result = fib_iter(n)
	}
	return result
}
`
	fn, config := buildInlineTestIR(t, src, "bench")
	config.MaxSize = 40

	result, err := InlinePassWith(config)(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	if n := countOp(result, OpCall); n == 0 {
		t.Fatalf("expected overflow-versioned numeric recurrence to remain behind call boundary\nIR:\n%s", Print(result))
	}
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

// TestInline_Recursive_Transitive verifies that a call inside an already-
// inlined callee body is itself inlined on a subsequent iteration. This is
// the spectral_norm pattern: multiplyAtAv -> multiplyAv -> A. After the
// first round, multiplyAv is inlined but its internal A(i,j) calls remain.
// The fixpoint iteration must inline those A calls too.
func TestInline_Recursive_Transitive(t *testing.T) {
	src := `
func leaf(x) {
	return x + 1
}
func mid(n) {
	s := 0
	for i := 0; i < n; i++ {
		s = s + leaf(i)
	}
	return s
}
func top(n) {
	return mid(n) + mid(n)
}
`
	fn, config := buildInlineTestIR(t, src, "top")
	t.Logf("Before inline:\n%s", Print(fn))

	// Before: top has 2 OpCalls (to mid).
	if n := countOp(fn, OpCall); n != 2 {
		t.Fatalf("expected 2 OpCall before inlining, got %d", n)
	}

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline:\n%s", Print(result))

	// After recursive inlining: both mid calls are inlined AND the leaf calls
	// inside them are also inlined. Expect 0 remaining OpCall.
	if n := countOp(result, OpCall); n != 0 {
		t.Errorf("expected 0 OpCall after recursive inlining, got %d", n)
	}

	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}

	// Correctness: top(10) should equal VM's top(10).
	vmResults := runVMFunc(t, src, "top", []runtime.Value{runtime.IntValue(10)})
	if len(vmResults) == 0 {
		t.Fatal("VM returned no results")
	}
	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(10)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	if len(irResults) == 0 {
		t.Fatal("IR interpreter returned no results")
	}
	assertValuesEqual(t, "top(10)", irResults[0], vmResults[0])
}

// TestInlineBoundedRecursion verifies that with MaxRecursion=2, a self-
// recursive callee (fib) gets inlined exactly 2 levels deep and then leaves
// residual OpCalls for the remaining recursion at runtime. Also verifies
// that MaxRecursion=0 disables recursive inlining (falls back to current
// behavior where fib stays as a call).
func TestInlineBoundedRecursion(t *testing.T) {
	src := `
func fib(n) {
	if n < 2 { return n }
	return fib(n-1) + fib(n-2)
}
func f(x) {
	return fib(x)
}
`

	// --- Case 1: MaxRecursion=2 ---
	fn, config := buildInlineTestIR(t, src, "f")
	config.MaxRecursion = 2
	t.Logf("Before inline (MaxRecursion=2):\n%s", Print(fn))

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline (MaxRecursion=2):\n%s", Print(result))

	// With MaxRecursion=2, fib is inlined 2 levels deep. Tree shape:
	//   f -> fib (depth 1)
	//          fib(n-1) -> depth 2 -> stop, leave OpCall (2 leaves)
	//          fib(n-2) -> depth 2 -> stop, leave OpCall (2 leaves)
	// Residual OpCalls = 4. Allow a 2..6 range for fixpoint variation.
	callCount := countOp(result, OpCall)
	if callCount == 0 {
		t.Errorf("expected residual OpCall > 0 after bounded inlining of fib, got 0 (recursion not inlined at all)")
	}
	if callCount >= 10 {
		t.Errorf("expected residual OpCall < 10 after bounded inlining of fib, got %d (inlining exploded)", callCount)
	}
	if callCount != 4 {
		t.Logf("note: expected exactly 4 residual OpCalls, got %d (acceptable if in range 2..6)", callCount)
		if callCount < 2 || callCount > 6 {
			t.Errorf("residual OpCall count %d outside acceptable range 2..6", callCount)
		}
	}

	// Validate structural integrity after bounded recursive inlining.
	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}

	// Correctness: the inlined IR must match the VM for fib(6) = 8.
	vmResults := runVMFunc(t, src, "f", []runtime.Value{runtime.IntValue(6)})
	if len(vmResults) == 0 {
		t.Fatal("VM returned no results")
	}
	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(6)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	if len(irResults) == 0 {
		t.Fatal("IR interpreter returned no results")
	}
	assertValuesEqual(t, "f(6) with bounded recursive inlining", irResults[0], vmResults[0])

	// --- Case 2: MaxRecursion=0 should fall back to current behavior ---
	// (recursive callee NOT inlined, original single OpCall remains).
	fn2, config2 := buildInlineTestIR(t, src, "f")
	config2.MaxRecursion = 0
	pass2 := InlinePassWith(config2)
	result2, err := pass2(fn2)
	if err != nil {
		t.Fatalf("InlinePass (MaxRecursion=0) error: %v", err)
	}
	if n := countOp(result2, OpCall); n != 1 {
		t.Errorf("with MaxRecursion=0 expected exactly 1 OpCall (no recursive inlining), got %d", n)
	}
}

func TestInlinePreserveSelfCalls(t *testing.T) {
	src := `
func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}
`
	fn, config := buildInlineTestIR(t, src, "ack")
	before := countOp(fn, OpCall)
	if before == 0 {
		t.Fatal("expected self-recursive OpCall before inlining")
	}

	config.MaxRecursion = 8
	config.MaxCumulativeSize = 120
	config.PreserveSelfCalls = true
	result, err := InlinePassWith(config)(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	if after := countOp(result, OpCall); after != before {
		t.Fatalf("PreserveSelfCalls should keep self-call shape: before=%d after=%d", before, after)
	}
	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}

// TestInlineMutualRecursion verifies that MaxRecursion bounds apply across
// mutual recursion (ping -> pong -> ping -> pong -> ...). Inlining must
// terminate, produce valid IR, and match VM output.
func TestInlineMutualRecursion(t *testing.T) {
	src := `
func ping(n) {
	if n < 1 { return 0 }
	return pong(n-1) + 1
}
func pong(n) {
	if n < 1 { return 0 }
	return ping(n-1) + 1
}
func caller(x) {
	return ping(x)
}
`
	fn, config := buildInlineTestIR(t, src, "caller")
	config.MaxRecursion = 2
	t.Logf("Before inline (mutual, MaxRecursion=2):\n%s", Print(fn))

	pass := InlinePassWith(config)
	result, err := pass(fn)
	if err != nil {
		t.Fatalf("InlinePass error: %v", err)
	}
	t.Logf("After inline (mutual, MaxRecursion=2):\n%s", Print(result))

	// With MaxRecursion=2 per-callee, ping/pong alternate up to depth=2 each,
	// then leave an OpCall to finish the remaining recursion at runtime.
	callCount := countOp(result, OpCall)
	if callCount < 1 {
		t.Errorf("expected at least 1 residual OpCall after bounded mutual inlining, got %d", callCount)
	}
	if callCount >= 20 {
		t.Errorf("mutual inlining exploded: %d residual OpCalls", callCount)
	}

	// Validate structural integrity.
	if errs := Validate(result); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}

	// Correctness: caller(5) must match VM.
	vmResults := runVMFunc(t, src, "caller", []runtime.Value{runtime.IntValue(5)})
	if len(vmResults) == 0 {
		t.Fatal("VM returned no results")
	}
	irResults, err := Interpret(result, []runtime.Value{runtime.IntValue(5)})
	if err != nil {
		t.Fatalf("IR interpreter error: %v", err)
	}
	if len(irResults) == 0 {
		t.Fatal("IR interpreter returned no results")
	}
	assertValuesEqual(t, "caller(5) with bounded mutual inlining", irResults[0], vmResults[0])
}
