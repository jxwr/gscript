// pass_dce.go removes SSA instructions whose results are never used.
// A value is "dead" if no other instruction references it and it has no
// side effects (not a store, call, branch, return, guard, or table mutation).
//
// The pass runs to a fixed point: removing a dead instruction may make its
// operands dead, which can be removed in the next iteration.

package methodjit

// DCEPass removes dead (unused, side-effect-free) instructions from the IR.
func DCEPass(fn *Function) (*Function, error) {
	// Fixed-point iteration: keep removing dead code until stable.
	for {
		useCounts := computeUseCounts(fn)
		removed := false

		for _, block := range fn.Blocks {
			alive := make([]*Instr, 0, len(block.Instrs))
			for _, instr := range block.Instrs {
				if useCounts[instr.ID] == 0 && !hasSideEffect(instr) {
					removed = true
					continue // drop this instruction
				}
				alive = append(alive, instr)
			}
			block.Instrs = alive
		}

		if !removed {
			break
		}
	}

	return fn, nil
}

// computeUseCounts counts how many times each value ID is referenced as an
// argument in any instruction across all blocks.
func computeUseCounts(fn *Function) map[int]int {
	counts := make(map[int]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				if arg != nil {
					counts[arg.ID]++
				}
			}
		}
	}
	return counts
}

// hasSideEffect returns true if the instruction has observable side effects
// and must not be removed even if its result is unused.
func hasSideEffect(instr *Instr) bool {
	switch instr.Op {
	// Control flow: always kept.
	case OpJump, OpBranch, OpReturn:
		return true

	// Store operations: mutate state.
	case OpStoreSlot, OpSetGlobal, OpSetUpval:
		return true

	// Table mutations: observable.
	case OpSetTable, OpSetField, OpSetList, OpAppend:
		return true

	// DenseMatrix writes: observable. Without this, DCE silently drops
	// matrix.setf since its SSA result is never read; JIT produces zeros
	// where VM mode produces correct values. R42-R48 matmul_dense wins
	// were partly on unwritten results. (Correctness fix: R52.)
	case OpMatrixSetF, OpMatrixStoreFAt, OpMatrixStoreFRow:
		return true

	// Calls have arbitrary side effects.
	case OpCall, OpSelf:
		return true

	// Guards: deoptimization side effect.
	case OpGuardType, OpGuardIntRange, OpGuardNonNil, OpGuardTruthy:
		return true

	// For-loop control: always kept.
	case OpForPrep, OpForLoop, OpTForCall, OpTForLoop:
		return true

	// Closure creation may capture state.
	case OpClosure, OpClose:
		return true

	// Channel/goroutine operations: always side-effectful.
	case OpGo, OpMakeChan, OpSend, OpRecv:
		return true

	// Phi nodes: kept (they participate in SSA structure).
	case OpPhi:
		return true

	default:
		return false
	}
}
