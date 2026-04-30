package vm

import "github.com/gscript/gscript/internal/runtime"

const primePredicateSumLoopMaxLimit int64 = 10_000_000

type primePredicateSumLoopShape struct {
	loopPC     int
	fnConst    int
	sumConst   int
	countConst int
}

func (vm *VM) tryPrimePredicateSumForLoopKernel(frame *CallFrame, base int, code []uint32, constants []runtime.Value, a int, sbx int) (bool, error) {
	if frame == nil || !vm.noGlobalLock || vm.globalOverrides != nil {
		return false, nil
	}
	forprepPC := frame.pc - 1
	shape, ok := matchPrimePredicateSumForLoopShape(code, constants, forprepPC, a, sbx)
	if !ok {
		return false, nil
	}

	initV := vm.regs[base+a]
	limitV := vm.regs[base+a+1]
	stepV := vm.regs[base+a+2]
	if !initV.IsInt() || !limitV.IsInt() || !stepV.IsInt() || stepV.Int() != 1 {
		return false, nil
	}
	start := initV.Int()
	limit := limitV.Int()
	if start > limit || start < 0 || limit > primePredicateSumLoopMaxLimit {
		return false, nil
	}

	fnVal, _, ok := vm.globalByStringConst(constants, shape.fnConst)
	if !ok {
		return false, nil
	}
	cl, ok := closureFromValue(fnVal)
	if !ok || !IsTrialDivisionPrimePredicateProto(cl.Proto) {
		return false, nil
	}

	sum, sumIdx, ok := vm.globalByStringConst(constants, shape.sumConst)
	if !ok || !sum.IsNumber() {
		return false, nil
	}
	count, countIdx, ok := vm.globalByStringConst(constants, shape.countConst)
	if !ok || !count.IsNumber() {
		return false, nil
	}

	one := runtime.IntValue(1)
	for n := start; n <= limit; n++ {
		if !trialDivisionPrimeInt(n) {
			continue
		}
		nv := runtime.IntValue(n)
		if !runtime.AddNums(&sum, &sum, &nv) || !runtime.AddNums(&count, &count, &one) {
			return false, nil
		}
	}

	vm.setGlobalByStringConst(constants, shape.sumConst, sumIdx, sum)
	vm.setGlobalByStringConst(constants, shape.countConst, countIdx, count)
	vm.regs[base+a] = limitV
	vm.regs[base+a+3] = limitV
	frame.pc = shape.loopPC + 1
	return true, nil
}

func (vm *VM) globalByStringConst(constants []runtime.Value, constIdx int) (runtime.Value, int, bool) {
	if constIdx < 0 || constIdx >= len(constants) || !constants[constIdx].IsString() {
		return runtime.NilValue(), 0, false
	}
	name := constants[constIdx].Str()
	idx, ok := vm.globalIndex[name]
	if !ok || idx < 0 || idx >= len(vm.globalArray) {
		return runtime.NilValue(), 0, false
	}
	return vm.globalArray[idx], idx, true
}

func (vm *VM) setGlobalByStringConst(constants []runtime.Value, constIdx int, globalIdx int, val runtime.Value) {
	vm.globalArray[globalIdx] = val
	vm.globals[constants[constIdx].Str()] = val
}

// HasPrimePredicateSumLoopKernel reports whether a proto contains a structural
// driver loop that can be batched by tryPrimePredicateSumForLoopKernel.
func HasPrimePredicateSumLoopKernel(proto *FuncProto, globals map[string]*FuncProto) bool {
	if proto == nil {
		return false
	}
	for pc, inst := range proto.Code {
		if DecodeOp(inst) != OP_FORPREP {
			continue
		}
		if IsPrimePredicateSumLoopAt(proto, pc, globals) {
			return true
		}
	}
	return false
}

