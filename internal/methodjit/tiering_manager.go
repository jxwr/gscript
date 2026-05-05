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
			proto.Name, proto.CallCount, tm.tier2Compiled[proto] != nil, tm.tier2Failed[proto])
	}
	// Already at Tier 2? Return cached.
	if t2, ok := tm.tier2Compiled[proto]; ok {
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

	if !tm.tier2Failed[proto] {
		if t2, ok := tm.compileFixedRecursiveTableBuilderTier2(proto); ok {
			tm.tier2Compiled[proto] = t2
			tm.installTier2(proto, t2)
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
		if profile.HasLoop && profile.LoopDepth >= 1 && !suppressedRecursivePartition && !tm.tier2Failed[proto] &&
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
	if tm.tier2Failed[proto] {
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
		tm.tier2Compiled[proto] = t2
		tm.installTier2(proto, t2)
		return t2
	}
	if t2, ok := tm.compileFixedRecursiveNestedIntFoldTier2(proto); ok {
		tm.tier2Compiled[proto] = t2
		tm.installTier2(proto, t2)
		return t2
	}
	if t2, ok := tm.compileFixedRecursiveTableFoldTier2(proto); ok {
		tm.tier2Compiled[proto] = t2
		tm.installTier2(proto, t2)
		return t2
	}
	if t2, ok := tm.compileMutualRecursiveIntSCCTier2(proto); ok {
		tm.tier2Compiled[proto] = t2
		tm.installTier2(proto, t2)
		return t2
	}
	t2, err := tm.compileTier2(proto)
	if err != nil {
		tm.tier2Failed[proto] = true
		tm.disableTier1FeedbackForNoTier2(proto)
		tm.traceEvent("fallback", "tier1", proto, map[string]any{
			"reason": err.Error(),
			"target": "tier1",
		})
		return t1
	}

	tm.tier2Compiled[proto] = t2
	tm.installTier2(proto, t2)

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
			proto.Name, tm.tier2Failed[proto], tm.tier2Compiled[proto] != nil)
	}

	// Try to compile at Tier 2.
	tm.ensureNativeLoopCallees(proto)
	tm.ensureRawIntLoopCallees(proto)
	t2, err := tm.compileTier2(proto)
	if err != nil {
		// Tier 2 compilation failed. Disable OSR for this function and
		// re-run at Tier 1 from the start with OSR disabled.
		tm.tier2Failed[proto] = true
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
	tm.tier2Compiled[proto] = t2
	tm.installTier2(proto, t2)

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
func protoConstString(proto *vm.FuncProto, idx int) string {
	if proto == nil || idx < 0 || idx >= len(proto.Constants) {
		return ""
	}
	val := proto.Constants[idx]
	if !val.IsString() {
		return ""
	}
	return val.Str()
}

func hasGenericStringFormatIntCall(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	type slotState struct {
		kind string
	}
	states := make([]slotState, proto.MaxStack+8)
	clear := func(slot int) {
		if slot >= 0 && slot < len(states) {
			states[slot] = slotState{}
		}
	}
	get := func(slot int) slotState {
		if slot >= 0 && slot < len(states) {
			return states[slot]
		}
		return slotState{}
	}
	set := func(slot int, st slotState) {
		if slot >= 0 && slot < len(states) {
			states[slot] = st
		}
	}
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_LOADK:
			s := protoConstString(proto, vm.DecodeBx(inst))
			if s != "" && simpleSingleDecimalIntFormat(s) {
				set(a, slotState{kind: "const_single_int_format"})
			} else {
				clear(a)
			}
		case vm.OP_GETGLOBAL:
			if protoConstString(proto, vm.DecodeBx(inst)) == "string" {
				set(a, slotState{kind: "string_global"})
			} else {
				clear(a)
			}
		case vm.OP_GETFIELD:
			if get(vm.DecodeB(inst)).kind == "string_global" && protoConstString(proto, vm.DecodeC(inst)) == "format" {
				set(a, slotState{kind: "string_format"})
			} else {
				clear(a)
			}
		case vm.OP_MOVE:
			set(a, get(vm.DecodeB(inst)))
		case vm.OP_CALL:
			b := vm.DecodeB(inst)
			if b == 3 && get(a).kind == "string_format" &&
				(get(a+1).kind == "const_single_int_format" || callSiteFeedbackHasStableStringFormatInt(proto, pc)) {
				return true
			}
			c := vm.DecodeC(inst)
			if c == 0 {
				clear(a)
			} else {
				for slot := a; slot <= a+c-2; slot++ {
					clear(slot)
				}
			}
		case vm.OP_FORLOOP:
			clear(a)
			clear(a + 3)
		case vm.OP_FORPREP:
			clear(a)
		case vm.OP_SETGLOBAL, vm.OP_SETTABLE, vm.OP_SETFIELD, vm.OP_SETUPVAL, vm.OP_SETLIST, vm.OP_RETURN:
		default:
			clear(a)
		}
	}
	return false
}

