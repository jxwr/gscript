package methodjit

// BoolTableCountLoopPass replaces a side-effect-free counted scan over a
// bool table with a guarded bulk count op. It is deliberately shaped around
// the generic CFG pattern produced by:
//
//	count := 0
//	for i := start; i <= end; i++ {
//	    if flags[i] { count = count + 1 }
//	}
//
// The generated op has a guarded packed-bool native path; the TieringManager
// table-exit path resumes with VM table-get semantics for guard misses.
func BoolTableCountLoopPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		cand, ok := detectBoolTableCountLoop(fn, header)
		if !ok || !boolCountLoopDefsAreLocal(fn, cand) {
			continue
		}
		countID := applyBoolTableCountLoop(fn, cand)
		functionRemarks(fn).Add("BoolTableCountLoop", "changed", cand.preheader.ID, countID, OpTableBoolArrayCount,
			"replaced bool table truthy scan loop with bulk bool-array count")
	}
	return fn, nil
}

type boolCountLoopCandidate struct {
	preheader *Block
	header    *Block
	loadBlock *Block
	incBlock  *Block
	exit      *Block
	table     *Value
	start     *Value
	end       *Value
	countPhi  *Instr
}

func detectBoolTableCountLoop(fn *Function, header *Block) (boolCountLoopCandidate, bool) {
	var zero boolCountLoopCandidate
	if fn == nil || header == nil || len(header.Preds) != 3 || len(header.Succs) != 2 || len(header.Instrs) == 0 {
		return zero, false
	}
	term := header.Instrs[len(header.Instrs)-1]
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 {
		return zero, false
	}
	cond := term.Args[0]
	if cond == nil || cond.Def == nil || cond.Def.Op != OpLeInt || len(cond.Def.Args) < 2 {
		return zero, false
	}
	key := cond.Def.Args[0]
	end := cond.Def.Args[1]
	if key == nil || key.Def == nil || key.Def.Op != OpAddInt {
		return zero, false
	}
	keyPhi, step, ok := boolFillLoopPhiAndStep(key.Def)
	if !ok || keyPhi == nil || keyPhi.Block != header || !boolFillStepIsOne(step) || len(keyPhi.Args) != len(header.Preds) {
		return zero, false
	}

	loadBlock, exit := header.Succs[0], header.Succs[1]
	if loadBlock == nil || exit == nil || len(loadBlock.Succs) != 2 || len(loadBlock.Instrs) == 0 {
		return zero, false
	}
	loadTerm := loadBlock.Instrs[len(loadBlock.Instrs)-1]
	if loadTerm == nil || loadTerm.Op != OpBranch || len(loadTerm.Args) != 1 || loadTerm.Args[0] == nil ||
		loadTerm.Args[0].Def == nil || loadTerm.Args[0].Def.Op != OpGuardTruthy {
		return zero, false
	}
	incBlock := loadBlock.Succs[0]
	if incBlock == nil || loadBlock.Succs[1] != header || len(incBlock.Succs) != 1 || incBlock.Succs[0] != header {
		return zero, false
	}

	preheaderIdx, loadIdx, incIdx := -1, -1, -1
	for i, pred := range header.Preds {
		switch pred {
		case loadBlock:
			loadIdx = i
		case incBlock:
			incIdx = i
		default:
			if preheaderIdx >= 0 {
				return zero, false
			}
			preheaderIdx = i
		}
	}
	if preheaderIdx < 0 || loadIdx < 0 || incIdx < 0 {
		return zero, false
	}
	preheader := header.Preds[preheaderIdx]
	if preheader == nil {
		return zero, false
	}
	if keyPhi.Args[loadIdx] == nil || keyPhi.Args[loadIdx].ID != key.ID ||
		keyPhi.Args[incIdx] == nil || keyPhi.Args[incIdx].ID != key.ID {
		return zero, false
	}
	init := keyPhi.Args[preheaderIdx]
	if init == nil || init.Def == nil || init.Def.Op != OpConstInt {
		return zero, false
	}
	start := (&Instr{Op: OpConstInt, Type: TypeInt, Aux: init.Def.Aux + 1}).Value()

	load, table, ok := boolCountLoopLoad(loadBlock, key)
	if !ok || loadTerm.Args[0].Def.Args[0] == nil || loadTerm.Args[0].Def.Args[0].ID != load.ID {
		return zero, false
	}
	incAdd := singleBoolCountIncrement(incBlock)
	if incAdd == nil {
		return zero, false
	}
	countPhi := boolCountLoopCountPhi(header, preheaderIdx, loadIdx, incIdx, incAdd)
	if countPhi == nil {
		return zero, false
	}
	if !boolCountRangeFitsInt48(fn, start, end) {
		return zero, false
	}

	return boolCountLoopCandidate{
		preheader: preheader,
		header:    header,
		loadBlock: loadBlock,
		incBlock:  incBlock,
		exit:      exit,
		table:     table,
		start:     start,
		end:       end,
		countPhi:  countPhi,
	}, true
}

func boolCountLoopLoad(block *Block, key *Value) (*Instr, *Value, bool) {
	var load *Instr
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op == OpNop || instr.Op == OpJump || instr.Op == OpBranch || instr.Op == OpGuardTruthy {
			continue
		}
		if instr.Op != OpTableArrayLoad || len(instr.Args) < 3 || instr.Type != TypeBool {
			return nil, nil, false
		}
		if load != nil || instr.Args[2] == nil || key == nil || instr.Args[2].ID != key.ID {
			return nil, nil, false
		}
		table, ok := tableArrayLoadTableValue(instr)
		if !ok {
			return nil, nil, false
		}
		load = instr
		return load, table, true
	}
	return nil, nil, false
}

