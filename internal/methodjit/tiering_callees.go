//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/vm"

// This is used by the inline pass to resolve callee functions at compile time.
func (tm *TieringManager) buildInlineGlobals() map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if tm.callVM == nil {
		return globals
	}
	for name, val := range tm.callVM.Globals() {
		if !val.IsFunction() {
			continue
		}
		if cl, ok := vmClosureFromValue(val); ok && cl != nil && cl.Proto != nil {
			globals[name] = cl.Proto
		}
	}
	return globals
}

// buildProtoInlineGlobals extracts global function declarations from the
// current proto's entry straight-line prefix. This covers top-level patterns
// produced by the compiler:
//
//	CLOSURE tmp, child
//	SETGLOBAL tmp, "name"
//
// The VM global table is authoritative once a script has executed, but during
// early <main> compilation these declarations have not run yet. Feeding this
// lexical table to the inline/filter pipeline lets the compiler resolve calls
// in the same top-level body without requiring Ackermann-specific hooks.
//
// The scan intentionally stops at the first non-declaration instruction. That
// keeps the contract conservative: function declarations inside branches,
// loops, or after executable statements are not treated as globally stable for
// the whole proto.
func buildProtoInlineGlobals(proto *vm.FuncProto) map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if proto == nil {
		return globals
	}
	regClosure := make(map[int]*vm.FuncProto)
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_CLOSURE:
			a := vm.DecodeA(inst)
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(proto.Protos) {
				delete(regClosure, a)
				continue
			}
			regClosure[a] = proto.Protos[bx]
		case vm.OP_MOVE:
			a := vm.DecodeA(inst)
			b := vm.DecodeB(inst)
			if cl := regClosure[b]; cl != nil {
				regClosure[a] = cl
			} else {
				delete(regClosure, a)
			}
		case vm.OP_SETGLOBAL:
			a := vm.DecodeA(inst)
			bx := vm.DecodeBx(inst)
			name := protoConstString(proto, bx)
			if name == "" {
				return globals
			}
			cl := regClosure[a]
			if cl == nil {
				return globals
			}
			globals[name] = cl
		case vm.OP_CLOSE:
			continue
		default:
			return globals
		}
	}
	return globals
}

// buildProtoStableGlobals extracts global function declarations across the
// whole proto when every write to that global is the same lexical closure.
// Unlike buildProtoInlineGlobals, this does not feed the inliner: it only gives
// the loop-call gate a stable callee identity for top-level driver scripts that
// declare helpers after executable setup code and call them later in a loop.
func buildProtoStableGlobals(proto *vm.FuncProto) map[string]*vm.FuncProto {
	globals := make(map[string]*vm.FuncProto)
	if proto == nil {
		return globals
	}
	invalid := make(map[string]bool)
	regClosure := make(map[int]*vm.FuncProto)
	for _, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_CLOSURE:
			bx := vm.DecodeBx(inst)
			if bx < 0 || bx >= len(proto.Protos) {
				delete(regClosure, a)
				continue
			}
			regClosure[a] = proto.Protos[bx]
		case vm.OP_MOVE:
			b := vm.DecodeB(inst)
			if cl := regClosure[b]; cl != nil {
				regClosure[a] = cl
			} else {
				delete(regClosure, a)
			}
		case vm.OP_SETGLOBAL:
			name := protoConstString(proto, vm.DecodeBx(inst))
			if name == "" || invalid[name] {
				continue
			}
			cl := regClosure[a]
			if cl == nil {
				invalid[name] = true
				delete(globals, name)
				continue
			}
			if prev := globals[name]; prev != nil && prev != cl {
				invalid[name] = true
				delete(globals, name)
				continue
			}
			globals[name] = cl
		case vm.OP_CLOSE:
			continue
		default:
			delete(regClosure, a)
		}
	}
	return globals
}

