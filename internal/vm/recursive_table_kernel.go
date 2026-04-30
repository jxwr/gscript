package vm

import (
	"github.com/gscript/gscript/internal/runtime"
)

const (
	recursiveTableBuilderMaxDepth = 20
	recursiveTableMaxTrackedSlots = 64

	recursiveTableMaxInt64 = int64(^uint64(0) >> 1)
	recursiveTableMinInt64 = -recursiveTableMaxInt64 - 1
	recursiveTableMaxInt48 = (1 << 47) - 1
	recursiveTableMinInt48 = -(1 << 47)
)

type recursiveTableKernelCache struct {
	analyzed bool
	builder  *recursiveTableBuilderKernel
	fold     *recursiveTableFoldKernel
}

type recursiveTableBuilderKernel struct {
	selfName string
	ctor     runtime.SmallTableCtor2
}

type recursiveTableFoldKernel struct {
	selfName    string
	nilField    string
	nilCache    runtime.FieldCacheEntry
	baseValue   int64
	combineBias int64
	children    []recursiveTableFoldChild
}

type recursiveTableFoldChild struct {
	field string
	cache runtime.FieldCacheEntry
}

type recursiveTableFoldExpr struct {
	constant int64
	calls    map[string]int
	valid    bool
}

type recursiveTableFoldSlot struct {
	selfName string
	field    string
	expr     recursiveTableFoldExpr
}

func IsFixedRecursiveTableBuilderKernelProto(proto *FuncProto) bool {
	_, ok := analyzeRecursiveTableBuilderKernel(proto)
	return ok
}

func IsFixedRecursiveTableFoldKernelProto(proto *FuncProto) bool {
	_, ok := analyzeRecursiveTableFoldKernel(proto)
	return ok
}

func (vm *VM) tryRunRecursiveTableValueKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 {
		return false, nil, nil
	}
	proto := cl.Proto
	if vm.methodJIT == nil || !proto.Tier2Promoted {
		return false, nil, nil
	}
	cache := recursiveTableKernelForProto(proto)
	if !cache.analyzed {
		return false, nil, nil
	}
	if cache.builder != nil {
		if !vm.recursiveTableSelfGlobalMatches(cl, cache.builder.selfName) || !args[0].IsInt() {
			return false, nil, nil
		}
		depth := args[0].Int()
		if depth < 0 || depth > recursiveTableBuilderMaxDepth {
			return false, nil, nil
		}
		result := runtime.FreshTableValue(runtime.NewLazyRecursiveTable(&cache.builder.ctor, depth))
		proto.EnteredTier2 = 1
		return true, runtime.ReuseValueSlice1(nil, result), nil
	}
	if cache.fold != nil {
		if !vm.recursiveTableSelfGlobalMatches(cl, cache.fold.selfName) {
			return false, nil, nil
		}
		n, ok := cache.fold.fold(args[0])
		if !ok {
			return false, nil, nil
		}
		result := runtime.IntValue(n)
		proto.EnteredTier2 = 1
		return true, runtime.ReuseValueSlice1(nil, result), nil
	}
	return false, nil, nil
}

func (vm *VM) tryRecursiveTableBuildFoldRegion(frame *CallFrame, base int, builderCl *Closure, builderA int, nArgs int, builderC int) (bool, error) {
	if frame == nil || frame.closure == nil || builderCl == nil || builderCl.Proto == nil || nArgs != 1 || builderC != 2 {
		return false, nil
	}
	builderProto := builderCl.Proto
	if vm.methodJIT == nil || !builderProto.Tier2Promoted {
		return false, nil
	}
	builderCache := recursiveTableKernelForProto(builderProto)
	if builderCache.builder == nil || !vm.recursiveTableSelfGlobalMatches(builderCl, builderCache.builder.selfName) {
		return false, nil
	}

	callPC := frame.pc - 1
	code := frame.closure.Proto.Code
	if callPC < 0 || callPC+2 >= len(code) {
		return false, nil
	}
	move := code[callPC+1]
	foldCall := code[callPC+2]
	if DecodeOp(move) != OP_MOVE || DecodeB(move) != builderA || DecodeOp(foldCall) != OP_CALL {
		return false, nil
	}
	foldA := DecodeA(foldCall)
	if DecodeA(move) != foldA+1 || DecodeB(foldCall) != 2 {
		return false, nil
	}
	foldCl, ok := closureFromValue(vm.regs[base+foldA])
	if !ok || foldCl == nil || foldCl.Proto == nil {
		return false, nil
	}
	foldProto := foldCl.Proto
	if !foldProto.Tier2Promoted {
		return false, nil
	}
	foldCache := recursiveTableKernelForProto(foldProto)
	if foldCache.fold == nil {
		return false, nil
	}
	if !vm.recursiveTableSelfGlobalMatches(foldCl, foldCache.fold.selfName) {
		return false, nil
	}

	depthValue := vm.regs[base+builderA+1]
	if !depthValue.IsInt() {
		return false, nil
	}
	depth := depthValue.Int()
	if depth < 0 || depth > recursiveTableBuilderMaxDepth {
		return false, nil
	}
	total, ok := foldCache.fold.foldLazyShape(depth, builderCache.builder.ctor.Key1, builderCache.builder.ctor.Key2)
	if !ok {
		return false, nil
	}
	result := runtime.IntValue(total)
	builderProto.EnteredTier2 = 1
	foldProto.EnteredTier2 = 1
	vm.retBuf[0] = result
	vm.writeCallResults(base+foldA, DecodeC(foldCall), vm.retBuf[:1])
	frame.pc = callPC + 3
	return true, nil
}

