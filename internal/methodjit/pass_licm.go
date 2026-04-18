// pass_licm.go implements Loop-Invariant Code Motion (LICM) for the
// Method JIT's CFG SSA IR. Instructions whose operands do not change
// inside a loop and whose op is on a conservative hoist-safe whitelist
// are moved to a newly created pre-header block. Processing goes
// innermost-first so that values hoisted out of an inner loop can also
// be hoisted further by an enclosing outer loop's pass.
//
// This file is platform-agnostic (no build tag). It only manipulates
// CFG/SSA data structures defined in ir.go and loops.go. Emitter and
// register allocator are unaffected — the pre-header is just another
// block with a terminator, visible to RPO and RegAlloc.
//
// Algorithm (per loop, innermost first):
//   1. Gather in-loop instructions, seed invariant set with constants
//      and anything whose def is outside the loop body.
//   2. Fixed-point iterate: an in-loop instr is invariant if it is
//      hoist-safe AND all Args are invariant.
//   3. Build a fresh pre-header block PH, redirect every outside pred
//      to PH, make PH's only successor the old header, update header
//      phis (first arg is now from PH).
//   4. Move invariant instrs into PH (before its terminator), preserving
//      original program order.
//   5. Recompute loopInfo before the next loop (block membership may
//      change after hoisting moves instructions across blocks and a new
//      pre-header is interposed).
//
// Correctness notes:
//   - OpGuardType IS hoisted when its operand is invariant. GScript's deopt
//     model (ExitCode=2, jump to deopt_epilogue) has no PC-dependent state.
//   - OpGuardTruthy/OpGuardNonNil are NOT hoisted (control-flow guards).
//   - OpLoadSlot is only hoisted if no in-loop OpStoreSlot writes the
//     same slot number (slots are independent VM registers).
//   - Int arithmetic is only hoisted when fn.Int48Safe marks it safe
//     (otherwise hoisting past an overflow check would relocate a deopt).

package methodjit

import "fmt"

// LICMPass moves loop-invariant computations out of loops into a
// pre-header. Safe to call on functions without loops (no-op). Returns
// a wrapping error if the IR fails validation after the transform.
func LICMPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}

	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return fn, nil
	}

	// Compute initial loop-nesting depth so we can process innermost
	// loops first. Depth = distance to outermost (0 for outermost).
	depth := loopDepths(li)

	// Collect and sort headers by descending depth (innermost first),
	// tiebreak by header block ID for determinism.
	type hdrEntry struct {
		id    int
		depth int
	}
	headers := make([]hdrEntry, 0, len(li.loopHeaders))
	for hid := range li.loopHeaders {
		headers = append(headers, hdrEntry{id: hid, depth: depth[hid]})
	}
	// Insertion sort: small N, stable, and keeps the file dependency-free.
	for i := 1; i < len(headers); i++ {
		for j := i; j > 0; j-- {
			a, b := headers[j-1], headers[j]
			if a.depth < b.depth || (a.depth == b.depth && a.id > b.id) {
				headers[j-1], headers[j] = b, a
			} else {
				break
			}
		}
	}

	for _, h := range headers {
		// Recompute loopInfo for each loop iteration: after hoisting we
		// inserted a pre-header block, mutated predecessor lists, and
		// moved instructions. The cheapest correct thing is to recompute.
		li = computeLoopInfo(fn)
		hdr := findBlockByID(fn, h.id)
		if hdr == nil || !li.loopHeaders[hdr.ID] {
			// Header no longer present (shouldn't happen: we never delete
			// blocks). Skip defensively.
			continue
		}
		hoistOneLoop(fn, li, hdr)
	}

	if errs := Validate(fn); len(errs) > 0 {
		return fn, fmt.Errorf("LICM produced invalid IR: %v", errs)
	}
	return fn, nil
}

