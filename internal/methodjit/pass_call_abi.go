//go:build darwin && arm64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

// AnnotateCallABIsPass annotates stable raw-int callsite ABI facts after
// inlining and TypeSpec have exposed precise argument types.
func AnnotateCallABIsPass(config CallABIAnnotationConfig) PassFunc {
	return func(fn *Function) (*Function, error) {
		return AnnotateCallABIs(fn, config), nil
	}
}

// AnnotateCallABIs installs CallABIDescriptor entries for non-tail, fixed
// arity, single-result global calls whose callee has a raw-int specialized ABI
// and whose actual arguments are all TypeInt.
func AnnotateCallABIs(fn *Function, config CallABIAnnotationConfig) *Function {
	if fn == nil {
		return fn
	}
	globals := callABIMergeGlobals(config.Globals, callABIStableGlobals(fn.Proto))
	fn.CallABIs = nil

	tails := callABITailCalls(fn)
	shiftAddOverflowVersions := make(map[*vm.FuncProto]bool)
	descs := make(map[int]CallABIDescriptor)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			if callABIAnnotateRawIntSelfResult(fn, instr, tails) {
				functionRemarks(fn).Add("CallABI", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("annotated raw-int self call result for %s", fn.Proto.Name))
				continue
			}
			if callABIAnnotateTypedSelfResult(fn, instr, tails) {
				functionRemarks(fn).Add("CallABI", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("annotated typed self call result for %s", fn.Proto.Name))
				continue
			}
			if len(globals) == 0 {
				continue
			}
			desc, reason := callABIDescriptorFor(fn, instr, globals, tails, shiftAddOverflowVersions)
			if desc.Callee == nil {
				functionRemarks(fn).Add("CallABI", "missed", block.ID, instr.ID, instr.Op, reason)
				continue
			}
			descs[instr.ID] = desc
			instr.Type = TypeInt
			functionRemarks(fn).Add("CallABI", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("annotated raw-int call ABI for %s", desc.Callee.Name))
		}
	}
	if len(descs) > 0 {
		fn.CallABIs = descs
	}
	return fn
}

func callABIAnnotateRawIntSelfResult(fn *Function, instr *Instr, tails map[int]bool) bool {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpCall {
		return false
	}
	if tails[instr.ID] || !callABIHasExactFixedShape(fn, instr) || !callABIIsStaticSelfCall(fn, instr) {
		return false
	}
	abi := AnalyzeRawIntSelfABI(fn.Proto)
	if !abi.Eligible || abi.Return != SpecializedABIReturnRawInt {
		return false
	}
	numArgs := len(instr.Args) - 1
	if numArgs != abi.NumParams {
		return false
	}
	for i := 0; i < numArgs; i++ {
		if !callABIValueIsInt(instr.Args[1+i]) {
			return false
		}
	}
	instr.Type = TypeInt
	return true
}

func callABIAnnotateTypedSelfResult(fn *Function, instr *Instr, tails map[int]bool) bool {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpCall {
		return false
	}
	if tails[instr.ID] || !callABIHasExactFixedShape(fn, instr) || !callABIIsStaticSelfCall(fn, instr) {
		return false
	}
	abi := AnalyzeTypedSelfABI(fn.Proto)
	if !abi.Eligible {
		return false
	}
	numArgs := len(instr.Args) - 1
	if numArgs != abi.NumParams || len(abi.Params) != numArgs {
		return false
	}
	for i := 0; i < numArgs; i++ {
		switch abi.Params[i] {
		case SpecializedABIParamRawInt:
			if !callABIValueIsInt(instr.Args[1+i]) {
				return false
			}
		case SpecializedABIParamRawTablePtr:
			if !callABIValueCanBeTable(instr.Args[1+i]) {
				return false
			}
		default:
			return false
		}
	}
	switch abi.Return {
	case SpecializedABIReturnRawInt:
		instr.Type = TypeInt
	case SpecializedABIReturnRawTablePtr:
		instr.Type = TypeTable
	default:
		return false
	}
	return true
}

