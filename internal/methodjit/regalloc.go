// regalloc.go implements a forward-walk register allocator for the Method JIT.
// Maps SSA values to ARM64 physical registers. Simpler than linear scan --
// walks instructions forward within each block, spilling LRU values when
// registers are full. Inspired by V8 Maglev's register allocator.
//
// ARM64 register convention:
//   X0-X15:  scratch / temporaries (caller-saved)
//   X19:     ExecContext pointer (reserved for emit.go)
//   X20-X23: allocatable GPRs (callee-saved, 4 registers)
//   X24:     NaN-boxing int tag constant (reserved)
//   X25:     NaN-boxing bool tag constant (reserved)
//   X26:     VM register base pointer (reserved)
//   X27:     constants pointer (reserved)
//   X28:     allocatable GPR (callee-saved, 5th register)
//   D4-D11,D16-D23: allocatable FPRs

package methodjit

// Allocatable GPR pool: X20, X21, X22, X23, X28.
// X19 is reserved for the ExecContext pointer (emit.go pinned register).
// X28 was previously reserved for trace JIT self-call overflow, but
// self-calls are removed in the Method JIT, freeing X28 as a 5th GPR.
var allocatableGPRs = [5]int{20, 21, 22, 23, 28}

// Allocatable FPR pool. D4-D7 and D16-D23 are caller-saved, and D8-D11 are
// already saved by the Tier 2 prologue when any FPR is used. Native BLR paths
// selectively spill live FPR SSA values across calls, so the caller-saved high
// registers are safe for call-free float-heavy loops without growing the frame.
var allocatableFPRs = [16]int{4, 5, 6, 7, 8, 9, 10, 11, 16, 17, 18, 19, 20, 21, 22, 23}

// PhysReg represents a physical ARM64 register.
type PhysReg struct {
	Reg     int  // register number (X19=19, D4=4, etc.)
	IsFloat bool // true for FPR, false for GPR
}

// RegAllocation is the result of register allocation for a function.
type RegAllocation struct {
	// ValueRegs maps SSA value ID -> physical register.
	ValueRegs map[int]PhysReg
	// SpillSlots maps SSA value ID -> spill slot index (only for spilled values).
	SpillSlots map[int]int
	// NumSpillSlots is the total number of spill slots needed.
	NumSpillSlots int
}

