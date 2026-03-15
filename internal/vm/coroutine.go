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
	status   VMCoroutineStatus
	closure  *Closure
	started  bool

	resumeCh chan []rt.Value      // caller -> coroutine
	yieldCh  chan vmYieldResult   // coroutine -> caller
}

// NewVMCoroutine creates a new VM coroutine wrapping the given closure.
func NewVMCoroutine(cl *Closure) *VMCoroutine {
	return &VMCoroutine{
		status:   VMCoroutineSuspended,
		closure:  cl,
		resumeCh: make(chan []rt.Value, 1),
		yieldCh:  make(chan vmYieldResult, 1),
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
			return []rt.Value{rt.AnyCoroutineValue(co)}, nil
		},
	}))

	// coroutine.resume(co, args...) -> ok, values...
	coLib.RawSet(rt.StringValue("resume"), rt.FunctionValue(&rt.GoFunction{
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
	}))

	// coroutine.yield(values...) -> resume args
	coLib.RawSet(rt.StringValue("yield"), rt.FunctionValue(&rt.GoFunction{
		Name: "coroutine.yield",
		Fn: func(args []rt.Value) ([]rt.Value, error) {
			co := getCurrentVMCoroutine()
			if co == nil {
				return nil, fmt.Errorf("cannot yield from outside a coroutine")
			}
			// Send yielded values to the resume caller
			co.yieldCh <- vmYieldResult{values: args}
			// Block until resumed — the resume caller will send args
			resumeVals := <-co.resumeCh
			return resumeVals, nil
		},
	}))

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
			return []rt.Value{rt.BoolValue(getCurrentVMCoroutine() != nil)}, nil
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
			dead := false
			wrapper := &rt.GoFunction{
				Name: "wrapped_coroutine",
				Fn: func(wargs []rt.Value) ([]rt.Value, error) {
					if dead {
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					results, err := vm.resumeCoroutine(co, wargs)
					if err != nil {
						return nil, err
					}
					// results[0] is ok bool
					if len(results) > 0 && !results[0].IsNil() && !results[0].Bool() {
						dead = true
						if len(results) > 1 {
							return nil, fmt.Errorf("%s", results[1].String())
						}
						return nil, fmt.Errorf("cannot resume dead coroutine")
					}
					// Return values after the ok bool; return nil if none (signals end of iteration)
					if len(results) > 1 {
						return results[1:], nil
					}
					// Coroutine returned without values — mark as done, return nil for for-range
					dead = true
					return []rt.Value{rt.NilValue()}, nil
				},
			}
			return []rt.Value{rt.FunctionValue(wrapper)}, nil
		},
	}))

	vm.SetGlobal("coroutine", rt.TableValue(coLib))
}

// resumeCoroutine resumes a suspended VM coroutine.
func (vm *VM) resumeCoroutine(co *VMCoroutine, args []rt.Value) ([]rt.Value, error) {
	if co.status == VMCoroutineDead {
		return []rt.Value{rt.BoolValue(false), rt.StringValue("cannot resume dead coroutine")}, nil
	}
	if co.status == VMCoroutineRunning {
		return []rt.Value{rt.BoolValue(false), rt.StringValue("cannot resume running coroutine")}, nil
	}

	co.status = VMCoroutineRunning

	if !co.started {
		co.started = true
		// Launch a new goroutine with its own VM sharing globals.
		go func() {
			setCurrentVMCoroutine(co)
			defer setCurrentVMCoroutine(nil)

			coVM := newChildVM(vm)

			// Wait for initial args from the first resume.
			initArgs := <-co.resumeCh

			// Execute the closure.
			results, err := coVM.call(co.closure, initArgs, 0, 0)
			if results == nil {
				results = []rt.Value{}
			}
			co.yieldCh <- vmYieldResult{values: results, err: err, done: true}
		}()
	}

	// Send args to the coroutine goroutine.
	co.resumeCh <- args

	// Wait for yield or completion.
	result := <-co.yieldCh

	if result.done || result.err != nil {
		co.status = VMCoroutineDead
	} else {
		co.status = VMCoroutineSuspended
	}

	if result.err != nil {
		return []rt.Value{rt.BoolValue(false), rt.StringValue(result.err.Error())}, nil
	}

	return append([]rt.Value{rt.BoolValue(true)}, result.values...), nil
}
