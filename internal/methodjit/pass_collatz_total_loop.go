package methodjit

// CollatzTotalLoopPass lowers a positive integer loop that totals Collatz
// sequence lengths. The match is structural and does not inspect function names.
func CollatzTotalLoopPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if lowerCollatzTotalOuterHeader(fn, header) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func lowerCollatzTotalOuterHeader(fn *Function, outer *Block) bool {
	if fn == nil || outer == nil || len(outer.Preds) != 2 || len(outer.Succs) != 2 {
		return false
	}
	totalPhi, indexPhi := firstTwoPhis(outer)
	if totalPhi == nil || indexPhi == nil {
		return false
	}
	next, limit, ok := parseUnitBoundedLoopHeader(outer, indexPhi)
	if !ok {
		return false
	}
	body := jumpTarget(outer.Succs[0])
	exit := outer.Succs[1]
	if body == nil || exit == nil || !exitReturnsValue(exit, totalPhi.Value()) {
		return false
	}
	inner := jumpTarget(body)
	if !validCollatzInnerLoop(inner, next) {
		return false
	}
	addBlock := inner.Succs[0]
	if addBlock == nil || !addsInnerStepsToTotal(addBlock, totalPhi.Value(), inner) || !containsBlock(addBlock.Succs, outer) {
		return false
	}
	op := &Instr{
		ID:    fn.newValueID(),
		Op:    OpCollatzTotalLoop,
		Type:  TypeInt,
		Args:  []*Value{limit},
		Block: outer,
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{op.Value()}, Block: outer}
	outer.Instrs = []*Instr{op, ret}
	outer.Preds = []*Block{outer.Preds[0]}
	outer.Succs = nil
	functionRemarks(fn).Add("CollatzTotalLoop", "changed", outer.ID, op.ID, op.Op,
		"lowered Collatz total loop")
	return true
}

func validCollatzInnerLoop(header *Block, start *Value) bool {
	if header == nil || len(header.Preds) != 2 || len(header.Succs) != 2 {
		return false
	}
	stepsPhi, xPhi := firstTwoPhis(header)
	if stepsPhi == nil || xPhi == nil || start == nil {
		return false
	}
	_ = stepsPhi
	hasDoneTest := false
	for _, instr := range header.Instrs {
		if instr != nil && instr.Op == OpEqInt && len(instr.Args) == 2 && sameSSAValue(instr.Args[0], xPhi.Value()) && isConstIntValue(instr.Args[1], 1) {
			hasDoneTest = true
		}
	}
	if !hasDoneTest {
		return false
	}
	branch := header.Succs[1]
	if branch == nil || len(branch.Succs) != 2 {
		return false
	}
	even := branch.Succs[0]
	odd := branch.Succs[1]
	join := (*Block)(nil)
	if even != nil && len(even.Succs) == 1 {
		join = even.Succs[0]
	}
	if join == nil || odd == nil || len(odd.Succs) != 1 || odd.Succs[0] != join || !containsBlock(join.Succs, header) {
		return false
	}
	return blockHasDivByTwo(even, xPhi.Value()) && blockHasTriplePlusOne(odd, xPhi.Value()) && blockAddsOne(join, stepsPhi.Value())
}

func addsInnerStepsToTotal(block *Block, total *Value, inner *Block) bool {
	stepsPhi, _ := firstTwoPhis(inner)
	if block == nil || total == nil || stepsPhi == nil {
		return false
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpAddInt || len(instr.Args) != 2 {
			continue
		}
		if (sameSSAValue(instr.Args[0], total) && sameSSAValue(instr.Args[1], stepsPhi.Value())) ||
			(sameSSAValue(instr.Args[1], total) && sameSSAValue(instr.Args[0], stepsPhi.Value())) {
			return true
		}
	}
	return false
}

func blockHasDivByTwo(block *Block, x *Value) bool {
	if block == nil {
		return false
	}
	for _, instr := range block.Instrs {
		if instr != nil && instr.Op == OpDivIntExact && len(instr.Args) == 2 && sameSSAValue(instr.Args[0], x) && isConstIntValue(instr.Args[1], 2) {
			return true
		}
	}
	return false
}

func blockHasTriplePlusOne(block *Block, x *Value) bool {
	if block == nil {
		return false
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpAddInt || len(instr.Args) != 2 {
			continue
		}
		for side := 0; side < 2; side++ {
			m := instr.Args[side]
			c := instr.Args[1-side]
			if !isConstIntValue(c, 1) || m == nil || m.Def == nil || m.Def.Op != OpMulInt || len(m.Def.Args) != 2 {
				continue
			}
			if (sameSSAValue(m.Def.Args[0], x) && isConstIntValue(m.Def.Args[1], 3)) ||
				(sameSSAValue(m.Def.Args[1], x) && isConstIntValue(m.Def.Args[0], 3)) {
				return true
			}
		}
	}
	return false
}

func blockAddsOne(block *Block, steps *Value) bool {
	if block == nil || steps == nil {
		return false
	}
	for _, instr := range block.Instrs {
		if instr != nil && instr.Op == OpAddInt && len(instr.Args) == 2 &&
			((sameSSAValue(instr.Args[0], steps) && isConstIntValue(instr.Args[1], 1)) ||
				(sameSSAValue(instr.Args[1], steps) && isConstIntValue(instr.Args[0], 1))) {
			return true
		}
	}
	return false
}
