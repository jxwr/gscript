package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TraceContext bridges compiled trace code and Go.
type TraceContext struct {
	Regs           uintptr // input: pointer to vm.regs[base]
	Constants      uintptr // input: pointer to proto.Constants[0]
	ExitPC         int64   // output: bytecode PC where trace exited
	ExitCode       int64   // output: 0=loop done, 1=side exit
	InnerCode      uintptr // input: code pointer for inner trace (sub-trace calling)
	InnerConstants uintptr // input: constants pointer for inner trace
}

// TraceContext field offsets for ARM64 codegen.
const (
	TraceCtxOffRegs           = 0
	TraceCtxOffConstants      = 8
	TraceCtxOffExitPC         = 16
	TraceCtxOffExitCode       = 24
	TraceCtxOffInnerCode      = 32
	TraceCtxOffInnerConstants = 40
)

// SideExitBlacklistThreshold is the minimum number of executions before
// blacklisting is considered. Below this count, the trace gets a warm-up
// period to accumulate both side-exits and full runs.
const SideExitBlacklistThreshold = 50

// SideExitBlacklistRatio is the minimum side-exit ratio to trigger blacklisting.
// A trace that side-exits 95%+ of the time is not doing useful work.
// Example: mandelbrot side-exits on "escape" (break) but full-runs on
// non-escaping pixels — ~60% side-exit ratio, so it stays active.
const SideExitBlacklistRatio = 0.95

// CompiledTrace holds native code for a trace.
type CompiledTrace struct {
	code      *CodeBlock
	proto     *vm.FuncProto
	loopPC    int              // PC of the FORLOOP instruction this trace was compiled for
	constants []runtime.Value // trace-level constant pool

	// Sub-trace calling: if this trace contains a CALL_INNER_TRACE,
	// innerTrace points to the compiled inner loop trace.
	innerTrace *CompiledTrace

	// ssaCompiled indicates this trace was compiled via SSA codegen
	// (as opposed to the regular trace compiler). Used by sub-trace calling:
	// only SSA-compiled inner traces are suitable for sub-trace calling
	// because they don't side-exit on GETGLOBAL/SETGLOBAL etc.
	ssaCompiled bool

	// Blacklisting: tracks whether this trace is doing useful work.
	// Counters are updated directly by RecordResult (called by VM on every execution).
	sideExitCount  int  // number of times this trace side-exited
	fullRunCount   int  // number of times this trace completed a full loop
	guardFailCount int  // number of consecutive guard failures (pre-loop type mismatch)
	blacklisted    bool // if true, interpreter should run instead
}

