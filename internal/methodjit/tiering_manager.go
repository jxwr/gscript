//go:build darwin && arm64

// tiering_manager.go implements the TieringManager, a multi-tier JIT engine
// that manages automatic promotion from Tier 1 (baseline) to Tier 2 (optimizing).
//
// The TieringManager implements vm.MethodJITEngine and is a drop-in replacement
// for BaselineJITEngine. It delegates to BaselineJITEngine for Tier 1, and uses
// the existing Tier 2 pipeline (BuildGraph → TypeSpec → ConstProp → DCE →
// RegAlloc → Compile) for Tier 2.
//
// Smart tiering strategy (profile-based):
//   - CallCount < 1:                      stay interpreted (return nil)
//   - Pure-compute + loop + arith > 3:    Tier 2 at callCount=1 (immediate)
//   - Dense arithmetic, no calls:         Tier 2 at callCount=1
//   - Loop + calls + arith > 2:           Tier 2 at callCount=2
//   - Loop + table ops:                   Tier 2 at callCount=3
//   - Calls only (no loops):              stay Tier 1 (BLR is faster)
//   - Default:                            stay Tier 1
//
// The CallCount is incremented both by the VM on every vm.call() and by
// Tier 1's native BLR call sequence (which increments the callee's
// proto.CallCount before the BLR instruction). This ensures that functions
// called via BLR also accumulate call counts toward Tier 2 promotion.
//
// If Tier 2 compilation fails for a function, it falls back to Tier 1 permanently.
//
// Execution dispatches based on the compiled type:
//   - *BaselineFunc:       executed by BaselineJITEngine
//   - *CompiledFunction:   executed by Tier 2 execute loop

package methodjit

