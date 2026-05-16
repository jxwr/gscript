//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// canPromoteToTier2 checks if a function is safe for Tier 2 compilation.
//
// All standard ops are now handled by Tier 2, either natively or via exit-resume:
//
// Native ARM64 fast paths:
//   - Arithmetic, comparison, unary: emitRawIntBinOp / emitFloatBinOp / etc.
//   - GETTABLE, SETTABLE: emitGetTableNative / emitSetTableNative
//   - GETFIELD, SETFIELD: emitGetField / emitSetField (inline cache + shape guard)
//   - GETGLOBAL: emitGetGlobalNative (per-instruction value cache + exit-resume)
//
// Native + exit-resume fallback:
//   - CALL: eliminated by inline pass; if not inlined, compileTier2 rejects via irHasCall
//
// Exit-resume (exit to Go, execute, resume JIT):
//   - SETGLOBAL, NEWTABLE, SETLIST, APPEND, LEN, CONCAT, SELF, POW: emitOpExit
//   - CLOSURE, GETUPVAL, SETUPVAL: emitOpExit with closure state from VM
//   - VARARG: emitOpExit with vararg state from VM frame
//
// Only goroutine/channel ops are blocked (fundamentally require Go runtime):
//   - GO, MAKECHAN, SEND, RECV
//
// CALL is no longer blocked here. Instead, compileTier2 runs the inline pass to
// eliminate calls, then checks the optimized IR with irHasCall. If calls remain
// after inlining, the function falls back to Tier 1 where BLR calls are faster.
// GETGLOBAL is fully native with a per-instruction value cache matching Tier 1.
func canPromoteToTier2(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		// Goroutine/channel ops (not in Tier 2):
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			return false
		}
	}
	return true
}

func firstUnsupportedTier2Bytecode(proto *vm.FuncProto) (string, bool) {
	gate := firstUnsupportedTier2BytecodeGate(proto)
	return gate.Reason, !gate.Allowed
}

func firstUnsupportedTier2BytecodeGate(proto *vm.FuncProto) GateResult {
	if proto == nil {
		return allowGate("Tier2Bytecode", "no proto")
	}
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			return blockGate("Tier2Bytecode", vm.OpName(op))
		}
	}
	return allowGate("Tier2Bytecode", "all bytecodes supported")
}

// feedbackHasObservations returns true if any entry has a non-Unobserved
// Left, Right, or Result. Used by feedback gates to delay Tier 2 compilation
// until feedback has had a chance to fill.
func feedbackHasObservations(fv []vm.TypeFeedback) bool {
	for i := range fv {
		if fv[i].Left != vm.FBUnobserved || fv[i].Right != vm.FBUnobserved ||
			fv[i].Result != vm.FBUnobserved || fv[i].Kind != vm.FBKindUnobserved {
			return true
		}
	}
	return false
}

// canPromoteToTier2NoCalls is the conservative version of canPromoteToTier2
// that also blocks CALL. Used by shouldPromoteTier2 to identify pure-compute
// functions that don't need the inline pass. GETGLOBAL is allowed because
// Tier 2 has a per-instruction value cache matching Tier 1's performance.
func canPromoteToTier2NoCalls(proto *vm.FuncProto) bool {
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CALL:
			return false
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			return false
		}
	}
	return true
}

// isOSRRestartSafe reports whether the current restart-style OSR can be used
// for proto. Tier 1 OSR exits after part of the loop has already executed, and
// handleOSR restarts the function from bytecode PC 0 at Tier 2. That is only
// correct when replaying the prefix cannot repeat externally visible effects.
//
// This is intentionally based on post-pipeline IR, not source bytecode. Some
// important loops (object_creation's create_and_sum/transform_chain) contain
// source-level calls and NewTable ops, but Inline + EscapeAnalysis fully
// virtualize them into pure numeric loops. Those are restart-safe. By contrast,
// table update loops such as table_field_access.step still contain residual
// GetTable/SetField/table exits after optimization and must not use restart OSR.
func (tm *TieringManager) isOSRRestartSafe(proto *vm.FuncProto, profile FuncProfile) bool {
	return tm.osrRestartSafetyGate(proto, profile).Allowed
}

