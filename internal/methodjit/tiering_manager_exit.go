//go:build darwin && arm64

// tiering_manager_exit.go implements exit handlers for the TieringManager's
// Tier 2 execute loop. These handlers are invoked when Tier 2 JIT code
// encounters operations it cannot handle natively (calls, globals, tables,
// generic ops).
//
// The handlers are functionally identical to those in tiering_execute.go and
// tiering_op_exit.go (MethodJITEngine), but operate on the TieringManager
// receiver. Slot indices in ExecContext are relative to the callee's frame
// (base=0 in JIT), so we add `base` for absolute positions.

package methodjit

import (
	"fmt"
	"math"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// executeCallExit handles a call-exit in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeCallExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM set for call-exit")
	}

	callSlot := int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	nRets := int(ctx.CallNRets)

	absSlot := base + callSlot
	if absSlot >= len(regs) {
		return fmt.Errorf("call slot %d (abs %d) out of range (regs len %d)", callSlot, absSlot, len(regs))
	}
	fnVal := regs[absSlot]

	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := absSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	results, err := tm.callVM.CallValue(fnVal, callArgs)
	if err != nil {
		return err
	}

	// Re-read regs — CallValue may have grown the register file.
	currentRegs := tm.callVM.Regs()

	nr := nRets
	if nr <= 0 {
		nr = 1
	}
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

	return nil
}

// executeGlobalExit handles a global-exit in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeGlobalExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if tm.callVM == nil {
		return fmt.Errorf("no callVM set for global-exit")
	}

	globalSlot := int(ctx.GlobalSlot)
	constIdx := int(ctx.GlobalConst)

	if constIdx >= len(proto.Constants) {
		return fmt.Errorf("global constant index %d out of range (len %d)", constIdx, len(proto.Constants))
	}
	globalName := proto.Constants[constIdx].Str()
	val := tm.callVM.GetGlobal(globalName)

	absSlot := base + globalSlot
	if absSlot < len(regs) {
		regs[absSlot] = val
	}

	return nil
}

// executeTableExit handles table operations in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeTableExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	switch ctx.TableOp {
	case TableOpNewTable:
		arrayHint := int(ctx.TableAux)
		hashHint := int(ctx.TableAux2)
		tbl := runtime.NewTableSized(arrayHint, hashHint)
		absSlot := base + int(ctx.TableSlot)
		if absSlot < len(regs) {
			regs[absSlot] = runtime.TableValue(tbl)
		}

	case TableOpGetTable:
		absTable := base + int(ctx.TableSlot)
		absKey := base + int(ctx.TableKeySlot)
		absResult := base + int(ctx.TableAux)
		if absTable < len(regs) && absKey < len(regs) {
			tblVal := regs[absTable]
			keyVal := regs[absKey]
			if tblVal.IsTable() {
				result := tblVal.Table().RawGet(keyVal)
				if absResult < len(regs) {
					regs[absResult] = result
				}
			} else if absResult < len(regs) {
				regs[absResult] = runtime.NilValue()
			}
		}

	case TableOpSetTable:
		absTable := base + int(ctx.TableSlot)
		absKey := base + int(ctx.TableKeySlot)
		absVal := base + int(ctx.TableValSlot)
		if absTable < len(regs) && absKey < len(regs) && absVal < len(regs) {
			tblVal := regs[absTable]
			keyVal := regs[absKey]
			valVal := regs[absVal]
			if tblVal.IsTable() {
				tblVal.Table().RawSet(keyVal, valVal)
			}
		}

	case TableOpGetField:
		absTable := base + int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		absResult := base + int(ctx.TableAux2)
		if absTable < len(regs) && constIdx < len(proto.Constants) {
			tblVal := regs[absTable]
			fieldName := proto.Constants[constIdx].Str()
			if tblVal.IsTable() {
				result := tblVal.Table().RawGetString(fieldName)
				if absResult < len(regs) {
					regs[absResult] = result
				}
			} else if absResult < len(regs) {
				regs[absResult] = runtime.NilValue()
			}
		}

	case TableOpSetField:
		absTable := base + int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		absVal := base + int(ctx.TableValSlot)
		if absTable < len(regs) && constIdx < len(proto.Constants) && absVal < len(regs) {
			tblVal := regs[absTable]
			fieldName := proto.Constants[constIdx].Str()
			valVal := regs[absVal]
			if tblVal.IsTable() {
				tblVal.Table().RawSetString(fieldName, valVal)
			}
		}

	default:
		return fmt.Errorf("unknown table op %d", ctx.TableOp)
	}
	return nil
}