func callSiteFeedbackHasStableStringFormatInt(proto *vm.FuncProto, pc int) bool {
	if proto == nil || proto.CallSiteFeedback == nil || pc < 0 || pc >= len(proto.CallSiteFeedback) {
		return false
	}
	cf := proto.CallSiteFeedback[pc]
	kind, data, ok := cf.StableCalleeNativeIdentity()
	if !ok || kind != runtime.NativeKindStdStringFormat || data != uintptr(runtime.StdStringFormatIdentityPtr()) {
		return false
	}
	if cf.NArgs != 2 || cf.Flags&vm.CallSiteArityPolymorphic != 0 || cf.ArgTypes[1] != vm.FBInt {
		return false
	}
	pattern, ok := cf.StableStringArg(0)
	return ok && simpleSingleDecimalIntFormat(pattern)
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
	return hasExpensiveInLoop(fn, func(op Op) bool { return op == OpCall })
}

// hasStaticCallInLoop is the bytecode-side prefilter for OSR. It marks PCs
// covered by backward loop edges (FORLOOP and while-style JMP) and reports
// whether an OP_CALL falls inside one of those ranges. The full Tier 2 gate
// still uses SSA loopInfo after inline; this helper only avoids known-futile
// OSR restarts before the expensive path runs.
func hasStaticCallInLoop(proto *vm.FuncProto) bool {
	if proto == nil || len(proto.Code) == 0 {
		return false
	}
	inLoop := staticLoopPCs(proto)
	for pc, inst := range proto.Code {
		if inLoop[pc] && vm.DecodeOp(inst) == vm.OP_CALL {
			return true
		}
	}
	return false
}

func hasFieldDispatchCallInLoop(proto *vm.FuncProto) bool {
	fn := BuildGraph(proto)
	if fn == nil || fn.Entry == nil || fn.Unpromotable {
		return false
	}
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpCall && len(instr.Args) > 0 &&
				callCalleeIsFieldDispatchValue(instr.Args[0]) {
				return true
			}
		}
	}
	return false
}

func callCalleeIsFieldDispatchValue(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	def := v.Def
	for def.Op == OpGuardType && len(def.Args) == 1 && def.Args[0] != nil && def.Args[0].Def != nil {
		def = def.Args[0].Def
	}
	switch def.Op {
	case OpSelf:
		return true
	case OpGetField:
		if len(def.Args) == 0 || def.Args[0] == nil || def.Args[0].Def == nil {
			return true
		}
		// Static library calls like math.floor should be handled by intrinsic
		// lowering before this gate. Do not admit unresolved global-field calls
		// as generic dynamic dispatch.
		return def.Args[0].Def.Op != OpGetGlobal
	default:
		return false
	}
}

func canPromoteWithNativeLoopCalls(proto *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if proto == nil || len(globals) == 0 {
		return false
	}
	inLoop := staticLoopPCs(proto)
	sawCall := false
	for pc, inst := range proto.Code {
		if !inLoop[pc] || vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		sawCall = true
		callee, ok := findGetGlobalCallee(proto, pc, vm.DecodeA(inst), globals)
		if !ok || !tier2LoopCallCalleeIsNativeCandidate(callee, globals) {
			return false
		}
	}
	return sawCall
}