func (tm *TieringManager) osrRestartSafetyGate(proto *vm.FuncProto, profile FuncProfile) GateResult {
	if proto == nil || !profile.HasLoop {
		return blockGate("OSRRestartSafety", "function has no restartable loop")
	}
	if profile.HasClosure || profile.HasUpval || profile.HasVararg {
		return blockGate("OSRRestartSafety", "function contains closure/upvalue/vararg state")
	}

	fn := BuildGraph(proto)
	if fn.Unpromotable {
		return blockGate("OSRRestartSafety", "graph is unpromotable")
	}
	if errs := Validate(fn); len(errs) > 0 {
		return blockGate("OSRRestartSafety", "initial graph validation failed")
	}

	inlineGlobals := tm.buildInlineGlobals()
	loopCallGlobals := inlineGlobals
	if protoGlobals := buildProtoInlineGlobals(proto); len(protoGlobals) > 0 {
		loopCallGlobals = make(map[string]*vm.FuncProto, len(inlineGlobals)+len(protoGlobals))
		for name, calleeProto := range inlineGlobals {
			loopCallGlobals[name] = calleeProto
		}
		for name, calleeProto := range protoGlobals {
			if _, ok := loopCallGlobals[name]; !ok {
				loopCallGlobals[name] = calleeProto
			}
		}
	}
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{InlineGlobals: inlineGlobals, InlineMaxSize: inlineMaxCalleeSize})
	if err != nil {
		return blockGate("OSRRestartSafety", err.Error())
	}
	if op, ok := firstExitResumeInLoop(fn, loopCallGlobals); ok {
		return blockGateOp("OSRRestartSafety", "optimized loop still needs exit/resume", op)
	}
	if hasRestartVisibleSideEffect(fn) {
		return blockGate("OSRRestartSafety", "optimized body has restart-visible side effects")
	}
	return allowGate("OSRRestartSafety", "restart OSR is safe")
}

func hasRestartVisibleSideEffect(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpCall,
				OpSetGlobal,
				OpSetTable, OpTableArrayStore, OpTableArraySwap, OpTableArraySwapPairs, OpSetField,
				OpNewTable, OpNewFixedTable, OpSetList, OpAppend,
				OpSelf,
				OpSetUpval,
				OpGo, OpMakeChan, OpSend, OpRecv,
				OpClosure, OpClose,
				OpVararg,
				OpConcat, OpLen, OpPow,
				OpTForCall, OpTForLoop:
				return true
			}
		}
	}
	return false
}

// canPromoteWithInlining checks if a function whose only blocker is OP_CALL
// (performance-blocked) can be promoted by inlining all calls. Returns true if
// ALL calls are to known, small, non-recursive global functions. The inline
// pass eliminates those calls, removing the performance blocker. GETGLOBAL is
// allowed regardless (Tier 2 has native value cache).
func canPromoteWithInlining(proto *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if len(globals) == 0 {
		return false
	}
	hasCall := false
	for i, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_CALL:
			hasCall = true
			callA := vm.DecodeA(inst)
			if !findInlineableGetGlobal(proto, i, callA, globals) {
				return false
			}
		case vm.OP_GETGLOBAL:
			// GETGLOBAL is needed for CALL resolution — allowed
			continue
		case vm.OP_GO, vm.OP_MAKECHAN, vm.OP_SEND, vm.OP_RECV:
			// Goroutine/channel ops not in Tier 2
			return false
		}
	}
	return hasCall
}

// findInlineableGetGlobal scans backwards from callPC to find the GETGLOBAL
// that loads the function into register targetReg. Returns true if the callee
// is in globals, small enough, and non-recursive.
func findInlineableGetGlobal(proto *vm.FuncProto, callPC, targetReg int, globals map[string]*vm.FuncProto) bool {
	callee, ok := findGetGlobalCallee(proto, callPC, targetReg, globals)
	if !ok {
		return false
	}
	// Check size budget.
	if len(callee.Code) > inlineMaxCalleeSize {
		return false
	}
	// Recursion: permitted when bounded by MaxRecursion in the
	// inline pass (R31 bounded recursive inline ADR). The pass
	// caps unrolling depth so self/mutual recursion stays finite.
	// The name-match + isRecursive checks that previously
	// rejected here have moved responsibility onto the pass's
	// MaxRecursion gate. Non-bounded recursion would still blow
	// the inline budget naturally (per-iteration size growth).
	//
	// Check callee has no loops (while-loops produce buggy
	// code when inlined into the caller's IR).
	return !analyzeFuncProfile(callee).HasLoop
}