// compileTrace compiles a Trace to native ARM64 code.
func compileTrace(trace *Trace) (*CompiledTrace, error) {
	// Skip compilation if the trace has float arithmetic: the regular trace
	// compiler only handles TypeInt guards. Float traces would always side-exit
	// on the first instruction, adding overhead and risking stale register writes.
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
			if ir.BType == runtime.TypeFloat || ir.CType == runtime.TypeFloat {
				return nil, fmt.Errorf("trace compile: float arithmetic not supported")
			}
		}
	}

	asm := NewAssembler()

	// Optimize trace IR before compilation
	trace = OptimizeTrace(trace)

	// Register allocation
	ra := NewRegAlloc(trace)

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

	// Initialize self-call depth counter
	if trace.HasSelfCalls {
		asm.MOVreg(X25, XZR) // X25 = 0 (outermost call)
	}

	// Load allocated VM registers into ARM64 registers (NaN-boxed → unbox int)
	for vmReg, armReg := range ra.Mapping {
		off := vmReg * ValueSize
		if off <= 32760 {
			asm.LDR(armReg, regRegs, off)
			EmitUnboxInt(asm, armReg, armReg)
		}
	}

	// === Trace loop / self-call entry ===
	asm.Label("trace_loop")

	for i, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_MOVE:
			emitTrMove(asm, &ir)
			// If dst is allocated, update the ARM64 register from memory.
			// emitTrMove copies the full Value via memory but does NOT update
			// the allocated register, causing stale reads on the next iteration.
			// (e.g., quicksort's "i = i+1" via MOVE R4 R10 needs R4's register updated)
			if armReg, ok := ra.Get(ir.A); ok {
				asm.LDR(armReg, regRegs, ir.A*ValueSize)
				EmitUnboxInt(asm, armReg, armReg)
			}
		case vm.OP_LOADINT:
			emitTrLoadInt(asm, &ir)
			// If dst is allocated, update the ARM64 register too
			if armReg, ok := ra.Get(ir.A); ok {
				asm.LDR(armReg, regRegs, ir.A*ValueSize)
				EmitUnboxInt(asm, armReg, armReg)
			}
		case vm.OP_LOADK:
			emitTrLoadK(asm, &ir)
			if armReg, ok := ra.Get(ir.A); ok {
				asm.LDR(armReg, regRegs, ir.A*ValueSize)
				EmitUnboxInt(asm, armReg, armReg)
			}
		case vm.OP_LOADNIL:
			emitTrLoadNil(asm, &ir)
			if armReg, ok := ra.Get(ir.A); ok {
				asm.MOVreg(armReg, XZR)
			}
		case vm.OP_LOADBOOL:
			emitTrLoadBool(asm, &ir)
			if armReg, ok := ra.Get(ir.A); ok {
				asm.LDR(armReg, regRegs, ir.A*ValueSize)
				EmitUnboxInt(asm, armReg, armReg)
			}
		case vm.OP_ADD:
			emitTrArithIntRA(asm, &ir, "ADD", ra)
		case vm.OP_SUB:
			emitTrArithIntRA(asm, &ir, "SUB", ra)
		case vm.OP_MUL:
			emitTrArithIntRA(asm, &ir, "MUL", ra)
		case vm.OP_FORPREP:
			emitTrForPrep(asm, &ir)
		case vm.OP_FORLOOP:
			emitTrForLoop(asm, &ir)
		case vm.OP_EQ:
			emitTrEQ(asm, &ir, i, trace)
		case vm.OP_LT:
			emitTrLT(asm, &ir, i, trace)
		case vm.OP_LE:
			emitTrLE(asm, &ir, i, trace)
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
		case vm.OP_SETFIELD:
			emitTrSetField(asm, &ir, i)
		case vm.OP_SETTABLE:
			emitTrSetTable(asm, &ir, i)
		case vm.OP_CALL:
			if ir.Intrinsic != IntrinsicNone {
				emitTrIntrinsic(asm, &ir)
			} else if ir.IsSelfCall && trace.HasSelfCalls {
				emitTrSelfCall(asm, &ir, i)
			} else {
				emitTrSideExit(asm, &ir)
			}
		case vm.OP_MOD:
			emitTrMod(asm, &ir, i)
		case vm.OP_UNM:
			emitTrUNM(asm, &ir)
		case vm.OP_LEN:
			emitTrLen(asm, &ir)
		case vm.OP_GETGLOBAL:
			emitTrGetGlobal(asm, &ir)
		case vm.OP_SETGLOBAL:
			emitTrSetGlobal(asm, &ir)
		case vm.OP_GETUPVAL:
			emitTrSideExit(asm, &ir) // TODO: implement
		case vm.OP_SETUPVAL:
			emitTrSideExit(asm, &ir) // TODO: implement
		case vm.OP_NEWTABLE:
			emitTrSideExit(asm, &ir) // table creation must go through Go
		case vm.OP_CONCAT:
			emitTrSideExit(asm, &ir) // string ops must go through Go
		case vm.OP_APPEND:
			emitTrSideExit(asm, &ir)
		case vm.OP_SETLIST:
			emitTrSideExit(asm, &ir)
		case vm.OP_CLOSURE:
			emitTrSideExit(asm, &ir)
		case vm.OP_CLOSE:
			emitTrSideExit(asm, &ir)
		case vm.OP_RETURN:
			// RETURN at depth 0 shouldn't appear in trace body
			// (FORLOOP handles the loop). Just side-exit.
			emitTrSideExit(asm, &ir)
		case vm.OP_DIV:
			emitTrDiv(asm, &ir)
		case vm.OP_POW:
			emitTrSideExit(asm, &ir) // pow needs Go math.Pow
		case vm.OP_SELF:
			emitTrSideExit(asm, &ir)
		case vm.OP_VARARG:
			emitTrSideExit(asm, &ir)
		default:
			emitTrSideExit(asm, &ir)
		}
	}

	// End of trace body → loop back
	asm.B("trace_loop")

	// Track which slots are written by arithmetic in the trace body.
	// Only these should be spilled — spilling an unmodified table slot
	// would corrupt the table pointer with stale integer data.
	writtenSlots := make(map[int]bool)
	for _, ir := range trace.IR {
		switch ir.Op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_UNM,
			vm.OP_LOADINT, vm.OP_LOADBOOL, vm.OP_LOADNIL, vm.OP_MOVE,
			vm.OP_FORLOOP:
			writtenSlots[ir.A] = true
			if ir.Op == vm.OP_FORLOOP {
				writtenSlots[ir.A+3] = true // loop variable
			}
		}
	}

	// Helper: spill only WRITTEN allocated registers back to memory (NaN-boxed)
	spillRegs := func() {
		for vmReg, armReg := range ra.Mapping {
			if !writtenSlots[vmReg] {
				continue
			}
			off := vmReg * ValueSize
			if off <= 32760 {
				EmitBoxInt(asm, X0, armReg, X1)
				asm.STR(X0, regRegs, off)
			}
		}
	}


	// === Side exit handler ===
	asm.Label("side_exit")
	if trace.HasSelfCalls {
		// If inside a self-call, unwind frames first
		asm.CBZ(X25, "side_exit_store") // depth==0: just exit
		// Unwind self-call frame
		asm.LDP(X25, regRegs, SP, 16)
		asm.LDPpost(X29, X30, SP, 32)
		asm.CBNZ(X25, "side_exit") // keep unwinding if still nested
		asm.Label("side_exit_store")
	}
	spillRegs() // spill allocated registers before exiting
	asm.STR(X9, X19, 16)            // ctx.ExitPC = X9
	asm.LoadImm64(X0, 1)            // ExitCode = 1 (side exit)
	asm.B("epilogue")

	// === Loop done (FORLOOP condition false) ===
	asm.Label("loop_done")
	if trace.HasSelfCalls {
		asm.CBNZ(X25, "self_return")
	}
	spillRegs() // spill allocated registers before exiting
	asm.LoadImm64(X0, 0)            // ExitCode = 0

	// === Self-call return (depth > 0): return to BL caller ===
	if trace.HasSelfCalls {
		asm.Label("self_return")
		// X0 should contain the return value (ival)
		// The BL caller will read X0 after the call returns
		asm.RET()
	}

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

	return &CompiledTrace{code: block, proto: trace.LoopProto, loopPC: trace.LoopPC, constants: trace.Constants}, nil
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
	asm.LoadImm64(X0, int64(ir.SBX))
	EmitBoxInt(asm, X1, X0, X2)
	asm.STR(X1, regRegs, dst)
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
	// Store NaN-boxed nil to each register
	EmitBoxNil(asm, X0)
	for i := ir.A; i <= ir.A+ir.B; i++ {
		off := i * ValueSize
		asm.STR(X0, regRegs, off)
	}
}

