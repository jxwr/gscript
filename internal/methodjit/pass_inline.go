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
// Transitive inlining: the pass runs to fixpoint — if an inlined callee body
// itself contains calls to eligible globals, those are inlined on subsequent
// rounds, up to inlineMaxIterations. The size budget naturally bounds the
// depth: each inlining grows the caller, so callees eventually stop fitting.

package methodjit

import (
	"fmt"
	"os"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// countOpHelper counts instructions of the given op (debug helper).
func countOpHelper(fn *Function, op Op) int {
	n := 0
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if instr.Op == op {
				n++
			}
		}
	}
	return n
}

// InlineConfig configures the function inlining pass.
type InlineConfig struct {
	Globals            map[string]*vm.FuncProto // global function name -> proto
	MaxSize            int                      // max callee bytecode count (default 30)
	MaxRecursion       int                      // max inlining depth for self/mutually-recursive callees (0 = no recursive inlining)
	MaxCumulativeSize  int                      // R166: V8-style cumulative-bytecode cap across all inlines in this compilation (0 = unbounded, preserves R73 behavior)
	PreserveSelfCalls  bool                     // keep direct self calls visible for specialized recursive ABIs/TCO
	RequirePureNumeric bool                     // only inline side-effect-free single-result numeric helpers
}

// inlineMaxIterations is the safety cap on recursive inlining iterations.
// Each iteration inlines one level of callees into the caller. A callee that
// itself makes calls is only fully flattened after multiple iterations.
// The budget (MaxSize) naturally bounds recursion — each inline grows the
// caller, and eventually no more callees fit the budget. This cap is a belt-
// and-suspenders guard against pathological cases (e.g., mutual recursion).
const inlineMaxIterations = 5

// InlinePassWith returns a PassFunc that inlines small monomorphic call sites.
// The pass runs to fixpoint: after each inlining round, it re-scans for new
// inlineable call sites (introduced by inlining callees that themselves made
// calls). Stops when no callee was inlined in a round or the iteration cap
// is reached.
//
// Note: we cannot terminate purely on call-count: inlining a multi-block
// callee can REPLACE one call with another (a leaf call from the callee
// body), leaving the count unchanged while still making progress. Instead,
// each round reports whether it inlined anything.
func InlinePassWith(config InlineConfig) PassFunc {
	if config.MaxSize == 0 {
		config.MaxSize = 30
	}
	// MaxRecursion is NOT defaulted here: a zero value is a valid caller
	// choice meaning "do not inline any recursive callee" (matches legacy
	// isRecursive-veto behavior). Callers that want bounded recursive
	// inlining set this explicitly (e.g., 2 for Tier 2).
	return func(fn *Function) (*Function, error) {
		// Expose the globals table on the Function so the IR correctness
		// oracle (Interpret) can resolve residual cross-function calls left
		// behind by bounded recursive inlining. Production code paths don't
		// read this field.
		if fn.Globals == nil && config.Globals != nil {
			fn.Globals = config.Globals
		}
		// recursionCounts tracks, per callee proto, how many times that proto
		// has been inlined into this caller across the whole fixpoint. It is
		// used to bound inlining of self- and mutually-recursive callees.
		// Non-recursive callees increment the counter too (harmless: the gate
		// only triggers for recursive callees), and since they don't produce
		// more calls to themselves, the counter never restricts useful work.
		recursionCounts := make(map[*vm.FuncProto]int)
		// recursiveMemo caches the isRecursiveOrMutual result so we don't
		// re-walk the transitive call graph on every call site.
		recursiveMemo := make(map[*vm.FuncProto]bool)
		// R166: track cumulative bytecode across all inlines. V8's model
		// (max-inlined-bytecode-size-cumulative=920). Bounds explosion for
		// asymmetric call trees (ack) while allowing deeper linear inline
		// for symmetric ones (fib).
		cumulativeCtx := &inlineCumulativeTracker{}
		for i := 0; i < inlineMaxIterations; i++ {
			var inlined bool
			var err error
			fn, inlined, err = inlineCalls(fn, config, recursionCounts, recursiveMemo, cumulativeCtx)
			if err != nil {
				return fn, err
			}
			if os.Getenv("GSCRIPT_INLINE_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "inline iter %d: inlined=%v calls=%d\n", i, inlined, countOpHelper(fn, OpCall))
			}
			if !inlined {
				break
			}
		}
		return fn, nil
	}
}

