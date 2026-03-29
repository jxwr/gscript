//go:build darwin && arm64

// emit_loop.go implements raw-int loop mode for the Method JIT.
// When a loop's phi values are int-typed, the loop body keeps those values
// as raw int64 in registers, avoiding NaN-box/unbox overhead on every
// iteration. Boxing only happens once at loop entry (unbox phi values from
// NaN-boxed) and once at loop exit (rebox before leaving the loop).
//
// Loop detection uses reachability: a block B is a loop header if it has
// phi nodes and one of its predecessors P is reachable from B through
// successors (forming a cycle B -> ... -> P -> B).
//
// Changes to emit behavior for loops:
//   - emitPhiMoves to loop header transfers raw ints directly (no boxing)
//   - emitBlock for loop header marks int-typed phis as rawInt
//   - emitBranch/emitJump at loop exit boxes raw-int phi values

package methodjit

// loopInfo holds precomputed loop structure for a function.
type loopInfo struct {
	// loopBlocks is the set of block IDs that are inside any loop.
	loopBlocks map[int]bool
	// loopHeaders is the set of block IDs that are loop headers.
	loopHeaders map[int]bool
	// loopPhis maps loop header block ID to the list of phi instruction IDs
	// in that header. Used to know which values need boxing at loop exit.
	loopPhis map[int][]int
	// loopValues is the set of value IDs that are defined inside loop blocks.
	loopValues map[int]bool
}

// computeLoopInfo analyzes the function's CFG to find natural loops.
// A loop header is a block with phi nodes where one of its predecessors
// is reachable from the block itself (forming a cycle). This handles
// any block ordering, including cases where the loop body has a lower
// block ID than the header.
func computeLoopInfo(fn *Function) *loopInfo {
	li := &loopInfo{
		loopBlocks:  make(map[int]bool),
		loopHeaders: make(map[int]bool),
		loopPhis:    make(map[int][]int),
		loopValues:  make(map[int]bool),
	}

	// Find loop headers: blocks with phis where a predecessor is reachable
	// from the block through its successors (cycle detection).
	for _, block := range fn.Blocks {
		hasPhi := len(block.Instrs) > 0 && block.Instrs[0].Op == OpPhi
		if !hasPhi {
			continue
		}
		for _, pred := range block.Preds {
			if isReachable(block, pred, block) {
				// Back-edge: pred -> block. block is the loop header.
				li.loopHeaders[block.ID] = true
				// Collect all blocks in the loop body.
				li.loopBlocks[block.ID] = true
				collectLoopBlocks(pred, block, li.loopBlocks)
			}
		}
	}

	// Collect phi value IDs for each loop header.
	for _, block := range fn.Blocks {
		if !li.loopHeaders[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				break
			}
			li.loopPhis[block.ID] = append(li.loopPhis[block.ID], instr.ID)
		}
	}

	// Collect all value IDs defined in loop blocks.
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				li.loopValues[instr.ID] = true
			}
		}
	}

	return li
}

// isReachable checks if 'target' is reachable from 'start' by following
// successor edges, without passing through 'avoid'. Used to detect cycles:
// if a predecessor P of block B is reachable from B (avoiding B itself on
// the path), then P -> B is a back-edge.
func isReachable(start, target, avoid *Block) bool {
	visited := make(map[int]bool)
	visited[avoid.ID] = true // don't go through the header again
	var dfs func(b *Block) bool
	dfs = func(b *Block) bool {
		if b == target {
			return true
		}
		if visited[b.ID] {
			return false
		}
		visited[b.ID] = true
		for _, succ := range b.Succs {
			if dfs(succ) {
				return true
			}
		}
		return false
	}
	// Start DFS from start's successors (not start itself, since
	// start == avoid == the header block).
	for _, succ := range start.Succs {
		if dfs(succ) {
			return true
		}
	}
	return false
}

// collectLoopBlocks walks backwards from 'block' to 'header' through
// predecessor edges, collecting all blocks that are part of the loop body.
// The header block should already be in the set.
func collectLoopBlocks(block *Block, header *Block, loopBlocks map[int]bool) {
	if loopBlocks[block.ID] {
		return // already visited
	}
	loopBlocks[block.ID] = true
	for _, pred := range block.Preds {
		collectLoopBlocks(pred, header, loopBlocks)
	}
}

