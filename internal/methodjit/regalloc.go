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
//   D4-D11:  allocatable FPRs (callee-saved, 8 registers)

package methodjit

// Allocatable GPR pool: X20, X21, X22, X23, X28.
// X19 is reserved for the ExecContext pointer (emit.go pinned register).
// X28 was previously reserved for trace JIT self-call overflow, but
// self-calls are removed in the Method JIT, freeing X28 as a 5th GPR.
var allocatableGPRs = [5]int{20, 21, 22, 23, 28}

// Allocatable FPR pool: D4, D5, D6, D7, D8, D9, D10, D11.
var allocatableFPRs = [8]int{4, 5, 6, 7, 8, 9, 10, 11}

// PhysReg represents a physical ARM64 register.
type PhysReg struct {
	Reg     int  // register number (X19=19, D4=4, etc.)
	IsFloat bool // true for FPR (D4-D11), false for GPR (X19-X23)
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

	for _, block := range fn.Blocks {
		allocateBlock(block, alloc, lastUse)
	}

	return alloc
}

// regState tracks the current state of a register pool (GPR or FPR).
type regState struct {
	pool    []int          // allocatable register numbers
	regToID map[int]int    // register number -> value ID currently held (-1 if free)
	idToReg map[int]int    // value ID -> register number
	lru     []int          // value IDs in order of last use (oldest first)
	isFloat bool           // true for FPR pool
}

func newRegState(pool []int, isFloat bool) *regState {
	rs := &regState{
		pool:    pool,
		regToID: make(map[int]int, len(pool)),
		idToReg: make(map[int]int),
		lru:     nil,
		isFloat: isFloat,
	}
	for _, r := range pool {
		rs.regToID[r] = -1 // free
	}
	return rs
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

// free releases the register held by valueID.
func (rs *regState) free(valueID int) {
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
func (rs *regState) touchLRU(valueID int) {
	rs.removeLRU(valueID)
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
func allocateBlock(block *Block, alloc *RegAllocation, lastUse map[int]int) {
	gprs := newRegState(allocatableGPRs[:], false)
	fprs := newRegState(allocatableFPRs[:], true)

	// Phase 1: pre-allocate registers for all phi instructions.
	// Do NOT call freeDeadValues between phis -- they are simultaneously live.
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
			// Still need to free dead values after this instruction's position
			// for args that had their last use at this phi. However, phi args
			// come from predecessor blocks, not from this block, so typically
			// no values in our regState need to be freed here. But to be safe
			// and consistent with the old behavior, call freeDeadValues.
			freeDeadValues(block, instrIdx, alloc, gprs, fprs, lastUse)
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
			// The evicted value loses its register.
			delete(alloc.ValueRegs, evictedID)

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
