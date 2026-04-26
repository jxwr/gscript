package methodjit

const (
	floatOneBits = 0x3ff0000000000000
	floatTwoBits = 0x4000000000000000
)

// FloatScaleReusePass rewrites a same-block pair:
//
//	x1 = MulFloat(1.0, x)
//	x2 = MulFloat(2.0, x)
//
// into:
//
//	x2 = AddFloat(x1, x1)
//
// This reuses the already materialized float conversion for mixed int/float
// code and removes a second constant load. It is safe for finite values and
// preserves NaN/Inf behavior for the supported 2.0 scale.
func FloatScaleReusePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		unitScale := make(map[int]*Instr)
		for _, instr := range block.Instrs {
			if instr.Op != OpMulFloat || len(instr.Args) != 2 {
				continue
			}
			if arg, ok := mulConstFloatArg(instr, floatOneBits); ok {
				unitScale[arg.ID] = instr
				continue
			}
			arg, ok := mulConstFloatArg(instr, floatTwoBits)
			if !ok {
				continue
			}
			base := unitScale[arg.ID]
			if base == nil || base.Type != TypeFloat {
				continue
			}
			instr.Op = OpAddFloat
			instr.Type = TypeFloat
			instr.Args = []*Value{base.Value(), base.Value()}
			functionRemarks(fn).Add("FloatScaleReuse", "changed", block.ID, instr.ID, instr.Op,
				"rewrote 2.0*x to (1.0*x)+(1.0*x)")
		}
	}
	return fn, nil
}

func mulConstFloatArg(instr *Instr, bits uint64) (*Value, bool) {
	if instr == nil || len(instr.Args) != 2 {
		return nil, false
	}
	if isConstFloatBits(instr.Args[0], bits) {
		return instr.Args[1], true
	}
	if isConstFloatBits(instr.Args[1], bits) {
		return instr.Args[0], true
	}
	return nil, false
}

func isConstFloatBits(v *Value, bits uint64) bool {
	return v != nil && v.Def != nil && v.Def.Op == OpConstFloat && uint64(v.Def.Aux) == bits
}