func staticLoopPCs(proto *vm.FuncProto) []bool {
	if proto == nil || len(proto.Code) == 0 {
		return nil
	}
	inLoop := make([]bool, len(proto.Code))
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op != vm.OP_FORLOOP && op != vm.OP_JMP {
			continue
		}
		target := pc + 1 + vm.DecodesBx(inst)
		if target < 0 || target > pc {
			continue
		}
		for i := target; i <= pc && i < len(inLoop); i++ {
			inLoop[i] = true
		}
	}
	return inLoop
}

func hasNonNativeCallInLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpCall && !tier2LoopCallIsNativeCandidate(fn, instr, globals) {
				return true
			}
		}
	}
	return false
}

func hasBlockingNonNativeCallInLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	if !hasNonNativeCallInLoop(fn, globals) {
		return false
	}
	return !nonNativeCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(fn, globals)
}

type tier2LoopRange struct {
	minPC       int
	maxPC       int
	blockCount  int
	hasCall     bool
	hasBlocker  bool
	callFreeOps int
}

func staticCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(proto *vm.FuncProto) bool {
	if proto == nil || len(proto.Code) == 0 {
		return false
	}
	ranges := make([]tier2LoopRange, 0, 4)
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op != vm.OP_FORLOOP && op != vm.OP_JMP {
			continue
		}
		target := pc + 1 + vm.DecodesBx(inst)
		if target < 0 || target > pc {
			continue
		}
		r := tier2LoopRange{minPC: target, maxPC: pc, blockCount: pc - target + 1, callFreeOps: pc - target + 1}
		for i := target; i <= pc && i < len(proto.Code); i++ {
			if vm.DecodeOp(proto.Code[i]) == vm.OP_CALL {
				r.hasCall = true
				break
			}
		}
		ranges = append(ranges, r)
	}
	return loopRangesHavePrefixBlockersBeforeHotCallFreeLoop(ranges)
}

func nonNativeCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	if fn == nil {
		return false
	}
	li := computeLoopInfo(fn)
	if li == nil || !li.hasLoops() {
		return false
	}
	ranges := make([]tier2LoopRange, 0, len(li.loopHeaders))
	for _, blocks := range li.headerBlocks {
		r := tier2LoopRange{minPC: -1, maxPC: -1, blockCount: len(blocks)}
		for _, block := range fn.Blocks {
			if block == nil || !blocks[block.ID] {
				continue
			}
			for _, instr := range block.Instrs {
				if instr == nil {
					continue
				}
				if instr.HasSource {
					if r.minPC < 0 || instr.SourcePC < r.minPC {
						r.minPC = instr.SourcePC
					}
					if instr.SourcePC > r.maxPC {
						r.maxPC = instr.SourcePC
					}
				}
				if instr.Op == OpCall {
					r.hasCall = true
					if !tier2LoopCallIsNativeCandidate(fn, instr, globals) {
						r.hasBlocker = true
					}
					continue
				}
				if !instr.Op.IsTerminator() {
					r.callFreeOps++
				}
			}
		}
		if r.minPC >= 0 && r.maxPC >= r.minPC {
			ranges = append(ranges, r)
		}
	}
	return loopRangesHavePrefixBlockersBeforeHotCallFreeLoop(ranges)
}

func loopRangesHavePrefixBlockersBeforeHotCallFreeLoop(ranges []tier2LoopRange) bool {
	if len(ranges) < 2 {
		return false
	}
	firstBlockerMax := -1
	blockerOps := 0
	for _, r := range ranges {
		if !(r.hasBlocker || r.hasCall) {
			continue
		}
		if firstBlockerMax < 0 || r.maxPC > firstBlockerMax {
			firstBlockerMax = r.maxPC
		}
		if r.callFreeOps > blockerOps {
			blockerOps = r.callFreeOps
		}
	}
	if firstBlockerMax < 0 {
		return false
	}
	for _, r := range ranges {
		if r.hasBlocker || r.hasCall || r.minPC <= firstBlockerMax {
			continue
		}
		if r.blockCount >= 2 || r.callFreeOps >= blockerOps {
			return true
		}
	}
	return false
}

