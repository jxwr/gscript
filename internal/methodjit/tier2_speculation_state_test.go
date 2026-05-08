//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTier2SpeculationStateSnapshotIncludesCompiledFailedAndSuppressed(t *testing.T) {
	tm := NewTieringManager()
	compiledProto := &vm.FuncProto{Name: "compiled"}
	failedProto := &vm.FuncProto{Name: "failed"}

	tm.ensureTierStateStore()
	cf := &CompiledFunction{
		SpecializationVersion: Tier2SpecializationVersion{Hash: 0x44, GuardCount: 5},
		ExitSites: map[int]ExitSiteMeta{
			11: {PC: 8, Op: "GuardType", Reason: "GuardType(int)"},
		},
		Continuations: map[Tier2ContinuationKey]Tier2Continuation{
			{PC: 8, Kind: Tier2ContinuationOp}:    {Offset: 128},
			{PC: 9, Kind: Tier2ContinuationTable}: {Offset: 160, Ambiguous: true},
		},
	}
	tm.tierState.markCompiled(compiledProto, cf)
	tm.suppressTier2GuardKind(compiledProto, 8, "GuardType")
	tm.suppressTier2GuardKind(compiledProto, 3, "GuardConstString")
	tm.recordTier2GuardFailure(compiledProto, 8, "GuardType")
	tm.recordTier2GuardFailure(compiledProto, 8, "GuardType")
	tm.recordTier2Exit(compiledProto, cf, &ExecContext{ExitCode: ExitDeopt, DeoptInstrID: 11})
	tm.recordTier2Exit(compiledProto, cf, &ExecContext{ExitCode: ExitDeopt, DeoptInstrID: 11})
	tm.markTier2Failed(failedProto, "blocked")

	snap := tm.Tier2SpeculationStateSnapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len=%d want 2: %#v", len(snap), snap)
	}
	compiled := findSpecState(t, snap, "compiled")
	if !compiled.Compiled || compiled.VersionHash != "44" || compiled.GuardCount != 5 {
		t.Fatalf("compiled state mismatch: %+v", compiled)
	}
	if compiled.ContinuationCount != 2 || compiled.AmbiguousContinuations != 1 {
		t.Fatalf("continuation state mismatch: %+v", compiled)
	}
	if compiled.SuppressedCount != 2 || compiled.SuppressedPCs[0] != 3 || compiled.SuppressedPCs[1] != 8 {
		t.Fatalf("suppressed state mismatch: %+v", compiled)
	}
	if compiled.SuppressedKinds["GuardType"] != 1 || compiled.SuppressedKinds["GuardConstString"] != 1 {
		t.Fatalf("suppressed kinds mismatch: %+v", compiled)
	}
	if compiled.GuardFailures["GuardType"] != 2 {
		t.Fatalf("guard failures mismatch: %+v", compiled)
	}
	if compiled.FeedbackReadiness.Kind != Tier2FeedbackReadyWide {
		t.Fatalf("feedback readiness mismatch: %+v", compiled.FeedbackReadiness)
	}
	if compiled.ExitCount != 2 || compiled.SuppressedGuardExits != 2 || compiled.ExitKinds["ExitDeopt"] != 2 {
		t.Fatalf("exit profile summary mismatch: %+v", compiled)
	}
	if compiled.TopExitName != "ExitDeopt" || compiled.TopExitReason != "deopt:GuardType(int)" ||
		compiled.TopExitPC != 8 || compiled.TopExitCount != 2 {
		t.Fatalf("top exit mismatch: %+v", compiled)
	}
	if compiled.NextAction != "suppressed_guard_residual" {
		t.Fatalf("next action=%q want suppressed_guard_residual: %+v", compiled.NextAction, compiled)
	}
	if compiled.NextTarget != "guard_policy" {
		t.Fatalf("next target=%q want guard_policy: %+v", compiled.NextTarget, compiled)
	}
	if compiled.NextPriority != 70 {
		t.Fatalf("next priority=%d want 70: %+v", compiled.NextPriority, compiled)
	}
	failed := findSpecState(t, snap, "failed")
	if !failed.Failed || failed.FailReason != "blocked" {
		t.Fatalf("failed state mismatch: %+v", failed)
	}
	if failed.NextAction != "tier2_failed" {
		t.Fatalf("failed next action=%q want tier2_failed", failed.NextAction)
	}
	if failed.NextPriority != 30 {
		t.Fatalf("failed next priority=%d want 30", failed.NextPriority)
	}
}

func TestTier2SpeculationNextActionPrioritizesRefreshAndHotExits(t *testing.T) {
	if got := tier2SpeculationNextAction(Tier2SpeculationState{QueuedRecompileExits: 1, ExitCount: 2}); got != "refresh_queued" {
		t.Fatalf("queued action=%q", got)
	}
	if got := tier2SpeculationNextAction(Tier2SpeculationState{ExitCount: 3, SuppressedGuardExits: 1}); got != "inspect_hot_exit" {
		t.Fatalf("hot exit action=%q", got)
	}
	if got := tier2SpeculationNextAction(Tier2SpeculationState{Compiled: true}); got != "monitor" {
		t.Fatalf("compiled action=%q", got)
	}
}

