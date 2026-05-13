//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTier2SpecDependencyQueuesCompiledCallerOnCalleeCompile(t *testing.T) {
	tm := NewTieringManager()
	caller := &vm.FuncProto{Name: "caller"}
	callee := &vm.FuncProto{Name: "callee"}
	callerCF := &CompiledFunction{
		Proto:                caller,
		SpecDependencyProtos: []*vm.FuncProto{callee},
	}
	calleeCF := &CompiledFunction{Proto: callee}

	tm.markTier2Compiled(caller, callerCF)
	if _, ok := tm.recompileQueue.take(caller); ok {
		t.Fatalf("caller should not be queued until a dependency changes")
	}

	tm.markTier2Compiled(callee, calleeCF)
	req, ok := tm.recompileQueue.take(caller)
	if !ok {
		t.Fatalf("caller was not queued after spec dependency compiled")
	}
	if req.Reason != "spec_dependency_compiled" {
		t.Fatalf("reason=%q, want spec_dependency_compiled", req.Reason)
	}
	if _, ok := tm.tier2CompiledFor(caller); ok {
		t.Fatalf("queued caller install should be cleared")
	}
}

func TestTier2SpecDependencyIgnoresUncompiledCaller(t *testing.T) {
	tm := NewTieringManager()
	caller := &vm.FuncProto{Name: "caller"}
	callee := &vm.FuncProto{Name: "callee"}
	tm.specDependents[callee] = map[*vm.FuncProto]bool{caller: true}

	tm.queueSpecDependentsForRefresh(callee, "spec_dependency_feedback_matured")
	if _, ok := tm.recompileQueue.take(caller); ok {
		t.Fatalf("uncompiled caller should not be queued")
	}
}

func TestSortedSpecDependencyProtosDropsSelfAndOrdersByName(t *testing.T) {
	self := &vm.FuncProto{Name: "self"}
	a := &vm.FuncProto{Name: "a"}
	b := &vm.FuncProto{Name: "b"}
	c := &vm.FuncProto{Name: "c"}
	fn := &Function{
		Proto: self,
		SpecDependencyProtos: map[*vm.FuncProto]bool{
			b:    true,
			self: true,
			a:    true,
		},
		FieldPolyShapeFacts: map[int][]FieldPolyShapeCase{
			10: {{VMProto: c}},
		},
	}

	got := sortedSpecDependencyProtos(fn)
	if len(got) != 3 || got[0] != a || got[1] != b || got[2] != c {
		t.Fatalf("dependencies=%v, want [a b c] without self", got)
	}
}
