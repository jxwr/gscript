package vm

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"

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

// vmYieldResult carries values or completion signals from a coroutine goroutine.
type vmYieldResult struct {
	values []rt.Value
	err    error
	done   bool
}

// VMCoroutine holds the state of a VM-based coroutine.
// Each coroutine runs in its own goroutine with its own VM instance,
// communicating with the caller via channels.
type VMCoroutine struct {
	status     VMCoroutineStatus
	closure    *Closure
	started    bool
	leafNoCall bool
	resultBuf  [8]rt.Value

	resumeArgs  []rt.Value
	yieldResult vmYieldResult
	resumeCh    chan struct{} // caller -> coroutine
	yieldCh     chan struct{} // coroutine -> caller
}

// NewVMCoroutine creates a new VM coroutine wrapping the given closure.
func NewVMCoroutine(cl *Closure) *VMCoroutine {
	return &VMCoroutine{
		status:     VMCoroutineSuspended,
		closure:    cl,
		leafNoCall: cl != nil && cl.Proto != nil && protoHasNoCalls(cl.Proto),
		resumeCh:   make(chan struct{}),
		yieldCh:    make(chan struct{}),
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

// goroutine-local map for finding the current coroutine from yield calls.
var vmCoMap sync.Map // goroutineID -> *VMCoroutine

func setCurrentVMCoroutine(co *VMCoroutine) {
	gid := vmGoroutineID()
	if co == nil {
		vmCoMap.Delete(gid)
	} else {
		vmCoMap.Store(gid, co)
	}
}

func getCurrentVMCoroutine() *VMCoroutine {
	gid := vmGoroutineID()
	v, ok := vmCoMap.Load(gid)
	if !ok {
		return nil
	}
	return v.(*VMCoroutine)
}

func (vm *VM) activeCoroutine() *VMCoroutine {
	if vm.currentCoroutine != nil {
		return vm.currentCoroutine
	}
	return getCurrentVMCoroutine()
}

func vmGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := string(buf[:n])
	s = strings.TrimPrefix(s, "goroutine ")
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return 0
	}
	id, _ := strconv.ParseInt(s[:idx], 10, 64)
	return id
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
			return []rt.Value{rt.AnyCoroutineValue(co)}, nil
		},
	}))

	// coroutine.resume(co, args...) -> ok, values...
	resumeFn := &rt.GoFunction{
		Name: "coroutine.resume",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			if len(args) < 1 || !args[0].IsCoroutine() {
				return nil, fmt.Errorf("coroutine.resume expects a coroutine")
			}
			co, ok := args[0].Ptr().(*VMCoroutine)
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
			co, ok := args[0].Ptr().(*VMCoroutine)
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
		// Launch a new goroutine with its own VM sharing globals.
		go func() {
			setCurrentVMCoroutine(co)
			defer setCurrentVMCoroutine(nil)

			coVM := newChildVM(vm, co)

			// Wait for initial args from the first resume.
			<-co.resumeCh
			initArgs := co.resumeArgs
			co.resumeArgs = nil

			// Execute the closure.
			results, err := coVM.call(co.closure, initArgs, 0, 0)
			if results == nil {
				results = []rt.Value{}
			}
			co.yieldResult = vmYieldResult{values: results, err: err, done: true}
			co.yieldCh <- struct{}{}
		}()
	}

	// Send args to the coroutine goroutine.
	co.resumeArgs = args
	co.resumeCh <- struct{}{}

	// Wait for yield or completion.
	<-co.yieldCh
	result := co.yieldResult
	co.yieldResult = vmYieldResult{}

	if result.done || result.err != nil {
		co.status = VMCoroutineDead
		vm.recordCoroutineCompleted()
	} else {
		co.status = VMCoroutineSuspended
	}

	if result.err != nil {
		return false, []rt.Value{rt.StringValue(result.err.Error())}, nil
	}

	return true, result.values, nil
}

func (vm *VM) yieldCoroutine(args []rt.Value) ([]rt.Value, error) {
	co := vm.activeCoroutine()
	if co == nil {
		return nil, fmt.Errorf("cannot yield from outside a coroutine")
	}
	vm.recordCoroutineYield()
	co.yieldResult = vmYieldResult{values: args}
	co.yieldCh <- struct{}{}
	<-co.resumeCh
	resumeVals := co.resumeArgs
	co.resumeArgs = nil
	return resumeVals, nil
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