func TestTier2SpeculationNextTargetClassifiesDominantExit(t *testing.T) {
	tests := []struct {
		state Tier2SpeculationState
		want  Tier2SpeculationTarget
	}{
		{Tier2SpeculationState{NextAction: "inspect_hot_exit", TopExitName: "ExitCallExit"}, "call_specialization"},
		{Tier2SpeculationState{NextAction: "inspect_hot_exit", TopExitName: "ExitTableExit", TopExitReason: "SetField"}, "table_field_exit"},
		{Tier2SpeculationState{NextAction: "inspect_hot_exit", TopExitName: "ExitTableExit", TopExitReason: "GetTable"}, "table_access_exit"},
		{Tier2SpeculationState{NextAction: "inspect_hot_exit", TopExitName: "ExitDeopt", TopExitReason: "deopt:GuardType(int)"}, "guard_policy"},
		{Tier2SpeculationState{NextAction: "monitor", TopExitName: "ExitCallExit"}, ""},
	}
	for _, tt := range tests {
		if got := tier2SpeculationNextTarget(tt.state); got != tt.want {
			t.Fatalf("target=%q want %q for %+v", got, tt.want, tt.state)
		}
	}
}

func TestTier2SpeculationNextPriorityCombinesActionAndTarget(t *testing.T) {
	tests := []struct {
		state Tier2SpeculationState
		want  int
	}{
		{Tier2SpeculationState{NextAction: Tier2SpecActionRefreshQueued}, 100},
		{Tier2SpeculationState{NextAction: Tier2SpecActionInspectHotExit, NextTarget: Tier2SpecTargetTableFieldExit}, 90},
		{Tier2SpeculationState{NextAction: Tier2SpecActionInspectHotExit, NextTarget: Tier2SpecTargetGuardPolicy}, 80},
		{Tier2SpeculationState{NextAction: Tier2SpecActionSuppressedGuardResidual, NextTarget: Tier2SpecTargetOpExit}, 50},
		{Tier2SpeculationState{NextAction: Tier2SpecActionMonitor}, 10},
	}
	for _, tt := range tests {
		if got := tier2SpeculationNextPriority(tt.state); got != tt.want {
			t.Fatalf("priority=%d want %d for %+v", got, tt.want, tt.state)
		}
	}
}

func TestTier2SpeculationWorklistSnapshotRanksActionableStates(t *testing.T) {
	tm := NewTieringManager()
	tableProto := &vm.FuncProto{Name: "table_hot"}
	guardProto := &vm.FuncProto{Name: "guard_hot"}
	monitorProto := &vm.FuncProto{Name: "monitor_only"}

	tableCF := &CompiledFunction{
		SpecializationVersion: Tier2SpecializationVersion{Hash: 0x11, GuardCount: 1},
		ExitSites: map[int]ExitSiteMeta{
			7: {PC: 12, Op: "SetField", Reason: "SetField"},
		},
	}
	guardCF := &CompiledFunction{
		SpecializationVersion: Tier2SpecializationVersion{Hash: 0x22, GuardCount: 1},
		ExitSites: map[int]ExitSiteMeta{
			9: {PC: 19, Op: "GuardTableKind", Reason: "GuardTableKind(2)"},
		},
	}
	monitorCF := &CompiledFunction{
		SpecializationVersion: Tier2SpecializationVersion{Hash: 0x33, GuardCount: 1},
	}
	tm.ensureTierStateStore()
	tm.tierState.markCompiled(tableProto, tableCF)
	tm.tierState.markCompiled(guardProto, guardCF)
	tm.tierState.markCompiled(monitorProto, monitorCF)
	tm.recordTier2Exit(tableProto, tableCF, &ExecContext{ExitCode: ExitTableExit, TableExitID: 7, TableOp: TableOpSetField})
	tm.recordTier2Exit(guardProto, guardCF, &ExecContext{ExitCode: ExitDeopt, DeoptInstrID: 9})

	worklist := tm.Tier2SpeculationWorklistSnapshot()
	if len(worklist) != 2 {
		t.Fatalf("worklist len=%d want 2: %+v", len(worklist), worklist)
	}
	if worklist[0].Rank != 1 || worklist[0].ProtoName != "table_hot" || worklist[0].Target != Tier2SpecTargetTableFieldExit || worklist[0].Priority != 90 {
		t.Fatalf("first work item mismatch: %+v", worklist[0])
	}
	if worklist[0].FeedbackReadiness.Kind != Tier2FeedbackReadyWide {
		t.Fatalf("first work item readiness mismatch: %+v", worklist[0])
	}
	if worklist[1].Rank != 2 || worklist[1].ProtoName != "guard_hot" || worklist[1].Target != Tier2SpecTargetGuardPolicy || worklist[1].Priority != 80 {
		t.Fatalf("second work item mismatch: %+v", worklist[1])
	}
}

func TestTier2SpeculationStateSnapshotIncludesExitProfileOnlyProto(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "exit_only"}
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{CallObserved: 1},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       3,
			GuardCount: 1,
		},
		ExitSites: map[int]ExitSiteMeta{
			5: {PC: 9, Op: "Call", Reason: "Call"},
		},
	}
	tm.recordTier2Exit(proto, cf, &ExecContext{ExitCode: ExitCallExit, CallID: 5})

	state := findSpecState(t, tm.Tier2SpeculationStateSnapshot(), "exit_only")
	if state.ExitCount != 1 || state.TopExitName != "ExitCallExit" || state.NextTarget != Tier2SpecTargetCallSpecialization {
		t.Fatalf("exit-only state mismatch: %+v", state)
	}
	worklist := tm.Tier2SpeculationWorklistSnapshot()
	if len(worklist) != 1 || worklist[0].ProtoName != "exit_only" || worklist[0].Target != Tier2SpecTargetCallSpecialization {
		t.Fatalf("exit-only worklist mismatch: %+v", worklist)
	}
}

func findSpecState(t *testing.T, states []Tier2SpeculationState, name string) Tier2SpeculationState {
	t.Helper()
	for _, state := range states {
		if state.ProtoName == name {
			return state
		}
	}
	t.Fatalf("missing state %q in %#v", name, states)
	return Tier2SpeculationState{}
}
