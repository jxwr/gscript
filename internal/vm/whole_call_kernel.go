package vm

import "github.com/gscript/gscript/internal/runtime"

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
	if handled, err := vm.tryRunSpectralWholeCallKernel(cl, args); handled || err != nil {
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
