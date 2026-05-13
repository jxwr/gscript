package methodjit

import (
	"fmt"
)

// FieldShapeCallSplitPass peels one case out of a polymorphic fixed-shape
// method call. The remaining shapes keep the existing OpFieldCallFloor
// fallback, while the peeled case becomes a monomorphic OpFieldCallFloor arm
// so later passes can lower it through the existing native typed-peer path.
//
// The pass is intentionally not wired into the production Tier 2 plan yet. It
// is a staging component for guarded runtime specialization; tests exercise the
// CFG rewrite before the pipeline starts using it broadly.
func FieldShapeCallSplitPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.FieldPolyShapeFacts) == 0 {
		return fn, nil
	}
	for splits := 0; splits < 16; splits++ {
		changed := false
		for _, block := range append([]*Block(nil), fn.Blocks...) {
			for idx, instr := range block.Instrs {
				if instr == nil || instr.Op != OpFieldCallFloor {
					continue
				}
				if fieldShapeSplitSingleBlockCase(fn, block, idx, instr) {
					changed = true
					break
				}
			}
			if changed {
				break
			}
		}
		if !changed {
			break
		}
	}
	return fn, nil
}

// FieldShapeCallSplitPreInlinePass converts one polymorphic field-dispatched
// OpCall into a shape branch whose hot arm carries a monomorphic call fact.
// The regular Inline pass then handles the branch with the existing guarded
// dynamic-callee machinery, so multi-block callees and normal lowering stay in
// one implementation path.
//
// This is intentionally not in the production Tier 2 plan yet: enabling it
// without a stable mid-run version switch can trigger repeated
// feedback_matured refreshes as the split arm introduces new guarded callee
// facts. Keep it as a tested staging component until speculation state can
// install the rewritten version exactly once per feedback epoch.
func FieldShapeCallSplitPreInlinePass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.FieldPolyShapeFacts) == 0 {
		return fn, nil
	}
	for splits := 0; splits < 16; splits++ {
		changed := false
		for _, block := range append([]*Block(nil), fn.Blocks...) {
			for idx, instr := range block.Instrs {
				if instr == nil || instr.Op != OpCall {
					continue
				}
				if fieldShapeSplitPreInlineCallCase(fn, block, idx, instr) {
					changed = true
					break
				}
			}
			if changed {
				break
			}
		}
		if !changed {
			break
		}
	}
	return fn, nil
}

func fieldShapeSplitPreInlineCallCase(fn *Function, block *Block, idx int, call *Instr) bool {
	calleeLoad := fieldShapeCallCalleeLoad(call)
	if calleeLoad == nil {
		return false
	}
	cases := fn.FieldPolyShapeFacts[calleeLoad.ID]
	if len(cases) < 2 || len(call.Args) < 2 {
		return false
	}
	callArgs, ok := inlineCallArgumentValues(call)
	if !ok || len(callArgs) == 0 {
		return false
	}
	for caseIdx, c := range cases {
		if reason := fieldShapeInlineSplitCaseRejectReason(c, callArgs, InlineConfig{MaxSize: 1 << 30}, computeLoopInfo(fn).loopBlocks[block.ID]); reason != "" {
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d proto=%s pre-inline split rejected: %s", c.ShapeID, fieldShapeCaseProtoName(c), reason))
			continue
		}
		fieldShapeSplitPreInlineCase(fn, block, idx, call, calleeLoad, c, cases, caseIdx)
		functionRemarks(fn).Add("FieldShapeCallSplit", "changed", block.ID, call.ID, call.Op,
			fmt.Sprintf("pre-inline split shape=%d proto=%s", c.ShapeID, fieldShapeCaseProtoName(c)))
		return true
	}
	return false
}

func fieldShapeCallCalleeLoad(call *Instr) *Instr {
	if call == nil || call.Op != OpCall || len(call.Args) == 0 || call.Args[0] == nil || call.Args[0].Def == nil {
		return nil
	}
	if call.Args[0].Def.Op != OpGetField {
		return nil
	}
	return call.Args[0].Def
}

