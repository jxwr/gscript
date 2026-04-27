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
	if len(globals) == 0 {
		return fn
	}

	tails := callABITailCalls(fn)
	descs := make(map[int]CallABIDescriptor)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			desc, reason := callABIDescriptorFor(fn, instr, globals, tails)
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

func callABIDescriptorFor(fn *Function, instr *Instr, globals map[string]*vm.FuncProto, tails map[int]bool) (CallABIDescriptor, string) {
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
		return CallABIDescriptor{}, "self call is not annotated by the MVP"
	}
	abi := AnalyzeSpecializedABI(callee)
	if !abi.Eligible || abi.Kind != SpecializedABIRawInt || abi.Return != SpecializedABIReturnRawInt {
		if abi.RejectWhy != "" {
			return CallABIDescriptor{}, "callee raw-int ABI rejected: " + abi.RejectWhy
		}
		return CallABIDescriptor{}, "callee is not raw-int ABI eligible"
	}
	numArgs := len(instr.Args) - 1
	if numArgs != callee.NumParams || len(abi.Params) != numArgs {
		return CallABIDescriptor{}, "argument count does not match callee ABI"
	}
	rawParams := make([]bool, numArgs)
	for i := 0; i < numArgs; i++ {
		if abi.Params[i] != SpecializedABIParamRawInt {
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
