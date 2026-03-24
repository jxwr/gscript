//go:build darwin && arm64

package jit

// UseDef holds use/def chain information for an SSAFunc.
type UseDef struct {
	// Users[i] = list of SSA refs that use instruction i as an operand
	Users [][]SSARef
	// SlotDefs maps VM slot to the list of SSA refs that define (write to) that slot
	SlotDefs map[int][]SSARef
}

// BuildUseDef computes use/def chains for the given SSAFunc.
func BuildUseDef(f *SSAFunc) *UseDef {
	n := len(f.Insts)
	ud := &UseDef{
		Users:    make([][]SSARef, n),
		SlotDefs: make(map[int][]SSARef),
	}

	for i, inst := range f.Insts {
		ref := SSARef(i)

		// Track uses via Arg1 — only if the op actually uses Arg1 as an SSA ref.
		// Ops like CONST_INT, CONST_FLOAT, LOAD_SLOT, etc. don't use Arg1;
		// their Arg1 field is zero (Go default), which would falsely register
		// instruction 0 as having users everywhere.
		if opUsesArg1(inst.Op) && inst.Arg1 != SSARefNone && inst.Arg1 >= 0 && int(inst.Arg1) < n {
			ud.Users[inst.Arg1] = append(ud.Users[inst.Arg1], ref)
		}

		// Track uses via Arg2 — same filtering for ops that use Arg2.
		if opUsesArg2(inst.Op) && inst.Arg2 != SSARefNone && inst.Arg2 >= 0 && int(inst.Arg2) < n {
			ud.Users[inst.Arg2] = append(ud.Users[inst.Arg2], ref)
		}

		// SSA_STORE_ARRAY, SSA_FMADD, SSA_FMSUB store a ref in AuxInt
		if inst.Op == SSA_STORE_ARRAY || inst.Op == SSA_FMADD || inst.Op == SSA_FMSUB {
			valRef := SSARef(inst.AuxInt)
			if valRef != SSARefNone && valRef >= 0 && int(valRef) < n {
				ud.Users[valRef] = append(ud.Users[valRef], ref)
			}
		}

		// Track slot definitions
		if inst.Slot >= 0 && definesSlot(inst.Op) {
			slot := int(inst.Slot)
			ud.SlotDefs[slot] = append(ud.SlotDefs[slot], ref)
		}
	}

	return ud
}

// opUsesArg1 returns true if the given SSA op reads Arg1 as an SSA reference.
// Ops that don't use Arg1 (constants, LOAD_SLOT, markers) have Arg1=0 by default,
// which would falsely create a use-def edge to instruction 0.
func opUsesArg1(op SSAOp) bool {
	switch op {
	case SSA_LOAD_SLOT, SSA_LOAD_GLOBAL,
		SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
		SSA_LOOP, SSA_INNER_LOOP, SSA_SNAPSHOT, SSA_NOP, SSA_SIDE_EXIT:
		return false
	}
	return true
}

// opUsesArg2 returns true if the given SSA op reads Arg2 as an SSA reference.
// Many ops only use Arg1 (unary ops, guards, moves, field loads).
func opUsesArg2(op SSAOp) bool {
	switch op {
	// Binary ops that use both Arg1 and Arg2
	case SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT,
		SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
		SSA_FMADD, SSA_FMSUB,
		SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
		SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
		SSA_LOAD_ARRAY,  // Arg1=table, Arg2=key
		SSA_STORE_ARRAY, // Arg1=table, Arg2=key (value in AuxInt)
		SSA_STORE_FIELD: // Arg1=table, Arg2=value
		return true
	}
	return false
}

// definesSlot returns true if the given op produces a value that defines a VM slot.
func definesSlot(op SSAOp) bool {
	switch op {
	case SSA_LOAD_SLOT,
		SSA_UNBOX_INT, SSA_UNBOX_FLOAT,
		SSA_BOX_INT, SSA_BOX_FLOAT,
		SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
		SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
		SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
		SSA_MOVE,
		SSA_LOAD_FIELD, SSA_LOAD_ARRAY,
		SSA_TABLE_LEN,
		SSA_PHI,
		SSA_INTRINSIC,
		SSA_LOAD_GLOBAL:
		return true
	}
	return false
}

// HasUsers returns true if instruction ref has any users.
func (ud *UseDef) HasUsers(ref SSARef) bool {
	if ref < 0 || int(ref) >= len(ud.Users) {
		return false
	}
	return len(ud.Users[ref]) > 0
}

// UserCount returns the number of users of instruction ref.
func (ud *UseDef) UserCount(ref SSARef) int {
	if ref < 0 || int(ref) >= len(ud.Users) {
		return 0
	}
	return len(ud.Users[ref])
}

// IsDeadCode returns true if the instruction has no users and is not a side-effecting op.
// Side-effecting ops are never dead: guards, STORE_SLOT, STORE_FIELD, STORE_ARRAY,
// SIDE_EXIT, CALL, CALL_SELF.
func (ud *UseDef) IsDeadCode(ref SSARef, f *SSAFunc) bool {
	if ref < 0 || int(ref) >= len(f.Insts) {
		return false
	}
	if ud.HasUsers(ref) {
		return false
	}
	return !isSideEffecting(f.Insts[ref].Op)
}

// isSideEffecting returns true if the op has side effects and must not be eliminated.
func isSideEffecting(op SSAOp) bool {
	switch op {
	case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA, SSA_GUARD_TRUTHY,
		SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY,
		SSA_SIDE_EXIT,
		SSA_CALL, SSA_CALL_SELF,
		SSA_CALL_INNER_TRACE:
		return true
	}
	return false
}
