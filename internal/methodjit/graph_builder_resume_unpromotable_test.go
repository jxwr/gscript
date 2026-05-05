//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// Source containing a function that calls coroutine.resume in a hot loop.
// The compiler lowers coroutine.resume / coroutine.yield to dedicated
// OP_RESUME / OP_YIELD bytecodes; without an explicit case in the Tier 2
// graph builder, the destination register is never written, which corrupts
// downstream IR and trips the validator. OP_RESUME is now modeled as OpResume;
// OP_YIELD remains unpromotable until it has continuation-aware lowering.
const resumeIRSrc = `
func make_producer(n) {
    return coroutine.create(func() {
        for i := 1; i <= n; i++ {
            coroutine.yield(i)
        }
    })
}

func consume(co, n) {
    sum := 0
    for i := 0; i < n; i++ {
        ok, v := coroutine.resume(co)
        if ok {
            sum = sum + v
        }
    }
    return sum
}

co := make_producer(1000)
result := consume(co, 1000)
`

func TestTier2ResumeBuildsOpResumeIR(t *testing.T) {
	proto := compileProto(t, resumeIRSrc)
	consume := findProtoByName(proto, "consume")
	if consume == nil {
		t.Fatal("consume proto not found")
	}

	// Sanity: the bytecode actually contains OP_RESUME.
	hasResume := false
	for _, inst := range consume.Code {
		if vm.DecodeOp(inst) == vm.OP_RESUME {
			hasResume = true
			break
		}
	}
	if !hasResume {
		t.Fatal("expected consume to contain OP_RESUME bytecode")
	}

	fn := BuildGraph(consume)
	if fn == nil {
		t.Fatal("BuildGraph returned nil")
	}
	if fn.Unpromotable {
		t.Fatal("OP_RESUME consumer should be Tier2-promotable")
	}
	if got := countOpHelper(fn, OpResume); got != 1 {
		t.Fatalf("OpResume count = %d, want 1", got)
	}
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("OpResume IR failed validation: %v", errs)
	}

	optimized, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("RunTier2Pipeline with OpResume: %v", err)
	}
	if got := countOpHelper(optimized, OpResume); got != 1 {
		t.Fatalf("optimized OpResume count = %d, want 1", got)
	}
}

func TestTier2YieldMarkedUnpromotable(t *testing.T) {
	proto := compileProto(t, resumeIRSrc)
	yielding := findAnonymousProtoWithOpcode(proto, vm.OP_YIELD)
	if yielding == nil {
		t.Fatal("yielding proto not found")
	}
	fn := BuildGraph(yielding)
	if fn == nil {
		t.Fatal("BuildGraph returned nil")
	}
	if !fn.Unpromotable {
		t.Fatal("expected fn.Unpromotable=true for proto containing OP_YIELD")
	}
}

func findAnonymousProtoWithOpcode(proto *vm.FuncProto, op vm.Opcode) *vm.FuncProto {
	if proto == nil {
		return nil
	}
	for _, child := range proto.Protos {
		for _, inst := range child.Code {
			if vm.DecodeOp(inst) == op {
				return child
			}
		}
		if got := findAnonymousProtoWithOpcode(child, op); got != nil {
			return got
		}
	}
	return nil
}
