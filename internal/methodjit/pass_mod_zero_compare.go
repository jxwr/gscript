package methodjit

// ModZeroComparePass rewrites integer modulo-zero comparisons into a boolean
// divisibility test. For x % c == 0, Lua's sign-adjusted modulo and ARM64's
// truncating remainder agree on the zero case, so codegen can skip the full
// modulo sequence and use a bit test for power-of-two constants.
func ModZeroComparePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if (instr.Op != OpEq && instr.Op != OpEqInt) || len(instr.Args) < 2 {
				continue
			}
			mod, divisor, ok := parseModZeroCompare(instr.Args[0], instr.Args[1])
			if !ok {
				mod, divisor, ok = parseModZeroCompare(instr.Args[1], instr.Args[0])
			}
			if !ok || mod == nil || len(mod.Args) < 1 {
				continue
			}
			before := instr.Op
			instr.Op = OpModZeroInt
			instr.Type = TypeBool
			instr.Args = []*Value{mod.Args[0]}
			instr.Aux = divisor
			instr.Aux2 = 0
			functionRemarks(fn).Add("ModZeroCompare", "changed", block.ID, instr.ID, instr.Op,
				"rewrote "+before.String()+" of ModInt-by-constant zero to divisibility test")
		}
	}
	return fn, nil
}

func parseModZeroCompare(modVal, zeroVal *Value) (*Instr, int64, bool) {
	zero, ok := constIntFromValue(zeroVal)
	if !ok || zero != 0 || modVal == nil || modVal.Def == nil {
		return nil, 0, false
	}
	mod := modVal.Def
	if (mod.Op != OpMod && mod.Op != OpModInt) || len(mod.Args) < 2 {
		return nil, 0, false
	}
	divisor, ok := constIntFromValue(mod.Args[1])
	if !ok || divisor == 0 {
		return nil, 0, false
	}
	return mod, divisor, true
}
