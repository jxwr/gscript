// pass_inline.go implements function inlining for the Method JIT.
//
// When a call site is monomorphic (always calls the same function) and the
// callee is small enough, the callee's IR body is copied inline into the
// caller, replacing the OpCall with the callee's instructions. This eliminates
// call-exit overhead and enables cross-function optimization.
//
// Algorithm:
//   1. Scan all instructions for OpCall.
//   2. For each OpCall, check if the callee can be resolved statically:
//      the function value's defining instruction is OpGetGlobal, and the
//      global name maps to a known FuncProto in the InlineConfig.
//   3. If the callee has <= MaxSize bytecodes and is not recursive, inline it.
//   4. Build the callee's IR via BuildGraph, renumber all value IDs to avoid
//      collisions with the caller, then splice the callee's blocks into the
//      caller at the call site.
//
// Inlining budget: callee must have <= MaxSize bytecode instructions (default 30).
// Only inline one level deep (no recursive or transitive inlining).

package methodjit

import (
	"github.com/gscript/gscript/internal/vm"
)

// InlineConfig configures the function inlining pass.
type InlineConfig struct {
	Globals map[string]*vm.FuncProto // global function name -> proto
	MaxSize int                       // max callee bytecode count (default 30)
}

// InlinePassWith returns a PassFunc that inlines small monomorphic call sites.
func InlinePassWith(config InlineConfig) PassFunc {
	if config.MaxSize == 0 {
		config.MaxSize = 30
	}
	return func(fn *Function) (*Function, error) {
		return inlineCalls(fn, config)
	}
}

// inlineCalls is the main inlining driver. It scans the caller for OpCall
// instructions that can be resolved statically and inlines eligible callees.
func inlineCalls(fn *Function, config InlineConfig) (*Function, error) {
	// Iterate over blocks. We may add new blocks during inlining, so we
	// snapshot the block list and process only the original blocks.
	origBlocks := make([]*Block, len(fn.Blocks))
	copy(origBlocks, fn.Blocks)

	for _, block := range origBlocks {
		inlineCallsInBlock(fn, block, config)
	}

	return fn, nil
}

// inlineCallsInBlock processes one block, looking for inlineable OpCall sites.
// When a call is inlined, the block's instruction list is rewritten in place.
func inlineCallsInBlock(fn *Function, block *Block, config InlineConfig) {
	// We iterate by index because we'll be replacing instructions in-place.
	for i := 0; i < len(block.Instrs); i++ {
		instr := block.Instrs[i]
		if instr.Op != OpCall {
			continue
		}

		calleeName, calleeProto := resolveCallee(instr, fn, config)
		if calleeProto == nil {
			continue
		}

		// Don't inline recursive functions (callee calls itself).
		if isRecursive(calleeProto) {
			continue
		}

		// Check size budget.
		if len(calleeProto.Code) > config.MaxSize {
			continue
		}

		// Build the callee's IR.
		calleeFn := BuildGraph(calleeProto)

		// Check if the callee is single-block (trivial inline).
		if len(calleeFn.Blocks) == 1 {
			newInstrs := inlineTrivial(fn, block, instr, i, calleeFn, calleeName)
			if newInstrs != nil {
				block.Instrs = newInstrs
				// Adjust index: the call was replaced, re-scan from the
				// same position since new instructions were inserted.
				i-- // will be incremented by the loop
				continue
			}
		}

		// Multi-block callee: inline with block splicing.
		// This modifies block.Instrs directly (truncates + adds jump),
		// moves post-call instrs to a merge block. Stop processing this block.
		inlineMultiBlock(fn, block, instr, i, calleeFn, calleeName)
		return
	}
}

// resolveCallee checks if an OpCall's function argument comes from an
// OpGetGlobal, and if so, looks up the callee's FuncProto in the config.
// Returns the global name and proto, or ("", nil) if unresolvable.
func resolveCallee(callInstr *Instr, fn *Function, config InlineConfig) (string, *vm.FuncProto) {
	if len(callInstr.Args) == 0 {
		return "", nil
	}
	fnArg := callInstr.Args[0]
	if fnArg == nil || fnArg.Def == nil {
		return "", nil
	}
	if fnArg.Def.Op != OpGetGlobal {
		return "", nil
	}

	// Get the global name from the caller's constant pool.
	constIdx := int(fnArg.Def.Aux)
	if fn.Proto == nil || constIdx < 0 || constIdx >= len(fn.Proto.Constants) {
		return "", nil
	}
	nameVal := fn.Proto.Constants[constIdx]
	if !nameVal.IsString() {
		return "", nil
	}
	name := nameVal.Str()

	proto, ok := config.Globals[name]
	if !ok {
		return "", nil
	}
	return name, proto
}

