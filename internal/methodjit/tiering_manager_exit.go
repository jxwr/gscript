//go:build darwin && arm64

// tiering_manager_exit.go implements exit handlers for the TieringManager's
// Tier 2 execute loop. These handlers are invoked when Tier 2 JIT code
// encounters operations it cannot handle natively (calls, globals, tables,
// generic ops).
//
// Slot indices in ExecContext are relative to the callee's frame (base=0 in
// JIT), so we add `base` for absolute positions.

package methodjit

import (
	"fmt"
	"math"
	"strings"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// executeCallExit handles a call-exit in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeCallExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM set for call-exit")
	}

	callSlot := int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	nRets := int(ctx.CallNRets)

	absSlot := base + callSlot
	if absSlot >= len(regs) {
		return fmt.Errorf("call slot %d (abs %d) out of range (regs len %d)", callSlot, absSlot, len(regs))
	}
	fnVal := regs[absSlot]

	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := absSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	results, err := tm.callValueForTier2Exit(fnVal, callArgs, proto)
	if err != nil {
		return err
	}

	// Re-read regs — CallValue may have grown the register file.
	currentRegs := tm.callVM.Regs()

	nr := nRets
	if nr <= 0 {
		nr = 1
	}
	for i := 0; i < nr; i++ {
		idx := absSlot + i
		if idx < len(currentRegs) {
			if i < len(results) {
				currentRegs[idx] = results[i]
			} else {
				currentRegs[idx] = runtime.NilValue()
			}
		}
	}

	return nil
}

func (tm *TieringManager) callValueForTier2Exit(fnVal runtime.Value, args []runtime.Value, callerProto *vm.FuncProto) ([]runtime.Value, error) {
	if !tm.shouldSuppressUnsafeSelfTier2Reentry(fnVal, callerProto) {
		return tm.callVM.CallValue(fnVal, args)
	}

	// DirectEntrySafe=false means native callers may not safely recurse into
	// this Tier 2 body. A self call-exit that goes through VM.CallValue would
	// otherwise re-enter the same Tier 2 function through the normal VM JIT
	// dispatch path, recreating the native stack nesting the direct-entry gate
	// was meant to avoid.
	oldDisabled := callerProto.JITDisabled
	callerProto.JITDisabled = true
	defer func() {
		callerProto.JITDisabled = oldDisabled
	}()
	return tm.callVM.CallValue(fnVal, args)
}

func (tm *TieringManager) shouldSuppressUnsafeSelfTier2Reentry(fnVal runtime.Value, callerProto *vm.FuncProto) bool {
	if tm == nil || callerProto == nil {
		return false
	}
	cl, ok := vmClosureFromValue(fnVal)
	if !ok || cl == nil || cl.Proto != callerProto {
		return false
	}
	cf := tm.tier2Compiled[callerProto]
	return cf != nil && !cf.DirectEntrySafe
}

func (tm *TieringManager) executeNativeCallExit(ctx *ExecContext, callerCF *CompiledFunction, regs []runtime.Value, callerBase int, callerProto *vm.FuncProto) ([]runtime.Value, error) {
	if tm.callVM == nil {
		return regs, fmt.Errorf("no callVM set for native-call-exit")
	}
	calleeProto, calleeCF, calleeBase, err := tm.nativeExitCallee(ctx, regs, callerBase)
	if err != nil {
		return regs, err
	}

	if !calleeCF.DirectEntrySafe {
		setFuncProtoTier2DirectEntries(calleeProto, 0, 0)
	}

	result, err := tm.resumeNativeTier2CalleeExit(ctx, calleeCF, regs, calleeBase, calleeProto)
	if err != nil {
		return regs, err
	}
	tm.setTier2ResumeContext(ctx, callerCF, callerProto, callerBase)
	regs = tm.callVM.Regs()
	absSlot := callerBase + int(ctx.CallSlot)
	nRets := int(ctx.CallNRets)
	if nRets <= 0 {
		nRets = 1
	}
	for i := 0; i < nRets; i++ {
		idx := absSlot + i
		if idx >= 0 && idx < len(regs) {
			if i == 0 {
				regs[idx] = result
			} else {
				regs[idx] = runtime.NilValue()
			}
		}
	}
	return regs, nil
}

