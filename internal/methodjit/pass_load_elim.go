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
//   - OpSetTable / OpAppend / OpSetList on an object kill typed table-array
//     header/len/data facts for that object, because the array kind, length,
//     or backing data pointer may change.
//   - OpCall / OpSelf conservatively clear the entire available map
//     and the guard available map, because a call could mutate any table
//     or change runtime types.
//
// R53: CSE for pure ops. OpGetGlobal with the same index is CSE'd within
// a block (globals' identity is stable during a function's body —
// OpSetGlobal kills the matching index). OpMatrixFlat / OpMatrixStride
// depend only on their SSA arg: two MatrixFlat(v10) return the same
// pointer; the type guard need only run once. Critical after R52's DCE
// correctness fix: with stores actually executing, each redundant guard
// carries real cost.

package methodjit

// loadKey identifies a specific field load: the SSA value ID of the
// object operand plus the constant-pool field index (Aux).
type loadKey struct {
	objID    int
	fieldAux int64
}

// tableKey identifies a specific dynamic-keyed table load: the SSA
// value IDs of the table operand and the key operand.
type tableKey struct {
	objID int
	keyID int
}

type tableArrayHeaderKey struct {
	objID int
	kind  int64
}

type tableArrayDerivedKey struct {
	headerID int
	kind     int64
}

