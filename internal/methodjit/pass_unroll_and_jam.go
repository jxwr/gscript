// pass_unroll_and_jam.go implements a conservative 2-way unroll for numeric
// float reductions.
//
// It targets the canonical innermost-loop pattern:
//   acc = Phi(0.0, new_acc)
//   iv  = Phi(init, iv + step)
//   new_acc = acc + Expr(iv)
//
// The transform clones the side-effect-free body once for iv+step, tightens the
// hot loop bound to full pairs only, and emits a scalar tail for odd trip
// counts. It also remaps companion float recurrences (for example a sign flip)
// through the cloned iteration. This keeps the original left-to-right reduction
// order while reducing hot back-edge traffic after LICM has moved invariant
// table/matrix facts out of the body.

package methodjit

import (
	"fmt"
	"sort"
)

// UnrollAndJamPass keeps the historical pass name, but deliberately implements
// a lower-risk serial unroll rather than split-accumulator unroll-and-jam.
func UnrollAndJamPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}

	// Call graph inlining can expose multiple independent helper loops in one
	// caller. Recompute loop info after each rewrite, but unroll each original
	// header at most once so the pass does not keep expanding the same loop.
	unrolledHeaders := make(map[int]bool)
	for {
		li := computeLoopInfo(fn)
		if !li.hasLoops() {
			break
		}

		headerIDs := make([]int, 0, len(li.loopHeaders))
		for headerID := range li.loopHeaders {
			headerIDs = append(headerIDs, headerID)
		}
		sort.Ints(headerIDs)

		changed := false
		for _, headerID := range headerIDs {
			if unrolledHeaders[headerID] {
				continue
			}
			header := findBlock(fn, headerID)
			if header == nil {
				continue
			}
			cand := detectFloatReductionLoop(fn, li, header)
			if cand == nil {
				continue
			}
			if err := unrollFloatReductionLoop2(fn, cand); err != nil {
				return nil, err
			}
			unrolledHeaders[headerID] = true
			changed = true
			functionRemarks(fn).Add("UnrollAndJam", "changed", cand.header.ID, cand.updateInstr.ID, cand.updateInstr.Op,
				"2-way unroll with scalar tail for float reduction loop")
			break
		}
		if !changed {
			break
		}
	}
	return fn, nil
}

type floatReductionCandidate struct {
	header      *Block
	bodyBlock   *Block
	accPhi      *Instr
	ivPhi       *Instr
	recurrences []*Instr
	stepInstr   *Instr
	stepValue   *Value
	step        int64
	limitValue  *Value
	outsidePred *Block
	exitBlock   *Block
	updateInstr *Instr
}

func detectFloatReductionLoop(fn *Function, li *loopInfo, header *Block) *floatReductionCandidate {
	bodyBlocks := li.headerBlocks[header.ID]
	if bodyBlocks == nil {
		return nil
	}
	inside, outside := loopPreds(li, header)
	if len(inside) != 1 || len(outside) != 1 || len(header.Succs) != 2 {
		return nil
	}

	var ivPhi *Instr
	var floatPhis []*Instr
	phiCount, intPhiCount := 0, 0
	for _, instr := range header.Instrs {
		if instr.Op != OpPhi {
			continue
		}
		phiCount++
		switch instr.Type {
		case TypeFloat:
			floatPhis = append(floatPhis, instr)
		case TypeInt:
			ivPhi = instr
			intPhiCount++
		default:
			return nil
		}
	}
	if phiCount < 2 || len(floatPhis) == 0 || intPhiCount != 1 || ivPhi == nil {
		return nil
	}

	var accPhi, updateInstr *Instr
	var bodyBlock *Block
	for _, phi := range floatPhis {
		update := findAccumUpdate(phi)
		if update == nil {
			continue
		}
		if update.Block == nil || update.Block != inside[0] || !bodyBlocks[update.Block.ID] {
			continue
		}
		if bodyBlock != nil && bodyBlock != update.Block {
			continue
		}
		accPhi = phi
		updateInstr = update
		bodyBlock = update.Block
		break
	}
	if accPhi == nil || updateInstr == nil || bodyBlock == nil {
		return nil
	}

	if blockStartsWithPhi(header.Succs[1]) || !valueUsesLimitedToBlocks(fn, accPhi.ID, bodyBlock, header.Succs[1]) {
		return nil
	}
	if len(bodyBlock.Preds) != 1 || bodyBlock.Preds[0] != header || len(bodyBlock.Succs) != 1 || bodyBlock.Succs[0] != header {
		return nil
	}
	if !headerBodyBranchTargets(header, bodyBlock) {
		return nil
	}

	stepInstr, stepValue, stepVal := findIntIVStep(fn, li, ivPhi)
	if stepInstr == nil || stepValue == nil || stepVal <= 0 {
		return nil
	}
	limitValue := findLoopLimit(header, stepInstr)
	if limitValue == nil {
		return nil
	}
	if !bodyIsSafeForUnroll(bodyBlock) {
		return nil
	}
	recurrences := collectFloatRecurrences(fn, header, bodyBlock, inside[0], accPhi)
	if recurrences == nil {
		return nil
	}

	return &floatReductionCandidate{
		header:      header,
		bodyBlock:   bodyBlock,
		accPhi:      accPhi,
		ivPhi:       ivPhi,
		recurrences: recurrences,
		stepInstr:   stepInstr,
		stepValue:   stepValue,
		step:        stepVal,
		limitValue:  limitValue,
		outsidePred: outside[0],
		exitBlock:   header.Succs[1],
		updateInstr: updateInstr,
	}
}

