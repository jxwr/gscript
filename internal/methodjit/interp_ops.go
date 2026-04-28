// interp_ops.go contains call handling and global lookup for the IR interpreter.
// Split from interp.go to keep file sizes manageable.

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// execCall handles function call instructions.
func (s *interpState) execCall(instr *Instr) (runtime.Value, error) {
	if len(instr.Args) == 0 {
		return runtime.NilValue(), fmt.Errorf("IR interpreter: OpCall with no args")
	}

	fnVal := s.val(instr.Args[0])
	callArgs := make([]runtime.Value, len(instr.Args)-1)
	for i := 1; i < len(instr.Args); i++ {
		callArgs[i-1] = s.val(instr.Args[i])
	}

	// Check if this is a self-recursive call.
	if fnVal.IsFunction() {
		if cl, ok := vmClosureFromValue(fnVal); ok && cl.Proto == s.fn.Proto {
			// Self-recursive call: interpret recursively with the same IR.
			results, err := interpretImpl(s.fn, callArgs, s.depth+1)
			if err != nil {
				return runtime.NilValue(), err
			}
			if len(results) > 0 {
				return results[0], nil
			}
			return runtime.NilValue(), nil
		}
	}

	// For non-self calls, use the VM to execute.
	return s.callViaVM(fnVal, callArgs)
}

// callViaVM executes a function call using the VM interpreter.
func (s *interpState) callViaVM(fnVal runtime.Value, args []runtime.Value) (runtime.Value, error) {
	// Create a minimal VM to execute the call.
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	// Populate VM globals with any function protos known to the caller.
	// This lets residual cross-function calls (e.g., after bounded recursive
	// inlining) resolve their callees when the VM executes their bytecode.
	if s.fn.Globals != nil {
		for name, p := range s.fn.Globals {
			if p == nil {
				continue
			}
			cl := vm.NewClosure(p)
			v.SetGlobal(name, runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl))
		}
	}

	results, err := v.CallValue(fnVal, args)
	if err != nil {
		return runtime.NilValue(), err
	}
	if len(results) > 0 {
		return results[0], nil
	}
	return runtime.NilValue(), nil
}

// getGlobal looks up a global variable by name.
// In the IR interpreter, globals are not available unless we have a VM context.
// For self-recursive functions, the function itself is the only global needed.
// When the Function's Globals table is populated (e.g., after bounded recursive
// inlining leaves residual cross-function calls), other named protos are also
// resolvable.
func (s *interpState) getGlobal(name string) runtime.Value {
	// If the name matches the function being interpreted, return a closure
	// wrapping the current proto so self-recursive calls work.
	if name == s.fn.Proto.Name {
		cl := vm.NewClosure(s.fn.Proto)
		return runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)
	}
	// Consult the inline pass's globals table if available.
	if s.fn.Globals != nil {
		if p, ok := s.fn.Globals[name]; ok && p != nil {
			cl := vm.NewClosure(p)
			return runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)
		}
	}
	return runtime.NilValue()
}
