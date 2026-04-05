// loops.go contains platform-agnostic loop analysis for the Method JIT.
// It defines loopInfo (natural loop structure), domInfo (immediate dominators),
// and helpers that build both. These types are used by architecture-specific
// emit code (emit_loop.go) and by passes (pass_licm.go) that need loop
// structure without touching register allocation.
//
// This file intentionally has no build tag so that loop-analysis tests and
// passes can run on any platform. Anything that depends on RegAllocation or
// ARM64 emission lives in emit_loop.go (darwin/arm64 only).

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

// loopNest maps each loop header block ID to its parent loop header ID,
// or -1 if the header is outermost. A header H has parent P if P is a
// different header and P's loop body contains H. When multiple candidate
// parents exist (e.g. triply-nested loops), the parent with the smallest
// loop body is chosen as the innermost enclosing header.
func loopNest(li *loopInfo) map[int]int {
	nest := make(map[int]int, len(li.loopHeaders))
	for hdrID := range li.loopHeaders {
		bestParent := -1
		bestSize := int(^uint(0) >> 1) // max int
		for candID, candBlocks := range li.headerBlocks {
			if candID == hdrID {
				continue
			}
			if !candBlocks[hdrID] {
				continue
			}
			if len(candBlocks) < bestSize {
				bestSize = len(candBlocks)
				bestParent = candID
			}
		}
		nest[hdrID] = bestParent
	}
	return nest
}

// loopPreds partitions hdr.Preds into back-edge preds (those inside this
// header's loop body — i.e. present in li.headerBlocks[hdr.ID]) and outside
// preds (all others). Returns inside, outside in that order.
func loopPreds(li *loopInfo, hdr *Block) (inside []*Block, outside []*Block) {
	bodyBlocks := li.headerBlocks[hdr.ID]
	for _, pred := range hdr.Preds {
		if bodyBlocks != nil && bodyBlocks[pred.ID] {
			inside = append(inside, pred)
		} else {
			outside = append(outside, pred)
		}
	}
	return inside, outside
}

// computeLoopPreheaders identifies the dedicated pre-header block of each
// loop header, matching the structure that LICMPass constructs. Result
// maps loop-header block ID → pre-header block ID. A block PH qualifies
// as the pre-header of header H only when:
//
//   - H has exactly one predecessor that is NOT inside H's loop body
//     (i.e. exactly one "outside" pred in loopPreds terminology), and
//   - that predecessor PH's Succs is precisely [H] (single successor).
//
// Headers without a unique outside predecessor, or whose outside
// predecessor has additional successors, are omitted from the map.
// The function is read-only: it mutates neither fn nor li.
func computeLoopPreheaders(fn *Function, li *loopInfo) map[int]int {
	if fn == nil || li == nil {
		return map[int]int{}
	}
	result := make(map[int]int, len(li.loopHeaders))
	for _, block := range fn.Blocks {
		if !li.loopHeaders[block.ID] {
			continue
		}
		_, outside := loopPreds(li, block)
		if len(outside) != 1 {
			continue
		}
		ph := outside[0]
		if len(ph.Succs) != 1 || ph.Succs[0] != block {
			continue
		}
		result[block.ID] = ph.ID
	}
	return result
}

// collectPreheaderInvariants walks each header → pre-header pair in
// preheaders and returns the set of SSA value IDs produced by non-terminator
// instructions in the pre-header that are consumed by at least one
// instruction (phi or otherwise) inside that header's loop body (the blocks
// listed in li.headerBlocks[headerID], which includes the header itself).
//
// The returned slices are sorted ascending for deterministic output. The
// function is read-only: it mutates neither fn nor li.
func collectPreheaderInvariants(fn *Function, li *loopInfo, preheaders map[int]int) map[int][]int {
	result := make(map[int][]int, len(preheaders))
	if fn == nil || li == nil || len(preheaders) == 0 {
		return result
	}

	// Build a quick map: block ID → *Block for pre-header lookups.
	blockByID := make(map[int]*Block, len(fn.Blocks))
	for _, b := range fn.Blocks {
		blockByID[b.ID] = b
	}

	for headerID, phID := range preheaders {
		ph := blockByID[phID]
		if ph == nil {
			continue
		}
		// Collect the value IDs defined by non-terminator instructions
		// in the pre-header — these are the candidates.
		defs := make(map[int]bool, len(ph.Instrs))
		for _, instr := range ph.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			defs[instr.ID] = true
		}
		if len(defs) == 0 {
			continue
		}

		// Walk every instruction in the header's loop body and record
		// which pre-header defs appear as Args.
		body := li.headerBlocks[headerID]
		if body == nil {
			continue
		}
		used := make(map[int]bool, len(defs))
		for _, b := range fn.Blocks {
			if !body[b.ID] {
				continue
			}
			for _, instr := range b.Instrs {
				for _, a := range instr.Args {
					if a == nil {
						continue
					}
					if defs[a.ID] {
						used[a.ID] = true
					}
				}
			}
		}
		if len(used) == 0 {
			continue
		}
		ids := make([]int, 0, len(used))
		for id := range used {
			ids = append(ids, id)
		}
		// Sort ascending for deterministic output.
		for i := 1; i < len(ids); i++ {
			for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
				ids[j-1], ids[j] = ids[j], ids[j-1]
			}
		}
		result[headerID] = ids
	}
	return result
}