// inlineCalls is the main inlining driver. It scans the caller for OpCall
// instructions that can be resolved statically and inlines eligible callees.
// Returns (fn, inlined, err) where inlined indicates whether any call was
// inlined during this pass (used by the fixpoint driver).
// inlineCumulativeTracker tracks total inlined bytecode across the
// entire fixpoint for a single compilation. R166's V8-alignment
// prevents asymmetric call trees (e.g. ackermann: 2 nested calls per
// level) from exploding the caller's code size when MaxRecursion is
// raised to permit deeper inlining of symmetric trees (e.g. fib).
type inlineCumulativeTracker struct {
	totalBytes int
}

func inlineCalls(fn *Function, config InlineConfig, recursionCounts map[*vm.FuncProto]int, recursiveMemo map[*vm.FuncProto]bool, cumulative *inlineCumulativeTracker) (*Function, bool, error) {
	// Iterate over blocks. We may add new blocks during inlining, so we
	// snapshot the block list and process only the original blocks.
	origBlocks := make([]*Block, len(fn.Blocks))
	copy(origBlocks, fn.Blocks)

	inlined := false
	for _, block := range origBlocks {
		if inlineCallsInBlock(fn, block, config, recursionCounts, recursiveMemo, cumulative) {
			inlined = true
		}
	}

	if inlined {
		// Rewire placeholder Value.Def pointers produced by remapDef so that
		// later passes (and the next fixpoint iteration) see the live Instr.
		relinkValueDefs(fn)
	}

	return fn, inlined, nil
}

// inlineCallsInBlock processes one block, looking for inlineable OpCall sites.
// When a call is inlined, the block's instruction list is rewritten in place.
// Returns true if at least one call in this block was inlined.
func inlineCallsInBlock(fn *Function, block *Block, config InlineConfig, recursionCounts map[*vm.FuncProto]int, recursiveMemo map[*vm.FuncProto]bool, cumulative *inlineCumulativeTracker) bool {
	inlined := false
	// We iterate by index because we'll be replacing instructions in-place.
	for i := 0; i < len(block.Instrs); i++ {
		instr := block.Instrs[i]
		if instr.Op != OpCall {
			continue
		}

		calleeName, calleeProto := resolveCallee(instr, fn, config)
		if calleeProto == nil {
			functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
				"callee is not statically resolved from inline globals")
			continue
		}
		if config.PreserveSelfCalls && calleeProto == fn.Proto {
			functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
				"preserved self call for specialized recursive entry")
			continue
		}

		// Bounded recursion gate: if this callee is (self- or mutually-)
		// recursive, we cap how many times it may be inlined across the whole
		// fixpoint for this caller. Non-recursive callees are never gated:
		// they don't generate more calls to themselves, so their counter
		// never restricts useful inlining.
		if isRecursiveOrMutualCached(calleeProto, config.Globals, recursiveMemo) {
			if recursionCounts[calleeProto] >= config.MaxRecursion {
				functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("recursive inline depth cap reached for %s", calleeName))
				continue
			}
		}

		// Check size budget.
		if len(calleeProto.Code) > config.MaxSize {
			functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("callee %s bytecode size %d exceeds max %d", calleeName, len(calleeProto.Code), config.MaxSize))
			continue
		}

		// R166: check cumulative-bytecode budget (V8 alignment).
		// Prevents asymmetric call trees from exploding caller body
		// when MaxRecursion permits deeper inlining.
		if config.MaxCumulativeSize > 0 &&
			cumulative.totalBytes+len(calleeProto.Code) > config.MaxCumulativeSize {
			functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("cumulative inline bytecode budget reached before %s", calleeName))
			continue
		}

		// Build the callee's IR.
		calleeFn := BuildGraph(calleeProto)
		if config.RequirePureNumeric {
			if reason := pureNumericInlineRejectReason(calleeFn); reason != "" {
				functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("callee %s rejected by pure numeric inline policy: %s", calleeName, reason))
				continue
			}
		}

		// Multi-block inlining rewires predecessor lists and phi args. General
		// loop-bearing callees inside caller loops are still too broad, but
		// small pure numeric helpers are profitable and do not introduce aliasing
		// or side-effect replay hazards across the new nested loop.
		if computeLoopInfo(calleeFn).hasLoops() && computeLoopInfo(fn).loopBlocks[block.ID] {
			if reason := pureNumericInlineRejectReason(calleeFn); reason != "" {
				functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("callee %s has loops inside caller loop and is not pure numeric: %s", calleeName, reason))
				continue
			}
			if callABICalleeHasShiftAddOverflowVersion(calleeProto, nil) {
				functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("callee %s has overflow-versioned numeric recurrence inside caller loop", calleeName))
				continue
			}
			functionRemarks(fn).Add("Inline", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("admitted pure numeric loop callee %s inside caller loop", calleeName))
		}

		// Check if the callee is single-block (trivial inline).
		if len(calleeFn.Blocks) == 1 {
			newInstrs := inlineTrivial(fn, block, instr, i, calleeFn, calleeName)
			if newInstrs != nil {
				block.Instrs = newInstrs
				inlined = true
				recursionCounts[calleeProto]++
				cumulative.totalBytes += len(calleeProto.Code)
				functionRemarks(fn).Add("Inline", "changed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("inlined single-block callee %s", calleeName))
				// Adjust index: the call was replaced, re-scan from the
				// same position since new instructions were inserted.
				i-- // will be incremented by the loop
				continue
			}
			functionRemarks(fn).Add("Inline", "missed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("single-block callee %s could not be spliced", calleeName))
		}

		// Multi-block callee: inline with block splicing.
		// This modifies block.Instrs directly (truncates + adds jump),
		// moves post-call instrs to a merge block. Stop processing this block.
		inlineMultiBlock(fn, block, instr, i, calleeFn, calleeName)
		recursionCounts[calleeProto]++
		cumulative.totalBytes += len(calleeProto.Code)
		functionRemarks(fn).Add("Inline", "changed", block.ID, instr.ID, instr.Op,
			fmt.Sprintf("inlined multi-block callee %s", calleeName))
		return true
	}
	return inlined
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

func pureNumericInlineRejectReason(calleeFn *Function) string {
	if calleeFn == nil || calleeFn.Proto == nil {
		return "missing callee IR"
	}
	if calleeFn.Unpromotable {
		return "callee uses unmodeled bytecode"
	}
	if calleeFn.Proto.IsVarArg {
		return "vararg callee"
	}
	if len(calleeFn.Proto.Upvalues) > 0 {
		return "callee captures upvalues"
	}

	returns := 0
	for _, block := range calleeFn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			if instr.Op == OpReturn {
				returns++
				if len(instr.Args) != 1 {
					return "callee does not have exactly one return value"
				}
				if !pureNumericValue(instr.Args[0]) {
					return "return value is not numeric"
				}
				continue
			}
			if !pureNumericInlineOp(instr.Op) {
				return fmt.Sprintf("side-effecting or escaping op %s", instr.Op)
			}
		}
	}
	if returns == 0 {
		return "callee has no return"
	}
	return ""
}

