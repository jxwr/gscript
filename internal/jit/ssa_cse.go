//go:build darwin && arm64

package jit

// cseKey identifies a unique computation: same Op, Type, Arg1, Arg2.
// For constants, AuxInt is also part of the key (same op+type but different
// constant values are NOT duplicates).
type cseKey struct {
	Op     SSAOp
	Type   SSAType
	Arg1   SSARef
	Arg2   SSARef
	AuxInt int64 // needed to distinguish constants with different values
}

// isPureOp returns true if the SSA operation is side-effect-free and
// safe to deduplicate. Operations that read mutable memory (LOAD_SLOT,
// LOAD_FIELD, LOAD_ARRAY, LOAD_GLOBAL), have side effects (guards, stores),
// or represent control flow (MOVE, SIDE_EXIT) are excluded.
func isPureOp(op SSAOp) bool {
	switch op {
	// Integer arithmetic
	case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT:
		return true
	// Float arithmetic
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT:
		return true
	// Comparisons
	case SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT, SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	// Constants
	case SSA_CONST_INT, SSA_CONST_FLOAT:
		return true
	// Box/Unbox (pure conversion)
	case SSA_UNBOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_INT, SSA_BOX_FLOAT:
		return true
	}
	return false
}

// CSE performs Common Subexpression Elimination on the SSA IR.
// It finds duplicate instructions (same Op, Type, Arg1, Arg2) within
// the loop body and replaces later occurrences with references to the first.
func CSE(f *SSAFunc) *SSAFunc {
	// Find the LOOP marker
	loopIdx := -1
	for i, inst := range f.Insts {
		if inst.Op == SSA_LOOP {
			loopIdx = i
			break
		}
	}
	if loopIdx < 0 {
		// No loop marker — nothing to optimize
		return f
	}

	// Map from computation key to first occurrence ref.
	seen := make(map[cseKey]SSARef)

	// Map from old ref to replacement ref (for rewriting).
	rewrites := make(map[SSARef]SSARef)

	// Scan instructions after LOOP for duplicates.
	for i := loopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		if !isPureOp(inst.Op) {
			continue
		}

		// Before computing the key, resolve any already-rewritten operands.
		// This enables transitive CSE: if C's operand was rewritten from
		// a duplicate to the original, C's key now matches another instruction
		// that uses the original.
		arg1 := inst.Arg1
		if replacement, ok := rewrites[arg1]; ok {
			arg1 = replacement
			inst.Arg1 = replacement
		}
		arg2 := inst.Arg2
		if replacement, ok := rewrites[arg2]; ok {
			arg2 = replacement
			inst.Arg2 = replacement
		}

		key := cseKey{
			Op:     inst.Op,
			Type:   inst.Type,
			Arg1:   arg1,
			Arg2:   arg2,
			AuxInt: inst.AuxInt,
		}

		ref := SSARef(i)
		if firstRef, exists := seen[key]; exists {
			// Duplicate found — NOP this instruction, record rewrite
			rewrites[ref] = firstRef
			f.Insts[i] = SSAInst{Op: SSA_NOP}
		} else {
			seen[key] = ref
		}
	}

	if len(rewrites) == 0 {
		return f
	}

	// Rewrite all references in the entire function (including pre-loop).
	for i := range f.Insts {
		inst := &f.Insts[i]
		if inst.Op == SSA_NOP {
			continue
		}

		if replacement, ok := rewrites[inst.Arg1]; ok {
			inst.Arg1 = replacement
		}
		if replacement, ok := rewrites[inst.Arg2]; ok {
			inst.Arg2 = replacement
		}

		// SSA_STORE_ARRAY uses AuxInt as an SSARef for the stored value
		if inst.Op == SSA_STORE_ARRAY {
			valRef := SSARef(inst.AuxInt)
			if replacement, ok := rewrites[valRef]; ok {
				inst.AuxInt = int64(replacement)
			}
		}
	}

	return f
}
