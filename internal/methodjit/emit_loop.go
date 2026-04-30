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

// Loop structure and dominator analysis live in loops.go (platform-agnostic).
// This file contains register-allocation-coupled helpers that emit code
// for loop headers and exits on darwin/arm64.

// loopPhiArgs is the set of value IDs that are ONLY used as phi arguments
// to loop header phis. These values don't need memory write-through because
// the phi move uses the register directly (emitPhiMoveRawInt).
// Computed by computeLoopPhiArgs after per-header register state is known.
type loopPhiArgSet map[int]bool

// computeLoopPhiArgs identifies cross-block values that can skip memory
// write-through because they'll be register-active at every use site.
//
// A value V (defined in block D) can skip write-through only if:
//  1. V is only used within loop blocks (no use outside loops)
//  2. At every site where V is read cross-block, V's register is active.
//     For phi args, the read happens in the predecessor block of the phi's
//     header. V must be register-active in that predecessor.
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

// computeLoopFPPhiArgs is the FPR equivalent of computeLoopPhiArgs. It marks
// raw-float values whose cross-block uses are satisfied by FPR phi moves, so
// storeRawFloat can skip per-iteration memory write-through.
func computeLoopFPPhiArgs(fn *Function, li *loopInfo, alloc *RegAllocation,
	headerRegs map[int]map[int]loopFPRegEntry) loopPhiArgSet {
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

	result := make(loopPhiArgSet)
	for valID := range li.loopValues {
		db := defBlock[valID]
		pr, hasReg := alloc.ValueRegs[valID]
		if !hasReg || !pr.IsFloat {
			continue
		}

		safe := true
		usedCrossBlock := false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpPhi {
					for predIdx, arg := range instr.Args {
						if arg.ID != valID {
							continue
						}
						usedCrossBlock = true
						if predIdx >= len(instr.Block.Preds) {
							safe = false
							break
						}
						pred := instr.Block.Preds[predIdx]
						if pred.ID == db {
							continue
						}
						if !li.loopBlocks[pred.ID] {
							safe = false
							break
						}
						if li.loopHeaders[pred.ID] {
							hdrRegs := headerRegs[pred.ID]
							if entry, ok := hdrRegs[pr.Reg]; !ok || entry.ValueID != valID {
								safe = false
							}
						} else {
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
	fusedCmps := computeFusedComparisons(fn)

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
			if fusedCmps[instr.ID] {
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
	fusedCmps := computeFusedComparisons(fn)
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
					if fusedCmps[instr.ID] {
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

func computeSafeLoopInvariantGPRs(fn *Function, li *loopInfo, alloc *RegAllocation) map[int]map[int]loopRegEntry {
	if fn == nil || li == nil || alloc == nil || len(alloc.LoopInvariantGPRs) == 0 {
		return nil
	}
	safe := make(map[int]map[int]loopRegEntry)
	fusedCmps := computeFusedComparisons(fn)
	for headerID, values := range alloc.LoopInvariantGPRs {
		bodyBlocks := li.headerBlocks[headerID]
		if bodyBlocks == nil {
			continue
		}
		for valueID, pr := range values {
			if pr.IsFloat {
				continue
			}
			clobbered := false
			for _, block := range fn.Blocks {
				if !bodyBlocks[block.ID] {
					continue
				}
				for _, instr := range block.Instrs {
					if instr.ID == valueID || instr.Op.IsTerminator() {
						continue
					}
					if fusedCmps[instr.ID] {
						continue
					}
					instrPR, ok := alloc.ValueRegs[instr.ID]
					if ok && !instrPR.IsFloat && instrPR.Reg == pr.Reg {
						clobbered = true
						break
					}
				}
				if clobbered {
					break
				}
			}
			if clobbered {
				continue
			}
			if safe[headerID] == nil {
				safe[headerID] = make(map[int]loopRegEntry)
			}
			safe[headerID][valueID] = loopRegEntry{ValueID: valueID, IsRawInt: isLoopInvariantRawInt(fn, valueID)}
		}
	}
	if len(safe) == 0 {
		return nil
	}
	return safe
}

func computeSafeLoopInvariantFPRs(fn *Function, li *loopInfo, alloc *RegAllocation) map[int]map[int]loopFPRegEntry {
	if fn == nil || li == nil || alloc == nil || len(alloc.LoopInvariantFPRs) == 0 {
		return nil
	}
	safe := make(map[int]map[int]loopFPRegEntry)
	for headerID, values := range alloc.LoopInvariantFPRs {
		bodyBlocks := li.headerBlocks[headerID]
		if bodyBlocks == nil {
			continue
		}
		for valueID, pr := range values {
			if !pr.IsFloat {
				continue
			}
			clobbered := false
			for _, block := range fn.Blocks {
				if !bodyBlocks[block.ID] {
					continue
				}
				for _, instr := range block.Instrs {
					if instr.ID == valueID || instr.Op.IsTerminator() {
						continue
					}
					instrPR, ok := alloc.ValueRegs[instr.ID]
					if ok && instrPR.IsFloat && instrPR.Reg == pr.Reg {
						clobbered = true
						break
					}
				}
				if clobbered {
					break
				}
			}
			if clobbered {
				continue
			}
			if safe[headerID] == nil {
				safe[headerID] = make(map[int]loopFPRegEntry)
			}
			safe[headerID][valueID] = loopFPRegEntry{ValueID: valueID}
		}
	}
	if len(safe) == 0 {
		return nil
	}
	return safe
}

func isLoopInvariantRawInt(fn *Function, valueID int) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.ID != valueID {
				continue
			}
			return instr.Op != OpTableArrayData
		}
	}
	return true
}

func (ec *emitContext) activateLoopInvariantGPRs(blockID int) {
	if ec == nil || ec.loop == nil {
		return
	}
	usedRegs := make(map[int]bool)
	for valueID := range ec.activeRegs {
		if pr, ok := ec.alloc.ValueRegs[valueID]; ok && !pr.IsFloat {
			usedRegs[pr.Reg] = true
		}
	}
	for _, headerID := range sortedLoopHeaders(ec.loop) {
		values := ec.loopInvariantGPRs[headerID]
		body := ec.loop.headerBlocks[headerID]
		if body == nil || !body[blockID] {
			continue
		}
		for _, valueID := range sortedLoopRegEntryIDs(values) {
			entry := values[valueID]
			pr, ok := ec.alloc.ValueRegs[entry.ValueID]
			if !ok || pr.IsFloat || usedRegs[pr.Reg] {
				continue
			}
			usedRegs[pr.Reg] = true
			ec.activeRegs[entry.ValueID] = true
			if entry.IsRawInt {
				ec.rawIntRegs[entry.ValueID] = true
			}
		}
	}
}

func (ec *emitContext) activateLoopInvariantFPRs(blockID int) {
	if ec == nil || ec.loop == nil {
		return
	}
	usedRegs := make(map[int]bool)
	for valueID := range ec.activeFPRegs {
		if pr, ok := ec.alloc.ValueRegs[valueID]; ok && pr.IsFloat {
			usedRegs[pr.Reg] = true
		}
	}
	for _, headerID := range sortedLoopHeaders(ec.loop) {
		values := ec.loopInvariantFPRs[headerID]
		body := ec.loop.headerBlocks[headerID]
		if body == nil || !body[blockID] {
			continue
		}
		for _, valueID := range sortedLoopFPRegEntryIDs(values) {
			entry := values[valueID]
			pr, ok := ec.alloc.ValueRegs[entry.ValueID]
			if !ok || !pr.IsFloat || usedRegs[pr.Reg] {
				continue
			}
			usedRegs[pr.Reg] = true
			ec.activeFPRegs[entry.ValueID] = true
		}
	}
}

func (ec *emitContext) activateLoopHeaderFPRs(blockID int) {
	if ec == nil || ec.loop == nil {
		return
	}
	for _, headerID := range sortedLoopHeadersByDepth(ec.loop) {
		if headerID == blockID {
			continue
		}
		body := ec.loop.headerBlocks[headerID]
		if body == nil || !body[blockID] {
			continue
		}
		for _, entry := range ec.safeHeaderFPRegs[headerID] {
			pr, ok := ec.alloc.ValueRegs[entry.ValueID]
			if !ok || !pr.IsFloat {
				continue
			}
			if ec.fprActiveRegisterTaken(pr.Reg, entry.ValueID) {
				continue
			}
			ec.activeFPRegs[entry.ValueID] = true
		}
	}
}

func (ec *emitContext) fprActiveRegisterTaken(reg int, valueID int) bool {
	for activeID := range ec.activeFPRegs {
		if activeID == valueID {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[activeID]
		if ok && pr.IsFloat && pr.Reg == reg {
			return true
		}
	}
	return false
}

func sortedLoopRegEntryIDs(m map[int]loopRegEntry) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

func sortedLoopFPRegEntryIDs(m map[int]loopFPRegEntry) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

// isRawIntOp returns true if the op produces a raw int64 result
// (stored via storeRawInt rather than storeResultNB).
func isRawIntOp(op Op) bool {
	switch op {
	case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt:
		return true
	default:
		return false
	}
}

// isRawFloatOp returns true if the op produces a raw float64 result
// (stored via storeRawFloat in an FPR).
func isRawFloatOp(op Op) bool {
	switch op {
	case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat, OpNumToFloat, OpGetFieldNumToFloat:
		return true
	default:
		return false
	}
}