func fieldShapeCaseProtoName(c FieldPolyShapeCase) string {
	if c.VMProto == nil {
		return "<nil>"
	}
	return c.VMProto.Name
}

func fieldShapeSplitPreInlineCase(fn *Function, block *Block, idx int, call, calleeLoad *Instr, c FieldPolyShapeCase, cases []FieldPolyShapeCase, caseIdx int) {
	uses := computeUseCounts(fn)
	localizeCalleeLoad := uses[calleeLoad.ID] == 1
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
	if localizeCalleeLoad {
		pre = removeInstrByID(pre, calleeLoad.ID)
	}
	receiver := call.Args[1]

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

	caseLoad := &Instr{
		ID:    fn.newValueID(),
		Op:    OpGetField,
		Type:  calleeLoad.Type,
		Args:  append([]*Value(nil), calleeLoad.Args...),
		Aux:   calleeLoad.Aux,
		Aux2:  calleeLoad.Aux2,
		Block: caseBlock,
	}
	caseLoad.copySourceFrom(calleeLoad)
	caseArgs := append([]*Value(nil), call.Args...)
	caseArgs[0] = caseLoad.Value()
	caseCall := &Instr{
		ID:    fn.newValueID(),
		Op:    OpCall,
		Type:  call.Type,
		Args:  caseArgs,
		Aux:   call.Aux,
		Aux2:  call.Aux2,
		Block: caseBlock,
	}
	caseCall.copySourceFrom(call)
	caseJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: caseBlock}
	caseJump.copySourceFrom(call)
	caseBlock.Instrs = []*Instr{caseLoad, caseCall, caseJump}
	caseBlock.Succs = []*Block{mergeBlock}
	fn.FieldPolyShapeFacts[caseLoad.ID] = []FieldPolyShapeCase{c}

	fallbackInstrs := make([]*Instr, 0, 3)
	if localizeCalleeLoad {
		fallbackLoad := &Instr{
			ID:    fn.newValueID(),
			Op:    OpGetField,
			Type:  calleeLoad.Type,
			Args:  append([]*Value(nil), calleeLoad.Args...),
			Aux:   calleeLoad.Aux,
			Aux2:  calleeLoad.Aux2,
			Block: fallbackBlock,
		}
		fallbackLoad.copySourceFrom(calleeLoad)
		fallbackArgs := append([]*Value(nil), call.Args...)
		fallbackArgs[0] = fallbackLoad.Value()
		call.Args = fallbackArgs
		fallbackInstrs = append(fallbackInstrs, fallbackLoad)
		fn.FieldPolyShapeFacts[fallbackLoad.ID] = fieldShapeCasesWithout(cases, caseIdx)
		delete(fn.FieldPolyShapeFacts, calleeLoad.ID)
	} else {
		fn.FieldPolyShapeFacts[calleeLoad.ID] = fieldShapeCasesWithout(cases, caseIdx)
	}
	call.Block = fallbackBlock
	fallbackJump := &Instr{ID: fn.newValueID(), Op: OpJump, Block: fallbackBlock}
	fallbackJump.copySourceFrom(call)
	fallbackInstrs = append(fallbackInstrs, call, fallbackJump)
	fallbackBlock.Instrs = fallbackInstrs
	fallbackBlock.Succs = []*Block{mergeBlock}

	mergeBlock.Preds = []*Block{caseBlock, fallbackBlock}
	phi := &Instr{
		ID:    fn.newValueID(),
		Op:    OpPhi,
		Type:  call.Type,
		Args:  []*Value{caseCall.Value(), call.Value()},
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

func removeInstrByID(instrs []*Instr, id int) []*Instr {
	out := instrs[:0]
	for _, instr := range instrs {
		if instr != nil && instr.ID == id {
			continue
		}
		out = append(out, instr)
	}
	return out
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
		argFacts := map[int]FixedShapeTableFact{0: c.ReceiverFact}
		abi := AnalyzeTypedPeerABIWithArgFacts(c.VMProto, argFacts)
		if !abi.Eligible || len(abi.Params) != len(call.Args) || abi.Params[0] != SpecializedABIParamRawTablePtr {
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d proto=%s is not native-typed-peer eligible: %s", c.ShapeID, c.VMProto.Name, abi.RejectWhy))
			continue
		}
		switch abi.Return {
		case SpecializedABIReturnRawInt, SpecializedABIReturnRawFloat:
		default:
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d proto=%s has unsupported typed-peer return %s", c.ShapeID, c.VMProto.Name, specializedABIReturnName(abi.Return)))
			continue
		}
		paramOK := true
		for i, rep := range abi.Params {
			switch rep {
			case SpecializedABIParamRawInt:
				if !callABIValueIsInt(call.Args[i]) {
					paramOK = false
				}
			case SpecializedABIParamRawTablePtr:
				if i != 0 && !callABIValueIsTable(call.Args[i]) {
					paramOK = false
				}
			default:
				paramOK = false
			}
			if !paramOK {
				break
			}
		}
		if !paramOK {
			functionRemarks(fn).Add("FieldShapeCallSplit", "missed", block.ID, call.ID, call.Op,
				fmt.Sprintf("case shape=%d proto=%s typed-peer ABI does not match current call values", c.ShapeID, c.VMProto.Name))
			continue
		}
		fieldShapeSplitCase(fn, block, idx, call, c, cases, caseIdx)
		functionRemarks(fn).Add("FieldShapeCallSplit", "changed", block.ID, call.ID, call.Op,
			fmt.Sprintf("split shape=%d proto=%s monomorphic method case", c.ShapeID, c.VMProto.Name))
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
		if !fieldShapeSplitInlineInstrSafe(instr) {
			return fmt.Sprintf("op %s needs callee-frame exit/deopt recovery", instr.Op)
		}
	}
	return ""
}