import (
	"fmt"
	"os"
	"sort"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// inlineMaxCalleeSize is the maximum bytecode count for a callee to be
// considered inlineable during the pre-scan and by the inline pass.
// R72: raised 80 → 250 so that medium-large callees like nbody's
// advance() (241 bytecode ops) can inline into <main>. Combined with
// the main-driver-promote clause in shouldPromoteTier2, this
// eliminates Tier 1 → Tier 2 BLR per loop iteration on driver
// patterns. The hasCallInLoop gate (tier2.compileTier2) prevents
// partial inlining from regressing: if full inline fails, main stays
// at Tier 1 as before, so the bump is safe-by-construction.
const inlineMaxCalleeSize = 250

// tmDefaultTier2Threshold is the BLR tier-up threshold. Controls when Tier 1's
// BLR call path falls to slow path to give TieringManager.TryCompile a chance
// to promote. With smart tiering, the actual promotion decision is per-function
// based on profile analysis (see shouldPromoteTier2 in func_profile.go).
const tmDefaultTier2Threshold = 2

// osrDefaultIterations is the default number of loop iterations before Tier 1
// triggers an OSR exit. After this many FORLOOP back-edges, the function exits
// with ExitOSR and the TieringManager compiles Tier 2 and re-enters.
const osrDefaultIterations = 1000

// TieringManager manages automatic promotion between Tier 1 and Tier 2.
// It implements vm.MethodJITEngine.
type TieringManager struct {
	tier1           *BaselineJITEngine
	tier2Compiled   map[*vm.FuncProto]*CompiledFunction
	tier2Failed     map[*vm.FuncProto]bool
	tier2FailReason map[*vm.FuncProto]string // reason a function failed Tier 2 (keyed by proto)
	tier2Attempts   int                      // total Tier 2 compilation attempts
	callVM          *vm.VM
	tier2Threshold  int // configurable threshold for testing (legacy fallback)
	profileCache    map[*vm.FuncProto]FuncProfile // cached function profiles
}

// NewTieringManager creates a new TieringManager with Tier 1 baseline support
// and Tier 2 optimizing support.
func NewTieringManager() *TieringManager {
	t1 := NewBaselineJITEngine()
	// Tell the Tier 1 engine to fall to slow path (callVM.CallValue) for callees
	// that have reached the Tier 2 threshold. The slow path goes through the VM's
	// call() which calls TieringManager.TryCompile(), enabling Tier 2 promotion.
	t1.SetTierUpThreshold(tmDefaultTier2Threshold)
	tm := &TieringManager{
		tier1:           t1,
		tier2Compiled:   make(map[*vm.FuncProto]*CompiledFunction),
		tier2Failed:     make(map[*vm.FuncProto]bool),
		tier2FailReason: make(map[*vm.FuncProto]string),
		tier2Threshold:  tmDefaultTier2Threshold,
		profileCache:    make(map[*vm.FuncProto]FuncProfile),
	}
	// Wire the outer compiler so handleCallFast routes through TieringManager
	t1.SetOuterCompiler(func(proto *vm.FuncProto) interface{} {
		return tm.TryCompile(proto)
	})
	return tm
}

// SetTier2Threshold sets the call count threshold for Tier 2 promotion.
// Only affects future compilations.
func (tm *TieringManager) SetTier2Threshold(n int) {
	tm.tier2Threshold = n
	tm.tier1.SetTierUpThreshold(n)
}

// SetCallVM sets the VM used for call-exit and global-exit during JIT execution.
func (tm *TieringManager) SetCallVM(v *vm.VM) {
	tm.callVM = v
	tm.tier1.SetCallVM(v)
}

// getProfile returns a cached FuncProfile for the given proto, computing it
// on first access.
func (tm *TieringManager) getProfile(proto *vm.FuncProto) FuncProfile {
	if p, ok := tm.profileCache[proto]; ok {
		return p
	}
	p := analyzeFuncProfile(proto)
	tm.profileCache[proto] = p
	return p
}

// TryCompile checks if a function should be compiled and returns the compiled
// code. Uses smart tiering: analyzes function characteristics (loops, arithmetic
// density, call patterns) to decide promotion thresholds instead of a simple
// call count.
func (tm *TieringManager) TryCompile(proto *vm.FuncProto) interface{} {
	// Already at Tier 2? Return cached.
	if t2, ok := tm.tier2Compiled[proto]; ok {
		return t2
	}

	// Below Tier 1 threshold? Stay interpreted.
	if proto.CallCount < BaselineCompileThreshold {
		return nil
	}

	// Get the function profile (cached after first computation).
	profile := tm.getProfile(proto)

	// Some function shapes are worse off compiled: tiny recursive
	// table-allocation builders pay more in Tier 1 exit-resume
	// overhead than they save in native templates. See
	// shouldStayTier0 in func_profile.go for the heuristic.
	if shouldStayTier0(profile) {
		return nil
	}

	// Use smart tiering to decide if this function should be promoted to Tier 2.
	// shouldPromoteTier2 considers loops, arithmetic density, call patterns, and
	// table ops. Functions with loops + calls + arithmetic are promoted at
	// threshold=2 — compileTier2 will try inlining and reject if calls remain.
	if !shouldPromoteTier2(proto, profile, proto.CallCount) {
		// Not ready for Tier 2: use Tier 1, but enable OSR for loop-heavy
		// functions so they can be upgraded mid-execution if they run hot.
		t1 := tm.tier1.TryCompile(proto)
		// Ensure feedback is initialized for Tier 1 type collection.
		if t1 != nil && proto.Feedback == nil {
			proto.EnsureFeedback()
		}
		// Enable OSR for deeply-nested-loop functions (LoopDepth >= 2).
		// Single-call compute functions (matmul, mandelbrot, spectral_norm,
		// fannkuch) reach Tier 2 via OSR instead of call-count threshold.
		if profile.HasLoop && profile.LoopDepth >= 2 && !tm.tier2Failed[proto] {
			tm.tier1.SetOSRCounter(proto, osrDefaultIterations)
		}
		return t1
	}

	// Tier 2 already failed? Use Tier 1.
	if tm.tier2Failed[proto] {
		return tm.tier1.TryCompile(proto)
	}

	// Ensure Tier 1 is compiled first (needed as deopt fallback).
	t1 := tm.tier1.TryCompile(proto)

	// Ensure feedback is initialized for type specialization.
	// Initialize now if needed -- TypeSpecializePass uses SSA-local inference
	// and doesn't require actual feedback data, so we don't need to wait
	// an extra call.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}

	// Attempt Tier 2 compilation.
	t2, err := tm.compileTier2(proto)
	if err != nil {
		tm.tier2Failed[proto] = true
		return t1
	}

	tm.tier2Compiled[proto] = t2
	proto.Tier2Promoted = true

	// Update DirectEntryPtr so Tier 1 BLR callers jump to Tier 2's direct entry.
	if t2.DirectEntryOffset > 0 {
		proto.DirectEntryPtr = uintptr(t2.Code.Ptr()) + uintptr(t2.DirectEntryOffset)
	}

	return t2
}

// Execute runs compiled code. Dispatches to Tier 1 or Tier 2 based on the
// compiled type. Handles OSR: if Tier 1 exits with an OSR request, compiles
// Tier 2 and re-enters the function from the start at Tier 2 speed.
func (tm *TieringManager) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	switch c := compiled.(type) {
	case *BaselineFunc:
		results, err := tm.tier1.Execute(c, regs, base, proto)
		if err == errOSRRequested {
			return tm.handleOSR(regs, base, proto)
		}
		// errIntSpecDeopt is handled internally by tier1.Execute.
		return results, err
	case *CompiledFunction:
		return tm.executeTier2(c, regs, base, proto)
	default:
		return nil, fmt.Errorf("tiering: unknown compiled type %T", compiled)
	}
}