func findAccumUpdate(phi *Instr) *Instr {
	for _, arg := range phi.Args {
		if arg == nil || arg.Def == nil || len(arg.Def.Args) != 2 {
			continue
		}
		if arg.Def.Op != OpAddFloat && arg.Def.Op != OpSubFloat {
			continue
		}
		if arg.Def.Args[0].ID == phi.ID || arg.Def.Args[1].ID == phi.ID {
			return arg.Def
		}
	}
	return nil
}

func collectFloatRecurrences(fn *Function, header, body, insidePred *Block, accPhi *Instr) []*Instr {
	recurrences := make([]*Instr, 0, 2)
	for _, instr := range header.Instrs {
		if instr.Op != OpPhi || instr.Type != TypeFloat || instr == accPhi {
			continue
		}
		arg := phiArgForPred(instr, header, insidePred)
		if arg == nil || arg.Def == nil || arg.Def.Block != body {
			return nil
		}
		if !valueUsesLimitedToBlocks(fn, instr.ID, body, header) {
			return nil
		}
		recurrences = append(recurrences, instr)
	}
	return recurrences
}

func phiArgForPred(phi *Instr, header, pred *Block) *Value {
	idx := predIndex(header, pred)
	if idx < 0 || idx >= len(phi.Args) {
		return nil
	}
	return phi.Args[idx]
}

func findIntIVStep(fn *Function, li *loopInfo, ivPhi *Instr) (*Instr, *Value, int64) {
	for _, arg := range ivPhi.Args {
		if arg == nil || arg.Def == nil || arg.Def.Op != OpAddInt || len(arg.Def.Args) != 2 {
			continue
		}
		var constArg *Instr
		var constVal *Value
		for _, a := range arg.Def.Args {
			if a != nil && a.Def != nil && a.Def.Op == OpConstInt {
				constArg = a.Def
				constVal = a
			}
		}
		if constArg == nil || arg.Def.Block == nil || !li.loopBlocks[arg.Def.Block.ID] {
			continue
		}
		return arg.Def, constVal, constArg.Aux
	}
	return nil, nil, 0
}

func findBlock(fn *Function, id int) *Block {
	for _, b := range fn.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func blockStartsWithPhi(block *Block) bool {
	return block != nil && len(block.Instrs) > 0 && block.Instrs[0].Op == OpPhi
}

func valueUsesLimitedToBlocks(fn *Function, valueID int, allowed ...*Block) bool {
	allowedSet := make(map[*Block]bool, len(allowed))
	for _, block := range allowed {
		if block != nil {
			allowedSet[block] = true
		}
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				if arg != nil && arg.ID == valueID && !allowedSet[block] {
					return false
				}
			}
		}
	}
	return true
}

