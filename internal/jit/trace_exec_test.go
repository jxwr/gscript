package jit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestTraceExec_MinimalLoop tests the most basic compiled trace: a for loop
// with just integer addition. This validates the ARM64 codegen infrastructure.
func TestTraceExec_BareReturn(t *testing.T) {
	// Absolute minimal: just prologue + epilogue. No trace body.
	asm := NewAssembler()
	asm.STPpre(X29, X30, SP, -96)
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)
	// Just return immediately
	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	asm.LDPpost(X29, X30, SP, 96)
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatal(err)
	}
	block.WriteCode(code)

	var dummy uint64
	callJIT(uintptr(block.Ptr()), uintptr(unsafe.Pointer(&dummy)))
	t.Log("bare return succeeded")
}

func TestTraceExec_NoopTrace(t *testing.T) {
	// Use JITContext (same as method JIT, known working) to test trace code.
	asm := NewAssembler()

	// Prologue (identical to method JIT)
	asm.STPpre(X29, X30, SP, -96)
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)

	// Test: use X19 instead of X28 for ctx pointer
	asm.MOVreg(X19, X0)     // X19 = ctx pointer (callee-saved, restored in epilogue)
	asm.LoadImm64(X0, 42)
	asm.STR(X0, X19, 16)    // ctx.ExitPC = 42
	asm.MOVreg(X0, XZR)     // return 0
	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	asm.LDPpost(X29, X30, SP, 96)
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatal(err)
	}
	block.WriteCode(code)

	regs := make([]runtime.Value, 10)
	var ctx JITContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))

	exitCode := callJIT(uintptr(block.Ptr()), uintptr(unsafe.Pointer(&ctx)))

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if ctx.ExitPC != 42 {
		t.Errorf("ExitPC = %d, want 42", ctx.ExitPC)
	}
}

func TestTraceExec_MinimalLoop(t *testing.T) {
	// Manually create a simple trace: FORLOOP + ADD
	// Simulates: for i := 1; i <= 5; i++ { sum = sum + i }
	//
	// Register layout (matching a compiled for-loop):
	// R0 = idx (FORLOOP control)
	// R1 = limit
	// R2 = step
	// R3 = i (loop variable, copy of idx)
	// R4 = sum

	proto := &vm.FuncProto{
		Constants: []runtime.Value{},
	}

	trace := &Trace{
		LoopProto: proto,
		LoopPC:    0,
		EntryPC:   0,
		IR: []TraceIR{
			// ADD R4, R4, R3  (sum += i)
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			// FORLOOP R0, sbx=-2  (idx += step; if idx <= limit, loop)
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct, err := compileTrace(trace)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// Set up registers
	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx (will be incremented to 1 first by FORLOOP)
	regs[1] = runtime.IntValue(5)  // limit
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i (loop var, updated by FORLOOP)
	regs[4] = runtime.IntValue(0)  // sum

	// Execute
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	callJIT(uintptr(ct.code.Ptr()), uintptr(unsafe.Pointer(&ctx)))

	// After loop: sum should be 1+2+3+4+5 = 15
	sum := regs[4].Int()
	if sum != 15 {
		t.Errorf("sum = %d, want 15 (exitCode=%d, exitPC=%d)", sum, ctx.ExitCode, ctx.ExitPC)
	}
}
