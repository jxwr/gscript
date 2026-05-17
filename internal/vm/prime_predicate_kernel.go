package vm

import "github.com/gscript/gscript/internal/runtime"

const intPredicateReductionLoopMaxLimit int64 = 10_000_000

type intPredicateReductionLoopShape struct {
	loopPC     int
	fnConst    int
	sumConst   int
	countConst int
}

type intBoolPredicateKernelCache struct {
	kernel *intBoolPredicateKernel
}

type intBoolPredicateKernel struct {
	code     []uint32
	maxStack int
}

func (vm *VM) tryIntPredicateReductionForLoopKernel(frame *CallFrame, base int, code []uint32, constants []runtime.Value, a int, sbx int) (bool, error) {
	if frame == nil || !vm.noGlobalLock || vm.globalOverrides != nil {
		return false, nil
	}
	forprepPC := frame.pc - 1
	shape, ok := matchIntPredicateReductionForLoopShape(code, constants, forprepPC, a, sbx)
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
	if start > limit || start < 0 || limit > intPredicateReductionLoopMaxLimit {
		return false, nil
	}

	fnVal, _, ok := vm.globalByStringConst(constants, shape.fnConst)
	if !ok {
		return false, nil
	}
	cl, ok := closureFromValue(fnVal)
	if !ok {
		return false, nil
	}
	predicate, ok := intBoolPredicateKernelForProto(cl.Proto)
	if !ok {
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

	if sum.IsInt() && count.IsInt() {
		sumInt := sum.Int()
		countInt := count.Int()
		for n := start; n <= limit; n++ {
			matched, ok := predicate.eval(n)
			if !ok {
				return false, nil
			}
			if matched {
				sumInt += n
				countInt++
			}
		}
		vm.setGlobalByStringConst(constants, shape.sumConst, sumIdx, runtime.IntValue(sumInt))
		vm.setGlobalByStringConst(constants, shape.countConst, countIdx, runtime.IntValue(countInt))
		vm.regs[base+a] = limitV
		vm.regs[base+a+3] = limitV
		frame.pc = shape.loopPC + 1
		return true, nil
	}

	one := runtime.IntValue(1)
	for n := start; n <= limit; n++ {
		matched, ok := predicate.eval(n)
		if !ok {
			return false, nil
		}
		if !matched {
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

// HasIntPredicateReductionLoopKernel reports whether a proto contains a
// structural driver loop that can be batched by
// tryIntPredicateReductionForLoopKernel.
func HasIntPredicateReductionLoopKernel(proto *FuncProto, globals map[string]*FuncProto) bool {
	if proto == nil {
		return false
	}
	for pc, inst := range proto.Code {
		if DecodeOp(inst) != OP_FORPREP {
			continue
		}
		if IsIntPredicateReductionLoopAt(proto, pc, globals) {
			return true
		}
	}
	return false
}

// IsIntPredicateReductionLoopAt checks one FORPREP site for the guarded
// predicate-call plus sum/count reduction shape.
func IsIntPredicateReductionLoopAt(proto *FuncProto, forprepPC int, globals map[string]*FuncProto) bool {
	if proto == nil || len(globals) == 0 || forprepPC < 0 || forprepPC >= len(proto.Code) {
		return false
	}
	inst := proto.Code[forprepPC]
	if DecodeOp(inst) != OP_FORPREP {
		return false
	}
	shape, ok := matchIntPredicateReductionForLoopShape(proto.Code, proto.Constants, forprepPC, DecodeA(inst), DecodesBx(inst))
	if !ok {
		return false
	}
	if shape.fnConst < 0 || shape.fnConst >= len(proto.Constants) || !proto.Constants[shape.fnConst].IsString() {
		return false
	}
	_, ok = intBoolPredicateKernelForProto(globals[proto.Constants[shape.fnConst].Str()])
	return ok
}

func matchIntPredicateReductionForLoopShape(code []uint32, constants []runtime.Value, forprepPC int, a int, sbx int) (intPredicateReductionLoopShape, bool) {
	var shape intPredicateReductionLoopShape
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

	return intPredicateReductionLoopShape{
		loopPC:     loopPC,
		fnConst:    fnConst,
		sumConst:   sumConst,
		countConst: countConst,
	}, true
}

func intBoolPredicateKernelForProto(p *FuncProto) (*intBoolPredicateKernel, bool) {
	if p == nil {
		return nil, false
	}
	if cache := p.IntBoolPredicateKernel; cache != nil {
		return cache.kernel, cache.kernel != nil
	}
	kernel, ok := analyzeIntBoolPredicateKernel(p)
	p.IntBoolPredicateKernel = &intBoolPredicateKernelCache{kernel: kernel}
	return kernel, ok
}

func analyzeIntBoolPredicateKernel(p *FuncProto) (*intBoolPredicateKernel, bool) {
	if p == nil || p.NumParams != 1 || p.IsVarArg || len(p.Protos) != 0 || p.MaxStack <= 0 || p.MaxStack > 32 {
		return nil, false
	}
	for _, c := range p.Constants {
		if !c.IsInt() {
			return nil, false
		}
	}
	for pc, inst := range p.Code {
		switch DecodeOp(inst) {
		case OP_LOADINT, OP_MOVE, OP_ADD, OP_MUL, OP_MOD, OP_EQ, OP_LT, OP_LE, OP_JMP, OP_LOADBOOL, OP_RETURN:
		default:
			return nil, false
		}
		if DecodeOp(inst) == OP_JMP {
			target := pc + 1 + DecodesBx(inst)
			if target < 0 || target > len(p.Code) {
				return nil, false
			}
		}
	}
	return &intBoolPredicateKernel{code: p.Code, maxStack: p.MaxStack}, true
}

func (k *intBoolPredicateKernel) eval(arg int64) (bool, bool) {
	if k == nil || k.maxStack <= 0 || k.maxStack > 32 {
		return false, false
	}
	var ints [32]int64
	var bools [32]bool
	var kinds [32]byte
	ints[0] = arg
	kinds[0] = 1
	pc := 0
	budget := 100000
	for pc >= 0 && pc < len(k.code) && budget > 0 {
		budget--
		inst := k.code[pc]
		pc++
		switch DecodeOp(inst) {
		case OP_LOADINT:
			a := DecodeA(inst)
			if a >= k.maxStack {
				return false, false
			}
			ints[a] = int64(DecodesBx(inst))
			kinds[a] = 1
		case OP_LOADBOOL:
			a := DecodeA(inst)
			if a >= k.maxStack {
				return false, false
			}
			bools[a] = DecodeB(inst) != 0
			kinds[a] = 2
			if DecodeC(inst) != 0 {
				pc++
			}
		case OP_MOVE:
			a, b := DecodeA(inst), DecodeB(inst)
			if a >= k.maxStack || b >= k.maxStack {
				return false, false
			}
			ints[a], bools[a], kinds[a] = ints[b], bools[b], kinds[b]
		case OP_ADD, OP_MUL, OP_MOD:
			a, b, c := DecodeA(inst), DecodeB(inst), DecodeC(inst)
			if a >= k.maxStack || b >= k.maxStack || c >= k.maxStack || kinds[b] != 1 || kinds[c] != 1 {
				return false, false
			}
			switch DecodeOp(inst) {
			case OP_ADD:
				ints[a] = ints[b] + ints[c]
			case OP_MUL:
				ints[a] = ints[b] * ints[c]
			case OP_MOD:
				if ints[c] == 0 {
					return false, false
				}
				ints[a] = ints[b] % ints[c]
			}
			kinds[a] = 1
		case OP_EQ, OP_LT, OP_LE:
			a, b, c := DecodeA(inst), DecodeB(inst), DecodeC(inst)
			if b >= k.maxStack || c >= k.maxStack || kinds[b] != 1 || kinds[c] != 1 {
				return false, false
			}
			var cmp bool
			switch DecodeOp(inst) {
			case OP_EQ:
				cmp = ints[b] == ints[c]
			case OP_LT:
				cmp = ints[b] < ints[c]
			case OP_LE:
				cmp = ints[b] <= ints[c]
			}
			if cmp != (a != 0) {
				pc++
			}
		case OP_JMP:
			pc += DecodesBx(inst)
		case OP_RETURN:
			a, b := DecodeA(inst), DecodeB(inst)
			if b != 2 || a >= k.maxStack || kinds[a] != 2 {
				return false, false
			}
			return bools[a], true
		default:
			return false, false
		}
	}
	return false, false
}
