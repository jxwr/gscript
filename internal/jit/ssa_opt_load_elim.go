//go:build darwin && arm64

package jit

// LoadElimination eliminates redundant LOAD_FIELD operations in a trace.
// In a linear trace (no control flow), if the same field of the same table
// was already loaded and no intervening store to that table exists,
// the load can be eliminated.
//
// Conservative approach:
//   - STORE_FIELD to ANY field of a table invalidates ALL cached fields for that table.
//   - This prevents issues where the codegen store-back mechanism or field interdependencies
//     cause stale values.
//   - CALL and CALL_INNER_TRACE invalidate everything.
//   - We ONLY do load-after-load elimination (not store-forwarding) for safety.
func LoadElimination(f *SSAFunc) *SSAFunc {
	if f == nil || len(f.Insts) == 0 {
		return f
	}

	type fieldKey struct {
		tableRef SSARef
		fieldIdx int
	}

	// Track known field values: (tableRef, fieldIdx) -> SSARef holding the value
	known := make(map[fieldKey]loadElimEntry)

	eliminated := 0

	for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
		inst := &f.Insts[i]

		switch inst.Op {
		case SSA_LOAD_FIELD:
			fieldIdx := int(int32(inst.AuxInt))
			if fieldIdx < 0 {
				continue
			}
			key := fieldKey{tableRef: inst.Arg1, fieldIdx: fieldIdx}
			if entry, ok := known[key]; ok && entry.fromLoad {
				// A previous LOAD_FIELD with same (table, field) exists, and no
				// store to this table happened in between. Safe to eliminate.
				// BUT: we must ensure the destination slot still gets written.
				// The eliminated LOAD's slot may differ from the original's slot.
				// Only eliminate if both loads write to the same slot, OR if the
				// eliminated load's result is only used via SSA ref (not memory).
				origSlot := int(f.Insts[entry.ref].Slot)
				thisSlot := int(inst.Slot)
				if origSlot == thisSlot {
					// Same slot — safe to eliminate entirely
					replaceRef(f, SSARef(i), entry.ref)
					inst.Op = SSA_NOP
					inst.Arg1 = SSARefNone
					inst.Arg2 = SSARefNone
					eliminated++
				} else {
					// Different slots — can't NOP because the slot write would be lost.
					// Keep the instruction but record it as a known value.
					known[key] = loadElimEntry{ref: SSARef(i), fromLoad: true}
				}
			} else {
				known[key] = loadElimEntry{ref: SSARef(i), fromLoad: true}
			}

		case SSA_STORE_FIELD:
			// Invalidate ALL cached fields for this table (conservative).
			// This prevents stale values when the store-back mechanism or
			// field interdependencies affect correctness.
			tblRef := inst.Arg1
			for k := range known {
				if k.tableRef == tblRef {
					delete(known, k)
				}
			}

		case SSA_CALL, SSA_CALL_INNER_TRACE, SSA_INNER_LOOP:
			// Function calls may modify any table — invalidate everything
			known = make(map[fieldKey]loadElimEntry)

		case SSA_STORE_ARRAY:
			// STORE_ARRAY modifies the array part of a table. While it doesn't
			// affect string-keyed fields, the table pointer might be shared.
			// Conservatively invalidate the table's cache.
			tblRef := inst.Arg1
			for k := range known {
				if k.tableRef == tblRef {
					delete(known, k)
				}
			}
		}
	}

	if debugTrace && eliminated > 0 {
		println("[LOAD_ELIM] eliminated", eliminated, "redundant LOAD_FIELD ops")
	}

	return f
}

type loadElimEntry struct {
	ref      SSARef
	fromLoad bool // true if from LOAD_FIELD, false if from STORE_FIELD forwarding
}

// replaceRef replaces all uses of oldRef with newRef in the SSA function.
func replaceRef(f *SSAFunc, oldRef, newRef SSARef) {
	for i := range f.Insts {
		inst := &f.Insts[i]
		if inst.Op == SSA_NOP {
			continue
		}
		if inst.Arg1 == oldRef {
			inst.Arg1 = newRef
		}
		if inst.Arg2 == oldRef {
			inst.Arg2 = newRef
		}
		if auxIntIsRef(inst.Op) && SSARef(inst.AuxInt) == oldRef {
			inst.AuxInt = int64(newRef)
		}
	}
	// Also replace in snapshots
	for si := range f.Snapshots {
		for ei := range f.Snapshots[si].Entries {
			if f.Snapshots[si].Entries[ei].Ref == oldRef {
				f.Snapshots[si].Entries[ei].Ref = newRef
			}
		}
	}
}
