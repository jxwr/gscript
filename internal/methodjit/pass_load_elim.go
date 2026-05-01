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

// LoadEliminationPass eliminates redundant GetField operations. The main walk
// is block-local for the broader CSE tables, then a narrow forward dataflow
// propagates only field facts that every predecessor agrees on. That keeps
// cross-block forwarding conservative while catching diamond-shaped code where
// a field is stored before a branch and read at the merge.
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
		tableArrayFacts := newTableArrayFactSet()
		pureAvail := make(map[pureCSEKey]int)
		// R93: store-to-load forwarding for dynamic-key table access.
		// After SetTable(t, k, v), map (t.ID, k.ID) → v.ID so a
		// subsequent GetTable(t, k) uses v directly.
		tableAvail := make(map[tableKey]int)

		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
				OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
				OpNumToFloat, OpSqrt, OpFloor, OpFMA, OpFMSUB,
				OpEqInt, OpLtInt, OpLeInt, OpModZeroInt, OpLtFloat, OpLeFloat:
				if instr.Op == OpNumToFloat && redundantNumToFloatArg(instr) {
					replaceAllUses(fn, instr.ID, instr.Args[0].Def)
					instr.Op = OpNop
					instr.Args = nil
					instr.Aux = 0
					instr.Aux2 = 0
					instr.Type = TypeUnknown
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, OpNumToFloat,
						"removed numeric-to-float conversion of statically-float value")
					continue
				}
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
				if orig := tableArrayFacts.LookupHeader(instr); orig != nil {
					origInstr := orig.Def
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier TableArrayHeader result")
				} else {
					tableArrayFacts.RecordHeader(instr)
				}

			case OpTableArrayLen:
				if orig := tableArrayFacts.LookupLen(instr); orig != nil {
					origInstr := orig.Def
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier TableArrayLen result")
				} else {
					tableArrayFacts.RecordLen(instr)
				}

			case OpTableArrayData:
				if orig := tableArrayFacts.LookupData(instr); orig != nil {
					origInstr := orig.Def
					replaceAllUses(fn, instr.ID, origInstr)
					functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
						"reused earlier TableArrayData result")
				} else {
					tableArrayFacts.RecordData(instr)
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
				if tableArrayFacts.InvalidateTable(objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"table mutation invalidated typed array facts")
				}

			case OpTableArrayStore:
				if len(instr.Args) < 5 || instr.Args[0] == nil || instr.Args[3] == nil {
					continue
				}
				objID := instr.Args[0].ID
				if invalidateDynamicTableCacheForObject(tableAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"typed array store invalidated dynamic-key table cache")
				}
				tableAvail[tableKey{objID: objID, keyID: instr.Args[3].ID}] = instr.Args[4].ID
				functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
					"recorded typed array store value for forwarding")

			case OpTableArraySwap:
				if len(instr.Args) < 1 || instr.Args[0] == nil {
					continue
				}
				objID := instr.Args[0].ID
				if invalidateDynamicTableCacheForObject(tableAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"typed array swap invalidated dynamic-key table cache")
				}

			case OpTableIntArrayReversePrefix:
				if len(instr.Args) < 1 || instr.Args[0] == nil {
					continue
				}
				objID := instr.Args[0].ID
				if invalidateDynamicTableCacheForObject(tableAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"int-array prefix kernel invalidated dynamic-key table cache")
				}
				if tableArrayFacts.InvalidateTable(objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"int-array prefix kernel invalidated typed array facts")
				}

			case OpTableIntArrayCopyPrefix:
				if len(instr.Args) < 1 || instr.Args[0] == nil {
					continue
				}
				objID := instr.Args[0].ID
				if invalidateDynamicTableCacheForObject(tableAvail, objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"int-array copy kernel invalidated dynamic-key table cache")
				}
				if tableArrayFacts.InvalidateTable(objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"int-array copy kernel invalidated typed array facts")
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
				if tableArrayFacts.InvalidateTable(objID) {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"array mutation invalidated typed array facts")
				}

			case OpCall, OpSelf:
				// Conservative: a call could mutate any table or change types.
				if len(available) > 0 || len(guardAvail) > 0 || len(globalAvail) > 0 ||
					len(matrixFlatAvail) > 0 || len(matrixStrideAvail) > 0 || len(tableAvail) > 0 ||
					!tableArrayFacts.Empty() {
					functionRemarks(fn).Add("LoadElim", "missed", block.ID, instr.ID, instr.Op,
						"call invalidated available load/guard facts")
				}
				available = make(map[loadKey]int)
				guardAvail = make(map[guardKey]int)
				globalAvail = make(map[int64]int)
				matrixFlatAvail = make(map[int]int)
				matrixStrideAvail = make(map[int]int)
				tableArrayFacts.Reset()
				tableAvail = make(map[tableKey]int)
			}

			if loadElimKillsPureCSE(instr) {
				pureAvail = make(map[pureCSEKey]int)
			}
		}
	}

	if crossBlockFieldLoadElimination(fn, instrByID) {
		cleanupProducerProvenGuards(fn)
	}

	return fn, nil
}

