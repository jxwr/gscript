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

import "sort"

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
	// LoopInvariantGPRs maps loop header block ID -> SSA value ID -> physical
	// GPR for selected loop-invariant values that should stay resident across
	// that loop. It is intentionally narrow today: table-array len/data facts
	// only.
	LoopInvariantGPRs map[int]map[int]PhysReg
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
	valueDefs := computeValueDefs(fn)
	blockLiveIn, _ := computeBlockLiveness(fn)
	rawIntBlockCarry := enableSinglePredRawIntCarry(fn)

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

	if fn.CarryPreheaderInvariants {
		alloc.LoopInvariantGPRs = assignLoopTableArrayInvariantGPRs(fn, li, alloc)
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

	// Raw-int single-predecessor carry: after a block is allocated, remember
	// its final GPR contents. A successor with exactly one predecessor can pin
	// raw-int values that are live into that successor so local allocation does
	// not reuse their physical registers before emission can read them.
	blockOutGPRs := make(map[int]map[int]PhysReg, len(fn.Blocks))

	for _, block := range fn.Blocks {
		// After allocating a pre-header block, collect FPR assignments
		// for invariant candidates from alloc.ValueRegs (set naturally by
		// the pre-header's allocateBlock). This avoids pre-allocating FPRs
		// that allocateBlock would overwrite.
		var carried map[int]PhysReg
		var temporaryCarried map[int]bool
		if li.loopBlocks[block.ID] && !li.loopHeaders[block.ID] {
			if innerHeader, ok := li.blockInnerHeader[block.ID]; ok {
				// Phi carry: only for tight-body headers (existing logic).
				if tightHeaders[innerHeader] {
					if carried == nil {
						carried = make(map[int]PhysReg)
					}
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
		if rawIntBlockCarry && len(block.Preds) == 1 && !li.loopHeaders[block.ID] {
			predID := block.Preds[0].ID
			if predOut := blockOutGPRs[predID]; len(predOut) > 0 {
				liveIn := blockLiveIn[block.ID]
				ids := make([]int, 0, len(predOut))
				for valueID := range predOut {
					ids = append(ids, valueID)
				}
				sort.Ints(ids)
				for _, valueID := range ids {
					if !liveIn[valueID] || !isRawIntCarryValue(valueDefs[valueID]) {
						continue
					}
					pr := predOut[valueID]
					if pr.IsFloat {
						continue
					}
					if canonical, ok := alloc.ValueRegs[valueID]; !ok || canonical != pr {
						continue
					}
					if carriedRegTaken(carried, pr) {
						continue
					}
					if carried == nil {
						carried = make(map[int]PhysReg)
					}
					carried[valueID] = pr
					if temporaryCarried == nil {
						temporaryCarried = make(map[int]bool)
					}
					temporaryCarried[valueID] = true
				}
			}
		}
		if li.loopBlocks[block.ID] && len(alloc.LoopInvariantGPRs) > 0 {
			carried = addLoopInvariantGPRCarry(block, li, alloc, carried)
		}
		blockOutGPRs[block.ID] = allocateBlock(block, alloc, lastUse, carried, temporaryCarried)

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

func carriedRegTaken(carried map[int]PhysReg, pr PhysReg) bool {
	for _, existing := range carried {
		if existing.IsFloat == pr.IsFloat && existing.Reg == pr.Reg {
			return true
		}
	}
	return false
}

func enableSinglePredRawIntCarry(fn *Function) bool {
	if fn == nil {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpCall && instr.Type == TypeInt {
				return true
			}
		}
	}
	return false
}

func computeValueDefs(fn *Function) map[int]*Instr {
	defs := make(map[int]*Instr)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defs[instr.ID] = instr
			}
		}
	}
	return defs
}

func isRawIntCarryValue(instr *Instr) bool {
	if instr == nil || instr.Type != TypeInt {
		return false
	}
	if isRawIntOp(instr.Op) {
		return true
	}
	switch instr.Op {
	case OpConstInt, OpLoadSlot, OpGuardType, OpCall, OpPhi:
		return true
	default:
		return false
	}
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

func assignLoopTableArrayInvariantGPRs(fn *Function, li *loopInfo, alloc *RegAllocation) map[int]map[int]PhysReg {
	if fn == nil || li == nil || !li.hasLoops() || alloc == nil {
		return nil
	}
	defs := make(map[int]*Instr)
	defBlocks := make(map[int]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			defs[instr.ID] = instr
			defBlocks[instr.ID] = block.ID
		}
	}
	dom := computeDominators(fn)
	headers := sortedLoopHeaders(li)
	out := make(map[int]map[int]PhysReg)
	for _, headerID := range headers {
		body := li.headerBlocks[headerID]
		if body == nil {
			continue
		}
		useCounts := make(map[int]int)
		for _, block := range fn.Blocks {
			if !body[block.ID] {
				continue
			}
			for _, instr := range block.Instrs {
				switch instr.Op {
				case OpTableArrayLoad:
					if len(instr.Args) >= 2 {
						recordTableArrayInvariantCandidate(instr.Args[0], body, headerID, defs, defBlocks, dom, useCounts)
						recordTableArrayInvariantCandidate(instr.Args[1], body, headerID, defs, defBlocks, dom, useCounts)
					}
				case OpTableArrayNestedLoad:
					if len(instr.Args) >= 2 {
						recordTableArrayInvariantCandidate(instr.Args[0], body, headerID, defs, defBlocks, dom, useCounts)
						recordTableArrayInvariantCandidate(instr.Args[1], body, headerID, defs, defBlocks, dom, useCounts)
					}
					if len(instr.Args) >= 3 {
						recordTableArrayInvariantCandidate(instr.Args[2], body, headerID, defs, defBlocks, dom, useCounts)
					}
				}
			}
		}
		if len(useCounts) == 0 {
			continue
		}

		candidates := make([]int, 0, len(useCounts))
		for id := range useCounts {
			candidates = append(candidates, id)
		}
		sortTableArrayInvariantCandidates(candidates, useCounts, defs)

		usedRegs := make(map[int]bool)
		for _, phiID := range li.loopPhis[headerID] {
			if pr, ok := alloc.ValueRegs[phiID]; ok && !pr.IsFloat {
				usedRegs[pr.Reg] = true
			}
		}

		const maxTableArrayGPRInvariants = 2
		for _, id := range candidates {
			if len(out[headerID]) >= maxTableArrayGPRInvariants {
				break
			}
			var pr PhysReg
			if existing, ok := alloc.ValueRegs[id]; ok && !existing.IsFloat && !usedRegs[existing.Reg] {
				pr = existing
			} else {
				reg, ok := firstFreeGPR(usedRegs)
				if !ok {
					break
				}
				pr = PhysReg{Reg: reg, IsFloat: false}
				alloc.ValueRegs[id] = pr
			}
			usedRegs[pr.Reg] = true
			if out[headerID] == nil {
				out[headerID] = make(map[int]PhysReg)
			}
			out[headerID][id] = pr
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func recordTableArrayInvariantCandidate(v *Value, body map[int]bool, headerID int, defs map[int]*Instr, defBlocks map[int]int, dom *domInfo, useCounts map[int]int) {
	if v == nil || dom == nil {
		return
	}
	def := defs[v.ID]
	if def == nil || !isTableArrayGPRInvariant(def) {
		return
	}
	defBlock, ok := defBlocks[v.ID]
	if !ok || body[defBlock] || !dom.dominates(defBlock, headerID) {
		return
	}
	useCounts[v.ID]++
}

func isTableArrayGPRInvariant(instr *Instr) bool {
	if instr == nil || instr.Type != TypeInt {
		return false
	}
	switch instr.Op {
	case OpTableArrayLen, OpTableArrayData:
		return true
	default:
		return false
	}
}

func sortTableArrayInvariantCandidates(ids []int, useCounts map[int]int, defs map[int]*Instr) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0; j-- {
			a, b := ids[j-1], ids[j]
			if tableArrayInvariantLess(b, a, useCounts, defs) {
				ids[j-1], ids[j] = ids[j], ids[j-1]
			} else {
				break
			}
		}
	}
}

func tableArrayInvariantLess(a, b int, useCounts map[int]int, defs map[int]*Instr) bool {
	if useCounts[a] != useCounts[b] {
		return useCounts[a] > useCounts[b]
	}
	ra := tableArrayInvariantRank(defs[a])
	rb := tableArrayInvariantRank(defs[b])
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func tableArrayInvariantRank(instr *Instr) int {
	if instr != nil && instr.Op == OpTableArrayData {
		return 0
	}
	return 1
}

func firstFreeGPR(used map[int]bool) (int, bool) {
	for _, reg := range allocatableGPRs {
		if !used[reg] {
			return reg, true
		}
	}
	return 0, false
}

func sortedLoopHeaders(li *loopInfo) []int {
	headers := make([]int, 0, len(li.loopHeaders))
	for id := range li.loopHeaders {
		headers = append(headers, id)
	}
	for i := 1; i < len(headers); i++ {
		for j := i; j > 0 && headers[j-1] > headers[j]; j-- {
			headers[j-1], headers[j] = headers[j], headers[j-1]
		}
	}
	return headers
}

func addLoopInvariantGPRCarry(block *Block, li *loopInfo, alloc *RegAllocation, carried map[int]PhysReg) map[int]PhysReg {
	if block == nil || li == nil || alloc == nil || len(alloc.LoopInvariantGPRs) == 0 {
		return carried
	}
	usedRegs := make(map[int]bool)
	for _, pr := range carried {
		if !pr.IsFloat {
			usedRegs[pr.Reg] = true
		}
	}
	for _, headerID := range sortedLoopHeaders(li) {
		body := li.headerBlocks[headerID]
		if body == nil || !body[block.ID] {
			continue
		}
		ids := sortedInvariantIDs(alloc.LoopInvariantGPRs[headerID])
		for _, id := range ids {
			pr := alloc.LoopInvariantGPRs[headerID][id]
			if pr.IsFloat || usedRegs[pr.Reg] {
				continue
			}
			if carried == nil {
				carried = make(map[int]PhysReg)
			}
			carried[id] = pr
			usedRegs[pr.Reg] = true
		}
	}
	return carried
}

func isLoopInvariantGPRValue(alloc *RegAllocation, valueID int) bool {
	if alloc == nil {
		return false
	}
	for _, values := range alloc.LoopInvariantGPRs {
		if _, ok := values[valueID]; ok {
			return true
		}
	}
	return false
}

func updateLoopInvariantGPRReg(alloc *RegAllocation, valueID int, pr PhysReg) {
	if alloc == nil || pr.IsFloat {
		return
	}
	for _, values := range alloc.LoopInvariantGPRs {
		if _, ok := values[valueID]; ok {
			values[valueID] = pr
		}
	}
}

func sortedInvariantIDs(m map[int]PhysReg) []int {
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

func (rs *regState) unpin(valueID int) {
	delete(rs.pinned, valueID)
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

func (rs *regState) assignPreferred(valueID, reg int) bool {
	if _, ok := rs.regToID[reg]; !ok {
		return false
	}
	if existingID := rs.regToID[reg]; existingID >= 0 && existingID != valueID {
		return false
	}
	rs.assign(valueID, reg)
	return true
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
func allocateBlock(block *Block, alloc *RegAllocation, lastUse map[int]int, carried map[int]PhysReg, temporaryCarried map[int]bool) map[int]PhysReg {
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
		// Pinned values are never eviction candidates while live: a body
		// instruction cannot take this register and clobber the carried value.
		// Single-predecessor carries are unpinned at their last use below;
		// loop/header carries remain pinned for the full block.
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
		freeTemporaryCarriedInputs(instr, gprs, fprs, lastUse, temporaryCarried)

		// Determine which pool to use based on the instruction's result type.
		wantFloat := needsFloatReg(instr)
		var rs *regState
		if wantFloat {
			rs = fprs
		} else {
			rs = gprs
		}

		if pr, ok := alloc.ValueRegs[instr.ID]; ok && pr.IsFloat == wantFloat {
			if rs.assignPreferred(instr.ID, pr.Reg) {
				if !wantFloat && isLoopInvariantGPRValue(alloc, instr.ID) {
					updateLoopInvariantGPRReg(alloc, instr.ID, pr)
					rs.pin(instr.ID)
				}
				freeDeadValues(block, instrIdx, alloc, gprs, fprs, lastUse, temporaryCarried)
				continue
			}
		}

		// Try to allocate a free register.
		r := rs.findFree()
		if r >= 0 {
			rs.assign(instr.ID, r)
			pr := PhysReg{Reg: r, IsFloat: wantFloat}
			alloc.ValueRegs[instr.ID] = pr
			if !wantFloat && isLoopInvariantGPRValue(alloc, instr.ID) {
				updateLoopInvariantGPRReg(alloc, instr.ID, pr)
				rs.pin(instr.ID)
			}
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
			pr := PhysReg{Reg: r, IsFloat: wantFloat}
			alloc.ValueRegs[instr.ID] = pr
			if !wantFloat && isLoopInvariantGPRValue(alloc, instr.ID) {
				updateLoopInvariantGPRReg(alloc, instr.ID, pr)
				rs.pin(instr.ID)
			}
		}

		// Free registers for values that die at this instruction.
		// A value dies at its last use; we free it after the instruction
		// that uses it last, since the output was already allocated above.
		freeDeadValues(block, instrIdx, alloc, gprs, fprs, lastUse, temporaryCarried)
	}
	return gprs.snapshot(false)
}

func freeTemporaryCarriedInputs(instr *Instr, gprs, fprs *regState, lastUse map[int]int, temporaryCarried map[int]bool) {
	if len(temporaryCarried) == 0 {
		return
	}
	for _, arg := range instr.Args {
		if arg == nil || !temporaryCarried[arg.ID] || lastUse[arg.ID] != instr.ID {
			continue
		}
		gprs.unpin(arg.ID)
		fprs.unpin(arg.ID)
		gprs.free(arg.ID)
		fprs.free(arg.ID)
		delete(temporaryCarried, arg.ID)
	}
}

// freeDeadValues frees registers for values whose last use is at instrIdx.
func freeDeadValues(block *Block, instrIdx int, alloc *RegAllocation, gprs, fprs *regState, lastUse map[int]int, temporaryCarried map[int]bool) {
	instr := block.Instrs[instrIdx]
	// Check all input args -- if this instruction is their last use, free them.
	for _, arg := range instr.Args {
		lu, ok := lastUse[arg.ID]
		if !ok {
			continue
		}
		if lu == instr.ID {
			if temporaryCarried[arg.ID] {
				gprs.unpin(arg.ID)
				fprs.unpin(arg.ID)
				delete(temporaryCarried, arg.ID)
			}
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

func (rs *regState) snapshot(isFloat bool) map[int]PhysReg {
	out := make(map[int]PhysReg, len(rs.idToReg))
	for valueID, reg := range rs.idToReg {
		out[valueID] = PhysReg{Reg: reg, IsFloat: isFloat}
	}
	return out
}

func computeBlockLiveness(fn *Function) (map[int]map[int]bool, map[int]map[int]bool) {
	use := make(map[int]map[int]bool, len(fn.Blocks))
	def := make(map[int]map[int]bool, len(fn.Blocks))

	for _, block := range fn.Blocks {
		useSet := make(map[int]bool)
		defSet := make(map[int]bool)
		definedSoFar := make(map[int]bool)
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi {
				defSet[instr.ID] = true
				definedSoFar[instr.ID] = true
			}
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi {
				continue
			}
			for _, arg := range instr.Args {
				if arg != nil && !definedSoFar[arg.ID] {
					useSet[arg.ID] = true
				}
			}
			if !instr.Op.IsTerminator() {
				defSet[instr.ID] = true
				definedSoFar[instr.ID] = true
			}
		}
		use[block.ID] = useSet
		def[block.ID] = defSet
	}

	liveIn := make(map[int]map[int]bool, len(fn.Blocks))
	liveOut := make(map[int]map[int]bool, len(fn.Blocks))
	for _, block := range fn.Blocks {
		liveIn[block.ID] = make(map[int]bool)
		liveOut[block.ID] = make(map[int]bool)
	}

	changed := true
	for changed {
		changed = false
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			block := fn.Blocks[i]
			nextOut := make(map[int]bool)
			for _, succ := range block.Succs {
				for valueID := range liveIn[succ.ID] {
					nextOut[valueID] = true
				}
				predIdx := -1
				for i, pred := range succ.Preds {
					if pred == block {
						predIdx = i
						break
					}
				}
				if predIdx >= 0 {
					for _, instr := range succ.Instrs {
						if instr.Op != OpPhi {
							break
						}
						if predIdx < len(instr.Args) && instr.Args[predIdx] != nil {
							nextOut[instr.Args[predIdx].ID] = true
						}
					}
				}
			}

			nextIn := make(map[int]bool, len(use[block.ID])+len(nextOut))
			for valueID := range use[block.ID] {
				nextIn[valueID] = true
			}
			for valueID := range nextOut {
				if !def[block.ID][valueID] {
					nextIn[valueID] = true
				}
			}

			if !sameBoolSet(liveOut[block.ID], nextOut) {
				liveOut[block.ID] = nextOut
				changed = true
			}
			if !sameBoolSet(liveIn[block.ID], nextIn) {
				liveIn[block.ID] = nextIn
				changed = true
			}
		}
	}

	return liveIn, liveOut
}

func computeInstrLiveAfter(fn *Function, blockLiveOut map[int]map[int]bool) map[int]map[int]bool {
	liveAfter := make(map[int]map[int]bool)
	for _, block := range fn.Blocks {
		live := cloneIntBoolSet(blockLiveOut[block.ID])
		for i := len(block.Instrs) - 1; i >= 0; i-- {
			instr := block.Instrs[i]
			liveAfter[instr.ID] = cloneIntBoolSet(live)
			if instr.Op != OpPhi && !instr.Op.IsTerminator() {
				delete(live, instr.ID)
			}
			if instr.Op != OpPhi {
				for _, arg := range instr.Args {
					if arg != nil {
						live[arg.ID] = true
					}
				}
			}
		}
	}
	return liveAfter
}

func computeSinglePredRawIntStoreElision(fn *Function, alloc *RegAllocation, blockLiveIn map[int]map[int]bool) map[int]bool {
	defs := computeValueDefs(fn)
	defBlock := make(map[int]int, len(defs))
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if !instr.Op.IsTerminator() {
				defBlock[instr.ID] = block.ID
			}
		}
	}

	result := make(map[int]bool)
	for valueID, def := range defs {
		if !isRawIntCarryValue(def) {
			continue
		}
		pr, ok := alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		db, ok := defBlock[valueID]
		if !ok {
			continue
		}
		hasCrossUse := false
		eligible := true
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpPhi {
					for _, arg := range instr.Args {
						if arg != nil && arg.ID == valueID {
							hasCrossUse = true
							eligible = false
							break
						}
					}
					if !eligible {
						break
					}
					continue
				}
				for _, arg := range instr.Args {
					if arg == nil || arg.ID != valueID || block.ID == db {
						continue
					}
					hasCrossUse = true
					if len(block.Preds) != 1 || block.Preds[0].ID != db || !blockLiveIn[block.ID][valueID] {
						eligible = false
						break
					}
				}
				if !eligible {
					break
				}
			}
			if !eligible {
				break
			}
		}
		if hasCrossUse && eligible {
			result[valueID] = true
		}
	}
	return result
}

func cloneIntBoolSet(in map[int]bool) map[int]bool {
	out := make(map[int]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sameBoolSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if b[k] != av {
			return false
		}
	}
	return true
}
