package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TraceContext bridges compiled trace code and Go.
type TraceContext struct {
	Regs      uintptr // input: pointer to vm.regs[base]
	Constants uintptr // input: pointer to proto.Constants[0]
	ExitPC    int64   // output: bytecode PC where trace exited
	ExitCode  int64   // output: 0=loop done, 1=side exit
}

// CompiledTrace holds native code for a trace.
type CompiledTrace struct {
	code  *CodeBlock
	proto *vm.FuncProto
}

// compileTrace compiles a Trace to native ARM64 code.
func compileTrace(trace *Trace) (*CompiledTrace, error) {
	asm := NewAssembler()

	// === Prologue: save callee-saved registers ===
	asm.STPpre(X29, X30, SP, -96)
	asm.STP(X19, X20, SP, 16)
	asm.STP(X21, X22, SP, 32)
	asm.STP(X23, X24, SP, 48)
	asm.STP(X25, X26, SP, 64)
	asm.STP(X27, X28, SP, 80)

	// Load context pointers (use X19 for ctx to avoid X28 conflict with Go runtime)
	trCtx := X19
	asm.MOVreg(trCtx, X0)                // X19 = ctx
	asm.LDR(regRegs, trCtx, 0)           // X26 = ctx.Regs
	asm.LDR(regConsts, trCtx, 8)         // X27 = ctx.Constants

	// === Trace loop ===
	asm.Label("trace_loop")

	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_MOVE:
			emitTrMove(asm, &ir)
		case vm.OP_LOADINT:
			emitTrLoadInt(asm, &ir)
		case vm.OP_LOADK:
			emitTrLoadK(asm, &ir)
		case vm.OP_LOADNIL:
			emitTrLoadNil(asm, &ir)
		case vm.OP_LOADBOOL:
			emitTrLoadBool(asm, &ir)
		case vm.OP_ADD:
			emitTrArithInt(asm, &ir, "ADD")
		case vm.OP_SUB:
			emitTrArithInt(asm, &ir, "SUB")
		case vm.OP_MUL:
			emitTrArithInt(asm, &ir, "MUL")
		case vm.OP_FORPREP:
			emitTrForPrep(asm, &ir)
		case vm.OP_FORLOOP:
			emitTrForLoop(asm, &ir)
		case vm.OP_EQ:
			emitTrEQ(asm, &ir)
		case vm.OP_LT:
			emitTrLT(asm, &ir)
		case vm.OP_LE:
			emitTrLE(asm, &ir)
		case vm.OP_TEST:
			emitTrTest(asm, &ir)
		case vm.OP_JMP:
			// JMP in trace: no-op (trace is linear, branches are guards)
		case vm.OP_NOT:
			emitTrNot(asm, &ir)
		case vm.OP_MOD:
			emitTrSideExit(asm, &ir)
		default:
			// Everything else: side exit to interpreter
			emitTrSideExit(asm, &ir)
		}
	}

	// End of trace body → loop back
	asm.B("trace_loop")

	// === Side exit handler ===
	// X9 holds the ExitPC (set by guard before branching here)
	// X19 = trace context pointer
	asm.Label("side_exit")
	asm.STR(X9, X19, 16)            // ctx.ExitPC = X9
	asm.LoadImm64(X0, 1)            // ExitCode = 1 (side exit)
	asm.B("epilogue")

	// === Loop done (FORLOOP condition false) ===
	asm.Label("loop_done")
	asm.LoadImm64(X0, 0)            // ExitCode = 0

	// === Epilogue ===
	asm.Label("epilogue")
	asm.STR(X0, X19, 24)            // ctx.ExitCode

	asm.LDP(X25, X26, SP, 64)
	asm.LDP(X23, X24, SP, 48)
	asm.LDP(X21, X22, SP, 32)
	asm.LDP(X19, X20, SP, 16)
	asm.LDPpost(X29, X30, SP, 96)
	asm.RET()

	// Finalize
	code, err := asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("trace compile: %w", err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		return nil, fmt.Errorf("trace alloc: %w", err)
	}
	if err := block.WriteCode(code); err != nil {
		return nil, fmt.Errorf("trace write: %w", err)
	}

	return &CompiledTrace{code: block, proto: trace.LoopProto}, nil
}

// --- Instruction emitters ---

func emitTrMove(asm *Assembler, ir *TraceIR) {
	src := ir.B * ValueSize
	dst := ir.A * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, regRegs, src+w*8)
		asm.STR(X0, regRegs, dst+w*8)
	}
}

