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
	code      *CodeBlock
	proto     *vm.FuncProto
	constants []runtime.Value // trace-level constant pool
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
	asm.LDR(regRegs, trCtx, 0)           // X26 = ctx.Regs (points to regs[startBase])
	asm.LDR(regConsts, trCtx, 8)         // X27 = ctx.Constants (trace constant pool)

	// === Trace loop ===
	asm.Label("trace_loop")

	for i, ir := range trace.IR {
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
			emitTrEQ(asm, &ir, i)
		case vm.OP_LT:
			emitTrLT(asm, &ir)
		case vm.OP_LE:
			emitTrLE(asm, &ir)
		case vm.OP_TEST:
			emitTrTest(asm, &ir, i)
		case vm.OP_JMP:
			// JMP in trace: no-op (trace is linear, branches are guards)
		case vm.OP_NOT:
			emitTrNot(asm, &ir, i)
		case vm.OP_GETFIELD:
			emitTrGetField(asm, &ir, i)
		case vm.OP_GETTABLE:
			emitTrGetTable(asm, &ir, i)
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

	return &CompiledTrace{code: block, proto: trace.LoopProto, constants: trace.Constants}, nil
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

func emitTrEQ(asm *Assembler, ir *TraceIR, idx int) {
	// OP_EQ A B C: if (RK(B) == RK(C)) != bool(A) then PC++
	//
	// During trace recording, the comparison caused a skip (PC++).
	// The skip happens when the result != bool(A):
	//   A=0: skip when equal (result != false → equal)
	//   A=1: skip when not-equal (result != true → not-equal)
	//
	// The guard ensures the same skip happens again:
	//   A=0: recorded path had "equal" → exit when NOT equal
	//   A=1: recorded path had "not-equal" → exit when equal
	asm.LoadImm64(X9, int64(ir.PC))

	bOff, bBase := trRKBase(ir.B)
	cOff, cBase := trRKBase(ir.C)

	// Check if both operands are int (common case) or both string
	asm.LDRB(X0, bBase, bOff)
	asm.LDRB(X3, cBase, cOff)

	// Try int comparison first
	intLabel := fmt.Sprintf("eq_int_%d", idx)
	strLabel := fmt.Sprintf("eq_str_%d", idx)
	doneLabel := fmt.Sprintf("eq_done_%d", idx)

	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondEQ, intLabel)
	asm.CMPimmW(X0, TypeString)
	asm.BCond(CondEQ, strLabel)
	asm.B("side_exit") // unsupported type for EQ

	// --- Integer path ---
	asm.Label(intLabel)
	asm.CMPimmW(X3, TypeInt)
	asm.BCond(CondNE, "side_exit")
	asm.LDR(X1, bBase, bOff+OffsetData)
	asm.LDR(X2, cBase, cOff+OffsetData)
	asm.CMPreg(X1, X2)
	if ir.A == 0 {
		// A=0: recorded path had "equal" → exit when not equal
		asm.BCond(CondNE, "side_exit")
	} else {
		// A=1: recorded path had "not-equal" → exit when equal
		asm.BCond(CondEQ, "side_exit")
	}
	asm.B(doneLabel)

	// --- String path ---
	asm.Label(strLabel)
	asm.CMPimmW(X3, TypeString)
	asm.BCond(CondNE, "side_exit")

	// Load string headers from Value.ptr (any interface → data pointer → Go string header)
	bPtrDataOff := bOff + OffsetPtrData
	cPtrDataOff := cOff + OffsetPtrData
	if bBase == regConsts {
		asm.LDR(X1, regConsts, bPtrDataOff)
	} else {
		asm.LDR(X1, regRegs, bPtrDataOff)
	}
	if cBase == regConsts {
		asm.LDR(X2, regConsts, cPtrDataOff)
	} else {
		asm.LDR(X2, regRegs, cPtrDataOff)
	}

	// Load B's string: ptr and len
	asm.LDR(X4, X1, 0)  // X4 = B.str.ptr
	asm.LDR(X5, X1, 8)  // X5 = B.str.len

	// Load C's string: ptr and len
	asm.LDR(X6, X2, 0)  // X6 = C.str.ptr
	asm.LDR(X7, X2, 8)  // X7 = C.str.len

	cmpLabel := fmt.Sprintf("eq_strcmp_%d", idx)
	notEqualLabel := fmt.Sprintf("eq_neq_%d", idx)
	equalLabel := fmt.Sprintf("eq_eq_%d", idx)

	// Compare lengths
	asm.CMPreg(X5, X7)
	asm.BCond(CondNE, notEqualLabel) // different lengths → not equal

	// Compare pointers (fast path for interned strings)
	asm.CMPreg(X4, X6)
	asm.BCond(CondEQ, equalLabel) // same pointer → equal

	// Byte-by-byte comparison
	asm.LoadImm64(X10, 0) // j = 0
	asm.Label(cmpLabel)
	asm.CMPreg(X10, X5) // j >= len?
	asm.BCond(CondGE, equalLabel)
	asm.LDRBreg(X11, X4, X10)  // B[j]
	asm.LDRBreg(X12, X6, X10)  // C[j]
	asm.CMPreg(X11, X12)
	asm.BCond(CondNE, notEqualLabel)
	asm.ADDimm(X10, X10, 1)
	asm.B(cmpLabel)

	// Strings are equal
	asm.Label(equalLabel)
	if ir.A == 0 {
		// A=0: recorded path had "equal" → continue
		asm.B(doneLabel)
	} else {
		// A=1: recorded path had "not-equal" → side exit (got equal)
		asm.B("side_exit")
	}

	// Strings are not equal
	asm.Label(notEqualLabel)
	if ir.A == 0 {
		// A=0: recorded path had "equal" → side exit (got not-equal)
		asm.B("side_exit")
	} else {
		// A=1: recorded path had "not-equal" → continue
		asm.B(doneLabel)
	}

	asm.Label(doneLabel)
}

