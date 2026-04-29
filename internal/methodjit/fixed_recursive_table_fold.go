//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

type fixedRecursiveTableFoldProtocol struct {
	nilField    string
	nilCache    runtime.FieldCacheEntry
	baseValue   int64
	combineBias int64
	children    []fixedRecursiveTableFoldChild
}

type fixedRecursiveTableFoldChild struct {
	field string
	cache runtime.FieldCacheEntry
}

type fixedRecursiveTableFoldExpr struct {
	constant int64
	calls    map[string]int
	valid    bool
}

type fixedRecursiveTableFoldSlot struct {
	selfFunc bool
	field    string
	expr     fixedRecursiveTableFoldExpr
}

const (
	fixedFoldMaxInt64 = int64(^uint64(0) >> 1)
	fixedFoldMinInt64 = -fixedFoldMaxInt64 - 1
	fixedFoldMaxInt48 = (1 << 47) - 1
	fixedFoldMinInt48 = -(1 << 47)
)

func qualifiesForFixedRecursiveTableFold(proto *vm.FuncProto) bool {
	_, ok := analyzeFixedRecursiveTableFold(proto)
	return ok
}

func analyzeFixedRecursiveTableFold(proto *vm.FuncProto) (*fixedRecursiveTableFoldProtocol, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 1 || proto.Name == "" {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) < 8 {
		return nil, false
	}

	nilField, basePC, recursePC, ok := fixedFoldParseNilBaseHeader(proto)
	if !ok {
		return nil, false
	}
	baseValue, ok := fixedFoldParseIntReturn(proto, basePC)
	if !ok {
		return nil, false
	}
	expr, children, ok := fixedFoldParseRecursiveExpr(proto, recursePC)
	if !ok || !expr.valid || len(expr.calls) == 0 {
		return nil, false
	}
	for _, child := range children {
		if expr.calls[child.field] != 1 {
			return nil, false
		}
	}
	if len(expr.calls) != len(children) {
		return nil, false
	}

	protocolChildren := make([]fixedRecursiveTableFoldChild, len(children))
	for i, child := range children {
		protocolChildren[i] = fixedRecursiveTableFoldChild{field: child.field}
	}
	return &fixedRecursiveTableFoldProtocol{
		nilField:    nilField,
		baseValue:   baseValue,
		combineBias: expr.constant,
		children:    protocolChildren,
	}, true
}

func fixedFoldParseNilBaseHeader(proto *vm.FuncProto) (nilField string, basePC, recursePC int, ok bool) {
	code := proto.Code
	if len(code) < 6 || vm.DecodeOp(code[0]) != vm.OP_GETFIELD || vm.DecodeB(code[0]) != 0 {
		return "", 0, 0, false
	}
	nilField = protoConstString(proto, vm.DecodeC(code[0]))
	if nilField == "" {
		return "", 0, 0, false
	}
	if vm.DecodeOp(code[1]) != vm.OP_LOADNIL {
		return "", 0, 0, false
	}
	fieldSlot := vm.DecodeA(code[0])
	nilStart := vm.DecodeA(code[1])
	nilEnd := nilStart + vm.DecodeB(code[1])
	if vm.DecodeOp(code[2]) != vm.OP_EQ {
		return "", 0, 0, false
	}
	eqB, eqC := vm.DecodeB(code[2]), vm.DecodeC(code[2])
	if !((eqB == fieldSlot && eqC >= nilStart && eqC <= nilEnd) ||
		(eqC == fieldSlot && eqB >= nilStart && eqB <= nilEnd)) {
		return "", 0, 0, false
	}
	if vm.DecodeOp(code[3]) != vm.OP_JMP {
		return "", 0, 0, false
	}
	basePC = 4
	recursePC = 4 + vm.DecodesBx(code[3])
	if recursePC <= basePC || recursePC >= len(code) {
		return "", 0, 0, false
	}
	return nilField, basePC, recursePC, true
}

