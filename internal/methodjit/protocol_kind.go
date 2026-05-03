//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

type compiledProtocolKind uint8

const (
	compiledProtocolNone compiledProtocolKind = iota
	compiledProtocolFixedRecursiveIntFold
	compiledProtocolFixedRecursiveNestedIntFold
	compiledProtocolFixedRecursiveTableBuilder
	compiledProtocolFixedRecursiveTableFold
	compiledProtocolMutualRecursiveIntSCC
)

func (k compiledProtocolKind) String() string {
	switch k {
	case compiledProtocolFixedRecursiveIntFold:
		return "fixed_recursive_int_fold"
	case compiledProtocolFixedRecursiveNestedIntFold:
		return "fixed_recursive_nested_int_fold"
	case compiledProtocolFixedRecursiveTableBuilder:
		return "fixed_recursive_table_builder"
	case compiledProtocolFixedRecursiveTableFold:
		return "fixed_recursive_table_fold"
	case compiledProtocolMutualRecursiveIntSCC:
		return "mutual_recursive_int_scc"
	default:
		return "none"
	}
}

func (cf *CompiledFunction) ProtocolKind() compiledProtocolKind {
	if cf == nil {
		return compiledProtocolNone
	}
	switch {
	case cf.FixedRecursiveIntFold != nil:
		return compiledProtocolFixedRecursiveIntFold
	case cf.FixedRecursiveNestedIntFold != nil:
		return compiledProtocolFixedRecursiveNestedIntFold
	case cf.FixedRecursiveTableBuilder != nil:
		return compiledProtocolFixedRecursiveTableBuilder
	case cf.FixedRecursiveTableFold != nil:
		return compiledProtocolFixedRecursiveTableFold
	case cf.MutualRecursiveIntSCC != nil:
		return compiledProtocolMutualRecursiveIntSCC
	default:
		return compiledProtocolNone
	}
}

func (tm *TieringManager) executeCompiledProtocol(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, bool, error) {
	switch cf.ProtocolKind() {
	case compiledProtocolFixedRecursiveIntFold:
		out, err := tm.executeFixedRecursiveIntFold(cf, regs, base, proto, retBuf)
		return out, true, err
	case compiledProtocolFixedRecursiveNestedIntFold:
		out, err := tm.executeFixedRecursiveNestedIntFold(cf, regs, base, proto, retBuf)
		return out, true, err
	case compiledProtocolFixedRecursiveTableBuilder:
		out, err := tm.executeFixedRecursiveTableBuilder(cf, regs, base, proto, retBuf)
		return out, true, err
	case compiledProtocolFixedRecursiveTableFold:
		out, err := tm.executeFixedRecursiveTableFold(cf, regs, base, proto, retBuf)
		return out, true, err
	case compiledProtocolMutualRecursiveIntSCC:
		out, err := tm.executeMutualRecursiveIntSCC(cf, regs, base, proto, retBuf)
		return out, true, err
	case compiledProtocolNone:
		return nil, false, nil
	default:
		return nil, true, fmt.Errorf("tier2: unknown compiled protocol kind %d", cf.ProtocolKind())
	}
}

func (tm *TieringManager) tryCompiledProtocolCallExit(fnVal runtime.Value, regs []runtime.Value, absSlot, nArgs, nRets int) (bool, error) {
	cl, ok := vmClosureFromValue(fnVal)
	if !ok || cl == nil || cl.Proto == nil || tm == nil || tm.callVM == nil {
		return false, nil
	}
	calleeProto := cl.Proto
	if nArgs != calleeProto.NumParams || nArgs < 0 || absSlot+nArgs >= len(regs) {
		return false, nil
	}
	cf := tm.tier2Compiled[calleeProto]
	kind := cf.ProtocolKind()
	if kind == compiledProtocolNone || !compiledProtocolCallExitFastPathSupports(kind) {
		return false, nil
	}

	result, ok, err := tm.executeCompiledProtocolCallExitResult(cf, calleeProto, regs, absSlot, nArgs)
	if err != nil {
		return false, nil
	}
	if !ok {
		return false, nil
	}
	storeCallExitSingleResult(regs, absSlot, nRets, result)
	if currentRegs := tm.callVM.Regs(); len(currentRegs) > 0 {
		storeCallExitSingleResult(currentRegs, absSlot, nRets, result)
	}
	return true, nil
}

func (tm *TieringManager) executeCompiledProtocolCallExitResult(cf *CompiledFunction, proto *vm.FuncProto, regs []runtime.Value, absSlot, nArgs int) (runtime.Value, bool, error) {
	switch cf.ProtocolKind() {
	case compiledProtocolFixedRecursiveIntFold:
		if nArgs != 1 || absSlot+1 >= len(regs) {
			return runtime.NilValue(), false, nil
		}
		if !tm.fixedRecursiveSelfGlobalMatches(proto) {
			tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive int fold self global changed")
			return runtime.NilValue(), false, nil
		}
		n, ok := cf.FixedRecursiveIntFold.fold(regs[absSlot+1])
		if !ok {
			return runtime.NilValue(), false, nil
		}
		proto.EnteredTier2 = 1
		return runtime.IntValue(n), true, nil
	case compiledProtocolFixedRecursiveNestedIntFold:
		if nArgs != 2 || absSlot+2 >= len(regs) {
			return runtime.NilValue(), false, nil
		}
		if !tm.fixedRecursiveSelfGlobalMatches(proto) {
			tm.disableTier2AfterRuntimeDeopt(proto, "tier2: fixed recursive nested int fold self global changed")
			return runtime.NilValue(), false, nil
		}
		n, ok := cf.FixedRecursiveNestedIntFold.fold(regs[absSlot+1], regs[absSlot+2])
		if !ok {
			return runtime.NilValue(), false, nil
		}
		proto.EnteredTier2 = 1
		return runtime.IntValue(n), true, nil
	case compiledProtocolMutualRecursiveIntSCC:
		if nArgs < 0 || nArgs > 4 || absSlot+nArgs >= len(regs) {
			return runtime.NilValue(), false, nil
		}
		var args [4]int64
		for i := 0; i < nArgs; i++ {
			arg := regs[absSlot+1+i]
			if !arg.IsInt() || arg.Int() < 0 {
				return runtime.NilValue(), false, nil
			}
			args[i] = arg.Int()
		}
		n, ok, err := tm.executeMutualRecursiveIntSCCArgs(cf, proto, args)
		if err != nil || !ok {
			return runtime.NilValue(), ok, err
		}
		return runtime.IntValue(n), true, nil
	default:
		return runtime.NilValue(), false, nil
	}
}

func compiledProtocolCallExitFastPathSupports(kind compiledProtocolKind) bool {
	switch kind {
	case compiledProtocolFixedRecursiveIntFold,
		compiledProtocolFixedRecursiveNestedIntFold,
		compiledProtocolMutualRecursiveIntSCC:
		return true
	default:
		return false
	}
}
