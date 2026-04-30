package vm

type bytecodePattern struct {
	code []uint32
}

func newBytecodePattern(code []uint32) bytecodePattern {
	return bytecodePattern{code: code}
}

func (p bytecodePattern) inst(pc int) (uint32, bool) {
	if pc < 0 || pc >= len(p.code) {
		return 0, false
	}
	return p.code[pc], true
}

func (p bytecodePattern) op(pc int, op Opcode) (uint32, bool) {
	inst, ok := p.inst(pc)
	return inst, ok && DecodeOp(inst) == op
}

func (p bytecodePattern) abc(pc int, op Opcode, a, b, c int) bool {
	inst, ok := p.op(pc, op)
	return ok && DecodeA(inst) == a && DecodeB(inst) == b && DecodeC(inst) == c
}

func (p bytecodePattern) asbx(pc int, op Opcode, a, sbx int) bool {
	inst, ok := p.op(pc, op)
	return ok && DecodeA(inst) == a && DecodesBx(inst) == sbx
}

func (p bytecodePattern) move(pc int, dst, src int) bool {
	return p.abc(pc, OP_MOVE, dst, src, 0)
}

func (p bytecodePattern) loadInt(pc int, dst int, value int) bool {
	return p.asbx(pc, OP_LOADINT, dst, value)
}

func (p bytecodePattern) loadBool(pc int, dst int, value bool) bool {
	b := 0
	if value {
		b = 1
	}
	return p.abc(pc, OP_LOADBOOL, dst, b, 0)
}

func (p bytecodePattern) returnFixed(pc int, src, count int) bool {
	return p.abc(pc, OP_RETURN, src, count, 0)
}

func (p bytecodePattern) jumpTarget(pc int) (int, bool) {
	inst, ok := p.op(pc, OP_JMP)
	if !ok {
		return 0, false
	}
	return pc + 1 + DecodesBx(inst), true
}

func (p bytecodePattern) jumpTo(pc int, target int) bool {
	got, ok := p.jumpTarget(pc)
	return ok && got == target
}

func (p bytecodePattern) numericForLoop(forprepPC, a int) (bodyPC, loopPC int, ok bool) {
	prep, ok := p.op(forprepPC, OP_FORPREP)
	if !ok || DecodeA(prep) != a {
		return 0, 0, false
	}
	bodyPC = forprepPC + 1
	loopPC = bodyPC + DecodesBx(prep)
	loop, ok := p.op(loopPC, OP_FORLOOP)
	if !ok || DecodeA(loop) != a || loopPC+1+DecodesBx(loop) != bodyPC {
		return 0, 0, false
	}
	return bodyPC, loopPC, true
}
