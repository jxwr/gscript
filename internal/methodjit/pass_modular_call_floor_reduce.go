package methodjit

// ModularCallFloorReducePass bounds wide floor-call results before they enter
// an additive expression that is immediately reduced by a positive modulo. The
// rewrite is algebraic, not benchmark-specific:
//
//	(a + floor(f(...))) % m  =>  (a + (floor(f(...)) % m)) % m
//
// for positive integer m. Keeping the call-floor leaf range-bounded lets the
// surrounding Tier 2 loop stay native without depending on a narrow speculative
// call-result range guard.
func ModularCallFloorReducePass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpModInt || len(instr.Args) < 2 {
				continue
			}
			divisor := instr.Args[1]
			if !positiveIntModDivisorValue(divisor) {
				continue
			}
			changed := reduceCallFloorLeavesInAdditiveTree(fn, block, instr.Args[0], divisor)
			if changed {
				functionRemarks(fn).Add("ModularCallFloorReduce", "changed", block.ID, instr.ID, instr.Op,
					"reduced wide floor-call leaves before positive modulo")
			}
		}
	}
	return fn, nil
}

func reduceCallFloorLeavesInAdditiveTree(fn *Function, block *Block, root, divisor *Value) bool {
	if fn == nil || block == nil || root == nil || root.Def == nil || divisor == nil {
		return false
	}
	seen := make(map[int]bool)
	var visit func(v *Value) bool
	visit = func(v *Value) bool {
		if v == nil || v.Def == nil || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
		instr := v.Def
		if instr.Op != OpAddInt {
			return false
		}
		changed := false
		for i, arg := range instr.Args {
			if arg == nil || arg.Def == nil {
				continue
			}
			if isModuloReducibleCallFloor(arg.Def) {
				if reduced, ok := insertModuloReductionAfterProducer(fn, block, arg.Def, divisor); ok {
					instr.Args[i] = reduced.Value()
					changed = true
				}
				continue
			}
			if arg.Def.Op == OpAddInt && visit(arg) {
				changed = true
			}
		}
		return changed
	}
	return visit(root)
}

func isModuloReducibleCallFloor(instr *Instr) bool {
	if instr == nil || instr.Type != TypeInt {
		return false
	}
	switch instr.Op {
	case OpCallFloor, OpFieldCallFloor:
		return true
	default:
		return false
	}
}

func insertModuloReductionAfterProducer(fn *Function, block *Block, producer *Instr, divisor *Value) (*Instr, bool) {
	if fn == nil || block == nil || producer == nil || divisor == nil {
		return nil, false
	}
	for i, instr := range block.Instrs {
		if instr != producer {
			continue
		}
		reduced := &Instr{
			ID:          fn.newValueID(),
			Op:          OpModInt,
			Type:        TypeInt,
			Args:        []*Value{producer.Value(), divisor},
			Block:       block,
			HasSource:   producer.HasSource,
			SourceProto: producer.SourceProto,
			SourcePC:    producer.SourcePC,
			SourceLine:  producer.SourceLine,
		}
		block.Instrs = append(block.Instrs[:i+1], append([]*Instr{reduced}, block.Instrs[i+1:]...)...)
		return reduced, true
	}
	return nil, false
}

func positiveIntModDivisorValue(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	n, ok := constIntFromValue(v)
	return ok && n > 0
}