func emitTrLoadInt(asm *Assembler, ir *TraceIR) {
	dst := ir.A * ValueSize
	asm.MOVimm16(X0, uint16(runtime.TypeInt))
	asm.STRB(X0, regRegs, dst)
	asm.LoadImm64(X0, int64(ir.SBX))
	asm.STR(X0, regRegs, dst+OffsetData)
}

func emitTrLoadK(asm *Assembler, ir *TraceIR) {
	src := ir.BX * ValueSize
	dst := ir.A * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, regConsts, src+w*8)
		asm.STR(X0, regRegs, dst+w*8)
	}
}

func emitTrLoadNil(asm *Assembler, ir *TraceIR) {
	for i := ir.A; i <= ir.A+ir.B; i++ {
		off := i * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			asm.STR(XZR, regRegs, off+w*8)
		}
	}
}

func emitTrLoadBool(asm *Assembler, ir *TraceIR) {
	dst := ir.A * ValueSize
	asm.MOVimm16(X0, uint16(runtime.TypeBool))
	asm.STRB(X0, regRegs, dst)
	if ir.B != 0 {
		asm.LoadImm64(X0, 1)
	} else {
		asm.MOVreg(X0, XZR)
	}
	asm.STR(X0, regRegs, dst+OffsetData)
}

// emitTrArithInt emits integer ADD/SUB/MUL with type guard.
// Guard failure → side exit at ir.PC.
func emitTrArithInt(asm *Assembler, ir *TraceIR, op string) {
	// Prepare side-exit PC in X9 (used by side_exit handler)
	asm.LoadImm64(X9, int64(ir.PC))

	// Type guard B operand
	bOff, bBase := trRKBase(ir.B)
	asm.LDRB(X0, bBase, bOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	// Type guard C operand
	cOff, cBase := trRKBase(ir.C)
	asm.LDRB(X0, cBase, cOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	// Load values
	asm.LDR(X1, bBase, bOff+OffsetData)
	asm.LDR(X2, cBase, cOff+OffsetData)

	// Compute
	switch op {
	case "ADD":
		asm.ADDreg(X0, X1, X2)
	case "SUB":
		asm.SUBreg(X0, X1, X2)
	case "MUL":
		asm.MUL(X0, X1, X2)
	}

	// Store result: typ=TypeInt, data=result
	dst := ir.A * ValueSize
	asm.STR(X0, regRegs, dst+OffsetData)
	asm.MOVimm16(X0, TypeInt)
	asm.STRB(X0, regRegs, dst)
}

func emitTrForPrep(asm *Assembler, ir *TraceIR) {
	aOff := ir.A * ValueSize
	stepOff := (ir.A + 2) * ValueSize
	asm.LDR(X0, regRegs, aOff+OffsetData)
	asm.LDR(X1, regRegs, stepOff+OffsetData)
	asm.SUBreg(X0, X0, X1)
	asm.STR(X0, regRegs, aOff+OffsetData)
}

func emitTrForLoop(asm *Assembler, ir *TraceIR) {
	aOff := ir.A * ValueSize

	// idx += step
	asm.LDR(X0, regRegs, aOff+OffsetData)
	asm.LDR(X1, regRegs, (ir.A+2)*ValueSize+OffsetData)
	asm.ADDreg(X0, X0, X1) // idx + step
	asm.STR(X0, regRegs, aOff+OffsetData)

	// R(A+3) = idx (expose as loop variable)
	asm.STR(X0, regRegs, (ir.A+3)*ValueSize+OffsetData)
	asm.MOVimm16(X2, TypeInt)
	asm.STRB(X2, regRegs, (ir.A+3)*ValueSize)

	// Compare: idx <= limit (for step > 0)
	asm.LDR(X1, regRegs, (ir.A+1)*ValueSize+OffsetData)
	asm.CMPreg(X0, X1)
	asm.BCond(CondGT, "loop_done")
}

func emitTrEQ(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))

	bOff, bBase := trRKBase(ir.B)
	cOff, cBase := trRKBase(ir.C)

	// Type guard: both int
	asm.LDRB(X0, bBase, bOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")
	asm.LDRB(X0, cBase, cOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	// Compare values
	asm.LDR(X1, bBase, bOff+OffsetData)
	asm.LDR(X2, cBase, cOff+OffsetData)
	asm.CMPreg(X1, X2)

	// A field: 0 = skip if equal, 1 = skip if not equal
	if ir.A != 0 {
		asm.BCond(CondNE, "side_exit") // expected equal, but not → exit
	} else {
		asm.BCond(CondEQ, "side_exit") // expected not equal, but equal → exit
	}
}

func emitTrLT(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))

	bOff, bBase := trRKBase(ir.B)
	cOff, cBase := trRKBase(ir.C)

	asm.LDRB(X0, bBase, bOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")
	asm.LDRB(X0, cBase, cOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	asm.LDR(X1, bBase, bOff+OffsetData)
	asm.LDR(X2, cBase, cOff+OffsetData)
	asm.CMPreg(X1, X2)

	if ir.A != 0 {
		asm.BCond(CondGE, "side_exit") // expected LT, but >= → exit
	} else {
		asm.BCond(CondLT, "side_exit") // expected >=, but LT → exit
	}
}

func emitTrLE(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))

	bOff, bBase := trRKBase(ir.B)
	cOff, cBase := trRKBase(ir.C)

	asm.LDRB(X0, bBase, bOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")
	asm.LDRB(X0, cBase, cOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	asm.LDR(X1, bBase, bOff+OffsetData)
	asm.LDR(X2, cBase, cOff+OffsetData)
	asm.CMPreg(X1, X2)

	if ir.A != 0 {
		asm.BCond(CondGT, "side_exit") // expected LE, but GT → exit
	} else {
		asm.BCond(CondLE, "side_exit") // expected GT, but LE → exit
	}
}

func emitTrTest(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))

	aOff := ir.A * ValueSize
	asm.LDRB(X0, regRegs, aOff) // load typ

	// TypeNil (0) is falsy
	asm.CMPimmW(X0, 0)
	if ir.C != 0 {
		// C=1: skip next if truthy. Guard: value must be truthy.
		asm.BCond(CondEQ, "side_exit") // nil → not truthy → exit
	}

	// TypeBool with data=0 is falsy
	asm.CMPimmW(X0, uint16(runtime.TypeBool))
	asm.BCond(CondNE, "test_done") // not bool → truthy → OK
	asm.LDR(X1, regRegs, aOff+OffsetData)
	asm.CBZ(X1, "side_exit") // bool(false) → exit

	asm.Label("test_done")
	// Note: this label name conflicts if multiple TEST ops in trace.
	// For Phase B this is acceptable; Phase C will use unique labels.
}

