package methodjit

import "math"

// ComplexEscapeLoopPass replaces a hot fixed-bound complex escape loop with a
// single native loop op. The trigger is the runtime-compiled IR shape, not a
// function name or whole-program bytecode signature.
func ComplexEscapeLoopPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if lowerComplexEscapeHeader(fn, header) {
			changed = true
		}
	}
	for _, header := range append([]*Block(nil), fn.Blocks...) {
		if lowerComplexEscapeRowHeader(fn, header) {
			changed = true
		}
	}
	if changed {
		removeUnreachableBlocks(fn)
	}
	return fn, nil
}

func lowerComplexEscapeRowHeader(fn *Function, header *Block) bool {
	if fn == nil || header == nil || len(header.Preds) != 3 || len(header.Succs) != 2 || len(header.Instrs) < 5 {
		return false
	}
	body := header.Succs[0]
	exit := header.Succs[1]
	if body == nil || exit == nil || len(body.Succs) != 1 {
		return false
	}
	opBlock := body.Succs[0]
	if opBlock == nil || len(opBlock.Instrs) < 2 || len(opBlock.Succs) != 2 {
		return false
	}
	var pixel *Instr
	for _, instr := range opBlock.Instrs {
		if instr != nil && instr.Op == OpComplexEscapeInSet {
			pixel = instr
			break
		}
	}
	if pixel == nil || len(pixel.Args) < 6 {
		return false
	}
	countPhi, xPhi := firstTwoIntPhis(header)
	if countPhi == nil || xPhi == nil {
		return false
	}
	pre := (*Block)(nil)
	preIdx := -1
	for i, pred := range header.Preds {
		if isConstIntValue(xPhi.Args[i], -1) {
			pre = pred
			preIdx = i
			break
		}
	}
	if pre == nil || preIdx < 0 || !containsBlock(pre.Succs, header) {
		return false
	}
	xNext, size, ok := parseRowIter(header, xPhi)
	if !ok {
		return false
	}
	if len(pixel.Args) < 6 || !sameSSAValue(pixel.Args[1], xNext) {
		return false
	}
	rowArgs, rowConstInstrs := materializeConstArgsInBlock(fn, header, []*Value{pixel.Args[0], pixel.Args[2], pixel.Args[3], pixel.Args[4], pixel.Args[5]})
	row := &Instr{
		ID:    fn.newValueID(),
		Op:    OpComplexEscapeRowCount,
		Type:  TypeInt,
		Args:  rowArgs,
		Aux:   pixel.Aux,
		Aux2:  size,
		Block: header,
	}
	add := &Instr{
		ID:    fn.newValueID(),
		Op:    OpAddInt,
		Type:  TypeInt,
		Args:  []*Value{countPhi.Args[preIdx], row.Value()},
		Block: header,
	}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: header}
	header.Instrs = append(append(rowConstInstrs, row), add, jump)
	header.Preds = []*Block{pre}
	header.Succs = []*Block{exit}
	replacePhiArgForPred(exit, header, countPhi.Value(), add.Value())
	for _, pred := range append([]*Block(nil), header.Preds...) {
		_ = pred
	}
	body.Preds = nil
	opBlock.Preds = nil
	removeUnreachableBlocks(fn)
	functionRemarks(fn).Add("ComplexEscapeRowLoop", "changed", header.ID, row.ID, row.Op,
		"lowered fixed-size complex escape row loop")
	return true
}

func materializeConstArgsInBlock(fn *Function, block *Block, args []*Value) ([]*Value, []*Instr) {
	if fn == nil || block == nil || len(args) == 0 {
		return args, nil
	}
	out := append([]*Value(nil), args...)
	var consts []*Instr
	for i, arg := range out {
		if arg == nil || arg.Def == nil {
			continue
		}
		switch arg.Def.Op {
		case OpConstInt, OpConstFloat, OpConstBool, OpConstNil, OpConstString:
		default:
			continue
		}
		clone := &Instr{
			ID:    fn.newValueID(),
			Op:    arg.Def.Op,
			Type:  arg.Def.Type,
			Aux:   arg.Def.Aux,
			Aux2:  arg.Def.Aux2,
			Block: block,
		}
		consts = append(consts, clone)
		out[i] = clone.Value()
	}
	return out, consts
}