func pureNumericValue(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	switch v.Def.Type {
	case TypeInt, TypeFloat:
		return true
	case TypeAny, TypeUnknown:
		switch v.Def.Op {
		case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm,
			OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
			OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
			OpNumToFloat, OpPhi, OpLoadSlot:
			return true
		}
	}
	return false
}

func pureNumericInlineOp(op Op) bool {
	switch op {
	case OpConstInt, OpConstFloat,
		OpLoadSlot,
		OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpNumToFloat, OpSqrt, OpFloor, OpFMA, OpFMSUB,
		OpEq, OpLt, OpLe, OpEqInt, OpLtInt, OpLeInt, OpLtFloat, OpLeFloat,
		OpModZeroInt,
		OpGuardType, OpGuardIntRange,
		OpJump, OpBranch,
		OpPhi:
		return true
	default:
		return false
	}
}

// isRecursive checks if a FuncProto contains any OP_GETGLOBAL that loads
// its own name, indicating it calls itself (directly recursive).
// Kept for the Tier 2 promotion heuristic in tiering_manager.go which gates
// on direct self-recursion only. Bounded inlining uses the broader
// isRecursiveOrMutualCached helper.
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

// isRecursiveOrMutualCached returns true if proto participates in any
// call cycle reachable from itself through OP_GETGLOBAL -> globals lookup.
// Covers both direct self-recursion (f -> f) and mutual recursion
// (f -> g -> ... -> f). Results are memoized per proto.
func isRecursiveOrMutualCached(proto *vm.FuncProto, globals map[string]*vm.FuncProto, memo map[*vm.FuncProto]bool) bool {
	if r, ok := memo[proto]; ok {
		return r
	}
	// DFS through the transitive call graph. We consider `proto` recursive
	// if any path from `proto` (through OP_GETGLOBAL references resolved via
	// the globals table) loops back to `proto` itself.
	visited := make(map[*vm.FuncProto]bool)
	var walk func(p *vm.FuncProto) bool
	walk = func(p *vm.FuncProto) bool {
		for _, inst := range p.Code {
			if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
				continue
			}
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(p.Constants) {
				continue
			}
			nameConst := p.Constants[bx]
			if !nameConst.IsString() {
				continue
			}
			target, ok := globals[nameConst.Str()]
			if !ok || target == nil {
				continue
			}
			if target == proto {
				return true
			}
			if visited[target] {
				continue
			}
			visited[target] = true
			if walk(target) {
				return true
			}
		}
		return false
	}
	result := walk(proto)
	memo[proto] = result
	return result
}

