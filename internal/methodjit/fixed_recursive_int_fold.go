//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sort"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const maxFixedRecursiveIntFoldIterations = 1_000_000

type fixedRecursiveIntFoldProtocol struct {
	threshold int64
	bias      int64
	terms     []fixedRecursiveIntFoldTerm
}

type fixedRecursiveIntFoldTerm struct {
	decrement int64
	count     int
}

type fixedRecursiveIntFoldExpr struct {
	constant int64
	calls    map[int64]int
	valid    bool
}

type fixedRecursiveIntFoldSlotKind uint8

const (
	fixedIntSlotUnknown fixedRecursiveIntFoldSlotKind = iota
	fixedIntSlotParam
	fixedIntSlotConst
	fixedIntSlotSelfFunc
	fixedIntSlotArg
	fixedIntSlotExpr
)

type fixedRecursiveIntFoldSlot struct {
	kind      fixedRecursiveIntFoldSlotKind
	constant  int64
	decrement int64
	expr      fixedRecursiveIntFoldExpr
}

func qualifiesForFixedRecursiveIntFold(proto *vm.FuncProto) bool {
	_, ok := analyzeFixedRecursiveIntFold(proto)
	return ok
}

func analyzeFixedRecursiveIntFold(proto *vm.FuncProto) (*fixedRecursiveIntFoldProtocol, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 1 || proto.Name == "" {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) < 8 {
		return nil, false
	}

	threshold, recursePC, ok := fixedIntParseIdentityBaseHeader(proto)
	if !ok {
		return nil, false
	}
	expr, ok := fixedIntParseRecursiveExpr(proto, recursePC)
	if !ok || !expr.valid || len(expr.calls) == 0 {
		return nil, false
	}

	decrements := make([]int64, 0, len(expr.calls))
	for decrement := range expr.calls {
		if decrement <= 0 {
			return nil, false
		}
		decrements = append(decrements, decrement)
	}
	sort.Slice(decrements, func(i, j int) bool { return decrements[i] < decrements[j] })

	terms := make([]fixedRecursiveIntFoldTerm, 0, len(decrements))
	for _, decrement := range decrements {
		terms = append(terms, fixedRecursiveIntFoldTerm{
			decrement: decrement,
			count:     expr.calls[decrement],
		})
	}
	return &fixedRecursiveIntFoldProtocol{
		threshold: threshold,
		bias:      expr.constant,
		terms:     terms,
	}, true
}