// isRecursive checks if a FuncProto contains any OP_GETGLOBAL that loads
// its own name, indicating it calls itself (directly recursive).
func isRecursive(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_GETGLOBAL {
			bx := vm.DecodeBx(inst)
			if bx >= 0 && bx < len(proto.Constants) {
				if proto.Constants[bx].IsString() && proto.Constants[bx].Str() == proto.Name {
					return true
				}
			}
		}
	}
	return false
}

// inlineTrivial inlines a single-block callee into the caller at position idx.
// Returns the new instruction list for the block, or nil if inlining failed.
//
// For a single-block callee:
//   1. Renumber all callee value IDs to be unique in the caller.
//   2. Replace callee's LoadSlot (parameter loads) with the caller's arguments.
//   3. Replace callee's OpReturn: the return value becomes the inline result.
//   4. Splice the callee's instructions (minus LoadSlots and Return) into the
//      caller block, replacing the OpCall.
func inlineTrivial(fn *Function, block *Block, callInstr *Instr, idx int, calleeFn *Function, calleeName string) []*Instr {
	calleeBlock := calleeFn.Entry
	callArgs := callInstr.Args[1:] // skip the function reference arg

	// Map callee value IDs to caller value IDs.
	idMap := make(map[int]int)

	// Map callee parameters (LoadSlot instructions) to caller's argument values.
	paramValues := make(map[int]*Value) // callee value ID -> caller Value
	paramCount := 0
	for _, ci := range calleeBlock.Instrs {
		if ci.Op == OpLoadSlot && paramCount < calleeFn.Proto.NumParams {
			if paramCount < len(callArgs) {
				paramValues[ci.ID] = callArgs[paramCount]
			}
			paramCount++
		}
	}

	// Assign new IDs for non-parameter callee instructions.
	for _, ci := range calleeBlock.Instrs {
		if _, isParam := paramValues[ci.ID]; isParam {
			continue
		}
		if ci.Op == OpReturn {
			continue
		}
		newID := fn.newValueID()
		idMap[ci.ID] = newID
	}

	// Find the return value (the value that OpReturn returns).
	var returnValue *Value
	for _, ci := range calleeBlock.Instrs {
		if ci.Op == OpReturn && len(ci.Args) > 0 {
			returnValue = ci.Args[0]
			break
		}
	}

	// Build remapped callee instructions (excluding LoadSlots and Return).
	var inlinedInstrs []*Instr
	for _, ci := range calleeBlock.Instrs {
		if _, isParam := paramValues[ci.ID]; isParam {
			continue
		}
		if ci.Op == OpReturn {
			continue
		}
		newInstr := &Instr{
			ID:    idMap[ci.ID],
			Op:    ci.Op,
			Type:  ci.Type,
			Aux:   remapAux(ci, fn, calleeFn),
			Aux2:  ci.Aux2,
			Block: block,
		}
		// Remap args.
		newInstr.Args = make([]*Value, len(ci.Args))
		for j, arg := range ci.Args {
			newInstr.Args[j] = remapValue(arg, idMap, paramValues)
		}
		inlinedInstrs = append(inlinedInstrs, newInstr)
	}

	// Remap the return value to get the inlined result.
	var inlineResult *Value
	if returnValue != nil {
		inlineResult = remapValue(returnValue, idMap, paramValues)
	}

	// Build the new instruction list:
	//   [instrs before call] + [inlined body] + [instrs after call]
	// The call instruction is removed. References to the call's result
	// (callInstr.ID) must now point to the inlined return value.
	newInstrs := make([]*Instr, 0, len(block.Instrs)+len(inlinedInstrs))
	newInstrs = append(newInstrs, block.Instrs[:idx]...)
	newInstrs = append(newInstrs, inlinedInstrs...)
	newInstrs = append(newInstrs, block.Instrs[idx+1:]...)

	// Rewrite all references to the old call result to use the inlined result.
	if inlineResult != nil {
		rewriteValueRefs(newInstrs[idx:], callInstr.ID, inlineResult)
	}

	// Also remove the OpGetGlobal that loaded the function (it's now dead).
	// We leave it for DCE to clean up — don't complicate inlining with dead code removal.

	return newInstrs
}

