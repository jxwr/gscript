package methodjit

import "github.com/gscript/gscript/internal/vm"

// BoolTableFillLoopPass replaces a narrow counted loop that only writes a
// contiguous constant bool range into a local table with one bulk fill op.
// The rewrite is intentionally conservative: stride must be exactly one, the
// loop-carried values must not escape the removed loop, and the SetTable site
// must already be known as a bool-array write.
func BoolTableFillLoopPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		cand, ok := detectBoolTableFillLoop(header)
		if !ok || !boolFillLoopDefsAreLocal(fn, cand) {
			continue
		}
		fillID := applyBoolTableFillLoop(fn, cand)
		functionRemarks(fn).Add("BoolTableFillLoop", "changed", cand.preheader.ID, fillID, OpTableBoolArrayFill,
			"replaced contiguous const-bool table initialization loop with bulk bool-array fill")
	}
	return fn, nil
}

type boolFillLoopCandidate struct {
	preheader *Block
	header    *Block
	body      *Block
	exit      *Block
	table     *Value
	end       *Value
	start     int64
	byteVal   int64
}

func detectBoolTableFillLoop(header *Block) (boolFillLoopCandidate, bool) {
	var zero boolFillLoopCandidate
	if header == nil || len(header.Preds) != 2 || len(header.Succs) != 2 || len(header.Instrs) == 0 {
		return zero, false
	}
	term := header.Instrs[len(header.Instrs)-1]
	if term == nil || term.Op != OpBranch || len(term.Args) != 1 {
		return zero, false
	}
	body, exit := header.Succs[0], header.Succs[1]
	if body == nil || exit == nil || len(body.Preds) != 1 || body.Preds[0] != header || len(body.Succs) != 1 || body.Succs[0] != header {
		return zero, false
	}
	bodyPredIdx := -1
	for i, pred := range header.Preds {
		if pred == body {
			bodyPredIdx = i
			break
		}
	}
	if bodyPredIdx < 0 {
		return zero, false
	}
	preheaderIdx := 1 - bodyPredIdx
	preheader := header.Preds[preheaderIdx]
	if preheader == nil {
		return zero, false
	}

	store := singleBoolFillBodyStore(body)
	if store == nil || len(store.Args) < 3 || store.Aux2 != int64(vm.FBKindBool) {
		return zero, false
	}
	table, key, val := store.Args[0], store.Args[1], store.Args[2]
	if table == nil || key == nil || val == nil || val.Def == nil || val.Def.Op != OpConstBool {
		return zero, false
	}
	if table.Def == nil || table.Def.Op != OpNewTable {
		return zero, false
	}

	cond := term.Args[0]
	if cond == nil || cond.Def == nil || cond.Def.Op != OpLeInt || len(cond.Def.Args) < 2 {
		return zero, false
	}
	if cond.Def.Args[0] == nil || cond.Def.Args[0].ID != key.ID || cond.Def.Args[1] == nil {
		return zero, false
	}
	add := key.Def
	if add == nil || add.Op != OpAddInt || len(add.Args) < 2 {
		return zero, false
	}
	phi, step, ok := boolFillLoopPhiAndStep(add)
	if !ok || phi.Block != header || len(phi.Args) != len(header.Preds) {
		return zero, false
	}
	if step.Def == nil || step.Def.Op != OpConstInt || step.Def.Aux != 1 {
		return zero, false
	}
	if phi.Args[bodyPredIdx] == nil || phi.Args[bodyPredIdx].ID != key.ID {
		return zero, false
	}
	init := phi.Args[preheaderIdx]
	if init == nil || init.Def == nil || init.Def.Op != OpConstInt {
		return zero, false
	}
	byteVal := int64(1)
	if val.Def.Aux != 0 {
		byteVal = 2
	}
	return boolFillLoopCandidate{
		preheader: preheader,
		header:    header,
		body:      body,
		exit:      exit,
		table:     table,
		end:       cond.Def.Args[1],
		start:     init.Def.Aux + 1,
		byteVal:   byteVal,
	}, true
}

func singleBoolFillBodyStore(body *Block) *Instr {
	var store *Instr
	for _, instr := range body.Instrs {
		if instr == nil || instr.Op == OpNop || instr.Op == OpJump {
			continue
		}
		if instr.Op == OpSetTable {
			if store != nil {
				return nil
			}
			store = instr
			continue
		}
		if instr.Op == OpConstInt || instr.Op == OpConstBool || instr.Op == OpConstNil {
			continue
		}
		return nil
	}
	return store
}

func boolFillLoopPhiAndStep(add *Instr) (*Instr, *Value, bool) {
	a, b := add.Args[0], add.Args[1]
	if a != nil && a.Def != nil && a.Def.Op == OpPhi {
		return a.Def, b, b != nil
	}
	if b != nil && b.Def != nil && b.Def.Op == OpPhi {
		return b.Def, a, a != nil
	}
	return nil, nil, false
}

func boolFillLoopDefsAreLocal(fn *Function, cand boolFillLoopCandidate) bool {
	removed := map[int]bool{cand.header.ID: true, cand.body.ID: true}
	defs := make(map[int]bool)
	for _, block := range []*Block{cand.header, cand.body} {
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
				return false
			}
		}
	}
	return true
}

func applyBoolTableFillLoop(fn *Function, cand boolFillLoopCandidate) int {
	start := &Instr{
		ID:    fn.nextID,
		Op:    OpConstInt,
		Type:  TypeInt,
		Aux:   cand.start,
		Block: cand.preheader,
	}
	fn.nextID++
	fill := &Instr{
		ID:    fn.nextID,
		Op:    OpTableBoolArrayFill,
		Type:  TypeUnknown,
		Args:  []*Value{cand.table, start.Value(), cand.end},
		Aux:   cand.byteVal,
		Block: cand.preheader,
	}
	fn.nextID++

	insertAt := len(cand.preheader.Instrs)
	if insertAt > 0 && cand.preheader.Instrs[insertAt-1].Op.IsTerminator() {
		insertAt--
	}
	inserted := []*Instr{start, fill}
	cand.preheader.Instrs = append(cand.preheader.Instrs[:insertAt], append(inserted, cand.preheader.Instrs[insertAt:]...)...)
	cand.preheader.Succs = []*Block{cand.exit}

	for i, pred := range cand.exit.Preds {
		if pred == cand.header {
			cand.exit.Preds[i] = cand.preheader
		}
	}

	filtered := fn.Blocks[:0]
	for _, block := range fn.Blocks {
		if block == cand.header || block == cand.body {
			continue
		}
		filtered = append(filtered, block)
	}
	fn.Blocks = filtered
	return fill.ID
}
