package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// OnInstruction is called for every instruction during execution.
func (r *TraceRecorder) OnInstruction(pc int, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base int) bool {
	if !r.recording {
		return false
	}

	// Skip instructions from non-inlined callee functions.
	// When a CALL is recorded but not inlined (callee has loops), the interpreter
	// executes the callee inline. We skip all its instructions until it returns.
	if r.skipDepth > 0 {
		op := vm.DecodeOp(inst)
		if op == vm.OP_CALL {
			r.skipDepth++ // nested call within the skipped function
		} else if op == vm.OP_RETURN {
			r.skipDepth--
		}
		return false
	}

	// Detect Method JIT partial execution of inlined callee.
	// When handleCall decided to inline a function (depth++), the VM may have
	// executed part of the callee via Method JIT before side-exiting. In that
	// case, the first instruction we see from the callee won't be at PC=0.
	// Fall back to treating the CALL as non-inlined (skipDepth) so the trace
	// doesn't get a partial view of the callee's computation.
	if r.inlineCallProto != nil && r.depth > r.inlineCallDepth {
		if proto == r.inlineCallProto && pc != 0 {
			// Method JIT partially executed the callee. Undo the inline:
			// record the CALL as a non-inlined call and skip the rest.
			r.depth = r.inlineCallDepth
			r.current.IR = append(r.current.IR, *r.inlineCallIR)
			r.skipDepth = 1
			r.inlineCallProto = nil
			r.inlineCallIR = nil
			// Pop the inline call stack entry that was pushed in handleCall
			if len(r.inlineCallStack) > 0 {
				r.inlineCallStack = r.inlineCallStack[:len(r.inlineCallStack)-1]
			}
			return false
		}
		// First instruction at PC=0: full recording, clear the check.
		r.inlineCallProto = nil
		r.inlineCallIR = nil
	}

	// Inner loop skip: when recording the outer loop and the inner loop body
	// is being executed, skip all instructions until we pass the FORLOOP PC.
	if r.innerLoopSkipEnd > 0 {
		if pc > r.innerLoopSkipEnd {
			// Past inner loop — resume recording
			r.innerLoopSkipEnd = 0
			r.innerLoopSkipStart = 0
			// Reset full-nesting state if this was a full nested recording
			if r.innerLoopDepth > 0 {
				r.innerLoopDepth = 0
				r.innerLoopForPC = 0
				r.innerLoopFirstSeen = false
	r.innerLoopRecorded = false
			}
			// Fall through to record this instruction
		} else {
			// Still inside inner loop body — skip
			return false
		}
	}

	// Set startBase on first instruction
	if len(r.current.IR) == 0 && r.current.StartBase == 0 {
		r.startBase = base
		r.current.StartBase = base
		// Copy the root function's constants as the initial trace constants
		r.current.Constants = make([]runtime.Value, len(proto.Constants))
		copy(r.current.Constants, proto.Constants)
	}

	// Check trace length limit
	if len(r.current.IR) >= r.maxLen {
		r.abortTrace()
		return false
	}

	// Build the trace IR for this instruction (decode, remap, capture types).
	ir, origA, origB := r.buildTraceIR(pc, inst, proto, regs, base)
	op := ir.Op

	// Handle CALL: try to inline
	if op == vm.OP_CALL {
		return r.handleCall(ir, regs, base)
	}

	// Handle RETURN from inlined function
	if op == vm.OP_RETURN && r.depth > 0 {
		r.recordInlinedReturn(ir, origA, origB, pc, proto, regs, base)
		return false
	}

	// Check for unsupported ops that abort recording.
	// These are structural limitations (nested loops, concurrency) that won't
	// change between attempts, so blacklist permanently.
	if r.shouldAbort(op) {
		r.abortAndBlacklist()
		return false
	}

	// Handle FORPREP for nested loop at root depth.
	if op == vm.OP_FORPREP && r.depth == 0 {
		return r.recordNestedForPrep(ir, proto)
	}

	// Handle inner FORLOOP during full nested recording.
	if op == vm.OP_FORLOOP && r.innerLoopDepth > 0 && pc == r.innerLoopForPC {
		r.recordInnerForLoop(ir)
		return false
	}

	// If we're recording and encounter a FORLOOP that is NOT our recorded loop,
	// the inner loop has exited and we're seeing the outer loop's FORLOOP.
	// Don't record it — the trace is complete without it.
	if op == vm.OP_FORLOOP && r.depth == 0 && pc != r.current.LoopPC {
		return false
	}

	// Detect unconditional JMP that exits the loop (break statement).
	if op == vm.OP_JMP && r.depth == 0 {
		if r.recordJmp(ir, pc, inst) {
			return false
		}
	}

	r.current.IR = append(r.current.IR, ir)
	return false
}

