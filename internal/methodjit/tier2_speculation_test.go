package methodjit

import (
	"testing"
	"unsafe"

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

func TestTier2SpeculationPlanQueriesSpecializationProfile(t *testing.T) {
	plan := Tier2SpeculationPlan{
		Profile: Tier2SpecializationProfile{
			Guards: []SpecializationGuard{
				{Kind: SpecGuardResultType, PC: 1, Type: TypeFloat},
				{Kind: SpecGuardOperandType, PC: 2, Slot: "left", Type: TypeInt},
				{Kind: SpecGuardOperandType, PC: 2, Slot: "right", Type: TypeFloat},
				{Kind: SpecGuardTableKind, PC: 3, TableKind: vm.FBKindFloat},
				{Kind: SpecGuardFieldShape, PC: 4, ShapeID: 11, FieldIdx: 5, Type: TypeInt},
				{Kind: SpecGuardStringShapeKey, PC: 5, Key: "k", ShapeID: 12, FieldIdx: 6, AccessKind: vm.TableAccessKindGet, Type: TypeFloat},
			},
		},
	}
	if typ, ok := plan.ResultGuardType(1); !ok || typ != TypeFloat {
		t.Fatalf("ResultGuardType=%v ok=%v want float,true", typ, ok)
	}
	left, leftOK, right, rightOK := plan.OperandGuardTypes(2)
	if !leftOK || !rightOK || left != TypeInt || right != TypeFloat {
		t.Fatalf("operands left=%v/%v right=%v/%v", left, leftOK, right, rightOK)
	}
	if got := plan.TableKindAux(3); got != int64(vm.FBKindFloat) {
		t.Fatalf("TableKindAux=%d want %d", got, vm.FBKindFloat)
	}
	if got := plan.FieldShapeAux2(4); got == 0 {
		t.Fatal("FieldShapeAux2 returned zero")
	}
	if typ, ok := plan.FieldValueGuardType(4); !ok || typ != TypeInt {
		t.Fatalf("FieldValueGuardType=%v ok=%v want int,true", typ, ok)
	}
	key, shapeID, fieldIdx, ok := plan.StableStringShapeField(5, vm.TableAccessKindGet)
	if !ok || key != "k" || shapeID != 12 || fieldIdx != 6 {
		t.Fatalf("StableStringShapeField=%q %d %d %v", key, shapeID, fieldIdx, ok)
	}
	if typ, ok := plan.StringShapeValueGuardType(5, vm.TableAccessKindGet); !ok || typ != TypeFloat {
		t.Fatalf("StringShapeValueGuardType=%v ok=%v want float,true", typ, ok)
	}
}

func TestTier2SpeculationPlanFallsBackToTableKeyArrayKind(t *testing.T) {
	proto := &vm.FuncProto{Name: "array_kind_fallback", Code: make([]uint32, 1)}
	proto.EnsureFeedback()
	tbl := runtime.NewTableSizedKind(4, 0, runtime.ArrayInt)
	proto.TableKeyFeedback[0].ObserveTableAccess(tbl, runtime.IntValue(1), runtime.IntValue(42), vm.TableAccessKindSet, 0, -1)

	plan := NewTier2SpeculationPlan(proto)
	if got := plan.TableKindAux(0); got != int64(vm.FBKindInt) {
		t.Fatalf("TableKindAux=%d want %d", got, vm.FBKindInt)
	}
}

func TestTier2SpeculationPlanUsesSameFieldWriteTypeForColdGet(t *testing.T) {
	proto := &vm.FuncProto{
		Name: "same_field_write_type",
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 3),
			vm.EncodeABC(vm.OP_SETFIELD, 0, 3, 2),
		},
	}
	proto.EnsureFeedback()
	proto.Feedback[1].Result = vm.FBInt

	plan := NewTier2SpeculationPlan(proto)
	if typ, ok := plan.FieldValueGuardType(0); !ok || typ != TypeInt {
		t.Fatalf("FieldValueGuardType=%v ok=%v want int,true", typ, ok)
	}
}