// AllocateRegisters performs register allocation on a Function.
// It computes liveness, then walks instructions forward in each block,
// assigning physical registers and spilling LRU values when needed.
func AllocateRegisters(fn *Function) *RegAllocation {
	alloc := &RegAllocation{
		ValueRegs:  make(map[int]PhysReg),
		SpillSlots: make(map[int]int),
	}

	lastUse := computeLastUse(fn)

	// Compute loop info so that non-header loop blocks can reserve their
	// innermost header's phi registers. Without this, the forward-walk
	// per-block allocator reuses the phi's FPR/GPR for body SSA results,
	// clobbering the loop-carried value and forcing per-use slot reloads.
	li := computeLoopInfo(fn)

	// Identify headers with a "tight" body: exactly 2 blocks (header + one
	// body). For these, the body block is directly reached from the header
	// and no other intervening block can clobber the header's phi registers
	// between their write and the body's entry. Only tight-body headers are
	// eligible for phi register carrying — nested/complex loops are skipped
	// because an inner-loop phi could write the same physical register and
	// invalidate the reservation at runtime.
	tightHeaders := make(map[int]bool)
	for hid, blocks := range li.headerBlocks {
		if len(blocks) == 2 { // header + exactly one body block
			tightHeaders[hid] = true
		}
	}

	// Pre-pass: pre-allocate loop-header phi registers into alloc.ValueRegs
	// for tight-body headers only. Block order is RPO but loop headers can
	// follow their body in RPO, so we can't rely on "allocate headers first
	// via natural order". This pre-pass is phi-only and deterministic.
	for hid := range tightHeaders {
		preAllocateHeaderPhis(findBlockByID(fn, hid), alloc)
	}

	// Invariant carry: identify LICM-hoisted loop-invariant float values
	// defined in pre-header blocks that should be pinned in FPRs across
	// loop-body blocks. Unlike phi carry (which requires tight 2-block
	// loops), invariant carry works for any loop with a pre-header.
	//
	// Phase 1 (pre-pass): identify candidate invariants per header,
	// filter, rank, and budget-limit. No FPR assignments yet.
	//
	// Phase 2 (main loop): after a pre-header block is naturally allocated,
	// collect the FPR assignments from alloc.ValueRegs for the top-N
	// candidates. Store these as pinnedInvariants.
	//
	// Phase 3 (body blocks): merge pinnedInvariants into the carried map.

	// invariantCandidates: headerID → ranked+budgeted list of value IDs
	invariantCandidates := make(map[int][]int)
	// preheaderToHeader: preheader block ID → header block ID
	preheaderToHeader := make(map[int]int)
	// pinnedInvariants: headerID → map[valueID]PhysReg (filled lazily)
	pinnedInvariants := make(map[int]map[int]PhysReg)

	if fn.CarryPreheaderInvariants {
		preheaders := computeLoopPreheaders(fn, li)
		allInvariants := collectPreheaderInvariants(fn, li, preheaders)

		// Build blockByID for instruction lookups.
		blockByID := make(map[int]*Block, len(fn.Blocks))
		for _, b := range fn.Blocks {
			blockByID[b.ID] = b
		}

		// Record reverse mapping: preheader block → header.
		for headerID, phID := range preheaders {
			preheaderToHeader[phID] = headerID
		}

		for headerID, invIDs := range allInvariants {
			phBlock := blockByID[preheaders[headerID]]
			if phBlock == nil {
				continue
			}

			// Build value ID → *Instr map for pre-header defs.
			phInstrs := make(map[int]*Instr, len(phBlock.Instrs))
			for _, instr := range phBlock.Instrs {
				if !instr.Op.IsTerminator() {
					phInstrs[instr.ID] = instr
				}
			}

			bodyBlocks := li.headerBlocks[headerID]

			// Filter 1: only float-typed values.
			// Filter 2: exclude values used OUTSIDE the loop body.
			var candidates []int
			for _, vid := range invIDs {
				instr := phInstrs[vid]
				if instr == nil || !needsFloatReg(instr) {
					continue
				}
				usedOutside := false
				for _, b := range fn.Blocks {
					if bodyBlocks[b.ID] {
						continue
					}
					if b.ID == preheaders[headerID] {
						continue
					}
					for _, bi := range b.Instrs {
						for _, a := range bi.Args {
							if a != nil && a.ID == vid {
								usedOutside = true
								break
							}
						}
						if usedOutside {
							break
						}
					}
					if usedOutside {
						break
					}
				}
				if usedOutside {
					continue
				}
				candidates = append(candidates, vid)
			}

			if len(candidates) == 0 {
				continue
			}

			// Rank by use-count inside the loop body (higher = better).
			useCount := make(map[int]int, len(candidates))
			for _, b := range fn.Blocks {
				if !bodyBlocks[b.ID] {
					continue
				}
				for _, bi := range b.Instrs {
					for _, a := range bi.Args {
						if a != nil {
							useCount[a.ID]++
						}
					}
				}
			}
			// Sort: descending use-count, tie-break ascending value ID.
			for i := 1; i < len(candidates); i++ {
				for j := i; j > 0; j-- {
					a, b := candidates[j-1], candidates[j]
					if useCount[a] < useCount[b] || (useCount[a] == useCount[b] && a > b) {
						candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
					} else {
						break
					}
				}
			}

			// Budget: available FPRs minus reserved temps minus float phis
			// already pre-allocated for this header.
			const reservedTemps = 3
			floatPhiCount := 0
			for _, phiID := range li.loopPhis[headerID] {
				if pr, ok := alloc.ValueRegs[phiID]; ok && pr.IsFloat {
					floatPhiCount++
				}
			}
			budget := len(allocatableFPRs) - reservedTemps - floatPhiCount
			if budget <= 0 {
				continue
			}
			if len(candidates) > budget {
				candidates = candidates[:budget]
			}
			invariantCandidates[headerID] = candidates
		}
	}

	for _, block := range fn.Blocks {
		// After allocating a pre-header block, collect FPR assignments
		// for invariant candidates from alloc.ValueRegs (set naturally by
		// the pre-header's allocateBlock). This avoids pre-allocating FPRs
		// that allocateBlock would overwrite.
		var carried map[int]PhysReg
		if li.loopBlocks[block.ID] && !li.loopHeaders[block.ID] {
			if innerHeader, ok := li.blockInnerHeader[block.ID]; ok {
				// Phi carry: only for tight-body headers (existing logic).
				if tightHeaders[innerHeader] {
					carried = make(map[int]PhysReg)
					for _, phiID := range li.loopPhis[innerHeader] {
						if pr, ok := alloc.ValueRegs[phiID]; ok {
							carried[phiID] = pr
						}
					}
					// Loop-bound carry: pin GPR-allocated non-phi int values
					// used by header comparisons (LeInt/LtInt/EqInt) so they
					// survive across the loop body without eviction.
					hdr := findBlockByID(fn, innerHeader)
					for _, vid := range collectLoopBoundGPRs(hdr, alloc) {
						if pr, ok := alloc.ValueRegs[vid]; ok {
							carried[vid] = pr
						}
					}
				}

				// Invariant carry: works for any loop with a pre-header.
				// Merge pinned invariant FPRs into the carried map.
				if pinned, ok := pinnedInvariants[innerHeader]; ok {
					if carried == nil {
						carried = make(map[int]PhysReg, len(pinned))
					}
					for vid, pr := range pinned {
						carried[vid] = pr
					}
				}
			}
		}
		allocateBlock(block, alloc, lastUse, carried)

		// After allocating a pre-header, collect the natural FPR assignments
		// for the top-N invariant candidates. These will be carried into
		// the loop body blocks to prevent eviction.
		if headerID, ok := preheaderToHeader[block.ID]; ok {
			candidates := invariantCandidates[headerID]
			if len(candidates) > 0 {
				headerPinned := make(map[int]PhysReg, len(candidates))
				for _, vid := range candidates {
					if pr, ok := alloc.ValueRegs[vid]; ok && pr.IsFloat {
						headerPinned[vid] = pr
					}
				}
				if len(headerPinned) > 0 {
					pinnedInvariants[headerID] = headerPinned
				}
			}
		}
	}

	return alloc
}

