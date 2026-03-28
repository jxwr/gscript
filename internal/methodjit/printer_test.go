// printer_test.go tests the IR printer for correctness and stability.

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestPrint_Empty(t *testing.T) {
	fn := &Function{
		Entry:  &Block{ID: 0, Instrs: []*Instr{{ID: 0, Op: OpReturn, Block: &Block{ID: 0}}}},
		Blocks: []*Block{{ID: 0}},
		Proto:  &vm.FuncProto{Name: "empty"},
	}
	fn.Blocks[0].Instrs = fn.Entry.Instrs
	fn.Blocks[0].Instrs[0].Block = fn.Blocks[0]
	out := Print(fn)
	if !strings.Contains(out, "B0") {
		t.Errorf("expected B0 in output, got: %s", out)
	}
	if !strings.Contains(out, "Return") {
		t.Errorf("expected Return in output, got: %s", out)
	}
}

func TestPrint_FibStructure(t *testing.T) {
	// Build fib graph and verify printer output has expected structure
	src := `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	proto := compile(t, src)
	fn := BuildGraph(proto)
	out := Print(fn)

	// Must contain entry block, branch, return, call
	for _, want := range []string{"B0 (entry)", "Branch", "Return", "Call", "Sub", "Add", "Lt"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in printer output", want)
		}
	}
}
