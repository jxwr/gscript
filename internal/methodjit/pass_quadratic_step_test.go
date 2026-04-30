package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestQuadraticStepStrengthReduction_AfterPureHelperInline(t *testing.T) {
	src := `
func A(i, j) {
	return 1.0 / ((i+j)*(i+j+1)/2 + i + 1)
}
func f(n, row) {
	sum := 0.0
	for j := 0; j < n; j++ {
		sum = sum + A(row, j)
	}
	return sum
}
`
	top := compileTop(t, src)
	globals := make(map[string]*vm.FuncProto)
	var f *vm.FuncProto
	for _, p := range top.Protos {
		globals[p.Name] = p
		if p.Name == "f" {
			f = p
		}
	}
	if f == nil {
		t.Fatal("missing f proto")
	}

	remarks := &OptimizationRemarks{}
	out, _, err := RunTier2Pipeline(BuildGraph(f), &Tier2PipelineOpts{
		InlineGlobals: globals,
		InlineMaxSize: inlineMaxCalleeSize,
		Remarks:       remarks,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	if !hasRemark(remarks, "QuadraticStepStrengthReduction", "changed") {
		t.Fatalf("expected quadratic step strength reduction\nIR:\n%s\nremarks:\n%s",
			Print(out), formatOptimizationRemarks(remarks.List()))
	}

	for _, n := range []int64{0, 1, 2, 3, 8, 9} {
		args := []runtime.Value{runtime.IntValue(n), runtime.IntValue(4)}
		got, err := Interpret(out, args)
		if err != nil {
			t.Fatalf("Interpret f(%d,4): %v\nIR:\n%s", n, err, Print(out))
		}
		want := runVMFunc(t, src, "f", args)
		assertValuesEqual(t, "f", got[0], want[0])
	}
}

func hasRemark(remarks *OptimizationRemarks, pass, kind string) bool {
	for _, r := range remarks.List() {
		if r.Pass == pass && r.Kind == kind {
			return true
		}
	}
	return false
}
