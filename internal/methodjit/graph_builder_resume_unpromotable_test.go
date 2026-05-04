//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// Source containing a function that calls coroutine.resume in a hot loop.
// The compiler lowers coroutine.resume / coroutine.yield to dedicated
// OP_RESUME / OP_YIELD bytecodes; without an explicit case in the Tier 2
// graph builder, the destination register is never written, which corrupts
// downstream IR and trips the validator. The fix marks the proto Unpromotable.
const resumeUnpromotableSrc = `
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

func TestTier2ResumeYieldMarkedUnpromotable(t *testing.T) {
	proto := compileProto(t, resumeUnpromotableSrc)
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

	// BuildGraph must mark the function unpromotable rather than emitting
	// a corrupt IR that downstream passes / Validate() will fail on.
	fn := BuildGraph(consume)
	if fn == nil {
		t.Fatal("BuildGraph returned nil")
	}
	if !fn.Unpromotable {
		t.Fatalf("expected fn.Unpromotable=true for proto containing OP_RESUME, got false")
	}

	// CompileTier2 must cleanly refuse the proto via the Unpromotable gate
	// (returning the standard "unmodeled bytecode" error string), not via a
	// validator failure or panic.
	tm := NewTieringManager()
	err := tm.CompileTier2(consume)
	if err == nil {
		t.Fatal("expected CompileTier2 to refuse proto with OP_RESUME, got nil error")
	}
	if strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("CompileTier2 should fail via Unpromotable gate, not validator: %v", err)
	}
	if !strings.Contains(err.Error(), "unmodeled bytecode") {
		t.Fatalf("expected unmodeled-bytecode skip reason, got: %v", err)
	}
}