func recursiveTableKernelForProto(proto *FuncProto) *recursiveTableKernelCache {
	cache := proto.RecursiveTableKernel
	if cache == nil {
		cache = analyzeRecursiveTableKernel(proto)
		proto.RecursiveTableKernel = cache
	}
	return cache
}

func analyzeRecursiveTableKernel(proto *FuncProto) *recursiveTableKernelCache {
	cache := &recursiveTableKernelCache{analyzed: true}
	if builder, ok := analyzeRecursiveTableBuilderKernel(proto); ok {
		cache.builder = builder
		return cache
	}
	if fold, ok := analyzeRecursiveTableFoldKernel(proto); ok {
		cache.fold = fold
	}
	return cache
}

func (vm *VM) recursiveTableSelfGlobalMatches(cl *Closure, selfName string) bool {
	if vm == nil || cl == nil || selfName == "" {
		return false
	}
	current, ok := closureFromValue(vm.GetGlobal(selfName))
	return ok && current == cl
}

func analyzeRecursiveTableBuilderKernel(proto *FuncProto) (*recursiveTableBuilderKernel, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 1 {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) != 15 {
		return nil, false
	}
	code := proto.Code
	if DecodeOp(code[0]) != OP_LOADINT || DecodeA(code[0]) != 1 || DecodesBx(code[0]) != 0 {
		return nil, false
	}
	if DecodeOp(code[1]) != OP_EQ || DecodeA(code[1]) != 0 ||
		!((DecodeB(code[1]) == 0 && DecodeC(code[1]) == 1) ||
			(DecodeB(code[1]) == 1 && DecodeC(code[1]) == 0)) {
		return nil, false
	}
	if DecodeOp(code[2]) != OP_JMP || 3+DecodesBx(code[2]) != 5 {
		return nil, false
	}
	if DecodeOp(code[3]) != OP_NEWTABLE ||
		DecodeA(code[3]) != 1 || DecodeB(code[3]) != 0 || DecodeC(code[3]) != 0 {
		return nil, false
	}
	if DecodeOp(code[4]) != OP_RETURN || DecodeA(code[4]) != 1 || DecodeB(code[4]) != 2 {
		return nil, false
	}
	leftName, ok := recursiveTableBuilderSelfCallName(proto, code[5], code[6], code[7], code[8], 2, 3)
	if !ok {
		return nil, false
	}
	rightName, ok := recursiveTableBuilderSelfCallName(proto, code[9], code[10], code[11], code[12], 3, 4)
	if !ok || rightName != leftName {
		return nil, false
	}
	if DecodeOp(code[13]) != OP_NEWOBJECT2 || DecodeA(code[13]) != 1 || DecodeC(code[13]) != 2 {
		return nil, false
	}
	ctorIdx := DecodeB(code[13])
	if ctorIdx < 0 || ctorIdx >= len(proto.TableCtors2) {
		return nil, false
	}
	if DecodeOp(code[14]) != OP_RETURN || DecodeA(code[14]) != 1 || DecodeB(code[14]) != 2 {
		return nil, false
	}
	ctor := proto.TableCtors2[ctorIdx].Runtime
	if !recursiveTableCacheableCtor2(&ctor) {
		return nil, false
	}
	return &recursiveTableBuilderKernel{selfName: leftName, ctor: ctor}, true
}

