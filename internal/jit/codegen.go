//go:build darwin && arm64

package jit

import (
	"fmt"
	"unsafe"

	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// JITContext is the bridge between Go and JIT-compiled code.
// The JIT function reads Regs/Constants/ResumePC and writes exit information.
type JITContext struct {
	Regs      uintptr // input:  pointer to vm.regs[base] (first Value in register window)
	Constants uintptr // input:  pointer to constants[0]
	ExitPC    int64   // output: bytecode PC where JIT exited
	ExitCode  int64   // output: 0 = normal return, 1 = side exit, 2 = call exit (resumable)
	RetBase   int64   // output: return base register index
	RetCount  int64   // output: return value count
	ResumePC  int64   // input:  non-zero → JIT resumes execution at this PC instead of starting from the beginning
}

// JITContext field offsets (verified at init).
const (
	ctxOffRegs      = 0
	ctxOffConstants = 8
	ctxOffExitPC    = 16
	ctxOffExitCode  = 24
	ctxOffRetBase   = 32
	ctxOffRetCount  = 40
	ctxOffResumePC  = 48
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
	if unsafe.Offsetof(ctx.ResumePC) != ctxOffResumePC {
		panic("jit: JITContext.ResumePC offset mismatch")
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

// forLoopDesc describes a detected numeric for-loop.
type forLoopDesc struct {
	prepPC           int
	forloopPC        int
	bodyStart        int
	aReg             int
	stepValue        int64
	stepKnown        bool
	canPin           bool  // false for non-innermost loops
	aliasLoopVar     bool  // true if R(A+3) can be aliased to R(A) (not written in body)
	bodyAccumulators []int // non-loop registers used as arithmetic accumulators in body
}

// regSet is a bitmask for VM register sets (supports registers 0-63).
type regSet = uint64

func regBit(r int) regSet {
	if r < 0 || r >= 64 {
		return 0
	}
	return 1 << uint(r)
}

func regSetHas(s regSet, r int) bool {
	return r >= 0 && r < 64 && s&regBit(r) != 0
}

// inlineCandidate describes a GETGLOBAL + CALL pattern that can be inlined.
type inlineCandidate struct {
	getglobalPC int           // PC of GETGLOBAL instruction
	callPC      int           // PC of CALL instruction
	callee      *vm.FuncProto // the function to inline
	fnReg       int           // register holding the function (A field of GETGLOBAL/CALL)
	nArgs       int           // number of arguments
	nResults    int           // number of expected results
	isSelfCall  bool          // true if this is a self-recursive call
}

// Codegen translates a FuncProto's bytecode to ARM64 machine code.
type Codegen struct {
	asm              *Assembler
	proto            *vm.FuncProto
	globals          map[string]rt.Value     // VM globals for function resolution
	knownInt         []regSet                // per-PC: bitmask of registers known to be TypeInt
	reachable        []bool                  // per-PC: has been reached by data-flow analysis
	forLoops         map[int]*forLoopDesc    // keyed by forloopPC
	pinnedRegs       map[int]Reg             // VM register → ARM register (active during loop body)
	pinnedVars       []int                   // ordered list of pinned VM registers (for spilling)
	inlineCandidates map[int]*inlineCandidate // keyed by CALL PC
	inlineSkipPCs    map[int]bool            // GETGLOBAL PCs to skip (part of inline pattern)
	hasSelfCalls     bool                    // true if function has self-recursive calls
	callExitPCs      []int                   // PCs that use call-exit (ExitCode=2) for resume dispatch
	cmpCallExitPCs   []int                   // PCs of comparison ops with non-int type guards (call-exit on guard fail)
	cmpResumeStubs   []cmpResumeStub         // resume stubs for comparison call-exits (deferred)
}

// cmpResumeStub describes a comparison call-exit resume stub with captured pinning state.
type cmpResumeStub struct {
	label      string         // label for this stub
	targetPC   string         // pcLabel to jump to after reload
	pinnedVars []int          // snapshot of pinnedVars at creation time
	pinnedRegs map[int]Reg    // snapshot of pinnedRegs at creation time
}

// Reserved register for self-recursion depth tracking.
const regSelfDepth = X25 // 0 = outermost call, >0 = self-recursive call

// Compile compiles a FuncProto to native ARM64 code.
func Compile(proto *vm.FuncProto) (*CompiledFunc, error) {
	return CompileWithGlobals(proto, nil)
}

// CompileWithGlobals compiles a FuncProto with access to globals for function inlining.
func CompileWithGlobals(proto *vm.FuncProto, globals map[string]rt.Value) (*CompiledFunc, error) {
	cg := &Codegen{
		asm:     NewAssembler(),
		proto:   proto,
		globals: globals,
	}

	cg.analyzeInlineCandidates() // must run first: identifies inline/self-call PCs for data flow
	cg.analyzeKnownIntRegs()
	cg.analyzeForLoops()
	cg.analyzeCallExitPCs()
	cg.emitPrologue()
	if err := cg.emitBody(); err != nil {
		return nil, err
	}
	// Emit deferred comparison resume stubs with captured pinning state.
	for _, stub := range cg.cmpResumeStubs {
		cg.asm.Label(stub.label)
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
		// Reload pinned registers using the captured state from when the comparison was emitted.
		for _, vmReg := range stub.pinnedVars {
			if armReg, ok := stub.pinnedRegs[vmReg]; ok {
				cg.asm.LDR(armReg, regRegs, regIvalOffset(vmReg))
			}
		}
		cg.asm.B(stub.targetPC)
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
// If there are call-exit PCs, it also emits a dispatch table for re-entry.
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

	// Dispatch table for call-exit resume.
	// When ResumePC != 0, jump to the resume point after the call-exit instruction.
	hasDispatch := len(cg.callExitPCs) > 0 || len(cg.cmpCallExitPCs) > 0
	if hasDispatch {
		a.LDR(X0, regCtx, ctxOffResumePC) // x0 = ctx.ResumePC
		a.CBZ(X0, "normal_start")         // if 0, start from beginning

		// Clear ResumePC so nested re-entries don't loop.
		a.STR(XZR, regCtx, ctxOffResumePC)

		// Initialize self-recursion depth to 0 on dispatch re-entry.
		// Without this, X25 holds whatever Go left in it, causing
		// emitSelfCallReturn to take the nested-call RET path (bare RET
		// without epilogue), which leaves the 96-byte stack frame unpopped.
		if cg.hasSelfCalls {
			a.MOVimm16(regSelfDepth, 0)
		}

		// Regular call-exit dispatch: CMP + B.EQ for each call-exit PC.
		// Resume value is nextPC (always ExitPC+1).
		for _, pc := range cg.callExitPCs {
			a.CMPimm(X0, uint16(pc+1))
			a.BCond(CondEQ, resumeLabel(pc))
		}

		// Comparison call-exit dispatch: resume value has high bit set
		// (nextPC | 0x8000) to avoid collisions with regular call-exits.
		for _, pc := range cg.cmpCallExitPCs {
			cmpStub := fmt.Sprintf("cmp_resume_%d", pc)
			a.LoadImm64(X1, int64((pc+1)|0x8000))
			a.CMPreg(X0, X1)
			a.BCond(CondEQ, cmpStub+"_1")
			a.LoadImm64(X1, int64((pc+2)|0x8000))
			a.CMPreg(X0, X1)
			a.BCond(CondEQ, cmpStub+"_2")
		}

		// Unknown resume PC — fall back to side-exit.
		a.STR(X0, regCtx, ctxOffExitPC)
		a.LoadImm64(X0, 1) // ExitCode = 1
		a.B("epilogue")

		a.Label("normal_start")
	}

	if cg.hasSelfCalls {
		a.MOVimm16(regSelfDepth, 0) // depth = 0 at outermost call

		// Type-guard parameters at outermost entry: side-exit if any param is not TypeInt.
		// Recursive self-calls enter at self_call_entry (below), skipping this guard.
		// This is safe because self-calls always pass results of int arithmetic (SUB/ADD).
		if cg.proto.NumParams > 0 {
			for i := 0; i < cg.proto.NumParams; i++ {
				off := regTypOffset(i)
				if off <= 4095 {
					a.LDRB(X0, regRegs, off)
				} else {
					a.LoadImm64(X10, int64(off))
					a.ADDreg(X10, regRegs, X10)
					a.LDRB(X0, X10, 0)
				}
				a.CMPimmW(X0, TypeInt)
				a.BCond(CondNE, "self_param_guard_fail")
			}
			a.B("self_param_guard_ok")
			a.Label("self_param_guard_fail")
			// Side exit: parameter is not int, fall back to interpreter.
			a.LoadImm64(X1, 0)
			a.STR(X1, regCtx, ctxOffExitPC)
			a.LoadImm64(X0, 1) // ExitCode = 1 (side exit)
			a.B("epilogue")
			a.Label("self_param_guard_ok")
		}

		a.Label("self_call_entry")  // self-recursive calls BL here
	}
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

// regFvalOffset returns the byte offset of R(i).data from regRegs (float stored as bits in data).
func regFvalOffset(i int) int {
	return i*ValueSize + OffsetData
}

// loadRegTyp loads the type byte of R(reg) into the ARM64 register dst (as W-form).
func (cg *Codegen) loadRegTyp(dst Reg, reg int) {
	off := regTypOffset(reg)
	if off <= 4095 {
		cg.asm.LDRB(dst, regRegs, off)
	} else {
		cg.asm.LoadImm64(X10, int64(off))
		cg.asm.ADDreg(X10, regRegs, X10)
		cg.asm.LDRB(dst, X10, 0)
	}
}

// storeRegTyp stores a type byte into R(reg).typ.
// Uses X10 as scratch for large offsets (not X9, which callers may pass as src).
func (cg *Codegen) storeRegTyp(src Reg, reg int) {
	off := regTypOffset(reg)
	if off <= 4095 {
		cg.asm.STRB(src, regRegs, off)
	} else {
		cg.asm.LoadImm64(X10, int64(off))
		cg.asm.ADDreg(X10, regRegs, X10)
		cg.asm.STRB(src, X10, 0)
	}
}

// loadRegIval loads R(reg).ival into the ARM64 register dst (64-bit).
// If the register is pinned, uses a register-to-register MOV instead of memory load.
func (cg *Codegen) loadRegIval(dst Reg, reg int) {
	if armReg, ok := cg.pinnedRegs[reg]; ok {
		if dst != armReg {
			cg.asm.MOVreg(dst, armReg)
		}
		return
	}
	off := regIvalOffset(reg)
	cg.asm.LDR(dst, regRegs, off)
}

// storeRegIval stores a 64-bit value into R(reg).ival.
// If the register is pinned, uses a register-to-register MOV instead of memory store.
func (cg *Codegen) storeRegIval(src Reg, reg int) {
	if armReg, ok := cg.pinnedRegs[reg]; ok {
		if src != armReg {
			cg.asm.MOVreg(armReg, src)
		}
		return
	}
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
// For pinned registers, skips the type store (typ is maintained on spill).
// For self-call functions, skips the type store because all values are guaranteed
// TypeInt (parameter guard at entry + int-only arithmetic).
func (cg *Codegen) storeIntValue(reg int, valReg Reg) {
	if _, pinned := cg.pinnedRegs[reg]; !pinned && !cg.hasSelfCalls {
		cg.asm.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, reg)
	}
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
			cg.asm.LoadImm64(X10, int64(off))
			cg.asm.ADDreg(X10, regConsts, X10)
			cg.asm.LDRB(dst, X10, 0)
		}
	} else {
		cg.loadRegTyp(dst, idx)
	}
}

// loadRKIval loads RK(idx).ival into dst.
// For small integer constants, emits a MOV immediate instead of a memory load.
func (cg *Codegen) loadRKIval(dst Reg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		// Optimize: if the constant is a small integer, use MOV immediate.
		if constIdx < len(cg.proto.Constants) && cg.proto.Constants[constIdx].IsInt() {
			v := cg.proto.Constants[constIdx].Int()
			cg.asm.LoadImm64(dst, v)
			return
		}
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
		off := constIdx*ValueSize + OffsetData
		cg.asm.FLDRd(dst, regConsts, off)
	} else {
		cg.loadRegFval(dst, idx)
	}
}

// rkSmallIntConst returns the integer value if idx refers to an RK constant that
// is a non-negative integer fitting in 12 bits (0..4095). Returns -1 otherwise.
func (cg *Codegen) rkSmallIntConst(idx int) int64 {
	if !vm.IsRK(idx) {
		return -1
	}
	constIdx := vm.RKToConstIdx(idx)
	if constIdx >= len(cg.proto.Constants) {
		return -1
	}
	c := cg.proto.Constants[constIdx]
	if !c.IsInt() {
		return -1
	}
	v := c.Int()
	if v >= 0 && v <= 4095 {
		return v
	}
	return -1
}

// ──────────────────────────────────────────────────────────────────────────────
// Copy a full Value (32 bytes) between registers.
// ──────────────────────────────────────────────────────────────────────────────

// copyValue copies the full Value (32 bytes = 4 words) from src to dst.
func (cg *Codegen) copyValue(dstReg, srcReg int) {
	srcBase := srcReg * ValueSize
	dstBase := dstReg * ValueSize
	a := cg.asm

	for i := 0; i < 4; i++ {
		a.LDR(X0, regRegs, srcBase+i*8)
		a.STR(X0, regRegs, dstBase+i*8)
	}
}

// copyRKValue copies a full Value from RK(idx) to R(dst).
func (cg *Codegen) copyRKValue(dstReg, rkIdx int) {
	if vm.IsRK(rkIdx) {
		constIdx := vm.RKToConstIdx(rkIdx)
		srcBase := constIdx * ValueSize
		dstBase := dstReg * ValueSize
		a := cg.asm

		for i := 0; i < 4; i++ {
			a.LDR(X0, regConsts, srcBase+i*8)
			a.STR(X0, regRegs, dstBase+i*8)
		}
	} else {
		cg.copyValue(dstReg, rkIdx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Type guard hoisting — forward data-flow analysis for known-int registers
// ──────────────────────────────────────────────────────────────────────────────

// analyzeKnownIntRegs computes, for each bytecode PC, which VM registers are
// guaranteed to hold TypeInt values. Uses bitmask-based set operations for speed.
func (cg *Codegen) analyzeKnownIntRegs() {
	code := cg.proto.Code
	n := len(code)
	if n == 0 {
		return
	}

	cg.knownInt = make([]regSet, n)
	cg.reachable = make([]bool, n)
	cg.reachable[0] = true // entry point

	// For self-call functions, parameters are guaranteed TypeInt at function entry.
	// The outermost call validates parameter types before self_call_entry;
	// all recursive self-calls pass int results from SUB/ADD which are always int.
	// This eliminates redundant type guards on parameters throughout the function body.
	if cg.hasSelfCalls && cg.proto.NumParams > 0 {
		for i := 0; i < cg.proto.NumParams && i < 64; i++ {
			cg.knownInt[0] |= regBit(i)
		}
	}

	changed := true
	for changed {
		changed = false
		for pc := 0; pc < n; pc++ {
			if !cg.reachable[pc] {
				continue
			}
			out := cg.intTransfer(pc)
			for _, succ := range cg.pcSuccessors(pc) {
				if succ < 0 || succ >= n {
					continue
				}
				if !cg.reachable[succ] {
					cg.reachable[succ] = true
					cg.knownInt[succ] = out
					changed = true
				} else {
					// Intersect: only keep bits present in both.
					merged := cg.knownInt[succ] & out
					if merged != cg.knownInt[succ] {
						cg.knownInt[succ] = merged
						changed = true
					}
				}
			}
		}
	}
}

// intTransfer computes the output known-int set after executing instruction at pc.
func (cg *Codegen) intTransfer(pc int) regSet {
	inst := cg.proto.Code[pc]
	op := vm.DecodeOp(inst)
	out := cg.knownInt[pc]

	switch op {
	case vm.OP_LOADINT:
		out |= regBit(vm.DecodeA(inst))
	case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_UNM:
		out |= regBit(vm.DecodeA(inst))
	case vm.OP_FORPREP:
		a := vm.DecodeA(inst)
		out |= regBit(a) | regBit(a+1) | regBit(a+2)
	case vm.OP_FORLOOP:
		a := vm.DecodeA(inst)
		out |= regBit(a) | regBit(a+3)
	case vm.OP_MOVE:
		a := vm.DecodeA(inst)
		if regSetHas(cg.knownInt[pc], vm.DecodeB(inst)) {
			out |= regBit(a)
		} else {
			out &^= regBit(a)
		}
	case vm.OP_LOADK:
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		if bx < len(cg.proto.Constants) && cg.proto.Constants[bx].IsInt() {
			out |= regBit(a)
		} else {
			out &^= regBit(a)
		}
	case vm.OP_LOADNIL:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		for i := a; i <= a+b; i++ {
			out &^= regBit(i)
		}
	case vm.OP_LOADBOOL, vm.OP_NOT:
		out &^= regBit(vm.DecodeA(inst))
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_JMP:
		// No register writes.
	case vm.OP_CALL:
		// For inline/self-call candidates, the result is placed in fnReg as TypeInt.
		if cg.inlineCandidates != nil {
			if candidate, ok := cg.inlineCandidates[pc]; ok {
				out |= regBit(candidate.fnReg)
				break
			}
		}
		out &^= regBit(vm.DecodeA(inst))
	case vm.OP_GETGLOBAL:
		// Skipped GETGLOBALs don't modify registers.
		if cg.inlineSkipPCs != nil && cg.inlineSkipPCs[pc] {
			break
		}
		out &^= regBit(vm.DecodeA(inst))
	default:
		out &^= regBit(vm.DecodeA(inst))
	}
	return out
}

// pcSuccessors returns the successor PCs for an instruction.
func (cg *Codegen) pcSuccessors(pc int) []int {
	inst := cg.proto.Code[pc]
	op := vm.DecodeOp(inst)

	if !cg.isSupported(op) {
		// Check if this is an inline/self-call CALL that we handle natively.
		if op == vm.OP_CALL && cg.inlineCandidates != nil {
			if _, ok := cg.inlineCandidates[pc]; ok {
				return []int{pc + 1}
			}
		}
		// Call-exit opcodes resume at pc+1 (executor handles the instruction).
		if isCallExitOp(op) {
			return []int{pc + 1}
		}
		return nil // permanent side-exit, no JIT successors
	}

	switch op {
	case vm.OP_RETURN:
		return nil
	case vm.OP_JMP:
		return []int{pc + 1 + vm.DecodesBx(inst)}
	case vm.OP_FORPREP:
		return []int{pc + 1 + vm.DecodesBx(inst)}
	case vm.OP_FORLOOP:
		return []int{pc + 1, pc + 1 + vm.DecodesBx(inst)}
	case vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST:
		return []int{pc + 1, pc + 2}
	case vm.OP_LOADBOOL:
		if vm.DecodeC(inst) != 0 {
			return []int{pc + 2}
		}
		return []int{pc + 1}
	default:
		return []int{pc + 1}
	}
}

// isRKKnownInt returns true if RK(idx) is guaranteed TypeInt at the given PC.
func (cg *Codegen) isRKKnownInt(pc, rkIdx int) bool {
	if vm.IsRK(rkIdx) {
		constIdx := vm.RKToConstIdx(rkIdx)
		if constIdx < len(cg.proto.Constants) {
			return cg.proto.Constants[constIdx].IsInt()
		}
		return false
	}
	if cg.knownInt != nil && pc < len(cg.knownInt) {
		return regSetHas(cg.knownInt[pc], rkIdx)
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// For-loop analysis and register pinning
// ──────────────────────────────────────────────────────────────────────────────

// analyzeForLoops detects numeric for-loop structures and determines
// step values for optimization.
func (cg *Codegen) analyzeForLoops() {
	code := cg.proto.Code
	cg.forLoops = make(map[int]*forLoopDesc)
	cg.pinnedRegs = make(map[int]Reg)

	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		if vm.DecodeOp(inst) != vm.OP_FORPREP {
			continue
		}
		aReg := vm.DecodeA(inst)
		sbx := vm.DecodesBx(inst)
		flPC := pc + 1 + sbx
		if flPC < 0 || flPC >= len(code) || vm.DecodeOp(code[flPC]) != vm.OP_FORLOOP {
			continue
		}
		flSbx := vm.DecodesBx(code[flPC])
		bodyStart := flPC + 1 + flSbx

		desc := &forLoopDesc{
			prepPC:    pc,
			forloopPC: flPC,
			bodyStart: bodyStart,
			aReg:      aReg,
			canPin:    true,
		}

		// Detect step value by scanning backward from FORPREP for LOADINT R(A+2).
		stepReg := aReg + 2
		for scanPC := pc - 1; scanPC >= 0; scanPC-- {
			si := code[scanPC]
			sa := vm.DecodeA(si)
			if sa == stepReg {
				if vm.DecodeOp(si) == vm.OP_LOADINT {
					desc.stepValue = int64(vm.DecodesBx(si))
					desc.stepKnown = true
				}
				break
			}
		}

		// Check if R(A+3) is written in the body — if not, alias it to R(A).
		loopVarReg := aReg + 3
		loopVarWritten := false
		for scanPC := bodyStart; scanPC < flPC; scanPC++ {
			si := code[scanPC]
			if vm.DecodeA(si) == loopVarReg {
				sop := vm.DecodeOp(si)
				// Skip comparison/test ops that don't write to R(A).
				if sop != vm.OP_EQ && sop != vm.OP_LT && sop != vm.OP_LE &&
					sop != vm.OP_TEST && sop != vm.OP_JMP {
					loopVarWritten = true
					break
				}
			}
		}
		desc.aliasLoopVar = !loopVarWritten

		// Determine body accumulator registers (read+write same reg in ADD/SUB/MUL).
		desc.bodyAccumulators = cg.findAccumulators(bodyStart, flPC, aReg)

		cg.forLoops[flPC] = desc
		cg.forLoops[pc] = desc // also index by prepPC
	}

	// Disable pinning for non-innermost loops (loops whose body contains another FORPREP).
	// Use a set to deduplicate (forLoops is indexed by both prepPC and forloopPC).
	seen := make(map[*forLoopDesc]bool)
	for _, desc := range cg.forLoops {
		if seen[desc] {
			continue
		}
		seen[desc] = true
		for innerPC := desc.bodyStart; innerPC < desc.forloopPC; innerPC++ {
			if vm.DecodeOp(code[innerPC]) == vm.OP_FORPREP {
				desc.canPin = false
				break
			}
		}
	}
}

// findAccumulators finds registers in the loop body that are both source and
// destination of arithmetic operations (e.g., s = s + i → R(s) is an accumulator).
// Also detects indirect accumulators: ADD Rtemp, Raccum, Rx; MOVE Raccum, Rtemp
// (where the compiler uses a temporary for s = s + i).
// Excludes for-loop control registers (aReg..aReg+3).
// Safety: excludes registers that are also written by non-integer-producing
// instructions (MOVE, LOADK with non-int constant, call-exit ops like GETTABLE,
// GETFIELD, etc.), because pinning such registers would corrupt non-integer values.
func (cg *Codegen) findAccumulators(bodyStart, bodyEnd, aReg int) []int {
	counts := make(map[int]int)
	code := cg.proto.Code
	for pc := bodyStart; pc < bodyEnd; pc++ {
		inst := code[pc]
		op := vm.DecodeOp(inst)
		switch op {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL:
			a := vm.DecodeA(inst)
			b := vm.DecodeB(inst)
			c := vm.DecodeC(inst)
			// Skip loop control registers
			if a >= aReg && a <= aReg+3 {
				continue
			}
			// Direct accumulator: s = s + i (R(A) is both source and dest)
			if (!vm.IsRK(b) && b == a) || (!vm.IsRK(c) && c == a) {
				counts[a]++
				continue
			}
			// Indirect accumulator: ADD Rtemp, Raccum, Rx; MOVE Raccum, Rtemp
			if pc+1 < bodyEnd && vm.DecodeOp(code[pc+1]) == vm.OP_MOVE {
				moveA := vm.DecodeA(code[pc+1])
				moveB := vm.DecodeB(code[pc+1])
				if moveB == a { // MOVE copies the ADD result
					// Check if the accumulator (moveA) is one of the ADD sources
					isAccum := (!vm.IsRK(b) && b == moveA) || (!vm.IsRK(c) && c == moveA)
					if isAccum && !(moveA >= aReg && moveA <= aReg+3) {
						counts[moveA]++ // pin the accumulator
						counts[a]++     // pin the temporary too
					}
				}
			}
		}
	}

	// Safety check: exclude registers that are written by non-integer-producing
	// instructions anywhere in the loop body. Pinning such registers would corrupt
	// non-integer values (tables, strings) during spill/reload cycles.
	unsafe := make(map[int]bool)
	for pc := bodyStart; pc < bodyEnd; pc++ {
		inst := code[pc]
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		switch op {
		case vm.OP_MOVE:
			// MOVE R(A) = R(B) — source could be any type
			unsafe[a] = true
		case vm.OP_LOADK:
			// LOADK with non-int constant writes a non-integer value
			bx := vm.DecodeBx(inst)
			if bx < len(cg.proto.Constants) && !cg.proto.Constants[bx].IsInt() {
				unsafe[a] = true
			}
		case vm.OP_LOADNIL, vm.OP_LOADBOOL:
			unsafe[a] = true
		case vm.OP_GETTABLE, vm.OP_GETFIELD, vm.OP_GETGLOBAL, vm.OP_GETUPVAL:
			unsafe[a] = true
		case vm.OP_CALL:
			unsafe[a] = true
		case vm.OP_NEWTABLE:
			unsafe[a] = true
		case vm.OP_LEN, vm.OP_CONCAT:
			unsafe[a] = true
		case vm.OP_SELF:
			unsafe[a] = true
			unsafe[a+1] = true
		case vm.OP_TESTSET:
			unsafe[a] = true
		}
	}

	// Return accumulators sorted by frequency (up to 3), excluding unsafe ones.
	var result []int
	for reg := range counts {
		if unsafe[reg] {
			continue
		}
		result = append(result, reg)
	}
	// Simple sort by count (descending)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if counts[result[j]] > counts[result[i]] {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	if len(result) > 3 {
		result = result[:3]
	}
	return result
}

// Callee-saved registers available for pinning.
var pinPool = []Reg{X19, X20, X21, X22, X23, X24, X25}

// setupLoopPinning configures register pinning for a for-loop and emits
// code to load VM registers into ARM registers. Returns true if pinning was set up.
func (cg *Codegen) setupLoopPinning(desc *forLoopDesc) bool {
	// Check we have enough pinning registers.
	needed := 4 + len(desc.bodyAccumulators) // loop control (4) + body accumulators
	if needed > len(pinPool) {
		needed = len(pinPool)
	}

	a := desc.aReg
	poolIdx := 0

	// Pin loop control registers: R(A)=idx, R(A+1)=limit, R(A+2)=step, R(A+3)=loopvar
	for i := 0; i < 4 && poolIdx < len(pinPool); i++ {
		vmReg := a + i
		if i == 3 && desc.aliasLoopVar {
			// Alias R(A+3) to R(A) — no separate ARM register needed.
			cg.pinnedRegs[vmReg] = cg.pinnedRegs[a]
			// Don't add to pinnedVars (spill only through R(A)).
			continue
		}
		armReg := pinPool[poolIdx]
		poolIdx++
		cg.pinnedRegs[vmReg] = armReg
		cg.pinnedVars = append(cg.pinnedVars, vmReg)
	}

	// Pin body accumulators.
	for _, vmReg := range desc.bodyAccumulators {
		if poolIdx >= len(pinPool) {
			break
		}
		armReg := pinPool[poolIdx]
		poolIdx++
		cg.pinnedRegs[vmReg] = armReg
		cg.pinnedVars = append(cg.pinnedVars, vmReg)
	}

	// Load pinned registers from memory.
	for _, vmReg := range cg.pinnedVars {
		armReg := cg.pinnedRegs[vmReg]
		cg.asm.LDR(armReg, regRegs, regIvalOffset(vmReg))
	}

	return true
}

// spillPinnedRegs stores all pinned ARM registers back to VM register memory.
// Iterates over all pinned registers including aliased ones (e.g., R(A+3) aliased to R(A)).
func (cg *Codegen) spillPinnedRegs() {
	for vmReg, armReg := range cg.pinnedRegs {
		cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
		cg.asm.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, vmReg)
	}
}

// reloadPinnedRegs loads all pinned ARM registers from VM register memory.
// Used at resume points after a call-exit to restore pinned state.
func (cg *Codegen) reloadPinnedRegs() {
	for _, vmReg := range cg.pinnedVars {
		armReg := cg.pinnedRegs[vmReg]
		cg.asm.LDR(armReg, regRegs, regIvalOffset(vmReg))
	}
}

// clearPinning removes all register pinning.
func (cg *Codegen) clearPinning() {
	cg.pinnedRegs = make(map[int]Reg)
	cg.pinnedVars = nil
}

// isCallExitOp returns true if the opcode should use call-exit (ExitCode=2)
// instead of a permanent side-exit (ExitCode=1).
// Call-exit allows the executor to handle the instruction and re-enter JIT.
func isCallExitOp(op vm.Opcode) bool {
	switch op {
	case vm.OP_CALL, vm.OP_GETGLOBAL, vm.OP_SETGLOBAL,
		vm.OP_GETTABLE, vm.OP_SETTABLE,
		vm.OP_GETFIELD, vm.OP_SETFIELD,
		vm.OP_NEWTABLE, vm.OP_LEN:
		return true
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Function inlining analysis
// ──────────────────────────────────────────────────────────────────────────────

// analyzeInlineCandidates detects GETGLOBAL + CALL patterns where the global
// resolves to a simple bytecode function that can be inlined, or where the
// global is a self-recursive call to the current function.
func (cg *Codegen) analyzeInlineCandidates() {
	cg.inlineCandidates = make(map[int]*inlineCandidate)
	cg.inlineSkipPCs = make(map[int]bool)

	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		globalA := vm.DecodeA(inst)
		globalBx := vm.DecodeBx(inst)
		if globalBx >= len(cg.proto.Constants) {
			continue
		}
		name := cg.proto.Constants[globalBx].Str()

		// Find the CALL that uses R(globalA) within the next few instructions
		for pc2 := pc + 1; pc2 < len(code) && pc2 <= pc+10; pc2++ {
			inst2 := code[pc2]
			op2 := vm.DecodeOp(inst2)
			if op2 == vm.OP_CALL && vm.DecodeA(inst2) == globalA {
				b := vm.DecodeB(inst2)
				c := vm.DecodeC(inst2)

				// Check for self-recursive call first.
				if name == cg.proto.Name && cg.proto.Name != "" {
					candidate := &inlineCandidate{
						getglobalPC: pc,
						callPC:      pc2,
						fnReg:       globalA,
						nArgs:       b - 1,
						nResults:    c - 1,
						isSelfCall:  true,
					}
					cg.inlineCandidates[pc2] = candidate
					cg.inlineSkipPCs[pc] = true
					cg.hasSelfCalls = true
					break
				}

				// Check for inlineable global function.
				if cg.globals != nil {
					fnVal, ok := cg.globals[name]
					if !ok || !fnVal.IsFunction() {
						break
					}
					vcl, _ := fnVal.Ptr().(*vm.Closure)
					if vcl == nil {
						break
					}
					if !cg.isInlineable(vcl.Proto) {
						break
					}
					candidate := &inlineCandidate{
						getglobalPC: pc,
						callPC:      pc2,
						callee:      vcl.Proto,
						fnReg:       globalA,
						nArgs:       b - 1,
						nResults:    c - 1,
					}
					cg.inlineCandidates[pc2] = candidate
					cg.inlineSkipPCs[pc] = true
				}
				break
			}
			// If the register is overwritten by another instruction, stop
			if vm.DecodeA(inst2) == globalA {
				break
			}
		}
	}
}

// isInlineable returns true if a function body is simple enough to inline.
func (cg *Codegen) isInlineable(proto *vm.FuncProto) bool {
	if len(proto.Upvalues) > 0 || proto.IsVarArg || len(proto.Protos) > 0 {
		return false
	}
	if len(proto.Code) > 20 {
		return false
	}
	for _, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD,
			vm.OP_MOVE, vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL,
			vm.OP_EQ, vm.OP_LT, vm.OP_LE, vm.OP_TEST, vm.OP_JMP,
			vm.OP_RETURN, vm.OP_UNM, vm.OP_NOT:
			continue
		default:
			return false
		}
	}
	return true
}

// emitInlineCall emits inline native code for a function call, replacing
// GETGLOBAL + CALL with the callee's body using register remapping.
func (cg *Codegen) emitInlineCall(pc int, candidate *inlineCandidate) error {
	callee := candidate.callee
	fnReg := candidate.fnReg

	// Build register mapping: callee register → caller register
	// Callee params map to the caller's argument registers
	// Callee locals map to scratch space after the args
	regMap := make(map[int]int)
	for i := 0; i < callee.NumParams; i++ {
		regMap[i] = fnReg + 1 + i
	}
	scratchBase := fnReg + 1 + candidate.nArgs
	for i := callee.NumParams; i < callee.MaxStack; i++ {
		regMap[i] = scratchBase + (i - callee.NumParams)
	}

	// Find the return register and map it to the result register
	for _, calleeInst := range callee.Code {
		if vm.DecodeOp(calleeInst) == vm.OP_RETURN {
			retA := vm.DecodeA(calleeInst)
			if candidate.nResults > 0 {
				regMap[retA] = fnReg // first result → R(fnReg)
			}
			break
		}
	}

	exitLabel := fmt.Sprintf("inline_exit_%d", pc)
	afterLabel := fmt.Sprintf("inline_done_%d", pc)

	// Emit callee instructions with register remapping
	for _, calleeInst := range callee.Code {
		calleeOp := vm.DecodeOp(calleeInst)
		if calleeOp == vm.OP_RETURN {
			// Handle result placement
			retA := vm.DecodeA(calleeInst)
			retB := vm.DecodeB(calleeInst)
			mappedRetA := regMap[retA]
			if retB == 1 {
				// Return nothing — set result to nil
				if candidate.nResults > 0 {
					cg.storeNilValue(fnReg)
				}
			} else if mappedRetA != fnReg && candidate.nResults > 0 {
				// Copy result to fnReg
				cg.copyValue(fnReg, mappedRetA)
			}
			break
		}

		switch calleeOp {
		case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL:
			a := vm.DecodeA(calleeInst)
			b := vm.DecodeB(calleeInst)
			c := vm.DecodeC(calleeInst)
			mappedA := regMap[a]

			// Remap B and C (register references only, not RK constants)
			mappedB := b
			if !vm.IsRK(b) {
				mappedB = regMap[b]
			}
			mappedC := c
			if !vm.IsRK(c) {
				mappedC = regMap[c]
			}

			// Handle RK constants from callee's constant pool
			// For callee constants, we need to load them directly since
			// regConsts points to the caller's constants
			bIsCalleeConst := vm.IsRK(b)
			cIsCalleeConst := vm.IsRK(c)

			// Type guards for non-known-int operands
			if !bIsCalleeConst {
				cg.loadRegTyp(X0, mappedB)
				cg.asm.CMPimmW(X0, TypeInt)
				cg.asm.BCond(CondNE, exitLabel)
			}
			if !cIsCalleeConst {
				cg.loadRegTyp(X0, mappedC)
				cg.asm.CMPimmW(X0, TypeInt)
				cg.asm.BCond(CondNE, exitLabel)
			}

			// Load operands
			if bIsCalleeConst {
				constIdx := vm.RKToConstIdx(b)
				if constIdx < len(callee.Constants) {
					cg.asm.LoadImm64(X0, callee.Constants[constIdx].Int())
				}
			} else {
				cg.loadRegIval(X0, mappedB)
			}

			if cIsCalleeConst {
				constIdx := vm.RKToConstIdx(c)
				if constIdx < len(callee.Constants) {
					cg.asm.LoadImm64(X1, callee.Constants[constIdx].Int())
				}
			} else {
				cg.loadRegIval(X1, mappedC)
			}

			// Arithmetic
			switch calleeOp {
			case vm.OP_ADD:
				cg.asm.ADDreg(X0, X0, X1)
			case vm.OP_SUB:
				cg.asm.SUBreg(X0, X0, X1)
			case vm.OP_MUL:
				cg.asm.MUL(X0, X0, X1)
			}
			cg.storeIntValue(mappedA, X0)

		case vm.OP_MOVE:
			a := regMap[vm.DecodeA(calleeInst)]
			b := regMap[vm.DecodeB(calleeInst)]
			cg.copyValue(a, b)

		case vm.OP_LOADINT:
			a := regMap[vm.DecodeA(calleeInst)]
			sbx := vm.DecodesBx(calleeInst)
			cg.asm.LoadImm64(X0, int64(sbx))
			cg.storeIntValue(a, X0)

		case vm.OP_LOADNIL:
			a := vm.DecodeA(calleeInst)
			b := vm.DecodeB(calleeInst)
			for i := a; i <= a+b; i++ {
				cg.storeNilValue(regMap[i])
			}

		default:
			// Unsupported opcode in inline — fall back to side exit
			cg.spillPinnedRegs()
			cg.asm.LoadImm64(X1, int64(candidate.getglobalPC))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
			return nil
		}
	}

	cg.asm.B(afterLabel)

	// Side exit for type guard failures
	cg.asm.Label(exitLabel)
	cg.spillPinnedRegs()
	cg.asm.LoadImm64(X1, int64(candidate.getglobalPC))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 1)
	cg.asm.B("epilogue")

	cg.asm.Label(afterLabel)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Call-exit analysis and resume dispatch
// ──────────────────────────────────────────────────────────────────────────────

// analyzeCallExitPCs collects bytecode PCs that will use call-exit (ExitCode=2).
// These are unsupported opcodes (GETGLOBAL, SETGLOBAL, CALL) that the executor
// can handle and then re-enter JIT at the next instruction.
//
// Optimization: A call-exit is only worthwhile if the successor instruction
// (pc+1) can do useful JIT work — i.e., it is supported, an inline candidate,
// or itself a call-exit. If the successor would immediately cause a permanent
// side-exit, we demote the current instruction to permanent side-exit too,
// avoiding a wasted exit/re-enter cycle.
// Processing in reverse order handles cascading: if pc+2 is unsupported, pc+1
// gets demoted, and then pc gets demoted as well.
func (cg *Codegen) analyzeCallExitPCs() {
	code := cg.proto.Code
	cg.callExitPCs = nil

	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if cg.inlineSkipPCs[pc] {
			continue
		}
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		if !cg.isSupported(op) && isCallExitOp(op) {
			cg.callExitPCs = append(cg.callExitPCs, pc)
		}
		// GETFIELD/GETTABLE are "supported" (native fast path) but still need
		// call-exit resume entries for the fallback slow path.
		if op == vm.OP_GETFIELD || op == vm.OP_GETTABLE {
			cg.callExitPCs = append(cg.callExitPCs, pc)
		}
	}

	// Detect comparison call-exit PCs.
	// EQ/LT/LE with non-integer operands will call-exit on type guard failure.
	// The executor may resume at pc+1 (condition false) or pc+2 (condition true/skip).
	cg.cmpCallExitPCs = nil
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op == vm.OP_EQ || op == vm.OP_LT || op == vm.OP_LE {
			bIdx := vm.DecodeB(code[pc])
			cIdx := vm.DecodeC(code[pc])
			bKnown := cg.isRKKnownInt(pc, bIdx)
			cKnown := cg.isRKKnownInt(pc, cIdx)
			if !bKnown || !cKnown {
				cg.cmpCallExitPCs = append(cg.cmpCallExitPCs, pc)
			}
		}
	}
}

// isJITProductive returns true if the instruction at pc will do useful work
// in JIT (not immediately side-exit). This includes:
// - natively supported instructions
// - inline candidates or inline-skip PCs
// - surviving call-exit candidates
func (cg *Codegen) isJITProductive(pc int, survivedCallExits map[int]bool) bool {
	if pc >= len(cg.proto.Code) {
		return false
	}
	if cg.inlineSkipPCs[pc] {
		return true
	}
	if _, ok := cg.inlineCandidates[pc]; ok {
		return true
	}
	op := vm.DecodeOp(cg.proto.Code[pc])
	if cg.isSupported(op) {
		return true
	}
	if survivedCallExits[pc] {
		return true
	}
	return false
}

// resumeLabel returns the label name for the resume point after a call-exit at pc.
func resumeLabel(pc int) string {
	return fmt.Sprintf("resume_after_%d", pc)
}

// ──────────────────────────────────────────────────────────────────────────────
// Body compilation
// ──────────────────────────────────────────────────────────────────────────────

func (cg *Codegen) emitBody() error {
	code := cg.proto.Code

	// Build set of call-exit PCs for fast lookup.
	callExitSet := make(map[int]bool, len(cg.callExitPCs))
	for _, pc := range cg.callExitPCs {
		callExitSet[pc] = true
	}

	// Emit code for each instruction.
	for pc := 0; pc < len(code); pc++ {
		cg.asm.Label(pcLabel(pc))
		inst := code[pc]
		op := vm.DecodeOp(inst)

		// Skip GETGLOBAL instructions that are part of an inline candidate
		if cg.inlineSkipPCs[pc] {
			continue
		}

		// Handle inlined CALL and self-recursive CALL instructions
		if candidate, ok := cg.inlineCandidates[pc]; ok {
			if candidate.isSelfCall {
				if err := cg.emitSelfCall(pc, candidate); err != nil {
					return fmt.Errorf("pc %d (self-call): %w", pc, err)
				}
			} else {
				if err := cg.emitInlineCall(pc, candidate); err != nil {
					return fmt.Errorf("pc %d (inline): %w", pc, err)
				}
			}
			continue
		}

		if !cg.isSupported(op) {
			if callExitSet[pc] {
				// Call-exit: spill state, exit with code 2, emit resume label.
				cg.spillPinnedRegs()
				cg.asm.LoadImm64(X1, int64(pc))
				cg.asm.STR(X1, regCtx, ctxOffExitPC)
				cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit, resumable)
				cg.asm.B("epilogue")

				// Resume label: re-entry point after executor handles the instruction.
				cg.asm.Label(resumeLabel(pc))
				cg.asm.LDR(regRegs, regCtx, ctxOffRegs) // reload in case regs were reallocated
				cg.reloadPinnedRegs()
				continue // next pc label is emitted by the loop
			}

			// Permanent side exit for unsupported ops that can't be call-exited.
			cg.spillPinnedRegs()
			cg.asm.LoadImm64(X1, int64(pc))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
			continue
		}

		if err := cg.emitInstruction(pc, inst); err != nil {
			return fmt.Errorf("pc %d: %w", pc, err)
		}

		// For ops with native fast path + call-exit fallback,
		// emit the resume label after the native code. The fast path
		// must skip the resume reload (which would corrupt pinned regs).
		if (op == vm.OP_GETFIELD || op == vm.OP_GETTABLE) && callExitSet[pc] {
			skipLabel := fmt.Sprintf("skip_resume_%d", pc)
			cg.asm.B(skipLabel)           // fast path: skip resume reload
			cg.asm.Label(resumeLabel(pc)) // call-exit resume entry
			cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
			cg.reloadPinnedRegs()
			cg.asm.Label(skipLabel)
		}
	}

	return nil
}