func emitTrLT(asm *Assembler, ir *TraceIR) {
	// OP_LT A B C: if (RK(B) < RK(C)) != bool(A) then PC++
	//
	// During trace recording, the comparison caused a skip (PC++).
	//   A=0: skip when B < C (result != false → LT is true)
	//   A=1: skip when B >= C (result != true → LT is false)
	//
	// Guard ensures the same skip:
	//   A=0: recorded path had LT true → exit when NOT (B < C), i.e., B >= C
	//   A=1: recorded path had LT false → exit when (B < C)
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

	if ir.A == 0 {
		asm.BCond(CondGE, "side_exit") // A=0: recorded LT true → exit on GE
	} else {
		asm.BCond(CondLT, "side_exit") // A=1: recorded LT false → exit on LT
	}
}

func emitTrLE(asm *Assembler, ir *TraceIR) {
	// OP_LE A B C: if (RK(B) <= RK(C)) != bool(A) then PC++
	//
	// During trace recording, the comparison caused a skip (PC++).
	//   A=0: skip when B <= C (result != false → LE is true)
	//   A=1: skip when B > C (result != true → LE is false)
	//
	// Guard ensures the same skip:
	//   A=0: recorded path had LE true → exit when NOT (B <= C), i.e., B > C
	//   A=1: recorded path had LE false → exit when (B <= C)
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

	if ir.A == 0 {
		asm.BCond(CondGT, "side_exit") // A=0: recorded LE true → exit on GT
	} else {
		asm.BCond(CondLE, "side_exit") // A=1: recorded LE false → exit on LE
	}
}

func emitTrTest(asm *Assembler, ir *TraceIR, idx int) {
	asm.LoadImm64(X9, int64(ir.PC))

	aOff := ir.A * ValueSize
	asm.LDRB(X0, regRegs, aOff) // load typ

	doneLabel := fmt.Sprintf("test_done_%d", idx)

	// TypeNil (0) is falsy
	asm.CMPimmW(X0, 0)
	if ir.C != 0 {
		// C=1: skip next if truthy. Guard: value must be truthy.
		asm.BCond(CondEQ, "side_exit") // nil → not truthy → exit
	} else {
		// C=0: skip next if falsy. Guard: value must be falsy.
		asm.BCond(CondEQ, doneLabel) // nil → falsy → OK
	}

	// TypeBool with data=0 is falsy
	asm.CMPimmW(X0, uint16(runtime.TypeBool))
	asm.BCond(CondNE, doneLabel) // not nil, not bool → truthy → proceed based on C
	asm.LDR(X1, regRegs, aOff+OffsetData)
	if ir.C != 0 {
		asm.CBZ(X1, "side_exit") // C=1: bool(false) → not truthy → exit
	} else {
		asm.CBNZ(X1, "side_exit") // C=0: bool(true) → not falsy → exit
	}

	asm.Label(doneLabel)
}

