//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

	"github.com/gscript/gscript/internal/vm"
)

// JITContext is the bridge between Go and JIT-compiled code.
// The JIT function reads Regs/Constants pointers and writes exit information.
type JITContext struct {
	Regs      uintptr // pointer to vm.regs[base] (first Value in register window)
	Constants uintptr // pointer to constants[0]
	ExitPC    int64   // output: bytecode PC to resume at (on side exit)
	ExitCode  int64   // output: 0 = normal return, 1 = side exit
	RetBase   int64   // output: return base register index
	RetCount  int64   // output: return value count
}

// JITContext field offsets (verified at init).
const (
	ctxOffRegs      = 0
	ctxOffConstants = 8
	ctxOffExitPC    = 16
	ctxOffExitCode  = 24
	ctxOffRetBase   = 32
	ctxOffRetCount  = 40
)

func init() {
	var ctx JITContext
	if unsafe.Offsetof(ctx.Regs) != ctxOffRegs {
		panic("jit: JITContext.Regs offset mismatch")
	}
	if unsafe.Offsetof(ctx.Constants) != ctxOffConstants {
		panic("jit: JITContext.Constants offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitPC) != ctxOffExitPC {
		panic("jit: JITContext.ExitPC offset mismatch")
	}
	if unsafe.Offsetof(ctx.ExitCode) != ctxOffExitCode {
		panic("jit: JITContext.ExitCode offset mismatch")
	}
	if unsafe.Offsetof(ctx.RetBase) != ctxOffRetBase {
		panic("jit: JITContext.RetBase offset mismatch")
	}
	if unsafe.Offsetof(ctx.RetCount) != ctxOffRetCount {
		panic("jit: JITContext.RetCount offset mismatch")
	}
}

// Reserved ARM64 registers in JIT code.
const (
	regCtx    = X28 // pointer to JITContext
	regRegs   = X26 // pointer to vm.regs[base]
	regConsts = X27 // pointer to constants[0]
)

// CompiledFunc holds the native code for a JIT-compiled function.
type CompiledFunc struct {
	Code  *CodeBlock
	Proto *vm.FuncProto
}

// Codegen translates a FuncProto's bytecode to ARM64 machine code.
type Codegen struct {
	asm   *Assembler
	proto *vm.FuncProto
}

// Compile compiles a FuncProto to native ARM64 code.
// Returns the compiled code block, or an error if compilation fails.
func Compile(proto *vm.FuncProto) (*CompiledFunc, error) {
	cg := &Codegen{
		asm:   NewAssembler(),
		proto: proto,
	}

	cg.emitPrologue()
	if err := cg.emitBody(); err != nil {
		return nil, err
	}
	cg.emitEpilogue()

	code, err := cg.asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("jit: finalize: %w", err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		return nil, fmt.Errorf("jit: alloc: %w", err)
	}

	if err := block.WriteCode(code); err != nil {
		block.Free()
		return nil, fmt.Errorf("jit: write: %w", err)
	}

	return &CompiledFunc{Code: block, Proto: proto}, nil
}

// emitPrologue saves callee-saved registers and sets up base pointers.
func (cg *Codegen) emitPrologue() {
	a := cg.asm

	// Save callee-saved registers + frame pointer + link register.
	// Stack frame: 96 bytes (12 x 8-byte registers, stored as 6 pairs).
	a.STPpre(X29, X30, SP, -96) // STP x29, x30, [sp, #-96]!
	a.MOVreg(X29, SP)           // x29 = sp
	a.STP(X19, X20, SP, 16)
	a.STP(X21, X22, SP, 32)
	a.STP(X23, X24, SP, 48)
	a.STP(X25, X26, SP, 64)
	a.STP(X27, X28, SP, 80)

	// Load context pointers.
	// x0 = *JITContext (input argument)
	a.MOVreg(regCtx, X0)             // x28 = JITContext*
	a.LDR(regRegs, regCtx, ctxOffRegs)       // x26 = regs base
	a.LDR(regConsts, regCtx, ctxOffConstants) // x27 = constants base
}

// emitEpilogue restores registers and returns.
func (cg *Codegen) emitEpilogue() {
	a := cg.asm

	a.Label("epilogue")
	// x0 already contains the exit code (set before jumping here)
	a.LDP(X19, X20, SP, 16)
	a.LDP(X21, X22, SP, 32)
	a.LDP(X23, X24, SP, 48)
	a.LDP(X25, X26, SP, 64)
	a.LDP(X27, X28, SP, 80)
	a.LDPpost(X29, X30, SP, 96)
	a.RET()
}

// emitSideExit emits a side exit that returns to the interpreter at the given bytecode PC.
func (cg *Codegen) emitSideExit(pc int) {
	a := cg.asm
	label := fmt.Sprintf("side_exit_%d", pc)
	a.Label(label)
	a.LoadImm64(X1, int64(pc))
	a.STR(X1, regCtx, ctxOffExitPC)
	a.LoadImm64(X0, 1) // exit code = 1 (side exit)
	a.B("epilogue")
}

// emitReturn emits a normal function return.
func (cg *Codegen) emitReturn(retBase, retCount int) {
	a := cg.asm
	a.LoadImm64(X1, int64(retBase))
	a.STR(X1, regCtx, ctxOffRetBase)
	a.LoadImm64(X1, int64(retCount))
	a.STR(X1, regCtx, ctxOffRetCount)
	a.LoadImm64(X0, 0) // exit code = 0 (normal return)
	a.B("epilogue")
}

// pcLabel returns the label for a bytecode PC.
func pcLabel(pc int) string {
	return fmt.Sprintf("pc_%d", pc)
}

// sideExitLabel returns the side exit label for a bytecode PC.
func sideExitLabel(pc int) string {
	return fmt.Sprintf("side_exit_%d", pc)
}

// ──────────────────────────────────────────────────────────────────────────────
// Value access helpers
// ──────────────────────────────────────────────────────────────────────────────

// regTypOffset returns the byte offset of R(i).typ from regRegs.
func regTypOffset(i int) int {
	return i*ValueSize + OffsetTyp
}

// regIvalOffset returns the byte offset of R(i).ival from regRegs.
func regIvalOffset(i int) int {
	return i*ValueSize + OffsetIval
}

// regFvalOffset returns the byte offset of R(i).fval from regRegs.
func regFvalOffset(i int) int {
	return i*ValueSize + OffsetFval
}

// loadRegTyp loads the type byte of R(reg) into the ARM64 register dst (as W-form).
func (cg *Codegen) loadRegTyp(dst Reg, reg int) {
	off := regTypOffset(reg)
	if off <= 4095 {
		cg.asm.LDRB(dst, regRegs, off)
	} else {
		cg.asm.LoadImm64(dst, int64(off))
		cg.asm.ADDreg(dst, regRegs, dst)
		cg.asm.LDRB(dst, dst, 0)
	}
}

// storeRegTyp stores a type byte into R(reg).typ.
func (cg *Codegen) storeRegTyp(src Reg, reg int) {
	off := regTypOffset(reg)
	if off <= 4095 {
		cg.asm.STRB(src, regRegs, off)
	} else {
		cg.asm.LoadImm64(X9, int64(off))
		cg.asm.ADDreg(X9, regRegs, X9)
		cg.asm.STRB(src, X9, 0)
	}
}

// loadRegIval loads R(reg).ival into the ARM64 register dst (64-bit).
func (cg *Codegen) loadRegIval(dst Reg, reg int) {
	off := regIvalOffset(reg)
	cg.asm.LDR(dst, regRegs, off)
}

// storeRegIval stores a 64-bit value into R(reg).ival.
func (cg *Codegen) storeRegIval(src Reg, reg int) {
	off := regIvalOffset(reg)
	cg.asm.STR(src, regRegs, off)
}

// loadRegFval loads R(reg).fval into the ARM64 FP register dst.
func (cg *Codegen) loadRegFval(dst FReg, reg int) {
	off := regFvalOffset(reg)
	cg.asm.FLDRd(dst, regRegs, off)
}

// storeRegFval stores a float64 into R(reg).fval.
func (cg *Codegen) storeRegFval(src FReg, reg int) {
	off := regFvalOffset(reg)
	cg.asm.FSTRd(src, regRegs, off)
}

// storeIntValue stores a complete IntValue: sets typ=TypeInt and ival=value.
func (cg *Codegen) storeIntValue(reg int, valReg Reg) {
	cg.asm.MOVimm16W(X9, TypeInt)
	cg.storeRegTyp(X9, reg)
	cg.storeRegIval(valReg, reg)
}

// storeNilValue stores NilValue (typ=0) in R(reg).
func (cg *Codegen) storeNilValue(reg int) {
	cg.asm.MOVimm16W(X9, TypeNil)
	cg.storeRegTyp(X9, reg)
}

// storeBoolValue stores a BoolValue (typ=TypeBool, ival=0 or 1).
func (cg *Codegen) storeBoolValue(reg int, valReg Reg) {
	cg.asm.MOVimm16W(X9, TypeBool)
	cg.storeRegTyp(X9, reg)
	cg.storeRegIval(valReg, reg)
}

// ──────────────────────────────────────────────────────────────────────────────
// RK value loading (register or constant)
// ──────────────────────────────────────────────────────────────────────────────

// loadRKTyp loads the type of RK(idx) into dst.
func (cg *Codegen) loadRKTyp(dst Reg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx*ValueSize + OffsetTyp
		if off <= 4095 {
			cg.asm.LDRB(dst, regConsts, off)
		} else {
			cg.asm.LoadImm64(dst, int64(off))
			cg.asm.ADDreg(dst, regConsts, dst)
			cg.asm.LDRB(dst, dst, 0)
		}
	} else {
		cg.loadRegTyp(dst, idx)
	}
}

