package methodjit

// StaticTableArrayElementTypePass propagates the homogeneous element type of a
// non-escaping SetList-initialized table to direct and lowered array loads. It
// is a type fact only: bounds, metatable, and representation guards remain in
// the normal table-array lowering.
func StaticTableArrayElementTypePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	elemTypes := collectStaticTableArrayElementTypes(fn)
	if len(elemTypes) == 0 {
		return fn, nil
	}
	headerTables := make(map[int]int)
	dataTables := make(map[int]int)
	changed := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpTableArrayHeader:
				if len(instr.Args) == 1 && instr.Args[0] != nil {
					if _, ok := elemTypes[instr.Args[0].ID]; ok {
						headerTables[instr.ID] = instr.Args[0].ID
					}
				}
			case OpTableArrayData:
				if len(instr.Args) == 1 && instr.Args[0] != nil {
					if tableID, ok := headerTables[instr.Args[0].ID]; ok {
						dataTables[instr.ID] = tableID
					}
				}
			case OpTableArrayLoad:
				if len(instr.Args) >= 1 && instr.Args[0] != nil {
					if tableID, ok := dataTables[instr.Args[0].ID]; ok {
						if setInstrType(instr, elemTypes[tableID]) {
							functionRemarks(fn).Add("StaticTableArrayType", "changed", block.ID, instr.ID, instr.Op,
								"propagated homogeneous SetList element type to lowered array load")
							changed = true
						}
					}
				}
			case OpGetTable:
				if len(instr.Args) >= 1 && instr.Args[0] != nil {
					if typ, ok := elemTypes[instr.Args[0].ID]; ok {
						if setInstrType(instr, typ) {
							functionRemarks(fn).Add("StaticTableArrayType", "changed", block.ID, instr.ID, instr.Op,
								"propagated homogeneous SetList element type to table load")
							changed = true
						}
					}
				}
			}
		}
	}
	if !changed {
		functionRemarks(fn).Add("StaticTableArrayType", "missed", 0, 0, OpTableArrayLoad,
			"no static SetList array loads consumed homogeneous element type")
	}
	return fn, nil
}

func collectStaticTableArrayElementTypes(fn *Function) map[int]Type {
	lengths := collectStaticTableLenCandidates(fn)
	if len(lengths) == 0 {
		return nil
	}
	out := make(map[int]Type)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpSetList || len(instr.Args) < 2 || instr.Args[0] == nil {
				continue
			}
			tableID := instr.Args[0].ID
			if _, ok := lengths[tableID]; !ok {
				continue
			}
			typ, ok := homogeneousStaticValueType(instr.Args[1:])
			if ok {
				out[tableID] = typ
			}
		}
	}
	return out
}

func homogeneousStaticValueType(values []*Value) (Type, bool) {
	var typ Type
	for _, v := range values {
		if v == nil || v.Def == nil {
			return TypeUnknown, false
		}
		cur := staticValueType(v.Def)
		if cur == TypeUnknown || cur == TypeAny {
			return TypeUnknown, false
		}
		if typ == TypeUnknown {
			typ = cur
			continue
		}
		if typ != cur {
			return TypeUnknown, false
		}
	}
	return typ, typ != TypeUnknown
}

func staticValueType(instr *Instr) Type {
	switch instr.Op {
	case OpConstInt:
		return TypeInt
	case OpConstFloat:
		return TypeFloat
	case OpConstBool:
		return TypeBool
	case OpConstString:
		return TypeString
	case OpConstNil:
		return TypeNil
	default:
		return TypeUnknown
	}
}

func setInstrType(instr *Instr, typ Type) bool {
	if typ == TypeUnknown || typ == TypeAny || instr.Type == typ {
		return false
	}
	instr.Type = typ
	return true
}
