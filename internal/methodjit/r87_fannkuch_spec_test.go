//go:build darwin && arm64

// r87_aux2_probe_test.go — R87 diagnostic.
//
// Documents R87's actual behavior: after TypeSpec infers GetTable's result
// type from Aux2 Kind feedback, downstream Le/Lt/Eq DO get rewritten to
// *Int/*Float variants when operands trace to GetTable. But the emit
// dispatch (emit_dispatch.go:132-137) maps {OpLe, OpLeInt} to the same
// emitIntCmp helper, so no wall-time difference results. R87 is
// correctness infrastructure; a follow-up round must add a genuine
// fast-path for OpLeInt to realize perf gains.

package methodjit

import (
	"os"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestR87_Fannkuch_SpecializationFires verifies that fannkuch's int-array
// GetTable compares become OpLtInt/OpEqInt/OpLeInt after TypeSpec.
// Requires real execution path (not diag) so Feedback.Kind gets populated.
func TestR87_Fannkuch_SpecializationFires(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/fannkuch.gs")
	if err != nil {
		t.Fatalf("read fannkuch.gs: %v", err)
	}
	topProto := compileProto(t, string(srcBytes))

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(topProto); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var p *vm.FuncProto
	for _, candidate := range topProto.Protos {
		if candidate.Name == "fannkuch" {
			p = candidate
			break
		}
	}
	if p == nil {
		t.Fatal("fannkuch proto not found")
	}

	// All OP_GETTABLE in fannkuch are on int arrays — expect Kind=FBKindInt.
	for pc, inst := range p.Code {
		if vm.DecodeOp(inst) != vm.OP_GETTABLE {
			continue
		}
		if p.Feedback[pc].Kind != vm.FBKindInt {
			t.Errorf("pc=%d: expected Kind=FBKindInt(2), got %d", pc, p.Feedback[pc].Kind)
		}
	}

	fn := BuildGraph(p)
	optFn, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass: %v", err)
	}

	var intSpecCount int
	var genericCmpCount int
	var spec []string
	for _, blk := range optFn.Blocks {
		for _, instr := range blk.Instrs {
			switch instr.Op {
			case OpLeInt, OpLtInt, OpEqInt:
				intSpecCount++
				spec = append(spec, instr.Op.String())
			case OpLe, OpLt, OpEq:
				genericCmpCount++
			}
		}
	}
	t.Logf("int-specialized compares: %d (%s)", intSpecCount, strings.Join(spec, ", "))
	t.Logf("generic compares: %d", genericCmpCount)
	if intSpecCount < 5 {
		t.Errorf("expected ≥5 int-specialized compares from R87, got %d", intSpecCount)
	}
}