func (tm *TieringManager) setTier2ResumeContext(ctx *ExecContext, cf *CompiledFunction, proto *vm.FuncProto, base int) {
	if ctx == nil || tm.callVM == nil {
		return
	}
	regs := tm.callVM.Regs()
	if base >= 0 && base < len(regs) {
		ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
		ctx.RegsBase = uintptr(unsafe.Pointer(&regs[0]))
		ctx.RegsEnd = ctx.RegsBase + uintptr(len(regs)*jit.ValueSize)
	}
	if proto != nil && len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	} else {
		ctx.Constants = 0
	}
	tm.setTier2FieldCacheContext(ctx, proto)
	if cf != nil && len(cf.GlobalCache) > 0 {
		ctx.Tier2GlobalCache = uintptr(unsafe.Pointer(&cf.GlobalCache[0]))
		ctx.Tier2GlobalCacheGen = uintptr(unsafe.Pointer(&cf.GlobalCacheGen))
	} else {
		ctx.Tier2GlobalCache = 0
		ctx.Tier2GlobalCacheGen = 0
	}
	ctx.Tier2GlobalGenPtr = uintptr(unsafe.Pointer(&tm.tier1.globalCacheGen))
	if proto != nil && cf != nil {
		if arrayPtr, verPtr, ver, ok := tm.prepareTier2GlobalIndexes(proto, cf); ok {
			ctx.Tier2GlobalIndex = proto.Tier2GlobalIndexPtr
			ctx.Tier2GlobalArray = arrayPtr
			ctx.Tier2GlobalVerPtr = uintptr(unsafe.Pointer(verPtr))
			ctx.Tier2GlobalVer = uint64(ver)
		} else {
			ctx.Tier2GlobalIndex = 0
			ctx.Tier2GlobalArray = 0
			ctx.Tier2GlobalVerPtr = 0
			ctx.Tier2GlobalVer = 0
		}
	}
	if cf != nil && len(cf.CallCache) > 0 {
		ctx.Tier2CallCache = uintptr(unsafe.Pointer(&cf.CallCache[0]))
	} else {
		ctx.Tier2CallCache = 0
	}
	if cl := tm.callVM.CurrentClosure(); cl != nil {
		ctx.BaselineClosurePtr = uintptr(unsafe.Pointer(cl))
	}
}

func (tm *TieringManager) nativeExitCallee(ctx *ExecContext, regs []runtime.Value, callerBase int) (*vm.FuncProto, *CompiledFunction, int, error) {
	calleeBase := callerBase + int(ctx.NativeCalleeBaseOff)/jit.ValueSize
	callSlot := callerBase + int(ctx.CallSlot)
	if callSlot < 0 || callSlot >= len(regs) {
		return nil, nil, 0, fmt.Errorf("native-call-exit: call slot %d out of range", callSlot)
	}
	fnVal := regs[callSlot]
	cl, ok := vmClosureFromValue(fnVal)
	if !ok || cl == nil || cl.Proto == nil {
		return nil, nil, 0, fmt.Errorf("native-call-exit: call slot %d is not a VM closure", callSlot)
	}
	if ctx.NativeCalleeClosurePtr != 0 && uintptr(unsafe.Pointer(cl)) != ctx.NativeCalleeClosurePtr {
		return nil, nil, 0, fmt.Errorf("native-call-exit: callee closure changed")
	}
	calleeCF := tm.tier2Compiled[cl.Proto]
	if calleeCF == nil {
		return nil, nil, 0, fmt.Errorf("native-call-exit: callee %q is not compiled at Tier 2", cl.Proto.Name)
	}
	return cl.Proto, calleeCF, calleeBase, nil
}

