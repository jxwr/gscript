// graph_builder_ssa_test.go covers SSA-construction edge cases that are hard
// to reach through full bytecode → BuildGraph driving (e.g. recursive variable
// resolution into an already-terminated entry block).

package methodjit

import (
	"strings"
	"testing"
)

// TestReadVariableRecursive_EntryAlreadyTerminated guards a defensive insert
// rule: when readVariableRecursive needs to materialize an OpLoadSlot in a
// sealed entry block whose final instruction is already a terminator, the new
// instruction must be spliced BEFORE the terminator, not appended after it.
//
// Otherwise the validator's checkTerminators rule (terminator must be last,
// no terminator in middle) rejects the function. This was originally surfaced
// by an unrelated missing-opcode case in producer_consumer_pipeline.consume
// after OP_RESUME landed (deebbcf), but the helper bug is general: any path
// that drives readVariableRecursive after the entry block has been forward-
// terminated trips it.
func TestReadVariableRecursive_EntryAlreadyTerminated(t *testing.T) {
	b := &graphBuilder{
		fn:        &Function{NumRegs: 4},
		currentPC: -1,
	}
	entry := &Block{
		ID:   0,
		defs: make(map[int]*Value),
	}
	b.fn.Blocks = []*Block{entry}
	b.fn.Entry = entry
	b.nextBlock = 1

	// Pre-seed the entry block with a terminator (Jump). Mirrors the real
	// shape: graph_builder forward-emits a Jump, then SSA recursion later
	// runs back through readVariable for a never-written param slot.
	jump := b.emit(entry, OpJump, TypeUnknown, nil, 0, 0)
	entry.sealed = true

	// Trigger the recursive read on a slot that was never written. The
	// entry block has no preds and is sealed, so readVariableRecursive
	// falls into the LoadSlot branch (graph_builder_ssa.go ~line 55).
	val := b.readVariableRecursive(2, entry)
	if val == nil {
		t.Fatalf("readVariableRecursive returned nil")
	}

	// The Jump must remain the terminator at the end of the block.
	if len(entry.Instrs) < 2 {
		t.Fatalf("expected at least 2 instrs, got %d", len(entry.Instrs))
	}
	last := entry.Instrs[len(entry.Instrs)-1]
	if last != jump {
		t.Fatalf("terminator no longer at end of block; last op = %s (expected Jump)", last.Op)
	}
	// The OpLoadSlot must be inserted somewhere before the terminator.
	foundLoad := false
	for i := 0; i < len(entry.Instrs)-1; i++ {
		if entry.Instrs[i].Op == OpLoadSlot {
			foundLoad = true
			break
		}
	}
	if !foundLoad {
		t.Fatalf("OpLoadSlot was not inserted before terminator; instrs: %v", opsOfBlock(entry.Instrs))
	}

	// Validator must NOT report a "terminator in middle" error. (Other
	// errors are tolerated here because the hand-built block has no
	// successor; we are only guarding the terminator-position invariant.)
	for _, err := range Validate(b.fn) {
		msg := err.Error()
		if strings.Contains(msg, "terminator in middle") || strings.Contains(msg, "is not a terminator") {
			t.Fatalf("validator caught terminator-position violation: %v", err)
		}
	}
}

func opsOfBlock(instrs []*Instr) []string {
	out := make([]string, len(instrs))
	for i, in := range instrs {
		out[i] = in.Op.String()
	}
	return out
}