// isSupported returns true if the opcode can be compiled directly to native code.
// Note: GETGLOBAL, SETGLOBAL, and CALL are handled via call-exit (ExitCode=2)
// and are NOT listed here — they go through the call-exit path in emitBody.
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
		vm.OP_GETFIELD,
		vm.OP_GETTABLE:
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
		return cg.emitNOT(pc, inst)
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
	case vm.OP_GETFIELD:
		return cg.emitGetField(pc, inst)
	case vm.OP_GETTABLE:
		return cg.emitGetTable(pc, inst)
	case vm.OP_RETURN:
		return cg.emitReturnOp(pc, inst)
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

	srcArm, srcPinned := cg.pinnedRegs[bReg]
	dstArm, dstPinned := cg.pinnedRegs[aReg]

	if srcPinned && dstPinned {
		// Both pinned: register-to-register move.
		if srcArm != dstArm {
			cg.asm.MOVreg(dstArm, srcArm)
		}
	} else if srcPinned {
		// Source pinned, dest in memory: write int value.
		cg.asm.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
		cg.asm.STR(srcArm, regRegs, regIvalOffset(aReg))
	} else if dstPinned {
		// Dest pinned, source in memory: load ival into ARM reg.
		cg.asm.LDR(dstArm, regRegs, regIvalOffset(bReg))
	} else {
		cg.copyValue(aReg, bReg)
	}
	return nil
}

