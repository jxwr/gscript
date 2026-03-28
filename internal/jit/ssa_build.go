//go:build darwin && arm64

package jit

import (
	"math"
	"sort"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ssaBuilder converts trace IR to SSA form.
type ssaBuilder struct {
	trace       *Trace
	insts       []SSAInst
	slotValues  map[int]SSARef // current SSA value per VM slot
	preLoopVals map[int]SSARef // pre-loop SSA values (for snapshot sparse encoding)
	slotType    map[int]SSAType
	snapshots   []Snapshot
	loopEmitted bool
}

// emit appends an SSA instruction and returns its reference.
func (b *ssaBuilder) emit(inst SSAInst) SSARef {
	ref := SSARef(len(b.insts))
	b.insts = append(b.insts, inst)
	return ref
}

// findLoopIdx returns the index of the SSA_LOOP marker.
func (b *ssaBuilder) findLoopIdx() int {
	for i, inst := range b.insts {
		if inst.Op == SSA_LOOP {
			return i
		}
	}
	return 0
}

// ssaTypeFromRuntime converts a runtime.ValueType to SSAType.
func ssaTypeFromRuntime(t runtime.ValueType) SSAType {
	switch t {
	case runtime.TypeInt:
		return SSATypeInt
	case runtime.TypeFloat:
		return SSATypeFloat
	case runtime.TypeBool:
		return SSATypeBool
	case runtime.TypeTable:
		return SSATypeTable
	case runtime.TypeString:
		return SSATypeString
	case runtime.TypeNil:
		return SSATypeNil
	default:
		return SSATypeUnknown
	}
}

// emitGuard loads a slot and guards its type.
func (b *ssaBuilder) emitGuard(slot int, typ runtime.ValueType, pc int) {
	loadRef := b.emit(SSAInst{
		Op:   SSA_LOAD_SLOT,
		Type: ssaTypeFromRuntime(typ),
		Slot: int16(slot),
		PC:   pc,
	})
	b.slotValues[slot] = loadRef
	b.preLoopVals[slot] = loadRef

	// Emit unbox for concrete types
	var ssaType SSAType
	switch typ {
	case runtime.TypeInt:
		ssaType = SSATypeInt
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_INT,
			Type: SSATypeInt,
			Arg1: loadRef,
			Slot: int16(slot),
			PC:   pc,
		})
		b.slotValues[slot] = unboxRef
		b.preLoopVals[slot] = unboxRef
	case runtime.TypeFloat:
		ssaType = SSATypeFloat
		unboxRef := b.emit(SSAInst{
			Op:   SSA_UNBOX_FLOAT,
			Type: SSATypeFloat,
			Arg1: loadRef,
			Slot: int16(slot),
			PC:   pc,
		})
		b.slotValues[slot] = unboxRef
		b.preLoopVals[slot] = unboxRef
	default:
		ssaType = ssaTypeFromRuntime(typ)
	}

	// Emit guard
	b.emit(SSAInst{
		Op:     SSA_GUARD_TYPE,
		Arg1:   loadRef,
		AuxInt: int64(typ),
		Slot:   int16(slot),
		PC:     pc,
		Type:   ssaType,
	})
	b.slotType[slot] = ssaType
}

// getSlotRef reads the current SSA value for a slot.
func (b *ssaBuilder) getSlotRef(slot int) SSARef {
	if ref, ok := b.slotValues[slot]; ok {
		return ref
	}
	return SSARefNone
}

// getSlotType returns the current SSAType for a slot.
func (b *ssaBuilder) getSlotType(slot int) SSAType {
	if t, ok := b.slotType[slot]; ok {
		return t
	}
	return SSATypeUnknown
}

// takeSnapshot captures current slot→value mapping.
func (b *ssaBuilder) takeSnapshot(pc int) {
	snap := Snapshot{PC: pc}
	for slot, ref := range b.slotValues {
		if ref == b.preLoopVals[slot] {
			continue // unchanged from pre-loop, memory still has correct value
		}
		snap.Entries = append(snap.Entries, SnapEntry{
			Slot: slot,
			Ref:  ref,
			Type: b.slotType[slot],
		})
	}
	// Sort entries by slot for deterministic output.
	sort.Slice(snap.Entries, func(i, j int) bool {
		return snap.Entries[i].Slot < snap.Entries[j].Slot
	})
	b.snapshots = append(b.snapshots, snap)
}

