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
	// headerBlocks maps each loop header block ID to the set of block IDs
	// in that specific header's loop body (including the header itself).
	// Unlike loopBlocks which is the union of all loops, this tracks per-loop.
	headerBlocks map[int]map[int]bool
	// blockInnerHeader maps each non-header loop block ID to its innermost
	// enclosing loop header block ID. Used to look up the correct per-header
	// register state for non-header loop blocks in nested loops.
	blockInnerHeader map[int]int
}

// domInfo holds dominator information for the CFG.
// idom[blockID] is the immediate dominator block ID. The entry block
// has idom = -1 (no dominator).
type domInfo struct {
	idom map[int]int
}

// dominates returns true if block A dominates block B (A is on every
// path from the entry to B). A block dominates itself.
func (d *domInfo) dominates(a, b int) bool {
	if a == b {
		return true
	}
	cur := b
	for {
		parent, ok := d.idom[cur]
		if !ok || parent < 0 {
			return false
		}
		if parent == a {
			return true
		}
		cur = parent
	}
}

// computeDominators computes the immediate dominator for each block using
// the iterative algorithm from Cooper, Harvey, and Kennedy (2001).
// Processes blocks in reverse postorder for guaranteed convergence.
func computeDominators(fn *Function) *domInfo {
	// Compute RPO and build index: blockID -> RPO position.
	rpo := computeRPO(fn)
	rpoIdx := make(map[int]int)
	for i, b := range rpo {
		rpoIdx[b.ID] = i
	}

	entryID := fn.Entry.ID

	// Initialize: entry dominates itself, all others undefined (-1).
	idom := make(map[int]int)
	for _, b := range fn.Blocks {
		idom[b.ID] = -1
	}
	idom[entryID] = entryID

	// Iterative fixed-point computation in RPO order.
	changed := true
	for changed {
		changed = false
		for _, b := range rpo {
			if b.ID == entryID {
				continue
			}
			newIdom := -1
			for _, pred := range b.Preds {
				if idom[pred.ID] == -1 && pred.ID != entryID {
					continue // predecessor not yet processed
				}
				if newIdom == -1 {
					newIdom = pred.ID
				} else {
					newIdom = intersectDom(idom, newIdom, pred.ID, rpoIdx, entryID)
				}
			}
			if newIdom != idom[b.ID] {
				idom[b.ID] = newIdom
				changed = true
			}
		}
	}

	idom[entryID] = -1
	return &domInfo{idom: idom}
}

// computeRPO returns the blocks in reverse postorder. This ordering
// guarantees that dominators converge in a single pass for reducible CFGs.
func computeRPO(fn *Function) []*Block {
	visited := make(map[int]bool)
	var postorder []*Block

	var visit func(b *Block)
	visit = func(b *Block) {
		if visited[b.ID] {
			return
		}
		visited[b.ID] = true
		for _, succ := range b.Succs {
			visit(succ)
		}
		postorder = append(postorder, b)
	}
	visit(fn.Entry)

	// Reverse to get RPO.
	rpo := make([]*Block, len(postorder))
	for i, b := range postorder {
		rpo[len(postorder)-1-i] = b
	}
	return rpo
}

// intersectDom finds the common dominator of blocks a and b by walking
// up the dominator tree. Uses RPO index to determine which node is
// "deeper" — higher RPO index means farther from entry.
func intersectDom(idom map[int]int, a, b int, rpoIdx map[int]int, entryID int) int {
	for a != b {
		for rpoIdx[a] > rpoIdx[b] {
			next := idom[a]
			if next == a || next == -1 {
				return entryID
			}
			a = next
		}
		for rpoIdx[b] > rpoIdx[a] {
			next := idom[b]
			if next == b || next == -1 {
				return entryID
			}
			b = next
		}
	}
	return a
}

