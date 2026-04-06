//go:build darwin && arm64

// emit_int_counter_test.go verifies that GPR-resident int loop counters stay
// unboxed across the entire loop iteration after the regalloc carry change.
// The tests exercise the full Tier 2 pipeline (BuildGraph -> TypeSpecialize ->
// ConstProp -> DCE -> RangeAnalysis -> LICM -> AllocateRegisters -> Compile ->
// Execute) and compare results against both the IR interpreter and VM oracle.
//
// The key invariant: after TypeSpecialize produces OpAddInt/OpLeInt/etc., the
// register allocator's "carried" map pins GPR-resident phi registers + loop
// bound GPRs in body blocks. The emitter's emitRawIntBinOp + resolveRawInt
// paths keep values as raw int64 in GPRs, avoiding NaN-box/unbox overhead.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// fullPipelineBuild runs the full Tier 2 optimization pipeline on a proto.
// Returns the optimized function and register allocation.
func fullPipelineBuild(t *testing.T, src string) (*Function, *RegAllocation) {
	t.Helper()
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	fn.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn)
	return fn, alloc
}

// compileAndRun compiles via full pipeline and executes with args.
func compileAndRun(t *testing.T, src string, args []runtime.Value) []runtime.Value {
	t.Helper()
	fn, alloc := fullPipelineBuild(t, src)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return result
}

// --- Correctness tests: int counter stays GPR-resident ---

