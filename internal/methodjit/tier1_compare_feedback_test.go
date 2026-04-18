//go:build darwin && arm64

// tier1_compare_feedback_test.go — R85 Option 2 verification.
//
// Tier 1 emitBaselineEQ/LT/LE now writes Left/Right feedback so that
// when the interpreter is bypassed (BaselineCompileThreshold=1),
// Tier 2 still sees populated feedback and graph_builder's
// FBInt/FBFloat GuardType path (R82 L2) can fire.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestTier1_Feedback_OpLE_Int runs a function whose hot loop is an
// integer `a <= b` compare at Tier 1, then verifies that after
// execution Feedback[pc].Left == FBInt and Right == FBInt on the
// comparison PC.
func TestTier1_Feedback_OpLE_Int(t *testing.T) {
	t.Skip("R85: Option 2 feedback emission was implemented but reverted — +5-12% regression on sort/math_intensive/sieve/fannkuch/mandelbrot exceeds 2% ceiling. Test kept as design record. See rounds/R085.yaml.")
	src := `
func check(x) {
    if x <= 100 {
        return 1
    }
    return 0
}
result := 0
for i := 1; i <= 200; i++ { result = check(i) }
`
	topProto := compileProto(t, src)
	if len(topProto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	inner := topProto.Protos[0]
	inner.EnsureFeedback()

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(topProto); err != nil {
		t.Fatalf("execute error: %v", err)
	}

	lePC := -1
	for pc, inst := range inner.Code {
		if vm.DecodeOp(inst) == vm.OP_LE {
			lePC = pc
			break
		}
	}
	if lePC < 0 {
		t.Fatal("OP_LE not found in inner proto")
	}

	fb := inner.Feedback[lePC]
	t.Logf("OP_LE pc=%d Left=%v Right=%v Result=%v", lePC, fb.Left, fb.Right, fb.Result)
	if fb.Left != vm.FBInt {
		t.Errorf("OP_LE Feedback.Left = %v, want FBInt", fb.Left)
	}
	if fb.Right != vm.FBInt {
		t.Errorf("OP_LE Feedback.Right = %v, want FBInt", fb.Right)
	}
}

// TestTier1_Feedback_OpLT_Float verifies float operand type detection.
func TestTier1_Feedback_OpLT_Float(t *testing.T) {
	t.Skip("R85 reverted (see TestTier1_Feedback_OpLE_Int). Design record.")
	src := `
func check(x) {
    if x < 3.5 {
        return 1
    }
    return 0
}
result := 0
for i := 1; i <= 200; i++ { result = check(i + 0.5) }
`
	topProto := compileProto(t, src)
	if len(topProto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	inner := topProto.Protos[0]
	inner.EnsureFeedback()

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(topProto); err != nil {
		t.Fatalf("execute error: %v", err)
	}

	ltPC := -1
	for pc, inst := range inner.Code {
		if vm.DecodeOp(inst) == vm.OP_LT {
			ltPC = pc
			break
		}
	}
	if ltPC < 0 {
		t.Fatal("OP_LT not found in inner proto")
	}

	fb := inner.Feedback[ltPC]
	t.Logf("OP_LT pc=%d Left=%v Right=%v Result=%v", ltPC, fb.Left, fb.Right, fb.Result)
	if fb.Left != vm.FBFloat {
		t.Errorf("OP_LT Feedback.Left = %v, want FBFloat", fb.Left)
	}
	if fb.Right != vm.FBFloat {
		t.Errorf("OP_LT Feedback.Right = %v, want FBFloat", fb.Right)
	}
}
