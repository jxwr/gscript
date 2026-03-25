//go:build darwin && arm64

package jit

import (
	"fmt"
	"strings"
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

	maxExecAttempts := 1000000
	for attempt := 0; attempt < maxExecAttempts; attempt++ {
		callJIT(uintptr(ct.code.Ptr()), ctxPtr)

		if debugTrace {
			fmt.Printf("[TRACE-EXEC] after: exitCode=%d exitPC=%d snapIdx=%d resumePC=%d\n",
				ctx.ExitCode, ctx.ExitPC, ctx.ExitSnapIdx, ctx.ResumePC)
		}

		switch ctx.ExitCode {
		case 3:
			// Legacy call-exit code (no longer emitted). Treat as side-exit.
			ct.guardFailCount = 0
			return int(ctx.ExitPC), true, false

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

		case 4:
			// Break exit: the trace hit a break condition (e.g., mandelbrot escape).
			// This is expected behavior, NOT a sign of a bad trace.
			// Don't count toward blacklisting.
			ct.guardFailCount = 0
			return int(ctx.ExitPC), true, false

		case 1:
			// Side exit
			ct.guardFailCount = 0
			ct.sideExitCount++
			// Blacklist if side-exiting too often relative to full runs.
			// Check after enough total executions for a reliable ratio.
			total := ct.sideExitCount + ct.fullRunCount
			if total >= 50 {
				ratio := float64(ct.sideExitCount) / float64(total)
				if ratio >= 0.90 {
					ct.blacklisted = true
					if ct.proto != nil {
						ct.proto.BlacklistTracePC(ct.loopPC)
					}
				}
			}
			return int(ctx.ExitPC), true, false

		default:
			// Loop done (exit code 0)
			ct.guardFailCount = 0
			ct.fullRunCount++
			return int(ctx.ExitPC), false, false
		}
	}
	// Safety: execution limit reached, treat as side-exit
	ct.blacklisted = true
	if ct.proto != nil {
		ct.proto.BlacklistTracePC(ct.loopPC)
	}
	return int(ctx.ExitPC), true, false
}

// handleTraceCallExit executes a call-exit opcode on behalf of the trace JIT.
// Returns the next PC (pc+1) on success, or -1 on failure.
func handleTraceCallExit(ct *CompiledTrace, regs []runtime.Value, base int, ctx *TraceContext) int {
	pc := int(ctx.ExitPC)
	proto := ct.proto
	if pc < 0 || pc >= len(proto.Code) {
		return -1
	}

	inst := proto.Code[pc]
	op := vm.DecodeOp(inst)
	constants := proto.Constants

	if debugTrace {
		fmt.Printf("[TRACE-CALL-EXIT] op=%s pc=%d base=%d\n", vm.OpName(op), pc, base)
	}

	switch op {
	case vm.OP_CALL:
		if ct.callHandler == nil {
			return -1
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
			return -1
		}

		for i := 0; i < nResults; i++ {
			if i < len(results) {
				regs[base+a+i] = results[i]
			} else {
				regs[base+a+i] = runtime.NilValue()
			}
		}
		return pc + 1

	case vm.OP_GETTABLE:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+b]
		var key runtime.Value
		if cidx >= vm.RKBit {
			key = constants[cidx-vm.RKBit]
		} else {
			key = regs[base+cidx]
		}
		if tableVal.IsTable() {
			tbl := tableVal.Table()
			regs[base+a] = tbl.RawGet(key)
		} else {
			regs[base+a] = runtime.NilValue()
		}
		return pc + 1

	case vm.OP_SETTABLE:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+a]
		var key, val runtime.Value
		if bidx >= vm.RKBit {
			key = constants[bidx-vm.RKBit]
		} else {
			key = regs[base+bidx]
		}
		if cidx >= vm.RKBit {
			val = constants[cidx-vm.RKBit]
		} else {
			val = regs[base+cidx]
		}
		if tableVal.IsTable() {
			tbl := tableVal.Table()
			tbl.RawSet(key, val)
		}
		return pc + 1

	case vm.OP_GETFIELD:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		tableVal := regs[base+b]
		if tableVal.IsTable() {
			tbl := tableVal.Table()
			if proto.FieldCache == nil {
				proto.FieldCache = make([]runtime.FieldCacheEntry, len(proto.Code))
			}
			regs[base+a] = tbl.RawGetStringCached(constants[c].Str(), &proto.FieldCache[pc])
		} else {
			regs[base+a] = runtime.NilValue()
		}
		return pc + 1

	case vm.OP_SETFIELD:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+a]
		var val runtime.Value
		if cidx >= vm.RKBit {
			val = constants[cidx-vm.RKBit]
		} else {
			val = regs[base+cidx]
		}
		if tableVal.IsTable() {
			tbl := tableVal.Table()
			if proto.FieldCache == nil {
				proto.FieldCache = make([]runtime.FieldCacheEntry, len(proto.Code))
			}
			tbl.RawSetStringCached(constants[b].Str(), val, &proto.FieldCache[pc])
		}
		return pc + 1

	case vm.OP_GETGLOBAL:
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		name := constants[bx].Str()
		if ct.globalsAccessor != nil {
			regs[base+a] = ct.globalsAccessor.GetGlobal(name)
		} else {
			regs[base+a] = runtime.NilValue()
		}
		return pc + 1

	case vm.OP_SETGLOBAL:
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		name := constants[bx].Str()
		if ct.globalsAccessor != nil {
			ct.globalsAccessor.SetGlobal(name, regs[base+a])
		}
		return pc + 1

	case vm.OP_LEN:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		bv := regs[base+b]
		if bv.IsTable() {
			regs[base+a] = runtime.IntValue(int64(bv.Table().Len()))
		} else if bv.IsString() {
			regs[base+a] = runtime.IntValue(int64(len(bv.Str())))
		} else {
			regs[base+a] = runtime.IntValue(0)
		}
		return pc + 1

	case vm.OP_CONCAT:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		var sb strings.Builder
		for i := b; i <= c; i++ {
			sb.WriteString(regs[base+i].String())
		}
		regs[base+a] = runtime.StringValue(sb.String())
		return pc + 1

	case vm.OP_NEWTABLE:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		regs[base+a] = runtime.TableValue(runtime.NewTableSized(b, c))
		return pc + 1

	case vm.OP_CLOSE:
		// CLOSE doesn't modify registers in a meaningful way for the trace.
		// Just advance past it.
		return pc + 1

	default:
		if debugTrace {
			fmt.Printf("[TRACE-CALL-EXIT] unhandled op=%s at pc=%d\n", vm.OpName(op), pc)
		}
		return -1
	}
}
