//go:build darwin && arm64

package methodjit

import (
	"math"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func observeTier2CallExitFeedback(proto *vm.FuncProto, cf *CompiledFunction, ctx *ExecContext, regs []runtime.Value, base int) {
	if proto == nil || proto.CallSiteFeedback == nil || cf == nil || ctx == nil {
		return
	}
	pc := tier2CallExitSourcePC(cf, ctx)
	if pc < 0 || pc >= len(proto.CallSiteFeedback) || pc >= len(proto.Code) {
		return
	}
	if vm.DecodeOp(proto.Code[pc]) != vm.OP_CALL {
		return
	}
	callSlot := base + int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	if callSlot < 0 || callSlot >= len(regs) || nArgs < 0 {
		return
	}
	argStart := callSlot + 1
	argEnd := argStart + nArgs
	rawC := vm.DecodeC(proto.Code[pc])
	if argStart >= 0 && argEnd >= argStart && argEnd <= len(regs) {
		proto.CallSiteFeedback[pc].ObserveCall(regs[callSlot], regs[argStart:argEnd], nArgs, rawC)
		return
	}
	proto.CallSiteFeedback[pc].ObserveCall(regs[callSlot], nil, nArgs, rawC)
}

func observeTier2CallExitResultFeedback(proto *vm.FuncProto, cf *CompiledFunction, ctx *ExecContext, result runtime.Value, hasResult bool) {
	if proto == nil || proto.CallSiteFeedback == nil || cf == nil || ctx == nil {
		return
	}
	if !hasResult && ctx.CallNRets <= 0 {
		return
	}
	pc := tier2CallExitSourcePC(cf, ctx)
	if pc < 0 || pc >= len(proto.CallSiteFeedback) || pc >= len(proto.Code) {
		return
	}
	if vm.DecodeOp(proto.Code[pc]) != vm.OP_CALL {
		return
	}
	if !hasResult {
		result = runtime.NilValue()
	}
	if projected, ok := tier2CallExitProjectedResult(cf, ctx, result); ok {
		result = projected
	}
	proto.CallSiteFeedback[pc].ObserveResult(result)
}

func tier2CallExitProjectedResult(cf *CompiledFunction, ctx *ExecContext, result runtime.Value) (runtime.Value, bool) {
	if cf == nil || ctx == nil || cf.ExitSites == nil {
		return runtime.NilValue(), false
	}
	meta, ok := cf.ExitSites[int(ctx.CallID)]
	if !ok {
		return runtime.NilValue(), false
	}
	if meta.Op != "CallFloor" && meta.Op != "FieldCallFloor" && meta.Reason != "FieldCallFloor" {
		return runtime.NilValue(), false
	}
	switch {
	case result.IsInt():
		return result, true
	case result.IsFloat():
		return runtime.IntValue(int64(math.Floor(result.Float()))), true
	default:
		return result, true
	}
}

func tier2CallExitSourcePC(cf *CompiledFunction, ctx *ExecContext) int {
	if cf == nil || ctx == nil || cf.ExitSites == nil {
		return -1
	}
	if meta, ok := cf.ExitSites[int(ctx.CallID)]; ok {
		return meta.PC
	}
	return -1
}

func mergeTier2CallCacheFeedback(proto *vm.FuncProto, cf *CompiledFunction) {
	if proto == nil || proto.CallSiteFeedback == nil || cf == nil ||
		len(cf.CallCache) == 0 || len(cf.CallCachePCs) == 0 {
		return
	}
	for siteIdx, pc := range cf.CallCachePCs {
		if pc < 0 || pc >= len(proto.CallSiteFeedback) || pc >= len(proto.Code) {
			continue
		}
		inst := proto.Code[pc]
		if vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		base := siteIdx * tier2CallCacheStrideWords
		if base+tier2CallCacheStrideWords > len(cf.CallCache) {
			continue
		}
		nArgs := vm.DecodeB(inst) - 1
		if nArgs < 0 {
			continue
		}
		resultArity := vm.DecodeC(inst)
		fb := &proto.CallSiteFeedback[pc]
		if fb.Count == 0 {
			fb.NArgs = uint8(nArgs)
			fb.ResultArity = uint8(resultArity)
		} else if int(fb.NArgs) != nArgs || fb.ResultArity != uint8(resultArity) {
			fb.Flags |= vm.CallSiteArityPolymorphic
			continue
		}
		observed := 0
		for way := 0; way < tier2CallCacheWays; way++ {
			protoWord := cf.CallCache[base+way*tier2CallCacheWordsPerWay+baselineCallCacheProtoOff/8]
			if protoWord == 0 {
				continue
			}
			callee := (*vm.FuncProto)(unsafe.Pointer(uintptr(protoWord)))
			if callee == nil {
				continue
			}
			if observed == 0 && fb.CalleeVMProto == nil {
				fb.CalleeVMProto = callee
			} else if fb.CalleeVMProto != nil && fb.CalleeVMProto != callee {
				fb.Flags |= vm.CallSiteCalleePolymorphic
			}
			observeCallFeedbackVMProto(fb, callee)
			observed++
		}
		if observed == 0 {
			continue
		}
		fb.CalleeType.Observe(runtime.TypeFunction)
		if fb.Count < wholeCallKernelMinStableObservations {
			fb.Count = wholeCallKernelMinStableObservations
		} else {
			fb.Count += uint32(observed)
		}
	}
}

func mergeBaselineCallCacheFeedback(proto *vm.FuncProto, bf *BaselineFunc) {
	if proto == nil || proto.CallSiteFeedback == nil || bf == nil || len(bf.CallCache) == 0 {
		return
	}
	for pc, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		if pc < 0 || pc >= len(proto.CallSiteFeedback) {
			continue
		}
		base := pc * baselineCallCacheStride
		if base+baselineCallCacheStride > len(bf.CallCache) {
			continue
		}
		protoWord := bf.CallCache[base+baselineCallCacheProtoOff/8]
		boxed := bf.CallCache[base+baselineCallCacheBoxedOff/8]
		if protoWord == 0 || boxed == 0 {
			continue
		}
		nArgs := vm.DecodeB(inst) - 1
		if nArgs < 0 {
			continue
		}
		resultArity := vm.DecodeC(inst)
		fb := &proto.CallSiteFeedback[pc]
		if !mergeCallFeedbackArity(fb, nArgs, resultArity) {
			continue
		}
		callee := (*vm.FuncProto)(unsafe.Pointer(uintptr(protoWord)))
		if callee == nil {
			continue
		}
		fn := runtime.Value(boxed)
		closure := uintptr(fn.VMClosurePointer())
		mergeCallFeedbackVMIdentity(fb, callee, closure)
		fb.CalleeType.Observe(runtime.TypeFunction)
		if fb.Count < wholeCallKernelMinStableObservations {
			fb.Count = wholeCallKernelMinStableObservations
		} else {
			fb.Count++
		}
	}
}