// hasExpensiveInLoop (R162) generalizes hasCallInLoop: it reports whether
// any op in a loop block matches a predicate. Used to gate Tier 2
// promotion on "post-pipeline body free of exit-resume-prone ops in
// loops". Includes: OpCall (exit-resume callee dispatch), OpGetTable /
// OpSetTable (dynamic-key table ops exit to executeTableExit),
// OpNewTable (residual allocations after EA fail → exit to Go),
// OpConcat / OpAppend / OpSetList / OpSelf / OpGetUpval / OpSetUpval
// / OpGo / OpSend / OpRecv / OpClosure / OpVararg (all exit-resume).
// Not included: OpGetField / OpSetField (static key, inline-cached,
// fast). GuardType is ok (<5 insns, not exit-resume).
func hasExpensiveInLoop(fn *Function, predicate func(Op) bool) bool {
	var li *loopInfo
	for _, block := range fn.Blocks {
		match := false
		for _, instr := range block.Instrs {
			if predicate(instr.Op) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		if li == nil {
			li = computeLoopInfo(fn)
		}
		if li.loopBlocks[block.ID] {
			return true
		}
	}
	return false
}

// hasExitResumeInLoop (R162) is the STRICT smart-gate predicate used
// for LoopDepth<2 candidates (the R162 widen bucket). Returns true
// when the post-pipeline IR has ANY op in a loop that's likely to
// exit-resume, including dynamic-key OpGetTable/OpSetTable, residual
// OpNewTable, and the always-exit-resume ops below. This is stricter
// than hasCallInLoop because the widen bucket is untested at Tier 2
// (never compiled there before R162) and we want a conservative
// bound to avoid correctness bugs (R152-observed int48-overflow +
// LCG + qs correctness bug was triggered by newly-admitted LoopDepth=1
// protos).
//
// OpGetField/OpSetField excluded (IC-cached, ~5 insns fast path). OpCall is
// allowed only when it statically resolves to a callee that can use the native
// path: an already-Tier2 direct entry, a tier-up-eligible stable function, a
// self-recursive raw-int callee, or a small leaf native candidate.
func hasExitResumeInLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	_, ok := firstExitResumeInLoop(fn, globals)
	return ok
}

func firstTier2ModBlockerInLoop(fn *Function) (string, bool) {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op != OpMod {
				continue
			}
			// Generic Mod now has native int/float lowering with op-exit fallback
			// for zero divisors and non-numeric operands. It is no longer an
			// exit-storm blocker by itself.
			continue
		}
	}
	return "", false
}

func tier2GenericModIsNativeNumeric(instr *Instr) bool {
	if instr == nil || instr.Op != OpMod || len(instr.Args) < 2 {
		return false
	}
	if instr.Type == TypeFloat {
		return true
	}
	return tier2ValueIsNativeNumeric(instr.Args[0], make(map[int]bool)) &&
		tier2ValueIsNativeNumeric(instr.Args[1], make(map[int]bool))
}

func tier2ValueIsNativeNumeric(v *Value, seen map[int]bool) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if v.Def.Type == TypeInt || v.Def.Type == TypeFloat {
		return true
	}
	if seen[v.ID] {
		return true
	}
	seen[v.ID] = true

	switch v.Def.Op {
	case OpConstInt, OpConstFloat, OpUnboxInt, OpUnboxFloat:
		return true
	case OpGuardType:
		t := Type(v.Def.Aux)
		return t == TypeInt || t == TypeFloat
	case OpGuardIntRange:
		return v.Def.Type == TypeInt && len(v.Def.Args) == 1 &&
			tier2ValueIsNativeNumeric(v.Def.Args[0], seen)
	case OpPhi:
		return tier2AllValuesNativeNumeric(v.Def.Args, seen)
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpFloor:
		return tier2AllValuesNativeNumeric(v.Def.Args, seen)
	default:
		return false
	}
}

