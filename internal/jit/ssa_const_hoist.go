//go:build darwin && arm64

package jit

// constHoistImpl hoists loop-invariant constants (CONST_INT, CONST_FLOAT,
// CONST_BOOL, CONST_NIL) from the loop body to just before the SSA_LOOP
// marker. After moving instructions, it rebuilds a remapping table
// (oldIdx -> newIdx) and rewrites all SSARef references: Arg1, Arg2,
// AuxInt (for ops that use it as a ref), and Snapshot entries.

func constHoistImpl(f *SSAFunc) *SSAFunc {
	// Find the LOOP marker.
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		return f
	}

	// Collect indices of CONST_* instructions in the loop body (after LOOP).
	// Only hoist constants with Slot < 0 (pool constants not bound to a VM slot).
	// Constants with a valid slot (>= 0) write to that slot during loop-body
	// emission (emitConstInt/emitConstFloat), which is needed for correct
	// interpreter state when side-exiting. Hoisting them would remove that
	// write, causing the interpreter to see a stale slot value after side-exit.
	var toHoist []int
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]
		switch inst.Op {
		case SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_BOOL, SSA_CONST_NIL:
			if inst.Slot < 0 {
				toHoist = append(toHoist, i)
			}
		}
	}
	if len(toHoist) == 0 {
		return f
	}

	// Build the new instruction list:
	//   [pre-loop instructions] + [hoisted constants] + [LOOP] + [remaining loop body]
	//
	// Strategy: partition instructions into three groups:
	//   1. pre-loop (indices 0..loopIdx-1)
	//   2. LOOP marker (index loopIdx)
	//   3. loop body (indices loopIdx+1..end), minus the hoisted constants

	hoistSet := make(map[int]bool, len(toHoist))
	for _, idx := range toHoist {
		hoistSet[idx] = true
	}

	newInsts := make([]SSAInst, 0, len(f.Insts))

	// oldToNew maps old instruction index to new instruction index.
	oldToNew := make([]int, len(f.Insts))

	// 1. Pre-loop instructions (unchanged order).
	for i := 0; i < loopIdx; i++ {
		oldToNew[i] = len(newInsts)
		newInsts = append(newInsts, f.Insts[i])
	}

	// 2. Hoisted constants (inserted just before LOOP).
	for _, idx := range toHoist {
		oldToNew[idx] = len(newInsts)
		newInsts = append(newInsts, f.Insts[idx])
	}

	// 3. LOOP marker.
	oldToNew[loopIdx] = len(newInsts)
	newInsts = append(newInsts, f.Insts[loopIdx])

	// 4. Remaining loop body (excluding hoisted constants).
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		if hoistSet[i] {
			continue // already placed above
		}
		oldToNew[i] = len(newInsts)
		newInsts = append(newInsts, f.Insts[i])
	}

	// Helper to remap an SSARef using the oldToNew table.
	remapRef := func(ref SSARef) SSARef {
		if ref == SSARefNone || ref < 0 {
			return ref
		}
		if int(ref) < len(oldToNew) {
			return SSARef(oldToNew[int(ref)])
		}
		return ref
	}

	// Rewrite all references in the new instruction list.
	for i := range newInsts {
		inst := &newInsts[i]

		// Rewrite Arg1 and Arg2.
		inst.Arg1 = remapRef(inst.Arg1)
		inst.Arg2 = remapRef(inst.Arg2)

		// Rewrite AuxInt for ops that use it as an SSA ref.
		if auxIntIsRef(inst.Op) && inst.AuxInt >= 0 {
			inst.AuxInt = int64(remapRef(SSARef(inst.AuxInt)))
		}
	}

	// Rewrite snapshot entries.
	newSnapshots := make([]Snapshot, len(f.Snapshots))
	for si, snap := range f.Snapshots {
		newEntries := make([]SnapEntry, len(snap.Entries))
		for ei, entry := range snap.Entries {
			newEntries[ei] = SnapEntry{
				Slot: entry.Slot,
				Ref:  remapRef(entry.Ref),
				Type: entry.Type,
			}
		}
		newSnapshots[si] = Snapshot{
			PC:      snap.PC,
			Entries: newEntries,
		}
	}

	// Update LoopIdx.
	newLoopIdx := oldToNew[loopIdx]

	return &SSAFunc{
		Insts:        newInsts,
		Snapshots:    newSnapshots,
		Trace:        f.Trace,
		LoopIdx:      newLoopIdx,
		AbsorbedMuls: f.AbsorbedMuls,
	}
}

// auxIntIsRef is defined in ssa_opt.go — shared helper that returns true
// when the given op uses AuxInt as an SSA ref.
