package vm

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/gscript/gscript/internal/runtime"
)

const (
	maxStack     = 256 // max registers per call frame
	maxCallDepth = 200 // max call stack depth
	maxMetaDepth = 50  // max __index chain depth
)

// JITEngine is the interface for JIT compilation engines.
type JITEngine interface {
	TryExecute(proto *FuncProto, regs []runtime.Value, base int, callCount int) ([]runtime.Value, int, bool)
}

// VM is the bytecode virtual machine.
type VM struct {
	regs         []runtime.Value // register file (shared across frames via base offset)
	frames       []CallFrame     // call stack
	frameCount   int             // current number of active frames
	globals      map[string]runtime.Value // legacy map (kept for interop)
	globalArray  []runtime.Value          // indexed globals (fast path)
	globalIndex  map[string]int           // name → index in globalArray
	globalVer    uint32                   // bumped on structural changes (new globals added)
	globalsMu    *sync.RWMutex  // protects globals for goroutine safety (shared across VMs)
	noGlobalLock bool           // skip globals mutex (single-threaded mode)
	openUpvals   []*Upvalue     // list of open upvalues (sorted by regIdx descending)
	top          int            // top of used registers (for variable returns)
	stringMeta   *runtime.Table // string metatable
	jit          JITEngine
	jitFactory   func(*VM) JITEngine
	argBuf       [16]runtime.Value // pre-allocated arg buffer for OP_CALL
	retBuf       [8]runtime.Value  // pre-allocated return buffer for OP_RETURN
	traceRec       TraceRecorderHook // optional trace recorder (nil = disabled)
	traceRecording bool              // cached: traceRec.IsRecording() (avoids interface dispatch)
}

// TraceExecutor executes a compiled trace.
type TraceExecutor interface {
	Execute(regs []runtime.Value, base int, proto *FuncProto) (exitPC int, sideExit bool, guardFail bool)
}

// TraceRecorderHook is the interface for the trace recorder.
type TraceRecorderHook interface {
	OnInstruction(pc int, inst uint32, proto *FuncProto, regs []runtime.Value, base int) bool
	OnLoopBackEdge(pc int, proto *FuncProto) bool
	IsRecording() bool
	PendingTrace() TraceExecutor
}

// traceResult holds the result of executing a compiled trace.
type traceResult struct {
	executed bool // true if a trace was actually executed
	exitPC   int  // bytecode PC where trace exited
	sideExit bool // true = side exit (resume at exitPC), false = loop done
}

// executeCompiledTrace runs a compiled trace if available.
func (vm *VM) executeCompiledTrace(proto *FuncProto, base int) traceResult {
	ct := vm.traceRec.PendingTrace()
	if ct == nil {
		return traceResult{}
	}
	exitPC, sideExit, guardFail := ct.Execute(vm.regs, base, proto)
	if guardFail {
		return traceResult{}
	}
	return traceResult{executed: true, exitPC: exitPC, sideExit: sideExit}
}

// SetTraceRecorder enables trace recording on this VM.
func (vm *VM) SetTraceRecorder(r TraceRecorderHook) {
	vm.traceRec = r
}

// SetJIT sets the JIT engine for this VM.
func (vm *VM) SetJIT(engine JITEngine) {
	vm.jit = engine
	// CallCount is tracked on FuncProto directly, no map needed.
}

// Regs returns the register file. Used by the JIT executor.
func (vm *VM) Regs() []runtime.Value {
	return vm.regs
}

// Globals returns the globals map.
func (vm *VM) Globals() map[string]runtime.Value {
	return vm.globals
}

// GetGlobal reads a global variable with proper locking.
func (vm *VM) GetGlobal(name string) runtime.Value {
	if vm.noGlobalLock {
		if idx, ok := vm.globalIndex[name]; ok {
			return vm.globalArray[idx]
		}
		return runtime.NilValue()
	}
	vm.globalsMu.RLock()
	if idx, ok := vm.globalIndex[name]; ok {
		v := vm.globalArray[idx]
		vm.globalsMu.RUnlock()
		return v
	}
	vm.globalsMu.RUnlock()
	return runtime.NilValue()
}

// SetGlobal writes a global variable with proper locking.
func (vm *VM) SetGlobal(name string, val runtime.Value) {
	if vm.noGlobalLock {
		if idx, ok := vm.globalIndex[name]; ok {
			vm.globalArray[idx] = val
			vm.globals[name] = val
		} else {
			idx = len(vm.globalArray)
			vm.globalArray = append(vm.globalArray, val)
			vm.globalIndex[name] = idx
			vm.globals[name] = val
			vm.globalVer++
		}
		return
	}
	vm.globalsMu.Lock()
	if idx, ok := vm.globalIndex[name]; ok {
		vm.globalArray[idx] = val
		vm.globals[name] = val
	} else {
		idx = len(vm.globalArray)
		vm.globalArray = append(vm.globalArray, val)
		vm.globalIndex[name] = idx
		vm.globals[name] = val
		vm.globalVer++
	}
	vm.globalsMu.Unlock()
}

// resolveGlobalIndex returns the globalArray index for a global name,
// creating a new entry if it doesn't exist.
func (vm *VM) resolveGlobalIndex(name string) int {
	if idx, ok := vm.globalIndex[name]; ok {
		return idx
	}
	// New global — add to array
	idx := len(vm.globalArray)
	val := vm.globals[name] // may be nil
	vm.globalArray = append(vm.globalArray, val)
	vm.globalIndex[name] = idx
	vm.globalVer++
	return idx
}

// New creates a new VM with the given globals.
func New(globals map[string]runtime.Value) *VM {
	// Build indexed global array from the initial map
	ga := make([]runtime.Value, 0, len(globals))
	gi := make(map[string]int, len(globals))
	for name, val := range globals {
		gi[name] = len(ga)
		ga = append(ga, val)
	}

	v := &VM{
		regs:         make([]runtime.Value, 1024),
		frames:       make([]CallFrame, maxCallDepth),
		globals:      globals,
		globalArray:  ga,
		globalIndex:  gi,
		globalsMu:    &sync.RWMutex{},
		noGlobalLock: true, // single-threaded by default
	}
	v.RegisterCoroutineLib()
	v.registerChannelBuiltins()
	return v
}