func TestTier2SpeculationPlanSuppressesUnstableGuardPC(t *testing.T) {
	proto := &vm.FuncProto{
		Name: "suppressed_guard",
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 3),
			vm.EncodeABC(vm.OP_SETFIELD, 0, 3, 2),
		},
	}
	proto.EnsureFeedback()
	proto.Feedback[0].Result = vm.FBInt
	proto.Feedback[1].Result = vm.FBInt

	plain := NewTier2SpeculationPlan(proto)
	if typ, ok := plain.ResultGuardType(0); !ok || typ != TypeInt {
		t.Fatalf("plain ResultGuardType=%v ok=%v want int,true", typ, ok)
	}
	if typ, ok := plain.FieldValueGuardType(0); !ok || typ != TypeInt {
		t.Fatalf("plain FieldValueGuardType=%v ok=%v want int,true", typ, ok)
	}

	suppressed := NewTier2SpeculationPlanWithSuppressedGuards(proto, map[int]bool{0: true})
	if typ, ok := suppressed.ResultGuardType(0); ok || typ != TypeUnknown {
		t.Fatalf("suppressed ResultGuardType=%v ok=%v want unknown,false", typ, ok)
	}
	if typ, ok := suppressed.FieldValueGuardType(0); ok || typ != TypeUnknown {
		t.Fatalf("suppressed FieldValueGuardType=%v ok=%v want unknown,false", typ, ok)
	}
	if typ, ok := suppressed.ResultGuardType(1); !ok || typ != TypeInt {
		t.Fatalf("unrelated ResultGuardType=%v ok=%v want int,true", typ, ok)
	}
	summary := suppressed.Summary()
	if summary.SuppressedCount != 1 || len(summary.SuppressedPCs) != 1 || summary.SuppressedPCs[0] != 0 {
		t.Fatalf("suppressed summary=%+v want one PC 0", summary)
	}
	if suppressed.Profile.Version.GuardCount >= plain.Profile.Version.GuardCount {
		t.Fatalf("suppressed active guard count=%d want less than plain %d",
			suppressed.Profile.Version.GuardCount, plain.Profile.Version.GuardCount)
	}
}

func TestTier2SpeculationPlanSuppressesOnlyMatchingGuardKind(t *testing.T) {
	proto := &vm.FuncProto{Name: "kind_scoped", Code: make([]uint32, 1)}
	proto.EnsureFeedback()
	proto.Feedback[0].Result = vm.FBInt
	proto.Feedback[0].Kind = vm.FBKindFloat

	constStringSuppressed := NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, nil, map[int]map[string]bool{
		0: {"GuardConstString": true},
	})
	if typ, ok := constStringSuppressed.ResultGuardType(0); !ok || typ != TypeInt {
		t.Fatalf("const-string suppression should not hide type guard: %v %v", typ, ok)
	}
	if got := constStringSuppressed.TableKindAux(0); got != int64(vm.FBKindFloat) {
		t.Fatalf("const-string suppression should not hide table-kind guard: %d", got)
	}

	typeSuppressed := NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, nil, map[int]map[string]bool{
		0: {"GuardType": true},
	})
	if typ, ok := typeSuppressed.ResultGuardType(0); ok || typ != TypeUnknown {
		t.Fatalf("type suppression should hide type guard: %v %v", typ, ok)
	}
	if got := typeSuppressed.TableKindAux(0); got != int64(vm.FBKindFloat) {
		t.Fatalf("type suppression should not hide table-kind guard: %d", got)
	}

	tableKindSuppressed := NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, nil, map[int]map[string]bool{
		0: {"GuardTableKind": true},
	})
	if typ, ok := tableKindSuppressed.ResultGuardType(0); !ok || typ != TypeInt {
		t.Fatalf("table-kind suppression should not hide type guard: %v %v", typ, ok)
	}
	if got := tableKindSuppressed.TableKindAux(0); got != 0 {
		t.Fatalf("table-kind suppression should hide table-kind guard: %d", got)
	}
	if tableKindSuppressed.Profile.Summary().GuardKinds[string(SpecGuardTableKind)] != 0 {
		t.Fatalf("table-kind suppression should remove active table-kind profile guard: %+v",
			tableKindSuppressed.Profile.Summary())
	}
}

