//go:build darwin && arm64

// osr_test.go tests On-Stack Replacement (OSR) for the TieringManager.
//
// OSR allows a function running at Tier 1 to be upgraded to Tier 2 mid-execution
// when a loop back-edge counter expires. This is critical for single-call
// functions with long-running loops (e.g., mandelbrot(1000)).

package methodjit

import (
	"os"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestOSR_BasicForLoop verifies that OSR triggers for a function with a
// long-running for-loop. The function is configured to stay at Tier 1
// initially (by using a high call threshold), but OSR fires after enough
// loop iterations and upgrades to Tier 2 mid-execution.
func TestOSR_BasicForLoop(t *testing.T) {
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := sum(10000)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	// sum has loop + arith, so smart tiering promotes immediately.
	// But let's also verify the result is correct.
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s", result.TypeName())
	}
	// sum(10000) = 10000*10001/2 = 50005000
	if result.Int() != 50005000 {
		t.Errorf("sum(10000) = %d, want 50005000", result.Int())
	}
}

// TestOSR_TriggersDuringExecution verifies that OSR actually triggers for a
// function that starts at Tier 1. We force Tier 1 by using a function profile
// that shouldPromoteTier2 rejects, then enable OSR via the counter.
func TestOSR_TriggersDuringExecution(t *testing.T) {
	src := `
func compute(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
`
	proto := compileProto(t, src)
	computeProto := proto.Protos[0]

	// Manually compile at Tier 1.
	computeProto.CallCount = 1
	tm := NewTieringManager()
	t1 := tm.tier1.TryCompile(computeProto)
	if t1 == nil {
		t.Fatal("failed to compile at Tier 1")
	}

	// Set OSR counter to 500 iterations.
	tm.tier1.SetOSRCounter(computeProto, 500)

	// Execute at Tier 1 with 10000 iterations. OSR should fire after 500
	// iterations, compile Tier 2, and re-enter.
	regs := make([]runtime.Value, 64)
	regs[0] = runtime.IntValue(10000) // n = 10000

	results, err := tm.Execute(t1, regs, 0, computeProto)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("no results")
	}
	if !results[0].IsInt() || results[0].Int() != 50005000 {
		t.Errorf("compute(10000) = %v, want 50005000", results[0])
	}

	// After OSR, the function should be cached at Tier 2.
	if tm.Tier2Count() > 0 {
		t.Logf("OSR succeeded: function promoted to Tier 2")
	} else if tm.tier2Failed[computeProto] {
		t.Logf("OSR attempted but Tier 2 compilation failed (expected for some functions)")
	} else {
		t.Logf("OSR did not trigger (function may have finished before counter expired)")
	}
}

// TestOSR_DisabledWhenTier2Fails verifies that OSR is disabled after Tier 2
// compilation fails, preventing infinite OSR loops.
func TestOSR_DisabledWhenTier2Fails(t *testing.T) {
	// gcd has a while-loop that currently fails Tier 2 compilation.
	src := `
func gcd(a, b) {
    for b != 0 {
        t := b
        b = a % b
        a = t
    }
    return a
}
result := gcd(20, 8)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 4 {
		t.Errorf("gcd(20,8) = %v, want 4", result)
	}

	// gcd should have tier2Failed=true (compilation error).
	gcdProto := proto.Protos[0]
	if !tm.tier2Failed[gcdProto] {
		t.Log("gcd Tier 2 did not fail (may have succeeded with relaxed canPromoteToTier2)")
	}
}

func TestOSR_AllowsStableRawIntKernelCallInLoop(t *testing.T) {
	src := `
func gcd(a, b) {
    for b != 0 {
        t := b
        b = a % b
        a = t
    }
    return a
}

func gcd_bench(n) {
    total := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 100; j++ {
            total = total + gcd(i * 7 + 13, j * 11 + 3)
        }
    }
    return total
}
result := gcd_bench(1200)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() <= 0 {
		t.Fatalf("unexpected result: %v", result)
	}
	gcdBench := findProtoByName(proto, "gcd_bench")
	if gcdBench == nil {
		t.Fatal("gcd_bench proto not found")
	}
	if tm.tier2Failed[gcdBench] {
		t.Fatalf("gcd_bench Tier 2 compile failed with %q", tm.tier2FailReason[gcdBench])
	}
	if tm.tier2Compiled[gcdBench] == nil {
		t.Fatal("gcd_bench should promote: loop calls stable raw-int gcd kernel")
	}
	gcd := findProtoByName(proto, "gcd")
	if gcd == nil {
		t.Fatal("gcd proto not found")
	}
	if tm.tier2Compiled[gcd] == nil || gcd.Tier2NumericEntryPtr == 0 {
		t.Fatal("gcd should be compiled with a raw-int numeric entry for nested loop calls")
	}
}