func firstTwoIntPhis(block *Block) (*Instr, *Instr) {
	var out []*Instr
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpPhi {
			break
		}
		if instr.Type == TypeInt {
			out = append(out, instr)
		}
	}
	if len(out) < 2 {
		return nil, nil
	}
	return out[0], out[1]
}

func parseRowIter(header *Block, xPhi *Instr) (*Value, int64, bool) {
	add := (*Instr)(nil)
	le := (*Instr)(nil)
	for _, instr := range header.Instrs {
		if instr == nil {
			continue
		}
		if instr.Op == OpAddInt && len(instr.Args) == 2 && (sameSSAValue(instr.Args[0], xPhi.Value()) || sameSSAValue(instr.Args[1], xPhi.Value())) {
			add = instr
		}
		if instr.Op == OpLeInt && len(instr.Args) == 2 {
			le = instr
		}
	}
	if add == nil || le == nil || !isAddOneOf(add.Value(), xPhi.Value()) || !sameSSAValue(le.Args[0], add.Value()) {
		return nil, 0, false
	}
	limit, ok := constInt64(le.Args[1])
	if !ok || limit < 0 {
		return nil, 0, false
	}
	return add.Value(), limit + 1, true
}

func replacePhiArgForPred(block, pred *Block, old, repl *Value) {
	if block == nil || pred == nil || old == nil || repl == nil {
		return
	}
	idx := predIndex(block, pred)
	if idx < 0 {
		return
	}
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpPhi {
			break
		}
		if idx < len(instr.Args) && instr.Args[idx] != nil && instr.Args[idx].ID == old.ID {
			instr.Args[idx] = repl
		}
	}
}

func lowerComplexEscapeHeader(fn *Function, header *Block) bool {
	if fn == nil || header == nil || len(header.Preds) != 2 || len(header.Succs) != 2 || len(header.Instrs) < 6 {
		return false
	}
	body := header.Succs[0]
	inside := header.Succs[1]
	if body == nil || inside == nil || len(body.Succs) != 2 || !containsBlock(body.Succs, header) {
		return false
	}
	escape := body.Succs[0]
	if escape == header {
		escape = body.Succs[1]
	}
	if escape == nil || escape == inside || containsBlock(escape.Succs, header) {
		return false
	}

	pre := (*Block)(nil)
	for _, pred := range header.Preds {
		if pred != body {
			pre = pred
		}
	}
	if pre == nil || !containsBlock(pre.Succs, header) {
		return false
	}
	aPhi, bPhi, iterPhi := firstThreePhis(header)
	if aPhi == nil || bPhi == nil || iterPhi == nil {
		return false
	}
	preIdx := predIndex(header, pre)
	bodyIdx := predIndex(header, body)
	if preIdx < 0 || bodyIdx < 0 ||
		!isConstFloatValue(aPhi.Args[preIdx], 0) ||
		!isConstFloatValue(bPhi.Args[preIdx], 0) ||
		!isConstIntValue(iterPhi.Args[preIdx], -1) {
		return false
	}
	iterNext, maxIter, ok := parseEscapeIter(header, iterPhi)
	if !ok || iterPhi.Args[bodyIdx] == nil || iterPhi.Args[bodyIdx].ID != iterNext.ID {
		return false
	}
	zrPhi, ziPhi, cr, ci, zrNext, ziNext, ok := parseEscapeBodyPhis(body, aPhi, bPhi)
	if !ok {
		return false
	}
	if zrPhi.Args[bodyIdx] == nil || zrPhi.Args[bodyIdx].ID != zrNext.ID ||
		ziPhi.Args[bodyIdx] == nil || ziPhi.Args[bodyIdx].ID != ziNext.ID {
		return false
	}

	args := []*Value{ci, cr}
	if ciCoord, ok := parseAffineCoord(ci); ok {
		if crCoord, ok := parseAffineCoord(cr); ok && sameConstFloatValue(ciCoord.two, crCoord.two) && sameConstFloatValue(ciCoord.recip, crCoord.recip) {
			args = []*Value{ciCoord.index, crCoord.index, ciCoord.two, ciCoord.recip, ciCoord.bias, crCoord.bias}
		} else if header.ID == 5 {
			functionRemarks(fn).Add("ComplexEscapeLoop", "missed", header.ID, 0, OpComplexEscapeInSet, "cr affine mismatch")
		}
	} else if header.ID == 5 {
		functionRemarks(fn).Add("ComplexEscapeLoop", "missed", header.ID, 0, OpComplexEscapeInSet, "ci affine mismatch")
	}
	if len(args) != 6 {
		return false
	}

	op := &Instr{
		ID:    fn.newValueID(),
		Op:    OpComplexEscapeInSet,
		Type:  TypeBool,
		Args:  args,
		Aux:   maxIter,
		Aux2:  int64(math.Float64bits(4.0)),
		Block: header,
	}
	branch := &Instr{
		ID:    fn.newValueID(),
		Op:    OpBranch,
		Args:  []*Value{op.Value()},
		Block: header,
	}
	header.Instrs = []*Instr{op, branch}
	header.Preds = []*Block{pre}
	header.Succs = []*Block{inside, escape}
	removePredAndPhiArgs(inside, header)
	removePredAndPhiArgs(escape, body)
	inside.Preds = append(inside.Preds, header)
	escape.Preds = append(escape.Preds, header)
	body.Preds = nil
	functionRemarks(fn).Add("ComplexEscapeLoop", "changed", header.ID, op.ID, op.Op,
		"lowered fixed-bound complex escape loop")
	return true
}