func emitTrNot(asm *Assembler, ir *TraceIR) {
	bOff := ir.B * ValueSize
	dstOff := ir.A * ValueSize

	asm.LDRB(X0, regRegs, bOff) // typ
	// result = !(truthy): nil or false → true, else → false
	asm.CMPimmW(X0, 0) // TypeNil
	asm.BCond(CondEQ, "not_true")
	asm.CMPimmW(X0, uint16(runtime.TypeBool))
	asm.BCond(CondNE, "not_false") // not nil, not bool → truthy → !truthy = false
	asm.LDR(X1, regRegs, bOff+OffsetData)
	asm.CBNZ(X1, "not_false") // bool(true) → truthy → false

	asm.Label("not_true")
	asm.MOVimm16(X0, uint16(runtime.TypeBool))
	asm.STRB(X0, regRegs, dstOff)
	asm.LoadImm64(X0, 1)
	asm.STR(X0, regRegs, dstOff+OffsetData)
	asm.B("not_end")

	asm.Label("not_false")
	asm.MOVimm16(X0, uint16(runtime.TypeBool))
	asm.STRB(X0, regRegs, dstOff)
	asm.STR(XZR, regRegs, dstOff+OffsetData)

	asm.Label("not_end")
}

func emitTrSideExit(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))
	asm.B("side_exit") // side_exit handler reads X9 for ExitPC
}

// trRKBase returns offset and base register for an RK index.
func trRKBase(idx int) (int, Reg) {
	if idx >= vm.RKBit {
		return (idx - vm.RKBit) * ValueSize, regConsts
	}
	return idx * ValueSize, regRegs
}

// Execute implements vm.TraceExecutor.
func (ct *CompiledTrace) Execute(regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool) {
	return executeTrace(ct, regs, base, proto)
}

// executeTrace runs compiled trace code.
func executeTrace(ct *CompiledTrace, regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool) {
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	callJIT(uintptr(ct.code.Ptr()), ctxPtr)

	return int(ctx.ExitPC), ctx.ExitCode == 1
}
