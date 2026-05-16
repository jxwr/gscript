package methodjit

import "fmt"

// ClosureUpvalueScalarPass promotes a loop-local inlined closure upvalue read to
// a header phi when the same loop writes the cell exactly through SetUpval. The
// store remains in the loop, so the closed-over cell stays observable; this pass
// only removes the redundant reload at the top of the next iteration.
func ClosureUpvalueScalarPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return fn, nil
	}
	for headerID := range li.loopHeaders {
		header := findBlockByID(fn, headerID)
		if header == nil || len(header.Preds) != 2 {
			continue
		}
		body := closureScalarSingleBackedge(li, header)
		if body == nil {
			continue
		}
		promoteClosureUpvalueInLoop(fn, header, body)
	}
	if errs := Validate(fn); len(errs) > 0 {
		return fn, fmt.Errorf("closure upvalue scalar promotion produced invalid IR: %v", errs)
	}
	return fn, nil
}

func closureScalarSingleBackedge(li *loopInfo, header *Block) *Block {
	bodyBlocks := li.headerBlocks[header.ID]
	if bodyBlocks == nil {
		return nil
	}
	var body *Block
	for _, pred := range header.Preds {
		if pred != nil && bodyBlocks[pred.ID] {
			if body != nil {
				return nil
			}
			body = pred
		}
	}
	return body
}

func promoteClosureUpvalueInLoop(fn *Function, header, body *Block) {
	for idx, set := range body.Instrs {
		if set == nil || set.Op != OpSetUpval || len(set.Args) < 2 || set.Args[1] == nil {
			continue
		}
		key := upvalueKey{closureID: set.Args[1].ID, upval: set.Aux}
		get := firstPriorGetUpval(body.Instrs[:idx], key)
		if get == nil || (get.Type != TypeInt && get.Type != TypeFloat) || get.Type != set.Args[0].Def.Type {
			continue
		}
		init := &Instr{
			ID:    fn.newValueID(),
			Op:    OpGetUpval,
			Type:  get.Type,
			Args:  []*Value{set.Args[1]},
			Aux:   set.Aux,
			Block: header.Preds[0],
		}
		init.copySourceFrom(get)
		init.Block = closureScalarPreheader(header, body)
		if init.Block == nil {
			continue
		}
		insertBeforeTerminatorClosureScalar(init.Block, init)
		phi := &Instr{
			ID:    fn.newValueID(),
			Op:    OpPhi,
			Type:  get.Type,
			Block: header,
		}
		for _, pred := range header.Preds {
			if pred == body {
				phi.Args = append(phi.Args, set.Args[0])
			} else {
				phi.Args = append(phi.Args, init.Value())
			}
		}
		insertHeaderPhi(header, phi)
		replaceAllUses(fn, get.ID, phi)
		get.Op = OpNop
		get.Args = nil
		get.Type = TypeUnknown
		if inlinedClosureValueDoesNotEscape(fn, set.Args[1]) {
			set.Op = OpNop
			set.Args = nil
			set.Type = TypeUnknown
			functionRemarks(fn).Add("ClosureUpvalueScalar", "changed", body.ID, set.ID, OpSetUpval,
				"removed dead inlined closure upvalue store for non-escaping closure")
		}
		functionRemarks(fn).Add("ClosureUpvalueScalar", "changed", body.ID, get.ID, OpGetUpval,
			"promoted loop-carried closure upvalue load to header phi")
		return
	}
}

func closureScalarPreheader(header, body *Block) *Block {
	for _, pred := range header.Preds {
		if pred != body {
			return pred
		}
	}
	return nil
}

func firstPriorGetUpval(instrs []*Instr, key upvalueKey) *Instr {
	for _, instr := range instrs {
		if instr == nil || instr.Op != OpGetUpval || len(instr.Args) < 1 || instr.Args[0] == nil {
			continue
		}
		if instr.Args[0].ID == key.closureID && instr.Aux == key.upval {
			return instr
		}
	}
	return nil
}

func insertBeforeTerminatorClosureScalar(block *Block, instr *Instr) {
	if block == nil || instr == nil {
		return
	}
	n := len(block.Instrs)
	if n == 0 || !block.Instrs[n-1].Op.IsTerminator() {
		block.Instrs = append(block.Instrs, instr)
		return
	}
	block.Instrs = append(block.Instrs, nil)
	copy(block.Instrs[n:], block.Instrs[n-1:])
	block.Instrs[n-1] = instr
}

func insertHeaderPhi(header *Block, phi *Instr) {
	if header == nil || phi == nil {
		return
	}
	idx := 0
	for idx < len(header.Instrs) && header.Instrs[idx].Op == OpPhi {
		idx++
	}
	header.Instrs = append(header.Instrs, nil)
	copy(header.Instrs[idx+1:], header.Instrs[idx:])
	header.Instrs[idx] = phi
}

func inlinedClosureValueDoesNotEscape(fn *Function, closure *Value) bool {
	if fn == nil || closure == nil || closure.Def == nil {
		return false
	}
	roots := map[int]bool{closure.ID: true}
	if closure.Def.Op == OpGuardCalleeProto && len(closure.Def.Args) > 0 && closure.Def.Args[0] != nil {
		roots[closure.Def.Args[0].ID] = true
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			for argIdx, arg := range instr.Args {
				if arg == nil || !roots[arg.ID] {
					continue
				}
				if closureUseKeepsValueLocal(instr, argIdx, roots) {
					continue
				}
				return false
			}
		}
	}
	return true
}

func closureUseKeepsValueLocal(instr *Instr, argIdx int, roots map[int]bool) bool {
	switch instr.Op {
	case OpGuardCalleeProto:
		return argIdx == 0
	case OpGetUpval:
		return argIdx == 0
	case OpSetUpval:
		return argIdx == 1
	case OpNop:
		return true
	default:
		return false
	}
}
