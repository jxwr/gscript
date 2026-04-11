//go:build darwin && arm64

// tier1_handlers.go contains the primary Tier 1 baseline JIT exit handlers.
// These handle the most common operations that the baseline JIT exits to Go for:
// calls, globals, tables, and field access.

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// handleBaselineOpExit dispatches a baseline op-exit to the appropriate handler.
func (e *BaselineJITEngine) handleBaselineOpExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto, bf *BaselineFunc) error {
	opCode := vm.Opcode(ctx.BaselineOp)
	switch opCode {
	case vm.OP_CALL:
		return e.handleCall(ctx, regs, base, proto)
	case vm.OP_GETGLOBAL:
		return e.handleGetGlobal(ctx, regs, base, proto, bf)
	case vm.OP_SETGLOBAL:
		return e.handleSetGlobal(ctx, regs, base, proto)
	case vm.OP_NEWTABLE:
		return e.handleNewTable(ctx, regs, base, proto)
	case vm.OP_GETTABLE:
		return e.handleGetTable(ctx, regs, base, proto)
	case vm.OP_SETTABLE:
		return e.handleSetTable(ctx, regs, base, proto)
	case vm.OP_GETFIELD:
		return e.handleGetField(ctx, regs, base, proto)
	case vm.OP_SETFIELD:
		return e.handleSetField(ctx, regs, base, proto)
	case vm.OP_SETLIST:
		return e.handleSetList(ctx, regs, base, proto)
	case vm.OP_APPEND:
		return e.handleAppend(ctx, regs, base, proto)
	case vm.OP_CONCAT:
		return e.handleConcat(ctx, regs, base, proto)
	case vm.OP_LEN:
		return e.handleLen(ctx, regs, base, proto)
	case vm.OP_CLOSURE:
		return e.handleClosure(ctx, regs, base, proto)
	case vm.OP_CLOSE:
		return e.handleClose(ctx, regs, base, proto)
	case vm.OP_GETUPVAL:
		return e.handleGetUpval(ctx, regs, base, proto)
	case vm.OP_SETUPVAL:
		return e.handleSetUpval(ctx, regs, base, proto)
	case vm.OP_SELF:
		return e.handleSelf(ctx, regs, base, proto)
	case vm.OP_VARARG:
		return e.handleVararg(ctx, regs, base, proto)
	case vm.OP_TFORCALL:
		return e.handleTForCall(ctx, regs, base, proto)
	case vm.OP_TFORLOOP:
		return e.handleTForLoop(ctx, regs, base, proto)
	case vm.OP_POW:
		return e.handlePow(ctx, regs, base, proto)
	case vm.OP_LT:
		return e.handleLT(ctx, regs, base, proto)
	case vm.OP_LE:
		return e.handleLE(ctx, regs, base, proto)
	default:
		return fmt.Errorf("unhandled baseline op-exit: %s (%d)", vm.OpName(opCode), opCode)
	}
}

// resolveCmpRK resolves a comparison operand (RK). For registers, base+idx;
// for constants, proto.Constants[idx - RKBit].
func resolveCmpRK(regs []runtime.Value, base int, proto *vm.FuncProto, idx int) runtime.Value {
	if idx >= vm.RKBit {
		k := idx - vm.RKBit
		if k >= 0 && k < len(proto.Constants) {
			return proto.Constants[k]
		}
		return runtime.NilValue()
	}
	abs := base + idx
	if abs >= 0 && abs < len(regs) {
		return regs[abs]
	}
	return runtime.NilValue()
}

// handleLT handles OP_LT exit for operands that the native path can't compare
// (typically strings). Computes (bval < cval) via Value.LessThan and overrides
// BaselinePC based on the VM semantics: if (result) != bool(A), then PC++.
// The exit emitter stored BaselinePC = pc+1 (instruction after LT); we adjust
// to pc+2 when the skip fires.
func (e *BaselineJITEngine) handleLT(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	bidx := int(ctx.BaselineB)
	cidx := int(ctx.BaselineC)

	bval := resolveCmpRK(regs, base, proto, bidx)
	cval := resolveCmpRK(regs, base, proto, cidx)

	lt, ok := bval.LessThan(cval)
	if !ok {
		return fmt.Errorf("LT: cannot compare %s with %s", bval.TypeName(), cval.TypeName())
	}
	if lt != (a != 0) {
		// VM does PC++ to skip the next instruction. BaselinePC is
		// already pc+1; bump to pc+2.
		ctx.BaselinePC++
	}
	return nil
}