func TestTier2SpeculationPlanRejectsConflictingSameFieldWriteTypes(t *testing.T) {
	proto := &vm.FuncProto{
		Name: "same_field_conflict",
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 3),
			vm.EncodeABC(vm.OP_SETFIELD, 0, 3, 2),
			vm.EncodeABC(vm.OP_SETFIELD, 0, 3, 4),
		},
	}
	proto.EnsureFeedback()
	proto.Feedback[1].Result = vm.FBInt
	proto.Feedback[2].Result = vm.FBFloat

	plan := NewTier2SpeculationPlan(proto)
	if typ, ok := plan.FieldValueGuardType(0); ok || typ != TypeUnknown {
		t.Fatalf("FieldValueGuardType=%v ok=%v want unknown,false", typ, ok)
	}
}

func TestTier2SpeculationPlanDoesNotInferSameFieldFloatWrites(t *testing.T) {
	proto := &vm.FuncProto{
		Name: "same_field_float",
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 3),
			vm.EncodeABC(vm.OP_SETFIELD, 0, 3, 2),
		},
	}
	proto.EnsureFeedback()
	proto.Feedback[1].Result = vm.FBFloat

	plan := NewTier2SpeculationPlan(proto)
	if typ, ok := plan.FieldValueGuardType(0); ok || typ != TypeUnknown {
		t.Fatalf("FieldValueGuardType=%v ok=%v want unknown,false", typ, ok)
	}
}

func TestTier2SpecializationProfileBuildsGenericGuardSet(t *testing.T) {
	proto := &vm.FuncProto{Name: "caller", Code: make([]uint32, 5)}
	callee := &vm.FuncProto{Name: "callee"}
	proto.EnsureFeedback()
	proto.Feedback[0].Left = vm.FBInt
	proto.Feedback[0].Right = vm.FBInt
	proto.Feedback[0].Result = vm.FBInt
	proto.Feedback[1].Kind = vm.FBKindInt
	proto.FieldAccessFeedback[2].ObserveFieldCache(runtime.FieldCacheEntry{ShapeID: 7, FieldIdx: 2}, runtime.IntValue(1), vm.TableAccessKindGet)
	proto.TableKeyFeedback[3].Count = 3
	proto.TableKeyFeedback[3].ShapeID = 9
	proto.TableKeyFeedback[3].FieldIdx = 1
	proto.TableKeyFeedback[3].FieldIdxSeen = true
	proto.TableKeyFeedback[3].StringKey = "stable"
	proto.TableKeyFeedback[3].StringKeySeen = true
	proto.TableKeyFeedback[3].ValueType = vm.FBFloat
	proto.TableKeyFeedback[3].AccessKind = vm.TableAccessKindGet
	proto.CallSiteFeedback[4].Count = callResultRangeGuardMinCount
	proto.CallSiteFeedback[4].CalleeVMProto = callee
	proto.CallSiteFeedback[4].CalleeVMProtos[0] = callee
	proto.CallSiteFeedback[4].CalleeVMProtoCount = 1
	proto.CallSiteFeedback[4].NArgs = 2
	proto.CallSiteFeedback[4].ResultArity = 1
	for i := 0; i < int(callResultRangeGuardMinCount); i++ {
		proto.CallSiteFeedback[4].ResultRange.Observe(runtime.IntValue(int64(i)))
	}

	profile := BuildTier2SpecializationProfile(proto)
	summary := profile.Summary()
	if summary.GuardCount < 7 {
		t.Fatalf("guard count=%d want at least 7", summary.GuardCount)
	}
	if summary.GuardKinds[string(SpecGuardOperandType)] != 2 {
		t.Fatalf("operand guards=%d want 2", summary.GuardKinds[string(SpecGuardOperandType)])
	}
	if summary.GuardKinds[string(SpecGuardResultType)] != 1 {
		t.Fatalf("result guards=%d want 1", summary.GuardKinds[string(SpecGuardResultType)])
	}
	if summary.GuardKinds[string(SpecGuardFieldShape)] != 1 {
		t.Fatalf("field guards=%d want 1", summary.GuardKinds[string(SpecGuardFieldShape)])
	}
	if summary.GuardKinds[string(SpecGuardStringShapeKey)] != 1 {
		t.Fatalf("string-shape guards=%d want 1", summary.GuardKinds[string(SpecGuardStringShapeKey)])
	}
	if summary.GuardKinds[string(SpecGuardCallVMProto)] != 1 {
		t.Fatalf("call proto guards=%d want 1", summary.GuardKinds[string(SpecGuardCallVMProto)])
	}
	if summary.GuardKinds[string(SpecGuardCallResultRange)] != 1 {
		t.Fatalf("call result range guards=%d want 1", summary.GuardKinds[string(SpecGuardCallResultRange)])
	}
	if profile.Version.Hash == 0 {
		t.Fatal("specialization version hash is zero")
	}
}

