// pipeline_test.go tests the optimization pass pipeline framework.
// Tests pass registration, ordering, enable/disable, execution, and dumping.

package methodjit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// makeTestFn creates a minimal Function for pipeline tests.
// It has one block with a single ConstInt instruction and a Return terminator.
func makeTestFn(name string) *Function {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: name},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	ci := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 42, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{ci.Value()}, Block: b}
	b.Instrs = []*Instr{ci, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	return fn
}

// TestPipeline_Empty verifies that an empty pipeline runs without error
// and returns the same function pointer.
func TestPipeline_Empty(t *testing.T) {
	p := NewPipeline()
	fn := makeTestFn("empty")
	result, err := p.Run(fn)
	if err != nil {
		t.Fatalf("empty pipeline returned error: %v", err)
	}
	if result != fn {
		t.Fatal("empty pipeline should return same function pointer")
	}
}

// TestPipeline_SinglePass registers one pass, runs it, and verifies it was called.
func TestPipeline_SinglePass(t *testing.T) {
	called := false
	p := NewPipeline()
	p.Add("testPass", func(fn *Function) (*Function, error) {
		called = true
		return fn, nil
	})
	fn := makeTestFn("single")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("pass was not called")
	}
}

// TestPipeline_MultiplePassesOrder registers 3 passes and verifies execution order.
func TestPipeline_MultiplePassesOrder(t *testing.T) {
	var order []string
	p := NewPipeline()
	for _, name := range []string{"A", "B", "C"} {
		n := name // capture
		p.Add(n, func(fn *Function) (*Function, error) {
			order = append(order, n)
			return fn, nil
		})
	}
	fn := makeTestFn("order")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(order))
	}
	expected := "A,B,C"
	got := strings.Join(order, ",")
	if got != expected {
		t.Fatalf("execution order: got %q, want %q", got, expected)
	}
}

// TestPipeline_DisablePass disables a pass by name and verifies it's skipped.
func TestPipeline_DisablePass(t *testing.T) {
	var order []string
	p := NewPipeline()
	for _, name := range []string{"A", "B", "C"} {
		n := name
		p.Add(n, func(fn *Function) (*Function, error) {
			order = append(order, n)
			return fn, nil
		})
	}
	p.Disable("B")
	fn := makeTestFn("disable")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "A,C"
	got := strings.Join(order, ",")
	if got != expected {
		t.Fatalf("execution order: got %q, want %q", got, expected)
	}
}

// TestPipeline_EnableDisable tests the enable/disable toggle.
func TestPipeline_EnableDisable(t *testing.T) {
	var order []string
	p := NewPipeline()
	for _, name := range []string{"A", "B"} {
		n := name
		p.Add(n, func(fn *Function) (*Function, error) {
			order = append(order, n)
			return fn, nil
		})
	}

	// Disable then re-enable B.
	p.Disable("B")
	p.Enable("B")

	fn := makeTestFn("toggle")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "A,B"
	got := strings.Join(order, ",")
	if got != expected {
		t.Fatalf("execution order: got %q, want %q", got, expected)
	}
}

// TestPipeline_PassModifiesIR tests a pass that adds a NOP instruction.
func TestPipeline_PassModifiesIR(t *testing.T) {
	addNop := func(fn *Function) (*Function, error) {
		// Insert a NOP before the terminator in the entry block.
		b := fn.Entry
		nop := &Instr{ID: fn.newValueID(), Op: OpNop, Block: b}
		// Insert before last instruction (the terminator).
		newInstrs := make([]*Instr, 0, len(b.Instrs)+1)
		newInstrs = append(newInstrs, b.Instrs[:len(b.Instrs)-1]...)
		newInstrs = append(newInstrs, nop)
		newInstrs = append(newInstrs, b.Instrs[len(b.Instrs)-1])
		b.Instrs = newInstrs
		return fn, nil
	}

	p := NewPipeline()
	p.Add("addNop", addNop)
	fn := makeTestFn("nop")
	origLen := len(fn.Entry.Instrs) // 2: ConstInt + Return

	result, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Entry.Instrs) != origLen+1 {
		t.Fatalf("expected %d instrs, got %d", origLen+1, len(result.Entry.Instrs))
	}
	// The NOP should be the second-to-last instruction.
	nopInstr := result.Entry.Instrs[len(result.Entry.Instrs)-2]
	if nopInstr.Op != OpNop {
		t.Fatalf("expected OpNop, got %v", nopInstr.Op)
	}
}

