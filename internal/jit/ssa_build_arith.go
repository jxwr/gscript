//go:build darwin && arm64

package jit

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

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
