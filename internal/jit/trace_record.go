//go:build darwin && arm64

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

	// Handle deferred GETGLOBAL constant capture.
	// OnInstruction is called BEFORE the VM executes the instruction, so at
	// the previous call for a GETGLOBAL, regs[base+a] still held the OLD value.
	// Now that the VM has executed the GETGLOBAL, we can capture the actual value.
	if r.pendingGlobalCapture {
		r.pendingGlobalCapture = false
		idx := r.pendingGlobalCaptureIdx
		reg := r.pendingGlobalCaptureReg
		if idx < len(r.current.IR) && reg >= 0 && reg < len(regs) {
			constIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, regs[reg])
			r.current.IR[idx].BX = constIdx
			// Also fix AType: at recording time, AType captured the PRE-execution
			// type of the destination register (which is stale). Now we have the
			// actual value, so update AType to match the real result type.
			r.current.IR[idx].AType = regs[reg].Type()
		}
	}

	// Skip instructions from non-inlined callee functions.
	if r.skipDepth > 0 {
		op := vm.DecodeOp(inst)
		if op == vm.OP_CALL {
			r.skipDepth++
		} else if op == vm.OP_RETURN {
			r.skipDepth--
		}
		return false
	}

	// Detect Method JIT partial execution of inlined callee.
	if r.inlineCallProto != nil && r.depth > r.inlineCallDepth {
		if proto == r.inlineCallProto && pc != 0 {
			// Method JIT partially executed the callee. Undo the inline.
			r.depth = r.inlineCallDepth
			r.current.IR = append(r.current.IR, *r.inlineCallIR)
			r.skipDepth = 1
			r.inlineCallProto = nil
			r.inlineCallIR = nil
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
			// Past inner loop -- resume recording
			r.innerLoopSkipEnd = 0
			r.innerLoopSkipStart = 0
			if r.innerLoopDepth > 0 {
				r.innerLoopDepth = 0
				r.innerLoopForPC = 0
				r.innerLoopFirstSeen = false
				r.innerLoopRecorded = false
			}
			// Fall through to record this instruction
		} else {
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

	// Build the trace IR for this instruction
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

	// If we encounter a FORLOOP that is NOT our recorded loop,
	// the inner loop has exited. Don't record it.
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

	// Immediately finish trace after recording the FORLOOP at LoopPC.
	// This prevents the trace from "overshooting" when the VM's FORLOOP
	// exits (falls through) instead of looping back. Without this, the
	// recorder would continue recording instructions past the loop exit
	// (outer loop body), producing a trace that mixes inner loop body
	// with outer loop body code -- causing infinite loops in JIT.
	if op == vm.OP_FORLOOP && r.depth == 0 && pc == r.current.LoopPC {
		r.finishTrace()
		return false
	}

	// Set up deferred GETGLOBAL capture for the NEXT instruction call.
	if op == vm.OP_GETGLOBAL {
		r.pendingGlobalCapture = true
		r.pendingGlobalCaptureIdx = len(r.current.IR) - 1
		r.pendingGlobalCaptureReg = base + vm.DecodeA(inst)
	}

	return false
}

// buildTraceIR decodes and remaps a bytecode instruction into a TraceIR.
func (r *TraceRecorder) buildTraceIR(pc int, inst uint32, proto *vm.FuncProto, regs []runtime.Value, base int) (TraceIR, int, int) {
	op := vm.DecodeOp(inst)
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	// Register offset: remap from absolute base to trace-relative
	baseOff := base - r.startBase

	// For comparison opcodes (EQ, LT, LE), the A field is a boolean flag, not a register.
	remappedA := baseOff + a
	switch op {
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE:
		remappedA = a
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
	if b < vm.RKBit {
		ir.B = baseOff + b
	}
	if c < vm.RKBit {
		ir.C = baseOff + c
	}

	// For inlined functions (depth > 0), remap constant references
	if r.depth > 0 {
		r.remapInlinedConstants(&ir, inst, proto, a, b, c)
	}

	// Capture type info
	r.captureTypeInfo(&ir, inst, proto, regs, base, a)

	// GETGLOBAL: BX will be set by deferred capture at the NEXT instruction.
	// We do NOT capture regs[base+a] here because the VM hasn't executed
	// this instruction yet.

	// Capture field index for GETFIELD/SETFIELD
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
	switch ir.Op {
	case vm.OP_LOADK:
		if ir.BX < len(proto.Constants) {
			traceConstIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, proto.Constants[ir.BX])
			ir.BX = traceConstIdx
		}
	case vm.OP_GETFIELD:
		origC := vm.DecodeC(inst)
		if origC < len(proto.Constants) {
			traceConstIdx := len(r.current.Constants)
			r.current.Constants = append(r.current.Constants, proto.Constants[origC])
			ir.C = traceConstIdx
		}
	}
}

// captureTypeInfo fills in AType, BType, CType from register values and constants.
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

	// For GETTABLE, AType is the PRE-execution type of the destination register,
	// not the result type. Fix by inspecting the table's array kind to determine
	// the actual element type. This is critical for correctness: if a table contains
	// floats but the destination register previously held an int, AType would
	// incorrectly be TypeInt, causing the JIT to compile LOAD_ARRAY as int-typed.
	op := vm.DecodeOp(inst)
	if op == vm.OP_GETTABLE {
		origB := vm.DecodeB(inst)
		origC := vm.DecodeC(inst)
		tableSlot := base + origB
		if tableSlot >= 0 && tableSlot < len(regs) && regs[tableSlot].IsTable() {
			tbl := regs[tableSlot].Table()
			if tbl != nil {
				ak := tbl.GetArrayKind()
				switch ak {
				case runtime.ArrayFloat:
					ir.AType = runtime.TypeFloat
				case runtime.ArrayInt:
					ir.AType = runtime.TypeInt
				case runtime.ArrayBool:
					ir.AType = runtime.TypeBool
				default:
					// ArrayMixed: sample the actual value using the key to determine type.
					// The key is in register C (or constant pool if RK).
					var key runtime.Value
					if origC >= vm.RKBit {
						constIdx := origC - vm.RKBit
						if constIdx < len(proto.Constants) {
							key = proto.Constants[constIdx]
						}
					} else {
						keySlot := base + origC
						if keySlot >= 0 && keySlot < len(regs) {
							key = regs[keySlot]
						}
					}
					if !key.IsNil() {
						sampled := tbl.RawGet(key)
						if !sampled.IsNil() {
							ir.AType = sampled.Type()
						}
					}
				}
			}
		}
	}
}

