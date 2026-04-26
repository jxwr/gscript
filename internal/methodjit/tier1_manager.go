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

// protoIntSpecDisabled is the set of protos for which int-spec has been
// disabled (due to a runtime guard failure). CompileBaseline consults this
// before running computeKnownIntSlots. Global because BaselineFunc is
// compiled via a package-level function (CompileBaseline), not a method.
var protoIntSpecDisabled = make(map[*vm.FuncProto]bool)

// DisableIntSpec marks a proto as ineligible for int-spec. Subsequent
// (re-)compilations use only the generic polymorphic templates.
func DisableIntSpec(proto *vm.FuncProto) {
	protoIntSpecDisabled[proto] = true
}

// IsIntSpecDisabled reports whether int-spec has been disabled for a proto.
func IsIntSpecDisabled(proto *vm.FuncProto) bool {
	return protoIntSpecDisabled[proto]
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
	// osrHandler: when set, nested Tier 1 calls that hit the OSR back-edge
	// counter are handled at the callee boundary instead of bubbling
	// errOSRRequested to the caller as a generic JIT failure.
	osrHandler func([]runtime.Value, int, *vm.FuncProto) ([]runtime.Value, error)
	// osrEnabled: per-proto OSR configuration. Maps proto -> initial OSR counter.
	// Positive value = counter threshold (triggers OSR after N iterations).
	// 0 or absent = OSR disabled. Set by TieringManager before Execute.
	osrCounters map[*vm.FuncProto]int64
	// intSpecDeoptPC holds the bytecode PC saved by the most recent int-spec
	// deopt. It is read in Execute to resume the interpreter at that exact
	// instruction instead of restarting at pc=0 (which replays side effects).
	// Safe: Execute is called serially within a single goroutine.
	intSpecDeoptPC int
}

