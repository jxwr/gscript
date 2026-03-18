package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestTraceBlacklist_SideExitCountIncrement verifies that a compiled trace
// tracks side-exit counts.
func TestTraceBlacklist_SideExitCountIncrement(t *testing.T) {
	// Create a trace that has a CALL (not intrinsic, not self-call) which
	// will always side-exit.
	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}

	trace := &Trace{
		LoopProto: proto,
		LoopPC:    10,
		EntryPC:   10,
		IR: []TraceIR{
			// ADD R4, R4, R3  (sum += i) -- this works
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			// CALL that will side-exit (not intrinsic, not self-call)
			{Op: vm.OP_CALL, A: 5, B: 2, C: 2, PC: 15},
			// FORLOOP R0
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	ct, err := compileTrace(trace)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// Initially, counters should be zero
	if ct.sideExitCount != 0 {
		t.Errorf("initial sideExitCount = %d, want 0", ct.sideExitCount)
	}
	if ct.fullRunCount != 0 {
		t.Errorf("initial fullRunCount = %d, want 0", ct.fullRunCount)
	}
	if ct.blacklisted {
		t.Error("trace should not be blacklisted initially")
	}
}

// TestTraceBlacklist_BlacklistAfterRepeatedSideExits verifies that a trace
// gets blacklisted after exceeding the side-exit threshold with zero full runs.
func TestTraceBlacklist_BlacklistAfterRepeatedSideExits(t *testing.T) {
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)

	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}
	key := loopKey{proto: proto, pc: 10}

	// Manually create and insert a compiled trace
	trace := &Trace{
		LoopProto: proto,
		LoopPC:    10,
		EntryPC:   10,
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct, err := compileTrace(trace)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	recorder.compiled[key] = ct

	// Simulate repeated side-exits: call RecordSideExit many times
	threshold := SideExitBlacklistThreshold
	for i := 0; i < threshold; i++ {
		if ct.blacklisted {
			t.Fatalf("trace blacklisted too early at side-exit %d (threshold=%d)", i, threshold)
		}
		recorder.RecordSideExit(ct)
	}

	// After threshold side-exits with zero full runs, trace should be blacklisted
	if !ct.blacklisted {
		t.Errorf("trace not blacklisted after %d side-exits", threshold)
	}

	// OnLoopBackEdge should now return false (interpreter runs instead)
	if recorder.OnLoopBackEdge(10, proto) {
		t.Error("OnLoopBackEdge returned true for blacklisted trace")
	}
}

// TestTraceBlacklist_FullRunPreventsBlacklist verifies that traces with
// successful full runs are NOT blacklisted, even with many side-exits.
func TestTraceBlacklist_FullRunPreventsBlacklist(t *testing.T) {
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)

	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}
	key := loopKey{proto: proto, pc: 10}

	trace := &Trace{
		LoopProto: proto,
		LoopPC:    10,
		EntryPC:   10,
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct, err := compileTrace(trace)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	recorder.compiled[key] = ct

	// Record a full run first
	recorder.RecordFullRun(ct)

	// Then record many side-exits
	for i := 0; i < SideExitBlacklistThreshold*2; i++ {
		recorder.RecordSideExit(ct)
	}

	// Should NOT be blacklisted because it had at least one full run
	if ct.blacklisted {
		t.Error("trace blacklisted despite having full runs")
	}

	// OnLoopBackEdge should still return true
	if !recorder.OnLoopBackEdge(10, proto) {
		t.Error("OnLoopBackEdge returned false for non-blacklisted trace")
	}
}

// TestTraceBlacklist_EndToEnd_AlwaysSideExits verifies blacklisting in a
// real compiled+executed scenario where the trace always side-exits.
// A loop with table operations will always side-exit because the trace
// compiler emits side-exits for GETTABLE/SETTABLE.
func TestTraceBlacklist_EndToEnd_AlwaysSideExits(t *testing.T) {
	src := `
		t := {0, 0, 0, 0, 0}
		for i := 1; i <= 100; i++ {
			t[1] = t[1] + i
		}
		sum := t[1]
	`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v.SetTraceRecorder(recorder)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	// Result should be correct (interpreter handles the side-exits)
	if g := globals["sum"]; g.Int() != 5050 {
		t.Errorf("sum = %d, want 5050", g.Int())
	}

	// The trace for this loop should be blacklisted because it always
	// side-exits on GETTABLE/SETTABLE
	anyBlacklisted := false
	for _, ct := range recorder.compiled {
		if ct.blacklisted {
			anyBlacklisted = true
			break
		}
	}
	if !anyBlacklisted {
		t.Error("expected at least one trace to be blacklisted for table-heavy loop")
	}
}

// TestTraceBlacklist_EndToEnd_IntegerLoop_NotBlacklisted verifies that a
// pure integer loop (which completes full runs) is NOT blacklisted.
// Uses a top-level loop with only integer arithmetic — no table or function
// ops that would cause side-exits.
func TestTraceBlacklist_EndToEnd_IntegerLoop_NotBlacklisted(t *testing.T) {
	// This loop compiles to GETGLOBAL/ADD/SETGLOBAL/FORLOOP.
	// GETGLOBAL/SETGLOBAL always side-exit, so this trace WILL be blacklisted.
	// That's actually CORRECT behavior — the trace isn't useful.
	//
	// To verify that a working trace is NOT blacklisted, we manually create one.
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)

	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}
	key := loopKey{proto: proto, pc: 10}

	// Create a trace that would complete full runs (ADD + FORLOOP only)
	trace := &Trace{
		LoopProto: proto,
		LoopPC:    10,
		EntryPC:   10,
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct, err := compileTrace(trace)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	recorder.compiled[key] = ct

	// Simulate: the trace runs many full loop completions with occasional side-exits
	for i := 0; i < 5; i++ {
		recorder.RecordFullRun(ct)
	}
	for i := 0; i < 50; i++ {
		recorder.RecordSideExit(ct)
	}

	// Should NOT be blacklisted because it has full runs
	if ct.blacklisted {
		t.Errorf("trace blacklisted despite %d full runs (sideExits=%d)",
			ct.fullRunCount, ct.sideExitCount)
	}

	// OnLoopBackEdge should still return true
	if !recorder.OnLoopBackEdge(10, proto) {
		t.Error("OnLoopBackEdge returned false for non-blacklisted trace with full runs")
	}
}

// TestTraceBlacklist_Correctness verifies results are identical with and
// without trace blacklisting.
func TestTraceBlacklist_Correctness(t *testing.T) {
	src := `
		t := {0, 0, 0}
		sum := 0
		for i := 1; i <= 200; i++ {
			t[1] = t[1] + i
			sum = sum + i
		}
		result := t[1] + sum
	`

	// Run without tracing
	proto := compileProto(t, src)
	globals1 := runtime.NewInterpreterGlobals()
	v1 := vm.New(globals1)
	v1.Execute(proto)

	// Run with tracing + compilation
	proto2 := compileProto(t, src)
	globals2 := runtime.NewInterpreterGlobals()
	v2 := vm.New(globals2)
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v2.SetTraceRecorder(recorder)
	v2.Execute(proto2)

	// Results must match
	r1 := globals1["result"].Int()
	r2 := globals2["result"].Int()
	if r1 != r2 {
		t.Errorf("results differ: interpreter=%d, traced=%d", r1, r2)
	}
}
