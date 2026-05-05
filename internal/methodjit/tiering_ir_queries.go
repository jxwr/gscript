//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

// irHasSelfCall (R40) scans the optimized IR for an OpCall whose function
// argument is an OpGetGlobal of this proto's own name. Used to gate the
// t2_self_entry lightweight prologue — only emitted for self-recursive
// functions so non-recursive functions keep their unchanged insn count.
func irHasSelfCall(fn *Function) bool {
	if fn == nil || fn.Proto == nil || fn.Proto.Name == "" {
		return false
	}
	// Find the constant pool index of the proto's own name.
	nameIdx := int64(-1)
	for i, c := range fn.Proto.Constants {
		if c.IsString() && c.Str() == fn.Proto.Name {
			nameIdx = int64(i)
			break
		}
	}
	if nameIdx < 0 {
		return false
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if instr.Op != OpCall {
				continue
			}
			if len(instr.Args) == 0 || instr.Args[0] == nil || instr.Args[0].Def == nil {
				continue
			}
			callee := instr.Args[0].Def
			if callee.Op == OpGetGlobal && callee.Aux == nameIdx {
				return true
			}
		}
	}
	return false
}

// irHasCall scans the optimized IR for any remaining OpCall instructions.
// Used after the inline pass to determine if all calls were eliminated.
// If OpCall remains, the function should stay at Tier 1 where BLR calls
// are faster than Tier 2's exit-resume.
func irHasCall(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpCall {
				return true
			}
		}
	}
	return false
}

// irHasGetGlobal scans the optimized IR for any remaining OpGetGlobal
// instructions. Used after the inline pass + DCE to determine if global
// accesses remain. OpGetGlobal uses exit-resume which is slower than
// Tier 1's per-PC value cache.
func irHasGetGlobal(fn *Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGetGlobal {
				return true
			}
		}
	}
	return false
}

// hasCallInLoop reports whether any OpCall in the optimized IR resides in
// a block that is part of a loop. Tier 2 exit-resume for CALL is ~30-80ns
// vs Tier 1's native BLR at ~10ns; inside a hot loop this difference
// destroys performance, but outside loops (loop depth 0) it is amortized.
// Uses the existing loopInfo infrastructure (natural-loop detection via
// back-edges + dominator analysis) — the same loopBlocks set the emitter
// uses for raw-int loop mode.
func hasCallInLoop(fn *Function) bool {
	return hasExpensiveInLoop(fn, func(op Op) bool { return op == OpCall })
}

// hasStaticCallInLoop is the bytecode-side prefilter for OSR. It marks PCs
// covered by backward loop edges (FORLOOP and while-style JMP) and reports
// whether an OP_CALL falls inside one of those ranges. The full Tier 2 gate
// still uses SSA loopInfo after inline; this helper only avoids known-futile
// OSR restarts before the expensive path runs.
func hasStaticCallInLoop(proto *vm.FuncProto) bool {
	if proto == nil || len(proto.Code) == 0 {
		return false
	}
	inLoop := staticLoopPCs(proto)
	for pc, inst := range proto.Code {
		if inLoop[pc] && vm.DecodeOp(inst) == vm.OP_CALL {
			return true
		}
	}
	return false
}

func hasFieldDispatchCallInLoop(proto *vm.FuncProto) bool {
	fn := BuildGraph(proto)
	if fn == nil || fn.Entry == nil || fn.Unpromotable {
		return false
	}
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpCall && len(instr.Args) > 0 &&
				callCalleeIsFieldDispatchValue(instr.Args[0]) {
				return true
			}
		}
	}
	return false
}

func callCalleeIsFieldDispatchValue(v *Value) bool {
	if v == nil || v.Def == nil {
		return false
	}
	def := v.Def
	for def.Op == OpGuardType && len(def.Args) == 1 && def.Args[0] != nil && def.Args[0].Def != nil {
		def = def.Args[0].Def
	}
	switch def.Op {
	case OpSelf:
		return true
	case OpGetField:
		if len(def.Args) == 0 || def.Args[0] == nil || def.Args[0].Def == nil {
			return true
		}
		// Static library calls like math.floor should be handled by intrinsic
		// lowering before this gate. Do not admit unresolved global-field calls
		// as generic dynamic dispatch.
		return def.Args[0].Def.Op != OpGetGlobal
	default:
		return false
	}
}

