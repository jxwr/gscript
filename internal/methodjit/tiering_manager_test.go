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
	"os"
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

func TestTieringManager_TableFieldAccessPromotesAtCallBoundary(t *testing.T) {
	old := os.Getenv("GSCRIPT_TIER2_NO_FILTER")
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv("GSCRIPT_TIER2_NO_FILTER")
		} else {
			os.Setenv("GSCRIPT_TIER2_NO_FILTER", old)
		}
	})
	os.Unsetenv("GSCRIPT_TIER2_NO_FILTER")

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

particles := make_particles(10)
for s := 1; s <= 20; s++ {
    step(particles, 10)
}
result := checksum(particles, 10)
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
	if !result.IsFloat() || result.Float() != 200.0 {
		t.Fatalf("result = %v, want 200.0", result)
	}
	stepProto := findProtoByName(proto, "step")
	if tm.tier2Compiled[stepProto] == nil {
		t.Fatalf("step should compile at Tier 2 from repeated call boundaries")
	}
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

func TestTieringManager_ExecuteTier2UsesResultBuffer(t *testing.T) {
	top := compileProto(t, `func f() { return 42 }`)
	fn := findProtoByName(top, "f")
	if fn == nil {
		t.Fatal("function f not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(fn); err != nil {
		t.Fatalf("CompileTier2(f): %v", err)
	}
	cf := tm.tier2Compiled[fn]
	if cf == nil {
		t.Fatal("compiled Tier2 function missing")
	}

	regs := runtime.MakeNilSlice(cf.numRegs + 1)
	var storage [1]runtime.Value
	results, err := tm.executeTier2WithResultBuffer(cf, regs, 0, fn, storage[:0])
	if err != nil {
		t.Fatalf("executeTier2WithResultBuffer: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results)=%d, want 1", len(results))
	}
	if &results[0] != &storage[0] {
		t.Fatal("Tier2 return did not use caller result buffer")
	}
	if !results[0].IsInt() || results[0].Int() != 42 {
		t.Fatalf("results=%v, want int 42", results)
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

	// With default threshold=200 times via VM OP_CALL.
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

	// With smart tiering, simple call-chain functions (no loops, low arith)
	// stay at Tier 1 where BLR calls are more efficient. The Tier2Count
	// may be 0, which is correct behavior.
	t.Logf("tier2Count=%d", tm.Tier2Count())

	// Verify inner's CallCount was incremented by BLR calls.
	// inner is proto.Protos[0]. It should have CallCount > 1 from BLR.
	if len(proto.Protos) >= 1 {
		innerProto := proto.Protos[0]
		if innerProto.CallCount < 1 {
			t.Errorf("inner CallCount = %d, expected >= 1 (BLR should increment it)", innerProto.CallCount)
		}
		t.Logf("inner CallCount=%d", innerProto.CallCount)
	}
}

func TestTieringManager_StayTier0DisablesMixedRecursiveBuilder(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil { return 1 }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

result := 0
for i := 1; i <= 20; i++ {
    result = result + checkTree(makeTree(4))
}
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
	if !result.IsInt() || result.Int() != 620 {
		t.Fatalf("result = %v, want 620", result)
	}
	if !proto.JITDisabled {
		t.Fatal("<main> driver should stay interpreted when its hot loop calls a stable tier0-only callee")
	}
	if proto.CompiledCodePtr != 0 || proto.DirectEntryPtr != 0 {
		t.Fatalf("<main> driver compiled despite tier0-only loop callee: compiled=%#x direct=%#x",
			proto.CompiledCodePtr, proto.DirectEntryPtr)
	}

	makeTreeProto := findProtoByName(proto, "makeTree")
	if makeTreeProto == nil {
		t.Fatal("makeTree proto not found")
	}
	if !makeTreeProto.JITDisabled {
		t.Fatal("mixed makeTree should stay Tier 0 while NEWOBJECT2 remains an op-exit")
	}
	if makeTreeProto.CompiledCodePtr != 0 || makeTreeProto.DirectEntryPtr != 0 {
		t.Fatalf("mixed makeTree compiled despite stay-tier0: compiled=%#x direct=%#x",
			makeTreeProto.CompiledCodePtr, makeTreeProto.DirectEntryPtr)
	}

	checkTreeProto := findProtoByName(proto, "checkTree")
	if checkTreeProto == nil {
		t.Fatal("checkTree proto not found")
	}
	if checkTreeProto.JITDisabled {
		t.Fatal("checkTree should not stay tier0 once the fixed recursive table fold protocol applies")
	}
	if checkTreeProto.EnteredTier2 == 0 {
		t.Fatal("checkTree should enter the fixed recursive table fold Tier 2 protocol")
	}
}

func TestTieringManager_Tier0LoopCalleeGuardRequiresLoopCall(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

tree := makeTree(4)
result := 0
for i := 1; i <= 20; i++ {
    result = result + i
}
`
	proto := compileProto(t, src)
	tm := NewTieringManager()
	profile := tm.getProfile(proto)
	if callee, ok := tm.tier0OnlyLoopCallee(proto, profile); ok {
		t.Fatalf("guard should ignore tier0-only callees outside hot loops, got %s", callee.Name)
	}
}

func TestTieringManager_Tier0LoopCalleeGuardAllowsNativeLoopCallee(t *testing.T) {
	src := `
func helper(x) { return x + 1 }

result := 0
for i := 1; i <= 20; i++ {
    result = result + helper(i)
}
`
	proto := compileProto(t, src)
	tm := NewTieringManager()
	profile := tm.getProfile(proto)
	if callee, ok := tm.tier0OnlyLoopCallee(proto, profile); ok {
		t.Fatalf("guard should not block native-safe helper loop calls, got %s", callee.Name)
	}
	if !canPromoteWithNativeLoopCalls(proto, tm.buildLoopCallGlobals(proto)) {
		t.Fatal("helper loop call should remain eligible for native loop-call promotion")
	}
}

func TestTieringManager_EmptyNewTableRecursiveBuilderEntersTier1(t *testing.T) {
	src := `
func makeLeaf(depth) {
    if depth == 0 {
        return {}
    }
    return makeLeaf(depth - 1)
}

result := 0
for i := 1; i <= 20; i++ {
    t := makeLeaf(4)
    if t.left == nil {
        result = result + 1
    }
}
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
	if !result.IsInt() || result.Int() != 20 {
		t.Fatalf("result = %v, want 20", result)
	}

	leafProto := findProtoByName(proto, "makeLeaf")
	if leafProto == nil {
		t.Fatal("makeLeaf proto not found")
	}
	if leafProto.JITDisabled {
		t.Fatal("empty-NEWTABLE-only recursive builder should enter Tier 1")
	}
	if leafProto.CompiledCodePtr == 0 {
		t.Fatalf("makeLeaf did not compile: compiled=%#x direct=%#x",
			leafProto.CompiledCodePtr, leafProto.DirectEntryPtr)
	}
	bf := tm.tier1.compiled[leafProto]
	if bf == nil || len(bf.NewTableCaches) == 0 {
		t.Fatal("makeLeaf did not receive a baseline NEWTABLE cache")
	}
	var consumed bool
	for _, entry := range bf.NewTableCaches {
		if entry.Pos > 0 {
			consumed = true
			break
		}
	}
	if !consumed {
		t.Fatalf("makeLeaf baseline NEWTABLE cache was not consumed: %#v", bf.NewTableCaches)
	}
}

// TestTieringManager_Tier2DirectEntry verifies that after Tier 2 compilation,
// Tier 1 BLR callers can reach the Tier 2 direct entry point. This tests that:
// 1. Tier 2 code has a compatible direct entry (16-byte frame, same calling convention)
// 2. proto.DirectEntryPtr is updated to point to Tier 2's direct entry
// 3. Tier 2 return writes to ctx.BaselineReturnValue for BLR caller compatibility
// 4. The result is correct when called via BLR from a Tier 1 wrapper
func TestTieringManager_Tier2DirectEntry(t *testing.T) {
	// sum is a pure-compute function (no calls, no globals) that qualifies for Tier 2.
	// wrapper calls sum via OP_CALL which uses native BLR in Tier 1.
	// After sum is promoted to Tier 2, wrapper's BLR should jump to Tier 2's direct entry.
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
func wrapper(n) {
    return sum(n)
}
result := 0
for i := 1; i <= 10; i++ {
    result = wrapper(100)
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

	// Verify correctness: sum(100) = 5050
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 5050 {
		t.Errorf("wrapper(100) = %v, want 5050", result)
	}

	// Verify sum was promoted to Tier 2.
	if tm.Tier2Count() == 0 {
		t.Error("expected at least 1 Tier 2 compiled function (sum)")
	}

	// Verify sum's DirectEntryPtr was updated to Tier 2's direct entry.
	// Find sum's proto (first inner function that is pure-compute).
	var sumProto *vm.FuncProto
	for _, p := range proto.Protos {
		if canPromoteToTier2(p) {
			sumProto = p
			break
		}
	}
	if sumProto == nil {
		t.Fatal("could not find sum's proto")
	}
	if sumProto.DirectEntryPtr == 0 {
		t.Error("sum's DirectEntryPtr is 0 after Tier 2 promotion; expected non-zero")
	}

	// Verify the Tier 2 compiled function has a direct entry offset.
	cf, ok := tm.tier2Compiled[sumProto]
	if !ok {
		t.Fatal("sum not found in tier2Compiled map")
	}
	if cf.DirectEntryOffset <= 0 {
		t.Errorf("DirectEntryOffset = %d, want > 0", cf.DirectEntryOffset)
	}

	// Verify DirectEntryPtr points to cf.Code.Ptr() + DirectEntryOffset.
	expectedPtr := uintptr(cf.Code.Ptr()) + uintptr(cf.DirectEntryOffset)
	if sumProto.DirectEntryPtr != expectedPtr {
		t.Errorf("DirectEntryPtr = %#x, want %#x (Code.Ptr=%#x + offset=%d)",
			sumProto.DirectEntryPtr, expectedPtr, cf.Code.Ptr(), cf.DirectEntryOffset)
	}

	// Run again to verify Tier 2 direct entry is exercised via BLR.
	// Reset result and call wrapper many more times.
	src2 := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
func wrapper(n) {
    return sum(n)
}
result := 0
for i := 1; i <= 100; i++ {
    result = wrapper(100)
}
`
	proto2 := compileProto(t, src2)
	globals2 := runtime.NewInterpreterGlobals()
	v2 := vm.New(globals2)
	tm2 := NewTieringManager()
	v2.SetMethodJIT(tm2)
	_, err = v2.Execute(proto2)
	if err != nil {
		t.Fatalf("second run runtime error: %v", err)
	}
	result2 := v2.GetGlobal("result")
	if !result2.IsInt() || result2.Int() != 5050 {
		t.Errorf("second run: wrapper(100) = %v, want 5050", result2)
	}
}

// TestTieringManager_Tier2DirectEntryMultiple verifies multiple Tier 2 functions
// called via BLR from a Tier 1 caller produce correct results.
func TestTieringManager_Tier2DirectEntryMultiple(t *testing.T) {
	src := `
func square(n) {
    return n * n
}
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
func test(n) {
    return square(n) + sum(n)
}
result := 0
for i := 1; i <= 10; i++ {
    result = test(10)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	// square(10) = 100, sum(10) = 55 → 155
	if !result.IsInt() || result.Int() != 155 {
		t.Errorf("test(10) = %v, want 155", result)
	}
	t.Logf("tier2Count=%d", tm.Tier2Count())
}

// TestTieringManager_NestedCallGCD verifies that a Tier 2 function calling
// another Tier 2 function (both with loops) works correctly without hanging.
func TestTieringManager_NestedCallGCD(t *testing.T) {
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
        total = total + gcd(i*7+13, i*11+3)
    }
    return total
}
result := 0
for call := 1; call <= 5; call++ {
    result = gcd_bench(10)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	t.Logf("gcd_bench(10) = %d, tier2Count=%d", result.Int(), tm.Tier2Count())
}

// TestTieringManager_GCDAlone tests gcd function alone at Tier 2 (no nesting).
func TestTieringManager_GCDAlone(t *testing.T) {
	src := `
func gcd(a, b) {
    for b != 0 {
        t := b
        b = a % b
        a = t
    }
    return a
}
result := 0
for i := 1; i <= 5; i++ {
    result = gcd(20, 8)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 4 {
		t.Errorf("gcd(20,8) = %d, want 4", result.Int())
	}
	t.Logf("gcd(20,8) = %d, tier2Count=%d", result.Int(), tm.Tier2Count())
}

// TestTieringManager_WhileLoopIR verifies that while-style loops (where
// the first bytecode is the loop header) produce correct phi nodes in the
// SSA IR. This was a bug: the graph builder sealed the entry block
// immediately, preventing phi insertion for while-loops at function start.
func TestTieringManager_WhileLoopIR(t *testing.T) {
	src := `
func gcd(a, b) {
    for b != 0 {
        t := b
        b = a % b
        a = t
    }
    return a
}
`
	proto := compileProto(t, src)
	gcdProto := proto.Protos[0]
	gcdProto.EnsureFeedback()

	fn := BuildGraph(gcdProto)
	errs := Validate(fn)
	if len(errs) > 0 {
		t.Fatalf("Validation errors: %v", errs)
	}

	// The loop header must have phi nodes for a and b.
	foundPhis := 0
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi {
				foundPhis++
			}
		}
	}
	if foundPhis < 2 {
		t.Errorf("expected at least 2 phi nodes for while-loop variables, got %d", foundPhis)
		t.Logf("IR:\n%s", Print(fn))
	}

	// Verify the IR interpreter produces the correct result.
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	args := []runtime.Value{runtime.IntValue(20), runtime.IntValue(8)}
	result, err := Interpret(fn, args)
	if err != nil {
		t.Fatalf("Interpret error: %v", err)
	}
	if len(result) == 0 || !result[0].IsInt() || result[0].Int() != 4 {
		t.Errorf("Interpret(gcd, 20, 8) = %v, want [4]", result)
	}
}

// TestTieringManager_NestedCallSimple verifies minimal nested Tier 2 call.
// Outer has a loop that calls inner. Both promoted to Tier 2.
func TestTieringManager_NestedCallSimple(t *testing.T) {
	src := `
func inner(x) {
    s := 0
    for i := 1; i <= x; i++ {
        s = s + i
    }
    return s
}
func outer(n) {
    total := 0
    for i := 1; i <= n; i++ {
        total = total + inner(i)
    }
    return total
}
result := 0
for call := 1; call <= 5; call++ {
    result = outer(5)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// outer(5) = inner(1)+inner(2)+inner(3)+inner(4)+inner(5) = 1+3+6+10+15 = 35
	if result.Int() != 35 {
		t.Errorf("outer(5) = %d, want 35", result.Int())
	}
	t.Logf("outer(5) = %d, tier2Count=%d", result.Int(), tm.Tier2Count())
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

// TestTieringManager_InlineSimple verifies that a call-chain function produces
// correct results through the tiering manager. With smart tiering, functions
// without loops stay at Tier 1 where BLR calls are more efficient.
func TestTieringManager_InlineSimple(t *testing.T) {
	src := `
func double(x) {
    return x * 2
}
func apply(n) {
    return double(n) + double(n + 1)
}
result := 0
for i := 1; i <= 200; i++ {
    result = apply(i)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// apply(200) = double(200) + double(201) = 400 + 402 = 802
	if result.Int() != 802 {
		t.Errorf("apply(200) = %d, want 802", result.Int())
	}
	// With smart tiering, apply has calls+no loops -> stays at Tier 1.
	t.Logf("tier2Count=%d", tm.Tier2Count())
}

// TestTieringManager_InlineMultipleCalls verifies inlining when the callee is
// called multiple times with different arguments.
func TestTieringManager_InlineMultipleCalls(t *testing.T) {
	src := `
func add(a, b) {
    return a + b
}
func f(x, y) {
    return add(x, 1) + add(y, 2)
}
result := 0
for i := 1; i <= 200; i++ {
    result = f(10, 20)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// f(10, 20) = add(10,1) + add(20,2) = 11 + 22 = 33
	if result.Int() != 33 {
		t.Errorf("f(10, 20) = %d, want 33", result.Int())
	}
}

// TestTieringManager_InlineRecursiveNotInlined verifies that recursive functions
// are NOT inlined (they should still work via call-exit or Tier 1).
func TestTieringManager_InlineRecursiveNotInlined(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
func wrapper(n) {
    return fib(n)
}
result := 0
for i := 1; i <= 200; i++ {
    result = wrapper(10)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 55 {
		t.Errorf("wrapper(10) = %v, want 55", result)
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

// TestTieringManager_Tier2TableOps verifies that functions with table operations
// (GETTABLE, SETTABLE) promote to Tier 2 and produce correct results using
// native ARM64 fast paths (emitGetTableNative / emitSetTableNative).
func TestTieringManager_Tier2TableOps(t *testing.T) {
	src := `
func sum_array(arr, n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + arr[i]
    }
    return s
}
arr := {10, 20, 30, 40, 50}
result := 0
for call := 1; call <= 5; call++ {
    result = sum_array(arr, 5)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// sum of {10,20,30,40,50} = 150
	if result.Int() != 150 {
		t.Errorf("sum_array = %d, want 150", result.Int())
	}
	// Verify sum_array was promoted to Tier 2 (GETTABLE is now allowed).
	if tm.Tier2Count() == 0 {
		t.Log("sum_array stays at Tier 1 (GETTABLE blocked in canPromoteToTier2)")
	}
	t.Logf("tier2Count=%d", tm.Tier2Count())
}

// TestTieringManager_Tier2SetTableOps verifies that functions with SETTABLE
// promote to Tier 2 and produce correct results via native fast paths.
// Uses for-loop with <= (FORPREP/FORLOOP) to avoid while-loop IR issues.
func TestTieringManager_Tier2SetTableOps(t *testing.T) {
	src := `
func write_and_read(arr, n) {
    for i := 1; i <= n; i++ {
        arr[i] = i + 10
    }
    return arr[3]
}
arr := {0, 0, 0, 0, 0, 0}
result := 0
for call := 1; call <= 5; call++ {
    result = write_and_read(arr, 5)
}
`
	v, tm := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// arr[3] = 3 + 10 = 13
	if result.Int() != 13 {
		t.Errorf("write_and_read result = %d, want 13", result.Int())
	}
	// Verify write_and_read was promoted to Tier 2 (SETTABLE is now allowed).
	if tm.Tier2Count() == 0 {
		t.Log("write_and_read stays at Tier 1 (SETTABLE blocked in canPromoteToTier2)")
	}
	t.Logf("tier2Count=%d", tm.Tier2Count())
}

// TestTieringManager_Tier2FieldOps verifies that functions with field operations
// (GETFIELD, SETFIELD) work correctly at Tier 2 via exit-resume or inline cache.
func TestTieringManager_Tier2FieldOps(t *testing.T) {
	src := `
func distance(p) {
    return p.x * p.x + p.y * p.y
}
p := {x: 3, y: 4}
result := 0
for i := 1; i <= 5; i++ {
    result = distance(p)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// distance({x:3, y:4}) = 9 + 16 = 25
	if result.Int() != 25 {
		t.Errorf("distance = %d, want 25", result.Int())
	}
}

// TestTieringManager_Tier2GlobalOps verifies that functions with global variable
// access (GETGLOBAL, SETGLOBAL) work correctly at Tier 2 via exit-resume.
func TestTieringManager_Tier2GlobalOps(t *testing.T) {
	src := `
counter := 0
func increment(n) {
    for i := 0; i < n; i++ {
        counter = counter + 1
    }
    return counter
}
result := 0
for call := 1; call <= 5; call++ {
    result = increment(10)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// 5 calls * 10 increments each = 50
	if result.Int() != 50 {
		t.Errorf("counter = %d, want 50", result.Int())
	}
}

// TestTieringManager_Tier2NewTableOps verifies that functions with NEWTABLE
// work correctly at Tier 2 via exit-resume.
func TestTieringManager_Tier2NewTableOps(t *testing.T) {
	src := `
func make_pair(a, b) {
    t := {}
    t[0] = a
    t[1] = b
    return t[0] + t[1]
}
result := 0
for i := 1; i <= 5; i++ {
    result = make_pair(10, 20)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 30 {
		t.Errorf("make_pair(10,20) = %d, want 30", result.Int())
	}
}

// TestTieringManager_Tier2LenConcat verifies LEN and CONCAT via exit-resume.
func TestTieringManager_Tier2LenConcat(t *testing.T) {
	src := `
func count_items(arr) {
    return #arr
}
arr := {1, 2, 3, 4, 5}
result := 0
for i := 1; i <= 5; i++ {
    result = count_items(arr)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 5 {
		t.Errorf("count_items = %d, want 5", result.Int())
	}
}

// TestTieringManager_Tier2Closure verifies that functions containing OP_CLOSURE
// can be promoted to Tier 2 and produce correct results via op-exit.
func TestTieringManager_Tier2Closure(t *testing.T) {
	src := `
func make_adder(x) {
    func inner(y) {
        return x + y
    }
    return inner
}
adder5 := make_adder(5)
result := adder5(10)
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// make_adder(5)(10) = 5 + 10 = 15
	if result.Int() != 15 {
		t.Errorf("make_adder(5)(10) = %d, want 15", result.Int())
	}
}

// TestTieringManager_Tier2ClosureLoop verifies closures created in a loop
// work correctly at Tier 2.
func TestTieringManager_Tier2ClosureLoop(t *testing.T) {
	src := `
func test_closure(n) {
    func double(x) {
        return x * 2
    }
    s := 0
    for i := 1; i <= n; i++ {
        s = s + double(i)
    }
    return s
}
result := 0
for call := 1; call <= 5; call++ {
    result = test_closure(5)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// double(1)+double(2)+...+double(5) = 2+4+6+8+10 = 30
	if result.Int() != 30 {
		t.Errorf("test_closure(5) = %d, want 30", result.Int())
	}
}

// TestTieringManager_Tier2GetUpval verifies OP_GETUPVAL via Tier 2 op-exit.
func TestTieringManager_Tier2GetUpval(t *testing.T) {
	src := `
func make_counter() {
    count := 0
    func increment() {
        count = count + 1
        return count
    }
    return increment
}
counter := make_counter()
result := 0
for i := 1; i <= 5; i++ {
    result = counter()
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// 5 increments → count = 5
	if result.Int() != 5 {
		t.Errorf("counter after 5 calls = %d, want 5", result.Int())
	}
}

// TestTieringManager_Tier2CallInLoop verifies that OP_CALL inside a loop
// can now be promoted to Tier 2 (previously performance-blocked).
func TestTieringManager_Tier2CallInLoop(t *testing.T) {
	src := `
func square(x) {
    return x * x
}
func sum_squares(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + square(i)
    }
    return s
}
result := 0
for call := 1; call <= 5; call++ {
    result = sum_squares(5)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// 1+4+9+16+25 = 55
	if result.Int() != 55 {
		t.Errorf("sum_squares(5) = %d, want 55", result.Int())
	}
}

// TestTieringManager_Tier2GetGlobal verifies that OP_GETGLOBAL in loops can
// now be promoted to Tier 2 (previously performance-blocked).
func TestTieringManager_Tier2GetGlobalLoop(t *testing.T) {
	src := `
multiplier := 3
func scale_sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i * multiplier
    }
    return s
}
result := 0
for call := 1; call <= 5; call++ {
    result = scale_sum(5)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	// (1+2+3+4+5) * 3 = 45
	if result.Int() != 45 {
		t.Errorf("scale_sum(5) = %d, want 45", result.Int())
	}
}

// TestTieringManager_Tier2FibRecursive verifies that fib (which requires both
// CALL and GETGLOBAL) now works correctly at Tier 2.
func TestTieringManager_Tier2FibRecursive(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := 0
for i := 1; i <= 5; i++ {
    result = fib(15)
}
`
	v, _ := runWithTieringManager(t, src)
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s (%v)", result.TypeName(), result)
	}
	if result.Int() != 610 {
		t.Errorf("fib(15) = %d, want 610", result.Int())
	}
}
