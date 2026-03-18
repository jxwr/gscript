//go:build darwin && arm64

package jit

// LiveInfo holds liveness analysis results for an SSAFunc.
type LiveInfo struct {
	// WrittenSlots is the set of VM slots modified inside the loop body.
	// These slots must be stored back to memory on loop exit/side-exit.
	WrittenSlots map[int]bool

	// SlotTypes maps written slots to their SSA type (Int, Float, etc.)
	// so the store-back knows whether to use integer or float store.
	SlotTypes map[int]SSAType
}

// NeedsStoreBack returns true if the given slot should be written back
// to the VM register array on trace exit.
func (li *LiveInfo) NeedsStoreBack(slot int) bool {
	return li.WrittenSlots[slot]
}

// AnalyzeLiveness computes which VM slots are modified inside the loop body
// and need to be stored back to the VM register array on exit.
// This replaces the ad-hoc ssaWrittenSlots mechanism.
func AnalyzeLiveness(f *SSAFunc) *LiveInfo {
	li := &LiveInfo{
		WrittenSlots: make(map[int]bool),
		SlotTypes:    make(map[int]SSAType),
	}

	// Step 1: Find the SSA_LOOP marker index.
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		// No loop marker — nothing to analyze.
		return li
	}

	// Step 2: Walk instructions after SSA_LOOP.
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		// Step 4: Stop at SSA_SIDE_EXIT — everything after is unreachable.
		if inst.Op == SSA_SIDE_EXIT {
			break
		}

		// Step 5: Skip NOPs (dead code).
		if inst.Op == SSA_NOP {
			continue
		}

		// Step 3: Check if this op produces a value that modifies a VM slot.
		if !isValueProducingOp(inst.Op) {
			continue
		}

		// Only include instructions with a valid VM slot (Slot >= 0).
		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}

		li.WrittenSlots[slot] = true
		li.SlotTypes[slot] = inst.Type
	}

	return li
}

// isValueProducingOp returns true if the SSA opcode produces a value that
// modifies VM state (i.e., writes a result to a VM slot). Guards, comparisons,
// loads from memory (LOAD_SLOT), stores to memory, and control flow ops are
// excluded.
func isValueProducingOp(op SSAOp) bool {
	switch op {
	// Integer arithmetic
	case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT:
		return true
	// Float arithmetic
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT:
		return true
	// Data movement / boxing
	case SSA_MOVE, SSA_UNBOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_INT, SSA_BOX_FLOAT:
		return true
	// Table reads (produce a value into a VM slot)
	case SSA_LOAD_FIELD, SSA_LOAD_ARRAY, SSA_TABLE_LEN:
		return true
	// Constants inside loop body
	case SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_BOOL:
		return true
	// Intrinsics (inlined Go functions)
	case SSA_INTRINSIC:
		return true
	}
	return false
}
