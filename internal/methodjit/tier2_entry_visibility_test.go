//go:build darwin && arm64

// tier2_entry_visibility_test.go exercises the R146 Tier 2 entry flag.
// When a Tier-2-compiled function's native prologue runs, it must set
// proto.EnteredTier2 = 1 so bench harnesses and -jit-stats can report,
// in one glance, whether the hot function actually ran through Tier 2
// native code (as opposed to being compiled but never entered, or
// falling back to Tier 1 / VM).
//
// This test is the red→green driver for rule 26 of CLAUDE.md on a
// diagnostic (but emit-touching) round.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestTier2EntryVisibility_Fib compiles fib at Tier 2 and asserts the
// EnteredTier2 flag transitions 0 → 1 on first native execution.
//
// Red state (R146 pre-flight): EnteredTier2 is 0 after v.Execute because
// no emit site writes to it yet.
// Green state (R146 Step 5b): prologue STRB sets it to 1.
func TestTier2EntryVisibility_Fib(t *testing.T) {
	src := `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(5)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	// Warm-up run registers fib in globals.
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("warmup Execute: %v", err)
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
		t.Fatal("fib proto not found among top-level protos")
	}

	// Force Tier 2 compile (idempotent if smart tiering already promoted
	// during warmup — fib(5)'s 15 recursive calls may clear the
	// BaselineCompileThreshold + Tier2 threshold on their own).
	if err := tm.CompileTier2(fibProto); err != nil {
		t.Fatalf("CompileTier2(fib) failed: %v", err)
	}
	if _, ok := tm.tier2Compiled[fibProto]; !ok {
		t.Fatal("fib did not land in tier2Compiled after CompileTier2")
	}

	// Execute again to guarantee at least one post-promotion invocation
	// dispatches through Tier 2 native code.
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if fibProto.EnteredTier2 != 1 {
		t.Fatalf("post-execute: fibProto.EnteredTier2 = %d, want 1 "+
			"(Tier 2 prologue did not set the entry flag)",
			fibProto.EnteredTier2)
	}
}

// TestTier2EntryVisibility_NotCompiled documents the negative case: a
// proto that never reaches Tier 2 compilation keeps EnteredTier2 = 0.
// This is the signal that drives the bench-harness ✗ column.
func TestTier2EntryVisibility_NotCompiled(t *testing.T) {
	src := `
func only_interpreted(x) {
    return x + 1
}
result := only_interpreted(7)
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var innerProto *vm.FuncProto
	for _, p := range proto.Protos {
		if p.Name == "only_interpreted" {
			innerProto = p
			break
		}
	}
	if innerProto == nil {
		t.Fatal("only_interpreted proto not found")
	}

	if innerProto.EnteredTier2 != 0 {
		t.Fatalf("never-compiled proto has EnteredTier2 = %d, want 0 "+
			"(flag is being set spuriously)", innerProto.EnteredTier2)
	}
}