func emitTrLoadBool(asm *Assembler, ir *TraceIR) {
	dst := ir.A * ValueSize
	if ir.B != 0 {
		asm.LoadImm64(X0, nb_i64(NB_ValTrue))
	} else {
		asm.LoadImm64(X0, nb_i64(NB_ValFalse))
	}
	asm.STR(X0, regRegs, dst)
}

// emitTrArithIntRA emits integer arithmetic using allocated registers when available.
func emitTrArithIntRA(asm *Assembler, ir *TraceIR, op string, ra *RegAlloc) {
	// Check if all three operands (A, B, C) are in registers
	aReg, aAlloc := ra.Get(ir.A)
	bReg, bAlloc := ra.Get(ir.B)
	cReg, cAlloc := ra.Get(ir.C)

	// If both sources are allocated, use register-to-register arithmetic
	if bAlloc && cAlloc && ir.B < 256 && ir.C < 256 {
		dstReg := X0
		if aAlloc {
			dstReg = aReg
		}
		switch op {
		case "ADD":
			asm.ADDreg(dstReg, bReg, cReg)
		case "SUB":
			asm.SUBreg(dstReg, bReg, cReg)
		case "MUL":
			asm.MUL(dstReg, bReg, cReg)
		}
		// Always write back to memory as NaN-boxed IntValue
		dst := ir.A * ValueSize
		EmitBoxInt(asm, X0, dstReg, X1)
		asm.STR(X0, regRegs, dst)
		return
	}

	// Partial register allocation: load non-allocated operands from memory
	asm.LoadImm64(X9, int64(ir.PC))

	var srcB, srcC Reg
	if bAlloc && ir.B < 256 {
		srcB = bReg
	} else {
		bOff, bBase := trRKBase(ir.B)
		// NaN-box type check: load value, LSR #48, compare with int tag
		asm.LDR(X1, bBase, bOff)
		asm.LSRimm(X0, X1, 48)
		asm.MOVimm16(X3, NB_TagIntShr48)
		asm.CMPreg(X0, X3)
		asm.BCond(CondNE, "side_exit")
		EmitUnboxInt(asm, X1, X1)
		srcB = X1
	}
	if cAlloc && ir.C < 256 {
		srcC = cReg
	} else {
		cOff, cBase := trRKBase(ir.C)
		asm.LDR(X2, cBase, cOff)
		asm.LSRimm(X0, X2, 48)
		asm.MOVimm16(X3, NB_TagIntShr48)
		asm.CMPreg(X0, X3)
		asm.BCond(CondNE, "side_exit")
		EmitUnboxInt(asm, X2, X2)
		srcC = X2
	}

	dstReg := X0
	if aAlloc {
		dstReg = aReg
	}

	switch op {
	case "ADD":
		asm.ADDreg(dstReg, srcB, srcC)
	case "SUB":
		asm.SUBreg(dstReg, srcB, srcC)
	case "MUL":
		asm.MUL(dstReg, srcB, srcC)
	}

	if !aAlloc {
		// Box and store to memory
		dst := ir.A * ValueSize
		EmitBoxInt(asm, X1, X0, X2)
		asm.STR(X1, regRegs, dst)
	}
}

// emitTrArithInt emits integer ADD/SUB/MUL with type guard (non-allocated fallback).
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

	// Store result as NaN-boxed IntValue
	dst := ir.A * ValueSize
	EmitBoxInt(asm, X1, X0, X2)
	asm.STR(X1, regRegs, dst)
}

func emitTrForPrep(asm *Assembler, ir *TraceIR) {
	aOff := ir.A * ValueSize
	stepOff := (ir.A + 2) * ValueSize
	// Load and unbox idx and step
	asm.LDR(X0, regRegs, aOff)
	EmitUnboxInt(asm, X0, X0)
	asm.LDR(X1, regRegs, stepOff)
	EmitUnboxInt(asm, X1, X1)
	asm.SUBreg(X0, X0, X1)
	// Box and store back
	EmitBoxInt(asm, X2, X0, X3)
	asm.STR(X2, regRegs, aOff)
}

