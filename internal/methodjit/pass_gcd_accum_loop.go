package methodjit

// EuclideanReductionLoopPass lowers a nested affine Euclidean-GCD reduction into a native
// loop body. It is intentionally keyed on IR shape, not on function or source
// names.
func EuclideanReductionLoopPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if lowerGcdAccumOuterHeader(fn, header) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func lowerGcdAccumOuterHeader(fn *Function, outer *Block) bool {
	if fn == nil || outer == nil || len(outer.Preds) != 2 || len(outer.Succs) != 2 {
		return false
	}
	pre, outerBack := splitPreAndBackByPhiConstZero(outer)
	if pre == nil || outerBack == nil {
		return false
	}
	outerTotalPhi, outerIndexPhi := firstTwoPhis(outer)
	if outerTotalPhi == nil || outerIndexPhi == nil {
		return false
	}
	outerNext, outerLimit, ok := parseUnitBoundedLoopHeader(outer, outerIndexPhi)
	if !ok {
		return false
	}
	outerBodyPre := jumpTarget(outer.Succs[0])
	exit := outer.Succs[1]
	if outerBodyPre == nil || exit == nil {
		return false
	}
	aMul, aAdd, inner, ok := parseAffineThenJump(outerBodyPre, outerNext)
	if !ok {
		aMul, aAdd, inner, ok = findAffineThenJump(fn, outerNext)
	}
	if !ok || inner == nil {
		return false
	}
	innerTotalPhi, innerIndexPhi := firstTwoPhis(inner)
	if innerTotalPhi == nil || innerIndexPhi == nil {
		return false
	}
	innerNext, innerLimit, ok := parseUnitBoundedLoopHeader(inner, innerIndexPhi)
	if !ok {
		innerNext, innerLimit, ok = parseAnyUnitBoundedLoopHeader(inner)
	}
	if !ok {
		return false
	}
	bBlock := inner.Succs[0]
	if bBlock == nil {
		return false
	}
	bMul, bAdd, gcdPre, ok := parseAffineThenJump(bBlock, innerNext)
	if !ok {
		bMul, bAdd, gcdPre, ok = findAffineThenJump(fn, innerNext)
	}
	if !ok || gcdPre == nil {
		return false
	}
	gcdHeader := jumpTarget(gcdPre)
	if !validGcdLoop(gcdHeader) {
		return false
	}
	addBlock := gcdHeader.Succs[0]
	if addBlock == nil || !addsGcdToTotal(addBlock, innerTotalPhi.Value(), gcdHeader) || !containsBlock(addBlock.Succs, inner) {
		return false
	}
	if !exitReturnsValue(exit, outerTotalPhi.Value()) {
		return false
	}

	op := &Instr{
		ID:    fn.newValueID(),
		Op:    OpEuclideanReductionLoop,
		Type:  TypeInt,
		Args:  []*Value{outerLimit, innerLimit, aMul, aAdd, bMul, bAdd},
		Block: outer,
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{op.Value()}, Block: outer}
	outer.Instrs = []*Instr{op, ret}
	outer.Preds = []*Block{pre}
	outer.Succs = nil
	functionRemarks(fn).Add("EuclideanReductionLoop", "changed", outer.ID, op.ID, op.Op,
		"lowered nested affine Euclidean-GCD reduction loop")
	return true
}

func splitPreAndBackByPhiConstZero(header *Block) (*Block, *Block) {
	phi := firstIntPhi(header)
	if phi == nil || len(phi.Args) != len(header.Preds) {
		return nil, nil
	}
	for i, arg := range phi.Args {
		if isConstIntValue(arg, 0) {
			if i == 0 && len(header.Preds) > 1 {
				return header.Preds[0], header.Preds[1]
			}
			if len(header.Preds) > 1 {
				return header.Preds[i], header.Preds[1-i]
			}
		}
	}
	return nil, nil
}

func firstIntPhi(block *Block) *Instr {
	if block == nil {
		return nil
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpPhi {
			break
		}
		if instr.Type == TypeInt {
			return instr
		}
	}
	return nil
}

func firstTwoPhis(block *Block) (*Instr, *Instr) {
	if block == nil {
		return nil, nil
	}
	var out []*Instr
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpPhi {
			break
		}
		out = append(out, instr)
	}
	if len(out) < 2 {
		return nil, nil
	}
	return out[0], out[1]
}

func parseUnitBoundedLoopHeader(header *Block, indexPhi *Instr) (*Value, *Value, bool) {
	if header == nil || indexPhi == nil {
		return nil, nil, false
	}
	var next, limit *Value
	for _, instr := range header.Instrs {
		if instr == nil {
			continue
		}
		if instr.Op == OpAddInt && len(instr.Args) == 2 && isAddOneOf(instr.Value(), indexPhi.Value()) {
			next = instr.Value()
		}
		if instr.Op == OpLeInt && len(instr.Args) == 2 {
			limit = instr.Args[1]
		}
	}
	if next == nil || limit == nil {
		return nil, nil, false
	}
	return next, limit, true
}

