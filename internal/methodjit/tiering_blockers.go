//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func tier2GenericModIsNativeNumeric(instr *Instr) bool {
	if instr == nil || instr.Op != OpMod || len(instr.Args) < 2 {
		return false
	}
	if instr.Type == TypeFloat {
		return true
	}
	return tier2ValueIsNativeNumeric(instr.Args[0], make(map[int]bool)) &&
		tier2ValueIsNativeNumeric(instr.Args[1], make(map[int]bool))
}

func tier2ValueIsNativeNumeric(v *Value, seen map[int]bool) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if v.Def.Type == TypeInt || v.Def.Type == TypeFloat {
		return true
	}
	if seen[v.ID] {
		return true
	}
	seen[v.ID] = true

	switch v.Def.Op {
	case OpConstInt, OpConstFloat, OpUnboxInt, OpUnboxFloat:
		return true
	case OpGuardType:
		t := Type(v.Def.Aux)
		return t == TypeInt || t == TypeFloat
	case OpGuardIntRange:
		return v.Def.Type == TypeInt && len(v.Def.Args) == 1 &&
			tier2ValueIsNativeNumeric(v.Def.Args[0], seen)
	case OpPhi:
		return tier2AllValuesNativeNumeric(v.Def.Args, seen)
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpUnm,
		OpAddInt, OpSubInt, OpMulInt, OpModInt, OpNegInt,
		OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat, OpNegFloat,
		OpFloor:
		return tier2AllValuesNativeNumeric(v.Def.Args, seen)
	default:
		return false
	}
}

func tier2AllValuesNativeNumeric(values []*Value, seen map[int]bool) bool {
	if len(values) == 0 {
		return false
	}
	for _, arg := range values {
		if !tier2ValueIsNativeNumeric(arg, seen) {
			return false
		}
	}
	return true
}

func tier2GenericModIsSmallConstAdditiveLoopCounter(instr *Instr) bool {
	if instr == nil || instr.Op != OpMod || len(instr.Args) < 2 {
		return false
	}
	if instr.Type == TypeFloat {
		return false
	}
	divisor := instr.Args[1]
	if divisor == nil || divisor.Def == nil || divisor.Def.Op != OpConstInt {
		return false
	}
	if divisor.Def.Aux == 0 || divisor.Def.Aux < -16 || divisor.Def.Aux > 16 {
		return false
	}
	return tier2ValueIsAdditiveIntLike(instr.Args[0], make(map[int]bool))
}

func tier2ValueIsAdditiveIntLike(v *Value, seen map[int]bool) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if v.Def.Type == TypeFloat {
		return false
	}
	if seen[v.ID] {
		return true
	}
	seen[v.ID] = true

	switch v.Def.Op {
	case OpConstInt, OpUnboxInt:
		return true
	case OpGuardType:
		return v.Def.Type == TypeInt && len(v.Def.Args) == 1 &&
			tier2ValueIsAdditiveIntLike(v.Def.Args[0], seen)
	case OpGuardIntRange:
		return v.Def.Type == TypeInt && len(v.Def.Args) == 1 &&
			tier2ValueIsAdditiveIntLike(v.Def.Args[0], seen)
	case OpPhi:
		return tier2AllValuesAdditiveIntLike(v.Def.Args, seen)
	case OpAdd, OpAddInt:
		return tier2SmallConstPlusAdditive(v.Def.Args, seen)
	case OpSub, OpSubInt:
		return tier2AdditiveMinusSmallConst(v.Def.Args, seen)
	default:
		return false
	}
}

func tier2AllValuesAdditiveIntLike(values []*Value, seen map[int]bool) bool {
	if len(values) == 0 {
		return false
	}
	for _, arg := range values {
		if !tier2ValueIsAdditiveIntLike(arg, seen) {
			return false
		}
	}
	return true
}

func tier2SmallConstPlusAdditive(args []*Value, seen map[int]bool) bool {
	if len(args) < 2 {
		return false
	}
	if tier2ValueIsSmallConst(args[0], 16) {
		return tier2ValueIsAdditiveIntLike(args[1], seen)
	}
	if tier2ValueIsSmallConst(args[1], 16) {
		return tier2ValueIsAdditiveIntLike(args[0], seen)
	}
	return false
}