// handleLE mirrors handleLT for OP_LE.
func (e *BaselineJITEngine) handleLE(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	bidx := int(ctx.BaselineB)
	cidx := int(ctx.BaselineC)

	bval := resolveCmpRK(regs, base, proto, bidx)
	cval := resolveCmpRK(regs, base, proto, cidx)

	// (b <= c) == !(c < b)
	gt, ok := cval.LessThan(bval)
	if !ok {
		return fmt.Errorf("LE: cannot compare %s with %s", bval.TypeName(), cval.TypeName())
	}
	le := !gt
	if le != (a != 0) {
		ctx.BaselinePC++
	}
	return nil
}

// handleCall handles OP_CALL exit: execute the function call via the VM.
// BaselineB and BaselineC are the raw B and C fields from the instruction:
//
//	B=0: variable args (use vm.top), else nArgs=B-1
//	C=0: return all values, C=1: no results, else nRets=C-1
func (e *BaselineJITEngine) handleCall(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for call-exit")
	}
	callSlot := int(ctx.BaselineA)
	rawB := int(ctx.BaselineB)
	rawC := int(ctx.BaselineC)

	absSlot := base + callSlot
	if absSlot >= len(regs) {
		return fmt.Errorf("call slot %d out of range", absSlot)
	}
	fnVal := regs[absSlot]

	// Determine number of arguments.
	var nArgs int
	if rawB == 0 {
		top := e.callVM.Top()
		nArgs = top - (absSlot + 1)
		if nArgs < 0 {
			nArgs = 0
		}
	} else {
		nArgs = rawB - 1
	}

	// Fast path: GScript closure with compiled proto. Avoids heap-allocating
	// callArgs and bypasses CallValue → callValue → call dispatch.
	if fnVal.IsFunction() {
		if cl, ok := fnVal.Ptr().(*vm.Closure); ok && !cl.Proto.IsVarArg {
			calleeProto := cl.Proto
			// If the TieringManager has set a tier-up threshold and the callee
			// has EXACTLY reached it, fall to slow path ONCE so the VM's
			// TryCompile can trigger Tier 2 compilation. Using == instead of
			// >= ensures this detour happens only once per function.
			if e.tierUpThreshold > 0 && calleeProto.CallCount == e.tierUpThreshold {
				goto slowPath
			}
			// If Tier 2 applied an intrinsic (e.g., math.sqrt→FSQRT), Tier 1
			// code would execute a different (slower, allocating) sequence.
			// Dispatch via slowPath so Tier 2's compiled code runs.
			// For functions without intrinsics, Tier 1 execution is equivalent
			// and faster than going through VM.CallValue.
			if calleeProto.NeedsTier2 {
				goto slowPath
			}
			calleeBF, compiled := e.compiled[calleeProto]
			if !compiled {
				// Try to compile the callee on the fly.
				calleeProto.CallCount++
				var compileResult interface{}
				if e.outerCompiler != nil {
					compileResult = e.outerCompiler(calleeProto)
				} else {
					compileResult = e.TryCompile(calleeProto)
				}
				// If the result is a *BaselineFunc, use it for fast-path execution.
				// If it's a *CompiledFunction (Tier 2), fall to slow path
				// so Execute dispatches to executeTier2.
				if bf, ok := compileResult.(*BaselineFunc); ok {
					calleeBF = bf
					compiled = true
				} else if compileResult != nil {
					// Tier 2 compiled — fall to slow path for proper dispatch.
					goto slowPath
				}
			}
			if compiled {
				// Compute callee base: after caller's register window.
				calleeBase := base + proto.MaxStack
				top := e.callVM.Top()
				if top > calleeBase {
					calleeBase = top
				}

				// Ensure register space (may grow the register file).
				needed := calleeBase + calleeProto.MaxStack + 1
				currentRegs := e.callVM.EnsureRegs(needed)

				// Copy args directly — no heap allocation.
				nParams := calleeProto.NumParams
				srcStart := absSlot + 1
				for i := 0; i < nParams && i < nArgs; i++ {
					currentRegs[calleeBase+i] = currentRegs[srcStart+i]
				}
				for i := nArgs; i < nParams; i++ {
					currentRegs[calleeBase+i] = runtime.NilValue()
				}

				// Push a VM frame so CurrentClosure() returns the callee's
				// closure (needed for GETUPVAL/SETUPVAL) and CloseUpvalues
				// works correctly on return.
				if !e.callVM.PushFrame(cl, calleeBase) {
					// Stack overflow — fall through to generic path.
					goto slowPath
				}

				// Execute the callee directly via JIT.
				results, err := e.Execute(calleeBF, currentRegs, calleeBase, calleeProto)

				// Close upvalues and pop frame regardless of error.
				e.callVM.CloseUpvalues(calleeBase)
				e.callVM.PopFrame()

				if err != nil {
					return err
				}

				// Re-read regs (Execute may have grown the register file).
				currentRegs = e.callVM.Regs()

				// Place results starting at the function slot.
				if rawC == 0 {
					for i, r := range results {
						idx := absSlot + i
						if idx < len(currentRegs) {
							currentRegs[idx] = r
						}
					}
					e.callVM.SetTop(absSlot + len(results))
				} else {
					nr := rawC - 1
					for i := 0; i < nr; i++ {
						idx := absSlot + i
						if idx < len(currentRegs) {
							if i < len(results) {
								currentRegs[idx] = results[i]
							} else {
								currentRegs[idx] = runtime.NilValue()
							}
						}
					}
				}
				return nil
			}
		}
	}

