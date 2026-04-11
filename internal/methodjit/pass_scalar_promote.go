// pass_scalar_promote.go implements LoopScalarPromotionPass: promote
// loop-carried (obj, field) pairs into an SSA phi at the loop header.
// R32 scope: float fields only, exactly one in-loop SetField, no calls
// in the loop body, no wide-kill writes to the same obj, single exit
// block with no critical edge, obj loop-invariant, dedicated pre-header.

package methodjit

// pairInfo collects the OpGetField and OpSetField instructions observed
// in a loop body for a single (objID, fieldAux) pair.
type pairInfo struct {
	objID    int
	fieldAux int64
	gets     []*Instr
	sets     []*Instr
	anyFloat bool
	allFloat bool
}

// ScalarPromotionPass is the pipeline entry point.
func ScalarPromotionPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return fn, nil
	}
	preheaders := computeLoopPreheaders(fn, li)
	if len(preheaders) == 0 {
		return fn, nil
	}
	for _, blk := range fn.Blocks {
		if !li.loopHeaders[blk.ID] {
			continue
		}
		phID, ok := preheaders[blk.ID]
		if !ok {
			continue
		}
		ph := findBlockByID(fn, phID)
		if ph == nil {
			continue
		}
		promoteLoopPairs(fn, li, blk, ph)
	}
	return fn, nil
}

// promoteLoopPairs processes a single loop header.
func promoteLoopPairs(fn *Function, li *loopInfo, hdr *Block, ph *Block) {
	bodyBlocks := li.headerBlocks[hdr.ID]
	if bodyBlocks == nil {
		return
	}
	// hdr.Preds[0] must be ph (LICM guarantees this); require a single
	// back-edge pred so phi arg indexing is [ph, back-edge].
	if len(hdr.Preds) != 2 || hdr.Preds[0] != ph {
		return
	}
	if !bodyBlocks[hdr.Preds[1].ID] {
		return
	}

	bodyList := make([]*Block, 0, len(bodyBlocks))
	for _, b := range fn.Blocks {
		if bodyBlocks[b.ID] {
			bodyList = append(bodyList, b)
		}
	}

	hasLoopCall := false
	wideKill := make(map[int]bool)
	pairs := make(map[loadKey]*pairInfo)
	getPair := func(objID int, fieldAux int64) *pairInfo {
		k := loadKey{objID: objID, fieldAux: fieldAux}
		p, ok := pairs[k]
		if !ok {
			p = &pairInfo{objID: objID, fieldAux: fieldAux, allFloat: true}
			pairs[k] = p
		}
		return p
	}
	for _, b := range bodyList {
		for _, instr := range b.Instrs {
			switch instr.Op {
			case OpCall, OpSelf:
				hasLoopCall = true
			case OpSetTable, OpAppend, OpSetList:
				if len(instr.Args) >= 1 {
					wideKill[instr.Args[0].ID] = true
				}
			case OpGetField:
				if len(instr.Args) < 1 {
					continue
				}
				p := getPair(instr.Args[0].ID, instr.Aux)
				p.gets = append(p.gets, instr)
				if instr.Type == TypeFloat {
					p.anyFloat = true
				} else {
					p.allFloat = false
				}
			case OpSetField:
				if len(instr.Args) < 2 {
					continue
				}
				p := getPair(instr.Args[0].ID, instr.Aux)
				p.sets = append(p.sets, instr)
			}
		}
	}
	if hasLoopCall {
		return
	}

	// Single exit block, no critical edge.
	var exitBlock *Block
	for _, b := range bodyList {
		for _, s := range b.Succs {
			if bodyBlocks[s.ID] {
				continue
			}
			if exitBlock == nil {
				exitBlock = s
			} else if exitBlock != s {
				return
			}
		}
	}
	if exitBlock == nil {
		return
	}
	for _, p := range exitBlock.Preds {
		if !bodyBlocks[p.ID] {
			return
		}
	}

	// Deterministic pair iteration: sort by (objID, fieldAux).
	ordered := make([]*pairInfo, 0, len(pairs))
	for _, p := range pairs {
		ordered = append(ordered, p)
	}
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0; j-- {
			a, b := ordered[j-1], ordered[j]
			if a.objID > b.objID || (a.objID == b.objID && a.fieldAux > b.fieldAux) {
				ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
			} else {
				break
			}
		}
	}

	for _, p := range ordered {
		if len(p.sets) != 1 || len(p.gets) == 0 {
			continue
		}
		if !p.anyFloat || !p.allFloat {
			continue
		}
		if wideKill[p.objID] {
			continue
		}
		if !isInvariantObj(bodyBlocks, p.gets[0]) {
			continue
		}
		promoteOnePair(fn, hdr, ph, exitBlock, p)
	}
}