func emitTrForLoop(asm *Assembler, ir *TraceIR) {
	aOff := ir.A * ValueSize

	// idx += step (load NaN-boxed, unbox, compute, box, store)
	asm.LDR(X0, regRegs, aOff)
	EmitUnboxInt(asm, X0, X0)
	asm.LDR(X1, regRegs, (ir.A+2)*ValueSize)
	EmitUnboxInt(asm, X1, X1)
	asm.ADDreg(X0, X0, X1) // idx + step

	// Box and store idx
	EmitBoxInt(asm, X2, X0, X3)
	asm.STR(X2, regRegs, aOff)

	// R(A+3) = idx (expose as loop variable, also NaN-boxed)
	asm.STR(X2, regRegs, (ir.A+3)*ValueSize)

	// Compare: idx <= limit (for step > 0)
	asm.LDR(X1, regRegs, (ir.A+1)*ValueSize)
	EmitUnboxInt(asm, X1, X1)
	asm.CMPreg(X0, X1)
	asm.BCond(CondGT, "loop_done")
}

// trComparisonGuardFlag computes the effective guard flag for a comparison instruction.
// During trace recording, if the comparison caused a skip (PC++), the next instruction
// in the trace is NOT a JMP. If the comparison did NOT cause a skip, the next instruction
// IS a JMP (which was executed instead of being skipped).
// The effective flag accounts for this: when the comparison didn't skip, the guard polarity
// is inverted (1 - A) compared to when it did skip.
func trComparisonGuardFlag(ir *TraceIR, idx int, trace *Trace) int {
	flag := ir.A
	// Check if comparison did NOT skip by looking at next trace IR
	didSkip := true
	if idx+1 < len(trace.IR) && trace.IR[idx+1].Op == vm.OP_JMP {
		didSkip = false
	}
	if !didSkip {
		flag = 1 - flag
	}
	return flag
}