type pureCSEKey struct {
	op   Op
	typ  Type
	aux  int64
	aux2 int64
	args [4]int
	narg int
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
		available := make(map[loadKey]int)     // loadKey → value ID to forward to
		guardAvail := make(map[guardKey]int)   // guardKey → guard instr ID
		globalAvail := make(map[int64]int)     // globals[idx] → SSA value ID
		matrixFlatAvail := make(map[int]int)   // MatrixFlat(arg_id) → SSA value ID
		matrixStrideAvail := make(map[int]int) // MatrixStride(arg_id) → SSA value ID
		tableHeaderAvail := make(map[tableArrayHeaderKey]int)
		tableLenAvail := make(map[tableArrayDerivedKey]int)
		tableDataAvail := make(map[tableArrayDerivedKey]int)
		pureAvail := make(map[pureCSEKey]int)
		// R93: store-to-load forwarding for dynamic-key table access.
		// After SetTable(t, k, v), map (t.ID, k.ID) → v.ID so a
		// subsequent GetTable(t, k) uses v directly.
		tableAvail := make(map[tableKey]int)

		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
				OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
				OpNumToFloat, OpSqrt, OpFMA,
				OpEqInt, OpLtInt, OpLeInt, OpModZeroInt, OpLtFloat, OpLeFloat:
				if key, ok := pureTypedCSEKey(instr); ok {
					if origID, ok := pureAvail[key]; ok {
						origInstr := instrByID[origID]
						if origInstr != nil {
							replaceAllUses(fn, instr.ID, origInstr)
							functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
								"reused earlier pure typed numeric result")
						}
					} else {
						pureAvail[key] = instr.ID
					}
				}

			case OpGetGlobal:
				// R53: globals are read-only for the body of a function in
				// nearly all GScript code. Two reads of globals[i] in the
				// same block return the same runtime pointer. CSE.
				if origID, ok := globalAvail[instr.Aux]; ok {
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier GetGlobal result")
				} else {
					globalAvail[instr.Aux] = instr.ID
				}

			case OpMatrixFlat:
				if len(instr.Args) < 1 {
					continue
				}
				if origID, ok := matrixFlatAvail[instr.Args[0].ID]; ok {
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier MatrixFlat result")
				} else {
					matrixFlatAvail[instr.Args[0].ID] = instr.ID
				}

			case OpMatrixStride:
				if len(instr.Args) < 1 {
					continue
				}
				if origID, ok := matrixStrideAvail[instr.Args[0].ID]; ok {
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier MatrixStride result")
				} else {
					matrixStrideAvail[instr.Args[0].ID] = instr.ID
				}

			case OpTableArrayHeader:
				if len(instr.Args) < 1 {
					continue
				}
				key := tableArrayHeaderKey{objID: instr.Args[0].ID, kind: instr.Aux}
				if origID, ok := tableHeaderAvail[key]; ok {
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier TableArrayHeader result")
				} else {
					tableHeaderAvail[key] = instr.ID
				}

			case OpTableArrayLen:
				if len(instr.Args) < 1 {
					continue
				}
				key := tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}
				if origID, ok := tableLenAvail[key]; ok {
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier TableArrayLen result")
				} else {
					tableLenAvail[key] = instr.ID
				}

			case OpTableArrayData:
				if len(instr.Args) < 1 {
					continue
				}
				key := tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}
				if origID, ok := tableDataAvail[key]; ok {
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier TableArrayData result")
				} else {
					tableDataAvail[key] = instr.ID
				}

			case OpSetGlobal:
				// SetGlobal on globals[i] kills the matching cache entry.
				if _, ok := globalAvail[instr.Aux]; ok {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"SetGlobal invalidated cached global value")
				}
				delete(globalAvail, instr.Aux)

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
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier GetField result")
				} else {
					available[key] = instr.ID
				}

			case OpGuardType:
				if len(instr.Args) < 1 {
					continue
				}
				if guardProvenByProducer(instr.Args[0], Type(instr.Aux)) {
					if def := instr.Args[0].Def; def != nil {
						replaceAllUses(fn, instr.ID, def)
						functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
							"guard proven by producer type")
					}
					instr.Op = OpNop
					instr.Args = nil
					instr.Aux = 0
					continue
				}
				key := guardKey{argID: instr.Args[0].ID, guardType: instr.Aux}
				if origID, ok := guardAvail[key]; ok {
					// Redundant guard — replace all uses with the original.
					origInstr := instrByID[origID]
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier GuardType result")
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
				if _, ok := available[key]; ok {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"SetField invalidated earlier field load")
				}
				delete(available, key)
				// Store-to-load forwarding: a subsequent GetField on the same
				// (obj, field) can reuse the stored value directly.
				if len(instr.Args) >= 2 {
					available[key] = instr.Args[1].ID
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"recorded SetField value for forwarding")
				}

			case OpGetTable:
				// R93: forward stored value if same (tbl, key) was just set.
				if len(instr.Args) < 2 {
					continue
				}
				key := tableKey{objID: instr.Args[0].ID, keyID: instr.Args[1].ID}
				if origID, ok := tableAvail[key]; ok {
					origInstr := instrByID[origID]
					if origInstr != nil {
						replaceAllUses(fn, instr.ID, origInstr)
						functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
							"forwarded value from earlier SetTable")
					}
				}
				// R94 (reverted): don't populate with GetTable's own result
				// — caused sieve regression, likely due to increased register
				// pressure extending SSA value lifetimes.

			case OpSetTable:
				if len(instr.Args) < 3 {
					continue
				}
				// Any SetTable on t invalidates ALL entries for that obj at
				// non-matching keys (aliasing unknown). Keep only the
				// just-written entry.
				objID := instr.Args[0].ID
				for k := range tableAvail {
					if k.objID == objID {
						delete(tableAvail, k)
						functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
							"SetTable invalidated dynamic-key table cache")
					}
				}
				// Record the stored value for future GetTable(t, k).
				key := tableKey{objID: objID, keyID: instr.Args[1].ID}
				tableAvail[key] = instr.Args[2].ID
				functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
					"recorded SetTable value for forwarding")
				if invalidateTableArrayFactsForObject(tableHeaderAvail, tableLenAvail, tableDataAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"table mutation invalidated typed array facts")
				}

			case OpAppend, OpSetList:
				if len(instr.Args) < 1 {
					continue
				}
				objID := instr.Args[0].ID
				if invalidateDynamicTableCacheForObject(tableAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"array mutation invalidated dynamic-key table cache")
				}
				if invalidateTableArrayFactsForObject(tableHeaderAvail, tableLenAvail, tableDataAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"array mutation invalidated typed array facts")
				}

			case OpCall, OpSelf:
				// Conservative: a call could mutate any table or change types.
				if len(available) > 0 || len(guardAvail) > 0 || len(globalAvail) > 0 ||
					len(matrixFlatAvail) > 0 || len(matrixStrideAvail) > 0 || len(tableAvail) > 0 ||
					len(tableHeaderAvail) > 0 || len(tableLenAvail) > 0 || len(tableDataAvail) > 0 {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"call invalidated available load/guard facts")
				}
				available = make(map[loadKey]int)
				guardAvail = make(map[guardKey]int)
				globalAvail = make(map[int64]int)
				matrixFlatAvail = make(map[int]int)
				matrixStrideAvail = make(map[int]int)
				tableHeaderAvail = make(map[tableArrayHeaderKey]int)
				tableLenAvail = make(map[tableArrayDerivedKey]int)
				tableDataAvail = make(map[tableArrayDerivedKey]int)
				tableAvail = make(map[tableKey]int)
			}

			if loadElimKillsPureCSE(instr) {
				pureAvail = make(map[pureCSEKey]int)
			}
		}
	}

	return fn, nil
}

