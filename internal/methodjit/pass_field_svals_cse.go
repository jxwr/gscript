package methodjit

import "fmt"

// FieldSvalsCSEPass merges duplicate OpFieldSvals values that prove the same
// table value still has the same fixed shape. FieldSvalsLower can create
// multiple svals anchors around stores or lowered table operations; keeping
// one SSA value avoids repeated shape checks and svals pointer loads.
func FieldSvalsCSEPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	changed := false
	for _, block := range fn.Blocks {
		if block == nil || len(block.Instrs) == 0 {
			continue
		}
		available := make(map[fieldSvalsLowerKey]*Instr)
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			if fieldSvalsGlobalBarrier(instr) {
				available = make(map[fieldSvalsLowerKey]*Instr)
				continue
			}
			if tableID, ok := fieldSvalsMutationTableID(instr); ok {
				for key := range available {
					if key.tableID == tableID {
						delete(available, key)
					}
				}
			}
			if instr.Op != OpFieldSvals || len(instr.Args) == 0 || instr.Args[0] == nil || instr.Aux == 0 {
				continue
			}
			key := fieldSvalsLowerKey{tableID: instr.Args[0].ID, shapeID: uint32(instr.Aux)}
			if prev := available[key]; prev != nil {
				replaceValueUses(fn, instr.ID, prev.Value(), prev.ID)
				instr.Op = OpNop
				instr.Type = TypeUnknown
				instr.Args = nil
				instr.Aux = 0
				instr.Aux2 = 0
				changed = true
				functionRemarks(fn).Add("FieldSvalsCSE", "changed", block.ID, prev.ID, prev.Op,
					fmt.Sprintf("reused svals v%d for table v%d shape %d", prev.ID, key.tableID, key.shapeID))
				continue
			}
			available[key] = instr
		}
	}
	if changed {
		relinkValueDefs(fn)
	}
	return fn, nil
}
