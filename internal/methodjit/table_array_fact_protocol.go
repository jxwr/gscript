package methodjit

// tableArrayFactSet owns the block-local protocol for typed table-array facts.
//
// Invariant: a complete fact for (table, kind) is visible only after a matching
// TableArrayHeader, TableArrayLen, and TableArrayData have all been observed
// without an intervening structural mutation of that table or a call. Checked
// OpTableArrayStore preserves the protocol; OpSetTable/OpAppend/OpSetList do
// not unless they have already been lowered to OpTableArrayStore.
type tableArrayFactSet struct {
	headersByTable map[tableArrayHeaderKey]*tableArrayHeaderFact
	headersByID    map[int]*tableArrayHeaderFact
	lens           map[tableArrayDerivedKey]*Value
	datas          map[tableArrayDerivedKey]*Value
}

type tableArrayHeaderFact struct {
	table  *Value
	header *Value
	kind   int64
}

type tableArrayCompleteFact struct {
	header *Value
	data   *Value
	len    *Value
	kind   int64
}

func newTableArrayFactSet() tableArrayFactSet {
	return tableArrayFactSet{
		headersByTable: make(map[tableArrayHeaderKey]*tableArrayHeaderFact),
		headersByID:    make(map[int]*tableArrayHeaderFact),
		lens:           make(map[tableArrayDerivedKey]*Value),
		datas:          make(map[tableArrayDerivedKey]*Value),
	}
}

func (s *tableArrayFactSet) Reset() {
	*s = newTableArrayFactSet()
}

func (s *tableArrayFactSet) Empty() bool {
	return len(s.headersByTable) == 0 && len(s.headersByID) == 0 && len(s.lens) == 0 && len(s.datas) == 0
}

func (s *tableArrayFactSet) LookupHeader(instr *Instr) *Value {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return nil
	}
	fact := s.headersByTable[tableArrayHeaderKey{objID: instr.Args[0].ID, kind: instr.Aux}]
	if fact == nil {
		return nil
	}
	return fact.header
}

func (s *tableArrayFactSet) RecordHeader(instr *Instr) {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return
	}
	tableKey := tableArrayHeaderKey{objID: instr.Args[0].ID, kind: instr.Aux}
	if _, exists := s.headersByTable[tableKey]; exists {
		return
	}
	fact := &tableArrayHeaderFact{
		table:  instr.Args[0],
		header: instr.Value(),
		kind:   instr.Aux,
	}
	s.headersByTable[tableKey] = fact
	s.headersByID[instr.ID] = fact
}

func (s *tableArrayFactSet) LookupLen(instr *Instr) *Value {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return nil
	}
	return s.lens[tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}]
}

func (s *tableArrayFactSet) RecordLen(instr *Instr) {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return
	}
	key := tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}
	if _, exists := s.lens[key]; exists {
		return
	}
	s.lens[key] = instr.Value()
}

func (s *tableArrayFactSet) LookupData(instr *Instr) *Value {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return nil
	}
	return s.datas[tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}]
}

func (s *tableArrayFactSet) RecordData(instr *Instr) {
	if instr == nil || len(instr.Args) < 1 || instr.Args[0] == nil {
		return
	}
	key := tableArrayDerivedKey{headerID: instr.Args[0].ID, kind: instr.Aux}
	if _, exists := s.datas[key]; exists {
		return
	}
	s.datas[key] = instr.Value()
}

func (s *tableArrayFactSet) Complete(tableID int, kind int64) (tableArrayCompleteFact, bool) {
	headerFact := s.headersByTable[tableArrayHeaderKey{objID: tableID, kind: kind}]
	if headerFact == nil || headerFact.header == nil {
		return tableArrayCompleteFact{}, false
	}
	key := tableArrayDerivedKey{headerID: headerFact.header.ID, kind: kind}
	lenVal := s.lens[key]
	dataVal := s.datas[key]
	if lenVal == nil || dataVal == nil {
		return tableArrayCompleteFact{}, false
	}
	return tableArrayCompleteFact{
		header: headerFact.header,
		data:   dataVal,
		len:    lenVal,
		kind:   kind,
	}, true
}

func (s *tableArrayFactSet) InvalidateTable(tableID int) bool {
	var killedHeaders []int
	for key, fact := range s.headersByTable {
		if key.objID != tableID {
			continue
		}
		delete(s.headersByTable, key)
		if fact != nil && fact.header != nil {
			killedHeaders = append(killedHeaders, fact.header.ID)
		}
	}
	if len(killedHeaders) == 0 {
		return false
	}
	for _, headerID := range killedHeaders {
		delete(s.headersByID, headerID)
		for key := range s.lens {
			if key.headerID == headerID {
				delete(s.lens, key)
			}
		}
		for key := range s.datas {
			if key.headerID == headerID {
				delete(s.datas, key)
			}
		}
	}
	return true
}