func tier2AllValuesNativeNumeric(values []*Value, seen map[int]bool) bool {
	if len(values) == 0 {
		return false
	}
	for _, arg := range values {
		if !tier2ValueIsNativeNumeric(arg, seen) {
			return false
		}
	}
	return true
}

func tier2GenericModIsSmallConstAdditiveLoopCounter(instr *Instr) bool {
	if instr == nil || instr.Op != OpMod || len(instr.Args) < 2 {
		return false
	}
	if instr.Type == TypeFloat {
		return false
	}
	divisor := instr.Args[1]
	if divisor == nil || divisor.Def == nil || divisor.Def.Op != OpConstInt {
		return false
	}
	if divisor.Def.Aux == 0 || divisor.Def.Aux < -16 || divisor.Def.Aux > 16 {
		return false
	}
	return tier2ValueIsAdditiveIntLike(instr.Args[0], make(map[int]bool))
}

func tier2ValueIsAdditiveIntLike(v *Value, seen map[int]bool) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if v.Def.Type == TypeFloat {
		return false
	}
	if seen[v.ID] {
		return true
	}
	seen[v.ID] = true

	switch v.Def.Op {
	case OpConstInt, OpUnboxInt:
		return true
	case OpGuardType:
		return v.Def.Type == TypeInt && len(v.Def.Args) == 1 &&
			tier2ValueIsAdditiveIntLike(v.Def.Args[0], seen)
	case OpGuardIntRange:
		return v.Def.Type == TypeInt && len(v.Def.Args) == 1 &&
			tier2ValueIsAdditiveIntLike(v.Def.Args[0], seen)
	case OpPhi:
		return tier2AllValuesAdditiveIntLike(v.Def.Args, seen)
	case OpAdd, OpAddInt:
		return tier2SmallConstPlusAdditive(v.Def.Args, seen)
	case OpSub, OpSubInt:
		return tier2AdditiveMinusSmallConst(v.Def.Args, seen)
	default:
		return false
	}
}

func tier2AllValuesAdditiveIntLike(values []*Value, seen map[int]bool) bool {
	if len(values) == 0 {
		return false
	}
	for _, arg := range values {
		if !tier2ValueIsAdditiveIntLike(arg, seen) {
			return false
		}
	}
	return true
}

func tier2SmallConstPlusAdditive(args []*Value, seen map[int]bool) bool {
	if len(args) < 2 {
		return false
	}
	if tier2ValueIsSmallConst(args[0], 16) {
		return tier2ValueIsAdditiveIntLike(args[1], seen)
	}
	if tier2ValueIsSmallConst(args[1], 16) {
		return tier2ValueIsAdditiveIntLike(args[0], seen)
	}
	return false
}

func tier2AdditiveMinusSmallConst(args []*Value, seen map[int]bool) bool {
	if len(args) < 2 {
		return false
	}
	return tier2ValueIsAdditiveIntLike(args[0], seen) &&
		tier2ValueIsSmallConst(args[1], 16)
}

func tier2ValueIsSmallConst(v *Value, limit int64) bool {
	if v == nil || v.Def == nil || v.Def.Op != OpConstInt {
		return false
	}
	c := v.Def.Aux
	return c >= -limit && c <= limit
}

func firstExitResumeInLoop(fn *Function, globals map[string]*vm.FuncProto) (Op, bool) {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				if tier2LoopCallIsNativeCandidate(fn, instr, globals) {
					continue
				}
				return instr.Op, true
			case OpSelf,
				OpNewTable, OpNewFixedTable,
				OpGetTable, OpSetTable,
				OpConcat, OpAppend, OpSetList,
				OpGetUpval, OpSetUpval,
				OpGo, OpMakeChan, OpSend, OpRecv,
				OpClosure, OpClose,
				OpVararg,
				OpLen, OpPow,
				OpTForCall, OpTForLoop:
				return instr.Op, true
			}
		}
	}
	return OpNop, false
}