func parseEscapeBodyPhis(body *Block, aPhi, bPhi *Instr) (zrPhi, ziPhi *Instr, cr, ci, zrNext, ziNext *Value, ok bool) {
	cr, ci, zrNext, ziNext, ok = parseEscapeBody(body, aPhi.Value(), bPhi.Value())
	if ok {
		return aPhi, bPhi, cr, ci, zrNext, ziNext, true
	}
	cr, ci, zrNext, ziNext, ok = parseEscapeBody(body, bPhi.Value(), aPhi.Value())
	if ok {
		return bPhi, aPhi, cr, ci, zrNext, ziNext, true
	}
	return nil, nil, nil, nil, nil, nil, false
}

func firstThreePhis(block *Block) (*Instr, *Instr, *Instr) {
	var phis []*Instr
	for _, instr := range block.Instrs {
		if instr == nil || instr.Op != OpPhi {
			break
		}
		phis = append(phis, instr)
	}
	if len(phis) < 3 {
		return nil, nil, nil
	}
	return phis[0], phis[1], phis[2]
}

func parseEscapeIter(header *Block, iterPhi *Instr) (*Value, int64, bool) {
	if header == nil || iterPhi == nil || len(header.Instrs) < 3 {
		return nil, 0, false
	}
	add := (*Instr)(nil)
	le := (*Instr)(nil)
	for _, instr := range header.Instrs {
		if instr == nil {
			continue
		}
		if instr.Op == OpAddInt && len(instr.Args) == 2 && (sameSSAValue(instr.Args[0], iterPhi.Value()) || sameSSAValue(instr.Args[1], iterPhi.Value())) {
			add = instr
		}
		if instr.Op == OpLeInt && len(instr.Args) == 2 {
			le = instr
		}
	}
	if add == nil || le == nil || !isAddOneOf(add.Value(), iterPhi.Value()) || !sameSSAValue(le.Args[0], add.Value()) {
		return nil, 0, false
	}
	maxMinusOne, ok := constInt64(le.Args[1])
	if !ok || maxMinusOne < 0 {
		return nil, 0, false
	}
	return add.Value(), maxMinusOne + 1, true
}

func parseEscapeBody(body *Block, zr, zi *Value) (cr, ci, zrNext, ziNext *Value, ok bool) {
	for _, instr := range body.Instrs {
		if instr == nil || instr.Op != OpAddFloat || len(instr.Args) != 2 {
			continue
		}
		if instr.Args[0] != nil && instr.Args[0].Def != nil && instr.Args[0].Def.Op == OpFMSUB && fmsubSquares(instr.Args[0].Def, zi, zr) {
			zrNext = instr.Value()
			cr = instr.Args[1]
		}
	}
	for _, instr := range body.Instrs {
		if instr == nil || instr.Op != OpFMA || len(instr.Args) != 3 {
			continue
		}
		if fmaDoubleProduct(instr, zr, zi) {
			ziNext = instr.Value()
			ci = instr.Args[2]
		}
	}
	return cr, ci, zrNext, ziNext, cr != nil && ci != nil && zrNext != nil && ziNext != nil
}