func emitTrEQ(asm *Assembler, ir *TraceIR, idx int, trace *Trace) {
	// OP_EQ A B C: if (RK(B) == RK(C)) != bool(A) then PC++
	//
	// Guard polarity depends on whether the comparison skipped during recording:
	//   Effective flag=0: recorded path had "equal" → exit when NOT equal
	//   Effective flag=1: recorded path had "not-equal" → exit when equal
	flag := trComparisonGuardFlag(ir, idx, trace)
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
	if flag == 0 {
		// flag=0: recorded path had "equal" → exit when not equal
		asm.BCond(CondNE, "side_exit")
	} else {
		// flag=1: recorded path had "not-equal" → exit when equal
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
	if flag == 0 {
		// flag=0: recorded path had "equal" → continue
		asm.B(doneLabel)
	} else {
		// flag=1: recorded path had "not-equal" → side exit (got equal)
		asm.B("side_exit")
	}

	// Strings are not equal
	asm.Label(notEqualLabel)
	if flag == 0 {
		// flag=0: recorded path had "equal" → side exit (got not-equal)
		asm.B("side_exit")
	} else {
		// flag=1: recorded path had "not-equal" → continue
		asm.B(doneLabel)
	}

	asm.Label(doneLabel)
}

func emitTrLT(asm *Assembler, ir *TraceIR, idx int, trace *Trace) {
	// OP_LT A B C: if (RK(B) < RK(C)) != bool(A) then PC++
	//
	// Guard polarity depends on whether the comparison skipped during recording:
	//   Effective flag=0: recorded path had LT true → exit on GE
	//   Effective flag=1: recorded path had LT false → exit on LT
	flag := trComparisonGuardFlag(ir, idx, trace)
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

	if flag == 0 {
		asm.BCond(CondGE, "side_exit") // flag=0: recorded LT true → exit on GE
	} else {
		asm.BCond(CondLT, "side_exit") // flag=1: recorded LT false → exit on LT
	}
}

func emitTrLE(asm *Assembler, ir *TraceIR, idx int, trace *Trace) {
	// OP_LE A B C: if (RK(B) <= RK(C)) != bool(A) then PC++
	//
	// Guard polarity depends on whether the comparison skipped during recording:
	//   Effective flag=0: recorded path had LE true → exit on GT
	//   Effective flag=1: recorded path had LE false → exit on LE
	flag := trComparisonGuardFlag(ir, idx, trace)
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

	if flag == 0 {
		asm.BCond(CondGT, "side_exit") // flag=0: recorded LE true → exit on GT
	} else {
		asm.BCond(CondLE, "side_exit") // flag=1: recorded LE false → exit on LE
	}
}

func emitTrTest(asm *Assembler, ir *TraceIR, idx int) {
	// OP_TEST A C: if (Truthy(R(A)) ~= bool(C)) then PC++
	//
	// During recording, the TEST caused a skip (PC++).
	//   C=0: skip when Truthy ~= false → Truthy is true → recorded path had TRUTHY value
	//   C=1: skip when Truthy ~= true  → Truthy is false → recorded path had FALSY value
	//
	// Guard ensures the same skip:
	//   C=0: value must be TRUTHY → side-exit if falsy
	//   C=1: value must be FALSY  → side-exit if truthy
	asm.LoadImm64(X9, int64(ir.PC))

	aOff := ir.A * ValueSize
	asm.LDRB(X0, regRegs, aOff) // load typ

	doneLabel := fmt.Sprintf("test_done_%d", idx)

	// TypeNil (0) is falsy
	asm.CMPimmW(X0, 0)
	if ir.C == 0 {
		// C=0: recorded path had truthy → exit if nil (falsy)
		asm.BCond(CondEQ, "side_exit")
	} else {
		// C=1: recorded path had falsy → nil is falsy → OK
		asm.BCond(CondEQ, doneLabel)
	}

	// TypeBool with data=0 is falsy; all other types are truthy
	asm.CMPimmW(X0, uint16(runtime.TypeBool))
	if ir.C == 0 {
		// C=0: non-nil, non-bool → truthy → OK
		asm.BCond(CondNE, doneLabel)
	} else {
		// C=1: non-nil, non-bool → truthy → exit
		asm.BCond(CondNE, "side_exit")
	}
	asm.LDR(X1, regRegs, aOff+OffsetData)
	if ir.C == 0 {
		// C=0: bool(false) → falsy → exit; bool(true) → truthy → OK
		asm.CBZ(X1, "side_exit")
	} else {
		// C=1: bool(true) → truthy → exit; bool(false) → falsy → OK
		asm.CBNZ(X1, "side_exit")
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
	asm.LoadImm64(X0, nb_i64(NB_ValTrue))
	asm.STR(X0, regRegs, dstOff)
	asm.B(endLabel)

	asm.Label(falseLabel)
	asm.LoadImm64(X0, nb_i64(NB_ValFalse))
	asm.STR(X0, regRegs, dstOff)

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
	EmitMulValueSize(asm, X0, X6, X5) // X0 = i * ValueSize
	asm.ADDreg(X7, X7, X0)            // X7 = &svals[i]

	// Copy Value from svals[i] to R(A)
	aOff := a * ValueSize
	for w := 0; w < ValueSize/8; w++ {
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

	// Step 5: Array bounds check: key >= 0 && key < array.len (0-indexed)
	asm.CMPimm(X2, 0) // key >= 0?
	asm.BCond(CondLT, fallbackLabel)

	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key < array.len?
	asm.BCond(CondGE, fallbackLabel)

	// Step 6: Load array[key]
	asm.LDR(X3, X0, TableOffArray) // X3 = array.ptr
	EmitMulValueSize(asm, X4, X2, X5) // X4 = key * ValueSize
	asm.ADDreg(X3, X3, X4)            // X3 = &array[key]

	aOff := a * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, X3, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	asm.B(doneLabel)

	// Fallback: side exit
	asm.Label(fallbackLabel)
	asm.B("side_exit")

	asm.Label(doneLabel)
}

// emitTrSetField compiles OP_SETFIELD R(A)[Constants[B]] = RK(C) in a trace.
// Fast path: R(A) is TypeTable, no metatable, key found in skeys.
func emitTrSetField(asm *Assembler, ir *TraceIR, idx int) {
	a := ir.A
	b := ir.B // constant index for field name
	cidx := ir.C

	asm.LoadImm64(X9, int64(ir.PC))

	fallbackLabel := fmt.Sprintf("setfield_exit_%d", idx)
	foundLabel := fmt.Sprintf("setfield_found_%d", idx)
	scanLabel := fmt.Sprintf("setfield_scan_%d", idx)
	nextLabel := fmt.Sprintf("setfield_next_%d", idx)
	cmpLabel := fmt.Sprintf("setfield_cmp_%d", idx)
	doneLabel := fmt.Sprintf("setfield_done_%d", idx)

	// Type check R(A).typ == TypeTable
	asm.LDRB(X0, regRegs, a*ValueSize)
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// Load *Table
	asm.LDR(X0, regRegs, a*ValueSize+OffsetPtrData)
	asm.CBZ(X0, fallbackLabel)

	// Check metatable == nil
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// Load skeys
	asm.LDR(X1, X0, TableOffSkeys)
	asm.LDR(X2, X0, TableOffSkeysLen)
	asm.CBZ(X2, fallbackLabel)

	asm.MOVreg(X8, X0) // save *Table

	// Load constant key string
	asm.LDR(X3, regConsts, b*ValueSize+OffsetPtrData)
	asm.LDR(X4, X3, 0) // key.ptr
	asm.LDR(X5, X3, 8) // key.len

	// Scan skeys
	asm.LoadImm64(X6, 0)
	asm.Label(scanLabel)
	asm.CMPreg(X6, X2)
	asm.BCond(CondGE, fallbackLabel)

	asm.LSLimm(X7, X6, 4)
	asm.ADDreg(X7, X1, X7)
	asm.LDR(X10, X7, 0)
	asm.LDR(X11, X7, 8)

	asm.CMPreg(X11, X5)
	asm.BCond(CondNE, nextLabel)
	asm.CMPreg(X10, X4)
	asm.BCond(CondEQ, foundLabel)

	asm.LoadImm64(X12, 0)
	asm.Label(cmpLabel)
	asm.CMPreg(X12, X5)
	asm.BCond(CondGE, foundLabel)
	asm.LDRBreg(X13, X10, X12)
	asm.LDRBreg(X14, X4, X12)
	asm.CMPreg(X13, X14)
	asm.BCond(CondNE, nextLabel)
	asm.ADDimm(X12, X12, 1)
	asm.B(cmpLabel)

	asm.Label(nextLabel)
	asm.ADDimm(X6, X6, 1)
	asm.B(scanLabel)

	// Found: write RK(C) value to svals[i]
	asm.Label(foundLabel)
	asm.LDR(X7, X8, TableOffSvals)
	EmitMulValueSize(asm, X0, X6, X5) // i * ValueSize
	asm.ADDreg(X7, X7, X0)            // &svals[i]

	// Load value from RK(C)
	valOff, valBase := trRKBase(cidx)
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, valBase, valOff+w*8)
		asm.STR(X0, X7, w*8)
	}
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	asm.B("side_exit")
	asm.Label(doneLabel)
}

// emitTrSetTable compiles OP_SETTABLE R(A)[RK(B)] = RK(C) in a trace.
// Fast path: R(A) is TypeTable, no metatable, RK(B) is TypeInt, key in array range.
func emitTrSetTable(asm *Assembler, ir *TraceIR, idx int) {
	a := ir.A
	bidx := ir.B
	cidx := ir.C

	asm.LoadImm64(X9, int64(ir.PC))

	fallbackLabel := fmt.Sprintf("settable_exit_%d", idx)
	doneLabel := fmt.Sprintf("settable_done_%d", idx)

	// Type check R(A)
	asm.LDRB(X0, regRegs, a*ValueSize)
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	asm.LDR(X0, regRegs, a*ValueSize+OffsetPtrData)
	asm.CBZ(X0, fallbackLabel)

	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// Load key, check TypeInt
	kOff, kBase := trRKBase(bidx)
	asm.LDRB(X2, kBase, kOff)
	asm.CMPimmW(X2, TypeInt)
	asm.BCond(CondNE, fallbackLabel)
	asm.LDR(X2, kBase, kOff+OffsetData)

	// Array bounds check with append fast path.
	inBoundsLabel := fmt.Sprintf("settable_inbounds_%d", idx)

	asm.CMPimm(X2, 0) // key >= 0? (0-indexed array)
	asm.BCond(CondLT, fallbackLabel)

	// Typed arrays set array=nil; only handle ArrayMixed here.
	asm.LDRB(X6, X0, TableOffArrayKind)
	asm.CBNZ(X6, fallbackLabel)

	asm.LDR(X3, X0, TableOffArray+8) // array.len
	asm.CMPreg(X2, X3)
	asm.BCond(CondLT, inBoundsLabel) // key < len: normal write
	asm.BCond(CondNE, fallbackLabel) // key > len: sparse, side-exit

	// key == len: append fast path (check capacity)
	asm.LDR(X4, X0, TableOffArray+16) // array.cap
	asm.CMPreg(X2, X4)
	asm.BCond(CondGE, fallbackLabel) // no room → side-exit (Go reallocs)

	// Append in-place: update len, set keysDirty
	asm.ADDimm(X5, X2, 1)
	asm.STR(X5, X0, TableOffArray+8)
	asm.MOVimm16(X5, 1)
	asm.STRB(X5, X0, TableOffKeysDirty)

	asm.Label(inBoundsLabel)

	// Write RK(C) to array[key]
	asm.LDR(X3, X0, TableOffArray)    // array.ptr
	EmitMulValueSize(asm, X4, X2, X5) // key * ValueSize
	asm.ADDreg(X3, X3, X4)

	valOff, valBase := trRKBase(cidx)
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, valBase, valOff+w*8)
		asm.STR(X0, X3, w*8)
	}
	asm.B(doneLabel)

	asm.Label(fallbackLabel)
	asm.B("side_exit")
	asm.Label(doneLabel)
}

