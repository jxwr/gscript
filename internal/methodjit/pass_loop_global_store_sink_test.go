//go:build darwin && arm64

package methodjit

import "testing"

func TestLoopGlobalStoreSink_SinksInvariantStore(t *testing.T) {
	top := compileTop(t, `
x := 42
result := 0
for i := 1; i <= 3; i++ {
    result = x
}
`)
	remarks := &OptimizationRemarks{}
	fn := BuildGraph(top)
	fn.Remarks = remarks
	if _, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{}); err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	if !hasRemark(remarks, "LoopGlobalStoreSink", "changed") {
		t.Fatalf("expected LoopGlobalStoreSink remark, got:\n%s", formatOptimizationRemarks(remarks.List()))
	}
}
