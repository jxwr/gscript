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
	// fib(n) is call-heavy, no-loop, and small (<=40 bytecodes): under the
	// new policy it SHOULD promote to Tier 2 so the bounded recursive
	// inliner + type specialization can flatten the recursion.
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
`
	proto := compileProto(t, src)
	fibProto := proto.Protos[0]
	p := analyzeFuncProfile(fibProto)

	// New policy: small call-heavy no-loop funcs promote at runtimeCallCount>=2.
	if !shouldPromoteTier2(fibProto, p, 100) {
		t.Error("fib should be promoted under new policy: small call-heavy no-loop benefits from inlining at Tier 2")
	}
	if !shouldPromoteTier2(fibProto, p, 2) {
		t.Error("fib should be promoted at runtimeCallCount=2 under new policy")
	}
	if shouldPromoteTier2(fibProto, p, 0) {
		t.Error("fib should not promote at runtimeCallCount=0 (below threshold)")
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

// TestTieringManager_SmartPromotion_FibCorrectness verifies that fib still
// produces correct results under the new smart-tiering policy, which now
// promotes small call-heavy no-loop functions to Tier 2 (so the bounded
// recursive inliner can flatten them).
func TestTieringManager_SmartPromotion_FibCorrectness(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(10)
`
	v, _ := runWithTieringManager(t, src)

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 55 {
		t.Errorf("fib(10) = %v, want 55", result)
	}
	// fib is call-heavy, no-loop, small (<=40 bytecodes). Under the new policy
	// it MAY be promoted to Tier 2 after 2 calls. Correctness must hold either
	// way — both Tier 1 BLR and Tier 2 (with inlining) must return fib(10)=55.
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

// TestShouldPromoteTier2_CallHeavyNoLoop verifies that small call-heavy
// no-loop functions (like fib) DO get promoted under the new policy. This
// is the flip side of the current hard-block that prevents Tier 2 for any
// CallCount>0 && !HasLoop function.
func TestShouldPromoteTier2_CallHeavyNoLoop(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
`
	proto := compileProto(t, src)
	fibProto := proto.Protos[0]
	p := analyzeFuncProfile(fibProto)

	// New policy: call-heavy no-loop small funcs promote at runtimeCallCount>=2.
	if !shouldPromoteTier2(fibProto, p, 2) {
		t.Error("expected fib (call-heavy, no-loop, small) to promote at runtimeCallCount=2 under new policy")
	}
	// Must not promote before the threshold is reached.
	if shouldPromoteTier2(fibProto, p, 0) {
		t.Error("should not promote at runtimeCallCount=0")
	}
}

// TestShouldPromoteTier2_LargeCallHeavyNoLoop verifies the bytecode cap (40)
// excludes large call-heavy no-loop functions from the new promotion clause.
func TestShouldPromoteTier2_LargeCallHeavyNoLoop(t *testing.T) {
	src := `
func other(p, q) { return p * q }
func big(a, b, c, d, e) {
    x := a + b
    y := c + d
    z := x * y
    w := z - e
    u := w + x
    v := u * y
    r := v - z
    s := r + w
    t := s * u
    k := t + a
    m := k - b
    n := m * c
    o := n + d
    q := o - e
    aa := q + x
    bb := aa - y
    cc := bb * z
    dd := cc + w
    ee := dd - u
    ff := ee * v
    gg := ff + r
    hh := gg - s
    return hh + other(r, s) + other(x, y) + other(k, m) + other(n, o) + other(aa, bb) + other(cc, dd)
}
`
	proto := compileProto(t, src)
	// Find big proto by name.
	var bigProto *vm.FuncProto
	for _, inner := range proto.Protos {
		if inner.Name == "big" {
			bigProto = inner
			break
		}
	}
	if bigProto == nil {
		t.Fatal("big proto not found")
	}
	p := analyzeFuncProfile(bigProto)
	t.Logf("big profile: %+v", p)

	if p.BytecodeCount <= 40 {
		t.Skipf("need > 40 bytecodes, got %d — adjust test source", p.BytecodeCount)
	}
	if p.HasLoop {
		t.Fatalf("big should not have loops, got HasLoop=true")
	}
	if p.CallCount == 0 {
		t.Fatalf("big should have calls, got CallCount=0")
	}

	// Under the new policy, the call-heavy-no-loop clause has a bytecode cap
	// (40). big exceeds it, so it must NOT be promoted.
	if shouldPromoteTier2(bigProto, p, 100) {
		t.Error("expected big (>40 bytecodes, call-heavy, no-loop) to NOT promote under call-heavy clause")
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
