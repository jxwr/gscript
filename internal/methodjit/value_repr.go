//go:build darwin && arm64

package methodjit

import "github.com/gscript/gscript/internal/jit"

// valueRepr is the emitter's register-content lattice for SSA values whose
// allocated register is active in the current block.
type valueRepr uint8

const (
	valueReprBoxed valueRepr = iota
	valueReprRawInt
	valueReprRawFloat
	valueReprRawTablePtr
	valueReprRawDataPtr
)

func (r valueRepr) String() string {
	switch r {
	case valueReprBoxed:
		return "boxed"
	case valueReprRawInt:
		return "raw-int"
	case valueReprRawFloat:
		return "raw-float"
	case valueReprRawTablePtr:
		return "raw-table-ptr"
	case valueReprRawDataPtr:
		return "raw-data-ptr"
	default:
		return "unknown"
	}
}

func (ec *emitContext) setValueRepr(valueID int, repr valueRepr) {
	if ec.valueReprs == nil {
		ec.valueReprs = make(map[int]valueRepr)
	}
	if repr == valueReprBoxed {
		delete(ec.valueReprs, valueID)
	} else {
		ec.valueReprs[valueID] = repr
	}

	// Compatibility mirrors for call sites that still query the old maps.
	switch repr {
	case valueReprRawInt:
		ec.rawIntRegs[valueID] = true
		delete(ec.rawTablePtrRegs, valueID)
	case valueReprRawTablePtr:
		ec.rawTablePtrRegs[valueID] = true
		delete(ec.rawIntRegs, valueID)
	default:
		delete(ec.rawIntRegs, valueID)
		delete(ec.rawTablePtrRegs, valueID)
	}
}

func (ec *emitContext) clearValueRepr(valueID int) {
	if ec.valueReprs != nil {
		delete(ec.valueReprs, valueID)
	}
	delete(ec.rawIntRegs, valueID)
	delete(ec.rawTablePtrRegs, valueID)
}

func (ec *emitContext) valueReprOf(valueID int) valueRepr {
	if ec == nil {
		return valueReprBoxed
	}
	if ec.rawTablePtrRegs[valueID] {
		return valueReprRawTablePtr
	}
	if ec.rawIntRegs[valueID] {
		return valueReprRawInt
	}
	if ec.activeFPRegs[valueID] {
		return valueReprRawFloat
	}
	if repr, ok := ec.valueReprs[valueID]; ok {
		if repr == valueReprRawDataPtr {
			return repr
		}
	}
	return valueReprBoxed
}

func (ec *emitContext) resetValueReprs() {
	ec.valueReprs = make(map[int]valueRepr)
	ec.rawIntRegs = make(map[int]bool)
	ec.rawTablePtrRegs = make(map[int]bool)
}

func cloneValueReprMap(src map[int]valueRepr) map[int]valueRepr {
	dst := make(map[int]valueRepr, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (ec *emitContext) emitStoreGPRValueAsBoxed(valueID int, reg jit.Reg, slot int) {
	switch ec.valueReprOf(valueID) {
	case valueReprRawTablePtr:
		emitBoxTablePtr(ec.asm, jit.X0, reg, jit.X17)
		ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
		ec.emitExitResumeCheckShadowStoreGPR(slot, jit.X0)
	case valueReprRawInt:
		jit.EmitBoxIntFast(ec.asm, jit.X0, reg, mRegTagInt)
		ec.asm.STR(jit.X0, mRegRegs, slotOffset(slot))
		ec.emitExitResumeCheckShadowStoreGPR(slot, jit.X0)
	default:
		ec.asm.STR(reg, mRegRegs, slotOffset(slot))
		ec.emitExitResumeCheckShadowStoreGPR(slot, reg)
	}
}

func (ec *emitContext) emitReloadGPRValueFromBoxed(valueID int, reg jit.Reg, slot int) {
	repr := ec.valueReprOf(valueID)
	ec.asm.LDR(reg, mRegRegs, slotOffset(slot))
	switch repr {
	case valueReprRawTablePtr:
		jit.EmitExtractPtr(ec.asm, reg, reg)
		ec.setValueRepr(valueID, valueReprRawTablePtr)
	case valueReprRawInt:
		// Reloaded homes are boxed. Raw-int callers that need convergence
		// explicitly re-unbox via emitUnboxRawIntRegs with their saved state.
		ec.clearValueRepr(valueID)
	default:
		ec.setValueRepr(valueID, valueReprBoxed)
	}
}
