//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"os"
	"time"

	"github.com/gscript/gscript/internal/vm"
)

func (tm *TieringManager) compileTier2(proto *vm.FuncProto) (cf *CompiledFunction, retErr error) {
	tm.tier2Attempts++
	attempt := tm.tier2Attempts
	tm.traceEvent("tier2_attempt", "tier2", proto, map[string]any{
		"attempt":    attempt,
		"call_count": proto.CallCount,
	})
	trace := tm.warmDumpTrace(proto)
	recordedWarmDump := false
	if tm.envR154Trace {
		fmt.Fprintf(os.Stderr, "[R154] compileTier2 ENTER proto=%q attempts=%d\n",
			proto.Name, tm.tier2Attempts)
		defer fmt.Fprintf(os.Stderr, "[R154] compileTier2 EXIT  proto=%q err=%v\n",
			proto.Name, retErr)
	}
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
			tm.traceEvent("tier2_fail", "tier2", proto, map[string]any{
				"attempt": attempt,
				"reason":  retErr.Error(),
			})
			if trace != nil && !recordedWarmDump {
				tm.recordWarmDumpCompile(proto, trace, cf, retErr)
			}
			if os.Getenv("GSCRIPT_JIT_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "tier2: compilation failed for %q: %v\n", proto.Name, retErr)
			}
		} else if os.Getenv("GSCRIPT_JIT_DEBUG") == "1" {
			tm.traceTier2Success(proto, cf, attempt)
			fmt.Fprintf(os.Stderr, "tier2: compiled %q\n", proto.Name)
		} else {
			tm.traceTier2Success(proto, cf, attempt)
		}
	}()

	cf, retErr = tm.compileTier2Pipeline(proto, trace)
	if trace != nil {
		tm.recordWarmDumpCompile(proto, trace, cf, retErr)
		recordedWarmDump = true
	}
	return cf, retErr
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
	tm.installTier2(proto, t2)

	return nil
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
	var remarks *OptimizationRemarks
	if trace != nil {
		remarks = &OptimizationRemarks{}
		defer func() {
			trace.OptimizationRemarks = remarks.List()
		}()
	}
	runStage := func(name string, body func() error) error {
		if trace == nil {
			return body()
		}
		start := time.Now()
		err := body()
		trace.PipelineStages = append(trace.PipelineStages, newPipelineStageTiming(name, time.Since(start), err))
		return err
	}

	if err := runStage("Tier2Gate", func() error {
		if !canPromoteToTier2(proto) {
			if op, ok := firstUnsupportedTier2Bytecode(proto); ok {
				remarks.Add("Tier2Gate", "blocked", 0, 0, OpNop,
					fmt.Sprintf("unsupported bytecode %s", op))
			} else {
				remarks.Add("Tier2Gate", "blocked", 0, 0, OpNop,
					"function has unsupported ops")
			}
			return fmt.Errorf("tier2: function has unsupported ops, staying at tier 1")
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var fn *Function
	if err := runStage("BuildGraph", func() error {
		fn = BuildGraph(proto)
		fn.Remarks = remarks
		if trace != nil {
			trace.IRBefore = Print(fn)
		}
		if fn.Unpromotable {
			remarks.Add("Tier2Gate", "blocked", 0, 0, OpNop,
				"BuildGraph marked function unpromotable")
			return fmt.Errorf("tier2: function uses unmodeled bytecode (variadic CALL), staying at Tier 1")
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := runStage("ValidateInitialIR", func() error {
		if errs := Validate(fn); len(errs) > 0 {
			remarks.Add("Tier2Gate", "blocked", 0, 0, OpNop,
				"initial IR validation failed: "+errs[0].Error())
			return fmt.Errorf("tier2: validation failed: %v", errs[0])
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var inlineGlobals map[string]*vm.FuncProto
	var loopCallGlobals map[string]*vm.FuncProto
	var opts *Tier2PipelineOpts
	if err := runStage("BuildPipelineOptions", func() error {
		inlineGlobals = tm.buildInlineGlobals()
		loopCallGlobals = inlineGlobals
		loopCallGlobalsOwned := false
		if protoGlobals := buildProtoInlineGlobals(proto); len(protoGlobals) > 0 {
			loopCallGlobals = make(map[string]*vm.FuncProto, len(inlineGlobals)+len(protoGlobals))
			loopCallGlobalsOwned = true
			for name, calleeProto := range inlineGlobals {
				loopCallGlobals[name] = calleeProto
			}
			for name, calleeProto := range protoGlobals {
				if _, ok := loopCallGlobals[name]; !ok {
					loopCallGlobals[name] = calleeProto
				}
			}
		}
		if stableGlobals := buildProtoStableGlobals(proto); len(stableGlobals) > 0 {
			if !loopCallGlobalsOwned {
				loopCallGlobals = make(map[string]*vm.FuncProto, len(inlineGlobals)+len(stableGlobals))
				loopCallGlobalsOwned = true
				for name, calleeProto := range inlineGlobals {
					loopCallGlobals[name] = calleeProto
				}
			}
			for name, calleeProto := range stableGlobals {
				if _, ok := loopCallGlobals[name]; !ok {
					loopCallGlobals[name] = calleeProto
				}
			}
		}
		opts = &Tier2PipelineOpts{
			InlineGlobals:         inlineGlobals,
			ProtocolGlobals:       loopCallGlobals,
			InlineMaxSize:         inlineMaxCalleeSize,
			FixedShapeArgFacts:    inferGuardedFixedShapeArgFactsForProto(proto, loopCallGlobals),
			FixedShapeEntryGuards: true,
			Remarks:               remarks,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var intrinsicNotes []string
	if err := runStage("RunTier2Pipeline", func() error {
		var err error
		fn, intrinsicNotes, err = RunTier2Pipeline(fn, opts)
		if err != nil {
			remarks.Add("Tier2Gate", "blocked", 0, 0, OpNop,
				"optimization pipeline failed: "+err.Error())
			return fmt.Errorf("tier2: pipeline: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := runStage("PostPipelineGates", func() error {
		if len(intrinsicNotes) > 0 {
			proto.NeedsTier2 = true
		}
		if shouldStayTier1ForBoxedRawIntKernel(proto, analyzeFuncProfile(proto)) {
			forceRawIntKernelIR(fn)
			if op, ok := firstResidualRawIntKernelGenericNumeric(fn); ok {
				remarks.Add("Tier2Gate", "blocked", 0, 0, op,
					fmt.Sprintf("raw-int kernel has residual generic numeric op %s", op))
				return fmt.Errorf("tier2: raw-int kernel has residual generic numeric op %s, staying at Tier 1", op)
			}
		}
		if op, ok := firstUnsupportedHighArityCallResultShapeInLoop(fn); ok {
			remarks.Add("Tier2Gate", "blocked", 0, 0, op,
				"high-arity loop call exit lacks a fixed result shape")
			return fmt.Errorf("tier2: high-arity loop call exit lacks fixed result shape, staying at Tier 1")
		}
		fn.CarryPreheaderInvariants = true
		if trace != nil {
			trace.IRAfter = Print(fn)
			trace.IntrinsicNotes = intrinsicNotes
		}

		if op, ok := firstSelfRecursiveTableMutationInLoop(fn); ok {
			remarks.Add("Tier2Gate", "blocked", 0, 0, op,
				fmt.Sprintf("self-recursive loop has residual table mutation %s", op))
			return fmt.Errorf("tier2: self-recursive loop has residual table mutation %s (exit-storm blocked), staying at Tier 1", op)
		}
		if modReason, ok := firstTier2ModBlockerInLoop(fn); ok {
			if !shouldStayTier1ForBoxedRawIntKernel(proto, analyzeFuncProfile(proto)) {
				remarks.Add("Tier2Gate", "blocked", 0, 0, OpMod,
					modReason+" remains inside loop")
				return fmt.Errorf("tier2: has %s (performance-blocked), staying at Tier 1", modReason)
			}
		}

		// R162/R171: reject Tier 2 promotion when a loop contains operations
		// whose Tier 2 path is still expected to be slower than Tier 1. This is
		// deliberately a call-boundary performance filter, not the restart-OSR
		// correctness filter: functions compiled before entering bytecode PC 0 do
		// not replay partially executed table mutations. Restart-style OSR remains
		// gated by isOSRRestartSafe before the OSR counter is armed.
		//
		// Bypass via GSCRIPT_TIER2_NO_FILTER=1 (diagnostic / perf-comparison).
		//
		// Depth-aware filter (R162): old LoopDepth>=2 candidates use the classic
		// non-native-call filter. LoopDepth<2 candidates use the stricter blocker
		// list below, but read-only OpGetTable is allowed because Tier 2 has native
		// int-key table fast paths plus table-exit resume metadata for misses.
		// Table writes that can resize/mutate dynamic structure, residual
		// allocations, and non-native calls are still blocked by default.
		if !tm.envTier2NoFilter {
			profile := tm.getProfile(proto)
			if profile.LoopDepth < 2 {
				if hasReadWriteGlobalInSameLoop(fn) {
					if !hasIndexedGlobalLoopProtocol(fn) {
						remarks.Add("Tier2Gate", "blocked", 0, 0, OpSetGlobal,
							"LoopDepth<2 candidate reads and writes a global in the same loop")
						return fmt.Errorf("tier2: LoopDepth<2 candidate has read/write global state inside loop, staying at Tier 1")
					}
					remarks.Add("Tier2Gate", "changed", 0, 0, OpSetGlobal,
						"LoopDepth<2 read/write globals accepted by indexed native global protocol")
				}
				if op, ok := firstCallBoundaryTier2BlockerInLoop(fn, loopCallGlobals); ok {
					remarks.Add("Tier2Gate", "blocked", 0, 0, op,
						fmt.Sprintf("LoopDepth<2 candidate has performance-blocked %s inside loop", op))
					return fmt.Errorf("tier2: LoopDepth<2 candidate has performance-blocked op %s inside loop, staying at Tier 1", op)
				}
			} else {
				if hasBlockingNonNativeCallInLoop(fn, loopCallGlobals) {
					remarks.Add("Tier2Gate", "blocked", 0, 0, OpCall,
						"non-native OpCall remains inside loop after inlining")
					return fmt.Errorf("tier2: has OpCall inside loop (performance-blocked), staying at Tier 1")
				}
			}
		}

		// R40: mark Proto.HasSelfCalls so the emitter opts in to the
		// t2_self_entry lightweight path. A self-call is an OpCall whose
		// function argument comes from an OpGetGlobal loading this proto's
		// own name.
		proto.LeafNoCall = protoHasNoCallLikeOps(proto)
		proto.NoGlobalOps = protoHasNoGlobalOps(proto)
		if irHasSelfCall(fn) {
			proto.HasSelfCalls = true
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var alloc *RegAllocation
	if err := runStage("RegAlloc", func() error {
		alloc = AllocateRegisters(fn)
		if trace != nil {
			trace.RegAllocMap = formatRegAlloc(alloc)
			trace.LoopDiagnostics = BuildLoopDiagnostics(fn, alloc)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var cf *CompiledFunction
	if err := runStage("ARM64Compile", func() error {
		var err error
		cf, err = Compile(fn, alloc)
		if err != nil {
			remarks.Add("Tier2Gate", "blocked", 0, 0, OpNop,
				"ARM64 compile failed: "+err.Error())
			return fmt.Errorf("tier2: compile failed: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if trace != nil {
		if err := runStage("SourceMap", func() error {
			trace.SourceMap = BuildIRASMMap(fn, cf.InstrCodeRanges)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	if cf.numRegs > proto.MaxStack {
		proto.MaxStack = cf.numRegs
	}

	// R124: The numeric entry (t2_numeric_self_entry_N) is emitted as
	// an extra label at the end of the same code block when the proto
	// qualifies, so caller BL is compile-time PC-relative.
	if ok, numParams := qualifyForNumeric(proto); ok {
		cf.NumericParamCount = numParams
	}

	return cf, nil
}
