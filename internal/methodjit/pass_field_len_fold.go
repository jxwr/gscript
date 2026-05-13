package methodjit

// FieldLenFoldPass folds len(obj.field) at simple join blocks when every
// predecessor writes that field to a constant string of the same byte length.
// Unlike profiled length ranges, this is a structural proof: no runtime guard
// is needed because the dominating predecessor writes determine the value.
func FieldLenFoldPass(fn *Function) (*Function, error) {
	if fn == nil || fn.Proto == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpLen || len(instr.Args) < 1 || instr.Args[0] == nil || instr.Args[0].Def == nil {
				continue
			}
			if foldProfiledExactLen(fn, block, instr) {
				continue
			}
			if foldPhiStringLen(fn, block, instr) {
				continue
			}
			get := unwrapFieldLenInput(instr.Args[0]).Def
			if get.Op != OpGetField || len(get.Args) < 1 || get.Args[0] == nil {
				continue
			}
			if lowerFieldPolyLen(fn, instr, get) {
				functionRemarks(fn).Add("FieldLenFold", "changed", block.ID, instr.ID, instr.Op,
					"lowered len(field) to guarded polymorphic field length")
				continue
			}
			lens, ok := constStringFieldLensFromPreds(fn, block, get.Args[0].ID, get.Aux)
			if !ok || len(lens) != len(block.Preds) {
				continue
			}
			if allInt64Equal(lens) {
				instr.Op = OpConstInt
				instr.Type = TypeInt
				instr.Args = nil
				instr.Aux = lens[0]
				instr.Aux2 = 0
				functionRemarks(fn).Add("FieldLenFold", "changed", block.ID, instr.ID, instr.Op,
					"folded len(field) from predecessor constant string stores")
				continue
			}
			phi := insertFieldLenPhi(fn, block, lens)
			if phi == nil {
				continue
			}
			replaceValueUses(fn, instr.ID, phi.Value(), phi.ID)
			instr.Op = OpNop
			instr.Type = TypeUnknown
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			functionRemarks(fn).Add("FieldLenFold", "changed", block.ID, phi.ID, phi.Op,
				"replaced len(field) with predecessor constant string length phi")
		}
	}
	return fn, nil
}

func ProfiledStringLenFoldPass(fn *Function) (*Function, error) {
	if fn == nil || fn.Proto == nil {
		return fn, nil
	}
	fieldLoadLens := fieldLoadExactLenFacts(fn)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpLen || len(instr.Args) < 1 || instr.Args[0] == nil {
				continue
			}
			if foldExactLenFromMap(fn, block, instr, fieldLoadLens) {
				continue
			}
			if foldProfiledExactLen(fn, block, instr) {
				continue
			}
			foldPhiStringLen(fn, block, instr)
		}
	}
	return fn, nil
}