// buildTraceIR decodes and remaps a bytecode instruction into a TraceIR.
// It handles register remapping, constant pool remapping for inlined functions,
// type info capture, and field/global metadata capture.
// Returns the built IR and the original (un-remapped) A and B operands,
// which are needed by some downstream handlers.
func (r *TraceRecorder) buildTraceIR(pc int, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base int) (TraceIR, int, int) {
	op := vm.DecodeOp(inst)
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	// Register offset: remap from absolute base to trace-relative
	baseOff := base - r.startBase

	// For comparison opcodes (EQ, LT, LE), the A field is a boolean flag
	// (0 or 1), NOT a register index. Do not remap it with baseOff.
	remappedA := baseOff + a
	switch op {
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
		remappedA = a // A is a flag, not a register
	}

	ir := TraceIR{
		Op:    op,
		A:     remappedA,
		B:     b,
		C:     c,
		PC:    pc,
		Proto: proto,
		Depth: r.depth,
		Base:  base,
	}

	// Decode format-specific fields
	switch op {
	case vm.OP_GETGLOBAL, vm.OP_SETGLOBAL, vm.OP_LOADK, vm.OP_CLOSURE:
		ir.BX = vm.DecodeBx(inst)
	case vm.OP_LOADINT, vm.OP_FORPREP, vm.OP_FORLOOP, vm.OP_JMP:
		ir.SBX = vm.DecodesBx(inst)
	}

	// Remap B and C register operands to trace-relative
	// (RK operands >= RKBit are constants, handled separately)
	if b < vm.RKBit {
		ir.B = baseOff + b
	}
	if c < vm.RKBit {
		ir.C = baseOff + c
	}

	// For inlined functions (depth > 0), remap constant references
	// by copying constants into the trace's constant pool
	if r.depth > 0 {
		r.remapInlinedConstants(&ir, inst, proto, a, b, c)
	}

	// Capture type info
	r.captureTypeInfo(&ir, inst, proto, regs, base, a)

	// Capture global VALUE for GETGLOBAL (snapshot at recording time).
	// The interpreter already executed GETGLOBAL, so regs[base+a] has the value.
	// We store it as a trace constant so the compiled trace can reload it each iteration.
	if op == vm.OP_GETGLOBAL {
		absSlot := base + a
		if absSlot < len(regs) {
			constIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, regs[absSlot])
			ir.BX = constIdx // repurpose BX to point to the value constant
		}
	}

	// Capture field index for GETFIELD/SETFIELD (skeys position at recording time)
	r.captureFieldIndex(&ir, inst, proto, regs, base, a)

	return ir, a, b
}

// remapInlinedConstants remaps constant references from an inlined function's
// constant pool into the trace's unified constant pool.
func (r *TraceRecorder) remapInlinedConstants(ir *TraceIR, inst uint32, proto *vm.FuncProto, a, b, c int) {
	if b >= vm.RKBit {
		constIdx := b - vm.RKBit
		if constIdx < len(proto.Constants) {
			traceConstIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, proto.Constants[constIdx])
			ir.B = traceConstIdx + vm.RKBit
		}
	}
	if c >= vm.RKBit {
		constIdx := c - vm.RKBit
		if constIdx < len(proto.Constants) {
			traceConstIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, proto.Constants[constIdx])
			ir.C = traceConstIdx + vm.RKBit
		}
	}
	// Remap BX for LOADK, GETGLOBAL, GETFIELD (constant index)
	switch ir.Op {
	case vm.OP_LOADK:
		if ir.BX < len(proto.Constants) {
			traceConstIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, proto.Constants[ir.BX])
			ir.BX = traceConstIdx
		}
	case vm.OP_GETFIELD:
		// C is the constant index for the field name
		origC := vm.DecodeC(inst)
		if origC < len(proto.Constants) {
			traceConstIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, proto.Constants[origC])
			ir.C = traceConstIdx // not RK, just constant index
		}
	}
}