// loadRKIval loads RK(idx).ival into dst.
func (cg *Codegen) loadRKIval(dst Reg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx*ValueSize + OffsetIval
		cg.asm.LDR(dst, regConsts, off)
	} else {
		cg.loadRegIval(dst, idx)
	}
}

// loadRKFval loads RK(idx).fval into dst.
func (cg *Codegen) loadRKFval(dst FReg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx*ValueSize + OffsetFval
		cg.asm.FLDRd(dst, regConsts, off)
	} else {
		cg.loadRegFval(dst, idx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Copy a full Value (56 bytes) between registers.
// ──────────────────────────────────────────────────────────────────────────────

// copyValue copies the full Value from src to dst using LDP/STP pairs.
// Uses X0-X7 as scratch.
func (cg *Codegen) copyValue(dstReg, srcReg int) {
	srcBase := srcReg * ValueSize
	dstBase := dstReg * ValueSize

	// 56 bytes = 7 x 8-byte words. Copy as 3 LDP/STP pairs (48 bytes) + 1 LDR/STR (8 bytes).
	a := cg.asm

	// Words 0-1 (bytes 0-15)
	a.LDP(X0, X1, regRegs, srcBase)
	a.STP(X0, X1, regRegs, dstBase)

	// Words 2-3 (bytes 16-31)
	a.LDP(X0, X1, regRegs, srcBase+16)
	a.STP(X0, X1, regRegs, dstBase+16)

	// Words 4-5 (bytes 32-47)
	a.LDP(X0, X1, regRegs, srcBase+32)
	a.STP(X0, X1, regRegs, dstBase+32)

	// Word 6 (bytes 48-55)
	a.LDR(X0, regRegs, srcBase+48)
	a.STR(X0, regRegs, dstBase+48)
}

// copyRKValue copies a full Value from RK(idx) to R(dst).
func (cg *Codegen) copyRKValue(dstReg, rkIdx int) {
	if vm.IsRK(rkIdx) {
		constIdx := vm.RKToConstIdx(rkIdx)
		srcBase := constIdx * ValueSize
		dstBase := dstReg * ValueSize
		a := cg.asm

		a.LDP(X0, X1, regConsts, srcBase)
		a.STP(X0, X1, regRegs, dstBase)
		a.LDP(X0, X1, regConsts, srcBase+16)
		a.STP(X0, X1, regRegs, dstBase+16)
		a.LDP(X0, X1, regConsts, srcBase+32)
		a.STP(X0, X1, regRegs, dstBase+32)
		a.LDR(X0, regConsts, srcBase+48)
		a.STR(X0, regRegs, dstBase+48)
	} else {
		cg.copyValue(dstReg, rkIdx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Body compilation
// ──────────────────────────────────────────────────────────────────────────────

func (cg *Codegen) emitBody() error {
	code := cg.proto.Code
	sideExits := make(map[int]bool) // PCs that need side exit labels

	// First pass: identify which PCs need side exit labels.
	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		op := vm.DecodeOp(inst)
		if !cg.isSupported(op) {
			sideExits[pc] = true
		}
	}

	// Second pass: emit code for each instruction.
	for pc := 0; pc < len(code); pc++ {
		cg.asm.Label(pcLabel(pc))
		inst := code[pc]
		op := vm.DecodeOp(inst)

		if !cg.isSupported(op) {
			// Emit a direct side exit.
			cg.asm.LoadImm64(X1, int64(pc))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
			continue
		}

		if err := cg.emitInstruction(pc, inst); err != nil {
			return fmt.Errorf("pc %d: %w", pc, err)
		}
	}

	return nil
}

// isSupported returns true if the opcode can be compiled to native code.
func (cg *Codegen) isSupported(op vm.Opcode) bool {
	switch op {
	case vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_LOADINT, vm.OP_LOADK,
		vm.OP_MOVE,
		vm.OP_ADD, vm.OP_SUB, vm.OP_MUL,
		vm.OP_UNM, vm.OP_NOT,
		vm.OP_EQ, vm.OP_LT, vm.OP_LE,
		vm.OP_JMP,
		vm.OP_FORPREP, vm.OP_FORLOOP,
		vm.OP_RETURN,
		vm.OP_TEST,
		vm.OP_GETGLOBAL, vm.OP_SETGLOBAL:
		return true
	}
	return false
}

func (cg *Codegen) emitInstruction(pc int, inst uint32) error {
	op := vm.DecodeOp(inst)

	switch op {
	case vm.OP_LOADNIL:
		return cg.emitLoadNil(inst)
	case vm.OP_LOADBOOL:
		return cg.emitLoadBool(pc, inst)
	case vm.OP_LOADINT:
		return cg.emitLoadInt(inst)
	case vm.OP_LOADK:
		return cg.emitLoadK(inst)
	case vm.OP_MOVE:
		return cg.emitMove(inst)
	case vm.OP_ADD:
		return cg.emitArithInt(pc, inst, "ADD")
	case vm.OP_SUB:
		return cg.emitArithInt(pc, inst, "SUB")
	case vm.OP_MUL:
		return cg.emitArithInt(pc, inst, "MUL")
	case vm.OP_UNM:
		return cg.emitUNM(pc, inst)
	case vm.OP_NOT:
		return cg.emitNOT(inst)
	case vm.OP_EQ:
		return cg.emitEQ(pc, inst)
	case vm.OP_LT:
		return cg.emitLT(pc, inst)
	case vm.OP_LE:
		return cg.emitLE(pc, inst)
	case vm.OP_JMP:
		return cg.emitJMP(pc, inst)
	case vm.OP_TEST:
		return cg.emitTest(pc, inst)
	case vm.OP_FORPREP:
		return cg.emitForPrep(pc, inst)
	case vm.OP_FORLOOP:
		return cg.emitForLoop(pc, inst)
	case vm.OP_RETURN:
		return cg.emitReturnOp(inst)
	case vm.OP_GETGLOBAL, vm.OP_SETGLOBAL:
		// Side exit — these need Go runtime interaction.
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
		return nil
	}
	return fmt.Errorf("unhandled opcode %s", vm.OpName(op))
}

// ──────────────────────────────────────────────────────────────────────────────
// Individual opcode emitters
// ──────────────────────────────────────────────────────────────────────────────

func (cg *Codegen) emitLoadNil(inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	for i := aReg; i <= aReg+b; i++ {
		cg.storeNilValue(i)
	}
	return nil
}

func (cg *Codegen) emitLoadBool(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	if b != 0 {
		cg.asm.LoadImm64(X0, 1)
	} else {
		cg.asm.LoadImm64(X0, 0)
	}
	cg.storeBoolValue(aReg, X0)

	if c != 0 {
		// Skip next instruction: jump to pc+2.
		cg.asm.B(pcLabel(pc + 2))
	}
	return nil
}

func (cg *Codegen) emitLoadInt(inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)
	cg.asm.LoadImm64(X0, int64(sbx))
	cg.storeIntValue(aReg, X0)
	return nil
}

func (cg *Codegen) emitLoadK(inst uint32) error {
	aReg := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)
	cg.copyRKValue(aReg, vm.ConstToRK(bx))
	return nil
}

func (cg *Codegen) emitMove(inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)
	cg.copyValue(aReg, bReg)
	return nil
}

// emitArithInt emits integer arithmetic with type guards.
// On type guard failure, side-exits to interpreter.
func (cg *Codegen) emitArithInt(pc int, inst uint32, arithOp string) error {
	aReg := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	exitLabel := fmt.Sprintf("arith_exit_%d", pc)

	// Type guard: check RK(B).typ == TypeInt
	cg.loadRKTyp(X0, bIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	// Type guard: check RK(C).typ == TypeInt
	cg.loadRKTyp(X0, cIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	// Load operands
	cg.loadRKIval(X0, bIdx)
	cg.loadRKIval(X1, cIdx)

	// Perform operation
	switch arithOp {
	case "ADD":
		cg.asm.ADDreg(X0, X0, X1)
	case "SUB":
		cg.asm.SUBreg(X0, X0, X1)
	case "MUL":
		cg.asm.MUL(X0, X0, X1)
	}

	// Store result as IntValue
	cg.storeIntValue(aReg, X0)

	// Jump past side exit
	after := fmt.Sprintf("arith_done_%d", pc)
	cg.asm.B(after)

	// Side exit for non-integer operands
	cg.asm.Label(exitLabel)
	cg.asm.LoadImm64(X1, int64(pc))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 1)
	cg.asm.B("epilogue")

	cg.asm.Label(after)
	return nil
}

func (cg *Codegen) emitUNM(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	exitLabel := fmt.Sprintf("unm_exit_%d", pc)

	// Type guard
	cg.loadRegTyp(X0, bReg)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	// Negate
	cg.loadRegIval(X0, bReg)
	cg.asm.NEG(X0, X0)
	cg.storeIntValue(aReg, X0)

	after := fmt.Sprintf("unm_done_%d", pc)
	cg.asm.B(after)

	cg.asm.Label(exitLabel)
	cg.asm.LoadImm64(X1, int64(pc))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 1)
	cg.asm.B("epilogue")

	cg.asm.Label(after)
	return nil
}

func (cg *Codegen) emitNOT(inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	// NOT: R(A) = !truthy(R(B))
	// Truthy: nil and false are falsy, everything else truthy.
	// TypeNil (0) = falsy. TypeBool (1) with ival==0 = falsy. Otherwise truthy.

	cg.loadRegTyp(X0, bReg)

	// Check if nil (typ == 0)
	cg.asm.CMPimmW(X0, TypeNil)
	cg.asm.BCond(CondEQ, fmt.Sprintf("not_true_%d", bReg))

	// Check if false bool (typ == 1 && ival == 0)
	cg.asm.CMPimmW(X0, TypeBool)
	cg.asm.BCond(CondNE, fmt.Sprintf("not_false_%d", bReg))
	cg.loadRegIval(X0, bReg)
	cg.asm.CMPimm(X0, 0)
	cg.asm.BCond(CondEQ, fmt.Sprintf("not_true_%d", bReg))

	// Truthy → NOT = false
	cg.asm.Label(fmt.Sprintf("not_false_%d", bReg))
	cg.asm.LoadImm64(X0, 0)
	cg.storeBoolValue(aReg, X0)
	cg.asm.B(fmt.Sprintf("not_done_%d", bReg))

	// Falsy → NOT = true
	cg.asm.Label(fmt.Sprintf("not_true_%d", bReg))
	cg.asm.LoadImm64(X0, 1)
	cg.storeBoolValue(aReg, X0)

	cg.asm.Label(fmt.Sprintf("not_done_%d", bReg))
	return nil
}

// emitEQ: if (RK(B) == RK(C)) != bool(A) then PC++ (skip next instruction)
func (cg *Codegen) emitEQ(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	exitLabel := fmt.Sprintf("eq_exit_%d", pc)
	skipLabel := pcLabel(pc + 2) // skip next instruction

	// Type guard: both must be int for JIT path
	cg.loadRKTyp(X0, bIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)
	cg.loadRKTyp(X0, cIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	// Compare ival
	cg.loadRKIval(X0, bIdx)
	cg.loadRKIval(X1, cIdx)
	cg.asm.CMPreg(X0, X1)

	// if (B == C) != bool(A) then skip
	if aFlag != 0 {
		// A=1: skip if NOT equal
		cg.asm.BCond(CondNE, skipLabel)
	} else {
		// A=0: skip if equal
		cg.asm.BCond(CondEQ, skipLabel)
	}
	return cg.emitComparisonSideExit(pc, exitLabel)
}

// emitLT: if (RK(B) < RK(C)) != bool(A) then PC++
func (cg *Codegen) emitLT(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	exitLabel := fmt.Sprintf("lt_exit_%d", pc)
	skipLabel := pcLabel(pc + 2)

	// Type guard
	cg.loadRKTyp(X0, bIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)
	cg.loadRKTyp(X0, cIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	cg.loadRKIval(X0, bIdx)
	cg.loadRKIval(X1, cIdx)
	cg.asm.CMPreg(X0, X1)

	// if (B < C) != bool(A) then skip
	if aFlag != 0 {
		// A=1: skip if NOT less (i.e., >=)
		cg.asm.BCond(CondGE, skipLabel)
	} else {
		// A=0: skip if less
		cg.asm.BCond(CondLT, skipLabel)
	}
	return cg.emitComparisonSideExit(pc, exitLabel)
}

// emitLE: if (RK(B) <= RK(C)) != bool(A) then PC++
// Note: the VM implements LE as !(C < B).
func (cg *Codegen) emitLE(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	exitLabel := fmt.Sprintf("le_exit_%d", pc)
	skipLabel := pcLabel(pc + 2)

	// Type guard
	cg.loadRKTyp(X0, bIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)
	cg.loadRKTyp(X0, cIdx)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	cg.loadRKIval(X0, bIdx)
	cg.loadRKIval(X1, cIdx)
	cg.asm.CMPreg(X0, X1)

	// if (B <= C) != bool(A) then skip
	if aFlag != 0 {
		// A=1: skip if NOT less-or-equal (i.e., >)
		cg.asm.BCond(CondGT, skipLabel)
	} else {
		// A=0: skip if <=
		cg.asm.BCond(CondLE, skipLabel)
	}
	return cg.emitComparisonSideExit(pc, exitLabel)
}

func (cg *Codegen) emitComparisonSideExit(pc int, exitLabel string) error {
	after := fmt.Sprintf("cmp_done_%d", pc)
	cg.asm.B(after)

	cg.asm.Label(exitLabel)
	cg.asm.LoadImm64(X1, int64(pc))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 1)
	cg.asm.B("epilogue")

	cg.asm.Label(after)
	return nil
}

func (cg *Codegen) emitJMP(pc int, inst uint32) error {
	sbx := vm.DecodesBx(inst)
	target := pc + 1 + sbx
	cg.asm.B(pcLabel(target))
	return nil
}

func (cg *Codegen) emitTest(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	c := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2) // skip next if test fails

	// Load type
	cg.loadRegTyp(X0, aReg)

	// Check nil → falsy
	cg.asm.CMPimmW(X0, TypeNil)
	if c != 0 {
		// C=1: skip if NOT truthy (i.e., falsy) → skip if nil
		cg.asm.BCond(CondEQ, skipLabel)
	} else {
		// C=0: skip if truthy → don't skip if nil
		notNil := fmt.Sprintf("test_not_nil_%d", pc)
		cg.asm.BCond(CondNE, notNil)
		// nil is falsy, C=0 means skip if truthy → no skip
		cg.asm.B(pcLabel(pc + 1))
		cg.asm.Label(notNil)
	}

	// Check bool
	cg.asm.CMPimmW(X0, TypeBool)
	notBool := fmt.Sprintf("test_truthy_%d", pc)
	cg.asm.BCond(CondNE, notBool)

	// It's a bool — check ival
	cg.loadRegIval(X0, aReg)
	cg.asm.CMPimm(X0, 0)
	if c != 0 {
		// C=1: skip if NOT truthy → skip if bool(false) (ival==0)
		cg.asm.BCond(CondEQ, skipLabel)
	} else {
		// C=0: skip if truthy → skip if bool(true) (ival!=0)
		cg.asm.BCond(CondNE, skipLabel)
	}
	cg.asm.B(pcLabel(pc + 1)) // not skipping
	// Everything else is truthy
	cg.asm.Label(notBool)
	if c == 0 {
		// C=0: skip if truthy → yes, everything non-nil non-false is truthy
		cg.asm.B(skipLabel)
	}
	// C=1: skip if NOT truthy → no, it's truthy → don't skip
	return nil
}

// emitForPrep: R(A) -= R(A+2); PC += sBx
// Integer specialization: guards that init/limit/step are all TypeInt.
func (cg *Codegen) emitForPrep(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	exitLabel := fmt.Sprintf("forprep_exit_%d", pc)

	// Type guard: R(A), R(A+1), R(A+2) must all be TypeInt
	cg.loadRegTyp(X0, aReg)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	cg.loadRegTyp(X0, aReg+1)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	cg.loadRegTyp(X0, aReg+2)
	cg.asm.CMPimmW(X0, TypeInt)
	cg.asm.BCond(CondNE, exitLabel)

	// R(A) -= R(A+2)
	cg.loadRegIval(X0, aReg)
	cg.loadRegIval(X1, aReg+2)
	cg.asm.SUBreg(X0, X0, X1)
	cg.storeRegIval(X0, aReg) // type stays TypeInt

	// Jump to FORLOOP (pc + 1 + sbx)
	target := pc + 1 + sbx
	cg.asm.B(pcLabel(target))

	// Side exit
	after := fmt.Sprintf("forprep_done_%d", pc)
	cg.asm.B(after) // dead code, but keeps labels happy

	cg.asm.Label(exitLabel)
	cg.asm.LoadImm64(X1, int64(pc))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 1)
	cg.asm.B("epilogue")

	cg.asm.Label(after)
	return nil
}