func tier2AdditiveMinusSmallConst(args []*Value, seen map[int]bool) bool {
	if len(args) < 2 {
		return false
	}
	return tier2ValueIsAdditiveIntLike(args[0], seen) &&
		tier2ValueIsSmallConst(args[1], 16)
}

func tier2ValueIsSmallConst(v *Value, limit int64) bool {
	if v == nil || v.Def == nil || v.Def.Op != OpConstInt {
		return false
	}
	c := v.Def.Aux
	return c >= -limit && c <= limit
}

func firstExitResumeInLoop(fn *Function, globals map[string]*vm.FuncProto) (Op, bool) {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				if tier2LoopCallIsNativeCandidate(fn, instr, globals) {
					continue
				}
				return instr.Op, true
			case OpGetUpval:
				if len(instr.Args) > 0 {
					continue
				}
				return instr.Op, true
			case OpSetUpval:
				if len(instr.Args) > 1 {
					continue
				}
				return instr.Op, true
			case OpSelf,
				OpNewTable, OpNewFixedTable,
				OpGetTable, OpSetTable,
				OpConcat, OpAppend, OpSetList,
				OpGo, OpMakeChan, OpSend, OpRecv,
				OpClosure, OpClose,
				OpVararg,
				OpLen, OpPow,
				OpTForCall, OpTForLoop:
				return instr.Op, true
			}
		}
	}
	return OpNop, false
}

func firstUnsupportedHighArityCallResultShapeInLoop(fn *Function) (Op, bool) {
	gate := firstUnsupportedHighArityCallResultShapeInLoopGate(fn)
	return gate.Op, !gate.Allowed
}

func firstUnsupportedHighArityCallResultShapeInLoopGate(fn *Function) GateResult {
	const maxSimpleCallArgs = 3
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				// Simple string.format-style loop calls are covered by existing
				// no-filter coverage. The unsafe log-tokenize case is a high-arity
				// inlined vararg call whose source shape no longer belongs to fn.Proto.
				if len(instr.Args)-1 <= maxSimpleCallArgs {
					continue
				}
				nRets, ok := callExactFixedResultCountFromC(instr.Aux2)
				if !ok || !callABIHasExactResultShape(fn, instr, nRets) {
					return blockGateOp("HighArityCallResultShape", "high-arity loop call exit lacks a fixed result shape", instr.Op)
				}
			case OpStringFormatConst:
				// StringFormatConst has its own precise op-exit protocol. Unlike
				// generic CallExit it always writes one IR result slot, independent
				// of the source CALL site that was inlined into this function.
			}
		}
	}
	return allowGate("HighArityCallResultShape", "no high-arity loop result-shape blocker")
}

func tier2BlockIsModuloColdBranch(block *Block) bool {
	if block == nil || len(block.Preds) != 1 {
		return false
	}
	pred := block.Preds[0]
	if pred == nil || len(pred.Instrs) == 0 {
		return false
	}
	term := pred.Instrs[len(pred.Instrs)-1]
	if term == nil || term.Op != OpBranch || len(term.Args) == 0 || term.Args[0] == nil ||
		term.Args[0].Def == nil || term.Args[0].Def.Op != OpModZeroInt {
		return false
	}
	divisor := term.Args[0].Def.Aux
	if divisor < 0 {
		divisor = -divisor
	}
	return divisor >= 100
}

func firstCallBoundaryTier2BlockerInLoop(fn *Function, globals map[string]*vm.FuncProto) (Op, bool) {
	gate := firstCallBoundaryTier2BlockerInLoopGate(fn, globals)
	return gate.Op, !gate.Allowed
}