// captureTypeInfo fills in AType, BType, CType on the TraceIR from register
// values and constant types at recording time.
func (r *TraceRecorder) captureTypeInfo(ir *TraceIR, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base, a int) {
	ir.AType = safeRegType(regs, base+a)
	if vm.DecodeB(inst) < vm.RKBit {
		ir.BType = safeRegType(regs, base+vm.DecodeB(inst))
	} else {
		constIdx := vm.DecodeB(inst) - vm.RKBit
		if constIdx < len(proto.Constants) {
			ir.BType = proto.Constants[constIdx].Type()
		}
	}
	if vm.DecodeC(inst) < vm.RKBit {
		ir.CType = safeRegType(regs, base+vm.DecodeC(inst))
	} else {
		constIdx := vm.DecodeC(inst) - vm.RKBit
		if constIdx < len(proto.Constants) {
			ir.CType = proto.Constants[constIdx].Type()
		}
	}
}

// captureFieldIndex fills in FieldIndex and ShapeID for GETFIELD/SETFIELD
// instructions by looking up the field position in the table's skeys at
// recording time.
func (r *TraceRecorder) captureFieldIndex(ir *TraceIR, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base, a int) {
	ir.FieldIndex = -1
	op := ir.Op
	if op == vm.OP_GETFIELD || op == vm.OP_SETFIELD {
		origB := vm.DecodeB(inst)
		tableSlot := base + origB
		if op == vm.OP_SETFIELD {
			tableSlot = base + a
		}
		if tableSlot < len(regs) && regs[tableSlot].IsTable() {
			tbl := regs[tableSlot].Table()
			if tbl != nil {
				// Get field name from proto constants (use original C, not remapped)
				origC := vm.DecodeC(inst)
				if origC < len(proto.Constants) {
					fieldName := proto.Constants[origC].Str()
					ir.FieldIndex = tbl.FieldIndex(fieldName)
					ir.ShapeID = tbl.ShapeID()
				}
			}
		}
	}
}

// recordInlinedReturn handles RETURN from an inlined function by emitting a
// synthetic MOVE to copy the callee's return value to the caller's
// call-destination register, then decrementing the inline depth.
func (r *TraceRecorder) recordInlinedReturn(ir TraceIR, origA, origB int, pc int, proto *vm.FuncProto, regs []runtime.Value, base int) {
	// RETURN A B: returns R(A)..R(A+B-2). We only handle single return (B=2).
	// The callee's return register is ir.A (trace-relative remappedA).
	if len(r.inlineCallStack) > 0 && origB >= 2 {
		callDst := r.inlineCallStack[len(r.inlineCallStack)-1]
		r.inlineCallStack = r.inlineCallStack[:len(r.inlineCallStack)-1]
		retSrc := ir.A // callee's R(A) in trace-relative coords
		if retSrc != callDst {
			// Emit synthetic MOVE at callee's depth (depth > 0) to copy
			// the return value to the caller's call register.
			moveIR := TraceIR{
				Op:    vm.OP_MOVE,
				A:     callDst,
				B:     retSrc,
				PC:    pc,
				Proto: proto,
				Depth: r.depth, // still at callee depth (before decrement)
				Base:  base,
				BType: safeRegType(regs, base+origA), // type of return value
			}
			r.current.IR = append(r.current.IR, moveIR)
		}
	} else if len(r.inlineCallStack) > 0 {
		// No return value (B<2) or void return — just pop the stack
		r.inlineCallStack = r.inlineCallStack[:len(r.inlineCallStack)-1]
	}
	r.depth--
}

