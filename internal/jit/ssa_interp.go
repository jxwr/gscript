//go:build darwin && arm64

package jit

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
)

// SSAInterpResult holds the result of SSA interpretation.
type SSAInterpResult struct {
	ExitCode int // 0=loop-done, 1=side-exit, 2=guard-fail
	ExitPC   int // bytecode PC at exit point
	Iters    int // number of loop iterations executed
}

// InterpretSSA executes an SSAFunc directly in Go without ARM64 compilation.
// It maintains a map[SSARef]uint64 for raw values (unboxed ints, raw float bits).
// regs is the VM register array. Modified in-place on exit (store-back).
func InterpretSSA(f *SSAFunc, regs []runtime.Value, base int) SSAInterpResult {
	vals := make(map[SSARef]uint64)
	valTypes := make(map[SSARef]SSAType)

	maxIters := 1000000 // safety limit
	iters := 0
	loopStart := -1

	pc := 0
	for pc < len(f.Insts) {
		inst := &f.Insts[pc]
		ref := SSARef(pc)

		switch inst.Op {
		case SSA_LOAD_SLOT:
			slot := int(inst.Slot)
			if slot >= 0 && base+slot < len(regs) {
				vals[ref] = uint64(regs[base+slot])
				valTypes[ref] = inst.Type
			}

		case SSA_GUARD_TYPE:
			loadRef := inst.Arg1
			val := runtime.Value(vals[loadRef])
			expectedType := int(inst.AuxInt)
			actualType := runtimeTypeOf(val)
			if actualType != expectedType {
				return SSAInterpResult{ExitCode: 2, ExitPC: inst.PC}
			}

		case SSA_UNBOX_INT:
			v := runtime.Value(vals[inst.Arg1])
			vals[ref] = uint64(v.Int())
			valTypes[ref] = SSATypeInt

		case SSA_UNBOX_FLOAT:
			v := runtime.Value(vals[inst.Arg1])
			vals[ref] = math.Float64bits(v.Float())
			valTypes[ref] = SSATypeFloat

		case SSA_LOOP:
			loopStart = pc + 1

		case SSA_ADD_INT:
			vals[ref] = uint64(int64(vals[inst.Arg1]) + int64(vals[inst.Arg2]))
			valTypes[ref] = SSATypeInt
		case SSA_SUB_INT:
			vals[ref] = uint64(int64(vals[inst.Arg1]) - int64(vals[inst.Arg2]))
			valTypes[ref] = SSATypeInt
		case SSA_MUL_INT:
			vals[ref] = uint64(int64(vals[inst.Arg1]) * int64(vals[inst.Arg2]))
			valTypes[ref] = SSATypeInt
		case SSA_DIV_INT:
			a, b := int64(vals[inst.Arg1]), int64(vals[inst.Arg2])
			if b != 0 {
				vals[ref] = uint64(a / b)
			} else {
				vals[ref] = 0
			}
			valTypes[ref] = SSATypeInt
		case SSA_MOD_INT:
			a, b := int64(vals[inst.Arg1]), int64(vals[inst.Arg2])
			if b != 0 {
				vals[ref] = uint64(a % b)
			} else {
				vals[ref] = 0
			}
			valTypes[ref] = SSATypeInt
		case SSA_NEG_INT:
			vals[ref] = uint64(-int64(vals[inst.Arg1]))
			valTypes[ref] = SSATypeInt

		case SSA_ADD_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			vals[ref] = math.Float64bits(a + b)
			valTypes[ref] = SSATypeFloat
		case SSA_SUB_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			vals[ref] = math.Float64bits(a - b)
			valTypes[ref] = SSATypeFloat
		case SSA_MUL_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			vals[ref] = math.Float64bits(a * b)
			valTypes[ref] = SSATypeFloat
		case SSA_DIV_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			vals[ref] = math.Float64bits(a / b)
			valTypes[ref] = SSATypeFloat
		case SSA_NEG_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			vals[ref] = math.Float64bits(-a)
			valTypes[ref] = SSATypeFloat

		case SSA_FMADD:
			// FMADD: Arg1*Arg2 + AuxInt(ref)
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			c := math.Float64frombits(vals[SSARef(inst.AuxInt)])
			vals[ref] = math.Float64bits(a*b + c)
			valTypes[ref] = SSATypeFloat
		case SSA_FMSUB:
			// FMSUB: Arg1*Arg2 - AuxInt(ref)
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			c := math.Float64frombits(vals[SSARef(inst.AuxInt)])
			vals[ref] = math.Float64bits(a*b - c)
			valTypes[ref] = SSATypeFloat

		case SSA_CONST_INT:
			vals[ref] = uint64(inst.AuxInt)
			valTypes[ref] = SSATypeInt
		case SSA_CONST_FLOAT:
			vals[ref] = uint64(inst.AuxInt) // AuxInt stores raw float64 bits
			valTypes[ref] = SSATypeFloat
		case SSA_CONST_NIL:
			vals[ref] = uint64(runtime.NilValue())
			valTypes[ref] = SSATypeNil
		case SSA_CONST_BOOL:
			if inst.AuxInt != 0 {
				vals[ref] = uint64(runtime.BoolValue(true))
			} else {
				vals[ref] = uint64(runtime.BoolValue(false))
			}
			valTypes[ref] = SSATypeBool

		case SSA_MOVE:
			vals[ref] = vals[inst.Arg1]
			valTypes[ref] = valTypes[inst.Arg1]

		case SSA_PHI:
			// PHI nodes in a tracing JIT are effectively MOVEs; the value
			// from the correct predecessor is already in Arg1 (pre-loop)
			// or Arg2 (loop body). On first entry Arg1 is live; on loop
			// back-edge Arg2 is live. Since the interpreter re-executes
			// from loopStart each iteration, Arg2 carries the loop value.
			// On first pass (before any loop), use Arg1.
			if iters == 0 {
				vals[ref] = vals[inst.Arg1]
				valTypes[ref] = valTypes[inst.Arg1]
			} else {
				vals[ref] = vals[inst.Arg2]
				valTypes[ref] = valTypes[inst.Arg2]
			}

		case SSA_EQ_INT:
			a, b := int64(vals[inst.Arg1]), int64(vals[inst.Arg2])
			auxA := int(inst.AuxInt)
			guardPass := false
			if auxA == 1 {
				guardPass = (a != b)
			} else {
				guardPass = (a == b)
			}
			if !guardPass {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}

		case SSA_LT_INT:
			a, b := int64(vals[inst.Arg1]), int64(vals[inst.Arg2])
			auxA := int(inst.AuxInt)
			lt := a < b
			guardPass := false
			if auxA == 0 {
				guardPass = !lt
			} else {
				guardPass = lt
			}
			if !guardPass {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}

		case SSA_LE_INT:
			a, b := int64(vals[inst.Arg1]), int64(vals[inst.Arg2])
			if inst.AuxInt == -1 {
				// FORLOOP exit: loop continues if a <= b
				if a > b {
					storeBackInterp(f, vals, valTypes, regs, base)
					return SSAInterpResult{ExitCode: 0, ExitPC: inst.PC, Iters: iters}
				}
			} else {
				guardPass := a <= b
				if inst.AuxInt == 0 {
					guardPass = !guardPass
				}
				if !guardPass {
					storeBackInterp(f, vals, valTypes, regs, base)
					return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
				}
			}

		case SSA_LT_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			lt := a < b
			guardPass := lt
			if inst.AuxInt == 0 {
				guardPass = !guardPass
			}
			if !guardPass {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}

		case SSA_LE_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			if inst.AuxInt == -1 {
				// FORLOOP exit: loop continues if a <= b
				if a > b {
					storeBackInterp(f, vals, valTypes, regs, base)
					return SSAInterpResult{ExitCode: 0, ExitPC: inst.PC, Iters: iters}
				}
			} else {
				guardPass := a <= b
				if inst.AuxInt == 0 {
					guardPass = !guardPass
				}
				if !guardPass {
					storeBackInterp(f, vals, valTypes, regs, base)
					return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
				}
			}

		case SSA_GT_FLOAT:
			a := math.Float64frombits(vals[inst.Arg1])
			b := math.Float64frombits(vals[inst.Arg2])
			gt := a > b
			guardPass := gt
			if inst.AuxInt == 0 {
				guardPass = !guardPass
			}
			if !guardPass {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}

		case SSA_GUARD_TRUTHY:
			// GUARD_TRUTHY reads from memory (slot), not from SSA ref.
			slot := int(inst.Slot)
			if slot < 0 || base+slot >= len(regs) {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}
			val := regs[base+slot]
			truthy := !val.IsNil() && !(val.IsBool() && !val.Bool())
			guardPass := truthy
			if inst.AuxInt == 0 {
				guardPass = !guardPass
			}
			if !guardPass {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}

		case SSA_GUARD_NNIL:
			// Guard that slot is non-nil
			slot := int(inst.Slot)
			if slot < 0 || base+slot >= len(regs) || regs[base+slot].IsNil() {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}

		case SSA_GUARD_NOMETA:
			// Guard that slot's table has no metatable
			slot := int(inst.Slot)
			if slot >= 0 && base+slot < len(regs) {
				val := regs[base+slot]
				if val.IsTable() {
					tbl := val.Table()
					if tbl != nil && tbl.HasMetatable() {
						storeBackInterp(f, vals, valTypes, regs, base)
						return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
					}
				}
			}

		case SSA_CALL:
			// Call-exit: return side-exit so interpreter handles the CALL
			storeBackInterp(f, vals, valTypes, regs, base)
			return SSAInterpResult{ExitCode: 1, ExitPC: int(inst.AuxInt)}

		case SSA_SIDE_EXIT:
			storeBackInterp(f, vals, valTypes, regs, base)
			return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}

		case SSA_LOAD_FIELD:
			// Load field from table by svals index
			tableSlot := int(inst.Slot)
			fieldIdx := int(int32(inst.AuxInt))
			if tableSlot < 0 || base+tableSlot >= len(regs) {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}
			tableVal := regs[base+tableSlot]
			if tableVal.IsTable() {
				tbl := tableVal.Table()
				if tbl != nil && fieldIdx >= 0 && fieldIdx < tbl.SkeysLen() {
					// Access svals[fieldIdx] via RawGetString with skeys[fieldIdx]
					v := interpTableGetField(tbl, fieldIdx)
					vals[ref] = interpUnboxForType(v, inst.Type)
				} else {
					storeBackInterp(f, vals, valTypes, regs, base)
					return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
				}
			} else {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}
			valTypes[ref] = inst.Type

		case SSA_STORE_FIELD:
			// Store field to table by svals index
			tableSlot := int(inst.Slot)
			fieldIdx := int(int32(inst.AuxInt))
			if tableSlot < 0 || base+tableSlot >= len(regs) {
				break
			}
			tableVal := regs[base+tableSlot]
			if tableVal.IsTable() {
				tbl := tableVal.Table()
				if tbl != nil && fieldIdx >= 0 && fieldIdx < tbl.SkeysLen() {
					var storeVal runtime.Value
					switch valTypes[inst.Arg2] {
					case SSATypeInt:
						storeVal = runtime.IntValue(int64(vals[inst.Arg2]))
					case SSATypeFloat:
						storeVal = runtime.FloatValue(math.Float64frombits(vals[inst.Arg2]))
					case SSATypeBool:
						storeVal = runtime.Value(vals[inst.Arg2])
					case SSATypeNil:
						storeVal = runtime.NilValue()
					default:
						storeVal = runtime.Value(vals[inst.Arg2])
					}
					interpTableSetField(tbl, fieldIdx, storeVal)
				}
			}

		case SSA_LOAD_ARRAY:
			// Load from table array by integer key
			tableSlot := int(inst.Slot)
			if tableSlot < 0 || base+tableSlot >= len(regs) {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}
			tableVal := regs[base+tableSlot]
			keyRef := inst.Arg2
			key := int64(vals[keyRef])
			if tableVal.IsTable() {
				tbl := tableVal.Table()
				if tbl != nil {
					v := tbl.RawGetInt(key)
					vals[ref] = interpUnboxForType(v, inst.Type)
				} else {
					storeBackInterp(f, vals, valTypes, regs, base)
					return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
				}
			} else {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}
			}
			valTypes[ref] = inst.Type

		case SSA_STORE_ARRAY:
			// Store to table array. Arg1=tableRef, Arg2=valRef, AuxInt=key slot.
			// This is a call-exit in the compiler, but we can execute it directly.
			tableSlot := int(inst.Slot)
			if tableSlot < 0 || base+tableSlot >= len(regs) {
				break
			}
			tableVal := regs[base+tableSlot]
			if tableVal.IsTable() {
				tbl := tableVal.Table()
				if tbl != nil {
					// AuxInt encodes the key (B register from OP_SETTABLE)
					key := int64(inst.AuxInt)
					var storeVal runtime.Value
					switch valTypes[inst.Arg2] {
					case SSATypeInt:
						storeVal = runtime.IntValue(int64(vals[inst.Arg2]))
					case SSATypeFloat:
						storeVal = runtime.FloatValue(math.Float64frombits(vals[inst.Arg2]))
					case SSATypeBool:
						storeVal = runtime.Value(vals[inst.Arg2])
					case SSATypeNil:
						storeVal = runtime.NilValue()
					default:
						storeVal = runtime.Value(vals[inst.Arg2])
					}
					tbl.RawSetInt(key, storeVal)
				}
			}

		case SSA_LOAD_GLOBAL:
			// LOAD_GLOBAL is a call-exit: the interpreter handles it.
			storeBackInterp(f, vals, valTypes, regs, base)
			return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}

		case SSA_TABLE_LEN:
			// TABLE_LEN: also a call-exit in the compiler.
			storeBackInterp(f, vals, valTypes, regs, base)
			return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}

		case SSA_INTRINSIC:
			interpIntrinsic(inst, vals, valTypes, ref, regs, base)

		case SSA_CALL_INNER_TRACE:
			// Inner trace call: side-exit and let the executor handle it.
			storeBackInterp(f, vals, valTypes, regs, base)
			return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}

		case SSA_INNER_LOOP:
			// Inner loop marker: side-exit and let the executor handle it.
			storeBackInterp(f, vals, valTypes, regs, base)
			return SSAInterpResult{ExitCode: 1, ExitPC: inst.PC}

		case SSA_NOP, SSA_SNAPSHOT, SSA_BOX_INT, SSA_BOX_FLOAT, SSA_STORE_SLOT:
			// No-op in interpreter. BOX/STORE_SLOT are codegen-only; the
			// interpreter stores back all values at exit via storeBackInterp.

		default:
			// Unknown op: skip
		}

		// Write value to memory for slot-bearing instructions that produce results.
		// Guards, comparisons, and store ops do not produce VM-visible values.
		if inst.Slot >= 0 && shouldWriteSlot(inst.Op) {
			slot := int(inst.Slot)
			if base+slot < len(regs) {
				writeInterpValue(regs, base+slot, vals[ref], valTypes[ref])
			}
		}

		pc++

		// Loop back-edge
		if pc >= len(f.Insts) && loopStart > 0 {
			iters++
			if iters >= maxIters {
				storeBackInterp(f, vals, valTypes, regs, base)
				return SSAInterpResult{ExitCode: 1, ExitPC: 0, Iters: iters}
			}
			pc = loopStart
		}
	}

	storeBackInterp(f, vals, valTypes, regs, base)
	return SSAInterpResult{ExitCode: 0, Iters: iters}
}