func findGetGlobalCallee(proto *vm.FuncProto, callPC, targetReg int, globals map[string]*vm.FuncProto) (*vm.FuncProto, bool) {
	for j := callPC - 1; j >= 0; j-- {
		prev := proto.Code[j]
		prevOp := vm.DecodeOp(prev)
		if prevOp == vm.OP_GETGLOBAL && vm.DecodeA(prev) == targetReg {
			bx := vm.DecodeBx(prev)
			if bx < 0 || bx >= len(proto.Constants) {
				return nil, false
			}
			name := proto.Constants[bx].Str()
			callee, ok := globals[name]
			if !ok {
				return nil, false
			}
			return callee, true
		}
		// If another instruction writes to targetReg before we find GETGLOBAL,
		// the function reference is not from a GETGLOBAL. Bail out.
		if prevOp != vm.OP_GETGLOBAL && vm.DecodeA(prev) == targetReg {
			return nil, false
		}
	}
	return nil, false
}

func (tm *TieringManager) shouldPromoteNativeLoopDriver(proto *vm.FuncProto, profile FuncProfile) bool {
	if tm == nil || proto == nil || proto.CallCount != 1 {
		return false
	}
	if !profile.HasLoop || profile.LoopDepth != 1 {
		return false
	}
	if proto.Name != "<main>" && (profile.HasClosure || profile.HasUpval || profile.HasVararg) {
		return false
	}
	globals := tm.buildLoopCallGlobals(proto)
	if proto.Name == "<main>" && mainNativeLoopDriverCallsAllocatingCallee(proto, globals) {
		return false
	}
	if proto.Name == "<main>" && mainProtoHasRecursiveChild(proto) {
		trip, ok := mainMaxConstantForLoopTrip(proto)
		if ok && trip < 10000 {
			return false
		}
	}
	return canPromoteWithNativeLoopCalls(proto, globals)
}

func mainNativeLoopDriverCallsAllocatingCallee(proto *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if proto == nil || proto.Name != "<main>" || len(globals) == 0 {
		return false
	}
	inLoop := staticLoopPCs(proto)
	callSites := 0
	allocatingSites := 0
	for pc, inst := range proto.Code {
		if !inLoop[pc] || vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		callSites++
		callee, ok := findGetGlobalCallee(proto, pc, vm.DecodeA(inst), globals)
		if !ok || callee == nil {
			continue
		}
		if protoHasAllocationBytecode(callee) {
			allocatingSites++
		}
	}
	return callSites == 1 && allocatingSites == 1
}

func protoHasAllocationBytecode(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_NEWTABLE, vm.OP_SETLIST:
			return true
		}
	}
	return false
}

func protoHasNativeCallUnsafeTableBytecode(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN, vm.OP_SETLIST,
			vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_APPEND:
			return true
		}
	}
	return false
}

func protoHasAllocationBytecodeInLoop(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	inLoop := staticLoopPCs(proto)
	for pc, inst := range proto.Code {
		if !inLoop[pc] {
			continue
		}
		switch vm.DecodeOp(inst) {
		case vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN, vm.OP_SETLIST:
			return true
		}
	}
	return false
}

func protoHasAllocationIRInLoop(proto *vm.FuncProto) bool {
	fn := BuildGraph(proto)
	if fn == nil || fn.Entry == nil {
		return true
	}
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
			case OpNewTable, OpNewFixedTable, OpSetList:
				return true
			}
		}
	}
	return false
}

func (tm *TieringManager) shouldSuppressMainLoopCallTier2(proto *vm.FuncProto, profile FuncProfile) bool {
	if tm == nil || tm.envTier2NoFilter || proto == nil || proto.Name != "<main>" {
		return false
	}
	return tm.shouldSuppressLoopCallTier2(proto, profile)
}

func (tm *TieringManager) shouldSuppressLoopCallTier2(proto *vm.FuncProto, profile FuncProfile) bool {
	if tm == nil || tm.envTier2NoFilter || proto == nil {
		return false
	}
	if !profile.HasLoop || profile.LoopDepth >= 2 || profile.CallCount == 0 || !hasStaticCallInLoop(proto) {
		return false
	}
	if hasGenericStringFormatIntCall(proto) {
		return false
	}
	if hasStringSplitScalarFusionCandidate(proto) {
		return false
	}
	globals := tm.buildLoopCallGlobals(proto)
	return !canPromoteWithInlining(proto, globals) && !canPromoteWithNativeLoopCalls(proto, globals)
}

