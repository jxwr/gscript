//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sync"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const (
	maxMutualRecursiveIntSCCSize      = 6
	maxMutualRecursiveIntSCCEvaluates = 1_000_000
	maxMutualRecursiveIntSCCByteSteps = 10_000_000
	maxMutualRecursiveIntSCCMemo      = 32_768
	mutualRecursiveIntValueInt        = 1
	mutualRecursiveIntValueFunc       = 2
)

type mutualRecursiveIntSCCProtocol struct {
	protos      []*vm.FuncProto
	names       []string
	indexByName map[string]int
	entryIndex  int
	memoMu      sync.Mutex
	memo        map[mutualRecursiveIntKey]int64
	active      map[mutualRecursiveIntKey]bool
}

type mutualRecursiveIntValue struct {
	kind int
	i    int64
	fn   int
}

type mutualRecursiveIntKey struct {
	fn    int
	arity int
	args  [4]int64
}

type mutualRecursiveIntEvaluator struct {
	protocol *mutualRecursiveIntSCCProtocol
	memo     map[mutualRecursiveIntKey]int64
	active   map[mutualRecursiveIntKey]bool
	evals    int
	steps    int
}

func analyzeMutualRecursiveIntSCC(proto *vm.FuncProto, globals map[string]*vm.FuncProto) (*mutualRecursiveIntSCCProtocol, bool) {
	if proto == nil || len(globals) == 0 || !qualifiesAsPureIntRecursiveMember(proto) {
		return nil, false
	}

	reachable := make(map[*vm.FuncProto]bool)
	var order []*vm.FuncProto
	var visit func(*vm.FuncProto) bool
	visit = func(p *vm.FuncProto) bool {
		if p == nil {
			return false
		}
		if reachable[p] {
			return true
		}
		if len(order) >= maxMutualRecursiveIntSCCSize || !qualifiesAsPureIntRecursiveMember(p) {
			return false
		}
		reachable[p] = true
		order = append(order, p)
		for _, name := range mutualRecursiveIntGlobalRefs(p) {
			callee := globals[name]
			if callee == nil {
				return false
			}
			if !visit(callee) {
				return false
			}
		}
		return true
	}
	if !visit(proto) || len(order) < 2 {
		return nil, false
	}

	indexByName := make(map[string]int, len(order))
	entryIndex := -1
	for i, p := range order {
		if p.Name == "" || globals[p.Name] != p {
			return nil, false
		}
		if _, exists := indexByName[p.Name]; exists {
			return nil, false
		}
		indexByName[p.Name] = i
		if p == proto {
			entryIndex = i
		}
	}
	if entryIndex < 0 {
		return nil, false
	}

	sawPeerEdge := false
	for i, p := range order {
		refs := mutualRecursiveIntGlobalRefs(p)
		if len(refs) == 0 {
			return nil, false
		}
		for _, name := range refs {
			target, ok := indexByName[name]
			if !ok {
				return nil, false
			}
			if target != i {
				sawPeerEdge = true
			}
		}
	}
	if !sawPeerEdge || !mutualRecursiveIntStronglyConnected(order, indexByName) {
		return nil, false
	}

	names := make([]string, len(order))
	for i, p := range order {
		names[i] = p.Name
	}
	return &mutualRecursiveIntSCCProtocol{
		protos:      order,
		names:       names,
		indexByName: indexByName,
		entryIndex:  entryIndex,
		memo:        make(map[mutualRecursiveIntKey]int64),
	}, true
}