func callABIDescriptorFor(fn *Function, instr *Instr, globals map[string]*vm.FuncProto, tails map[int]bool, shiftAddOverflowVersions map[*vm.FuncProto]bool) (CallABIDescriptor, string) {
	if instr == nil || instr.Op != OpCall {
		return CallABIDescriptor{}, "not a call"
	}
	if tails[instr.ID] {
		return CallABIDescriptor{}, "tail call"
	}
	if !callABIHasExactFixedShape(fn, instr) {
		return CallABIDescriptor{}, "call does not have fixed arity and one exact result"
	}
	_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
	if callee == nil {
		return CallABIDescriptor{}, "callee is not statically resolved from stable globals"
	}
	if fn != nil && callee == fn.Proto {
		return CallABIDescriptor{}, "self call uses separate raw-int result annotation"
	}
	abi := AnalyzeSpecializedABI(callee)
	crossRecursiveNumeric := false
	if !abi.Eligible || abi.Kind != SpecializedABIRawInt || abi.Return != SpecializedABIReturnRawInt {
		crossRecursiveNumeric = qualifiesForNumericCrossRecursiveCandidate(callee)
		if !crossRecursiveNumeric && abi.RejectWhy != "" {
			return CallABIDescriptor{}, "callee raw-int ABI rejected: " + abi.RejectWhy
		}
		if !crossRecursiveNumeric {
			return CallABIDescriptor{}, "callee is not raw-int ABI eligible"
		}
	}
	if callABICalleeHasShiftAddOverflowVersion(callee, shiftAddOverflowVersions) {
		return CallABIDescriptor{}, "callee may promote raw-int recurrence on overflow"
	}
	numArgs := len(instr.Args) - 1
	if numArgs != callee.NumParams {
		return CallABIDescriptor{}, "argument count does not match callee ABI"
	}
	if !crossRecursiveNumeric && len(abi.Params) != numArgs {
		return CallABIDescriptor{}, "argument count does not match callee ABI"
	}
	rawParams := make([]bool, numArgs)
	for i := 0; i < numArgs; i++ {
		if !crossRecursiveNumeric && abi.Params[i] != SpecializedABIParamRawInt {
			return CallABIDescriptor{}, "callee has non-raw-int ABI parameter"
		}
		if !callABIValueIsInt(instr.Args[1+i]) {
			return CallABIDescriptor{}, "actual argument is not TypeInt"
		}
		rawParams[i] = true
	}
	return CallABIDescriptor{
		Callee:       callee,
		NumArgs:      numArgs,
		NumRets:      1,
		RawIntParams: rawParams,
		RawIntReturn: true,
	}, ""
}

func callABIHasExactFixedShape(fn *Function, instr *Instr) bool {
	if fn == nil || fn.Proto == nil || instr == nil || len(instr.Args) == 0 || instr.Aux2 != 2 {
		return false
	}
	if !instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.Code) {
		return false
	}
	inst := fn.Proto.Code[instr.SourcePC]
	if vm.DecodeOp(inst) != vm.OP_CALL || vm.DecodeA(inst) != int(instr.Aux) {
		return false
	}
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	if b == 0 || c != 2 {
		return false
	}
	return b-1 == len(instr.Args)-1
}

func callABIValueIsInt(v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Type == TypeInt
}

func callABIValueIsTable(v *Value) bool {
	return v != nil && v.Def != nil && v.Def.Type == TypeTable
}

func callABIValueCanBeTable(v *Value) bool {
	return v != nil && v.Def != nil && (v.Def.Type == TypeTable || v.Def.Type == TypeAny || v.Def.Type == TypeUnknown)
}