func emitTrNot(asm *Assembler, ir *TraceIR, idx int) {
	bOff := ir.B * ValueSize
	dstOff := ir.A * ValueSize

	trueLabel := fmt.Sprintf("not_true_%d", idx)
	falseLabel := fmt.Sprintf("not_false_%d", idx)
	endLabel := fmt.Sprintf("not_end_%d", idx)

	asm.LDRB(X0, regRegs, bOff) // typ
	// result = !(truthy): nil or false → true, else → false
	asm.CMPimmW(X0, 0) // TypeNil
	asm.BCond(CondEQ, trueLabel)
	asm.CMPimmW(X0, uint16(runtime.TypeBool))
	asm.BCond(CondNE, falseLabel) // not nil, not bool → truthy → !truthy = false
	asm.LDR(X1, regRegs, bOff+OffsetData)
	asm.CBNZ(X1, falseLabel) // bool(true) → truthy → false

	asm.Label(trueLabel)
	asm.MOVimm16(X0, uint16(runtime.TypeBool))
	asm.STRB(X0, regRegs, dstOff)
	asm.LoadImm64(X0, 1)
	asm.STR(X0, regRegs, dstOff+OffsetData)
	asm.B(endLabel)

	asm.Label(falseLabel)
	asm.MOVimm16(X0, uint16(runtime.TypeBool))
	asm.STRB(X0, regRegs, dstOff)
	asm.STR(XZR, regRegs, dstOff+OffsetData)

	asm.Label(endLabel)
}

