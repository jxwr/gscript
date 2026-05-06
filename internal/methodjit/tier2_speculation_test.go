package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTier2SpeculationPlanSnapshotsFeedbackMaturity(t *testing.T) {
	proto := &vm.FuncProto{Code: make([]uint32, 3)}
	proto.EnsureFeedback()
	proto.Feedback[0].Result = vm.FBInt
	proto.FieldAccessFeedback[1].ObserveFieldCache(runtime.FieldCacheEntry{ShapeID: 7, FieldIdx: 2}, runtime.IntValue(1), vm.TableAccessKindGet)
	proto.TableKeyFeedback[2].ObserveTableAccess(runtime.NewTable(), runtime.StringValue("k"), runtime.IntValue(2), vm.TableAccessKindGet, -1, -1)

	plan := NewTier2SpeculationPlan(proto)
	if plan.Snapshot.TypeObserved != 1 {
		t.Fatalf("TypeObserved=%d want 1", plan.Snapshot.TypeObserved)
	}
	if plan.Snapshot.FieldObserved != 1 {
		t.Fatalf("FieldObserved=%d want 1", plan.Snapshot.FieldObserved)
	}
	if plan.Snapshot.TableKeyObserved != 1 {
		t.Fatalf("TableKeyObserved=%d want 1", plan.Snapshot.TableKeyObserved)
	}
	if typ, ok := plan.ResultGuardType(0); !ok || typ != TypeInt {
		t.Fatalf("ResultGuardType=%v ok=%v want int,true", typ, ok)
	}
	if aux := plan.FieldShapeAux2(1); aux == 0 {
		t.Fatal("FieldShapeAux2 returned zero for stable field feedback")
	}
}

func TestTier2RecompilePolicyKeepsCompiledCodeWithoutMaturedFeedback(t *testing.T) {
	var policy Tier2RecompilePolicy
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{
			TypeObserved:     10,
			FieldObserved:    2,
			TableKeyObserved: 3,
			CallObserved:     1,
		},
	}
	current := Tier2FeedbackSnapshot{
		TypeObserved:     10,
		FieldObserved:    2,
		TableKeyObserved: 3,
		CallObserved:     1,
	}
	if policy.ShouldRefresh(nil, cf, current) {
		t.Fatal("policy should preserve Tier2 code when feedback has not matured")
	}
}

func TestTier2RecompilePolicyRefreshesWhenStructuralFeedbackArrivesLate(t *testing.T) {
	var policy Tier2RecompilePolicy
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{
			TypeObserved: 4,
		},
	}
	current := Tier2FeedbackSnapshot{
		TypeObserved:     4,
		TableKeyObserved: 1,
	}
	if !policy.ShouldRefresh(nil, cf, current) {
		t.Fatal("policy should refresh when table-key feedback appears after Tier2 compile")
	}
}

func TestTier2RecompilePolicyIgnoresTinyTypeOnlyGrowth(t *testing.T) {
	var policy Tier2RecompilePolicy
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{
			TypeObserved: 4,
		},
	}
	current := Tier2FeedbackSnapshot{
		TypeObserved: 6,
	}
	if policy.ShouldRefresh(nil, cf, current) {
		t.Fatal("policy should not refresh for small type-only feedback growth")
	}
}

func TestTieringManagerRetiresStaleTier2AfterExitFeedback(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "leaf", Code: make([]uint32, 2)}
	proto.EnsureFeedback()
	proto.Feedback[0].Result = vm.FBInt
	proto.TableKeyFeedback[1].Count = 1
	proto.DirectEntryPtr = 123
	proto.Tier2DirectEntryPtr = 456
	proto.Tier2Promoted = true

	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{
			TypeObserved: 1,
		},
	}
	tm.retireStaleTier2AfterFeedback(proto, cf)

	if proto.DirectEntryPtr != 0 || proto.Tier2DirectEntryPtr != 0 || proto.Tier2Promoted {
		t.Fatalf("stale Tier2 install was not cleared: direct=%#x tier2=%#x promoted=%v",
			proto.DirectEntryPtr, proto.Tier2DirectEntryPtr, proto.Tier2Promoted)
	}
}