// loopDepths returns a map from header ID to its nesting depth. Depth
// 0 is outermost; each additional enclosing loop adds 1.
func loopDepths(li *loopInfo) map[int]int {
	nest := loopNest(li)
	depth := make(map[int]int, len(li.loopHeaders))
	var walk func(int) int
	walk = func(hid int) int {
		if d, ok := depth[hid]; ok {
			return d
		}
		parent, ok := nest[hid]
		if !ok || parent < 0 {
			depth[hid] = 0
			return 0
		}
		d := walk(parent) + 1
		depth[hid] = d
		return d
	}
	for hid := range li.loopHeaders {
		walk(hid)
	}
	return depth
}

// hoistOneLoop performs LICM for a single loop identified by its header.
// Assumes li reflects the current state of fn.
func hoistOneLoop(fn *Function, li *loopInfo, hdr *Block) {
	bodyBlocks := li.headerBlocks[hdr.ID]
	if bodyBlocks == nil {
		return
	}

	// Collect body blocks in deterministic order: walk fn.Blocks, filter.
	bodyList := make([]*Block, 0, len(bodyBlocks))
	for _, b := range fn.Blocks {
		if bodyBlocks[b.ID] {
			bodyList = append(bodyList, b)
		}
	}

	// Invariant set: value IDs that are loop-invariant.
	invariant := make(map[int]bool)

	// Seed 1: values defined OUTSIDE the loop body are invariant. We
	// compute this as "in fn.Blocks but not in bodyBlocks".
	for _, b := range fn.Blocks {
		if bodyBlocks[b.ID] {
			continue
		}
		for _, instr := range b.Instrs {
			invariant[instr.ID] = true
		}
	}

	// Collect all in-loop instructions so we can iterate to fixpoint
	// without revisiting out-of-loop blocks.
	type instrLoc struct {
		instr *Instr
		block *Block
	}
	var inLoop []instrLoc
	// stores: slot number → true (for LoadSlot hoist check).
	storedSlots := make(map[int64]bool)
	for _, b := range bodyList {
		for _, instr := range b.Instrs {
			inLoop = append(inLoop, instrLoc{instr: instr, block: b})
			if instr.Op == OpStoreSlot {
				storedSlots[instr.Aux] = true
			}
		}
	}

	// Collect in-loop field writes, global writes, and calls for alias analysis.
	setFields := make(map[loadKey]bool)
	setGlobals := make(map[int64]bool) // Aux (constant pool index) of in-loop SetGlobal
	hasLoopCall := false
	for _, b := range bodyList {
		for _, instr := range b.Instrs {
			switch instr.Op {
			case OpSetField:
				if len(instr.Args) >= 1 {
					setFields[loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}] = true
				}
			case OpSetTable:
				// SetTable uses dynamic keys — conservatively kills all fields on that obj.
				// Use fieldAux = -1 as sentinel for "any field on this obj".
				if len(instr.Args) >= 1 {
					setFields[loadKey{objID: instr.Args[0].ID, fieldAux: -1}] = true
				}
			case OpAppend:
				// table.insert mutates the table's array part.
				if len(instr.Args) >= 1 {
					setFields[loadKey{objID: instr.Args[0].ID, fieldAux: -1}] = true
				}
			case OpSetList:
				// table.setlist mutates the table's array part.
				if len(instr.Args) >= 1 {
					setFields[loadKey{objID: instr.Args[0].ID, fieldAux: -1}] = true
				}
			case OpSetGlobal:
				setGlobals[instr.Aux] = true
			case OpCall, OpSelf:
				hasLoopCall = true
			}
		}
	}

	// Seed 2: in-loop constants with no args are invariant.
	for _, loc := range inLoop {
		op := loc.instr.Op
		if op == OpConstInt || op == OpConstFloat || op == OpConstBool || op == OpConstNil {
			invariant[loc.instr.ID] = true
		}
	}

	// Fixed-point iteration: mark an in-loop instr invariant when it is
	// hoist-safe AND all its Args are invariant.
	for {
		changed := false
		for _, loc := range inLoop {
			instr := loc.instr
			if invariant[instr.ID] {
				continue
			}
			if instr.Op == OpPhi || instr.Op.IsTerminator() {
				continue
			}
			if !canHoistOp(instr.Op) {
				continue
			}
			// LoadSlot: also require no in-loop store to same slot.
			if instr.Op == OpLoadSlot {
				if storedSlots[instr.Aux] {
					continue
				}
			}
			// GetField: require no in-loop store to same (obj, field) and no calls.
			if instr.Op == OpGetField {
				if hasLoopCall {
					continue
				}
				if len(instr.Args) >= 1 {
					key := loadKey{objID: instr.Args[0].ID, fieldAux: instr.Aux}
					if setFields[key] {
						continue
					}
					// Also check if SetTable on the same obj (any field).
					if setFields[(loadKey{objID: instr.Args[0].ID, fieldAux: -1})] {
						continue
					}
				}
			}
			// GetTable: require no in-loop SetTable on same obj and no calls.
			if instr.Op == OpGetTable {
				if hasLoopCall {
					continue
				}
				if len(instr.Args) >= 1 {
					// SetTable on same obj kills all table accesses (fieldAux=-1 sentinel).
					if setFields[(loadKey{objID: instr.Args[0].ID, fieldAux: -1})] {
						continue
					}
				}
			}
			// GetGlobal: require no in-loop SetGlobal on same name and no calls.
			if instr.Op == OpGetGlobal {
				if hasLoopCall {
					continue
				}
				if setGlobals[instr.Aux] {
					continue
				}
			}
			// Int arithmetic: require Int48Safe marking.
			if isIntArithOp(instr.Op) {
				if fn.Int48Safe == nil || !fn.Int48Safe[instr.ID] {
					continue
				}
			}
			// All args invariant?
			allInv := true
			for _, a := range instr.Args {
				if a == nil {
					continue // treat as constant parameter
				}
				if a.Def == nil {
					continue // function parameter — treat as invariant
				}
				if !invariant[a.ID] {
					allInv = false
					break
				}
			}
			if !allInv {
				continue
			}
			invariant[instr.ID] = true
			changed = true
		}
		if !changed {
			break
		}
	}

	// Collect the set of in-loop invariant instructions to actually move.
	// An "in-loop" instr has Block in bodyBlocks and is not a phi/terminator.
	var toHoist []*Instr
	hoistSet := make(map[int]bool)
	for _, loc := range inLoop {
		instr := loc.instr
		if !invariant[instr.ID] {
			continue
		}
		if instr.Op == OpPhi || instr.Op.IsTerminator() {
			continue
		}
		// Constants with no args, LoadSlot, etc. — only hoist if the
		// defining block is inside the loop body (we only move in-loop
		// instructions).
		if !bodyBlocks[instr.Block.ID] {
			continue
		}
		toHoist = append(toHoist, instr)
		hoistSet[instr.ID] = true
	}

	if len(toHoist) == 0 {
		return
	}

	// Split predecessors of hdr into inside/outside.
	inside, outside := loopPreds(li, hdr)
	if len(outside) == 0 {
		return // unreachable header; skip
	}

	// Create a fresh pre-header block.
	ph := &Block{
		ID:   nextBlockID(fn),
		defs: make(map[int]*Value),
	}

	// Redirect each outside pred's terminator so branches to hdr go to ph.
	for _, p := range outside {
		retargetTerminator(p, hdr.ID, ph.ID)
		// Update p.Succs: replace hdr with ph.
		for i, s := range p.Succs {
			if s == hdr {
				p.Succs[i] = ph
			}
		}
	}

	// ph.Preds = outside (same order), ph.Succs = [hdr].
	ph.Preds = append(ph.Preds, outside...)
	ph.Succs = []*Block{hdr}

	// Remove outside preds from hdr.Preds, then prepend ph.
	newHdrPreds := make([]*Block, 0, 1+len(inside))
	newHdrPreds = append(newHdrPreds, ph)
	// Preserve inside order from the original hdr.Preds sequence.
	for _, p := range hdr.Preds {
		for _, ip := range inside {
			if ip == p {
				newHdrPreds = append(newHdrPreds, p)
				break
			}
		}
	}

	// Fix up header phis. For each phi P with old args indexed by the
	// OLD hdr.Preds order, compute the PH-slot arg:
	//   - Collect the old args at positions where the old pred was in
	//     `outside` (in outside order).
	//   - If all the collected args point at the same Value ID, use
	//     that Value as the PH-slot arg directly.
	//   - Otherwise, insert a fresh phi at the top of PH whose Args are
	//     these outside args (same order as ph.Preds = outside) and
	//     whose Type matches P's Type.
	oldPreds := hdr.Preds // capture before reassigning
	// Build position index: block pointer -> old pred index.
	oldPredIdx := make(map[*Block]int, len(oldPreds))
	for i, p := range oldPreds {
		oldPredIdx[p] = i
	}

	var phPhis []*Instr // new phis prepended to PH
	for _, instr := range hdr.Instrs {
		if instr.Op != OpPhi {
			break
		}
		// Collect outside args in outside-pred order.
		outsideArgs := make([]*Value, 0, len(outside))
		for _, op := range outside {
			idx, ok := oldPredIdx[op]
			if !ok || idx >= len(instr.Args) {
				outsideArgs = append(outsideArgs, nil)
				continue
			}
			outsideArgs = append(outsideArgs, instr.Args[idx])
		}
		// Collect inside args in inside-pred order.
		insideArgs := make([]*Value, 0, len(inside))
		for _, ip := range inside {
			idx, ok := oldPredIdx[ip]
			if !ok || idx >= len(instr.Args) {
				insideArgs = append(insideArgs, nil)
				continue
			}
			insideArgs = append(insideArgs, instr.Args[idx])
		}
		var phSlotArg *Value
		if sameValue(outsideArgs) {
			if len(outsideArgs) > 0 {
				phSlotArg = outsideArgs[0]
			}
		} else {
			// Create a fresh phi in PH. Args ordered as ph.Preds = outside.
			phPhi := &Instr{
				ID:    fn.newValueID(),
				Op:    OpPhi,
				Type:  instr.Type,
				Block: ph,
				Args:  outsideArgs,
			}
			phPhis = append(phPhis, phPhi)
			phSlotArg = phPhi.Value()
		}
		// Rewrite P.Args = [phSlotArg, ...insideArgs].
		newArgs := make([]*Value, 0, 1+len(insideArgs))
		newArgs = append(newArgs, phSlotArg)
		newArgs = append(newArgs, insideArgs...)
		instr.Args = newArgs
	}
	// Commit new hdr.Preds.
	hdr.Preds = newHdrPreds

	// Build PH's instruction list: [phPhis..., hoisted..., Jump hdr].
	phJump := &Instr{
		ID:    fn.newValueID(),
		Op:    OpJump,
		Type:  TypeUnknown,
		Block: ph,
		Aux:   int64(hdr.ID),
	}
	ph.Instrs = make([]*Instr, 0, len(phPhis)+len(toHoist)+1)
	ph.Instrs = append(ph.Instrs, phPhis...)

	// Hoist instructions in their original order (bodyList order, then
	// position within each block). Remove from their source block and
	// append to PH before the Jump.
	for _, b := range bodyList {
		kept := b.Instrs[:0]
		for _, instr := range b.Instrs {
			if hoistSet[instr.ID] && instr.Op != OpPhi && !instr.Op.IsTerminator() {
				instr.Block = ph
				ph.Instrs = append(ph.Instrs, instr)
			} else {
				kept = append(kept, instr)
			}
		}
		b.Instrs = kept
	}
	ph.Instrs = append(ph.Instrs, phJump)

	// Insert PH into fn.Blocks just before hdr's position for readable
	// printer output and for RPO to pick it up correctly.
	insertBlockBefore(fn, ph, hdr)
}

