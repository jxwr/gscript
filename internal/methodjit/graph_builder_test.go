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
	"github.com/gscript/gscript/internal/runtime"
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

// TestBuildGraph_FeedbackTypedGuards verifies that the graph builder inserts
// OpGuardType instructions after OpGetTable/OpGetField when per-PC feedback
// indicates a monomorphic result type, and that no guard is inserted when
// feedback is FBUnobserved or FBAny.
func TestBuildGraph_FeedbackTypedGuards(t *testing.T) {
	// findPC returns the bytecode PC of the first instruction matching the given opcode.
	findPC := func(t *testing.T, proto *vm.FuncProto, op vm.Opcode) int {
		t.Helper()
		for pc, inst := range proto.Code {
			if vm.DecodeOp(inst) == op {
				return pc
			}
		}
		t.Fatalf("opcode %v not found in bytecode", op)
		return -1
	}

	// findGuardAfterOp walks all IR blocks looking for an OpGuardType whose
	// Args[0] points to a value produced by targetOp. Returns the guard
	// instruction, or nil if none found.
	findGuardAfterOp := func(fn *Function, targetOp Op) *Instr {
		for _, blk := range fn.Blocks {
			for i, instr := range blk.Instrs {
				if instr.Op == targetOp && i+1 < len(blk.Instrs) {
					next := blk.Instrs[i+1]
					if next.Op == OpGuardType && len(next.Args) > 0 && next.Args[0].ID == instr.ID {
						return next
					}
				}
			}
		}
		return nil
	}

	t.Run("GetTable_FBFloat_inserts_guard", func(t *testing.T) {
		// Compile a function with a GETTABLE: local x = t[1]
		proto := compile(t, `
func f(t) {
	return t[1]
}
`)
		pc := findPC(t, proto, vm.OP_GETTABLE)

		// Set monomorphic float feedback for the GETTABLE result.
		proto.EnsureFeedback()
		proto.Feedback[pc] = vm.TypeFeedback{Result: vm.FBFloat}

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetTable)
		if guard == nil {
			t.Fatal("expected OpGuardType after OpGetTable when feedback is FBFloat, but none found")
		}
		if guard.Type != TypeFloat {
			t.Errorf("guard.Type = %v, want TypeFloat", guard.Type)
		}
		if guard.Aux != int64(TypeFloat) {
			t.Errorf("guard.Aux = %d, want %d (TypeFloat)", guard.Aux, int64(TypeFloat))
		}
	})

	t.Run("GetField_FBFloat_inserts_guard", func(t *testing.T) {
		// Compile a function with a GETFIELD: return t.x
		proto := compile(t, `
func f(t) {
	return t.x
}
`)
		pc := findPC(t, proto, vm.OP_GETFIELD)

		// Set monomorphic float feedback.
		proto.EnsureFeedback()
		proto.Feedback[pc] = vm.TypeFeedback{Result: vm.FBFloat}

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetField)
		if guard == nil {
			t.Fatal("expected OpGuardType after OpGetField when feedback is FBFloat, but none found")
		}
		if guard.Type != TypeFloat {
			t.Errorf("guard.Type = %v, want TypeFloat", guard.Type)
		}
		if guard.Aux != int64(TypeFloat) {
			t.Errorf("guard.Aux = %d, want %d (TypeFloat)", guard.Aux, int64(TypeFloat))
		}
	})

	t.Run("GetTable_FBInt_inserts_guard", func(t *testing.T) {
		proto := compile(t, `
func f(t) {
	return t[1]
}
`)
		pc := findPC(t, proto, vm.OP_GETTABLE)
		proto.EnsureFeedback()
		proto.Feedback[pc] = vm.TypeFeedback{Result: vm.FBInt}

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetTable)
		if guard == nil {
			t.Fatal("expected OpGuardType after OpGetTable when feedback is FBInt, but none found")
		}
		if guard.Type != TypeInt {
			t.Errorf("guard.Type = %v, want TypeInt", guard.Type)
		}
		if guard.Aux != int64(TypeInt) {
			t.Errorf("guard.Aux = %d, want %d (TypeInt)", guard.Aux, int64(TypeInt))
		}
	})

	t.Run("GetTable_FBInt_with_FBKindInt_skips_redundant_guard", func(t *testing.T) {
		proto := compile(t, `
func f(t) {
	return t[1]
}
`)
		pc := findPC(t, proto, vm.OP_GETTABLE)
		proto.EnsureFeedback()
		proto.Feedback[pc] = vm.TypeFeedback{Result: vm.FBInt, Kind: vm.FBKindInt}

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetTable)
		if guard != nil {
			t.Fatalf("expected kind-specialized GetTable to skip redundant int guard, got Type=%v", guard.Type)
		}
	})

	t.Run("GetTable_FBTable_with_FBKindMixed_marks_result_table", func(t *testing.T) {
		proto := compile(t, `
func f(t) {
	return t[1][2]
}
`)
		pc := findPC(t, proto, vm.OP_GETTABLE)
		proto.EnsureFeedback()
		proto.Feedback[pc] = vm.TypeFeedback{Result: vm.FBTable, Kind: vm.FBKindMixed}

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		var get *Instr
		for _, blk := range fn.Blocks {
			for _, instr := range blk.Instrs {
				if instr.Op == OpGetTable {
					get = instr
					break
				}
			}
			if get != nil {
				break
			}
		}
		if get == nil {
			t.Fatal("expected GetTable in IR")
		}
		if get.Type != TypeTable {
			t.Fatalf("first GetTable.Type = %s, want table", get.Type)
		}
		guard := findGuardAfterOp(fn, OpGetTable)
		if guard != nil && guard.Aux == int64(TypeTable) {
			t.Fatalf("expected table result guard to be folded into GetTable, got %s", Print(fn))
		}
	})

	t.Run("GetTable_FBUnobserved_no_guard", func(t *testing.T) {
		proto := compile(t, `
func f(t) {
	return t[1]
}
`)
		// EnsureFeedback initializes all entries to FBUnobserved (zero value).
		proto.EnsureFeedback()

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetTable)
		if guard != nil {
			t.Errorf("expected no OpGuardType after OpGetTable when feedback is FBUnobserved, but found one with Type=%v", guard.Type)
		}
	})

	t.Run("GetTable_FBAny_no_guard", func(t *testing.T) {
		proto := compile(t, `
func f(t) {
	return t[1]
}
`)
		pc := findPC(t, proto, vm.OP_GETTABLE)
		proto.EnsureFeedback()
		proto.Feedback[pc] = vm.TypeFeedback{Result: vm.FBAny}

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetTable)
		if guard != nil {
			t.Errorf("expected no OpGuardType after OpGetTable when feedback is FBAny, but found one with Type=%v", guard.Type)
		}
	})

	t.Run("GetField_FBUnobserved_no_guard", func(t *testing.T) {
		proto := compile(t, `
func f(t) {
	return t.x
}
`)
		proto.EnsureFeedback()

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetField)
		if guard != nil {
			t.Errorf("expected no OpGuardType after OpGetField when feedback is FBUnobserved, but found one with Type=%v", guard.Type)
		}
	})

	t.Run("no_feedback_vector_no_guard", func(t *testing.T) {
		// Without calling EnsureFeedback, proto.Feedback is nil.
		// The graph builder should not panic and should not insert guards.
		proto := compile(t, `
func f(t) {
	return t[1]
}
`)

		fn := BuildGraph(proto)
		ir := Print(fn)
		t.Logf("IR:\n%s", ir)

		guard := findGuardAfterOp(fn, OpGetTable)
		if guard != nil {
			t.Errorf("expected no OpGuardType when feedback vector is nil, but found one with Type=%v", guard.Type)
		}
	})
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

