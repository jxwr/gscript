//go:build darwin && arm64

package jit

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// JIT compilation threshold: compile after this many calls.
const DefaultHotThreshold = 10

// compiledEntry holds a compiled function and its cached purego wrapper.
type compiledEntry struct {
	cf *CompiledFunc
	fn func(uintptr) int64
}

// Engine manages JIT compilation and execution.
type Engine struct {
	entries   map[*vm.FuncProto]*compiledEntry
	blacklist map[*vm.FuncProto]bool // functions known to not benefit from JIT
	threshold int
}

// NewEngine creates a new JIT engine.
func NewEngine() *Engine {
	return &Engine{
		entries:   make(map[*vm.FuncProto]*compiledEntry),
		blacklist: make(map[*vm.FuncProto]bool),
		threshold: DefaultHotThreshold,
	}
}

// SetThreshold sets the call count threshold for JIT compilation.
func (e *Engine) SetThreshold(n int) {
	e.threshold = n
}

// shouldCompile checks if a function is worth JIT compiling.
// Only compile functions that will run mostly in native code.
// Functions with CALL instructions would side-exit on every call, wasting time.
func shouldCompile(proto *vm.FuncProto) bool {
	if len(proto.Code) == 0 {
		return false
	}
	hasLoop := false
	hasCall := false
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_FORLOOP {
			hasLoop = true
		}
		if op == vm.OP_CALL || op == vm.OP_TFORCALL {
			hasCall = true
		}
	}
	// Functions with loops and no calls are ideal JIT candidates.
	// Functions with calls would side-exit repeatedly, so skip them
	// unless they have a loop (the loop body is still worth JIT'ing).
	if hasCall && !hasLoop {
		return false
	}
	return true
}

// TryExecute attempts to JIT-execute a function.
// Returns (results, resumePC, ok).
// If ok is true, the function completed (results contains return values).
// If ok is false, the JIT bailed out at resumePC and the interpreter should take over.
func (e *Engine) TryExecute(proto *vm.FuncProto, regs []rt.Value, base int, callCount int) (results []rt.Value, resumePC int, ok bool) {
	// Check blacklist first.
	if e.blacklist[proto] {
		return nil, 0, false
	}

	// Check if already compiled.
	entry, compiled := e.entries[proto]
	if !compiled {
		// Check if hot enough to compile.
		if callCount < e.threshold {
			return nil, 0, false
		}
		// Check if worth compiling.
		if !shouldCompile(proto) {
			e.blacklist[proto] = true
			return nil, 0, false
		}
		// Try to compile.
		cf, err := Compile(proto)
		if err != nil {
			e.blacklist[proto] = true
			return nil, 0, false
		}
		// Create and cache the purego function wrapper.
		var fn func(uintptr) int64
		purego.RegisterFunc(&fn, uintptr(cf.Code.Ptr()))
		entry = &compiledEntry{cf: cf, fn: fn}
		e.entries[proto] = entry
	}

	// Prepare JIT context.
	ctx := JITContext{
		Regs: uintptr(unsafe.Pointer(&regs[base])),
	}
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Call JIT code using cached function wrapper.
	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	exitCode := entry.fn(ctxPtr)
	// Keep ctx alive until after the JIT call completes.
	runtime.KeepAlive(ctx)

	if exitCode == 0 {
		// Normal return.
		retBase := int(ctx.RetBase)
		retCount := int(ctx.RetCount)
		if retCount == 0 {
			return nil, 0, true
		}
		ret := make([]rt.Value, retCount)
		for i := 0; i < retCount; i++ {
			ret[i] = regs[base+retBase+i]
		}
		return ret, 0, true
	}

	// Side exit — return the PC to resume at.
	return nil, int(ctx.ExitPC), false
}

// Free releases all compiled code.
func (e *Engine) Free() {
	for _, entry := range e.entries {
		if entry != nil && entry.cf != nil {
			entry.cf.Code.Free()
		}
	}
	e.entries = nil
}
