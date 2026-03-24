package jit

// debugSSAGuardElim enables debug logging for SSA-level guard elimination.
var debugSSAGuardElim = false

// BuildSSA converts a Trace into SSA IR with type inference.
func BuildSSA(trace *Trace) *SSAFunc {
	b := &ssaBuilder{
		trace:    trace,
		slotDefs: make(map[int]SSARef), // current SSA ref for each VM slot
		slotType: make(map[int]SSAType), // known type for each VM slot
	}
	return b.build()
}

// SSAIsUseful returns true if the SSA function can actually loop natively.
// A trace is useful if it has:
//  1. A loop exit check (LE_INT or LT_INT with AuxInt != 1)
//  2. Useful operations (arithmetic, table ops, etc.)
//  3. No unconditional SIDE_EXIT (which would prevent looping)
//
// For numeric for-loops, the exit check (LE_INT) appears at the END of the body.
// For while-loops, it appears at the BEGINNING (condition check comes first).
// Both patterns are valid — we scan the entire loop body.
func SSAIsUseful(f *SSAFunc) bool {
	loopSeen := false
	hasUsefulOp := false
	hasExitCheck := false
	for _, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopSeen = true
			continue
		}
		if loopSeen {
			switch inst.Op {
			case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
				SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
				SSA_FMADD, SSA_FMSUB,
				SSA_EQ_INT:
				hasUsefulOp = true
			case SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
				SSA_GUARD_TRUTHY:
				// Conditional guards — don't block the loop
				hasUsefulOp = true
			case SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_FIELD, SSA_STORE_FIELD:
				hasUsefulOp = true
			case SSA_INTRINSIC, SSA_LOAD_GLOBAL, SSA_CALL_INNER_TRACE, SSA_INNER_LOOP:
				hasUsefulOp = true
			case SSA_LE_INT, SSA_LT_INT:
				// Check if this is an inner loop check (AuxInt=1 or 2) — don't terminate scan
				if inst.AuxInt == 1 || inst.AuxInt == 2 {
					hasUsefulOp = true
					continue
				}
				// Outer loop exit check found
				hasExitCheck = true
			case SSA_SIDE_EXIT:
				// Unconditional side-exit — trace always exits, never loops
				return false
			}
		}
	}
	return hasExitCheck && hasUsefulOp
}

// OptimizeSSA runs optimization passes on the SSA IR.
func OptimizeSSA(f *SSAFunc) *SSAFunc {
	// Pass 1: Guard hoisting — guards are already at the top (before LOOP)
	// This is ensured by BuildSSA's structure.

	// Pass 2: SSA-level guard elimination is done during code generation,
	// not here. See emitSSAPreLoopGuards which uses use-def chains to skip
	// guards whose LOAD_SLOT refs have no loop-body users.

	// Pass 3: Dead code elimination
	f = eliminateDeadCode(f)

	return f
}

// eliminateDeadCode removes SSA instructions whose results are never used.
func eliminateDeadCode(f *SSAFunc) *SSAFunc {
	// Count references to each instruction
	refCount := make([]int, len(f.Insts))
	for _, inst := range f.Insts {
		if inst.Arg1 >= 0 && int(inst.Arg1) < len(refCount) {
			refCount[inst.Arg1]++
		}
		if inst.Arg2 >= 0 && int(inst.Arg2) < len(refCount) {
			refCount[inst.Arg2]++
		}
	}

	// Mark side-effecting instructions as live
	for i, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA, SSA_GUARD_TRUTHY,
			SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY,
			SSA_LOAD_ARRAY, // table loads have side-exits, keep alive
			SSA_LOOP, SSA_INNER_LOOP, SSA_SNAPSHOT, SSA_SIDE_EXIT,
			SSA_LE_INT, SSA_LT_INT, SSA_EQ_INT,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_CALL, SSA_CALL_SELF,
			SSA_CALL_INNER_TRACE:
			refCount[i]++ // keep alive
		}
	}

	// Mark loop-carried values as live: any value-producing instruction after LOOP
	// that writes to a VM slot (Slot >= 0) is potentially a loop-carried definition
	// and must not be eliminated.
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx >= 0 {
		for i := loopIdx + 1; i < len(f.Insts); i++ {
			inst := &f.Insts[i]
			switch inst.Op {
			case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
				SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
				SSA_FMADD, SSA_FMSUB,
				SSA_CONST_INT, SSA_CONST_FLOAT, SSA_MOVE:
				if inst.Slot >= 0 {
					refCount[i]++ // keep alive: writes to a VM slot
				}
			}
		}
	}

	// NOP out dead instructions
	for i := range f.Insts {
		if refCount[i] == 0 && f.Insts[i].Op != SSA_NOP {
			f.Insts[i].Op = SSA_NOP
		}
	}

	return f
}