// handleOSR compiles the function at Tier 2 and re-enters it from the start.
// The register file already has the function's arguments from the original call.
// This is a simplified OSR: instead of entering at the loop header, we restart
// the entire function at Tier 2. The restart overhead is negligible compared to
// long-running loops (e.g., mandelbrot(1000) with 1M iterations).
func (tm *TieringManager) handleOSR(regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	// Ensure feedback is initialized.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}

	// Try to compile at Tier 2.
	t2, err := tm.compileTier2(proto)
	if err != nil {
		// Tier 2 compilation failed. Disable OSR for this function and
		// re-run at Tier 1 from the start with OSR disabled.
		tm.tier2Failed[proto] = true
		tm.tier1.SetOSRCounter(proto, -1) // disable OSR
		t1 := tm.tier1.TryCompile(proto)
		if t1 == nil {
			return nil, fmt.Errorf("tiering: OSR fallback failed: no Tier 1 code")
		}
		return tm.tier1.Execute(t1, regs, base, proto)
	}

	// Cache the Tier 2 compilation for future calls.
	tm.tier2Compiled[proto] = t2
	proto.Tier2Promoted = true
	if t2.DirectEntryOffset > 0 {
		proto.DirectEntryPtr = uintptr(t2.Code.Ptr()) + uintptr(t2.DirectEntryOffset)
	}

	// Re-enter the function from the start at Tier 2.
	return tm.executeTier2(t2, regs, base, proto)
}

// compileTier2 compiles a function at Tier 2 (optimizing).
// Uses the pipeline: BuildGraph → TypeSpec → [Inline →] ConstProp →
// DCE → RegAlloc → Compile.

// canPromoteToTier2 checks if a function is safe for Tier 2 compilation.
//
// All standard ops are now handled by Tier 2, either natively or via exit-resume:
//
// Native ARM64 fast paths:
//   - Arithmetic, comparison, unary: emitRawIntBinOp / emitFloatBinOp / etc.
//   - GETTABLE, SETTABLE: emitGetTableNative / emitSetTableNative
//   - GETFIELD, SETFIELD: emitGetField / emitSetField (inline cache + shape guard)
//   - GETGLOBAL: emitGetGlobalNative (per-instruction value cache + exit-resume)
//
// Native + exit-resume fallback:
//   - CALL: eliminated by inline pass; if not inlined, compileTier2 rejects via irHasCall
//
// Exit-resume (exit to Go, execute, resume JIT):
//   - SETGLOBAL, NEWTABLE, SETLIST, APPEND, LEN, CONCAT, SELF, POW: emitOpExit
//   - CLOSURE, GETUPVAL, SETUPVAL: emitOpExit with closure state from VM
//   - VARARG: emitOpExit with vararg state from VM frame
//
// Only goroutine/channel ops are blocked (fundamentally require Go runtime):
//   - GO, MAKECHAN, SEND, RECV
//
// CALL is no longer blocked here. Instead, compileTier2 runs the inline pass to
// eliminate calls, then checks the optimized IR with irHasCall. If calls remain
// after inlining, the function falls back to Tier 1 where BLR calls are faster.
// GETGLOBAL is fully native with a per-instruction value cache (~5ns on hit).
func canPromoteToTier2(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		// Goroutine/channel ops (not in Tier 2):
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			return false
		}
	}
	return true
}