func firstUnsupportedHighArityCallResultShapeInLoop(fn *Function) (Op, bool) {
	const maxSimpleCallArgs = 3
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				// Simple string.format-style loop calls are covered by existing
				// no-filter coverage. The unsafe log-tokenize case is a high-arity
				// inlined vararg call whose source shape no longer belongs to fn.Proto.
				if len(instr.Args)-1 <= maxSimpleCallArgs {
					continue
				}
				nRets, ok := callExactFixedResultCountFromC(instr.Aux2)
				if !ok || !callABIHasExactResultShape(fn, instr, nRets) {
					return instr.Op, true
				}
			case OpStringFormatConst:
				// High-arity format op-exits are acceptable in loop cold branches,
				// but should not re-open always-hot inlined formatting loops.
				if len(instr.Args)-2 > maxSimpleCallArgs && !tier2BlockIsModuloColdBranch(block) {
					return instr.Op, true
				}
			}
		}
	}
	return OpNop, false
}

func tier2BlockIsModuloColdBranch(block *Block) bool {
	if block == nil || len(block.Preds) != 1 {
		return false
	}
	pred := block.Preds[0]
	if pred == nil || len(pred.Instrs) == 0 {
		return false
	}
	term := pred.Instrs[len(pred.Instrs)-1]
	if term == nil || term.Op != OpBranch || len(term.Args) == 0 || term.Args[0] == nil ||
		term.Args[0].Def == nil || term.Args[0].Def.Op != OpModZeroInt {
		return false
	}
	divisor := term.Args[0].Def.Aux
	if divisor < 0 {
		divisor = -divisor
	}
	return divisor >= 100
}

func firstCallBoundaryTier2BlockerInLoop(fn *Function, globals map[string]*vm.FuncProto) (Op, bool) {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				if tier2LoopCallIsNativeCandidate(fn, instr, globals) {
					continue
				}
				return instr.Op, true
			case OpSelf,
				OpConcat, OpAppend, OpSetList,
				OpGetUpval, OpSetUpval,
				OpGo, OpMakeChan, OpSend, OpRecv,
				OpClosure, OpClose,
				OpVararg,
				OpPow,
				OpTForCall, OpTForLoop:
				return instr.Op, true
			case OpNewTable:
				if tier2NewTableLoopCandidateIsSafe(instr) {
					continue
				}
				return instr.Op, true
			case OpNewFixedTable:
				if tier2NewFixedTableLoopCandidateIsSafe(fn, instr) {
					continue
				}
				return instr.Op, true
			case OpSetTable:
				if tier2SetTableLoopCandidateIsSafe(fn, instr) {
					continue
				}
				return instr.Op, true
			}
		}
	}
	return OpNop, false
}

func hasReadWriteGlobalInSameLoop(fn *Function) bool {
	li := computeLoopInfo(fn)
	for _, blocks := range li.headerBlocks {
		read := make(map[int64]bool)
		write := make(map[int64]bool)
		for _, block := range fn.Blocks {
			if !blocks[block.ID] {
				continue
			}
			for _, instr := range block.Instrs {
				switch instr.Op {
				case OpGetGlobal:
					read[instr.Aux] = true
				case OpSetGlobal:
					write[instr.Aux] = true
				}
			}
		}
		for nameIdx := range write {
			if read[nameIdx] {
				return true
			}
		}
	}
	return false
}

func tier2NewTableLoopCandidateIsSafe(instr *Instr) bool {
	// Direct-entry Tier 2 can execute cache-backed NEWTABLE sites in loops:
	// the hot path pops a fresh table from the compiled function cache, while
	// cache misses use the precise table-exit continuation and mark the result
	// slot as modified. Restart-style OSR still rejects OpNewTable via
	// firstExitResumeInLoop/hasRestartVisibleSideEffect.
	return newTableCacheBatchSize(instr) > 1
}

func tier2NewFixedTableLoopCandidateIsSafe(fn *Function, instr *Instr) bool {
	// Fixed-shape constructors have the same direct-entry property as cached
	// NEWTABLE sites: the common path reuses a cached table and only misses
	// through the table-exit continuation once per cache refill batch.
	if fn == nil || instr == nil || instr.Op != OpNewFixedTable {
		return false
	}
	return fixedTableCtor2Cacheable(fn.Proto, instr) || fixedTableCtorNCacheable(fn.Proto, instr)
}