// emitArithInt emits integer arithmetic with type guards.
// Type guards are skipped for operands known to be TypeInt (type guard hoisting).
func (cg *Codegen) emitArithInt(pc int, inst uint32, arithOp string) error {
	aReg := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needGuardB := !bKnown
	needGuardC := !cKnown

	if needGuardB || needGuardC {
		exitLabel := fmt.Sprintf("arith_exit_%d", pc)
		if needGuardB {
			cg.loadRKTyp(X0, bIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if needGuardC {
			cg.loadRKTyp(X0, cIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}

		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		switch arithOp {
		case "ADD":
			cg.asm.ADDreg(X0, X0, X1)
		case "SUB":
			cg.asm.SUBreg(X0, X0, X1)
		case "MUL":
			cg.asm.MUL(X0, X0, X1)
		}
		cg.storeIntValue(aReg, X0)

		after := fmt.Sprintf("arith_done_%d", pc)
		cg.asm.B(after)

		cg.asm.Label(exitLabel)
		cg.spillPinnedRegs()
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")

		cg.asm.Label(after)
	} else {
		// Both operands known TypeInt — no type guards needed.
		// Try direct register-register operation if all operands are pinned.
		aArm, aPinned := cg.pinnedRegs[aReg]
		var bArm, cArm Reg
		var bPinned, cPinned bool
		if !vm.IsRK(bIdx) {
			bArm, bPinned = cg.pinnedRegs[bIdx]
		}
		if !vm.IsRK(cIdx) {
			cArm, cPinned = cg.pinnedRegs[cIdx]
		}

		if aPinned && bPinned && cPinned {
			// All three in ARM registers — emit single instruction.
			switch arithOp {
			case "ADD":
				cg.asm.ADDreg(aArm, bArm, cArm)
			case "SUB":
				cg.asm.SUBreg(aArm, bArm, cArm)
			case "MUL":
				cg.asm.MUL(aArm, bArm, cArm)
			}
		} else if (arithOp == "ADD" || arithOp == "SUB") && cg.rkSmallIntConst(cIdx) >= 0 {
			// ADD/SUB with small integer constant: use immediate form.
			// SUB R(A), R(B), K(imm) → SUBimm X0, X0, #imm
			imm := cg.rkSmallIntConst(cIdx)
			cg.loadRKIval(X0, bIdx)
			switch arithOp {
			case "ADD":
				cg.asm.ADDimm(X0, X0, uint16(imm))
			case "SUB":
				cg.asm.SUBimm(X0, X0, uint16(imm))
			}
			cg.storeIntValue(aReg, X0)
		} else if (arithOp == "ADD" || arithOp == "SUB") && arithOp == "ADD" && cg.rkSmallIntConst(bIdx) >= 0 {
			// ADD R(A), K(imm), R(C) → ADDimm X0, X0, #imm (commutative)
			imm := cg.rkSmallIntConst(bIdx)
			cg.loadRKIval(X0, cIdx)
			cg.asm.ADDimm(X0, X0, uint16(imm))
			cg.storeIntValue(aReg, X0)
		} else {
			// Fallback: load through X0/X1.
			cg.loadRKIval(X0, bIdx)
			cg.loadRKIval(X1, cIdx)
			switch arithOp {
			case "ADD":
				cg.asm.ADDreg(X0, X0, X1)
			case "SUB":
				cg.asm.SUBreg(X0, X0, X1)
			case "MUL":
				cg.asm.MUL(X0, X0, X1)
			}
			cg.storeIntValue(aReg, X0)
		}
	}
	return nil
}

func (cg *Codegen) emitUNM(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	bKnown := cg.isRKKnownInt(pc, bReg)

	if !bKnown {
		exitLabel := fmt.Sprintf("unm_exit_%d", pc)
		cg.loadRegTyp(X0, bReg)
		cg.asm.CMPimmW(X0, TypeInt)
		cg.asm.BCond(CondNE, exitLabel)

		cg.loadRegIval(X0, bReg)
		cg.asm.NEG(X0, X0)
		cg.storeIntValue(aReg, X0)

		after := fmt.Sprintf("unm_done_%d", pc)
		cg.asm.B(after)

		cg.asm.Label(exitLabel)
		cg.spillPinnedRegs()
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")

		cg.asm.Label(after)
	} else {
		cg.loadRegIval(X0, bReg)
		cg.asm.NEG(X0, X0)
		cg.storeIntValue(aReg, X0)
	}
	return nil
}

func (cg *Codegen) emitNOT(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	bReg := vm.DecodeB(inst)

	// NOT: R(A) = !truthy(R(B))
	// Truthy: nil and false are falsy, everything else truthy.
	// TypeNil (0) = falsy. TypeBool (1) with ival==0 = falsy. Otherwise truthy.

	cg.loadRegTyp(X0, bReg)

	// Check if nil (typ == 0)
	cg.asm.CMPimmW(X0, TypeNil)
	cg.asm.BCond(CondEQ, fmt.Sprintf("not_true_%d", pc))

	// Check if false bool (typ == 1 && ival == 0)
	cg.asm.CMPimmW(X0, TypeBool)
	cg.asm.BCond(CondNE, fmt.Sprintf("not_false_%d", pc))
	cg.loadRegIval(X0, bReg)
	cg.asm.CMPimm(X0, 0)
	cg.asm.BCond(CondEQ, fmt.Sprintf("not_true_%d", pc))

	// Truthy → NOT = false
	cg.asm.Label(fmt.Sprintf("not_false_%d", pc))
	cg.asm.LoadImm64(X0, 0)
	cg.storeBoolValue(aReg, X0)
	cg.asm.B(fmt.Sprintf("not_done_%d", pc))

	// Falsy → NOT = true
	cg.asm.Label(fmt.Sprintf("not_true_%d", pc))
	cg.asm.LoadImm64(X0, 1)
	cg.storeBoolValue(aReg, X0)

	cg.asm.Label(fmt.Sprintf("not_done_%d", pc))
	return nil
}

// emitEQ: if (RK(B) == RK(C)) != bool(A) then PC++ (skip next instruction)
func (cg *Codegen) emitEQ(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needExit := !bKnown || !cKnown

	var exitLabel string
	if needExit {
		exitLabel = fmt.Sprintf("eq_exit_%d", pc)
		if !bKnown {
			cg.loadRKTyp(X0, bIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if !cKnown {
			cg.loadRKTyp(X0, cIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
	}

	// Use CMP immediate when one operand is a small non-negative integer constant.
	// EQ is symmetric so no condition reversal needed.
	if cImm := cg.rkSmallIntConst(cIdx); cImm >= 0 {
		cg.loadRKIval(X0, bIdx)
		cg.asm.CMPimm(X0, uint16(cImm))
	} else if bImm := cg.rkSmallIntConst(bIdx); bImm >= 0 {
		cg.loadRKIval(X0, cIdx)
		cg.asm.CMPimm(X0, uint16(bImm))
	} else {
		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		cg.asm.CMPreg(X0, X1)
	}

	if aFlag != 0 {
		cg.asm.BCond(CondNE, skipLabel)
	} else {
		cg.asm.BCond(CondEQ, skipLabel)
	}

	if needExit {
		return cg.emitComparisonSideExit(pc, exitLabel)
	}
	return nil
}

// emitLT: if (RK(B) < RK(C)) != bool(A) then PC++
func (cg *Codegen) emitLT(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needExit := !bKnown || !cKnown

	var exitLabel string
	if needExit {
		exitLabel = fmt.Sprintf("lt_exit_%d", pc)
		if !bKnown {
			cg.loadRKTyp(X0, bIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if !cKnown {
			cg.loadRKTyp(X0, cIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
	}

	// Use CMP immediate when one operand is a small non-negative integer constant.
	if cImm := cg.rkSmallIntConst(cIdx); cImm >= 0 {
		cg.loadRKIval(X0, bIdx)
		cg.asm.CMPimm(X0, uint16(cImm))
	} else if bImm := cg.rkSmallIntConst(bIdx); bImm >= 0 {
		// B < C with B constant: load C, compare reversed.
		// B < C ⟺ C > B, so flip the condition.
		cg.loadRKIval(X0, cIdx)
		cg.asm.CMPimm(X0, uint16(bImm))
		// Reverse the condition: instead of checking B < C, we check C > B.
		if aFlag != 0 {
			// Original: skip if NOT (B < C), i.e., B >= C → with reversal: skip if C <= B
			cg.asm.BCond(CondLE, skipLabel)
		} else {
			// Original: skip if (B < C) → with reversal: skip if C > B
			cg.asm.BCond(CondGT, skipLabel)
		}
		if needExit {
			return cg.emitComparisonSideExit(pc, exitLabel)
		}
		return nil
	} else {
		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		cg.asm.CMPreg(X0, X1)
	}

	if aFlag != 0 {
		cg.asm.BCond(CondGE, skipLabel)
	} else {
		cg.asm.BCond(CondLT, skipLabel)
	}

	if needExit {
		return cg.emitComparisonSideExit(pc, exitLabel)
	}
	return nil
}

// emitLE: if (RK(B) <= RK(C)) != bool(A) then PC++
// Note: the VM implements LE as !(C < B).
func (cg *Codegen) emitLE(pc int, inst uint32) error {
	aFlag := vm.DecodeA(inst)
	bIdx := vm.DecodeB(inst)
	cIdx := vm.DecodeC(inst)

	skipLabel := pcLabel(pc + 2)

	bKnown := cg.isRKKnownInt(pc, bIdx)
	cKnown := cg.isRKKnownInt(pc, cIdx)
	needExit := !bKnown || !cKnown

	var exitLabel string
	if needExit {
		exitLabel = fmt.Sprintf("le_exit_%d", pc)
		if !bKnown {
			cg.loadRKTyp(X0, bIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
		if !cKnown {
			cg.loadRKTyp(X0, cIdx)
			cg.asm.CMPimmW(X0, TypeInt)
			cg.asm.BCond(CondNE, exitLabel)
		}
	}

	// Use CMP immediate when one operand is a small non-negative integer constant.
	if cImm := cg.rkSmallIntConst(cIdx); cImm >= 0 {
		// B <= C with C as immediate: CMP B, #C then check LE.
		cg.loadRKIval(X0, bIdx)
		cg.asm.CMPimm(X0, uint16(cImm))
	} else if bImm := cg.rkSmallIntConst(bIdx); bImm >= 0 {
		// B <= C with B constant: CMP C, #B, then reverse condition.
		// B <= C ⟺ C >= B
		cg.loadRKIval(X0, cIdx)
		cg.asm.CMPimm(X0, uint16(bImm))
		if aFlag != 0 {
			// Original: skip if NOT (B <= C), i.e., B > C → with reversal: skip if C < B
			cg.asm.BCond(CondLT, skipLabel)
		} else {
			// Original: skip if (B <= C) → with reversal: skip if C >= B
			cg.asm.BCond(CondGE, skipLabel)
		}
		if needExit {
			return cg.emitComparisonSideExit(pc, exitLabel)
		}
		return nil
	} else {
		cg.loadRKIval(X0, bIdx)
		cg.loadRKIval(X1, cIdx)
		cg.asm.CMPreg(X0, X1)
	}

	if aFlag != 0 {
		cg.asm.BCond(CondGT, skipLabel)
	} else {
		cg.asm.BCond(CondLE, skipLabel)
	}

	if needExit {
		return cg.emitComparisonSideExit(pc, exitLabel)
	}
	return nil
}

func (cg *Codegen) emitComparisonSideExit(pc int, exitLabel string) error {
	after := fmt.Sprintf("cmp_done_%d", pc)
	cg.asm.B(after)

	cg.asm.Label(exitLabel)
	cg.spillPinnedRegs()
	cg.asm.LoadImm64(X1, int64(pc))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 2) // call-exit: executor handles non-integer comparison and resumes
	cg.asm.B("epilogue")

	// Defer resume stubs with captured pinning state.
	// These will be emitted after the main instruction loop.
	cmpStub := fmt.Sprintf("cmp_resume_%d", pc)
	// Capture current pinning state.
	capturedVars := make([]int, len(cg.pinnedVars))
	copy(capturedVars, cg.pinnedVars)
	capturedRegs := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedRegs[k] = v
	}
	cg.cmpResumeStubs = append(cg.cmpResumeStubs,
		cmpResumeStub{cmpStub + "_1", pcLabel(pc + 1), capturedVars, capturedRegs},
		cmpResumeStub{cmpStub + "_2", pcLabel(pc + 2), capturedVars, capturedRegs},
	)

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

	// Emit side-exit BEFORE setting up pinning (at runtime, the exit path
	// skips the pinning loads, so we must not emit spill code here).
	guardsOK := fmt.Sprintf("forprep_ok_%d", pc)
	cg.asm.B(guardsOK)

	cg.asm.Label(exitLabel)
	cg.asm.LoadImm64(X1, int64(pc))
	cg.asm.STR(X1, regCtx, ctxOffExitPC)
	cg.asm.LoadImm64(X0, 1)
	cg.asm.B("epilogue")

	cg.asm.Label(guardsOK)

	// Set up register pinning if this loop was analyzed and is innermost.
	desc := cg.forLoops[pc]
	if desc != nil && desc.canPin {
		cg.setupLoopPinning(desc)

		// Pre-set R(A+3).typ = TypeInt in memory (once, before loop).
		cg.asm.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg+3)

		// Also set typ for body accumulators (they're pinned, typ won't be written in body).
		for _, vmReg := range desc.bodyAccumulators {
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}

		// R(A) -= R(A+2) using pinned registers.
		idxReg := cg.pinnedRegs[aReg]
		stepReg := cg.pinnedRegs[aReg+2]
		cg.asm.SUBreg(idxReg, idxReg, stepReg)
	} else {
		// Fallback: no pinning.
		cg.loadRegIval(X0, aReg)
		cg.loadRegIval(X1, aReg+2)
		cg.asm.SUBreg(X0, X0, X1)
		cg.storeRegIval(X0, aReg)
	}

	// Jump to FORLOOP (pc + 1 + sbx)
	target := pc + 1 + sbx
	cg.asm.B(pcLabel(target))
	return nil
}

// emitForLoop: R(A) += R(A+2); if in range: R(A+3) = R(A), PC += sBx.
// Optimized with register pinning and step-sign specialization.
func (cg *Codegen) emitForLoop(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	desc := cg.forLoops[pc]
	loopBody := pcLabel(pc + 1 + sbx)
	exitFor := fmt.Sprintf("forloop_exit_%d", pc)

	if desc != nil && len(cg.pinnedRegs) > 0 {
		idxReg := cg.pinnedRegs[aReg]
		limitReg := cg.pinnedRegs[aReg+1]
		stepReg := cg.pinnedRegs[aReg+2]
		loopVarReg := cg.pinnedRegs[aReg+3]

		if desc.stepKnown && desc.stepValue > 0 {
			// Optimized: known positive step — bottom-tested loop.
			if desc.stepValue == 1 {
				cg.asm.ADDimm(idxReg, idxReg, 1)
			} else {
				cg.asm.ADDreg(idxReg, idxReg, stepReg)
			}
			cg.asm.CMPreg(idxReg, limitReg)

			if loopVarReg != idxReg {
				cg.asm.MOVreg(loopVarReg, idxReg)
			}
			// Conditional back-edge: continue if idx <= limit.
			cg.asm.BCond(CondLE, loopBody)
		} else if desc.stepKnown && desc.stepValue < 0 {
			// Optimized: known negative step — bottom-tested loop.
			if desc.stepValue == -1 {
				cg.asm.SUBimm(idxReg, idxReg, 1)
			} else {
				cg.asm.ADDreg(idxReg, idxReg, stepReg)
			}
			cg.asm.CMPreg(idxReg, limitReg)

			if loopVarReg != idxReg {
				cg.asm.MOVreg(loopVarReg, idxReg)
			}
			// Conditional back-edge: continue if idx >= limit.
			cg.asm.BCond(CondGE, loopBody)
		} else {
			// Unknown step sign: general path with pinned registers.
			cg.asm.ADDreg(idxReg, idxReg, stepReg)

			cg.asm.CMPimm(stepReg, 0)
			negStep := fmt.Sprintf("forloop_neg_%d", pc)
			cg.asm.BCond(CondLT, negStep)

			cg.asm.CMPreg(idxReg, limitReg)
			cg.asm.BCond(CondGT, exitFor)
			cg.asm.B(fmt.Sprintf("forloop_cont_%d", pc))

			cg.asm.Label(negStep)
			cg.asm.CMPreg(idxReg, limitReg)
			cg.asm.BCond(CondLT, exitFor)

			cg.asm.Label(fmt.Sprintf("forloop_cont_%d", pc))
			if loopVarReg != idxReg {
				cg.asm.MOVreg(loopVarReg, idxReg)
			}
			cg.asm.B(loopBody)
		}

		// Loop exit: spill pinned registers back to memory and clear pinning.
		cg.asm.Label(exitFor)
		cg.spillPinnedRegs()
		cg.clearPinning()
	} else {
		// Fallback: no pinning (original code).
		cg.loadRegIval(X0, aReg)
		cg.loadRegIval(X1, aReg+2)
		cg.asm.ADDreg(X0, X0, X1)
		cg.storeRegIval(X0, aReg)

		cg.loadRegIval(X2, aReg+1)

		cg.asm.CMPimm(X1, 0)
		negStep := fmt.Sprintf("forloop_neg_%d", pc)
		cg.asm.BCond(CondLT, negStep)

		cg.asm.CMPreg(X0, X2)
		cg.asm.BCond(CondGT, exitFor)
		cg.asm.B(fmt.Sprintf("forloop_cont_%d", pc))

		cg.asm.Label(negStep)
		cg.asm.CMPreg(X0, X2)
		cg.asm.BCond(CondLT, exitFor)

		cg.asm.Label(fmt.Sprintf("forloop_cont_%d", pc))
		cg.storeIntValue(aReg+3, X0)
		cg.asm.B(loopBody)

		cg.asm.Label(exitFor)
	}
	return nil
}

// emitGetField compiles OP_GETFIELD R(A) = R(B).Constants[C] natively.
// Fast path: R(B) is TypeTable, no metatable, key found in flat skeys.
// Slow path: falls through to call-exit for non-table, metatable, or smap cases.
func (cg *Codegen) emitGetField(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)
	asm := cg.asm

	// Label for call-exit fallback
	fallbackLabel := fmt.Sprintf("getfield_fallback_%d", pc)
	doneLabel := fmt.Sprintf("getfield_done_%d", pc)

	// --- Step 1: Type check R(B).typ == TypeTable ---
	bTypOff := regTypOffset(b)
	if bTypOff <= 4095 {
		asm.LDRB(X0, regRegs, bTypOff)
	} else {
		asm.LoadImm64(X0, int64(bTypOff))
		asm.LDRBreg(X0, regRegs, X0)
	}
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// --- Step 2: Load *Table from R(B).ptr.data ---
	bPtrDataOff := b*ValueSize + OffsetPtrData
	if bPtrDataOff <= 32760 {
		asm.LDR(X0, regRegs, bPtrDataOff) // X0 = *Table
	} else {
		asm.LoadImm64(X1, int64(bPtrDataOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	asm.CBZ(X0, fallbackLabel) // nil table check

	// --- Step 3: Check metatable == nil ---
	asm.LDR(X1, X0, TableOffMetatable) // X1 = table.metatable
	asm.CBNZ(X1, fallbackLabel)        // has metatable → fallback

	// --- Step 4: Load skeys slice (ptr, len) ---
	asm.LDR(X1, X0, TableOffSkeys)    // X1 = skeys base pointer
	asm.LDR(X2, X0, TableOffSkeysLen) // X2 = skeys.len
	asm.CBZ(X2, fallbackLabel)         // no skeys → fallback (might be in smap)

	// Save table pointer for later svals access
	asm.MOVreg(X9, X0) // X9 = *Table (preserved)

	// --- Step 5: Load constant key string ---
	// Constants[C].ptr is a string stored in an `any` interface.
	// Interface data pointer (the boxed string header) is at offset OffsetPtrData.
	cPtrDataOff := c*ValueSize + OffsetPtrData
	if cPtrDataOff <= 32760 {
		asm.LDR(X3, regConsts, cPtrDataOff) // X3 = pointer to string header
	} else {
		asm.LoadImm64(X4, int64(cPtrDataOff))
		asm.ADDreg(X4, regConsts, X4)
		asm.LDR(X3, X4, 0)
	}
	asm.LDR(X4, X3, 0) // X4 = key string data ptr
	asm.LDR(X5, X3, 8) // X5 = key string len

	// --- Step 6: Linear scan of skeys ---
	loopLabel := fmt.Sprintf("getfield_scan_%d", pc)
	nextLabel := fmt.Sprintf("getfield_next_%d", pc)
	foundLabel := fmt.Sprintf("getfield_found_%d", pc)
	cmpLoopLabel := fmt.Sprintf("getfield_cmp_%d", pc)

	asm.LoadImm64(X6, 0) // X6 = i = 0

	asm.Label(loopLabel)
	asm.CMPreg(X6, X2) // i >= skeys.len?
	asm.BCond(CondGE, fallbackLabel)

	// Load skeys[i]: string at X1 + i*16
	asm.LSLimm(X7, X6, 4)   // X7 = i * 16
	asm.ADDreg(X7, X1, X7)  // X7 = &skeys[i]
	asm.LDR(X10, X7, 0)     // X10 = skeys[i].ptr
	asm.LDR(X11, X7, 8)     // X11 = skeys[i].len

	// Compare lengths first (fast reject)
	asm.CMPreg(X11, X5) // skeys[i].len == key.len?
	asm.BCond(CondNE, nextLabel)

	// Compare data pointers (fast accept for interned strings)
	asm.CMPreg(X10, X4) // same pointer?
	asm.BCond(CondEQ, foundLabel)

	// Byte-by-byte comparison for non-interned strings
	asm.LoadImm64(X12, 0) // j = 0
	asm.Label(cmpLoopLabel)
	asm.CMPreg(X12, X5) // j >= len?
	asm.BCond(CondGE, foundLabel)
	asm.LDRBreg(X13, X10, X12) // skeys[i].ptr[j]
	asm.LDRBreg(X14, X4, X12)  // key.ptr[j]
	asm.CMPreg(X13, X14)
	asm.BCond(CondNE, nextLabel)
	asm.ADDimm(X12, X12, 1)
	asm.B(cmpLoopLabel)

	asm.Label(nextLabel)
	asm.ADDimm(X6, X6, 1) // i++
	asm.B(loopLabel)

	// --- Step 7: Found - load svals[i] into R(A) ---
	asm.Label(foundLabel)
	// svals base is at Table + TableOffSvals
	asm.LDR(X7, X9, TableOffSvals) // X7 = svals base pointer
	// svals[i] is at X7 + i * ValueSize (32)
	asm.LSLimm(X8, X6, 5)   // X8 = i * 32
	asm.ADDreg(X7, X7, X8)  // X7 = &svals[i]

	// Copy Value (32 bytes = 4 words) from svals[i] to R(A)
	aOff := a * ValueSize
	for w := 0; w < 4; w++ {
		asm.LDR(X0, X7, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	asm.B(doneLabel)

	// --- Fallback: call-exit ---
	asm.Label(fallbackLabel)
	cg.spillPinnedRegs()
	asm.LoadImm64(X1, int64(pc))
	asm.STR(X1, regCtx, ctxOffExitPC)
	asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit)
	asm.B("epilogue")

	asm.Label(doneLabel)
	return nil
}

// emitGetTable compiles OP_GETTABLE R(A) = R(B)[RK(C)] natively.
// Fast path: R(B) is TypeTable, no metatable, RK(C) is TypeInt, key in array range.
// Slow path: call-exit for non-table, metatable, non-int keys, or imap.
func (cg *Codegen) emitGetTable(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)
	asm := cg.asm

	fallbackLabel := fmt.Sprintf("gettable_fallback_%d", pc)
	doneLabel := fmt.Sprintf("gettable_done_%d", pc)

	// --- Step 1: Type check R(B).typ == TypeTable ---
	bTypOff := regTypOffset(b)
	if bTypOff <= 4095 {
		asm.LDRB(X0, regRegs, bTypOff)
	} else {
		asm.LoadImm64(X0, int64(bTypOff))
		asm.LDRBreg(X0, regRegs, X0)
	}
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// --- Step 2: Load *Table from R(B).ptr.data ---
	bPtrDataOff := b*ValueSize + OffsetPtrData
	if bPtrDataOff <= 32760 {
		asm.LDR(X0, regRegs, bPtrDataOff)
	} else {
		asm.LoadImm64(X1, int64(bPtrDataOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	asm.CBZ(X0, fallbackLabel)

	// --- Step 3: Check metatable == nil ---
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// --- Step 4: Load key from RK(C) ---
	// Check key type == TypeInt
	var keyTypOff, keyDataOff int
	if cidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(cidx)
		keyTypOff = constIdx*ValueSize + OffsetTyp
		keyDataOff = constIdx*ValueSize + OffsetData
		// Load from constants
		if keyTypOff <= 4095 {
			asm.LDRB(X2, regConsts, keyTypOff)
		} else {
			asm.LoadImm64(X2, int64(keyTypOff))
			asm.LDRBreg(X2, regConsts, X2)
		}
		asm.CMPimmW(X2, TypeInt)
		asm.BCond(CondNE, fallbackLabel)
		if keyDataOff <= 32760 {
			asm.LDR(X2, regConsts, keyDataOff) // X2 = key int value
		} else {
			asm.LoadImm64(X3, int64(keyDataOff))
			asm.ADDreg(X3, regConsts, X3)
			asm.LDR(X2, X3, 0)
		}
	} else {
		keyTypOff = regTypOffset(cidx)
		keyDataOff = cidx*ValueSize + OffsetData
		if keyTypOff <= 4095 {
			asm.LDRB(X3, regRegs, keyTypOff)
		} else {
			asm.LoadImm64(X3, int64(keyTypOff))
			asm.LDRBreg(X3, regRegs, X3)
		}
		asm.CMPimmW(X3, TypeInt)
		asm.BCond(CondNE, fallbackLabel)
		if keyDataOff <= 32760 {
			asm.LDR(X2, regRegs, keyDataOff) // X2 = key int value
		} else {
			asm.LoadImm64(X3, int64(keyDataOff))
			asm.ADDreg(X3, regRegs, X3)
			asm.LDR(X2, X3, 0)
		}
	}

	// --- Step 5: Array bounds check ---
	// array is at Table + TableOffArray: {ptr(8), len(8), cap(8)}
	// Check: key >= 1 && key < array.len
	asm.CMPimm(X2, 1) // key >= 1?
	asm.BCond(CondLT, fallbackLabel)

	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key < array.len?
	asm.BCond(CondGE, fallbackLabel)

	// --- Step 6: Load array[key] (Value at array.ptr + key * ValueSize) ---
	asm.LDR(X3, X0, TableOffArray) // X3 = array.ptr
	asm.LSLimm(X4, X2, 5)          // X4 = key * 32 (ValueSize)
	asm.ADDreg(X3, X3, X4)         // X3 = &array[key]

	aOff := a * ValueSize
	for w := 0; w < 4; w++ {
		asm.LDR(X0, X3, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	asm.B(doneLabel)

	// --- Fallback: call-exit ---
	asm.Label(fallbackLabel)
	cg.spillPinnedRegs()
	asm.LoadImm64(X1, int64(pc))
	asm.STR(X1, regCtx, ctxOffExitPC)
	asm.LoadImm64(X0, 2)
	asm.B("epilogue")

	asm.Label(doneLabel)
	return nil
}

func (cg *Codegen) emitReturnOp(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	// Spill pinned registers before returning (return values must be in memory).
	if len(cg.pinnedRegs) > 0 {
		cg.spillPinnedRegs()
	}

	if cg.hasSelfCalls {
		return cg.emitSelfCallReturn(pc, aReg, b)
	}

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

// emitSelfCallReturn handles RETURN for functions with self-recursive calls.
// If X25 > 0 (we're inside a self-call), return via RET with result in X0.
// If X25 == 0 (outermost call), write to JITContext and go to epilogue.
func (cg *Codegen) emitSelfCallReturn(pc, aReg, b int) error {
	a := cg.asm

	if b == 0 {
		// Variable return count (RETURN A B=0).
		// In self-call functions, treat as a single-value return at all depths.
		// The JIT doesn't maintain vm.top, so we can't side-exit to the interpreter
		// for variable returns — it would compute a wrong return count.
		// Self-call functions only return single values through the JIT path anyway.
		cg.loadRegIval(X0, aReg)
		outerVarLabel := fmt.Sprintf("outermost_varret_%d", pc)
		a.CBZ(regSelfDepth, outerVarLabel)
		a.RET() // nested self-call: return single value in X0

		// Outermost: return single value via JITContext.
		// Write type tag for the return register so the executor reads a valid Value.
		a.Label(outerVarLabel)
		a.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
		a.LoadImm64(X1, int64(aReg))
		a.STR(X1, regCtx, ctxOffRetBase)
		a.LoadImm64(X1, 1) // 1 return value
		a.STR(X1, regCtx, ctxOffRetCount)
		a.LoadImm64(X0, 0) // ExitCode = 0 (normal return)
		a.B("epilogue")
		return nil
	}

	nret := 0
	if b > 1 {
		nret = b - 1
		// Load first return value into X0.
		cg.loadRegIval(X0, aReg)
	} else {
		// Return nothing — X0 = 0.
		a.LoadImm64(X0, 0)
	}

	// Check depth: if > 0, this is a nested self-call return.
	outerLabel := fmt.Sprintf("outermost_ret_%d", pc)
	a.CBZ(regSelfDepth, outerLabel)
	// Self-call return: X0 = result, just RET back to BL caller.
	a.RET()

	// Outermost return: write type tag for the return register so the executor
	// reads a valid Value from the register array.
	a.Label(outerLabel)
	if nret > 0 {
		a.MOVimm16W(X9, TypeInt)
		cg.storeRegTyp(X9, aReg)
	}
	a.LoadImm64(X1, int64(aReg))
	a.STR(X1, regCtx, ctxOffRetBase)
	a.LoadImm64(X1, int64(nret))
	a.STR(X1, regCtx, ctxOffRetCount)
	a.LoadImm64(X0, 0)
	a.B("epilogue")
	return nil
}

// Maximum self-recursion depth before falling back to interpreter.
const maxSelfRecursionDepth = 200

// emitSelfCall emits native ARM64 code for a self-recursive function call.
// Saves only LR (x30) on the ARM64 stack (16 bytes for alignment). regRegs (x26)
// is restored by subtraction after the call. The depth counter (x25) is managed
// via increment/decrement without save/restore.
func (cg *Codegen) emitSelfCall(pc int, candidate *inlineCandidate) error {
	a := cg.asm
	fnReg := candidate.fnReg

	overflowLabel := fmt.Sprintf("self_overflow_%d", pc)
	doneLabel := fmt.Sprintf("self_done_%d", pc)

	// Increment depth counter (before stack push, so overflow unwind is simpler).
	a.ADDimm(regSelfDepth, regSelfDepth, 1)

	// Check depth limit — side exit if too deep.
	a.CMPimm(regSelfDepth, maxSelfRecursionDepth)
	a.BCond(CondGE, overflowLabel)

	// Save only LR (x30). SP must remain 16-byte aligned, so we push 16 bytes.
	a.STRpre(X30, SP, -16) // [SP] = X30; SP -= 16

	// Advance regRegs to callee's register window.
	// Callee's R(0) = Caller's R(fnReg+1).
	offset := (fnReg + 1) * ValueSize
	if offset <= 4095 {
		a.ADDimm(regRegs, regRegs, uint16(offset))
	} else {
		a.LoadImm64(X0, int64(offset))
		a.ADDreg(regRegs, regRegs, X0)
	}

	// Argument is already at callee's R(0) because:
	// caller's R(fnReg+1) = callee's R(0) after advancing regRegs.

	// BL to self_call_entry (re-enters the function body).
	a.BL("self_call_entry")

	// After return: X0 = result (ival).
	// Restore LR from stack.
	a.LDRpost(X30, SP, 16) // restore X30; SP += 16

	// Restore regRegs by subtracting the offset (avoids saving/restoring x26).
	if offset <= 4095 {
		a.SUBimm(regRegs, regRegs, uint16(offset))
	} else {
		// Use X1 as scratch since X0 holds the result.
		a.LoadImm64(X1, int64(offset))
		a.SUBreg(regRegs, regRegs, X1)
	}

	a.SUBimm(regSelfDepth, regSelfDepth, 1) // depth--

	// Store result to R(fnReg) in caller's register window.
	// Type tag write is skipped: self-call functions guarantee all values are TypeInt
	// (parameter guard at outermost entry + int-only arithmetic).
	a.STR(X0, regRegs, regIvalOffset(fnReg))

	a.B(doneLabel)

	// Overflow handler: unwind all self-call frames at once.
	// X29 was set to SP in the prologue and is never modified by self-call pushes,
	// so restoring SP from X29 unwinds all 16-byte self-call frames.
	a.Label(overflowLabel)
	a.MOVreg(SP, X29)                       // unwind all self-call stack frames
	a.LDR(regRegs, regCtx, ctxOffRegs)      // restore original regRegs from context
	a.MOVimm16(regSelfDepth, 0)             // reset depth
	a.LoadImm64(X1, int64(candidate.getglobalPC))
	a.STR(X1, regCtx, ctxOffExitPC)
	a.LoadImm64(X0, 1) // side exit
	a.B("epilogue")

	a.Label(doneLabel)
	return nil
}
