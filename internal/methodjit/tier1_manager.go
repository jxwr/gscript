//go:build darwin && arm64

// tier1_manager.go manages the Tier 1 baseline JIT engine.
// It implements the vm.MethodJITEngine interface, compiling functions
// to native ARM64 code using the baseline compiler (no SSA, no optimization).
//
// The execution loop uses exit-resume: when the JIT encounters an operation
// it cannot handle natively (calls, globals, tables, etc.), it exits to Go
// with a descriptor in ExecContext. Go performs the operation, then re-enters
// the JIT at the resume point following the exit.
//
// Flow:
//  1. TryCompile: if call count >= threshold, compile via CompileBaseline.
//  2. Execute: enter JIT code. On exit, handle the operation, resume.
//  3. On normal return: read result from regs[0] and return.
//
// Exit handlers are split into tier1_handlers.go (primary: calls, globals,
// tables, fields) and tier1_handlers_misc.go (remaining: concat, closures,
// upvalues, etc.).

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// BaselineCompileThreshold is the number of calls before baseline compilation.
// Compile on first call — every function gets Tier 1 immediately.
const BaselineCompileThreshold = 1

// escapeToHeap forces its argument to escape to the heap.
// This is critical for objects accessed via uintptr from JIT code,
// because Go's stack copier does not update uintptr values.
var heapSink interface{}

//go:noinline
func escapeToHeap(x interface{}) {
	heapSink = x
	heapSink = nil
}

// BaselineJITEngine implements vm.MethodJITEngine for the Tier 1 baseline compiler.
type BaselineJITEngine struct {
	compiled       map[*vm.FuncProto]*BaselineFunc
	failed         map[*vm.FuncProto]bool
	callVM         *vm.VM
	ctxPool        []*ExecContext // pre-allocated ExecContext pool (acts as stack for recursive calls)
	ctxTop         int            // next free index in ctxPool
	globalCacheGen uint64         // incremented on every SETGLOBAL; used to invalidate GlobalValCache
	// tierUpThreshold: when > 0, handleCall falls to slow path (callVM.CallValue)
	// for callees whose CallCount >= this threshold. This allows the TieringManager
	// to intercept calls and trigger Tier 2 compilation via the VM's TryCompile.
	tierUpThreshold int
	// outerCompiler: when set, handleCallFast uses this instead of e.TryCompile
	// for on-the-fly compilation. Set by TieringManager so that uncompiled callees
	// go through the tiering pipeline instead of being locked to Tier 1.
	outerCompiler func(*vm.FuncProto) interface{}
}

// NewBaselineJITEngine creates a new baseline JIT engine.
func NewBaselineJITEngine() *BaselineJITEngine {
	e := &BaselineJITEngine{
		compiled: make(map[*vm.FuncProto]*BaselineFunc),
		failed:   make(map[*vm.FuncProto]bool),
	}
	// Pre-allocate pool of ExecContexts (heap-allocated, safe for uintptr).
	const poolSize = 32
	e.ctxPool = make([]*ExecContext, poolSize)
	for i := range e.ctxPool {
		ctx := new(ExecContext)
		escapeToHeap(ctx) // ensure heap allocation
		e.ctxPool[i] = ctx
	}
	return e
}

// acquireCtx returns a pre-allocated ExecContext from the pool.
// If the pool is exhausted, it grows dynamically to handle deep recursion.
func (e *BaselineJITEngine) acquireCtx() *ExecContext {
	if e.ctxTop < len(e.ctxPool) {
		ctx := e.ctxPool[e.ctxTop]
		e.ctxTop++
		return ctx
	}
	// Pool exhausted: grow dynamically (keeps ctxTop consistent with depth).
	ctx := new(ExecContext)
	escapeToHeap(ctx)
	e.ctxPool = append(e.ctxPool, ctx)
	e.ctxTop++
	return ctx
}

// releaseCtx returns an ExecContext to the pool.
func (e *BaselineJITEngine) releaseCtx() {
	if e.ctxTop > 0 {
		e.ctxTop--
	}
}

// SetCallVM sets the VM used for exit-resume during JIT execution.
func (e *BaselineJITEngine) SetCallVM(v *vm.VM) {
	e.callVM = v
}

// SetTierUpThreshold configures the CallCount threshold at which handleCall
// falls to the slow path (callVM.CallValue) instead of executing the callee
// directly. This allows the TieringManager to trigger Tier 2 compilation.
func (e *BaselineJITEngine) SetTierUpThreshold(n int) {
	e.tierUpThreshold = n
}

// SetOuterCompiler sets a callback used by handleCallFast for on-the-fly
// compilation of callees. When set, this replaces e.TryCompile so that
// uncompiled callees go through the TieringManager's pipeline (which can
// route to Tier 2) instead of being locked to Tier 1.
func (e *BaselineJITEngine) SetOuterCompiler(fn func(*vm.FuncProto) interface{}) {
	e.outerCompiler = fn
}

// TryCompile checks if a function should be baseline-compiled.
// Returns the compiled function (as interface{}) if available, nil if not ready.
func (e *BaselineJITEngine) TryCompile(proto *vm.FuncProto) interface{} {
	if bf, ok := e.compiled[proto]; ok {
		return bf
	}
	if e.failed[proto] {
		return nil
	}
	if proto.CallCount < BaselineCompileThreshold {
		return nil
	}

	bf, err := CompileBaseline(proto)
	if err != nil {
		e.failed[proto] = true
		return nil
	}
	e.compiled[proto] = bf
	proto.CompiledCodePtr = uintptr(bf.Code.Ptr())
	if bf.DirectEntryOffset >= 0 {
		proto.DirectEntryPtr = uintptr(bf.Code.Ptr()) + uintptr(bf.DirectEntryOffset)
	}
	if len(bf.GlobalValCache) > 0 {
		proto.GlobalValCachePtr = uintptr(unsafe.Pointer(&bf.GlobalValCache[0]))
	}
	return bf
}