func fieldShapeSplitInlineInstrSafe(instr *Instr) bool {
	if instr == nil {
		return false
	}
	if instr.Op == OpGetField || instr.Op == OpGetFieldNumToFloat {
		return instr.Aux2 != 0
	}
	if instr.Op == OpSetField {
		return fieldSvalsSetFieldPreservesShape(instr)
	}
	if instr.Op == OpBranch || instr.Op == OpJump || instr.Op == OpPhi {
		return true
	}
	if instr.Op == OpTableArrayHeader || instr.Op == OpTableArrayLen ||
		instr.Op == OpTableArrayData || instr.Op == OpTableArrayLoad ||
		instr.Op == OpTableArrayStore {
		return true
	}
	return fieldShapeSplitInlineOpSafe(instr.Op)
}

func fieldShapeSplitInlineOpSafe(op Op) bool {
	switch op {
	case OpConstInt, OpConstFloat, OpConstBool, OpConstNil, OpConstString,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpEqInt, OpLtInt, OpLeInt, OpEqString, OpLtFloat, OpLeFloat,
		OpFloor, OpNumToFloat, OpFieldSvals, OpFieldLoad, OpFieldLoadNumToFloat,
		OpFieldPolyLen,
		OpGuardType, OpGuardIntRange, OpGuardCalleeProto:
		return true
	default:
		return false
	}
}

func fieldShapeSplitCase(fn *Function, block *Block, idx int, call *Instr, c FieldPolyShapeCase, cases []FieldPolyShapeCase, caseIdx int) {
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

	caseCall := &Instr{
		ID:        fn.newValueID(),
		Op:        OpFieldCallFloor,
		Type:      call.Type,
		Args:      append([]*Value(nil), call.Args...),
		Aux:       call.Aux,
		Aux2:      call.Aux2,
		Block:     caseBlock,
		HasSource: call.HasSource,
		SourcePC:  call.SourcePC,
	}
	caseCall.copySourceFrom(call)
	caseBlock.Instrs = append(caseBlock.Instrs, caseCall)
	fn.FieldPolyShapeFacts[caseCall.ID] = []FieldPolyShapeCase{c}
	caseResult := caseCall.Value()
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
