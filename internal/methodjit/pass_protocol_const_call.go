//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func ProtocolConstCallFoldPass(globals map[string]*vm.FuncProto) PassFunc {
	return func(fn *Function) (*Function, error) {
		return AnnotateProtocolConstCallFolds(fn, globals), nil
	}
}

func AnnotateProtocolConstCallFolds(fn *Function, globals map[string]*vm.FuncProto) *Function {
	if fn == nil || fn.Proto == nil || len(globals) == 0 {
		return fn
	}
	stableInts := collectProtocolStableIntGlobals(fn)
	folds := make(map[int]ProtocolConstCallFoldFact)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpCall || callResultCountFromAux2(instr.Aux2) != 1 {
				continue
			}
			name, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
			if name == "" || callee == nil || len(instr.Args) < 2 || len(instr.Args)-1 > 4 {
				continue
			}
			args, intGuardConsts, intGuardValues, ok := protocolConstCallArgs(instr, stableInts)
			if !ok {
				continue
			}
			result, guardNames, guardProtos, ok := foldProtocolConstCall(callee, globals, args)
			if !ok {
				continue
			}
			if len(guardNames) == 0 {
				guardNames = []string{name}
				guardProtos = []*vm.FuncProto{callee}
			}
			guardConsts, ok := protocolGuardConstIndexes(fn.Proto, guardNames)
			if !ok {
				continue
			}
			folds[instr.ID] = ProtocolConstCallFoldFact{
				CalleeProto:    callee,
				Result:         result,
				GuardConsts:    guardConsts,
				GuardProtos:    guardProtos,
				IntGuardConsts: intGuardConsts,
				IntGuardValues: intGuardValues,
			}
		}
	}
	if len(folds) == 0 {
		fn.ProtocolConstCallFolds = nil
		return fn
	}
	fn.ProtocolConstCallFolds = folds
	return fn
}

func protocolConstCallArgs(instr *Instr, stableInts map[int]int64) ([]runtime.Value, []int, []int64, bool) {
	n := len(instr.Args) - 1
	args := make([]runtime.Value, n)
	var guardConsts []int
	var guardValues []int64
	for i := 0; i < n; i++ {
		arg := instr.Args[1+i]
		if arg == nil || arg.Def == nil {
			return nil, nil, nil, false
		}
		switch arg.Def.Op {
		case OpConstInt:
			if arg.Def.Aux < 0 {
				return nil, nil, nil, false
			}
			args[i] = runtime.IntValue(arg.Def.Aux)
		case OpGetGlobal:
			constIdx := int(arg.Def.Aux)
			v, ok := stableInts[constIdx]
			if !ok || v < 0 {
				return nil, nil, nil, false
			}
			args[i] = runtime.IntValue(v)
			guardConsts = append(guardConsts, constIdx)
			guardValues = append(guardValues, v)
		default:
			return nil, nil, nil, false
		}
	}
	return args, guardConsts, guardValues, true
}

func foldProtocolConstCall(callee *vm.FuncProto, globals map[string]*vm.FuncProto, args []runtime.Value) (int64, []string, []*vm.FuncProto, bool) {
	if callee == nil || len(args) != callee.NumParams {
		return 0, nil, nil, false
	}
	if cf, ok := newFixedRecursiveIntFoldCompiled(callee); ok {
		if len(args) != 1 {
			return 0, nil, nil, false
		}
		out, ok := cf.FixedRecursiveIntFold.fold(args[0])
		return out, nil, nil, ok
	}
	if cf, ok := newFixedRecursiveNestedIntFoldCompiled(callee); ok {
		if len(args) != 2 {
			return 0, nil, nil, false
		}
		out, ok := cf.FixedRecursiveNestedIntFold.fold(args[0], args[1])
		return out, nil, nil, ok
	}
	if cf, ok := newMutualRecursiveIntSCCCompiled(callee, globals); ok {
		var intArgs [4]int64
		for i, arg := range args {
			if !arg.IsInt() || arg.Int() < 0 {
				return 0, nil, nil, false
			}
			intArgs[i] = arg.Int()
		}
		protocol := cf.MutualRecursiveIntSCC
		e := &mutualRecursiveIntEvaluator{
			protocol: protocol,
			memo:     make(map[mutualRecursiveIntKey]int64),
			active:   make(map[mutualRecursiveIntKey]bool),
		}
		out, ok := e.eval(protocol.entryIndex, intArgs)
		if !ok {
			return 0, nil, nil, false
		}
		return out, append([]string(nil), protocol.names...), append([]*vm.FuncProto(nil), protocol.protos...), true
	}
	return 0, nil, nil, false
}

func protocolGuardConstIndexes(proto *vm.FuncProto, names []string) ([]int, bool) {
	if proto == nil || len(names) == 0 {
		return nil, false
	}
	out := make([]int, 0, len(names))
	seen := make(map[int]bool, len(names))
	for _, name := range names {
		idx := -1
		for i, c := range proto.Constants {
			if c.IsString() && c.Str() == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, false
		}
		if !seen[idx] {
			seen[idx] = true
			out = append(out, idx)
		}
	}
	return out, true
}

func collectProtocolStableIntGlobals(fn *Function) map[int]int64 {
	if fn == nil || fn.Proto == nil {
		return nil
	}
	values := make(map[int]int64)
	invalid := make(map[int]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpSetGlobal || len(instr.Args) == 0 {
				continue
			}
			constIdx := int(instr.Aux)
			arg := instr.Args[0]
			if arg == nil || arg.Def == nil || arg.Def.Op != OpConstInt {
				invalid[constIdx] = true
				delete(values, constIdx)
				continue
			}
			next := arg.Def.Aux
			if cur, ok := values[constIdx]; ok && cur != next {
				invalid[constIdx] = true
				delete(values, constIdx)
				continue
			}
			if !invalid[constIdx] {
				values[constIdx] = next
			}
		}
	}
	return values
}