slowPath:
	// Generic path: heap-allocate args and go through CallValue.
	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := absSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	results, err := e.callVM.CallValue(fnVal, callArgs)
	if err != nil {
		return err
	}

	// Re-read regs in case the callee grew the register file.
	currentRegs := e.callVM.Regs()

	// Place results: overwrite starting from the function slot.
	if rawC == 0 {
		for i, r := range results {
			idx := absSlot + i
			if idx < len(currentRegs) {
				currentRegs[idx] = r
			}
		}
		e.callVM.SetTop(absSlot + len(results))
	} else {
		nr := rawC - 1
		for i := 0; i < nr; i++ {
			idx := absSlot + i
			if idx < len(currentRegs) {
				if i < len(results) {
					currentRegs[idx] = results[i]
				} else {
					currentRegs[idx] = runtime.NilValue()
				}
			}
		}
	}
	return nil
}

// handleGetGlobal handles OP_GETGLOBAL exit.
// Populates bf.GlobalValCache so the native inline cache hits on next access.
func (e *BaselineJITEngine) handleGetGlobal(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto, bf *BaselineFunc) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for global-exit")
	}
	a := int(ctx.BaselineA)
	bx := int(ctx.BaselineB)
	if bx >= len(proto.Constants) {
		return fmt.Errorf("global const index %d out of range", bx)
	}
	name := proto.Constants[bx].Str()
	val := e.callVM.GetGlobal(name)
	absSlot := base + a
	if absSlot < len(regs) {
		regs[absSlot] = val
	}
	// Populate the per-PC global value cache for the native fast path.
	// BaselinePC is the resume (next) PC, so current instruction PC = BaselinePC - 1.
	if bf.GlobalValCache != nil {
		pc := int(ctx.BaselinePC) - 1
		if pc >= 0 && pc < len(bf.GlobalValCache) && uint64(val) != 0 {
			// If the generation has changed since we last cached, ALL entries
			// are potentially stale. Clear the entire cache before repopulating
			// this entry. Without this, updating CachedGlobalGen would make
			// other PCs' stale cached values appear valid.
			if bf.CachedGlobalGen != e.globalCacheGen {
				for i := range bf.GlobalValCache {
					bf.GlobalValCache[i] = 0
				}
			}
			bf.GlobalValCache[pc] = uint64(val)
			bf.CachedGlobalGen = e.globalCacheGen
			ctx.BaselineGlobalCachedGen = e.globalCacheGen
		}
	}
	return nil
}