func (tm *TieringManager) resumeNativeTier2CalleeExit(ctx *ExecContext, cf *CompiledFunction, regs []runtime.Value, base int, proto *vm.FuncProto) (runtime.Value, error) {
	codePtr := uintptr(0)
	switch ctx.NativeCalleeExitCode {
	case ExitTableExit:
		if err := tm.executeTableExit(ctx, regs, base, proto, cf); err != nil {
			return runtime.NilValue(), fmt.Errorf("callee table-exit: %w", err)
		}
		resumeOff, ok := cf.resumeOffset(int(ctx.TableExitID), ctx.NativeCalleeResumePass != 0)
		if !ok {
			return runtime.NilValue(), fmt.Errorf("callee table-exit: no resume for %d", ctx.TableExitID)
		}
		codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
	case ExitGlobalExit:
		if err := tm.executeGlobalExit(ctx, regs, base, proto, cf); err != nil {
			return runtime.NilValue(), fmt.Errorf("callee global-exit: %w", err)
		}
		resumeOff, ok := cf.resumeOffset(int(ctx.GlobalExitID), ctx.NativeCalleeResumePass != 0)
		if !ok {
			return runtime.NilValue(), fmt.Errorf("callee global-exit: no resume for %d", ctx.GlobalExitID)
		}
		codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
	case ExitOpExit:
		if err := tm.executeOpExit(ctx, regs, base, proto); err != nil {
			return runtime.NilValue(), fmt.Errorf("callee op-exit: %w", err)
		}
		resumeOff, ok := cf.resumeOffset(int(ctx.OpExitID), ctx.NativeCalleeResumePass != 0)
		if !ok {
			return runtime.NilValue(), fmt.Errorf("callee op-exit: no resume for %d", ctx.OpExitID)
		}
		codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
	case ExitCallExit:
		if err := tm.executeCallExit(ctx, regs, base, proto); err != nil {
			return runtime.NilValue(), fmt.Errorf("callee call-exit: %w", err)
		}
		resumeOff, ok := cf.resumeOffset(int(ctx.CallID), ctx.NativeCalleeResumePass != 0)
		if !ok {
			return runtime.NilValue(), fmt.Errorf("callee call-exit: no resume for %d", ctx.CallID)
		}
		codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
	case ExitDeopt:
		tm.disableTier2AfterRuntimeDeopt(proto, "tier2 native callee deopt")
		if ctx.NativeCalleeResumePC > 0 {
			if !tm.callVM.PushFrame(ptrToVMClosure(ctx.NativeCalleeClosurePtr), base) {
				return runtime.NilValue(), fmt.Errorf("native-call-exit: stack overflow")
			}
			results, err := tm.callVM.ResumeFromPC(int(ctx.NativeCalleeResumePC))
			tm.callVM.PopFrame()
			if err != nil {
				return runtime.NilValue(), err
			}
			if len(results) > 0 {
				return results[0], nil
			}
			return runtime.NilValue(), nil
		}
		return runtime.NilValue(), fmt.Errorf("callee deopt")
	default:
		return runtime.NilValue(), fmt.Errorf("unknown callee exit code %d", ctx.NativeCalleeExitCode)
	}

	currentRegs := tm.callVM.Regs()
	tm.setTier2ResumeContext(ctx, cf, proto, base)
	ctx.BaselineClosurePtr = ctx.NativeCalleeClosurePtr
	ctx.CallMode = 1
	ctx.ExitCode = 0
	ctx.ResumeNumericPass = 0

	for {
		jit.CallJIT(codePtr, uintptr(unsafe.Pointer(ctx)))
		switch ctx.ExitCode {
		case ExitNormal:
			return runtime.Value(ctx.BaselineReturnValue), nil
		case ExitTableExit:
			if err := tm.executeTableExit(ctx, currentRegs, base, proto, cf); err != nil {
				return runtime.NilValue(), fmt.Errorf("callee table-exit: %w", err)
			}
			currentRegs = tm.callVM.Regs()
			resumeOff, ok := cf.resumeOffset(int(ctx.TableExitID), ctx.ResumeNumericPass != 0)
			if !ok {
				return runtime.NilValue(), fmt.Errorf("callee table-exit: no resume for %d", ctx.TableExitID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			ctx.ResumeNumericPass = 0
		case ExitGlobalExit:
			if err := tm.executeGlobalExit(ctx, currentRegs, base, proto, cf); err != nil {
				return runtime.NilValue(), fmt.Errorf("callee global-exit: %w", err)
			}
			currentRegs = tm.callVM.Regs()
			resumeOff, ok := cf.resumeOffset(int(ctx.GlobalExitID), ctx.ResumeNumericPass != 0)
			if !ok {
				return runtime.NilValue(), fmt.Errorf("callee global-exit: no resume for %d", ctx.GlobalExitID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			ctx.ResumeNumericPass = 0
		case ExitOpExit:
			if err := tm.executeOpExit(ctx, currentRegs, base, proto); err != nil {
				return runtime.NilValue(), fmt.Errorf("callee op-exit: %w", err)
			}
			currentRegs = tm.callVM.Regs()
			resumeOff, ok := cf.resumeOffset(int(ctx.OpExitID), ctx.ResumeNumericPass != 0)
			if !ok {
				return runtime.NilValue(), fmt.Errorf("callee op-exit: no resume for %d", ctx.OpExitID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			ctx.ResumeNumericPass = 0
		case ExitCallExit:
			if err := tm.executeCallExit(ctx, currentRegs, base, proto); err != nil {
				return runtime.NilValue(), fmt.Errorf("callee call-exit: %w", err)
			}
			currentRegs = tm.callVM.Regs()
			resumeOff, ok := cf.resumeOffset(int(ctx.CallID), ctx.ResumeNumericPass != 0)
			if !ok {
				return runtime.NilValue(), fmt.Errorf("callee call-exit: no resume for %d", ctx.CallID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			ctx.ResumeNumericPass = 0
		case ExitDeopt:
			tm.traceEvent("native_callee_deopt", "tier2", proto, map[string]any{
				"deopt_instr_id": ctx.DeoptInstrID,
				"resume_pc":      ctx.ExitResumePC,
				"call_id":        ctx.CallID,
				"table_exit_id":  ctx.TableExitID,
				"op_exit_id":     ctx.OpExitID,
			})
			tm.disableTier2AfterRuntimeDeopt(proto, "tier2 native callee deopt")
			return runtime.NilValue(), fmt.Errorf("callee deopt")
		default:
			return runtime.NilValue(), fmt.Errorf("unknown callee exit code %d", ctx.ExitCode)
		}
		ctx.Regs = uintptr(unsafe.Pointer(&currentRegs[base]))
		ctx.RegsBase = uintptr(unsafe.Pointer(&currentRegs[0]))
		ctx.RegsEnd = ctx.RegsBase + uintptr(len(currentRegs)*jit.ValueSize)
		tm.setTier2FieldCacheContext(ctx, proto)
	}
}

// executeGlobalExit handles a global-exit in the TieringManager's Tier 2 path.
// After resolving the global value, populates the per-instruction value cache
// in CompiledFunction.GlobalCache so subsequent accesses hit the fast path.
func (tm *TieringManager) executeGlobalExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto, cf *CompiledFunction) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM set for global-exit")
	}

	globalSlot := int(ctx.GlobalSlot)
	constIdx := int(ctx.GlobalConst)

	if constIdx >= len(proto.Constants) {
		return fmt.Errorf("global constant index %d out of range (len %d)", constIdx, len(proto.Constants))
	}
	globalName := proto.Constants[constIdx].Str()
	val := tm.callVM.GetGlobal(globalName)

	absSlot := base + globalSlot
	if absSlot < len(regs) {
		regs[absSlot] = val
	}

	// Populate the per-instruction global value cache.
	cacheIdx := int(ctx.GlobalCacheIdx)
	if cacheIdx >= 0 && cf != nil && cf.GlobalCache != nil && cacheIdx < len(cf.GlobalCache) {
		valBits := uint64(val)
		if valBits != 0 { // don't cache zero (used as "empty" sentinel)
			// If the generation has changed since we last cached, clear all
			// entries before repopulating. Without this, updating GlobalCacheGen
			// would make other entries' stale values appear valid.
			if cf.GlobalCacheGen != tm.tier1.globalCacheGen {
				for i := range cf.GlobalCache {
					cf.GlobalCache[i] = 0
				}
			}
			cf.GlobalCache[cacheIdx] = valBits
			cf.GlobalCacheGen = tm.tier1.globalCacheGen
		}
	}

	return nil
}

// executeTableExit handles table operations in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeTableExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto, cf *CompiledFunction) error {
	switch ctx.TableOp {
	case TableOpNewTable:
		arrayHint := int(ctx.TableAux)
		hashHint, arrayKind := unpackNewTableAux2(ctx.TableAux2)
		tbl := cf.allocateNewTableForExit(int(ctx.TableExitID), arrayHint, hashHint, arrayKind)
		absSlot := base + int(ctx.TableSlot)
		if absSlot < len(regs) {
			regs[absSlot] = runtime.FreshTableValue(tbl)
		}

	case TableOpGetTable:
		absTable := base + int(ctx.TableSlot)
		absKey := base + int(ctx.TableKeySlot)
		absResult := base + int(ctx.TableAux)
		if absTable < len(regs) && absKey < len(regs) {
			tblVal := regs[absTable]
			keyVal := regs[absKey]
			if tblVal.IsTable() {
				result := tblVal.Table().RawGet(keyVal)
				if absResult < len(regs) {
					regs[absResult] = result
				}
			} else if absResult < len(regs) {
				regs[absResult] = runtime.NilValue()
			}
		}

	case TableOpSetTable:
		absTable := base + int(ctx.TableSlot)
		absKey := base + int(ctx.TableKeySlot)
		absVal := base + int(ctx.TableValSlot)
		if absTable < len(regs) && absKey < len(regs) && absVal < len(regs) {
			tblVal := regs[absTable]
			keyVal := regs[absKey]
			valVal := regs[absVal]
			if tblVal.IsTable() {
				tblVal.Table().RawSet(keyVal, valVal)
			}
		}

	case TableOpGetField:
		absTable := base + int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		absResult := base + int(ctx.TableAux2)
		if absTable < len(regs) && constIdx < len(proto.Constants) {
			tblVal := regs[absTable]
			fieldName := proto.Constants[constIdx].Str()
			if tblVal.IsTable() {
				var result runtime.Value
				pc := int(ctx.TableKeySlot)
				if pc >= 0 && pc < len(proto.Code) && vm.DecodeOp(proto.Code[pc]) == vm.OP_GETFIELD {
					ensureFieldCache(proto)
					result = tblVal.Table().RawGetStringCached(fieldName, &proto.FieldCache[pc])
				} else {
					result = tblVal.Table().RawGetString(fieldName)
				}
				if absResult < len(regs) {
					regs[absResult] = result
				}
			} else if absResult < len(regs) {
				regs[absResult] = runtime.NilValue()
			}
		}

	case TableOpSetField:
		absTable := base + int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		absVal := base + int(ctx.TableValSlot)
		if absTable < len(regs) && constIdx < len(proto.Constants) && absVal < len(regs) {
			tblVal := regs[absTable]
			fieldName := proto.Constants[constIdx].Str()
			valVal := regs[absVal]
			if tblVal.IsTable() {
				pc := int(ctx.TableKeySlot)
				if pc >= 0 && pc < len(proto.Code) && vm.DecodeOp(proto.Code[pc]) == vm.OP_SETFIELD {
					ensureFieldCache(proto)
					tblVal.Table().RawSetStringCached(fieldName, valVal, &proto.FieldCache[pc])
				} else {
					tblVal.Table().RawSetString(fieldName, valVal)
				}
			}
		}

	default:
		return fmt.Errorf("unknown table op %d", ctx.TableOp)
	}
	return nil
}

// executeOpExit handles generic op-exits in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeOpExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	op := Op(ctx.OpExitOp)
	absSlot := base + int(ctx.OpExitSlot)
	absArg1 := base + int(ctx.OpExitArg1)
	absArg2 := base + int(ctx.OpExitArg2)
	aux := int(ctx.OpExitAux)

	switch op {
	case OpConstString:
		if aux >= 0 && aux < len(proto.Constants) {
			if absSlot < len(regs) {
				regs[absSlot] = proto.Constants[aux]
			}
		}

	case OpConcat:
		tempBase := absArg1
		nArgs := int(ctx.OpExitArg2)
		if absSlot < len(regs) && tempBase >= 0 && nArgs >= 0 && tempBase+nArgs <= len(regs) {
			var sb strings.Builder
			for i := 0; i < nArgs; i++ {
				sb.WriteString(regs[tempBase+i].String())
			}
			regs[absSlot] = runtime.StringValue(sb.String())
		}

	case OpLen:
		if absArg1 < len(regs) && absSlot < len(regs) {
			v := regs[absArg1]
			if v.IsTable() {
				regs[absSlot] = runtime.IntValue(int64(v.Table().Len()))
			} else if v.IsString() {
				regs[absSlot] = runtime.IntValue(int64(len(v.Str())))
			} else {
				regs[absSlot] = runtime.IntValue(0)
			}
		}

	case OpEq:
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			regs[absSlot] = runtime.BoolValue(regs[absArg1].Equal(regs[absArg2]))
		}

	case OpLt:
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			lt, ok := regs[absArg1].LessThan(regs[absArg2])
			if !ok {
				return fmt.Errorf("attempt to compare %s with %s", regs[absArg1].TypeName(), regs[absArg2].TypeName())
			}
			regs[absSlot] = runtime.BoolValue(lt)
		}

	case OpLe:
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			lt, ok := regs[absArg2].LessThan(regs[absArg1])
			if !ok {
				return fmt.Errorf("attempt to compare %s with %s", regs[absArg1].TypeName(), regs[absArg2].TypeName())
			}
			regs[absSlot] = runtime.BoolValue(!lt)
		}

	case OpPow:
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			var base2, exp float64
			v1 := regs[absArg1]
			v2 := regs[absArg2]
			if v1.IsInt() {
				base2 = float64(v1.Int())
			} else {
				base2 = v1.Float()
			}
			if v2.IsInt() {
				exp = float64(v2.Int())
			} else {
				exp = v2.Float()
			}
			regs[absSlot] = runtime.FloatValue(math.Pow(base2, exp))
		}

	case OpSetGlobal:
		if tm.callVM == nil {
			return fmt.Errorf("no callVM set for SetGlobal op-exit")
		}
		if aux >= 0 && aux < len(proto.Constants) {
			name := proto.Constants[aux].Str()
			if absArg1 < len(regs) {
				tm.callVM.SetGlobal(name, regs[absArg1])
			}
			tm.invalidateGlobalValueCaches(name)
		}

	case OpAppend:
		if absArg1 < len(regs) && absArg2 < len(regs) {
			tblVal := regs[absArg1]
			val := regs[absArg2]
			if tblVal.IsTable() {
				tblVal.Table().Append(val)
			}
		}

	case OpSelf:
		if absArg1 < len(regs) && absSlot < len(regs) && absSlot+1 < len(regs) {
			tblVal := regs[absArg1]
			regs[absSlot+1] = tblVal
			if tblVal.IsTable() && aux >= 0 && aux < len(proto.Constants) {
				methodName := proto.Constants[aux].Str()
				regs[absSlot] = tblVal.Table().RawGetString(methodName)
			} else {
				regs[absSlot] = runtime.NilValue()
			}
		}

	case OpClose:
		// No-op.

	case OpSetList:
		// SetList: slot=nValues, arg1=table slot, arg2=tempBase slot, aux=arrayStart
		nValues := int(ctx.OpExitSlot)
		absTable := base + int(ctx.OpExitArg1)
		absTempBase := base + int(ctx.OpExitArg2)
		arrayStart := aux // 1-based array start index
		if absTable < len(regs) && regs[absTable].IsTable() {
			tbl := regs[absTable].Table()
			for i := 0; i < nValues; i++ {
				absVal := absTempBase + i
				if absVal < len(regs) {
					tbl.RawSetInt(int64(arrayStart+i), regs[absVal])
				}
			}
		}

	case OpClosure:
		return tm.executeClosureOpExit(ctx, regs, base, proto)

	case OpGetUpval:
		return tm.executeGetUpvalOpExit(ctx, regs, base)

	case OpSetUpval:
		return tm.executeSetUpvalOpExit(ctx, regs, base)

	case OpVararg:
		return tm.executeVarargOpExit(ctx, regs, base)

	case OpTestSet:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpForPrep, OpForLoop:
		return fmt.Errorf("op-exit unexpected: %s (should be decomposed by graph builder)", op)

	case OpTForCall, OpTForLoop:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpGuardType, OpGuardNonNil, OpGuardTruthy:
		return fmt.Errorf("op-exit guard failure: %s", op)

	case OpGo, OpMakeChan, OpSend, OpRecv:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	default:
		return fmt.Errorf("unsupported op-exit: %s (%d)", op, int(op))
	}

	return nil
}