// IsPrimePredicateSumLoopAt checks one FORPREP site for the guarded
// predicate-call plus sum/count reduction shape.
func IsPrimePredicateSumLoopAt(proto *FuncProto, forprepPC int, globals map[string]*FuncProto) bool {
	if proto == nil || len(globals) == 0 || forprepPC < 0 || forprepPC >= len(proto.Code) {
		return false
	}
	inst := proto.Code[forprepPC]
	if DecodeOp(inst) != OP_FORPREP {
		return false
	}
	shape, ok := matchPrimePredicateSumForLoopShape(proto.Code, proto.Constants, forprepPC, DecodeA(inst), DecodesBx(inst))
	if !ok {
		return false
	}
	if shape.fnConst < 0 || shape.fnConst >= len(proto.Constants) || !proto.Constants[shape.fnConst].IsString() {
		return false
	}
	return IsTrialDivisionPrimePredicateProto(globals[proto.Constants[shape.fnConst].Str()])
}

func matchPrimePredicateSumForLoopShape(code []uint32, constants []runtime.Value, forprepPC int, a int, sbx int) (primePredicateSumLoopShape, bool) {
	var shape primePredicateSumLoopShape
	bodyPC := forprepPC + 1
	loopPC := bodyPC + sbx
	if forprepPC < 0 || bodyPC < 0 || loopPC < 0 || loopPC >= len(code) || loopPC-bodyPC != 12 {
		return shape, false
	}
	loop := code[loopPC]
	if DecodeOp(loop) != OP_FORLOOP || DecodeA(loop) != a || loopPC+1+DecodesBx(loop) != bodyPC {
		return shape, false
	}

	getFn := code[bodyPC]
	moveArg := code[bodyPC+1]
	call := code[bodyPC+2]
	test := code[bodyPC+3]
	skip := code[bodyPC+4]
	if DecodeOp(getFn) != OP_GETGLOBAL || DecodeOp(moveArg) != OP_MOVE || DecodeOp(call) != OP_CALL ||
		DecodeOp(test) != OP_TEST || DecodeOp(skip) != OP_JMP {
		return shape, false
	}
	fnSlot := DecodeA(getFn)
	fnConst := DecodeBx(getFn)
	loopVar := a + 3
	if DecodeA(moveArg) != fnSlot+1 || DecodeB(moveArg) != loopVar ||
		DecodeA(call) != fnSlot || DecodeB(call) != 2 || DecodeC(call) != 2 ||
		DecodeA(test) != fnSlot || DecodeC(test) != 0 ||
		bodyPC+5+DecodesBx(skip) != loopPC {
		return shape, false
	}

	sumGet := code[bodyPC+5]
	sumAdd := code[bodyPC+6]
	sumSet := code[bodyPC+7]
	countGet := code[bodyPC+8]
	one := code[bodyPC+9]
	countAdd := code[bodyPC+10]
	countSet := code[bodyPC+11]
	if DecodeOp(sumGet) != OP_GETGLOBAL || DecodeOp(sumAdd) != OP_ADD || DecodeOp(sumSet) != OP_SETGLOBAL ||
		DecodeOp(countGet) != OP_GETGLOBAL || DecodeOp(one) != OP_LOADINT ||
		DecodeOp(countAdd) != OP_ADD || DecodeOp(countSet) != OP_SETGLOBAL {
		return shape, false
	}

	sumReadSlot := DecodeA(sumGet)
	sumWriteSlot := DecodeA(sumAdd)
	sumConst := DecodeBx(sumGet)
	if DecodeA(sumSet) != sumWriteSlot || DecodeBx(sumSet) != sumConst ||
		!addOperandsMatch(sumAdd, sumReadSlot, loopVar) {
		return shape, false
	}

	countReadSlot := DecodeA(countGet)
	oneSlot := DecodeA(one)
	countWriteSlot := DecodeA(countAdd)
	countConst := DecodeBx(countGet)
	if DecodesBx(one) != 1 ||
		DecodeA(countSet) != countWriteSlot || DecodeBx(countSet) != countConst ||
		!addOperandsMatch(countAdd, countReadSlot, oneSlot) {
		return shape, false
	}
	if !stringConst(constants, fnConst) || !stringConst(constants, sumConst) || !stringConst(constants, countConst) ||
		fnConst == sumConst || fnConst == countConst || sumConst == countConst {
		return shape, false
	}

	return primePredicateSumLoopShape{
		loopPC:     loopPC,
		fnConst:    fnConst,
		sumConst:   sumConst,
		countConst: countConst,
	}, true
}