// shouldWriteSlot returns true if the instruction produces a value that should
// be written back to the VM register file during execution. Guards, comparisons,
// and explicit store ops do not produce register-visible values.
func shouldWriteSlot(op SSAOp) bool {
	switch op {
	case SSA_LOAD_SLOT, SSA_GUARD_TYPE, SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA:
		return false
	case SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT, SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT:
		return false
	case SSA_STORE_SLOT, SSA_STORE_FIELD, SSA_STORE_ARRAY:
		return false
	case SSA_NOP, SSA_SNAPSHOT, SSA_LOOP, SSA_SIDE_EXIT:
		return false
	case SSA_CALL, SSA_CALL_INNER_TRACE, SSA_INNER_LOOP:
		return false
	case SSA_BOX_INT, SSA_BOX_FLOAT:
		return false
	}
	return true
}

// runtimeTypeOf returns the runtime.ValueType constant (as int) for a NaN-boxed value.
func runtimeTypeOf(v runtime.Value) int {
	switch v.Type() {
	case runtime.TypeNil:
		return TypeNil
	case runtime.TypeBool:
		return TypeBool
	case runtime.TypeInt:
		return TypeInt
	case runtime.TypeFloat:
		return TypeFloat
	case runtime.TypeString:
		return TypeString
	case runtime.TypeTable:
		return TypeTable
	case runtime.TypeFunction:
		return TypeFunction
	default:
		return -1
	}
}

