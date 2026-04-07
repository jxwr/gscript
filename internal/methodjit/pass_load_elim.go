// pass_load_elim.go implements block-local load elimination (GetField CSE)
// with store-to-load forwarding and GuardType CSE. Within each basic block,
// it tracks available values keyed by (object value ID, field Aux). When a
// GetField matches an available entry, all uses of the redundant GetField
// are replaced with the available value, making the redundant instruction
// dead for DCE.
//
// GuardType CSE: when the same value is guarded for the same type multiple
// times within a block, redundant guards are eliminated. The redundant guard
// is converted to OpNop (since guards are side-effecting and DCE would
// otherwise keep them). This is important for hot loops like nbody where
// feedback-driven guards on the same GetField result appear multiple times.
//
// Store-to-load forwarding: after SetField(obj, field, val), the stored
// value is recorded so a subsequent GetField(obj, field) reuses val
// directly instead of reloading from memory.
//
// Invalidation rules:
//   - OpSetField on the same (obj, field) kills the previous entry,
//     then records the stored value for forwarding.
//   - OpCall / OpSelf conservatively clear the entire available map
//     and the guard available map, because a call could mutate any table
//     or change runtime types.

package methodjit

// loadKey identifies a specific field load: the SSA value ID of the
// object operand plus the constant-pool field index (Aux).
type loadKey struct {
	objID    int
	fieldAux int64
}

// guardKey identifies a specific type guard: the SSA value ID of the
// guarded operand plus the guard type (stored in Aux).
type guardKey struct {
	argID     int   // the value being guarded (Args[0].ID)
	guardType int64 // the guard type (Aux field)
}

// LoadEliminationPass eliminates redundant GetField operations within
// each basic block. It is a forward dataflow pass: no cross-block
// propagation, keeping it simple and correct.
func LoadEliminationPass(fn *Function) (*Function, error) {
	// Build an instruction lookup table so we can find the *Instr for
	// any value ID when performing use-replacement.
	instrByID := make(map[int]*Instr)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			instrByID[instr.ID] = instr
		}
	}

	for _, block := range fn.Blocks {
		available := make(map[loadKey]int)  // loadKey → value ID to forward to
		guardAvail := make(map[guardKey]int) // guardKey → guard instr ID

		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpGetField:
				if len(instr.Args) < 1 {
					continue
				}
				key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
				if origID, ok := available[key]; ok {
					// Redundant load — replace all uses of this GetField
					// with the original one.
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
				} else {
					available[key] = instr.ID
				}

			case OpGuardType:
				if len(instr.Args) < 1 {
					continue
				}
				key := guardKey{argID: instr.Args[0].ID, guardType: instr.Aux}
				if origID, ok := guardAvail[key]; ok {
					// Redundant guard — replace all uses with the original.
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					// Guards are side-effecting so DCE won't remove them.
					// Convert to Nop to make the redundant guard dead.
					instr.Op = OpNop
					instr.Args = nil
					instr.Aux = 0
				} else {
					guardAvail[key] = instr.ID
				}

			case OpSetField:
				if len(instr.Args) < 1 {
					continue
				}
				// Kill the specific (obj, field) entry, then record stored value.
				key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
				delete(available, key)
				// Store-to-load forwarding: a subsequent GetField on the same
				// (obj, field) can reuse the stored value directly.
				if len(instr.Args) >= 2 {
					available[key] = instr.Args[1].ID
				}

			case OpCall, OpSelf:
				// Conservative: a call could mutate any table or change types.
				available = make(map[loadKey]int)
				guardAvail = make(map[guardKey]int)
			}
		}
	}

	return fn, nil
}

// replaceAllUses rewrites every instruction argument that references oldID
// to point to newInstr's value instead.
func replaceAllUses(fn *Function, oldID int, newInstr *Instr) {
	newVal := newInstr.Value()
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for i, arg := range instr.Args {
				if arg != nil && arg.ID == oldID {
					instr.Args[i] = newVal
				}
			}
		}
	}
}