func mergeCallFeedbackArity(fb *vm.CallSiteFeedback, nArgs, resultArity int) bool {
	if fb == nil || nArgs < 0 {
		return false
	}
	if fb.Count == 0 {
		fb.NArgs = uint8(nArgs)
		fb.ResultArity = uint8(resultArity)
		return true
	}
	if int(fb.NArgs) != nArgs || fb.ResultArity != uint8(resultArity) {
		fb.Flags |= vm.CallSiteArityPolymorphic
		return false
	}
	return true
}

func mergeCallFeedbackVMIdentity(fb *vm.CallSiteFeedback, callee *vm.FuncProto, closure uintptr) {
	if fb == nil || callee == nil {
		return
	}
	if fb.CalleeVMProto == nil {
		fb.CalleeVMProto = callee
	} else if fb.CalleeVMProto != callee {
		fb.Flags |= vm.CallSiteCalleePolymorphic
	}
	if closure != 0 {
		if fb.CalleeVMClosure == 0 {
			fb.CalleeVMClosure = closure
		} else if fb.CalleeVMClosure != closure {
			fb.Flags |= vm.CallSiteVMClosurePolymorphic
		}
	}
	observeCallFeedbackVMProto(fb, callee)
}

func observeCallFeedbackVMProto(fb *vm.CallSiteFeedback, proto *vm.FuncProto) {
	if fb == nil || proto == nil {
		return
	}
	for i := 0; i < int(fb.CalleeVMProtoCount); i++ {
		if fb.CalleeVMProtos[i] == proto {
			return
		}
	}
	if fb.CalleeVMProtoCount >= vm.MaxCallSiteFeedbackVMProtos {
		return
	}
	fb.CalleeVMProtos[fb.CalleeVMProtoCount] = proto
	fb.CalleeVMProtoCount++
}

func mergeTier2CallCacheEntryForTest(proto *vm.FuncProto, cf *CompiledFunction, siteIdx int, pc int, callees ...*vm.FuncProto) {
	if proto == nil || cf == nil || siteIdx < 0 {
		return
	}
	for len(cf.CallCachePCs) <= siteIdx {
		cf.CallCachePCs = append(cf.CallCachePCs, -1)
	}
	cf.CallCachePCs[siteIdx] = pc
	need := (siteIdx + 1) * tier2CallCacheStrideWords
	if len(cf.CallCache) < need {
		cf.CallCache = make([]uint64, need)
	}
	base := siteIdx * tier2CallCacheStrideWords
	for way, callee := range callees {
		if way >= tier2CallCacheWays || callee == nil {
			break
		}
		cf.CallCache[base+way*tier2CallCacheWordsPerWay+baselineCallCacheProtoOff/8] = uint64(uintptr(unsafe.Pointer(callee)))
	}
}