// findBlockByID looks up a block by its ID. Returns nil if not found.
func findBlockByID(fn *Function, id int) *Block {
	for _, b := range fn.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// preAllocateHeaderPhis walks the leading phi instructions of a loop header
// block and commits their FPR/GPR assignments into alloc.ValueRegs. This is
// called before the main block-by-block allocation loop so that non-header
// loop-body blocks (which may be processed before their header in RPO) can
// reserve the header's phi registers and avoid clobbering them. If a phi
// cannot fit (pool exhausted), it is spilled here, matching Phase 1 of
// allocateBlock's logic.
func preAllocateHeaderPhis(block *Block, alloc *RegAllocation) {
	if block == nil {
		return
	}
	gprs := newRegState(allocatableGPRs[:], false)
	fprs := newRegState(allocatableFPRs[:], true)
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			break
		}
		wantFloat := needsFloatReg(instr)
		var rs *regState
		if wantFloat {
			rs = fprs
		} else {
			rs = gprs
		}
		r := rs.findFree()
		if r >= 0 {
			rs.assign(instr.ID, r)
			alloc.ValueRegs[instr.ID] = PhysReg{Reg: r, IsFloat: wantFloat}
		} else {
			// Pool exhausted: spill. The later full allocateBlock call on
			// this header will see the spill and skip re-allocation.
			alloc.SpillSlots[instr.ID] = alloc.NumSpillSlots
			alloc.NumSpillSlots++
		}
	}
}

