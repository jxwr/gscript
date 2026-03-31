//go:build darwin && arm64

// tiering_manager_test.go tests the TieringManager that automatically promotes
// functions from Tier 1 (baseline JIT) to Tier 2 (optimizing JIT).
//
// Tests verify:
// - Functions start at Tier 1 and produce correct results
// - Functions automatically promote to Tier 2 after sufficient calls
// - Tier 2 compiled functions produce correct results
// - Tier 2 compilation failure falls back to Tier 1

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runWithTieringManager compiles and runs GScript source with the TieringManager.
// Returns the VM and manager for inspection.
func runWithTieringManager(t *testing.T, src string) (*vm.VM, *TieringManager) {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return v, tm
}

// TestTieringManager_Sum verifies sum(100)=5050 produces correct results
// at both Tier 1 and Tier 2.
func TestTieringManager_Sum(t *testing.T) {
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
    result = sum(100)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 5050 {
		t.Errorf("sum(100) = %d, want 5050", result.Int())
	}
}

// TestTieringManager_Promotion verifies explicit Tier 2 promotion via
// CompileTier2. Automatic promotion requires counter integration in Tier 1
// code (future work); this test verifies the Tier 2 compilation and execution
// path works correctly when triggered explicitly.
func TestTieringManager_Promotion(t *testing.T) {
	src := `
func add(a, b) {
    return a + b
}
result := add(3, 4)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	// First run: Tier 1 compiles add, gets correct result.
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 7 {
		t.Fatalf("Tier 1: add(3,4) = %v, want 7", result)
	}

	// Explicitly promote add to Tier 2.
	if len(proto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	addProto := proto.Protos[0]
	if err := tm.CompileTier2(addProto); err != nil {
		t.Fatalf("CompileTier2 failed: %v", err)
	}
	if tm.Tier2Count() != 1 {
		t.Errorf("expected Tier2Count=1, got %d", tm.Tier2Count())
	}

	// Run again: add should now execute via Tier 2.
	proto2 := compileProto(t, src)
	globals2 := runtime.NewInterpreterGlobals()
	v2 := vm.New(globals2)
	tm2 := NewTieringManager()
	// Pre-populate Tier 2 for the add function.
	v2.SetMethodJIT(tm2)
	if len(proto2.Protos) > 0 {
		addProto2 := proto2.Protos[0]
		addProto2.EnsureFeedback()
		tm2.CompileTier2(addProto2)
	}
	_, err = v2.Execute(proto2)
	if err != nil {
		t.Fatalf("runtime error with Tier 2: %v", err)
	}
	result2 := v2.GetGlobal("result")
	if !result2.IsInt() || result2.Int() != 7 {
		t.Errorf("Tier 2: add(3,4) = %v, want 7", result2)
	}
}

// TestTieringManager_AutoPromotion verifies that functions called enough times
// through the VM path (not BLR) get automatically promoted.
// With tmDefaultTier2Threshold=2, the second VM-path call triggers Tier 2.
func TestTieringManager_AutoPromotion(t *testing.T) {
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
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	// With default threshold=2, sum is called 200 times via VM OP_CALL.
	// After 2 calls, it should be promoted to Tier 2.
	if tm.Tier2Count() == 0 {
		t.Error("expected at least 1 Tier 2 compiled function with threshold=2")
	}

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 55 {
		t.Errorf("sum(10) = %v, want 55", result)
	}
}

// TestTieringManager_BLRCallCountIncrement verifies that Tier 1 native BLR
// calls increment the callee's CallCount. This is critical for Tier 2
// promotion of functions called primarily via BLR (not through the VM).
func TestTieringManager_BLRCallCountIncrement(t *testing.T) {
	// inner() is called from outer() via native BLR.
	// outer() is called 5 times via VM OP_CALL, each calling inner() once.
	// Without BLR call count increment, inner's CallCount would only be 5
	// (from the 5 VM-path calls). With the increment, BLR calls also count.
	src := `
func inner(x) {
    return x * 2
}
func outer(n) {
    return inner(n)
}
result := 0
for i := 1; i <= 5; i++ {
    result = outer(i)
}
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

	// Check correctness: outer(5) = inner(5) = 10
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 10 {
		t.Errorf("outer(5) = %v, want 10", result)
	}

	// inner() is called via BLR from outer(). The BLR call sequence
	// increments inner's CallCount. With default threshold=2, inner
	// should be promoted to Tier 2 after enough calls.
	// outer() is called 5 times via VM, so it should also be at Tier 2.
	if tm.Tier2Count() == 0 {
		t.Error("expected at least 1 Tier 2 compiled function")
	}

	// Verify inner's CallCount was incremented by BLR calls.
	// inner is proto.Protos[0]. It should have CallCount > 2 from BLR.
	if len(proto.Protos) >= 1 {
		innerProto := proto.Protos[0]
		if innerProto.CallCount < 2 {
			t.Errorf("inner CallCount = %d, expected >= 2 (BLR should increment it)", innerProto.CallCount)
		}
	}
}

