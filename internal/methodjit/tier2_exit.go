//go:build darwin && arm64

// tier2_exit.go implements exit-resume for the Tier 2 memory-to-memory emitter.
// When the JIT encounters an operation it cannot compile natively (calls,
// globals, tables, etc.), it exits to Go with state in ExecContext. Go performs
// the operation, then re-enters the JIT at the resume point.
//
// This reuses the same ExecContext fields and exit codes as the regalloc-based
// Tier 2 emitter, so the Execute loop can handle both.

package methodjit

import (
	"fmt"
	"math"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
)

// --- Call exit ---

func (tc *tier2Context) t2EmitCallExit(instr *Instr) {
	asm := tc.asm

	funcSlot := int(instr.Aux)
	nArgs := len(instr.Args) - 1
	nRets := 1
	if instr.Aux2 >= 2 {
		nRets = int(instr.Aux2) - 1
	}

	// Store the function value to regs[funcSlot].
	if len(instr.Args) > 0 {
		tc.t2LoadValue(jit.X0, instr.Args[0].ID)
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(funcSlot))
	}

	// Store arguments to regs[funcSlot+1..funcSlot+nArgs].
	for i := 1; i < len(instr.Args); i++ {
		tc.t2LoadValue(jit.X0, instr.Args[i].ID)
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(funcSlot+i))
	}

	// Write call descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(funcSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallSlot)
	asm.LoadImm64(jit.X0, int64(nArgs))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNArgs)
	asm.LoadImm64(jit.X0, int64(nRets))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallNRets)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffCallID)

	// Set ExitCode = ExitCallExit and return to Go.
	asm.LoadImm64(jit.X0, ExitCallExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t2_exit")

	// Continue label: resume entry jumps here.
	continueLabel := fmt.Sprintf("t2_call_cont_%d", instr.ID)
	asm.Label(continueLabel)

	// Load call result from regs[funcSlot] into the SSA value's home.
	resultSlot, ok := tc.slotMap[instr.ID]
	if ok {
		asm.LDR(jit.X0, mRegRegs, t2SlotOffset(funcSlot))
		asm.STR(jit.X0, mRegRegs, t2SlotOffset(resultSlot))
	}

	// Record for deferred resume entry generation.
	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Global exit ---

func (tc *tier2Context) t2EmitGlobalExit(instr *Instr) {
	asm := tc.asm

	resultSlot, ok := tc.slotMap[instr.ID]
	if !ok {
		resultSlot = tc.nextSlot
		tc.slotMap[instr.ID] = resultSlot
		tc.nextSlot++
	}

	// Write global descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalSlot)
	asm.LoadImm64(jit.X0, instr.Aux) // constant pool index for name
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalConst)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffGlobalExitID)

	// Set ExitCode = ExitGlobalExit and return to Go.
	asm.LoadImm64(jit.X0, ExitGlobalExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t2_exit")

	// Continue label.
	continueLabel := fmt.Sprintf("t2_global_cont_%d", instr.ID)
	asm.Label(continueLabel)

	// The Go-side handler stored the result to regs[resultSlot].

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Table exit ---

func (tc *tier2Context) t2EmitTableExit(instr *Instr) {
	asm := tc.asm

	resultSlot, ok := tc.slotMap[instr.ID]
	if !ok {
		resultSlot = tc.nextSlot
		tc.slotMap[instr.ID] = resultSlot
		tc.nextSlot++
	}

	// Determine the table operation and set up the descriptor.
	switch instr.Op {
	case OpNewTable:
		asm.LoadImm64(jit.X0, TableOpNewTable)
		asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
		asm.LoadImm64(jit.X0, int64(resultSlot))
		asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
		asm.LoadImm64(jit.X0, instr.Aux) // array hint
		asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
		asm.LoadImm64(jit.X0, instr.Aux2) // hash hint
		asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)

	case OpGetTable:
		if len(instr.Args) >= 2 {
			// Store table and key to their slots.
			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			tableSlot := tc.slotMap[instr.Args[0].ID]
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(tableSlot))

			tc.t2LoadValue(jit.X0, instr.Args[1].ID)
			keySlot := tc.slotMap[instr.Args[1].ID]
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(keySlot))

			asm.LoadImm64(jit.X0, TableOpGetTable)
			asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
			asm.LoadImm64(jit.X0, int64(tableSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
			asm.LoadImm64(jit.X0, int64(keySlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
			asm.LoadImm64(jit.X0, int64(resultSlot)) // result in Aux
			asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
		}

	case OpSetTable:
		if len(instr.Args) >= 3 {
			tableSlot := tc.slotMap[instr.Args[0].ID]
			keySlot := tc.slotMap[instr.Args[1].ID]
			valSlot := tc.slotMap[instr.Args[2].ID]

			// Ensure values are in memory.
			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(tableSlot))
			tc.t2LoadValue(jit.X0, instr.Args[1].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(keySlot))
			tc.t2LoadValue(jit.X0, instr.Args[2].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(valSlot))

			asm.LoadImm64(jit.X0, TableOpSetTable)
			asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
			asm.LoadImm64(jit.X0, int64(tableSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
			asm.LoadImm64(jit.X0, int64(keySlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableKeySlot)
			asm.LoadImm64(jit.X0, int64(valSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
		}

	case OpGetField:
		if len(instr.Args) >= 1 {
			tableSlot := tc.slotMap[instr.Args[0].ID]
			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(tableSlot))

			asm.LoadImm64(jit.X0, TableOpGetField)
			asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
			asm.LoadImm64(jit.X0, int64(tableSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
			asm.LoadImm64(jit.X0, instr.Aux) // constant pool index
			asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
			asm.LoadImm64(jit.X0, int64(resultSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableAux2)
		}

	case OpSetField:
		if len(instr.Args) >= 2 {
			tableSlot := tc.slotMap[instr.Args[0].ID]
			valSlot := tc.slotMap[instr.Args[1].ID]

			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(tableSlot))
			tc.t2LoadValue(jit.X0, instr.Args[1].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(valSlot))

			asm.LoadImm64(jit.X0, TableOpSetField)
			asm.STR(jit.X0, mRegCtx, execCtxOffTableOp)
			asm.LoadImm64(jit.X0, int64(tableSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableSlot)
			asm.LoadImm64(jit.X0, instr.Aux)
			asm.STR(jit.X0, mRegCtx, execCtxOffTableAux)
			asm.LoadImm64(jit.X0, int64(valSlot))
			asm.STR(jit.X0, mRegCtx, execCtxOffTableValSlot)
		}
	}

	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffTableExitID)

	// Set ExitCode = ExitTableExit and return to Go.
	asm.LoadImm64(jit.X0, ExitTableExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t2_exit")

	// Continue label.
	continueLabel := fmt.Sprintf("t2_table_cont_%d", instr.ID)
	asm.Label(continueLabel)

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Generic op-exit ---

func (tc *tier2Context) t2EmitOpExit(instr *Instr) {
	asm := tc.asm

	resultSlot, ok := tc.slotMap[instr.ID]
	if !ok {
		resultSlot = tc.nextSlot
		tc.slotMap[instr.ID] = resultSlot
		tc.nextSlot++
	}

	// Resolve arg slots and store values to memory.
	arg1Slot := int64(0)
	arg2Slot := int64(0)
	if len(instr.Args) > 0 {
		s, sok := tc.slotMap[instr.Args[0].ID]
		if sok {
			arg1Slot = int64(s)
			tc.t2LoadValue(jit.X0, instr.Args[0].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(s))
		}
	}
	if len(instr.Args) > 1 {
		s, sok := tc.slotMap[instr.Args[1].ID]
		if sok {
			arg2Slot = int64(s)
			tc.t2LoadValue(jit.X0, instr.Args[1].ID)
			asm.STR(jit.X0, mRegRegs, t2SlotOffset(s))
		}
	}

	// Write op descriptor to ExecContext.
	asm.LoadImm64(jit.X0, int64(instr.Op))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitOp)
	asm.LoadImm64(jit.X0, int64(resultSlot))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitSlot)
	asm.LoadImm64(jit.X0, arg1Slot)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg1)
	asm.LoadImm64(jit.X0, arg2Slot)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitArg2)
	asm.LoadImm64(jit.X0, instr.Aux)
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitAux)
	asm.LoadImm64(jit.X0, int64(instr.ID))
	asm.STR(jit.X0, mRegCtx, execCtxOffOpExitID)

	// Set ExitCode = ExitOpExit and return to Go.
	asm.LoadImm64(jit.X0, ExitOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
	asm.B("t2_exit")

	// Continue label.
	continueLabel := fmt.Sprintf("t2_op_cont_%d", instr.ID)
	asm.Label(continueLabel)

	// Load result from register file.
	asm.LDR(jit.X0, mRegRegs, t2SlotOffset(resultSlot))
	tc.t2StoreValue(jit.X0, instr.ID)

	tc.exitIDs = append(tc.exitIDs, instr.ID)
	tc.deferredResumes = append(tc.deferredResumes, tier2Resume{
		instrID:       instr.ID,
		continueLabel: continueLabel,
	})
}

// --- Go-side exit handlers ---

// t2ExecuteCallExit handles a call-exit for the Tier 2 Execute loop.
func (cf *Tier2CompiledFunc) t2ExecuteCallExit(ctx *ExecContext, regs []runtime.Value) error {
	callSlot := int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	nRets := int(ctx.CallNRets)

	if callSlot >= len(regs) {
		return fmt.Errorf("call slot %d out of range", callSlot)
	}
	fnVal := regs[callSlot]

	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := callSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	if cf.CallVM == nil {
		return fmt.Errorf("no CallVM set for call-exit")
	}
	results, err := cf.CallVM.CallValue(fnVal, callArgs)
	if err != nil {
		return err
	}

	nr := nRets
	if nr <= 0 {
		nr = 1
	}
	for i := 0; i < nr; i++ {
		idx := callSlot + i
		if idx < len(regs) {
			if i < len(results) {
				regs[idx] = results[i]
			} else {
				regs[idx] = runtime.NilValue()
			}
		}
	}
	return nil
}

// t2ExecuteGlobalExit handles a global-exit for the Tier 2 Execute loop.
func (cf *Tier2CompiledFunc) t2ExecuteGlobalExit(ctx *ExecContext, regs []runtime.Value) error {
	if cf.CallVM == nil {
		return fmt.Errorf("no CallVM set for global-exit")
	}

	globalSlot := int(ctx.GlobalSlot)
	constIdx := int(ctx.GlobalConst)

	if cf.Proto == nil || constIdx >= len(cf.Proto.Constants) {
		return fmt.Errorf("global constant index %d out of range", constIdx)
	}
	globalName := cf.Proto.Constants[constIdx].Str()
	val := cf.CallVM.GetGlobal(globalName)

	if globalSlot < len(regs) {
		regs[globalSlot] = val
	}
	return nil
}

// t2ExecuteTableExit handles a table-exit for the Tier 2 Execute loop.
func (cf *Tier2CompiledFunc) t2ExecuteTableExit(ctx *ExecContext, regs []runtime.Value) error {
	switch ctx.TableOp {
	case TableOpNewTable:
		arrayHint := int(ctx.TableAux)
		hashHint := int(ctx.TableAux2)
		tbl := runtime.NewTableSized(arrayHint, hashHint)
		resultSlot := int(ctx.TableSlot)
		if resultSlot < len(regs) {
			regs[resultSlot] = runtime.TableValue(tbl)
		}

	case TableOpGetTable:
		tableSlot := int(ctx.TableSlot)
		keySlot := int(ctx.TableKeySlot)
		resultSlot := int(ctx.TableAux)
		if tableSlot < len(regs) && keySlot < len(regs) {
			tblVal := regs[tableSlot]
			keyVal := regs[keySlot]
			if tblVal.IsTable() {
				result := tblVal.Table().RawGet(keyVal)
				if resultSlot < len(regs) {
					regs[resultSlot] = result
				}
			} else if resultSlot < len(regs) {
				regs[resultSlot] = runtime.NilValue()
			}
		}

	case TableOpSetTable:
		tableSlot := int(ctx.TableSlot)
		keySlot := int(ctx.TableKeySlot)
		valSlot := int(ctx.TableValSlot)
		if tableSlot < len(regs) && keySlot < len(regs) && valSlot < len(regs) {
			tblVal := regs[tableSlot]
			keyVal := regs[keySlot]
			valVal := regs[valSlot]
			if tblVal.IsTable() {
				tblVal.Table().RawSet(keyVal, valVal)
			}
		}

	case TableOpGetField:
		tableSlot := int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		resultSlot := int(ctx.TableAux2)
		if tableSlot < len(regs) && cf.Proto != nil && constIdx < len(cf.Proto.Constants) {
			tblVal := regs[tableSlot]
			fieldName := cf.Proto.Constants[constIdx].Str()
			if tblVal.IsTable() {
				result := tblVal.Table().RawGetString(fieldName)
				if resultSlot < len(regs) {
					regs[resultSlot] = result
				}
			} else if resultSlot < len(regs) {
				regs[resultSlot] = runtime.NilValue()
			}
		}

	case TableOpSetField:
		tableSlot := int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		valSlot := int(ctx.TableValSlot)
		if tableSlot < len(regs) && cf.Proto != nil && constIdx < len(cf.Proto.Constants) && valSlot < len(regs) {
			tblVal := regs[tableSlot]
			fieldName := cf.Proto.Constants[constIdx].Str()
			valVal := regs[valSlot]
			if tblVal.IsTable() {
				tblVal.Table().RawSetString(fieldName, valVal)
			}
		}

	default:
		return fmt.Errorf("unknown table op %d", ctx.TableOp)
	}
	return nil
}

// t2ExecuteOpExit handles a generic op-exit for the Tier 2 Execute loop.
func (cf *Tier2CompiledFunc) t2ExecuteOpExit(ctx *ExecContext, regs []runtime.Value) error {
	op := Op(ctx.OpExitOp)
	slot := int(ctx.OpExitSlot)
	arg1 := int(ctx.OpExitArg1)
	arg2 := int(ctx.OpExitArg2)
	aux := int(ctx.OpExitAux)

	switch op {
	case OpConstString:
		if cf.Proto != nil && aux >= 0 && aux < len(cf.Proto.Constants) {
			if slot < len(regs) {
				regs[slot] = cf.Proto.Constants[aux]
			}
		}

	case OpConcat:
		if arg1 < len(regs) && arg2 < len(regs) && slot < len(regs) {
			s1 := regs[arg1].String()
			s2 := regs[arg2].String()
			regs[slot] = runtime.StringValue(s1 + s2)
		}

	case OpLen:
		if arg1 < len(regs) && slot < len(regs) {
			v := regs[arg1]
			if v.IsTable() {
				regs[slot] = runtime.IntValue(int64(v.Table().Len()))
			} else if v.IsString() {
				regs[slot] = runtime.IntValue(int64(len(v.Str())))
			} else {
				regs[slot] = runtime.IntValue(0)
			}
		}

	case OpPow:
		if arg1 < len(regs) && arg2 < len(regs) && slot < len(regs) {
			var base2, exp float64
			v1 := regs[arg1]
			v2 := regs[arg2]
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
			regs[slot] = runtime.FloatValue(math.Pow(base2, exp))
		}

	case OpSetGlobal:
		if cf.CallVM != nil && cf.Proto != nil && aux >= 0 && aux < len(cf.Proto.Constants) {
			name := cf.Proto.Constants[aux].Str()
			if arg1 < len(regs) {
				cf.CallVM.SetGlobal(name, regs[arg1])
			}
		}

	case OpAppend:
		if arg1 < len(regs) && arg2 < len(regs) {
			tblVal := regs[arg1]
			val := regs[arg2]
			if tblVal.IsTable() {
				tblVal.Table().Append(val)
			}
		}

	case OpSelf:
		if arg1 < len(regs) && slot < len(regs) && slot+1 < len(regs) {
			tblVal := regs[arg1]
			regs[slot+1] = tblVal
			if tblVal.IsTable() && cf.Proto != nil && aux >= 0 && aux < len(cf.Proto.Constants) {
				methodName := cf.Proto.Constants[aux].Str()
				regs[slot] = tblVal.Table().RawGetString(methodName)
			} else {
				regs[slot] = runtime.NilValue()
			}
		}

	case OpClose:
		// No-op.

	default:
		return fmt.Errorf("tier2 op-exit not supported: %s (%d)", op, int(op))
	}

	return nil
}