// Execute runs a baseline-compiled function using the VM's register file.
// Arguments are already in regs[base..base+numParams-1].
func (e *BaselineJITEngine) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	bf := compiled.(*BaselineFunc)

	// Ensure register space.
	needed := base + proto.MaxStack + 1
	if needed > len(regs) {
		return nil, fmt.Errorf("baseline: register file too small: need %d, have %d", needed, len(regs))
	}

	// Initialize unused registers to nil.
	for i := base + proto.NumParams; i < base+proto.MaxStack; i++ {
		if i < len(regs) {
			regs[i] = runtime.NilValue()
		}
	}

	// Acquire a pre-allocated, heap-escaped ExecContext from the pool.
	// Pool avoids per-call allocation overhead for small/recursive functions.
	ctx := e.acquireCtx()
	defer e.releaseCtx()
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	ctx.RegsBase = uintptr(unsafe.Pointer(&regs[0]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}
	if e.callVM != nil {
		ctx.TopPtr = uintptr(unsafe.Pointer(e.callVM.TopPtr()))
	}

	// Set up FieldCache pointer for native GETFIELD/SETFIELD.
	syncFieldCache := func() {
		if proto.FieldCache != nil && len(proto.FieldCache) > 0 {
			ctx.BaselineFieldCache = uintptr(unsafe.Pointer(&proto.FieldCache[0]))
		} else {
			ctx.BaselineFieldCache = 0
		}
	}
	syncFieldCache()

	// Set up Closure pointer for native GETUPVAL/SETUPVAL.
	// The closure is available from the VM's current call frame.
	syncClosure := func() {
		if e.callVM != nil {
			cl := e.callVM.CurrentClosure()
			if cl != nil {
				ctx.BaselineClosurePtr = uintptr(unsafe.Pointer(cl))
			}
		}
	}
	syncClosure()

	// Set up GlobalCache pointer and generation for native GETGLOBAL.
	if bf.GlobalValCache != nil && len(bf.GlobalValCache) > 0 {
		ctx.BaselineGlobalCache = uintptr(unsafe.Pointer(&bf.GlobalValCache[0]))
		ctx.BaselineGlobalGenPtr = uintptr(unsafe.Pointer(&e.globalCacheGen))
		ctx.BaselineGlobalCachedGen = bf.CachedGlobalGen
	}

	// Set RegsEnd for native BLR bounds checking.
	ctx.RegsEnd = uintptr(unsafe.Pointer(&regs[0])) + uintptr(len(regs)*8)

	codePtr := uintptr(bf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))

	// resyncRegs re-reads the VM's register file after exits.
	resyncRegs := func() {
		if e.callVM != nil {
			regs = e.callVM.Regs()
			ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
			ctx.RegsBase = uintptr(unsafe.Pointer(&regs[0]))
			ctx.RegsEnd = uintptr(unsafe.Pointer(&regs[0])) + uintptr(len(regs)*8)
		}
		// Refresh FieldCache pointer only if function uses field ops.
		if bf.HasFieldOps {
			syncFieldCache()
		}
	}

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			// Normal return: result is in ctx.BaselineReturnValue (not slot 0,
			// because RETURN must not clobber register slots that upvalues point to).
			result := runtime.Value(ctx.BaselineReturnValue)
			return []runtime.Value{result}, nil

		case ExitBaselineOpExit:
			// Baseline op-exit: handle operation via Go, then resume.
			if err := e.handleBaselineOpExit(ctx, regs, base, proto, bf); err != nil {
				return nil, fmt.Errorf("baseline: op-exit: %w", err)
			}
			resyncRegs()

			// Resume at the next bytecode PC.
			resumePC := int(ctx.BaselinePC)
			resumeOff, ok := bf.Labels[resumePC]
			if !ok {
				return nil, fmt.Errorf("baseline: no resume label for PC %d", resumePC)
			}
			codePtr = uintptr(bf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitNativeCallExit:
			// Native BLR callee hit an exit-resume op mid-execution.
			// The callee's exit state is in ctx (BaselineOp, BaselinePC, etc.)
			// but the caller's stack frame has already been restored by the ARM64
			// code. We need to:
			// 1. Reconstruct the callee's context and finish its execution
			// 2. Place the result in the caller's destination register
			// 3. Resume the caller at PC+1
			result, err := e.handleNativeCallExit(ctx, regs, base, proto, bf)
			if err != nil {
				return nil, fmt.Errorf("baseline: native-call-exit: %w", err)
			}

			// Place result in caller's register[A].
			callA := int(ctx.NativeCallA)
			callC := int(ctx.NativeCallC)
			absA := base + callA
			if absA < len(regs) {
				regs[absA] = result
			}
			// Fill extra return slots with nil if C > 2.
			if callC > 2 {
				for i := 1; i < callC-1; i++ {
					idx := absA + i
					if idx < len(regs) {
						regs[idx] = runtime.NilValue()
					}
				}
			}

			resyncRegs()

			// Resume caller at PC+1.
			resumePC := int(ctx.BaselinePC)
			resumeOff, ok := bf.Labels[resumePC]
			if !ok {
				return nil, fmt.Errorf("baseline: no resume label for native-call-exit PC %d", resumePC)
			}
			codePtr = uintptr(bf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("baseline: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// CompiledCount returns the number of compiled functions.
func (e *BaselineJITEngine) CompiledCount() int {
	return len(e.compiled)
}