func firstCallBoundaryTier2BlockerInLoopGate(fn *Function, globals map[string]*vm.FuncProto) GateResult {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall:
				if tier2LoopCallIsNativeCandidate(fn, instr, globals) {
					continue
				}
				return blockGateOp("CallBoundaryLoop", "non-native OpCall remains inside loop after inlining", instr.Op)
			case OpGetUpval:
				if len(instr.Args) > 0 {
					continue
				}
				return blockGateOp("CallBoundaryLoop", "performance-blocked op remains inside loop", instr.Op)
			case OpSetUpval:
				if len(instr.Args) > 1 {
					continue
				}
				return blockGateOp("CallBoundaryLoop", "performance-blocked op remains inside loop", instr.Op)
			case OpSelf,
				OpConcat, OpAppend, OpSetList,
				OpGo, OpMakeChan, OpSend, OpRecv,
				OpClosure, OpClose,
				OpVararg,
				OpPow,
				OpTForCall, OpTForLoop:
				return blockGateOp("CallBoundaryLoop", "performance-blocked op remains inside loop", instr.Op)
			case OpNewTable:
				if tier2NewTableLoopCandidateIsSafe(instr) {
					continue
				}
				return blockGateOp("CallBoundaryLoop", "uncached table allocation remains inside loop", instr.Op)
			case OpNewFixedTable:
				if tier2NewFixedTableLoopCandidateIsSafe(fn, instr) {
					continue
				}
				return blockGateOp("CallBoundaryLoop", "uncached fixed-table allocation remains inside loop", instr.Op)
			case OpSetTable:
				if tier2SetTableLoopCandidateIsSafe(fn, instr) {
					continue
				}
				return blockGateOp("CallBoundaryLoop", "dynamic table mutation remains inside loop", instr.Op)
			}
		}
	}
	return allowGate("CallBoundaryLoop", "no call-boundary loop blocker")
}

func firstLoopAllocationBlockerGate(fn *Function) GateResult {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			switch instr.Op {
			case OpNewTable:
				if tier2LoopNewTableDirectEntryIsSafe(instr) {
					continue
				}
				return blockGateOp("LoopAllocation", "table allocation remains inside loop", instr.Op)
			case OpNewFixedTable:
				return blockGateOp("LoopAllocation", "fixed-table allocation remains inside loop", instr.Op)
			case OpSetList:
				return blockGateOp("LoopAllocation", "setlist allocation-style initialization remains inside loop", instr.Op)
			}
		}
	}
	return allowGate("LoopAllocation", "no loop allocation blocker")
}

func hasReadWriteGlobalInSameLoop(fn *Function) bool {
	return !readWriteGlobalInSameLoopGate(fn).Allowed
}

func readWriteGlobalInSameLoopGate(fn *Function) GateResult {
	li := computeLoopInfo(fn)
	for _, blocks := range li.headerBlocks {
		read := make(map[int64]bool)
		write := make(map[int64]bool)
		for _, block := range fn.Blocks {
			if !blocks[block.ID] {
				continue
			}
			for _, instr := range block.Instrs {
				switch instr.Op {
				case OpGetGlobal:
					read[instr.Aux] = true
				case OpSetGlobal:
					write[instr.Aux] = true
				}
			}
		}
		for nameIdx := range write {
			if read[nameIdx] {
				return blockGateOp("LoopGlobalState", "loop reads and writes the same global", OpSetGlobal)
			}
		}
	}
	return allowGate("LoopGlobalState", "no loop read/write global overlap")
}

func tier2NewTableLoopCandidateIsSafe(instr *Instr) bool {
	return false
}

func tier2LoopNewTableDirectEntryIsSafe(instr *Instr) bool {
	if instr == nil || instr.Op != OpNewTable || newTableCacheBatchSize(instr) <= 1 {
		return false
	}
	_, kind := unpackNewTableAux2(instr.Aux2)
	return kind != runtime.ArrayBool
}

func tier2NewFixedTableLoopCandidateIsSafe(fn *Function, instr *Instr) bool {
	// Fixed-shape constructors have the same direct-entry property as cached
	// NEWTABLE sites: the common path reuses a cached table and only misses
	// through the table-exit continuation once per cache refill batch.
	if fn == nil || instr == nil || instr.Op != OpNewFixedTable {
		return false
	}
	return fixedTableCtor2Cacheable(fn.Proto, instr) || fixedTableCtorNCacheable(fn.Proto, instr)
}

