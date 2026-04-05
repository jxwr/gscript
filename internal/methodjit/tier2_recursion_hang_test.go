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
