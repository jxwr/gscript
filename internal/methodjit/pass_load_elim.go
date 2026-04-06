// pass_load_elim.go implements block-local load elimination (GetField CSE)
// with store-to-load forwarding. Within each basic block, it tracks available
// values keyed by (object value ID, field Aux). When a GetField matches an
// available entry, all uses of the redundant GetField are replaced with the
// available value, making the redundant instruction dead for DCE.
//
// Store-to-load forwarding: after SetField(obj, field, val), the stored
// value is recorded so a subsequent GetField(obj, field) reuses val
// directly instead of reloading from memory.
//
// Invalidation rules:
//   - OpSetField on the same (obj, field) kills the previous entry,
//     then records the stored value for forwarding.
//   - OpCall / OpSelf conservatively clear the entire available map,
//     because a call could mutate any table.

package methodjit

// loadKey identifies a specific field load: the SSA value ID of the
// object operand plus the constant-pool field index (Aux).
type loadKey struct {
	objID    int
	fieldAux int64
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
		available := make(map[loadKey]int) // loadKey → value ID to forward to

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
				// Conservative: a call could mutate any table.
				available = make(map[loadKey]int)
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