// inlineTrivial inlines a single-block callee into the caller at position idx.
// Returns the new instruction list for the block, or nil if inlining failed.
//
// For a single-block callee:
//  1. Renumber all callee value IDs to be unique in the caller.
//  2. Replace callee's LoadSlot (parameter loads) with the caller's arguments.
//  3. Replace callee's OpReturn: the return value becomes the inline result.
//  4. Splice the callee's instructions (minus LoadSlots and Return) into the
//     caller block, replacing the OpCall.
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
		newInstr.copySourceFrom(ci)
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

		// Also rewrite references in ALL other blocks. The call result may be
		// used as a phi argument in another block (e.g., a loop header phi for
		// a loop-carried variable). Without this, the phi would still reference
		// the old (now dead) call ID and get garbage/zero at emit time.
		for _, b := range fn.Blocks {
			if b == block {
				continue // already handled above via newInstrs
			}
			rewriteValueRefs(b.Instrs, callInstr.ID, inlineResult)
		}
	}

	copyInlinedFixedTableConstructors(fn, calleeFn, idMap)

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

	// Find the maximum block ID currently in use. Scanning is required because
	// after previous inlining rounds the block list is not necessarily sorted
	// by ID (the original entry keeps its low ID, newly spliced blocks get
	// high IDs, and any block added since then may follow). Using the tail
	// block's ID would not be safe in the fixpoint loop.
	maxBlockID := 0
	for _, b := range fn.Blocks {
		if b.ID > maxBlockID {
			maxBlockID = b.ID
		}
	}

	// Create a merge block for instructions after the call.
	mergeBlock := &Block{
		ID:   maxBlockID + 1,
		defs: make(map[int]*Value),
	}

	// Renumber all callee block IDs and value IDs.
	nextBlockID := mergeBlock.ID + 1
	idMap := make(map[int]int)       // callee value ID -> caller value ID
	blockMap := make(map[int]*Block) // callee block ID -> new block

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
				jmp.copySourceFrom(ci)
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
			newInstr.copySourceFrom(ci)

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
			}
		}
	}

	// Preserve each cloned block's predecessor order from the callee CFG so
	// phi argument indexes continue to line up with Block.Preds after inlining.
	for _, cb := range calleeFn.Blocks {
		newBlock := blockMap[cb.ID]
		for _, pred := range cb.Preds {
			if newPred := blockMap[pred.ID]; newPred != nil {
				newBlock.Preds = append(newBlock.Preds, newPred)
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
		phi.copySourceFrom(callInstr)
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

		// Also rewrite references in ALL other blocks. The call result may be
		// used as a phi argument in another block (e.g., a loop header phi for
		// a loop-carried variable). Without this, the phi would still reference
		// the old (now dead) call ID and get garbage/zero at emit time.
		for _, b := range fn.Blocks {
			if b == block || b == mergeBlock {
				continue // already handled
			}
			rewriteValueRefs(b.Instrs, callInstr.ID, inlineResult)
		}
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
	jmpToCallee.copySourceFrom(callInstr)
	block.Instrs = append(block.Instrs, jmpToCallee)
	block.Succs = []*Block{calleeEntry}
	calleeEntry.Preds = append(calleeEntry.Preds, block)

	// Add all new blocks to the function.
	for _, cb := range calleeFn.Blocks {
		fn.Blocks = append(fn.Blocks, blockMap[cb.ID])
	}
	fn.Blocks = append(fn.Blocks, mergeBlock)
	copyInlinedFixedTableConstructors(fn, calleeFn, idMap)

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

func copyInlinedFixedTableConstructors(callerFn, calleeFn *Function, idMap map[int]int) {
	if callerFn == nil || callerFn.Proto == nil || calleeFn == nil || calleeFn.Proto == nil || len(calleeFn.FixedTableConstructors) == 0 {
		return
	}
	for oldID, fact := range calleeFn.FixedTableConstructors {
		newID, ok := idMap[oldID]
		if !ok {
			continue
		}
		mapped, ok := remapInlineFixedTableConstructorFact(callerFn.Proto, calleeFn.Proto, fact)
		if !ok {
			continue
		}
		if callerFn.FixedTableConstructors == nil {
			callerFn.FixedTableConstructors = make(map[int]FixedTableConstructorFact)
		}
		callerFn.FixedTableConstructors[newID] = mapped
	}
}

func remapInlineFixedTableConstructorFact(caller, callee *vm.FuncProto, fact FixedTableConstructorFact) (FixedTableConstructorFact, bool) {
	switch {
	case fact.Ctor2Index >= 0:
		if fact.Ctor2Index >= len(callee.TableCtors2) {
			return FixedTableConstructorFact{}, false
		}
		ctor := callee.TableCtors2[fact.Ctor2Index].Runtime
		idx := ensureInlineTableCtor2(caller, ctor.Key1, ctor.Key2)
		return FixedTableConstructorFact{
			Ctor2Index: idx,
			CtorNIndex: -1,
			FieldNames: append([]string(nil), fact.FieldNames...),
		}, true
	case fact.CtorNIndex >= 0:
		if fact.CtorNIndex >= len(callee.TableCtorsN) {
			return FixedTableConstructorFact{}, false
		}
		keys := append([]string(nil), callee.TableCtorsN[fact.CtorNIndex].Runtime.Keys...)
		idx := ensureInlineTableCtorN(caller, keys)
		return FixedTableConstructorFact{
			Ctor2Index: -1,
			CtorNIndex: idx,
			FieldNames: append([]string(nil), fact.FieldNames...),
		}, true
	default:
		return FixedTableConstructorFact{}, false
	}
}

func ensureInlineTableCtor2(proto *vm.FuncProto, key1, key2 string) int {
	for i := range proto.TableCtors2 {
		ctor := proto.TableCtors2[i].Runtime
		if ctor.Key1 == key1 && ctor.Key2 == key2 {
			return i
		}
	}
	key1Const := ensureInlineStringConstant(proto, key1)
	key2Const := ensureInlineStringConstant(proto, key2)
	proto.TableCtors2 = append(proto.TableCtors2, vm.TableCtor2{
		Key1Const: key1Const,
		Key2Const: key2Const,
		Runtime:   runtime.NewSmallTableCtor2(key1, key2),
	})
	return len(proto.TableCtors2) - 1
}

func ensureInlineTableCtorN(proto *vm.FuncProto, keys []string) int {
	for i := range proto.TableCtorsN {
		ctor := proto.TableCtorsN[i].Runtime
		if sameStringList(ctor.Keys, keys) {
			return i
		}
	}
	keyConsts := make([]int, len(keys))
	for i, key := range keys {
		keyConsts[i] = ensureInlineStringConstant(proto, key)
	}
	proto.TableCtorsN = append(proto.TableCtorsN, vm.TableCtorN{
		KeyConsts: keyConsts,
		Runtime:   runtime.NewSmallTableCtorN(keys),
	})
	return len(proto.TableCtorsN) - 1
}

func ensureInlineStringConstant(proto *vm.FuncProto, key string) int {
	for i, c := range proto.Constants {
		if c.IsString() && c.Str() == key {
			return i
		}
	}
	idx := len(proto.Constants)
	proto.Constants = append(proto.Constants, runtime.StringValue(key))
	return idx
}

func sameStringList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameInlineConstant(a, b runtime.Value) bool {
	if a.IsString() && b.IsString() {
		return a.Str() == b.Str()
	}
	return a == b
}

// remapDef returns a remapped Def pointer if the original def's ID is in idMap.
// This is a shallow remap — just updates the ID for Value lookups. The Def
// pointer is rewired to the true (live) Instr by relinkValueDefs after
// inlining completes, so downstream passes see the remapped Aux and Args.
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

// relinkValueDefs scans the entire function and rewires each Value.Def to
// point to the live Instr with the matching ID. This repairs placeholder
// Def pointers produced by remapDef (and remapValue) during inlining so
// that later passes / subsequent inlining iterations can read fields like
// Aux directly off Value.Def.
func relinkValueDefs(fn *Function) {
	// Build id -> live Instr index.
	liveByID := make(map[int]*Instr, 64)
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			liveByID[instr.ID] = instr
		}
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			for _, arg := range instr.Args {
				if arg == nil {
					continue
				}
				if live, ok := liveByID[arg.ID]; ok {
					arg.Def = live
				}
			}
		}
	}
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
			if sameInlineConstant(c, calleeConst) {
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
