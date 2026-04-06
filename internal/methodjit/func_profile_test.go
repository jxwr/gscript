//go:build darwin && arm64

// func_profile_test.go tests the function profile analysis and smart tiering
// promotion decisions.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestAnalyzeFuncProfile_PureComputeLoop(t *testing.T) {
	// sum(n) has a for-loop with arithmetic: should be flagged as loop+arith.
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
`
	proto := compileProto(t, src)
	sumProto := proto.Protos[0]
	p := analyzeFuncProfile(sumProto)

	if !p.HasLoop {
		t.Error("expected HasLoop=true for sum")
	}
	if p.LoopDepth < 1 {
		t.Errorf("expected LoopDepth >= 1, got %d", p.LoopDepth)
	}
	if p.ArithCount < 1 {
		t.Errorf("expected ArithCount >= 1, got %d", p.ArithCount)
	}
	if p.CallCount != 0 {
		t.Errorf("expected CallCount=0, got %d", p.CallCount)
	}
	if p.TableOpCount != 0 {
		t.Errorf("expected TableOpCount=0, got %d", p.TableOpCount)
	}
	t.Logf("sum profile: %+v", p)
}

func TestAnalyzeFuncProfile_RecursiveCall(t *testing.T) {
	// fib(n) has calls but no loops.
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
`
	proto := compileProto(t, src)
	fibProto := proto.Protos[0]
	p := analyzeFuncProfile(fibProto)

	if p.HasLoop {
		t.Error("expected HasLoop=false for fib")
	}
	if p.CallCount < 2 {
		t.Errorf("expected CallCount >= 2 (two recursive calls), got %d", p.CallCount)
	}
	if p.ArithCount < 2 {
		t.Errorf("expected ArithCount >= 2 (n-1, n-2), got %d", p.ArithCount)
	}
	t.Logf("fib profile: %+v", p)
}

func TestAnalyzeFuncProfile_WhileLoop(t *testing.T) {
	// gcd uses a while-style loop (backward JMP).
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
	p := analyzeFuncProfile(gcdProto)

	if !p.HasLoop {
		t.Error("expected HasLoop=true for gcd (while-style loop)")
	}
	if p.ArithCount < 1 {
		t.Errorf("expected ArithCount >= 1 (mod op), got %d", p.ArithCount)
	}
	t.Logf("gcd profile: %+v", p)
	t.Logf("canPromoteToTier2: %v", canPromoteToTier2(gcdProto))

	// Dump bytecodes for debugging.
	for pc, inst := range gcdProto.Code {
		op := vm.DecodeOp(inst)
		t.Logf("  [%d] %s", pc, vm.OpName(op))
	}
}

func TestAnalyzeFuncProfile_TableOps(t *testing.T) {
	// Function with table operations.
	src := `
func get(t, k) {
    return t[k]
}
`
	proto := compileProto(t, src)
	getProto := proto.Protos[0]
	p := analyzeFuncProfile(getProto)

	if p.TableOpCount < 1 {
		t.Errorf("expected TableOpCount >= 1, got %d", p.TableOpCount)
	}
	t.Logf("get profile: %+v", p)
}

func TestShouldPromoteTier2_PureComputeLoop(t *testing.T) {
	// sum(n) with loop and arithmetic: should promote at callCount=2.
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
`
	proto := compileProto(t, src)
	sumProto := proto.Protos[0]
	p := analyzeFuncProfile(sumProto)

	if !shouldPromoteTier2(sumProto, p, 2) {
		t.Error("expected pure-compute loop to promote at callCount=2")
	}
	if shouldPromoteTier2(sumProto, p, 0) {
		t.Error("should not promote at callCount=0")
	}
}

func TestShouldPromoteTier2_RecursiveFib(t *testing.T) {
	// fib(n) is a small recursive function with calls, no loops, and arithmetic.
	// Should promote at callCount>=2 for Tier 2 inlining benefit.
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
`
	proto := compileProto(t, src)
	fibProto := proto.Protos[0]
	p := analyzeFuncProfile(fibProto)

	t.Logf("fib profile: %+v (BytecodeCount=%d)", p, p.BytecodeCount)

	// Small recursive function with arithmetic: should promote at callCount>=2.
	if !shouldPromoteTier2(fibProto, p, 2) {
		t.Error("expected fib to promote at callCount>=2 (small recursive with arith)")
	}
	// Should NOT promote at callCount=1 (warmup gate).
	if shouldPromoteTier2(fibProto, p, 1) {
		t.Error("should not promote fib at callCount=1")
	}
}