func addOperandsMatch(inst uint32, left int, right int) bool {
	b := DecodeB(inst)
	c := DecodeC(inst)
	return (b == left && c == right) || (b == right && c == left)
}

func stringConst(constants []runtime.Value, idx int) bool {
	return idx >= 0 && idx < len(constants) && constants[idx].IsString()
}

// IsTrialDivisionPrimePredicateProto recognizes the bytecode shape for the
// single-argument trial-division predicate that returns only booleans.
func IsTrialDivisionPrimePredicateProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || len(p.Constants) != 0 || len(p.Protos) != 0 {
		return false
	}
	return codeEquals(p.Code, []uint32{
		EncodeAsBx(OP_LOADINT, 1, 2),
		EncodeABC(OP_LT, 0, 0, 1),
		EncodeAsBx(OP_JMP, 0, 2),
		EncodeABC(OP_LOADBOOL, 1, 0, 0),
		EncodeABC(OP_RETURN, 1, 2, 0),
		EncodeAsBx(OP_LOADINT, 1, 4),
		EncodeABC(OP_LT, 0, 0, 1),
		EncodeAsBx(OP_JMP, 0, 2),
		EncodeABC(OP_LOADBOOL, 1, 1, 0),
		EncodeABC(OP_RETURN, 1, 2, 0),
		EncodeAsBx(OP_LOADINT, 2, 2),
		EncodeABC(OP_MOD, 1, 0, 2),
		EncodeAsBx(OP_LOADINT, 2, 0),
		EncodeABC(OP_EQ, 0, 1, 2),
		EncodeAsBx(OP_JMP, 0, 2),
		EncodeABC(OP_LOADBOOL, 1, 0, 0),
		EncodeABC(OP_RETURN, 1, 2, 0),
		EncodeAsBx(OP_LOADINT, 2, 3),
		EncodeABC(OP_MOD, 1, 0, 2),
		EncodeAsBx(OP_LOADINT, 2, 0),
		EncodeABC(OP_EQ, 0, 1, 2),
		EncodeAsBx(OP_JMP, 0, 2),
		EncodeABC(OP_LOADBOOL, 1, 0, 0),
		EncodeABC(OP_RETURN, 1, 2, 0),
		EncodeAsBx(OP_LOADINT, 1, 5),
		EncodeABC(OP_MUL, 2, 1, 1),
		EncodeABC(OP_LE, 0, 2, 0),
		EncodeAsBx(OP_JMP, 0, 18),
		EncodeABC(OP_MOD, 2, 0, 1),
		EncodeAsBx(OP_LOADINT, 3, 0),
		EncodeABC(OP_EQ, 0, 2, 3),
		EncodeAsBx(OP_JMP, 0, 2),
		EncodeABC(OP_LOADBOOL, 2, 0, 0),
		EncodeABC(OP_RETURN, 2, 2, 0),
		EncodeAsBx(OP_LOADINT, 4, 2),
		EncodeABC(OP_ADD, 3, 1, 4),
		EncodeABC(OP_MOD, 2, 0, 3),
		EncodeAsBx(OP_LOADINT, 3, 0),
		EncodeABC(OP_EQ, 0, 2, 3),
		EncodeAsBx(OP_JMP, 0, 2),
		EncodeABC(OP_LOADBOOL, 2, 0, 0),
		EncodeABC(OP_RETURN, 2, 2, 0),
		EncodeAsBx(OP_LOADINT, 3, 6),
		EncodeABC(OP_ADD, 2, 1, 3),
		EncodeABC(OP_MOVE, 1, 2, 0),
		EncodeAsBx(OP_JMP, 0, -21),
		EncodeABC(OP_LOADBOOL, 2, 1, 0),
		EncodeABC(OP_RETURN, 2, 2, 0),
	})
}

func trialDivisionPrimeInt(n int64) bool {
	if n < 2 {
		return false
	}
	if n < 4 {
		return true
	}
	if n%2 == 0 || n%3 == 0 {
		return false
	}
	for i := int64(5); i*i <= n; i += 6 {
		if n%i == 0 || n%(i+2) == 0 {
			return false
		}
	}
	return true
}