// inlineMultiBlock inlines a multi-block callee by splicing the callee's
// blocks into the caller. The call block is split at the call site:
//   - Pre-call instructions stay in the original block
//   - Callee blocks are added to the function
//   - A merge block collects the return values
//   - Post-call instructions move to the merge block
//
// Modifies the block and function in place.
func inlineMultiBlock(fn *Function, block *Block, callInstr *Instr, idx int, calleeFn *Function, calleeName string) {
	callArgs := callInstr.Args[1:]

	// Create a merge block for instructions after the call.
	mergeBlock := &Block{
		ID:   fn.Blocks[len(fn.Blocks)-1].ID + 1,
		defs: make(map[int]*Value),
	}

	// Renumber all callee block IDs and value IDs.
	nextBlockID := mergeBlock.ID + 1
	idMap := make(map[int]int)       // callee value ID -> caller value ID
	blockMap := make(map[int]*Block)  // callee block ID -> new block

	// Map parameter LoadSlots to caller arguments.
	paramValues := make(map[int]*Value)
	paramCount := 0
	for _, ci := range calleeFn.Entry.Instrs {
		if ci.Op == OpLoadSlot && paramCount < calleeFn.Proto.NumParams {
			if paramCount < len(callArgs) {
				paramValues[ci.ID] = callArgs[paramCount]
			}
			paramCount++
		}
	}

	// Create new blocks for all callee blocks.
	for _, cb := range calleeFn.Blocks {
		newBlock := &Block{
			ID:   nextBlockID,
			defs: make(map[int]*Value),
		}
		nextBlockID++
		blockMap[cb.ID] = newBlock
	}

	// Assign new value IDs for all callee instructions (except param loads).
	for _, cb := range calleeFn.Blocks {
		for _, ci := range cb.Instrs {
			if _, isParam := paramValues[ci.ID]; isParam {
				continue
			}
			newID := fn.newValueID()
			idMap[ci.ID] = newID
		}
	}

	// Collect return values for the merge phi.
	var returnValues []*Value
	var returnPreds []*Block

	// Copy callee instructions into new blocks, remapping IDs and edges.
	for _, cb := range calleeFn.Blocks {
		newBlock := blockMap[cb.ID]

		for _, ci := range cb.Instrs {
			// Skip parameter loads (replaced by caller args).
			if _, isParam := paramValues[ci.ID]; isParam {
				continue
			}

			if ci.Op == OpReturn {
				// Replace return with jump to merge block.
				if len(ci.Args) > 0 {
					rv := remapValue(ci.Args[0], idMap, paramValues)
					returnValues = append(returnValues, rv)
					returnPreds = append(returnPreds, newBlock)
				}
				// Emit jump to merge block.
				jmp := &Instr{
					ID:    fn.newValueID(),
					Op:    OpJump,
					Type:  TypeUnknown,
					Block: newBlock,
				}
				newBlock.Instrs = append(newBlock.Instrs, jmp)
				newBlock.Succs = append(newBlock.Succs, mergeBlock)
				mergeBlock.Preds = append(mergeBlock.Preds, newBlock)
				continue
			}

			newInstr := &Instr{
				ID:    idMap[ci.ID],
				Op:    ci.Op,
				Type:  ci.Type,
				Aux:   remapAux(ci, fn, calleeFn),
				Aux2:  ci.Aux2,
				Block: newBlock,
			}

			// Remap args.
			newInstr.Args = make([]*Value, len(ci.Args))
			for j, arg := range ci.Args {
				newInstr.Args[j] = remapValue(arg, idMap, paramValues)
			}

			// For branch/jump, we need to remap successor blocks.
			if ci.Op == OpBranch || ci.Op == OpJump {
				// Succs are handled via block edges below.
			}

			newBlock.Instrs = append(newBlock.Instrs, newInstr)
		}

		// Remap successor edges.
		for _, succ := range cb.Succs {
			newSucc := blockMap[succ.ID]
			if newSucc != nil {
				newBlock.Succs = append(newBlock.Succs, newSucc)
				newSucc.Preds = append(newSucc.Preds, newBlock)
			}
		}
	}

	// Build the merge block with a phi for the return value (if multiple returns)
	// or just the single return value.
	var inlineResult *Value
	if len(returnValues) == 1 {
		inlineResult = returnValues[0]
	} else if len(returnValues) > 1 {
		phi := &Instr{
			ID:    fn.newValueID(),
			Op:    OpPhi,
			Type:  TypeAny,
			Args:  returnValues,
			Block: mergeBlock,
		}
		mergeBlock.Instrs = append(mergeBlock.Instrs, phi)
		inlineResult = phi.Value()
	}

	// Move post-call instructions from original block to merge block.
	postCallInstrs := block.Instrs[idx+1:]
	for _, pi := range postCallInstrs {
		pi.Block = mergeBlock
		mergeBlock.Instrs = append(mergeBlock.Instrs, pi)
	}

	// Rewrite references to old call result in post-call instructions.
	if inlineResult != nil {
		rewriteValueRefs(mergeBlock.Instrs, callInstr.ID, inlineResult)
	}

	// Transfer successor edges from original block's terminator to merge block.
	// The original block's successors become the merge block's successors.
	mergeBlock.Succs = block.Succs
	for _, succ := range block.Succs {
		// Replace the original block in succ's predecessors with the merge block.
		for k, pred := range succ.Preds {
			if pred == block {
				succ.Preds[k] = mergeBlock
			}
		}
	}

	// Original block: keep only pre-call instructions + jump to callee entry.
	block.Instrs = block.Instrs[:idx]
	block.Succs = nil

	// Add jump from original block to callee entry block.
	calleeEntry := blockMap[calleeFn.Entry.ID]
	jmpToCallee := &Instr{
		ID:    fn.newValueID(),
		Op:    OpJump,
		Type:  TypeUnknown,
		Block: block,
	}
	block.Instrs = append(block.Instrs, jmpToCallee)
	block.Succs = []*Block{calleeEntry}
	calleeEntry.Preds = append(calleeEntry.Preds, block)

	// Add all new blocks to the function.
	for _, cb := range calleeFn.Blocks {
		fn.Blocks = append(fn.Blocks, blockMap[cb.ID])
	}
	fn.Blocks = append(fn.Blocks, mergeBlock)

}

