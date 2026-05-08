//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// executeTier2 runs a Tier 2 compiled function using the VM's register file.
// This is the Tier 2 execute loop, handling exit codes and resuming JIT code.
func (tm *TieringManager) executeTier2(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto) ([]runtime.Value, error) {
	return tm.executeTier2WithResultBuffer(cf, regs, base, proto, tm.retBuf[:0])
}

func (tm *TieringManager) executeTier2WithResultBuffer(cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	if results, handled, err := tm.executeCompiledProtocol(cf, regs, base, proto, retBuf); handled {
		return results, err
	}
	if tm.callVM != nil {
		regs = tm.ensureTier2RegisterBudget(cf, regs, base, proto)
	}

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
	ctx := getTier2ExecContext()
	defer putTier2ExecContext(ctx)
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	ctx.RegsBase = uintptr(unsafe.Pointer(&regs[0]))
	ctx.RegsEnd = ctx.RegsBase + uintptr(len(regs)*jit.ValueSize)
	ctx.RawSelfRegsEnd = rawSelfRegsEnd(ctx.Regs, ctx.RegsEnd, cf.numRegs)
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}
	tm.setTier2FieldCacheContext(ctx, proto)
	if tm.callVM != nil {
		ctx.TopPtr = uintptr(unsafe.Pointer(tm.callVM.TopPtr()))
		if cl := tm.callVM.CurrentClosure(); cl != nil {
			ctx.BaselineClosurePtr = uintptr(unsafe.Pointer(cl))
		}
	}

	// Set up Tier 2 global value cache pointers.
	ctx.Tier2GlobalGenPtr = uintptr(unsafe.Pointer(&tm.tier1.globalCacheGen))
	if len(cf.GlobalCache) > 0 {
		ctx.Tier2GlobalCache = uintptr(unsafe.Pointer(&cf.GlobalCache[0]))
		ctx.Tier2GlobalCacheGen = uintptr(unsafe.Pointer(&cf.GlobalCacheGen))
	}
	if arrayPtr, verPtr, ver, ok := tm.prepareTier2GlobalIndexes(proto, cf); ok {
		ctx.Tier2GlobalIndex = proto.Tier2GlobalIndexPtr
		ctx.Tier2GlobalArray = arrayPtr
		ctx.Tier2GlobalVerPtr = uintptr(unsafe.Pointer(verPtr))
		ctx.Tier2GlobalVer = uint64(ver)
	}
	// R108: set mono call-IC cache pointer.
	if len(cf.CallCache) > 0 {
		ctx.Tier2CallCache = uintptr(unsafe.Pointer(&cf.CallCache[0]))
	}
	exitCheck := newExitResumeCheckState(cf)
	ctx.ExitResumeCheckShadow = exitCheck.shadowPtr()

	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(ctx))
	if cf.TypedSelfABI.Eligible {
		ensureTypedSelfTier2NativeStack()
	} else {
		ensureTier2NativeStack()
	}
	if tm.timeline != nil {
		tm.traceEvent("tier2_entered", "tier2", proto, map[string]any{
			"base":       base,
			"num_regs":   cf.numRegs,
			"code_bytes": cf.Code.Size(),
		})
	}

	// resyncRegs re-reads the VM's register file after exits.
	resyncRegs := func() {
		if tm.callVM == nil {
			return
		}
		regs = tm.callVM.Regs()
		ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
		ctx.RegsBase = uintptr(unsafe.Pointer(&regs[0]))
		ctx.RegsEnd = ctx.RegsBase + uintptr(len(regs)*jit.ValueSize)
		ctx.RawSelfRegsEnd = rawSelfRegsEnd(ctx.Regs, ctx.RegsEnd, cf.numRegs)
		tm.setTier2FieldCacheContext(ctx, proto)
		if cl := tm.callVM.CurrentClosure(); cl != nil {
			ctx.BaselineClosurePtr = uintptr(unsafe.Pointer(cl))
		}
	}
	syncNativeGlobals := func() {
		if tm.callVM == nil || len(cf.NativeSetGlobals) == 0 || len(cf.GlobalIndexByConst) == 0 {
			return
		}
		tm.callVM.SyncTier2GlobalMap(proto.Constants, cf.GlobalIndexByConst, cf.NativeSetGlobals)
	}

	var r154_exitCount int
	for {
		ctx.CallMode = 0
		if tm.perfStatsEnabled {
			start := time.Now()
			jit.CallJIT(codePtr, ctxPtr)
			tm.perfStats.record(perfTier2NativeExecution, time.Since(start))
		} else {
			jit.CallJIT(codePtr, ctxPtr)
		}
		syncNativeGlobals()

		if tm.envR154Trace {
			r154_exitCount++
			if r154_exitCount <= 20 || r154_exitCount%100000 == 0 {
				fmt.Fprintf(os.Stderr, "[R154] executeTier2 proto=%q exit#%d code=%d deoptID=%d resumePass=%d callID=%d globalID=%d tableExitID=%d tableOp=%d tableSlot=%d\n",
					proto.Name, r154_exitCount, ctx.ExitCode,
					ctx.DeoptInstrID, ctx.ResumeNumericPass, ctx.CallID, ctx.GlobalExitID,
					ctx.TableExitID, ctx.TableOp, ctx.TableSlot)
			}
		}

		tm.recordTier2Exit(proto, cf, ctx)

		switch ctx.ExitCode {
		case ExitNormal:
			// Tier 2 return: result in regs[base] (slot 0 relative to base).
			result := regs[base]
			return runtime.ReuseValueSlice1(retBuf, result), nil

		case ExitDeopt:
			deoptAction := Tier2DeoptPolicy{}.DecideRuntimeDeopt(proto, cf, int(ctx.ExitResumePC))
			if guardAction, ok := tm.guardDeoptRefreshAction(proto, cf, ctx); ok {
				deoptAction = guardAction
			}
			if tm.envR154Trace && tm.r154DeoptPrints < 20 {
				var r0, r1 uint64
				if base < len(regs) {
					r0 = uint64(regs[base])
				}
				if base+1 < len(regs) {
					r1 = uint64(regs[base+1])
				}
				tm.r154DeoptPrints++
				fmt.Fprintf(os.Stderr, "[R154] deopt proto=%q id=%d base=%d r0=%016x r1=%016x callID=%d globalID=%d\n",
					proto.Name, ctx.DeoptInstrID, base, r0, r1, ctx.CallID, ctx.GlobalExitID)
			}
			tm.traceEvent("runtime_deopt", "tier2", proto, map[string]any{
				"exit_code":        ctx.ExitCode,
				"deopt_instr_id":   ctx.DeoptInstrID,
				"resume_pass":      ctx.ResumeNumericPass,
				"resume_pc":        ctx.ExitResumePC,
				"action":           deoptAction.Kind,
				"reason":           deoptAction.Reason,
				"version_after":    fmt.Sprintf("%x", deoptAction.CurrentProfile.Version.Hash),
				"guards_after":     deoptAction.CurrentProfile.Version.GuardCount,
				"guard_relaxed_pc": deoptAction.GuardRelaxedPC,
				"guard_relaxed_op": deoptAction.GuardRelaxedOp,
			})
			tm.applyTier2DeoptAction(proto, deoptAction)
			if deoptAction.PreciseResume && tm.callVM != nil {
				resumePC := deoptAction.ResumePC
				ctx.ExitResumePC = 0
				tm.traceEvent("fallback", "tier0", proto, map[string]any{
					"reason": "tier2_precise_deopt",
					"target": "interpreter",
					"pc":     resumePC,
				})
				return tm.callVM.ResumeFromPC(resumePC)
			}
			tm.traceEvent("fallback", "tier0", proto, map[string]any{
				"reason": "tier2_runtime_deopt",
				"target": "interpreter",
			})
			// Bail to interpreter. Return error so the VM falls through.
			return nil, fmt.Errorf("tier2: deopt")

		case ExitCallExit:
			site := cf.exitResumeCheckSite(ctx)
			before, err := exitCheck.checkBefore(ctx, site, regs, base, protoNameForCheck(proto))
			if err != nil {
				return nil, err
			}
			if err := tm.executeCallExit(ctx, regs, base, proto, cf); err != nil {
				if vm.IsCoroutineYield(err) {
					return nil, err
				}
				return nil, fmt.Errorf("tier2: call-exit: %w", err)
			}
			tm.retireStaleTier2AfterFeedback(proto, cf)
			resyncRegs()
			if err := exitCheck.checkAfter(site, before, regs, base, protoNameForCheck(proto)); err != nil {
				return nil, err
			}
			callID := int(ctx.CallID)
			resumeOff, ok := cf.resumeOffset(callID, ctx.ResumeNumericPass != 0)
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for call %d", callID)
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
				tm.perfStats.record(perfTier2ExitResume, time.Since(start))
				continue
			}
			codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
			continue

		case ExitNativeCallExit:
			var err error
			if tm.perfStatsEnabled {
				start := time.Now()
				regs, err = tm.executeNativeCallExit(ctx, cf, regs, base, proto)
				tm.perfStats.record(perfTier2NativeCallExitProtocol, time.Since(start))
			} else {
				regs, err = tm.executeNativeCallExit(ctx, cf, regs, base, proto)
			}
			if err != nil {
				if err == errNestedNativeCallExit {
					// Known fallback: avoid wrapping with fmt.Errorf on the
					// hot recursive leaf path.
					return nil, err
				}
				return nil, fmt.Errorf("tier2: native-call-exit: %w", err)
			}
			tm.retireStaleTier2AfterFeedback(proto, cf)
			resyncRegs()
			callID := int(ctx.CallID)
			resumeOff, ok := cf.resumeOffset(callID, ctx.ResumeNumericPass != 0)
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for native call %d", callID)
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
				tm.perfStats.record(perfTier2ExitResume, time.Since(start))
				continue
			}
			codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
			continue

		case ExitGlobalExit:
			site := cf.exitResumeCheckSite(ctx)
			before, err := exitCheck.checkBefore(ctx, site, regs, base, protoNameForCheck(proto))
			if err != nil {
				return nil, err
			}
			if err := tm.executeGlobalExit(ctx, regs, base, proto, cf); err != nil {
				return nil, fmt.Errorf("tier2: global-exit: %w", err)
			}
			tm.retireStaleTier2AfterFeedback(proto, cf)
			resyncRegs()
			if err := exitCheck.checkAfter(site, before, regs, base, protoNameForCheck(proto)); err != nil {
				return nil, err
			}
			globalID := int(ctx.GlobalExitID)
			resumeOff, ok := cf.resumeOffset(globalID, ctx.ResumeNumericPass != 0)
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for global %d", globalID)
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
				tm.perfStats.record(perfTier2ExitResume, time.Since(start))
				continue
			}
			codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
			continue

		case ExitTableExit:
			site := cf.exitResumeCheckSite(ctx)
			before, err := exitCheck.checkBefore(ctx, site, regs, base, protoNameForCheck(proto))
			if err != nil {
				return nil, err
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				err = tm.executeTableExit(ctx, regs, base, proto, cf)
				tm.perfStats.record(perfTier2TableExit, time.Since(start))
			} else {
				err = tm.executeTableExit(ctx, regs, base, proto, cf)
			}
			if err != nil {
				return nil, fmt.Errorf("tier2: table-exit: %w", err)
			}
			tm.retireStaleTier2AfterFeedback(proto, cf)
			resyncRegs()
			if err := exitCheck.checkAfter(site, before, regs, base, protoNameForCheck(proto)); err != nil {
				return nil, err
			}
			tableID := int(ctx.TableExitID)
			resumeOff, ok := cf.resumeOffset(tableID, ctx.ResumeNumericPass != 0)
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for table %d", tableID)
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
				tm.perfStats.record(perfTier2ExitResume, time.Since(start))
				continue
			}
			codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
			continue

		case ExitOpExit:
			site := cf.exitResumeCheckSite(ctx)
			before, err := exitCheck.checkBefore(ctx, site, regs, base, protoNameForCheck(proto))
			if err != nil {
				return nil, err
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				err = tm.executeOpExit(ctx, regs, base, proto)
				tm.perfStats.record(perfTier2OpExit, time.Since(start))
			} else {
				err = tm.executeOpExit(ctx, regs, base, proto)
			}
			if err != nil {
				return nil, fmt.Errorf("tier2: op-exit: %w", err)
			}
			tm.retireStaleTier2AfterFeedback(proto, cf)
			resyncRegs()
			if err := exitCheck.checkAfter(site, before, regs, base, protoNameForCheck(proto)); err != nil {
				return nil, err
			}
			opID := int(ctx.OpExitID)
			resumeOff, ok := cf.resumeOffset(opID, ctx.ResumeNumericPass != 0)
			if !ok {
				return nil, fmt.Errorf("tier2: no resume for op %d", opID)
			}
			if tm.perfStatsEnabled {
				start := time.Now()
				codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
				tm.perfStats.record(perfTier2ExitResume, time.Since(start))
				continue
			}
			codePtr = tier2ExitResumeCodePtr(cf, ctx, resumeOff)
			continue

		default:
			return nil, fmt.Errorf("tier2: unknown exit code %d", ctx.ExitCode)
		}
	}
}

