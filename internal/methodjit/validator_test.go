// validator_test.go tests the IR structural invariant checker.
// Tests both valid IR (from BuildGraph) and intentionally broken IR.

package methodjit

import (
	"strings"
	"testing"
)

// ---------- Valid IR tests (from BuildGraph — should pass) ----------

func TestValidate_ValidFib(t *testing.T) {
	proto := compile(t, `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n - 1) + fib(n - 2)
}
`)
	fn := BuildGraph(proto)
	errs := Validate(fn)
	if len(errs) > 0 {
		t.Logf("IR:\n%s", Print(fn))
		for _, err := range errs {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestValidate_ValidForLoop(t *testing.T) {
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
	errs := Validate(fn)
	if len(errs) > 0 {
		t.Logf("IR:\n%s", Print(fn))
		for _, err := range errs {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestValidate_ValidIfElse(t *testing.T) {
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
	errs := Validate(fn)
	if len(errs) > 0 {
		t.Logf("IR:\n%s", Print(fn))
		for _, err := range errs {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

// ---------- Broken IR tests (should fail with descriptive errors) ----------

func TestValidate_NoTerminator(t *testing.T) {
	fn := &Function{
		Blocks: make([]*Block, 0),
	}
	entry := &Block{ID: 0}
	// Add a non-terminator instruction but no terminator.
	entry.Instrs = append(entry.Instrs, &Instr{
		ID:    0,
		Op:    OpConstInt,
		Type:  TypeInt,
		Block: entry,
		Aux:   42,
	})
	fn.Entry = entry
	fn.Blocks = append(fn.Blocks, entry)

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for block without terminator")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "terminator") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about missing terminator, got: %v", errs)
	}
}

func TestValidate_SuccPredMismatch(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}

	// b0 has b1 in Succs, but b1 does NOT have b0 in Preds.
	b0.Succs = []*Block{b1}
	b1.Preds = nil // intentionally empty — mismatch

	b0.Instrs = []*Instr{{ID: 0, Op: OpJump, Block: b0}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for succ/pred mismatch")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Succ") || strings.Contains(err.Error(), "Pred") ||
			strings.Contains(err.Error(), "succ") || strings.Contains(err.Error(), "pred") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about succ/pred mismatch, got: %v", errs)
	}
}

func TestValidate_DuplicateValueID(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}

	// Two instructions with the same ID.
	b0.Instrs = []*Instr{
		{ID: 0, Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 1},
		{ID: 0, Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 2}, // duplicate ID
		{ID: 1, Op: OpReturn, Block: b0},
	}

	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for duplicate value ID")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "Duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about duplicate value ID, got: %v", errs)
	}
}

func TestValidate_OrphanBlock(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1} // orphan — not reachable from b0

	b0.Instrs = []*Instr{{ID: 0, Op: OpReturn, Block: b0}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	errs := Validate(fn)
	// Orphan blocks should produce a warning (reported as an error).
	if len(errs) == 0 {
		t.Fatal("expected validation warning for orphan block")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "unreachable") || strings.Contains(err.Error(), "orphan") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about unreachable block, got: %v", errs)
	}
}

func TestValidate_EmptyFunction(t *testing.T) {
	fn := &Function{
		Entry:  nil,
		Blocks: nil,
	}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for empty function")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "entry") || strings.Contains(err.Error(), "Entry") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about nil/missing entry block, got: %v", errs)
	}
}

func TestValidate_NilSuccessor(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}

	b0.Succs = []*Block{nil} // nil successor
	b0.Instrs = []*Instr{{ID: 0, Op: OpJump, Block: b0}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for nil successor")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "nil") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about nil successor, got: %v", errs)
	}
}

func TestValidate_BranchArgCount(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}
	b2 := &Block{ID: 2}

	// Branch with 0 args instead of 1.
	b0.Succs = []*Block{b1, b2}
	b1.Preds = []*Block{b0}
	b2.Preds = []*Block{b0}
	b0.Instrs = []*Instr{{ID: 0, Op: OpBranch, Block: b0, Args: nil}} // missing condition arg
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}
	b2.Instrs = []*Instr{{ID: 2, Op: OpReturn, Block: b2}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for Branch with wrong arg count")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Branch") && strings.Contains(err.Error(), "arg") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about Branch arg count, got: %v", errs)
	}
}

