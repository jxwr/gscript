package methodjit

import "fmt"

// FixedTableConstructorLoweringPass combines surviving fixed-field table
// constructors into one value-producing op after escape analysis has had a
// chance to scalar-replace the expanded NewTable+SetField form.
func FixedTableConstructorLoweringPass(fn *Function) (*Function, error) {
	if fn == nil || fn.Proto == nil || len(fn.FixedTableConstructors) == 0 {
		return fn, nil
	}
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if instr == nil || instr.Op != OpNewTable {
				continue
			}
			fact, ok := fn.FixedTableConstructors[instr.ID]
			if !ok {
				continue
			}
			if lowerFixedTableConstructor2(fn, block, i, instr, fact) ||
				lowerFixedTableConstructorN(fn, block, i, instr, fact) {
				functionRemarks(fn).Add("FixedTableConstructorLowering", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("lowered fixed table constructor fields=%v", fact.FieldNames))
			}
		}
	}
	return fn, nil
}

func lowerFixedTableConstructor2(fn *Function, block *Block, idx int, alloc *Instr, fact FixedTableConstructorFact) bool {
	if fn == nil || fn.Proto == nil || block == nil || alloc == nil {
		return false
	}
	if fact.Ctor2Index < 0 || fact.Ctor2Index >= len(fn.Proto.TableCtors2) {
		return false
	}
	ctor := fn.Proto.TableCtors2[fact.Ctor2Index]
	if ctor.Runtime.Key1 == ctor.Runtime.Key2 {
		return false
	}
	set1, pos, ok := nextFixedCtorSetField(block.Instrs, idx+1, alloc.ID, int64(ctor.Key1Const))
	if !ok {
		return false
	}
	set2, _, ok := nextFixedCtorSetField(block.Instrs, pos, alloc.ID, int64(ctor.Key2Const))
	if !ok {
		return false
	}
	if len(set1.Args) < 2 || set1.Args[1] == nil || len(set2.Args) < 2 || set2.Args[1] == nil {
		return false
	}

	alloc.Op = OpNewFixedTable
	alloc.Type = TypeTable
	alloc.Args = []*Value{set1.Args[1], set2.Args[1]}
	alloc.Aux = int64(fact.Ctor2Index)
	alloc.Aux2 = 2
	nopInstruction(set1)
	nopInstruction(set2)
	return true
}

func lowerFixedTableConstructorN(fn *Function, block *Block, idx int, alloc *Instr, fact FixedTableConstructorFact) bool {
	if fn == nil || fn.Proto == nil || block == nil || alloc == nil {
		return false
	}
	if fact.CtorNIndex < 0 || fact.CtorNIndex >= len(fn.Proto.TableCtorsN) {
		return false
	}
	ctor := fn.Proto.TableCtorsN[fact.CtorNIndex]
	if len(ctor.KeyConsts) == 0 || len(ctor.KeyConsts) != len(ctor.Runtime.Keys) {
		return false
	}
	values := make([]*Value, 0, len(ctor.KeyConsts))
	pos := idx + 1
	for _, keyConst := range ctor.KeyConsts {
		set, next, ok := nextFixedCtorSetField(block.Instrs, pos, alloc.ID, int64(keyConst))
		if !ok || len(set.Args) < 2 || set.Args[1] == nil {
			return false
		}
		values = append(values, set.Args[1])
		pos = next
	}

	alloc.Op = OpNewFixedTable
	alloc.Type = TypeTable
	alloc.Args = values
	alloc.Aux = int64(fact.CtorNIndex)
	alloc.Aux2 = int64(len(values))
	for i := idx + 1; i < pos; i++ {
		instr := block.Instrs[i]
		if instr != nil && instr.Op == OpSetField && len(instr.Args) > 0 && instr.Args[0] != nil && instr.Args[0].ID == alloc.ID {
			nopInstruction(instr)
		}
	}
	return true
}

func nextFixedCtorSetField(instrs []*Instr, start, allocID int, constIdx int64) (*Instr, int, bool) {
	for i := start; i < len(instrs); i++ {
		instr := instrs[i]
		if instr == nil || instr.Op == OpNop {
			continue
		}
		if instr.Op != OpSetField || instr.Aux != constIdx || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[0].ID != allocID {
			return nil, i, false
		}
		return instr, i + 1, true
	}
	return nil, len(instrs), false
}

func nopInstruction(instr *Instr) {
	instr.Op = OpNop
	instr.Type = TypeUnknown
	instr.Args = nil
	instr.Aux = 0
	instr.Aux2 = 0
}