func parseAnyUnitBoundedLoopHeader(header *Block) (*Value, *Value, bool) {
	if header == nil {
		return nil, nil, false
	}
	for _, instr := range header.Instrs {
		if instr == nil || instr.Op != OpLeInt || len(instr.Args) != 2 {
			continue
		}
		if add := instr.Args[0]; add != nil && add.Def != nil && add.Def.Op == OpAddInt && len(add.Def.Args) == 2 {
			return add, instr.Args[1], true
		}
	}
	return nil, nil, false
}

func parseAffineThenJump(block *Block, index *Value) (mul, add *Value, succ *Block, ok bool) {
	if block == nil {
		return nil, nil, nil, false
	}
	block = jumpTarget(block)
	if block == nil || len(block.Succs) != 1 {
		return nil, nil, nil, false
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpAddInt || len(instr.Args) != 2 {
			continue
		}
		if m, a, ok := parseAffineAdd(instr, index); ok {
			return m, a, block.Succs[0], true
		}
	}
	return nil, nil, nil, false
}

func findAffineThenJump(fn *Function, index *Value) (mul, add *Value, succ *Block, ok bool) {
	if fn == nil || index == nil {
		return nil, nil, nil, false
	}
	for _, block := range fn.Blocks {
		if block == nil || len(block.Succs) != 1 {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpAddInt || len(instr.Args) != 2 {
				continue
			}
			if m, a, ok := parseAffineAdd(instr, index); ok {
				return m, a, block.Succs[0], true
			}
		}
	}
	return nil, nil, nil, false
}

func parseAffineAdd(instr *Instr, index *Value) (mul, add *Value, ok bool) {
	for side := 0; side < 2; side++ {
		m := instr.Args[side]
		a := instr.Args[1-side]
		if a == nil || a.Def == nil || a.Def.Op != OpConstInt || m == nil || m.Def == nil || m.Def.Op != OpMulInt || len(m.Def.Args) != 2 {
			continue
		}
		if sameSSAValue(m.Def.Args[0], index) && isConstInt(m.Def.Args[1]) {
			return m.Def.Args[1], a, true
		}
		if sameSSAValue(m.Def.Args[1], index) && isConstInt(m.Def.Args[0]) {
			return m.Def.Args[0], a, true
		}
	}
	return nil, nil, false
}

func validGcdLoop(header *Block) bool {
	if header == nil || len(header.Preds) != 2 || len(header.Succs) != 2 {
		return false
	}
	aPhi, bPhi := firstTwoPhis(header)
	if aPhi == nil || bPhi == nil {
		return false
	}
	body := header.Succs[1]
	if body == nil || !containsBlock(body.Succs, header) {
		return false
	}
	hasExitTest := false
	for _, instr := range header.Instrs {
		if instr != nil && instr.Op == OpEqInt && len(instr.Args) == 2 && sameSSAValue(instr.Args[0], bPhi.Value()) && isConstIntValue(instr.Args[1], 0) {
			hasExitTest = true
		}
	}
	if !hasExitTest {
		return false
	}
	for _, instr := range body.Instrs {
		if instr != nil && instr.Op == OpModInt && len(instr.Args) == 2 && sameSSAValue(instr.Args[0], aPhi.Value()) && sameSSAValue(instr.Args[1], bPhi.Value()) {
			return true
		}
	}
	return false
}

func addsGcdToTotal(block *Block, total *Value, gcdHeader *Block) bool {
	if block == nil || gcdHeader == nil {
		return false
	}
	aPhi, _ := firstTwoPhis(gcdHeader)
	if aPhi == nil {
		return false
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpAddInt || len(instr.Args) != 2 {
			continue
		}
		if (sameSSAValue(instr.Args[0], total) && sameSSAValue(instr.Args[1], aPhi.Value())) ||
			(sameSSAValue(instr.Args[1], total) && sameSSAValue(instr.Args[0], aPhi.Value())) {
			return true
		}
	}
	return false
}

func exitReturnsValue(exit *Block, v *Value) bool {
	if exit == nil || v == nil {
		return false
	}
	for _, instr := range exit.Instrs {
		if instr != nil && instr.Op == OpReturn && len(instr.Args) == 1 && sameSSAValue(instr.Args[0], v) {
			return true
		}
	}
	return false
}

func isConstInt(v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpConstInt
}

func jumpTarget(block *Block) *Block {
	if block == nil || len(block.Instrs) != 1 || block.Instrs[0] == nil || block.Instrs[0].Op != OpJump || len(block.Succs) != 1 {
		return block
	}
	return block.Succs[0]
}