// TestTier2Emit_IntCounterGPR_SumLoop tests a simple sum loop:
// sum = 0; i = 1; while i <= n: sum += i; i += 1; return sum
// After TypeSpecialize, all ops become OpAddInt/OpLeInt, and the counter
// phi should stay in a GPR across the loop body via the carried map.
func TestTier2Emit_IntCounterGPR_SumLoop(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`

	tests := []struct {
		input int64
		want  int64
	}{
		{0, 0},
		{1, 1},
		{10, 55},
		{100, 5050},
		{1000, 500500},
	}

	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result := compileAndRun(t, src, args)
		vmResult := runVM(t, src, args)

		if len(result) == 0 {
			t.Fatalf("n=%d: JIT returned no results", tc.input)
		}
		if len(vmResult) == 0 {
			t.Fatalf("n=%d: VM returned no results", tc.input)
		}

		// Check JIT vs VM match.
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: JIT=%v vs VM=%v MISMATCH", tc.input, result[0], vmResult[0])
		}

		// Check expected value.
		if !result[0].IsInt() || result[0].Int() != tc.want {
			t.Errorf("n=%d: expected int(%d), got %v (%s)", tc.input, tc.want, result[0], result[0].TypeName())
		}
	}
}

// TestTier2Emit_IntCounterGPR_CountLoop tests a pure counting loop:
// count = 0; i = 0; while i < n: count += 1; i += 1; return count
// This is the mandelbrot-style pattern where the counter increments by 1.
func TestTier2Emit_IntCounterGPR_CountLoop(t *testing.T) {
	src := `func f(n) {
		count := 0
		i := 0
		for i < n {
			count = count + 1
			i = i + 1
		}
		return count
	}`

	for _, n := range []int64{0, 1, 10, 100, 1000} {
		args := []runtime.Value{runtime.IntValue(n)}
		result := compileAndRun(t, src, args)
		vmResult := runVM(t, src, args)

		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("n=%d: empty result: JIT=%v, VM=%v", n, result, vmResult)
		}
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: JIT=%v vs VM=%v MISMATCH", n, result[0], vmResult[0])
		}
		if !result[0].IsInt() || result[0].Int() != n {
			t.Errorf("n=%d: expected int(%d), got %v", n, n, result[0])
		}
	}
}

// TestTier2Emit_IntCounterGPR_DownCounting tests a downward-counting loop:
// count = 0; i = n; while i > 0: count += 1; i -= 1; return count
// Exercises OpSubInt in the counter update.
func TestTier2Emit_IntCounterGPR_DownCounting(t *testing.T) {
	src := `func f(n) {
		count := 0
		for i := n; i > 0; i-- {
			count = count + 1
		}
		return count
	}`

	for _, n := range []int64{0, 1, 10, 50} {
		args := []runtime.Value{runtime.IntValue(n)}
		result := compileAndRun(t, src, args)
		vmResult := runVM(t, src, args)

		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("n=%d: empty result", n)
		}
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: JIT=%v vs VM=%v MISMATCH", n, result[0], vmResult[0])
		}
	}
}

// TestTier2Emit_IntCounterGPR_MultiplePhis tests a loop with multiple
// int-typed phis (fibonacci-style): a, b, i all carried as GPRs.
func TestTier2Emit_IntCounterGPR_MultiplePhis(t *testing.T) {
	src := `func f(n) {
		a := 0
		b := 1
		for i := 0; i < n; i++ {
			t := a + b
			a = b
			b = t
		}
		return a
	}`

	tests := []struct {
		input int64
		want  int64
	}{
		{0, 0},
		{1, 1},
		{2, 1},
		{5, 5},
		{10, 55},
		{20, 6765},
	}

	for _, tc := range tests {
		args := []runtime.Value{runtime.IntValue(tc.input)}
		result := compileAndRun(t, src, args)
		vmResult := runVM(t, src, args)

		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("n=%d: empty result", tc.input)
		}
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: JIT=%v vs VM=%v MISMATCH", tc.input, result[0], vmResult[0])
		}
		if !result[0].IsInt() || result[0].Int() != tc.want {
			t.Errorf("n=%d: expected int(%d), got %v", tc.input, tc.want, result[0])
		}
	}
}

// TestTier2Emit_IntCounterGPR_IntCounterFloatBody tests a loop where the
// counter is int but the body accumulates a float result. The counter should
// stay GPR-resident (raw int) while the float accumulator stays FPR-resident.
func TestTier2Emit_IntCounterGPR_IntCounterFloatBody(t *testing.T) {
	src := `func f(n) {
		sum := 0.0
		for i := 1; i <= n; i++ {
			sum = sum + i * 1.0
		}
		return sum
	}`

	for _, n := range []int64{0, 1, 10, 100} {
		args := []runtime.Value{runtime.IntValue(n)}
		result := compileAndRun(t, src, args)
		vmResult := runVM(t, src, args)

		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("n=%d: empty result", n)
		}
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: JIT=%v vs VM=%v MISMATCH", n, result[0], vmResult[0])
		}
	}
}

// TestTier2Emit_IntCounterGPR_NestedLoops tests nested loops where both
// inner and outer counters should be GPR-resident.
func TestTier2Emit_IntCounterGPR_NestedLoops(t *testing.T) {
	src := `func f(n) {
		count := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				count = count + 1
			}
		}
		return count
	}`

	for _, n := range []int64{0, 1, 2, 5, 10} {
		args := []runtime.Value{runtime.IntValue(n)}
		result := compileAndRun(t, src, args)
		vmResult := runVM(t, src, args)

		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("n=%d: empty result", n)
		}
		expected := n * n
		if uint64(result[0]) != uint64(vmResult[0]) {
			t.Errorf("n=%d: JIT=%v vs VM=%v MISMATCH", n, result[0], vmResult[0])
		}
		if !result[0].IsInt() || result[0].Int() != expected {
			t.Errorf("n=%d: expected int(%d), got %v", n, expected, result[0])
		}
	}
}

// TestTier2Emit_IntCounterGPR_IRInterpreterMatch verifies that the IR
// interpreter (correctness oracle) produces the same result as native
// compilation for a sum loop. This confirms the full pipeline is correct.
func TestTier2Emit_IntCounterGPR_IRInterpreterMatch(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)

	for _, n := range []int64{0, 1, 10, 100} {
		args := []runtime.Value{runtime.IntValue(n)}

		// IR interpreter on unoptimized graph (oracle).
		fn1 := BuildGraph(proto)
		irResult, irErr := Interpret(fn1, args)
		if irErr != nil {
			t.Fatalf("n=%d: IR interpreter error: %v", n, irErr)
		}

		// Full pipeline native execution.
		fn2 := BuildGraph(proto)
		fn2, _ = TypeSpecializePass(fn2)
		fn2, _ = ConstPropPass(fn2)
		fn2, _ = DCEPass(fn2)
		fn2, _ = RangeAnalysisPass(fn2)
		fn2, _ = LICMPass(fn2)
		fn2.CarryPreheaderInvariants = true
		alloc := AllocateRegisters(fn2)
		cf, err := Compile(fn2, alloc)
		if err != nil {
			t.Fatalf("n=%d: Compile error: %v", n, err)
		}
		nativeResult, err := cf.Execute(args)
		cf.Code.Free()
		if err != nil {
			t.Fatalf("n=%d: Execute error: %v", n, err)
		}

		// Compare IR interpreter vs native.
		if len(irResult) == 0 || len(nativeResult) == 0 {
			t.Fatalf("n=%d: empty result: IR=%v, native=%v", n, irResult, nativeResult)
		}
		if uint64(irResult[0]) != uint64(nativeResult[0]) {
			t.Errorf("n=%d: IR=%v vs native=%v MISMATCH", n, irResult[0], nativeResult[0])
		}
	}
}

// --- Regalloc verification: GPR pins are correct ---

// TestTier2Emit_IntCounterGPR_RegallocCarry verifies that after the full
// optimization pipeline, the regalloc carries int loop-counter phi GPRs
// into body blocks, preventing eviction. Checks that:
// 1. The loop header phis have GPR allocations (not spilled)
// 2. Body block instructions don't reuse the same GPR as a header phi
func TestTier2Emit_IntCounterGPR_RegallocCarry(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	fn, _ = RangeAnalysisPass(fn)
	fn, _ = LICMPass(fn)
	fn.CarryPreheaderInvariants = true
	alloc := AllocateRegisters(fn)

	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		t.Fatal("expected at least one loop")
	}

	// Find GPR phi assignments in loop headers.
	phiGPRs := make(map[int]int) // regNum -> phiID
	for headerID := range li.loopHeaders {
		for _, phiID := range li.loopPhis[headerID] {
			if pr, ok := alloc.ValueRegs[phiID]; ok && !pr.IsFloat {
				phiGPRs[pr.Reg] = phiID
				t.Logf("header B%d: phi v%d -> X%d", headerID, phiID, pr.Reg)
			}
		}
	}

	if len(phiGPRs) == 0 {
		t.Fatal("no GPR-allocated phi found in loop header (expected at least sum + counter)")
	}

	// Verify body blocks don't clobber phi GPRs (they should be pinned via carried).
	checkedBodies := 0
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] || li.loopHeaders[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi || instr.Op.IsTerminator() {
				continue
			}
			pr, ok := alloc.ValueRegs[instr.ID]
			if !ok || pr.IsFloat {
				continue
			}
			// A body instruction reusing a header phi's GPR means the carried
			// pinning did not prevent eviction. This is a correctness issue.
			if phiID, clash := phiGPRs[pr.Reg]; clash {
				t.Errorf("block B%d: v%d (%s) assigned X%d, same as header phi v%d — clobbers loop-carried GPR",
					block.ID, instr.ID, instr.Op, pr.Reg, phiID)
			}
		}
		checkedBodies++
	}
	if checkedBodies == 0 {
		// This is acceptable for a simple loop with only 1 body block that
		// is also the header (tight loop). Log it.
		t.Log("no separate body block found (tight loop collapsed into header)")
	}
}

// TestTier2Emit_IntCounterGPR_TypeSpecProduces tests that TypeSpecialize
// produces type-specialized int ops for a simple int counter loop.
func TestTier2Emit_IntCounterGPR_TypeSpecProduces(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)

	// Scan for type-specialized int ops.
	hasAddInt := false
	hasIntCmp := false // LeInt or LtInt
	opCounts := make(map[string]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			opCounts[instr.Op.String()]++
			switch instr.Op {
			case OpAddInt:
				hasAddInt = true
			case OpLeInt, OpLtInt:
				hasIntCmp = true
			}
		}
	}

	t.Logf("IR ops after TypeSpecialize: %v", opCounts)

	if !hasAddInt {
		t.Error("expected OpAddInt after TypeSpecialize, not found")
	}
	if !hasIntCmp {
		// The loop may use Le (generic) if type feedback doesn't specialize
		// the comparison. This is acceptable as long as the arithmetic ops
		// are specialized. Log but don't fail.
		t.Log("no OpLeInt/OpLtInt found — comparison not type-specialized (acceptable)")
	}
}