func unrollFloatReductionLoop2(fn *Function, cand *floatReductionCandidate) error {
	body, header, exit, preheader := cand.bodyBlock, cand.header, cand.exitBlock, cand.outsidePred
	if body == nil || header == nil || exit == nil || preheader == nil || len(body.Instrs) == 0 {
		return nil
	}
	term := body.Instrs[len(body.Instrs)-1]
	if term.Op != OpJump {
		return fmt.Errorf("unroll: body B%d terminator is %s, want Jump", body.ID, term.Op)
	}
	bodyPredIdx := predIndex(header, body)
	if bodyPredIdx < 0 {
		return fmt.Errorf("unroll: body B%d is not a predecessor of header B%d", body.ID, header.ID)
	}

	tailCheck := &Block{ID: nextBlockID(fn)}
	tailBody := &Block{ID: tailCheck.ID + 1}
	insertBlockAfter(fn, header, tailCheck)
	insertBlockAfter(fn, tailCheck, tailBody)

	pairLimit := &Instr{
		ID:    fn.newValueID(),
		Op:    OpSubInt,
		Type:  TypeInt,
		Args:  []*Value{cand.limitValue, cand.stepValue},
		Block: preheader,
	}
	insertBeforeTerminator(preheader, pairLimit)

	headerCmp := header.Instrs[len(header.Instrs)-2]
	if headerCmp.Op != OpLeInt || len(headerCmp.Args) != 2 || headerCmp.Args[0].ID != cand.stepInstr.ID {
		return fmt.Errorf("unroll: header B%d compare shape changed", header.ID)
	}
	headerCmp.Args[1] = pairLimit.Value()

	originalBody := append([]*Instr(nil), body.Instrs[:len(body.Instrs)-1]...)
	k2 := &Instr{
		ID:    fn.newValueID(),
		Op:    OpAddInt,
		Type:  TypeInt,
		Args:  []*Value{cand.stepInstr.Value(), cand.stepValue},
		Block: body,
	}
	body.Instrs = append(body.Instrs[:len(body.Instrs)-1], k2)

	remap := map[int]*Value{
		cand.stepInstr.ID:   k2.Value(),
		cand.accPhi.ID:      cand.updateInstr.Value(),
		cand.updateInstr.ID: cand.updateInstr.Value(),
	}
	seedRecurrenceRemap(bodyPredIdx, remap, cand.recurrences)
	cloneUpdate, err := cloneBodyInstructions(fn, body, originalBody, remap, cand.updateInstr.ID)
	if err != nil {
		return err
	}
	body.Instrs = append(body.Instrs, cloneTerminator(fn, body, OpJump, nil, header, nil, term))
	cand.accPhi.Args[bodyPredIdx] = cloneUpdate.Value()
	cand.ivPhi.Args[bodyPredIdx] = k2.Value()
	updateRecurrenceBackedges(bodyPredIdx, remap, cand.recurrences)

	tailCond := &Instr{
		ID:    fn.newValueID(),
		Op:    OpLeInt,
		Type:  TypeBool,
		Args:  []*Value{cand.stepInstr.Value(), cand.limitValue},
		Block: tailCheck,
	}
	tailBranch := cloneTerminator(fn, tailCheck, OpBranch, []*Value{tailCond.Value()}, tailBody, exit, nil)
	tailCheck.Instrs = []*Instr{tailCond, tailBranch}
	tailCheck.Preds = []*Block{header}
	tailCheck.Succs = []*Block{tailBody, exit}

	tailUpdate, err := cloneBodyInstructions(fn, tailBody, originalBody, map[int]*Value{}, cand.updateInstr.ID)
	if err != nil {
		return err
	}
	tailBody.Instrs = append(tailBody.Instrs, cloneTerminator(fn, tailBody, OpJump, nil, exit, nil, term))
	tailBody.Preds = []*Block{tailCheck}
	tailBody.Succs = []*Block{exit}

	header.Succs[1] = tailCheck
	headerTerm := header.Instrs[len(header.Instrs)-1]
	headerTerm.Aux2 = int64(tailCheck.ID)
	replacePred(exit, header, tailCheck)
	exit.Preds = append(exit.Preds, tailBody)

	exitAcc := &Instr{
		ID:    fn.newValueID(),
		Op:    OpPhi,
		Type:  TypeFloat,
		Args:  []*Value{cand.accPhi.Value(), tailUpdate.Value()},
		Block: exit,
	}
	exit.Instrs = append([]*Instr{exitAcc}, exit.Instrs...)
	replaceValueUsesInBlock(exit, cand.accPhi.ID, exitAcc.Value(), 1)
	return nil
}

func seedRecurrenceRemap(predIdx int, remap map[int]*Value, recurrences []*Instr) {
	for _, phi := range recurrences {
		if phi == nil || predIdx < 0 || predIdx >= len(phi.Args) || phi.Args[predIdx] == nil {
			continue
		}
		remap[phi.ID] = phi.Args[predIdx]
	}
}

func updateRecurrenceBackedges(predIdx int, remap map[int]*Value, recurrences []*Instr) {
	for _, phi := range recurrences {
		if phi == nil || predIdx < 0 || predIdx >= len(phi.Args) || phi.Args[predIdx] == nil {
			continue
		}
		if repl := remap[phi.Args[predIdx].ID]; repl != nil {
			phi.Args[predIdx] = repl
		}
	}
}