// sameValue returns true when every non-nil Value in args refers to the
// same SSA value (same ID). An empty list returns true. If any entry is
// nil we treat the set as non-uniform (conservative: force a phi).
func sameValue(args []*Value) bool {
	if len(args) == 0 {
		return true
	}
	var refID int
	have := false
	for _, a := range args {
		if a == nil {
			return false
		}
		if !have {
			refID = a.ID
			have = true
			continue
		}
		if a.ID != refID {
			return false
		}
	}
	return true
}

// retargetTerminator rewrites a block's last instruction so that any
// successor-block-ID equal to oldID becomes newID. Only touches
// Aux/Aux2 on OpJump/OpBranch; Return has no successor.
func retargetTerminator(b *Block, oldID, newID int) {
	if len(b.Instrs) == 0 {
		return
	}
	last := b.Instrs[len(b.Instrs)-1]
	switch last.Op {
	case OpJump:
		if last.Aux == int64(oldID) {
			last.Aux = int64(newID)
		}
	case OpBranch:
		if last.Aux == int64(oldID) {
			last.Aux = int64(newID)
		}
		if last.Aux2 == int64(oldID) {
			last.Aux2 = int64(newID)
		}
	}
}

// nextBlockID returns a block ID that is not currently used by any
// block in fn.Blocks.
func nextBlockID(fn *Function) int {
	max := -1
	for _, b := range fn.Blocks {
		if b.ID > max {
			max = b.ID
		}
	}
	return max + 1
}

