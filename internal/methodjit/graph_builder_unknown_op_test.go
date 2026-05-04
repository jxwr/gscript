//go:build darwin && arm64

// graph_builder_unknown_op_test.go pins the default-case behavior of
// the Tier 2 graph builder's bytecode switch. Any opcode that lacks an
// explicit case must flip fn.Unpromotable so the function falls back
// to Tier 1 cleanly instead of silently emitting a Nop and corrupting
// downstream SSA register liveness (the failure mode that produced the
// "Jump in middle of block" bug behind commit deebbcf).

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestUnknownOpcodeMarksUnpromotable constructs a synthetic FuncProto
// containing OP_MAX (the sentinel — never a real op, guaranteed to hit
// the default branch of the graph builder switch) and verifies that
// BuildGraph marks the function unpromotable rather than producing
// a graph the Tier 2 pipeline would have to reject downstream.
func TestUnknownOpcodeMarksUnpromotable(t *testing.T) {
	proto := &vm.FuncProto{
		Name:      "unknown_op_synthetic",
		NumParams: 0,
		MaxStack:  1,
		Code: []uint32{
			vm.EncodeABC(vm.OP_MAX, 0, 0, 0),
			vm.EncodeABC(vm.OP_RETURN, 0, 1, 0),
		},
	}

	fn := BuildGraph(proto)
	if fn == nil {
		t.Fatal("BuildGraph returned nil")
	}
	if !fn.Unpromotable {
		t.Fatalf("expected Unpromotable=true after default-case opcode, got false")
	}
}