func hasIndexedGlobalLoopProtocol(fn *Function) bool {
	if !fnSupportsNativeSetGlobalProtocol(fn) {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpGetGlobal && instr.Op != OpSetGlobal {
				continue
			}
			constIdx := int(instr.Aux)
			if constIdx < 0 || constIdx >= len(fn.Proto.Constants) || !fn.Proto.Constants[constIdx].IsString() {
				return false
			}
		}
	}
	return true
}

func firstSelfRecursiveTableMutationInLoop(fn *Function) (Op, bool) {
	gate := firstSelfRecursiveTableMutationInLoopGate(fn)
	return gate.Op, !gate.Allowed
}

func firstSelfRecursiveTableMutationInLoopGate(fn *Function) GateResult {
	if !irHasSelfCall(fn) {
		return allowGate("SelfRecursiveTableMutation", "not self-recursive")
	}
	summary := analyzeLoopTableMutationRecovery(fn)
	if site, ok := summary.firstUnadmitted(); ok {
		return blockGateOp("SelfRecursiveTableMutation", "self-recursive loop has residual table mutation", site.Op)
	}
	return allowGate("SelfRecursiveTableMutation", "self-recursive table mutations are recoverable")
}

func tier2SetTableLoopCandidateIsSafe(fn *Function, instr *Instr) bool {
	if irHasSelfCall(fn) {
		return loopTableMutationRecoveryAdmitsInstr(fn, instr)
	}
	if isLocalTableRowSetTable(instr) {
		return true
	}
	if isProfiledStringSetTableLoopCandidate(fn, instr) {
		return true
	}
	// Aux2 carries monomorphic array-kind feedback from Tier 1. Only typed
	// arrays get the Tier 2 append/write fast path; Mixed stores remain too
	// broad because they can carry pointers and rely more on runtime table
	// growth/absorb behavior.
	switch instr.Aux2 {
	case int64(vm.FBKindInt), int64(vm.FBKindFloat), int64(vm.FBKindBool):
		return true
	default:
		return isScalarArraySetTable(instr)
	}
}

func isLocalTableRowSetTable(instr *Instr) bool {
	if instr == nil || instr.Op != OpSetTable || len(instr.Args) < 3 {
		return false
	}
	if instr.Aux2 != 0 && instr.Aux2 != int64(vm.FBKindMixed) {
		return false
	}
	if !isIntLikeTableKey(instr.Args[1], make(map[int]bool)) {
		return false
	}
	tbl := instr.Args[0]
	val := instr.Args[2]
	if tbl == nil || tbl.Def == nil || tbl.Def.Op != OpNewTable {
		return false
	}
	return val != nil && val.Def != nil && val.Def.Type == TypeTable
}

func isProfiledStringSetTableLoopCandidate(fn *Function, instr *Instr) bool {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpSetTable || len(instr.Args) < 3 {
		return false
	}
	if !instr.HasSource || instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.Code) {
		return false
	}
	key := instr.Args[1]
	val := instr.Args[2]
	if key == nil || key.Def == nil || key.Def.Type != TypeString {
		return false
	}
	if val == nil || val.Def == nil || val.Def.Type == TypeNil {
		return false
	}
	if instr.SourcePC >= len(fn.Proto.TableKeyFeedback) {
		return false
	}
	fb := fn.Proto.TableKeyFeedback[instr.SourcePC]
	if fb.Count == 0 || fb.KeyType != vm.FBString || fb.Flags&vm.TableAccessMetatableSeen != 0 {
		return false
	}
	if fb.Flags&(vm.TableAccessAppendSeen|vm.TableAccessOverwriteSeen) == 0 || fb.Flags&vm.TableAccessSparseSeen != 0 {
		return false
	}
	return profiledStringSetTableCacheReady(fn.Proto, instr.SourcePC)
}

