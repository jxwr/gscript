// validator.go checks structural invariants on the CFG SSA IR.
// Run Validate(fn) after every compilation pass to catch bugs immediately.
// Returns a list of errors — empty list means the IR is well-formed.
// This is a safety net: if a pass produces invalid IR, the validator
// catches it before it causes mysterious failures downstream.
//
// Invariants checked:
//   - Entry block exists and is in Blocks
//   - All blocks terminated (last instr is terminator, no terminator in middle)
//   - Succ/Pred consistency (bidirectional edges match)
//   - Terminator successor counts (Branch=2, Jump=1, Return=0)
//   - Branch arg count (exactly 1 condition)
//   - Unique value IDs and block IDs
//   - No nil successors or predecessors
//   - Reachability from entry (warning for orphan blocks)

package methodjit

import (
	"fmt"
)

// Validate checks all structural invariants of a Function's IR.
// Returns nil if the IR is well-formed, or a list of errors describing violations.
func Validate(fn *Function) []error {
	v := &validator{fn: fn}
	v.run()
	if len(v.errs) == 0 {
		return nil
	}
	return v.errs
}

// validator holds state for a single validation pass.
type validator struct {
	fn   *Function
	errs []error
}

func (v *validator) errorf(format string, args ...interface{}) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

func (v *validator) run() {
	// 1. Entry block exists.
	if v.fn.Entry == nil {
		v.errorf("entry block is nil")
		return // can't check anything else without an entry
	}

	// 2. Entry is in Blocks.
	if !v.checkEntryInBlocks() {
		return // block list is inconsistent, further checks unreliable
	}

	// 3. Block ID uniqueness.
	v.checkBlockIDUniqueness()

	// 4. No nil successors or predecessors.
	v.checkNilEdges()

	// 5. All blocks terminated + no terminator in middle.
	v.checkTerminators()

	// 6. Terminator successor counts.
	v.checkTerminatorSuccCounts()

	// 7. Succ/Pred consistency.
	v.checkSuccPredConsistency()

	// 8. Branch arg count.
	v.checkBranchArgs()

	// 9. Unique value IDs.
	v.checkValueIDUniqueness()

	// 10. Reachability.
	v.checkReachability()
}

// checkEntryInBlocks verifies fn.Entry is present in fn.Blocks.
func (v *validator) checkEntryInBlocks() bool {
	for _, blk := range v.fn.Blocks {
		if blk == v.fn.Entry {
			return true
		}
	}
	v.errorf("entry block B%d is not in fn.Blocks", v.fn.Entry.ID)
	return false
}

// checkBlockIDUniqueness verifies no two blocks share an ID.
func (v *validator) checkBlockIDUniqueness() {
	seen := make(map[int]*Block)
	for _, blk := range v.fn.Blocks {
		if prev, ok := seen[blk.ID]; ok {
			_ = prev
			v.errorf("duplicate block ID %d", blk.ID)
		}
		seen[blk.ID] = blk
	}
}

// checkNilEdges checks for nil entries in Succs and Preds.
func (v *validator) checkNilEdges() {
	for _, blk := range v.fn.Blocks {
		for i, succ := range blk.Succs {
			if succ == nil {
				v.errorf("B%d: nil successor at index %d", blk.ID, i)
			}
		}
		for i, pred := range blk.Preds {
			if pred == nil {
				v.errorf("B%d: nil predecessor at index %d", blk.ID, i)
			}
		}
	}
}

// checkTerminators verifies every block ends with a terminator and has no
// terminator in the middle.
func (v *validator) checkTerminators() {
	for _, blk := range v.fn.Blocks {
		if len(blk.Instrs) == 0 {
			v.errorf("B%d: block has no instructions (missing terminator)", blk.ID)
			continue
		}

		// Last instruction must be a terminator.
		last := blk.Instrs[len(blk.Instrs)-1]
		if !last.Op.IsTerminator() {
			v.errorf("B%d: last instruction %s (v%d) is not a terminator",
				blk.ID, last.Op, last.ID)
		}

		// No terminator should appear in the middle.
		for i := 0; i < len(blk.Instrs)-1; i++ {
			if blk.Instrs[i].Op.IsTerminator() {
				v.errorf("B%d: terminator %s (v%d) in middle of block at position %d",
					blk.ID, blk.Instrs[i].Op, blk.Instrs[i].ID, i)
			}
		}
	}
}