// executeClosureOpExit handles OpClosure via op-exit. Creates a new closure
// with the child proto and captures upvalues from the parent closure and the
// register file, mirroring Tier 1's handleClosure in tier1_handlers_misc.go.
//
// Op-exit descriptor:
//
//	OpExitSlot = result slot (where to store the new closure)
//	OpExitAux  = child proto index (bx from OP_CLOSURE)
func (tm *TieringManager) executeClosureOpExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	absSlot := base + int(ctx.OpExitSlot)
	bx := int(ctx.OpExitAux)

	if bx < 0 || bx >= len(proto.Protos) {
		return fmt.Errorf("closure proto index %d out of range (len %d)", bx, len(proto.Protos))
	}
	subProto := proto.Protos[bx]

	cl := vm.NewClosure(subProto)

	// Get the parent closure for non-InStack upvalues.
	var parentCl *vm.Closure
	if tm.callVM != nil {
		parentCl = tm.callVM.CurrentClosure()
	}

	for i, desc := range subProto.Upvalues {
		if desc.InStack {
			// Upvalue refers to a local in the current frame's register file.
			absIdx := base + desc.Index
			if absIdx < len(regs) {
				uv := vm.NewOpenUpvalue(&regs[absIdx], absIdx)
				if tm.callVM != nil {
					uv = tm.callVM.FindOrCreateUpvalue(absIdx)
				}
				cl.Upvalues[i] = uv
			}
		} else {
			// Upvalue refers to a parent closure's upvalue.
			if parentCl != nil && desc.Index < len(parentCl.Upvalues) && parentCl.Upvalues[desc.Index] != nil {
				cl.Upvalues[i] = parentCl.Upvalues[desc.Index]
			} else {
				cl.Upvalues[i] = vm.NewOpenUpvalue(new(runtime.Value), 0)
			}
		}
	}

	if absSlot < len(regs) {
		regs[absSlot] = runtime.VMClosureFastValue(unsafe.Pointer(cl))
	}
	return nil
}

