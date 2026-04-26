// pass_escape.go implements escape analysis + scalar replacement for
// short-lived Table allocations. It identifies OpNewTable SSA values
// whose only uses are static-key GetField/SetField within the same
// block, then rewrites those uses into direct SSA references to the
// last-stored value per field. The original NewTable and its
// SetField stores become dead and are removed by DCE.
//
// MVP scope (R158-R163):
//   - R158: detection only (this file's EscapeAnalyzeFn helper +
//           `virtualAllocs` side-table population).
//   - R159: field-variable SSA rewrite within a block.
//   - R160: if/else merges via Phi.
//   - R161: loop-carried virtual allocs.
//   - R162: pipeline integration (post-LoadElim, pre-DCE).
//   - R163: bench + correctness.
//
// Design reference: TurboFan's src/compiler/escape-analysis.cc
// (see docs-internal/decisions/adr-v8-alignment.md). GScript's MVP
// omits V8's FrameState/ObjectState deopt materialization: we bail
// on any allocation reaching a frame-state edge (= any Guard op,
// since guards can deopt).

package methodjit

// virtualAllocInfo describes a NewTable allocation that passed
// R158's MVP escape predicate. Populated by the analysis phase of
// EscapeAnalysisPass (R159); consumed by the rewrite phase.
type virtualAllocInfo struct {
	allocID   int   // ID of the OpNewTable instruction
	blockID   int   // block where the allocation lives
	fieldUses []int // IDs of OpGetField/OpSetField instrs using this alloc
	// phiReachable (R161) is true when the alloc has a use by an
	// OpPhi in addition to block-local field accesses. For these
	// the block-local rewrite (R159) does not apply directly;
	// they're handled by identifyVirtualPhis + virtual-Phi rewrite.
	phiReachable bool
}

// identifyVirtualAllocs runs a single forward pass over fn's blocks
// and returns the set of OpNewTable allocations that meet the MVP
// virtual-allocation predicate:
//
//	(a) op is OpNewTable
//	(b) every use of the result is OpGetField/OpSetField with
//	    static Aux, whose Args[0] is the alloc (not Args[1])
//	(c) all uses live in the SAME block as the alloc
//
// Any other use kills the candidacy. R160 will relax (c) to allow
// if/else merges; R161 relaxes to loops.
func identifyVirtualAllocs(fn *Function) map[int]*virtualAllocInfo {
	if fn == nil || len(fn.Blocks) == 0 {
		return nil
	}

	// First pass: collect all OpNewTable candidates.
	candidates := make(map[int]*virtualAllocInfo)
	allocBlock := make(map[int]int) // allocID → defining block ID
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewTable {
				candidates[instr.ID] = &virtualAllocInfo{
					allocID: instr.ID,
					blockID: block.ID,
				}
				allocBlock[instr.ID] = block.ID
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// Second pass: scan every use of every candidate. Any violating
	// use removes the candidate.
	kill := func(allocID int) {
		delete(candidates, allocID)
	}

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			// Determine which candidates this instruction consumes
			// and how.
			for argIdx, arg := range instr.Args {
				if arg == nil {
					continue
				}
				cand, isCand := candidates[arg.ID]
				if !isCand {
					continue
				}

				// Rule 1: determine whether this use is OK or
				// escapes the allocation.
				switch instr.Op {
				case OpGetField:
					if argIdx != 0 {
						kill(arg.ID)
						continue
					}
					if block.ID != cand.blockID {
						kill(arg.ID)
						continue
					}
					cand.fieldUses = append(cand.fieldUses, instr.ID)

				case OpSetField:
					if argIdx != 0 {
						kill(arg.ID)
						continue
					}
					if block.ID != cand.blockID {
						kill(arg.ID)
						continue
					}
					cand.fieldUses = append(cand.fieldUses, instr.ID)

				case OpPhi:
					// R161: use-by-Phi is reachable-virtual if the
					// Phi can also be a virtual-Phi. Block-local
					// rewrite does not apply; the Phi rewrite in
					// rewriteVirtualPhis will process this feeder.
					cand.phiReachable = true

				// Any other operation escapes the allocation.
				default:
					kill(arg.ID)
				}
			}
		}
	}

	return candidates
}

