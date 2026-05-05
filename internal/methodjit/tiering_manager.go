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
const inlineMaxCalleeSize = 500

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
	tier1            *BaselineJITEngine
	tier2Compiled    map[*vm.FuncProto]*CompiledFunction
	tier2Failed      map[*vm.FuncProto]bool
	tier2FailReason  map[*vm.FuncProto]string // reason a function failed Tier 2 (keyed by proto)
	tier2Attempts    int                      // total Tier 2 compilation attempts
	exitStats        exitStatsCollector
	perfStats        *tier2PerfStatsCollector
	perfStatsEnabled bool
	callVM           *vm.VM
	retBuf           [8]runtime.Value
	tier2Threshold   int                           // configurable threshold for testing (legacy fallback)
	profileCache     map[*vm.FuncProto]FuncProfile // cached function profiles

	// R162: env-var caches evaluated ONCE at construction. Previously
	// R154's os.Getenv calls were placed inside hot paths
	// (executeTier2's main loop, TryCompile) causing a 25% fib
	// regression because os.Getenv is ~100-300ns per call on macOS.
	// These caches preserve the env-var diagnostic hook at zero hot-
	// path cost.
	envR154Trace     bool
	envTier2NoFilter bool
	r154DeoptPrints  int

	timeline *JITTimeline
	warmDump *WarmDumpSession
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
		// R162: cache env vars once to keep hot paths free of syscalls.
		envR154Trace:     os.Getenv("R154_TRACE") == "1",
		envTier2NoFilter: os.Getenv("GSCRIPT_TIER2_NO_FILTER") == "1",
	}
	// Wire the outer compiler so handleCallFast routes through TieringManager
	t1.SetOuterCompiler(func(proto *vm.FuncProto) interface{} {
		return tm.TryCompile(proto)
	})
	t1.SetOSRHandler(tm.handleOSR)
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