func (tm *TieringManager) invalidateGlobalValueCaches(name string) {
	if name == "" {
		return
	}
	tm.tier1.invalidateGlobalValueCaches(name)
	for _, cf := range tm.tier2Compiled {
		if cf == nil || cf.Proto == nil || len(cf.GlobalCache) == 0 {
			continue
		}
		for cacheIdx, constIdx := range cf.GlobalCacheConsts {
			if cacheIdx >= len(cf.GlobalCache) || constIdx < 0 || constIdx >= len(cf.Proto.Constants) {
				continue
			}
			kv := cf.Proto.Constants[constIdx]
			if kv.IsString() && kv.Str() == name {
				cf.GlobalCache[cacheIdx] = 0
			}
		}
	}
}

// executeGetUpvalOpExit handles OpGetUpval via op-exit. Reads a captured
// upvalue from the current closure.
//
// Op-exit descriptor:
//
//	OpExitSlot = result slot
//	OpExitAux  = upvalue index
func (tm *TieringManager) executeGetUpvalOpExit(ctx *ExecContext, regs []runtime.Value, base int) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM for GetUpval op-exit")
	}
	cl := tm.callVM.CurrentClosure()
	if cl == nil {
		return fmt.Errorf("GetUpval: no current closure")
	}

	absSlot := base + int(ctx.OpExitSlot)
	uvIdx := int(ctx.OpExitAux)

	if uvIdx < 0 || uvIdx >= len(cl.Upvalues) || cl.Upvalues[uvIdx] == nil {
		return fmt.Errorf("GetUpval: upvalue %d out of range (len %d)", uvIdx, len(cl.Upvalues))
	}

	if absSlot < len(regs) {
		regs[absSlot] = cl.Upvalues[uvIdx].Get()
	}
	return nil
}

