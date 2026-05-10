package methodjit

// CallReturnProjectionPass turns a side-effecting call followed by a pure
// projection of its single result into one explicit call-projection op. This
// gives codegen one protocol owner for fast path, fallback, and callee-exit
// recovery instead of trying to fuse two independent instructions ad hoc.
func CallReturnProjectionPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	uses := computeUseCounts(fn)
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr == nil || instr.Op != OpCall || uses[instr.ID] != 1 || i+1 >= len(block.Instrs) {
				continue
			}
			if !callReturnProjectionCandidate(fn, instr) {
				continue
			}
			next := block.Instrs[i+1]
			if next == nil || next.Op != OpFloor || len(next.Args) != 1 ||
				next.Args[0] == nil || next.Args[0].ID != instr.ID {
				continue
			}
			instr.Op = OpCallFloor
			instr.Type = TypeInt
			if calleeLoad := fieldShapeMethodCalleeLoad(instr); calleeLoad != nil &&
				uses[calleeLoad.ID] == 1 && fieldShapeTypedPeerProjectionCandidate(fn, instr) {
				if fn.FieldPolyShapeFacts != nil {
					if cases := fn.FieldPolyShapeFacts[calleeLoad.ID]; len(cases) > 0 {
						fn.FieldPolyShapeFacts[instr.ID] = cases
					}
				}
				instr.Op = OpFieldCallFloor
				instr.Args = append([]*Value(nil), instr.Args[1:]...)
				calleeLoad.Op = OpNop
				calleeLoad.Type = TypeUnknown
				calleeLoad.Args = nil
				calleeLoad.Aux = 0
				calleeLoad.Aux2 = 0
			}
			replaceValueUses(fn, next.ID, instr.Value(), instr.ID)
			next.Op = OpNop
			next.Type = TypeUnknown
			next.Args = nil
			next.Aux = 0
			next.Aux2 = 0
			functionRemarks(fn).Add("CallReturnProjection", "changed", block.ID, instr.ID, instr.Op,
				"folded single-use call result into floor projection")
		}
	}
	return fn, nil
}

func fieldShapeMethodCalleeLoad(instr *Instr) *Instr {
	if instr == nil || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[0].Def == nil {
		return nil
	}
	calleeLoad := instr.Args[0].Def
	if calleeLoad.Op != OpGetField || len(calleeLoad.Args) == 0 || calleeLoad.Args[0] == nil {
		return nil
	}
	if instr.Args[1] == nil || instr.Args[1].ID != calleeLoad.Args[0].ID {
		return nil
	}
	return calleeLoad
}

func callReturnProjectionCandidate(fn *Function, instr *Instr) bool {
	if fn == nil || instr == nil || instr.Op != OpCall {
		return false
	}
	if fn.CallABIs != nil {
		if _, ok := fn.CallABIs[instr.ID]; ok {
			return true
		}
	}
	return fieldShapeTypedPeerProjectionCandidate(fn, instr)
}

func fieldShapeTypedPeerProjectionCandidate(fn *Function, instr *Instr) bool {
	if fn == nil || instr == nil || (instr.Op != OpCall && instr.Op != OpCallFloor) || len(instr.Args) < 2 ||
		instr.Args[0] == nil || instr.Args[0].Def == nil {
		return false
	}
	calleeLoad := instr.Args[0].Def
	if calleeLoad.Op != OpGetField || len(calleeLoad.Args) == 0 || calleeLoad.Args[0] == nil {
		return false
	}
	receiver := calleeLoad.Args[0]
	if instr.Args[1] == nil || instr.Args[1].ID != receiver.ID {
		return false
	}
	nArgs := len(instr.Args) - 1
	if callResultCountFromAux2(instr.Aux2) != 1 || nArgs < 1 || nArgs > 4 {
		return false
	}
	cases := fn.FieldPolyShapeFacts[calleeLoad.ID]
	if len(cases) < 2 {
		return false
	}
	var paramReps []SpecializedABIParamRep
	for _, c := range cases {
		if c.ShapeID == 0 || c.FieldIdx < 0 || c.VMProto == nil || c.VMProto.NumParams != nArgs {
			return false
		}
		abi := AnalyzeTypedPeerABIWithArgFacts(c.VMProto, map[int]FixedShapeTableFact{0: c.ReceiverFact})
		if !abi.Eligible || len(abi.Params) != nArgs || abi.Params[0] != SpecializedABIParamRawTablePtr {
			return false
		}
		switch abi.Return {
		case SpecializedABIReturnRawInt, SpecializedABIReturnRawFloat:
		default:
			return false
		}
		if len(paramReps) == 0 {
			paramReps = append([]SpecializedABIParamRep(nil), abi.Params...)
		} else {
			for i, rep := range abi.Params {
				if paramReps[i] != rep {
					return false
				}
			}
		}
		for i, rep := range abi.Params {
			switch rep {
			case SpecializedABIParamRawInt:
				if !callABIValueIsInt(instr.Args[1+i]) {
					return false
				}
			case SpecializedABIParamRawTablePtr:
				if i != 0 && !callABIValueIsTable(instr.Args[1+i]) {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}