func cloneBodyInstructions(fn *Function, block *Block, instrs []*Instr, remap map[int]*Value, updateID int) (*Instr, error) {
	var update *Instr
	for _, instr := range instrs {
		clone := cloneInstrWithRemap(fn, block, instr, remap)
		block.Instrs = append(block.Instrs, clone)
		remap[instr.ID] = clone.Value()
		if instr.ID == updateID {
			update = clone
		}
	}
	if update == nil {
		return nil, fmt.Errorf("unroll: cloned body B%d did not clone accumulator update v%d", block.ID, updateID)
	}
	return update, nil
}

func cloneInstrWithRemap(fn *Function, block *Block, instr *Instr, remap map[int]*Value) *Instr {
	args := make([]*Value, len(instr.Args))
	for i, arg := range instr.Args {
		if arg == nil {
			continue
		}
		if repl := remap[arg.ID]; repl != nil {
			args[i] = repl
		} else {
			args[i] = arg
		}
	}
	return &Instr{
		ID:         fn.newValueID(),
		Op:         instr.Op,
		Type:       instr.Type,
		Args:       args,
		Aux:        instr.Aux,
		Aux2:       instr.Aux2,
		Block:      block,
		HasSource:  instr.HasSource,
		SourcePC:   instr.SourcePC,
		SourceLine: instr.SourceLine,
	}
}

func cloneTerminator(fn *Function, block *Block, op Op, args []*Value, succ0, succ1 *Block, src *Instr) *Instr {
	instr := &Instr{ID: fn.newValueID(), Op: op, Type: TypeUnknown, Args: args, Block: block}
	if succ0 != nil {
		instr.Aux = int64(succ0.ID)
	}
	if succ1 != nil {
		instr.Aux2 = int64(succ1.ID)
	}
	if src != nil {
		instr.HasSource = src.HasSource
		instr.SourcePC = src.SourcePC
		instr.SourceLine = src.SourceLine
	}
	return instr
}

func insertBlockAfter(fn *Function, after *Block, inserted *Block) {
	out := make([]*Block, 0, len(fn.Blocks)+1)
	done := false
	for _, b := range fn.Blocks {
		out = append(out, b)
		if b == after {
			out = append(out, inserted)
			done = true
		}
	}
	if !done {
		out = append(out, inserted)
	}
	fn.Blocks = out
}

func predIndex(block, pred *Block) int {
	for i, p := range block.Preds {
		if p == pred {
			return i
		}
	}
	return -1
}

func replacePred(block, oldPred, newPred *Block) {
	for i, pred := range block.Preds {
		if pred == oldPred {
			block.Preds[i] = newPred
			return
		}
	}
}

func replaceValueUsesInBlock(block *Block, oldID int, repl *Value, startInstr int) {
	for i, instr := range block.Instrs {
		if i < startInstr {
			continue
		}
		for argIdx, arg := range instr.Args {
			if arg != nil && arg.ID == oldID {
				instr.Args[argIdx] = repl
			}
		}
	}
}

func headerBodyBranchTargets(header, body *Block) bool {
	if header == nil || body == nil || len(header.Succs) != 2 || len(header.Instrs) == 0 {
		return false
	}
	term := header.Instrs[len(header.Instrs)-1]
	return term.Op == OpBranch && header.Succs[0] == body
}

func findLoopLimit(header *Block, stepInstr *Instr) *Value {
	if header == nil || stepInstr == nil || len(header.Instrs) == 0 {
		return nil
	}
	term := header.Instrs[len(header.Instrs)-1]
	if term.Op != OpBranch || len(term.Args) != 1 || term.Args[0] == nil || term.Args[0].Def == nil {
		return nil
	}
	cmp := term.Args[0].Def
	if cmp.Op != OpLeInt || len(cmp.Args) != 2 || cmp.Args[0] == nil || cmp.Args[0].ID != stepInstr.ID {
		return nil
	}
	return cmp.Args[1]
}

func bodyIsSafeForUnroll(body *Block) bool {
	if body == nil || len(body.Instrs) == 0 {
		return false
	}
	for _, instr := range body.Instrs[:len(body.Instrs)-1] {
		if !isUnrollCloneableOp(instr.Op) {
			return false
		}
	}
	return true
}

func isUnrollCloneableOp(op Op) bool {
	switch op {
	case OpConstInt, OpConstFloat, OpConstBool, OpConstNil, OpConstString,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat, OpSqrt, OpFloor, OpFMA, OpFMSUB,
		OpNumToFloat, OpGuardType, OpGuardIntRange, OpGuardNonNil, OpGuardTruthy,
		OpMatrixLoadFAt, OpMatrixLoadFRow, OpTableArrayLoad, OpTableArrayNestedLoad:
		return true
	default:
		return false
	}
}