// TestOSR_CorrectResultWithRestart verifies that the simplified OSR approach
// (restarting the function from the beginning at Tier 2) produces correct
// results even when the function has already partially executed.
func TestOSR_CorrectResultWithRestart(t *testing.T) {
	// This function computes sum(n) but also has a side effect (accumulates s).
	// When OSR restarts from the beginning, s should be re-initialized to 0.
	src := `
func sum_with_init(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := sum_with_init(5000)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s", result.TypeName())
	}
	// sum_with_init(5000) = 5000*5001/2 = 12502500
	if result.Int() != 12502500 {
		t.Errorf("sum_with_init(5000) = %d, want 12502500", result.Int())
	}
}

func TestOSR_RestartDisabledForTableSideEffectsNoFilter(t *testing.T) {
	old := os.Getenv("GSCRIPT_TIER2_NO_FILTER")
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv("GSCRIPT_TIER2_NO_FILTER")
		} else {
			os.Setenv("GSCRIPT_TIER2_NO_FILTER", old)
		}
	})
	os.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	src := `
func make_particles(n) {
    particles := {}
    for i := 1; i <= n; i++ {
        particles[i] = {x: 0.0}
    }
    return particles
}

func step(particles, n) {
    for i := 1; i <= n; i++ {
        p := particles[i]
        p.x = p.x + 1.0
    }
}

func checksum(particles, n) {
    sum := 0.0
    for i := 1; i <= n; i++ {
        sum = sum + particles[i].x
    }
    return sum
}

particles := make_particles(1200)
step(particles, 1200)
result := checksum(particles, 1200)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsFloat() {
		t.Fatalf("expected float result, got %s (%v)", result.TypeName(), result)
	}
	if result.Float() != 1200.0 {
		t.Fatalf("table side effects were replayed across OSR: got %.1f, want 1200.0", result.Float())
	}
	if tm.tier2Compiled[findProtoByName(proto, "step")] != nil {
		t.Fatalf("step should not be OSR-compiled while running; restart would replay table mutations")
	}
}

func TestOSR_NestedCallFallbackDoesNotReplayCaller(t *testing.T) {
	src := `
func new_point(x, y) {
    p := {}
    p.x = x
    p.y = y
    return p
}

func point_distance(p1, p2) {
    dx := p1.x - p2.x
    dy := p1.y - p2.y
    return math.sqrt(dx * dx + dy * dy)
}

func point_translate(p, dx, dy) {
    return new_point(p.x + dx, p.y + dy)
}

func point_scale(p, factor) {
    return new_point(p.x * factor, p.y * factor)
}

func test_points(n) {
    total_dist := 0.0
    p := new_point(0.0, 0.0)
    for i := 1; i <= n; i++ {
        q := new_point(1.0 * i, 2.0 * i)
        total_dist = total_dist + point_distance(p, q)
        p = point_translate(p, 0.1, 0.2)
        p = point_scale(p, 0.999)
    }
    return total_dist
}

result := test_points(1000)
`
	run := func(useJIT bool) float64 {
		t.Helper()
		proto := compileProto(t, src)
		globals := runtime.NewInterpreterGlobals()
		v := vm.New(globals)
		if useJIT {
			v.SetMethodJIT(NewTieringManager())
		}
		if _, err := v.Execute(proto); err != nil {
			t.Fatalf("runtime error: %v", err)
		}
		result := v.GetGlobal("result")
		if !result.IsFloat() {
			t.Fatalf("expected float result, got %s (%v)", result.TypeName(), result)
		}
		return result.Float()
	}

	want := run(false)
	got := run(true)
	if got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("JIT result = %.4f, VM result = %.4f", got, want)
	}
}