// handleSetGlobal handles OP_SETGLOBAL exit.
// Increments globalCacheGen to invalidate all GlobalValCache entries.
func (e *BaselineJITEngine) handleSetGlobal(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM for setglobal-exit")
	}
	a := int(ctx.BaselineA)
	bx := int(ctx.BaselineB)
	if bx >= len(proto.Constants) {
		return fmt.Errorf("setglobal const index %d out of range", bx)
	}
	name := proto.Constants[bx].Str()
	absSlot := base + a
	if absSlot < len(regs) {
		e.callVM.SetGlobal(name, regs[absSlot])
	}
	// Invalidate all global value caches by bumping the generation.
	e.globalCacheGen++
	return nil
}

// handleNewTable handles OP_NEWTABLE exit.
func (e *BaselineJITEngine) handleNewTable(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // array hint
	c := int(ctx.BaselineC) // hash hint
	absSlot := base + a
	tbl := runtime.NewTableSized(b, c)
	if absSlot < len(regs) {
		regs[absSlot] = runtime.TableValue(tbl)
	}
	return nil
}

// handleGetTable handles OP_GETTABLE exit.
func (e *BaselineJITEngine) handleGetTable(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC)

	absB := base + b
	if absB >= len(regs) {
		return nil
	}
	tblVal := regs[absB]

	// Resolve RK(C)
	var key runtime.Value
	if c >= vm.RKBit {
		key = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			key = regs[absC]
		}
	}

	absA := base + a
	if tblVal.IsTable() {
		if absA < len(regs) {
			tbl := tblVal.Table()
			regs[absA] = tbl.RawGet(key)
			// Record type feedback so Tier 2 can specialize.
			pc := int(ctx.BaselinePC) - 1
			if proto.Feedback != nil && pc >= 0 && pc < len(proto.Feedback) {
				proto.Feedback[pc].Result.Observe(regs[absA].Type())
				// Record array kind for table-access specialization.
				proto.Feedback[pc].ObserveKind(uint8(tbl.GetArrayKind()))
			}
		}
	} else if absA < len(regs) {
		regs[absA] = runtime.NilValue()
	}
	return nil
}

// handleSetTable handles OP_SETTABLE exit.
func (e *BaselineJITEngine) handleSetTable(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC)

	absA := base + a
	if absA >= len(regs) {
		return nil
	}
	tblVal := regs[absA]

	// Resolve RK(B) = key
	var key runtime.Value
	if b >= vm.RKBit {
		key = proto.Constants[b-vm.RKBit]
	} else {
		absB := base + b
		if absB < len(regs) {
			key = regs[absB]
		}
	}

	// Resolve RK(C) = value
	var val runtime.Value
	if c >= vm.RKBit {
		val = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			val = regs[absC]
		}
	}

	if tblVal.IsTable() {
		tbl := tblVal.Table()
		tbl.RawSet(key, val)
		// Record array kind feedback for table-access specialization.
		pc := int(ctx.BaselinePC) - 1
		if proto.Feedback != nil && pc >= 0 && pc < len(proto.Feedback) {
			proto.Feedback[pc].ObserveKind(uint8(tbl.GetArrayKind()))
		}
	}
	return nil
}

// ensureFieldCache lazily allocates the FieldCache on the FuncProto if nil.
func ensureFieldCache(proto *vm.FuncProto) {
	if proto.FieldCache == nil {
		proto.FieldCache = make([]runtime.FieldCacheEntry, len(proto.Code))
	}
}