func fixedIntParseIdentityBaseHeader(proto *vm.FuncProto) (threshold int64, recursePC int, ok bool) {
	code := proto.Code
	if len(code) < 5 || vm.DecodeOp(code[0]) != vm.OP_LOADINT {
		return 0, 0, false
	}
	thresholdSlot := vm.DecodeA(code[0])
	threshold = int64(vm.DecodesBx(code[0]))

	if vm.DecodeOp(code[1]) != vm.OP_LT || vm.DecodeA(code[1]) != 0 ||
		vm.DecodeB(code[1]) != 0 || vm.DecodeC(code[1]) != thresholdSlot {
		return 0, 0, false
	}
	if vm.DecodeOp(code[2]) != vm.OP_JMP {
		return 0, 0, false
	}
	recursePC = 3 + vm.DecodesBx(code[2])
	if recursePC <= 4 || recursePC >= len(code) {
		return 0, 0, false
	}

	switch vm.DecodeOp(code[3]) {
	case vm.OP_MOVE:
		baseSlot := vm.DecodeA(code[3])
		if vm.DecodeB(code[3]) != 0 || vm.DecodeOp(code[4]) != vm.OP_RETURN ||
			vm.DecodeA(code[4]) != baseSlot || vm.DecodeB(code[4]) != 2 {
			return 0, 0, false
		}
	case vm.OP_RETURN:
		if vm.DecodeA(code[3]) != 0 || vm.DecodeB(code[3]) != 2 {
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	return threshold, recursePC, true
}

func fixedIntParseRecursiveExpr(proto *vm.FuncProto, startPC int) (fixedRecursiveIntFoldExpr, bool) {
	slots := make([]fixedRecursiveIntFoldSlot, maxTrackedSlots)
	slots[0] = fixedRecursiveIntFoldSlot{kind: fixedIntSlotParam}
	for pc := startPC; pc < len(proto.Code); pc++ {
		inst := proto.Code[pc]
		a, b, c := vm.DecodeA(inst), vm.DecodeB(inst), vm.DecodeC(inst)
		switch vm.DecodeOp(inst) {
		case vm.OP_LOADINT:
			if !fixedFoldSlotOK(a) {
				return fixedRecursiveIntFoldExpr{}, false
			}
			v := int64(vm.DecodesBx(inst))
			slots[a] = fixedRecursiveIntFoldSlot{
				kind:     fixedIntSlotConst,
				constant: v,
				expr:     fixedIntExprConst(v),
			}
		case vm.OP_MOVE:
			if !fixedFoldSlotOK(a) || !fixedFoldSlotOK(b) {
				return fixedRecursiveIntFoldExpr{}, false
			}
			slots[a] = slots[b]
		case vm.OP_GETGLOBAL:
			if !fixedFoldSlotOK(a) || protoConstString(proto, vm.DecodeBx(inst)) != proto.Name {
				return fixedRecursiveIntFoldExpr{}, false
			}
			slots[a] = fixedRecursiveIntFoldSlot{kind: fixedIntSlotSelfFunc}
		case vm.OP_SUB:
			if !fixedFoldSlotOK(a) || !fixedFoldSlotOK(b) || !fixedFoldSlotOK(c) {
				return fixedRecursiveIntFoldExpr{}, false
			}
			if slots[b].kind != fixedIntSlotParam || slots[c].kind != fixedIntSlotConst {
				return fixedRecursiveIntFoldExpr{}, false
			}
			slots[a] = fixedRecursiveIntFoldSlot{
				kind:      fixedIntSlotArg,
				decrement: slots[c].constant,
			}
		case vm.OP_CALL:
			if !fixedFoldSlotOK(a) || !slots[a].isFixedIntSelfFunc() || b != 2 || c != 2 || !fixedFoldSlotOK(a+1) {
				return fixedRecursiveIntFoldExpr{}, false
			}
			arg := slots[a+1]
			if arg.kind != fixedIntSlotArg || arg.decrement <= 0 {
				return fixedRecursiveIntFoldExpr{}, false
			}
			slots[a] = fixedRecursiveIntFoldSlot{
				kind: fixedIntSlotExpr,
				expr: fixedIntExprCall(arg.decrement),
			}
		case vm.OP_ADD:
			if !fixedFoldSlotOK(a) || !fixedFoldSlotOK(b) || !fixedFoldSlotOK(c) {
				return fixedRecursiveIntFoldExpr{}, false
			}
			expr, ok := fixedIntExprAdd(slots[b].expr, slots[c].expr)
			if !ok {
				return fixedRecursiveIntFoldExpr{}, false
			}
			slots[a] = fixedRecursiveIntFoldSlot{kind: fixedIntSlotExpr, expr: expr}
		case vm.OP_RETURN:
			if !fixedFoldSlotOK(a) || b != 2 {
				return fixedRecursiveIntFoldExpr{}, false
			}
			return slots[a].expr, true
		default:
			return fixedRecursiveIntFoldExpr{}, false
		}
	}
	return fixedRecursiveIntFoldExpr{}, false
}

func (s fixedRecursiveIntFoldSlot) isFixedIntSelfFunc() bool {
	return s.kind == fixedIntSlotSelfFunc
}

func fixedIntExprConst(v int64) fixedRecursiveIntFoldExpr {
	return fixedRecursiveIntFoldExpr{constant: v, calls: make(map[int64]int), valid: true}
}

func fixedIntExprCall(decrement int64) fixedRecursiveIntFoldExpr {
	return fixedRecursiveIntFoldExpr{calls: map[int64]int{decrement: 1}, valid: true}
}

func fixedIntExprAdd(a, b fixedRecursiveIntFoldExpr) (fixedRecursiveIntFoldExpr, bool) {
	if !a.valid || !b.valid {
		return fixedRecursiveIntFoldExpr{}, false
	}
	constant, ok := fixedFoldCheckedAdd(a.constant, b.constant)
	if !ok {
		return fixedRecursiveIntFoldExpr{}, false
	}
	out := fixedRecursiveIntFoldExpr{
		constant: constant,
		calls:    make(map[int64]int, len(a.calls)+len(b.calls)),
		valid:    true,
	}
	for decrement, count := range a.calls {
		out.calls[decrement] += count
	}
	for decrement, count := range b.calls {
		out.calls[decrement] += count
	}
	return out, true
}

func newFixedRecursiveIntFoldCompiled(proto *vm.FuncProto) (*CompiledFunction, bool) {
	protocol, ok := analyzeFixedRecursiveIntFold(proto)
	if !ok {
		return nil, false
	}
	return &CompiledFunction{
		Proto:                 proto,
		numRegs:               proto.MaxStack,
		FixedRecursiveIntFold: protocol,
	}, true
}

func (tm *TieringManager) compileFixedRecursiveIntFoldTier2(proto *vm.FuncProto) (*CompiledFunction, bool) {
	cf, ok := newFixedRecursiveIntFoldCompiled(proto)
	if !ok {
		return nil, false
	}
	tm.tier2Attempts++
	attempt := tm.tier2Attempts
	tm.traceEvent("tier2_attempt", "tier2", proto, map[string]any{
		"attempt":    attempt,
		"call_count": proto.CallCount,
		"protocol":   "fixed_recursive_int_fold",
	})
	tm.traceTier2Success(proto, cf, attempt)
	return cf, true
}

func (tm *TieringManager) executeFixedRecursiveIntFold(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	if cf == nil || cf.FixedRecursiveIntFold == nil || proto == nil {
		return nil, fmt.Errorf("tier2: missing fixed recursive int fold protocol")
	}
	if base < 0 || base >= len(regs) {
		return nil, fmt.Errorf("tier2: fixed recursive int fold base %d outside regs len %d", base, len(regs))
	}
	if !tm.fixedRecursiveSelfGlobalMatches(proto) {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive int fold self global changed")
		return nil, fmt.Errorf("tier2: fixed recursive int fold self global changed")
	}
	proto.EnteredTier2 = 1
	n, ok := cf.FixedRecursiveIntFold.fold(regs[base])
	if !ok {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive int fold fallback")
		return nil, fmt.Errorf("tier2: fixed recursive int fold fallback")
	}
	result := runtime.IntValue(n)
	regs[base] = result
	return runtime.ReuseValueSlice1(retBuf, result), nil
}

func (p *fixedRecursiveIntFoldProtocol) fold(v runtime.Value) (int64, bool) {
	if p == nil || !v.IsInt() {
		return 0, false
	}
	n := v.Int()
	if n < p.threshold {
		return n, true
	}
	iterations := n - p.threshold + 1
	if iterations < 0 || iterations > maxFixedRecursiveIntFoldIterations {
		return 0, false
	}
	values := make([]int64, int(iterations))
	for k := p.threshold; k <= n; k++ {
		total := p.bias
		for _, term := range p.terms {
			child := k - term.decrement
			childValue := child
			if child >= p.threshold {
				childValue = values[child-p.threshold]
			}
			for i := 0; i < term.count; i++ {
				var ok bool
				total, ok = fixedFoldCheckedAdd(total, childValue)
				if !ok {
					return 0, false
				}
			}
		}
		values[k-p.threshold] = total
	}
	return values[len(values)-1], true
}