// NewCoroutineChildEngine returns a child-VM-bound JIT engine for coroutine
// bodies. It intentionally uses Tier 1 only: yielding functions need a compact
// bytecode-PC continuation, while Tier 2 still marks OP_YIELD unpromotable.
func (tm *TieringManager) NewCoroutineChildEngine(child *vm.VM) vm.MethodJITEngine {
	if tm == nil || tm.tier1 == nil {
		return nil
	}
	return tm.tier1.NewCoroutineChildEngine(child)
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
	if tm.envR154Trace {
		fmt.Fprintf(os.Stderr, "[R154] TryCompile proto=%q CallCount=%d tier2Compiled_has=%v tier2Failed=%v\n",
			proto.Name, proto.CallCount, tm.tier2Compiled[proto] != nil, tm.tier2HasFailed(proto))
	}
	// Already at Tier 2? Return cached.
	if t2, ok := tm.tier2CompiledFor(proto); ok {
		return t2
	}

	// Below Tier 1 threshold? Stay interpreted.
	if proto.CallCount < BaselineCompileThreshold {
		tm.traceEvent("tier1_skip", "tier1", proto, map[string]any{
			"reason":     "below_threshold",
			"call_count": proto.CallCount,
			"threshold":  BaselineCompileThreshold,
		})
		tm.traceEvent("fallback", "tier0", proto, map[string]any{
			"reason": "tier1_below_threshold",
			"target": "interpreter",
		})
		return nil
	}

	// Get the function profile (cached after first computation).
	profile := tm.getProfile(proto)

	if d, ok := tm.structuralKernelTieringDecision(proto); ok {
		tm.disableForStructuralKernelTiering(proto, d)
		return nil
	}

	if !tm.tier2HasFailed(proto) {
		if t2, ok := tm.compileFixedRecursiveTableBuilderTier2(proto); ok {
			tm.markTier2Compiled(proto, t2)
			return t2
		}
	}

	if shouldStayTier0CoroutineRuntime(proto, profile) {
		tm.disableJITForTier0Policy(proto, tier0DisableDecision{
			reason:         "stay_tier0_coroutine_runtime",
			fallbackReason: "coroutine_runtime",
		})
		return nil
	}

	if shouldStayTier0StringTokenLoop(proto, profile) {
		tm.disableJITForTier0Policy(proto, tier0DisableDecision{
			reason:         "stay_tier0_string_token_loop",
			fallbackReason: "string_token_loop",
		})
		return nil
	}

	// Some function shapes are worse off compiled: tiny recursive
	// table-allocation builders pay more in Tier 1 exit-resume
	// overhead than they save in native templates. See
	// shouldStayTier0 in func_profile.go for the heuristic.
	if shouldStayTier0ForProto(proto, profile) {
		tm.disableJITForTier0Policy(proto, tier0DisableDecision{
			reason:         "stay_tier0_profile",
			fallbackReason: "jit_disabled",
		})
		return nil
	}

	if shouldStayTier0RecursiveTableWalker(proto, profile) {
		tm.disableJITForTier0Policy(proto, tier0DisableDecision{
			reason:         "stay_tier0_recursive_table_walker",
			fallbackReason: "jit_disabled",
		})
		return nil
	}

	if callee, ok := tm.tier0OnlyLoopCallee(proto, profile); ok {
		tm.disableJITForTier0Policy(proto, tier0DisableDecision{
			reason:         "tier1_driver_tier0_loop_callee",
			fallbackReason: "driver_tier0_loop_callee",
			callee:         callee,
		})
		return nil
	}

	// Use smart tiering to decide if this function should be promoted to Tier 2.
	// shouldPromoteTier2 considers loops, arithmetic density, call patterns, and
	// table ops. Functions with loops + calls + arithmetic are promoted at
	// threshold=2 — compileTier2 will try inlining and reject if calls remain.
	promoteTier2 := shouldPromoteTier2(proto, profile, proto.CallCount)
	suppressedRecursivePartition := tm.shouldSuppressRecursivePartitionTableMutationTier2(proto, profile)
	if promoteTier2 && tm.shouldSuppressLoopCallTier2(proto, profile) {
		promoteTier2 = false
	}
	if promoteTier2 && suppressedRecursivePartition {
		promoteTier2 = false
	}
	if !promoteTier2 && !suppressedRecursivePartition && tm.shouldPromoteNativeLoopDriver(proto, profile) {
		promoteTier2 = true
	}
	if !promoteTier2 {
		// Not ready for Tier 2: use Tier 1, but enable OSR for loop-heavy
		// functions so they can be upgraded mid-execution if they run hot.
		if suppressedRecursivePartition {
			tm.disableTier1FeedbackForNoTier2(proto)
			if proto.CallCount <= tmDefaultTier2Threshold {
				proto.CallCount = tmDefaultTier2Threshold + 1
			}
			tm.tier1.SetOSRCounter(proto, -1)
			tm.traceEvent("tier2_skip", "tier2", proto, map[string]any{
				"reason": "recursive_partition_table_mutation",
				"target": "tier1",
			})
		}
		tier1AlreadyCompiled := tm.tier1.compiled[proto] != nil
		t1 := tm.tier1.TryCompile(proto)
		tm.traceTier1CompileResult(proto, tier1AlreadyCompiled, t1, "not_ready_for_tier2")
		// Ensure feedback is initialized for Tier 1 type collection.
		if t1 != nil && proto.Feedback == nil && !IsFeedbackCollectionDisabled(proto) {
			proto.EnsureFeedback()
		}
		// R162 widened OSR to LoopDepth >= 1 for clean post-pipeline bodies.
		// R170 keeps the classic LoopDepth>=2 path open for already-proven
		// deep-loop benchmarks (for example fannkuch), while LoopDepth<2
		// candidates must pass the restart-safety check so restart-style OSR
		// cannot replay table mutations from single-loop drivers. No-filter
		// may bypass the performance-only call-in-loop prefilter, but it must
		// not bypass restart-safety: replayed side effects are correctness bugs.
		if profile.HasLoop && profile.LoopDepth >= 1 && !suppressedRecursivePartition && !tm.tier2HasFailed(proto) &&
			(profile.LoopDepth >= 2 || tm.isOSRRestartSafe(proto, profile)) &&
			(tm.envTier2NoFilter || !tm.osrWouldHitCallInLoopGate(proto, profile)) {
			tm.tier1.SetOSRCounter(proto, osrDefaultIterations)
			tm.traceEvent("osr_armed", "tier1", proto, map[string]any{
				"counter":    osrDefaultIterations,
				"loop_depth": profile.LoopDepth,
			})
			if tm.envR154Trace {
				fmt.Fprintf(os.Stderr, "[R162] SetOSRCounter proto=%q loopDepth=%d\n",
					proto.Name, profile.LoopDepth)
			}
		}
		return t1
	}

	// Tier 2 already failed? Use Tier 1.
	if tm.tier2HasFailed(proto) {
		tm.disableTier1FeedbackForNoTier2(proto)
		tier1AlreadyCompiled := tm.tier1.compiled[proto] != nil
		t1 := tm.tier1.TryCompile(proto)
		tm.traceTier1CompileResult(proto, tier1AlreadyCompiled, t1, "tier2_failed")
		tm.traceEvent("fallback", "tier1", proto, map[string]any{
			"reason": "tier2_failed",
			"target": "tier1",
		})
		return t1
	}

	tm.ensureNativeLoopCallees(proto)
	tm.ensureRawIntLoopCallees(proto)

	// Ensure Tier 1 is compiled first (needed as deopt fallback).
	tier1AlreadyCompiled := tm.tier1.compiled[proto] != nil
	t1 := tm.tier1.TryCompile(proto)
	tm.traceTier1CompileResult(proto, tier1AlreadyCompiled, t1, "tier2_deopt_fallback")

	// Ensure feedback is initialized for type specialization.
	// Initialize now if needed -- TypeSpecializePass uses SSA-local inference
	// and doesn't require actual feedback data, so we don't need to wait
	// an extra call.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}

	// Attempt Tier 2 compilation.
	if t2, ok := tm.compileFixedRecursiveIntFoldTier2(proto); ok {
		tm.markTier2Compiled(proto, t2)
		return t2
	}
	if t2, ok := tm.compileFixedRecursiveNestedIntFoldTier2(proto); ok {
		tm.markTier2Compiled(proto, t2)
		return t2
	}
	if t2, ok := tm.compileFixedRecursiveTableFoldTier2(proto); ok {
		tm.markTier2Compiled(proto, t2)
		return t2
	}
	if t2, ok := tm.compileMutualRecursiveIntSCCTier2(proto); ok {
		tm.markTier2Compiled(proto, t2)
		return t2
	}
	t2, err := tm.compileTier2(proto)
	if err != nil {
		tm.markTier2Failed(proto, err.Error())
		tm.disableTier1FeedbackForNoTier2(proto)
		tm.traceEvent("fallback", "tier1", proto, map[string]any{
			"reason": err.Error(),
			"target": "tier1",
		})
		return t1
	}

	tm.markTier2Compiled(proto, t2)

	return t2
}

