package vm

import "github.com/gscript/gscript/internal/runtime"

const (
	rawIntNestedMaxIterations = 1_000_000
	rawIntNestedMaxStack      = 65_536
	rawIntNestedMaxInt64      = int64(^uint64(0) >> 1)
	rawIntNestedMinInt64      = -rawIntNestedMaxInt64 - 1
	rawIntNestedMaxInt48      = (1 << 47) - 1
	rawIntNestedMinInt48      = -(1 << 47)
)

type rawIntNestedKernelCache struct {
	analyzed bool
	kernel   *rawIntNestedKernel
}

type rawIntNestedKernel struct {
	selfName string
	baseAdd  int64
	zeroArg  int64
	mStep    int64
	nStep    int64
}

func IsRawIntNestedKernelProto(proto *FuncProto) bool {
	_, ok := analyzeRawIntNestedKernel(proto)
	return ok
}

func (vm *VM) tryRunRawIntNestedValueKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if vm == nil || cl == nil || cl.Proto == nil || len(args) != 2 || vm.methodJIT == nil || !cl.Proto.Tier2Promoted {
		return false, nil, nil
	}
	cache := rawIntNestedKernelForProto(cl.Proto)
	if cache.kernel == nil || !vm.rawIntNestedSelfGlobalMatches(cl, cache.kernel.selfName) {
		return false, nil, nil
	}
	n, ok := cache.kernel.fold(args[0], args[1])
	if !ok {
		return false, nil, nil
	}
	result := runtime.IntValue(n)
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(vm.retBuf[:0], result), nil
}

func rawIntNestedKernelForProto(proto *FuncProto) *rawIntNestedKernelCache {
	cache := proto.RawIntNestedKernel
	if cache == nil {
		cache = &rawIntNestedKernelCache{analyzed: true}
		if kernel, ok := analyzeRawIntNestedKernel(proto); ok {
			cache.kernel = kernel
		}
		proto.RawIntNestedKernel = cache
	}
	return cache
}

func (vm *VM) rawIntNestedSelfGlobalMatches(cl *Closure, selfName string) bool {
	if vm == nil || cl == nil || selfName == "" {
		return false
	}
	current, ok := closureFromValue(vm.GetGlobal(selfName))
	return ok && current == cl
}

func analyzeRawIntNestedKernel(proto *FuncProto) (*rawIntNestedKernel, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 2 || proto.Name == "" {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) < 20 {
		return nil, false
	}

	secondHeader, baseAdd, ok := rawIntNestedParseBaseCase(proto, 0, 0, 1)
	if !ok {
		return nil, false
	}
	generalStart, zeroArg, mStep, ok := rawIntNestedParseZeroCase(proto, secondHeader)
	if !ok {
		return nil, false
	}
	mStep2, nStep, end, ok := rawIntNestedParseNestedCase(proto, generalStart)
	if !ok || end != len(proto.Code) || mStep2 != mStep {
		return nil, false
	}
	if mStep <= 0 || nStep <= 0 || zeroArg < 0 {
		return nil, false
	}
	return &rawIntNestedKernel{
		selfName: proto.Name,
		baseAdd:  baseAdd,
		zeroArg:  zeroArg,
		mStep:    mStep,
		nStep:    nStep,
	}, true
}

func rawIntNestedParseBaseCase(proto *FuncProto, pc, mSlot, nSlot int) (next int, baseAdd int64, ok bool) {
	code := proto.Code
	if pc+5 >= len(code) {
		return 0, 0, false
	}
	zeroSlot, zero, ok := rawIntNestedLoadInt(proto, pc)
	if !ok || zero != 0 {
		return 0, 0, false
	}
	if !rawIntNestedEqSlotConstZero(code[pc+1], mSlot, zeroSlot) || DecodeOp(code[pc+2]) != OP_JMP {
		return 0, 0, false
	}
	next = pc + 3 + DecodesBx(code[pc+2])
	if next <= pc+3 || next > len(code) {
		return 0, 0, false
	}
	addSlot, addValue, ok := rawIntNestedLoadInt(proto, pc+3)
	if !ok {
		return 0, 0, false
	}
	inst := code[pc+4]
	if DecodeOp(inst) != OP_ADD {
		return 0, 0, false
	}
	retSlot := DecodeA(inst)
	b, c := DecodeB(inst), DecodeC(inst)
	if !((b == nSlot && c == addSlot) || (b == addSlot && c == nSlot)) {
		return 0, 0, false
	}
	if DecodeOp(code[pc+5]) != OP_RETURN || DecodeA(code[pc+5]) != retSlot || DecodeB(code[pc+5]) != 2 {
		return 0, 0, false
	}
	return next, addValue, true
}