// emitTrIntrinsic compiles a recognized GoFunction as inline ARM64.
// OP_CALL A B C: R(A)..R(A+C-2) = R(A)(R(A+1)..R(A+B-1))
// For intrinsics: R(A) = result, R(A+1) = arg1, R(A+2) = arg2
func emitTrIntrinsic(asm *Assembler, ir *TraceIR) {
	a := ir.A         // result register (also function register)
	arg1 := a + 1     // first argument
	arg2 := a + 2     // second argument (for binary ops)
	dstOff := a * ValueSize

	switch ir.Intrinsic {
	case IntrinsicBxor:
		// bit32.bxor(a, b) → R(A) = R(A+1) XOR R(A+2)
		asm.LDR(X0, regRegs, arg1*ValueSize)
		EmitUnboxInt(asm, X0, X0)
		asm.LDR(X1, regRegs, arg2*ValueSize)
		EmitUnboxInt(asm, X1, X1)
		asm.EORreg(X0, X0, X1)
		EmitBoxInt(asm, X1, X0, X2)
		asm.STR(X1, regRegs, dstOff)

	case IntrinsicBand:
		// bit32.band(a, b) → R(A) = R(A+1) AND R(A+2)
		asm.LDR(X0, regRegs, arg1*ValueSize)
		EmitUnboxInt(asm, X0, X0)
		asm.LDR(X1, regRegs, arg2*ValueSize)
		EmitUnboxInt(asm, X1, X1)
		asm.ANDreg(X0, X0, X1)
		EmitBoxInt(asm, X1, X0, X2)
		asm.STR(X1, regRegs, dstOff)

	case IntrinsicBor:
		// bit32.bor(a, b) → R(A) = R(A+1) OR R(A+2)
		asm.LDR(X0, regRegs, arg1*ValueSize)
		EmitUnboxInt(asm, X0, X0)
		asm.LDR(X1, regRegs, arg2*ValueSize)
		EmitUnboxInt(asm, X1, X1)
		asm.ORRreg(X0, X0, X1)
		EmitBoxInt(asm, X1, X0, X2)
		asm.STR(X1, regRegs, dstOff)

	default:
		// Unknown intrinsic — side exit
		emitTrSideExit(asm, ir)
	}
}