// newChildVM creates a child VM that shares globals with the parent.
// Used by coroutines which need to see the caller's global state.
func newChildVM(parent *VM) *VM {
	child := &VM{
		regs:         make([]runtime.Value, 1024),
		frames:       make([]CallFrame, maxCallDepth),
		globals:      parent.globals,
		globalArray:  parent.globalArray,
		globalIndex:  parent.globalIndex,
		globalVer:    parent.globalVer,
		globalsMu:    parent.globalsMu,
		noGlobalLock: false, // shared globals, must lock
		stringMeta:   parent.stringMeta,
	}
	if parent.jitFactory != nil {
		engine := parent.jitFactory(child)
		child.SetJIT(engine)
	}
	child.RegisterCoroutineLib()
	return child
}

// newIsolatedChildVM creates a child VM with a snapshot of the parent's globals.
// Used by OP_GO goroutines for lock-free reads. Shared heap objects (tables,
// channels) remain shared via pointers; globals array and index are copied.
func newIsolatedChildVM(parent *VM) *VM {
	// Copy both globalArray and globalIndex for full isolation
	ga := make([]runtime.Value, len(parent.globalArray))
	copy(ga, parent.globalArray)

	gi := make(map[string]int, len(parent.globalIndex))
	for k, v := range parent.globalIndex {
		gi[k] = v
	}

	childGlobals := make(map[string]runtime.Value, len(gi))
	for name, idx := range gi {
		childGlobals[name] = ga[idx]
	}

	child := &VM{
		regs:         make([]runtime.Value, 1024),
		frames:       make([]CallFrame, maxCallDepth),
		globals:      childGlobals,
		globalArray:  ga,
		globalIndex:  gi,
		globalVer:    parent.globalVer,
		globalsMu:    &sync.RWMutex{},
		noGlobalLock: true, // own copy, fully lock-free
		stringMeta:   parent.stringMeta,
	}
	if parent.jitFactory != nil {
		engine := parent.jitFactory(child)
		child.SetJIT(engine)
	}
	child.RegisterCoroutineLib()
	return child
}

// SetJITFactory sets a factory function that creates new JIT engines.
func (vm *VM) SetJITFactory(factory func(*VM) JITEngine) {
	vm.jitFactory = factory
}

// registerChannelBuiltins adds channel-related builtins to globals.
func (vm *VM) registerChannelBuiltins() {
	vm.SetGlobal("close", runtime.FunctionValue(&runtime.GoFunction{
		Name: "close",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			if len(args) < 1 || !args[0].IsChannel() {
				return nil, fmt.Errorf("close expects a channel")
			}
			ch := args[0].Channel()
			if err := ch.Close(); err != nil {
				return nil, err
			}
			return nil, nil
		},
	}))
}

// SetStringMeta sets the string metatable.
func (vm *VM) SetStringMeta(meta *runtime.Table) {
	vm.stringMeta = meta
}

// Execute runs a top-level function prototype.
func (vm *VM) Execute(proto *FuncProto) ([]runtime.Value, error) {
	cl := &Closure{Proto: proto}
	vm.frameCount = 0
	vm.top = 0
	return vm.call(cl, nil, 0, 0)
}

// CallValue calls a function value with the given arguments (exported for gscript wrapper).
func (vm *VM) CallValue(fn runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	return vm.callValue(fn, args)
}

// call pushes a new call frame and executes.
func (vm *VM) call(cl *Closure, args []runtime.Value, base int, numResults int) ([]runtime.Value, error) {
	proto := cl.Proto

	// Ensure register space
	needed := base + proto.MaxStack + 1
	if needed > len(vm.regs) {
		newRegs := make([]runtime.Value, needed*2)
		copy(newRegs, vm.regs)
		vm.regs = newRegs
	}

	// Place args in registers
	nParams := proto.NumParams
	var varargs []runtime.Value
	for i := 0; i < nParams && i < len(args); i++ {
		vm.regs[base+i] = args[i]
	}
	for i := len(args); i < nParams; i++ {
		vm.regs[base+i] = runtime.NilValue()
	}
	if proto.IsVarArg && len(args) > nParams {
		varargs = make([]runtime.Value, len(args)-nParams)
		copy(varargs, args[nParams:])
	}

	// Push frame
	if vm.frameCount >= maxCallDepth {
		return nil, fmt.Errorf("stack overflow (max call depth %d)", maxCallDepth)
	}
	frame := &vm.frames[vm.frameCount]
	frame.closure = cl
	frame.pc = 0
	frame.base = base
	frame.numResults = numResults
	frame.varargs = varargs
	vm.frameCount++

	// Try JIT execution if available.
	if vm.jit != nil && !proto.IsVarArg {
		proto.CallCount++
		results, resumePC, ok := vm.jit.TryExecute(proto, vm.regs, base, proto.CallCount)
		if ok {
			vm.closeUpvalues(base)
			vm.frameCount--
			return results, nil
		}
		if resumePC > 0 {
			frame.pc = resumePC
		}
	}

	result, err := vm.run()
	vm.frameCount--
	return result, err
}

// wrapLineErr wraps an error with source location info from the current frame.
func wrapLineErr(frame *CallFrame, err error) error {
	if err == nil {
		return nil
	}
	pc := frame.pc - 1
	line := 0
	if pc >= 0 && pc < len(frame.closure.Proto.LineInfo) {
		line = frame.closure.Proto.LineInfo[pc]
	}
	name := frame.closure.Proto.Source
	if name == "" {
		name = frame.closure.Proto.Name
	}
	if line > 0 {
		return fmt.Errorf("%s:%d: %w", name, line, err)
	}
	return err
}

