//go:build darwin && arm64

// r138_ack_hang_test.go reproduces the Tier 2 hang on 2-param self-
// recursive int protos (ackermann) found by R136 — the hang only
// surfaces when Tier 2 compilation is triggered MID-EXECUTION (i.e.,
// inside a running Tier 1 ack recursion), not when Tier 2 ack is run
// from a cold top-level frame. This remains a regression canary for the
// raw-int self ABI exit-resume protocol.

package methodjit

import (
	"testing"
	"time"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestR138_AckTier2Hang reproduces the bench hang for ack(3,4) x500
// when Ack is promoted to Tier 2 mid-execution.
func TestR138_AckTier2Hang(t *testing.T) {
	// The raw self-call path must not leak post-call rawIntRegs state into
	// fallback materialization or exit-resume.
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