func profiledStringSetTableCacheReady(proto *vm.FuncProto, pc int) bool {
	if proto == nil || len(proto.TableStringKeyCache) == 0 || pc < 0 {
		return false
	}
	slot := runtime.TableStringKeyCacheSlot(proto.TableStringKeyCache, pc)
	for i := range slot {
		entry := &slot[i]
		if entry.ShapeID == 0 || entry.FieldIdx < 0 || entry.FieldIdx >= runtime.SmallFieldCap {
			continue
		}
		if entry.AppendShapeID != 0 && entry.AppendShape != nil {
			return true
		}
		if entry.KeyData != 0 && entry.KeyLen > 0 {
			return true
		}
	}
	return false
}

func hasStaticSelfRecursivePartitionSetTableLoop(proto *vm.FuncProto) bool {
	if proto == nil || !staticallyCallsOnlySelf(proto) {
		return false
	}
	inLoop := staticLoopPCs(proto)
	setTablesByTableReg := make(map[int]int)
	for pc, inst := range proto.Code {
		if pc >= len(inLoop) || !inLoop[pc] || vm.DecodeOp(inst) != vm.OP_SETTABLE {
			continue
		}
		tableReg := vm.DecodeA(inst)
		setTablesByTableReg[tableReg]++
		if setTablesByTableReg[tableReg] >= 2 {
			return true
		}
	}
	return false
}

func isScalarArraySetTable(instr *Instr) bool {
	if instr == nil || instr.Op != OpSetTable || len(instr.Args) < 3 {
		return false
	}
	key := instr.Args[1]
	val := instr.Args[2]
	if !isIntLikeTableKey(key, make(map[int]bool)) || val == nil || val.Def == nil {
		return false
	}
	switch val.Def.Type {
	case TypeInt, TypeFloat, TypeBool:
		return true
	case TypeString:
		return instr.Args[0] != nil && instr.Args[0].Def != nil && instr.Args[0].Def.Op == OpNewTable
	default:
		return tier2ValueIsNativeNumeric(val, make(map[int]bool))
	}
}

func isIntLikeTableKey(v *Value, seen map[int]bool) bool {
	if v == nil || v.Def == nil {
		return false
	}
	if v.Def.Type == TypeInt {
		return true
	}
	if seen[v.ID] {
		return true
	}
	seen[v.ID] = true
	switch v.Def.Op {
	case OpConstInt, OpUnboxInt:
		return true
	case OpAddInt, OpSubInt, OpMulInt, OpModInt, OpDivIntExact:
		return allIntLikeArgs(v.Def, seen)
	case OpAdd, OpSub, OpMul, OpMod:
		return allIntLikeArgs(v.Def, seen)
	case OpPhi:
		return allIntLikeArgs(v.Def, seen)
	default:
		return false
	}
}

func allIntLikeArgs(instr *Instr, seen map[int]bool) bool {
	if instr == nil || len(instr.Args) == 0 {
		return false
	}
	for _, arg := range instr.Args {
		if !isIntLikeTableKey(arg, seen) {
			return false
		}
	}
	return true
}

func tier2LoopCallIsNativeCandidate(fn *Function, instr *Instr, globals map[string]*vm.FuncProto) bool {
	if instr != nil && instr.Op == OpFieldCallFloor {
		return true
	}
	if instr != nil && len(instr.Args) > 0 && callCalleeIsFieldDispatchValue(instr.Args[0]) {
		return true
	}
	_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
	if tier2LoopCallCalleeIsNativeCandidate(callee, globals) {
		return true
	}
	return tier2LoopCallFeedbackIsNativeCandidate(fn, instr, globals)
}

func tier2LoopCallCalleeIsNativeCandidate(callee *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if tier2LoopCallCalleeHasTier2DirectEntry(callee) {
		return true
	}
	if callee != nil && tier2LoopCallCalleeCanTierUp(callee, globals) {
		return true
	}
	if callee != nil && staticallyCallsOnlySelf(callee) {
		ok, _ := qualifyForNumeric(callee)
		return ok
	}
	if callee != nil && tier2LoopCallCalleeIsLeafNativeCandidate(callee) {
		return true
	}
	if callee != nil && shouldStayTier1ForBoxedRawIntKernel(callee, analyzeFuncProfile(callee)) {
		return true
	}
	return false
}