// collectLoopBoundGPRs scans a loop header block for int comparison ops
// (LeInt, LtInt, EqInt) and returns value IDs of non-phi, GPR-allocated
// arguments (e.g., loop bounds from LoadSlot). These are candidates for
// carrying across the loop body to avoid eviction and per-iteration reloads.
func collectLoopBoundGPRs(hdr *Block, alloc *RegAllocation) []int {
	if hdr == nil {
		return nil
	}
	phiIDs := make(map[int]bool)
	for _, instr := range hdr.Instrs {
		if instr.Op == OpPhi {
			phiIDs[instr.ID] = true
		}
	}
	var bounds []int
	for _, instr := range hdr.Instrs {
		if instr.Op != OpLeInt && instr.Op != OpLtInt && instr.Op != OpEqInt {
			continue
		}
		for _, arg := range instr.Args {
			if arg == nil || phiIDs[arg.ID] {
				continue
			}
			if pr, ok := alloc.ValueRegs[arg.ID]; ok && !pr.IsFloat {
				bounds = append(bounds, arg.ID)
			}
		}
	}
	return bounds
}

// regState tracks the current state of a register pool (GPR or FPR).
type regState struct {
	pool    []int       // allocatable register numbers
	regToID map[int]int // register number -> value ID currently held (-1 if free)
	idToReg map[int]int // value ID -> register number
	lru     []int       // value IDs in order of last use (oldest first)
	isFloat bool        // true for FPR pool
	// pinned is the set of value IDs that must not be evicted. Used to
	// reserve loop-header phi registers in non-header loop-body blocks so
	// that body SSA results cannot clobber the loop-carried value at
	// runtime. Pinned IDs never appear in the lru list.
	pinned map[int]bool
}

func newRegState(pool []int, isFloat bool) *regState {
	rs := &regState{
		pool:    pool,
		regToID: make(map[int]int, len(pool)),
		idToReg: make(map[int]int),
		lru:     nil,
		isFloat: isFloat,
		pinned:  make(map[int]bool),
	}
	for _, r := range pool {
		rs.regToID[r] = -1 // free
	}
	return rs
}

// pin marks valueID as non-evictable. The value keeps its register until
// the block finishes. Pinned values are kept out of the LRU list, so they
// are never picked as eviction victims.
func (rs *regState) pin(valueID int) {
	rs.pinned[valueID] = true
	rs.removeLRU(valueID)
}

// findFree returns a free register, or -1 if all are occupied.
func (rs *regState) findFree() int {
	for _, r := range rs.pool {
		if rs.regToID[r] == -1 {
			return r
		}
	}
	return -1
}

// assign maps valueID to register r.
func (rs *regState) assign(valueID, r int) {
	rs.regToID[r] = valueID
	rs.idToReg[valueID] = r
	rs.touchLRU(valueID)
}

// free releases the register held by valueID. Pinned values are immune:
// they retain their register for the full block lifetime.
func (rs *regState) free(valueID int) {
	if rs.pinned[valueID] {
		return
	}
	r, ok := rs.idToReg[valueID]
	if !ok {
		return
	}
	rs.regToID[r] = -1
	delete(rs.idToReg, valueID)
	rs.removeLRU(valueID)
}

// evictLRU evicts the least recently used value, returning its register.
func (rs *regState) evictLRU() (reg int, evictedID int) {
	if len(rs.lru) == 0 {
		return -1, -1
	}
	evictedID = rs.lru[0]
	reg = rs.idToReg[evictedID]
	rs.regToID[reg] = -1
	delete(rs.idToReg, evictedID)
	rs.lru = rs.lru[1:]
	return reg, evictedID
}

// touchLRU moves valueID to the end of the LRU list (most recently used).
// Pinned values are NOT re-added to the LRU list; they stay out-of-band
// so evictLRU never considers them.
func (rs *regState) touchLRU(valueID int) {
	rs.removeLRU(valueID)
	if rs.pinned[valueID] {
		return
	}
	rs.lru = append(rs.lru, valueID)
}