func TestTier2SpecializationProfileRequiresMaturePolymorphicCallFeedback(t *testing.T) {
	proto := &vm.FuncProto{Name: "caller", Code: make([]uint32, 2)}
	calleeA := &vm.FuncProto{Name: "a"}
	calleeB := &vm.FuncProto{Name: "b"}
	proto.EnsureFeedback()
	fb := &proto.CallSiteFeedback[1]
	fb.Count = 1
	fb.Flags = vm.CallSiteCalleePolymorphic
	fb.NArgs = 1
	fb.ResultArity = 1
	fb.CalleeVMProtos[0] = calleeA
	fb.CalleeVMProtos[1] = calleeB
	fb.CalleeVMProtoCount = 2

	profile := BuildTier2SpecializationProfile(proto)
	if profile.Summary().GuardKinds[string(SpecGuardCallPolymorphic)] != 0 {
		t.Fatalf("immature polymorphic call produced guard: %+v", profile.Summary())
	}
	fb.Count = wholeCallKernelMinStableObservations
	profile = BuildTier2SpecializationProfile(proto)
	if profile.Summary().GuardKinds[string(SpecGuardCallPolymorphic)] != 1 {
		t.Fatalf("mature polymorphic call guard count mismatch: %+v", profile.Summary())
	}
	fb.Flags |= vm.CallSiteArityPolymorphic
	profile = BuildTier2SpecializationProfile(proto)
	if profile.Summary().GuardKinds[string(SpecGuardCallPolymorphic)] != 0 {
		t.Fatalf("arity-polymorphic call produced guard: %+v", profile.Summary())
	}
}

func TestTier2SpecializationProfilePrefersStableVMClosureGuard(t *testing.T) {
	proto := &vm.FuncProto{Name: "caller", Code: make([]uint32, 2)}
	callee := &vm.FuncProto{Name: "step"}
	cl := vm.NewClosure(callee)
	proto.EnsureFeedback()
	fb := &proto.CallSiteFeedback[1]
	fb.Count = 3
	fb.NArgs = 0
	fb.ResultArity = 1
	fb.CalleeVMProto = callee
	fb.CalleeVMClosure = uintptr(unsafe.Pointer(cl))
	fb.CalleeVMProtos[0] = callee
	fb.CalleeVMProtoCount = 1

	profile := BuildTier2SpecializationProfile(proto)
	summary := profile.Summary()
	if summary.GuardKinds[string(SpecGuardCallVMClosure)] != 1 {
		t.Fatalf("closure guard count mismatch: %+v", summary)
	}
	if summary.GuardKinds[string(SpecGuardCallVMProto)] != 0 {
		t.Fatalf("stable closure should suppress weaker proto guard: %+v", summary)
	}
}

