package methodjit

import "math"

// QuadraticStepStrengthReductionPass rewrites adjacent unrolled evaluations of
// a triangular quadratic denominator:
//
//	x*(x+1)*0.5 + linear + 1
//
// into a recurrence for the second evaluation. This shape appears after pure
// numeric helper inlining and two-way loop unrolling; the rewrite is generic to
// the expression shape, not to a function or benchmark name.
func QuadraticStepStrengthReductionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		rewriteQuadraticStepsInBlock(fn, block)
	}
	return fn, nil
}

type quadraticDenomExpr struct {
	instr    *Instr
	x        *Value
	xPlusOne *Value
	linear   *Value
	halfID   int
	oneID    int
	instrIdx int
	block    *Block
}

func rewriteQuadraticStepsInBlock(fn *Function, block *Block) {
	if block == nil {
		return
	}
	var prev *quadraticDenomExpr
	for idx := 0; idx < len(block.Instrs); idx++ {
		expr, ok := parseQuadraticDenom(block, idx)
		if !ok {
			continue
		}
		if prev != nil {
			if delta := quadraticStepDelta(prev, expr); delta != nil {
				repl := &Instr{
					ID:    fn.newValueID(),
					Op:    OpAddFloat,
					Type:  TypeFloat,
					Args:  []*Value{prev.instr.Value(), delta},
					Block: block,
				}
				repl.copySourceFrom(expr.instr)
				block.Instrs = append(block.Instrs, nil)
				copy(block.Instrs[idx+1:], block.Instrs[idx:])
				block.Instrs[idx] = repl
				replaceUsesAfter(fn, block, idx+1, expr.instr.ID, repl.Value())
				functionRemarks(fn).Add("QuadraticStepStrengthReduction", "changed", block.ID, expr.instr.ID, expr.instr.Op,
					"reused previous triangular quadratic denominator")
				idx++
			}
		}
		prev = expr
	}
}

func parseQuadraticDenom(block *Block, idx int) (*quadraticDenomExpr, bool) {
	if block == nil || idx < 0 || idx >= len(block.Instrs) {
		return nil, false
	}
	add := block.Instrs[idx]
	if add == nil || add.Op != OpAddFloat || len(add.Args) != 2 {
		return nil, false
	}

	var fma *Instr
	var one *Value
	if add.Args[0] != nil && add.Args[0].Def != nil && add.Args[0].Def.Op == OpFMA {
		fma = add.Args[0].Def
		one = add.Args[1]
	} else if add.Args[1] != nil && add.Args[1].Def != nil && add.Args[1].Def.Op == OpFMA {
		fma = add.Args[1].Def
		one = add.Args[0]
	}
	if fma == nil || len(fma.Args) != 3 || !isConstOne(one) {
		return nil, false
	}

	mul := fma.Args[0]
	half := fma.Args[1]
	linear := fma.Args[2]
	if !isConstHalf(half) || mul == nil || mul.Def == nil || mul.Def.Op != OpMulInt || len(mul.Def.Args) != 2 {
		return nil, false
	}

	x, xPlusOne, ok := parseXTimesXPlusOne(mul.Def.Args[0], mul.Def.Args[1])
	if !ok {
		return nil, false
	}

	return &quadraticDenomExpr{
		instr:    add,
		x:        x,
		xPlusOne: xPlusOne,
		linear:   linear,
		halfID:   half.ID,
		oneID:    one.ID,
		instrIdx: idx,
		block:    block,
	}, true
}

func parseXTimesXPlusOne(a, b *Value) (*Value, *Value, bool) {
	if isAddOneOf(a, b) {
		return b, a, true
	}
	if isAddOneOf(b, a) {
		return a, b, true
	}
	return nil, nil, false
}

func quadraticStepDelta(prev, cur *quadraticDenomExpr) *Value {
	if prev == nil || cur == nil || prev.block != cur.block {
		return nil
	}
	if prev.halfID != cur.halfID || prev.oneID != cur.oneID {
		return nil
	}
	if sameSSAValue(prev.linear, cur.linear) {
		if valueIsPrevPlusOne(cur.x, prev.x) {
			return cur.x
		}
	}
	if valueIsPrevPlusOne(cur.linear, prev.linear) {
		if valueIsPrevPlusOne(cur.x, prev.x) {
			return cur.xPlusOne
		}
	}
	return nil
}

func isAddOneOf(v, base *Value) bool {
	if v == nil || base == nil || v.Def == nil || v.Def.Op != OpAddInt || len(v.Def.Args) != 2 {
		return false
	}
	return (sameSSAValue(v.Def.Args[0], base) && isConstOne(v.Def.Args[1])) ||
		(sameSSAValue(v.Def.Args[1], base) && isConstOne(v.Def.Args[0]))
}

func valueIsPrevPlusOne(cur, prev *Value) bool {
	if isAddOneOf(cur, prev) {
		return true
	}
	if cur == nil || prev == nil || cur.Def == nil || prev.Def == nil {
		return false
	}
	if cur.Def.Op != OpAddInt || prev.Def.Op != OpAddInt || len(cur.Def.Args) != 2 || len(prev.Def.Args) != 2 {
		return false
	}
	for _, pair := range [][4]*Value{
		{cur.Def.Args[0], cur.Def.Args[1], prev.Def.Args[0], prev.Def.Args[1]},
		{cur.Def.Args[0], cur.Def.Args[1], prev.Def.Args[1], prev.Def.Args[0]},
		{cur.Def.Args[1], cur.Def.Args[0], prev.Def.Args[0], prev.Def.Args[1]},
		{cur.Def.Args[1], cur.Def.Args[0], prev.Def.Args[1], prev.Def.Args[0]},
	} {
		if sameSSAValue(pair[0], pair[2]) && isAddOneOf(pair[1], pair[3]) {
			return true
		}
	}
	return false
}

func sameSSAValue(a, b *Value) bool {
	return a != nil && b != nil && a.ID == b.ID
}

func isConstOne(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	switch v.Def.Op {
	case OpConstInt:
		return v.Def.Aux == 1
	case OpConstFloat:
		return math.Float64frombits(uint64(v.Def.Aux)) == 1.0
	default:
		return false
	}
}

func isConstHalf(v *Value) bool {
	if v == nil || v.Def == nil || v.Def.Op != OpConstFloat {
		return false
	}
	return math.Float64frombits(uint64(v.Def.Aux)) == 0.5
}

func replaceUsesAfter(fn *Function, definingBlock *Block, startIdx int, oldID int, repl *Value) {
	for _, block := range fn.Blocks {
		begin := 0
		if block == definingBlock {
			begin = startIdx
		}
		for i := begin; i < len(block.Instrs); i++ {
			instr := block.Instrs[i]
			if instr == nil {
				continue
			}
			for argIdx, arg := range instr.Args {
				if arg != nil && arg.ID == oldID {
					instr.Args[argIdx] = repl
				}
			}
		}
	}
}