// canPromoteToTier2NoCalls is the conservative version of canPromoteToTier2
// that also blocks CALL. Used by shouldPromoteTier2 to identify pure-compute
// functions that don't need the inline pass. GETGLOBAL is allowed because
// Tier 2 has a per-instruction value cache matching Tier 1's performance.
func canPromoteToTier2NoCalls(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CALL:
			return false
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			return false
		}
	}
	return true
}

// canPromoteWithInlining checks if a function whose only blocker is OP_CALL
// (performance-blocked) can be promoted by inlining all calls. Returns true if
// ALL calls are to known, small, non-recursive global functions. The inline
// pass eliminates those calls, removing the performance blocker. GETGLOBAL is
// allowed regardless (Tier 2 has native value cache).
func canPromoteWithInlining(proto *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if len(globals) == 0 {
		return false
	}
	hasCall := false
	for i, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CALL:
			hasCall = true
			callA := vm.DecodeA(inst)
			if !findInlineableGetGlobal(proto, i, callA, globals) {
				return false
			}
		case vm.OP_GETGLOBAL:
			// GETGLOBAL is needed for CALL resolution — allowed
			continue
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			// Goroutine/channel ops not in Tier 2
			return false
		}
	}
	return hasCall
}

// findInlineableGetGlobal scans backwards from callPC to find the GETGLOBAL
// that loads the function into register targetReg. Returns true if the callee
// is in globals, small enough, and non-recursive.
func findInlineableGetGlobal(proto *vm.FuncProto, callPC, targetReg int, globals map[string]*vm.FuncProto) bool {
	for j := callPC - 1; j >= 0; j-- {
		prev := proto.Code[j]
		prevOp := vm.DecodeOp(prev)
		if prevOp == vm.OP_GETGLOBAL && vm.DecodeA(prev) == targetReg {
			bx := vm.DecodeBx(prev)
			if bx < 0 || bx >= len(proto.Constants) {
				return false
			}
			name := proto.Constants[bx].Str()
			callee, ok := globals[name]
			if !ok {
				return false
			}
			// Check size budget.
			if len(callee.Code) > inlineMaxCalleeSize {
				return false
			}
			// Recursion: permitted when bounded by MaxRecursion in the
			// inline pass (R31 bounded recursive inline ADR). The pass
			// caps unrolling depth so self/mutual recursion stays finite.
			// The name-match + isRecursive checks that previously
			// rejected here have moved responsibility onto the pass's
			// MaxRecursion gate. Non-bounded recursion would still blow
			// the inline budget naturally (per-iteration size growth).
			//
			// Check callee has no loops (while-loops produce buggy
			// code when inlined into the caller's IR).
			calleeProfile := analyzeFuncProfile(callee)
			if calleeProfile.HasLoop {
				return false
			}
			return true
		}
		// If another instruction writes to targetReg before we find GETGLOBAL,
		// the function reference is not from a GETGLOBAL. Bail out.
		if prevOp != vm.OP_GETGLOBAL && vm.DecodeA(prev) == targetReg {
			return false
		}
	}
	return false
}

// buildInlineGlobals extracts global function protos from the VM's globals.
// This is used by the inline pass to resolve callee functions at compile time.
func (tm *TieringManager) buildInlineGlobals() map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if tm.callVM == nil {
		return globals
	}
	for _, val := range tm.callVM.Globals() {
		if !val.IsFunction() {
			continue
		}
		ptr := val.Ptr()
		if ptr == nil {
			continue
		}
		if cl, ok := ptr.(*vm.Closure); ok && cl != nil && cl.Proto != nil {
			globals[cl.Proto.Name] = cl.Proto
		}
	}
	return globals
}