// remapValue translates a callee Value reference to the caller's namespace.
// Parameters are replaced with caller argument values; other values use idMap.
func remapValue(v *Value, idMap map[int]int, paramValues map[int]*Value) *Value {
	if v == nil {
		return nil
	}
	// Check if this is a parameter that maps to a caller argument.
	if pv, ok := paramValues[v.ID]; ok {
		return pv
	}
	// Otherwise, remap the ID.
	if newID, ok := idMap[v.ID]; ok {
		return &Value{ID: newID, Def: remapDef(v.Def, idMap)}
	}
	// Fallback: return as-is (shouldn't happen for well-formed IR).
	return v
}

// remapDef returns a remapped Def pointer if the original def's ID is in idMap.
// This is a shallow remap — just updates the ID for Value lookups.
func remapDef(def *Instr, idMap map[int]int) *Instr {
	if def == nil {
		return nil
	}
	if newID, ok := idMap[def.ID]; ok {
		// Return a placeholder Instr with the new ID so Value.ID lookups work.
		// The actual Instr is in the block's instruction list.
		return &Instr{ID: newID, Op: def.Op, Type: def.Type}
	}
	return def
}

// remapAux handles Aux field remapping for instructions that reference the
// constant pool. Since the callee has its own constant pool, we need to
// copy constants to the caller's pool when necessary.
//
// For OpConstString and OpGetGlobal, the Aux is a constant pool index.
// We copy the referenced constant from the callee's pool to the caller's.
func remapAux(ci *Instr, callerFn *Function, calleeFn *Function) int64 {
	switch ci.Op {
	case OpConstString, OpGetGlobal, OpSetGlobal, OpGetField, OpSetField:
		// These ops use Aux as a constant pool index into the callee's pool.
		calleeIdx := int(ci.Aux)
		if calleeFn.Proto == nil || calleeIdx < 0 || calleeIdx >= len(calleeFn.Proto.Constants) {
			return ci.Aux
		}
		calleeConst := calleeFn.Proto.Constants[calleeIdx]

		// Find or add this constant in the caller's pool.
		for j, c := range callerFn.Proto.Constants {
			if c == calleeConst {
				return int64(j)
			}
		}
		// Append to caller's constant pool.
		newIdx := len(callerFn.Proto.Constants)
		callerFn.Proto.Constants = append(callerFn.Proto.Constants, calleeConst)
		return int64(newIdx)

	default:
		return ci.Aux
	}
}

// rewriteValueRefs replaces all references to oldID with newValue in the
// given instruction slice.
func rewriteValueRefs(instrs []*Instr, oldID int, newValue *Value) {
	for _, instr := range instrs {
		for j, arg := range instr.Args {
			if arg != nil && arg.ID == oldID {
				instr.Args[j] = newValue
			}
		}
	}
}