func canPromoteWithNativeLoopCalls(proto *vm.FuncProto, globals map[string]*vm.FuncProto) bool {
	if proto == nil || len(globals) == 0 {
		return false
	}
	inLoop := staticLoopPCs(proto)
	sawCall := false
	for pc, inst := range proto.Code {
		if !inLoop[pc] || vm.DecodeOp(inst) != vm.OP_CALL {
			continue
		}
		sawCall = true
		callee, ok := findGetGlobalCallee(proto, pc, vm.DecodeA(inst), globals)
		if !ok || !tier2LoopCallCalleeIsNativeCandidate(callee, globals) {
			return false
		}
	}
	return sawCall
}

func staticLoopPCs(proto *vm.FuncProto) []bool {
	if proto == nil || len(proto.Code) == 0 {
		return nil
	}
	inLoop := make([]bool, len(proto.Code))
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op != vm.OP_FORLOOP && op != vm.OP_JMP {
			continue
		}
		target := pc + 1 + vm.DecodesBx(inst)
		if target < 0 || target > pc {
			continue
		}
		for i := target; i <= pc && i < len(inLoop); i++ {
			inLoop[i] = true
		}
	}
	return inLoop
}

func hasNonNativeCallInLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpCall && !tier2LoopCallIsNativeCandidate(fn, instr, globals) {
				return true
			}
		}
	}
	return false
}

func hasBlockingNonNativeCallInLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	if !hasNonNativeCallInLoop(fn, globals) {
		return false
	}
	return !nonNativeCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(fn, globals)
}

type tier2LoopRange struct {
	minPC       int
	maxPC       int
	blockCount  int
	hasCall     bool
	hasBlocker  bool
	callFreeOps int
}

func staticCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(proto *vm.FuncProto) bool {
	if proto == nil || len(proto.Code) == 0 {
		return false
	}
	ranges := make([]tier2LoopRange, 0, 4)
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op != vm.OP_FORLOOP && op != vm.OP_JMP {
			continue
		}
		target := pc + 1 + vm.DecodesBx(inst)
		if target < 0 || target > pc {
			continue
		}
		r := tier2LoopRange{minPC: target, maxPC: pc, blockCount: pc - target + 1, callFreeOps: pc - target + 1}
		for i := target; i <= pc && i < len(proto.Code); i++ {
			if vm.DecodeOp(proto.Code[i]) == vm.OP_CALL {
				r.hasCall = true
				break
			}
		}
		ranges = append(ranges, r)
	}
	return loopRangesHavePrefixBlockersBeforeHotCallFreeLoop(ranges)
}

func nonNativeCallsConfinedToPrefixLoopsBeforeCallFreeHotLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	if fn == nil {
		return false
	}
	li := computeLoopInfo(fn)
	if li == nil || !li.hasLoops() {
		return false
	}
	ranges := make([]tier2LoopRange, 0, len(li.loopHeaders))
	for _, blocks := range li.headerBlocks {
		r := tier2LoopRange{minPC: -1, maxPC: -1, blockCount: len(blocks)}
		for _, block := range fn.Blocks {
			if block == nil || !blocks[block.ID] {
				continue
			}
			for _, instr := range block.Instrs {
				if instr == nil {
					continue
				}
				if instr.HasSource {
					if r.minPC < 0 || instr.SourcePC < r.minPC {
						r.minPC = instr.SourcePC
					}
					if instr.SourcePC > r.maxPC {
						r.maxPC = instr.SourcePC
					}
				}
				if instr.Op == OpCall {
					r.hasCall = true
					if !tier2LoopCallIsNativeCandidate(fn, instr, globals) {
						r.hasBlocker = true
					}
					continue
				}
				if !instr.Op.IsTerminator() {
					r.callFreeOps++
				}
			}
		}
		if r.minPC >= 0 && r.maxPC >= r.minPC {
			ranges = append(ranges, r)
		}
	}
	return loopRangesHavePrefixBlockersBeforeHotCallFreeLoop(ranges)
}