func (tm *TieringManager) compileTier2(proto *vm.FuncProto) (cf *CompiledFunction, retErr error) {
	tm.tier2Attempts++
	defer func() {
		if r := recover(); r != nil {
			cf = nil
			retErr = fmt.Errorf("tier2: panic during compilation: %v", r)
			if os.Getenv("GSCRIPT_JIT_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "tier2: panic during compilation of %q: %v\n", proto.Name, r)
			}
		}
		if retErr != nil {
			if tm.tier2FailReason == nil {
				tm.tier2FailReason = make(map[*vm.FuncProto]string)
			}
			tm.tier2FailReason[proto] = retErr.Error()
			if os.Getenv("GSCRIPT_JIT_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "tier2: compilation failed for %q: %v\n", proto.Name, retErr)
			}
		} else if os.Getenv("GSCRIPT_JIT_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "tier2: compiled %q\n", proto.Name)
		}
	}()

	return tm.compileTier2Pipeline(proto, nil)
}

// compileTier2Pipeline is the pure pipeline body shared between production
// compileTier2 and CompileForDiagnostics. It performs NO bookkeeping
// (counters, fail-reason maps, debug logging) so diagnostic calls cannot
// contaminate production state. It DOES mutate proto.NeedsTier2 and
// proto.MaxStack when the optimized function requires it — both are part of
// production compilation semantics and must be preserved identically so the
// diagnostic path is bit-identical to production.
//
// trace is optional. When non-nil, intermediate artifacts are captured into
// it for the diagnostic caller. When nil, the pipeline runs without
// observation overhead.
//
// Any change to this function's body is a change to the production Tier 2
// compile semantics AND to what the diagnostic tool sees, by construction.
// That is the load-bearing invariant of rule 5 in CLAUDE.md.
func (tm *TieringManager) compileTier2Pipeline(proto *vm.FuncProto, trace *Tier2Trace) (*CompiledFunction, error) {
	if !canPromoteToTier2(proto) {
		return nil, fmt.Errorf("tier2: function has unsupported ops, staying at tier 1")
	}

	fn := BuildGraph(proto)
	if trace != nil {
		trace.IRBefore = Print(fn)
	}

	if fn.Unpromotable {
		return nil, fmt.Errorf("tier2: function uses unmodeled bytecode (variadic CALL), staying at Tier 1")
	}

	if errs := Validate(fn); len(errs) > 0 {
		return nil, fmt.Errorf("tier2: validation failed: %v", errs[0])
	}

	inlineGlobals := tm.buildInlineGlobals()
	opts := &Tier2PipelineOpts{InlineGlobals: inlineGlobals, InlineMaxSize: inlineMaxCalleeSize}
	fn, intrinsicNotes, err := RunTier2Pipeline(fn, opts)
	if err != nil {
		return nil, fmt.Errorf("tier2: pipeline: %w", err)
	}
	if len(intrinsicNotes) > 0 {
		proto.NeedsTier2 = true
	}
	fn.CarryPreheaderInvariants = true
	if trace != nil {
		trace.IRAfter = Print(fn)
		trace.IntrinsicNotes = intrinsicNotes
	}

	if hasCallInLoop(fn) {
		return nil, fmt.Errorf("tier2: has OpCall inside loop (performance-blocked), staying at Tier 1")
	}

	// R40: mark Proto.HasSelfCalls so the emitter opts in to the
	// t2_self_entry lightweight path. A self-call is an OpCall whose
	// function argument comes from an OpGetGlobal loading this proto's
	// own name.
	if irHasSelfCall(fn) {
		proto.HasSelfCalls = true
	}

	alloc := AllocateRegisters(fn)
	if trace != nil {
		trace.RegAllocMap = formatRegAlloc(alloc)
	}

	cf, err := Compile(fn, alloc)
	if err != nil {
		return nil, fmt.Errorf("tier2: compile failed: %w", err)
	}

	if cf.numRegs > proto.MaxStack {
		proto.MaxStack = cf.numRegs
	}

	return cf, nil
}

