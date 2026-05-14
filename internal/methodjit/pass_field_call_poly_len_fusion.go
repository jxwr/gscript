package methodjit

// FieldCallPolyLenFusionPass connects a guarded typed-peer field call with a
// later same-block guarded field length on the same receiver. It does not
// rewrite IR; it records a codegen-side fusion that lets the successful call
// shape arm produce the later length value and lets OpFieldPolyLen skip its own
// shape dispatch when that value is already live.
func FieldCallPolyLenFusionPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 || len(fn.FieldPolyShapeFacts) == 0 {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for i, call := range block.Instrs {
			if call == nil || call.Op != OpFieldCallFloor || len(call.Args) == 0 || call.Args[0] == nil {
				continue
			}
			callCases := fn.FieldPolyShapeFacts[call.ID]
			if len(callCases) < 2 {
				continue
			}
			for j := i + 1; j < len(block.Instrs); j++ {
				cur := block.Instrs[j]
				if fieldCallPolyLenFusionBarrier(cur) {
					break
				}
				if cur == nil || cur.Op != OpFieldPolyLen || len(cur.Args) == 0 || cur.Args[0] == nil || cur.Args[0].ID != call.Args[0].ID {
					continue
				}
				fusions := fieldCallPolyLenFusionCases(fn, call, cur, callCases)
				if len(fusions) == 0 {
					continue
				}
				if fn.FieldCallPolyLenFusions == nil {
					fn.FieldCallPolyLenFusions = make(map[int][]FieldCallPolyLenFusion)
				}
				fn.FieldCallPolyLenFusions[call.ID] = append(fn.FieldCallPolyLenFusions[call.ID], fusions...)
				if remarks := functionRemarks(fn); remarks != nil {
					remarks.Add("FieldCallPolyLenFusion", "changed", block.ID, call.ID, call.Op,
						"reused field-call shape dispatch for later field length")
				}
				break
			}
		}
	}
	return fn, nil
}

func fieldCallPolyLenFusionBarrier(instr *Instr) bool {
	if instr == nil {
		return false
	}
	switch instr.Op {
	case OpSetField, OpSetTable, OpSetList, OpAppend, OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs,
		OpTableBoolArrayFill, OpTableIntArrayReversePrefix, OpTableIntArrayCopyPrefix,
		OpCall, OpCallFloor, OpFieldCallFloor, OpResume, OpYield, OpSelf, OpGo, OpSend, OpRecv,
		OpReturn, OpJump, OpBranch:
		return true
	default:
		return false
	}
}

func fieldCallPolyLenFusionCases(fn *Function, call, ln *Instr, callCases []FieldPolyShapeCase) []FieldCallPolyLenFusion {
	if fn == nil || call == nil || ln == nil || len(callCases) == 0 {
		return nil
	}
	name := fieldNameFromAux(fn, ln.Aux)
	if name == "" {
		return nil
	}
	lenCases := fn.FieldPolyShapeFacts[ln.ID]
	if len(lenCases) == 0 {
		return nil
	}
	lenByShape := make(map[uint32]int64, len(lenCases))
	for _, c := range lenCases {
		r, ok := c.ReceiverFact.FieldLenRanges[name]
		if c.ShapeID == 0 || !ok || !r.known || r.min != r.max {
			return nil
		}
		lenByShape[c.ShapeID] = r.min
	}
	out := make([]FieldCallPolyLenFusion, 0, len(callCases))
	for _, c := range callCases {
		if c.ShapeID == 0 || c.VMProto == nil {
			return nil
		}
		lnValue, ok := lenByShape[c.ShapeID]
		if !ok {
			return nil
		}
		effects := SummarizeFieldEffects(c.VMProto)
		if !effects.ParamMutationKnown(0) || effects.WritesParamField(0, name) {
			return nil
		}
		out = append(out, FieldCallPolyLenFusion{
			LenValueID: ln.ID,
			FieldAux:   ln.Aux,
			ShapeID:    c.ShapeID,
			Len:        lnValue,
		})
	}
	return out
}
