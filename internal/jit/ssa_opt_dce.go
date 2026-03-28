//go:build darwin && arm64

package jit

// DCE (Dead Code Elimination) removes SSA instructions whose results are
// never used by any other instruction or snapshot. This eliminates dead code
// such as GETGLOBAL + GETFIELD sequences that prepare function values for
// calls that were inlined as intrinsics.
//
// Only side-effect-free instructions are removable. Instructions with side
// effects (stores, guards, calls, control flow) are always kept.
func DCE(f *SSAFunc) *SSAFunc {
	n := len(f.Insts)
	if n == 0 {
		return f
	}

	// Step 1: Count uses of each SSARef.
	uses := make([]int, n)
	for i := range f.Insts {
		inst := &f.Insts[i]
		if inst.Arg1 != SSARefNone && int(inst.Arg1) < n {
			uses[int(inst.Arg1)]++
		}
		if inst.Arg2 != SSARefNone && int(inst.Arg2) < n {
			uses[int(inst.Arg2)]++
		}
		// AuxInt as SSARef (STORE_ARRAY, FMADD, FMSUB)
		if auxIntIsRef(inst.Op) && inst.AuxInt >= 0 && int(inst.AuxInt) < n {
			uses[int(inst.AuxInt)]++
		}
	}

	// Snapshot entries also count as uses.
	for _, snap := range f.Snapshots {
		for _, entry := range snap.Entries {
			if entry.Ref != SSARefNone && int(entry.Ref) < n {
				uses[int(entry.Ref)]++
			}
		}
	}

	// For function traces, the return value slot is implicitly "used" —
	// it's the function's return value. Find instructions that write to the
	// return slot and mark them as used so DCE won't remove them.
	if f.Trace != nil && f.Trace.IsFuncTrace {
		retSlot := int16(f.Trace.FuncReturnSlot)
		for i := range f.Insts {
			inst := &f.Insts[i]
			if inst.Slot == retSlot && inst.Op != SSA_NOP && inst.Op != SSA_SNAPSHOT && inst.Op != SSA_LOOP {
				uses[i]++ // keep alive
			}
		}
	}

	// Step 2: Mark dead instructions (zero uses, no side effects).
	// Only eliminate instructions in the LOOP BODY (after SSA_LOOP).
	// Pre-loop instructions (LOAD_SLOT, UNBOX, GUARD) initialize register
	// state and must not be removed even if they appear "unused" in SSA terms.
	loopStart := f.LoopIdx + 1
	if loopStart <= 0 {
		return f // no loop found
	}

	changed := true
	for changed {
		changed = false
		for i := loopStart; i < n; i++ {
			inst := &f.Insts[i]
			if inst.Op == SSA_NOP {
				continue
			}
			if uses[i] > 0 {
				continue
			}
			if !isDCERemovable(inst.Op) {
				continue
			}

			// This instruction is dead — remove it.
			// Decrement use counts of its operands (may make them dead too).
			if inst.Arg1 != SSARefNone && int(inst.Arg1) < n {
				uses[int(inst.Arg1)]--
				if uses[int(inst.Arg1)] == 0 {
					changed = true
				}
			}
			if inst.Arg2 != SSARefNone && int(inst.Arg2) < n {
				uses[int(inst.Arg2)]--
				if uses[int(inst.Arg2)] == 0 {
					changed = true
				}
			}
			if auxIntIsRef(inst.Op) && inst.AuxInt >= 0 && int(inst.AuxInt) < n {
				uses[int(inst.AuxInt)]--
				if uses[int(inst.AuxInt)] == 0 {
					changed = true
				}
			}

			inst.Op = SSA_NOP
			inst.Arg1 = SSARefNone
			inst.Arg2 = SSARefNone
			changed = true
		}
	}

	return f
}

// isDCERemovable returns true if the instruction can be safely removed
// when its result is unused. Side-effecting instructions are NOT removable.
func isDCERemovable(op SSAOp) bool {
	switch op {
	// Pure computations — removable (no memory writes, no side effects)
	// NOTE: MOVE is NOT removable because it writes to a VM register slot
	// needed for interpreter state at side-exit. Arithmetic ops with Slot >= 0
	// also write to memory, but their primary purpose is computing a value —
	// if the value is unused, the memory write is also unnecessary. MOVE is
	// different: its only purpose IS the memory write.
	case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT, SSA_DIV_INT,
		SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
		SSA_FMADD, SSA_FMSUB,
		SSA_UNBOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_INT, SSA_BOX_FLOAT,
		SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL:
		return true

	// Load ops with Slot < 0 (pool constants, no memory write) are removable.
	// Load ops with Slot >= 0 write to VM register memory and may be needed
	// for side-exit state or store-back, so they are NOT removable.
	case SSA_LOAD_FIELD, SSA_LOAD_ARRAY, SSA_LOAD_GLOBAL, SSA_LOAD_SLOT:
		return false

	// Side effects — NOT removable
	// STORE_FIELD, STORE_ARRAY: write to table (observable)
	// STORE_SLOT: write to VM register
	// GUARD_TYPE, GUARD_TRUTHY, GUARD_NNIL, GUARD_NOMETA: control flow
	// Comparisons (EQ/LT/LE/GT): guards that branch to side-exit
	// CALL, INTRINSIC: function calls (may have side effects)
	// TABLE_LEN: native load, writes to slot (like LOAD_FIELD)
	// LOOP, SIDE_EXIT, NOP, SNAPSHOT: control flow / metadata
	// CALL_INNER_TRACE, INNER_LOOP: control flow
	// PHI: loop-carried value
	default:
		return false
	}
}