// TestTieringManager_Tier1Correct verifies single-call functions produce
// correct results. With eager Tier 2 promotion, these may be at Tier 2.
func TestTieringManager_Tier1Correct(t *testing.T) {
	src := `
func double(x) {
    return x * 2
}
result := double(21)
`
	v, _ := runWithTieringManager(t, src)

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 42 {
		t.Errorf("double(21) = %v, want 42", result)
	}
}

// TestTieringManager_WithCall verifies that Tier 2 compiled functions
// can call other functions via call-exit.
func TestTieringManager_WithCall(t *testing.T) {
	src := `
func double(x) {
    return x * 2
}
func apply(n) {
    return double(n)
}
result := 0
for i := 1; i <= 200; i++ {
    result = apply(i)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// Last iteration: apply(200) = double(200) = 400
	if result.Int() != 400 {
		t.Errorf("apply(200) = %d, want 400", result.Int())
	}
}

// TestTieringManager_WithGlobal verifies that Tier 2 compiled functions
// can access globals via global-exit.
func TestTieringManager_WithGlobal(t *testing.T) {
	src := `
multiplier := 10
func scale(x) {
    return x * multiplier
}
result := 0
for i := 1; i <= 200; i++ {
    result = scale(i)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// Last iteration: scale(200) = 2000
	if result.Int() != 2000 {
		t.Errorf("scale(200) = %d, want 2000", result.Int())
	}
}

// TestTieringManager_Fib verifies recursive functions work through
// the tiering manager. fib uses call-exit for recursion + global-exit.
func TestTieringManager_Fib(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(10)
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 55 {
		t.Errorf("fib(10) = %d, want 55", result.Int())
	}
}

// TestTieringManager_ForLoop verifies a loop-heavy function through tiering.
func TestTieringManager_ForLoop(t *testing.T) {
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
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// sum(10) = 55
	if result.Int() != 55 {
		t.Errorf("sum(10) = %d, want 55", result.Int())
	}
}

// TestTieringManager_DropInReplacement verifies that TieringManager is a
// complete drop-in replacement for BaselineJITEngine by running the same
// programs and comparing results.
func TestTieringManager_DropInReplacement(t *testing.T) {
	programs := []struct {
		name   string
		src    string
		global string
		want   int64
	}{
		{
			name: "add",
			src: `
func add(a, b) { return a + b }
result := add(3, 4)
`,
			global: "result",
			want:   7,
		},
		{
			name: "call_chain",
			src: `
func double(x) { return x * 2 }
func apply(n) { return double(n) }
result := 0
for i := 1; i <= 200; i++ {
    result = apply(i)
}
`,
			global: "result",
			want:   400, // apply(200) = double(200) = 400
		},
		{
			name: "loop_sum",
			src: `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ { s = s + i }
    return s
}
result := sum(10)
`,
			global: "result",
			want:   55,
		},
	}

	for _, tc := range programs {
		t.Run(tc.name, func(t *testing.T) {
			// Run with baseline only
			baseGlobals := runVMFullWithJIT(t, tc.src)

			// Run with tiering manager
			v, _ := runWithTieringManager(t, tc.src)
			tmResult := v.GetGlobal(tc.global)
			baseResult := baseGlobals[tc.global]

			// Both should produce the same result
			if uint64(tmResult) != uint64(baseResult) {
				t.Errorf("TieringManager result %v != Baseline result %v",
					tmResult, baseResult)
			}
		})
	}
}