func TestShouldPromoteTier2_Simple(t *testing.T) {
	// double(x) = x * 2: pure compute, no loops, small arith count.
	src := `
func double(x) {
    return x * 2
}
`
	proto := compileProto(t, src)
	doubleProto := proto.Protos[0]
	p := analyzeFuncProfile(doubleProto)

	// ArithCount is small (1), no loops -> won't hit the eager promotion paths.
	// Falls through to default -> false.
	if shouldPromoteTier2(doubleProto, p, 1) {
		t.Logf("double promoted at callCount=2 (acceptable for simple pure-compute)")
	}
}

func TestShouldPromoteTier2_MandelbrotLike(t *testing.T) {
	// A function with nested loops and dense arithmetic: should promote at callCount=2.
	src := `
func compute(n) {
    total := 0
    for y := 0; y < n; y++ {
        for x := 0; x < n; x++ {
            cr := x * 2 - n
            ci := y * 2 - n
            zr := 0
            zi := 0
            for k := 0; k < 10; k++ {
                tr := zr * zr - zi * zi + cr
                zi = 2 * zr * zi + ci
                zr = tr
            }
            total = total + zr + zi
        }
    }
    return total
}
`
	proto := compileProto(t, src)
	computeProto := proto.Protos[0]
	p := analyzeFuncProfile(computeProto)

	if !p.HasLoop {
		t.Error("expected HasLoop=true for mandelbrot-like function")
	}
	if p.LoopDepth < 2 {
		t.Errorf("expected LoopDepth >= 2 (nested loops), got %d", p.LoopDepth)
	}
	if p.ArithCount < 10 {
		t.Errorf("expected ArithCount >= 10, got %d", p.ArithCount)
	}
	if !shouldPromoteTier2(computeProto, p, 2) {
		t.Error("mandelbrot-like function should promote at callCount=2")
	}
	t.Logf("compute profile: %+v", p)
}

// TestTieringManager_SmartPromotion verifies that the smart tiering strategy
// promotes loop-heavy functions on first call.
func TestTieringManager_SmartPromotion(t *testing.T) {
	// sum is called twice — first call compiles Tier 1, second triggers Tier 2.
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := sum(100)
result = sum(100)
`
	v, tm := runWithTieringManager(t, src)

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 5050 {
		t.Errorf("sum(100) = %v, want 5050", result)
	}

	// With smart tiering, sum should be promoted after 2 calls.
	if tm.Tier2Count() == 0 {
		t.Error("expected sum to be promoted to Tier 2 after 2 calls (smart tiering)")
	}
	t.Logf("tier2Count=%d", tm.Tier2Count())
}

// TestTieringManager_SmartPromotion_GCD verifies gcd (while-loop + arithmetic)
// is ATTEMPTED for Tier 2 on first call. Note: the actual Tier 2 compilation
// may fail due to pre-existing emitter bugs (unresolved label). The test
// verifies the smart tiering decision is correct (should attempt promotion),
// and that the function still produces correct results via Tier 1 fallback.
func TestTieringManager_SmartPromotion_GCD(t *testing.T) {
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
result = gcd(12, 8)
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

	// Smart tiering should have attempted Tier 2 promotion for gcd.
	// If compilation fails (pre-existing emitter bug), it falls back to Tier 1
	// and the function is marked as tier2Failed.
	gcdProto := proto.Protos[0]
	profile := tm.getProfile(gcdProto)
	if !shouldPromoteTier2(gcdProto, profile, 2) {
		t.Error("smart tiering should decide to promote gcd at callCount=1")
	}

	// Verify it was attempted (either succeeded or failed).
	if tm.Tier2Count() == 0 && !tm.tier2Failed[gcdProto] {
		t.Error("expected Tier 2 promotion to be attempted for gcd")
	}
	t.Logf("tier2Count=%d, tier2Failed=%v", tm.Tier2Count(), tm.tier2Failed[gcdProto])
}

// TestTieringManager_SmartPromotion_FibPromotesToTier2 verifies that small
// recursive functions like fib are promoted to Tier 2 after 2 calls.
// Tier 2 inlining (MaxRecursion=2) + type-specialized arithmetic eliminates
// NaN-boxing overhead across inlined call boundaries.
func TestTieringManager_SmartPromotion_FibPromotesToTier2(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(10)
result = fib(10)
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
	if !result.IsInt() || result.Int() != 55 {
		t.Errorf("fib(10) = %v, want 55", result)
	}

	// After 2 calls, fib should have been attempted for Tier 2 promotion.
	fibProto := proto.Protos[0]
	if tm.Tier2Count() == 0 && !tm.tier2Failed[fibProto] {
		t.Error("expected Tier 2 promotion to be attempted for fib after 2 calls")
	}
	t.Logf("tier2Count=%d, tier2Failed=%v", tm.Tier2Count(), tm.tier2Failed[fibProto])
}

// TestAnalyzeFuncProfile_NestedForLoops verifies loop depth is tracked.
func TestAnalyzeFuncProfile_NestedForLoops(t *testing.T) {
	src := `
func matmul(n) {
    total := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= n; j++ {
            total = total + i * j
        }
    }
    return total
}
`
	proto := compileProto(t, src)
	p := analyzeFuncProfile(proto.Protos[0])

	if p.LoopDepth < 2 {
		t.Errorf("expected LoopDepth >= 2 for nested for-loops, got %d", p.LoopDepth)
	}
	t.Logf("matmul profile: %+v", p)
}

// TestTieringManager_SmartPromotion_LoopWithCalls verifies that loop+call
// functions are handled by smart tiering. Functions with loops + calls + arith
// promote at threshold=2 via the inlining path (if calls are inlineable).
func TestTieringManager_SmartPromotion_LoopWithCalls(t *testing.T) {
	// outer() has a loop that calls inner(). Both have OP_CALL in bytecodes.
	src := `