func loopRangesHavePrefixBlockersBeforeHotCallFreeLoop(ranges []tier2LoopRange) bool {
	if len(ranges) < 2 {
		return false
	}
	firstBlockerMax := -1
	blockerOps := 0
	for _, r := range ranges {
		if !(r.hasBlocker || r.hasCall) {
			continue
		}
		if firstBlockerMax < 0 || r.maxPC > firstBlockerMax {
			firstBlockerMax = r.maxPC
		}
		if r.callFreeOps > blockerOps {
			blockerOps = r.callFreeOps
		}
	}
	if firstBlockerMax < 0 {
		return false
	}
	for _, r := range ranges {
		if r.hasBlocker || r.hasCall || r.minPC <= firstBlockerMax {
			continue
		}
		if r.blockCount >= 2 || r.callFreeOps >= blockerOps {
			return true
		}
	}
	return false
}

// hasExpensiveInLoop (R162) generalizes hasCallInLoop: it reports whether
// any op in a loop block matches a predicate. Used to gate Tier 2
// promotion on "post-pipeline body free of exit-resume-prone ops in
// loops". Includes: OpCall (exit-resume callee dispatch), OpGetTable /
// OpSetTable (dynamic-key table ops exit to executeTableExit),
// OpNewTable (residual allocations after EA fail → exit to Go),
// OpConcat / OpAppend / OpSetList / OpSelf / OpGetUpval / OpSetUpval
// / OpGo / OpSend / OpRecv / OpClosure / OpVararg (all exit-resume).
// Not included: OpGetField / OpSetField (static key, inline-cached,
// fast). GuardType is ok (<5 insns, not exit-resume).
func hasExpensiveInLoop(fn *Function, predicate func(Op) bool) bool {
	var li *loopInfo
	for _, block := range fn.Blocks {
		match := false
		for _, instr := range block.Instrs {
			if predicate(instr.Op) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		if li == nil {
			li = computeLoopInfo(fn)
		}
		if li.loopBlocks[block.ID] {
			return true
		}
	}
	return false
}

// hasExitResumeInLoop (R162) is the STRICT smart-gate predicate used
// for LoopDepth<2 candidates (the R162 widen bucket). Returns true
// when the post-pipeline IR has ANY op in a loop that's likely to
// exit-resume, including dynamic-key OpGetTable/OpSetTable, residual
// OpNewTable, and the always-exit-resume ops below. This is stricter
// than hasCallInLoop because the widen bucket is untested at Tier 2
// (never compiled there before R162) and we want a conservative
// bound to avoid correctness bugs (R152-observed int48-overflow +
// LCG + qs correctness bug was triggered by newly-admitted LoopDepth=1
// protos).
//
// OpGetField/OpSetField excluded (IC-cached, ~5 insns fast path). OpCall is
// allowed only when it statically resolves to a callee that can use the native
// path: an already-Tier2 direct entry, a tier-up-eligible stable function, a
// self-recursive raw-int callee, or a small leaf native candidate.
func hasExitResumeInLoop(fn *Function, globals map[string]*vm.FuncProto) bool {
	_, ok := firstExitResumeInLoop(fn, globals)
	return ok
}

func firstTier2ModBlockerInLoop(fn *Function) (string, bool) {
	gate := firstTier2ModBlockerInLoopGate(fn)
	return gate.Reason, !gate.Allowed
}

func firstTier2ModBlockerInLoopGate(fn *Function) GateResult {
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op != OpMod {
				continue
			}
			// Generic Mod now has native int/float lowering with op-exit fallback
			// for zero divisors and non-numeric operands. It is no longer an
			// exit-storm blocker by itself.
			continue
		}
	}
	return allowGate("Tier2ModLoop", "generic mod has native lowering")
}