// hasLoops returns true if any loops were detected.
func (li *loopInfo) hasLoops() bool {
	return len(li.loopHeaders) > 0
}

// loopPhiArgs is the set of value IDs that are ONLY used as phi arguments
// to loop header phis. These values don't need memory write-through because
// the phi move uses the register directly (emitPhiMoveRawInt).
// Computed lazily by computeLoopPhiArgs.
type loopPhiArgSet map[int]bool

// computeLoopPhiArgs identifies cross-block values that can skip memory
// write-through because they're only consumed within loop blocks where
// they're register-active. This covers two cases:
//
// 1. Values only used as phi args to loop header phis (served by
//    emitPhiMoveRawInt which reads from the register).
// 2. Values only used in loop blocks where loopHeaderRegs makes them
//    register-active (served by resolveRawInt from the register).
//
// For both cases, no non-loop block needs to load the value from memory.
func computeLoopPhiArgs(fn *Function, li *loopInfo) loopPhiArgSet {
	if !li.hasLoops() {
		return nil
	}

	// Find all values defined in loop blocks that have cross-block uses.
	defBlock := make(map[int]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defBlock[instr.ID] = block.ID
			}
		}
	}

	// Check each loop-defined value: does it have ANY cross-block use
	// outside of loop blocks?
	hasNonLoopCrossUse := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				db, ok := defBlock[arg.ID]
				if !ok || db == block.ID {
					continue // same block or external
				}
				// Cross-block use. Is the consuming block outside the loop?
				if !li.loopBlocks[block.ID] {
					hasNonLoopCrossUse[arg.ID] = true
				}
			}
		}
	}

	// Also mark phi args from loop headers: if the phi is int-typed,
	// emitPhiMoveRawInt reads from the register. The memory is NOT used
	// by the phi move. But the phi move itself writes to memory if
	// the phi value is cross-block. So phi args don't need write-through
	// separately.
	// (This is handled by the general "only loop block consumers" check above.)

	// Values defined in loop blocks whose ONLY cross-block consumers are
	// within loop blocks can skip write-through.
	result := make(loopPhiArgSet)
	for valID := range li.loopValues {
		if hasNonLoopCrossUse[valID] {
			continue
		}
		// Only skip for values that ARE cross-block live (otherwise
		// storeRawInt wouldn't write-through anyway).
		db, ok := defBlock[valID]
		if !ok {
			continue
		}
		// Check it's actually used in another block.
		for _, block := range fn.Blocks {
			if block.ID == db {
				continue
			}
			for _, instr := range block.Instrs {
				for _, arg := range instr.Args {
					if arg.ID == valID {
						result[valID] = true
					}
				}
			}
		}
	}

	return result
}

// loopRegState describes the register state at the end of the loop header,
// which is the state that non-header loop blocks will see at entry.
// Maps register number -> (valueID, isRawInt).
type loopRegEntry struct {
	ValueID  int
	IsRawInt bool
}

// computeHeaderExitRegs analyzes the loop header block to determine
// which registers hold which values after all instructions are processed.
// This allows non-header loop blocks to know which registers are valid
// at their entry without needing to emit the header first.
func (li *loopInfo) computeHeaderExitRegs(fn *Function, alloc *RegAllocation) map[int]loopRegEntry {
	regs := make(map[int]loopRegEntry) // register number -> entry

	for _, block := range fn.Blocks {
		if !li.loopHeaders[block.ID] {
			continue
		}

		// Start with phi activations.
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				break
			}
			if pr, ok := alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
				regs[pr.Reg] = loopRegEntry{
					ValueID:  instr.ID,
					IsRawInt: instr.Type == TypeInt,
				}
			}
		}

		// Process instructions to track register overwrites.
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi || instr.Op.IsTerminator() {
				continue
			}
			pr, ok := alloc.ValueRegs[instr.ID]
			if !ok || pr.IsFloat {
				continue
			}
			isRaw := isRawIntOp(instr.Op)
			regs[pr.Reg] = loopRegEntry{
				ValueID:  instr.ID,
				IsRawInt: isRaw,
			}
		}
	}

	return regs
}

// isRawIntOp returns true if the op produces a raw int64 result
// (stored via storeRawInt rather than storeResultNB).
func isRawIntOp(op Op) bool {
	switch op {
	case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt:
		return true
	default:
		return false
	}
}
