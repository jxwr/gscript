//go:build darwin && arm64

// tiering_execute.go implements exit handlers for the tiering path's Execute loop.
// When the JIT encounters a call, global lookup, or table operation, it exits
// to Go with the relevant state in ExecContext. These handlers perform the
// operation using the caller VM's register file and return so Execute can
// re-enter the JIT at the resume point.
//
// Unlike the standalone CompiledFunction.Execute (emit_execute.go) which uses
// its own VM and register file, the tiering path operates on the caller VM's
// shared register file (regs[base:]). Slot indices in ExecContext are relative
// to the callee's frame (base=0 in JIT), so we add `base` to get absolute
// positions in the shared register file.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// executeCallExit handles a call-exit in the tiering path.
// The JIT has stored all register-resident values to memory before exiting.
// The function value and arguments are at regs[base+callSlot..base+callSlot+nArgs].
// We call the function via the VM and place the result back.
//
// IMPORTANT: The callee (via vm.call) may grow the VM's register file, which
// invalidates the regs slice. After CallValue returns, we re-read the VM's
// register file to ensure results are written to the correct backing array.
func (e *MethodJITEngine) executeCallExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM set for call-exit")
	}

	callSlot := int(ctx.CallSlot)
	nArgs := int(ctx.CallNArgs)
	nRets := int(ctx.CallNRets)

	// Absolute position in the shared register file.
	absSlot := base + callSlot
	if absSlot >= len(regs) {
		return fmt.Errorf("call slot %d (abs %d) out of range (regs len %d)", callSlot, absSlot, len(regs))
	}
	fnVal := regs[absSlot]

	// Collect arguments from regs[absSlot+1 .. absSlot+nArgs].
	callArgs := make([]runtime.Value, nArgs)
	for i := 0; i < nArgs; i++ {
		idx := absSlot + 1 + i
		if idx < len(regs) {
			callArgs[i] = regs[idx]
		}
	}

	// Execute the call via the VM. This may grow vm.regs.
	results, err := e.callVM.CallValue(fnVal, callArgs)
	if err != nil {
		return err
	}

	// Re-read the VM's register file — it may have been reallocated by the callee.
	currentRegs := e.callVM.Regs()

	// Place results back at currentRegs[absSlot..absSlot+nRets-1].
	// Follows Lua calling convention: results overwrite the function slot.
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

// executeGlobalExit handles a global-exit in the tiering path.
// Loads a global variable by name (from the constants pool) and stores
// the result to regs[base+globalSlot].
func (e *MethodJITEngine) executeGlobalExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	if e.callVM == nil {
		return fmt.Errorf("no callVM set for global-exit")
	}

	globalSlot := int(ctx.GlobalSlot)
	constIdx := int(ctx.GlobalConst)

	// Look up the global name from the constants pool.
	if constIdx >= len(proto.Constants) {
		return fmt.Errorf("global constant index %d out of range (len %d)", constIdx, len(proto.Constants))
	}
	globalName := proto.Constants[constIdx].Str()

	// Resolve the global value via the VM.
	val := e.callVM.GetGlobal(globalName)

	// Store to the absolute slot in the shared register file.
	absSlot := base + globalSlot
	if absSlot < len(regs) {
		regs[absSlot] = val
	}

	return nil
}

// executeTableExit handles table operations (NewTable, GetTable, SetTable,
// GetField, SetField) in the tiering path. Slot indices in ExecContext are
// relative to the callee's frame; we add `base` for absolute positions.
func (e *MethodJITEngine) executeTableExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
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
		absResult := base + int(ctx.TableAux) // result slot stored in Aux
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
