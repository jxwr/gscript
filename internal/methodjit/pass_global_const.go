package methodjit

import "github.com/gscript/gscript/internal/runtime"

func GlobalConstSpecializationPass(values map[int]runtime.Value) PassFunc {
	return func(fn *Function) (*Function, error) {
		return globalConstSpecializationPass(fn, values)
	}
}

func globalConstSpecializationPass(fn *Function, values map[int]runtime.Value) (*Function, error) {
	if fn == nil || len(values) == 0 {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		changed := false
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpGetGlobal {
				continue
			}
			v, ok := values[int(instr.Aux)]
			if !ok || (!v.IsInt() && !v.IsFloat()) {
				continue
			}
			changed = true
			break
		}
		if !changed {
			continue
		}
		newInstrs := make([]*Instr, 0, len(block.Instrs)*2)
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpGetGlobal {
				newInstrs = append(newInstrs, instr)
				continue
			}
			v, ok := values[int(instr.Aux)]
			if !ok || (!v.IsInt() && !v.IsFloat()) {
				newInstrs = append(newInstrs, instr)
				continue
			}
			guard := emitIRInstr(fn, block, OpGuardGlobalConst, TypeUnknown, nil, instr.Aux, int64(uint64(v)))
			guard.copySourceFrom(instr)
			newInstrs = append(newInstrs, guard)
			if v.IsInt() {
				instr.Op = OpConstInt
				instr.Type = TypeInt
				instr.Aux = v.Int()
			} else {
				instr.Op = OpConstFloat
				instr.Type = TypeFloat
				instr.Aux = int64(uint64(v))
			}
			instr.Args = nil
			instr.Aux2 = 0
			functionRemarks(fn).Add("GlobalConstSpecialization", "changed", block.ID, instr.ID, OpGetGlobal,
				"guarded numeric global as constant")
			newInstrs = append(newInstrs, instr)
		}
		block.Instrs = newInstrs
	}
	return fn, nil
}

func globalConstFunctionSafe(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpCall, OpResume, OpYield, OpSelf, OpSetGlobal, OpSetUpval, OpGo, OpSend, OpRecv:
				return false
			}
		}
	}
	return true
}
