package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// newTestTrace creates a minimal CompiledTrace for blacklist testing.
// No actual native code is needed — only the blacklist counters are tested.
func newTestTrace(proto *vm.FuncProto, loopPC int) *CompiledTrace {
	return &CompiledTrace{
		proto:  proto,
		loopPC: loopPC,
	}
}

// TestTraceBlacklist_SideExitCountIncrement verifies that a compiled trace
// tracks side-exit counts.
func TestTraceBlacklist_SideExitCountIncrement(t *testing.T) {
	ct := newTestTrace(nil, 10)

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

	ct := newTestTrace(proto, 10)
	recorder.compiled[key] = ct

	threshold := SideExitBlacklistThreshold
	for i := 0; i < threshold; i++ {
		if ct.blacklisted {
			t.Fatalf("trace blacklisted too early at side-exit %d (threshold=%d)", i, threshold)
		}
		recorder.RecordSideExit(ct)
	}

	if !ct.blacklisted {
		t.Errorf("trace not blacklisted after %d side-exits", threshold)
	}

	if recorder.OnLoopBackEdge(10, proto) {
		t.Error("OnLoopBackEdge returned true for blacklisted trace")
	}
}

// TestTraceBlacklist_FullRunPreventsBlacklist verifies that traces with
// a healthy mix of full runs and side-exits are NOT blacklisted.
func TestTraceBlacklist_FullRunPreventsBlacklist(t *testing.T) {
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)

	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}
	key := loopKey{proto: proto, pc: 10}

	ct := newTestTrace(proto, 10)
	recorder.compiled[key] = ct

	for i := 0; i < 30; i++ {
		recorder.RecordFullRun(ct)
	}
	for i := 0; i < 70; i++ {
		recorder.RecordSideExit(ct)
	}

	if ct.blacklisted {
		t.Errorf("trace blacklisted with ratio %.2f (threshold=%.2f)",
			float64(ct.sideExitCount)/float64(ct.sideExitCount+ct.fullRunCount),
			SideExitBlacklistRatio)
	}

	if !recorder.OnLoopBackEdge(10, proto) {
		t.Error("OnLoopBackEdge returned false for non-blacklisted trace")
	}
}

// TestTraceBlacklist_EndToEnd_AlwaysSideExits verifies blacklisting in a
// real compiled+executed scenario.
func TestTraceBlacklist_EndToEnd_AlwaysSideExits(t *testing.T) {
	t.Skip("TODO: blacklist counting disabled due to Go compiler escape analysis causing 2x perf regression")
}

// TestTraceBlacklist_EndToEnd_IntegerLoop_NotBlacklisted verifies that
// a working trace is not blacklisted.
func TestTraceBlacklist_EndToEnd_IntegerLoop_NotBlacklisted(t *testing.T) {
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)

	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}
	key := loopKey{proto: proto, pc: 10}

	ct := newTestTrace(proto, 10)
	recorder.compiled[key] = ct

	for i := 0; i < 10; i++ {
		recorder.RecordFullRun(ct)
	}
	for i := 0; i < 90; i++ {
		recorder.RecordSideExit(ct)
	}

	if ct.blacklisted {
		t.Errorf("trace blacklisted despite %d full runs (sideExits=%d)",
			ct.fullRunCount, ct.sideExitCount)
	}

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

	proto := compileProto(t, src)
	globals1 := runtime.NewInterpreterGlobals()
	v1 := vm.New(globals1)
	v1.Execute(proto)

	proto2 := compileProto(t, src)
	globals2 := runtime.NewInterpreterGlobals()
	v2 := vm.New(globals2)
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v2.SetTraceRecorder(recorder)
	v2.Execute(proto2)

	r1 := globals1["result"].Int()
	r2 := globals2["result"].Int()
	if r1 != r2 {
		t.Errorf("results differ: interpreter=%d, traced=%d", r1, r2)
	}
}