// isInvariantObj returns true if the obj value used by get is defined
// outside the loop body (or is a parameter).
func isInvariantObj(bodyBlocks map[int]bool, get *Instr) bool {
	if len(get.Args) < 1 || get.Args[0] == nil {
		return false
	}
	def := get.Args[0].Def
	if def == nil || def.Block == nil {
		return true
	}
	return !bodyBlocks[def.Block.ID]
}

// promoteOnePair performs the actual IR mutation for one promotable pair.
func promoteOnePair(fn *Function, hdr, ph, exitBlock *Block, p *pairInfo) {
	objVal := p.gets[0].Args[0]
	fieldAux := p.fieldAux

	// 1. Pre-header init load before ph's terminator.
	initLoad := &Instr{
		ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat,
		Args: []*Value{objVal}, Aux: fieldAux, Block: ph,
	}
	insertBeforeTerminator(ph, initLoad)

	// 2. New header phi prepended before any existing phis.
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: hdr}
	storeInstr := p.sets[0]
	phi.Args = []*Value{initLoad.Value(), storeInstr.Args[1]}
	hdr.Instrs = append([]*Instr{phi}, hdr.Instrs...)

	// 3. Replace in-loop GetField uses with phi, then delete them.
	for _, g := range p.gets {
		replaceAllUses(fn, g.ID, phi)
	}
	for _, g := range p.gets {
		removeInstr(g.Block, g)
	}

	// Normalize phi.Args[1] in case replaceAllUses touched storeInstr.
	phi.Args[1] = storeInstr.Args[1]

	// 4. Remove the in-loop SetField.
	removeInstr(storeInstr.Block, storeInstr)

	// 5. Insert exit-block SetField(obj, field, phi) after any phis.
	exitStore := &Instr{
		ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown,
		Args: []*Value{objVal, phi.Value()}, Aux: fieldAux, Block: exitBlock,
	}
	insertAtTopAfterPhis(exitBlock, exitStore)
}

// insertBeforeTerminator appends instr to b just before b's terminator.
func insertBeforeTerminator(b *Block, instr *Instr) {
	n := len(b.Instrs)
	if n == 0 {
		b.Instrs = []*Instr{instr}
		return
	}
	last := b.Instrs[n-1]
	if last.Op.IsTerminator() {
		b.Instrs = append(b.Instrs[:n-1], instr, last)
		return
	}
	b.Instrs = append(b.Instrs, instr)
}

// insertAtTopAfterPhis inserts instr at the beginning of b's list,
// after any leading phis.
func insertAtTopAfterPhis(b *Block, instr *Instr) {
	idx := 0
	for idx < len(b.Instrs) && b.Instrs[idx].Op == OpPhi {
		idx++
	}
	b.Instrs = append(b.Instrs, nil)
	copy(b.Instrs[idx+1:], b.Instrs[idx:])
	b.Instrs[idx] = instr
}

// removeInstr removes instr from b.Instrs by pointer identity.
func removeInstr(b *Block, instr *Instr) {
	if b == nil {
		return
	}
	for i, x := range b.Instrs {
		if x == instr {
			b.Instrs = append(b.Instrs[:i], b.Instrs[i+1:]...)
			return
		}
	}
}
