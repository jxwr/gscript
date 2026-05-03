//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
)

func collectCallExitArgs(regs []runtime.Value, absSlot, nArgs int) []runtime.Value {
	var local [16]runtime.Value
	var args []runtime.Value
	if nArgs <= len(local) {
		args = local[:nArgs]
	} else {
		args = make([]runtime.Value, nArgs)
	}
	for i := range args {
		args[i] = runtime.NilValue()
	}
	for i := 0; i < nArgs; i++ {
		idx := absSlot + 1 + i
		if idx >= 0 && idx < len(regs) {
			args[i] = regs[idx]
		}
	}
	return args
}

func callGoFunctionFast(gf *runtime.GoFunction, regs []runtime.Value, absSlot, nArgs int) (runtime.Value, bool, error) {
	if gf == nil {
		return runtime.NilValue(), false, nil
	}
	switch nArgs {
	case 0:
		if gf.Fast1 != nil {
			v, err := gf.Fast1(nil)
			return v, true, err
		}
	case 1:
		if gf.FastArg1 != nil && absSlot+1 >= 0 && absSlot+1 < len(regs) {
			v, err := gf.FastArg1(regs[absSlot+1])
			return v, true, err
		}
	case 2:
		if gf.FastArg2 != nil && absSlot+1 >= 0 && absSlot+2 < len(regs) {
			v, err := gf.FastArg2(regs[absSlot+1], regs[absSlot+2])
			return v, true, err
		}
	case 3:
		if gf.FastArg3 != nil && absSlot+1 >= 0 && absSlot+3 < len(regs) {
			v, err := gf.FastArg3(regs[absSlot+1], regs[absSlot+2], regs[absSlot+3])
			return v, true, err
		}
	}
	return runtime.NilValue(), false, nil
}

func storeCallExitSingleResult(regs []runtime.Value, absSlot, nRets int, result runtime.Value) {
	for i := 0; i < nRets; i++ {
		idx := absSlot + i
		if idx < 0 || idx >= len(regs) {
			continue
		}
		if i == 0 {
			regs[idx] = result
		} else {
			regs[idx] = runtime.NilValue()
		}
	}
}