func recursiveTableBuilderSelfCallName(proto *FuncProto, get, one, sub, call uint32, fnSlot, argSlot int) (string, bool) {
	if DecodeOp(get) != OP_GETGLOBAL || DecodeA(get) != fnSlot {
		return "", false
	}
	selfName := recursiveTableProtoConstString(proto, DecodeBx(get))
	if selfName == "" {
		return "", false
	}
	if DecodeOp(one) != OP_LOADINT || DecodeA(one) != argSlot+1 || DecodesBx(one) != 1 {
		return "", false
	}
	if DecodeOp(sub) != OP_SUB || DecodeA(sub) != argSlot ||
		DecodeB(sub) != 0 || DecodeC(sub) != argSlot+1 {
		return "", false
	}
	ok := DecodeOp(call) == OP_CALL &&
		DecodeA(call) == fnSlot &&
		DecodeB(call) == 2 &&
		DecodeC(call) == 2
	return selfName, ok
}

func recursiveTableCacheableCtor2(ctor *runtime.SmallTableCtor2) bool {
	return ctor != nil && ctor.Key1 != ctor.Key2 && ctor.Shape != nil
}

func analyzeRecursiveTableFoldKernel(proto *FuncProto) (*recursiveTableFoldKernel, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 1 {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) < 8 {
		return nil, false
	}

	nilField, basePC, recursePC, ok := recursiveTableFoldParseNilBaseHeader(proto)
	if !ok {
		return nil, false
	}
	baseValue, ok := recursiveTableFoldParseIntReturn(proto, basePC)
	if !ok {
		return nil, false
	}
	expr, children, selfName, ok := recursiveTableFoldParseRecursiveExpr(proto, recursePC)
	if !ok || selfName == "" || !expr.valid || len(expr.calls) == 0 {
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

	protocolChildren := make([]recursiveTableFoldChild, len(children))
	for i, child := range children {
		protocolChildren[i] = recursiveTableFoldChild{field: child.field}
	}
	return &recursiveTableFoldKernel{
		selfName:    selfName,
		nilField:    nilField,
		baseValue:   baseValue,
		combineBias: expr.constant,
		children:    protocolChildren,
	}, true
}

func recursiveTableFoldParseNilBaseHeader(proto *FuncProto) (nilField string, basePC, recursePC int, ok bool) {
	code := proto.Code
	if len(code) < 6 || DecodeOp(code[0]) != OP_GETFIELD || DecodeB(code[0]) != 0 {
		return "", 0, 0, false
	}
	nilField = recursiveTableProtoConstString(proto, DecodeC(code[0]))
	if nilField == "" {
		return "", 0, 0, false
	}
	if DecodeOp(code[1]) != OP_LOADNIL {
		return "", 0, 0, false
	}
	fieldSlot := DecodeA(code[0])
	nilStart := DecodeA(code[1])
	nilEnd := nilStart + DecodeB(code[1])
	if DecodeOp(code[2]) != OP_EQ {
		return "", 0, 0, false
	}
	eqB, eqC := DecodeB(code[2]), DecodeC(code[2])
	if !((eqB == fieldSlot && eqC >= nilStart && eqC <= nilEnd) ||
		(eqC == fieldSlot && eqB >= nilStart && eqB <= nilEnd)) {
		return "", 0, 0, false
	}
	if DecodeOp(code[3]) != OP_JMP {
		return "", 0, 0, false
	}
	basePC = 4
	recursePC = 4 + DecodesBx(code[3])
	if recursePC <= basePC || recursePC >= len(code) {
		return "", 0, 0, false
	}
	return nilField, basePC, recursePC, true
}

func recursiveTableFoldParseIntReturn(proto *FuncProto, pc int) (int64, bool) {
	if pc < 0 || pc+1 >= len(proto.Code) {
		return 0, false
	}
	load := proto.Code[pc]
	ret := proto.Code[pc+1]
	if DecodeOp(load) != OP_LOADINT || DecodeOp(ret) != OP_RETURN {
		return 0, false
	}
	if DecodeA(ret) != DecodeA(load) || DecodeB(ret) != 2 {
		return 0, false
	}
	return int64(DecodesBx(load)), true
}

func recursiveTableFoldParseRecursiveExpr(proto *FuncProto, startPC int) (recursiveTableFoldExpr, []recursiveTableFoldChild, string, bool) {
	slots := make([]recursiveTableFoldSlot, recursiveTableMaxTrackedSlots)
	children := make([]recursiveTableFoldChild, 0, 2)
	selfName := ""
	for pc := startPC; pc < len(proto.Code); pc++ {
		inst := proto.Code[pc]
		a, b, c := DecodeA(inst), DecodeB(inst), DecodeC(inst)
		switch DecodeOp(inst) {
		case OP_LOADINT:
			if !recursiveTableFoldSlotOK(a) {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			slots[a] = recursiveTableFoldSlot{expr: recursiveTableFoldConst(int64(DecodesBx(inst)))}
		case OP_MOVE:
			if !recursiveTableFoldSlotOK(a) || !recursiveTableFoldSlotOK(b) {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			slots[a] = slots[b]
		case OP_GETGLOBAL:
			if !recursiveTableFoldSlotOK(a) {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			name := recursiveTableProtoConstString(proto, DecodeBx(inst))
			if name == "" {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			slots[a] = recursiveTableFoldSlot{selfName: name}
		case OP_GETFIELD:
			if !recursiveTableFoldSlotOK(a) || b != 0 {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			field := recursiveTableProtoConstString(proto, c)
			if field == "" {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			slots[a] = recursiveTableFoldSlot{field: field}
		case OP_CALL:
			if !recursiveTableFoldSlotOK(a) || slots[a].selfName == "" || b != 2 || c != 2 || !recursiveTableFoldSlotOK(a+1) {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			if selfName == "" {
				selfName = slots[a].selfName
			} else if selfName != slots[a].selfName {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			field := slots[a+1].field
			if field == "" {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			children = append(children, recursiveTableFoldChild{field: field})
			slots[a] = recursiveTableFoldSlot{expr: recursiveTableFoldCall(field)}
		case OP_ADD:
			if !recursiveTableFoldSlotOK(a) || !recursiveTableFoldSlotOK(b) || !recursiveTableFoldSlotOK(c) {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			expr, ok := recursiveTableFoldAdd(slots[b].expr, slots[c].expr)
			if !ok {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			slots[a] = recursiveTableFoldSlot{expr: expr}
		case OP_RETURN:
			if !recursiveTableFoldSlotOK(a) || b != 2 {
				return recursiveTableFoldExpr{}, nil, "", false
			}
			return slots[a].expr, children, selfName, true
		default:
			return recursiveTableFoldExpr{}, nil, "", false
		}
	}
	return recursiveTableFoldExpr{}, nil, "", false
}

func recursiveTableFoldSlotOK(slot int) bool {
	return slot >= 0 && slot < recursiveTableMaxTrackedSlots
}

func recursiveTableFoldConst(v int64) recursiveTableFoldExpr {
	return recursiveTableFoldExpr{constant: v, calls: make(map[string]int), valid: true}
}

func recursiveTableFoldCall(field string) recursiveTableFoldExpr {
	return recursiveTableFoldExpr{calls: map[string]int{field: 1}, valid: true}
}

func recursiveTableFoldAdd(a, b recursiveTableFoldExpr) (recursiveTableFoldExpr, bool) {
	if !a.valid || !b.valid {
		return recursiveTableFoldExpr{}, false
	}
	constant, ok := recursiveTableCheckedAdd(a.constant, b.constant)
	if !ok {
		return recursiveTableFoldExpr{}, false
	}
	out := recursiveTableFoldExpr{
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

func (p *recursiveTableFoldKernel) fold(v runtime.Value) (int64, bool) {
	t := v.Table()
	if t == nil {
		return 0, false
	}
	if n, ok := p.foldLazy(t); ok {
		return n, true
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
		total, ok = recursiveTableCheckedAdd(total, childTotal)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func (p *recursiveTableFoldKernel) foldLazy(t *runtime.Table) (int64, bool) {
	depth, key1, key2, ok := t.LazyRecursiveTablePureInfo()
	if !ok || depth < 0 || p.nilField != key1 || len(p.children) != 2 {
		return 0, false
	}
	return p.foldLazyShape(depth, key1, key2)
}

func (p *recursiveTableFoldKernel) foldLazyShape(depth int64, key1, key2 string) (int64, bool) {
	if depth < 0 || p.nilField != key1 || len(p.children) != 2 {
		return 0, false
	}
	if p.children[0].field != key1 || p.children[1].field != key2 {
		return 0, false
	}
	total := p.baseValue
	for i := int64(0); i < depth; i++ {
		next := p.combineBias
		var ok bool
		next, ok = recursiveTableCheckedAdd(next, total)
		if !ok {
			return 0, false
		}
		next, ok = recursiveTableCheckedAdd(next, total)
		if !ok {
			return 0, false
		}
		total = next
	}
	return total, true
}

func recursiveTableCheckedAdd(a, b int64) (int64, bool) {
	if (b > 0 && a > recursiveTableMaxInt64-b) || (b < 0 && a < recursiveTableMinInt64-b) {
		return 0, false
	}
	out := a + b
	if out < recursiveTableMinInt48 || out > recursiveTableMaxInt48 {
		return 0, false
	}
	return out, true
}

func recursiveTableProtoConstString(proto *FuncProto, idx int) string {
	if proto == nil || idx < 0 || idx >= len(proto.Constants) {
		return ""
	}
	val := proto.Constants[idx]
	if !val.IsString() {
		return ""
	}
	return val.Str()
}
