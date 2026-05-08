//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestAnalyzeTier2FeedbackReadinessClassifiesStructuralMaturity(t *testing.T) {
	proto := &vm.FuncProto{
		Name: "structural",
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 0),
			vm.EncodeABC(vm.OP_SETFIELD, 0, 0, 1),
			vm.EncodeABC(vm.OP_GETTABLE, 2, 0, 1),
			vm.EncodeABC(vm.OP_CALL, 3, 2, 1),
		},
	}
	readiness := AnalyzeTier2FeedbackReadiness(proto, Tier2FeedbackSnapshot{})
	if readiness.Kind != Tier2FeedbackDelay {
		t.Fatalf("kind=%s want delay: %+v", readiness.Kind, readiness)
	}
	if readiness.ExpectedFieldSites != 2 || readiness.ExpectedTableKeySites != 1 || readiness.ExpectedCallSites != 1 {
		t.Fatalf("expected site counts mismatch: %+v", readiness)
	}
	if !readiness.ShouldDelayInitialTier2(tier2FeedbackHardHotCallCount - 1) {
		t.Fatalf("readiness should delay below hard-hot: %+v", readiness)
	}
	if readiness.ShouldDelayInitialTier2(tier2FeedbackHardHotCallCount) {
		t.Fatalf("readiness should not delay hard-hot functions: %+v", readiness)
	}

	partial := AnalyzeTier2FeedbackReadiness(proto, Tier2FeedbackSnapshot{FieldObserved: 1})
	if partial.Kind != Tier2FeedbackProvisionalNarrow {
		t.Fatalf("partial kind=%s want provisional_narrow: %+v", partial.Kind, partial)
	}

	ready := AnalyzeTier2FeedbackReadiness(proto, Tier2FeedbackSnapshot{FieldObserved: 2, TableKeyObserved: 1, CallObserved: 1})
	if ready.Kind != Tier2FeedbackReadyWide || ready.structuralImmature() != 0 {
		t.Fatalf("ready mismatch: %+v", ready)
	}
}

func TestTier2FeedbackReadinessDetectsMaturityImprovement(t *testing.T) {
	previous := Tier2FeedbackReadiness{Kind: Tier2FeedbackProvisionalNarrow, ImmatureFieldSites: 1}
	current := Tier2FeedbackReadiness{Kind: Tier2FeedbackReadyWide}
	if !current.MoreReadyThan(previous) {
		t.Fatalf("current=%+v should be more ready than previous=%+v", current, previous)
	}
	if previous.MoreReadyThan(current) {
		t.Fatalf("previous=%+v should not be more ready than current=%+v", previous, current)
	}
}

func TestTier2RecompilePolicyRefreshesWhenReadinessImproves(t *testing.T) {
	proto := &vm.FuncProto{
		Name: "readiness",
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 0),
			vm.EncodeABC(vm.OP_GETFIELD, 2, 0, 1),
		},
	}
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{FieldObserved: 1},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       7,
			GuardCount: 2,
		},
	}
	current := Tier2SpecializationProfile{
		Snapshot: Tier2FeedbackSnapshot{FieldObserved: 2},
		Version: Tier2SpecializationVersion{
			Hash:       7,
			GuardCount: 2,
		},
	}
	var policy Tier2RecompilePolicy
	if policy.ShouldRefreshProfile(cf, current) {
		t.Fatal("legacy profile-only policy should not see readiness improvement without proto context")
	}
	if !policy.ShouldRefreshProfileForProto(proto, cf, current) {
		t.Fatal("proto-aware policy should refresh when readiness improves")
	}
}