// run is the main execution loop. Handles inline call/return to avoid
// Go stack growth for GScript function calls.
func (vm *VM) run() (retVals []runtime.Value, retErr error) {
	initialFC := vm.frameCount

	// On error, reset frame count to clean up any inline sub-frames.
	defer func() {
		if retErr != nil {
			vm.frameCount = initialFC
		}
	}()

	frame := &vm.frames[vm.frameCount-1]
	code := frame.closure.Proto.Code
	constants := frame.closure.Proto.Constants
	base := frame.base

	for {
		if frame.pc >= len(code) {
			// End of function - implicit return nil
			vm.closeUpvalues(base)
			if vm.frameCount <= initialFC {
				return nil, nil
			}
			// Inline return with no values
			vm.frameCount--
			rc := frame.resultCount
			rb := frame.resultBase
			if rc != 0 {
				nr := rc - 1
				for i := 0; i < nr; i++ {
					vm.regs[rb+i] = runtime.NilValue()
				}
			} else {
				vm.top = rb
			}
			frame = &vm.frames[vm.frameCount-1]
			code = frame.closure.Proto.Code
			constants = frame.closure.Proto.Constants
			base = frame.base
			continue
		}
		inst := code[frame.pc]
		frame.pc++

		// Trace recorder: only hook when actively recording (fast bool check)
		if vm.traceRecording {
			vm.traceRec.OnInstruction(frame.pc-1, inst, frame.closure.Proto, vm.regs, base)
		}

		op := DecodeOp(inst)

		switch op {
		case OP_LOADNIL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			for i := a; i <= a+b; i++ {
				vm.regs[base+i] = runtime.NilValue()
			}

		case OP_LOADBOOL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			vm.regs[base+a] = runtime.BoolValue(b != 0)
			if c != 0 {
				frame.pc++
			}

		case OP_LOADINT:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			vm.regs[base+a] = runtime.IntValue(int64(sbx))

		case OP_LOADK:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			vm.regs[base+a] = constants[bx]

		case OP_MOVE:
			a := DecodeA(inst)
			b := DecodeB(inst)
			vm.regs[base+a] = vm.regs[base+b]

		case OP_GETGLOBAL:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			// Lazy-init GlobalCache
			proto := frame.closure.Proto
			if proto.GlobalCache == nil {
				proto.GlobalCache = make([]globalCacheEntry, len(proto.Constants))
				for i := range proto.GlobalCache {
					proto.GlobalCache[i].index = -1
				}
			}
			cache := &proto.GlobalCache[bx]
			if cache.index >= 0 && cache.version == vm.globalVer {
				if vm.noGlobalLock {
					// Single-threaded: no lock needed
					vm.regs[base+a] = vm.globalArray[cache.index]
				} else {
					// Multi-threaded: use indexed array but lock for memory barrier
					vm.globalsMu.RLock()
					vm.regs[base+a] = vm.globalArray[cache.index]
					vm.globalsMu.RUnlock()
				}
			} else if vm.noGlobalLock {
				// Single-threaded cache miss: resolve + cache without lock
				name := constants[bx].Str()
				idx := vm.resolveGlobalIndex(name)
				cache.index = int32(idx)
				cache.version = vm.globalVer
				vm.regs[base+a] = vm.globalArray[idx]
			} else {
				// Multi-threaded cache miss: locked map fallback
				name := constants[bx].Str()
				vm.globalsMu.RLock()
				v := vm.globals[name]
				vm.globalsMu.RUnlock()
				vm.regs[base+a] = v
			}

		case OP_SETGLOBAL:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			val := vm.regs[base+a]
			if vm.noGlobalLock {
				// Single-threaded fast path
				proto := frame.closure.Proto
				if proto.GlobalCache == nil {
					proto.GlobalCache = make([]globalCacheEntry, len(proto.Constants))
					for i := range proto.GlobalCache {
						proto.GlobalCache[i].index = -1
					}
				}
				cache := &proto.GlobalCache[bx]
				name := constants[bx].Str()
				if cache.index >= 0 && cache.version == vm.globalVer {
					vm.globalArray[cache.index] = val
				} else {
					idx := vm.resolveGlobalIndex(name)
					cache.index = int32(idx)
					cache.version = vm.globalVer
					vm.globalArray[idx] = val
				}
				vm.globals[name] = val
			} else {
				// Multi-threaded: locked access, update both map and array
				name := constants[bx].Str()
				vm.globalsMu.Lock()
				vm.globals[name] = val
				if idx, ok := vm.globalIndex[name]; ok {
					vm.globalArray[idx] = val
				}
				vm.globalsMu.Unlock()
			}

		case OP_GETUPVAL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			vm.regs[base+a] = frame.closure.Upvalues[b].Get()

		case OP_SETUPVAL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			frame.closure.Upvalues[b].Set(vm.regs[base+a])

		case OP_NEWTABLE:
			a := DecodeA(inst)
			b := DecodeB(inst) // array hint
			c := DecodeC(inst) // hash hint
			vm.regs[base+a] = runtime.TableValue(runtime.NewTableSized(b, c))

		case OP_GETTABLE:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cidx := DecodeC(inst)
			tableVal := vm.regs[base+b]
			var key runtime.Value
			if cidx >= RKBit {
				key = constants[cidx-RKBit]
			} else {
				key = vm.regs[base+cidx]
			}
			// Fast path: plain table (no metatable)
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					vm.regs[base+a] = tbl.RawGet(key)
					break
				}
			}
			val, err := vm.tableGet(tableVal, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val

		case OP_SETTABLE:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			tableVal := vm.regs[base+a]
			var key, val runtime.Value
			if bidx >= RKBit {
				key = constants[bidx-RKBit]
			} else {
				key = vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				val = constants[cidx-RKBit]
			} else {
				val = vm.regs[base+cidx]
			}
			// Fast path: plain table
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					tbl.RawSet(key, val)
					break
				}
			}
			if err := vm.tableSet(tableVal, key, val); err != nil {
				return nil, err
			}

		case OP_GETFIELD:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			tableVal := vm.regs[base+b]
			// Fast path: plain table → direct string field lookup with inline cache
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					proto := frame.closure.Proto
					if proto.FieldCache == nil {
						proto.FieldCache = make([]runtime.FieldCacheEntry, len(proto.Code))
					}
					vm.regs[base+a] = tbl.RawGetStringCached(constants[c].Str(), &proto.FieldCache[frame.pc-1])
					break
				}
			}
			key := constants[c]
			val, err := vm.tableGet(tableVal, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val

		case OP_SETFIELD:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cidx := DecodeC(inst)
			tableVal := vm.regs[base+a]
			var val runtime.Value
			if cidx >= RKBit {
				val = constants[cidx-RKBit]
			} else {
				val = vm.regs[base+cidx]
			}
			// Fast path: plain table → direct string field set with inline cache
			if tableVal.IsTable() {
				if tbl := tableVal.Table(); tbl.GetMetatable() == nil {
					proto := frame.closure.Proto
					if proto.FieldCache == nil {
						proto.FieldCache = make([]runtime.FieldCacheEntry, len(proto.Code))
					}
					tbl.RawSetStringCached(constants[b].Str(), val, &proto.FieldCache[frame.pc-1])
					break
				}
			}
			key := constants[b]
			if err := vm.tableSet(tableVal, key, val); err != nil {
				return nil, err
			}

		case OP_SETLIST:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			t := vm.regs[base+a].Table()
			if t == nil {
				return nil, fmt.Errorf("SETLIST on non-table")
			}
			offset := (c - 1) * 50
			for i := 1; i <= b; i++ {
				t.RawSetInt(int64(offset+i), vm.regs[base+a+i])
			}

		case OP_APPEND:
			a := DecodeA(inst)
			b := DecodeB(inst)
			t := vm.regs[base+a].Table()
			if t == nil {
				return nil, fmt.Errorf("APPEND on non-table")
			}
			t.Append(vm.regs[base+b])

		// ---- Arithmetic ----
		case OP_ADD:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.AddNums(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__add", func(x, y float64) float64 { return x + y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_SUB:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.SubNums(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__sub", func(x, y float64) float64 { return x - y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_MUL:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.MulNums(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__mul", func(x, y float64) float64 { return x * y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_DIV:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			dst := &vm.regs[base+a]
			if !runtime.DivNums(dst, bp, cp) {
				r, err := vm.arith(*bp, *cp, "__div", func(x, y float64) float64 { return x / y })
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				*dst = r
			}

		case OP_MOD:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit { bv = constants[bidx-RKBit] } else { bv = vm.regs[base+bidx] }
			if cidx >= RKBit { cv = constants[cidx-RKBit] } else { cv = vm.regs[base+cidx] }
			r, err := vm.arithMod(bv, cv)
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_POW:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit { bv = constants[bidx-RKBit] } else { bv = vm.regs[base+bidx] }
			if cidx >= RKBit { cv = constants[cidx-RKBit] } else { cv = vm.regs[base+cidx] }
			r, err := vm.arith(bv, cv, "__pow", func(x, y float64) float64 { return math.Pow(x, y) })
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_UNM:
			a := DecodeA(inst)
			bv := vm.regs[base+DecodeB(inst)]
			r, err := vm.unaryMinus(bv)
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

		case OP_NOT:
			a := DecodeA(inst)
			bv := vm.regs[base+DecodeB(inst)]
			vm.regs[base+a] = runtime.BoolValue(!bv.Truthy())

		case OP_LEN:
			a := DecodeA(inst)
			bv := vm.regs[base+DecodeB(inst)]
			r, err := vm.length(bv)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = r

		case OP_CONCAT:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			var sb strings.Builder
			for i := b; i <= c; i++ {
				sb.WriteString(vm.regs[base+i].String())
			}
			vm.regs[base+a] = runtime.StringValue(sb.String())

		// ---- Comparison ----
		case OP_EQ:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			if bp.RawType() == runtime.TypeInt && cp.RawType() == runtime.TypeInt {
				if (bp.RawInt() == cp.RawInt()) != (a != 0) {
					frame.pc++
				}
			} else {
				if (*bp).Equal(*cp) != (a != 0) {
					frame.pc++
				}
			}

		case OP_LT:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			if lt, ok := runtime.LTInts(bp, cp); ok {
				if lt != (a != 0) {
					frame.pc++
				}
			} else {
				lt, ok := (*bp).LessThan(*cp)
				if !ok {
					return nil, fmt.Errorf("attempt to compare %s with %s", bp.TypeName(), cp.TypeName())
				}
				if lt != (a != 0) {
					frame.pc++
				}
			}

		case OP_LE:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bp, cp *runtime.Value
			if bidx >= RKBit {
				bp = &constants[bidx-RKBit]
			} else {
				bp = &vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cp = &constants[cidx-RKBit]
			} else {
				cp = &vm.regs[base+cidx]
			}
			if le, ok := runtime.LEInts(bp, cp); ok {
				if le != (a != 0) {
					frame.pc++
				}
			} else {
				lt, ok := (*cp).LessThan(*bp)
				if !ok {
					return nil, fmt.Errorf("attempt to compare %s with %s", bp.TypeName(), cp.TypeName())
				}
				if !lt != (a != 0) {
					frame.pc++
				}
			}

		// ---- Logical ----
		case OP_TEST:
			a := DecodeA(inst)
			c := DecodeC(inst)
			if vm.regs[base+a].Truthy() != (c != 0) {
				frame.pc++
			}

		case OP_TESTSET:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			bv := vm.regs[base+b]
			if bv.Truthy() != (c != 0) {
				frame.pc++
			} else {
				vm.regs[base+a] = bv
			}

		// ---- Jump ----
		case OP_JMP:
			sbx := DecodesBx(inst)
			jmpPC := frame.pc - 1 // PC of this JMP instruction
			frame.pc += sbx
			// While-loop back-edge detection: notify trace recorder on backward jumps.
			// Only checks when sbx < 0 (backward) and traceRec is non-nil (zero overhead otherwise).
			if sbx < 0 && vm.traceRec != nil && !frame.closure.Proto.IsTraceBlacklisted(jmpPC) {
				if vm.traceRec.OnLoopBackEdge(jmpPC, frame.closure.Proto) {
					tr := vm.executeCompiledTrace(frame.closure.Proto, base)
					if tr.executed {
						if tr.sideExit {
							frame.pc = tr.exitPC
						} else {
							frame.pc = jmpPC + 1
						}
					}
				}
				// Update cached recording state (OnLoopBackEdge may start/stop recording)
				vm.traceRecording = vm.traceRec.IsRecording()
			}

		// ---- Call / Return (INLINE) ----
		case OP_CALL:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)

			fnVal := vm.regs[base+a]
			nArgs := b - 1
			if b == 0 {
				nArgs = vm.top - (base + a + 1)
			}

			// ---- Fast path: VM Closure (inline call) ----
			if cl, ok := fnVal.Ptr().(*Closure); ok {
				proto := cl.Proto

				// Compute new base: after current frame's registers
				newBase := base + frame.closure.Proto.MaxStack
				if vm.top > newBase {
					newBase = vm.top
				}

				// Ensure register space
				needed := newBase + proto.MaxStack + 1
				if needed > len(vm.regs) {
					newRegs := make([]runtime.Value, needed*2)
					copy(newRegs, vm.regs)
					vm.regs = newRegs
				}

				// Copy args directly to new frame's registers
				nParams := proto.NumParams
				srcStart := base + a + 1
				for i := 0; i < nParams && i < nArgs; i++ {
					vm.regs[newBase+i] = vm.regs[srcStart+i]
				}
				for i := nArgs; i < nParams; i++ {
					vm.regs[newBase+i] = runtime.NilValue()
				}
				var varargs []runtime.Value
				if proto.IsVarArg && nArgs > nParams {
					varargs = make([]runtime.Value, nArgs-nParams)
					for i := range varargs {
						varargs[i] = vm.regs[srcStart+nParams+i]
					}
				}

				// Push new frame
				if vm.frameCount >= maxCallDepth {
					return nil, fmt.Errorf("stack overflow (max call depth %d)", maxCallDepth)
				}
				newFrame := &vm.frames[vm.frameCount]
				newFrame.closure = cl
				newFrame.pc = 0
				newFrame.base = newBase
				newFrame.varargs = varargs
				newFrame.resultBase = base + a
				newFrame.resultCount = c
				vm.frameCount++

				// Try JIT
				if vm.jit != nil && !proto.IsVarArg {
					proto.CallCount++
					results, resumePC, jitOK := vm.jit.TryExecute(proto, vm.regs, newBase, proto.CallCount)
					if jitOK {
						vm.closeUpvalues(newBase)
						vm.frameCount--
						// Place results (cached locals still point to caller)
						if c == 0 {
							for i, r := range results {
								vm.regs[base+a+i] = r
							}
							vm.top = base + a + len(results)
						} else {
							nr := c - 1
							for i := 0; i < nr; i++ {
								if i < len(results) {
									vm.regs[base+a+i] = results[i]
								} else {
									vm.regs[base+a+i] = runtime.NilValue()
								}
							}
						}
						break
					}
					if resumePC > 0 {
						newFrame.pc = resumePC
					}
				}

				// Switch to new frame (inline)
				frame = newFrame
				code = proto.Code
				constants = proto.Constants
				base = newBase
				continue
			}

			// ---- Fast path: GoFunction (direct call, skip callValue) ----
			if fnVal.IsFunction() {
				if gf := fnVal.GoFunction(); gf != nil {
					var args []runtime.Value
					if nArgs <= len(vm.argBuf) {
						args = vm.argBuf[:nArgs]
					} else {
						args = make([]runtime.Value, nArgs)
					}
					for i := 0; i < nArgs; i++ {
						args[i] = vm.regs[base+a+1+i]
					}
					results, err := gf.Fn(args)
					if err != nil {
						return nil, wrapLineErr(frame, err)
					}
					if c == 0 {
						for i, r := range results {
							vm.regs[base+a+i] = r
						}
						vm.top = base + a + len(results)
					} else {
						nr := c - 1
						for i := 0; i < nr; i++ {
							if i < len(results) {
								vm.regs[base+a+i] = results[i]
							} else {
								vm.regs[base+a+i] = runtime.NilValue()
							}
						}
					}
					break
				}
			}

			// ---- Slow path: __call metamethod, tree-walker closures, etc. ----
			var args []runtime.Value
			if nArgs <= len(vm.argBuf) {
				args = vm.argBuf[:nArgs]
			} else {
				args = make([]runtime.Value, nArgs)
			}
			for i := 0; i < nArgs; i++ {
				args[i] = vm.regs[base+a+1+i]
			}
			results, err := vm.callValue(fnVal, args)
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			if c == 0 {
				for i, r := range results {
					vm.regs[base+a+i] = r
				}
				vm.top = base + a + len(results)
			} else {
				nr := c - 1
				for i := 0; i < nr; i++ {
					if i < len(results) {
						vm.regs[base+a+i] = results[i]
					} else {
						vm.regs[base+a+i] = runtime.NilValue()
					}
				}
			}

		case OP_RETURN:
			a := DecodeA(inst)
			b := DecodeB(inst)

			vm.closeUpvalues(base)

			// Initial frame return → back to Go caller (call() will pop)
			if vm.frameCount <= initialFC {
				if b == 0 {
					nret := vm.top - (base + a)
					var ret []runtime.Value
					if nret <= len(vm.retBuf) {
						ret = vm.retBuf[:nret]
					} else {
						ret = make([]runtime.Value, nret)
					}
					for i := 0; i < nret; i++ {
						ret[i] = vm.regs[base+a+i]
					}
					return ret, nil
				}
				if b == 1 {
					return nil, nil
				}
				nret := b - 1
				var ret []runtime.Value
				if nret <= len(vm.retBuf) {
					ret = vm.retBuf[:nret]
				} else {
					ret = make([]runtime.Value, nret)
				}
				for i := 0; i < nret; i++ {
					ret[i] = vm.regs[base+a+i]
				}
				return ret, nil
			}

			// Inline sub-frame return
			vm.frameCount--

			resultBase := frame.resultBase
			resultCount := frame.resultCount

			var nret int
			if b == 0 {
				nret = vm.top - (base + a)
			} else if b == 1 {
				nret = 0
			} else {
				nret = b - 1
			}

			if resultCount == 0 {
				// Return all results
				for i := 0; i < nret; i++ {
					vm.regs[resultBase+i] = vm.regs[base+a+i]
				}
				vm.top = resultBase + nret
			} else {
				nr := resultCount - 1
				for i := 0; i < nr; i++ {
					if i < nret {
						vm.regs[resultBase+i] = vm.regs[base+a+i]
					} else {
						vm.regs[resultBase+i] = runtime.NilValue()
					}
				}
			}

			// Restore parent frame
			frame = &vm.frames[vm.frameCount-1]
			code = frame.closure.Proto.Code
			constants = frame.closure.Proto.Constants
			base = frame.base
			continue

		case OP_CLOSURE:
			a := DecodeA(inst)
			bx := DecodeBx(inst)
			subProto := frame.closure.Proto.Protos[bx]
			cl := &Closure{
				Proto:    subProto,
				Upvalues: make([]*Upvalue, len(subProto.Upvalues)),
			}
			for i, desc := range subProto.Upvalues {
				if desc.InStack {
					cl.Upvalues[i] = vm.findOrCreateUpvalue(base + desc.Index)
				} else {
					cl.Upvalues[i] = frame.closure.Upvalues[desc.Index]
				}
			}
			vm.regs[base+a] = runtime.FunctionValue(cl)

		case OP_CLOSE:
			a := DecodeA(inst)
			vm.closeUpvalues(base + a)

		// ---- Numeric For Loop ----
		case OP_FORPREP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			initV := vm.regs[base+a]
			stepV := vm.regs[base+a+2]
			if initV.IsInt() && stepV.IsInt() {
				vm.regs[base+a] = runtime.IntValue(initV.Int() - stepV.Int())
			} else {
				vm.regs[base+a] = runtime.FloatValue(initV.Number() - stepV.Number())
			}
			frame.pc += sbx

		case OP_FORLOOP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			idxP := &vm.regs[base+a]
			if idxP.RawType() == runtime.TypeInt {
				stepP := &vm.regs[base+a+2]
				limitP := &vm.regs[base+a+1]
				if stepP.RawType() == runtime.TypeInt && limitP.RawType() == runtime.TypeInt {
					step := stepP.RawInt()
					idx := idxP.RawInt() + step
					limit := limitP.RawInt()
					var cont bool
					if step > 0 {
						cont = idx <= limit
					} else {
						cont = idx >= limit
					}
					if cont {
						idxP.SetInt(idx)
						vm.regs[base+a+3].SetInt(idx)
						forloopPC := frame.pc - 1
						frame.pc += sbx
						// Trace: check for compiled trace.
						// Fast-path: skip OnLoopBackEdge if this FORLOOP PC is trace-blacklisted
						// (avoids ~30-50ns interface dispatch + map lookup per iteration).
						if vm.traceRec != nil && sbx < 0 && !frame.closure.Proto.IsTraceBlacklisted(forloopPC) {
							if vm.traceRec.OnLoopBackEdge(forloopPC, frame.closure.Proto) {
								tr := vm.executeCompiledTrace(frame.closure.Proto, base)
								if tr.executed {
									if tr.sideExit {
										frame.pc = tr.exitPC
									} else {
										frame.pc = forloopPC + 1
									}
								}
							}
							// Update cached recording state (OnLoopBackEdge may start/stop recording)
							vm.traceRecording = vm.traceRec.IsRecording()
						}
					}
					break
				}
			}
			step := vm.regs[base+a+2].Number()
			limit := vm.regs[base+a+1].Number()
			idx := vm.regs[base+a].Number() + step
			cont := false
			if step > 0 {
				cont = idx <= limit
			} else {
				cont = idx >= limit
			}
			if cont {
				if floatIsExactInt(idx) {
					vm.regs[base+a] = runtime.IntValue(int64(idx))
					vm.regs[base+a+3] = runtime.IntValue(int64(idx))
				} else {
					vm.regs[base+a] = runtime.FloatValue(idx)
					vm.regs[base+a+3] = runtime.FloatValue(idx)
				}
				frame.pc += sbx
			} else {
				vm.regs[base+a] = runtime.FloatValue(idx)
			}

		case OP_VARARG:
			a := DecodeA(inst)
			b := DecodeB(inst)
			va := frame.varargs
			if b == 0 {
				for i, v := range va {
					vm.regs[base+a+i] = v
				}
				vm.top = base + a + len(va)
			} else {
				n := b - 1
				for i := 0; i < n; i++ {
					if i < len(va) {
						vm.regs[base+a+i] = va[i]
					} else {
						vm.regs[base+a+i] = runtime.NilValue()
					}
				}
			}

		case OP_SELF:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cidx := DecodeC(inst)
			obj := vm.regs[base+b]
			vm.regs[base+a+1] = obj
			var key runtime.Value
			if cidx >= RKBit {
				key = constants[cidx-RKBit]
			} else {
				key = vm.regs[base+cidx]
			}
			val, err := vm.tableGet(obj, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val

		case OP_TFORCALL:
			a := DecodeA(inst)
			c := DecodeC(inst)
			fnVal := vm.regs[base+a]

			if fnVal.IsChannel() {
				ch := fnVal.Channel()
				val, ok := ch.Recv()
				if ok {
					vm.regs[base+a+3] = val
					for i := 1; i < c; i++ {
						vm.regs[base+a+3+i] = runtime.NilValue()
					}
				} else {
					for i := 0; i < c; i++ {
						vm.regs[base+a+3+i] = runtime.NilValue()
					}
				}
			} else {
				args := []runtime.Value{vm.regs[base+a+1], vm.regs[base+a+2]}
				results, err := vm.callValue(fnVal, args)
				if err != nil {
					return nil, err
				}
				for i := 0; i < c; i++ {
					if i < len(results) {
						vm.regs[base+a+3+i] = results[i]
					} else {
						vm.regs[base+a+3+i] = runtime.NilValue()
					}
				}
			}

		case OP_TFORLOOP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			if !vm.regs[base+a+1].IsNil() {
				vm.regs[base+a] = vm.regs[base+a+1]
				frame.pc += sbx
			}

		case OP_GO:
			// Mark shared table objects as concurrent and switch parent
			// to locked mode (prevents concurrent writes to globalIndex).
			if vm.noGlobalLock {
				vm.markGlobalTablesConcurrent()
				vm.noGlobalLock = false
			}

			a := DecodeA(inst)
			b := DecodeB(inst)
			fnVal := vm.regs[base+a]
			nArgs := b - 1
			if b == 0 {
				nArgs = vm.top - (base + a + 1)
			}
			args := make([]runtime.Value, nArgs)
			for i := 0; i < nArgs; i++ {
				args[i] = vm.regs[base+a+1+i]
			}
			go func(fn runtime.Value, goArgs []runtime.Value) {
				goVM := newIsolatedChildVM(vm)
				if cl, ok := fn.Ptr().(*Closure); ok {
					goVM.call(cl, goArgs, 0, 0)
				} else if gf := fn.GoFunction(); gf != nil {
					gf.Fn(goArgs)
				}
			}(fnVal, args)

		case OP_MAKECHAN:
			a := DecodeA(inst)
			b := DecodeB(inst)
			cc := DecodeC(inst)
			capacity := 0
			if cc == 1 {
				sizeVal := vm.regs[base+b]
				if sizeVal.IsInt() {
					capacity = int(sizeVal.Int())
				} else if sizeVal.IsFloat() {
					capacity = int(sizeVal.Float())
				}
			}
			ch := runtime.NewChannel(capacity)
			vm.regs[base+a] = runtime.ChannelValue(ch)

		case OP_SEND:
			a := DecodeA(inst)
			b := DecodeB(inst)
			chVal := vm.regs[base+a]
			if !chVal.IsChannel() {
				return nil, fmt.Errorf("send on non-channel value (got %s)", chVal.TypeName())
			}
			ch := chVal.Channel()
			val := vm.regs[base+b]
			if err := ch.Send(val); err != nil {
				return nil, err
			}

		case OP_RECV:
			a := DecodeA(inst)
			b := DecodeB(inst)
			chVal := vm.regs[base+b]
			if !chVal.IsChannel() {
				return nil, fmt.Errorf("receive from non-channel value (got %s)", chVal.TypeName())
			}
			ch := chVal.Channel()
			val, ok := ch.Recv()
			if ok {
				vm.regs[base+a] = val
			} else {
				vm.regs[base+a] = runtime.NilValue()
			}

		default:
			return nil, fmt.Errorf("unhandled opcode %d (%s)", op, OpName(op))
		}
	}
}

// callValue dispatches a function call (supports Closure, GoFunction, and __call metamethod).
func (vm *VM) callValue(fnVal runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	if fnVal.IsFunction() {
		if cl, ok := fnVal.Ptr().(*Closure); ok {
			newBase := vm.top
			if vm.frameCount > 0 {
				curFrame := &vm.frames[vm.frameCount-1]
				minBase := curFrame.base + curFrame.closure.Proto.MaxStack
				if newBase < minBase {
					newBase = minBase
				}
			}
			return vm.call(cl, args, newBase, -1)
		}
		if gf := fnVal.GoFunction(); gf != nil {
			return gf.Fn(args)
		}
		if c := fnVal.Closure(); c != nil {
			return nil, fmt.Errorf("cannot call tree-walker closure from VM")
		}
	}
	if fnVal.IsTable() {
		mt := fnVal.Table().GetMetatable()
		if mt != nil {
			callMM := mt.RawGet(runtime.StringValue("__call"))
			if !callMM.IsNil() {
				newArgs := make([]runtime.Value, len(args)+1)
				newArgs[0] = fnVal
				copy(newArgs[1:], args)
				return vm.callValue(callMM, newArgs)
			}
		}
	}
	return nil, fmt.Errorf("attempt to call a %s value", fnVal.TypeName())
}

// tableGet performs table access with __index metamethod support.
func (vm *VM) tableGet(t runtime.Value, key runtime.Value) (runtime.Value, error) {
	return vm.tableGetDepth(t, key, 0)
}

func (vm *VM) tableGetDepth(t runtime.Value, key runtime.Value, depth int) (runtime.Value, error) {
	if depth > maxMetaDepth {
		return runtime.NilValue(), fmt.Errorf("__index chain too deep")
	}

	if t.IsString() {
		if vm.stringMeta != nil {
			v := vm.stringMeta.RawGet(key)
			if !v.IsNil() {
				return v, nil
			}
		}
		return runtime.NilValue(), nil
	}

	if !t.IsTable() {
		if t.IsNil() && vm.frameCount > 0 {
			frame := &vm.frames[vm.frameCount-1]
			fmt.Printf("[DEBUG] attempt to index nil in %s pc=%d key=%v\n",
				frame.closure.Proto.Name, frame.pc, key)
		}
		return runtime.NilValue(), fmt.Errorf("attempt to index a %s value", t.TypeName())
	}

	tbl := t.Table()
	v := tbl.RawGet(key)
	if !v.IsNil() {
		return v, nil
	}

	mt := tbl.GetMetatable()
	if mt == nil {
		return runtime.NilValue(), nil
	}
	idx := mt.RawGet(runtime.StringValue("__index"))
	if idx.IsNil() {
		return runtime.NilValue(), nil
	}
	if idx.IsTable() {
		return vm.tableGetDepth(runtime.TableValue(idx.Table()), key, depth+1)
	}
	if idx.IsFunction() {
		results, err := vm.callValue(idx, []runtime.Value{t, key})
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return runtime.NilValue(), nil
	}
	return runtime.NilValue(), nil
}

// tableSet performs table assignment with __newindex metamethod support.
func (vm *VM) tableSet(t runtime.Value, key runtime.Value, val runtime.Value) error {
	if !t.IsTable() {
		return fmt.Errorf("attempt to index a %s value", t.TypeName())
	}
	tbl := t.Table()

	existing := tbl.RawGet(key)
	if existing.IsNil() {
		mt := tbl.GetMetatable()
		if mt != nil {
			ni := mt.RawGet(runtime.StringValue("__newindex"))
			if !ni.IsNil() {
				if ni.IsFunction() {
					_, err := vm.callValue(ni, []runtime.Value{t, key, val})
					return err
				}
				if ni.IsTable() {
					return vm.tableSet(runtime.TableValue(ni.Table()), key, val)
				}
			}
		}
	}

	tbl.RawSet(key, val)
	return nil
}

// ---- Arithmetic helpers ----

func (vm *VM) arith(a, b runtime.Value, metamethod string, op func(float64, float64) float64) (runtime.Value, error) {
	if a.IsInt() && b.IsInt() {
		switch metamethod {
		case "__add":
			return runtime.IntValue(a.Int() + b.Int()), nil
		case "__sub":
			return runtime.IntValue(a.Int() - b.Int()), nil
		case "__mul":
			return runtime.IntValue(a.Int() * b.Int()), nil
		case "__pow":
			return runtime.FloatValue(math.Pow(float64(a.Int()), float64(b.Int()))), nil
		}
	}
	if a.IsNumber() && b.IsNumber() {
		result := op(a.Number(), b.Number())
		if a.IsInt() && b.IsInt() && metamethod != "__div" && metamethod != "__pow" {
			if floatIsExactInt(result) {
				return runtime.IntValue(int64(result)), nil
			}
		}
		return runtime.FloatValue(result), nil
	}
	ac, aok := a.ToNumber()
	bc, bok := b.ToNumber()
	if aok && bok {
		return vm.arith(ac, bc, metamethod, op)
	}
	mm, err := vm.getMetamethod(a, b, metamethod)
	if err == nil && !mm.IsNil() {
		results, err := vm.callValue(mm, []runtime.Value{a, b})
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
		return runtime.NilValue(), nil
	}
	return runtime.NilValue(), fmt.Errorf("attempt to perform arithmetic on %s and %s", a.TypeName(), b.TypeName())
}

func (vm *VM) arithMod(a, b runtime.Value) (runtime.Value, error) {
	if a.IsInt() && b.IsInt() {
		bi := b.Int()
		if bi == 0 {
			return runtime.NilValue(), fmt.Errorf("attempt to perform 'n%%0'")
		}
		r := a.Int() % bi
		if r != 0 && (r^bi) < 0 {
			r += bi
		}
		return runtime.IntValue(r), nil
	}
	if a.IsNumber() && b.IsNumber() {
		bf := b.Number()
		if bf == 0 {
			return runtime.NilValue(), fmt.Errorf("attempt to perform 'n%%0'")
		}
		r := math.Mod(a.Number(), bf)
		if r != 0 && (r < 0) != (bf < 0) {
			r += bf
		}
		return runtime.FloatValue(r), nil
	}
	return vm.arith(a, b, "__mod", func(x, y float64) float64 { return math.Mod(x, y) })
}

func (vm *VM) unaryMinus(v runtime.Value) (runtime.Value, error) {
	if v.IsInt() {
		return runtime.IntValue(-v.Int()), nil
	}
	if v.IsFloat() {
		return runtime.FloatValue(-v.Float()), nil
	}
	if nv, ok := v.ToNumber(); ok {
		return vm.unaryMinus(nv)
	}
	mm, err := vm.getMetamethod(v, v, "__unm")
	if err == nil && !mm.IsNil() {
		results, err := vm.callValue(mm, []runtime.Value{v})
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(results) > 0 {
			return results[0], nil
		}
	}
	return runtime.NilValue(), fmt.Errorf("attempt to negate a %s value", v.TypeName())
}

func (vm *VM) length(v runtime.Value) (runtime.Value, error) {
	if v.IsString() {
		return runtime.IntValue(int64(len(v.Str()))), nil
	}
	if v.IsTable() {
		mt := v.Table().GetMetatable()
		if mt != nil {
			mm := mt.RawGet(runtime.StringValue("__len"))
			if !mm.IsNil() {
				results, err := vm.callValue(mm, []runtime.Value{v})
				if err != nil {
					return runtime.NilValue(), err
				}
				if len(results) > 0 {
					return results[0], nil
				}
				return runtime.IntValue(0), nil
			}
		}
		return runtime.IntValue(int64(v.Table().Len())), nil
	}
	return runtime.NilValue(), fmt.Errorf("attempt to get length of a %s value", v.TypeName())
}

func (vm *VM) getMetamethod(a, b runtime.Value, name string) (runtime.Value, error) {
	key := runtime.StringValue(name)
	if a.IsTable() {
		mt := a.Table().GetMetatable()
		if mt != nil {
			mm := mt.RawGet(key)
			if !mm.IsNil() {
				return mm, nil
			}
		}
	}
	if b.IsTable() {
		mt := b.Table().GetMetatable()
		if mt != nil {
			mm := mt.RawGet(key)
			if !mm.IsNil() {
				return mm, nil
			}
		}
	}
	return runtime.NilValue(), fmt.Errorf("no metamethod %s", name)
}

// markGlobalTablesConcurrent enables mutex on all top-level global tables.
// Called once when the first OP_GO goroutine is spawned.
func (vm *VM) markGlobalTablesConcurrent() {
	vm.globalsMu.Lock()
	for _, v := range vm.globals {
		if v.IsTable() {
			v.Table().SetConcurrent(true)
		}
	}
	vm.globalsMu.Unlock()
}

// ---- Upvalue management ----

func (vm *VM) findOrCreateUpvalue(regIdx int) *Upvalue {
	for _, uv := range vm.openUpvals {
		if uv.regIdx == regIdx {
			return uv
		}
	}
	uv := NewOpenUpvalue(&vm.regs[regIdx], regIdx)
	vm.openUpvals = append(vm.openUpvals, uv)
	return uv
}

func (vm *VM) closeUpvalues(fromReg int) {
	kept := vm.openUpvals[:0]
	for _, uv := range vm.openUpvals {
		if uv.regIdx >= fromReg {
			uv.Close()
		} else {
			kept = append(kept, uv)
		}
	}
	vm.openUpvals = kept
}

// ---- Helpers ----

func floatIsExactInt(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	return f == math.Trunc(f) && f >= math.MinInt64 && f <= math.MaxInt64
}

func init() {
}
