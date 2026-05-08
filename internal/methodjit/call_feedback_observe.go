//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func observeTier2CallExitFeedback(proto *vm.FuncProto, cf *CompiledFunction, ctx *ExecContext, regs []runtime.Value, base int) {
	if proto == nil || proto.CallSiteFeedback == nil || cf == nil || ctx == nil {
		return
	}
	pc := -1
	if cf.ExitSites != nil {
		if meta, ok := cf.ExitSites[int(ctx.CallID)]; ok {
			pc = meta.PC
		}
	}
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
