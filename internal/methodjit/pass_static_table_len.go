package methodjit

// StaticTableLenPass folds #t for small immutable table literals represented
// as NewTable followed by one SetList initializer. It is deliberately about
// object lifetime and mutation, not source literals or benchmark-specific
// names: if a freshly allocated table never escapes and is never modified after
// SetList, its array length is the SetList arity.
func StaticTableLenPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	candidates := collectStaticTableLenCandidates(fn)
	changed := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpLen || len(instr.Args) != 1 {
				continue
			}
			arg := instr.Args[0]
			if arg == nil {
				continue
			}
			length, ok := candidates[arg.ID]
			if !ok {
				continue
			}
			instr.Op = OpConstInt
			instr.Type = TypeInt
			instr.Args = nil
			instr.Aux = int64(length)
			instr.Aux2 = 0
			functionRemarks(fn).Add("StaticTableLen", "changed", block.ID, instr.ID, OpLen,
				"folded length of non-escaping SetList-initialized table")
			changed = true
		}
	}
	if !changed {
		functionRemarks(fn).Add("StaticTableLen", "missed", 0, 0, OpLen,
			"no non-escaping SetList-initialized table lengths found")
	}
	return fn, nil
}

type staticTableLenState struct {
	length  int
	setList *Instr
	ok      bool
}

func collectStaticTableLenCandidates(fn *Function) map[int]int {
	states := make(map[int]*staticTableLenState)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewTable {
				states[instr.ID] = &staticTableLenState{length: -1, ok: true}
			}
		}
	}
	if len(states) == 0 {
		return nil
	}

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for argIdx, arg := range instr.Args {
				if arg == nil {
					continue
				}
				s := states[arg.ID]
				if s == nil || !s.ok {
					continue
				}
				if !staticTableLenUseAllowed(instr, argIdx, s) {
					s.ok = false
				}
			}
		}
	}

	out := make(map[int]int)
	for id, s := range states {
		if s.ok && s.setList != nil && s.length >= 0 {
			out[id] = s.length
		}
	}
	return out
}

func staticTableLenUseAllowed(instr *Instr, argIdx int, s *staticTableLenState) bool {
	switch instr.Op {
	case OpSetList:
		if argIdx != 0 || s.setList != nil || len(instr.Args) == 0 || instr.Aux != 1 {
			return false
		}
		s.setList = instr
		s.length = len(instr.Args) - 1
		return true
	case OpLen:
		return argIdx == 0
	case OpGetTable, OpTableArrayHeader:
		return argIdx == 0
	default:
		return false
	}
}