// emitTrGetField compiles OP_GETFIELD R(A) = R(B)[Constants[C]] in a trace.
// Fast path: R(B) is TypeTable, no metatable, linear scan of skeys.
// Guard failure → side exit.
func emitTrGetField(asm *Assembler, ir *TraceIR, idx int) {
	a := ir.A
	b := ir.B
	c := ir.C // constant index for the key string

	asm.LoadImm64(X9, int64(ir.PC))

	fallbackLabel := fmt.Sprintf("getfield_exit_%d", idx)
	foundLabel := fmt.Sprintf("getfield_found_%d", idx)
	scanLabel := fmt.Sprintf("getfield_scan_%d", idx)
	nextLabel := fmt.Sprintf("getfield_next_%d", idx)
	cmpLabel := fmt.Sprintf("getfield_cmp_%d", idx)
	doneLabel := fmt.Sprintf("getfield_done_%d", idx)

	// Step 1: Type check R(B).typ == TypeTable
	bTypOff := b * ValueSize
	asm.LDRB(X0, regRegs, bTypOff)
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// Step 2: Load *Table from R(B).ptr.data
	bPtrDataOff := b*ValueSize + OffsetPtrData
	asm.LDR(X0, regRegs, bPtrDataOff) // X0 = *Table
	asm.CBZ(X0, fallbackLabel)         // nil table check

	// Step 3: Check metatable == nil
	asm.LDR(X1, X0, TableOffMetatable) // X1 = table.metatable
	asm.CBNZ(X1, fallbackLabel)        // has metatable → fallback

	// Step 4: Load skeys slice (ptr, len)
	asm.LDR(X1, X0, TableOffSkeys)    // X1 = skeys base pointer
	asm.LDR(X2, X0, TableOffSkeysLen) // X2 = skeys.len
	asm.CBZ(X2, fallbackLabel)         // no skeys → fallback

	// Save table pointer for svals access
	asm.MOVreg(X8, X0) // X8 = *Table (preserved)

	// Step 5: Load constant key string
	// Constants[C].ptr.data → pointer to Go string header {ptr(8), len(8)}
	cPtrDataOff := c * ValueSize + OffsetPtrData
	asm.LDR(X3, regConsts, cPtrDataOff) // X3 = pointer to string header
	asm.LDR(X4, X3, 0)                  // X4 = key string data ptr
	asm.LDR(X5, X3, 8)                  // X5 = key string len

	// Step 6: Linear scan of skeys
	asm.LoadImm64(X6, 0) // X6 = i = 0

	asm.Label(scanLabel)
	asm.CMPreg(X6, X2) // i >= skeys.len?
	asm.BCond(CondGE, fallbackLabel)

	// Load skeys[i]: string header at X1 + i*16
	asm.LSLimm(X7, X6, 4)  // X7 = i * 16
	asm.ADDreg(X7, X1, X7) // X7 = &skeys[i]
	asm.LDR(X10, X7, 0)    // X10 = skeys[i].ptr
	asm.LDR(X11, X7, 8)    // X11 = skeys[i].len

	// Compare lengths first (fast reject)
	asm.CMPreg(X11, X5) // skeys[i].len == key.len?
	asm.BCond(CondNE, nextLabel)

	// Compare data pointers (fast accept for interned strings)
	asm.CMPreg(X10, X4) // same pointer?
	asm.BCond(CondEQ, foundLabel)

	// Byte-by-byte comparison
	asm.LoadImm64(X12, 0)  // j = 0
	asm.Label(cmpLabel)
	asm.CMPreg(X12, X5) // j >= len?
	asm.BCond(CondGE, foundLabel)
	asm.LDRBreg(X13, X10, X12) // skeys[i].ptr[j]
	asm.LDRBreg(X14, X4, X12)  // key.ptr[j]
	asm.CMPreg(X13, X14)
	asm.BCond(CondNE, nextLabel)
	asm.ADDimm(X12, X12, 1)
	asm.B(cmpLabel)

	asm.Label(nextLabel)
	asm.ADDimm(X6, X6, 1) // i++
	asm.B(scanLabel)

	// Step 7: Found - load svals[i] into R(A)
	asm.Label(foundLabel)
	asm.LDR(X7, X8, TableOffSvals) // X7 = svals base pointer
	asm.LSLimm(X0, X6, 5)          // X0 = i * 32 (ValueSize)
	asm.ADDreg(X7, X7, X0)         // X7 = &svals[i]

	// Copy Value (32 bytes = 4 words) from svals[i] to R(A)
	aOff := a * ValueSize
	for w := 0; w < 4; w++ {
		asm.LDR(X0, X7, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	asm.B(doneLabel)

	// Fallback: side exit
	asm.Label(fallbackLabel)
	asm.B("side_exit")

	asm.Label(doneLabel)
}

// emitTrGetTable compiles OP_GETTABLE R(A) = R(B)[RK(C)] in a trace.
// Fast path: R(B) is TypeTable, no metatable, RK(C) is TypeInt, key in array range.
// Guard failure → side exit.
func emitTrGetTable(asm *Assembler, ir *TraceIR, idx int) {
	a := ir.A
	b := ir.B
	cidx := ir.C

	asm.LoadImm64(X9, int64(ir.PC))

	fallbackLabel := fmt.Sprintf("gettable_exit_%d", idx)
	doneLabel := fmt.Sprintf("gettable_done_%d", idx)

	// Step 1: Type check R(B).typ == TypeTable
	bTypOff := b * ValueSize
	asm.LDRB(X0, regRegs, bTypOff)
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// Step 2: Load *Table from R(B).ptr.data
	bPtrDataOff := b*ValueSize + OffsetPtrData
	asm.LDR(X0, regRegs, bPtrDataOff) // X0 = *Table
	asm.CBZ(X0, fallbackLabel)

	// Step 3: Check metatable == nil
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// Step 4: Load key from RK(C), check TypeInt
	cOff, cBase := trRKBase(cidx)
	asm.LDRB(X2, cBase, cOff)
	asm.CMPimmW(X2, TypeInt)
	asm.BCond(CondNE, fallbackLabel)
	asm.LDR(X2, cBase, cOff+OffsetData) // X2 = key int value

	// Step 5: Array bounds check: key >= 1 && key < array.len
	asm.CMPimm(X2, 1) // key >= 1?
	asm.BCond(CondLT, fallbackLabel)

	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key < array.len?
	asm.BCond(CondGE, fallbackLabel)

	// Step 6: Load array[key]
	asm.LDR(X3, X0, TableOffArray) // X3 = array.ptr
	asm.LSLimm(X4, X2, 5)         // X4 = key * 32 (ValueSize)
	asm.ADDreg(X3, X3, X4)        // X3 = &array[key]

	aOff := a * ValueSize
	for w := 0; w < 4; w++ {
		asm.LDR(X0, X3, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	asm.B(doneLabel)

	// Fallback: side exit
	asm.Label(fallbackLabel)
	asm.B("side_exit")

	asm.Label(doneLabel)
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
	// Use the trace's constant pool (includes inlined function constants)
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}

	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	callJIT(uintptr(ct.code.Ptr()), ctxPtr)

	return int(ctx.ExitPC), ctx.ExitCode == 1
}