// emitTrSelfCall compiles a self-recursive CALL in a trace.
// Uses X25 as depth counter, BL to trace_loop for re-entry.
// Result returned in X0 (ival of return value).
func emitTrSelfCall(asm *Assembler, ir *TraceIR, idx int) {
	fnReg := ir.A // function register (trace-relative)
	nArgs := ir.B - 1
	nResults := ir.C

	overflowLabel := fmt.Sprintf("self_overflow_%d", idx)
	doneLabel := fmt.Sprintf("self_done_%d", idx)

	// Save state: frame pointer, link register, depth counter, regRegs
	asm.STPpre(X29, X30, SP, -32)
	asm.STP(X25, regRegs, SP, 16)

	// Increment depth
	asm.ADDimm(X25, X25, 1)

	// Depth limit check (max 50 recursive calls)
	asm.CMPimm(X25, 50)
	asm.BCond(CondGE, overflowLabel)

	// Advance regRegs to callee's register window.
	// Caller's R(fnReg+1) becomes callee's R(0).
	offset := (fnReg + 1) * ValueSize
	if offset <= 4095 {
		asm.ADDimm(regRegs, regRegs, uint16(offset))
	} else {
		asm.LoadImm64(X0, int64(offset))
		asm.ADDreg(regRegs, regRegs, X0)
	}

	// Copy arguments: already at R(fnReg+1)..R(fnReg+nArgs)
	// After advancing regRegs, these are at the callee's R(0)..R(nArgs-1)
	_ = nArgs // args are already in place due to register layout

	// BL to trace_loop (re-enter the trace body as the callee)
	asm.BL("trace_loop")

	// After return: X0 = result ival (set by RETURN or loop_done)
	// Restore state
	asm.LDP(X25, regRegs, SP, 16)
	asm.LDPpost(X29, X30, SP, 32)

	// Store result to R(fnReg) in caller's register window
	// Result type is TypeInt (most common for recursive numeric functions)
	EmitBoxInt(asm, X1, X0, X2)
	asm.STR(X1, regRegs, fnReg*ValueSize)

	// If multiple results expected, fill remaining with nil
	if nResults > 2 {
		for i := 1; i < nResults-1; i++ {
			off := (fnReg + i) * ValueSize
			for w := 0; w < ValueSize/8; w++ {
				asm.STR(XZR, regRegs, off+w*8)
			}
		}
	}

	asm.B(doneLabel)

	// Overflow: unwind all self-call frames and side-exit
	asm.Label(overflowLabel)
	asm.LDP(X25, regRegs, SP, 16)
	asm.LDPpost(X29, X30, SP, 32)
	// Keep unwinding if depth > 0
	asm.CBNZ(X25, overflowLabel)
	// At depth 0: side-exit to interpreter
	asm.LoadImm64(X9, int64(ir.PC))
	asm.B("side_exit")

	asm.Label(doneLabel)
}

// emitTrMod compiles OP_MOD R(A) = RK(B) % RK(C) for integers.
func emitTrMod(asm *Assembler, ir *TraceIR, idx int) {
	asm.LoadImm64(X9, int64(ir.PC))

	bOff, bBase := trRKBase(ir.B)
	cOff, cBase := trRKBase(ir.C)

	// Type guards
	asm.LDRB(X0, bBase, bOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")
	asm.LDRB(X0, cBase, cOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	// Load values
	asm.LDR(X1, bBase, bOff+OffsetData)
	asm.LDR(X2, cBase, cOff+OffsetData)

	// Check divisor != 0
	asm.CBZ(X2, "side_exit")

	// r = a % b = a - (a/b)*b
	asm.SDIV(X3, X1, X2)   // X3 = a / b
	asm.MSUB(X0, X3, X2, X1) // X0 = a - (a/b)*b = a % b

	// Lua-style: result has same sign as divisor
	// If r != 0 && (r ^ b) < 0: r += b
	doneLabel := fmt.Sprintf("mod_done_%d", idx)
	asm.CBZ(X0, doneLabel)
	asm.EORreg(X3, X0, X2)   // X3 = r ^ b
	asm.CMPreg(X3, XZR)      // signed compare with 0
	asm.BCond(CondGE, doneLabel)
	asm.ADDreg(X0, X0, X2)   // r += b

	asm.Label(doneLabel)
	dst := ir.A * ValueSize
	EmitBoxInt(asm, X1, X0, X2)
	asm.STR(X1, regRegs, dst)
}

// emitTrDiv compiles OP_DIV R(A) = RK(B) / RK(C).
// Always returns float (Lua semantics).
func emitTrDiv(asm *Assembler, ir *TraceIR) {
	// Division always returns float — side-exit for simplicity
	emitTrSideExit(asm, ir)
}

// emitTrUNM compiles OP_UNM R(A) = -R(B).
func emitTrUNM(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))

	bOff := ir.B * ValueSize
	asm.LDRB(X0, regRegs, bOff)
	asm.CMPimmW(X0, TypeInt)
	asm.BCond(CondNE, "side_exit")

	asm.LDR(X1, regRegs, bOff)
	EmitUnboxInt(asm, X1, X1)
	asm.NEG(X0, X1) // X0 = -X1

	dst := ir.A * ValueSize
	EmitBoxInt(asm, X1, X0, X2)
	asm.STR(X1, regRegs, dst)
}