func fixedFoldParseIntReturn(proto *vm.FuncProto, pc int) (int64, bool) {
	if pc < 0 || pc+1 >= len(proto.Code) {
		return 0, false
	}
	load := proto.Code[pc]
	ret := proto.Code[pc+1]
	if vm.DecodeOp(load) != vm.OP_LOADINT || vm.DecodeOp(ret) != vm.OP_RETURN {
		return 0, false
	}
	if vm.DecodeA(ret) != vm.DecodeA(load) || vm.DecodeB(ret) != 2 {
		return 0, false
	}
	return int64(vm.DecodesBx(load)), true
}

func fixedFoldParseRecursiveExpr(proto *vm.FuncProto, startPC int) (fixedRecursiveTableFoldExpr, []fixedRecursiveTableFoldChild, bool) {
	slots := make([]fixedRecursiveTableFoldSlot, maxTrackedSlots)
	children := make([]fixedRecursiveTableFoldChild, 0, 2)
	for pc := startPC; pc < len(proto.Code); pc++ {
		inst := proto.Code[pc]
		a, b, c := vm.DecodeA(inst), vm.DecodeB(inst), vm.DecodeC(inst)
		switch vm.DecodeOp(inst) {
		case vm.OP_LOADINT:
			if !fixedFoldSlotOK(a) {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			slots[a] = fixedRecursiveTableFoldSlot{expr: fixedFoldConst(int64(vm.DecodesBx(inst)))}
		case vm.OP_MOVE:
			if !fixedFoldSlotOK(a) || !fixedFoldSlotOK(b) {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			slots[a] = slots[b]
		case vm.OP_GETGLOBAL:
			if !fixedFoldSlotOK(a) || protoConstString(proto, vm.DecodeBx(inst)) != proto.Name {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			slots[a] = fixedRecursiveTableFoldSlot{selfFunc: true}
		case vm.OP_GETFIELD:
			if !fixedFoldSlotOK(a) || b != 0 {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			field := protoConstString(proto, c)
			if field == "" {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			slots[a] = fixedRecursiveTableFoldSlot{field: field}
		case vm.OP_CALL:
			if !fixedFoldSlotOK(a) || !slots[a].selfFunc || b != 2 || c != 2 || !fixedFoldSlotOK(a+1) {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			field := slots[a+1].field
			if field == "" {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			children = append(children, fixedRecursiveTableFoldChild{field: field})
			slots[a] = fixedRecursiveTableFoldSlot{expr: fixedFoldCall(field)}
		case vm.OP_ADD:
			if !fixedFoldSlotOK(a) || !fixedFoldSlotOK(b) || !fixedFoldSlotOK(c) {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			expr, ok := fixedFoldAdd(slots[b].expr, slots[c].expr)
			if !ok {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			slots[a] = fixedRecursiveTableFoldSlot{expr: expr}
		case vm.OP_RETURN:
			if !fixedFoldSlotOK(a) || b != 2 {
				return fixedRecursiveTableFoldExpr{}, nil, false
			}
			return slots[a].expr, children, true
		default:
			return fixedRecursiveTableFoldExpr{}, nil, false
		}
	}
	return fixedRecursiveTableFoldExpr{}, nil, false
}

func fixedFoldSlotOK(slot int) bool {
	return slot >= 0 && slot < maxTrackedSlots
}

func fixedFoldConst(v int64) fixedRecursiveTableFoldExpr {
	return fixedRecursiveTableFoldExpr{constant: v, calls: make(map[string]int), valid: true}
}

func fixedFoldCall(field string) fixedRecursiveTableFoldExpr {
	return fixedRecursiveTableFoldExpr{calls: map[string]int{field: 1}, valid: true}
}

func fixedFoldAdd(a, b fixedRecursiveTableFoldExpr) (fixedRecursiveTableFoldExpr, bool) {
	if !a.valid || !b.valid {
		return fixedRecursiveTableFoldExpr{}, false
	}
	constant, ok := fixedFoldCheckedAdd(a.constant, b.constant)
	if !ok {
		return fixedRecursiveTableFoldExpr{}, false
	}
	out := fixedRecursiveTableFoldExpr{
		constant: constant,
		calls:    make(map[string]int, len(a.calls)+len(b.calls)),
		valid:    true,
	}
	for field, count := range a.calls {
		out.calls[field] += count
	}
	for field, count := range b.calls {
		out.calls[field] += count
	}
	return out, true
}

func fixedFoldCheckedAdd(a, b int64) (int64, bool) {
	if (b > 0 && a > fixedFoldMaxInt64-b) || (b < 0 && a < fixedFoldMinInt64-b) {
		return 0, false
	}
	out := a + b
	if out < fixedFoldMinInt48 || out > fixedFoldMaxInt48 {
		return 0, false
	}
	return out, true
}

func newFixedRecursiveTableFoldCompiled(proto *vm.FuncProto) (*CompiledFunction, bool) {
	protocol, ok := analyzeFixedRecursiveTableFold(proto)
	if !ok {
		return nil, false
	}
	return &CompiledFunction{
		Proto:                   proto,
		numRegs:                 proto.MaxStack,
		FixedRecursiveTableFold: protocol,
	}, true
}

func (tm *TieringManager) compileFixedRecursiveTableFoldTier2(proto *vm.FuncProto) (*CompiledFunction, bool) {
	cf, ok := newFixedRecursiveTableFoldCompiled(proto)
	if !ok {
		return nil, false
	}
	tm.tier2Attempts++
	attempt := tm.tier2Attempts
	tm.traceEvent("tier2_attempt", "tier2", proto, map[string]any{
		"attempt":    attempt,
		"call_count": proto.CallCount,
		"protocol":   "fixed_recursive_table_fold",
	})
	tm.traceTier2Success(proto, cf, attempt)
	return cf, true
}

func (tm *TieringManager) executeFixedRecursiveTableFold(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	if cf == nil || cf.FixedRecursiveTableFold == nil || proto == nil {
		return nil, fmt.Errorf("tier2: missing fixed recursive table fold protocol")
	}
	if base < 0 || base >= len(regs) {
		return nil, fmt.Errorf("tier2: fixed recursive table fold base %d outside regs len %d", base, len(regs))
	}
	if !tm.fixedRecursiveSelfGlobalMatches(proto) {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive table fold self global changed")
		return nil, fmt.Errorf("tier2: fixed recursive table fold self global changed")
	}
	proto.EnteredTier2 = 1
	n, ok := cf.FixedRecursiveTableFold.fold(regs[base])
	if !ok {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive table fold fallback")
		return nil, fmt.Errorf("tier2: fixed recursive table fold fallback")
	}
	result := runtime.IntValue(n)
	regs[base] = result
	return runtime.ReuseValueSlice1(retBuf, result), nil
}

func (tm *TieringManager) fixedRecursiveSelfGlobalMatches(proto *vm.FuncProto) bool {
	if tm == nil || tm.callVM == nil || proto == nil || proto.Name == "" {
		return false
	}
	cl, ok := vmClosureFromValue(tm.callVM.GetGlobal(proto.Name))
	return ok && cl != nil && cl.Proto == proto
}

func (p *fixedRecursiveTableFoldProtocol) fold(v runtime.Value) (int64, bool) {
	t := v.Table()
	if t == nil {
		return 0, false
	}
	nilValue := t.RawGetStringCached(p.nilField, &p.nilCache)
	if nilValue.IsNil() {
		return p.baseValue, true
	}
	total := p.combineBias
	for i := range p.children {
		childValue := t.RawGetStringCached(p.children[i].field, &p.children[i].cache)
		childTotal, ok := p.fold(childValue)
		if !ok {
			return 0, false
		}
		total, ok = fixedFoldCheckedAdd(total, childTotal)
		if !ok {
			return 0, false
		}
	}
	return total, true
}
