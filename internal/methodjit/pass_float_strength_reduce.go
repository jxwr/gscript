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
	for _, block := range fn.Blocks {
		for i := 0; i < len(block.Instrs); i++ {
			instr := block.Instrs[i]
			if instr == nil || instr.Op != OpDivFloat || len(instr.Args) < 2 {
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
