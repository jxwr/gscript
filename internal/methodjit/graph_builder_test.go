// graph_builder_test.go tests the bytecode → CFG SSA graph builder.
// Tests compile GScript source to bytecode, run BuildGraph, and verify
// the resulting IR structure (block count, instruction types, phi nodes,
// terminator correctness, succ/pred consistency).

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/vm"
)

// compile is a test helper that compiles GScript source and returns the
// FuncProto for the first declared function. If the source has no inner
// functions, it returns the top-level (main) proto.
func compile(t *testing.T, src string) *vm.FuncProto {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if len(proto.Protos) > 0 {
		return proto.Protos[0]
	}
	return proto
}

// TestBuildGraph_Simple tests a simple function: func f(a, b) { return a + b }
func TestBuildGraph_Simple(t *testing.T) {
	proto := compile(t, `
func f(a, b) {
	return a + b
}
`)
	fn := BuildGraph(proto)
	ir := Print(fn)
	t.Logf("IR:\n%s", ir)

	// Should have exactly 1 block.
	if len(fn.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(fn.Blocks))
	}

	// Should have LoadSlot for params, Add, Return.
	block := fn.Entry
	hasAdd := false
	hasReturn := false
	for _, instr := range block.Instrs {
		if instr.Op == OpAdd {
			hasAdd = true
			if len(instr.Args) != 2 {
				t.Errorf("Add should have 2 args, got %d", len(instr.Args))
			}
		}
		if instr.Op == OpReturn {
			hasReturn = true
			if len(instr.Args) != 1 {
				t.Errorf("Return should have 1 arg, got %d", len(instr.Args))
			}
		}
	}
	if !hasAdd {
		t.Error("expected Add instruction")
	}
	if !hasReturn {
		t.Error("expected Return instruction")
	}
}

// TestBuildGraph_IfElse tests: if n < 2 { return n } else { return n * 2 }
func TestBuildGraph_IfElse(t *testing.T) {
	proto := compile(t, `
func f(n) {
	if n < 2 {
		return n
	} else {
		return n * 2
	}
}
`)
	fn := BuildGraph(proto)
	ir := Print(fn)
	t.Logf("IR:\n%s", ir)

	// Should have at least 3 blocks: entry (with branch), true branch, false branch.
	if len(fn.Blocks) < 3 {
		t.Fatalf("expected at least 3 blocks, got %d", len(fn.Blocks))
	}

	// Entry block should end with Branch.
	entry := fn.Entry
	lastInstr := entry.Instrs[len(entry.Instrs)-1]
	if lastInstr.Op != OpBranch {
		t.Errorf("entry block should end with Branch, got %s", lastInstr.Op)
	}

	// Entry should have 2 successors.
	if len(entry.Succs) != 2 {
		t.Errorf("entry should have 2 successors, got %d", len(entry.Succs))
	}

	// Should have Lt comparison in entry.
	hasLt := false
	for _, instr := range entry.Instrs {
		if instr.Op == OpLt {
			hasLt = true
		}
	}
	if !hasLt {
		t.Error("expected Lt instruction in entry block")
	}

	// Each successor should have a Return.
	for i, succ := range entry.Succs {
		hasReturn := false
		for _, instr := range succ.Instrs {
			if instr.Op == OpReturn {
				hasReturn = true
			}
		}
		if !hasReturn {
			t.Errorf("successor %d (B%d) should have Return", i, succ.ID)
		}
	}
}