func invalidateDynamicTableCacheForObject(tableAvail map[tableKey]int, objID int) bool {
	changed := false
	for k := range tableAvail {
		if k.objID == objID {
			delete(tableAvail, k)
			changed = true
		}
	}
	return changed
}

func invalidateTableArrayFactsForObject(
	headerAvail map[tableArrayHeaderKey]int,
	lenAvail map[tableArrayDerivedKey]int,
	dataAvail map[tableArrayDerivedKey]int,
	objID int,
) bool {
	var killedHeaders []int
	for k, id := range headerAvail {
		if k.objID == objID {
			delete(headerAvail, k)
			killedHeaders = append(killedHeaders, id)
		}
	}
	if len(killedHeaders) == 0 {
		return false
	}

	changed := true
	for _, headerID := range killedHeaders {
		for k := range lenAvail {
			if k.headerID == headerID {
				delete(lenAvail, k)
			}
		}
		for k := range dataAvail {
			if k.headerID == headerID {
				delete(dataAvail, k)
			}
		}
	}
	return changed
}

func pureTypedCSEKey(instr *Instr) (pureCSEKey, bool) {
	if instr == nil || len(instr.Args) > 4 {
		return pureCSEKey{}, false
	}
	key := pureCSEKey{
		op:   instr.Op,
		typ:  instr.Type,
		aux:  instr.Aux,
		aux2: instr.Aux2,
		narg: len(instr.Args),
	}
	for i, arg := range instr.Args {
		if arg == nil {
			return pureCSEKey{}, false
		}
		key.args[i] = arg.ID
	}
	return key, true
}

func loadElimKillsPureCSE(instr *Instr) bool {
	if instr == nil {
		return false
	}
	switch instr.Op {
	case OpJump, OpBranch, OpReturn:
		return false
	}
	return hasSideEffect(instr)
}

func guardProvenByProducer(v *Value, guardType Type) bool {
	if v == nil || v.Def == nil || guardType == TypeUnknown {
		return false
	}
	switch guardType {
	case TypeInt:
		switch v.Def.Op {
		case OpConstInt, OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt:
			return true
		}
	case TypeFloat:
		switch v.Def.Op {
		case OpConstFloat, OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat, OpNumToFloat, OpGetFieldNumToFloat, OpSqrt, OpFMA:
			return true
		}
	case TypeBool:
		switch v.Def.Op {
		case OpConstBool, OpEqInt, OpLtInt, OpLeInt, OpModZeroInt, OpLtFloat, OpLeFloat, OpEq, OpLt, OpLe, OpNot:
			return true
		}
	case TypeNil:
		return v.Def.Op == OpConstNil
	case TypeString:
		return v.Def.Op == OpConstString
	case TypeTable:
		return v.Def.Op == OpNewTable || v.Def.Op == OpNewFixedTable
	case TypeFunction:
		return v.Def.Op == OpClosure
	}
	return false
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
