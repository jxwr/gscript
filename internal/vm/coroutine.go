package vm

import (
	"errors"
	"fmt"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
)

// VMCoroutineStatus represents the state of a VM coroutine.
type VMCoroutineStatus int

const (
	VMCoroutineSuspended VMCoroutineStatus = iota
	VMCoroutineRunning
	VMCoroutineDead
	VMCoroutineNormal // resumed another coroutine
)

// vmYieldResult carries yielded values from a paused coroutine VM.
type vmYieldResult struct {
	values []rt.Value
}

var errCoroutineYield = errors.New("coroutine yield")

// VMCoroutine holds the state of a VM-based coroutine. Long-lived coroutines
// keep a child VM paused at the bytecode immediately after coroutine.yield.
type VMCoroutine struct {
	status     VMCoroutineStatus
	closure    *Closure
	started    bool
	leafNoCall bool
	resultBuf  [8]rt.Value
	vm         *VM
	yieldDst   int
	yieldC     int

	yieldResult vmYieldResult
}

// NewVMCoroutine creates a new VM coroutine wrapping the given closure.
func NewVMCoroutine(cl *Closure) *VMCoroutine {
	return &VMCoroutine{
		status:     VMCoroutineSuspended,
		closure:    cl,
		leafNoCall: cl != nil && cl.Proto != nil && protoHasNoCalls(cl.Proto),
	}
}

// Status returns the human-readable status string.
func (co *VMCoroutine) Status() string {
	switch co.status {
	case VMCoroutineSuspended:
		return "suspended"
	case VMCoroutineRunning:
		return "running"
	case VMCoroutineDead:
		return "dead"
	case VMCoroutineNormal:
		return "normal"
	}
	return "dead"
}

func vmCoroutineFromValue(v rt.Value) (*VMCoroutine, bool) {
	if p := v.AnyCoroutinePointer(); p != nil {
		return (*VMCoroutine)(p), true
	}
	co, ok := v.Ptr().(*VMCoroutine)
	return co, ok
}

func (vm *VM) activeCoroutine() *VMCoroutine {
	return vm.currentCoroutine
}

// RegisterCoroutineLib installs VM-native coroutine functions into globals,
// overriding the tree-walker's coroutine library which cannot handle VM closures.
func (vm *VM) RegisterCoroutineLib() {
	vm.SetGlobal("coroutine", rt.TableValue(vm.newCoroutineLib()))
}

func (vm *VM) newCoroutineLib() *rt.Table {
	coLib := rt.NewTable()

	// coroutine.create(fn) -> coroutine
	coLib.RawSet(rt.StringValue("create"), rt.FunctionValue(&rt.GoFunction{
		Name: "coroutine.create",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsFunction() {
				return nil, fmt.Errorf("coroutine.create expects a function")
			}
			cl, ok := args[0].Ptr().(*Closure)
			if !ok {
				// Also accept GoFunctions — wrap in a tiny VM closure is not possible,
				// but we can use the GoFunction approach.
				return nil, fmt.Errorf("coroutine.create expects a GScript function, got Go function")
			}
			co := NewVMCoroutine(cl)
			vm.recordCoroutineCreated(false)
			return []rt.Value{rt.VMCoroutineValue(unsafe.Pointer(co), co)}, nil
		},
	}))

	// coroutine.resume(co, args...) -> ok, values...
	resumeFn := &rt.GoFunction{
		Name: "coroutine.resume",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.resume expects a coroutine")
			}
			co, ok := vmCoroutineFromValue(args[0])
			if !ok {
				return nil, fmt.Errorf("coroutine.resume expects a VM coroutine")
			}
			return vm.resumeCoroutine(co, args[1:])
		},
	}
	vm.coroutineResumeFn = resumeFn
	coLib.RawSet(rt.StringValue("resume"), rt.FunctionValue(resumeFn))

	// coroutine.yield(values...) -> resume args
	yieldFn := &rt.GoFunction{
		Name: "coroutine.yield",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			return vm.yieldCoroutine(args)
		},
	}
	vm.coroutineYieldFn = yieldFn
	coLib.RawSet(rt.StringValue("yield"), rt.FunctionValue(yieldFn))

	// coroutine.status(co) -> string
	coLib.RawSet(rt.StringValue("status"), rt.FunctionValue(&rt.GoFunction{
		Name: "coroutine.status",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.status expects a coroutine")
			}
			co, ok := vmCoroutineFromValue(args[0])
			if !ok {
				return nil, fmt.Errorf("coroutine.status expects a VM coroutine")
			}
			return []rt.Value{rt.StringValue(co.Status())}, nil
		},
	}))

	// coroutine.isyieldable() -> bool
	coLib.RawSet(rt.StringValue("isyieldable"), rt.FunctionValue(&rt.GoFunction{
		Name: "coroutine.isyieldable",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			return []rt.Value{rt.BoolValue(vm.activeCoroutine() != nil)}, nil
		},
	}))

	// coroutine.wrap(fn) -> iterator function
	coLib.RawSet(rt.StringValue("wrap"), rt.FunctionValue(&rt.GoFunction{
		Name: "coroutine.wrap",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsFunction() {
				return nil, fmt.Errorf("coroutine.wrap expects a function")
			}
			cl, ok := args[0].Ptr().(*Closure)
			if !ok {
				return nil, fmt.Errorf("coroutine.wrap expects a GScript function")
			}
			co := NewVMCoroutine(cl)
			vm.recordCoroutineCreated(true)
			dead := false
			wrapper := &rt.GoFunction{
				Name: "wrapped_coroutine",
				Fn: func(wargs []rt.Value) ([]rt.Value, error) {
					if dead {
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					ok, values, err := vm.resumeCoroutineRaw(co, wargs)
					if err != nil {
						return nil, err
					}
					if !ok {
						dead = true
						if len(values) > 0 {
							return nil, fmt.Errorf("%s", values[0].String())
						}
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					// Return yielded values; return nil if none (signals end of iteration).
					if len(values) > 0 {
						return values, nil
					}
					// Coroutine returned without values — mark as done, return nil for for-range
					dead = true
					return []rt.Value{rt.NilValue()}, nil
				},
			}
			return []rt.Value{rt.FunctionValue(wrapper)}, nil
		},
	}))

	return coLib
}