func TestTier2SpecializationProfileMarksClosureRecurrenceGuard(t *testing.T) {
	proto := &vm.FuncProto{Name: "caller", Code: make([]uint32, 2)}
	callee := &vm.FuncProto{
		Name:      "step",
		NumParams: 0,
		MaxStack:  5,
		Upvalues: []vm.UpvalDesc{
			{Name: "value", InStack: true, Index: 2},
			{Name: "delta", InStack: true, Index: 1},
		},
		Code: []uint32{
			vm.EncodeABC(vm.OP_GETUPVAL, 1, 0, 0),
			vm.EncodeABC(vm.OP_GETUPVAL, 2, 1, 0),
			vm.EncodeABC(vm.OP_ADD, 0, 1, 2),
			vm.EncodeABC(vm.OP_SETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_GETUPVAL, 0, 0, 0),
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}
	cl := vm.NewClosure(callee)
	proto.EnsureFeedback()
	fb := &proto.CallSiteFeedback[1]
	fb.Count = 3
	fb.NArgs = 0
	fb.ResultArity = 1
	fb.CalleeVMProto = callee
	fb.CalleeVMClosure = uintptr(unsafe.Pointer(cl))
	fb.CalleeVMProtos[0] = callee
	fb.CalleeVMProtoCount = 1

	profile := BuildTier2SpecializationProfile(proto)
	summary := profile.Summary()
	if summary.GuardKinds[string(SpecGuardCallClosureRecurrence)] != 1 {
		t.Fatalf("closure recurrence guard count mismatch: %+v", summary)
	}
	if summary.GuardKinds[string(SpecGuardCallVMClosure)] != 0 {
		t.Fatalf("closure recurrence should suppress generic closure guard: %+v", summary)
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

func TestTier2RecompilePolicyRefreshesWhenVersionGainsGuards(t *testing.T) {
	var policy Tier2RecompilePolicy
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{TypeObserved: 1},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 1,
		},
	}
	current := Tier2SpecializationProfile{
		Snapshot: Tier2FeedbackSnapshot{
			TypeObserved:  1,
			FieldObserved: 1,
		},
		Version: Tier2SpecializationVersion{
			Hash:       2,
			GuardCount: 2,
		},
	}
	if !policy.ShouldRefreshProfile(cf, current) {
		t.Fatal("policy should refresh when specialization version gains a guard")
	}
}

func TestTier2RecompilePolicyRefreshesWhenStructuralVersionChanges(t *testing.T) {
	var policy Tier2RecompilePolicy
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{CallObserved: 1},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 2,
		},
	}
	current := Tier2SpecializationProfile{
		Snapshot: Tier2FeedbackSnapshot{CallObserved: 1},
		Version: Tier2SpecializationVersion{
			Hash:       2,
			GuardCount: 2,
		},
	}
	if !policy.ShouldRefreshProfile(cf, current) {
		t.Fatal("policy should refresh when structural specialization version changes")
	}
}

func TestTier2DeoptPolicyClassifiesMaturedFeedbackRefresh(t *testing.T) {
	proto := &vm.FuncProto{Name: "deopt", Code: make([]uint32, 2)}
	proto.EnsureFeedback()
	proto.Feedback[0].Result = vm.FBInt
	proto.FieldAccessFeedback[1].ObserveFieldCache(runtime.FieldCacheEntry{ShapeID: 3, FieldIdx: 1}, runtime.IntValue(1), vm.TableAccessKindGet)
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{TypeObserved: 1},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 1,
		},
	}

	action := Tier2DeoptPolicy{}.DecideRuntimeDeopt(proto, cf, 7)
	if action.Kind != Tier2DeoptRefreshAndFallback {
		t.Fatalf("action=%s want %s", action.Kind, Tier2DeoptRefreshAndFallback)
	}
	if !action.PreciseResume || action.ResumePC != 7 {
		t.Fatalf("resume=%v pc=%d want precise pc 7", action.PreciseResume, action.ResumePC)
	}
	if action.CurrentProfile.Version.GuardCount <= cf.SpecializationVersion.GuardCount {
		t.Fatalf("guard count did not grow: after=%d before=%d",
			action.CurrentProfile.Version.GuardCount, cf.SpecializationVersion.GuardCount)
	}
}

func TestTier2DeoptPolicyUsesProvidedActiveProfile(t *testing.T) {
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 0,
		},
	}
	active := Tier2SpecializationProfile{
		Snapshot: Tier2FeedbackSnapshot{TypeObserved: 1},
		Version:  Tier2SpecializationVersion{Hash: 1, GuardCount: 0},
	}
	action := Tier2DeoptPolicy{}.DecideRuntimeDeoptWithProfile(cf, 0, active)
	if action.Kind != Tier2DeoptDisableAndFallback {
		t.Fatalf("action=%s want disable when active profile has no refreshable delta", action.Kind)
	}
}