// osrWouldHitCallInLoopGate returns true when the cheap bytecode profile says
// OSR would likely restart a running Tier 1 loop only to hit compileTier2's
// post-inline OpCall-in-loop performance gate. That failed OSR path restarts
// the function from the beginning in Tier 1, which is visible in hot callers
// like math_intensive.gcd_bench. Keep this conservative: only suppress OSR
// when there is a static call in a loop and the existing inline pre-scan cannot
// prove all calls are inlineable under the current globals.
func (tm *TieringManager) osrWouldHitCallInLoopGate(proto *vm.FuncProto, profile FuncProfile) bool {
	if proto == nil || profile.LoopDepth < 2 || profile.CallCount == 0 || !hasStaticCallInLoop(proto) {
		return false
	}
	globals := tm.buildInlineGlobals()
	if protoGlobals := buildProtoInlineGlobals(proto); len(protoGlobals) > 0 {
		if len(globals) == 0 {
			globals = protoGlobals
		} else {
			merged := make(map[string]*vm.FuncProto, len(globals)+len(protoGlobals))
			for name, callee := range globals {
				merged[name] = callee
			}
			for name, callee := range protoGlobals {
				if _, ok := merged[name]; !ok {
					merged[name] = callee
				}
			}
			globals = merged
		}
	}
	if stableGlobals := buildProtoStableGlobals(proto); len(stableGlobals) > 0 {
		if len(globals) == 0 {
			globals = stableGlobals
		} else {
			merged := make(map[string]*vm.FuncProto, len(globals)+len(stableGlobals))
			for name, callee := range globals {
				merged[name] = callee
			}
			for name, callee := range stableGlobals {
				if _, ok := merged[name]; !ok {
					merged[name] = callee
				}
			}
			globals = merged
		}
	}
	if canPromoteWithInlining(proto, globals) || canPromoteWithNativeLoopCalls(proto, globals) {
		return false
	}
	return !staticCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(proto)
}

