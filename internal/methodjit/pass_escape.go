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
	allocID     int   // ID of the OpNewTable instruction
	blockID     int   // block where the allocation lives
	fieldUses   []int // IDs of OpGetField/OpSetField instrs using this alloc
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

				// Rule 1: uses must be in the defining block.
				if block.ID != cand.blockID {
					kill(arg.ID)
					continue
				}

				// Rule 2: determine whether this use is OK or
				// escapes the allocation.
				switch instr.Op {
				case OpGetField:
					// Only argIdx == 0 is the "self" slot. GetField
					// has exactly one arg, so argIdx must be 0.
					if argIdx != 0 {
						kill(arg.ID)
						continue
					}
					cand.fieldUses = append(cand.fieldUses, instr.ID)

				case OpSetField:
					// argIdx 0 = self (OK); argIdx 1 = value being
					// stored INTO another table → escapes.
					if argIdx == 0 {
						cand.fieldUses = append(cand.fieldUses, instr.ID)
					} else {
						kill(arg.ID)
					}

				// Any other operation escapes the allocation. The
				// broad list includes OpCall/OpSelf/OpReturn/
				// OpSetGlobal/OpSetUpval/OpGuardType/OpGuardNonNil/
				// OpGuardTruthy/OpPhi/OpEq/OpLt/OpLe and dynamic-
				// key table ops (OpGetTable/OpSetTable/OpGetField
				// on OTHER tables when this alloc is their VALUE,
				// which argIdx!=0 covers above).
				default:
					kill(arg.ID)
				}
			}
		}
	}

	return candidates
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

	// For each virtual alloc, walk its block and rewrite field ops.
	// We process each virtual independently because a block may
	// contain multiple virtual allocs with disjoint field uses.
	for allocID, info := range virtuals {
		block := fn.Blocks[info.blockID]
		fieldSSA := make(map[int64]int) // fieldAux → value ID to forward

		// Bail flag: if we hit a GetField read-before-write, mark
		// the allocation as non-virtual after all and skip rewrites.
		bailed := false
		bailReason := ""

		// First forward walk: validate and collect.
		for _, instr := range block.Instrs {
			if instr.Op == OpGetField && len(instr.Args) >= 1 &&
				instr.Args[0].ID == allocID {
				if _, ok := fieldSSA[instr.Aux]; !ok {
					bailed = true
					bailReason = "read-before-write"
					break
				}
			}
			if instr.Op == OpSetField && len(instr.Args) >= 2 &&
				instr.Args[0].ID == allocID {
				fieldSSA[instr.Aux] = instr.Args[1].ID
			}
		}

		_ = bailReason
		if bailed {
			continue
		}

		// Second forward walk: apply rewrites.
		// Reset fieldSSA and rebuild as we go, because the first
		// walk's final map may differ from the mid-walk state.
		fieldSSA = make(map[int64]int)
		for _, instr := range block.Instrs {
			switch {
			case instr.Op == OpSetField && len(instr.Args) >= 2 &&
				instr.Args[0].ID == allocID:
				fieldSSA[instr.Aux] = instr.Args[1].ID
				// Dead-store: the virtual's field value is only
				// observed through GetField rewrites, not through
				// the in-memory table. Convert to Nop.
				instr.Op = OpNop
				instr.Args = nil
				instr.Aux = 0

			case instr.Op == OpGetField && len(instr.Args) >= 1 &&
				instr.Args[0].ID == allocID:
				valID, ok := fieldSSA[instr.Aux]
				if !ok {
					// Should not happen (first walk validated); be
					// defensive — leave the instr alone.
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

		// The OpNewTable itself: no more uses after the rewrites
		// above. DCE will remove it. Make it explicitly Nop too,
		// so the output IR is clean even if DCE is skipped.
		if allocInstr, ok := instrByID[allocID]; ok {
			allocInstr.Op = OpNop
			allocInstr.Args = nil
			allocInstr.Aux = 0
			allocInstr.Aux2 = 0
		}
	}

	return fn, nil
}