// captureFieldIndex fills in FieldIndex and ShapeID for GETFIELD/SETFIELD.
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
				// GETFIELD: A B C → field name is Constants[C]
				// SETFIELD: A B C → field name is Constants[B]
				var fieldConstIdx int
				if op == vm.OP_SETFIELD {
					fieldConstIdx = origB
				} else {
					fieldConstIdx = vm.DecodeC(inst)
				}
				if fieldConstIdx < len(proto.Constants) {
					fieldName := proto.Constants[fieldConstIdx].Str()
					ir.FieldIndex = tbl.FieldIndex(fieldName)
					ir.ShapeID = tbl.ShapeID()
				}
			}
		}
	}
}

// recordInlinedReturn handles RETURN from an inlined function.
func (r *TraceRecorder) recordInlinedReturn(ir TraceIR, origA, origB int, pc int, proto *vm.FuncProto, regs []runtime.Value, base int) {
	if len(r.inlineCallStack) > 0 && origB >= 2 {
		callDst := r.inlineCallStack[len(r.inlineCallStack)-1]
		r.inlineCallStack = r.inlineCallStack[:len(r.inlineCallStack)-1]
		retSrc := ir.A
		if retSrc != callDst {
			moveIR := TraceIR{
				Op:    vm.OP_MOVE,
				A:     callDst,
				B:     retSrc,
				PC:    pc,
				Proto: proto,
				Depth: r.depth,
				Base:  base,
				BType: safeRegType(regs, base+origA),
			}
			r.current.IR = append(r.current.IR, moveIR)
		}
	} else if len(r.inlineCallStack) > 0 {
		r.inlineCallStack = r.inlineCallStack[:len(r.inlineCallStack)-1]
	}
	r.depth--
}