// getRKRef returns the SSA ref for an RK operand. If the operand is a constant
// (>= RKBit), it emits a constant instruction. Otherwise it returns the slot ref.
func (b *ssaBuilder) getRKRef(rk int, rkType runtime.ValueType, ir *TraceIR) SSARef {
	if vm.IsRK(rk) {
		return b.emitConstFromPool(vm.RKToConstIdx(rk), ir)
	}
	return b.getSlotRef(rk)
}

// getRKType returns the SSAType for an RK operand.
func (b *ssaBuilder) getRKType(rk int, rkType runtime.ValueType) SSAType {
	if vm.IsRK(rk) {
		return ssaTypeFromRuntime(rkType)
	}
	return b.getSlotType(rk)
}

// emitConstFromPool emits a constant from the trace constant pool or the proto's constants.
func (b *ssaBuilder) emitConstFromPool(idx int, ir *TraceIR) SSARef {
	// Try trace-level constants first, then proto constants.
	var val runtime.Value
	if idx < len(b.trace.Constants) {
		val = b.trace.Constants[idx]
	} else if ir.Proto != nil && idx < len(ir.Proto.Constants) {
		val = ir.Proto.Constants[idx]
	} else if b.trace.LoopProto != nil && idx < len(b.trace.LoopProto.Constants) {
		val = b.trace.LoopProto.Constants[idx]
	} else {
		// Fallback: emit nil constant
		return b.emit(SSAInst{Op: SSA_CONST_NIL, Type: SSATypeNil})
	}

	switch val.Type() {
	case runtime.TypeInt:
		ref := b.emit(SSAInst{
			Op:     SSA_CONST_INT,
			Type:   SSATypeInt,
			AuxInt: val.Int(),
			Slot:   -1, // pool constant, not bound to a VM slot
		})
		return ref
	case runtime.TypeFloat:
		ref := b.emit(SSAInst{
			Op:     SSA_CONST_FLOAT,
			Type:   SSATypeFloat,
			AuxInt: int64(math.Float64bits(val.Float())),
			Slot:   -1,
		})
		return ref
	case runtime.TypeBool:
		bv := int64(0)
		if val.Truthy() {
			bv = 1
		}
		ref := b.emit(SSAInst{
			Op:     SSA_CONST_BOOL,
			Type:   SSATypeBool,
			AuxInt: bv,
			Slot:   -1,
		})
		return ref
	default:
		return b.emit(SSAInst{Op: SSA_CONST_NIL, Type: SSATypeNil, Slot: -1})
	}
}

// inferArithType determines the result type for arithmetic on two operands.
func (b *ssaBuilder) inferArithType(bSlot, cSlot int, ir *TraceIR) SSAType {
	bt := b.getRKType(bSlot, ir.BType)
	ct := b.getRKType(cSlot, ir.CType)

	// If we have slot types, use them; otherwise fall back to recording-time types
	if bt == SSATypeUnknown {
		bt = ssaTypeFromRuntime(ir.BType)
	}
	if ct == SSATypeUnknown {
		ct = ssaTypeFromRuntime(ir.CType)
	}

	// Both int → int; either float → float
	if bt == SSATypeFloat || ct == SSATypeFloat {
		return SSATypeFloat
	}
	if bt == SSATypeInt && ct == SSATypeInt {
		return SSATypeInt
	}
	// Fallback to recording-time types
	if ir.BType == runtime.TypeFloat || ir.CType == runtime.TypeFloat {
		return SSATypeFloat
	}
	return SSATypeInt
}

// emitIntToFloat converts an int SSA ref to float if needed.
func (b *ssaBuilder) emitIntToFloat(ref SSARef, refType SSAType) SSARef {
	if refType == SSATypeInt {
		// SCVTF conversion: int → float. Slot=-1 marks this as a pure temporary
		// that must NOT be spilled to any VM slot (avoids overwriting slot 0).
		return b.emit(SSAInst{
			Op:   SSA_BOX_INT,
			Type: SSATypeFloat,
			Arg1: ref,
			Slot: -1,
		})
	}
	return ref
}

