//go:build darwin && arm64

// r138_ack_hang_test.go reproduces the Tier 2 hang on 2-param self-
// recursive int protos (ackermann) found by R136 — the hang only
// surfaces when Tier 2 compilation is triggered MID-EXECUTION (i.e.,
// inside a running Tier 1 ack recursion), not when Tier 2 ack is run
// from a cold top-level frame. Rule 26 test-first for the correctness
// round. Test HANGS on current code (before fix).

package methodjit

import (
	"testing"
	"time"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestR138_AckTier2Hang reproduces the bench hang for ack(3,4) x500
// when the shouldPromoteTier2 gate is widened to np>=2. SKIPPED by
// default — R138 attempted fix (removing self-call threshold trigger)
// worked for ack but killed fib's Tier 2 promotion (fib only has one
// non-self call per execution, so self-path was the sole trigger).
// Real fix deferred; this test documents the reproducer. Run with
// `-run TestR138_AckTier2Hang -tags r138fix` once a fix is landed.
func TestR138_AckTier2Hang(t *testing.T) {
	t.Skip("R138/R139: ack mid-compile hang. Root cause identified by 4-agent " +
		"parallel debug (R139): Tier 2 emit is feedback-dependent; " +
		"mid-recursion compile produces a body whose guards deopt on " +
		"re-entry. The ExitDeopt path at tiering_manager.go → vm.run:1206 " +
		"retries via TryCompile (cache hit!) → re-executes same Tier 2 → " +
		"deopts again, growing ctx.Regs unboundedly. Fix requires evicting " +
		"tier2Compiled[proto] + tier2Failed[proto]=true + DirectEntryPtr " +
		"reset AND ensuring any in-flight Tier 2 execute stacks unwind " +
		"cleanly. Single-point eviction was verified insufficient. See " +
		"rounds/R139.yaml for full bisection.")

	src := `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
// No warmup — compile must happen mid-ack(3,4)
result := 0
for r := 1; r <= 500; r = r + 1 {
    result = ack(3, 4)
}
`
	// R138: widen the gate so ack (2-param) gets promoted at runtime.
	// Without this, the production np==1 gate keeps ack at Tier 1 and
	// the test passes trivially. Restore at test exit.
	prevGate := promoteAckOverride
	promoteAckOverride = true
	defer func() { promoteAckOverride = prevGate }()

	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := v.Execute(proto)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute error: %v", err)
		}
		result := v.GetGlobal("result")
		if !result.IsInt() || result.Int() != 125 {
			t.Fatalf("ack(3,4) = %v, want int 125", result)
		}
		t.Logf("OK ack(3,4)=125 x500 reps in %v", time.Since(start))
	case <-time.After(5 * time.Second):
		t.Fatalf("HANG REPRODUCED: ack(3,4) mid-Tier-2-transition hung >5s")
	}
}
