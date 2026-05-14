package methodjit

import "fmt"

// FieldPolyLenPhiPass replaces a guarded polymorphic field length with SSA
// constants/phis when existing shape-split control flow already proves the
// receiver shape on every incoming edge.
func FieldPolyLenPhiPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 || len(fn.FieldPolyShapeFacts) == 0 {
		return fn, nil
	}
	changed := false
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpFieldPolyLen || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			lens := fieldPolyLenExactLens(fn, instr)
			if len(lens) < 2 {
				continue
			}
			ctx := &fieldPolyLenPhiContext{
				fn:      fn,
				tableID: instr.Args[0].ID,
				lens:    lens,
				memo:    make(map[int]*Value),
				active:  make(map[int]bool),
			}
			repl := ctx.valueAtEndOfBlock(block)
			if repl == nil {
				functionRemarks(fn).Add("FieldPolyLenPhi", "missed", block.ID, instr.ID, instr.Op,
					"shape-split predecessors did not prove exact field length")
				continue
			}
			replaceValueUses(fn, instr.ID, repl, repl.ID)
			instr.Op = OpNop
			instr.Type = TypeUnknown
			instr.Args = nil
			instr.Aux = 0
			instr.Aux2 = 0
			changed = true
			functionRemarks(fn).Add("FieldPolyLenPhi", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("replaced polymorphic field length with shape-controlled value v%d", repl.ID))
		}
	}
	if changed {
		relinkValueDefs(fn)
	}
	return fn, nil
}

type fieldPolyLenPhiContext struct {
	fn      *Function
	tableID int
	lens    map[uint32]int64
	memo    map[int]*Value
	active  map[int]bool
}

func fieldPolyLenExactLens(fn *Function, instr *Instr) map[uint32]int64 {
	out := make(map[uint32]int64)
	name := fieldNameFromAux(fn, instr.Aux)
	if name == "" {
		return nil
	}
	for _, c := range fn.FieldPolyShapeFacts[instr.ID] {
		r, ok := c.ReceiverFact.FieldLenRanges[name]
		if c.ShapeID == 0 || !ok || !r.known || r.min != r.max {
			return nil
		}
		out[c.ShapeID] = r.min
	}
	return out
}

func (ctx *fieldPolyLenPhiContext) valueAtEndOfBlock(block *Block) *Value {
	if ctx == nil || block == nil {
		return nil
	}
	if v := ctx.memo[block.ID]; v != nil {
		return v
	}
	if ctx.active[block.ID] {
		return nil
	}
	ctx.active[block.ID] = true
	defer delete(ctx.active, block.ID)

	if shape, ok := fieldPolyLenBlockExactShape(block, ctx.tableID); ok {
		if ln, ok := ctx.lens[shape]; ok {
			v := ctx.constInBlock(block, ln)
			ctx.memo[block.ID] = v
			return v
		}
	}
	if fieldPolyLenBlockMutatesTable(block, ctx.tableID) {
		return nil
	}
	switch len(block.Preds) {
	case 0:
		return nil
	case 1:
		v := ctx.valueAtEndOfBlock(block.Preds[0])
		ctx.memo[block.ID] = v
		return v
	default:
		args := make([]*Value, len(block.Preds))
		var constVal *int64
		allSameConst := true
		for i, pred := range block.Preds {
			v := ctx.valueAtEndOfBlock(pred)
			if v == nil {
				return nil
			}
			args[i] = v
			if v.Def == nil || v.Def.Op != OpConstInt {
				allSameConst = false
				continue
			}
			if constVal == nil {
				x := v.Def.Aux
				constVal = &x
			} else if *constVal != v.Def.Aux {
				allSameConst = false
			}
		}
		if allSameConst && constVal != nil {
			v := ctx.constInBlock(block, *constVal)
			ctx.memo[block.ID] = v
			return v
		}
		phi := &Instr{ID: ctx.fn.newValueID(), Op: OpPhi, Type: TypeInt, Args: args, Block: block}
		insertAtTopAfterPhis(block, phi)
		v := phi.Value()
		ctx.memo[block.ID] = v
		return v
	}
}

func (ctx *fieldPolyLenPhiContext) constInBlock(block *Block, n int64) *Value {
	c := &Instr{ID: ctx.fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: n, Block: block}
	insertBeforeTerminator(block, c)
	return c.Value()
}

func fieldPolyLenBlockExactShape(block *Block, tableID int) (uint32, bool) {
	var shape uint32
	for _, instr := range block.Instrs {
		if instr == nil || len(instr.Args) == 0 || instr.Args[0] == nil || instr.Args[0].ID != tableID {
			continue
		}
		var cur uint32
		switch instr.Op {
		case OpGuardFieldCalleeProto:
			cur = uint32(instr.Aux2 >> 32)
		case OpFieldSvals:
			cur = uint32(instr.Aux)
		default:
			continue
		}
		if cur == 0 {
			continue
		}
		if shape != 0 && shape != cur {
			return 0, false
		}
		shape = cur
	}
	return shape, shape != 0
}

func fieldPolyLenBlockMutatesTable(block *Block, tableID int) bool {
	for _, instr := range block.Instrs {
		if mutated, ok := fieldSvalsMutationTableID(instr); ok && mutated == tableID {
			return true
		}
	}
	return false
}
