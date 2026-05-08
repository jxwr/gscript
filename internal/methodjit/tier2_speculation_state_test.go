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
	if compiled.SuppressedCount != 2 || compiled.SuppressedPCs[0] != 3 || compiled.SuppressedPCs[1] != 8 {
		t.Fatalf("suppressed state mismatch: %+v", compiled)
	}
	if compiled.SuppressedKinds["GuardType"] != 1 || compiled.SuppressedKinds["GuardConstString"] != 1 {
		t.Fatalf("suppressed kinds mismatch: %+v", compiled)
	}
	if compiled.GuardFailures["GuardType"] != 2 {
		t.Fatalf("guard failures mismatch: %+v", compiled)
	}
	if compiled.ExitCount != 2 || compiled.SuppressedGuardExits != 2 || compiled.ExitKinds["ExitDeopt"] != 2 {
		t.Fatalf("exit profile summary mismatch: %+v", compiled)
	}
	failed := findSpecState(t, snap, "failed")
	if !failed.Failed || failed.FailReason != "blocked" {
		t.Fatalf("failed state mismatch: %+v", failed)
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