func TestTieringManagerRefreshDeoptDoesNotMarkTier2Failed(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "refresh", Code: make([]uint32, 2)}
	proto.EnsureFeedback()
	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{TypeObserved: 1},
		SpecializationVersion: Tier2SpecializationVersion{
			Hash:       1,
			GuardCount: 1,
		},
	}
	tm.ensureTierStateStore()
	tm.tierState.markCompiled(proto, cf)
	proto.Tier2Promoted = true
	proto.DirectEntryPtr = 123

	tm.applyTier2DeoptAction(proto, Tier2DeoptAction{
		Kind:   Tier2DeoptRefreshAndFallback,
		Reason: "test refresh",
		CurrentProfile: Tier2SpecializationProfile{
			Version: Tier2SpecializationVersion{Hash: 2, GuardCount: 2},
		},
	})

	if tm.tier2HasFailed(proto) {
		t.Fatal("refresh deopt should not mark Tier2 failed")
	}
	if _, ok := tm.tier2CompiledFor(proto); ok {
		t.Fatal("refresh deopt should clear stale compiled install")
	}
	if proto.Tier2Promoted || proto.DirectEntryPtr != 0 {
		t.Fatalf("refresh deopt left install visible: promoted=%v direct=%#x",
			proto.Tier2Promoted, proto.DirectEntryPtr)
	}
	if _, ok := tm.recompileQueue.take(proto); !ok {
		t.Fatal("refresh deopt should queue a forced recompile")
	}
}

func TestTieringManagerGuardDeoptSuppressesPCAndRefreshes(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "guard_refresh", Code: make([]uint32, 4)}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			42: {PC: 3, Op: "GuardType", Reason: "GuardType(int)"},
		},
	}
	ctx := &ExecContext{
		DeoptInstrID: 42,
		ExitResumePC: 9,
	}

	action, ok := tm.guardDeoptRefreshAction(proto, cf, ctx)
	if !ok {
		t.Fatal("guard deopt should produce refresh action")
	}
	if action.Kind != Tier2DeoptRefreshAndFallback {
		t.Fatalf("action=%s want %s", action.Kind, Tier2DeoptRefreshAndFallback)
	}
	if action.GuardRelaxedPC != 3 {
		t.Fatalf("GuardRelaxedPC=%d want 3", action.GuardRelaxedPC)
	}
	if !action.PreciseResume || action.ResumePC != 9 {
		t.Fatalf("resume=%v pc=%d want precise pc 9", action.PreciseResume, action.ResumePC)
	}
	if !tm.tier2SuppressedGuards(proto)[3] {
		t.Fatal("guard PC 3 was not recorded as suppressed")
	}

	tm.ensureTierStateStore()
	tm.tierState.markCompiled(proto, cf)
	tm.applyTier2DeoptAction(proto, action)
	if tm.tier2HasFailed(proto) {
		t.Fatal("guard refresh should not mark Tier2 failed")
	}
}

func TestTieringManagerCalleeGuardDeoptSuppressesCallsiteAndRefreshes(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "callee_guard_refresh", Code: make([]uint32, 5)}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			77: {PC: 4, Op: "GuardCalleeProto", Reason: "GuardCalleeProto"},
		},
	}
	ctx := &ExecContext{DeoptInstrID: 77}

	action, ok := tm.guardDeoptRefreshAction(proto, cf, ctx)
	if !ok {
		t.Fatal("callee guard deopt should produce refresh action")
	}
	if action.Kind != Tier2DeoptRefreshAndFallback {
		t.Fatalf("action=%s want %s", action.Kind, Tier2DeoptRefreshAndFallback)
	}
	if action.GuardRelaxedPC != 4 {
		t.Fatalf("GuardRelaxedPC=%d want 4", action.GuardRelaxedPC)
	}
	if !tm.tier2SuppressedGuards(proto)[4] {
		t.Fatal("callsite PC 4 was not recorded as suppressed")
	}
}

