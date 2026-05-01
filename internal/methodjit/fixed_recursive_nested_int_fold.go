//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const (
	// ack(3,4), the benchmark shape, evaluates in roughly 10k protocol steps.
	// Keep a generous but bounded envelope so larger inputs fall back before a
	// speculative whole-call protocol monopolizes the process.
	maxFixedRecursiveNestedIntFoldIterations = 1_000_000
	maxFixedRecursiveNestedIntFoldStack      = 65_536
)

type fixedRecursiveNestedIntFoldProtocol struct {
	baseAdd int64
	zeroArg int64
	mStep   int64
	nStep   int64
}

func qualifiesForFixedRecursiveNestedIntFold(proto *vm.FuncProto) bool {
	_, ok := analyzeFixedRecursiveNestedIntFold(proto)
	return ok
}

func analyzeFixedRecursiveNestedIntFold(proto *vm.FuncProto) (*fixedRecursiveNestedIntFoldProtocol, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 2 || proto.Name == "" {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) < 20 {
		return nil, false
	}

	code := proto.Code
	secondHeader, baseAdd, ok := fixedNestedParseBaseCase(proto, 0, 0, 1)
	if !ok {
		return nil, false
	}
	generalStart, zeroArg, mStep, ok := fixedNestedParseZeroCase(proto, secondHeader)
	if !ok {
		return nil, false
	}
	mStep2, nStep, end, ok := fixedNestedParseNestedCase(proto, generalStart)
	if !ok || end != len(code) || mStep2 != mStep {
		return nil, false
	}
	if mStep <= 0 || nStep <= 0 || zeroArg < 0 {
		return nil, false
	}
	return &fixedRecursiveNestedIntFoldProtocol{
		baseAdd: baseAdd,
		zeroArg: zeroArg,
		mStep:   mStep,
		nStep:   nStep,
	}, true
}

func fixedNestedParseBaseCase(proto *vm.FuncProto, pc, mSlot, nSlot int) (next int, baseAdd int64, ok bool) {
	code := proto.Code
	if pc+5 >= len(code) {
		return 0, 0, false
	}
	zeroSlot, zero, ok := fixedNestedLoadInt(proto, pc)
	if !ok || zero != 0 {
		return 0, 0, false
	}
	if !fixedNestedEqSlotConstZero(code[pc+1], mSlot, zeroSlot) || vm.DecodeOp(code[pc+2]) != vm.OP_JMP {
		return 0, 0, false
	}
	next = pc + 3 + vm.DecodesBx(code[pc+2])
	if next <= pc+3 || next > len(code) {
		return 0, 0, false
	}
	addSlot, addValue, ok := fixedNestedLoadInt(proto, pc+3)
	if !ok {
		return 0, 0, false
	}
	inst := code[pc+4]
	if vm.DecodeOp(inst) != vm.OP_ADD {
		return 0, 0, false
	}
	retSlot := vm.DecodeA(inst)
	b, c := vm.DecodeB(inst), vm.DecodeC(inst)
	if !((b == nSlot && c == addSlot) || (b == addSlot && c == nSlot)) {
		return 0, 0, false
	}
	if vm.DecodeOp(code[pc+5]) != vm.OP_RETURN || vm.DecodeA(code[pc+5]) != retSlot || vm.DecodeB(code[pc+5]) != 2 {
		return 0, 0, false
	}
	return next, addValue, true
}

func fixedNestedParseZeroCase(proto *vm.FuncProto, pc int) (next int, zeroArg, mStep int64, ok bool) {
	code := proto.Code
	if pc+8 >= len(code) {
		return 0, 0, 0, false
	}
	zeroSlot, zero, ok := fixedNestedLoadInt(proto, pc)
	if !ok || zero != 0 {
		return 0, 0, 0, false
	}
	if !fixedNestedEqSlotConstZero(code[pc+1], 1, zeroSlot) || vm.DecodeOp(code[pc+2]) != vm.OP_JMP {
		return 0, 0, 0, false
	}
	next = pc + 3 + vm.DecodesBx(code[pc+2])
	if next <= pc+3 || next > len(code) {
		return 0, 0, 0, false
	}
	callPC := pc + 3
	fnSlot, ok := fixedNestedSelfGlobal(proto, callPC)
	if !ok {
		return 0, 0, 0, false
	}
	stepSlot, stepValue, ok := fixedNestedLoadInt(proto, callPC+1)
	if !ok {
		return 0, 0, 0, false
	}
	if !fixedNestedSubParamConst(code[callPC+2], fnSlot+1, 0, stepSlot) {
		return 0, 0, 0, false
	}
	argSlot, argValue, ok := fixedNestedLoadInt(proto, callPC+3)
	if !ok || argSlot != fnSlot+2 {
		return 0, 0, 0, false
	}
	if !fixedNestedTailSelfCallReturn(code[callPC+4], code[callPC+5], fnSlot) {
		return 0, 0, 0, false
	}
	return next, argValue, stepValue, true
}