// identifyVirtualPhis (R161) finds OpPhi instructions that merge
// multiple virtual NewTable allocations with compatible field
// shapes. Each feeder must be in `candidates` (from
// identifyVirtualAllocs) and must have been marked phiReachable.
//
// Returns a map from Phi instruction ID → virtualPhiInfo.
//
// Compatibility rule: all feeders must write the same set of
// field names (strings, looked up via proto.Constants[aux]). If
// feeders differ in the set of fields they set, the Phi cannot
// be safely rewritten.
func identifyVirtualPhis(fn *Function, candidates map[int]*virtualAllocInfo) map[int]*virtualPhiInfo {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	// Build quick lookup: allocID → last-stored value per field name.
	// For a virtual feeder we capture the full field → value map by
	// walking the feeder's block in order.
	instrByID := make(map[int]*Instr)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			instrByID[instr.ID] = instr
		}
	}
	allocFields := make(map[int]map[string]int) // allocID → fieldName → valueID
	for allocID, info := range candidates {
		if !info.phiReachable {
			continue
		}
		fm := make(map[string]int)
		block := fn.Blocks[info.blockID]
		for _, ins := range block.Instrs {
			if ins.Op != OpSetField || len(ins.Args) < 2 {
				continue
			}
			if ins.Args[0].ID != allocID {
				continue
			}
			fieldName := fieldNameFromAux(fn, ins.Aux)
			if fieldName == "" {
				// Non-string field — bail on this feeder.
				fm = nil
				break
			}
			fm[fieldName] = ins.Args[1].ID
		}
		if fm == nil {
			continue
		}
		allocFields[allocID] = fm
	}

	// Walk every OpPhi. Candidate if all Args are keys in allocFields
	// AND all have identical field-name sets.
	result := make(map[int]*virtualPhiInfo)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpPhi || len(instr.Args) < 2 {
				continue
			}
			feeders := make([]int, 0, len(instr.Args))
			shape := map[string]bool{}
			allMatch := true
			for i, arg := range instr.Args {
				if arg == nil {
					allMatch = false
					break
				}
				fields, ok := allocFields[arg.ID]
				if !ok {
					allMatch = false
					break
				}
				feeders = append(feeders, arg.ID)
				if i == 0 {
					for k := range fields {
						shape[k] = true
					}
				} else {
					if len(fields) != len(shape) {
						allMatch = false
						break
					}
					for k := range shape {
						if _, ok := fields[k]; !ok {
							allMatch = false
							break
						}
					}
					if !allMatch {
						break
					}
				}
			}
			if !allMatch {
				continue
			}
			// Check uses of the Phi result — must be only GetField
			// (static key). No SetField (we don't model through-Phi
			// writes in MVP).
			allowedUse := true
			for _, b2 := range fn.Blocks {
				for _, ins2 := range b2.Instrs {
					for ui, use := range ins2.Args {
						if use == nil || use.ID != instr.ID {
							continue
						}
						if ins2.Op == OpGetField && ui == 0 {
							continue
						}
						allowedUse = false
						break
					}
					if !allowedUse {
						break
					}
				}
				if !allowedUse {
					break
				}
			}
			if !allowedUse {
				continue
			}
			result[instr.ID] = &virtualPhiInfo{
				phiID:   instr.ID,
				blockID: block.ID,
				feeders: feeders,
			}
		}
	}
	return result
}

// virtualPhiInfo records a Phi merging multiple virtual NewTable
// allocations with identical field shape (R161).
type virtualPhiInfo struct {
	phiID   int
	blockID int
	feeders []int // allocation IDs in Phi-arg order (matches block.Preds)
}