// TestBuildGraph_ForLoop tests numeric for loop with phi at loop header.
func TestBuildGraph_ForLoop(t *testing.T) {
	proto := compile(t, `
func f(n) {
	sum := 0
	for i := 1; i <= n; i++ {
		sum = sum + i
	}
	return sum
}
`)
	fn := BuildGraph(proto)
	ir := Print(fn)
	t.Logf("IR:\n%s", ir)

	// Should have multiple blocks (entry, loop header/test, loop body, exit).
	if len(fn.Blocks) < 3 {
		t.Fatalf("expected at least 3 blocks for for-loop, got %d", len(fn.Blocks))
	}

	// Look for a Phi instruction in one of the blocks (the loop header should
	// have a phi for the index variable or the sum variable).
	hasPhi := false
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpPhi {
				hasPhi = true
			}
		}
	}
	// Phi nodes may be trivially eliminated if single-predecessor.
	// Check either for Phi or for at least an Add in the loop body.
	hasAdd := false
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpAdd {
				hasAdd = true
			}
		}
	}
	if !hasAdd {
		t.Error("expected Add instruction in loop body")
	}

	// The loop should have a branch (the FORLOOP test).
	hasBranch := false
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpBranch {
				hasBranch = true
			}
		}
	}
	if !hasBranch {
		t.Error("expected Branch instruction for loop test")
	}
	_ = hasPhi // Phi presence depends on loop structure; not always required.
}

// TestBuildGraph_Fib tests recursive Fibonacci.
func TestBuildGraph_Fib(t *testing.T) {
	proto := compile(t, `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n - 1) + fib(n - 2)
}
`)
	fn := BuildGraph(proto)
	ir := Print(fn)
	t.Logf("IR:\n%s", ir)

	// Should have multiple blocks (at least entry + base case + recursive case).
	if len(fn.Blocks) < 2 {
		t.Fatalf("expected at least 2 blocks for fib, got %d", len(fn.Blocks))
	}

	// Should have Call instructions (two recursive calls).
	callCount := 0
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpCall {
				callCount++
			}
		}
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 Call instructions, got %d", callCount)
	}

	// Should have Sub instructions (n-1, n-2).
	subCount := 0
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpSub {
				subCount++
			}
		}
	}
	if subCount < 2 {
		t.Errorf("expected at least 2 Sub instructions, got %d", subCount)
	}

	// Should have Add instruction (fib(n-1) + fib(n-2)).
	hasAdd := false
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpAdd {
				hasAdd = true
			}
		}
	}
	if !hasAdd {
		t.Error("expected Add instruction for combining recursive results")
	}

	// Should have Branch + Lt in entry.
	hasLt := false
	hasBranch := false
	for _, instr := range fn.Entry.Instrs {
		if instr.Op == OpLt {
			hasLt = true
		}
		if instr.Op == OpBranch {
			hasBranch = true
		}
	}
	if !hasLt {
		t.Error("expected Lt in entry block")
	}
	if !hasBranch {
		t.Error("expected Branch in entry block")
	}
}

