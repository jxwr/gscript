//go:build darwin && arm64

package jit

import (
	"encoding/binary"
	"hash/fnv"
	"runtime"
	"sync"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Global JIT code cache — keyed by bytecode hash. Compiled code is reused across
// Engine instances when the same bytecodes are encountered.
var (
	globalCodeCache sync.Map // uint64 → *compiledEntry
)

// JIT compilation threshold: compile after this many calls.
const DefaultHotThreshold = 10

// compiledEntry holds a compiled function.
type compiledEntry struct {
	cf  *CompiledFunc
	fn  func(uintptr) int64 // purego wrapper (kept for backward compat)
	ptr uintptr              // direct code pointer for callJIT trampoline
}

// Engine manages JIT compilation and execution.
type Engine struct {
	entries   map[*vm.FuncProto]*compiledEntry
	blacklist map[*vm.FuncProto]bool // functions known to not benefit from JIT
	threshold int
	globals   map[string]rt.Value // reference to VM globals for function inlining
}

// NewEngine creates a new JIT engine.
func NewEngine() *Engine {
	return &Engine{
		entries:   make(map[*vm.FuncProto]*compiledEntry),
		blacklist: make(map[*vm.FuncProto]bool),
		threshold: DefaultHotThreshold,
	}
}

// SetGlobals sets the globals map for function inlining.
func (e *Engine) SetGlobals(globals map[string]rt.Value) {
	e.globals = globals
}

// SetThreshold sets the call count threshold for JIT compilation.
func (e *Engine) SetThreshold(n int) {
	e.threshold = n
}

// shouldCompile checks if a function is worth JIT compiling.
// Only compile functions that will run mostly in native code.
// Functions with CALL instructions would side-exit on every call, wasting time.
// Exception: self-recursive functions (all CALLs target the function itself) are allowed.
func shouldCompile(proto *vm.FuncProto) bool {
	if len(proto.Code) == 0 {
		return false
	}
	hasLoop := false
	hasExternalCall := false
	for i, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_FORLOOP {
			hasLoop = true
		}
		if op == vm.OP_CALL || op == vm.OP_TFORCALL {
			if op == vm.OP_CALL && isSelfCall(proto, i) {
				continue // self-recursive calls are handled natively
			}
			hasExternalCall = true
		}
	}
	// Functions with loops and no calls are ideal JIT candidates.
	// Functions with calls would side-exit repeatedly, so skip them
	// unless they have a loop (the loop body is still worth JIT'ing).
	if hasExternalCall && !hasLoop {
		return false
	}
	return true
}

// isSelfCall checks whether the CALL at callPC is a self-recursive call
// by looking backward for a GETGLOBAL that loads the function's own name.
func isSelfCall(proto *vm.FuncProto, callPC int) bool {
	if proto.Name == "" {
		return false
	}
	callA := vm.DecodeA(proto.Code[callPC])
	for pc := callPC - 1; pc >= 0; pc-- {
		inst := proto.Code[pc]
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		if op == vm.OP_GETGLOBAL && a == callA {
			bx := vm.DecodeBx(inst)
			if bx < len(proto.Constants) {
				return proto.Constants[bx].Str() == proto.Name
			}
			return false
		}
		// If the register is written by another instruction, stop searching.
		if a == callA && op != vm.OP_EQ && op != vm.OP_LT && op != vm.OP_LE && op != vm.OP_TEST {
			return false
		}
	}
	return false
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

	// Check if already compiled (per-proto fast path).
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
		// Check global code cache first (reuses compiled code across engine instances).
		codeHash := hashBytecodes(proto.Code)
		if cached, ok := globalCodeCache.Load(codeHash); ok {
			entry = cached.(*compiledEntry)
		} else {
			// Try to compile.
			cf, err := CompileWithGlobals(proto, e.globals)
			if err != nil {
				e.blacklist[proto] = true
				return nil, 0, false
			}
			entry = &compiledEntry{cf: cf, ptr: uintptr(cf.Code.Ptr())}
			globalCodeCache.Store(codeHash, entry)
		}
		e.entries[proto] = entry
	}

	// Prepare JIT context.
	ctx := JITContext{
		Regs: uintptr(unsafe.Pointer(&regs[base])),
	}
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Call JIT code using direct assembly trampoline (avoids purego overhead).
	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	exitCode := callJIT(entry.ptr, ctxPtr)
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

// hashBytecodes computes a fast hash of a bytecode array for cache lookup.
func hashBytecodes(code []uint32) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	for _, inst := range code {
		binary.LittleEndian.PutUint32(buf[:], inst)
		h.Write(buf[:])
	}
	return h.Sum64()
}

// Free releases compiled code owned by this engine (not global cache entries).
func (e *Engine) Free() {
	for _, entry := range e.entries {
		if entry != nil && entry.cf != nil {
			entry.cf.Code.Free()
		}
	}
	e.entries = nil
}