// applyVirtualPhiRewrite (R161) rewrites one virtual Phi into per-
// field Phis. Each feeder's SetField (for each field name F) is
// captured into a new OpPhi whose args, in pred order, match the
// feeders' per-F stored values. The feeder allocations and all
// their SetFields become Nop. The original table-typed Phi becomes
// Nop. GetField(vphi, F) uses are replaceAllUses'd to the new F-Phi.
func applyVirtualPhiRewrite(fn *Function, vphi *virtualPhiInfo,
	candidates map[int]*virtualAllocInfo,
	instrByID map[int]*Instr,
) {
	phiBlock := fn.Blocks[vphi.blockID]
	phiInstr := instrByID[vphi.phiID]
	if phiInstr == nil {
		return
	}

	// Step 1: build per-feeder field maps {fieldName → stored value ID}.
	feederFields := make([]map[string]int, len(vphi.feeders))
	// Also capture the aux index used for each field in ANY feeder, so
	// we can use it as the Aux for GetField-lookup key. Field names map
	// to aux indices per-block; for the GetField side (which is what
	// readers see), we need aux indices used by downstream consumers,
	// not the feeders. So build field map keyed by name.
	for i, allocID := range vphi.feeders {
		cand, ok := candidates[allocID]
		if !ok {
			return
		}
		fm := make(map[string]int)
		block := fn.Blocks[cand.blockID]
		for _, ins := range block.Instrs {
			if ins.Op == OpSetField && len(ins.Args) >= 2 &&
				ins.Args[0].ID == allocID {
				name := fieldNameFromAux(fn, ins.Aux)
				if name == "" {
					return
				}
				fm[name] = ins.Args[1].ID
			}
		}
		feederFields[i] = fm
	}

	// Step 2: build a set of all field names (should be identical across
	// feeders by identifyVirtualPhis's contract; take union to be safe).
	fieldNames := map[string]bool{}
	for _, fm := range feederFields {
		for name := range fm {
			fieldNames[name] = true
		}
	}

	// Step 3: materialize per-field Phis. Insert into phiBlock.Instrs
	// right BEFORE the original Phi, so definition order stays legal.
	fieldPhiID := make(map[string]int) // fieldName → new Phi ID
	// Find the index of the original Phi in phiBlock.Instrs.
	phiIdx := -1
	for i, ins := range phiBlock.Instrs {
		if ins.ID == vphi.phiID {
			phiIdx = i
			break
		}
	}
	if phiIdx < 0 {
		return
	}
	newPhis := make([]*Instr, 0, len(fieldNames))
	for name := range fieldNames {
		args := make([]*Value, len(vphi.feeders))
		phiType := TypeUnknown
		for i := range vphi.feeders {
			valID := feederFields[i][name]
			// Find the defining instr for this valID to build a Value.
			defInstr := instrByID[valID]
			if defInstr != nil {
				args[i] = defInstr.Value()
				phiType = joinVirtualFieldType(phiType, defInstr.Type)
			} else {
				// Value IDs < numRegs come from LoadSlot / parameters;
				// represent them as an undefined arg. To stay safe,
				// bail.
				return
			}
		}
		if phiType == TypeUnknown {
			phiType = phiInstr.Type
		}
		newID := fn.newValueID()
		newPhi := &Instr{
			ID:    newID,
			Op:    OpPhi,
			Type:  phiType,
			Args:  args,
			Block: phiBlock,
		}
		fieldPhiID[name] = newID
		newPhis = append(newPhis, newPhi)
		instrByID[newID] = newPhi
	}
	// Splice into Instrs.
	phiBlock.Instrs = append(phiBlock.Instrs[:phiIdx],
		append(append([]*Instr{}, newPhis...), phiBlock.Instrs[phiIdx:]...)...)

	// Step 4: rewrite all GetField(vphi.phiID, Aux=F) uses.
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op != OpGetField || len(ins.Args) < 1 {
				continue
			}
			if ins.Args[0].ID != vphi.phiID {
				continue
			}
			name := fieldNameFromAux(fn, ins.Aux)
			if name == "" {
				continue
			}
			newID, ok := fieldPhiID[name]
			if !ok {
				continue
			}
			newDef := instrByID[newID]
			if newDef == nil {
				continue
			}
			replaceAllUses(fn, ins.ID, newDef)
			ins.Op = OpNop
			ins.Args = nil
			ins.Aux = 0
		}
	}

	// Step 5: Nop the original Phi and each feeder NewTable +
	// associated SetFields.
	phiInstr.Op = OpNop
	phiInstr.Args = nil
	phiInstr.Aux = 0
	for _, allocID := range vphi.feeders {
		cand, ok := candidates[allocID]
		if !ok {
			continue
		}
		allocInstr := instrByID[allocID]
		if allocInstr != nil {
			allocInstr.Op = OpNop
			allocInstr.Args = nil
			allocInstr.Aux = 0
			allocInstr.Aux2 = 0
		}
		block := fn.Blocks[cand.blockID]
		for _, ins := range block.Instrs {
			if ins.Op == OpSetField && len(ins.Args) >= 2 &&
				ins.Args[0].ID == allocID {
				ins.Op = OpNop
				ins.Args = nil
				ins.Aux = 0
			}
		}
	}
}

func joinVirtualFieldType(current, next Type) Type {
	if next == TypeUnknown || next == TypeAny {
		return current
	}
	if current == TypeUnknown || current == TypeAny {
		return next
	}
	if current == next {
		return current
	}
	if (current == TypeInt && next == TypeFloat) || (current == TypeFloat && next == TypeInt) {
		return TypeFloat
	}
	return TypeUnknown
}

