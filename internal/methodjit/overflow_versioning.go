package methodjit

// shiftAddOverflowVersion describes a loop-local recurrence of the form:
//
//	x' = y
//	y' = x + y
//
// guarded by a simple integer induction loop. Codegen can run the int prefix
// in raw registers and switch to a float continuation when x+y exceeds int48.
type shiftAddOverflowVersion struct {
	entry  *Block
	header *Block
	body   *Block
	exit   *Block

	leftPhi  *Instr
	rightPhi *Instr
	add      *Instr
	ret      *Instr

	counterPhi *Instr
	counterAdd *Instr
	cond       *Instr

	leftInit    *Value
	rightInit   *Value
	counterInit *Value
	bound       *Value
	step        int64

	leftInitConst    int64
	rightInitConst   int64
	counterInitConst int64
	boundParamSlot   int
	boundAdjust      int64

	hasCheckFreePrefix   bool
	safeLastCounter      int64
	firstOverflowCounter int64
}

func detectShiftAddOverflowVersion(fn *Function) (*shiftAddOverflowVersion, bool) {
	if fn == nil || fn.Entry == nil || len(fn.Blocks) == 0 {
		return nil, false
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return nil, false
	}

	var found *shiftAddOverflowVersion
	for _, header := range fn.Blocks {
		if !li.loopHeaders[header.ID] {
			continue
		}
		spec, ok := detectShiftAddOverflowVersionAt(fn, li, header)
		if !ok {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = spec
	}
	return found, found != nil
}

func detectShiftAddOverflowVersionAt(fn *Function, li *loopInfo, header *Block) (*shiftAddOverflowVersion, bool) {
	if header == nil || len(header.Preds) != 2 {
		return nil, false
	}
	term := blockTerminator(header)
	if term == nil || term.Op != OpBranch || len(term.Args) == 0 || term.Args[0] == nil {
		return nil, false
	}
	body, exit, ok := branchLoopBodyAndExit(li, header, term)
	if !ok || body == nil || exit == nil {
		return nil, false
	}
	entry := nonLoopPred(header, li.headerBlocks[header.ID])
	if entry == nil || entry != fn.Entry || blockTerminator(entry) == nil || blockTerminator(entry).Op != OpJump {
		return nil, false
	}

	add := soleBodyAdd(body, header)
	if add == nil || add.Op != OpAdd || len(add.Args) < 2 {
		return nil, false
	}
	leftPhi := headerPhiValue(add.Args[0], header)
	rightPhi := headerPhiValue(add.Args[1], header)
	if leftPhi == nil || rightPhi == nil || leftPhi == rightPhi {
		return nil, false
	}
	if !phisFormShiftAdd(leftPhi, rightPhi, add, body, entry) {
		return nil, false
	}

	ret := blockTerminator(exit)
	if ret == nil || ret.Op != OpReturn || len(ret.Args) == 0 || ret.Args[0] == nil || ret.Args[0].ID != leftPhi.ID {
		return nil, false
	}

	cond := term.Args[0].Def
	counterPhi, counterAdd, bound, step, ok := detectVersionedCounter(header, cond)
	if !ok {
		return nil, false
	}
	if phiBackedgeArg(counterPhi, body) == nil || phiBackedgeArg(counterPhi, body).ID != counterAdd.ID {
		return nil, false
	}
	counterInit := phiPredArg(counterPhi, entry)
	if counterInit == nil {
		return nil, false
	}
	leftInit := phiPredArg(leftPhi, entry)
	rightInit := phiPredArg(rightPhi, entry)
	leftInitConst, ok := constIntFromValue(leftInit)
	if !ok {
		return nil, false
	}
	rightInitConst, ok := constIntFromValue(rightInit)
	if !ok {
		return nil, false
	}
	counterInitConst, ok := constIntFromValue(counterInit)
	if !ok {
		return nil, false
	}
	boundSlot, boundAdjust, ok := shiftAddBoundParamAdjust(bound)
	if !ok || boundAdjust < -4095 || boundAdjust > 4095 {
		return nil, false
	}
	if !headerHasOnlyShiftAddVersionOps(header, leftPhi, rightPhi, counterPhi, counterAdd, cond, term) {
		return nil, false
	}

	hasCheckFreePrefix, safeLastCounter, firstOverflowCounter :=
		shiftAddCheckFreePrefix(leftInitConst, rightInitConst, counterInitConst, step)

	return &shiftAddOverflowVersion{
		entry:                entry,
		header:               header,
		body:                 body,
		exit:                 exit,
		leftPhi:              leftPhi,
		rightPhi:             rightPhi,
		add:                  add,
		ret:                  ret,
		counterPhi:           counterPhi,
		counterAdd:           counterAdd,
		cond:                 cond,
		leftInit:             leftInit,
		rightInit:            rightInit,
		counterInit:          counterInit,
		bound:                bound,
		step:                 step,
		leftInitConst:        leftInitConst,
		rightInitConst:       rightInitConst,
		counterInitConst:     counterInitConst,
		boundParamSlot:       boundSlot,
		boundAdjust:          boundAdjust,
		hasCheckFreePrefix:   hasCheckFreePrefix,
		safeLastCounter:      safeLastCounter,
		firstOverflowCounter: firstOverflowCounter,
	}, true
}

func shiftAddCheckFreePrefix(left, right, counterInit, step int64) (bool, int64, int64) {
	if step <= 0 {
		return false, 0, 0
	}
	if !fitsSignedInt48(left) || !fitsSignedInt48(right) {
		return false, 0, 0
	}
	counter := counterInit
	for trips := int64(0); trips < 4096; trips++ {
		nextCounter := counter + step
		sum := left + right
		if !fitsSignedInt48(sum) {
			safeLastCounter := counter
			firstOverflowCounter := nextCounter
			if safeLastCounter < 0 || safeLastCounter > 4095 ||
				firstOverflowCounter < 0 || firstOverflowCounter > 4095 {
				return false, 0, 0
			}
			return true, safeLastCounter, firstOverflowCounter
		}
		left, right, counter = right, sum, nextCounter
	}
	return false, 0, 0
}

func fitsSignedInt48(v int64) bool {
	return v >= MinInt48 && v <= MaxInt48
}

func branchLoopBodyAndExit(li *loopInfo, header *Block, branch *Instr) (*Block, *Block, bool) {
	if branch == nil || header == nil || li == nil {
		return nil, nil, false
	}
	if len(header.Succs) < 2 {
		return nil, nil, false
	}
	trueBlock := header.Succs[0]
	falseBlock := header.Succs[1]
	bodySet := li.headerBlocks[header.ID]
	trueInLoop := bodySet[trueBlock.ID]
	falseInLoop := bodySet[falseBlock.ID]
	if trueInLoop == falseInLoop {
		return nil, nil, false
	}
	if trueInLoop {
		return trueBlock, falseBlock, true
	}
	return falseBlock, trueBlock, true
}

func nonLoopPred(header *Block, body map[int]bool) *Block {
	if header == nil {
		return nil
	}
	var pred *Block
	for _, p := range header.Preds {
		if p == nil || body[p.ID] {
			continue
		}
		if pred != nil {
			return nil
		}
		pred = p
	}
	return pred
}

func soleBodyAdd(body, header *Block) *Instr {
	if body == nil || header == nil {
		return nil
	}
	term := blockTerminator(body)
	if term == nil || term.Op != OpJump || len(body.Succs) == 0 || body.Succs[0] != header {
		return nil
	}
	var add *Instr
	for _, instr := range body.Instrs {
		switch instr.Op {
		case OpNop, OpJump:
			continue
		case OpAdd:
			if add != nil {
				return nil
			}
			add = instr
		default:
			return nil
		}
	}
	return add
}

func phisFormShiftAdd(leftPhi, rightPhi, add *Instr, body, entry *Block) bool {
	if leftPhi == nil || rightPhi == nil || add == nil || body == nil || entry == nil {
		return false
	}
	leftInit := phiPredArg(leftPhi, entry)
	rightInit := phiPredArg(rightPhi, entry)
	if leftInit == nil || rightInit == nil || !valueIsIntegerSeed(leftInit) || !valueIsIntegerSeed(rightInit) {
		return false
	}
	leftBack := phiBackedgeArg(leftPhi, body)
	rightBack := phiBackedgeArg(rightPhi, body)
	if leftBack == nil || rightBack == nil {
		return false
	}
	return leftBack.ID == rightPhi.ID && rightBack.ID == add.ID
}

func detectVersionedCounter(header *Block, cond *Instr) (*Instr, *Instr, *Value, int64, bool) {
	if header == nil || cond == nil || len(cond.Args) < 2 {
		return nil, nil, nil, 0, false
	}
	switch cond.Op {
	case OpLeInt, OpLtInt:
	default:
		return nil, nil, nil, 0, false
	}
	counterAdd := cond.Args[0].Def
	if counterAdd == nil || counterAdd.Op != OpAddInt || counterAdd.Block != header || len(counterAdd.Args) < 2 {
		return nil, nil, nil, 0, false
	}
	var counterPhi *Instr
	var step int64
	if p := headerPhiValue(counterAdd.Args[0], header); p != nil {
		counterPhi = p
		if s, ok := constIntFromValue(counterAdd.Args[1]); ok {
			step = s
		}
	} else if p := headerPhiValue(counterAdd.Args[1], header); p != nil {
		counterPhi = p
		if s, ok := constIntFromValue(counterAdd.Args[0]); ok {
			step = s
		}
	}
	if counterPhi == nil || counterPhi.Type != TypeInt || step <= 0 || step > 4095 {
		return nil, nil, nil, 0, false
	}
	return counterPhi, counterAdd, cond.Args[1], step, true
}

func headerHasOnlyShiftAddVersionOps(header *Block, leftPhi, rightPhi, counterPhi, counterAdd, cond, branch *Instr) bool {
	if header == nil {
		return false
	}
	allowed := map[int]bool{
		leftPhi.ID:    true,
		rightPhi.ID:   true,
		counterPhi.ID: true,
		counterAdd.ID: true,
		cond.ID:       true,
		branch.ID:     true,
	}
	for _, instr := range header.Instrs {
		if instr.Op == OpNop {
			continue
		}
		if !allowed[instr.ID] {
			return false
		}
	}
	return true
}

func phiPredArg(phi *Instr, pred *Block) *Value {
	if phi == nil || pred == nil || phi.Block == nil {
		return nil
	}
	for i, p := range phi.Block.Preds {
		if p == pred && i < len(phi.Args) {
			return phi.Args[i]
		}
	}
	return nil
}

func phiBackedgeArg(phi *Instr, body *Block) *Value {
	return phiPredArg(phi, body)
}

func valueIsIntegerSeed(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	return v.Def.Type == TypeInt || v.Def.Op == OpConstInt || v.Def.Op == OpGuardType && Type(v.Def.Aux) == TypeInt || v.Def.Op == OpGuardIntRange
}

func shiftAddBoundParamAdjust(v *Value) (slot int, adjust int64, ok bool) {
	if slot, ok := guardedLoadSlot(v); ok {
		return slot, 0, true
	}
	if v == nil || v.Def == nil || len(v.Def.Args) < 2 {
		return 0, 0, false
	}
	switch v.Def.Op {
	case OpAddInt:
		if slot, ok := guardedLoadSlot(v.Def.Args[0]); ok {
			if c, ok := constIntFromValue(v.Def.Args[1]); ok {
				return slot, c, true
			}
		}
		if slot, ok := guardedLoadSlot(v.Def.Args[1]); ok {
			if c, ok := constIntFromValue(v.Def.Args[0]); ok {
				return slot, c, true
			}
		}
	case OpSubInt:
		if slot, ok := guardedLoadSlot(v.Def.Args[0]); ok {
			if c, ok := constIntFromValue(v.Def.Args[1]); ok {
				return slot, -c, true
			}
		}
	}
	return 0, 0, false
}

func guardedLoadSlot(v *Value) (int, bool) {
	if v == nil || v.Def == nil {
		return 0, false
	}
	if v.Def.Op == OpGuardType && Type(v.Def.Aux) == TypeInt && len(v.Def.Args) > 0 {
		v = v.Def.Args[0]
	}
	if v != nil && v.Def != nil && v.Def.Op == OpGuardIntRange && len(v.Def.Args) > 0 {
		v = v.Def.Args[0]
	}
	if v == nil || v.Def == nil || v.Def.Op != OpLoadSlot {
		return 0, false
	}
	return int(v.Def.Aux), true
}
