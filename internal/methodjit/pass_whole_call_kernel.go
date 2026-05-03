//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

const wholeCallKernelMinStableObservations = 2

func WholeCallKernelExitPass(globals map[string]*vm.FuncProto) PassFunc {
	return func(fn *Function) (*Function, error) {
		return AnnotateWholeCallKernelExits(fn, globals), nil
	}
}

func AnnotateWholeCallKernelExits(fn *Function, globals map[string]*vm.FuncProto) *Function {
	if fn == nil {
		return fn
	}
	kernels := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpCall || callResultCountFromAux2(instr.Aux2) != 0 {
				continue
			}
			nArgs := len(instr.Args) - 1
			if !vmWholeCallKernelArity(nArgs) {
				continue
			}
			if !stableNoResultWholeCallCandidate(fn, instr, globals, nArgs) {
				continue
			}
			kernels[instr.ID] = true
		}
	}
	if len(kernels) == 0 {
		fn.WholeCallNoResultKernels = nil
		fn.WholeCallNoResultBatches = nil
		return fn
	}
	fn.WholeCallNoResultKernels = kernels
	fn.WholeCallNoResultBatches = buildWholeCallNoResultBatches(fn, globals, kernels)
	return fn
}

func vmWholeCallKernelArity(n int) bool {
	return n == 1 || n == 2 || n == 3
}

func stableNoResultWholeCallCandidate(fn *Function, instr *Instr, globals map[string]*vm.FuncProto, nArgs int) bool {
	if fn == nil || instr == nil {
		return false
	}
	if proto, ok := stableFeedbackCalleeProto(fn, instr, nArgs); ok {
		return proto != nil
	}
	_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
	return protoHasNoResultWholeCallKernel(callee)
}

func stableFeedbackCalleeProto(fn *Function, instr *Instr, nArgs int) (*vm.FuncProto, bool) {
	if fn == nil || fn.Proto == nil || instr == nil || !instr.HasSource ||
		instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
		return nil, false
	}
	fb := fn.Proto.CallSiteFeedback[instr.SourcePC]
	if fb.Count < wholeCallKernelMinStableObservations || fb.Flags&vm.CallSiteArityPolymorphic != 0 ||
		int(fb.NArgs) != nArgs || fb.ResultArity != 1 {
		return nil, false
	}
	return fb.StableCalleeVMProto()
}

func protoHasNoResultWholeCallKernel(proto *vm.FuncProto) bool {
	for _, info := range vm.RecognizedWholeCallKernels(proto) {
		if info.Route == vm.KernelRouteWholeCallNoResult && info.Results == 0 {
			return true
		}
	}
	return false
}

func buildWholeCallNoResultBatches(fn *Function, globals map[string]*vm.FuncProto, kernels map[int]bool) map[int]WholeCallNoResultBatchFact {
	if fn == nil || fn.Proto == nil || len(kernels) == 0 {
		return nil
	}
	byPC := make(map[int]*Instr, len(kernels))
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == OpCall && instr.HasSource && kernels[instr.ID] {
				byPC[instr.SourcePC] = instr
			}
		}
	}
	out := make(map[int]WholeCallNoResultBatchFact)
	code := fn.Proto.Code
	for pc, inst := range code {
		if vm.DecodeOp(inst) != vm.OP_FORLOOP {
			continue
		}
		loopStart := pc + 1 + vm.DecodesBx(inst)
		if loopStart < 0 || loopStart >= pc {
			continue
		}
		loopBase := vm.DecodeA(inst)
		var calls []WholeCallNoResultBatchCall
		var last *Instr
		ok := true
		for callPC := loopStart; callPC < pc; callPC++ {
			callInst := code[callPC]
			if vm.DecodeOp(callInst) != vm.OP_CALL {
				continue
			}
			instr := byPC[callPC]
			if instr == nil {
				ok = false
				break
			}
			call, callOK := wholeCallNoResultGlobalCallRecipe(fn.Proto, code, loopStart, callPC, globals)
			if !callOK {
				ok = false
				break
			}
			calls = append(calls, call)
			last = instr
		}
		if !ok || last == nil || len(calls) == 0 {
			continue
		}
		out[last.ID] = WholeCallNoResultBatchFact{
			LoopBase: loopBase,
			ExitPC:   pc + 1,
			Calls:    calls,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func wholeCallNoResultGlobalCallRecipe(proto *vm.FuncProto, code []uint32, loopStart, callPC int, globals map[string]*vm.FuncProto) (WholeCallNoResultBatchCall, bool) {
	inst := code[callPC]
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	if b <= 0 || b > 4 {
		return WholeCallNoResultBatchCall{}, false
	}
	call := WholeCallNoResultBatchCall{FuncConst: -1, ArgConsts: make([]int, 0, b-1)}
	for slot := a; slot < a+b; slot++ {
		constIdx, ok := lastGlobalWriterBeforeCall(code, loopStart, callPC, slot)
		if !ok {
			return WholeCallNoResultBatchCall{}, false
		}
		if slot == a {
			name := protoConstString(proto, constIdx)
			if name == "" || !protoHasNoResultWholeCallKernel(globals[name]) {
				return WholeCallNoResultBatchCall{}, false
			}
			call.FuncConst = constIdx
			continue
		}
		call.ArgConsts = append(call.ArgConsts, constIdx)
	}
	return call, call.FuncConst >= 0
}

func lastGlobalWriterBeforeCall(code []uint32, startPC, callPC, slot int) (int, bool) {
	for pc := callPC - 1; pc >= startPC; pc-- {
		inst := code[pc]
		a := vm.DecodeA(inst)
		if a != slot {
			continue
		}
		if vm.DecodeOp(inst) == vm.OP_GETGLOBAL {
			return vm.DecodeBx(inst), true
		}
		return -1, false
	}
	return -1, false
}