// executeSetUpvalOpExit handles OpSetUpval via op-exit. Writes a value to a
// captured upvalue in the current closure.
//
// Op-exit descriptor:
//
//	OpExitArg1 = source slot (the value to set)
//	OpExitAux  = upvalue index
func (tm *TieringManager) executeSetUpvalOpExit(ctx *ExecContext, regs []runtime.Value, base int) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM for SetUpval op-exit")
	}
	cl := tm.callVM.CurrentClosure()
	if cl == nil {
		return fmt.Errorf("SetUpval: no current closure")
	}

	absArg1 := base + int(ctx.OpExitArg1)
	uvIdx := int(ctx.OpExitAux)

	if uvIdx < 0 || uvIdx >= len(cl.Upvalues) || cl.Upvalues[uvIdx] == nil {
		return fmt.Errorf("SetUpval: upvalue %d out of range (len %d)", uvIdx, len(cl.Upvalues))
	}

	if absArg1 < len(regs) {
		cl.Upvalues[uvIdx].Set(regs[absArg1])
	}
	return nil
}

// executeVarargOpExit handles OpVararg via op-exit. Copies variable arguments
// from the VM frame into the register file.
//
// Op-exit descriptor:
//
//	OpExitAux  = dest register (a from OP_VARARG)
//	OpExitSlot = result slot (used for storing first vararg result to SSA home)
//
// The actual varargs come from the VM frame. Aux2 encoding: Aux = a (dest base),
// the count is derived from the graph builder's Aux2 (stored in OpExitArg1 as
// a secondary channel since op-exit only has Aux for one aux field).
func (tm *TieringManager) executeVarargOpExit(ctx *ExecContext, regs []runtime.Value, base int) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM for Vararg op-exit")
	}

	destReg := int(ctx.OpExitAux)     // destination register (a)
	resultSlot := int(ctx.OpExitSlot) // SSA result slot
	bCount := int(ctx.OpExitArg1)     // B field (0 = all, >=2 means B-1 results)

	va := tm.callVM.CurrentVarargs()

	if bCount == 0 {
		// B=0: copy all varargs.
		for i, v := range va {
			absIdx := base + destReg + i
			if absIdx < len(regs) {
				regs[absIdx] = v
			}
		}
	} else {
		// B>=2: copy exactly B-1 varargs.
		n := bCount - 1
		for i := 0; i < n; i++ {
			absIdx := base + destReg + i
			if absIdx < len(regs) {
				if i < len(va) {
					regs[absIdx] = va[i]
				} else {
					regs[absIdx] = runtime.NilValue()
				}
			}
		}
	}

	// Also write the first vararg to the SSA result slot so the JIT can
	// pick it up after resuming.
	absResult := base + resultSlot
	if absResult < len(regs) {
		if len(va) > 0 {
			regs[absResult] = va[0]
		} else {
			regs[absResult] = runtime.NilValue()
		}
	}

	return nil
}
