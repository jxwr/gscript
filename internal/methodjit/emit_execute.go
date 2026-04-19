//go:build darwin && arm64

// emit_execute.go implements the Execute loop for CompiledFunction.
// Handles normal return, deoptimization, call-exit (function calls via VM),
// global-exit (global variable lookup), and table-exit (field access).
// Each exit type stores state in ExecContext, returns to Go, executes
// the operation, then re-enters the JIT at a resume point.

package methodjit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

var _ = fmt.Sprintf
var _ unsafe.Pointer
var _ jit.Reg
var _ runtime.Value
var _ *vm.FuncProto

func (cf *CompiledFunction) Execute(args []runtime.Value) ([]runtime.Value, error) {
	// Allocate VM registers (NaN-boxed values).
	nregs := cf.numRegs
	if nregs < len(args)+1 {
		nregs = len(args) + 1
	}
	if nregs < 16 {
		nregs = 16 // minimum to avoid out-of-bounds
	}
	regs := make([]runtime.Value, nregs)

	// Load arguments into slots 0, 1, 2, ...
	for i, arg := range args {
		regs[i] = arg
	}
	// Fill remaining with nil.
	for i := len(args); i < nregs; i++ {
		regs[i] = runtime.NilValue()
	}

	// Set up ExecContext.
	var ctx ExecContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if cf.Proto != nil && len(cf.Proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&cf.Proto.Constants[0]))
	}

	// Set up Tier 2 global value cache pointers (standalone mode).
	// Uses a local generation counter since there's no TieringManager.
	var standaloneGenCounter uint64
	if len(cf.GlobalCache) > 0 {
		ctx.Tier2GlobalCache = uintptr(unsafe.Pointer(&cf.GlobalCache[0]))
		ctx.Tier2GlobalCacheGen = uintptr(unsafe.Pointer(&cf.GlobalCacheGen))
		ctx.Tier2GlobalGenPtr = uintptr(unsafe.Pointer(&standaloneGenCounter))
	}
	// R108: set mono call-IC cache pointer.
	if len(cf.CallCache) > 0 {
		ctx.Tier2CallCache = uintptr(unsafe.Pointer(&cf.CallCache[0]))
	}

	// Entry point: start at the beginning of the function.
	codePtr := uintptr(cf.Code.Ptr())
	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	for {
		jit.CallJIT(codePtr, ctxPtr)

		switch ctx.ExitCode {
		case ExitNormal:
			// Normal return: read result from slot 0.
			return []runtime.Value{regs[0]}, nil

		case ExitDeopt:
			// JIT bailed out: fall back to VM interpreter.
			if cf.DeoptFunc != nil {
				return cf.DeoptFunc(args)
			}
			return nil, fmt.Errorf("methodjit: deopt with no DeoptFunc set")

		case ExitCallExit:
			// Call-exit: execute the call via VM, then resume JIT.
			err := cf.executeCallExit(&ctx, regs)
			if err != nil {
				return nil, fmt.Errorf("methodjit: call-exit error: %w", err)
			}

			// Resume at the resume point for this call instruction.
			callID := int(ctx.CallID)
			resumeOff, ok := cf.ResumeAddrs[callID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume address for call ID %d", callID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitGlobalExit:
			// Global-exit: load a global variable via the VM, then resume JIT.
			err := cf.executeGlobalExit(&ctx, regs)
			if err != nil {
				return nil, fmt.Errorf("methodjit: global-exit error: %w", err)
			}

			// Resume at the resume point for this global instruction.
			globalID := int(ctx.GlobalExitID)
			resumeOff, ok := cf.ResumeAddrs[globalID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume address for global ID %d", globalID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitTableExit:
			// Table-exit: perform table operation via Go, then resume JIT.
			err := cf.executeTableExit(&ctx, regs)
			if err != nil {
				return nil, fmt.Errorf("methodjit: table-exit error: %w", err)
			}

			// Resume at the resume point for this table instruction.
			tableID := int(ctx.TableExitID)
			resumeOff, ok := cf.ResumeAddrs[tableID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume address for table ID %d", tableID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		case ExitOpExit:
			// Op-exit: execute unsupported operation via Go, then resume JIT.
			err := cf.executeOpExit(&ctx, regs)
			if err != nil {
				return nil, fmt.Errorf("methodjit: op-exit error: %w", err)
			}

			// Resume at the resume point for this op instruction.
			opID := int(ctx.OpExitID)
			resumeOff, ok := cf.ResumeAddrs[opID]
			if !ok {
				return nil, fmt.Errorf("methodjit: no resume address for op ID %d", opID)
			}
			codePtr = uintptr(cf.Code.Ptr()) + uintptr(resumeOff)
			ctx.ExitCode = 0
			continue

		default:
			return nil, fmt.Errorf("methodjit: unknown exit code %d", ctx.ExitCode)
		}
	}
}

// executeCallExit handles a call-exit by executing the call via the VM.
// The JIT has stored all register-resident values to memory before exiting,
// so the VM register file (regs) is fully up-to-date.
func (cf *CompiledFunction) executeCallExit(ctx *ExecContext, regs []runtime.Value) error {
	callSlot := int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	nRets := int(ctx.CallNRets)

	// Get the function value from the register file.
	if callSlot >= len(regs) {
		return fmt.Errorf("call slot %d out of range (regs len %d)", callSlot, len(regs))
	}
	fnVal := regs[callSlot]

	// Collect arguments from regs[callSlot+1 .. callSlot+nArgs].
	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := callSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	// Execute the call.
	var results []runtime.Value
	var err error

	if cf.CallVM != nil {
		results, err = cf.CallVM.CallValue(fnVal, callArgs)
	} else if cf.DeoptFunc != nil {
		// Fallback: no CallVM, try to use the function value directly.
		return fmt.Errorf("no CallVM set for call-exit")
	} else {
		return fmt.Errorf("no CallVM or DeoptFunc set for call-exit")
	}
	if err != nil {
		return err
	}

	// Place results back into the register file at regs[callSlot..callSlot+nRets-1].
	// This follows Lua calling convention: results overwrite the function slot.
	nr := nRets
	if nr <= 0 {
		nr = 1 // at minimum, 1 result
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

// executeGlobalExit handles a global-exit by loading a global variable via the VM.
// The global name is looked up from the constants pool and resolved via the VM.
// Also populates the per-instruction global value cache in CompiledFunction.
func (cf *CompiledFunction) executeGlobalExit(ctx *ExecContext, regs []runtime.Value) error {
	globalSlot := int(ctx.GlobalSlot)
	constIdx := int(ctx.GlobalConst)

	if cf.CallVM == nil {
		return fmt.Errorf("no CallVM set for global-exit")
	}

	// Look up the global name from the constants pool.
	if cf.Proto == nil || constIdx >= len(cf.Proto.Constants) {
		return fmt.Errorf("global constant index %d out of range", constIdx)
	}
	globalName := cf.Proto.Constants[constIdx].Str()

	// Resolve the global value.
	val := cf.CallVM.GetGlobal(globalName)

	// Store the global value to the register file.
	if globalSlot < len(regs) {
		regs[globalSlot] = val
	}

	// Populate the per-instruction global value cache (standalone mode).
	// In standalone mode there's no shared generation counter, so we just
	// populate and never invalidate (no SetGlobal path in standalone tests).
	cacheIdx := int(ctx.GlobalCacheIdx)
	if cacheIdx >= 0 && cf.GlobalCache != nil && cacheIdx < len(cf.GlobalCache) {
		valBits := uint64(val)
		if valBits != 0 {
			cf.GlobalCache[cacheIdx] = valBits
		}
	}

	return nil
}

// executeTableExit handles table operations (NewTable, GetTable, SetTable,
// GetField/SetField fallback) by executing them in Go, then resuming the JIT.
func (cf *CompiledFunction) executeTableExit(ctx *ExecContext, regs []runtime.Value) error {
	switch ctx.TableOp {
	case TableOpNewTable:
		// Create a new table with the given array/hash hints.
		arrayHint := int(ctx.TableAux)
		hashHint := int(ctx.TableAux2)
		tbl := runtime.NewTableSized(arrayHint, hashHint)
		resultSlot := int(ctx.TableSlot)
		if resultSlot < len(regs) {
			regs[resultSlot] = runtime.TableValue(tbl)
		}

	case TableOpGetTable:
		// R(result) = R(table)[R(key)]
		tableSlot := int(ctx.TableSlot)
		keySlot := int(ctx.TableKeySlot)
		resultSlot := int(ctx.TableAux) // result slot stored in Aux
		if tableSlot < len(regs) && keySlot < len(regs) {
			tblVal := regs[tableSlot]
			keyVal := regs[keySlot]
			if tblVal.IsTable() {
				tbl := tblVal.Table()
				result := tbl.RawGet(keyVal)
				if resultSlot < len(regs) {
					regs[resultSlot] = result
				}
			} else if resultSlot < len(regs) {
				regs[resultSlot] = runtime.NilValue()
			}
		}

	case TableOpSetTable:
		// R(table)[R(key)] = R(val)
		tableSlot := int(ctx.TableSlot)
		keySlot := int(ctx.TableKeySlot)
		valSlot := int(ctx.TableValSlot)
		if tableSlot < len(regs) && keySlot < len(regs) && valSlot < len(regs) {
			tblVal := regs[tableSlot]
			keyVal := regs[keySlot]
			valVal := regs[valSlot]
			if tblVal.IsTable() {
				tbl := tblVal.Table()
				tbl.RawSet(keyVal, valVal)
			}
		}

	case TableOpGetField:
		// R(result) = R(table).Constants[constIdx]
		tableSlot := int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		resultSlot := int(ctx.TableAux2)
		if tableSlot < len(regs) && cf.Proto != nil && constIdx < len(cf.Proto.Constants) {
			tblVal := regs[tableSlot]
			fieldName := cf.Proto.Constants[constIdx].Str()
			if tblVal.IsTable() {
				tbl := tblVal.Table()
				result := tbl.RawGetString(fieldName)
				if resultSlot < len(regs) {
					regs[resultSlot] = result
				}
			} else if resultSlot < len(regs) {
				regs[resultSlot] = runtime.NilValue()
			}
		}

	case TableOpSetField:
		// R(table).Constants[constIdx] = R(val)
		tableSlot := int(ctx.TableSlot)
		constIdx := int(ctx.TableAux)
		valSlot := int(ctx.TableValSlot)
		if tableSlot < len(regs) && cf.Proto != nil && constIdx < len(cf.Proto.Constants) && valSlot < len(regs) {
			tblVal := regs[tableSlot]
			fieldName := cf.Proto.Constants[constIdx].Str()
			valVal := regs[valSlot]
			if tblVal.IsTable() {
				tbl := tblVal.Table()
				tbl.RawSetString(fieldName, valVal)
			}
		}

	default:
		return fmt.Errorf("unknown table op %d", ctx.TableOp)
	}
	return nil
}

// executeOpExit handles a generic op-exit for the standalone Execute path.
// Slot indices are absolute (base=0 in standalone mode).
func (cf *CompiledFunction) executeOpExit(ctx *ExecContext, regs []runtime.Value) error {
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

	case OpSetGlobal:
		if cf.CallVM != nil && cf.Proto != nil && aux >= 0 && aux < len(cf.Proto.Constants) {
			name := cf.Proto.Constants[aux].Str()
			if arg1 < len(regs) {
				cf.CallVM.SetGlobal(name, regs[arg1])
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

	case OpAppend:
		if arg1 < len(regs) && arg2 < len(regs) {
			tblVal := regs[arg1]
			val := regs[arg2]
			if tblVal.IsTable() {
				tblVal.Table().Append(val)
			}
		}

	case OpSetList:
		// slot=nValues, arg1=table slot, arg2=tempBase slot, aux=arrayStart
		nValues := slot
		tableSlot := arg1
		tempBase := arg2
		arrayStart := aux
		if tableSlot < len(regs) && regs[tableSlot].IsTable() {
			tbl := regs[tableSlot].Table()
			for i := 0; i < nValues; i++ {
				valSlot := tempBase + i
				if valSlot < len(regs) {
					tbl.RawSetInt(int64(arrayStart+i), regs[valSlot])
				}
			}
		}

	default:
		return fmt.Errorf("unsupported op-exit in standalone mode: %s (%d)", op, int(op))
	}

	return nil
}
