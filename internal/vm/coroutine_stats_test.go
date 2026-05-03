package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

func compileAndRunWithCoroutineStats(t *testing.T, src string) CoroutineStatsSnapshot {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	bvm := New(runtime.NewInterpreterGlobals())
	bvm.EnableCoroutineStats()
	if _, err := bvm.Execute(proto); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return bvm.CoroutineStats()
}

func TestVMCoroutineStatsSeparateLeafAndGoroutinePaths(t *testing.T) {
	stats := compileAndRunWithCoroutineStats(t, `
leaf := coroutine.create(func(x) {
	return x * 2
})
ok1, v1 := coroutine.resume(leaf, 21)

co := coroutine.create(func() {
	coroutine.yield(1)
	return 2
})
ok2, v2 := coroutine.resume(co)
ok3, v3 := coroutine.resume(co)
result := v1 + v2 + v3
`)

	if stats.Created != 2 {
		t.Fatalf("Created = %d, want 2", stats.Created)
	}
	if stats.ResumeCalls != 3 {
		t.Fatalf("ResumeCalls = %d, want 3", stats.ResumeCalls)
	}
	if stats.YieldCalls != 1 {
		t.Fatalf("YieldCalls = %d, want 1", stats.YieldCalls)
	}
	if stats.LeafFastPath != 1 {
		t.Fatalf("LeafFastPath = %d, want 1", stats.LeafFastPath)
	}
	if stats.GoroutineStarts != 1 {
		t.Fatalf("GoroutineStarts = %d, want 1", stats.GoroutineStarts)
	}
	if stats.Completed != 2 {
		t.Fatalf("Completed = %d, want 2", stats.Completed)
	}
	if stats.ResumeErrors != 0 {
		t.Fatalf("ResumeErrors = %d, want 0", stats.ResumeErrors)
	}
}

func TestVMRuntimePathStatsCoroutineCounters(t *testing.T) {
	pathStats := runtime.EnableRuntimePathStats()
	defer runtime.DisableRuntimePathStats()

	_ = compileAndRunWithCoroutineStats(t, `
leaf := coroutine.create(func(x) {
	return x + 1
})
ok1, v1 := coroutine.resume(leaf, 41)

co := coroutine.create(func() {
	coroutine.yield(1)
	return 2
})
ok2, v2 := coroutine.resume(co)
ok3, v3 := coroutine.resume(co)
`)

	snap := pathStats.Snapshot()
	if snap.Coroutine.Resume != 3 {
		t.Fatalf("Coroutine.Resume = %d, want 3", snap.Coroutine.Resume)
	}
	if snap.Coroutine.Yield != 1 {
		t.Fatalf("Coroutine.Yield = %d, want 1", snap.Coroutine.Yield)
	}
	if snap.Coroutine.Fast != 1 {
		t.Fatalf("Coroutine.Fast = %d, want 1", snap.Coroutine.Fast)
	}
	if snap.Coroutine.Fallback != 1 {
		t.Fatalf("Coroutine.Fallback = %d, want 1", snap.Coroutine.Fallback)
	}
}
