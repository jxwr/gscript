package methodjit

// TableArrayDataPtrFactPass records the explicit raw data-pointer ABI facts
// behind the lowered typed-array shape:
//
//	header = TableArrayHeader(table, kind)
//	len    = TableArrayLen(header, kind)
//	data   = TableArrayData(header, kind)
//
// Codegen uses these facts to keep the backing pointer in raw pointer form
// across loop-carried registers and fallback spills instead of treating it as
// a boxed integer. The fact is generic for all typed table arrays; it does not
// depend on a benchmark-specific source pattern.
func TableArrayDataPtrFactPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	facts := collectTableArrayDataPtrFacts(fn)
	if len(facts) == 0 {
		fn.TableArrayDataPtrs = nil
		return fn, nil
	}
	fn.TableArrayDataPtrs = facts
	return fn, nil
}

func collectTableArrayDataPtrFacts(fn *Function) map[int]TableArrayDataPtrFact {
	if fn == nil {
		return nil
	}
	lens := make(map[tableArrayDerivedKey]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpTableArrayLen || len(instr.Args) < 1 || instr.Args[0] == nil {
				continue
			}
			lens[tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}] = instr.ID
		}
	}

	facts := make(map[int]TableArrayDataPtrFact)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpTableArrayData || len(instr.Args) < 1 || instr.Args[0] == nil {
				continue
			}
			header := instr.Args[0].Def
			if header == nil || header.Op != OpTableArrayHeader || len(header.Args) < 1 || header.Args[0] == nil {
				continue
			}
			if header.Aux != instr.Aux || !tableArrayLowerableKind(instr.Aux) {
				continue
			}
			facts[instr.ID] = TableArrayDataPtrFact{
				TableID:  header.Args[0].ID,
				HeaderID: header.ID,
				LenID:    lens[tableArrayDerivedKey{headerID: header.ID, kind: instr.Aux}],
				Kind:     instr.Aux,
			}
			functionRemarks(fn).Add("TableArrayDataPtrFact", "changed", block.ID, instr.ID, instr.Op,
				"recorded guard-backed raw table-array data pointer")
		}
	}
	return facts
}

func (f *Function) tableArrayDataPtrFact(valueID int) (TableArrayDataPtrFact, bool) {
	if f == nil || f.TableArrayDataPtrs == nil {
		return TableArrayDataPtrFact{}, false
	}
	fact, ok := f.TableArrayDataPtrs[valueID]
	return fact, ok
}