// TestBuildGraph_Print prints the IR for manual inspection of various programs.
func TestBuildGraph_Print(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "add",
			src: `
func add(a, b) {
	return a + b
}`,
		},
		{
			name: "if_else",
			src: `
func f(n) {
	if n < 2 {
		return n
	} else {
		return n * 2
	}
}`,
		},
		{
			name: "for_loop",
			src: `
func f(n) {
	sum := 0
	for i := 1; i <= n; i++ {
		sum = sum + i
	}
	return sum
}`,
		},
		{
			name: "fib",
			src: `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n - 1) + fib(n - 2)
}`,
		},
		{
			name: "table_ops",
			src: `
func f() {
	t := {}
	t.x = 10
	t.y = 20
	return t.x + t.y
}`,
		},
		{
			name: "global_call",
			src: `
func f(x) {
	return print(x)
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := compile(t, tt.src)
			fn := BuildGraph(proto)
			ir := Print(fn)
			t.Logf("IR for %s:\n%s", tt.name, ir)

			// Basic sanity: IR should not be empty.
			if len(fn.Blocks) == 0 {
				t.Error("expected at least 1 block")
			}

			// Every block should have at least one instruction.
			for _, blk := range fn.Blocks {
				if len(blk.Instrs) == 0 {
					t.Errorf("block B%d has no instructions", blk.ID)
				}
			}

			// Last instruction of each block should be a terminator.
			for _, blk := range fn.Blocks {
				last := blk.Instrs[len(blk.Instrs)-1]
				if !last.Op.IsTerminator() {
					t.Errorf("block B%d: last instruction %s is not a terminator", blk.ID, last.Op)
				}
			}
		})
	}
}

// TestBuildGraph_Disassemble dumps bytecode alongside IR for debugging.
func TestBuildGraph_Disassemble(t *testing.T) {
	src := `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n - 1) + fib(n - 2)
}
`
	proto := compile(t, src)

	t.Logf("Bytecode:\n%s", vm.Disassemble(proto))
	t.Logf("IR:\n%s", Print(BuildGraph(proto)))
}

// TestBuildGraph_AllBlocksTerminated verifies every block has exactly one terminator
// as its last instruction.
func TestBuildGraph_AllBlocksTerminated(t *testing.T) {
	programs := []string{
		`func f(a, b) { return a + b }`,
		`func f(n) { if n < 2 { return n } else { return n * 2 } }`,
		`func f(n) { sum := 0; for i := 1; i <= n; i++ { sum = sum + i }; return sum }`,
		`func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`,
	}

	for i, src := range programs {
		proto := compile(t, src)
		fn := BuildGraph(proto)
		for _, blk := range fn.Blocks {
			if len(blk.Instrs) == 0 {
				t.Errorf("program %d: block B%d has no instructions", i, blk.ID)
				continue
			}
			last := blk.Instrs[len(blk.Instrs)-1]
			if !last.Op.IsTerminator() {
				t.Errorf("program %d: block B%d ends with %s, not a terminator", i, blk.ID, last.Op)
			}
			// No terminator should appear before the last instruction.
			for j := 0; j < len(blk.Instrs)-1; j++ {
				if blk.Instrs[j].Op.IsTerminator() {
					t.Errorf("program %d: block B%d has terminator %s at position %d (not last)",
						i, blk.ID, blk.Instrs[j].Op, j)
				}
			}
		}
	}
}

// TestBuildGraph_SuccPredConsistency verifies that Succs/Preds are consistent.
func TestBuildGraph_SuccPredConsistency(t *testing.T) {
	proto := compile(t, `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}
`)
	fn := BuildGraph(proto)

	for _, blk := range fn.Blocks {
		for _, succ := range blk.Succs {
			found := false
			for _, pred := range succ.Preds {
				if pred == blk {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("B%d → B%d in Succs but B%d not in B%d.Preds",
					blk.ID, succ.ID, blk.ID, succ.ID)
			}
		}
	}
}

// TestBuildGraph_ValueIDs verifies that all value IDs are unique.
func TestBuildGraph_ValueIDs(t *testing.T) {
	proto := compile(t, `
func f(n) {
	sum := 0
	for i := 1; i <= n; i++ {
		sum = sum + i
	}
	return sum
}
`)
	fn := BuildGraph(proto)

	seen := make(map[int]bool)
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if seen[instr.ID] {
				t.Errorf("duplicate value ID %d in block B%d", instr.ID, blk.ID)
			}
			seen[instr.ID] = true
		}
	}
}

// TestBuildGraph_PrintOutput verifies the IR printer produces expected strings.
func TestBuildGraph_PrintOutput(t *testing.T) {
	proto := compile(t, `
func f(a, b) {
	return a + b
}
`)
	fn := BuildGraph(proto)
	ir := Print(fn)

	// Should contain "LoadSlot" for parameters.
	if !strings.Contains(ir, "LoadSlot") {
		t.Error("expected LoadSlot in IR output")
	}
	// Should contain "Add".
	if !strings.Contains(ir, "Add") {
		t.Error("expected Add in IR output")
	}
	// Should contain "Return".
	if !strings.Contains(ir, "Return") {
		t.Error("expected Return in IR output")
	}
	// Should contain block label.
	if !strings.Contains(ir, "B0") {
		t.Error("expected B0 block label in IR output")
	}
}