func crossBlockFieldLoadElimination(fn *Function, instrByID map[int]*Instr) bool {
	if fn == nil || len(fn.Blocks) <= 1 {
		return false
	}

	in := make(map[int]map[loadKey]int, len(fn.Blocks))
	out := make(map[int]map[loadKey]int, len(fn.Blocks))
	changed := true
	for changed {
		changed = false
		for _, block := range fn.Blocks {
			nextIn := meetPredFieldFacts(block, out)
			if !fieldFactMapsEqual(in[block.ID], nextIn) {
				in[block.ID] = nextIn
				changed = true
			}
			nextOut := transferFieldFacts(cloneFieldFactMap(nextIn), block)
			if !fieldFactMapsEqual(out[block.ID], nextOut) {
				out[block.ID] = nextOut
				changed = true
			}
		}
	}

	rewrote := false
	for _, block := range fn.Blocks {
		available := cloneFieldFactMap(in[block.ID])
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			if instr.Op == OpGetField && len(instr.Args) >= 1 && instr.Args[0] != nil {
				key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
				if origID, ok := available[key]; ok && origID != instr.ID {
					if origInstr := instrByID[origID]; origInstr != nil {
						replaceAllUses(fn, instr.ID, origInstr)
						functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, instr.Op,
							"forwarded field value from dominating predecessor")
						rewrote = true
					}
				}
			}
			available = transferFieldFactInstr(available, instr)
		}
	}
	return rewrote
}

func meetPredFieldFacts(block *Block, out map[int]map[loadKey]int) map[loadKey]int {
	if block == nil || len(block.Preds) == 0 {
		return nil
	}
	var result map[loadKey]int
	for i, pred := range block.Preds {
		var predFacts map[loadKey]int
		if pred != nil {
			predFacts = out[pred.ID]
		}
		if i == 0 {
			result = cloneFieldFactMap(predFacts)
			continue
		}
		for key, valueID := range result {
			if predFacts == nil || predFacts[key] != valueID {
				delete(result, key)
			}
		}
		if len(result) == 0 {
			return nil
		}
	}
	return result
}

func transferFieldFacts(facts map[loadKey]int, block *Block) map[loadKey]int {
	for _, instr := range block.Instrs {
		facts = transferFieldFactInstr(facts, instr)
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func transferFieldFactInstr(facts map[loadKey]int, instr *Instr) map[loadKey]int {
	if instr == nil {
		return facts
	}
	// Loop backedges can carry facts for a value ID into the block where that
	// same SSA value is defined. Those facts describe the previous iteration's
	// object, not the value being defined now.
	invalidateFieldFactsForObject(facts, instr.ID)
	switch instr.Op {
	case OpGetField:
		if len(instr.Args) < 1 || instr.Args[0] == nil {
			return facts
		}
		key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
		if _, ok := facts[key]; !ok {
			if facts == nil {
				facts = make(map[loadKey]int)
			}
			facts[key] = instr.ID
		}
	case OpSetField:
		if len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
			return facts
		}
		// Different SSA table values can still alias the same runtime table.
		// A named-field store only invalidates that field, then records the
		// exact stored value for the object used by this store.
		invalidateFieldFactsForField(facts, instr.Aux)
		if facts == nil {
			facts = make(map[loadKey]int)
		}
		key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
		facts[key] = instr.Args[1].ID
	case OpSetTable, OpTableArrayStore, OpTableArraySwap, OpTableBoolArrayFill,
		OpTableIntArrayReversePrefix, OpTableIntArrayCopyPrefix, OpAppend, OpSetList:
		clearFieldFacts(facts)
	case OpCall, OpSelf:
		clearFieldFacts(facts)
	}
	return facts
}

func invalidateFieldFactsForObject(facts map[loadKey]int, objID int) {
	for key := range facts {
		if key.objID == objID {
			delete(facts, key)
		}
	}
}

func invalidateFieldFactsForField(facts map[loadKey]int, fieldAux int64) {
	for key := range facts {
		if key.fieldAux == fieldAux {
			delete(facts, key)
		}
	}
}

func clearFieldFacts(facts map[loadKey]int) {
	for key := range facts {
		delete(facts, key)
	}
}

func cloneFieldFactMap(in map[loadKey]int) map[loadKey]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[loadKey]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func fieldFactMapsEqual(a, b map[loadKey]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		if bv, ok := b[key]; !ok || bv != av {
			return false
		}
	}
	return true
}

func cleanupProducerProvenGuards(fn *Function) {
	if fn == nil {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpGuardType || len(instr.Args) < 1 {
				continue
			}
			if !guardProvenByProducer(instr.Args[0], Type(instr.Aux)) {
				continue
			}
			if def := instr.Args[0].Def; def != nil {
				replaceAllUses(fn, instr.ID, def)
				functionRemarks(fn).Add("LoadElim", "changed", block.ID, instr.ID, OpGuardType,
					"guard proven after cross-block field forwarding")
			}
			instr.Op = OpNop
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			instr.Type = TypeUnknown
		}
	}
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

func redundantNumToFloatArg(instr *Instr) bool {
	return instr != nil &&
		instr.Op == OpNumToFloat &&
		len(instr.Args) == 1 &&
		instr.Args[0] != nil &&
		instr.Args[0].Def != nil &&
		instr.Args[0].Def.Type == TypeFloat
}

func guardProvenByProducer(v *Value, guardType Type) bool {
	if v == nil || v.Def == nil || guardType == TypeUnknown {
		return false
	}
	switch guardType {
	case TypeInt:
		switch v.Def.Op {
		case OpConstInt, OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt, OpFloor:
			return true
		}
	case TypeFloat:
		switch v.Def.Op {
		case OpConstFloat, OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat, OpNumToFloat, OpGetFieldNumToFloat, OpSqrt, OpFMA, OpFMSUB:
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