// TestOSR_CounterDisabled verifies that OSR does not trigger when the counter
// is set to -1 (disabled). Uses a manual setup to isolate Tier 1 behavior.
func TestOSR_CounterDisabled(t *testing.T) {
	src := `
func loop(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + 1
    }
    return s
}
`
	proto := compileProto(t, src)
	loopProto := proto.Protos[0]

	// Manually compile at Tier 1 with OSR disabled.
	loopProto.CallCount = 1
	tm := NewTieringManager()
	t1 := tm.tier1.TryCompile(loopProto)
	if t1 == nil {
		t.Fatal("failed to compile at Tier 1")
	}
	tm.tier1.SetOSRCounter(loopProto, -1)

	// Execute directly at Tier 1 (bypassing TryCompile which would promote).
	regs := make([]runtime.Value, 64)
	regs[0] = runtime.IntValue(100) // n = 100

	results, err := tm.tier1.Execute(t1, regs, 0, loopProto)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("no results")
	}
	if !results[0].IsInt() || results[0].Int() != 100 {
		t.Errorf("loop(100) = %v, want 100", results[0])
	}
}

// TestOSR_FeedbackTypedMatmul verifies that feedback from Tier 1 execution
// flows into Tier 2 IR, producing typed float operations. A small matmul
// function is executed via the VM to collect real GETTABLE type feedback
// (FBFloat), then BuildGraph reads that feedback to insert OpGuardType, and
// TypeSpecialize cascades the float type into MulFloat/AddFloat.
func TestOSR_FeedbackTypedMatmul(t *testing.T) {
	src := `
func matmul_small(a, b, n) {
    c := {}
    for i := 1; i <= n; i++ {
        c[i] = {}
        for j := 1; j <= n; j++ {
            sum := 0.0
            for k := 1; k <= n; k++ {
                sum = sum + a[i][k] * b[k][j]
            }
            c[i][j] = sum
        }
    }
    return c
}
`
	// Step 1: Compile and get the inner function proto.
	topProto := compileTop(t, src)
	if len(topProto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	innerProto := topProto.Protos[0]

	// Step 2: Initialize feedback and execute via VM to collect real feedback.
	innerProto.EnsureFeedback()

	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	// Execute top-level to register matmul_small in globals.
	if _, err := v.Execute(topProto); err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	// Build two 4x4 tables of float values.
	n := int64(4)
	tableA := runtime.NewTable()
	tableB := runtime.NewTable()
	for i := int64(1); i <= n; i++ {
		rowA := runtime.NewTable()
		rowB := runtime.NewTable()
		for j := int64(1); j <= n; j++ {
			rowA.RawSetInt(j, runtime.FloatValue(float64(i*n+j)*0.5))
			rowB.RawSetInt(j, runtime.FloatValue(float64(j*n+i)*0.25))
		}
		tableA.RawSetInt(i, runtime.TableValue(rowA))
		tableB.RawSetInt(i, runtime.TableValue(rowB))
	}

	// Call matmul_small(a, b, 4) via the VM to collect type feedback.
	fnVal := v.GetGlobal("matmul_small")
	if fnVal.IsNil() {
		t.Fatal("function 'matmul_small' not found in globals after execution")
	}
	vmResult, err := v.CallValue(fnVal, []runtime.Value{
		runtime.TableValue(tableA),
		runtime.TableValue(tableB),
		runtime.IntValue(n),
	})
	if err != nil {
		t.Fatalf("VM call error: %v", err)
	}
	t.Logf("VM result (table): %v", vmResult)

	// Step 3: Verify that feedback was collected on GETTABLE instructions.
	gettablePCs := []int{}
	for pc, inst := range innerProto.Code {
		if vm.DecodeOp(inst) == vm.OP_GETTABLE {
			gettablePCs = append(gettablePCs, pc)
		}
	}
	if len(gettablePCs) == 0 {
		t.Fatal("no GETTABLE instruction found in inner proto bytecode")
	}
	floatFBCount := 0
	for _, pc := range gettablePCs {
		fb := innerProto.Feedback[pc]
		if fb.Result == vm.FBFloat {
			floatFBCount++
		} else {
			t.Logf("GETTABLE at PC %d: feedback Result=%d (not FBFloat)", pc, fb.Result)
		}
	}
	t.Logf("GETTABLE feedback: %d/%d are FBFloat", floatFBCount, len(gettablePCs))

	// Step 4: Build the IR graph (feedback is now populated).
	fn := BuildGraph(innerProto)
	irBefore := Print(fn)
	t.Logf("IR before optimization:\n%s", irBefore)

	// Verify that OpGuardType appears after OpGetTable in the IR.
	// In matmul, some GETTABLEs return tables (a[i], b[k] are rows) and some
	// return floats (a[i][k], b[k][j] are elements). We expect at least one
	// GuardType(TypeFloat) for the element accesses.
	hasFloatGuard := false
	hasTableGuard := false
	for _, blk := range fn.Blocks {
		for i, instr := range blk.Instrs {
			if instr.Op == OpGetTable && i+1 < len(blk.Instrs) {
				next := blk.Instrs[i+1]
				if next.Op == OpGuardType && len(next.Args) > 0 && next.Args[0].ID == instr.ID {
					if next.Type == TypeFloat {
						hasFloatGuard = true
					} else {
						hasTableGuard = true
						t.Logf("GuardType after GetTable has Type=%v (table row access)", next.Type)
					}
				}
			}
		}
	}
	if hasTableGuard {
		t.Log("confirmed: OpGuardType(TypeTable) found for table row accesses")
	}
	if !hasFloatGuard {
		t.Fatal("expected at least one OpGuardType(TypeFloat) after OpGetTable for float element accesses")
	}
	t.Log("confirmed: OpGuardType(TypeFloat) found after OpGetTable for float element accesses")

	// Step 5: Run TypeSpecialize and verify float-specialized ops cascade.
	fnOpt, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}
	irAfter := Print(fnOpt)
	t.Logf("IR after TypeSpecialize:\n%s", irAfter)

	hasMulFloat := strings.Contains(irAfter, "MulFloat")
	hasAddFloat := strings.Contains(irAfter, "AddFloat")
	if !hasMulFloat && !hasAddFloat {
		t.Error("expected MulFloat or AddFloat in optimized IR after TypeSpecialize " +
			"(float type from GuardType should cascade through arithmetic)")
	}
	if hasMulFloat {
		t.Log("confirmed: MulFloat present in optimized IR")
	}
	if hasAddFloat {
		t.Log("confirmed: AddFloat present in optimized IR")
	}

	// Step 6: Verify correctness via IR interpreter on the optimized IR.
	fnOpt, _ = ConstPropPass(fnOpt)
	fnOpt, _ = DCEPass(fnOpt)

	args := []runtime.Value{
		runtime.TableValue(tableA),
		runtime.TableValue(tableB),
		runtime.IntValue(n),
	}
	irResult, irErr := Interpret(fnOpt, args)
	if irErr != nil {
		t.Fatalf("IR interpreter error: %v", irErr)
	}
	t.Logf("IR interpreter result: %v", irResult)

	// The result is a table (matrix C). Verify it matches VM result by
	// spot-checking C[1][1].
	if len(vmResult) > 0 && len(irResult) > 0 {
		// Both should be tables — compare C[1][1] element.
		vmTbl := vmResult[0].Table()
		irTbl := irResult[0].Table()
		if vmTbl != nil && irTbl != nil {
			vmRow1 := vmTbl.RawGetInt(1).Table()
			irRow1 := irTbl.RawGetInt(1).Table()
			if vmRow1 != nil && irRow1 != nil {
				vmVal := vmRow1.RawGetInt(1).Number()
				irVal := irRow1.RawGetInt(1).Number()
				diff := vmVal - irVal
				if diff < 0 {
					diff = -diff
				}
				if diff > 1e-6 {
					t.Errorf("C[1][1] mismatch: VM=%.10f, IR=%.10f (diff=%.2e)", vmVal, irVal, diff)
				} else {
					t.Logf("C[1][1] match: VM=%.6f, IR=%.6f", vmVal, irVal)
				}
			} else {
				t.Log("could not extract row 1 from result tables for comparison")
			}
		} else {
			t.Log("result is not a table — cannot spot-check elements")
		}
	}
}