func tier2ExitResumeCodePtr(cf *CompiledFunction, ctx *ExecContext, resumeOff int) uintptr {
	codePtr := uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
	ctx.ExitCode = 0
	ctx.ResumeNumericPass = 0
	return codePtr
}

func (tm *TieringManager) disableTier2AfterRuntimeDeopt(proto *vm.FuncProto, reason string) {
	if proto == nil {
		return
	}
	tm.markTier2Failed(proto, reason)
	tm.clearTier2Install(proto)
	tm.tier1.SetOSRCounter(proto, -1)
	tm.tier1.EvictCompiled(proto)
	tm.traceEvent("runtime_disable", "tier2", proto, map[string]any{
		"reason": reason,
	})
}

func (tm *TieringManager) guardDeoptRefreshAction(proto *vm.FuncProto, cf *CompiledFunction, ctx *ExecContext) (Tier2DeoptAction, bool) {
	if tm == nil || proto == nil || cf == nil || ctx == nil || cf.ExitSites == nil {
		return Tier2DeoptAction{}, false
	}
	meta, ok := cf.ExitSites[int(ctx.DeoptInstrID)]
	if !ok || meta.PC < 0 || (meta.Op != "GuardType" && meta.Op != "GuardCalleeProto" && meta.Op != "GuardConstString") {
		return Tier2DeoptAction{}, false
	}
	tm.suppressTier2GuardKind(proto, meta.PC, meta.Op)
	reason := "tier2: guard deopt; recompile without unstable guard"
	if meta.Op == "GuardCalleeProto" {
		reason = "tier2: callee guard deopt; recompile without unstable callsite guard"
	} else if meta.Op == "GuardConstString" {
		reason = "tier2: const-string guard deopt; recompile without unstable string-key guard"
	}
	return Tier2DeoptAction{
		Kind:           Tier2DeoptRefreshAndFallback,
		Reason:         reason,
		PreciseResume:  int(ctx.ExitResumePC) > 0,
		ResumePC:       int(ctx.ExitResumePC),
		CurrentProfile: BuildTier2SpecializationProfile(proto),
		GuardRelaxedPC: meta.PC,
		GuardRelaxedOp: meta.Op,
	}, true
}

func (tm *TieringManager) applyTier2DeoptAction(proto *vm.FuncProto, action Tier2DeoptAction) {
	if proto == nil {
		return
	}
	switch action.Kind {
	case Tier2DeoptRefreshAndFallback:
		tm.clearTier2Install(proto)
		tm.tier1.SetOSRCounter(proto, -1)
		tm.tier1.EvictCompiled(proto)
		tm.traceEvent("runtime_refresh", "tier2", proto, map[string]any{
			"reason":           action.Reason,
			"version_after":    fmt.Sprintf("%x", action.CurrentProfile.Version.Hash),
			"guards_after":     action.CurrentProfile.Version.GuardCount,
			"guard_relaxed_pc": action.GuardRelaxedPC,
			"guard_relaxed_op": action.GuardRelaxedOp,
		})
	default:
		tm.disableTier2AfterRuntimeDeopt(proto, action.Reason)
	}
}