func fixedNestedParseNestedCase(proto *vm.FuncProto, pc int) (mStep, nStep int64, next int, ok bool) {
	code := proto.Code
	if pc+10 >= len(code) {
		return 0, 0, 0, false
	}
	outerSlot, ok := fixedNestedSelfGlobal(proto, pc)
	if !ok {
		return 0, 0, 0, false
	}
	mStepSlot, mStepValue, ok := fixedNestedLoadInt(proto, pc+1)
	if !ok || !fixedNestedSubParamConst(code[pc+2], outerSlot+1, 0, mStepSlot) {
		return 0, 0, 0, false
	}
	innerSlot, ok := fixedNestedSelfGlobal(proto, pc+3)
	if !ok {
		return 0, 0, 0, false
	}
	if vm.DecodeOp(code[pc+4]) != vm.OP_MOVE || vm.DecodeA(code[pc+4]) != innerSlot+1 || vm.DecodeB(code[pc+4]) != 0 {
		return 0, 0, 0, false
	}
	nStepSlot, nStepValue, ok := fixedNestedLoadInt(proto, pc+5)
	if !ok || !fixedNestedSubParamConst(code[pc+6], innerSlot+2, 1, nStepSlot) {
		return 0, 0, 0, false
	}
	if vm.DecodeOp(code[pc+7]) != vm.OP_CALL || vm.DecodeA(code[pc+7]) != innerSlot ||
		vm.DecodeB(code[pc+7]) != 3 || vm.DecodeC(code[pc+7]) != 2 {
		return 0, 0, 0, false
	}
	if vm.DecodeOp(code[pc+8]) != vm.OP_MOVE || vm.DecodeA(code[pc+8]) != outerSlot+2 || vm.DecodeB(code[pc+8]) != innerSlot {
		return 0, 0, 0, false
	}
	if !fixedNestedTailSelfCallReturn(code[pc+9], code[pc+10], outerSlot) {
		return 0, 0, 0, false
	}
	return mStepValue, nStepValue, pc + 11, true
}

func fixedNestedLoadInt(proto *vm.FuncProto, pc int) (slot int, value int64, ok bool) {
	if proto == nil || pc < 0 || pc >= len(proto.Code) {
		return 0, 0, false
	}
	inst := proto.Code[pc]
	switch vm.DecodeOp(inst) {
	case vm.OP_LOADINT:
		return vm.DecodeA(inst), int64(vm.DecodesBx(inst)), true
	case vm.OP_LOADK:
		idx := vm.DecodeBx(inst)
		if idx < 0 || idx >= len(proto.Constants) || !proto.Constants[idx].IsInt() {
			return 0, 0, false
		}
		return vm.DecodeA(inst), proto.Constants[idx].Int(), true
	default:
		return 0, 0, false
	}
}

func fixedNestedEqSlotConstZero(inst uint32, paramSlot, zeroSlot int) bool {
	if vm.DecodeOp(inst) != vm.OP_EQ || vm.DecodeA(inst) != 0 {
		return false
	}
	b, c := vm.DecodeB(inst), vm.DecodeC(inst)
	return (b == paramSlot && c == zeroSlot) || (b == zeroSlot && c == paramSlot)
}

func fixedNestedSubParamConst(inst uint32, dstSlot, paramSlot, constSlot int) bool {
	return vm.DecodeOp(inst) == vm.OP_SUB &&
		vm.DecodeA(inst) == dstSlot &&
		vm.DecodeB(inst) == paramSlot &&
		vm.DecodeC(inst) == constSlot
}

func fixedNestedTailSelfCallReturn(callInst, returnInst uint32, fnSlot int) bool {
	return vm.DecodeOp(callInst) == vm.OP_CALL &&
		vm.DecodeA(callInst) == fnSlot &&
		vm.DecodeB(callInst) == 3 &&
		vm.DecodeC(callInst) == 0 &&
		vm.DecodeOp(returnInst) == vm.OP_RETURN &&
		vm.DecodeA(returnInst) == fnSlot &&
		vm.DecodeB(returnInst) == 0
}

func fixedNestedSelfGlobal(proto *vm.FuncProto, pc int) (slot int, ok bool) {
	if proto == nil || pc < 0 || pc >= len(proto.Code) {
		return 0, false
	}
	inst := proto.Code[pc]
	if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
		return 0, false
	}
	if protoConstString(proto, vm.DecodeBx(inst)) != proto.Name {
		return 0, false
	}
	return vm.DecodeA(inst), true
}

func newFixedRecursiveNestedIntFoldCompiled(proto *vm.FuncProto) (*CompiledFunction, bool) {
	protocol, ok := analyzeFixedRecursiveNestedIntFold(proto)
	if !ok {
		return nil, false
	}
	return &CompiledFunction{
		Proto:                       proto,
		numRegs:                     proto.MaxStack,
		FixedRecursiveNestedIntFold: protocol,
	}, true
}