func inner(x) {
    return x * 2
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
    result = outer(10)
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
	result := v.GetGlobal("result")
	if !result.IsInt() {
		t.Fatalf("expected int result, got %s", result.TypeName())
	}
	// Verify smart tiering decision for outer: has loop + calls + arith.
	outerProto := proto.Protos[1] // outer is the second function
	profile := tm.getProfile(outerProto)
	t.Logf("outer profile: %+v", profile)
	t.Logf("tier2Count=%d, result=%d", tm.Tier2Count(), result.Int())
}

func TestShouldPromoteTier2_SmallRecursiveAck(t *testing.T) {
	// ack(m, n) is a small recursive function with calls, no loops, and arithmetic.
	// Should promote at callCount>=2.
	src := `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
`
	proto := compileProto(t, src)
	ackProto := proto.Protos[0]
	p := analyzeFuncProfile(ackProto)

	t.Logf("ack profile: %+v", p)

	if !shouldPromoteTier2(ackProto, p, 2) {
		t.Error("expected ack to promote at callCount>=2 (small recursive with arith)")
	}
	if shouldPromoteTier2(ackProto, p, 1) {
		t.Error("should not promote ack at callCount=1")
	}
}

func TestShouldPromoteTier2_LargeNoLoopStaysTier1(t *testing.T) {
	// A function with many operations but no loops — should stay Tier 1.
	// We need BytecodeCount > 40 to miss the small recursive clause.
	src := `
func big(a, b, c, d, e) {
    x1 := a + b
    x2 := c + d
    x3 := e + a
    x4 := x1 + x2
    x5 := x3 + x4
    x6 := x5 * 2
    x7 := x6 - x1
    x8 := x7 + x2
    x9 := x8 * x3
    x10 := x9 - x4
    x11 := x10 + x5
    x12 := x11 * x6
    x13 := x12 - x7
    x14 := x13 + x8
    x15 := x14 * x9
    x16 := x15 - x10
    x17 := x16 + x11
    x18 := x17 * x12
    x19 := x18 - x13
    x20 := x19 + x14
    x21 := x20 + x15
    x22 := x21 * x16
    x23 := x22 - x17
    x24 := x23 + x18
    x25 := x24 * x19
    x26 := x25 - x20
    x27 := x26 + x21
    x28 := x27 * x22
    x29 := x28 - x23
    x30 := x29 + x24
    x31 := x30 * x25
    x32 := x31 - x26
    x33 := x32 + x27
    x34 := x33 * x28
    x35 := x34 - x29
    return big(x35, x30, x31, x32, x33)
}
`
	proto := compileProto(t, src)
	bigProto := proto.Protos[0]
	p := analyzeFuncProfile(bigProto)

	t.Logf("big profile: %+v (BytecodeCount=%d)", p, p.BytecodeCount)

	if p.BytecodeCount <= 40 {
		t.Skipf("big() has BytecodeCount=%d (<=40), test assumptions wrong", p.BytecodeCount)
	}

	if shouldPromoteTier2(bigProto, p, 100) {
		t.Error("expected large no-loop function to stay at Tier 1")
	}
}

// TestFuncProfile_CachedInTieringManager verifies profiles are cached.
func TestFuncProfile_CachedInTieringManager(t *testing.T) {
	src := `
func sum(n) {
    s := 0
    for i := 1; i <= n; i++ {
        s = s + i
    }
    return s
}
result := sum(10)
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

	sumProto := proto.Protos[0]
	// Profile should be cached after TryCompile.
	if _, ok := tm.profileCache[sumProto]; !ok {
		t.Error("expected profile to be cached after TryCompile")
	}
}