func (tm *TieringManager) buildLoopCallGlobals(proto *vm.FuncProto) map[string]*vm.FuncProto {
	globals := tm.buildInlineGlobals()
	if protoGlobals := buildProtoInlineGlobals(proto); len(protoGlobals) > 0 {
		merged := make(map[string]*vm.FuncProto, len(globals)+len(protoGlobals))
		for name, callee := range globals {
			merged[name] = callee
		}
		for name, callee := range protoGlobals {
			if _, ok := merged[name]; !ok {
				merged[name] = callee
			}
		}
		globals = merged
	}
	if stableGlobals := buildProtoStableGlobals(proto); len(stableGlobals) > 0 {
		merged := make(map[string]*vm.FuncProto, len(globals)+len(stableGlobals))
		for name, callee := range globals {
			merged[name] = callee
		}
		for name, callee := range stableGlobals {
			if _, ok := merged[name]; !ok {
				merged[name] = callee
			}
		}
		globals = merged
	}
	return globals
}

func (tm *TieringManager) ensureRawIntLoopCallees(proto *vm.FuncProto) {
	if proto == nil || tm == nil {
		return
	}
	if analyzeFuncProfile(proto).LoopDepth < 2 {
		return
	}
	globals := tm.buildLoopCallGlobals(proto)
	if len(globals) == 0 {
		return
	}
	for _, callee := range rawIntLoopCallCallees(BuildGraph(proto), globals) {
		if callee == nil || tm.tier2Compiled[callee] != nil || tm.tier2HasFailed(callee) {
			continue
		}
		if !shouldStayTier1ForBoxedRawIntKernel(callee, analyzeFuncProfile(callee)) {
			continue
		}
		cf, err := tm.compileTier2(callee)
		if err != nil {
			tm.markTier2Failed(callee, err.Error())
			continue
		}
		tm.markTier2Compiled(callee, cf)
	}
}

func (tm *TieringManager) ensureNativeLoopCallees(proto *vm.FuncProto) {
	if proto == nil || tm == nil || !hasStaticCallInLoop(proto) {
		return
	}
	globals := tm.buildLoopCallGlobals(proto)
	if len(globals) == 0 {
		return
	}
	for _, callee := range nativeLoopCallCallees(BuildGraph(proto), globals) {
		if callee == nil || callee == proto || tm.tier2Compiled[callee] != nil || tm.tier2HasFailed(callee) {
			continue
		}
		if !canPromoteToTier2(callee) || !nativeLoopCalleePrecompileSafe(callee) {
			continue
		}
		if cf, ok := tm.compileMutualRecursiveIntSCCTier2WithGlobals(callee, globals); ok {
			tm.markTier2Compiled(callee, cf)
			continue
		}
		cf, err := tm.compileTier2(callee)
		if err != nil {
			tm.markTier2Failed(callee, err.Error())
			continue
		}
		tm.markTier2Compiled(callee, cf)
	}
}

func nativeLoopCallCallees(fn *Function, globals map[string]*vm.FuncProto) []*vm.FuncProto {
	if fn == nil || len(globals) == 0 {
		return nil
	}
	li := computeLoopInfo(fn)
	if li == nil || !li.hasLoops() {
		return nil
	}
	seen := make(map[*vm.FuncProto]bool)
	var out []*vm.FuncProto
	for _, block := range fn.Blocks {
		if block == nil || !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpCall {
				continue
			}
			_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
			if callee == nil || seen[callee] {
				continue
			}
			seen[callee] = true
			out = append(out, callee)
		}
	}
	return out
}

func nativeLoopCalleePrecompileSafe(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD, vm.OP_NEWTABLE, vm.OP_NEWOBJECT2, vm.OP_NEWOBJECTN, vm.OP_SETLIST, vm.OP_APPEND:
			return false
		}
	}
	return true
}

func rawIntLoopCallCallees(fn *Function, globals map[string]*vm.FuncProto) []*vm.FuncProto {
	if fn == nil || len(globals) == 0 {
		return nil
	}
	seen := make(map[*vm.FuncProto]bool)
	var out []*vm.FuncProto
	li := computeLoopInfo(fn)
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op != OpCall {
				continue
			}
			_, callee := resolveCallee(instr, fn, InlineConfig{Globals: globals})
			if callee == nil || seen[callee] {
				continue
			}
			if shouldStayTier1ForBoxedRawIntKernel(callee, analyzeFuncProfile(callee)) {
				seen[callee] = true
				out = append(out, callee)
			}
		}
	}
	return out
}
