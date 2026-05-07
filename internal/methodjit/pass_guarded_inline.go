package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/vm"
)

const guardedInlineMaxTargets = 4

func tryInlineGuardedDynamicCall(fn *Function, block *Block, callInstr *Instr, idx int, config InlineConfig, cumulative *inlineCumulativeTracker) bool {
	if fn == nil || block == nil || callInstr == nil || callInstr.Op != OpCall || !callInstr.HasSource || callInstr.SourcePC < 0 {
		return false
	}
	if isGuardedInlineFallbackCall(block, callInstr) {
		return false
	}
	nArgs := len(callInstr.Args) - 1
	resultArity := callResultCountFromAux2(callInstr.Aux2)
	if nArgs < 0 || resultArity != 1 {
		return false
	}
	targets := config.Speculation.CallSiteVMProtoTargets(callInstr.SourcePC, 0, nArgs, resultArity)
	if len(targets) == 0 {
		return false
	}
	if len(targets) > guardedInlineMaxTargets {
		targets = targets[:guardedInlineMaxTargets]
	}

	type preparedTarget struct {
		proto *vm.FuncProto
		fn    *Function
	}
	prepared := make([]preparedTarget, 0, len(targets))
	seen := make(map[*vm.FuncProto]bool, len(targets))
	for _, target := range targets {
		calleeProto := target.Proto
		if !guardedDynamicInlineProtoEligible(fn, calleeProto, config, cumulative) || seen[calleeProto] {
			continue
		}
		calleeFn := BuildGraph(calleeProto)
		if calleeFn.Unpromotable || len(calleeFn.Blocks) != 1 {
			functionRemarks(fn).Add("GuardedInline", "missed", block.ID, callInstr.ID, callInstr.Op,
				fmt.Sprintf("callee %s is not single-block Tier2 IR", calleeProto.Name))
			continue
		}
		prepared = append(prepared, preparedTarget{proto: calleeProto, fn: calleeFn})
		seen[calleeProto] = true
	}
	if len(prepared) == 0 {
		return false
	}

	origSuccs := append([]*Block(nil), block.Succs...)
	postCallInstrs := append([]*Instr(nil), block.Instrs[idx+1:]...)
	nextBlockID := maxInlineBlockID(fn) + 1
	newBlock := func() *Block {
		b := &Block{ID: nextBlockID, defs: make(map[int]*Value)}
		nextBlockID++
		return b
	}

	mergeBlock := newBlock()
	missBlock := newBlock()
	guardBlocks := make([]*Block, len(prepared))
	for i := range guardBlocks {
		guardBlocks[i] = newBlock()
	}

	returnValues := make([]*Value, 0, len(prepared)+1)
	var clonedBlocks []*Block
	for _, target := range prepared {
		clone := cloneInlineCalleeGraph(fn, target.fn, callInstr.Args[1:], nextBlockID, mergeBlock)
		nextBlockID += len(clone.blocks)
		clonedBlocks = append(clonedBlocks, clone.blocks...)
		returnValues = append(returnValues, clone.returnValues...)
		copyInlinedFixedTableConstructors(fn, target.fn, clone.idMap)
	}

	// Miss path preserves the original dynamic call and joins the same merge.
	callInstr.Block = missBlock
	missBlock.Instrs = append(missBlock.Instrs, callInstr)
	missJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: missBlock}
	missJump.copySourceFrom(callInstr)
	missBlock.Instrs = append(missBlock.Instrs, missJump)
	missBlock.Succs = []*Block{mergeBlock}
	mergeBlock.Preds = append(mergeBlock.Preds, missBlock)
	returnValues = append(returnValues, callInstr.Value())

	inlineResult := mergeInlineReturnValues(fn, mergeBlock, callInstr, returnValues)
	for _, pi := range postCallInstrs {
		pi.Block = mergeBlock
		mergeBlock.Instrs = append(mergeBlock.Instrs, pi)
	}
	if inlineResult != nil {
		rewriteValueRefs(mergeBlock.Instrs, callInstr.ID, inlineResult)
		for _, b := range fn.Blocks {
			if b == block || b == mergeBlock || b == missBlock {
				continue
			}
			rewriteValueRefs(b.Instrs, callInstr.ID, inlineResult)
		}
	}

	mergeBlock.Succs = origSuccs
	for _, succ := range origSuccs {
		for i, pred := range succ.Preds {
			if pred == block {
				succ.Preds[i] = mergeBlock
			}
		}
	}

	for i, guardBlock := range guardBlocks {
		target := prepared[i]
		ref := fn.AddFuncProtoRef(target.proto)
		guard := &Instr{
			ID:    fn.newValueID(),
			Op:    OpIsVMClosureProto,
			Type:  TypeBool,
			Args:  []*Value{callInstr.Args[0]},
			Aux:   int64(ref),
			Block: guardBlock,
		}
		guard.copySourceFrom(callInstr)
		branch := &Instr{
			ID:    fn.newValueID(),
			Op:    OpBranch,
			Type:  TypeUnknown,
			Args:  []*Value{guard.Value()},
			Block: guardBlock,
		}
		branch.copySourceFrom(callInstr)
		guardBlock.Instrs = append(guardBlock.Instrs, guard, branch)
		trueTarget := clonedBlocks[i]
		falseTarget := missBlock
		if i+1 < len(guardBlocks) {
			falseTarget = guardBlocks[i+1]
		}
		guardBlock.Succs = []*Block{trueTarget, falseTarget}
		trueTarget.Preds = append(trueTarget.Preds, guardBlock)
		falseTarget.Preds = append(falseTarget.Preds, guardBlock)
	}

	block.Instrs = block.Instrs[:idx]
	block.Succs = []*Block{guardBlocks[0]}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: block}
	jump.copySourceFrom(callInstr)
	block.Instrs = append(block.Instrs, jump)
	guardBlocks[0].Preds = append(guardBlocks[0].Preds, block)

	fn.Blocks = append(fn.Blocks, guardBlocks...)
	fn.Blocks = append(fn.Blocks, clonedBlocks...)
	fn.Blocks = append(fn.Blocks, missBlock, mergeBlock)

	for _, target := range prepared {
		cumulative.totalBytes += len(target.proto.Code)
	}
	functionRemarks(fn).Add("GuardedInline", "changed", block.ID, callInstr.ID, callInstr.Op,
		fmt.Sprintf("guarded dynamic inline targets=%d pc=%d", len(prepared), callInstr.SourcePC))
	return true
}

func isGuardedInlineFallbackCall(block *Block, callInstr *Instr) bool {
	if block == nil || callInstr == nil {
		return false
	}
	for _, pred := range block.Preds {
		if pred == nil || len(pred.Instrs) < 2 {
			continue
		}
		branch := pred.Instrs[len(pred.Instrs)-1]
		if branch.Op != OpBranch || len(pred.Instrs) < 2 {
			continue
		}
		guard := pred.Instrs[len(pred.Instrs)-2]
		if guard.Op == OpIsVMClosureProto && guard.HasSource && callInstr.HasSource && guard.SourcePC == callInstr.SourcePC {
			return true
		}
	}
	return false
}

func guardedDynamicInlineProtoEligible(caller *Function, callee *vm.FuncProto, config InlineConfig, cumulative *inlineCumulativeTracker) bool {
	if caller == nil || callee == nil || callee == caller.Proto || callee.IsVarArg || len(callee.Upvalues) > 0 {
		return false
	}
	if len(callee.Code) > config.MaxSize {
		return false
	}
	if config.MaxCumulativeSize > 0 && cumulative != nil && cumulative.totalBytes+len(callee.Code) > config.MaxCumulativeSize {
		return false
	}
	return true
}