func TestTieringManagerConstStringGuardDeoptSuppressesPCAndRefreshes(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "const_string_guard_refresh", Code: make([]uint32, 3)}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			11: {PC: 2, Op: "GuardConstString", Reason: "GuardConstString"},
		},
	}
	ctx := &ExecContext{DeoptInstrID: 11}

	action, ok := tm.guardDeoptRefreshAction(proto, cf, ctx)
	if !ok {
		t.Fatal("const-string guard deopt should produce refresh action")
	}
	if action.Kind != Tier2DeoptRefreshAndFallback || action.GuardRelaxedOp != "GuardConstString" {
		t.Fatalf("action=%+v want refresh GuardConstString", action)
	}
	if action.GuardFailCount != 1 {
		t.Fatalf("guard fail count=%d want 1", action.GuardFailCount)
	}
	if !tm.tier2SuppressedGuards(proto)[2] {
		t.Fatal("PC 2 was not recorded as suppressed")
	}
}

func TestTieringManagerTableKindGuardDeoptSuppressesPCAndRefreshes(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "table_kind_guard_refresh", Code: make([]uint32, 4)}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			21: {PC: 3, Op: "GuardTableKind", Reason: "GuardTableKind(float)"},
		},
	}
	ctx := &ExecContext{DeoptInstrID: 21, ExitResumePC: 6}

	action, ok := tm.guardDeoptRefreshAction(proto, cf, ctx)
	if !ok {
		t.Fatal("table-kind guard deopt should produce refresh action")
	}
	if action.Kind != Tier2DeoptRefreshAndFallback || action.GuardRelaxedOp != "GuardTableKind" {
		t.Fatalf("action=%+v want refresh GuardTableKind", action)
	}
	if action.GuardFailCount != 1 {
		t.Fatalf("guard fail count=%d want 1", action.GuardFailCount)
	}
	if action.GuardRelaxedPC != 3 || !action.PreciseResume || action.ResumePC != 6 {
		t.Fatalf("action resume/pc mismatch: %+v", action)
	}
	kinds := tm.tier2SuppressedGuardKinds(proto)
	if kinds[3]["GuardTableKind"] != true {
		t.Fatalf("table-kind guard was not kind-suppressed: %#v", kinds)
	}
	if kinds[tier2GlobalGuardSuppressPC]["GuardTableKind"] != true {
		t.Fatalf("table-kind guard was not globally kind-suppressed: %#v", kinds)
	}
	plan := NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, tm.tier2SuppressedGuards(proto), kinds)
	if !plan.GuardKindSuppressed(0, "GuardTableKind") {
		t.Fatalf("table-kind guard should be globally suppressed by deopt: %#v", kinds)
	}
}

func TestTieringManagerIntRangeGuardDeoptSuppressesPCAndRefreshes(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "int_range_guard_refresh", Code: make([]uint32, 4)}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			31: {PC: 2, Op: "GuardIntRange", Reason: "GuardIntRange(-2147483648..2147483647)"},
		},
	}
	ctx := &ExecContext{DeoptInstrID: 31, ExitResumePC: 5}

	action, ok := tm.guardDeoptRefreshAction(proto, cf, ctx)
	if !ok {
		t.Fatal("int-range guard deopt should produce refresh action")
	}
	if action.Kind != Tier2DeoptRefreshAndFallback || action.GuardRelaxedOp != "GuardIntRange" {
		t.Fatalf("action=%+v want refresh GuardIntRange", action)
	}
	if action.GuardFailCount != 1 {
		t.Fatalf("guard fail count=%d want 1", action.GuardFailCount)
	}
	if action.GuardRelaxedPC != 2 || !action.PreciseResume || action.ResumePC != 5 {
		t.Fatalf("action resume/pc mismatch: %+v", action)
	}
	kinds := tm.tier2SuppressedGuardKinds(proto)
	if kinds[2]["GuardIntRange"] != true {
		t.Fatalf("int-range guard was not kind-suppressed: %#v", kinds)
	}
	plan := NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, tm.tier2SuppressedGuards(proto), kinds)
	if !plan.GuardKindSuppressed(2, "GuardIntRange") {
		t.Fatalf("int-range guard should be suppressed at deopt pc: %#v", kinds)
	}
}