// computeLoopInfo analyzes the function's CFG to find natural loops.
// A loop header is a block with phi nodes where one of its predecessors
// is dominated by the header (forming a natural loop back-edge).
func computeLoopInfo(fn *Function) *loopInfo {
	li := &loopInfo{
		loopBlocks:       make(map[int]bool),
		loopHeaders:      make(map[int]bool),
		loopPhis:         make(map[int][]int),
		loopValues:       make(map[int]bool),
		headerBlocks:     make(map[int]map[int]bool),
		blockInnerHeader: make(map[int]int),
	}

	// Compute dominators for correct back-edge detection.
	// A back-edge pred→header requires header to dominate pred.
	dom := computeDominators(fn)

	// Find loop headers: blocks with phis where a predecessor is dominated
	// by the block (forming a natural loop back-edge).
	for _, block := range fn.Blocks {
		hasPhi := len(block.Instrs) > 0 && block.Instrs[0].Op == OpPhi
		if !hasPhi {
			continue
		}
		for _, pred := range block.Preds {
			if dom.dominates(block.ID, pred.ID) {
				// Back-edge: pred -> block. block is the loop header.
				li.loopHeaders[block.ID] = true
				perHeader := map[int]bool{block.ID: true}
				collectLoopBlocks(pred, block, perHeader)
				// Merge into headerBlocks (a header may have multiple back-edges).
				if existing, ok := li.headerBlocks[block.ID]; ok {
					for bid := range perHeader {
						existing[bid] = true
					}
				} else {
					li.headerBlocks[block.ID] = perHeader
				}
			}
		}
	}

	// Compute loopBlocks as the union of all per-header block sets.
	// Done after all headers are found to avoid interference between
	// inner and outer loop detection in collectLoopBlocks.
	for _, blocks := range li.headerBlocks {
		for bid := range blocks {
			li.loopBlocks[bid] = true
		}
	}

	// Compute innermost header for each non-header loop block.
	// The innermost header is the one whose loop body is smallest
	// (fewest blocks), meaning it's the most tightly enclosing loop.
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] || li.loopHeaders[block.ID] {
			continue
		}
		bestHeader := -1
		bestSize := int(^uint(0) >> 1) // max int
		for headerID, blocks := range li.headerBlocks {
			if blocks[block.ID] && len(blocks) < bestSize {
				bestSize = len(blocks)
				bestHeader = headerID
			}
		}
		if bestHeader >= 0 {
			li.blockInnerHeader[block.ID] = bestHeader
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
// Computed by computeLoopPhiArgs after per-header register state is known.
type loopPhiArgSet map[int]bool

// computeLoopPhiArgs identifies cross-block values that can skip memory
// write-through because they'll be register-active at every use site.
//
// A value V (defined in block D) can skip write-through only if:
// 1. V is only used within loop blocks (no use outside loops)
// 2. At every site where V is read cross-block, V's register is active.
//    For phi args, the read happens in the predecessor block of the phi's
//    header. V must be register-active in that predecessor.
//
// With nested loops, a value defined in an outer header may be used as a
// phi arg from an inner loop block where the value isn't register-active.
// This function conservatively requires that V is in the headerExitRegs
// of the block's innermost header for every cross-block use site.
func computeLoopPhiArgs(fn *Function, li *loopInfo, alloc *RegAllocation,
	headerRegs map[int]map[int]loopRegEntry) loopPhiArgSet {
	if !li.hasLoops() {
		return nil
	}

	defBlock := make(map[int]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defBlock[instr.ID] = block.ID
			}
		}
	}

	// For each candidate value, check all cross-block uses. The value can
	// skip write-through only if it's register-active at every use site.
	result := make(loopPhiArgSet)

	for valID := range li.loopValues {
		db := defBlock[valID]
		pr, hasReg := alloc.ValueRegs[valID]
		if !hasReg || pr.IsFloat {
			continue
		}

		// Check every cross-block use. If ANY use site won't have the value
		// register-active, we can't skip write-through.
		safe := true
		usedCrossBlock := false

		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpPhi {
					// For phi args, the read happens in the predecessor block.
					for predIdx, arg := range instr.Args {
						if arg.ID != valID {
							continue
						}
						usedCrossBlock = true
						// The phi move for this arg executes in pred's context.
						if predIdx >= len(instr.Block.Preds) {
							safe = false
							break
						}
						pred := instr.Block.Preds[predIdx]
						if pred.ID == db {
							// Same block as definition: value was just computed,
							// register is active. Safe.
							continue
						}
						// Different block: is V's register active in pred?
						if !li.loopBlocks[pred.ID] {
							safe = false
							break
						}
						if li.loopHeaders[pred.ID] {
							// pred is a header: check if V is in its headerExitRegs
							hdrRegs := headerRegs[pred.ID]
							if entry, ok := hdrRegs[pr.Reg]; !ok || entry.ValueID != valID {
								safe = false
							}
						} else {
							// pred is a non-header loop block: check its innerHeader's regs
							innerH, ok := li.blockInnerHeader[pred.ID]
							if !ok {
								safe = false
								break
							}
							hdrRegs := headerRegs[innerH]
							if entry, ok := hdrRegs[pr.Reg]; !ok || entry.ValueID != valID {
								safe = false
							}
						}
					}
				} else {
					// Non-phi use: value must be register-active in this block.
					for _, arg := range instr.Args {
						if arg.ID != valID || block.ID == db {
							continue
						}
						usedCrossBlock = true
						if !li.loopBlocks[block.ID] {
							safe = false
							break
						}
						if li.loopHeaders[block.ID] {
							hdrRegs := headerRegs[block.ID]
							if entry, ok := hdrRegs[pr.Reg]; !ok || entry.ValueID != valID {
								safe = false
							}
						} else {
							innerH, ok := li.blockInnerHeader[block.ID]
							if !ok {
								safe = false
								break
							}
							hdrRegs := headerRegs[innerH]
							if entry, ok := hdrRegs[pr.Reg]; !ok || entry.ValueID != valID {
								safe = false
							}
						}
					}
				}
				if !safe {
					break
				}
			}
			if !safe {
				break
			}
		}

		if safe && usedCrossBlock {
			result[valID] = true
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

// loopFPRegEntry describes an FPR's state at the end of the loop header.
// Maps FPR number -> valueID.
type loopFPRegEntry struct {
	ValueID int
}

// computeHeaderExitRegs analyzes each loop header block to determine
// which registers hold which values after all instructions are processed.
// Returns a per-header map so that non-header blocks in nested loops
// can look up registers from their innermost enclosing header.
func (li *loopInfo) computeHeaderExitRegs(fn *Function, alloc *RegAllocation) map[int]map[int]loopRegEntry {
	perHeader := make(map[int]map[int]loopRegEntry) // headerBlockID -> (register number -> entry)

	for _, block := range fn.Blocks {
		if !li.loopHeaders[block.ID] {
			continue
		}

		regs := make(map[int]loopRegEntry)

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

		perHeader[block.ID] = regs
	}

	return perHeader
}

// computeHeaderExitFPRegs analyzes each loop header to determine which FPR
// registers hold which values after all instructions are processed.
// Returns a per-header map so that non-header blocks in nested loops
// can look up FPRs from their innermost enclosing header.
func (li *loopInfo) computeHeaderExitFPRegs(fn *Function, alloc *RegAllocation) map[int]map[int]loopFPRegEntry {
	perHeader := make(map[int]map[int]loopFPRegEntry) // headerBlockID -> (FPR number -> entry)

	for _, block := range fn.Blocks {
		if !li.loopHeaders[block.ID] {
			continue
		}

		regs := make(map[int]loopFPRegEntry)

		// Start with phi activations.
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi {
				break
			}
			if pr, ok := alloc.ValueRegs[instr.ID]; ok && pr.IsFloat {
				regs[pr.Reg] = loopFPRegEntry{ValueID: instr.ID}
			}
		}

		// Process instructions to track FPR overwrites.
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi || instr.Op.IsTerminator() {
				continue
			}
			pr, ok := alloc.ValueRegs[instr.ID]
			if !ok || !pr.IsFloat {
				continue
			}
			regs[pr.Reg] = loopFPRegEntry{ValueID: instr.ID}
		}

		perHeader[block.ID] = regs
	}

	return perHeader
}