// convertArith handles OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD.
func (b *ssaBuilder) convertArith(ir *TraceIR, intOp, floatOp SSAOp) {
	bRef := b.getRKRef(ir.B, ir.BType, ir)
	cRef := b.getRKRef(ir.C, ir.CType, ir)
	resType := b.inferArithType(ir.B, ir.C, ir)

	var op SSAOp
	if resType == SSATypeFloat {
		op = floatOp
		// Convert int operands to float if needed
		bt := b.getRKType(ir.B, ir.BType)
		ct := b.getRKType(ir.C, ir.CType)
		if bt == SSATypeUnknown {
			bt = ssaTypeFromRuntime(ir.BType)
		}
		if ct == SSATypeUnknown {
			ct = ssaTypeFromRuntime(ir.CType)
		}
		bRef = b.emitIntToFloat(bRef, bt)
		cRef = b.emitIntToFloat(cRef, ct)
	} else {
		op = intOp
	}

	ref := b.emit(SSAInst{
		Op:   op,
		Type: resType,
		Arg1: bRef,
		Arg2: cRef,
		Slot: int16(ir.A),
		PC:   ir.PC,
	})
	b.slotValues[ir.A] = ref
	b.slotType[ir.A] = resType
}

// convertIR converts one trace IR instruction to SSA.
func (b *ssaBuilder) convertIR(idx int, ir *TraceIR) {
	switch ir.Op {
	case vm.OP_ADD:
		b.convertArith(ir, SSA_ADD_INT, SSA_ADD_FLOAT)

	case vm.OP_SUB:
		b.convertArith(ir, SSA_SUB_INT, SSA_SUB_FLOAT)

	case vm.OP_MUL:
		b.convertArith(ir, SSA_MUL_INT, SSA_MUL_FLOAT)

	case vm.OP_DIV:
		// In GScript/Lua, / always returns float even for integer operands.
		// Force float division: convert both operands to float, use SSA_DIV_FLOAT.
		bRef := b.getRKRef(ir.B, ir.BType, ir)
		cRef := b.getRKRef(ir.C, ir.CType, ir)
		bt := b.getRKType(ir.B, ir.BType)
		ct := b.getRKType(ir.C, ir.CType)
		if bt == SSATypeUnknown {
			bt = ssaTypeFromRuntime(ir.BType)
		}
		if ct == SSATypeUnknown {
			ct = ssaTypeFromRuntime(ir.CType)
		}
		bRef = b.emitIntToFloat(bRef, bt)
		cRef = b.emitIntToFloat(cRef, ct)
		ref := b.emit(SSAInst{
			Op:   SSA_DIV_FLOAT,
			Type: SSATypeFloat,
			Arg1: bRef,
			Arg2: cRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = SSATypeFloat

	case vm.OP_MOD:
		// MOD with float operands is not natively supported (no SSA_MOD_FLOAT).
		// If either operand is float, emit as a call-exit so the interpreter handles it.
		resType := b.inferArithType(ir.B, ir.C, ir)
		if resType == SSATypeFloat {
			b.takeSnapshot(ir.PC)
			ref := b.emit(SSAInst{
				Op:     SSA_CALL,
				Type:   SSATypeUnknown,
				AuxInt: int64(ir.PC),
				Slot:   int16(ir.A),
				PC:     ir.PC,
			})
			b.slotValues[ir.A] = ref
			b.slotType[ir.A] = SSATypeUnknown
		} else {
			b.convertArith(ir, SSA_MOD_INT, SSA_MOD_INT)
		}

	case vm.OP_UNM:
		bRef := b.getSlotRef(ir.B)
		bt := b.getSlotType(ir.B)
		if bt == SSATypeUnknown {
			bt = ssaTypeFromRuntime(ir.BType)
		}
		var op SSAOp
		if bt == SSATypeFloat {
			op = SSA_NEG_FLOAT
		} else {
			op = SSA_NEG_INT
		}
		ref := b.emit(SSAInst{
			Op:   op,
			Type: bt,
			Arg1: bRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = bt

	case vm.OP_MOVE:
		bRef := b.getSlotRef(ir.B)
		bt := b.getSlotType(ir.B)
		if bt == SSATypeUnknown {
			bt = ssaTypeFromRuntime(ir.BType)
		}
		ref := b.emit(SSAInst{
			Op:   SSA_MOVE,
			Type: bt,
			Arg1: bRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = bt

	case vm.OP_LOADK:
		idx := ir.BX
		var val runtime.Value
		if idx < len(b.trace.Constants) {
			val = b.trace.Constants[idx]
		} else if ir.Proto != nil && idx < len(ir.Proto.Constants) {
			val = ir.Proto.Constants[idx]
		} else if b.trace.LoopProto != nil && idx < len(b.trace.LoopProto.Constants) {
			val = b.trace.LoopProto.Constants[idx]
		}
		var ref SSARef
		var ssaType SSAType
		if val.Type() == runtime.TypeFloat {
			ref = b.emit(SSAInst{
				Op:     SSA_CONST_FLOAT,
				Type:   SSATypeFloat,
				AuxInt: int64(math.Float64bits(val.Float())),
				Slot:   int16(ir.A),
				PC:     ir.PC,
			})
			ssaType = SSATypeFloat
		} else if val.Type() == runtime.TypeInt {
			ref = b.emit(SSAInst{
				Op:     SSA_CONST_INT,
				Type:   SSATypeInt,
				AuxInt: val.Int(),
				Slot:   int16(ir.A),
				PC:     ir.PC,
			})
			ssaType = SSATypeInt
		} else {
			ref = b.emit(SSAInst{
				Op:   SSA_CONST_NIL,
				Type: SSATypeNil,
				Slot: int16(ir.A),
				PC:   ir.PC,
			})
			ssaType = SSATypeNil
		}
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = ssaType

	case vm.OP_LOADINT:
		ref := b.emit(SSAInst{
			Op:     SSA_CONST_INT,
			Type:   SSATypeInt,
			AuxInt: int64(ir.SBX),
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_LOADBOOL:
		bv := int64(0)
		if ir.B != 0 {
			bv = 1
		}
		ref := b.emit(SSAInst{
			Op:     SSA_CONST_BOOL,
			Type:   SSATypeBool,
			AuxInt: bv,
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = SSATypeBool

	case vm.OP_LOADNIL:
		for j := 0; j <= ir.B; j++ {
			slot := ir.A + j
			ref := b.emit(SSAInst{
				Op:   SSA_CONST_NIL,
				Type: SSATypeNil,
				Slot: int16(slot),
				PC:   ir.PC,
			})
			b.slotValues[slot] = ref
			b.slotType[slot] = SSATypeNil
		}

	case vm.OP_GETFIELD:
		// Call-exit: take snapshot before
		b.takeSnapshot(ir.PC)
		tableRef := b.getSlotRef(ir.B)
		ref := b.emit(SSAInst{
			Op:     SSA_LOAD_FIELD,
			Type:   ssaTypeFromRuntime(ir.AType),
			Arg1:   tableRef,
			AuxInt: int64(ir.FieldIndex),
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = ssaTypeFromRuntime(ir.AType)

	case vm.OP_SETFIELD:
		// Call-exit: take snapshot before
		b.takeSnapshot(ir.PC)
		tableRef := b.getSlotRef(ir.A)
		valRef := b.getRKRef(ir.C, ir.CType, ir)
		b.emit(SSAInst{
			Op:     SSA_STORE_FIELD,
			Type:   SSATypeUnknown,
			Arg1:   tableRef,
			Arg2:   valRef,
			AuxInt: int64(ir.FieldIndex),
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})

	case vm.OP_GETTABLE:
		// Call-exit: take snapshot before
		b.takeSnapshot(ir.PC)
		tableRef := b.getSlotRef(ir.B)
		keyRef := b.getRKRef(ir.C, ir.CType, ir)
		ref := b.emit(SSAInst{
			Op:     SSA_LOAD_ARRAY,
			Type:   ssaTypeFromRuntime(ir.AType),
			Arg1:   tableRef,
			Arg2:   keyRef,
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = ssaTypeFromRuntime(ir.AType)

	case vm.OP_SETTABLE:
		// Native table array store: table[key] = value
		b.takeSnapshot(ir.PC)
		keyRef := b.getRKRef(ir.B, ir.BType, ir)
		valRef := b.getRKRef(ir.C, ir.CType, ir)
		b.emit(SSAInst{
			Op:   SSA_STORE_ARRAY,
			Type: SSATypeUnknown,
			Arg1: keyRef,
			Arg2: valRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})

	case vm.OP_GETGLOBAL:
		// Call-exit: take snapshot before
		b.takeSnapshot(ir.PC)
		ref := b.emit(SSAInst{
			Op:     SSA_LOAD_GLOBAL,
			Type:   ssaTypeFromRuntime(ir.AType),
			AuxInt: int64(ir.BX),
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = ssaTypeFromRuntime(ir.AType)

	case vm.OP_EQ:
		bRef := b.getRKRef(ir.B, ir.BType, ir)
		cRef := b.getRKRef(ir.C, ir.CType, ir)
		// Determine whether the trace followed "skip" or "not skip" path.
		// EQ A B C: skip if (B==C) != bool(A).
		// If next IR is JMP → trace did NOT skip → AuxInt = A
		// If next IR is NOT JMP → trace DID skip → AuxInt = A ^ 1
		auxA := int64(ir.A)
		if !(idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP) {
			auxA ^= 1
		}
		b.emit(SSAInst{
			Op:     SSA_EQ_INT,
			Type:   SSATypeBool,
			Arg1:   bRef,
			Arg2:   cRef,
			AuxInt: auxA,
			PC:     ir.PC,
		})

	case vm.OP_LT:
		bRef := b.getRKRef(ir.B, ir.BType, ir)
		cRef := b.getRKRef(ir.C, ir.CType, ir)
		bt := b.getRKType(ir.B, ir.BType)
		ct := b.getRKType(ir.C, ir.CType)
		var op SSAOp
		if bt == SSATypeFloat || ct == SSATypeFloat {
			op = SSA_LT_FLOAT
		} else {
			op = SSA_LT_INT
		}
		// Same skip/not-skip detection as EQ.
		auxA := int64(ir.A)
		if !(idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP) {
			auxA ^= 1
		}
		b.emit(SSAInst{
			Op:     op,
			Type:   SSATypeBool,
			Arg1:   bRef,
			Arg2:   cRef,
			AuxInt: auxA,
			PC:     ir.PC,
		})

	case vm.OP_LE:
		bRef := b.getRKRef(ir.B, ir.BType, ir)
		cRef := b.getRKRef(ir.C, ir.CType, ir)
		bt := b.getRKType(ir.B, ir.BType)
		ct := b.getRKType(ir.C, ir.CType)
		var op SSAOp
		if bt == SSATypeFloat || ct == SSATypeFloat {
			op = SSA_LE_FLOAT
		} else {
			op = SSA_LE_INT
		}
		// Same skip/not-skip detection as EQ.
		auxA := int64(ir.A)
		if !(idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP) {
			auxA ^= 1
		}
		b.emit(SSAInst{
			Op:     op,
			Type:   SSATypeBool,
			Arg1:   bRef,
			Arg2:   cRef,
			AuxInt: auxA,
			PC:     ir.PC,
		})

	case vm.OP_TEST:
		aRef := b.getSlotRef(ir.A)
		// OP_TEST A C: skip next instruction if bool(R(A)) != bool(C).
		// Determine whether the trace followed the "skip" or "not skip" path.
		// If the next trace IR is a JMP, the trace did NOT skip (fell through to JMP).
		// If the next trace IR is NOT a JMP, the trace DID skip (skipped over JMP).
		traceSkipped := true
		if idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP {
			traceSkipped = false
		}
		var guardC int64
		if traceSkipped {
			// Guard passes when skip condition holds: truthy(R(A)) != bool(C)
			//   C=0 → truthy → AuxInt=1; C=1 → falsy → AuxInt=0
			guardC = int64(ir.C) ^ 1
		} else {
			// Guard passes when NOT-skip condition holds: truthy(R(A)) == bool(C)
			//   C=0 → falsy → AuxInt=0; C=1 → truthy → AuxInt=1
			guardC = int64(ir.C)
		}
		b.emit(SSAInst{
			Op:     SSA_GUARD_TRUTHY,
			Type:   SSATypeBool,
			Arg1:   aRef,
			AuxInt: guardC,
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})

	case vm.OP_JMP:
		// JMP in a trace is typically part of a conditional skip pattern.
		// Emit as NOP — the guard before it handles the branch.
		b.emit(SSAInst{
			Op: SSA_NOP,
			PC: ir.PC,
		})

	case vm.OP_FORLOOP:
		// FORLOOP: index += step; if index <= limit then continue loop
		// Slots: A=index, A+1=limit, A+2=step, A+3=exposed var
		indexRef := b.getSlotRef(ir.A)
		limitRef := b.getSlotRef(ir.A + 1)
		stepRef := b.getSlotRef(ir.A + 2)
		indexType := b.getSlotType(ir.A)
		if indexType == SSATypeUnknown {
			indexType = ssaTypeFromRuntime(ir.AType)
		}

		var addOp, cmpOp SSAOp
		if indexType == SSATypeFloat {
			addOp = SSA_ADD_FLOAT
			cmpOp = SSA_LE_FLOAT
		} else {
			addOp = SSA_ADD_INT
			cmpOp = SSA_LE_INT
		}

		// index = index + step
		newIndex := b.emit(SSAInst{
			Op:   addOp,
			Type: indexType,
			Arg1: indexRef,
			Arg2: stepRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})
		b.slotValues[ir.A] = newIndex
		b.slotType[ir.A] = indexType

		// if index <= limit (tagged as FORLOOP exit with AuxInt=-1)
		b.emit(SSAInst{
			Op:     cmpOp,
			Type:   SSATypeBool,
			Arg1:   newIndex,
			Arg2:   limitRef,
			AuxInt: -1, // sentinel: this is the FORLOOP exit comparison
			PC:     ir.PC,
		})

		// A+3 = index (exposed loop variable)
		moveRef := b.emit(SSAInst{
			Op:   SSA_MOVE,
			Type: indexType,
			Arg1: newIndex,
			Slot: int16(ir.A + 3),
			PC:   ir.PC,
		})
		b.slotValues[ir.A+3] = moveRef
		b.slotType[ir.A+3] = indexType

	case vm.OP_FORPREP:
		// FORPREP: index = index - step (pre-loop adjustment)
		// For inner loops, this may emit an inner loop marker
		if ir.FieldIndex > 0 {
			// Inner loop: emit INNER_LOOP marker
			b.emit(SSAInst{
				Op:     SSA_INNER_LOOP,
				Type:   SSATypeUnknown,
				AuxInt: int64(ir.FieldIndex),
				Slot:   int16(ir.A),
				PC:     ir.PC,
			})
		} else {
			// Standard forprep: index -= step
			indexRef := b.getSlotRef(ir.A)
			stepRef := b.getSlotRef(ir.A + 2)
			indexType := b.getSlotType(ir.A)
			if indexType == SSATypeUnknown {
				indexType = ssaTypeFromRuntime(ir.AType)
			}

			var subOp SSAOp
			if indexType == SSATypeFloat {
				subOp = SSA_SUB_FLOAT
			} else {
				subOp = SSA_SUB_INT
			}

			ref := b.emit(SSAInst{
				Op:   subOp,
				Type: indexType,
				Arg1: indexRef,
				Arg2: stepRef,
				Slot: int16(ir.A),
				PC:   ir.PC,
			})
			b.slotValues[ir.A] = ref
			b.slotType[ir.A] = indexType
		}

	case vm.OP_CALL:
		if ir.Intrinsic != IntrinsicNone {
			// Inlined intrinsic
			b.convertIntrinsic(ir)
		} else {
			// Call-exit: take snapshot before
			b.takeSnapshot(ir.PC)
			ref := b.emit(SSAInst{
				Op:     SSA_CALL,
				Type:   SSATypeUnknown,
				AuxInt: int64(ir.PC),
				Slot:   int16(ir.A),
				PC:     ir.PC,
			})
			// Write return values
			if ir.C > 0 {
				for j := 0; j < ir.C-1; j++ {
					b.slotValues[ir.A+j] = ref
					b.slotType[ir.A+j] = SSATypeUnknown
				}
			} else {
				b.slotValues[ir.A] = ref
				b.slotType[ir.A] = SSATypeUnknown
			}
		}

	case vm.OP_LEN:
		bRef := b.getSlotRef(ir.B)
		ref := b.emit(SSAInst{
			Op:   SSA_TABLE_LEN,
			Type: SSATypeInt,
			Arg1: bRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = SSATypeInt

	case vm.OP_NOT:
		bRef := b.getSlotRef(ir.B)
		ref := b.emit(SSAInst{
			Op:   SSA_GUARD_TRUTHY,
			Type: SSATypeBool,
			Arg1: bRef,
			Slot: int16(ir.A),
			PC:   ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = SSATypeBool

	case vm.OP_CONCAT, vm.OP_RETURN, vm.OP_SELF,
		vm.OP_SETGLOBAL, vm.OP_CLOSE, vm.OP_NEWTABLE:
		// These are call-exit instructions that the VM handles
		b.takeSnapshot(ir.PC)
		ref := b.emit(SSAInst{
			Op:     SSA_CALL,
			Type:   SSATypeUnknown,
			AuxInt: int64(ir.PC),
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		// Update writes for ops that produce results
		switch ir.Op {
		case vm.OP_CONCAT, vm.OP_SELF, vm.OP_NEWTABLE:
			b.slotValues[ir.A] = ref
			b.slotType[ir.A] = SSATypeUnknown
		}

	case vm.OP_TESTSET:
		bRef := b.getSlotRef(ir.B)
		// Determine whether the trace followed the "skip" or "not skip" path.
		// Same logic as OP_TEST: if next IR is JMP, trace did NOT skip.
		traceSkipped := true
		if idx+1 < len(b.trace.IR) && b.trace.IR[idx+1].Op == vm.OP_JMP {
			traceSkipped = false
		}
		var guardC int64
		if traceSkipped {
			guardC = int64(ir.C) ^ 1
		} else {
			guardC = int64(ir.C)
		}
		ref := b.emit(SSAInst{
			Op:     SSA_GUARD_TRUTHY,
			Type:   SSATypeBool,
			Arg1:   bRef,
			AuxInt: guardC,
			Slot:   int16(ir.A),
			PC:     ir.PC,
		})
		b.slotValues[ir.A] = ref
		b.slotType[ir.A] = b.getSlotType(ir.B)

	default:
		// Unknown opcode — emit NOP
		b.emit(SSAInst{
			Op: SSA_NOP,
			PC: ir.PC,
		})
	}
}

// convertIntrinsic handles recognized intrinsic calls.
func (b *ssaBuilder) convertIntrinsic(ir *TraceIR) {
	ref := b.emit(SSAInst{
		Op:     SSA_INTRINSIC,
		Type:   SSATypeFloat, // most intrinsics return float
		AuxInt: int64(ir.Intrinsic),
		Slot:   int16(ir.A),
		PC:     ir.PC,
	})
	b.slotValues[ir.A] = ref
	b.slotType[ir.A] = SSATypeFloat
}

// computeLiveIn performs liveness analysis on the trace to determine which
// slots need pre-loop guards. Returns:
//   - liveIn: map of slot → true if the slot is live-in (read before written)
//   - slotType: map of slot → runtime.ValueType (the type expected at entry)
//   - slotClass: the full classification map from classifySlots
func computeLiveIn(trace *Trace) (map[int]bool, map[int]runtime.ValueType, map[int]*SlotInfo) {
	slotClass := classifySlots(trace)
	liveIn := make(map[int]bool)
	slotType := make(map[int]runtime.ValueType)

	for slot, info := range slotClass {
		if info.Class == SlotLiveIn {
			liveIn[slot] = true
			slotType[slot] = info.GuardType
		}
	}

	return liveIn, slotType, slotClass
}

// BuildSSA converts a recorded Trace into SSA form.
func BuildSSA(trace *Trace) *SSAFunc {
	slotClass := classifySlots(trace)
	b := &ssaBuilder{
		trace:       trace,
		slotValues:  map[int]SSARef{},
		preLoopVals: map[int]SSARef{},
		slotType:    map[int]SSAType{},
	}

	// Phase 1: Pre-loop — LOAD_SLOT + GUARD for live-in slots.
	// Sort slots for deterministic output.
	var liveInSlots []int
	for slot, info := range slotClass {
		if info.Class == SlotLiveIn {
			liveInSlots = append(liveInSlots, slot)
		}
	}
	sort.Ints(liveInSlots)
	for _, slot := range liveInSlots {
		info := slotClass[slot]
		b.emitGuard(slot, info.GuardType, info.FirstPC)
	}

	// Phase 2: LOOP marker
	b.emit(SSAInst{Op: SSA_LOOP})
	b.loopEmitted = true
	b.takeSnapshot(0) // snapshot #0: loop entry

	// Phase 3: Convert loop body
	for i := range trace.IR {
		if trace.IR[i].Dead {
			continue // skip killed IR entries (e.g., GETGLOBAL for inlined functions)
		}
		b.convertIR(i, &trace.IR[i])
	}

	return &SSAFunc{
		Insts:          b.insts,
		Snapshots:      b.snapshots,
		Trace:          trace,
		LoopIdx:        b.findLoopIdx(),
		MaxDepth0Slot:  trace.MaxDepth0Slot,
	}
}

// OptimizeSSA runs optimization passes on the SSA function.
// Currently applies while-loop exit detection; other optimization passes
// (CSE, ConstHoist, etc.) are applied separately by the caller.
func OptimizeSSA(f *SSAFunc) *SSAFunc {
	// Detect while-loop exit patterns: if there's no FORLOOP exit (AuxInt=-1)
	// but there IS a comparison right after SSA_LOOP, mark it as the while-loop
	// exit (AuxInt=-2). This allows while-loop traces to compile.
	hasForloopExit := false
	for _, inst := range f.Insts {
		if (inst.Op == SSA_LE_INT || inst.Op == SSA_LE_FLOAT) && inst.AuxInt == -1 {
			hasForloopExit = true
			break
		}
	}
	if !hasForloopExit && f.LoopIdx > 0 {
		// Scan for first comparison after SSA_LOOP
		for i := f.LoopIdx + 1; i < len(f.Insts); i++ {
			inst := &f.Insts[i]
			// Skip NOPs, moves, constants, snapshots
			if inst.Op == SSA_NOP || inst.Op == SSA_MOVE || inst.Op == SSA_SNAPSHOT ||
				inst.Op == SSA_CONST_INT || inst.Op == SSA_CONST_FLOAT ||
				inst.Op == SSA_CONST_NIL || inst.Op == SSA_CONST_BOOL {
				continue
			}
			// If the first meaningful instruction after LOOP is a LE or LT comparison,
			// this is the while-loop exit condition.
			if inst.Op == SSA_LE_INT || inst.Op == SSA_LT_INT ||
				inst.Op == SSA_LE_FLOAT || inst.Op == SSA_LT_FLOAT {
				inst.AuxInt = -2 // while-loop exit sentinel
				break
			}
			// If something else comes first, it's not a simple while-loop pattern
			break
		}
	}
	return f
}

// isFloatOp returns true if the SSA opcode operates on float values.
func isFloatOp(op SSAOp) bool {
	switch op {
	case SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT,
		SSA_NEG_FLOAT, SSA_FMADD, SSA_FMSUB,
		SSA_UNBOX_FLOAT, SSA_BOX_FLOAT,
		SSA_CONST_FLOAT,
		SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return true
	}
	return false
}
