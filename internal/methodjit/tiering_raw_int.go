//go:build darwin && arm64

package methodjit

func forceRawIntKernelIR(fn *Function) {
	if fn == nil || fn.Proto == nil {
		return
	}
	for {
		changed := false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				switch instr.Op {
				case OpLoadSlot:
					if int(instr.Aux) < fn.Proto.NumParams && instr.Type != TypeInt {
						instr.Type = TypeInt
						changed = true
					}
				case OpConstInt:
					if instr.Type != TypeInt {
						instr.Type = TypeInt
						changed = true
					}
				case OpPhi:
					if instr.Type != TypeInt {
						instr.Type = TypeInt
						changed = true
					}
				case OpAdd, OpSub, OpMul, OpMod:
					if allInstrArgsType(instr, TypeInt) {
						switch instr.Op {
						case OpAdd:
							instr.Op = OpAddInt
						case OpSub:
							instr.Op = OpSubInt
						case OpMul:
							instr.Op = OpMulInt
						case OpMod:
							instr.Op = OpModInt
						}
						instr.Type = TypeInt
						changed = true
					}
				case OpEq, OpLt, OpLe:
					if allInstrArgsType(instr, TypeInt) {
						switch instr.Op {
						case OpEq:
							instr.Op = OpEqInt
						case OpLt:
							instr.Op = OpLtInt
						case OpLe:
							instr.Op = OpLeInt
						}
						if instr.Type != TypeBool {
							instr.Type = TypeBool
						}
						changed = true
					}
				}
			}
		}
		if !changed {
			return
		}
	}
}

func firstResidualRawIntKernelGenericNumeric(fn *Function) (Op, bool) {
	gate := firstResidualRawIntKernelGenericNumericGate(fn)
	return gate.Op, !gate.Allowed
}

func firstResidualRawIntKernelGenericNumericGate(fn *Function) GateResult {
	if fn == nil {
		return allowGate("RawIntKernelIR", "no function")
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm:
				return blockGateOp("RawIntKernelIR", "raw-int kernel has residual generic numeric op", instr.Op)
			}
		}
	}
	return allowGate("RawIntKernelIR", "no residual generic numeric op")
}

func allInstrArgsType(instr *Instr, typ Type) bool {
	if instr == nil || len(instr.Args) == 0 {
		return false
	}
	for _, arg := range instr.Args {
		if arg == nil || arg.Def == nil || arg.Def.Type != typ {
			return false
		}
	}
	return true
}
