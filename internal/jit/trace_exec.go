package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// debugSSAStoreBack enables debug logging around trace execution.
const debugSSAStoreBack = false

// TraceContext bridges compiled trace code and Go.
type TraceContext struct {
	Regs           uintptr // input: pointer to vm.regs[base]
	Constants      uintptr // input: pointer to proto.Constants[0]
	ExitPC         int64   // output: bytecode PC where trace exited
	ExitCode       int64   // output: 0=loop done, 1=side exit, 2=guard fail, 3=call-exit
	InnerCode      uintptr // input: code pointer for inner trace (sub-trace calling)
	InnerConstants uintptr // input: constants pointer for inner trace
	ResumePC       int64   // input: bytecode PC to resume at after call-exit (0=normal entry)
}

// TraceContext field offsets for ARM64 codegen.
const (
	TraceCtxOffRegs           = 0
	TraceCtxOffConstants      = 8
	TraceCtxOffExitPC         = 16
	TraceCtxOffExitCode       = 24
	TraceCtxOffInnerCode      = 32
	TraceCtxOffInnerConstants = 40
	TraceCtxOffResumePC       = 48
)

// SideExitBlacklistThreshold is the minimum number of executions before
// blacklisting is considered.
const SideExitBlacklistThreshold = 50

// SideExitBlacklistRatio is the minimum side-exit ratio to trigger blacklisting.
const SideExitBlacklistRatio = 0.95

// TraceCallHandler executes an external function call on behalf of trace JIT code.
type TraceCallHandler func(fnVal runtime.Value, args []runtime.Value) ([]runtime.Value, error)

// CompiledTrace holds native code for a trace.
type CompiledTrace struct {
	code      *CodeBlock
	proto     *vm.FuncProto
	loopPC    int              // PC of the FORLOOP instruction this trace was compiled for
	constants []runtime.Value  // trace-level constant pool

	// Sub-trace calling: if this trace contains a CALL_INNER_TRACE,
	// innerTrace points to the compiled inner loop trace.
	innerTrace *CompiledTrace

	// hasCallExit indicates this trace contains SSA_CALL instructions
	// that require call-exit re-entry (ExitCode=3).
	hasCallExit bool

	// callHandler executes external function calls for call-exit support.
	callHandler TraceCallHandler

	// Blacklisting: tracks whether this trace is doing useful work.
	sideExitCount  int
	fullRunCount   int
	guardFailCount int
	blacklisted    bool
}

// Execute implements vm.TraceExecutor.
func (ct *CompiledTrace) Execute(regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool, guardFail bool) {
	return executeTrace(ct, regs, base, proto)
}

// RecordResult implements vm.TraceExecutor.
func (ct *CompiledTrace) RecordResult(sideExit bool) {
	if sideExit {
		ct.sideExitCount++
	} else {
		ct.fullRunCount++
	}
	total := ct.sideExitCount + ct.fullRunCount
	if total == SideExitBlacklistThreshold {
		ratio := float64(ct.sideExitCount) / float64(total)
		if ratio >= SideExitBlacklistRatio {
			ct.blacklisted = true
		}
	}
}

// guardFailBlacklistThreshold is the number of consecutive guard failures
// before a trace is blacklisted.
const guardFailBlacklistThreshold = 5

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

	if debugSSAStoreBack {
		fmt.Printf("[TRACE-EXEC] before: R21=0x%x\n", uint64(regs[base+21]))
	}

	for {
		callJIT(uintptr(ct.code.Ptr()), ctxPtr)

		if debugSSAStoreBack {
			fmt.Printf("[TRACE-EXEC] after:  R21=0x%x exitCode=%d exitPC=%d loopPC=%d\n", uint64(regs[base+21]), ctx.ExitCode, ctx.ExitPC, ct.loopPC)
		}

		switch ctx.ExitCode {
		case 3:
			// Call-exit: trace hit an OP_CALL, needs VM to execute it.
			if ct.callHandler == nil {
				ct.guardFailCount = 0
				return int(ctx.ExitPC), true, false
			}
			nextPC := handleTraceCallExit(ct, regs, base, &ctx)
			ctx.ResumePC = int64(nextPC)
			ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
			continue

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
			ct.guardFailCount = 0
			return int(ctx.ExitPC), true, false // side exit

		default:
			ct.guardFailCount = 0
			return int(ctx.ExitPC), false, false // loop done
		}
	}
}

// handleTraceCallExit executes an OP_CALL instruction on behalf of the trace JIT.
func handleTraceCallExit(ct *CompiledTrace, regs []runtime.Value, base int, ctx *TraceContext) int {
	pc := int(ctx.ExitPC)
	proto := ct.proto
	if pc < 0 || pc >= len(proto.Code) {
		return pc + 1
	}

	inst := proto.Code[pc]
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
		if debugSSAStoreBack {
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
