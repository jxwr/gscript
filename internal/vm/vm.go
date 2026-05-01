package vm

import (
	"fmt"
	"math"
	"sync"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

const (
	maxStack                 = 256    // max registers per call frame
	maxCallDepth             = 100000 // max call stack depth
	initialCallFrameCapacity = 64
	maxMetaDepth             = 50 // max __index chain depth
)

// MethodJITEngine is the interface for the Method JIT compiler.
// It compiles hot functions to native code and executes them.
type MethodJITEngine interface {
	TryCompile(proto *FuncProto) interface{} // returns *CompiledFunction or nil
	Execute(compiled interface{}, regs []runtime.Value, base int, proto *FuncProto) ([]runtime.Value, error)
	SetCallVM(v *VM) // sets the VM for call-exit/global-exit
}

type methodJITEngineWithResultBuffer interface {
	ExecuteWithResultBuffer(compiled interface{}, regs []runtime.Value, base int, proto *FuncProto, retBuf []runtime.Value) ([]runtime.Value, error)
}

// VM is the bytecode virtual machine.
type VM struct {
	regs               []runtime.Value          // register file (shared across frames via base offset)
	frames             []CallFrame              // call stack
	frameCount         int                      // current number of active frames
	globals            map[string]runtime.Value // legacy map (kept for interop)
	globalArray        []runtime.Value          // indexed globals (fast path)
	globalIndex        map[string]int           // name → index in globalArray
	globalVer          uint32                   // bumped on structural changes (new globals added)
	globalOverrides    map[string]runtime.Value // per-VM global overrides for coroutine-local builtins
	globalOverrideIdx  map[int]runtime.Value    // indexed mirror of globalOverrides for GETGLOBAL cache hits
	globalOverrideFast int                      // single indexed override fast path (-1 = disabled)
	globalOverrideVal  runtime.Value
	globalsMu          *sync.RWMutex  // protects globals for goroutine safety (shared across VMs)
	noGlobalLock       bool           // skip globals mutex (single-threaded mode)
	openUpvals         []*Upvalue     // list of open upvalues (sorted by regIdx descending)
	top                int            // top of used registers (for variable returns)
	stringMeta         *runtime.Table // string metatable
	methodJIT          MethodJITEngine
	argBuf             [16]runtime.Value // pre-allocated arg buffer for OP_CALL
	retBuf             [8]runtime.Value  // pre-allocated return buffer for OP_RETURN
	coroutineResultBuf [8]runtime.Value  // pre-allocated coroutine.resume result buffer
	wholeCallFloatBuf  []float64         // reusable non-pointer scratch for guarded whole-call kernels
	wholeCallIntBuf    []int64           // reusable non-pointer scratch for guarded whole-call kernels
	wholeCallValueBuf  []runtime.Value   // reusable Value scratch; scanned as GC roots below
	spectralKernel     spectralKernelCache
	currentCoroutine   *VMCoroutine // coroutine currently running on this VM, if any
	coroutineStats     *coroutineStats
	coroutineCreateFn  *runtime.GoFunction
	coroutineResumeFn  *runtime.GoFunction
	coroutineYieldFn   *runtime.GoFunction
}

// SetMethodJIT sets the Method JIT engine for this VM.
// When set, hot functions are automatically compiled and executed natively.
// Also sets the VM reference on the engine for call-exit support.
func (vm *VM) SetMethodJIT(engine MethodJITEngine) {
	vm.methodJIT = engine
	if engine != nil {
		engine.SetCallVM(vm)
	}
}

// Regs returns the register file. Used by the JIT executor.
func (vm *VM) Regs() []runtime.Value {
	return vm.regs
}

// SetTop sets the top-of-stack pointer. Used by the Method JIT to reserve
// register space for temp slots before executing calls via call-exit.
func (vm *VM) SetTop(top int) {
	vm.top = top
}

// Top returns the current top-of-stack pointer. Used by the baseline JIT
// to implement B=0 (variable args) in OP_CALL.
func (vm *VM) Top() int {
	return vm.top
}

// TopPtr returns a pointer to vm.top. Used by the JIT to read/write Top
// from native code for variable-arg (B=0) and variable-return (C=0) calls.
func (vm *VM) TopPtr() *int {
	return &vm.top
}

// CurrentClosure returns the closure for the current (topmost) call frame.
// Used by the baseline JIT to access upvalues. Returns nil if no frame is active.
func (vm *VM) CurrentClosure() *Closure {
	if vm.frameCount > 0 {
		return vm.frames[vm.frameCount-1].closure
	}
	return nil
}

// CurrentVarargs returns the varargs for the current (topmost) call frame.
// Used by the Tier 2 JIT to support OP_VARARG via exit-resume.
func (vm *VM) CurrentVarargs() []runtime.Value {
	if vm.frameCount > 0 {
		return vm.frames[vm.frameCount-1].varargs
	}
	return nil
}

// PushFrame pushes a minimal call frame for the given closure and base.
// Used by the baseline JIT's fast call path so that CurrentClosure() and
// CloseUpvalues() work correctly for the callee.
// Returns false if the call stack would overflow.
func (vm *VM) PushFrame(cl *Closure, base int) bool {
	if !vm.ensureFrameSlot() {
		return false
	}
	frame := &vm.frames[vm.frameCount]
	frame.closure = cl
	frame.pc = 0
	frame.base = base
	frame.numResults = -1
	frame.varargs = nil
	vm.frameCount++
	return true
}

func (vm *VM) ensureFrameSlot() bool {
	if vm.frameCount < len(vm.frames) {
		return true
	}
	if vm.frameCount >= maxCallDepth {
		return false
	}
	newLen := len(vm.frames) * 2
	if newLen == 0 {
		newLen = initialCallFrameCapacity
	}
	if newLen <= vm.frameCount {
		newLen = vm.frameCount + 1
	}
	if newLen > maxCallDepth {
		newLen = maxCallDepth
	}
	newFrames := make([]CallFrame, newLen)
	copy(newFrames, vm.frames)
	vm.frames = newFrames
	return true
}

// PopFrame removes the topmost call frame.
// Used by the baseline JIT's fast call path after callee execution.
func (vm *VM) PopFrame() {
	if vm.frameCount > 0 {
		vm.frameCount--
	}
}

// FrameCount returns the current call stack depth.
func (vm *VM) FrameCount() int {
	return vm.frameCount
}

// EnsureRegs ensures the register file has at least `needed` slots.
// If the register file is grown, returns the new slice.
func (vm *VM) EnsureRegs(needed int) []runtime.Value {
	if needed > len(vm.regs) {
		newRegs := runtime.MakeNilSlice(needed * 2)
		copy(newRegs, vm.regs)
		vm.regs = newRegs
	}
	return vm.regs
}

// Globals returns the globals map.
func (vm *VM) Globals() map[string]runtime.Value {
	return vm.globals
}

// GetGlobal reads a global variable with proper locking.
func (vm *VM) GetGlobal(name string) runtime.Value {
	if vm.globalOverrides != nil {
		if v, ok := vm.globalOverrides[name]; ok {
			return v
		}
	}
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

// PrepareTier2GlobalArray resolves the requested string constants as indexed
// globals and returns the data needed by the Tier 2 indexed-global fast path.
// The native path is enabled only for single-threaded VMs without per-VM
// overrides; other VM shapes fall back to the existing exit-resume protocol.
func (vm *VM) PrepareTier2GlobalArray(constants []runtime.Value, usedConsts map[int]bool) ([]int32, uintptr, *uint32, uint32, bool) {
	if vm == nil || !vm.noGlobalLock || vm.globalOverrides != nil {
		return nil, 0, nil, 0, false
	}
	indices := make([]int32, len(constants))
	for i := range indices {
		indices[i] = -1
	}
	for constIdx := range usedConsts {
		if constIdx < 0 || constIdx >= len(constants) {
			return nil, 0, nil, 0, false
		}
		c := constants[constIdx]
		if !c.IsString() {
			return nil, 0, nil, 0, false
		}
		idx := vm.resolveGlobalIndex(c.Str())
		indices[constIdx] = int32(idx)
	}
	if len(vm.globalArray) == 0 {
		return indices, 0, &vm.globalVer, vm.globalVer, true
	}
	return indices, uintptr(unsafe.Pointer(&vm.globalArray[0])), &vm.globalVer, vm.globalVer, true
}

// Tier2GlobalArrayState returns the current indexed-global backing pointer and
// version state for a previously prepared Tier 2 global index map.
func (vm *VM) Tier2GlobalArrayState() (uintptr, *uint32, uint32, bool) {
	if vm == nil || !vm.noGlobalLock || vm.globalOverrides != nil {
		return 0, nil, 0, false
	}
	if len(vm.globalArray) == 0 {
		return 0, &vm.globalVer, vm.globalVer, true
	}
	return uintptr(unsafe.Pointer(&vm.globalArray[0])), &vm.globalVer, vm.globalVer, true
}

// SyncTier2GlobalMap mirrors indexed global values back into the legacy globals
// map for names written natively by Tier 2. VM.GetGlobal reads globalArray, but
// the map is still part of the VM's public interop surface.
func (vm *VM) SyncTier2GlobalMap(constants []runtime.Value, indices []int32, constSet map[int]bool) {
	if vm == nil || len(indices) == 0 || len(constSet) == 0 {
		return
	}
	if vm.noGlobalLock {
		for constIdx := range constSet {
			if constIdx < 0 || constIdx >= len(constants) || constIdx >= len(indices) {
				continue
			}
			idx := int(indices[constIdx])
			if idx < 0 || idx >= len(vm.globalArray) || !constants[constIdx].IsString() {
				continue
			}
			vm.globals[constants[constIdx].Str()] = vm.globalArray[idx]
		}
		return
	}
	vm.globalsMu.Lock()
	for constIdx := range constSet {
		if constIdx < 0 || constIdx >= len(constants) || constIdx >= len(indices) {
			continue
		}
		idx := int(indices[constIdx])
		if idx < 0 || idx >= len(vm.globalArray) || !constants[constIdx].IsString() {
			continue
		}
		vm.globals[constants[constIdx].Str()] = vm.globalArray[idx]
	}
	vm.globalsMu.Unlock()
}

func (vm *VM) setGlobalOverride(name string, val runtime.Value) {
	if vm.globalOverrides == nil {
		vm.globalOverrides = make(map[string]runtime.Value, 1)
	}
	vm.globalOverrides[name] = val
	vm.globalOverrideFast = -1
	if vm.globalOverrideIdx == nil {
		vm.globalOverrideIdx = make(map[int]runtime.Value, 1)
	}
	if vm.noGlobalLock {
		if idx, ok := vm.globalIndex[name]; ok {
			vm.globalOverrideIdx[idx] = val
			if len(vm.globalOverrides) == 1 {
				vm.globalOverrideFast = idx
				vm.globalOverrideVal = val
			}
		}
		return
	}
	vm.globalsMu.RLock()
	idx, ok := vm.globalIndex[name]
	vm.globalsMu.RUnlock()
	if ok {
		vm.globalOverrideIdx[idx] = val
		if len(vm.globalOverrides) == 1 {
			vm.globalOverrideFast = idx
			vm.globalOverrideVal = val
		}
	}
}

// resolveGlobalIndex returns the globalArray index for a global name,
// creating a new entry if it doesn't exist.
func (vm *VM) resolveGlobalIndex(name string) int {
	if idx, ok := vm.globalIndex[name]; ok {
		return idx
	}
	// New global — add to array
	idx := len(vm.globalArray)
	val, ok := vm.globals[name]
	if !ok {
		val = runtime.NilValue()
	}
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
		regs:               runtime.MakeNilSlice(1024),
		frames:             make([]CallFrame, initialCallFrameCapacity),
		globals:            globals,
		globalArray:        ga,
		globalIndex:        gi,
		globalOverrideFast: -1,
		globalsMu:          &sync.RWMutex{},
		noGlobalLock:       true, // single-threaded by default
	}
	v.RegisterCoroutineLib()
	v.registerChannelBuiltins()
	runtime.RegisterVM(v)
	return v
}

// Close unregisters this VM from the GC root scanner.
// Should be called when the VM is no longer needed.
func (vm *VM) Close() {
	runtime.UnregisterVM(vm)
}

// ScanGCRoots implements runtime.GCRootScanner. It visits all live GC root
// pointers reachable from this VM: registers, globals, open upvalues, call
// frame closures, proto constants, and recursively all table contents.
func (vm *VM) ScanGCRoots(visitor func(unsafe.Pointer)) {
	seen := make(map[uintptr]struct{}, 256)
	seenProtos := make(map[*FuncProto]struct{}, 32)

	// Scan the entire register file conservatively.
	// The previous optimization (capping at frames[-1].base + maxStack) missed
	// registers used by JIT self-calls: the JIT advances mRegRegs by calleeBaseOff
	// per recursive level without pushing vm.frames entries.  A table referenced
	// only from a deep self-call register would be invisible to gcCompact, causing
	// premature eviction from gcLog and subsequent use-after-free crashes.
	// Scanning all registers is safe: nil/float/int values return immediately in
	// ScanValueRoots, and old stale pointers in unused slots keep their referents
	// alive until those slots are overwritten — a minor delay in GC, not a leak.
	for i := 0; i < len(vm.regs); i++ {
		runtime.ScanValueRoots(vm.regs[i], visitor, seen)
	}

	// Scan globals array.
	for _, v := range vm.globalArray {
		runtime.ScanValueRoots(v, visitor, seen)
	}

	// Scan open upvalues (closed upvalues point into registers already scanned).
	for _, uv := range vm.openUpvals {
		if uv != nil {
			runtime.ScanValueRoots(uv.Get(), visitor, seen)
		}
	}

	// Scan call frame closures, their upvalues, and their proto constants.
	for i := 0; i < vm.frameCount; i++ {
		f := &vm.frames[i]
		if f.closure != nil {
			for _, uv := range f.closure.Upvalues {
				if uv != nil && !uv.open {
					runtime.ScanValueRoots(uv.Get(), visitor, seen)
				}
			}
			// Scan the proto's constants and nested protos recursively.
			scanProtoRoots(f.closure.Proto, visitor, seen, seenProtos)
		}
		// Scan varargs.
		for _, v := range f.varargs {
			runtime.ScanValueRoots(v, visitor, seen)
		}
	}

	// Scan call/result scratch buffers that may contain live values from recent calls.
	for _, v := range vm.argBuf {
		runtime.ScanValueRoots(v, visitor, seen)
	}
	for _, v := range vm.retBuf {
		runtime.ScanValueRoots(v, visitor, seen)
	}
	for _, v := range vm.coroutineResultBuf {
		runtime.ScanValueRoots(v, visitor, seen)
	}
	for _, v := range vm.wholeCallValueBuf {
		runtime.ScanValueRoots(v, visitor, seen)
	}

	// Scan string metatable.
	if vm.stringMeta != nil {
		mp := unsafe.Pointer(vm.stringMeta)
		if _, already := seen[uintptr(mp)]; !already {
			seen[uintptr(mp)] = struct{}{}
			visitor(mp)
			runtime.ScanTableRootsExported(vm.stringMeta, visitor, seen)
		}
	}
}

// scanProtoRoots scans a FuncProto's constants and recursively its children.
func scanProtoRoots(proto *FuncProto, visitor func(unsafe.Pointer), seen map[uintptr]struct{}, seenProtos map[*FuncProto]struct{}) {
	if proto == nil {
		return
	}
	if _, already := seenProtos[proto]; already {
		return
	}
	seenProtos[proto] = struct{}{}

	// Scan constants (contains string literals, function values, etc.)
	for _, v := range proto.Constants {
		runtime.ScanValueRoots(v, visitor, seen)
	}

	// Recursively scan nested function prototypes.
	for _, child := range proto.Protos {
		scanProtoRoots(child, visitor, seen, seenProtos)
	}
}

// newChildVM creates a child VM that shares globals with the parent.
// Used by coroutines which need to see the caller's global state.
func newChildVM(parent *VM, co *VMCoroutine) *VM {
	child := &VM{
		regs:               runtime.MakeNilSlice(1024),
		frames:             make([]CallFrame, initialCallFrameCapacity),
		globals:            parent.globals,
		globalArray:        parent.globalArray,
		globalIndex:        parent.globalIndex,
		globalVer:          parent.globalVer,
		globalOverrideFast: -1,
		globalsMu:          parent.globalsMu,
		noGlobalLock:       false, // shared globals, must lock
		stringMeta:         parent.stringMeta,
		currentCoroutine:   co,
		coroutineStats:     parent.coroutineStats,
	}
	child.setGlobalOverride("coroutine", runtime.TableValue(child.newCoroutineLib()))
	runtime.RegisterVM(child)
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
		regs:               runtime.MakeNilSlice(1024),
		frames:             make([]CallFrame, initialCallFrameCapacity),
		globals:            childGlobals,
		globalArray:        ga,
		globalIndex:        gi,
		globalVer:          parent.globalVer,
		globalOverrideFast: -1,
		globalsMu:          &sync.RWMutex{},
		noGlobalLock:       true, // own copy, fully lock-free
		stringMeta:         parent.stringMeta,
		coroutineStats:     parent.coroutineStats,
	}
	child.RegisterCoroutineLib()
	runtime.RegisterVM(child)
	return child
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

func (vm *VM) executeMethodJIT(compiled interface{}, regs []runtime.Value, base int, proto *FuncProto) ([]runtime.Value, error) {
	if exec, ok := vm.methodJIT.(methodJITEngineWithResultBuffer); ok {
		return exec.ExecuteWithResultBuffer(compiled, regs, base, proto, vm.retBuf[:0])
	}
	return vm.methodJIT.Execute(compiled, regs, base, proto)
}

// call pushes a new call frame and executes.
func (vm *VM) call(cl *Closure, args []runtime.Value, base int, numResults int) ([]runtime.Value, error) {
	// GC safe point at function entry: all caller's register writes are complete.
	runtime.CheckGC()

	proto := cl.Proto

	// Ensure register space
	needed := base + proto.MaxStack + 1
	if needed > len(vm.regs) {
		newRegs := runtime.MakeNilSlice(needed * 2)
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
	if !vm.ensureFrameSlot() {
		return nil, fmt.Errorf("stack overflow (max call depth %d)", maxCallDepth)
	}
	frame := &vm.frames[vm.frameCount]
	frame.closure = cl
	frame.pc = 0
	frame.base = base
	frame.numResults = numResults
	frame.varargs = varargs
	vm.frameCount++

	// Method JIT: check for compiled function.
	if vm.methodJIT != nil && !proto.IsVarArg && !proto.JITDisabled {
		proto.CallCount++
		if compiled := vm.methodJIT.TryCompile(proto); compiled != nil {
			results, err := vm.executeMethodJIT(compiled, vm.regs, base, proto)
			if err == nil {
				vm.closeUpvalues(base)
				vm.frameCount--
				return results, nil
			}
			// Method JIT execution failed; fall through to interpreter.
		}
	}

	result, err := vm.run()
	if err == errCoroutineYield {
		return result, err
	}
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
	coroutineChild := vm.currentCoroutine != nil
	if coroutineChild && initialFC > 1 {
		initialFC = 1
	}

	// On error, reset frame count to clean up any inline sub-frames.
	if !coroutineChild {
		defer func() {
			if retErr != nil && retErr != errCoroutineYield {
				vm.frameCount = initialFC
			}
		}()
	}

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
				if int(cache.index) == vm.globalOverrideFast {
					vm.regs[base+a] = vm.globalOverrideVal
					break
				}
				if vm.globalOverrideIdx != nil {
					if v, ok := vm.globalOverrideIdx[int(cache.index)]; ok {
						vm.regs[base+a] = v
						break
					}
				}
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
				if vm.globalOverrides != nil {
					if v, ok := vm.globalOverrides[name]; ok {
						vm.globalOverrideIdx[idx] = v
						vm.regs[base+a] = v
						break
					}
				}
				vm.regs[base+a] = vm.globalArray[idx]
			} else {
				// Multi-threaded cache miss: locked map fallback
				name := constants[bx].Str()
				if vm.globalOverrides != nil {
					if ov, ok := vm.globalOverrides[name]; ok {
						vm.globalsMu.RLock()
						idx, hasIdx := vm.globalIndex[name]
						ver := vm.globalVer
						vm.globalsMu.RUnlock()
						if hasIdx {
							cache.index = int32(idx)
							cache.version = ver
							vm.globalOverrideIdx[idx] = ov
						}
						vm.regs[base+a] = ov
						break
					}
				}
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
			vm.regs[base+a] = runtime.FreshTableValue(runtime.NewTableSized(b, c))

		case OP_NEWOBJECT2:
			a := DecodeA(inst)
			b := DecodeB(inst) // table ctor index
			c := DecodeC(inst) // first value register
			if b >= 0 && b < len(frame.closure.Proto.TableCtors2) {
				ctor := &frame.closure.Proto.TableCtors2[b].Runtime
				val1 := vm.regs[base+c]
				val2 := vm.regs[base+c+1]
				vm.regs[base+a] = runtime.FreshTableValue(runtime.NewTableFromCtor2(ctor, val1, val2))
			} else {
				vm.regs[base+a] = runtime.FreshTableValue(runtime.NewTableSized(0, 2))
			}

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
					if key.IsString() {
						proto := frame.closure.Proto
						if proto.TableStringKeyCache == nil {
							proto.TableStringKeyCache = make([]runtime.TableStringKeyCacheEntry, len(proto.Code)*runtime.TableStringKeyCacheWays)
						}
						vm.regs[base+a] = tbl.RawGetStringDynamicCached(
							key.Str(),
							runtime.TableStringKeyCacheSlot(proto.TableStringKeyCache, frame.pc-1),
						)
					} else {
						vm.regs[base+a] = tbl.RawGet(key)
					}
					if frame.closure.Proto.Feedback != nil {
						fb := &frame.closure.Proto.Feedback[frame.pc-1]
						fb.Left.Observe(tableVal.Type())
						fb.Right.Observe(key.Type())
						fb.Result.Observe(vm.regs[base+a].Type())
						if frame.closure.Proto.TableKeyFeedback != nil {
							tkf := &frame.closure.Proto.TableKeyFeedback[frame.pc-1]
							tkf.ObserveIntKey(key)
							tkf.ObserveDenseMatrix(tbl)
						}
					}
					break
				}
			}
			val, err := vm.tableGet(tableVal, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(tableVal.Type())
				fb.Right.Observe(key.Type())
				fb.Result.Observe(val.Type())
				if frame.closure.Proto.TableKeyFeedback != nil {
					tkf := &frame.closure.Proto.TableKeyFeedback[frame.pc-1]
					tkf.ObserveIntKey(key)
					if tableVal.IsTable() {
						tkf.ObserveDenseMatrix(tableVal.Table())
					}
				}
			}

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
					if key.IsString() {
						proto := frame.closure.Proto
						if proto.TableStringKeyCache == nil {
							proto.TableStringKeyCache = make([]runtime.TableStringKeyCacheEntry, len(proto.Code)*runtime.TableStringKeyCacheWays)
						}
						tbl.RawSetStringDynamicCached(
							key.Str(),
							val,
							runtime.TableStringKeyCacheSlot(proto.TableStringKeyCache, frame.pc-1),
						)
					} else {
						tbl.RawSet(key, val)
					}
					if frame.closure.Proto.Feedback != nil {
						fb := &frame.closure.Proto.Feedback[frame.pc-1]
						fb.Left.Observe(tableVal.Type())
						fb.Right.Observe(key.Type())
						fb.Result.Observe(val.Type())
						if frame.closure.Proto.TableKeyFeedback != nil {
							frame.closure.Proto.TableKeyFeedback[frame.pc-1].ObserveIntKey(key)
						}
					}
					break
				}
			}
			if err := vm.tableSet(tableVal, key, val); err != nil {
				return nil, err
			}
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(tableVal.Type())
				fb.Right.Observe(key.Type())
				fb.Result.Observe(val.Type())
				if frame.closure.Proto.TableKeyFeedback != nil {
					frame.closure.Proto.TableKeyFeedback[frame.pc-1].ObserveIntKey(key)
				}
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
					if proto.Feedback != nil {
						fb := &proto.Feedback[frame.pc-1]
						fb.Result.Observe(vm.regs[base+a].Type())
					}
					break
				}
			}
			key := constants[c]
			val, err := vm.tableGet(tableVal, key)
			if err != nil {
				return nil, err
			}
			vm.regs[base+a] = val
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Result.Observe(val.Type())
			}

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
					if proto.Feedback != nil {
						fb := &proto.Feedback[frame.pc-1]
						fb.Result.Observe(val.Type())
					}
					break
				}
			}
			key := constants[b]
			if err := vm.tableSet(tableVal, key, val); err != nil {
				return nil, err
			}
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Result.Observe(val.Type())
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
				fb.Result.Observe(dst.Type())
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
				fb.Result.Observe(dst.Type())
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
				fb.Result.Observe(dst.Type())
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
				fb.Result.Observe(dst.Type())
			}

		case OP_MOD:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit {
				bv = constants[bidx-RKBit]
			} else {
				bv = vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cv = constants[cidx-RKBit]
			} else {
				cv = vm.regs[base+cidx]
			}
			r, err := vm.arithMod(bv, cv)
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bv.Type())
				fb.Right.Observe(cv.Type())
				fb.Result.Observe(r.Type())
			}

		case OP_POW:
			a := DecodeA(inst)
			bidx := DecodeB(inst)
			cidx := DecodeC(inst)
			var bv, cv runtime.Value
			if bidx >= RKBit {
				bv = constants[bidx-RKBit]
			} else {
				bv = vm.regs[base+bidx]
			}
			if cidx >= RKBit {
				cv = constants[cidx-RKBit]
			} else {
				cv = vm.regs[base+cidx]
			}
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Result.Observe(r.Type())
			}

		case OP_CONCAT:
			a := DecodeA(inst)
			b := DecodeB(inst)
			c := DecodeC(inst)
			r, err := vm.ConcatValues(vm.regs[base+b : base+c+1])
			if err != nil {
				return nil, wrapLineErr(frame, err)
			}
			vm.regs[base+a] = r

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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
			}
			if bp.RawType() == runtime.TypeInt && cp.RawType() == runtime.TypeInt {
				if (bp.RawInt() == cp.RawInt()) != (a != 0) {
					frame.pc++
				}
			} else if eq, ok := runtime.EQStrings(bp, cp); ok {
				if eq != (a != 0) {
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
			}
			if lt, ok := runtime.LTInts(bp, cp); ok {
				if lt != (a != 0) {
					frame.pc++
				}
			} else if lt, ok := runtime.LTStrings(bp, cp); ok {
				if lt != (a != 0) {
					frame.pc++
				}
			} else {
				lt, ok := (*bp).LessThan(*cp)
				if !ok {
					return nil, fmt.Errorf("attempt to compare %s with %s at pc=%d B=%d C=%d bp=0x%x cp=0x%x", bp.TypeName(), cp.TypeName(), frame.pc-1, bidx, cidx, uint64(*bp), uint64(*cp))
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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(bp.Type())
				fb.Right.Observe(cp.Type())
			}
			if le, ok := runtime.LEInts(bp, cp); ok {
				if le != (a != 0) {
					frame.pc++
				}
			} else if le, ok := runtime.LEStrings(bp, cp); ok {
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
			frame.pc += sbx

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
			if frame.closure.Proto.Feedback != nil {
				fb := &frame.closure.Proto.Feedback[frame.pc-1]
				fb.Left.Observe(fnVal.Type())
			}

			if fnVal.IsFunction() {
				if gf := fnVal.GoFunction(); gf != nil {
					if handled, err := vm.tryFastCoroutineCall(gf, base, a, nArgs, c); handled {
						if err != nil {
							if err == errCoroutineYield {
								return nil, err
							}
							return nil, wrapLineErr(frame, err)
						}
						break
					}
				}
			}

			// GC safe point at ordinary function call boundaries. VM-native
			// coroutine intrinsics above only rearrange already-rooted VM state.
			runtime.CheckGC()

			// Fixed-arity self recursion is common in Tier0-only table walkers
			// such as tree constructors/traversals. When the global lookup still
			// resolved to the active closure, enter the next VM frame directly
			// without the generic closure dispatch, vararg setup, or JIT probe.
			if b != 0 {
				proto := frame.closure.Proto
				if !proto.IsVarArg && nArgs == proto.NumParams && (vm.methodJIT == nil || proto.JITDisabled) {
					if p := fnVal.VMClosurePointer(); p == unsafe.Pointer(frame.closure) {
						newBase := base + proto.MaxStack
						if vm.top > newBase {
							newBase = vm.top
						}
						needed := newBase + proto.MaxStack + 1
						if needed > len(vm.regs) {
							newRegs := runtime.MakeNilSlice(needed * 2)
							copy(newRegs, vm.regs)
							vm.regs = newRegs
						}

						srcStart := base + a + 1
						switch nArgs {
						case 0:
						case 1:
							vm.regs[newBase] = vm.regs[srcStart]
						case 2:
							vm.regs[newBase] = vm.regs[srcStart]
							vm.regs[newBase+1] = vm.regs[srcStart+1]
						default:
							for i := 0; i < nArgs; i++ {
								vm.regs[newBase+i] = vm.regs[srcStart+i]
							}
						}

						if !vm.ensureFrameSlot() {
							return nil, fmt.Errorf("stack overflow (max call depth %d)", maxCallDepth)
						}
						newFrame := &vm.frames[vm.frameCount]
						newFrame.closure = frame.closure
						newFrame.pc = 0
						newFrame.base = newBase
						newFrame.numResults = 0
						newFrame.varargs = nil
						newFrame.resultBase = base + a
						newFrame.resultCount = c
						vm.frameCount++

						frame = newFrame
						code = proto.Code
						constants = proto.Constants
						base = newBase
						continue
					}
				}
			}

			// ---- Fast path: VM Closure (inline call) ----
			if cl, ok := closureFromValue(fnVal); ok {
				if b != 0 && nArgs == 1 {
					handled, err := vm.tryRecursiveTableBuildFoldRegion(frame, base, cl, a, nArgs, c)
					if handled {
						if err != nil {
							return nil, wrapLineErr(frame, err)
						}
						break
					}
				}
				if b != 0 && wholeCallKernelArity(nArgs) {
					args := vm.regs[base+a+1 : base+a+1+nArgs]
					handled, err := vm.tryValueWholeCallKernel(cl, args, c, base+a)
					if handled {
						if err != nil {
							return nil, wrapLineErr(frame, err)
						}
						break
					}
					handled, err = vm.tryWholeCallKernel(cl, args, c, base+a)
					if handled {
						if err != nil {
							return nil, wrapLineErr(frame, err)
						}
						break
					}
				}

				proto := cl.Proto

				// Compute new base: after current frame's registers
				newBase := base + frame.closure.Proto.MaxStack
				if vm.top > newBase {
					newBase = vm.top
				}

				// Ensure register space
				needed := newBase + proto.MaxStack + 1
				if needed > len(vm.regs) {
					newRegs := runtime.MakeNilSlice(needed * 2)
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
				if !vm.ensureFrameSlot() {
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

				// Method JIT: check for compiled function
				if vm.methodJIT != nil && !proto.IsVarArg && !proto.JITDisabled {
					proto.CallCount++
					if compiled := vm.methodJIT.TryCompile(proto); compiled != nil {
						results, err := vm.executeMethodJIT(compiled, vm.regs, newBase, proto)
						if err == nil {
							vm.closeUpvalues(newBase)
							vm.frameCount--
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
						// Compilation or execution failed; fall through to interpreter.
					}
				}

				// Switch to new frame (inline).
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
					if gf.Fast1 != nil {
						result, err := gf.Fast1(args)
						if err != nil {
							return nil, wrapLineErr(frame, err)
						}
						if c == 0 {
							vm.regs[base+a] = result
							vm.top = base + a + 1
						} else {
							nr := c - 1
							if nr > 0 {
								vm.regs[base+a] = result
								for i := 1; i < nr; i++ {
									vm.regs[base+a+i] = runtime.NilValue()
								}
							}
						}
						break
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
			cl := NewClosure(subProto)
			switch len(subProto.Upvalues) {
			case 0:
			case 1:
				desc := subProto.Upvalues[0]
				if desc.InStack {
					cl.Upvalues[0] = vm.findOrCreateUpvalue(base + desc.Index)
				} else {
					cl.Upvalues[0] = frame.closure.Upvalues[desc.Index]
				}
			default:
				for i, desc := range subProto.Upvalues {
					if desc.InStack {
						cl.Upvalues[i] = vm.findOrCreateUpvalue(base + desc.Index)
					} else {
						cl.Upvalues[i] = frame.closure.Upvalues[desc.Index]
					}
				}
			}
			vm.regs[base+a] = runtime.VMClosureFastValue(unsafe.Pointer(cl))

		case OP_CLOSE:
			a := DecodeA(inst)
			vm.closeUpvalues(base + a)

		// ---- Numeric For Loop ----
		case OP_FORPREP:
			a := DecodeA(inst)
			sbx := DecodesBx(inst)
			if handled, err := vm.tryPrimePredicateSumForLoopKernel(frame, base, code, constants, a, sbx); handled {
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				break
			}
			if handled, err := vm.tryNBodyAdvanceForLoopKernel(frame, base, code, constants, a, sbx); handled {
				if err != nil {
					return nil, wrapLineErr(frame, err)
				}
				break
			}
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
						idxP.SetIntUnchecked(idx)
						vm.regs[base+a+3].SetIntUnchecked(idx)
						frame.pc += sbx
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
				vm.globalVer++
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
				if cl, ok := closureFromValue(fn); ok {
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

func (vm *VM) tryFastCoroutineCall(gf *runtime.GoFunction, base, a, nArgs, c int) (bool, error) {
	if gf.NativeKind == goFunctionKindCoroutineWrapper {
		co, ok := vmCoroutineFromNativeData(gf.NativeData)
		if !ok {
			return true, fmt.Errorf("invalid wrapped coroutine")
		}
		if co.status == VMCoroutineDead {
			return true, fmt.Errorf("cannot resume dead coroutine")
		}
		var args []runtime.Value
		if nArgs > 0 {
			start := base + a + 1
			args = vm.regs[start : start+nArgs]
		}
		okResult, values, err := vm.resumeCoroutineRaw(co, args)
		if err != nil {
			return true, err
		}
		if !okResult {
			if len(values) > 0 {
				return true, fmt.Errorf("%s", values[0].String())
			}
			return true, fmt.Errorf("cannot resume dead coroutine")
		}
		if len(values) == 0 {
			vm.writeSingleCallResult(base+a, c, runtime.NilValue())
			return true, nil
		}
		vm.writeCallResults(base+a, c, values)
		return true, nil
	}

	if gf == vm.coroutineCreateFn || gf.Name == coroutineCreateName {
		if nArgs < 1 || !vm.regs[base+a+1].IsFunction() {
			return true, fmt.Errorf("coroutine.create expects a function")
		}
		cl, ok := closureFromValue(vm.regs[base+a+1])
		if !ok {
			return true, fmt.Errorf("coroutine.create expects a GScript function, got Go function")
		}
		co := NewVMCoroutine(cl)
		vm.recordCoroutineCreated(false)
		vm.writeSingleCallResult(base+a, c, runtime.VMCoroutineValue(unsafe.Pointer(co), co))
		return true, nil
	}

	if gf == vm.coroutineResumeFn || gf.Name == coroutineResumeName {
		if nArgs < 1 || !vm.regs[base+a+1].IsCoroutine() {
			return true, fmt.Errorf("coroutine.resume expects a coroutine")
		}
		co, ok := vmCoroutineFromValue(vm.regs[base+a+1])
		if !ok {
			return true, fmt.Errorf("coroutine.resume expects a VM coroutine")
		}
		var args []runtime.Value
		if nArgs > 1 {
			start := base + a + 2
			args = vm.regs[start : start+nArgs-1]
		}
		okResult, values, err := vm.resumeCoroutineRaw(co, args)
		if err != nil {
			return true, err
		}
		vm.writeCoroutineResumeResults(base+a, c, okResult, values)
		return true, nil
	}

	if gf == vm.coroutineYieldFn || gf.Name == coroutineYieldName {
		var args []runtime.Value
		if nArgs > 0 {
			start := base + a + 1
			args = vm.regs[start : start+nArgs]
		}
		if co := vm.currentCoroutine; co != nil {
			vm.recordCoroutineYield()
			co.yieldResult = vmYieldResult{values: args}
			co.yieldDst = base + a
			co.yieldC = c
			return true, errCoroutineYield
		}
		results, err := vm.yieldCoroutine(args)
		if err != nil {
			return true, err
		}
		vm.writeCallResults(base+a, c, results)
		return true, nil
	}

	if gf.Name == coroutineIsYieldableName {
		vm.writeSingleCallResult(base+a, c, runtime.BoolValue(vm.activeCoroutine() != nil))
		return true, nil
	}

	return false, nil
}

func (vm *VM) writeSingleCallResult(dst, c int, result runtime.Value) {
	if c == 0 {
		vm.regs[dst] = result
		vm.top = dst + 1
		return
	}
	if c == 1 {
		return
	}
	vm.regs[dst] = result
	for i := 1; i < c-1; i++ {
		vm.regs[dst+i] = runtime.NilValue()
	}
}

func (vm *VM) writeCoroutineResumeResults(dst, c int, ok bool, values []runtime.Value) {
	if c == 0 {
		vm.regs[dst] = runtime.BoolValue(ok)
		for i, r := range values {
			vm.regs[dst+1+i] = r
		}
		vm.top = dst + 1 + len(values)
		return
	}
	if c == 3 && len(values) == 1 {
		vm.regs[dst] = runtime.BoolValue(ok)
		vm.regs[dst+1] = values[0]
		return
	}
	if c == 2 && len(values) == 0 {
		vm.regs[dst] = runtime.BoolValue(ok)
		return
	}
	nr := c - 1
	for i := 0; i < nr; i++ {
		switch {
		case i == 0:
			vm.regs[dst] = runtime.BoolValue(ok)
		case i-1 < len(values):
			vm.regs[dst+i] = values[i-1]
		default:
			vm.regs[dst+i] = runtime.NilValue()
		}
	}
}

func (vm *VM) writeCallResults(dst, c int, results []runtime.Value) {
	if c == 0 {
		for i, r := range results {
			vm.regs[dst+i] = r
		}
		vm.top = dst + len(results)
		return
	}
	if c == 1 {
		return
	}
	if c == 2 {
		if len(results) > 0 {
			vm.regs[dst] = results[0]
		} else {
			vm.regs[dst] = runtime.NilValue()
		}
		return
	}
	nr := c - 1
	for i := 0; i < nr; i++ {
		if i < len(results) {
			vm.regs[dst+i] = results[i]
		} else {
			vm.regs[dst+i] = runtime.NilValue()
		}
	}
}

// callValue dispatches a function call (supports Closure, GoFunction, and __call metamethod).
func (vm *VM) callValue(fnVal runtime.Value, args []runtime.Value) ([]runtime.Value, error) {
	if fnVal.IsFunction() {
		if cl, ok := closureFromValue(fnVal); ok {
			if wholeCallKernelArity(len(args)) {
				if handled, results, err := vm.tryRunRawIntNestedValueKernel(cl, args); handled {
					return results, err
				}
				if handled, results, err := vm.tryRunNonRecursiveTableValueWholeCallKernel(cl, args); handled {
					return results, err
				}
				if handled, err := vm.tryRunWholeCallKernel(cl, args); handled {
					return nil, err
				}
			}
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
			if gf.Fast1 != nil {
				v, err := gf.Fast1(args)
				if err != nil {
					return nil, err
				}
				return []runtime.Value{v}, nil
			}
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
		return runtime.IntValue(int64(runtime.StringLen(v))), nil
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

func (vm *VM) ConcatValues(values []runtime.Value) (runtime.Value, error) {
	if len(values) == 0 {
		return runtime.StringValue(""), nil
	}
	allNative := true
	for _, v := range values {
		if !(v.IsString() || v.IsNumber()) {
			allNative = false
			break
		}
	}
	if allNative {
		result := values[0]
		if len(values) == 1 {
			s, _ := runtime.ConcatOperandString(result)
			return runtime.StringValue(s), nil
		}
		for i := 1; i < len(values); i++ {
			result = runtime.LazyStringValue(result, values[i])
		}
		return result, nil
	}

	result := values[0]
	for i := 1; i < len(values); i++ {
		var err error
		result, err = vm.concatPair(result, values[i])
		if err != nil {
			return runtime.NilValue(), err
		}
	}
	return result, nil
}

func (vm *VM) concatPair(a, b runtime.Value) (runtime.Value, error) {
	if (a.IsString() || a.IsNumber()) && (b.IsString() || b.IsNumber()) {
		return runtime.LazyStringValue(a, b), nil
	}
	mm, err := vm.getMetamethod(a, b, "__concat")
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
	if !(a.IsString() || a.IsNumber()) {
		return runtime.NilValue(), fmt.Errorf("attempt to concatenate a %s value", a.TypeName())
	}
	return runtime.NilValue(), fmt.Errorf("attempt to concatenate a %s value", b.TypeName())
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

// RegisterOpenUpvalue adds an existing open upvalue to the tracked list so that
// closeUpvalues will close it when the enclosing function returns.
// Used by the baseline JIT's CLOSURE handler.
func (vm *VM) RegisterOpenUpvalue(uv *Upvalue) {
	// Don't add duplicates.
	for _, existing := range vm.openUpvals {
		if existing == uv {
			return
		}
	}
	vm.openUpvals = append(vm.openUpvals, uv)
}

// FindOrCreateUpvalue returns the VM-tracked open upvalue for regIdx.
// JIT op-exit closure creation uses this to mirror interpreter OP_CLOSURE
// semantics and avoid accumulating duplicate open upvalues for loop locals.
func (vm *VM) FindOrCreateUpvalue(regIdx int) *Upvalue {
	return vm.findOrCreateUpvalue(regIdx)
}

// CloseUpvalues closes all open upvalues at or above fromReg.
// Used by the baseline JIT for OP_CLOSE handling.
func (vm *VM) CloseUpvalues(fromReg int) {
	vm.closeUpvalues(fromReg)
}

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
	if len(vm.openUpvals) == 0 {
		return
	}
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
