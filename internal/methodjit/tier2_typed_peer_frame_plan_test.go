//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"
)

func TestTypedPeerFramePlanRequiresFullFrameForAllocatedCalleeSavedRegs(t *testing.T) {
	top := compileTop(t, `func step(a, tick) {
    a.count = a.count + tick
    return a.count
}`)
	step := findProtoByName(top, "step")
	if step == nil {
		t.Fatal("step proto not found")
	}
	step.LeafNoCall = protoHasNoCallLikeOps(step)
	fn := BuildGraph(step)
	fn.FixedShapeEntryGuards = map[int]FixedShapeTableFact{
		0: {
			ShapeID:    101,
			FieldNames: []string{"count"},
			FieldTypes: map[string]Type{"count": TypeInt},
			Guarded:    true,
		},
	}
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(step): %v", err)
	}
	abi := AnalyzeTypedPeerABIWithArgFacts(step, fn.FixedShapeEntryGuards)
	if !abi.Eligible {
		t.Fatalf("typed peer ABI rejected: %s", abi.RejectWhy)
	}
	plan := AnalyzeTypedPeerFramePlan(fn, AllocateRegisters(fn), abi)
	if !typedPeerClobberABIEnabled(abi) {
		t.Fatalf("table/int typed peer ABI should allow clobber entry")
	}
	if plan.CanUseThinEntry {
		t.Fatalf("thin entry unexpectedly allowed: %+v", plan)
	}
	if !typedPeerPlanReasonContains(plan, "callee-saved GPRs") {
		t.Fatalf("plan reasons missing callee-saved GPR reason: %+v", plan)
	}
	if !typedPeerPlanReasonContains(plan, "unwind-safe frame protocol") {
		t.Fatalf("plan reasons missing unwind protocol reason: %+v", plan)
	}
}

func TestCompiledFunctionCarriesTypedPeerFramePlan(t *testing.T) {
	top := compileTop(t, `func step(a, tick) {
    a.count = a.count + tick
    return a.count
}`)
	step := findProtoByName(top, "step")
	if step == nil {
		t.Fatal("step proto not found")
	}
	step.LeafNoCall = protoHasNoCallLikeOps(step)
	fn := BuildGraph(step)
	fn.FixedShapeEntryGuards = map[int]FixedShapeTableFact{
		0: {
			ShapeID:    101,
			FieldNames: []string{"count"},
			FieldTypes: map[string]Type{"count": TypeInt},
			Guarded:    true,
		},
	}
	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile(step): %v", err)
	}
	defer cf.Code.Free()
	if cf.TypedPeerFramePlan.CanUseThinEntry {
		t.Fatalf("thin entry unexpectedly allowed: %+v", cf.TypedPeerFramePlan)
	}
	if len(cf.TypedPeerFramePlan.Reasons) == 0 {
		t.Fatalf("missing typed peer frame reasons: %+v", cf.TypedPeerFramePlan)
	}
}

func TestTypedPeerCompactFrameBytesMatchesEmitterLayout(t *testing.T) {
	if got, want := typedPeerCompactFrameBytes([]int{20, 21, 22, 23, 28}, nil), 64; got != want {
		t.Fatalf("GPR-only compact frame=%d want %d", got, want)
	}
	if got, want := typedPeerCompactFrameBytes([]int{20, 21, 22, 23, 28}, []int{8}), 80; got != want {
		t.Fatalf("GPR+FPR compact frame=%d want %d", got, want)
	}
}

func typedPeerPlanReasonContains(plan Tier2TypedPeerFramePlan, needle string) bool {
	for _, reason := range plan.Reasons {
		if strings.Contains(reason, needle) {
			return true
		}
	}
	return false
}