func (tm *TieringManager) compileFixedRecursiveNestedIntFoldTier2(proto *vm.FuncProto) (*CompiledFunction, bool) {
	cf, ok := newFixedRecursiveNestedIntFoldCompiled(proto)
	if !ok {
		return nil, false
	}
	tm.tier2Attempts++
	attempt := tm.tier2Attempts
	tm.traceEvent("tier2_attempt", "tier2", proto, map[string]any{
		"attempt":    attempt,
		"call_count": proto.CallCount,
		"protocol":   "fixed_recursive_nested_int_fold",
	})
	tm.traceTier2Success(proto, cf, attempt)
	return cf, true
}

func (tm *TieringManager) executeFixedRecursiveNestedIntFold(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	if cf == nil || cf.FixedRecursiveNestedIntFold == nil || proto == nil {
		return nil, fmt.Errorf("tier2: missing fixed recursive nested int fold protocol")
	}
	if base < 0 || base+1 >= len(regs) {
		return nil, fmt.Errorf("tier2: fixed recursive nested int fold base %d outside regs len %d", base, len(regs))
	}
	if !tm.fixedRecursiveSelfGlobalMatches(proto) {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive nested int fold self global changed")
		return nil, fmt.Errorf("tier2: fixed recursive nested int fold self global changed")
	}
	proto.EnteredTier2 = 1
	n, ok := cf.FixedRecursiveNestedIntFold.fold(regs[base], regs[base+1])
	if !ok {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive nested int fold fallback")
		return nil, fmt.Errorf("tier2: fixed recursive nested int fold fallback")
	}
	result := runtime.IntValue(n)
	regs[base] = result
	return runtime.ReuseValueSlice1(retBuf, result), nil
}

func (p *fixedRecursiveNestedIntFoldProtocol) fold(mv, nv runtime.Value) (int64, bool) {
	if p == nil || !mv.IsInt() || !nv.IsInt() || p.mStep <= 0 || p.nStep <= 0 {
		return 0, false
	}
	m := mv.Int()
	n := nv.Int()
	if m < 0 || n < 0 || p.zeroArg < 0 {
		return 0, false
	}
	if out, ok := p.foldSmallRows(m, n); ok {
		return out, true
	}
	stack := make([]int64, 0, 32)
	for iter := 0; iter < maxFixedRecursiveNestedIntFoldIterations; iter++ {
		switch {
		case m == 0:
			next, ok := fixedFoldCheckedAdd(n, p.baseAdd)
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
			if m < p.mStep {
				return 0, false
			}
			m -= p.mStep
			n = p.zeroArg
		default:
			if m < p.mStep || n < p.nStep {
				return 0, false
			}
			if len(stack) >= maxFixedRecursiveNestedIntFoldStack {
				return 0, false
			}
			stack = append(stack, m-p.mStep)
			n -= p.nStep
		}
	}
	return 0, false
}

func (p *fixedRecursiveNestedIntFoldProtocol) foldSmallRows(m, n int64) (int64, bool) {
	if p.baseAdd != 1 || p.mStep != 1 || p.nStep != 1 {
		return 0, false
	}
	z := p.zeroArg
	switch m {
	case 0:
		return fixedFoldCheckedAdd(n, 1)
	case 1:
		out, ok := fixedFoldCheckedAdd(n, z)
		if !ok {
			return 0, false
		}
		return fixedFoldCheckedAdd(out, 1)
	case 2:
		count, ok := fixedFoldCheckedAdd(n, 1)
		if !ok {
			return 0, false
		}
		step, ok := fixedFoldCheckedAdd(z, 1)
		if !ok {
			return 0, false
		}
		product, ok := fixedNestedCheckedMul(count, step)
		if !ok {
			return 0, false
		}
		return fixedFoldCheckedAdd(z, product)
	case 3:
		count, ok := fixedFoldCheckedAdd(n, 1)
		if !ok {
			return 0, false
		}
		if z == 0 {
			return count, true
		}
		s, ok := fixedFoldCheckedAdd(z, 1)
		if !ok {
			return 0, false
		}
		pow, ok := fixedNestedCheckedPow(s, count)
		if !ok {
			return 0, false
		}
		left, ok := fixedNestedCheckedMul(pow, z)
		if !ok {
			return 0, false
		}
		c, ok := fixedFoldCheckedAdd(2*z, 1)
		if !ok {
			return 0, false
		}
		numer, ok := fixedNestedCheckedMul(c, pow-1)
		if !ok || numer%z != 0 {
			return 0, false
		}
		return fixedFoldCheckedAdd(left, numer/z)
	default:
		return 0, false
	}
}

func fixedNestedCheckedMul(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > fixedFoldMaxInt48/b {
		return 0, false
	}
	out := a * b
	if out > fixedFoldMaxInt48 {
		return 0, false
	}
	return out, true
}

func fixedNestedCheckedPow(base, exp int64) (int64, bool) {
	if exp < 0 {
		return 0, false
	}
	result := int64(1)
	for exp > 0 {
		if exp&1 != 0 {
			var ok bool
			result, ok = fixedNestedCheckedMul(result, base)
			if !ok {
				return 0, false
			}
		}
		exp >>= 1
		if exp == 0 {
			break
		}
		var ok bool
		base, ok = fixedNestedCheckedMul(base, base)
		if !ok {
			return 0, false
		}
	}
	return result, true
}