func rawIntNestedParseZeroCase(proto *FuncProto, pc int) (next int, zeroArg, mStep int64, ok bool) {
	code := proto.Code
	if pc+8 >= len(code) {
		return 0, 0, 0, false
	}
	zeroSlot, zero, ok := rawIntNestedLoadInt(proto, pc)
	if !ok || zero != 0 {
		return 0, 0, 0, false
	}
	if !rawIntNestedEqSlotConstZero(code[pc+1], 1, zeroSlot) || DecodeOp(code[pc+2]) != OP_JMP {
		return 0, 0, 0, false
	}
	next = pc + 3 + DecodesBx(code[pc+2])
	if next <= pc+3 || next > len(code) {
		return 0, 0, 0, false
	}
	callPC := pc + 3
	fnSlot, ok := rawIntNestedSelfGlobal(proto, callPC)
	if !ok {
		return 0, 0, 0, false
	}
	stepSlot, stepValue, ok := rawIntNestedLoadInt(proto, callPC+1)
	if !ok {
		return 0, 0, 0, false
	}
	if !rawIntNestedSubParamConst(code[callPC+2], fnSlot+1, 0, stepSlot) {
		return 0, 0, 0, false
	}
	argSlot, argValue, ok := rawIntNestedLoadInt(proto, callPC+3)
	if !ok || argSlot != fnSlot+2 {
		return 0, 0, 0, false
	}
	if !rawIntNestedTailSelfCallReturn(code[callPC+4], code[callPC+5], fnSlot) {
		return 0, 0, 0, false
	}
	return next, argValue, stepValue, true
}

func rawIntNestedParseNestedCase(proto *FuncProto, pc int) (mStep, nStep int64, next int, ok bool) {
	code := proto.Code
	if pc+10 >= len(code) {
		return 0, 0, 0, false
	}
	outerSlot, ok := rawIntNestedSelfGlobal(proto, pc)
	if !ok {
		return 0, 0, 0, false
	}
	mStepSlot, mStepValue, ok := rawIntNestedLoadInt(proto, pc+1)
	if !ok || !rawIntNestedSubParamConst(code[pc+2], outerSlot+1, 0, mStepSlot) {
		return 0, 0, 0, false
	}
	innerSlot, ok := rawIntNestedSelfGlobal(proto, pc+3)
	if !ok {
		return 0, 0, 0, false
	}
	if DecodeOp(code[pc+4]) != OP_MOVE || DecodeA(code[pc+4]) != innerSlot+1 || DecodeB(code[pc+4]) != 0 {
		return 0, 0, 0, false
	}
	nStepSlot, nStepValue, ok := rawIntNestedLoadInt(proto, pc+5)
	if !ok || !rawIntNestedSubParamConst(code[pc+6], innerSlot+2, 1, nStepSlot) {
		return 0, 0, 0, false
	}
	if DecodeOp(code[pc+7]) != OP_CALL || DecodeA(code[pc+7]) != innerSlot ||
		DecodeB(code[pc+7]) != 3 || DecodeC(code[pc+7]) != 2 {
		return 0, 0, 0, false
	}
	if DecodeOp(code[pc+8]) != OP_MOVE || DecodeA(code[pc+8]) != outerSlot+2 || DecodeB(code[pc+8]) != innerSlot {
		return 0, 0, 0, false
	}
	if !rawIntNestedTailSelfCallReturn(code[pc+9], code[pc+10], outerSlot) {
		return 0, 0, 0, false
	}
	return mStepValue, nStepValue, pc + 11, true
}

func rawIntNestedLoadInt(proto *FuncProto, pc int) (slot int, value int64, ok bool) {
	if proto == nil || pc < 0 || pc >= len(proto.Code) {
		return 0, 0, false
	}
	inst := proto.Code[pc]
	switch DecodeOp(inst) {
	case OP_LOADINT:
		return DecodeA(inst), int64(DecodesBx(inst)), true
	case OP_LOADK:
		idx := DecodeBx(inst)
		if idx < 0 || idx >= len(proto.Constants) || !proto.Constants[idx].IsInt() {
			return 0, 0, false
		}
		return DecodeA(inst), proto.Constants[idx].Int(), true
	default:
		return 0, 0, false
	}
}

func rawIntNestedEqSlotConstZero(inst uint32, paramSlot, zeroSlot int) bool {
	if DecodeOp(inst) != OP_EQ || DecodeA(inst) != 0 {
		return false
	}
	b, c := DecodeB(inst), DecodeC(inst)
	return (b == paramSlot && c == zeroSlot) || (b == zeroSlot && c == paramSlot)
}

func rawIntNestedSubParamConst(inst uint32, dstSlot, paramSlot, constSlot int) bool {
	return DecodeOp(inst) == OP_SUB &&
		DecodeA(inst) == dstSlot &&
		DecodeB(inst) == paramSlot &&
		DecodeC(inst) == constSlot
}

func rawIntNestedTailSelfCallReturn(callInst, returnInst uint32, fnSlot int) bool {
	return DecodeOp(callInst) == OP_CALL &&
		DecodeA(callInst) == fnSlot &&
		DecodeB(callInst) == 3 &&
		DecodeC(callInst) == 0 &&
		DecodeOp(returnInst) == OP_RETURN &&
		DecodeA(returnInst) == fnSlot &&
		DecodeB(returnInst) == 0
}