// executeTier2 runs a Tier 2 compiled function using the VM's register file.
// This is the Tier 2 execute loop, handling exit codes and resuming JIT code.

func (tm *TieringManager) executeTier2(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	// Ensure register space.
	needed := base + cf.numRegs
	if needed > len(regs) {
		return nil, fmt.Errorf("tier2: register file too small: need %d, have %d", needed, len(regs))
	}

	// Initialize unused registers to nil.
	for i := base + proto.NumParams; i < base+cf.numRegs; i++ {
		if i < len(regs) {
			regs[i] = runtime.NilValue()
		}
	}

	// Set up ExecContext.
	ctx := new(ExecContext)
	escapeToHeap(ctx)
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Set up Tier 2 global value cache pointers.
	if len(cf.GlobalCache) > 0 {
		ctx.Tier2GlobalCache = uintptr(unsafe.Pointer(&cf.GlobalCache[0]))
		ctx.Tier2GlobalCacheGen = uintptr(unsafe.Pointer(&cf.GlobalCacheGen))
		ctx.Tier2GlobalGenPtr = uintptr(unsafe.Pointer(&tm.tier1.globalCacheGen))
	}

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))

	// resyncRegs re-reads the VM's register file after exits.
	resyncRegs := func() {
		if tm.callVM == nil {
			return
		}
		regs = tm.callVM.Regs()
		ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	}

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			// Tier 2 return: result in regs[base] (slot 0 relative to base).
			result := regs[base]
			return []runtime.Value{result}, nil

		case ExitDeopt:
			// Bail to interpreter. Return error so the VM falls through.
			return nil, fmt.Errorf("tier2: deopt")

		case ExitCallExit:
			if err := tm.executeCallExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: call-exit: %w", err)
			}
			resyncRegs()
			callID := int(ctx.CallID)
			resumeOff, ok := cf.ResumeAddrs[callID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for call %d", callID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitGlobalExit:
			if err := tm.executeGlobalExit(ctx, regs, base, proto, cf); err != nil {
				return nil, fmt.Errorf("tier2: global-exit: %w", err)
			}
			resyncRegs()
			globalID := int(ctx.GlobalExitID)
			resumeOff, ok := cf.ResumeAddrs[globalID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for global %d", globalID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitTableExit:
			if err := tm.executeTableExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: table-exit: %w", err)
			}
			resyncRegs()
			tableID := int(ctx.TableExitID)
			resumeOff, ok := cf.ResumeAddrs[tableID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for table %d", tableID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitOpExit:
			if err := tm.executeOpExit(ctx, regs, base, proto); err != nil {
				return nil, fmt.Errorf("tier2: op-exit: %w", err)
			}
			resyncRegs()
			opID := int(ctx.OpExitID)
			resumeOff, ok := cf.ResumeAddrs[opID]
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for op %d", opID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("tier2: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// CompileTier2 explicitly compiles a function at Tier 2. This bypasses the
// call count threshold and is useful for testing or when the caller knows
// the function is hot. Returns error if Tier 2 compilation fails.
func (tm *TieringManager) CompileTier2(proto *vm.FuncProto) error {
	if _, ok := tm.tier2Compiled[proto]; ok {
		return nil // already compiled
	}
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}
	t2, err := tm.compileTier2(proto)
	if err != nil {
		tm.tier2Failed[proto] = true
		return err
	}
	tm.tier2Compiled[proto] = t2
	proto.Tier2Promoted = true

	// Update DirectEntryPtr so Tier 1 BLR callers jump to Tier 2's direct entry.
	if t2.DirectEntryOffset > 0 {
		proto.DirectEntryPtr = uintptr(t2.Code.Ptr()) + uintptr(t2.DirectEntryOffset)
	}

	return nil
}

// Tier2Count returns the number of functions compiled at Tier 2.
func (tm *TieringManager) Tier2Count() int {
	return len(tm.tier2Compiled)
}

// Tier1Count returns the number of functions compiled at Tier 1.
func (tm *TieringManager) Tier1Count() int {
	return tm.tier1.CompiledCount()
}

// Tier2Compiled returns the names of protos that successfully compiled at
// Tier 2, sorted alphabetically. Used by CLI diagnostics (-jit-stats).
func (tm *TieringManager) Tier2Compiled() []string {
	names := make([]string, 0, len(tm.tier2Compiled))
	for proto := range tm.tier2Compiled {
		names = append(names, proto.Name)
	}
	sort.Strings(names)
	return names
}

// Tier2Failed returns a map of proto name -> error message for Tier 2
// compilations that failed. Used by CLI diagnostics (-jit-stats).
func (tm *TieringManager) Tier2Failed() map[string]string {
	out := make(map[string]string, len(tm.tier2FailReason))
	for proto, reason := range tm.tier2FailReason {
		out[proto.Name] = reason
	}
	return out
}

// Tier2Attempted returns the total number of Tier 2 compilation attempts
// (both successes and failures).
func (tm *TieringManager) Tier2Attempted() int {
	return tm.tier2Attempts
}

// irHasSelfCall (R40) scans the optimized IR for an OpCall whose function
// argument is an OpGetGlobal of this proto's own name. Used to gate the
// t2_self_entry lightweight prologue — only emitted for self-recursive
// functions so non-recursive functions keep their unchanged insn count.
func irHasSelfCall(fn *Function) bool {
	if fn == nil || fn.Proto == nil || fn.Proto.Name == "" {
		return false
	}
	// Find the constant pool index of the proto's own name.
	nameIdx := int64(-1)
	for i, c := range fn.Proto.Constants {
		if c.IsString() && c.Str() == fn.Proto.Name {
			nameIdx = int64(i)
			break
		}
	}
	if nameIdx < 0 {
		return false
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if instr.Op != OpCall {
				continue
			}
			if len(instr.Args) == 0 || instr.Args[0] == nil || instr.Args[0].Def == nil {
				continue
			}
			callee := instr.Args[0].Def
			if callee.Op == OpGetGlobal && callee.Aux == nameIdx {
				return true
			}
		}
	}
	return false
}

