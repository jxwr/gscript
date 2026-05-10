package methodjit

import (
	"fmt"
	"unsafe"
)

// FieldShapeCallSplitPass peels one single-block case out of a polymorphic
// fixed-shape method call. The remaining shapes keep the existing
// OpFieldCallFloor fallback, while the peeled case becomes normal inlined IR so
// later passes can optimize its table/string/arithmetic operations.
//
// The pass is intentionally not wired into the production Tier 2 plan yet. It
// is a staging component for guarded runtime specialization; tests exercise the
// CFG rewrite before the pipeline starts using it broadly.
func FieldShapeCallSplitPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.FieldPolyShapeFacts) == 0 {
		return fn, nil
	}
	for _, block := range append([]*Block(nil), fn.Blocks...) {
		for idx, instr := range block.Instrs {
			if instr == nil || instr.Op != OpFieldCallFloor {
				continue
			}
			if fieldShapeSplitSingleBlockCase(fn, block, idx, instr) {
				return fn, nil
			}
		}
	}
	return fn, nil
}

func fieldShapeSplitSingleBlockCase(fn *Function, block *Block, idx int, call *Instr) bool {
	cases := fn.FieldPolyShapeFacts[call.ID]
	if len(cases) < 2 || len(call.Args) == 0 {
		functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
			fmt.Sprintf("missing field-shape cases for call: cases=%d args=%d", len(cases), len(call.Args)))
		return false
	}
	for caseIdx, c := range cases {
		if c.ShapeID == 0 || c.FieldIdx < 0 || c.VMProto == nil || c.ReceiverFact.ShapeID == 0 {
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d field=%d has incomplete guard/proto facts", c.ShapeID, c.FieldIdx))
			continue
		}
		if c.VMProto.NumParams != len(call.Args) {
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d proto=%s arg-count mismatch proto=%d call=%d",
					c.ShapeID, c.VMProto.Name, c.VMProto.NumParams, len(call.Args)))
			continue
		}
		calleeFn, reason := buildSingleBlockFieldShapeInlineCallee(c)
		ok := reason == ""
		if !ok {
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d proto=%s is not safe single-block after local lowering: %s", c.ShapeID, c.VMProto.Name, reason))
			continue
		}
		fieldShapeSplitCase(fn, block, idx, call, c, cases, caseIdx, calleeFn)
		functionRemarks(fn).Add("FieldShapeCallSplit", "changed", block.ID, call.ID, call.Op,
			fmt.Sprintf("split shape=%d proto=%s single-block method case", c.ShapeID, c.VMProto.Name))
		return true
	}
	return false
}

func buildSingleBlockFieldShapeInlineCallee(c FieldPolyShapeCase) (*Function, string) {
	calleeFn := BuildGraph(c.VMProto)
	if calleeFn == nil || len(calleeFn.Blocks) != 1 || calleeFn.Unpromotable {
		return nil, fieldShapeSplitCalleeShapeReason(calleeFn)
	}
	var err error
	calleeFn, err = TypeSpecializePass(calleeFn)
	if err != nil {
		return nil, "type specialization failed"
	}
	calleeFn, err = FixedShapeTableFactsPassWith(FixedShapeTableFactsConfig{
		ArgFacts: map[int]FixedShapeTableFact{0: c.ReceiverFact},
	})(calleeFn)
	if err != nil {
		return nil, "fixed-shape fact propagation failed"
	}
	calleeFn, err = TypeSpecializePass(calleeFn)
	if err != nil {
		return nil, "post-fact type specialization failed"
	}
	calleeFn, err = TableArrayLowerPass(calleeFn)
	if err != nil {
		return nil, "table-array lowering failed"
	}
	calleeFn, err = TableArrayLoadTypeSpecializePass(calleeFn)
	if err != nil {
		return nil, "table-array load type specialization failed"
	}
	calleeFn, err = TableArrayNestedLoadPass(calleeFn)
	if err != nil {
		return nil, "nested table-array load lowering failed"
	}
	calleeFn, err = FieldSvalsLowerPass(calleeFn)
	if err != nil {
		return nil, "field-svals lowering failed"
	}
	calleeFn, err = ConstPropPass(calleeFn)
	if err != nil {
		return nil, "const propagation failed"
	}
	calleeFn, err = DCEPass(calleeFn)
	if err != nil {
		return nil, "dce failed"
	}
	if reason := fieldShapeSplitCalleeShapeReason(calleeFn); reason != "" {
		return nil, reason
	}
	if reason := fieldShapeSplitCalleeExitResumeUnsafeReason(calleeFn); reason != "" {
		return nil, reason
	}
	return calleeFn, ""
}