// emitTrLen compiles OP_LEN R(A) = #R(B).
// Fast path: R(B) is TypeTable → read array length.
func emitTrLen(asm *Assembler, ir *TraceIR) {
	asm.LoadImm64(X9, int64(ir.PC))

	bOff := ir.B * ValueSize
	asm.LDRB(X0, regRegs, bOff)
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, "side_exit")

	// Load *Table
	asm.LDR(X0, regRegs, bOff+OffsetPtrData)
	asm.CBZ(X0, "side_exit")

	// table.array.len - 1 (array is 1-indexed, index 0 unused)
	asm.LDR(X1, X0, TableOffArray+8) // array.len
	asm.SUBimm(X1, X1, 1)            // length = len - 1

	dst := ir.A * ValueSize
	EmitBoxInt(asm, X2, X1, X3)
	asm.STR(X2, regRegs, dst)
}

// emitTrGetGlobal compiles OP_GETGLOBAL R(A) = globals[Constants[Bx]].
// In the trace, Constants[Bx] is in the trace's constant pool.
// We load the constant Value (which IS the global value captured at recording time)
// directly. This is correct because globals are read-only after init.
func emitTrGetGlobal(asm *Assembler, ir *TraceIR) {
	// GETGLOBAL is recorded with BX = constant pool index.
	// The trace constants[BX] holds the global's name string.
	// But we need the global's VALUE, not its name.
	// Since we can't do a map lookup in ARM64, side-exit for now.
	// However, if the global is in the VM's globalArray, we could
	// load it from there. For now, side-exit.
	emitTrSideExit(asm, ir)
}

// emitTrSetGlobal compiles OP_SETGLOBAL.
func emitTrSetGlobal(asm *Assembler, ir *TraceIR) {
	emitTrSideExit(asm, ir)
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
func (ct *CompiledTrace) Execute(regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool, guardFail bool) {
	return executeTrace(ct, regs, base, proto)
}

// RecordResult implements vm.TraceExecutor. Updates side-exit/full-run counters
// and blacklists the trace if the side-exit ratio exceeds the threshold.
// This is called on every trace execution, so it must be cheap — no allocations,
// no interface dispatch, just counter increments and a conditional check.
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
// before a trace is blacklisted. Guard failures mean the pre-loop type checks
// never match, so the trace always exits without doing work.
const guardFailBlacklistThreshold = 5

// executeTrace runs compiled trace code.
func executeTrace(ct *CompiledTrace, regs []runtime.Value, base int, proto *vm.FuncProto) (exitPC int, sideExit bool, guardFail bool) {
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[base]))
	// Use the trace's constant pool (includes inlined function constants)
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}
	// Set inner trace code and constants pointers if this trace calls an inner trace
	if ct.innerTrace != nil {
		ctx.InnerCode = uintptr(ct.innerTrace.code.Ptr())
		if len(ct.innerTrace.constants) > 0 {
			ctx.InnerConstants = uintptr(unsafe.Pointer(&ct.innerTrace.constants[0]))
		}
	}

	ctxPtr := uintptr(unsafe.Pointer(&ctx))
	callJIT(uintptr(ct.code.Ptr()), ctxPtr)

	switch ctx.ExitCode {
	case 2:
		// Guard fail: the trace's pre-loop type checks didn't match.
		// Track consecutive guard failures and blacklist if the trace ALWAYS
		// fails. This avoids repeated JIT calls that waste time and can cause
		// subtle register corruption from the prologue/epilogue overhead.
		ct.guardFailCount++
		if ct.guardFailCount >= guardFailBlacklistThreshold {
			ct.blacklisted = true
			if ct.proto != nil {
				ct.proto.BlacklistTracePC(ct.loopPC)
			}
		}
		return 0, false, true // guard fail — not executed
	case 1:
		ct.guardFailCount = 0 // reset on successful execution
		return int(ctx.ExitPC), true, false // side exit
	default:
		ct.guardFailCount = 0 // reset on successful execution
		return int(ctx.ExitPC), false, false // loop done
	}
}
