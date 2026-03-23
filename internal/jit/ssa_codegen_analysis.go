//go:build darwin && arm64

package jit

import (
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ssaIsCompilable returns true if all SSA ops in the function are supported by the codegen.
func ssaIsCompilable(f *SSAFunc) bool {
	for _, inst := range f.Insts {
		switch inst.Op {
		case SSA_GUARD_TYPE, SSA_GUARD_NNIL, SSA_GUARD_NOMETA, SSA_GUARD_TRUTHY,
			SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT,
			SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT,
			SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT,
			SSA_FMADD, SSA_FMSUB,
			SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT,
			SSA_LOAD_SLOT, SSA_STORE_SLOT,
			SSA_UNBOX_INT, SSA_BOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_FLOAT,
			SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_TABLE_LEN, SSA_LOAD_GLOBAL,
			SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL,
			SSA_LOOP, SSA_PHI, SSA_SNAPSHOT,
			SSA_MOVE, SSA_NOP,
			SSA_INTRINSIC,
			SSA_CALL_INNER_TRACE,
			SSA_INNER_LOOP,
			SSA_CALL:
			continue
		case SSA_SIDE_EXIT:
			continue
		default:
			return false
		}
	}
	return true
}

// findWBRFloatSlots identifies float slots that are written before their first
// read in the trace body. These slots may hold non-float types at trace entry
// (e.g., LOADBOOL on mandelbrot escape path), but the value is overwritten
// before first use. For these slots, the pre-loop GUARD_TYPE can be relaxed to
// accept any non-pointer type instead of requiring exact TypeFloat.
func findWBRFloatSlots(f *SSAFunc) map[int]bool {
	result := make(map[int]bool)
	if f == nil || f.Trace == nil {
		return result
	}

	// Check if the trace has non-numeric operations
	hasNonNumeric := false
	for _, ir := range f.Trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_GETGLOBAL, vm.OP_CALL:
			hasNonNumeric = true
		}
	}

	// Build set of float slots from trace type info
	floatSlots := make(map[int]bool)
	for _, ir := range f.Trace.IR {
		if ir.BType == runtime.TypeFloat && ir.B < 256 {
			floatSlots[ir.B] = true
		}
		if ir.CType == runtime.TypeFloat && ir.C < 256 {
			floatSlots[ir.C] = true
		}
	}

	for slot := range floatSlots {
		if hasNonNumeric {
			// For traces with field/table/call ops, use extended WBR check
			// that recognizes GETFIELD/GETTABLE/CALL as valid writes.
			// This allows relaxing guards for intermediate float results
			// like dx/dy/dz in nbody's "dx = bi.x - bj.x" pattern.
			b := &ssaBuilder{trace: f.Trace, slotType: make(map[int]SSAType)}
			if b.isWrittenBeforeFirstReadExt(slot) {
				result[slot] = true
			}
		} else {
			// Pure numeric trace: use original isSlotWBR
			if isSlotWBR(f.Trace, slot) {
				result[slot] = true
			}
		}
	}
	return result
}

// isSlotWBR checks if a slot is written before its first read in the trace.
// This mirrors ssaBuilder.isWrittenBeforeFirstReadImpl.
func isSlotWBR(trace *Trace, slot int) bool {
	// First pass: bail out if used by non-numeric ops
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_GETTABLE:
			if ir.A == slot || ir.B == slot || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_SETTABLE:
			if ir.A == slot || (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_GETFIELD:
			if ir.A == slot || ir.B == slot {
				return false
			}
		case vm.OP_SETFIELD:
			if ir.A == slot || (ir.C < 256 && ir.C == slot) {
				return false
			}
		case vm.OP_GETGLOBAL:
			if ir.A == slot {
				return false
			}
		case vm.OP_CALL:
			if slot >= ir.A && slot < ir.A+ir.B {
				return false
			}
			if ir.A == slot {
				return false
			}
		}
	}

	// Second pass: check write-before-read for numeric ops
	for _, ir := range trace.IR {
		isRead := false
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV,
			vm.OP_LT, vm.OP_LE, vm.OP_EQ:
			if (ir.B < 256 && ir.B == slot) || (ir.C < 256 && ir.C == slot) {
				isRead = true
			}
		case vm.OP_UNM:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_MOVE:
			if ir.B == slot {
				isRead = true
			}
		case vm.OP_FORLOOP:
			if slot == ir.A || slot == ir.A+1 || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_FORPREP:
			if slot == ir.A || slot == ir.A+2 {
				isRead = true
			}
		case vm.OP_TEST:
			if ir.A == slot {
				isRead = true
			}
		}
		if isRead {
			return false
		}

		isWrite := false
		switch ir.Op {
		case vm.OP_LOADK, vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL:
			isWrite = (ir.A == slot)
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV:
			isWrite = (ir.A == slot)
		case vm.OP_UNM:
			isWrite = (ir.A == slot)
		case vm.OP_MOVE:
			isWrite = (ir.A == slot)
		}
		if isWrite {
			return true
		}
	}
	return false
}

// ssaIsNumericOnly kept for backward compat
func ssaIsNumericOnly(f *SSAFunc) bool { return ssaIsCompilable(f) }

// Keep old name as alias
func ssaIsIntegerOnly(f *SSAFunc) bool { return ssaIsNumericOnly(f) }