// executeOpExit handles generic op-exits in the TieringManager's Tier 2 path.
func (tm *TieringManager) executeOpExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	op := Op(ctx.OpExitOp)
	absSlot := base + int(ctx.OpExitSlot)
	absArg1 := base + int(ctx.OpExitArg1)
	absArg2 := base + int(ctx.OpExitArg2)
	aux := int(ctx.OpExitAux)

	switch op {
	case OpConstString:
		if aux >= 0 && aux < len(proto.Constants) {
			if absSlot < len(regs) {
				regs[absSlot] = proto.Constants[aux]
			}
		}

	case OpConcat:
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			s1 := regs[absArg1].String()
			s2 := regs[absArg2].String()
			regs[absSlot] = runtime.StringValue(s1 + s2)
		}

	case OpLen:
		if absArg1 < len(regs) && absSlot < len(regs) {
			v := regs[absArg1]
			if v.IsTable() {
				regs[absSlot] = runtime.IntValue(int64(v.Table().Len()))
			} else if v.IsString() {
				regs[absSlot] = runtime.IntValue(int64(len(v.Str())))
			} else {
				regs[absSlot] = runtime.IntValue(0)
			}
		}

	case OpPow:
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			var base2, exp float64
			v1 := regs[absArg1]
			v2 := regs[absArg2]
			if v1.IsInt() {
				base2 = float64(v1.Int())
			} else {
				base2 = v1.Float()
			}
			if v2.IsInt() {
				exp = float64(v2.Int())
			} else {
				exp = v2.Float()
			}
			regs[absSlot] = runtime.FloatValue(math.Pow(base2, exp))
		}

	case OpSetGlobal:
		if tm.callVM == nil {
			return fmt.Errorf("no callVM set for SetGlobal op-exit")
		}
		if aux >= 0 && aux < len(proto.Constants) {
			name := proto.Constants[aux].Str()
			if absArg1 < len(regs) {
				tm.callVM.SetGlobal(name, regs[absArg1])
			}
		}

	case OpAppend:
		if absArg1 < len(regs) && absArg2 < len(regs) {
			tblVal := regs[absArg1]
			val := regs[absArg2]
			if tblVal.IsTable() {
				tblVal.Table().Append(val)
			}
		}

	case OpSelf:
		if absArg1 < len(regs) && absSlot < len(regs) && absSlot+1 < len(regs) {
			tblVal := regs[absArg1]
			regs[absSlot+1] = tblVal
			if tblVal.IsTable() && aux >= 0 && aux < len(proto.Constants) {
				methodName := proto.Constants[aux].Str()
				regs[absSlot] = tblVal.Table().RawGetString(methodName)
			} else {
				regs[absSlot] = runtime.NilValue()
			}
		}

	case OpClose:
		// No-op.

	case OpSetList:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpGetUpval, OpSetUpval, OpClosure:
		return fmt.Errorf("op-exit not yet implemented: %s (needs closure)", op)

	case OpVararg:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpTestSet:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpForPrep, OpForLoop:
		return fmt.Errorf("op-exit unexpected: %s (should be decomposed by graph builder)", op)

	case OpTForCall, OpTForLoop:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpGuardType, OpGuardNonNil, OpGuardTruthy:
		return fmt.Errorf("op-exit guard failure: %s", op)

	case OpGo, OpMakeChan, OpSend, OpRecv:
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	default:
		return fmt.Errorf("unsupported op-exit: %s (%d)", op, int(op))
	}

	return nil
}