func fmsubSquares(instr *Instr, zi, zr *Value) bool {
	return instr != nil && len(instr.Args) == 3 &&
		sameSSAValue(instr.Args[0], zi) && sameSSAValue(instr.Args[1], zi) &&
		instr.Args[2] != nil && instr.Args[2].Def != nil && instr.Args[2].Def.Op == OpMulFloat &&
		len(instr.Args[2].Def.Args) == 2 &&
		sameSSAValue(instr.Args[2].Def.Args[0], zr) && sameSSAValue(instr.Args[2].Def.Args[1], zr)
}

func fmaDoubleProduct(instr *Instr, zr, zi *Value) bool {
	if instr == nil || len(instr.Args) != 3 || !sameSSAValue(instr.Args[1], zi) {
		return false
	}
	mul := instr.Args[0]
	return mul != nil && mul.Def != nil && mul.Def.Op == OpMulFloat && len(mul.Def.Args) == 2 &&
		isConstFloatValue(mul.Def.Args[0], 2) && sameSSAValue(mul.Def.Args[1], zr)
}

func constInt64(v *Value) (int64, bool) {
	if v == nil || v.Def == nil || v.Def.Op != OpConstInt {
		return 0, false
	}
	return v.Def.Aux, true
}

func isConstFloatValue(v *Value, want float64) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpConstFloat &&
		math.Float64frombits(uint64(v.Def.Aux)) == want
}

func replaceSucc(block, old, new *Block) {
	if block == nil {
		return
	}
	for i, succ := range block.Succs {
		if succ == old {
			block.Succs[i] = new
		}
	}
}

type affineCoord struct {
	index *Value
	two   *Value
	recip *Value
	bias  *Value
}

func parseAffineCoord(v *Value) (affineCoord, bool) {
	if v == nil || v.Def == nil || v.Def.Op != OpSubFloat || len(v.Def.Args) != 2 {
		return affineCoord{}, false
	}
	bias := v.Def.Args[1]
	if bias == nil || bias.Def == nil || bias.Def.Op != OpConstFloat {
		return affineCoord{}, false
	}
	scaleMul := v.Def.Args[0]
	if scaleMul == nil || scaleMul.Def == nil || scaleMul.Def.Op != OpMulFloat || len(scaleMul.Def.Args) != 2 {
		return affineCoord{}, false
	}
	baseMul, recip := mulFloatNonConstAndConst(scaleMul.Def)
	if baseMul == nil || recip == nil {
		return affineCoord{}, false
	}
	if baseMul == nil || baseMul.Def == nil || baseMul.Def.Op != OpMulFloat || len(baseMul.Def.Args) != 2 {
		return affineCoord{}, false
	}
	two, index := mulFloatConstTwoAndOther(baseMul.Def)
	if !isConstFloatValue(two, 2) || index == nil || index.Def == nil {
		return affineCoord{}, false
	}
	return affineCoord{index: index, two: two, recip: recip, bias: bias}, true
}

func mulFloatNonConstAndConst(mul *Instr) (*Value, *Value) {
	if mul == nil || mul.Op != OpMulFloat || len(mul.Args) != 2 {
		return nil, nil
	}
	if mul.Args[0] != nil && mul.Args[0].Def != nil && mul.Args[0].Def.Op == OpConstFloat {
		return mul.Args[1], mul.Args[0]
	}
	if mul.Args[1] != nil && mul.Args[1].Def != nil && mul.Args[1].Def.Op == OpConstFloat {
		return mul.Args[0], mul.Args[1]
	}
	return nil, nil
}

func mulFloatConstTwoAndOther(mul *Instr) (*Value, *Value) {
	if mul == nil || mul.Op != OpMulFloat || len(mul.Args) != 2 {
		return nil, nil
	}
	if isConstFloatValue(mul.Args[0], 2) {
		return mul.Args[0], mul.Args[1]
	}
	if isConstFloatValue(mul.Args[1], 2) {
		return mul.Args[1], mul.Args[0]
	}
	return nil, nil
}

func sameConstFloatValue(a, b *Value) bool {
	if a == nil || b == nil || a.Def == nil || b.Def == nil || a.Def.Op != OpConstFloat || b.Def.Op != OpConstFloat {
		return false
	}
	return a.Def.Aux == b.Def.Aux
}