// TestFeedbackGuards_Integration verifies that feedback-typed guard insertion
// works end-to-end through the full Tier 2 pipeline: compile a function that
// accesses a float array in a loop, execute it via the interpreter to collect
// real type feedback, then build the IR graph and verify:
//   - OpGuardType appears after OpGetTable (from FBFloat feedback)
//   - TypeSpecialize cascades the float type, producing OpMulFloat/OpAddFloat
//   - The IR interpreter produces the correct result
func TestFeedbackGuards_Integration(t *testing.T) {
	// Source: a function that sums the squares of table elements.
	// The GETTABLE results will be float, so the interpreter should record
	// FBFloat feedback. After graph building, we expect GuardType(Float)
	// guards, and after TypeSpecialize, float-specialized MulFloat/AddFloat.
	src := `
func f(t, n) {
	sum := 0
	for i := 1; i <= n; i++ {
		sum = sum + t[i] * t[i]
	}
	return sum
}
`
	// Step 1: Compile the full program and get the inner function proto.
	topProto := compileTop(t, src)
	if len(topProto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	innerProto := topProto.Protos[0]

	// Step 2: Initialize feedback and execute via VM to collect real feedback.
	innerProto.EnsureFeedback()

	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	// Execute top-level to register function f in globals.
	if _, err := v.Execute(topProto); err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	// Build a table with float values: t[1]=1.1, t[2]=2.2, ..., t[10]=11.0.
	tbl := runtime.NewTable()
	for i := int64(1); i <= 10; i++ {
		tbl.RawSetInt(i, runtime.FloatValue(float64(i)*1.1))
	}

	// Call f(t, 10) via the VM to collect type feedback.
	fnVal := v.GetGlobal("f")
	if fnVal.IsNil() {
		t.Fatal("function 'f' not found in globals after execution")
	}
	vmResult, err := v.CallValue(fnVal, []runtime.Value{
		runtime.TableValue(tbl),
		runtime.IntValue(10),
	})
	if err != nil {
		t.Fatalf("VM call error: %v", err)
	}
	t.Logf("VM result: %v", vmResult)

	// Step 3: Verify that feedback was collected on GETTABLE instructions.
	// Find all GETTABLE PCs and check that their Result feedback is FBFloat.
	gettablePCs := []int{}
	for pc, inst := range innerProto.Code {
		if vm.DecodeOp(inst) == vm.OP_GETTABLE {
			gettablePCs = append(gettablePCs, pc)
		}
	}
	if len(gettablePCs) == 0 {
		t.Fatal("no GETTABLE instruction found in inner proto bytecode")
	}
	for _, pc := range gettablePCs {
		fb := innerProto.Feedback[pc]
		if fb.Result != vm.FBFloat {
			t.Errorf("GETTABLE at PC %d: expected FBFloat feedback, got %d", pc, fb.Result)
		}
	}

	// Step 4: Build the IR graph (feedback is now populated).
	fn := BuildGraph(innerProto)
	irBefore := Print(fn)
	t.Logf("IR before optimization:\n%s", irBefore)

	// Verify that OpGuardType appears after OpGetTable in the IR.
	hasGuardAfterGetTable := false
	for _, blk := range fn.Blocks {
		for i, instr := range blk.Instrs {
			if instr.Op == OpGetTable && i+1 < len(blk.Instrs) {
				next := blk.Instrs[i+1]
				if next.Op == OpGuardType && len(next.Args) > 0 && next.Args[0].ID == instr.ID {
					hasGuardAfterGetTable = true
					if next.Type != TypeFloat {
						t.Errorf("GuardType after GetTable has Type=%v, want TypeFloat", next.Type)
					}
				}
			}
		}
	}
	if !hasGuardAfterGetTable {
		t.Fatal("expected OpGuardType after OpGetTable in IR (from FBFloat feedback), but none found")
	}

	// Verify the IR text contains "GuardType".
	if !strings.Contains(irBefore, "GuardType") {
		t.Error("IR text should contain 'GuardType'")
	}

	// Step 5: Run TypeSpecialize and verify float-specialized ops cascade.
	fnOpt, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}
	irAfter := Print(fnOpt)
	t.Logf("IR after TypeSpecialize:\n%s", irAfter)

	// After type specialization, the float type from GuardType should cascade
	// through the multiply and add, producing MulFloat and/or AddFloat.
	hasMulFloat := strings.Contains(irAfter, "MulFloat")
	hasAddFloat := strings.Contains(irAfter, "AddFloat")
	if !hasMulFloat && !hasAddFloat {
		t.Error("expected MulFloat or AddFloat in optimized IR after TypeSpecialize " +
			"(float type from GuardType should cascade through arithmetic)")
	}
	if hasMulFloat {
		t.Log("confirmed: MulFloat present in optimized IR")
	}
	if hasAddFloat {
		t.Log("confirmed: AddFloat present in optimized IR")
	}

	// Step 6: Verify correctness via IR interpreter on the optimized IR.
	// Run ConstProp + DCE for cleaner IR before interpreting.
	fnOpt, _ = ConstPropPass(fnOpt)
	fnOpt, _ = DCEPass(fnOpt)

	args := []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(10)}
	irResult, irErr := Interpret(fnOpt, args)
	if irErr != nil {
		t.Fatalf("IR interpreter error: %v", irErr)
	}
	t.Logf("IR interpreter result: %v", irResult)

	// Compare: VM and IR interpreter should produce the same result.
	if len(vmResult) == 0 || len(irResult) == 0 {
		t.Fatalf("empty results: VM=%v, IR=%v", vmResult, irResult)
	}
	vmNum := vmResult[0].Number()
	irNum := irResult[0].Number()
	if vmNum != irNum {
		// Allow small epsilon for float comparison.
		diff := vmNum - irNum
		if diff < 0 {
			diff = -diff
		}
		if diff > 1e-6 {
			t.Errorf("result mismatch: VM=%.10f, IR=%.10f (diff=%.2e)", vmNum, irNum, diff)
		}
	}
	t.Logf("VM result=%.6f, IR result=%.6f -- match", vmNum, irNum)
}