// computeSafeHeaderRegs filters per-header register maps to only include
// entries whose register is NOT clobbered by any non-header block in the
// loop body. When a register is clobbered, the header's value won't survive
// to non-header blocks, so activating it would be incorrect.
func computeSafeHeaderRegs(fn *Function, li *loopInfo, alloc *RegAllocation,
	headerRegs map[int]map[int]loopRegEntry) map[int]map[int]loopRegEntry {
	safe := make(map[int]map[int]loopRegEntry)
	for headerID, regs := range headerRegs {
		bodyBlocks := li.headerBlocks[headerID]
		safeRegs := make(map[int]loopRegEntry)
		for reg, entry := range regs {
			clobbered := false
			for _, block := range fn.Blocks {
				if block.ID == headerID || !bodyBlocks[block.ID] {
					continue
				}
				for _, instr := range block.Instrs {
					if instr.Op == OpPhi || instr.Op.IsTerminator() {
						continue
					}
					instrPR, ok := alloc.ValueRegs[instr.ID]
					if ok && !instrPR.IsFloat && instrPR.Reg == reg {
						clobbered = true
						break
					}
				}
				if clobbered {
					break
				}
			}
			if !clobbered {
				safeRegs[reg] = entry
			}
		}
		safe[headerID] = safeRegs
	}
	return safe
}

// computeSafeHeaderFPRegs is the FPR equivalent of computeSafeHeaderRegs.
func computeSafeHeaderFPRegs(fn *Function, li *loopInfo, alloc *RegAllocation,
	headerFPRegs map[int]map[int]loopFPRegEntry) map[int]map[int]loopFPRegEntry {
	safe := make(map[int]map[int]loopFPRegEntry)
	for headerID, regs := range headerFPRegs {
		bodyBlocks := li.headerBlocks[headerID]
		safeRegs := make(map[int]loopFPRegEntry)
		for reg, entry := range regs {
			clobbered := false
			for _, block := range fn.Blocks {
				if block.ID == headerID || !bodyBlocks[block.ID] {
					continue
				}
				for _, instr := range block.Instrs {
					if instr.Op == OpPhi || instr.Op.IsTerminator() {
						continue
					}
					instrPR, ok := alloc.ValueRegs[instr.ID]
					if ok && instrPR.IsFloat && instrPR.Reg == reg {
						clobbered = true
						break
					}
				}
				if clobbered {
					break
				}
			}
			if !clobbered {
				safeRegs[reg] = entry
			}
		}
		safe[headerID] = safeRegs
	}
	return safe
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

// isRawFloatOp returns true if the op produces a raw float64 result
// (stored via storeRawFloat in an FPR).
func isRawFloatOp(op Op) bool {
	switch op {
	case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat:
		return true
	default:
		return false
	}
}
