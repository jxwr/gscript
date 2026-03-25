//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// Execute implements vm.TraceExecutor.
func (ct *CompiledTrace) Execute(regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool, guardFail bool) {
	return executeTrace(ct, regs, base, proto)
}

// executeTrace runs compiled trace code.
func executeTrace(ct *CompiledTrace, regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool, guardFail bool) {
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}
	if ct.innerTrace != nil {
		ctx.InnerCode = uintptr(ct.innerTrace.code.Ptr())
		if len(ct.innerTrace.constants) > 0 {
			ctx.InnerConstants = uintptr(unsafe.Pointer(&ct.innerTrace.constants[0]))
		}
	}

	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	if debugTrace {
		fmt.Printf("[TRACE-EXEC] before: base=%d loopPC=%d\n", base, ct.loopPC)
	}

	for {
		callJIT(uintptr(ct.code.Ptr()), ctxPtr)

		if debugTrace {
			fmt.Printf("[TRACE-EXEC] after: exitCode=%d exitPC=%d snapIdx=%d\n",
				ctx.ExitCode, ctx.ExitPC, ctx.ExitSnapIdx)
		}

		switch ctx.ExitCode {
		case 3:
			// Call-exit: trace hit an OP_CALL/GETGLOBAL/etc, needs VM to execute it.
			if ct.callHandler == nil {
				ct.guardFailCount = 0
				return int(ctx.ExitPC), true, false
			}
			nextPC := handleTraceCallExit(ct, regs, base, &ctx)
			// Call-exit re-entry is not yet supported in SSA-compiled traces.
			// Return as side-exit so the VM continues from nextPC.
			ct.guardFailCount = 0
			return nextPC, true, false

		case 2:
			// Guard fail: pre-loop type checks didn't match.
			ct.guardFailCount++
			if ct.guardFailCount >= guardFailBlacklistThreshold {
				ct.blacklisted = true
				if ct.proto != nil {
					ct.proto.BlacklistTracePC(ct.loopPC)
				}
			}
			return 0, false, true

		case 1:
			// Side exit
			ct.guardFailCount = 0
			return int(ctx.ExitPC), true, false

		default:
			// Loop done (exit code 0)
			ct.guardFailCount = 0
			return int(ctx.ExitPC), false, false
		}
	}
}

// handleTraceCallExit executes a call-exit opcode on behalf of the trace JIT.
func handleTraceCallExit(ct *CompiledTrace, regs []runtime.Value, base int, ctx *TraceContext) int {
	pc := int(ctx.ExitPC)
	proto := ct.proto
	if pc < 0 || pc >= len(proto.Code) {
		return pc + 1
	}

	inst := proto.Code[pc]
	op := vm.DecodeOp(inst)

	// OP_CALL: use trace-safe result placement.
	if op == vm.OP_CALL {
		if ct.callHandler == nil {
			return pc + 1
		}
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		nArgs := b - 1
		if b == 0 {
			nArgs = 0
		}
		nResults := c - 1
		if c == 0 {
			nResults = 1
		}

		fnVal := regs[base+a]
		args := make([]runtime.Value, nArgs)
		for i := 0; i < nArgs; i++ {
			args[i] = regs[base+a+1+i]
		}

		results, err := ct.callHandler(fnVal, args)
		if err != nil {
			if debugTrace {
				fmt.Printf("[TRACE-CALL-EXIT] call error: %v\n", err)
			}
			return pc + 1
		}

		for i := 0; i < nResults; i++ {
			if i < len(results) {
				regs[base+a+i] = results[i]
			} else {
				regs[base+a+i] = runtime.NilValue()
			}
		}
		return pc + 1
	}

	// Non-CALL opcodes: return pc (NOT pc+1) so the VM re-executes
	// this instruction in the interpreter. The trace has already stored
	// register values back to memory, so the interpreter has correct state.
	if debugTrace {
		fmt.Printf("[TRACE-CALL-EXIT] unhandled op=%d at pc=%d, returning to interpreter\n", op, pc)
	}
	return pc
}
