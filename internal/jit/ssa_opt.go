//go:build darwin && arm64

package jit

// ────────────────────────────────────────────────────────────────────────────
// CSE — Common Subexpression Elimination
// ────────────────────────────────────────────────────────────────────────────

// CSE performs common subexpression elimination on the SSA IR.
// It scans the loop body for duplicate pure operations (same op + same args
// + same AuxInt) and replaces duplicates with NOP, rewriting all users to
// reference the first occurrence.
//
// The pass handles cascading elimination: when a duplicate is removed and its
// users are rewritten, downstream instructions may become new duplicates of
// earlier instructions. Because rewriteUsers patches all instructions
// immediately (including not-yet-visited ones), a single forward pass suffices.
func cseImpl(f *SSAFunc) *SSAFunc {
	// Find the LOOP marker by scanning. Hand-built test SSAFuncs may not
	// set f.LoopIdx, so we find it ourselves.
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

	type cseKey struct {
		Op     SSAOp
		Arg1   SSARef
		Arg2   SSARef
		AuxInt int64
	}

	seen := map[cseKey]SSARef{}

	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		// Only CSE pure operations (no side effects, no memory reads/writes)
		if !isPureOp(inst.Op) {
			continue
		}

		key := cseKey{inst.Op, inst.Arg1, inst.Arg2, inst.AuxInt}
		if prev, ok := seen[key]; ok {
			// Duplicate found — replace with NOP and rewrite all users
			oldRef := SSARef(i)
			rewriteUsers(f, oldRef, prev)
			inst.Op = SSA_NOP
		} else {
			seen[key] = SSARef(i)
		}
	}
	return f
}

// isPureOp returns true if the operation is pure (deterministic, no side
// effects, no memory access) and therefore safe to deduplicate via CSE.
func isPureOp(op SSAOp) bool {
	switch op {
	case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT, SSA_DIV_INT,
		SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
		SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
		SSA_UNBOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_INT, SSA_BOX_FLOAT:
		return true
	}
	return false
}

// rewriteUsers replaces all references to oldRef with newRef throughout
// the entire SSA function: instruction args, AuxInt (for ops that encode
// an SSA ref there), and snapshot entries.
func rewriteUsers(f *SSAFunc, oldRef, newRef SSARef) {
	for i := range f.Insts {
		inst := &f.Insts[i]
		if inst.Arg1 == oldRef {
			inst.Arg1 = newRef
		}
		if inst.Arg2 == oldRef {
			inst.Arg2 = newRef
		}
		// Some ops encode an SSA ref in AuxInt:
		//   STORE_ARRAY: AuxInt = value ref
		//   FMADD/FMSUB: AuxInt = addend ref
		if auxIntIsRef(inst.Op) && SSARef(inst.AuxInt) == oldRef {
			inst.AuxInt = int64(newRef)
		}
	}

	// Rewrite snapshot entries
	for si := range f.Snapshots {
		for ei := range f.Snapshots[si].Entries {
			if f.Snapshots[si].Entries[ei].Ref == oldRef {
				f.Snapshots[si].Entries[ei].Ref = newRef
			}
		}
	}
}

// auxIntIsRef returns true if the given op uses AuxInt to hold an SSA ref.
func auxIntIsRef(op SSAOp) bool {
	switch op {
	case SSA_STORE_ARRAY, SSA_FMADD, SSA_FMSUB:
		return true
	}
	return false
}