// insertBlockBefore inserts blk into fn.Blocks just before target. If
// target is not present, appends blk to the end.
func insertBlockBefore(fn *Function, blk, target *Block) {
	for i, b := range fn.Blocks {
		if b == target {
			out := make([]*Block, 0, len(fn.Blocks)+1)
			out = append(out, fn.Blocks[:i]...)
			out = append(out, blk)
			out = append(out, fn.Blocks[i:]...)
			fn.Blocks = out
			return
		}
	}
	fn.Blocks = append(fn.Blocks, blk)
}

// canHoistOp returns true if moving an instruction with this op out of
// a loop is semantically safe (assuming all its operands are also
// invariant). The emitter and regalloc must still be able to place the
// result; we only whitelist pure, side-effect-free computations.
func canHoistOp(op Op) bool {
	switch op {
	case OpConstInt, OpConstFloat, OpConstBool, OpConstNil:
		return true
	case OpLoadSlot:
		return true
	case OpGetField:
		// Caller must also check alias info (no SetField/SetTable/Call in loop).
		return true
	case OpGetGlobal:
		// Caller must also check alias info (no SetGlobal on same name, no Call in loop).
		return true
	case OpSqrt:
		// Pure single-input float op with no side effects.
		return true
	case OpGetTable:
		// Caller must also check alias info (no SetTable on same obj, no Call in loop).
		return true
	case OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat:
		return true
	case OpAddInt, OpSubInt, OpMulInt, OpNegInt:
		// Caller must also check fn.Int48Safe.
		return true
	case OpLtInt, OpLeInt, OpEqInt, OpLtFloat, OpLeFloat, OpNot:
		return true
	case OpGuardType:
		// Pure type check; deopt metadata has no PC-dependent state,
		// so hoisting is safe when the guarded value is invariant.
		return true
	case OpMatrixFlat, OpMatrixStride:
		// R45: extracting dmFlat / dmStride is pure (output depends
		// only on the Table argument; DenseMatrix descriptor is
		// immutable once NewDenseMatrix returns). Hoisting these to
		// the preheader is the entire point of the R45 split.
		// Caller must still check that no in-loop call could invalidate
		// m (hasLoopCall) — LICM already enforces that for GetField/
		// GetTable, and the same guard applies here.
		return true
	case OpMatrixRowPtr:
		// R46: row-pointer arithmetic is pure. Hoists when all 3 inputs
		// (flat, stride, i) are loop-invariant. In matmul's inner k-loop
		// with a[i][k], i is invariant → row_a hoists outside the k-loop.
		return true
	}
	return false
}

// isIntArithOp reports whether the op is an integer arithmetic op that
// requires an Int48Safe guarantee before we can hoist past its overflow
// check. Comparisons (LtInt/LeInt/EqInt) and NegInt with safe input are
// also listed in canHoistOp, but only the adds/subs/muls/negs carry the
// emitter's SBFX+CMP overflow sequence — comparisons don't.
func isIntArithOp(op Op) bool {
	switch op {
	case OpAddInt, OpSubInt, OpMulInt, OpNegInt:
		return true
	}
	return false
}