// checkTerminatorSuccCounts verifies each terminator has the correct number
// of successors: Branch=2, Jump=1, Return=0.
func (v *validator) checkTerminatorSuccCounts() {
	for _, blk := range v.fn.Blocks {
		if len(blk.Instrs) == 0 {
			continue
		}
		last := blk.Instrs[len(blk.Instrs)-1]
		nSuccs := len(blk.Succs)

		switch last.Op {
		case OpBranch:
			if nSuccs != 2 {
				v.errorf("B%d: Branch must have 2 successors, got %d", blk.ID, nSuccs)
			}
		case OpJump:
			if nSuccs != 1 {
				v.errorf("B%d: Jump must have 1 successor, got %d", blk.ID, nSuccs)
			}
		case OpReturn:
			if nSuccs != 0 {
				v.errorf("B%d: Return must have 0 successors, got %d", blk.ID, nSuccs)
			}
		}
	}
}

// checkSuccPredConsistency verifies that if B is in A.Succs then A is in B.Preds,
// and vice versa.
func (v *validator) checkSuccPredConsistency() {
	// Forward: A.Succs contains B → B.Preds must contain A.
	for _, blk := range v.fn.Blocks {
		for _, succ := range blk.Succs {
			if succ == nil {
				continue // nil edges caught separately
			}
			if !containsBlock(succ.Preds, blk) {
				v.errorf("B%d in Succs of B%d, but B%d not in Preds of B%d",
					succ.ID, blk.ID, blk.ID, succ.ID)
			}
		}
	}

	// Reverse: A.Preds contains B → B.Succs must contain A.
	for _, blk := range v.fn.Blocks {
		for _, pred := range blk.Preds {
			if pred == nil {
				continue
			}
			if !containsBlock(pred.Succs, blk) {
				v.errorf("B%d in Preds of B%d, but B%d not in Succs of B%d",
					pred.ID, blk.ID, blk.ID, pred.ID)
			}
		}
	}
}

// checkBranchArgs verifies OpBranch instructions have exactly 1 arg (the condition).
func (v *validator) checkBranchArgs() {
	for _, blk := range v.fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpBranch && len(instr.Args) != 1 {
				v.errorf("B%d: Branch (v%d) must have exactly 1 arg (condition), got %d",
					blk.ID, instr.ID, len(instr.Args))
			}
		}
	}
}

// checkValueIDUniqueness verifies no two instructions share a value ID.
func (v *validator) checkValueIDUniqueness() {
	type loc struct {
		blockID int
		instrOp Op
	}
	seen := make(map[int]loc)
	for _, blk := range v.fn.Blocks {
		for _, instr := range blk.Instrs {
			if prev, ok := seen[instr.ID]; ok {
				v.errorf("duplicate value ID v%d: in B%d (%s) and B%d (%s)",
					instr.ID, prev.blockID, prev.instrOp, blk.ID, instr.Op)
			}
			seen[instr.ID] = loc{blockID: blk.ID, instrOp: instr.Op}
		}
	}
}

// checkReachability verifies all blocks are reachable from fn.Entry.
// Unreachable blocks are reported as warnings (still errors in the return value).
func (v *validator) checkReachability() {
	reachable := make(map[*Block]bool)
	var walk func(b *Block)
	walk = func(b *Block) {
		if reachable[b] {
			return
		}
		reachable[b] = true
		for _, succ := range b.Succs {
			if succ != nil {
				walk(succ)
			}
		}
	}
	walk(v.fn.Entry)

	for _, blk := range v.fn.Blocks {
		if !reachable[blk] {
			v.errorf("B%d is unreachable from entry", blk.ID)
		}
	}
}

// containsBlock returns true if blocks contains target.
func containsBlock(blocks []*Block, target *Block) bool {
	for _, b := range blocks {
		if b == target {
			return true
		}
	}
	return false
}