func hasIndexedGlobalLoopProtocol(fn *Function) bool {
	if !fnSupportsNativeSetGlobalProtocol(fn) {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetGlobal && instr.Op != OpSetGlobal {
				continue
			}
			constIdx := int(instr.Aux)
			if constIdx < 0 || constIdx >= len(fn.Proto.Constants) || !fn.Proto.Constants[constIdx].IsString() {
				return false
			}
		}
	}
	return true
}

func firstSelfRecursiveTableMutationInLoop(fn *Function) (Op, bool) {
	if !irHasSelfCall(fn) {
		return OpNop, false
	}
	summary := analyzeLoopTableMutationRecovery(fn)
	if site, ok := summary.firstUnadmitted(); ok {
		return site.Op, true
	}
	return OpNop, false
}

func tier2SetTableLoopCandidateIsSafe(fn *Function, instr *Instr) bool {
	if irHasSelfCall(fn) {
		return loopTableMutationRecoveryAdmitsInstr(fn, instr)
	}
	if isLocalTableRowSetTable(instr) {
		return true
	}
	// Aux2 carries monomorphic array-kind feedback from Tier 1. Only typed
	// arrays get the Tier 2 append/write fast path; Mixed stores remain too
	// broad because they can carry pointers and rely more on runtime table
	// growth/absorb behavior.
	switch instr.Aux2 {
	case int64(vm.FBKindInt), int64(vm.FBKindFloat), int64(vm.FBKindBool):
		return true
	default:
		return isScalarArraySetTable(instr)
	}
}

func isLocalTableRowSetTable(instr *Instr) bool {
	if instr == nil || instr.Op != OpSetTable || len(instr.Args) < 3 {
		return false
	}
	if instr.Aux2 != 0 && instr.Aux2 != int64(vm.FBKindMixed) {
		return false
	}
	if !isIntLikeTableKey(instr.Args[1], make(map[int]bool)) {
		return false
	}
	tbl := instr.Args[0]
	val := instr.Args[2]
	if tbl == nil || tbl.Def == nil || tbl.Def.Op != OpNewTable {
		return false
	}
	return val != nil && val.Def != nil && val.Def.Type == TypeTable
}

func hasStaticSelfRecursivePartitionSetTableLoop(proto *vm.FuncProto) bool {
	if proto == nil || !staticallyCallsOnlySelf(proto) {
		return false
	}
	inLoop := staticLoopPCs(proto)
	setTablesByTableReg := make(map[int]int)
	for pc, inst := range proto.Code {
		if pc >= len(inLoop) || !inLoop[pc] || vm.DecodeOp(inst) != vm.OP_SETTABLE {
			continue
		}
		tableReg := vm.DecodeA(inst)
		setTablesByTableReg[tableReg]++
		if setTablesByTableReg[tableReg] >= 2 {
			return true
		}
	}
	return false
}

func isScalarArraySetTable(instr *Instr) bool {
	if instr == nil || instr.Op != OpSetTable || len(instr.Args) < 3 {
		return false
	}
	key := instr.Args[1]
	val := instr.Args[2]
	if !isIntLikeTableKey(key, make(map[int]bool)) || val == nil || val.Def == nil {
		return false
	}
	switch val.Def.Type {
	case TypeInt, TypeFloat, TypeBool:
		return true
	case TypeString:
		return instr.Args[0] != nil && instr.Args[0].Def != nil && instr.Args[0].Def.Op == OpNewTable
	default:
		return tier2ValueIsNativeNumeric(val, make(map[int]bool))
	}
}

func isIntLikeTableKey(v *Value, seen map[int]bool) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if v.Def.Type == TypeInt {
		return true
	}
	if seen[v.ID] {
		return true
	}
	seen[v.ID] = true
	switch v.Def.Op {
	case OpConstInt, OpUnboxInt:
		return true
	case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact:
		return allIntLikeArgs(v.Def, seen)
	case OpAdd, OpSub, OpMul, OpMod:
		return allIntLikeArgs(v.Def, seen)
	case OpPhi:
		return allIntLikeArgs(v.Def, seen)
	default:
		return false
	}
}