// removeLRU removes valueID from the LRU list.
func (rs *regState) removeLRU(valueID int) {
	for i, id := range rs.lru {
		if id == valueID {
			rs.lru = append(rs.lru[:i], rs.lru[i+1:]...)
			return
		}
	}
}

// allocateBlock performs per-block register allocation.
// Each block starts with a fresh register state (simple per-block model).
//
// Phi handling: All phi instructions in a block are simultaneously live at
// block entry (they represent merged values from predecessor blocks). They
// MUST NOT share physical registers, otherwise the phi moves at the end of
// predecessor blocks would clobber each other.
//
// To enforce this, we pre-allocate registers for ALL phis in the block first,
// WITHOUT calling freeDeadValues between them. This ensures that each phi
// gets a distinct register. After all phis are allocated, we process non-phi
// instructions normally.
func allocateBlock(block *Block, alloc *RegAllocation, lastUse map[int]int, carried map[int]PhysReg) {
	gprs := newRegState(allocatableGPRs[:], false)
	fprs := newRegState(allocatableFPRs[:], true)

	// Pre-populate regstate with loop-header phi assignments so that body
	// SSA results don't reuse the phi's physical register. carriedIDs
	// tracks which IDs were pre-populated so that eviction does NOT delete
	// their global alloc.ValueRegs entry (that entry was set by the
	// defining header's allocation and must remain authoritative).
	carriedIDs := make(map[int]bool, len(carried))
	for valID, pr := range carried {
		var rs *regState
		if pr.IsFloat {
			rs = fprs
		} else {
			rs = gprs
		}
		// Skip if the register is already taken (defensive — shouldn't
		// happen with fresh regstates but guards against future changes).
		if rs.regToID[pr.Reg] != -1 {
			continue
		}
		// Pin FIRST so that the subsequent assign's touchLRU is a no-op.
		// Pinned phis are never eviction candidates: a body instruction
		// cannot take this register and clobber the loop-carried value.
		rs.pin(valID)
		rs.assign(valID, pr.Reg)
		carriedIDs[valID] = true
	}

	// Phase 1: pre-allocate registers for all phi instructions.
	// Do NOT call freeDeadValues between phis -- they are simultaneously live.
	// If a phi was already assigned by preAllocateHeaderPhis (loop headers),
	// honor that assignment by occupying the same register in the fresh
	// regstate rather than allocating a new one.
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			continue
		}

		// Determine which pool to use based on the phi's result type.
		wantFloat := needsFloatReg(instr)
		var rs *regState
		if wantFloat {
			rs = fprs
		} else {
			rs = gprs
		}

		// Honor pre-allocated assignments from preAllocateHeaderPhis.
		if pr, ok := alloc.ValueRegs[instr.ID]; ok {
			if pr.IsFloat == wantFloat && rs.regToID[pr.Reg] == -1 {
				rs.assign(instr.ID, pr.Reg)
				continue
			}
		}
		// Honor pre-committed spill from preAllocateHeaderPhis.
		if _, ok := alloc.SpillSlots[instr.ID]; ok {
			continue
		}

		// Try to allocate a free register.
		r := rs.findFree()
		if r >= 0 {
			rs.assign(instr.ID, r)
			alloc.ValueRegs[instr.ID] = PhysReg{Reg: r, IsFloat: wantFloat}
		} else {
			// All registers full -- we cannot evict another phi (they are all
			// simultaneously live). Spill this phi to a spill slot.
			// Note: evicting the LRU here would evict another phi, which is
			// wrong. So we directly spill this phi.
			alloc.SpillSlots[instr.ID] = alloc.NumSpillSlots
			alloc.NumSpillSlots++
		}
	}

	// Phase 2: process non-phi instructions normally.
	for instrIdx, instr := range block.Instrs {
		// Skip terminators -- they don't produce values.
		if instr.Op.IsTerminator() {
			continue
		}
		// Skip phis -- already allocated in phase 1.
		if instr.Op == OpPhi {
			// Phi arguments are consumed on predecessor edges, not in the
			// header block itself. Freeing them here can incorrectly release
			// another header phi's live register in loop-carried swaps such as
			// a'=b, b'=a+b, forcing per-iteration slot reloads.
			continue
		}

		// Touch input registers so they are "recently used".
		for _, arg := range instr.Args {
			if _, ok := gprs.idToReg[arg.ID]; ok {
				gprs.touchLRU(arg.ID)
			}
			if _, ok := fprs.idToReg[arg.ID]; ok {
				fprs.touchLRU(arg.ID)
			}
		}

		// Determine which pool to use based on the instruction's result type.
		wantFloat := needsFloatReg(instr)
		var rs *regState
		if wantFloat {
			rs = fprs
		} else {
			rs = gprs
		}

		// Try to allocate a free register.
		r := rs.findFree()
		if r >= 0 {
			rs.assign(instr.ID, r)
			alloc.ValueRegs[instr.ID] = PhysReg{Reg: r, IsFloat: wantFloat}
		} else {
			// All registers full -- spill the LRU value.
			r, evictedID := rs.evictLRU()
			if r == -1 {
				// Should not happen if pool is non-empty, but be safe.
				alloc.SpillSlots[instr.ID] = alloc.NumSpillSlots
				alloc.NumSpillSlots++
				continue
			}

			// Spill the evicted value (only if it wasn't already spilled).
			if _, alreadySpilled := alloc.SpillSlots[evictedID]; !alreadySpilled {
				alloc.SpillSlots[evictedID] = alloc.NumSpillSlots
				alloc.NumSpillSlots++
			}
			// The evicted value loses its register -- BUT only delete the
			// global assignment if this value was DEFINED in this block.
			// Pre-populated loop-header phis have their canonical PhysReg
			// set by the header's allocation; evicting locally doesn't
			// invalidate the header's assignment.
			if !carriedIDs[evictedID] {
				delete(alloc.ValueRegs, evictedID)
			}

			// Assign the freed register to the new value.
			rs.assign(instr.ID, r)
			alloc.ValueRegs[instr.ID] = PhysReg{Reg: r, IsFloat: wantFloat}
		}

		// Free registers for values that die at this instruction.
		// A value dies at its last use; we free it after the instruction
		// that uses it last, since the output was already allocated above.
		freeDeadValues(block, instrIdx, alloc, gprs, fprs, lastUse)
	}
}