// recordNestedForPrep handles FORPREP for nested loops at root depth.
func (r *TraceRecorder) recordNestedForPrep(ir TraceIR, proto *vm.FuncProto) bool {
	forloopPC := ir.PC + ir.SBX + 1

	// Strategy 0: sub-trace calling when already in full-nesting mode
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

	// Strategy 1: Full nested recording.
	// Currently disabled due to register allocation conflicts between slot-level
	// and ref-level float allocators in the outer trace. The inner loop trace
	// with break_exit handles the correctness correctly.
	// TODO: Fix full nesting register allocation and re-enable.
	if r.innerLoopDepth == 0 {
		r.abortAndBlacklist()
		return false
	}

	// Deeper nesting without compiled trace: abort
	r.abortAndBlacklist()
	return false
}

// recordInnerForLoop handles FORLOOP during full nested recording.
func (r *TraceRecorder) recordInnerForLoop(ir TraceIR) {
	if !r.innerLoopFirstSeen {
		r.innerLoopFirstSeen = true
		return
	}
	if !r.innerLoopRecorded {
		r.innerLoopRecorded = true
		r.current.IR = append(r.current.IR, ir)
		r.innerLoopSkipStart = ir.PC + ir.SBX + 1
		r.innerLoopSkipEnd = ir.PC
		return
	}
}

// recordJmp handles JMP instructions at root depth.
func (r *TraceRecorder) recordJmp(ir TraceIR, pc int, inst uint32) bool {
	jmpTarget := pc + vm.DecodesBx(inst) + 1
	if jmpTarget > r.current.LoopPC {
		isConditionalSkip := false
		if len(r.current.IR) > 0 {
			prevOp := r.current.IR[len(r.current.IR)-1].Op
			switch prevOp {
			case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_TESTSET:
				isConditionalSkip = true
			}
		}
		// For while-loop exit detection: if a conditional JMP jumps past LoopPC
		// and it's at the very beginning of the trace (loop condition), the trace
		// recorded the exit iteration. Abort to avoid including post-loop code.
		if isConditionalSkip && len(r.current.IR) <= 1 {
			r.abortTrace()
			return true
		}
		if !isConditionalSkip {
			r.abortTrace()
			return true
		}
	}
	return false
}

// handleCall attempts to inline a function call into the trace.
func (r *TraceRecorder) handleCall(ir TraceIR, regs []runtime.Value, base int) bool {
	if r.depth >= r.maxDepth {
		r.current.IR = append(r.current.IR, ir)
		r.skipDepth = 1
		return false
	}

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
		// Check for intrinsic GoFunctions
		if gf := fnVal.GoFunction(); gf != nil {
			if intrinsic := recognizeIntrinsic(gf.Name); intrinsic != IntrinsicNone {
				ir.Intrinsic = intrinsic
				r.current.IR = append(r.current.IR, ir)
				return false
			}
		}
		// Unknown GoFunction -- side-exit
		r.current.IR = append(r.current.IR, ir)
		return false
	}

	// Check for self-recursion
	if cl.Proto == r.current.LoopProto {
		ir.IsSelfCall = true
		r.current.HasSelfCalls = true
		r.current.IR = append(r.current.IR, ir)
		r.skipDepth = 1
		return false
	}

	// Check if callee has a for-loop -- can't inline those
	for _, inst := range cl.Proto.Code {
		if vm.DecodeOp(inst) == vm.OP_FORPREP {
			r.current.IR = append(r.current.IR, ir)
			r.skipDepth = 1
			return false
		}
	}

	// Simple callee without loops: inline it.
	irCopy := ir
	r.inlineCallProto = cl.Proto
	r.inlineCallIR = &irCopy
	r.inlineCallDepth = r.depth
	r.skipNextJIT = true
	r.inlineCallStack = append(r.inlineCallStack, ir.A)
	r.depth++
	return false
}

// shouldAbort returns true for opcodes that can't be traced.
func (r *TraceRecorder) shouldAbort(op vm.Opcode) bool {
	switch op {
	case vm.OP_GO, vm.OP_SEND, vm.OP_RECV, vm.OP_MAKECHAN:
		return true
	case vm.OP_TFORCALL, vm.OP_TFORLOOP:
		return true
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