func stableNumericGlobals(proto *vm.FuncProto) map[string]int64 {
	nums := make(map[string]int64)
	regNums := make(map[int]int64)
	invalid := make(map[string]bool)
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_LOADINT:
			regNums[a] = int64(vm.DecodesBx(inst))
		case vm.OP_LOADK:
			bx := vm.DecodeBx(inst)
			if bx >= 0 && bx < len(proto.Constants) {
				if n, ok := staticIntConstant(proto.Constants[bx]); ok {
					regNums[a] = n
				} else {
					delete(regNums, a)
				}
			} else {
				delete(regNums, a)
			}
		case vm.OP_SETGLOBAL:
			name := protoConstString(proto, vm.DecodeBx(inst))
			if name == "" || invalid[name] {
				continue
			}
			n, ok := regNums[a]
			if !ok {
				invalid[name] = true
				delete(nums, name)
				continue
			}
			if prev, exists := nums[name]; exists && prev != n {
				invalid[name] = true
				delete(nums, name)
				continue
			}
			nums[name] = n
		default:
			delete(regNums, a)
		}
	}
	return nums
}

func staticForTripCount(proto *vm.FuncProto, globalNums map[string]int64, pc, a int) (int64, bool) {
	if pc < 3 {
		return 0, false
	}
	init, ok := staticIntValueForReg(proto, globalNums, proto.Code[pc-3], a)
	if !ok {
		return 0, false
	}
	limit, ok := staticIntValueForReg(proto, globalNums, proto.Code[pc-2], a+1)
	if !ok {
		return 0, false
	}
	step, ok := staticIntValueForReg(proto, globalNums, proto.Code[pc-1], a+2)
	if !ok || step <= 0 || init > limit {
		return 0, false
	}
	return (limit-init)/step + 1, true
}

func staticIntValueForReg(proto *vm.FuncProto, globalNums map[string]int64, inst uint32, reg int) (int64, bool) {
	if vm.DecodeA(inst) != reg {
		return 0, false
	}
	switch vm.DecodeOp(inst) {
	case vm.OP_LOADINT:
		return int64(vm.DecodesBx(inst)), true
	case vm.OP_LOADK:
		bx := vm.DecodeBx(inst)
		if bx >= 0 && bx < len(proto.Constants) {
			return staticIntConstant(proto.Constants[bx])
		}
	case vm.OP_GETGLOBAL:
		name := protoConstString(proto, vm.DecodeBx(inst))
		if name != "" {
			n, ok := globalNums[name]
			return n, ok
		}
	}
	return 0, false
}

func staticIntConstant(v runtime.Value) (int64, bool) {
	if v.IsInt() {
		return v.Int(), true
	}
	if v.IsFloat() {
		f := v.Float()
		i := int64(f)
		if float64(i) == f {
			return i, true
		}
	}
	return 0, false
}

func (tm *TieringManager) shouldSuppressRecursivePartitionTableMutationTier2(proto *vm.FuncProto, profile FuncProfile) bool {
	if tm == nil || tm.envTier2NoFilter || proto == nil {
		return false
	}
	return hasStaticSelfRecursivePartitionSetTableLoop(proto)
}

// tier0OnlyLoopCallee reports stable loop callees that are deliberately kept
// in the interpreter. Compiling the driver around that callee creates a mixed
// Tier1/Tier0 path: every hot call exits Tier1, re-enters the VM, and then
// immediately declines to compile the callee. For driver loops this can be
// slower than keeping the whole driver interpreted.
func (tm *TieringManager) tier0OnlyLoopCallee(proto *vm.FuncProto, profile FuncProfile) (*vm.FuncProto, bool) {
	if tm == nil || proto == nil || !profile.HasLoop || profile.CallCount == 0 || !hasStaticCallInLoop(proto) {
		return nil, false
	}
	globals := tm.buildLoopCallGlobals(proto)
	if len(globals) == 0 {
		return nil, false
	}
	inLoop := staticLoopPCs(proto)
	for pc, inst := range proto.Code {
		if !inLoop[pc] || vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		callee, ok := findGetGlobalCallee(proto, pc, vm.DecodeA(inst), globals)
		if !ok || callee == nil {
			continue
		}
		if tm.isTier0OnlyCallee(callee) {
			return callee, true
		}
	}
	return nil, false
}

func (tm *TieringManager) isTier0OnlyCallee(callee *vm.FuncProto) bool {
	if callee == nil {
		return false
	}
	if callee.JITDisabled {
		return true
	}
	if tm.shouldSuppressRecursivePartitionTableMutationTier2(callee, tm.getProfile(callee)) {
		return true
	}
	if vm.IsSieveKernelProto(callee) {
		return true
	}
	profile := tm.getProfile(callee)
	return shouldStayTier0ForProto(callee, profile) || shouldStayTier0RecursiveTableWalker(callee, profile)
}