// TestValidate_TerminatorInMiddle checks that a terminator in the middle of a block is caught.
func TestValidate_TerminatorInMiddle(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}

	b0.Instrs = []*Instr{
		{ID: 0, Op: OpReturn, Block: b0}, // terminator in middle
		{ID: 1, Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 1},
		{ID: 2, Op: OpReturn, Block: b0},
	}

	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for terminator in middle of block")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "terminator") && strings.Contains(err.Error(), "middle") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about terminator in middle, got: %v", errs)
	}
}

// TestValidate_JumpSuccCount checks Jump must have exactly 1 successor.
func TestValidate_JumpSuccCount(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}
	b2 := &Block{ID: 2}

	// Jump block with 2 successors (wrong, should be 1).
	b0.Succs = []*Block{b1, b2}
	b1.Preds = []*Block{b0}
	b2.Preds = []*Block{b0}
	b0.Instrs = []*Instr{{ID: 0, Op: OpJump, Block: b0}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}
	b2.Instrs = []*Instr{{ID: 2, Op: OpReturn, Block: b2}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for Jump with wrong succ count")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Jump") && strings.Contains(err.Error(), "successor") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about Jump successor count, got: %v", errs)
	}
}

// TestValidate_ReturnSuccCount checks Return must have 0 successors.
func TestValidate_ReturnSuccCount(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}

	// Return block with a successor (wrong, should be 0).
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}
	b0.Instrs = []*Instr{{ID: 0, Op: OpReturn, Block: b0}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for Return with successors")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Return") && strings.Contains(err.Error(), "successor") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about Return successor count, got: %v", errs)
	}
}

// TestValidate_DuplicateBlockID checks that duplicate block IDs are caught.
func TestValidate_DuplicateBlockID(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 0} // duplicate block ID

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}
	b0.Instrs = []*Instr{{ID: 0, Op: OpJump, Block: b0}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for duplicate block ID")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "Duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about duplicate block ID, got: %v", errs)
	}
}

// TestValidate_EntryNotInBlocks checks that entry block must be in fn.Blocks.
func TestValidate_EntryNotInBlocks(t *testing.T) {
	fn := &Function{}
	entry := &Block{ID: 0}
	entry.Instrs = []*Instr{{ID: 0, Op: OpReturn, Block: entry}}

	other := &Block{ID: 1}
	other.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: other}}

	fn.Entry = entry
	fn.Blocks = []*Block{other} // entry is NOT in Blocks

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for entry not in Blocks")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Entry") || strings.Contains(err.Error(), "entry") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about entry not in Blocks, got: %v", errs)
	}
}

// TestValidate_NilPredecessor checks that nil entries in Preds are caught.
func TestValidate_NilPredecessor(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{nil} // nil predecessor
	b0.Instrs = []*Instr{{ID: 0, Op: OpJump, Block: b0}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for nil predecessor")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "nil") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about nil predecessor, got: %v", errs)
	}
}

// TestValidate_BranchSuccCount checks Branch must have exactly 2 successors.
func TestValidate_BranchSuccCount(t *testing.T) {
	fn := &Function{}
	b0 := &Block{ID: 0}
	b1 := &Block{ID: 1}
	cond := &Value{ID: 99}

	// Branch block with only 1 successor (wrong, should be 2).
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}
	b0.Instrs = []*Instr{{ID: 0, Op: OpBranch, Block: b0, Args: []*Value{cond}}}
	b1.Instrs = []*Instr{{ID: 1, Op: OpReturn, Block: b1}}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("expected validation errors for Branch with wrong succ count")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Branch") && strings.Contains(err.Error(), "successor") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about Branch successor count, got: %v", errs)
	}
}
