//go:build darwin && arm64

// tiering_op_exit.go implements the Go-side handler for op-exit (ExitCode=6).
// When the JIT encounters an operation it cannot compile natively, it exits
// to Go with the operation descriptor in ExecContext. This handler performs
// the operation using the VM register file, writes the result back, and
// returns so the Execute loop can resume the JIT at the continue point.
//
// The handler reads:
//   OpExitOp:   which IR Op to execute
//   OpExitSlot: destination slot (relative to callee base)
//   OpExitArg1: first operand slot (relative to callee base)
//   OpExitArg2: second operand slot (relative to callee base)
//   OpExitAux:  auxiliary data (constant pool index, etc.)
//   OpExitID:   resume point ID (instruction ID)

package methodjit

import (
	"fmt"
	"math"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// executeOpExit handles a generic op-exit in the tiering path.
// Slot indices in ExecContext are relative to the callee's frame (base=0 in JIT).
// We add `base` to get absolute positions in the shared register file.
func (e *MethodJITEngine) executeOpExit(ctx *ExecContext, regs []runtime.Value, base int, proto *vm.FuncProto) error {
	op := Op(ctx.OpExitOp)
	absSlot := base + int(ctx.OpExitSlot)
	absArg1 := base + int(ctx.OpExitArg1)
	absArg2 := base + int(ctx.OpExitArg2)
	aux := int(ctx.OpExitAux)

	switch op {
	case OpConstString:
		// Load string constant from the constant pool.
		if aux >= 0 && aux < len(proto.Constants) {
			if absSlot < len(regs) {
				regs[absSlot] = proto.Constants[aux]
			}
		}

	case OpConcat:
		// Concatenate two string values.
		if absArg1 < len(regs) && absArg2 < len(regs) && absSlot < len(regs) {
			s1 := regs[absArg1].String()
			s2 := regs[absArg2].String()
			regs[absSlot] = runtime.StringValue(s1 + s2)
		}

	case OpLen:
		// Get length of table or string.
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
		// Exponentiation: arg1 ** arg2.
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
			result := math.Pow(base2, exp)
			// Return float for pow (Lua semantics).
			regs[absSlot] = runtime.FloatValue(result)
		}

	case OpSetGlobal:
		// Set a global variable. Aux = constant pool index for the name.
		// Arg1 = value to set.
		if e.callVM == nil {
			return fmt.Errorf("no callVM set for SetGlobal op-exit")
		}
		if aux >= 0 && aux < len(proto.Constants) {
			name := proto.Constants[aux].Str()
			if absArg1 < len(regs) {
				e.callVM.SetGlobal(name, regs[absArg1])
			}
		}

	case OpSetList:
		// SETLIST: bulk-set table array portion.
		// Aux = Arg (table slot in bytecode), Arg1 slot has the table.
		// For now, do nothing — the VM handles this via OpSetList.
		// This is a complex op that needs per-element iteration.
		// Fall through to deopt for safety.
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpAppend:
		// table.insert(table, value).
		if absArg1 < len(regs) && absArg2 < len(regs) {
			tblVal := regs[absArg1]
			val := regs[absArg2]
			if tblVal.IsTable() {
				tbl := tblVal.Table()
				tbl.Append(val)
			}
		}

	case OpGetUpval:
		// Upvalue access requires the closure, which we don't have in the
		// tiering path. Fall back to error.
		return fmt.Errorf("op-exit not yet implemented: %s (needs closure)", op)

	case OpSetUpval:
		return fmt.Errorf("op-exit not yet implemented: %s (needs closure)", op)

	case OpClosure:
		return fmt.Errorf("op-exit not yet implemented: %s (needs closure)", op)

	case OpClose:
		// Close upvalues. No result to write.
		// Without closure support, this is a no-op in the op-exit path.
		// Safe to skip since Method JIT doesn't create upvalues.

	case OpSelf:
		// Method call: R(A+1) = R(B); R(A) = R(B)[RK(C)]
		// arg1 = table slot, aux = constant pool index for method name
		if absArg1 < len(regs) && absSlot < len(regs) && absSlot+1 < len(regs) {
			tblVal := regs[absArg1]
			// R(A+1) = R(B) — the table itself as the "self" argument
			regs[absSlot+1] = tblVal
			// R(A) = R(B)[method_name]
			if tblVal.IsTable() && aux >= 0 && aux < len(proto.Constants) {
				methodName := proto.Constants[aux].Str()
				regs[absSlot] = tblVal.Table().RawGetString(methodName)
			} else {
				regs[absSlot] = runtime.NilValue()
			}
		}

	case OpVararg:
		// Varargs are complex. For now, return error.
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpTestSet:
		// Short-circuit: if bool(arg1) != bool(aux) then skip, else result = arg1
		// This is tricky with control flow. For now, error.
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpForPrep, OpForLoop:
		// These should never appear in the IR — the graph builder decomposes
		// them into OpAdd/OpSub/OpLe/OpBranch. If they somehow appear, error.
		return fmt.Errorf("op-exit unexpected: %s (should be decomposed by graph builder)", op)

	case OpTForCall:
		// Generic for iterator call. Complex, needs closure.
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpTForLoop:
		// Generic for loop control. Complex.
		return fmt.Errorf("op-exit not yet implemented: %s", op)

	case OpGuardType, OpGuardNonNil, OpGuardTruthy:
		// Guard failure should deopt, not op-exit.
		// But if we're here, just return error to trigger deopt.
		return fmt.Errorf("op-exit guard failure: %s", op)

	case OpGo:
		return fmt.Errorf("op-exit not yet implemented: %s (goroutine)", op)

	case OpMakeChan:
		return fmt.Errorf("op-exit not yet implemented: %s (channel)", op)

	case OpSend:
		return fmt.Errorf("op-exit not yet implemented: %s (channel)", op)

	case OpRecv:
		return fmt.Errorf("op-exit not yet implemented: %s (channel)", op)

	default:
		return fmt.Errorf("unsupported op-exit: %s (%d)", op, int(op))
	}

	return nil
}
