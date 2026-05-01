package methodjit

// FieldNumToFloatFusionPass fuses a single-use GetField with its following
// NumToFloat conversion without moving the field load past intervening code.
// The original GetField instruction becomes the fused load/conversion and the
// later NumToFloat is replaced by Nop after its users are redirected.
func FieldNumToFloatFusionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}

	uses := computeUseCounts(fn)
	for _, block := range fn.Blocks {
		pos := make(map[int]int, len(block.Instrs))
		for i, instr := range block.Instrs {
			pos[instr.ID] = i
		}

		for i, instr := range block.Instrs {
			if instr.Op != OpNumToFloat || len(instr.Args) != 1 {
				continue
			}
			get := instr.Args[0].Def
			if get == nil || get.Op != OpGetField || get.Block != block || len(get.Args) != 1 {
				continue
			}
			getPos, ok := pos[get.ID]
			if !ok || getPos >= i || uses[get.ID] != 1 {
				continue
			}
			if !fieldNumFusionGapIsSafe(block.Instrs[getPos+1 : i]) {
				functionRemarks(fn).Add("FieldNumFusion", "missed", block.ID, instr.ID, instr.Op,
					"intervening instruction may deopt, exit, call, or mutate state")
				continue
			}

			get.Op = OpGetFieldNumToFloat
			get.Type = TypeFloat
			replaceValueUses(fn, instr.ID, get.Value(), get.ID)
			instr.Op = OpNop
			instr.Type = TypeUnknown
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			functionRemarks(fn).Add("FieldNumFusion", "changed", block.ID, get.ID, get.Op,
				"fused GetField with NumToFloat at original field-load position")
		}
	}
	return fn, nil
}

func fieldNumFusionGapIsSafe(instrs []*Instr) bool {
	for _, instr := range instrs {
		if instr == nil {
			return false
		}
		switch instr.Op {
		case OpNop,
			OpConstInt, OpConstFloat, OpConstBool, OpConstNil, OpConstString,
			OpLoadSlot,
			OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
			OpSqrt, OpFMA, OpFMSUB,
			OpLtFloat, OpLeFloat:
			continue
		default:
			return false
		}
	}
	return true
}