// NewBaselineJITEngine creates a new baseline JIT engine.
func NewBaselineJITEngine() *BaselineJITEngine {
	e := &BaselineJITEngine{
		compiled:    make(map[*vm.FuncProto]*BaselineFunc),
		failed:      make(map[*vm.FuncProto]bool),
		osrCounters: make(map[*vm.FuncProto]int64),
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

// SetOSRHandler sets a callback used when a nested Tier 1 callee requests OSR.
func (e *BaselineJITEngine) SetOSRHandler(fn func([]runtime.Value, int, *vm.FuncProto) ([]runtime.Value, error)) {
	e.osrHandler = fn
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
	results, err := e.executeInner(compiled, regs, base, proto)
	if err == errIntSpecDeopt {
		DisableIntSpec(proto)
		e.EvictCompiled(proto)
		deoptPC := e.intSpecDeoptPC
		e.intSpecDeoptPC = 0
		// If the deopt was from an arithmetic overflow (deoptPC > 0), resume
		// the interpreter at the exact guard PC. This avoids replaying side
		// effects (SETGLOBAL exits, native SETFIELD writes, etc.) that occurred
		// between pc=0 and the overflow. The JIT has already executed those
		// instructions and their results are live in the register file.
		if deoptPC > 0 && e.callVM != nil {
			return e.callVM.ResumeFromPC(deoptPC)
		}
		// Param-entry guard failure (deoptPC=0): no bytecodes ran, so restarting
		// from pc=0 is safe. Recompile and re-execute with generic templates.
		recompiled := e.TryCompile(proto)
		if recompiled == nil {
			return nil, fmt.Errorf("baseline: int-spec deopt recompile failed")
		}
		return e.executeInner(recompiled, regs, base, proto)
	}
	return results, err
}

// executeInner is the raw JIT entry loop. Execute wraps it to handle
// int-spec deopt fallback.
func (e *BaselineJITEngine) executeInner(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
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

	// Set OSR counter for this function. Positive = enabled, negative = disabled.
	if counter, ok := e.osrCounters[proto]; ok && counter > 0 {
		ctx.OSRCounter = counter
	} else {
		ctx.OSRCounter = -1 // disabled
	}
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
	// Always set BaselineGlobalCachedGen from the function's BF, or 0 if
	// no cache (prevents stale CachedGen from pool reuse).
	if bf.GlobalValCache != nil && len(bf.GlobalValCache) > 0 {
		ctx.BaselineGlobalCache = uintptr(unsafe.Pointer(&bf.GlobalValCache[0]))
		ctx.BaselineGlobalGenPtr = uintptr(unsafe.Pointer(&e.globalCacheGen))
		ctx.BaselineGlobalCachedGen = bf.CachedGlobalGen
	} else {
		ctx.BaselineGlobalCache = 0
		ctx.BaselineGlobalGenPtr = 0
		ctx.BaselineGlobalCachedGen = 0
	}
	if len(bf.CallCache) > 0 {
		ctx.BaselineCallCache = uintptr(unsafe.Pointer(&bf.CallCache[0]))
	} else {
		ctx.BaselineCallCache = 0
	}

	// Set up FeedbackPtr for Tier 1 type feedback collection.
	if proto.Feedback != nil && len(proto.Feedback) > 0 {
		ctx.BaselineFeedbackPtr = uintptr(unsafe.Pointer(&proto.Feedback[0]))
	} else {
		ctx.BaselineFeedbackPtr = 0
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

			// Re-read regs first — the callee's Execute may have grown the register file.
			resyncRegs()

			// The callee's re-execution may have changed globalCacheGen (e.g.,
			// via SETGLOBAL). Force a cache miss on subsequent GETGLOBAL ops
			// by invalidating ALL global value caches. This is heavy-handed but
			// safe: it only happens once per callee (DirectEntryPtr cleared).
			e.globalCacheGen++
			ctx.BaselineGlobalCachedGen = e.globalCacheGen

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

			// Resume caller at PC+1.
			resumePC := int(ctx.BaselinePC)
			resumeOff, ok := bf.Labels[resumePC]
			if !ok {
				return nil, fmt.Errorf("baseline: no resume label for native-call-exit PC %d", resumePC)
			}
			codePtr = uintptr(bf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitOSR:
			// OSR: loop counter expired. Return a sentinel error so the
			// TieringManager can intercept and upgrade to Tier 2.
			return nil, errOSRRequested

		case ExitDeopt:
			// Int-spec guard failed or int-spec arith overflowed. Save the
			// guard PC so Execute can resume the interpreter there rather than
			// restarting at pc=0 (which would replay earlier side effects).
			e.intSpecDeoptPC = int(ctx.ExitResumePC)
			return nil, errIntSpecDeopt

		default:
			return nil, fmt.Errorf("baseline: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// errOSRRequested is a sentinel error returned by BaselineJITEngine.Execute
// when the OSR counter reaches zero. The TieringManager intercepts this to
// compile Tier 2 and re-enter the function at optimizing speed.
var errOSRRequested = fmt.Errorf("baseline: OSR requested")

// errIntSpecDeopt is a sentinel error returned by BaselineJITEngine.Execute
// when an int-spec guard fails (param not int) or an int-spec arith overflows.
// The TieringManager intercepts it, disables int-spec for the proto, and
// recompiles generic Tier 1 code, then re-executes.
var errIntSpecDeopt = fmt.Errorf("baseline: int-spec deopt")

// SetOSRCounter sets the OSR loop iteration counter for a function.
// When positive, Tier 1's FORLOOP will exit with ExitOSR after this many
// iterations, triggering Tier 2 compilation.
func (e *BaselineJITEngine) SetOSRCounter(proto *vm.FuncProto, counter int64) {
	e.osrCounters[proto] = counter
}

// CompiledCount returns the number of compiled functions.
func (e *BaselineJITEngine) CompiledCount() int {
	return len(e.compiled)
}

// EvictCompiled removes the cached BaselineFunc for a proto so the next
// TryCompile rebuilds it from scratch. Used by the tiering manager to force
// a recompile after an int-spec deopt.
func (e *BaselineJITEngine) EvictCompiled(proto *vm.FuncProto) {
	if bf, ok := e.compiled[proto]; ok {
		// Leave the old code block allocated — it may still be referenced by
		// proto.CompiledCodePtr from in-flight native calls. Freeing here
		// could race with a concurrent BLR. The leak is bounded by the number
		// of deopts (at most once per proto in practice).
		_ = bf
		delete(e.compiled, proto)
	}
	delete(e.failed, proto)
	proto.CompiledCodePtr = 0
	proto.DirectEntryPtr = 0
	proto.Tier2DirectEntryPtr = 0
	e.clearBaselineCallCachesForProto(proto)
}

func (e *BaselineJITEngine) clearBaselineCallCachesForProto(proto *vm.FuncProto) {
	if proto == nil {
		return
	}
	protoPtr := uint64(uintptr(unsafe.Pointer(proto)))
	for _, bf := range e.compiled {
		cache := bf.CallCache
		for i := 0; i+3 < len(cache); i += 4 {
			if cache[i+3] == protoPtr {
				cache[i] = 0
				cache[i+1] = 0
				cache[i+2] = 0
				cache[i+3] = 0
			}
		}
	}
}

func (e *BaselineJITEngine) invalidateGlobalValueCaches(name string) {
	if e == nil || name == "" {
		return
	}
	for _, bf := range e.compiled {
		if bf == nil || bf.Proto == nil || len(bf.GlobalValCache) == 0 {
			continue
		}
		for pc, inst := range bf.Proto.Code {
			if pc >= len(bf.GlobalValCache) || vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
				continue
			}
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(bf.Proto.Constants) {
				continue
			}
			kv := bf.Proto.Constants[bx]
			if kv.IsString() && kv.Str() == name {
				bf.GlobalValCache[pc] = 0
			}
		}
	}
}