func rawIntNestedSelfGlobal(proto *FuncProto, pc int) (slot int, ok bool) {
	if proto == nil || pc < 0 || pc >= len(proto.Code) {
		return 0, false
	}
	inst := proto.Code[pc]
	if DecodeOp(inst) != OP_GETGLOBAL {
		return 0, false
	}
	idx := DecodeBx(inst)
	if idx < 0 || idx >= len(proto.Constants) || !proto.Constants[idx].IsString() || proto.Constants[idx].Str() != proto.Name {
		return 0, false
	}
	return DecodeA(inst), true
}

func (k *rawIntNestedKernel) fold(mv, nv runtime.Value) (int64, bool) {
	if k == nil || !mv.IsInt() || !nv.IsInt() || k.mStep <= 0 || k.nStep <= 0 {
		return 0, false
	}
	m := mv.Int()
	n := nv.Int()
	if m < 0 || n < 0 || k.zeroArg < 0 {
		return 0, false
	}
	if out, ok := k.foldSmallRows(m, n); ok {
		return out, true
	}
	stack := make([]int64, 0, 32)
	for iter := 0; iter < rawIntNestedMaxIterations; iter++ {
		switch {
		case m == 0:
			next, ok := rawIntNestedCheckedAdd(n, k.baseAdd)
			if !ok {
				return 0, false
			}
			n = next
			if len(stack) == 0 {
				return n, true
			}
			m = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		case n == 0:
			if m < k.mStep {
				return 0, false
			}
			m -= k.mStep
			n = k.zeroArg
		default:
			if m < k.mStep || n < k.nStep {
				return 0, false
			}
			if len(stack) >= rawIntNestedMaxStack {
				return 0, false
			}
			stack = append(stack, m-k.mStep)
			n -= k.nStep
		}
	}
	return 0, false
}

func (k *rawIntNestedKernel) foldSmallRows(m, n int64) (int64, bool) {
	if k.baseAdd != 1 || k.mStep != 1 || k.nStep != 1 {
		return 0, false
	}
	z := k.zeroArg
	switch m {
	case 0:
		return rawIntNestedCheckedAdd(n, 1)
	case 1:
		out, ok := rawIntNestedCheckedAdd(n, z)
		if !ok {
			return 0, false
		}
		return rawIntNestedCheckedAdd(out, 1)
	case 2:
		count, ok := rawIntNestedCheckedAdd(n, 1)
		if !ok {
			return 0, false
		}
		step, ok := rawIntNestedCheckedAdd(z, 1)
		if !ok {
			return 0, false
		}
		product, ok := rawIntNestedCheckedMul(count, step)
		if !ok {
			return 0, false
		}
		return rawIntNestedCheckedAdd(z, product)
	case 3:
		count, ok := rawIntNestedCheckedAdd(n, 1)
		if !ok {
			return 0, false
		}
		if z == 0 {
			return count, true
		}
		s, ok := rawIntNestedCheckedAdd(z, 1)
		if !ok {
			return 0, false
		}
		pow, ok := rawIntNestedCheckedPow(s, count)
		if !ok {
			return 0, false
		}
		left, ok := rawIntNestedCheckedMul(pow, z)
		if !ok {
			return 0, false
		}
		c, ok := rawIntNestedCheckedAdd(2*z, 1)
		if !ok {
			return 0, false
		}
		numer, ok := rawIntNestedCheckedMul(c, pow-1)
		if !ok || numer%z != 0 {
			return 0, false
		}
		right := numer / z
		return rawIntNestedCheckedAdd(left, right)
	default:
		return 0, false
	}
}

func rawIntNestedCheckedAdd(a, b int64) (int64, bool) {
	if (b > 0 && a > rawIntNestedMaxInt64-b) || (b < 0 && a < rawIntNestedMinInt64-b) {
		return 0, false
	}
	out := a + b
	if out < rawIntNestedMinInt48 || out > rawIntNestedMaxInt48 {
		return 0, false
	}
	return out, true
}

func rawIntNestedCheckedMul(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > rawIntNestedMaxInt48/b {
		return 0, false
	}
	out := a * b
	if out > rawIntNestedMaxInt48 {
		return 0, false
	}
	return out, true
}

func rawIntNestedCheckedPow(base, exp int64) (int64, bool) {
	if exp < 0 {
		return 0, false
	}
	result := int64(1)
	for exp > 0 {
		if exp&1 != 0 {
			var ok bool
			result, ok = rawIntNestedCheckedMul(result, base)
			if !ok {
				return 0, false
			}
		}
		exp >>= 1
		if exp == 0 {
			break
		}
		var ok bool
		base, ok = rawIntNestedCheckedMul(base, base)
		if !ok {
			return 0, false
		}
	}
	return result, true
}
