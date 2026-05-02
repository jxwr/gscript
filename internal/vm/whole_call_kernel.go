package vm

import "github.com/gscript/gscript/internal/runtime"

const maxWholeCallScalarScratch = 1 << 20

func wholeCallKernelArity(n int) bool {
	return n == 1 || n == 2 || n == 3
}

func WholeCallKernelArity(n int) bool {
	return wholeCallKernelArity(n)
}

func (vm *VM) tryValueWholeCallKernel(cl *Closure, args []runtime.Value, c int, dst int) (bool, error) {
	handled, results, err := vm.tryRunValueWholeCallKernel(cl, args)
	if !handled || err != nil {
		return handled, err
	}
	vm.writeCallResults(dst, c, results)
	return true, nil
}

func (vm *VM) TryValueWholeCallKernelValue(fn runtime.Value, args []runtime.Value, c int, dst int) (bool, error) {
	cl, ok := closureFromValue(fn)
	if !ok {
		return false, nil
	}
	return vm.tryValueWholeCallKernel(cl, args, c, dst)
}

func (vm *VM) tryRunValueWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if handled, results, err := vm.tryRunRawIntNestedValueKernel(cl, args); handled || err != nil {
		return handled, results, err
	}
	includeRecursiveTable := cl != nil && cl.Proto != nil && vm.methodJIT != nil && cl.Proto.Tier2Promoted
	return vm.tryRunCachedValueWholeCallKernel(cl, args, includeRecursiveTable)
}

func (vm *VM) tryRunNonRecursiveTableValueWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	return vm.tryRunCachedValueWholeCallKernel(cl, args, false)
}

// tryWholeCallKernel executes a guarded whole-call numeric kernel and writes
// the no-result call convention used by in-place kernels.
func (vm *VM) tryWholeCallKernel(cl *Closure, args []runtime.Value, c int, dst int) (bool, error) {
	handled, err := vm.tryRunWholeCallKernel(cl, args)
	if !handled || err != nil {
		return handled, err
	}
	vm.writeNoResults(dst, c)
	return true, nil
}

func (vm *VM) tryRunWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	return vm.tryRunCachedNoResultWholeCallKernel(cl, args)
}

func (vm *VM) tryRunCachedValueWholeCallKernel(cl *Closure, args []runtime.Value, includeRecursiveTable bool) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil {
		return false, nil, nil
	}
	if !mayHaveWholeCallValueKernelCandidate(cl.Proto, len(args), includeRecursiveTable) {
		return false, nil, nil
	}
	recognized := cachedWholeCallKernelBits(cl.Proto)
	if recognized == 0 {
		return false, nil, nil
	}
	for i, entry := range wholeCallKernelRegistry {
		if recognized&(uint64(1)<<uint(i)) == 0 || entry.info.Route != KernelRouteWholeCallValue || entry.runValue == nil {
			continue
		}
		if entry.recursiveTable && !includeRecursiveTable {
			continue
		}
		handled, results, err := entry.runValue(vm, cl, args)
		if handled || err != nil {
			return handled, results, err
		}
	}
	return false, nil, nil
}

func (vm *VM) tryRunCachedNoResultWholeCallKernel(cl *Closure, args []runtime.Value) (bool, error) {
	if cl == nil || cl.Proto == nil {
		return false, nil
	}
	if !mayHaveWholeCallNoResultKernelCandidate(cl.Proto, len(args)) {
		return false, nil
	}
	recognized := cachedWholeCallKernelBits(cl.Proto)
	if recognized == 0 {
		return false, nil
	}
	for i, entry := range wholeCallKernelRegistry {
		if recognized&(uint64(1)<<uint(i)) == 0 || entry.info.Route != KernelRouteWholeCallNoResult || entry.runNoResult == nil {
			continue
		}
		handled, err := entry.runNoResult(vm, cl, args)
		if handled || err != nil {
			return handled, err
		}
	}
	return false, nil
}

func mayHaveWholeCallValueKernelCandidate(proto *FuncProto, argc int, includeRecursiveTable bool) bool {
	if proto == nil || proto.IsVarArg {
		return false
	}
	switch argc {
	case 1:
		if proto.NumParams != 1 {
			return false
		}
		if (proto.MaxStack == 15 && len(proto.Constants) == 0 && len(proto.Code) == 21) ||
			(proto.MaxStack == 18 && len(proto.Constants) == 3 && len(proto.Code) == 35) ||
			(proto.MaxStack == 23 && len(proto.Constants) == 0 && len(proto.Code) == 38) {
			return true
		}
		if includeRecursiveTable {
			return true
		}
		return (proto.MaxStack == 30 && len(proto.Constants) == 2 && len(proto.Protos) == 0) ||
			(proto.MaxStack >= 13 && len(proto.Constants) == 0 && len(proto.Protos) == 0 && len(proto.Code) == 45)
	case 2:
		return (proto.NumParams == 2 && proto.MaxStack == 20 && len(proto.Constants) == 0 && len(proto.Code) == 37) ||
			(proto.NumParams == 2 && proto.MaxStack == 49 && len(proto.Constants) == 24 && len(proto.Code) == 169)
	case 3:
		return proto.NumParams == 3 && (len(proto.Constants) == 1 ||
			(len(proto.Constants) == 16 && len(proto.Code) == 130))
	default:
		return false
	}
}

func mayHaveWholeCallNoResultKernelCandidate(proto *FuncProto, argc int) bool {
	if proto == nil || proto.IsVarArg {
		return false
	}
	switch argc {
	case 1:
		return proto.NumParams == 1 && len(proto.Constants) >= 10 && len(proto.Code) == 99
	case 3:
		return proto.NumParams == 3 && len(proto.Constants) >= 1
	default:
		return false
	}
}

func (vm *VM) writeNoResults(dst, c int) {
	if c == 0 {
		vm.top = dst
		return
	}
	for i := 0; i < c-1; i++ {
		vm.regs[dst+i] = runtime.NilValue()
	}
}

func (vm *VM) wholeCallFloatScratch(n int) []float64 {
	if n <= 0 {
		return nil
	}
	if n > maxWholeCallScalarScratch {
		return make([]float64, n)
	}
	if cap(vm.wholeCallFloatBuf) < n {
		vm.wholeCallFloatBuf = make([]float64, n)
	}
	return vm.wholeCallFloatBuf[:n]
}

func (vm *VM) wholeCallIntScratch(n int) []int64 {
	if n <= 0 {
		return nil
	}
	if n > maxWholeCallScalarScratch {
		return make([]int64, n)
	}
	if cap(vm.wholeCallIntBuf) < n {
		vm.wholeCallIntBuf = make([]int64, n)
	}
	return vm.wholeCallIntBuf[:n]
}

func (vm *VM) wholeCallValueScratch(n int) []runtime.Value {
	if n <= 0 {
		return nil
	}
	if cap(vm.wholeCallValueBuf) < n {
		vm.wholeCallValueBuf = make([]runtime.Value, n)
	}
	return vm.wholeCallValueBuf[:n]
}