// Execute runs compiled code. Dispatches to Tier 1 or Tier 2 based on the
// compiled type. Handles OSR: if Tier 1 exits with an OSR request, compiles
// Tier 2 and re-enters the function from the start at Tier 2 speed.
func (tm *TieringManager) Execute(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	return tm.ExecuteWithResultBuffer(compiled, regs, base, proto, tm.retBuf[:0])
}

func (tm *TieringManager) ExecuteWithResultBuffer(compiled interface{}, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	switch c := compiled.(type) {
	case *BaselineFunc:
		results, err := tm.tier1.ExecuteWithResultBuffer(c, regs, base, proto, retBuf)
		if err == errOSRRequested {
			return tm.handleOSRWithResultBuffer(regs, base, proto, retBuf)
		}
		if err != nil {
			tm.traceEvent("fallback", "tier0", proto, map[string]any{
				"reason": err.Error(),
				"target": "interpreter",
			})
		}
		// errIntSpecDeopt is handled internally by tier1.Execute.
		return results, err
	case *CompiledFunction:
		return tm.executeTier2WithResultBuffer(c, regs, base, proto, retBuf)
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
	return tm.handleOSRWithResultBuffer(regs, base, proto, tm.retBuf[:0])
}

func (tm *TieringManager) handleOSRWithResultBuffer(regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	tm.traceEvent("osr_fired", "tier1", proto, map[string]any{
		"base": base,
	})
	// Ensure feedback is initialized.
	if proto.Feedback == nil {
		proto.EnsureFeedback()
	}

	if tm.envR154Trace {
		fmt.Fprintf(os.Stderr, "[R154] handleOSR proto=%q tier2Failed=%v tier2Compiled_has=%v\n",
			proto.Name, tm.tier2HasFailed(proto), tm.tier2Compiled[proto] != nil)
	}

	// Try to compile at Tier 2.
	tm.ensureNativeLoopCallees(proto)
	tm.ensureRawIntLoopCallees(proto)
	t2, err := tm.compileTier2(proto)
	if err != nil {
		// Tier 2 compilation failed. Disable OSR for this function and
		// re-run at Tier 1 from the start with OSR disabled.
		tm.markTier2Failed(proto, err.Error())
		tm.disableTier1FeedbackForNoTier2(proto)
		tm.tier1.SetOSRCounter(proto, -1) // disable OSR
		tm.traceEvent("fallback", "tier1", proto, map[string]any{
			"reason": err.Error(),
			"target": "tier1",
		})
		t1 := tm.tier1.TryCompile(proto)
		if t1 == nil {
			return nil, fmt.Errorf("tiering: OSR fallback failed: no Tier 1 code")
		}
		return tm.tier1.ExecuteWithResultBuffer(t1, regs, base, proto, retBuf)
	}

	// Cache the Tier 2 compilation for future calls.
	tm.markTier2Compiled(proto, t2)

	// Re-enter the function from the start at Tier 2.
	return tm.executeTier2WithResultBuffer(t2, regs, base, proto, retBuf)
}

func (tm *TieringManager) disableTier1FeedbackForNoTier2(proto *vm.FuncProto) {
	if proto == nil || IsFeedbackCollectionDisabled(proto) {
		return
	}
	tm.tier1.DisableFeedbackCollection(proto)
	tm.tier1.EvictCompiled(proto)
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