func fieldShapeSplitCalleeExitResumeSafe(calleeFn *Function) bool {
	return fieldShapeSplitCalleeExitResumeUnsafeReason(calleeFn) == ""
}

func fieldShapeSplitCalleeShapeReason(calleeFn *Function) string {
	if calleeFn == nil {
		return "graph unavailable"
	}
	if calleeFn.Unpromotable {
		return "callee graph is unpromotable"
	}
	if len(calleeFn.Blocks) != 1 {
		return fmt.Sprintf("callee has %d blocks", len(calleeFn.Blocks))
	}
	return ""
}

func fieldShapeSplitCalleeExitResumeUnsafeReason(calleeFn *Function) string {
	if calleeFn == nil || len(calleeFn.Blocks) != 1 {
		return fieldShapeSplitCalleeShapeReason(calleeFn)
	}
	for _, instr := range calleeFn.Blocks[0].Instrs {
		if instr == nil || instr.Op == OpReturn || instr.Op == OpLoadSlot {
			continue
		}
		if !fieldShapeSplitInlineOpSafe(instr.Op) {
			return fmt.Sprintf("op %s needs callee-frame exit/deopt recovery", instr.Op)
		}
	}
	return ""
}

func fieldShapeSplitInlineOpSafe(op Op) bool {
	switch op {
	case OpConstInt, OpConstFloat, OpConstBool, OpConstNil, OpConstString,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpEqInt, OpLtInt, OpLeInt, OpEqString, OpLtFloat, OpLeFloat,
		OpFloor, OpNumToFloat, OpFieldSvals, OpFieldLoad, OpFieldLoadNumToFloat,
		OpGuardType, OpGuardIntRange, OpGuardCalleeProto:
		return true
	default:
		return false
	}
}

func fieldShapeSplitCase(fn *Function, block *Block, idx int, call *Instr, c FieldPolyShapeCase, cases []FieldPolyShapeCase, caseIdx int, calleeFn *Function) {
	maxBlockID := 0
	for _, b := range fn.Blocks {
		if b.ID > maxBlockID {
			maxBlockID = b.ID
		}
	}
	caseBlock := &Block{ID: maxBlockID + 1, defs: make(map[int]*Value)}
	fallbackBlock := &Block{ID: maxBlockID + 2, defs: make(map[int]*Value)}
	mergeBlock := &Block{ID: maxBlockID + 3, defs: make(map[int]*Value)}

	postCallInstrs := append([]*Instr(nil), block.Instrs[idx+1:]...)
	oldSuccs := append([]*Block(nil), block.Succs...)
	pre := append([]*Instr(nil), block.Instrs[:idx]...)
	receiver := call.Args[0]

	shape := emitIRInstr(fn, block, OpTableShapeID, TypeInt, []*Value{receiver}, 0, 0)
	shape.copySourceFrom(call)
	shapeConst := emitIRInstr(fn, block, OpConstInt, TypeInt, nil, int64(c.ShapeID), 0)
	shapeConst.copySourceFrom(call)
	eq := emitIRInstr(fn, block, OpEqInt, TypeBool, []*Value{shape.Value(), shapeConst.Value()}, 0, 0)
	eq.copySourceFrom(call)
	br := &Instr{ID: fn.newValueID(), Op: OpBranch, Args: []*Value{eq.Value()}, Block: block}
	br.copySourceFrom(call)
	block.Instrs = append(pre, shape, shapeConst, eq, br)
	block.Succs = []*Block{caseBlock, fallbackBlock}
	caseBlock.Preds = []*Block{block}
	fallbackBlock.Preds = []*Block{block}

	caseResult := appendFieldShapeInlinedSingleBlock(fn, caseBlock, call, c, calleeFn)
	caseJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: caseBlock}
	caseJump.copySourceFrom(call)
	caseBlock.Instrs = append(caseBlock.Instrs, caseJump)
	caseBlock.Succs = []*Block{mergeBlock}

	call.Block = fallbackBlock
	fallbackJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: fallbackBlock}
	fallbackJump.copySourceFrom(call)
	fallbackBlock.Instrs = []*Instr{call, fallbackJump}
	fallbackBlock.Succs = []*Block{mergeBlock}
	fn.FieldPolyShapeFacts[call.ID] = fieldShapeCasesWithout(cases, caseIdx)

	mergeBlock.Preds = []*Block{caseBlock, fallbackBlock}
	phi := &Instr{
		ID:    fn.newValueID(),
		Op:    OpPhi,
		Type:  TypeInt,
		Args:  []*Value{caseResult, call.Value()},
		Block: mergeBlock,
	}
	phi.copySourceFrom(call)
	mergeBlock.Instrs = []*Instr{phi}
	for _, pi := range postCallInstrs {
		pi.Block = mergeBlock
		mergeBlock.Instrs = append(mergeBlock.Instrs, pi)
	}
	rewriteValueRefs(mergeBlock.Instrs[1:], call.ID, phi.Value())
	for _, b := range fn.Blocks {
		if b == block || b == mergeBlock || b == caseBlock || b == fallbackBlock {
			continue
		}
		rewriteValueRefs(b.Instrs, call.ID, phi.Value())
	}

	mergeBlock.Succs = oldSuccs
	for _, succ := range oldSuccs {
		for i, pred := range succ.Preds {
			if pred == block {
				succ.Preds[i] = mergeBlock
			}
		}
	}
	fn.Blocks = append(fn.Blocks, caseBlock, fallbackBlock, mergeBlock)
	canonicalizeBlockOrder(fn)
}

