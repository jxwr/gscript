//go:build darwin && arm64

package jit

import (
	"fmt"
	"runtime"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// handleCallExit handles a call-exit (ExitCode=2) by executing the instruction
// at ctx.ExitPC in Go and placing results back in the register array.
// Returns (updatedRegs, nextPC, error). updatedRegs is non-nil only if regs were
// reallocated. nextPC is the bytecode PC to resume at (usually ExitPC+1, but
// comparison ops may skip an instruction, returning ExitPC+2).
func (e *Engine) handleCallExit(proto *vm.FuncProto, regs []rt.Value, base int, ctx *JITContext) ([]rt.Value, int, error) {
	pc := int(ctx.ExitPC)
	if pc < 0 || pc >= len(proto.Code) {
		return nil, 0, fmt.Errorf("jit: call-exit PC %d out of range", pc)
	}

	inst := proto.Code[pc]
	op := vm.DecodeOp(inst)

	// OP_CALL gets special treatment: try the cross-call fast path first,
	// then fall back to the shared helper for the slow path.
	if op == vm.OP_CALL {
		return e.handleCallExitCall(proto, regs, base, ctx, pc, inst)
	}

	// All other opcodes: delegate to the shared helper.
	res, err := ExecuteCallExitOp(proto.Code, proto.Constants, regs, base, pc, int(ctx.Top), nil, e.globalsAcc)
	if err != nil {
		return nil, 0, err
	}
	if res.NewTop >= 0 {
		ctx.Top = int64(res.NewTop)
	}
	return nil, res.NextPC, nil
}

// handleCallExitCall handles OP_CALL within a method-JIT call-exit.
// It tries the cross-call fast path first, then falls back to the shared slow path.
func (e *Engine) handleCallExitCall(proto *vm.FuncProto, regs []rt.Value, base int, ctx *JITContext, pc int, inst uint32) ([]rt.Value, int, error) {
	if e.callHandler == nil {
		return nil, 0, fmt.Errorf("jit: no call handler for OP_CALL")
	}
	nextPC := pc + 1

	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	// Resolve nArgs: B=0 means variable args (from previous call's return).
	// Use ctx.Top to compute the actual count.
	nArgs := b - 1
	if b == 0 {
		top := int(ctx.Top)
		if top > 0 {
			nArgs = top - (a + 1)
			if nArgs < 0 {
				nArgs = 0
			}
		} else {
			// Top not set — fall back to slow path.
			return nil, 0, fmt.Errorf("jit: variable args (B=0) without Top")
		}
	}

	// Resolve nResults: C=0 means variable returns.
	nResults := c - 1
	variableResults := c == 0

	fnVal := regs[base+a]

	// Fast path: if the callee is a compiled VM closure, run it directly
	// via JIT instead of going through the full VM call handler.
	// This eliminates frame push/pop, args allocation, and VM dispatch overhead.
	if fnVal.IsFunction() {
		if vcl, _ := fnVal.Ptr().(*vm.Closure); vcl != nil {
			calleeEntry := e.lookupCompiledEntry(vcl.Proto)
			if calleeEntry != nil && !calleeEntry.demoted {
				// For variable results (C=0), request 1 result (most common for mutual recursion).
				calleeNResults := nResults
				if variableResults {
					calleeNResults = 1
				}
				_, err := e.executeCompiledCallee(vcl.Proto, calleeEntry, regs, base, a, nArgs, calleeNResults)
				if err == nil {
					// Update Top for subsequent B=0 calls.
					// Top = a + retCount: result[0] at R(a), so first unused = R(a+retCount).
					if variableResults {
						ctx.Top = int64(a + calleeNResults)
					}
					// Check if regs were reallocated during nested call.
					var newRegs []rt.Value
					if e.globalsAcc != nil {
						latestRegs := e.globalsAcc.Regs()
						if &latestRegs[0] != &regs[0] {
							newRegs = latestRegs
						}
					}
					return newRegs, nextPC, nil
				}
				if debugCrossCall {
					fmt.Printf("[cross-call] fast path FAILED for %s (a=%d nArgs=%d nResults=%d): %v\n",
						vcl.Proto.Name, a, nArgs, calleeNResults, err)
				}
				// Fast path failed — fall through to slow path.
			} else if debugCrossCall {
				fmt.Printf("[cross-call] no compiled entry for %s\n", vcl.Proto.Name)
			}
		}
	}

	// Slow path: delegate to the shared helper using e.callHandler.
	res, err := ExecuteCallExitOp(proto.Code, proto.Constants, regs, base, pc, int(ctx.Top), e.callHandler, e.globalsAcc)
	if err != nil {
		return nil, 0, err
	}

	// Check if regs were reallocated during the call.
	var newRegs []rt.Value
	if e.globalsAcc != nil {
		latestRegs := e.globalsAcc.Regs()
		if &latestRegs[0] != &regs[0] {
			newRegs = latestRegs
		}
	}

	if res.NewTop >= 0 {
		ctx.Top = int64(res.NewTop)
	}

	return newRegs, res.NextPC, nil
}

// resolveRK resolves an RK index to a value (register or constant).
func resolveRK(idx int, regs []rt.Value, base int, constants []rt.Value) rt.Value {
	if idx >= vm.RKBit {
		return constants[idx-vm.RKBit]
	}
	return regs[base+idx]
}

// maxCrossCallDepth limits recursion depth for cross-function JIT calls.
// Beyond this depth, we fall back to the VM call handler to avoid stack overflow.
const maxCrossCallDepth = 500

// executeCompiledCallee runs a compiled callee function directly via JIT,
// bypassing the VM call handler. This eliminates frame push/pop, args allocation,
// and VM dispatch overhead for mutual recursion and other cross-function patterns.
//
// The callee's register window starts at regs[base+callReg+1], where callReg is
// the CALL instruction's A field. Arguments are already in place from the caller.
// Returns error if the fast path cannot handle this call (fall through to slow path).
func (e *Engine) executeCompiledCallee(
	calleeProto *vm.FuncProto,
	calleeEntry *compiledEntry,
	regs []rt.Value,
	base int,
	callReg int,
	nArgs int,
	nResults int,
) ([]rt.Value, error) {
	return e.executeCompiledCalleeDepth(calleeProto, calleeEntry, regs, base, callReg, nArgs, nResults, 0)
}

func (e *Engine) executeCompiledCalleeDepth(
	calleeProto *vm.FuncProto,
	calleeEntry *compiledEntry,
	regs []rt.Value,
	base int,
	callReg int,
	nArgs int,
	nResults int,
	depth int,
) ([]rt.Value, error) {
	if depth >= maxCrossCallDepth {
		return nil, fmt.Errorf("jit: cross-call depth exceeded")
	}

	// Callee's register window: R(0) = regs[calleeBase]
	calleeBase := base + callReg + 1

	// Ensure register space for the callee.
	needed := calleeBase + calleeProto.MaxStack + 1
	if needed > len(regs) {
		// Regs need to grow — fall back to slow path (VM handles reallocation).
		return nil, fmt.Errorf("jit: callee needs register growth")
	}

	// Nil-fill parameters beyond actual args (matches VM behavior).
	for i := nArgs; i < calleeProto.NumParams; i++ {
		regs[calleeBase+i] = rt.NilValue()
	}

	// Set up JIT context for the callee.
	ctx := JITContext{
		Regs: uintptr(unsafe.Pointer(&regs[calleeBase])),
	}
	if len(calleeProto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&calleeProto.Constants[0]))
	}

	ctxPtr := uintptr(unsafe.Pointer(&ctx))

	for {
		exitCode := callJIT(calleeEntry.ptr, ctxPtr)
		runtime.KeepAlive(ctx)

		switch exitCode {
		case 0:
			// Normal return. Place results in caller's register window.
			retBase := int(ctx.RetBase)
			retCount := int(ctx.RetCount)
			for i := 0; i < nResults; i++ {
				if i < retCount {
					regs[base+callReg+i] = regs[calleeBase+retBase+i]
				} else {
					regs[base+callReg+i] = rt.NilValue()
				}
			}
			return nil, nil

		case 1:
			// Side exit — can't handle in fast path.
			return nil, fmt.Errorf("jit: callee side-exited")

		case 2:
			// Call-exit in the callee. Handle it, then re-enter.
			calleePC := int(ctx.ExitPC)
			if calleePC < 0 || calleePC >= len(calleeProto.Code) {
				return nil, fmt.Errorf("jit: callee call-exit PC out of range")
			}
			calleeOp := vm.DecodeOp(calleeProto.Code[calleePC])
			nextCalleePC := calleePC + 1

			if calleeOp == vm.OP_CALL {
				// OP_CALL: try cross-call fast path, then shared slow path.
				nextCalleePC = e.handleCalleeCallOpInDepth(
					calleeProto, regs, calleeBase, &ctx, calleePC, depth, &regs)
				if nextCalleePC < 0 {
					return nil, fmt.Errorf("jit: callee call-exit error")
				}
			} else {
				// All other opcodes: delegate to the shared helper.
				res, err := ExecuteCallExitOp(
					calleeProto.Code, calleeProto.Constants, regs, calleeBase, calleePC,
					int(ctx.Top), nil, e.globalsAcc,
				)
				if err != nil {
					return nil, err
				}
				nextCalleePC = res.NextPC
				if res.NewTop >= 0 {
					ctx.Top = int64(res.NewTop)
				}
			}

			// Batch consecutive call-exit opcodes (same logic as TryExecute).
			lastExitOp := calleeOp
			for nextCalleePC < len(calleeProto.Code) {
				nextOp := vm.DecodeOp(calleeProto.Code[nextCalleePC])
				if !isCallExitOp(nextOp) {
					break
				}
				// Don't batch comparison ops (they have special resume dispatch)
				if nextOp == vm.OP_EQ || nextOp == vm.OP_LT || nextOp == vm.OP_LE {
					break
				}
				// Don't batch OP_CALL (may change register file pointer)
				if nextOp == vm.OP_CALL {
					break
				}
				res, err := ExecuteCallExitOp(
					calleeProto.Code, calleeProto.Constants, regs, calleeBase, nextCalleePC,
					int(ctx.Top), nil, e.globalsAcc,
				)
				if err != nil {
					break
				}
				if res.NewTop >= 0 {
					ctx.Top = int64(res.NewTop)
				}
				lastExitOp = nextOp
				nextCalleePC = res.NextPC
			}

			// Set resume PC.
			if lastExitOp == vm.OP_EQ || lastExitOp == vm.OP_LT || lastExitOp == vm.OP_LE {
				ctx.ResumePC = int64(nextCalleePC | 0x8000)
			} else {
				ctx.ResumePC = int64(nextCalleePC)
			}
			ctx.Regs = uintptr(unsafe.Pointer(&regs[calleeBase]))
			ctxPtr = uintptr(unsafe.Pointer(&ctx))
			continue

		default:
			return nil, fmt.Errorf("jit: callee unknown exit code %d", exitCode)
		}
	}
}