// handleGetField handles OP_GETFIELD exit: R(A) = R(B).Constants[Bx]
// Populates proto.FieldCache so the native inline cache hits on next access.
func (e *BaselineJITEngine) handleGetField(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	c := int(ctx.BaselineC) // constant index for field name

	absB := base + b
	absA := base + a
	if absB >= len(regs) || absA >= len(regs) {
		return nil
	}
	tblVal := regs[absB]
	if c >= len(proto.Constants) {
		return nil
	}
	fieldName := proto.Constants[c].Str()

	if tblVal.IsTable() {
		tbl := tblVal.Table()
		// Use the cached path to populate the FieldCache for the native inline cache.
		// BaselinePC is the resume (next) PC, so current instruction PC = BaselinePC - 1.
		pc := int(ctx.BaselinePC) - 1
		ensureFieldCache(proto)
		regs[absA] = tbl.RawGetStringCached(fieldName, &proto.FieldCache[pc])
		// Record type feedback so Tier 2 can specialize.
		if proto.Feedback != nil && pc < len(proto.Feedback) {
			proto.Feedback[pc].Result.Observe(regs[absA].Type())
		}
	} else {
		regs[absA] = runtime.NilValue()
	}
	return nil
}

// handleSetField handles OP_SETFIELD exit: R(A).Constants[Bx] = RK(C)
// Populates proto.FieldCache so the native inline cache hits on next access.
func (e *BaselineJITEngine) handleSetField(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // constant index for field name
	c := int(ctx.BaselineC) // RK(C) = value

	absA := base + a
	if absA >= len(regs) || b >= len(proto.Constants) {
		return nil
	}
	tblVal := regs[absA]
	fieldName := proto.Constants[b].Str()

	// Resolve RK(C) = value
	var val runtime.Value
	if c >= vm.RKBit {
		val = proto.Constants[c-vm.RKBit]
	} else {
		absC := base + c
		if absC < len(regs) {
			val = regs[absC]
		}
	}

	if tblVal.IsTable() {
		tbl := tblVal.Table()
		// Use the cached path to populate the FieldCache for the native inline cache.
		// BaselinePC is the resume (next) PC, so current instruction PC = BaselinePC - 1.
		pc := int(ctx.BaselinePC) - 1
		ensureFieldCache(proto)
		tbl.RawSetStringCached(fieldName, val, &proto.FieldCache[pc])
	}
	return nil
}

// handleSetList handles OP_SETLIST exit.
func (e *BaselineJITEngine) handleSetList(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB) // count
	c := int(ctx.BaselineC) // block

	absA := base + a
	if absA >= len(regs) {
		return nil
	}
	tblVal := regs[absA]
	if !tblVal.IsTable() {
		return fmt.Errorf("SETLIST on non-table")
	}
	tbl := tblVal.Table()
	offset := (c - 1) * 50
	for i := 1; i <= b; i++ {
		idx := absA + i
		if idx < len(regs) {
			tbl.RawSetInt(int64(offset+i), regs[idx])
		}
	}
	return nil
}

// handleAppend handles OP_APPEND exit.
func (e *BaselineJITEngine) handleAppend(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	a := int(ctx.BaselineA)
	b := int(ctx.BaselineB)
	absA := base + a
	absB := base + b
	if absA >= len(regs) || absB >= len(regs) {
		return nil
	}
	tblVal := regs[absA]
	if tblVal.IsTable() {
		tblVal.Table().Append(regs[absB])
	}
	return nil
}