// storeBackInterp writes all slot-bearing SSA values back to the VM register file.
// It scans all instructions and writes back the latest value for each slot.
func storeBackInterp(f *SSAFunc, vals map[SSARef]uint64, valTypes map[SSARef]SSAType, regs []runtime.Value, base int) {
	// Track the last-written ref per slot.
	lastRef := make(map[int]SSARef)
	for i, inst := range f.Insts {
		if inst.Slot >= 0 && shouldWriteSlot(inst.Op) {
			lastRef[int(inst.Slot)] = SSARef(i)
		}
	}

	for slot, ref := range lastRef {
		if base+slot < len(regs) {
			if _, ok := vals[ref]; ok {
				writeInterpValue(regs, base+slot, vals[ref], valTypes[ref])
			}
		}
	}
}

// writeInterpValue boxes a raw value according to its SSA type and writes it
// to the register file.
func writeInterpValue(regs []runtime.Value, idx int, raw uint64, typ SSAType) {
	switch typ {
	case SSATypeInt:
		regs[idx] = runtime.IntValue(int64(raw))
	case SSATypeFloat:
		regs[idx] = runtime.FloatValue(math.Float64frombits(raw))
	case SSATypeBool:
		// raw is the NaN-boxed bool bits
		regs[idx] = runtime.Value(raw)
	case SSATypeNil:
		regs[idx] = runtime.NilValue()
	default:
		// Unknown type: write raw bits (could be a NaN-boxed value)
		regs[idx] = runtime.Value(raw)
	}
}