// TestPipeline_DumpBetweenPasses enables dumping and verifies snapshots are recorded.
func TestPipeline_DumpBetweenPasses(t *testing.T) {
	identity := func(fn *Function) (*Function, error) { return fn, nil }

	p := NewPipeline()
	p.Add("passA", identity)
	p.Add("passB", identity)
	p.EnableDump(true)

	fn := makeTestFn("dump")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dump := p.Dump()
	t.Logf("Dump:\n%s", dump)

	// Should have 3 snapshots: "input", "passA", "passB"
	if !strings.Contains(dump, "=== input ===") {
		t.Fatal("dump missing 'input' snapshot")
	}
	if !strings.Contains(dump, "=== passA ===") {
		t.Fatal("dump missing 'passA' snapshot")
	}
	if !strings.Contains(dump, "=== passB ===") {
		t.Fatal("dump missing 'passB' snapshot")
	}
}

// TestPipeline_DiffBetweenPasses tests diff output between two pipeline stages.
func TestPipeline_DiffBetweenPasses(t *testing.T) {
	addNop := func(fn *Function) (*Function, error) {
		b := fn.Entry
		nop := &Instr{ID: fn.newValueID(), Op: OpNop, Block: b}
		newInstrs := make([]*Instr, 0, len(b.Instrs)+1)
		newInstrs = append(newInstrs, b.Instrs[:len(b.Instrs)-1]...)
		newInstrs = append(newInstrs, nop)
		newInstrs = append(newInstrs, b.Instrs[len(b.Instrs)-1])
		b.Instrs = newInstrs
		return fn, nil
	}

	p := NewPipeline()
	p.Add("addNop", addNop)
	p.EnableDump(true)

	fn := makeTestFn("diff")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	diff := p.Diff("input", "addNop")
	t.Logf("Diff:\n%s", diff)

	// The diff should show at least one added line (the NOP).
	if !strings.Contains(diff, "+") {
		t.Fatal("diff should contain added lines marked with '+'")
	}
	if !strings.Contains(diff, "Nop") {
		t.Fatal("diff should mention the Nop instruction")
	}
}

// TestPipeline_PassReturnsError tests that an error from a pass stops the pipeline.
func TestPipeline_PassReturnsError(t *testing.T) {
	var order []string
	p := NewPipeline()
	p.Add("A", func(fn *Function) (*Function, error) {
		order = append(order, "A")
		return fn, nil
	})
	p.Add("failing", func(fn *Function) (*Function, error) {
		return nil, fmt.Errorf("something went wrong")
	})
	p.Add("C", func(fn *Function) (*Function, error) {
		order = append(order, "C")
		return fn, nil
	})

	fn := makeTestFn("error")
	_, err := p.Run(fn)
	if err == nil {
		t.Fatal("expected error from pipeline")
	}
	if !strings.Contains(err.Error(), "failing") {
		t.Fatalf("error should mention pass name 'failing', got: %v", err)
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Fatalf("error should contain original message, got: %v", err)
	}
	// Pass C should not have run.
	for _, name := range order {
		if name == "C" {
			t.Fatal("pass C should not have run after error")
		}
	}
}

// TestPipeline_ValidateAfterEachPass tests that a validator runs after each pass.
func TestPipeline_ValidateAfterEachPass(t *testing.T) {
	var validationCount int
	p := NewPipeline()
	p.Add("A", func(fn *Function) (*Function, error) { return fn, nil })
	p.Add("B", func(fn *Function) (*Function, error) { return fn, nil })
	p.SetValidator(func(fn *Function) []error {
		validationCount++
		return nil // no errors
	})

	fn := makeTestFn("validate")
	_, err := p.Run(fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Validator should run once per pass (2 passes).
	if validationCount != 2 {
		t.Fatalf("expected 2 validator calls, got %d", validationCount)
	}
}

// TestPipeline_ValidatorReturnsErrors tests that validator errors stop the pipeline.
func TestPipeline_ValidatorReturnsErrors(t *testing.T) {
	p := NewPipeline()
	p.Add("breakIR", func(fn *Function) (*Function, error) { return fn, nil })
	p.Add("shouldNotRun", func(fn *Function) (*Function, error) {
		t.Fatal("this pass should not run")
		return fn, nil
	})
	p.SetValidator(func(fn *Function) []error {
		return []error{fmt.Errorf("block B0 has no terminator")}
	})

	fn := makeTestFn("validate-fail")
	_, err := p.Run(fn)
	if err == nil {
		t.Fatal("expected error from validator")
	}
	if !strings.Contains(err.Error(), "breakIR") {
		t.Fatalf("error should mention pass name 'breakIR', got: %v", err)
	}
	if !strings.Contains(err.Error(), "no terminator") {
		t.Fatalf("error should contain validator message, got: %v", err)
	}
}