func fieldShapeCasesWithout(cases []FieldPolyShapeCase, idx int) []FieldPolyShapeCase {
	if idx < 0 || idx >= len(cases) {
		return append([]FieldPolyShapeCase(nil), cases...)
	}
	out := make([]FieldPolyShapeCase, 0, len(cases)-1)
	out = append(out, cases[:idx]...)
	out = append(out, cases[idx+1:]...)
	return out
}

func appendFieldShapeInlinedSingleBlock(fn *Function, block *Block, call *Instr, c FieldPolyShapeCase, calleeFn *Function) *Value {
	svals := emitIRInstr(fn, block, OpFieldSvals, TypeInt, []*Value{call.Args[0]}, int64(c.ShapeID), 0)
	svals.copySourceFrom(call)
	method := emitIRInstr(fn, block, OpFieldLoad, TypeFunction, []*Value{svals.Value()}, int64(c.FieldIdx), 0)
	method.copySourceFrom(call)
	guard := emitIRInstr(fn, block, OpGuardCalleeProto, TypeFunction, []*Value{method.Value()}, int64(uintptr(unsafe.Pointer(c.VMProto))), 0)
	guard.copySourceFrom(call)
	block.Instrs = append(block.Instrs, svals, method, guard)

	calleeBlock := calleeFn.Entry
	paramValues := inlineParamValues(calleeFn, call.Args)
	idMap := make(map[int]int)
	for _, ci := range calleeBlock.Instrs {
		if _, isParam := paramValues[ci.ID]; isParam || ci.Op == OpReturn {
			continue
		}
		idMap[ci.ID] = fn.newValueID()
	}
	var returnValue *Value
	for _, ci := range calleeBlock.Instrs {
		if ci.Op == OpReturn && len(ci.Args) > 0 {
			returnValue = ci.Args[0]
			break
		}
	}
	for _, ci := range calleeBlock.Instrs {
		if _, isParam := paramValues[ci.ID]; isParam || ci.Op == OpReturn {
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
		newInstr.copySourceFrom(call)
		newInstr.Args = make([]*Value, len(ci.Args))
		for j, arg := range ci.Args {
			newInstr.Args[j] = remapValue(arg, idMap, paramValues)
		}
		block.Instrs = append(block.Instrs, newInstr)
	}
	copyInlinedFixedTableConstructors(fn, calleeFn, idMap)
	result := remapValue(returnValue, idMap, paramValues)
	if result == nil {
		nilConst := emitIRInstr(fn, block, OpConstNil, TypeAny, nil, 0, 0)
		nilConst.copySourceFrom(call)
		block.Instrs = append(block.Instrs, nilConst)
		return nilConst.Value()
	}
	if result.Def != nil && result.Def.Type == TypeInt {
		return result
	}
	floor := emitIRInstr(fn, block, OpFloor, TypeInt, []*Value{result}, 0, 0)
	floor.copySourceFrom(call)
	block.Instrs = append(block.Instrs, floor)
	return floor.Value()
}