// recordNestedForPrep handles FORPREP for nested loops at root depth (depth==0).
// Two strategies:
//  1. Full nesting: record one inner iteration inline (no sub-trace call).
//     Eliminates ~61 instruction prologue/epilogue per inner call.
//  2. Sub-trace calling: skip inner body, call pre-compiled inner trace.
//     Fallback when full nesting is already in use (deeper nesting).
//
// Priority: full nesting first (better codegen, unified register allocation).
// Returns false (never stops execution).
func (r *TraceRecorder) recordNestedForPrep(ir TraceIR, proto *vm.FuncProto) bool {
	forloopPC := ir.PC + ir.SBX + 1

	// Strategy 0: When already in full-nesting mode (innerLoopDepth > 0) and
	// a compiled inner trace exists, use sub-trace calling to avoid triple nesting.
	// Example: y-loop (full nesting x-loop) encounters iter-loop FORPREP —
	// use compiled iter-loop trace instead of going 3 levels deep.
	if r.innerLoopDepth > 0 {
		innerKey := loopKey{proto: proto, pc: forloopPC}
		if innerCT, ok := r.compiled[innerKey]; ok && innerCT != nil {
			r.innerLoopSkipStart = ir.PC + 1
			r.innerLoopSkipEnd = forloopPC
			ir.FieldIndex = forloopPC
			r.current.IR = append(r.current.IR, ir)
			return false
		}
	}

	// Strategy 1: Full nested recording (preferred for first level of nesting).
	// Record the FORPREP normally, then record exactly ONE iteration
	// of the inner body. The inner FORLOOP will be recorded too.
	// Remaining inner iterations are skipped via innerLoopSkipEnd.
	if r.innerLoopDepth == 0 {
		r.innerLoopDepth = 1
		r.innerLoopForPC = forloopPC
		r.innerLoopFirstSeen = false
		r.innerLoopRecorded = false
		ir.FieldIndex = 0
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Deeper nesting: Strategy 0 already handles compiled inner traces above.
	// If we reach here, there's no compiled trace for this inner loop — abort.
	r.abortAndBlacklist()
	return false
}

// recordInnerForLoop handles FORLOOP during full nested recording.
// The FORPREP jumps directly to the FORLOOP. So the sequence is:
//  1. FORPREP recorded -> interpreter jumps to FORLOOP
//  2. First FORLOOP encounter (setup): DON'T record. Let interpreter
//     execute it to check condition and jump to body start.
//  3. Body instructions: recorded normally.
//  4. Second FORLOOP encounter (after body): record it, then skip remaining.
func (r *TraceRecorder) recordInnerForLoop(ir TraceIR) {
	if !r.innerLoopFirstSeen {
		// First encounter (right after FORPREP): skip this FORLOOP.
		// The interpreter will execute it, increment idx, check limit,
		// and if the loop continues, jump to the body start.
		r.innerLoopFirstSeen = true
		return
	}
	if !r.innerLoopRecorded {
		// Second encounter (after one body iteration): record the FORLOOP
		// and set up skip for remaining inner iterations.
		r.innerLoopRecorded = true
		r.current.IR = append(r.current.IR, ir)
		// Skip remaining inner iterations.
		r.innerLoopSkipStart = ir.PC + ir.SBX + 1 // inner body start
		r.innerLoopSkipEnd = ir.PC                 // inner FORLOOP PC (inclusive)
		return
	}
	// Subsequent encounters should not happen (skip is active)
}

// recordJmp handles JMP instructions at root depth, detecting unconditional
// break statements that exit the loop. Returns true if the instruction was
// handled (aborted or skipped), false if it should be recorded normally.
func (r *TraceRecorder) recordJmp(ir TraceIR, pc int, inst uint32) bool {
	jmpTarget := pc + vm.DecodesBx(inst) + 1
	if jmpTarget > r.current.LoopPC {
		// Check if the previous recorded instruction was a comparison/test.
		// If so, this JMP is a conditional skip (if-else), not a break.
		isConditionalSkip := false
		if len(r.current.IR) > 0 {
			prevOp := r.current.IR[len(r.current.IR)-1].Op
			switch prevOp {
			case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
				isConditionalSkip = true
			}
		}
		if !isConditionalSkip {
			// Unconditional JMP past loop = break
			r.abortTrace()
			return true
		}
	}
	return false
}

// handleCall attempts to inline a function call into the trace.
func (r *TraceRecorder) handleCall(ir TraceIR, regs []runtime.Value, base int) bool {
	if r.depth >= r.maxDepth {
		// Too deep — record as a CALL (will be side-exit in compilation)
		r.current.IR = append(r.current.IR, ir)
		r.skipDepth = 1
		return false
	}

	// Check if the callee is a VM closure we can inline
	// ir.A is trace-relative; add startBase to get absolute register index
	absIdx := r.startBase + ir.A
	if absIdx < 0 || absIdx >= len(regs) {
		r.current.IR = append(r.current.IR, ir)
		return false
	}
	fnVal := regs[absIdx]
	if !fnVal.IsFunction() {
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	cl, ok := fnVal.Ptr().(*vm.Closure)
	if !ok || cl == nil {
		// Check for intrinsic GoFunctions (bit32.bxor, etc.)
		if gf := fnVal.GoFunction(); gf != nil {
			if intrinsic := recognizeIntrinsic(gf.Name); intrinsic != IntrinsicNone {
				ir.Intrinsic = intrinsic
				r.current.IR = append(r.current.IR, ir)
				return false
			}
		}
		// Unknown GoFunction — side-exit
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Check for self-recursion: callee is the same function as the trace's loop function
	if cl.Proto == r.current.LoopProto {
		// Self-recursive call — record as CALL (trace compiler handles natively)
		ir.IsSelfCall = true
		r.current.HasSelfCalls = true
		r.current.IR = append(r.current.IR, ir)
		r.skipDepth = 1
		return false
	}

	// Check if callee has a for-loop (FORPREP) — can't inline those
	for _, inst := range cl.Proto.Code {
		if vm.DecodeOp(inst) == vm.OP_FORPREP {
			// Callee has nested loop — record as CALL (side-exit).
			// Skip the callee's instructions until it returns.
			r.current.IR = append(r.current.IR, ir)
			r.skipDepth = 1
			return false
		}
	}

	// Simple callee without loops: inline it.
	// Save the call info for partial-execution detection and set skipNextJIT
	// so the VM skips Method JIT for this callee (allowing full trace recording).
	irCopy := ir
	r.inlineCallProto = cl.Proto
	r.inlineCallIR = &irCopy
	r.inlineCallDepth = r.depth
	r.skipNextJIT = true
	// Push the call destination slot so the RETURN handler can emit a synthetic
	// MOVE from the callee's return register to the caller's call register.
	r.inlineCallStack = append(r.inlineCallStack, ir.A)
	r.depth++
	return false
}

// shouldAbort returns true for opcodes that can't be traced.
func (r *TraceRecorder) shouldAbort(op vm.Opcode) bool {
	switch op {
	case vm.OP_GO, vm.OP_SEND, vm.OP_RECV, vm.OP_MAKECHAN:
		return true // concurrency ops
	case vm.OP_TFORCALL, vm.OP_TFORLOOP:
		return true // generic for (complex iterator)
	case vm.OP_FORPREP:
		return false // SSA codegen handles nested loops
	}
	return false
}

// recognizeIntrinsic returns the intrinsic ID for a known GoFunction, or 0.
func recognizeIntrinsic(name string) int {
	switch name {
	case "bit32.bxor":
		return IntrinsicBxor
	case "bit32.band":
		return IntrinsicBand
	case "bit32.bor":
		return IntrinsicBor
	case "bit32.bnot":
		return IntrinsicBnot
	case "bit32.lshift":
		return IntrinsicLshift
	case "bit32.rshift":
		return IntrinsicRshift
	case "math.sqrt":
		return IntrinsicSqrt
	}
	return IntrinsicNone
}

// safeRegType returns the type of a register, handling out-of-range gracefully.
func safeRegType(regs []runtime.Value, idx int) runtime.ValueType {
	if idx < 0 || idx >= len(regs) {
		return runtime.TypeNil
	}
	return regs[idx].Type()
}