func fieldLoadExactLenFacts(fn *Function) map[int]intRange {
	if fn == nil {
		return nil
	}
	svalsFacts := make(map[int]FixedShapeTableFact)
	out := make(map[int]intRange)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpFieldSvals:
				if len(instr.Args) == 0 || instr.Args[0] == nil {
					continue
				}
				fact, ok := fixedShapeFactForValue(fn, instr.Args[0].ID)
				if !ok || fact.ShapeID == 0 || uint32(instr.Aux) != fact.ShapeID {
					continue
				}
				svalsFacts[instr.ID] = fact
			case OpFieldLoad:
				if len(instr.Args) == 0 || instr.Args[0] == nil {
					continue
				}
				fact, ok := svalsFacts[instr.Args[0].ID]
				if !ok {
					continue
				}
				idx := int(instr.Aux)
				if idx < 0 || idx >= len(fact.FieldNames) {
					continue
				}
				name := fact.FieldNames[idx]
				if r, ok := fact.FieldLenRanges[name]; ok && r.known && r.min == r.max && r.min >= 0 {
					out[instr.ID] = r
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func fixedShapeFactForValue(fn *Function, id int) (FixedShapeTableFact, bool) {
	if fn == nil {
		return FixedShapeTableFact{}, false
	}
	if fact, ok := fn.FixedShapeTables[id]; ok {
		return fact, true
	}
	if fact, ok := fn.FixedShapeArgFacts[id]; ok {
		return fact, true
	}
	return FixedShapeTableFact{}, false
}

func foldExactLenFromMap(fn *Function, block *Block, lenInstr *Instr, lens map[int]intRange) bool {
	if fn == nil || lenInstr == nil || len(lenInstr.Args) == 0 || lenInstr.Args[0] == nil || len(lens) == 0 {
		return false
	}
	r, ok := lens[lenInstr.Args[0].ID]
	if !ok || !r.known || r.min != r.max || r.min < 0 {
		return false
	}
	lenInstr.Op = OpConstInt
	lenInstr.Type = TypeInt
	lenInstr.Args = nil
	lenInstr.Aux = r.min
	lenInstr.Aux2 = 0
	functionRemarks(fn).Add("FieldLenFold", "changed", block.ID, lenInstr.ID, lenInstr.Op,
		"folded lowered field string length from fixed-shape facts")
	return true
}

func foldProfiledExactLen(fn *Function, block *Block, lenInstr *Instr) bool {
	if fn == nil || lenInstr == nil || len(lenInstr.Args) == 0 || lenInstr.Args[0] == nil {
		return false
	}
	r, ok := fn.ProfiledLenRanges[lenInstr.Args[0].ID]
	if !ok || !r.known || r.min != r.max || r.min < 0 {
		return false
	}
	lenInstr.Op = OpConstInt
	lenInstr.Type = TypeInt
	lenInstr.Args = nil
	lenInstr.Aux = r.min
	lenInstr.Aux2 = 0
	functionRemarks(fn).Add("FieldLenFold", "changed", block.ID, lenInstr.ID, lenInstr.Op,
		"folded guarded exact string length")
	return true
}

func foldPhiStringLen(fn *Function, block *Block, lenInstr *Instr) bool {
	if fn == nil || block == nil || lenInstr == nil || len(lenInstr.Args) == 0 || lenInstr.Args[0] == nil {
		return false
	}
	phi := lenInstr.Args[0].Def
	if phi == nil || phi.Op != OpPhi || phi.Block == nil || len(phi.Args) == 0 || len(phi.Args) != len(phi.Block.Preds) {
		return false
	}
	lens := make([]int64, len(phi.Args))
	for i, arg := range phi.Args {
		if arg == nil {
			return false
		}
		r, ok := fn.ProfiledLenRanges[arg.ID]
		if !ok || !r.known || r.min != r.max || r.min < 0 {
			return false
		}
		lens[i] = r.min
	}
	if allInt64Equal(lens) {
		lenInstr.Op = OpConstInt
		lenInstr.Type = TypeInt
		lenInstr.Args = nil
		lenInstr.Aux = lens[0]
		lenInstr.Aux2 = 0
		functionRemarks(fn).Add("FieldLenFold", "changed", block.ID, lenInstr.ID, lenInstr.Op,
			"folded len(phi(strings)) from guarded exact lengths")
		return true
	}
	lenPhi := insertFieldLenPhi(fn, phi.Block, lens)
	if lenPhi == nil {
		return false
	}
	replaceValueUses(fn, lenInstr.ID, lenPhi.Value(), lenPhi.ID)
	lenInstr.Op = OpNop
	lenInstr.Type = TypeUnknown
	lenInstr.Args = nil
	lenInstr.Aux = 0
	lenInstr.Aux2 = 0
	functionRemarks(fn).Add("FieldLenFold", "changed", phi.Block.ID, lenPhi.ID, lenPhi.Op,
		"replaced len(phi(strings)) with guarded string length phi")
	return true
}

func lowerFieldPolyLen(fn *Function, lenInstr, get *Instr) bool {
	if fn == nil || lenInstr == nil || get == nil || get.Op != OpGetField || len(get.Args) == 0 || get.Args[0] == nil {
		return false
	}
	cases := fieldPolyExactLenCases(fn, get)
	if len(cases) < 2 {
		return false
	}
	if fn.FieldPolyShapeFacts == nil {
		fn.FieldPolyShapeFacts = make(map[int][]FieldPolyShapeCase)
	}
	fn.FieldPolyShapeFacts[lenInstr.ID] = cases
	name := fieldNameFromAux(fn, get.Aux)
	if r, ok := fieldPolyLenRange(fn, name, cases); ok {
		if fn.ProfiledIntRanges == nil {
			fn.ProfiledIntRanges = make(map[int]intRange)
		}
		fn.ProfiledIntRanges[lenInstr.ID] = r
	}
	lenInstr.Op = OpFieldPolyLen
	lenInstr.Type = TypeInt
	lenInstr.Args = []*Value{get.Args[0]}
	lenInstr.Aux = get.Aux
	lenInstr.Aux2 = 0
	return true
}

func fieldPolyExactLenCases(fn *Function, get *Instr) []FieldPolyShapeCase {
	if fn == nil || get == nil || get.Op != OpGetField {
		return nil
	}
	name := fieldNameFromAux(fn, get.Aux)
	if name == "" {
		return nil
	}
	src := fn.FieldPolyShapeFacts[get.ID]
	if len(src) < 2 {
		return nil
	}
	out := make([]FieldPolyShapeCase, 0, len(src))
	for _, c := range src {
		if c.ShapeID == 0 {
			return nil
		}
		r, ok := c.ReceiverFact.FieldLenRanges[name]
		if !ok || !r.known || r.min != r.max {
			return nil
		}
		out = append(out, c)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func fieldPolyLenRange(fn *Function, name string, cases []FieldPolyShapeCase) (intRange, bool) {
	if fn == nil || name == "" || len(cases) == 0 {
		return intRange{}, false
	}
	var out intRange
	for _, c := range cases {
		r, ok := c.ReceiverFact.FieldLenRanges[name]
		if !ok || !r.known {
			return intRange{}, false
		}
		if !out.known {
			out = r
			continue
		}
		out = joinRange(out, r)
	}
	return out, out.known
}

func unwrapFieldLenInput(v *Value) *Value {
	for v != nil && v.Def != nil {
		switch v.Def.Op {
		case OpGuardType, OpGuardConstString:
			if len(v.Def.Args) == 0 || v.Def.Args[0] == nil {
				return v
			}
			v = v.Def.Args[0]
		default:
			return v
		}
	}
	return v
}

func constStringFieldLensFromPreds(fn *Function, block *Block, tableID int, fieldAux int64) ([]int64, bool) {
	if fn == nil || block == nil || len(block.Preds) == 0 {
		return nil, false
	}
	out := make([]int64, len(block.Preds))
	for _, pred := range block.Preds {
		n, ok := lastConstStringStoreLen(fn, pred, tableID, fieldAux)
		if !ok {
			return nil, false
		}
		idx := -1
		for i, p := range block.Preds {
			if p == pred {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, false
		}
		out[idx] = n
	}
	return out, true
}

func allInt64Equal(vals []int64) bool {
	if len(vals) == 0 {
		return false
	}
	for _, v := range vals[1:] {
		if v != vals[0] {
			return false
		}
	}
	return true
}

func insertFieldLenPhi(fn *Function, block *Block, lens []int64) *Instr {
	if fn == nil || block == nil || len(lens) != len(block.Preds) {
		return nil
	}
	args := make([]*Value, len(lens))
	for i, pred := range block.Preds {
		c := &Instr{
			ID:    fn.newValueID(),
			Op:    OpConstInt,
			Type:  TypeInt,
			Aux:   lens[i],
			Block: pred,
		}
		insertBeforeTerminator(pred, c)
		args[i] = c.Value()
	}
	phi := &Instr{
		ID:    fn.newValueID(),
		Op:    OpPhi,
		Type:  TypeInt,
		Args:  args,
		Block: block,
	}
	insertAtTopAfterPhis(block, phi)
	return phi
}
func lastConstStringStoreLen(fn *Function, block *Block, tableID int, fieldAux int64) (int64, bool) {
	if fn == nil || fn.Proto == nil || block == nil {
		return 0, false
	}
	for i := len(block.Instrs) - 1; i >= 0; i-- {
		instr := block.Instrs[i]
		if instr == nil || instr.Op == OpNop || instr.Op.IsTerminator() {
			continue
		}
		if instr.Op == OpSetField && instr.Aux == fieldAux && len(instr.Args) >= 2 &&
			instr.Args[0] != nil && instr.Args[0].ID == tableID {
			return constStringLen(fn, instr.Args[1])
		}
		if fieldLenFoldBarrier(instr) {
			return 0, false
		}
	}
	return 0, false
}

func fieldLenFoldBarrier(instr *Instr) bool {
	switch instr.Op {
	case OpCall, OpSetField, OpSetTable, OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs,
		OpSetGlobal, OpSetUpval, OpAppend, OpSetList:
		return true
	default:
		return false
	}
}

func constStringLen(fn *Function, v *Value) (int64, bool) {
	if fn == nil || fn.Proto == nil || v == nil || v.Def == nil || v.Def.Op != OpConstString {
		return 0, false
	}
	idx := int(v.Def.Aux)
	if idx < 0 || idx >= len(fn.Proto.Constants) {
		return 0, false
	}
	c := fn.Proto.Constants[idx]
	if !c.IsString() {
		return 0, false
	}
	return int64(len(c.Str())), true
}