// interpUnboxForType converts a runtime.Value to the raw uint64 representation
// appropriate for the given SSA type.
func interpUnboxForType(v runtime.Value, typ SSAType) uint64 {
	switch typ {
	case SSATypeInt:
		return uint64(v.Int())
	case SSATypeFloat:
		return math.Float64bits(v.Float())
	default:
		// For table/string/unknown types, store the NaN-boxed value directly
		return uint64(v)
	}
}

// interpTableGetField retrieves svals[fieldIdx] from a table.
// Uses the exported SvalsGet accessor for direct skeys/svals index access.
func interpTableGetField(tbl *runtime.Table, fieldIdx int) runtime.Value {
	return tbl.SvalsGet(fieldIdx)
}

// interpTableSetField sets svals[fieldIdx] in a table.
// Uses the exported SvalsSet accessor for direct skeys/svals index access.
func interpTableSetField(tbl *runtime.Table, fieldIdx int, val runtime.Value) {
	tbl.SvalsSet(fieldIdx, val)
}

// interpIntrinsic handles SSA_INTRINSIC opcodes.
func interpIntrinsic(inst *SSAInst, vals map[SSARef]uint64, valTypes map[SSARef]SSAType, ref SSARef, regs []runtime.Value, base int) {
	switch int(inst.AuxInt) {
	case IntrinsicSqrt:
		// sqrt(R(A+1)) -> R(A). Argument is in slot A+1.
		argSlot := int(inst.Slot) + 1
		if base+argSlot < len(regs) {
			argVal := regs[base+argSlot]
			var f64 float64
			if argVal.IsFloat() {
				f64 = argVal.Float()
			} else if argVal.Type() == runtime.TypeInt {
				f64 = float64(argVal.Int())
			}
			vals[ref] = math.Float64bits(math.Sqrt(f64))
			valTypes[ref] = SSATypeFloat
		}
	case IntrinsicBxor:
		argSlot1 := int(inst.Slot) + 1
		argSlot2 := int(inst.Slot) + 2
		if base+argSlot2 < len(regs) {
			a := regs[base+argSlot1].Int()
			b := regs[base+argSlot2].Int()
			vals[ref] = uint64(a ^ b)
			valTypes[ref] = SSATypeInt
		}
	case IntrinsicBand:
		argSlot1 := int(inst.Slot) + 1
		argSlot2 := int(inst.Slot) + 2
		if base+argSlot2 < len(regs) {
			a := regs[base+argSlot1].Int()
			b := regs[base+argSlot2].Int()
			vals[ref] = uint64(a & b)
			valTypes[ref] = SSATypeInt
		}
	case IntrinsicBor:
		argSlot1 := int(inst.Slot) + 1
		argSlot2 := int(inst.Slot) + 2
		if base+argSlot2 < len(regs) {
			a := regs[base+argSlot1].Int()
			b := regs[base+argSlot2].Int()
			vals[ref] = uint64(a | b)
			valTypes[ref] = SSATypeInt
		}
	case IntrinsicBnot:
		argSlot1 := int(inst.Slot) + 1
		if base+argSlot1 < len(regs) {
			a := regs[base+argSlot1].Int()
			vals[ref] = uint64(^a)
			valTypes[ref] = SSATypeInt
		}
	case IntrinsicLshift:
		argSlot1 := int(inst.Slot) + 1
		argSlot2 := int(inst.Slot) + 2
		if base+argSlot2 < len(regs) {
			a := regs[base+argSlot1].Int()
			b := regs[base+argSlot2].Int()
			vals[ref] = uint64(a << uint(b))
			valTypes[ref] = SSATypeInt
		}
	case IntrinsicRshift:
		argSlot1 := int(inst.Slot) + 1
		argSlot2 := int(inst.Slot) + 2
		if base+argSlot2 < len(regs) {
			a := regs[base+argSlot1].Int()
			b := regs[base+argSlot2].Int()
			// Logical right shift (unsigned)
			vals[ref] = uint64(uint64(a) >> uint(b))
			valTypes[ref] = SSATypeInt
		}
	default:
		// Unknown intrinsic: treat as no-op
	}
}