// handleNativeCallExit handles the case where a callee invoked via native BLR
// hits an exit-resume op mid-execution. The callee's exit state is in ctx.
//
// Rather than trying to resume the callee mid-execution (which is fragile with
// nested BLR calls — the exitHandleLabel chain overwrites BaselinePC at each
// level), we take the simpler approach:
//   1. For persistent exits (OP_CALL depth limit, NEWTABLE, CONCAT, ...),
//      disable BLR for this callee so future calls go straight to slow path.
//   2. For transient cache-backed exits (OP_GETGLOBAL), keep DirectEntryPtr
//      intact — the nested Execute warms the IC and subsequent BLR calls hit
//      the fast path without re-exiting.
//   3. Re-execute the callee from scratch via e.Execute() which handles all
//      op-exits correctly through its own exit-resume loop.
func (e *BaselineJITEngine) handleNativeCallExit(ctx *ExecContext, regs []runtime.Value, base int, callerProto *vm.FuncProto, callerBF *BaselineFunc) (runtime.Value, error) {
	calleeBaseOff := int(ctx.NativeCalleeBaseOff)
	calleeBase := base + calleeBaseOff/8

	// Identify the callee closure. The caller's regs[A] holds the function value
	// that was called. We read it from the register file since ctx.BaselineClosurePtr
	// was already restored to the caller's closure by the ARM64 restore sequence.
	callA := int(ctx.NativeCallA)
	absCallA := base + callA
	if absCallA >= len(regs) {
		return runtime.NilValue(), fmt.Errorf("native-call-exit: call slot %d out of range", absCallA)
	}
	fnVal := regs[absCallA]
	if !fnVal.IsFunction() {
		return runtime.NilValue(), fmt.Errorf("native-call-exit: regs[%d] is not a function", absCallA)
	}
	cl, ok := fnVal.Ptr().(*vm.Closure)
	if !ok {
		return runtime.NilValue(), fmt.Errorf("native-call-exit: regs[%d] is not a vm.Closure", absCallA)
	}
	calleeProto := cl.Proto

	// Disable BLR for this callee only for persistent exits (OP_CALL depth
	// limit, NEWTABLE, CONCAT, ...). Transient cache-backed exits
	// (OP_GETGLOBAL) do not recur after the nested Execute warms the IC slot,
	// so preserve DirectEntryPtr to keep the BLR fast path.
	if !isTransientOpExit(vm.Opcode(ctx.BaselineOp)) {
		calleeProto.DirectEntryPtr = 0
	}

	calleeBF, ok := e.compiled[calleeProto]
	if !ok {
		return runtime.NilValue(), fmt.Errorf("native-call-exit: callee not compiled")
	}

	// Re-read regs (may have been grown).
	if e.callVM != nil {
		regs = e.callVM.Regs()
	}

	// The BLR caller already copied arguments to regs[calleeBase..]. Re-initialize
	// unused registers to nil (same as Execute does), then re-execute the callee
	// from scratch. This is safe because:
	// - The callee's partial execution only modified registers (no external side effects)
	// - Op-exits like NEWTABLE are at the beginning or are idempotent on retry
	for i := calleeBase + calleeProto.NumParams; i < calleeBase+calleeProto.MaxStack; i++ {
		if i < len(regs) {
			regs[i] = runtime.NilValue()
		}
	}

	// Push a VM frame for the callee (needed for GETUPVAL, CloseUpvalues).
	if e.callVM != nil {
		if !e.callVM.PushFrame(cl, calleeBase) {
			return runtime.NilValue(), fmt.Errorf("native-call-exit: stack overflow")
		}
	}

	// Re-execute the callee from scratch via Execute, which has a proper
	// exit-resume loop that handles all op-exits correctly.
	results, err := e.Execute(calleeBF, regs, calleeBase, calleeProto)

	// Close upvalues and pop frame regardless of error.
	if e.callVM != nil {
		e.callVM.CloseUpvalues(calleeBase)
		e.callVM.PopFrame()
	}

	if err != nil {
		return runtime.NilValue(), err
	}

	if len(results) > 0 {
		return results[0], nil
	}
	return runtime.NilValue(), nil
}

// isTransientOpExit reports whether the given baseline opcode represents a
// one-shot, cache-backed exit that will not recur after the nested Execute
// warms its IC slot. Transient exits are safe to re-enter via BLR; the
// handler should NOT zero DirectEntryPtr or bump globalCacheGen for them.
// Persistent exits (OP_CALL depth limit, writes like NEWTABLE/CONCAT) do
// recur and must retain the conservative invalidation path.
func isTransientOpExit(op vm.Opcode) bool {
	return op == vm.OP_GETGLOBAL
}

// ptrToVMClosure converts a uintptr (from JIT-stored BaselineClosurePtr) to *vm.Closure.
// This is a legitimate conversion: the pointer was obtained from a NaN-boxed value
// that is kept alive by the runtime's GC root system.
//
//go:nosplit
func ptrToVMClosure(ptr uintptr) *vm.Closure {
	// Store the uintptr in a local, then convert via unsafe.Pointer.
	// The pointer is valid because it's kept alive by runtime.keepAliveIface.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&ptr))
	return (*vm.Closure)(p)
}
