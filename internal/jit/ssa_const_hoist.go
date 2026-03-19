//go:build darwin && arm64

package jit

// ConstHoist moves loop-invariant constants (SSA_CONST_INT, SSA_CONST_FLOAT)
// from inside the loop body to the pre-loop section (before SSA_LOOP marker).
// This eliminates constant rematerialization on every iteration.
//
// After moving instructions, all SSARef references (Arg1, Arg2, and AuxInt
// for SSA_STORE_ARRAY) are updated to reflect the new instruction indices.
func ConstHoist(f *SSAFunc) *SSAFunc {
	// Find the LOOP marker
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		// No loop marker — nothing to do
		return f
	}

	// Collect slots that are written by non-constant ops in the loop body.
	// Constants for these slots must NOT be hoisted because the slot is
	// modified during the loop and the constant re-initializes it.
	writtenByNonConst := make(map[int]bool)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		slot := int(inst.Slot)
		if slot < 0 {
			continue
		}
		switch inst.Op {
		case SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL, SSA_NOP:
			// These are the constants themselves, don't count
		default:
			if isValueProducingOp(inst.Op) || inst.Op == SSA_CALL_INNER_TRACE {
				writtenByNonConst[slot] = true
			}
		}
	}

	// Collect indices of constants that are after the LOOP marker
	// and whose slots are safe to hoist (not written by non-constant ops)
	var constIndices []int
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		op := f.Insts[i].Op
		if op == SSA_CONST_INT || op == SSA_CONST_FLOAT {
			slot := int(f.Insts[i].Slot)
			if slot >= 0 && writtenByNonConst[slot] {
				// This constant's slot is also written by a non-constant op.
				// Don't hoist — the constant needs to re-initialize the slot
				// on every iteration (e.g., inner loop control registers).
				continue
			}
			constIndices = append(constIndices, i)
		}
	}

	if len(constIndices) == 0 {
		// No constants to hoist
		return f
	}

	// Build the new instruction list:
	//   1. Pre-loop instructions (before LOOP)
	//   2. Hoisted constants (extracted from after LOOP)
	//   3. LOOP marker
	//   4. Remaining loop body instructions (non-constant)
	newInsts := make([]SSAInst, 0, len(f.Insts))

	// Track which old indices are constants being hoisted
	isHoisted := make(map[int]bool, len(constIndices))
	for _, idx := range constIndices {
		isHoisted[idx] = true
	}

	// Step 1: Copy pre-loop instructions (indices 0..loopIdx-1)
	for i := 0; i < loopIdx; i++ {
		newInsts = append(newInsts, f.Insts[i])
	}

	// Step 2: Insert hoisted constants (just before LOOP)
	for _, idx := range constIndices {
		newInsts = append(newInsts, f.Insts[idx])
	}

	// Step 3: LOOP marker
	newInsts = append(newInsts, f.Insts[loopIdx])

	// Step 4: Remaining loop body (skip hoisted constants)
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		if !isHoisted[i] {
			newInsts = append(newInsts, f.Insts[i])
		}
	}

	// Build the old→new index mapping
	oldToNew := make(map[int]int, len(f.Insts))

	// Pre-loop instructions keep the same index
	newIdx := 0
	for i := 0; i < loopIdx; i++ {
		oldToNew[i] = newIdx
		newIdx++
	}

	// Hoisted constants get new positions
	for _, idx := range constIndices {
		oldToNew[idx] = newIdx
		newIdx++
	}

	// LOOP marker
	oldToNew[loopIdx] = newIdx
	newIdx++

	// Remaining loop body
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		if !isHoisted[i] {
			oldToNew[i] = newIdx
			newIdx++
		}
	}

	// Rewrite all SSARef references using the mapping
	for i := range newInsts {
		inst := &newInsts[i]
		inst.Arg1 = remapRef(inst.Arg1, oldToNew)
		inst.Arg2 = remapRef(inst.Arg2, oldToNew)

		// SSA_STORE_ARRAY stores a value ref in AuxInt
		if inst.Op == SSA_STORE_ARRAY {
			valRef := SSARef(inst.AuxInt)
			inst.AuxInt = int64(remapRef(valRef, oldToNew))
		}
	}

	return &SSAFunc{
		Insts: newInsts,
		Trace: f.Trace,
	}
}

// remapRef translates an SSARef from old indices to new indices.
// SSARefNone and negative refs (not SSARefNone) are left unchanged.
func remapRef(ref SSARef, oldToNew map[int]int) SSARef {
	if ref == SSARefNone || ref < 0 {
		return ref
	}
	if newIdx, ok := oldToNew[int(ref)]; ok {
		return SSARef(newIdx)
	}
	return ref
}
