//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Keep the native whole-call builder bounded by a practical allocation limit.
// depth=20 is already roughly two million nodes; deeper inputs fall back to the
// interpreter so unusual programs keep normal VM semantics instead of letting a
// specialized protocol monopolize the process.
const fixedRecursiveTableBuilderMaxDepth = 20

type fixedRecursiveTableBuilderProtocol struct {
	ctor runtime.SmallTableCtor2
}

func qualifiesForFixedRecursiveTableBuilder(proto *vm.FuncProto) bool {
	_, ok := analyzeFixedRecursiveTableBuilder(proto)
	return ok
}

func analyzeFixedRecursiveTableBuilder(proto *vm.FuncProto) (*fixedRecursiveTableBuilderProtocol, bool) {
	if proto == nil || proto.IsVarArg || proto.NumParams != 1 || proto.Name == "" {
		return nil, false
	}
	if len(proto.Upvalues) != 0 || len(proto.Protos) != 0 || len(proto.Code) != 15 {
		return nil, false
	}
	code := proto.Code
	if vm.DecodeOp(code[0]) != vm.OP_LOADINT || vm.DecodeA(code[0]) != 1 || vm.DecodesBx(code[0]) != 0 {
		return nil, false
	}
	if vm.DecodeOp(code[1]) != vm.OP_EQ || vm.DecodeA(code[1]) != 0 ||
		!((vm.DecodeB(code[1]) == 0 && vm.DecodeC(code[1]) == 1) ||
			(vm.DecodeB(code[1]) == 1 && vm.DecodeC(code[1]) == 0)) {
		return nil, false
	}
	if vm.DecodeOp(code[2]) != vm.OP_JMP || 3+vm.DecodesBx(code[2]) != 5 {
		return nil, false
	}
	if vm.DecodeOp(code[3]) != vm.OP_NEWTABLE ||
		vm.DecodeA(code[3]) != 1 || vm.DecodeB(code[3]) != 0 || vm.DecodeC(code[3]) != 0 {
		return nil, false
	}
	if vm.DecodeOp(code[4]) != vm.OP_RETURN || vm.DecodeA(code[4]) != 1 || vm.DecodeB(code[4]) != 2 {
		return nil, false
	}
	if !fixedBuilderSelfCall(proto, code[5], code[6], code[7], code[8], 2, 3) {
		return nil, false
	}
	if !fixedBuilderSelfCall(proto, code[9], code[10], code[11], code[12], 3, 4) {
		return nil, false
	}
	if vm.DecodeOp(code[13]) != vm.OP_NEWOBJECT2 || vm.DecodeA(code[13]) != 1 ||
		vm.DecodeC(code[13]) != 2 {
		return nil, false
	}
	ctorIdx := vm.DecodeB(code[13])
	if ctorIdx < 0 || ctorIdx >= len(proto.TableCtors2) {
		return nil, false
	}
	if vm.DecodeOp(code[14]) != vm.OP_RETURN || vm.DecodeA(code[14]) != 1 || vm.DecodeB(code[14]) != 2 {
		return nil, false
	}
	ctor := proto.TableCtors2[ctorIdx].Runtime
	if !cacheableSmallCtor2(&ctor) {
		return nil, false
	}
	return &fixedRecursiveTableBuilderProtocol{ctor: ctor}, true
}

func fixedBuilderSelfCall(proto *vm.FuncProto, get, one, sub, call uint32, fnSlot, argSlot int) bool {
	if vm.DecodeOp(get) != vm.OP_GETGLOBAL || vm.DecodeA(get) != fnSlot {
		return false
	}
	if protoConstString(proto, vm.DecodeBx(get)) != proto.Name {
		return false
	}
	if vm.DecodeOp(one) != vm.OP_LOADINT || vm.DecodeA(one) != argSlot+1 || vm.DecodesBx(one) != 1 {
		return false
	}
	if vm.DecodeOp(sub) != vm.OP_SUB || vm.DecodeA(sub) != argSlot ||
		vm.DecodeB(sub) != 0 || vm.DecodeC(sub) != argSlot+1 {
		return false
	}
	return vm.DecodeOp(call) == vm.OP_CALL &&
		vm.DecodeA(call) == fnSlot &&
		vm.DecodeB(call) == 2 &&
		vm.DecodeC(call) == 2
}

func newFixedRecursiveTableBuilderCompiled(proto *vm.FuncProto) (*CompiledFunction, bool) {
	protocol, ok := analyzeFixedRecursiveTableBuilder(proto)
	if !ok {
		return nil, false
	}
	return &CompiledFunction{
		Proto:                      proto,
		numRegs:                    proto.MaxStack,
		FixedRecursiveTableBuilder: protocol,
	}, true
}

func (tm *TieringManager) compileFixedRecursiveTableBuilderTier2(proto *vm.FuncProto) (*CompiledFunction, bool) {
	cf, ok := newFixedRecursiveTableBuilderCompiled(proto)
	if !ok {
		return nil, false
	}
	tm.tier2Attempts++
	attempt := tm.tier2Attempts
	tm.traceEvent("tier2_attempt", "tier2", proto, map[string]any{
		"attempt":    attempt,
		"call_count": proto.CallCount,
		"protocol":   "fixed_recursive_table_builder",
	})
	tm.traceTier2Success(proto, cf, attempt)
	return cf, true
}

func (tm *TieringManager) executeFixedRecursiveTableBuilder(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	if cf == nil || cf.FixedRecursiveTableBuilder == nil || proto == nil {
		return nil, fmt.Errorf("tier2: missing fixed recursive table builder protocol")
	}
	if base < 0 || base >= len(regs) {
		return nil, fmt.Errorf("tier2: fixed recursive table builder base %d outside regs len %d", base, len(regs))
	}
	if !tm.fixedRecursiveSelfGlobalMatches(proto) {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive table builder self global changed")
		return nil, fmt.Errorf("tier2: fixed recursive table builder self global changed")
	}
	depthValue := regs[base]
	if !depthValue.IsInt() {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive table builder non-int depth")
		return nil, fmt.Errorf("tier2: fixed recursive table builder non-int depth")
	}
	depth := depthValue.Int()
	if depth < 0 || depth > fixedRecursiveTableBuilderMaxDepth {
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive table builder depth outside fast range")
		return nil, fmt.Errorf("tier2: fixed recursive table builder depth outside fast range")
	}
	result := cf.FixedRecursiveTableBuilder.build(depth)
	regs[base] = result
	proto.EnteredTier2 = 1
	return runtime.ReuseValueSlice1(retBuf, result), nil
}

func (p *fixedRecursiveTableBuilderProtocol) build(depth int64) runtime.Value {
	return runtime.FreshTableValue(runtime.NewLazyRecursiveTable(&p.ctor, depth))
}