func tier2LoopCallFeedbackIsNativeCandidate(fn *Function, instr *Instr, globals map[string]*vm.FuncProto) bool {
	protos := tier2LoopCallFeedbackVMProtos(fn, instr)
	if len(protos) == 0 {
		return false
	}
	nArgs := len(instr.Args) - 1
	for _, callee := range protos {
		if callee == nil || callee.IsVarArg || callee.NumParams != nArgs {
			return false
		}
		if !tier2LoopCallCalleeIsNativeCandidate(callee, globals) {
			return false
		}
	}
	return true
}

func tier2LoopCallFeedbackVMProtos(fn *Function, instr *Instr) []*vm.FuncProto {
	if fn == nil || fn.Proto == nil || instr == nil || (instr.Op != OpCall && instr.Op != OpCallFloor) ||
		len(instr.Args) == 0 || !instr.HasSource ||
		instr.SourcePC < 0 || instr.SourcePC >= len(fn.Proto.CallSiteFeedback) {
		return nil
	}
	fb := fn.Proto.CallSiteFeedback[instr.SourcePC]
	if fb.Count < wholeCallKernelMinStableObservations ||
		fb.Flags&vm.CallSiteArityPolymorphic != 0 ||
		int(fb.NArgs) != len(instr.Args)-1 ||
		fb.ResultArity != uint8(instr.Aux2) {
		return nil
	}
	return fb.MaturePolymorphicVMProtos(wholeCallKernelMinStableObservations, len(instr.Args)-1, uint8(instr.Aux2))
}

func tier2LoopCallCalleeHasTier2DirectEntry(callee *vm.FuncProto) bool {
	return callee != nil && callee.Tier2Promoted &&
		(callee.DirectEntryPtr != 0 || callee.Tier2DirectEntryPtr != 0)
}

func tier2LoopCallCalleeCanTierUp(callee *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if callee == nil || callee.IsVarArg {
		return false
	}
	if hasStaticSelfRecursivePartitionSetTableLoop(callee) {
		return false
	}
	if !canPromoteToTier2(callee) {
		return false
	}
	profile := analyzeFuncProfile(callee)
	if shouldStayTier0(profile) {
		return false
	}
	if profile.LoopDepth < 2 {
		if !profile.HasLoop {
			return false
		}
		return tier2LoopCallCalleePassesLoopDepth1Gate(callee, globals)
	}
	runtimeCallCount := callee.CallCount
	if runtimeCallCount < tmDefaultTier2Threshold {
		runtimeCallCount = tmDefaultTier2Threshold
	}
	return shouldPromoteTier2(callee, profile, runtimeCallCount)
}

func tier2LoopCallCalleePassesLoopDepth1Gate(callee *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	fn := BuildGraph(callee)
	if fn == nil || fn.Entry == nil || fn.Unpromotable {
		return false
	}
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals: globals,
		InlineMaxSize: inlineMaxCalleeSize,
	})
	if err != nil {
		return false
	}
	if _, ok := firstTier2ModBlockerInLoop(fn); ok {
		return false
	}
	if _, blocked := firstCallBoundaryTier2BlockerInLoop(fn, globals); blocked {
		return false
	}
	return true
}

func tier2LoopCallCalleeIsLeafNativeCandidate(callee *vm.FuncProto) bool {
	if callee == nil || callee.IsVarArg || len(callee.Code) > inlineMaxCalleeSize {
		return false
	}
	if !canPromoteToTier2(callee) {
		return false
	}
	profile := analyzeFuncProfile(callee)
	if profile.HasLoop {
		return false
	}
	for _, inst := range callee.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_LEN,
			vm.OP_CONCAT,
			vm.OP_APPEND,
			vm.OP_SETLIST,
			vm.OP_SELF,
			vm.OP_GETUPVAL,
			vm.OP_SETUPVAL,
			vm.OP_CLOSURE,
			vm.OP_VARARG,
			vm.OP_POW,
			vm.OP_TFORCALL,
			vm.OP_TFORLOOP,
			vm.OP_GO,
			vm.OP_MAKECHAN,
			vm.OP_SEND,
			vm.OP_RECV:
			return false
		}
	}
	return true
}
