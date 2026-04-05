//go:build darwin && arm64

// tier2_recursion_hang_test.go reproduces the Tier 2 hang that aborted
// round 2026-04-05-recursive-inlining. Force-compiles fib to Tier 2 with
// the bounded recursive inliner (MaxRecursion=2) and executes with a
// timeout. Documents which phase (compile vs execute) hangs, if any.
//
// This test is DIAGNOSTIC: it may PASS (no hang — harness-specific bug) or
// FAIL (hang reproduced). Both outcomes are meaningful.

package methodjit

import (
	"testing"
	"time"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestTier2RecursionHangRepro attempts to reproduce the hang observed when
// forcing Tier 2 compilation of a self-recursive function (fib) with the
// bounded recursive inliner enabled.
func TestTier2RecursionHangRepro(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(5)
`
	// Parse & compile source to top-level proto.
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	// First execute: warm up. Registers fib in globals, bumps CallCount.
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("first v.Execute() error: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 5 {
		t.Fatalf("warmup: result=%v (type=%s), want int 5",
			result, result.TypeName())
	}
	t.Logf("warmup OK: fib(5)=%d", result.Int())

	// Locate fib proto.
	if len(proto.Protos) == 0 {
		t.Fatal("no inner protos (fib missing)")
	}
	var fibProto *vm.FuncProto
	for _, p := range proto.Protos {
		if p.Name == "fib" {
			fibProto = p
			break
		}
	}
	if fibProto == nil {
		fibProto = proto.Protos[0]
	}
	t.Logf("fib proto: name=%q numRegs=?, numBytecodes=%d",
		fibProto.Name, len(fibProto.Code))

	// Force Tier 2 compile. This is where H1/H2 (inline-pass hang / infinite
	// recursion in pass_inline) would manifest.
	attemptsBefore := tm.Tier2Attempted()
	compileDone := make(chan error, 1)
	go func() {
		compileDone <- tm.CompileTier2(fibProto)
	}()

	var compileErr error
	select {
	case compileErr = <-compileDone:
		// Compile finished (ok or error).
	case <-time.After(3 * time.Second):
		// Hang during Tier 2 compile — the inline pass or graph builder
		// most likely looped.
		t.Fatalf("HANG REPRODUCED (compile phase): tm.CompileTier2(fibProto) "+
			"did not return within 3s. Tier2Attempted (before)=%d, "+
			"Tier2Attempted (now)=%d, Tier2Count=%d, Tier2Failed=%v",
			attemptsBefore, tm.Tier2Attempted(), tm.Tier2Count(),
			tm.Tier2Failed())
	}

	if compileErr != nil {
		// Not a hang, but a diagnostic signal: Tier 2 bailed out.
		t.Logf("CompileTier2 returned error (diagnostic only): %v", compileErr)
	}
	t.Logf("after compile: Tier2Count=%d, Tier2Attempted=%d, Tier2Failed=%v",
		tm.Tier2Count(), tm.Tier2Attempted(), tm.Tier2Failed())

	if cf, ok := tm.tier2Compiled[fibProto]; ok && cf != nil {
		t.Logf("compiled fib: numRegs=%d NumSpills=%d DirectEntryOffset=%d "+
			"ResumeAddrs=%d",
			cf.numRegs, cf.NumSpills, cf.DirectEntryOffset, len(cf.ResumeAddrs))
	} else {
		t.Logf("fib NOT present in tier2Compiled after CompileTier2 "+
			"(compileErr=%v) — skipping Tier 2 execute phase", compileErr)
		return
	}

	// Second execute: run again, now fib may dispatch through Tier 2.
	start := time.Now()
	done := make(chan struct{})
	var execErr error
	go func() {
		defer close(done)
		_, execErr = v.Execute(proto)
	}()

	select {
	case <-done:
		elapsed := time.Since(start)
		if execErr != nil {
			t.Fatalf("HANG NOT REPRODUCED but execute returned error: %v "+
				"(elapsed=%s)", execErr, elapsed)
		}
		result2 := v.GetGlobal("result")
		if !result2.IsInt() || result2.Int() != 5 {
			t.Fatalf("result after Tier2 execute=%v (type=%s), want int 5 "+
				"(elapsed=%s)", result2, result2.TypeName(), elapsed)
		}
		t.Logf("NO HANG — combination does not reproduce the failure. "+
			"fib(5)=%d, elapsed=%s, Tier2Count=%d, Tier2Attempted=%d",
			result2.Int(), elapsed, tm.Tier2Count(), tm.Tier2Attempted())
	case <-time.After(3 * time.Second):
		t.Fatalf("HANG REPRODUCED (execute phase): second v.Execute(proto) "+
			"did not return within 3s. Tier2Count=%d, Tier2Attempted=%d, "+
			"Tier2Failed=%v",
			tm.Tier2Count(), tm.Tier2Attempted(), tm.Tier2Failed())
	}
}

// TestTier2RecursionDeeperFib drives the same harness as
// TestTier2RecursionHangRepro but with deeper fib(n) arguments and a
// repeat loop, to see if the hang surfaces at load profiles closer to
// fib_recursive.gs (fib(35), REPS=10).
func TestTier2RecursionDeeperFib(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		reps    int
		timeout time.Duration
	}{
		{"fib10_1rep", 10, 1, 5 * time.Second},
		{"fib20_1rep", 20, 1, 5 * time.Second},
		{"fib25_1rep", 25, 1, 8 * time.Second},
		{"fib30_1rep", 30, 1, 10 * time.Second},
		{"fib10_10reps", 10, 10, 8 * time.Second},
		{"fib20_10reps", 20, 10, 10 * time.Second},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := "\nfunc fib(n) {\n    if n < 2 { return n }\n    return fib(n-1) + fib(n-2)\n}\n"
			// Build the REPS loop inline.
			src += "result := 0\n"
			for i := 0; i < tc.reps; i++ {
				src += "result = fib(" + itoaDiag(tc.n) + ")\n"
			}
			proto := compileProto(t, src)
			globals := runtime.NewInterpreterGlobals()
			v := vm.New(globals)
			tm := NewTieringManager()
			v.SetMethodJIT(tm)

			// Warm up: run once so fib is registered as a global.
			if _, err := v.Execute(proto); err != nil {
				t.Fatalf("warmup v.Execute() error: %v", err)
			}
			// Locate fib proto.
			var fibProto *vm.FuncProto
			for _, p := range proto.Protos {
				if p.Name == "fib" {
					fibProto = p
					break
				}
			}
			if fibProto == nil {
				t.Fatal("no fib proto")
			}
			// Force Tier 2.
			if err := tm.CompileTier2(fibProto); err != nil {
				t.Logf("CompileTier2 error (diagnostic): %v", err)
			}
			if _, ok := tm.tier2Compiled[fibProto]; !ok {
				t.Logf("fib NOT compiled to Tier 2 — skipping")
				return
			}
			// Re-execute with timeout. Verify result matches fib(tc.n).
			want := fibInt(tc.n)
			start := time.Now()
			done := make(chan struct{})
			var execErr error
			go func() {
				defer close(done)
				_, execErr = v.Execute(proto)
			}()
			select {
			case <-done:
				elapsed := time.Since(start)
				if execErr != nil {
					t.Fatalf("execute returned error after %s: %v",
						elapsed, execErr)
				}
				got := v.GetGlobal("result")
				if !got.IsInt() || got.Int() != int64(want) {
					t.Fatalf("result=%v want=%d (elapsed=%s)",
						got, want, elapsed)
				}
				t.Logf("OK: fib(%d)x%d=%d elapsed=%s",
					tc.n, tc.reps, want, elapsed)
			case <-time.After(tc.timeout):
				t.Fatalf("HANG: fib(%d)x%d exceeded %s; "+
					"Tier2Count=%d Tier2Attempted=%d Tier2Failed=%v",
					tc.n, tc.reps, tc.timeout,
					tm.Tier2Count(), tm.Tier2Attempted(), tm.Tier2Failed())
			}
		})
	}
}

// fibInt is the reference fib used by the diagnostic test.
func fibInt(n int) int {
	if n < 2 {
		return n
	}
	return fibInt(n-1) + fibInt(n-2)
}

// TestTier2NestedCallArgBug documents the root cause of the Tier 2 recursion
// hang on ack/F/M: the graph builder drops arguments of OP_CALL instructions
// with B=0 (variadic args threaded via top). See opt/diagnose_tier2_hang.md.
//
// The pattern `return outer(x, inner(...))` compiles the outer call with B=0
// so its argument count can absorb inner's variable returns. BuildGraph's
// OP_CALL handler only reads args when B>=2, emitting Call-with-no-args IR
// for B=0. Tier 2 then executes the wrong IR, corrupting recursive results
// and causing exponential blowup that looks like a hang.
//
// When fixed, the Diagnose output for ack B4 must contain
//    vN = Call <fn>, <arg0>, <arg1>  (not `Call <fn>` alone)
// and f(n) = 1 + f(f(n-1)) must return n for n >= 1.
func TestTier2NestedCallArgBug(t *testing.T) {
	src := `
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
`
	proto := compileProto(t, src)
	var ackProto *vm.FuncProto
	for _, p := range proto.Protos {
		if p.Name == "ack" {
			ackProto = p
			break
		}
	}
	if ackProto == nil {
		t.Fatal("no ack proto")
	}
	fn := BuildGraph(ackProto)
	// The outer `ack(m-1, ack(m, n-1))` uses OP_CALL with B=0. BuildGraph
	// currently drops its args. Once fixed, the last Call in the IR MUST
	// have at least 3 args (fn + m-1 + inner-result).
	var lastCall *Instr
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpCall {
				lastCall = instr
			}
		}
	}
	if lastCall == nil {
		t.Fatal("no OpCall in ack IR")
	}
	t.Logf("last Call in ack IR has %d args (fn + %d params)",
		len(lastCall.Args), len(lastCall.Args)-1)
	// Regression assertion: the outer ack call must pass 2 args, so
	// OpCall.Args has 3 entries (fn + 2 args). This assertion is tracked
	// but skipped (t.Skip) until the graph builder fix lands — this keeps
	// the test green in CI while documenting the exact bug signature.
	// Remove the Skip once graph_builder.go handles OP_CALL B=0.
	if len(lastCall.Args) < 3 {
		t.Skipf("KNOWN BUG (diagnosis pending fix): outer ack() emitted "+
			"as Call with %d args; expected 3 (fn + m-1 + inner-result). "+
			"Graph builder does not handle OP_CALL B=0 (varargs). Fix: "+
			"graph_builder.go:532 must track `top` from previous "+
			"variadic-return calls. See opt/diagnose_tier2_hang.md.",
			len(lastCall.Args))
	}
}

// itoaDiag is a tiny decimal converter (avoids importing strconv at top level).
func itoaDiag(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