// fieldNameFromAux resolves a constant-pool index (Instr.Aux) to
// its string value. Returns "" if the pool slot is not a string.
func fieldNameFromAux(fn *Function, aux int64) string {
	if fn == nil || fn.Proto == nil {
		return ""
	}
	if aux < 0 || int(aux) >= len(fn.Proto.Constants) {
		return ""
	}
	k := fn.Proto.Constants[aux]
	if !k.IsString() {
		return ""
	}
	return k.Str()
}

// EscapeAnalysisPass identifies virtual allocations and scalar-
// replaces their field accesses. Block-local only (R159). Non-
// virtual allocations are untouched.
//
// For each virtual allocation V in block B:
//
//  1. Walk B.Instrs in order. Maintain field_ssa map[fieldAux → valueID].
//
//  2. On OpSetField(self=V, value=X, Aux=F):
//     field_ssa[F] = X.ID. Replace instr.Op = OpNop (X is still
//     reachable through the map).
//
//  3. On OpGetField(self=V, Aux=F):
//     If field_ssa[F] exists, replaceAllUses(fn, instr.ID, valueInstr).
//     Replace instr.Op = OpNop.
//     If field_ssa[F] does NOT exist (read-before-write), we bail
//     on this allocation — convert it back from virtual to real.
//     This is conservative; R160+ may tighten.
//
//  4. After the block walk, the OpNewTable itself has no remaining
//     uses and becomes dead. DCE removes it.
//
// The pass runs at pipeline stage post-LoadElim, pre-DCE so that
// LoadElim has already forwarded any trivially-forwardable fields,
// and DCE cleans up our OpNop'd instructions.
func EscapeAnalysisPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}

	virtuals := identifyVirtualAllocs(fn)
	if len(virtuals) == 0 {
		return fn, nil
	}

	// Build an instruction lookup table for replaceAllUses.
	instrByID := make(map[int]*Instr)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			instrByID[instr.ID] = instr
		}
	}

	// R161: virtual-Phi rewrite FIRST — if a Phi merges multiple
	// virtual allocations, materialize per-field Phis at the Phi's
	// block, then rewrite GetField(phi) uses to the field-Phi.
	// Feeders and their SetFields become Nop.
	vphis := identifyVirtualPhis(fn, virtuals)
	for _, vphi := range vphis {
		applyVirtualPhiRewrite(fn, vphi, virtuals, instrByID)
	}

	// For each (non-Phi-reachable) virtual alloc, walk its block
	// and rewrite block-local field ops. We process each virtual
	// independently because a block may contain multiple virtual
	// allocs with disjoint field uses. Field matching is by
	// string NAME (not aux index), since inline can introduce
	// duplicate const-pool entries for the same field across
	// inline sites.
	for allocID, info := range virtuals {
		if info.phiReachable {
			continue
		}
		block := fn.Blocks[info.blockID]
		fieldSSA := make(map[string]int) // fieldName → value ID to forward

		bailed := false

		// First forward walk: validate and collect.
		for _, instr := range block.Instrs {
			if instr.Op == OpGetField && len(instr.Args) >= 1 &&
				instr.Args[0].ID == allocID {
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" {
					bailed = true
					break
				}
				if _, ok := fieldSSA[name]; !ok {
					bailed = true
					break
				}
			}
			if instr.Op == OpSetField && len(instr.Args) >= 2 &&
				instr.Args[0].ID == allocID {
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" {
					bailed = true
					break
				}
				fieldSSA[name] = instr.Args[1].ID
			}
		}
		if bailed {
			continue
		}

		// Second forward walk: apply rewrites, rebuilding the map.
		fieldSSA = make(map[string]int)
		for _, instr := range block.Instrs {
			switch {
			case instr.Op == OpSetField && len(instr.Args) >= 2 &&
				instr.Args[0].ID == allocID:
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" {
					continue
				}
				fieldSSA[name] = instr.Args[1].ID
				instr.Op = OpNop
				instr.Args = nil
				instr.Aux = 0

			case instr.Op == OpGetField && len(instr.Args) >= 1 &&
				instr.Args[0].ID == allocID:
				name := fieldNameFromAux(fn, instr.Aux)
				if name == "" {
					continue
				}
				valID, ok := fieldSSA[name]
				if !ok {
					continue
				}
				defInstr, ok := instrByID[valID]
				if !ok || defInstr == nil {
					continue
				}
				replaceAllUses(fn, instr.ID, defInstr)
				instr.Op = OpNop
				instr.Args = nil
				instr.Aux = 0
			}
		}

		if allocInstr, ok := instrByID[allocID]; ok {
			allocInstr.Op = OpNop
			allocInstr.Args = nil
			allocInstr.Aux = 0
			allocInstr.Aux2 = 0
		}
	}

	return fn, nil
}