// irHasCall scans the optimized IR for any remaining OpCall instructions.
// Used after the inline pass to determine if all calls were eliminated.
// If OpCall remains, the function should stay at Tier 1 where BLR calls
// are faster than Tier 2's exit-resume.
func irHasCall(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpCall {
				return true
			}
		}
	}
	return false
}

// hasCallInLoop reports whether any OpCall in the optimized IR resides in
// a block that is part of a loop. Tier 2 exit-resume for CALL is ~30-80ns
// vs Tier 1's native BLR at ~10ns; inside a hot loop this difference
// destroys performance, but outside loops (loop depth 0) it is amortized.
// Uses the existing loopInfo infrastructure (natural-loop detection via
// back-edges + dominator analysis) — the same loopBlocks set the emitter
// uses for raw-int loop mode.
func hasCallInLoop(fn *Function) bool {
	var li *loopInfo
	for _, block := range fn.Blocks {
		// Fast path: skip blocks with no OpCall.
		hasCall := false
		for _, instr := range block.Instrs {
			if instr.Op == OpCall {
				hasCall = true
				break
			}
		}
		if !hasCall {
			continue
		}
		// Lazily compute loop info only when we actually find a call.
		if li == nil {
			li = computeLoopInfo(fn)
		}
		if li.loopBlocks[block.ID] {
			return true
		}
	}
	return false
}

// irHasGetGlobal scans the optimized IR for any remaining OpGetGlobal
// instructions. Used after the inline pass + DCE to determine if global
// accesses remain. OpGetGlobal uses exit-resume which is slower than
// Tier 1's per-PC value cache.
func irHasGetGlobal(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGetGlobal {
				return true
			}
		}
	}
	return false
}

// feedbackHasObservations returns true if any entry has a non-Unobserved
// Left, Right, or Result. Used by R82 Layer 1 gate to delay Tier 2
// compilation until feedback has had a chance to fill.
func feedbackHasObservations(fv []vm.TypeFeedback) bool {
	for i := range fv {
		if fv[i].Left != vm.FBUnobserved || fv[i].Right != vm.FBUnobserved ||
			fv[i].Result != vm.FBUnobserved || fv[i].Kind != vm.FBKindUnobserved {
			return true
		}
	}
	return false
}
