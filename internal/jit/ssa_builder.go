package jit

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

type ssaBuilder struct {
	trace    *Trace
	insts    []SSAInst
	slotDefs map[int]SSARef  // VM register → current SSA definition
	slotType map[int]SSAType // VM register → known type

	// Full nested loop tracking
	loopEmitted bool // true after SSA_LOOP is emitted (outer loop header)
	innerLoop   bool // true when processing inner loop body
}

func (b *ssaBuilder) emit(inst SSAInst) SSARef {
	ref := SSARef(len(b.insts))
	b.insts = append(b.insts, inst)
	return ref
}

func (b *ssaBuilder) build() *SSAFunc {
	// Phase 1: Scan trace to determine initial slot types from recording
	for _, ir := range b.trace.IR {
		if ir.BType == runtime.TypeInt {
			b.slotType[ir.B] = SSATypeInt
		} else if ir.BType == runtime.TypeFloat {
			b.slotType[ir.B] = SSATypeFloat
		}
		if ir.CType == runtime.TypeInt {
			b.slotType[ir.C] = SSATypeInt
		} else if ir.CType == runtime.TypeFloat {
			b.slotType[ir.C] = SSATypeFloat
		}
	}

	// Phase 2: Emit guards for loop entry using liveness analysis.
	// Only emit guards for slots that are LIVE at the loop header — i.e., their
	// value from the previous iteration is actually read in the current iteration
	// before being overwritten. Dead slots (overwritten before first read) get
	// no guards, eliminating false guard-fails from stale types.
	liveIn, slotRuntimeType, slotPC := computeLiveIn(b.trace)
	guardedSlots := make(map[int]bool)
	for slot := range liveIn {
		if guardedSlots[slot] {
			continue
		}
		typ, ok := slotRuntimeType[slot]
		if !ok {
			continue
		}
		pc := slotPC[slot]
		b.emitGuard(slot, typ, pc)
		guardedSlots[slot] = true
	}

	// Phase 3: Emit LOOP marker
	b.emit(SSAInst{Op: SSA_LOOP})
	b.loopEmitted = true

	// Phase 4: Convert each trace instruction to SSA
	for i := range b.trace.IR {
		b.convertIR(i, &b.trace.IR[i])
	}

	return &SSAFunc{Insts: b.insts, Trace: b.trace}
}

func (b *ssaBuilder) emitGuard(slot int, typ runtime.ValueType, pc int) {
	loadRef := b.emit(SSAInst{
		Op:   SSA_LOAD_SLOT,
		Type: SSATypeUnknown,
		Slot: int16(slot),
		PC:   pc,
	})
	b.slotDefs[slot] = loadRef

	var ssaType SSAType
	switch typ {
	case runtime.TypeInt:
		ssaType = SSATypeInt
	case runtime.TypeFloat:
		ssaType = SSATypeFloat
	case runtime.TypeTable:
		ssaType = SSATypeTable
	case runtime.TypeString:
		ssaType = SSATypeString
	default:
		ssaType = SSATypeUnknown
	}

	b.emit(SSAInst{
		Op:     SSA_GUARD_TYPE,
		Type:   ssaType,
		Arg1:   loadRef,
		AuxInt: int64(typ),
		PC:     pc,
	})

	// After guard, the slot has known type
	b.slotType[slot] = ssaType

	// Emit unbox for known types
	if ssaType == SSATypeInt {
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_INT,
			Type: SSATypeInt,
			Arg1: loadRef,
			Slot: int16(slot),
		})
		b.slotDefs[slot] = unboxRef
	} else if ssaType == SSATypeFloat {
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_FLOAT,
			Type: SSATypeFloat,
			Arg1: loadRef,
			Slot: int16(slot),
		})
		b.slotDefs[slot] = unboxRef
	}
}

