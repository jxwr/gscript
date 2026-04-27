//go:build darwin && arm64

// emit_int_overflow_correctness_test.go — R77 failing tests for the
// tier2-intbinmod-correctness bug (R15 forward class).
//
// JIT and VM diverge when integer arithmetic overflows the int48
// NaN-box range. Expected behavior: both paths promote to float
// (or both wrap consistently). Observed: JIT produces values
// inconsistent with VM.
//
// Minimal repro discovered via R77 parallel bisect agent:
//   x := 42; for i in 1..15 { x = x*13 + 1 }; print x
//   VM:  2154072997676319744 (~2e18, promoted to float-backed int)
//   JIT: -54999090331011     (~-5.5e13, int48-wrapped)
//
// The exact value that JIT produces isn't the issue — the divergence
// from VM is. Whatever semantics we pick, both paths must agree.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestTier2_IntOverflow_Loop_Matches_VM is the canonical R77 failing
// test. Under current gscript, this test FAILS:
//
//	JIT overflow arithmetic in a main-loop diverges from VM.
//
// The trailing `_ = string.format(...)` call ensures profile.CallCount > 0
// which triggers R72's main-promote clause in shouldPromoteTier2 → main
// reaches Tier 2 → the bug surfaces. Without the call, main stays at
// Tier 1 (which correctly promotes int→float on overflow).
func TestTier2_IntOverflow_Loop_Matches_VM(t *testing.T) {
	src := `
x := 42
for i := 1; i <= 15; i++ {
    x = x * 13 + 1
}
result := x
_ = string.format("trigger t2: %d", x)
`
	compareTier2Result(t, src, "result")
}

// TestTier2_IntOverflow_JITProbe — log exact JIT result + tiering stats.
// CLI `gscript ov_min.gs` prints -54999090331011 (int). If this test
// shows float, the harness path diverges from CLI in terms of tiering.
func TestTier2_IntOverflow_JITProbe(t *testing.T) {
	src := `
x := 42
for i := 1; i <= 15; i++ {
    x = x * 13 + 1
}
result := x
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT execute: %v", err)
	}
	result := v.GetGlobal("result")
	t.Logf("JIT result: IsInt=%v IsFloat=%v", result.IsInt(), result.IsFloat())
	if result.IsFloat() {
		t.Logf("JIT float value: %f", result.Float())
	}
	if result.IsInt() {
		t.Logf("JIT int value: %d", result.Int())
	}
	t.Logf("Tier2 attempted=%d compiled=%d", tm.tier2Attempts, len(tm.tier2Compiled))
	for p := range tm.tier2Compiled {
		t.Logf("  T2: %s", p.Name)
	}
}

// TestTier2_IntOverflow_VMProbe — verify VM's own output for this loop.
// This is a sanity check: CLI `gscript -vm ov_min.gs` prints
// 2154072997676319744. The harness should also see this via VM path.
func TestTier2_IntOverflow_VMProbe(t *testing.T) {
	src := `
x := 42
for i := 1; i <= 15; i++ {
    x = x * 13 + 1
}
result := x
`
	protoVM := compileProto(t, src)
	globalsVM := runtime.NewInterpreterGlobals()
	vVM := vm.New(globalsVM)
	defer vVM.Close()
	if _, err := vVM.Execute(protoVM); err != nil {
		t.Fatalf("VM execute: %v", err)
	}
	result := vVM.GetGlobal("result")
	t.Logf("VM result: IsInt=%v IsFloat=%v raw=%v", result.IsInt(), result.IsFloat(), result)
	if result.IsFloat() {
		t.Logf("VM float value: %f", result.Float())
	}
	if result.IsInt() {
		t.Logf("VM int value: %d", result.Int())
	}
}

// TestTier2_IntOverflow_ForceTier2_Viable — same script but wraps the
// arithmetic in a function called twice so runtimeCallCount >= 2
// triggers the standard shouldPromoteTier2 path. This confirms whether
// the T2 compile of the arithmetic itself (not _main_) miscomputes.
func TestTier2_IntOverflow_ForceTier2_Viable(t *testing.T) {
	src := `
func overflow_loop() {
    x := 42
    for i := 1; i <= 15; i++ {
        x = x * 13 + 1
    }
    return x
}
overflow_loop()          // first call — warms profile
result := overflow_loop() // second call — triggers T2
`
	compareTier2Result(t, src, "result")
}

// TestTier2_IntOverflow_Shorter_Loop — same pattern but fewer iters.
// If this passes but the 15-iter one fails, the bug triggers at a
// specific overflow threshold.
func TestTier2_IntOverflow_Shorter_Loop(t *testing.T) {
	src := `
x := 42
for i := 1; i <= 10; i++ {
    x = x * 13 + 1
}
result := x
`
	compareTier2Result(t, src, "result")
}

// TestTier2_IntOverflow_MulOnly — isolates mul overflow (no add).
func TestTier2_IntOverflow_MulOnly(t *testing.T) {
	src := `
x := 42
for i := 1; i <= 15; i++ {
    x = x * 13
}
result := x
`
	compareTier2Result(t, src, "result")
}

// TestTier2_IntOverflow_AddOnly — isolates add overflow. At large
// values, repeated addition can still overflow.
func TestTier2_IntOverflow_AddOnly(t *testing.T) {
	src := `
x := 100000000000   // 10^11, within int48
for i := 1; i <= 2000; i++ {
    x = x + 100000000000
}
result := x
`
	compareTier2Result(t, src, "result")
}

func TestTier2_FibOverflow_FinalBOverflowKeepsReturnType(t *testing.T) {
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
for warm := 1; warm <= 15; warm++ {
    fib_iter(10)
}
result := fib_iter(69)
`
	compareTier2Result(t, src, "result")
}

func TestTier2_FibOverflow_SmallTripCounts(t *testing.T) {
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
for warm := 1; warm <= 15; warm++ {
    fib_iter(10)
}
result := fib_iter(0) + fib_iter(1) + fib_iter(2) + fib_iter(3) + fib_iter(4) + fib_iter(5) + fib_iter(6) + fib_iter(7) + fib_iter(8) + fib_iter(9) + fib_iter(10)
`
	compareTier2Result(t, src, "result")
}

func TestTier2_FibOverflow_ReturnsOverflowedValue(t *testing.T) {
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
for warm := 1; warm <= 15; warm++ {
    fib_iter(10)
}
result := fib_iter(70)
`
	compareTier2Result(t, src, "result")
}
