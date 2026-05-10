package methodjit

// ModRangeSimplifyPass removes integer modulo operations that range analysis
// proves are identity operations. It is deliberately conservative: it only
// rewrites positive constant divisors and non-negative dividends whose maximum
// is strictly below the divisor, preserving Lua modulo semantics for negatives.
func ModRangeSimplifyPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 || len(fn.IntRanges) == 0 {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpModInt || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
				continue
			}
			divisor, ok := constIntFromValue(instr.Args[1])
			if !ok || divisor <= 0 {
				continue
			}
			if divisor == 1 {
				instr.Op = OpConstInt
				instr.Type = TypeInt
				instr.Args = nil
				instr.Aux = 0
				instr.Aux2 = 0
				functionRemarks(fn).Add("ModRangeSimplify", "changed", block.ID, instr.ID, instr.Op,
					"folded x % 1 to zero")
				continue
			}
			lhs := instr.Args[0]
			r, ok := fn.IntRanges[lhs.ID]
			if !ok || !r.known || r.min < 0 || r.max >= divisor {
				continue
			}
			replaceValueUses(fn, instr.ID, lhs, lhs.ID)
			instr.Op = OpNop
			instr.Type = TypeUnknown
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			functionRemarks(fn).Add("ModRangeSimplify", "changed", block.ID, instr.ID, instr.Op,
				"replaced range-proven x % const with x")
		}
	}
	return fn, nil
}