// freeDeadValues frees registers for values whose last use is at instrIdx.
func freeDeadValues(block *Block, instrIdx int, alloc *RegAllocation, gprs, fprs *regState, lastUse map[int]int) {
	instr := block.Instrs[instrIdx]
	// Check all input args -- if this instruction is their last use, free them.
	for _, arg := range instr.Args {
		lu, ok := lastUse[arg.ID]
		if !ok {
			continue
		}
		if lu == instr.ID {
			gprs.free(arg.ID)
			fprs.free(arg.ID)
		}
	}
}

// needsFloatReg returns true if the instruction's result should go in an FPR.
// Note: Float COMPARISON ops (OpLtFloat, OpLeFloat) produce boolean results
// (NaN-boxed bool), NOT float results, so they should NOT get FPR allocations.
func needsFloatReg(instr *Instr) bool {
	// Comparisons produce bools, not floats, regardless of operand type.
	switch instr.Op {
	case OpLtFloat, OpLeFloat:
		return false
	}
	if instr.Type == TypeFloat {
		return true
	}
	switch instr.Op {
	case OpConstFloat, OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpUnboxFloat, OpBoxFloat:
		return true
	}
	return false
}

// computeLastUse computes, for every value ID, the ID of the instruction that
// uses it last (across all blocks). This is a simple whole-function liveness
// approximation: the last instruction (by ID) that references a value as an arg.
func computeLastUse(fn *Function) map[int]int {
	lastUse := make(map[int]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				// Update: this instruction (instr.ID) uses arg.ID.
				// We want the maximum instruction ID that uses each value.
				if existing, ok := lastUse[arg.ID]; !ok || instr.ID > existing {
					lastUse[arg.ID] = instr.ID
				}
			}
		}
	}
	return lastUse
}