// emitForLoop: R(A) += R(A+2); if in range: R(A+3) = R(A), PC += sBx.
// Integer specialization.
func (cg *Codegen) emitForLoop(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	// idx += step
	cg.loadRegIval(X0, aReg)     // idx
	cg.loadRegIval(X1, aReg+2)   // step
	cg.asm.ADDreg(X0, X0, X1)    // new idx
	cg.storeRegIval(X0, aReg)    // R(A).ival = new idx

	// Load limit
	cg.loadRegIval(X2, aReg+1)

	// Check step sign
	cg.asm.CMPimm(X1, 0)
	negStep := fmt.Sprintf("forloop_neg_%d", pc)
	cg.asm.BCond(CondLT, negStep)

	// Positive step: continue if idx <= limit
	cg.asm.CMPreg(X0, X2)
	loopBody := pcLabel(pc + 1 + sbx)
	exitFor := fmt.Sprintf("forloop_exit_%d", pc)
	cg.asm.BCond(CondGT, exitFor)
	cg.asm.B(fmt.Sprintf("forloop_cont_%d", pc))

	// Negative step: continue if idx >= limit
	cg.asm.Label(negStep)
	cg.asm.CMPreg(X0, X2)
	cg.asm.BCond(CondLT, exitFor)

	// Continue: R(A+3) = R(A) (as IntValue)
	cg.asm.Label(fmt.Sprintf("forloop_cont_%d", pc))
	cg.storeIntValue(aReg+3, X0) // R(A+3) = IntValue(idx)
	cg.asm.B(loopBody)

	// Loop done
	cg.asm.Label(exitFor)
	return nil
}

func (cg *Codegen) emitReturnOp(inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	if b == 0 {
		// Return R(A) to top — side exit since we don't track 'top'.
		cg.asm.LoadImm64(X1, int64(-1)) // signal variable return
		cg.asm.STR(X1, regCtx, ctxOffRetBase)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
		return nil
	}
	if b == 1 {
		// Return nothing.
		cg.asm.LoadImm64(X1, int64(aReg))
		cg.asm.STR(X1, regCtx, ctxOffRetBase)
		cg.asm.LoadImm64(X1, 0)
		cg.asm.STR(X1, regCtx, ctxOffRetCount)
		cg.asm.LoadImm64(X0, 0)
		cg.asm.B("epilogue")
		return nil
	}

	nret := b - 1
	cg.asm.LoadImm64(X1, int64(aReg))
	cg.asm.STR(X1, regCtx, ctxOffRetBase)
	cg.asm.LoadImm64(X1, int64(nret))
	cg.asm.STR(X1, regCtx, ctxOffRetCount)
	cg.asm.LoadImm64(X0, 0)
	cg.asm.B("epilogue")
	return nil
}