func (b *ssaBuilder) convertIR(idx int, ir *TraceIR) {
	switch ir.Op {
	// Arithmetic
	case vm.OP_ADD:
		b.convertArithTyped(ir, SSA_ADD_INT, SSA_ADD_FLOAT)
	case vm.OP_SUB:
		b.convertArithTyped(ir, SSA_SUB_INT, SSA_SUB_FLOAT)
	case vm.OP_MUL:
		b.convertArithTyped(ir, SSA_MUL_INT, SSA_MUL_FLOAT)
	case vm.OP_DIV:
		b.convertArith(ir, SSA_DIV_FLOAT) // division always float
	case vm.OP_MOD:
		b.convertArith(ir, SSA_MOD_INT)
	case vm.OP_UNM:
		src := b.getSlotRef(ir.B)
		ref := b.emit(SSAInst{Op: SSA_NEG_INT, Type: SSATypeInt, Arg1: src, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	// For-loop control
	case vm.OP_FORLOOP:
		b.convertForLoop(ir)
	case vm.OP_FORPREP:
		b.convertForPrep(ir)

	// Loads and moves
	case vm.OP_MOVE, vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADBOOL:
		b.convertLoadOp(ir)

	// Globals
	case vm.OP_GETGLOBAL:
		// GETGLOBAL: load global value captured at recording time.
		// The value is stored in trace.Constants[ir.BX] by the recorder.
		ref := b.emit(SSAInst{
			Op: SSA_LOAD_GLOBAL, Type: SSATypeUnknown,
			Slot: int16(ir.A), PC: ir.PC,
			AuxInt: int64(ir.BX), // constant pool index
		})
		b.slotDefs[ir.A] = ref

	// Comparisons and guards
	case vm.OP_LT:
		b.convertComparison(idx, ir, SSA_LT_INT, SSA_LT_FLOAT)
	case vm.OP_LE:
		b.convertComparison(idx, ir, SSA_LE_INT, SSA_LE_FLOAT)
	case vm.OP_EQ:
		arg1 := b.getSlotOrRK(ir.B)
		arg2 := b.getSlotOrRK(ir.C)
		b.emit(SSAInst{Op: SSA_EQ_INT, Type: SSATypeBool, Arg1: arg1, Arg2: arg2, AuxInt: int64(ir.A), PC: ir.PC})
	case vm.OP_TEST:
		b.convertTest(idx, ir)

	// Table and field access
	case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD:
		b.convertTableOp(idx, ir)

	// Function calls
	case vm.OP_CALL:
		b.convertCall(idx, ir)

	case vm.OP_JMP:
		// JMP in trace body: no-op (trace is linear; guards handle branching)

	default:
		// Unsupported op → side-exit marker
		b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
	}
}

// convertForPrep handles OP_FORPREP: R(A) -= R(A+2), plus inner loop setup.
func (b *ssaBuilder) convertForPrep(ir *TraceIR) {
	// FORPREP: R(A) -= R(A+2)
	//
	// For inner FORPREPs (full nested loop), the LOADINT instructions
	// before the FORPREP already set the correct init/limit/step values
	// in slotDefs. We use those directly — no need to force a memory reload.
	// The LOADINT constants are re-executed on each outer iteration in the
	// compiled code, overwriting any stale values from the inner FORLOOP.
	init := b.getSlotRef(ir.A)
	step := b.getSlotRef(ir.A + 2)
	ref := b.emit(SSAInst{Op: SSA_SUB_INT, Type: SSATypeInt, Arg1: init, Arg2: step, Slot: int16(ir.A), PC: ir.PC})
	b.slotDefs[ir.A] = ref
	b.slotType[ir.A] = SSATypeInt

	if b.loopEmitted && ir.FieldIndex == 0 {
		// Full nested loop: simulate the first FORLOOP iteration.
		// In the VM, FORPREP jumps to FORLOOP which increments idx before
		// the body runs. In the compiled trace, the body is recorded BEFORE
		// the FORLOOP instruction. So we must do the first increment here
		// to match the VM's semantics.

		// Simulate first FORLOOP: R(A) += R(A+2) → idx = init
		incRef := b.emit(SSAInst{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: ref, Arg2: step, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = incRef

		// Set loop variable: R(A+3) = idx
		moveRef := b.emit(SSAInst{Op: SSA_MOVE, Type: SSATypeInt, Arg1: incRef, Slot: int16(ir.A + 3), PC: ir.PC})
		b.slotDefs[ir.A+3] = moveRef
		b.slotType[ir.A+3] = SSATypeInt

		// Check if the inner loop should execute at all: idx <= limit
		limit := b.getSlotRef(ir.A + 1)
		b.emit(SSAInst{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: incRef, Arg2: limit, AuxInt: 2, PC: ir.PC})
		// AuxInt=2 means "inner loop entry check" — skip inner loop if GT

		// Emit inner loop label AFTER the first increment
		b.emit(SSAInst{Op: SSA_INNER_LOOP})
		b.innerLoop = true
	} else if ir.FieldIndex > 0 {
		// Sub-trace calling: emit the first FORLOOP iteration
		// (idx += step) before calling the inner trace.
		// The inner trace expects idx to be already incremented by the first
		// FORLOOP (which the interpreter normally does before calling the trace).

		// Simulate first FORLOOP: R(A) += R(A+2) → idx = init
		incRef := b.emit(SSAInst{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: ref, Arg2: step, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = incRef

		// Set loop variable: R(A+3) = idx
		moveRef := b.emit(SSAInst{Op: SSA_MOVE, Type: SSATypeInt, Arg1: incRef, Slot: int16(ir.A + 3), PC: ir.PC})
		b.slotDefs[ir.A+3] = moveRef
		b.slotType[ir.A+3] = SSATypeInt

		b.emit(SSAInst{
			Op:     SSA_CALL_INNER_TRACE,
			Type:   SSATypeUnknown,
			Slot:   int16(ir.A),
			PC:     ir.PC,
			AuxInt: int64(ir.FieldIndex), // inner FORLOOP PC (used for lookup)
		})
	}
}

// convertLoadOp handles OP_MOVE, OP_LOADINT, OP_LOADK, and OP_LOADBOOL.
func (b *ssaBuilder) convertLoadOp(ir *TraceIR) {
	switch ir.Op {
	case vm.OP_MOVE:
		src := b.getSlotRef(ir.B)
		// Emit an actual SSA_MOVE to copy the value from slot B to slot A.
		// A simple alias (slotDefs[A] = src) breaks loop-carried values because
		// the source slot's register is never copied to the destination slot's register,
		// causing stale reads on the next loop iteration.
		ref := b.emit(SSAInst{Op: SSA_MOVE, Type: b.slotType[ir.B], Arg1: src, Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = b.slotType[ir.B]

	case vm.OP_LOADINT:
		ref := b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: int64(ir.SBX), Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_LOADK:
		if ir.BX < len(b.trace.Constants) {
			c := b.trace.Constants[ir.BX]
			if c.IsInt() {
				ref := b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: c.Int(), Slot: int16(ir.A), PC: ir.PC})
				b.slotDefs[ir.A] = ref
				b.slotType[ir.A] = SSATypeInt
			} else if c.IsFloat() {
				ref := b.emit(SSAInst{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: int64(math.Float64bits(c.Float())), Slot: int16(ir.A), PC: ir.PC})
				b.slotDefs[ir.A] = ref
				b.slotType[ir.A] = SSATypeFloat
			} else {
				b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
			}
		} else {
			b.emit(SSAInst{Op: SSA_SIDE_EXIT, PC: ir.PC})
		}

	case vm.OP_LOADBOOL:
		ref := b.emit(SSAInst{Op: SSA_CONST_BOOL, Type: SSATypeBool, AuxInt: int64(ir.B), Slot: int16(ir.A), PC: ir.PC})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeBool
	}
}

// convertTest handles OP_TEST: if (Truthy(R(A)) ~= bool(C)) then skip.
func (b *ssaBuilder) convertTest(idx int, ir *TraceIR) {
	// Detect skip/no-skip by checking if next instruction is JMP.
	didSkip := true
	if idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP {
		didSkip = false
	}
	// AuxInt: 0=expect truthy, 1=expect falsy
	auxInt := int64(ir.C)
	if !didSkip {
		auxInt = 1 - auxInt
	}
	src := b.getSlotRef(ir.A)
	b.emit(SSAInst{Op: SSA_GUARD_TRUTHY, Type: SSATypeBool, Arg1: src, Slot: int16(ir.A), AuxInt: auxInt, PC: ir.PC})
}

// convertTableOp handles OP_GETTABLE, OP_SETTABLE, OP_GETFIELD, and OP_SETFIELD.
func (b *ssaBuilder) convertTableOp(idx int, ir *TraceIR) {
	switch ir.Op {
	case vm.OP_GETTABLE:
		// GETTABLE A B C: R(A) = R(B)[RK(C)]
		tableRef := b.getSlotRef(ir.B)
		keyRef := b.getSlotOrRK(ir.C)

		// Use result type for specialization.
		// AType is the stale pre-execution type of R(A), so it's unreliable.
		// Instead, scan forward to see how the result slot is consumed.
		ssaType := b.inferResultType(idx, ir.A)
		// Fallback to AType if forward scan didn't find a consumer
		if ssaType == SSATypeUnknown {
			switch ir.AType {
			case runtime.TypeInt:
				ssaType = SSATypeInt
			case runtime.TypeFloat:
				ssaType = SSATypeFloat
			case runtime.TypeBool:
				ssaType = SSATypeInt
			}
		}

		ref := b.emit(SSAInst{
			Op: SSA_LOAD_ARRAY, Type: ssaType,
			Arg1: tableRef, Arg2: keyRef,
			Slot: int16(ir.A), PC: ir.PC,
		})
		b.slotDefs[ir.A] = ref
		if ssaType != SSATypeUnknown {
			b.slotType[ir.A] = ssaType
		}

	case vm.OP_SETTABLE:
		// SETTABLE A B C: R(A)[RK(B)] = RK(C)
		tableRef := b.getSlotRef(ir.A)
		keyRef := b.getSlotOrRK(ir.B)
		valRef := b.getSlotOrRK(ir.C)
		b.emit(SSAInst{
			Op: SSA_STORE_ARRAY, Type: SSATypeUnknown,
			Arg1: tableRef, Arg2: keyRef,
			Slot: int16(ir.A), PC: ir.PC,
			AuxInt: int64(valRef), // store value ref in AuxInt
		})

	case vm.OP_GETFIELD:
		// GETFIELD A B C: R(A) = R(B).Constants[C]
		// AuxInt packs fieldIndex (low 32) + shapeID (high 32)
		tableRef := b.getSlotRef(ir.B)
		auxInt := int64(uint32(ir.FieldIndex)) | (int64(ir.ShapeID) << 32)
		ref := b.emit(SSAInst{
			Op: SSA_LOAD_FIELD, Type: SSATypeUnknown,
			Arg1: tableRef,
			Slot: int16(ir.A), PC: ir.PC,
			AuxInt: auxInt,
		})
		b.slotDefs[ir.A] = ref

	case vm.OP_SETFIELD:
		// SETFIELD A B C: R(A).Constants[B] = RK(C)
		tableRef := b.getSlotRef(ir.A)
		valRef := b.getSlotOrRK(ir.C)
		auxInt := int64(uint32(ir.FieldIndex)) | (int64(ir.ShapeID) << 32)
		b.emit(SSAInst{
			Op: SSA_STORE_FIELD, Type: SSATypeUnknown,
			Arg1: tableRef, Arg2: valRef,
			Slot: int16(ir.A), PC: ir.PC,
			AuxInt: auxInt,
		})
	}
}

// convertCall handles OP_CALL: intrinsic calls and non-intrinsic VM calls.
func (b *ssaBuilder) convertCall(idx int, ir *TraceIR) {
	if ir.Intrinsic != IntrinsicNone {
		// Recognized intrinsic GoFunction → SSA_INTRINSIC
		// CALL A B C: R(A) = fn(R(A+1), ..., R(A+B-1))
		arg1Ref := b.getSlotRef(ir.A + 1)
		var arg2Ref SSARef = SSARefNone
		if ir.B > 2 { // binary op has 2 args
			arg2Ref = b.getSlotRef(ir.A + 2)
		}
		ref := b.emit(SSAInst{
			Op:     SSA_INTRINSIC,
			Type:   SSATypeFloat, // most intrinsics return float (sqrt, etc.)
			Arg1:   arg1Ref,
			Arg2:   arg2Ref,
			Slot:   int16(ir.A),
			PC:     ir.PC,
			AuxInt: int64(ir.Intrinsic),
		})
		b.slotDefs[ir.A] = ref
		b.slotType[ir.A] = SSATypeFloat
	} else {
		// Non-intrinsic CALL → call-exit (VM executes the call, then trace resumes)
		b.emit(SSAInst{Op: SSA_CALL, PC: ir.PC, Slot: int16(ir.A), AuxInt: int64(ir.B)<<16 | int64(ir.C)})
		// Result is now in R(A) in memory after the VM call — reload it.
		// Scan forward to find how the result is used, to get the correct type.
		resultType := b.inferResultType(idx, ir.A)
		ref := b.emit(SSAInst{Op: SSA_LOAD_SLOT, Slot: int16(ir.A), Type: resultType})
		b.slotDefs[ir.A] = ref
		if resultType != SSATypeUnknown {
			b.slotType[ir.A] = resultType
		} else {
			delete(b.slotType, ir.A)
		}
	}
}

// inferResultType scans forward in the trace from position idx to determine the
// SSA type of a result written to resultSlot, based on how downstream instructions
// consume it. Returns SSATypeUnknown if no consumer reveals the type.
func (b *ssaBuilder) inferResultType(idx int, resultSlot int) SSAType {
	for i := idx + 1; i < len(b.trace.IR); i++ {
		futureIR := &b.trace.IR[i]
		switch futureIR.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV:
			if futureIR.B < 256 && futureIR.B == resultSlot {
				if t := ssaTypFromRuntime(futureIR.BType); t != SSATypeUnknown {
					return t
				}
			}
			if futureIR.C < 256 && futureIR.C == resultSlot {
				if t := ssaTypFromRuntime(futureIR.CType); t != SSATypeUnknown {
					return t
				}
			}
		case vm.OP_MOVE:
			if futureIR.B == resultSlot {
				if t := ssaTypFromRuntime(futureIR.BType); t != SSATypeUnknown {
					return t
				}
			}
		}
		// If the slot is overwritten before being read, give up
		if getWriteSlotContains(futureIR, resultSlot) {
			break
		}
	}
	return SSATypeUnknown
}

func (b *ssaBuilder) convertArith(ir *TraceIR, op SSAOp) {
	arg1 := b.getSlotOrRK(ir.B)
	arg2 := b.getSlotOrRK(ir.C)
	typ := SSATypeInt
	if isFloatOp(op) {
		typ = SSATypeFloat
	}
	ref := b.emit(SSAInst{Op: op, Type: typ, Arg1: arg1, Arg2: arg2, Slot: int16(ir.A), PC: ir.PC})
	b.slotDefs[ir.A] = ref
	b.slotType[ir.A] = typ
}

// convertArithTyped picks the int or float SSA op based on operand types.
func (b *ssaBuilder) convertArithTyped(ir *TraceIR, intOp, floatOp SSAOp) {
	// Determine operand types. Prefer SSA-level type info (slotType), but
	// fall back to recorded runtime types if the SSA type is unknown
	// (e.g., after a CALL whose return type is not known at SSA build time).
	bType := b.slotType[ir.B]
	cType := b.slotType[ir.C]
	if ir.B >= vm.RKBit {
		bType = ssaTypFromRuntime(ir.BType)
	} else if bType == SSATypeUnknown {
		bType = ssaTypFromRuntime(ir.BType)
	}
	if ir.C >= vm.RKBit {
		cType = ssaTypFromRuntime(ir.CType)
	} else if cType == SSATypeUnknown {
		cType = ssaTypFromRuntime(ir.CType)
	}

	// If either operand is float, use float op
	if bType == SSATypeFloat || cType == SSATypeFloat {
		b.convertArith(ir, floatOp)
	} else {
		b.convertArith(ir, intOp)
	}
}

func isFloatOp(op SSAOp) bool {
	switch op {
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
		SSA_FMADD, SSA_FMSUB,
		SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	}
	return false
}

func (b *ssaBuilder) convertForLoop(ir *TraceIR) {
	// idx += step
	idx := b.getSlotRef(ir.A)
	step := b.getSlotRef(ir.A + 2)
	newIdx := b.emit(SSAInst{Op: SSA_ADD_INT, Type: SSATypeInt, Arg1: idx, Arg2: step, Slot: int16(ir.A), PC: ir.PC})
	b.slotDefs[ir.A] = newIdx

	// R(A+3) = idx (loop variable) — emit explicit MOVE so liveness analysis
	// sees that slot A+3 is written and needs store-back.
	moveRef := b.emit(SSAInst{Op: SSA_MOVE, Type: SSATypeInt, Arg1: newIdx, Slot: int16(ir.A + 3), PC: ir.PC})
	b.slotDefs[ir.A+3] = moveRef
	b.slotType[ir.A+3] = SSATypeInt

	// Compare: idx <= limit
	limit := b.getSlotRef(ir.A + 1)

	if b.innerLoop {
		// Inner FORLOOP: use AuxInt=1 to mark this as an inner loop exit check.
		// The codegen will emit a branch back to the inner loop header (not the
		// outer loop header) on success, and fall through on exit.
		b.emit(SSAInst{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: newIdx, Arg2: limit, AuxInt: 1, PC: ir.PC})
		b.innerLoop = false

		// After the inner FORLOOP, delete slotDefs for inner control slots
		// so the outer body reads fresh values from memory.
		delete(b.slotDefs, ir.A)
		delete(b.slotDefs, ir.A+1)
		delete(b.slotDefs, ir.A+2)
		delete(b.slotDefs, ir.A+3)
	} else {
		// Outer FORLOOP: standard loop exit check
		b.emit(SSAInst{Op: SSA_LE_INT, Type: SSATypeBool, Arg1: newIdx, Arg2: limit, PC: ir.PC})
	}
}

func (b *ssaBuilder) getSlotRef(slot int) SSARef {
	if ref, ok := b.slotDefs[slot]; ok {
		return ref
	}
	// Not yet defined → load from memory
	ref := b.emit(SSAInst{Op: SSA_LOAD_SLOT, Type: b.slotType[slot], Slot: int16(slot)})
	b.slotDefs[slot] = ref
	return ref
}

func (b *ssaBuilder) getSlotOrRK(idx int) SSARef {
	if idx >= vm.RKBit {
		// Constant from pool — not bound to any VM slot
		constIdx := idx - vm.RKBit
		if constIdx < len(b.trace.Constants) {
			c := b.trace.Constants[constIdx]
			if c.IsInt() {
				return b.emit(SSAInst{Op: SSA_CONST_INT, Type: SSATypeInt, AuxInt: c.Int(), Slot: -1})
			}
			if c.IsFloat() {
				return b.emit(SSAInst{Op: SSA_CONST_FLOAT, Type: SSATypeFloat, AuxInt: int64(math.Float64bits(c.Float())), Slot: -1})
			}
		}
		return b.emit(SSAInst{Op: SSA_LOAD_SLOT, Type: SSATypeUnknown, Slot: int16(idx)})
	}
	return b.getSlotRef(idx)
}

// convertComparison handles OP_LT / OP_LE with typed int/float dispatch.
//
// OP_LT A B C: if (RK(B) < RK(C)) != bool(A) then PC++ (skip next JMP)
//
// The guard must reproduce the SAME skip/no-skip behavior as the recording.
// We detect which path was taken by checking the next instruction in the trace:
//   - Next is JMP → recording saw NO skip (comparison didn't trigger)
//   - Next is NOT JMP → recording saw SKIP (comparison triggered, JMP was skipped)
//
// AuxInt encoding: 0 = guard expects comparison TRUE, 1 = guard expects FALSE.
// For skip path: (B<C) != bool(A) was TRUE, so (B<C) == !bool(A)
// For no-skip path: (B<C) != bool(A) was FALSE, so (B<C) == bool(A)
func (b *ssaBuilder) convertComparison(idx int, ir *TraceIR, intOp, floatOp SSAOp) {
	arg1 := b.getSlotOrRK(ir.B)
	arg2 := b.getSlotOrRK(ir.C)

	// Determine types
	bType := b.slotType[ir.B]
	cType := b.slotType[ir.C]
	if ir.B >= vm.RKBit {
		bType = ssaTypFromRuntime(ir.BType)
	}
	if ir.C >= vm.RKBit {
		cType = ssaTypFromRuntime(ir.CType)
	}

	op := intOp
	if bType == SSATypeFloat || cType == SSATypeFloat {
		op = floatOp
	}

	// Detect skip vs no-skip by looking at the next trace instruction
	didSkip := true
	if idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP {
		didSkip = false // JMP follows → comparison didn't skip it
	}

	// Encode guard polarity in AuxInt:
	// For LT: the comparison is (B < C)
	//   didSkip + A=0: skip when B<C → guard: expect B<C → AuxInt=0 (exit on GE)
	//   didSkip + A=1: skip when B>=C → guard: expect B>=C → AuxInt=1 (exit on LT)
	//   !didSkip + A=0: no-skip when B>=C → guard: expect B>=C → AuxInt=1 (exit on LT)
	//   !didSkip + A=1: no-skip when B<C → guard: expect B<C → AuxInt=0 (exit on GE)
	auxInt := int64(ir.A)
	if !didSkip {
		// Invert: no-skip means the comparison result MATCHED bool(A),
		// so the guard expects the opposite condition from the skip case
		auxInt = 1 - auxInt
	}

	b.emit(SSAInst{Op: op, Type: SSATypeBool, Arg1: arg1, Arg2: arg2, AuxInt: auxInt, PC: ir.PC})
}

// ssaTypFromRuntime converts runtime.ValueType to SSAType.
func ssaTypFromRuntime(t runtime.ValueType) SSAType {
	switch t {
	case runtime.TypeInt:
		return SSATypeInt
	case runtime.TypeFloat:
		return SSATypeFloat
	case runtime.TypeBool:
		return SSATypeBool
	case runtime.TypeNil:
		return SSATypeNil
	case runtime.TypeTable:
		return SSATypeTable
	case runtime.TypeString:
		return SSATypeString
	default:
		return SSATypeUnknown
	}
}