// handleCalleeCallOpInDepth handles OP_CALL within executeCompiledCalleeDepth.
// Tries the cross-call fast path, then falls back to the shared slow path.
// Updates *regsPtr if register reallocation is detected.
// Returns the next PC, or -1 on error.
func (e *Engine) handleCalleeCallOpInDepth(
	calleeProto *vm.FuncProto,
	regs []rt.Value,
	calleeBase int,
	ctx *JITContext,
	calleePC int,
	depth int,
	regsPtr *[]rt.Value,
) int {
	calleeInst := calleeProto.Code[calleePC]
	ca := vm.DecodeA(calleeInst)
	cb := vm.DecodeB(calleeInst)
	cc := vm.DecodeC(calleeInst)

	// Resolve nArgs for B=0 (variable args from previous call's return).
	cnArgs := cb - 1
	if cb == 0 {
		top := int(ctx.Top)
		if top > 0 {
			cnArgs = top - (ca + 1)
			if cnArgs < 0 {
				cnArgs = 0
			}
		} else {
			return -1
		}
	}

	cnResults := cc - 1
	nestedVariableResults := cc == 0
	nestedFnVal := regs[calleeBase+ca]

	// Try fast path for nested compiled callee.
	if nestedFnVal.IsFunction() {
		if vcl, _ := nestedFnVal.Ptr().(*vm.Closure); vcl != nil {
			nestedEntry := e.lookupCompiledEntry(vcl.Proto)
			if nestedEntry != nil && !nestedEntry.demoted {
				effectiveNResults := cnResults
				if nestedVariableResults {
					effectiveNResults = 1
				}
				_, err := e.executeCompiledCalleeDepth(
					vcl.Proto, nestedEntry, regs, calleeBase, ca, cnArgs, effectiveNResults, depth+1)
				if err == nil {
					if nestedVariableResults {
						ctx.Top = int64(ca + effectiveNResults)
					}
					// Check for reg reallocation.
					if e.globalsAcc != nil {
						latestRegs := e.globalsAcc.Regs()
						if &latestRegs[0] != &regs[0] {
							*regsPtr = latestRegs
						}
					}
					return calleePC + 1
				}
			}
		}
	}

	// Slow path: use the shared helper with e.callHandler.
	res, err := ExecuteCallExitOp(
		calleeProto.Code, calleeProto.Constants, regs, calleeBase, calleePC,
		int(ctx.Top), e.callHandler, e.globalsAcc,
	)
	if err != nil {
		return -1
	}
	// Check for reg reallocation after slow-path call.
	if e.globalsAcc != nil {
		latestRegs := e.globalsAcc.Regs()
		if &latestRegs[0] != &regs[0] {
			*regsPtr = latestRegs
		}
	}
	if res.NewTop >= 0 {
		ctx.Top = int64(res.NewTop)
	}
	return res.NextPC
}

// lookupCompiledEntry returns the compiled entry for a proto, using the cached
// JITEntry field first and falling back to the entries map.
// Returns nil if not compiled. Does NOT check demoted status (caller must check).
func (e *Engine) lookupCompiledEntry(proto *vm.FuncProto) *compiledEntry {
	if proto.JITEntry != nil {
		return (*compiledEntry)(proto.JITEntry)
	}
	entry, ok := e.entries[proto]
	if ok {
		proto.JITEntry = unsafe.Pointer(entry)
	}
	return entry
}