func singleBoolCountIncrement(block *Block) *Instr {
	var add *Instr
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op == OpNop || instr.Op == OpJump {
			continue
		}
		if instr.Op == OpConstInt {
			continue
		}
		if instr.Op == OpAdd || instr.Op == OpAddInt {
			if add != nil || !boolCountAddOne(instr) {
				return nil
			}
			add = instr
			continue
		}
		return nil
	}
	return add
}

func boolCountAddOne(instr *Instr) bool {
	if instr == nil || len(instr.Args) < 2 {
		return false
	}
	a, b := instr.Args[0], instr.Args[1]
	if c, ok := constIntFromValue(a); ok && c == 1 && b != nil {
		return true
	}
	if c, ok := constIntFromValue(b); ok && c == 1 && a != nil {
		return true
	}
	return false
}

func boolCountLoopCountPhi(header *Block, preheaderIdx, loadIdx, incIdx int, incAdd *Instr) *Instr {
	for _, phi := range header.Instrs {
		if phi == nil || phi.Op != OpPhi || len(phi.Args) != len(header.Preds) {
			continue
		}
		init, ok := constIntFromValue(phi.Args[preheaderIdx])
		if !ok || init != 0 {
			continue
		}
		if phi.Args[loadIdx] == nil || phi.Args[loadIdx].ID != phi.ID {
			continue
		}
		if phi.Args[incIdx] == nil || phi.Args[incIdx].ID != incAdd.ID {
			continue
		}
		if !boolCountIncrementUsesPhi(incAdd, phi.ID) {
			continue
		}
		return phi
	}
	return nil
}

func boolCountIncrementUsesPhi(add *Instr, phiID int) bool {
	if add == nil || len(add.Args) < 2 {
		return false
	}
	return (add.Args[0] != nil && add.Args[0].ID == phiID) ||
		(add.Args[1] != nil && add.Args[1].ID == phiID)
}

func boolCountRangeFitsInt48(fn *Function, start, end *Value) bool {
	startRange := boolFillValueRange(fn, start)
	endRange := boolFillValueRange(fn, end)
	if !startRange.known || !endRange.known {
		return false
	}
	if startRange.min < 0 {
		return false
	}
	if endRange.max < startRange.min {
		return true
	}
	maxCount := endRange.max - startRange.min + 1
	return maxCount >= 0 && maxCount <= MaxInt48
}

func boolCountLoopDefsAreLocal(fn *Function, cand boolCountLoopCandidate) bool {
	removed := map[int]bool{cand.header.ID: true, cand.loadBlock.ID: true, cand.incBlock.ID: true}
	defs := make(map[int]bool)
	for _, block := range []*Block{cand.header, cand.loadBlock, cand.incBlock} {
		for _, instr := range block.Instrs {
			if instr != nil && !instr.Op.IsTerminator() {
				defs[instr.ID] = true
			}
		}
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			for _, arg := range instr.Args {
				if arg == nil || !defs[arg.ID] || removed[block.ID] {
					continue
				}
				if block == cand.exit && cand.countPhi != nil && arg.ID == cand.countPhi.ID {
					continue
				}
				return false
			}
		}
	}
	return true
}

func applyBoolTableCountLoop(fn *Function, cand boolCountLoopCandidate) int {
	start := cand.start
	var inserted []*Instr
	if start != nil && start.Def != nil && start.Def.ID == 0 && start.Def.Block == nil && start.Def.Op == OpConstInt {
		startInstr := &Instr{
			ID:    fn.nextID,
			Op:    OpConstInt,
			Type:  TypeInt,
			Aux:   start.Def.Aux,
			Block: cand.preheader,
		}
		fn.nextID++
		start = startInstr.Value()
		inserted = append(inserted, startInstr)
	}
	count := &Instr{
		ID:    fn.nextID,
		Op:    OpTableBoolArrayCount,
		Type:  TypeInt,
		Args:  []*Value{cand.table, start, cand.end},
		Block: cand.preheader,
	}
	fn.nextID++
	inserted = append(inserted, count)

	insertAt := len(cand.preheader.Instrs)
	if insertAt > 0 && cand.preheader.Instrs[insertAt-1].Op.IsTerminator() {
		insertAt--
	}
	cand.preheader.Instrs = append(cand.preheader.Instrs[:insertAt], append(inserted, cand.preheader.Instrs[insertAt:]...)...)
	cand.preheader.Succs = []*Block{cand.exit}

	for i, pred := range cand.exit.Preds {
		if pred == cand.header {
			cand.exit.Preds[i] = cand.preheader
		}
	}
	replaceValueUsesOutsideBlocks(fn, cand.countPhi.ID, count.Value(), map[int]bool{
		cand.header.ID:    true,
		cand.loadBlock.ID: true,
		cand.incBlock.ID:  true,
	})

	filtered := fn.Blocks[:0]
	for _, block := range fn.Blocks {
		if block == cand.header || block == cand.loadBlock || block == cand.incBlock {
			continue
		}
		filtered = append(filtered, block)
	}
	fn.Blocks = filtered
	return count.ID
}

func replaceValueUsesOutsideBlocks(fn *Function, oldID int, replacement *Value, excluded map[int]bool) {
	for _, block := range fn.Blocks {
		if block == nil || excluded[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			for i, arg := range instr.Args {
				if arg != nil && arg.ID == oldID {
					instr.Args[i] = replacement
				}
			}
		}
	}
}