// resumeCoroutine resumes a suspended VM coroutine.
func (vm *VM) resumeCoroutine(co *VMCoroutine, args []rt.Value) ([]rt.Value, error) {
	ok, values, err := vm.resumeCoroutineRaw(co, args)
	if err != nil {
		return nil, err
	}
	return co.resumeResults(ok, values), nil
}

func (vm *VM) resumeCoroutineRaw(co *VMCoroutine, args []rt.Value) (bool, []rt.Value, error) {
	vm.recordCoroutineResume()
	if co.status == VMCoroutineDead {
		vm.recordCoroutineResumeError()
		return false, []rt.Value{rt.StringValue("cannot resume dead coroutine")}, nil
	}
	if co.status == VMCoroutineRunning {
		vm.recordCoroutineResumeError()
		return false, []rt.Value{rt.StringValue("cannot resume running coroutine")}, nil
	}

	co.status = VMCoroutineRunning

	if !co.started && co.leafNoCall {
		co.started = true
		vm.recordCoroutineLeafFastPath()
		results, err, ok := vm.callLeafCoroutine(co, args)
		if !ok {
			vm.recordCoroutineLeafFallback()
			coVM := newChildVM(vm, co)
			results, err = coVM.call(co.closure, args, 0, 0)
		}
		if results == nil {
			results = []rt.Value{}
		}
		co.status = VMCoroutineDead
		vm.recordCoroutineCompleted()
		if err != nil {
			return false, []rt.Value{rt.StringValue(err.Error())}, nil
		}
		return true, results, nil
	}

	if !co.started {
		co.started = true
		vm.recordCoroutineGoroutineStart()
		co.vm = newChildVM(vm, co)
		results, err := co.vm.call(co.closure, args, 0, 0)
		return vm.finishCoroutineRun(co, results, err)
	}

	if co.vm == nil {
		co.status = VMCoroutineDead
		vm.recordCoroutineResumeError()
		return false, []rt.Value{rt.StringValue("cannot resume dead coroutine")}, nil
	}
	co.vm.writeCallResults(co.yieldDst, co.yieldC, args)
	results, err := co.vm.run()
	return vm.finishCoroutineRun(co, results, err)
}

func (vm *VM) finishCoroutineRun(co *VMCoroutine, results []rt.Value, err error) (bool, []rt.Value, error) {
	if err == errCoroutineYield {
		result := co.yieldResult
		co.yieldResult = vmYieldResult{}
		co.status = VMCoroutineSuspended
		return true, result.values, nil
	}
	if results == nil {
		results = []rt.Value{}
	}
	if co.vm != nil {
		co.vm.frameCount = 0
		co.vm.top = 0
	}
	if err != nil {
		co.status = VMCoroutineDead
		vm.recordCoroutineCompleted()
		return false, []rt.Value{rt.StringValue(err.Error())}, nil
	}
	co.status = VMCoroutineDead
	vm.recordCoroutineCompleted()
	return true, results, nil
}

func (vm *VM) yieldCoroutine(args []rt.Value) ([]rt.Value, error) {
	co := vm.activeCoroutine()
	if co == nil {
		return nil, fmt.Errorf("cannot yield from outside a coroutine")
	}
	return nil, fmt.Errorf("coroutine.yield requires VM call dispatch")
}

func protoHasNoCalls(proto *FuncProto) bool {
	for _, inst := range proto.Code {
		switch DecodeOp(inst) {
		case OP_CALL, OP_TFORCALL:
			return false
		}
	}
	return true
}

func (co *VMCoroutine) resumeResults(ok bool, values []rt.Value) []rt.Value {
	n := 1 + len(values)
	if n <= len(co.resultBuf) {
		out := co.resultBuf[:n]
		out[0] = rt.BoolValue(ok)
		copy(out[1:], values)
		return out
	}
	out := make([]rt.Value, n)
	out[0] = rt.BoolValue(ok)
	copy(out[1:], values)
	return out
}

func (vm *VM) callLeafCoroutine(co *VMCoroutine, args []rt.Value) ([]rt.Value, error, bool) {
	cl := co.closure
	if cl == nil || cl.Proto == nil {
		return nil, nil, false
	}

	base := vm.top
	if vm.frameCount > 0 {
		curFrame := &vm.frames[vm.frameCount-1]
		minBase := curFrame.base + curFrame.closure.Proto.MaxStack
		if base < minBase {
			base = minBase
		}
	}
	if base+cl.Proto.MaxStack+1 > len(vm.regs) {
		return nil, nil, false
	}

	savedTop := vm.top
	savedJIT := vm.methodJIT
	vm.methodJIT = nil
	defer func() {
		vm.methodJIT = savedJIT
		vm.top = savedTop
	}()

	results, err := vm.call(cl, args, base, 0)
	return results, err, true
}
