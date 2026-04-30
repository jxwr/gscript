package vm

import "github.com/gscript/gscript/internal/runtime"

const maxWholeCallFloatScratch = 1 << 20

func wholeCallKernelArity(n int) bool {
	return n == 1 || n == 3
}

func (vm *VM) tryValueWholeCallKernel(cl *Closure, args []runtime.Value, c int, dst int) (bool, error) {
	handled, results, err := vm.tryRunValueWholeCallKernel(cl, args)
	if !handled || err != nil {
		return handled, err
	}
	vm.writeCallResults(dst, c, results)
	return true, nil
}

func (vm *VM) tryRunValueWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if handled, results, err := vm.tryRunRecursiveTableValueKernel(cl, args); handled || err != nil {
		return handled, results, err
	}
	return vm.tryRunNonRecursiveTableValueWholeCallKernel(cl, args)
}

func (vm *VM) tryRunNonRecursiveTableValueWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if handled, results, err := vm.tryRunFannkuchReduxWholeCallKernel(cl, args); handled || err != nil {
		return handled, results, err
	}
	if handled, results, err := vm.tryRunSieveWholeCallKernel(cl, args); handled || err != nil {
		return handled, results, err
	}
	if handled, results, err := vm.tryRunMatmulWholeCallKernel(cl, args); handled || err != nil {
		return handled, results, err
	}
	return false, nil, nil
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
	if handled, err := vm.tryRunIntSortWholeCallKernel(cl, args); handled || err != nil {
		return handled, err
	}
	if handled, err := vm.tryRunSpectralWholeCallKernel(cl, args); handled || err != nil {
		return handled, err
	}
	if handled, err := vm.tryRunNBodyAdvanceKernel(cl, args); handled || err != nil {
		return handled, err
	}
	return false, nil
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
	if n > maxWholeCallFloatScratch {
		return make([]float64, n)
	}
	if cap(vm.wholeCallFloatBuf) < n {
		vm.wholeCallFloatBuf = make([]float64, n)
	}
	return vm.wholeCallFloatBuf[:n]
}
