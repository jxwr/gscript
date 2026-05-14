package methodjit

import "math"

// FloatStrengthReductionPass rewrites exact floating division by a
// power-of-two constant into multiplication by the reciprocal.
//
// Division and multiplication by powers of two are both exponent adjustments
// in binary floating point, so this preserves IEEE-754 results while avoiding
// a high-latency FDIV in hot numeric loops.
func FloatStrengthReductionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	uses := countFloatStrengthValueUses(fn)
	for _, block := range fn.Blocks {
		for i := 0; i < len(block.Instrs); i++ {
			instr := block.Instrs[i]
			if instr == nil {
				continue
			}
			if instr.Op == OpMulFloat && len(instr.Args) >= 2 {
				if rewriteAffineFloatScale(fn, block, i, instr, uses) {
					continue
				}
			}
			if instr.Op != OpDivFloat || len(instr.Args) < 2 {
				continue
			}
			reciprocal, ok := pow2Reciprocal(instr.Args[1])
			if !ok {
				continue
			}

			c := &Instr{
				ID:    fn.newValueID(),
				Op:    OpConstFloat,
				Type:  TypeFloat,
				Aux:   int64(math.Float64bits(reciprocal)),
				Block: block,
			}
			block.Instrs = append(block.Instrs, nil)
			copy(block.Instrs[i+1:], block.Instrs[i:])
			block.Instrs[i] = c
			i++

			before := instr.Op
			instr.Op = OpMulFloat
			instr.Type = TypeFloat
			instr.Args = []*Value{instr.Args[0], c.Value()}
			instr.Aux = 0
			instr.Aux2 = 0
			functionRemarks(fn).Add("FloatStrengthReduction", "changed", block.ID, instr.ID, instr.Op,
				"rewrote "+before.String()+" by power-of-two constant to MulFloat")
		}
	}
	return fn, nil
}

func rewriteAffineFloatScale(fn *Function, block *Block, idx int, instr *Instr, uses map[int]int) bool {
	if fn == nil || block == nil || instr == nil || instr.Op != OpMulFloat || len(instr.Args) < 2 {
		return false
	}
	addArg, scaleArg := instr.Args[0], instr.Args[1]
	if addArg == nil || addArg.Def == nil || addArg.Def.Op != OpAddFloat {
		addArg, scaleArg = instr.Args[1], instr.Args[0]
	}
	if addArg == nil || addArg.Def == nil || addArg.Def.Op != OpAddFloat || len(addArg.Def.Args) < 2 {
		return false
	}
	if uses[addArg.ID] != 1 {
		return false
	}
	scale, ok := constFloat64Value(scaleArg)
	if !ok || scale == 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return false
	}
	base, offset, ok := affineAddBaseAndConst(addArg.Def)
	if !ok {
		return false
	}
	bias := offset * scale
	if math.IsNaN(bias) || math.IsInf(bias, 0) {
		return false
	}
	biasConst := &Instr{
		ID:    fn.newValueID(),
		Op:    OpConstFloat,
		Type:  TypeFloat,
		Aux:   int64(math.Float64bits(bias)),
		Block: block,
	}
	block.Instrs = append(block.Instrs, nil)
	copy(block.Instrs[idx+1:], block.Instrs[idx:])
	block.Instrs[idx] = biasConst
	instr.Op = OpFMA
	instr.Type = TypeFloat
	instr.Args = []*Value{base, scaleArg, biasConst.Value()}
	instr.Aux = 0
	instr.Aux2 = 0
	addArg.Def.Op = OpNop
	addArg.Def.Args = nil
	functionRemarks(fn).Add("FloatStrengthReduction", "changed", block.ID, instr.ID, instr.Op,
		"folded affine float recurrence scale into FMA")
	return true
}

func affineAddBaseAndConst(add *Instr) (*Value, float64, bool) {
	if add == nil || len(add.Args) < 2 {
		return nil, 0, false
	}
	if c, ok := constFloat64Value(add.Args[0]); ok {
		return add.Args[1], c, add.Args[1] != nil
	}
	if c, ok := constFloat64Value(add.Args[1]); ok {
		return add.Args[0], c, add.Args[0] != nil
	}
	return nil, 0, false
}

func constFloat64Value(v *Value) (float64, bool) {
	if v == nil || v.Def == nil {
		return 0, false
	}
	switch v.Def.Op {
	case OpConstFloat:
		return math.Float64frombits(uint64(v.Def.Aux)), true
	case OpConstInt:
		return float64(v.Def.Aux), true
	default:
		return 0, false
	}
}

func countFloatStrengthValueUses(fn *Function) map[int]int {
	uses := make(map[int]int)
	if fn == nil {
		return uses
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			for _, arg := range instr.Args {
				if arg != nil {
					uses[arg.ID]++
				}
			}
		}
	}
	return uses
}

func pow2Reciprocal(v *Value) (float64, bool) {
	if v == nil || v.Def == nil {
		return 0, false
	}
	switch v.Def.Op {
	case OpConstInt:
		d := v.Def.Aux
		if d == 0 {
			return 0, false
		}
		abs := d
		if abs < 0 {
			if abs == math.MinInt64 {
				return 0, false
			}
			abs = -abs
		}
		if abs&(abs-1) != 0 {
			return 0, false
		}
		return 1.0 / float64(d), true
	case OpConstFloat:
		d := math.Float64frombits(uint64(v.Def.Aux))
		if d == 0 || math.IsInf(d, 0) || math.IsNaN(d) {
			return 0, false
		}
		mant, _ := math.Frexp(math.Abs(d))
		if mant != 0.5 {
			return 0, false
		}
		return 1.0 / d, true
	default:
		return 0, false
	}
}