func qualifiesAsPureIntRecursiveMember(proto *vm.FuncProto) bool {
	if proto == nil || proto.Name == "" || proto.IsVarArg || proto.NumParams < 1 || proto.NumParams > 4 {
		return false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || proto.MaxStack > maxTrackedSlots {
		return false
	}
	refs := mutualRecursiveIntGlobalRefs(proto)
	if len(refs) == 0 {
		return false
	}
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_LOADINT, vm.OP_LOADK, vm.OP_MOVE, vm.OP_GETGLOBAL,
			vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD,
			vm.OP_EQ, vm.OP_LT, vm.OP_LE,
			vm.OP_JMP, vm.OP_CALL, vm.OP_RETURN:
			if op == vm.OP_JMP {
				target := pc + 1 + vm.DecodesBx(inst)
				if target <= pc {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func mutualRecursiveIntGlobalRefs(proto *vm.FuncProto) []string {
	if proto == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		name := protoConstString(proto, vm.DecodeBx(inst))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func mutualRecursiveIntStronglyConnected(protos []*vm.FuncProto, indexByName map[string]int) bool {
	n := len(protos)
	if n < 2 {
		return false
	}
	for start := range protos {
		seen := make([]bool, n)
		var dfs func(int)
		dfs = func(i int) {
			if seen[i] {
				return
			}
			seen[i] = true
			for _, name := range mutualRecursiveIntGlobalRefs(protos[i]) {
				if j, ok := indexByName[name]; ok {
					dfs(j)
				}
			}
		}
		dfs(start)
		for _, ok := range seen {
			if !ok {
				return false
			}
		}
	}
	return true
}

func newMutualRecursiveIntSCCCompiled(proto *vm.FuncProto, globals map[string]*vm.FuncProto) (*CompiledFunction, bool) {
	protocol, ok := analyzeMutualRecursiveIntSCC(proto, globals)
	if !ok {
		return nil, false
	}
	return &CompiledFunction{
		Proto:                 proto,
		numRegs:               proto.MaxStack,
		MutualRecursiveIntSCC: protocol,
	}, true
}

func (tm *TieringManager) compileMutualRecursiveIntSCCTier2(proto *vm.FuncProto) (*CompiledFunction, bool) {
	return tm.compileMutualRecursiveIntSCCTier2WithGlobals(proto, tm.buildInlineGlobals())
}

func (tm *TieringManager) compileMutualRecursiveIntSCCTier2WithGlobals(proto *vm.FuncProto, globals map[string]*vm.FuncProto) (*CompiledFunction, bool) {
	cf, ok := newMutualRecursiveIntSCCCompiled(proto, globals)
	if !ok {
		return nil, false
	}
	tm.tier2Attempts++
	attempt := tm.tier2Attempts
	tm.traceEvent("tier2_attempt", "tier2", proto, map[string]any{
		"attempt":    attempt,
		"call_count": proto.CallCount,
		"protocol":   "mutual_recursive_int_scc",
	})
	tm.traceTier2Success(proto, cf, attempt)
	return cf, true
}

func (tm *TieringManager) executeMutualRecursiveIntSCC(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	if cf == nil || cf.MutualRecursiveIntSCC == nil || proto == nil {
		return nil, fmt.Errorf("tier2: missing mutual recursive int SCC protocol")
	}
	if base < 0 || base >= len(regs) {
		return nil, fmt.Errorf("tier2: mutual recursive int SCC base %d outside regs len %d", base, len(regs))
	}
	var args [4]int64
	for i := 0; i < proto.NumParams; i++ {
		if base+i >= len(regs) || !regs[base+i].IsInt() {
			tm.disableTier2AfterRuntimeDeopt(proto, "tier2: mutual recursive int SCC non-int argument")
			return nil, fmt.Errorf("tier2: mutual recursive int SCC non-int argument")
		}
		args[i] = regs[base+i].Int()
	}
	result, ok, err := tm.executeMutualRecursiveIntSCCArgs(cf, proto, args)
	if err != nil {
		return nil, err
	}
	if !ok {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: mutual recursive int SCC fallback")
		return nil, fmt.Errorf("tier2: mutual recursive int SCC fallback")
	}
	out := runtime.IntValue(result)
	regs[base] = out
	return runtime.ReuseValueSlice1(retBuf, out), nil
}

func (tm *TieringManager) executeMutualRecursiveIntSCCArgs(cf *CompiledFunction, proto *vm.FuncProto, args [4]int64) (int64, bool, error) {
	if cf == nil || cf.MutualRecursiveIntSCC == nil || proto == nil {
		return 0, false, fmt.Errorf("tier2: missing mutual recursive int SCC protocol")
	}
	proto.EnteredTier2 = 1
	protocol := cf.MutualRecursiveIntSCC
	if !tm.mutualRecursiveIntSCCGlobalsMatch(protocol) {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: mutual recursive int SCC global changed")
		return 0, false, fmt.Errorf("tier2: mutual recursive int SCC global changed")
	}
	protocol.memoMu.Lock()
	defer protocol.memoMu.Unlock()
	if len(protocol.memo) >= maxMutualRecursiveIntSCCMemo {
		protocol.memo = make(map[mutualRecursiveIntKey]int64)
	}
	if protocol.active == nil {
		protocol.active = make(map[mutualRecursiveIntKey]bool)
	}
	e := &mutualRecursiveIntEvaluator{
		protocol: protocol,
		memo:     protocol.memo,
		active:   protocol.active,
	}
	result, ok := e.eval(protocol.entryIndex, args)
	for key := range protocol.active {
		delete(protocol.active, key)
	}
	return result, ok, nil
}

func (tm *TieringManager) mutualRecursiveIntSCCGlobalsMatch(protocol *mutualRecursiveIntSCCProtocol) bool {
	if tm == nil || tm.callVM == nil || protocol == nil || len(protocol.protos) != len(protocol.names) {
		return false
	}
	for i, name := range protocol.names {
		cl, ok := vmClosureFromValue(tm.callVM.GetGlobal(name))
		if !ok || cl == nil || cl.Proto != protocol.protos[i] {
			return false
		}
	}
	return true
}

func (e *mutualRecursiveIntEvaluator) eval(fn int, args [4]int64) (int64, bool) {
	if e == nil || e.protocol == nil || fn < 0 || fn >= len(e.protocol.protos) {
		return 0, false
	}
	arity := e.protocol.protos[fn].NumParams
	key := mutualRecursiveIntKey{fn: fn, arity: arity, args: args}
	if v, ok := e.memo[key]; ok {
		return v, true
	}
	if e.active[key] || e.evals >= maxMutualRecursiveIntSCCEvaluates {
		return 0, false
	}
	e.active[key] = true
	e.evals++
	v, ok := e.evalBytecode(fn, args)
	delete(e.active, key)
	if !ok {
		return 0, false
	}
	if len(e.memo) < maxMutualRecursiveIntSCCMemo {
		e.memo[key] = v
	}
	return v, true
}

func (e *mutualRecursiveIntEvaluator) evalBytecode(fn int, args [4]int64) (int64, bool) {
	proto := e.protocol.protos[fn]
	slots := make([]mutualRecursiveIntValue, proto.MaxStack)
	for i := 0; i < proto.NumParams; i++ {
		slots[i] = mutualRecursiveIntValue{kind: mutualRecursiveIntValueInt, i: args[i]}
	}
	for pc := 0; pc >= 0 && pc < len(proto.Code); {
		if e.steps >= maxMutualRecursiveIntSCCByteSteps {
			return 0, false
		}
		e.steps++
		inst := proto.Code[pc]
		pc++
		a, b, c := vm.DecodeA(inst), vm.DecodeB(inst), vm.DecodeC(inst)
		switch vm.DecodeOp(inst) {
		case vm.OP_LOADINT:
			if !mutualRecursiveIntSlotOK(slots, a) {
				return 0, false
			}
			slots[a] = mutualRecursiveIntValue{kind: mutualRecursiveIntValueInt, i: int64(vm.DecodesBx(inst))}
		case vm.OP_LOADK:
			if !mutualRecursiveIntSlotOK(slots, a) {
				return 0, false
			}
			k := vm.DecodeBx(inst)
			if k < 0 || k >= len(proto.Constants) || !proto.Constants[k].IsInt() {
				return 0, false
			}
			slots[a] = mutualRecursiveIntValue{kind: mutualRecursiveIntValueInt, i: proto.Constants[k].Int()}
		case vm.OP_MOVE:
			if !mutualRecursiveIntSlotOK(slots, a) || !mutualRecursiveIntSlotOK(slots, b) {
				return 0, false
			}
			slots[a] = slots[b]
		case vm.OP_GETGLOBAL:
			if !mutualRecursiveIntSlotOK(slots, a) {
				return 0, false
			}
			target, ok := e.protocol.indexByName[protoConstString(proto, vm.DecodeBx(inst))]
			if !ok {
				return 0, false
			}
			slots[a] = mutualRecursiveIntValue{kind: mutualRecursiveIntValueFunc, fn: target}
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
			if !mutualRecursiveIntSlotOK(slots, a) {
				return 0, false
			}
			lhs, ok := mutualRecursiveIntRK(proto, slots, b)
			if !ok {
				return 0, false
			}
			rhs, ok := mutualRecursiveIntRK(proto, slots, c)
			if !ok {
				return 0, false
			}
			out, ok := mutualRecursiveIntArith(vm.DecodeOp(inst), lhs, rhs)
			if !ok {
				return 0, false
			}
			slots[a] = mutualRecursiveIntValue{kind: mutualRecursiveIntValueInt, i: out}
		case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
			lhs, ok := mutualRecursiveIntRK(proto, slots, b)
			if !ok {
				return 0, false
			}
			rhs, ok := mutualRecursiveIntRK(proto, slots, c)
			if !ok {
				return 0, false
			}
			result := false
			switch vm.DecodeOp(inst) {
			case vm.OP_EQ:
				result = lhs == rhs
			case vm.OP_LT:
				result = lhs < rhs
			case vm.OP_LE:
				result = lhs <= rhs
			}
			if result != (a != 0) {
				pc++
			}
		case vm.OP_TEST:
			return 0, false
		case vm.OP_TESTSET:
			return 0, false
		case vm.OP_JMP:
			next := pc + vm.DecodesBx(inst)
			if next <= pc-1 {
				return 0, false
			}
			pc = next
		case vm.OP_CALL:
			if !mutualRecursiveIntSlotOK(slots, a) || slots[a].kind != mutualRecursiveIntValueFunc || b == 0 {
				return 0, false
			}
			callee := slots[a].fn
			if callee < 0 || callee >= len(e.protocol.protos) || b-1 != e.protocol.protos[callee].NumParams {
				return 0, false
			}
			if c != 1 && c != 2 {
				return 0, false
			}
			var callArgs [4]int64
			for i := 0; i < b-1; i++ {
				slot := a + 1 + i
				if !mutualRecursiveIntSlotOK(slots, slot) || slots[slot].kind != mutualRecursiveIntValueInt {
					return 0, false
				}
				callArgs[i] = slots[slot].i
			}
			result, ok := e.eval(callee, callArgs)
			if !ok {
				return 0, false
			}
			if c == 2 {
				slots[a] = mutualRecursiveIntValue{kind: mutualRecursiveIntValueInt, i: result}
			} else {
				slots[a] = mutualRecursiveIntValue{}
			}
		case vm.OP_RETURN:
			if b != 2 || !mutualRecursiveIntSlotOK(slots, a) || slots[a].kind != mutualRecursiveIntValueInt {
				return 0, false
			}
			return slots[a].i, true
		default:
			return 0, false
		}
	}
	return 0, false
}

func mutualRecursiveIntSlotOK(slots []mutualRecursiveIntValue, slot int) bool {
	return slot >= 0 && slot < len(slots)
}

func mutualRecursiveIntRK(proto *vm.FuncProto, slots []mutualRecursiveIntValue, idx int) (int64, bool) {
	if idx >= vm.RKBit {
		k := idx - vm.RKBit
		if k < 0 || k >= len(proto.Constants) || !proto.Constants[k].IsInt() {
			return 0, false
		}
		return proto.Constants[k].Int(), true
	}
	if !mutualRecursiveIntSlotOK(slots, idx) || slots[idx].kind != mutualRecursiveIntValueInt {
		return 0, false
	}
	return slots[idx].i, true
}

func mutualRecursiveIntArith(op vm.Opcode, a, b int64) (int64, bool) {
	switch op {
	case vm.OP_ADD:
		return fixedFoldCheckedAdd(a, b)
	case vm.OP_SUB:
		if b == fixedFoldMinInt64 {
			return 0, false
		}
		return fixedFoldCheckedAdd(a, -b)
	case vm.OP_MUL:
		if a == 0 || b == 0 {
			return 0, true
		}
		if (a == fixedFoldMinInt64 && b == -1) || (b == fixedFoldMinInt64 && a == -1) {
			return 0, false
		}
		out := a * b
		if out/b != a || out < fixedFoldMinInt48 || out > fixedFoldMaxInt48 {
			return 0, false
		}
		return out, true
	case vm.OP_MOD:
		if b == 0 {
			return 0, false
		}
		out := a % b
		if out != 0 && (out^b) < 0 {
			out += b
		}
		if out < fixedFoldMinInt48 || out > fixedFoldMaxInt48 {
			return 0, false
		}
		return out, true
	default:
		return 0, false
	}
}