func allIntLikeArgs(instr *Instr, seen map[int]bool) bool {
	if instr == nil || len(instr.Args) == 0 {
		return false
	}
	for _, arg := range instr.Args {
		if !isIntLikeTableKey(arg, seen) {
			return false
		}
	}
	return true
}

func tier2LoopCallIsNativeCandidate(fn *Function, instr *Instr, globals map[string]*vm.FuncProto) bool {
	if instr != nil && len(instr.Args) > 0 && callCalleeIsFieldDispatchValue(instr.Args[0]) {
		return true
	}
	_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
	return tier2LoopCallCalleeIsNativeCandidate(callee, globals)
}

func tier2LoopCallCalleeIsNativeCandidate(callee *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if tier2LoopCallCalleeHasTier2DirectEntry(callee) {
		return true
	}
	if callee != nil && tier2LoopCallCalleeCanTierUp(callee, globals) {
		return true
	}
	if callee != nil && staticallyCallsOnlySelf(callee) {
		ok, _ := qualifyForNumeric(callee)
		return ok
	}
	if callee != nil && tier2LoopCallCalleeIsLeafNativeCandidate(callee) {
		return true
	}
	if callee != nil && shouldStayTier1ForBoxedRawIntKernel(callee, analyzeFuncProfile(callee)) {
		return true
	}
	return false
}

func tier2LoopCallCalleeHasTier2DirectEntry(callee *vm.FuncProto) bool {
	return callee != nil && callee.Tier2Promoted &&
		(callee.DirectEntryPtr != 0 || callee.Tier2DirectEntryPtr != 0)
}

func tier2LoopCallCalleeCanTierUp(callee *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if callee == nil || callee.IsVarArg {
		return false
	}
	if !canPromoteToTier2(callee) {
		return false
	}
	profile := analyzeFuncProfile(callee)
	if shouldStayTier0(profile) {
		return false
	}
	if profile.LoopDepth < 2 {
		if !profile.HasLoop {
			return false
		}
		return tier2LoopCallCalleePassesLoopDepth1Gate(callee, globals)
	}
	runtimeCallCount := callee.CallCount
	if runtimeCallCount < tmDefaultTier2Threshold {
		runtimeCallCount = tmDefaultTier2Threshold
	}
	return shouldPromoteTier2(callee, profile, runtimeCallCount)
}

func tier2LoopCallCalleePassesLoopDepth1Gate(callee *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	fn := BuildGraph(callee)
	if fn == nil || fn.Entry == nil || fn.Unpromotable {
		return false
	}
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals: globals,
		InlineMaxSize: inlineMaxCalleeSize,
	})
	if err != nil {
		return false
	}
	if _, ok := firstTier2ModBlockerInLoop(fn); ok {
		return false
	}
	if _, blocked := firstCallBoundaryTier2BlockerInLoop(fn, globals); blocked {
		return false
	}
	return true
}

func tier2LoopCallCalleeIsLeafNativeCandidate(callee *vm.FuncProto) bool {
	if callee == nil || callee.IsVarArg || len(callee.Code) > inlineMaxCalleeSize {
		return false
	}
	if !canPromoteToTier2(callee) {
		return false
	}
	profile := analyzeFuncProfile(callee)
	if profile.HasLoop {
		return false
	}
	for _, inst := range callee.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_LEN,
			vm.OP_CONCAT,
			vm.OP_APPEND,
			vm.OP_SETLIST,
			vm.OP_SELF,
			vm.OP_GETUPVAL,
			vm.OP_SETUPVAL,
			vm.OP_CLOSURE,
			vm.OP_VARARG,
			vm.OP_POW,
			vm.OP_TFORCALL,
			vm.OP_TFORLOOP,
			vm.OP_GO,
			vm.OP_MAKECHAN,
			vm.OP_SEND,
			vm.OP_RECV:
			return false
		}
	}
	return true
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