func TestTieringManagerSyntheticGuardDeoptSuppressesKindGlobally(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{Name: "synthetic_int_range_guard_refresh", Code: make([]uint32, 4)}
	cf := &CompiledFunction{
		ExitSites: map[int]ExitSiteMeta{
			31: {PC: -1, Op: "GuardIntRange", Reason: "GuardIntRange(0..1048576)"},
		},
	}
	ctx := &ExecContext{DeoptInstrID: 31, ExitResumePC: 5}

	action, ok := tm.guardDeoptRefreshAction(proto, cf, ctx)
	if !ok {
		t.Fatal("synthetic int-range guard deopt should produce refresh action")
	}
	if action.Kind != Tier2DeoptRefreshAndFallback || action.GuardRelaxedOp != "GuardIntRange" {
		t.Fatalf("action=%+v want refresh GuardIntRange", action)
	}
	if action.GuardRelaxedPC != tier2GlobalGuardSuppressPC {
		t.Fatalf("GuardRelaxedPC=%d want global sentinel", action.GuardRelaxedPC)
	}
	kinds := tm.tier2SuppressedGuardKinds(proto)
	if kinds[tier2GlobalGuardSuppressPC]["GuardIntRange"] != true {
		t.Fatalf("synthetic int-range guard was not globally suppressed: %#v", kinds)
	}
	plan := NewTier2SpeculationPlanWithSuppressedGuardKinds(proto, tm.tier2SuppressedGuards(proto), kinds)
	if !plan.GuardKindSuppressed(99, "GuardIntRange") {
		t.Fatalf("global int-range guard suppression not visible to speculation plan: %#v", kinds)
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

func TestTieringManagerQueuesLoopTier2RefreshAtNextEntry(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{
		Name: "loop",
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_FORPREP, 0, 1),
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 0),
			vm.EncodeAsBx(vm.OP_FORLOOP, 0, -2),
		},
	}
	proto.EnsureFeedback()
	proto.FieldAccessFeedback[1].Count = 1
	proto.FieldAccessFeedback[1].ShapeID = 11
	proto.FieldAccessFeedback[1].FieldIdx = 0
	proto.DirectEntryPtr = 123
	proto.Tier2DirectEntryPtr = 456
	proto.Tier2Promoted = true

	cf := &CompiledFunction{
		SpeculationSnapshot: Tier2FeedbackSnapshot{},
	}
	tm.tier2Compiled[proto] = cf
	tm.retireStaleTier2AfterFeedback(proto, cf)

	if _, ok := tm.tier2CompiledFor(proto); !ok {
		t.Fatal("loop refresh should leave current compiled body available for diagnostics/current invocation")
	}
	if proto.DirectEntryPtr != 0 || proto.Tier2DirectEntryPtr != 0 || proto.Tier2Promoted {
		t.Fatalf("loop refresh should revoke published entries: direct=%#x tier2=%#x promoted=%v",
			proto.DirectEntryPtr, proto.Tier2DirectEntryPtr, proto.Tier2Promoted)
	}
	if _, ok := tm.recompileQueue.take(proto); !ok {
		t.Fatal("loop refresh should queue next-entry recompile")
	}
	if cf.SpeculationSnapshot.FieldObserved != 1 || cf.SpecializationVersion.Hash == 0 {
		t.Fatalf("loop refresh should mark compiled body at current epoch: snapshot=%#v version=%#v",
			cf.SpeculationSnapshot, cf.SpecializationVersion)
	}
}

func TestTieringManagerLoopTier2RefreshTracesOnlyFirstQueue(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{
		Name: "loop",
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_FORPREP, 0, 1),
			vm.EncodeABC(vm.OP_GETFIELD, 1, 0, 0),
			vm.EncodeAsBx(vm.OP_FORLOOP, 0, -2),
		},
	}
	proto.EnsureFeedback()
	proto.FieldAccessFeedback[1].Count = 1
	proto.FieldAccessFeedback[1].ShapeID = 11
	proto.FieldAccessFeedback[1].FieldIdx = 0
	cf := &CompiledFunction{SpeculationSnapshot: Tier2FeedbackSnapshot{}}

	tm.retireStaleTier2AfterFeedback(proto, cf)
	tm.retireStaleTier2AfterFeedback(proto, cf)
	tm.retireStaleTier2AfterFeedback(proto, cf)

	if _, ok := tm.recompileQueue.take(proto); !ok {
		t.Fatal("expected one queued refresh request")
	}
	if _, ok := tm.recompileQueue.take(proto); ok {
		t.Fatal("expected refresh request to be queued only once")
	}
}