func callABIIsStaticSelfCall(fn *Function, instr *Instr) bool {
	if fn == nil || fn.Proto == nil || instr == nil || len(instr.Args) == 0 {
		return false
	}
	fnArg := instr.Args[0]
	if fnArg == nil || fnArg.Def == nil || fnArg.Def.Op != OpGetGlobal {
		return false
	}
	constIdx := int(fnArg.Def.Aux)
	if constIdx < 0 || constIdx >= len(fn.Proto.Constants) {
		return false
	}
	kv := fn.Proto.Constants[constIdx]
	return kv.IsString() && kv.Str() == fn.Proto.Name
}

func callABICalleeHasShiftAddOverflowVersion(callee *vm.FuncProto, memo map[*vm.FuncProto]bool) bool {
	if callee == nil {
		return false
	}
	if memo != nil {
		if cached, ok := memo[callee]; ok {
			return cached
		}
	}
	setResult := func(result bool) bool {
		if memo != nil {
			memo[callee] = result
		}
		return result
	}
	fn := BuildGraph(callee)
	if fn == nil || fn.Entry == nil || fn.Unpromotable {
		return setResult(false)
	}
	passes := []PassFunc{
		SimplifyPhisPass,
		TypeSpecializePass,
		ConstPropPass,
		DCEPass,
		RangeAnalysisPass,
		OverflowBoxingPass,
	}
	var err error
	for _, pass := range passes {
		fn, err = pass(fn)
		if err != nil {
			return setResult(false)
		}
	}
	_, ok := detectShiftAddOverflowVersion(fn)
	return setResult(ok)
}

func callABITailCalls(fn *Function) map[int]bool {
	out := make(map[int]bool)
	if fn == nil {
		return out
	}
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			j := i + 1
			for j < len(block.Instrs) && block.Instrs[j].Op == OpNop {
				j++
			}
			if j >= len(block.Instrs) {
				continue
			}
			next := block.Instrs[j]
			if next.Op == OpReturn && len(next.Args) == 1 && next.Args[0].ID == instr.ID {
				out[instr.ID] = true
			}
		}
	}
	return out
}

func callABIMergeGlobals(primary, secondary map[string]*vm.FuncProto) map[string]*vm.FuncProto {
	if len(primary) == 0 {
		return secondary
	}
	if len(secondary) == 0 {
		return primary
	}
	merged := make(map[string]*vm.FuncProto, len(primary)+len(secondary))
	for name, proto := range primary {
		merged[name] = proto
	}
	for name, proto := range secondary {
		if _, ok := merged[name]; !ok {
			merged[name] = proto
		}
	}
	return merged
}

func callABIStableGlobals(proto *vm.FuncProto) map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if proto == nil {
		return globals
	}
	invalid := make(map[string]bool)
	regClosure := make(map[int]*vm.FuncProto)
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_CLOSURE:
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(proto.Protos) {
				delete(regClosure, a)
				continue
			}
			regClosure[a] = proto.Protos[bx]
		case vm.OP_MOVE:
			b := vm.DecodeB(inst)
			if cl := regClosure[b]; cl != nil {
				regClosure[a] = cl
			} else {
				delete(regClosure, a)
			}
		case vm.OP_SETGLOBAL:
			name := callABIProtoConstString(proto, vm.DecodeBx(inst))
			if name == "" || invalid[name] {
				continue
			}
			cl := regClosure[a]
			if cl == nil {
				invalid[name] = true
				delete(globals, name)
				continue
			}
			if prev := globals[name]; prev != nil && prev != cl {
				invalid[name] = true
				delete(globals, name)
				continue
			}
			globals[name] = cl
		case vm.OP_CLOSE:
			continue
		default:
			delete(regClosure, a)
		}
	}
	return globals
}

func callABIProtoConstString(proto *vm.FuncProto, idx int) string {
	if proto == nil || idx < 0 || idx >= len(proto.Constants) {
		return ""
	}
	kv := proto.Constants[idx]
	if !kv.IsString() {
		return ""
	}
	return kv.Str()
}
