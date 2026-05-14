package methodjit

import "fmt"

// GuardFieldCalleePass fuses a fixed-shape method field load that feeds only a
// callee-proto guard:
//
//	callee = GetField(obj.step)
//	guard  = GuardCalleeProto(callee, proto)
//
// into one guard that loads obj.step and checks the closure proto in a single
// native sequence. The optimization is generic over shape id, field index and
// proto pointer; it does not inspect source names or benchmark names.
func GuardFieldCalleePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	uses := computeUseCounts(fn)
	for _, block := range fn.Blocks {
		if block == nil || len(block.Instrs) == 0 {
			continue
		}
		changed := false
		newInstrs := make([]*Instr, 0, len(block.Instrs))
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			if instr.Op == OpGuardCalleeProto && len(instr.Args) == 1 && instr.Args[0] != nil {
				load := instr.Args[0].Def
				if aux2, ok := guardFieldCalleeLoadAux2(fn, load); ok {
					instr.Op = OpGuardFieldCalleeProto
					instr.Args = []*Value{load.Args[0]}
					instr.Aux2 = aux2
					functionRemarks(fn).Add("GuardFieldCallee", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("fused field callee guard for shape/field aux2=%d", instr.Aux2))
					changed = true
				}
			}
			if instr.Op == OpGetField && uses[instr.ID] == 1 {
				if nextGuardUsesFusedFieldLoad(fn, block, instr) {
					changed = true
					continue
				}
			}
			newInstrs = append(newInstrs, instr)
		}
		if changed {
			block.Instrs = newInstrs
		}
	}
	return fn, nil
}

func guardFieldCalleeLoadAux2(fn *Function, instr *Instr) (int64, bool) {
	if instr == nil || instr.Op != OpGetField || len(instr.Args) == 0 || instr.Args[0] == nil || instr.Aux2 == 0 {
		if fn == nil || instr == nil || instr.Op != OpGetField || len(instr.Args) == 0 || instr.Args[0] == nil {
			return 0, false
		}
		cases := fn.FieldPolyShapeFacts[instr.ID]
		if len(cases) != 1 || cases[0].ShapeID == 0 || cases[0].FieldIdx < 0 {
			return 0, false
		}
		return int64(cases[0].ShapeID)<<32 | int64(uint32(cases[0].FieldIdx)), true
	}
	shapeID := uint32(instr.Aux2 >> 32)
	fieldIdx := int(int32(instr.Aux2 & 0xFFFFFFFF))
	if shapeID == 0 || fieldIdx < 0 {
		return 0, false
	}
	return instr.Aux2, true
}

func nextGuardUsesFusedFieldLoad(fn *Function, block *Block, load *Instr) bool {
	if block == nil {
		return false
	}
	aux2, ok := guardFieldCalleeLoadAux2(fn, load)
	if !ok {
		return false
	}
	for i, instr := range block.Instrs {
		if instr != load {
			continue
		}
		if i+1 >= len(block.Instrs) {
			return false
		}
		next := block.Instrs[i+1]
		if next == nil || len(next.Args) != 1 || next.Args[0] == nil || len(load.Args) == 0 {
			return false
		}
		if next.Op == OpGuardCalleeProto {
			return next.Args[0].ID == load.ID
		}
		return next.Op == OpGuardFieldCalleeProto &&
			next.Args[0].ID == load.Args[0].ID && next.Aux2 == aux2
	}
	return false
}
