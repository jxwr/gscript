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

func TestTier2RecompilePolicyDefaultKeepsCompiledCode(t *testing.T) {
	var policy Tier2RecompilePolicy
	if policy.ShouldRefresh(nil, nil, Tier2FeedbackSnapshot{TypeObserved: 10}) {
		t.Fatal("default recompile policy should preserve existing Tier2 behavior")
	}
}
