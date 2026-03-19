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
	Top       int64   // output: register index past last used register (for variable returns, C=0)
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
	ctxOffTop       = 56
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
	if unsafe.Offsetof(ctx.Top) != ctxOffTop {
		panic("jit: JITContext.Top offset mismatch")
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

// inlineArgTrace records traced argument sources for inline optimization.
type inlineArgTrace struct {
	fromReg   int   // source register (if traced through MOVE)
	fromConst int64 // constant value (if traced through LOADINT)
	isConst   bool  // true if fromConst is valid
	traced    bool  // true if successfully traced
	setupPC   int   // PC of the MOVE/LOADINT that set up this arg (for skipping)
}

// inlineCandidate describes a GETGLOBAL + CALL pattern that can be inlined.
type inlineCandidate struct {
	getglobalPC int              // PC of GETGLOBAL instruction
	callPC      int              // PC of CALL instruction
	callee      *vm.FuncProto    // the function to inline
	fnReg       int              // register holding the function (A field of GETGLOBAL/CALL)
	nArgs       int              // number of arguments
	nResults    int              // number of expected results
	isSelfCall  bool             // true if this is a self-recursive call
	argTraces   []inlineArgTrace // traced argument sources (populated during analysis)
	resultDest  int              // final destination register for result (-1 if no tracing)
	resultMovePC int             // PC of the MOVE that copies result to resultDest (-1 if none)
}

// crossCallInfo describes a detected cross-call pattern for direct BLR optimization.
type crossCallInfo struct {
	getglobalPC int              // PC of GETGLOBAL instruction
	callPC      int              // PC of CALL instruction
	calleeName  string           // name of the callee function
	calleeProto *vm.FuncProto    // callee's proto (if found in globals)
	fnReg       int              // register holding the function
	nArgs       int              // number of arguments (from B field)
	nResults    int              // number of expected results (from C field)
	slot        *crossCallSlot   // slot holding callee's code pointer (filled during compilation)
}

// Codegen translates a FuncProto's bytecode to ARM64 machine code.
type Codegen struct {
	asm              *Assembler
	proto            *vm.FuncProto
	globals          map[string]rt.Value     // VM globals for function resolution
	engine           *Engine                 // JIT engine for cross-call slot allocation
	knownInt         []regSet                // per-PC: bitmask of registers known to be TypeInt
	reachable        []bool                  // per-PC: has been reached by data-flow analysis
	forLoops         map[int]*forLoopDesc    // keyed by forloopPC
	pinnedRegs       map[int]Reg             // VM register → ARM register (active during loop body)
	pinnedVars       []int                   // ordered list of pinned VM registers (for spilling)
	inlineCandidates map[int]*inlineCandidate // keyed by CALL PC
	inlineSkipPCs    map[int]bool            // GETGLOBAL PCs to skip (part of inline pattern)
	inlineArgSkipPCs map[int]bool            // arg setup PCs to skip (traced through by inline)
	hasSelfCalls     bool                    // true if function has self-recursive calls
	crossCalls       map[int]*crossCallInfo  // keyed by CALL PC (for direct BLR optimization)
	crossCallSkipPCs map[int]bool            // GETGLOBAL PCs to skip (part of cross-call pattern)
	callExitPCs      []int                   // PCs that use call-exit (ExitCode=2) for resume dispatch
	cmpCallExitPCs   []int                   // PCs of comparison ops with non-int type guards (call-exit on guard fail)
	cmpResumeStubs   []cmpResumeStub         // resume stubs for comparison call-exits (deferred)
	coldStubs        []coldStub              // deferred cold code stubs (emitted after hot path)
}

// coldStub records a deferred code emission for the cold section.
// The label is emitted first, then the emit function generates the code.
type coldStub struct {
	label string
	emit  func()
}

// cmpResumeStub describes a comparison call-exit resume stub with captured pinning state.
type cmpResumeStub struct {
	label      string         // label for this stub
	targetPC   string         // pcLabel to jump to after reload
	pinnedVars []int          // snapshot of pinnedVars at creation time
	pinnedRegs map[int]Reg    // snapshot of pinnedRegs at creation time
}

// deferCold records a cold code stub to be emitted after the hot path.
// In the hot path, a B <label> instruction jumps to this cold code.
func (cg *Codegen) deferCold(label string, emit func()) {
	cg.coldStubs = append(cg.coldStubs, coldStub{label: label, emit: emit})
}

// emitColdStubs emits all deferred cold code stubs.
func (cg *Codegen) emitColdStubs() {
	for _, stub := range cg.coldStubs {
		cg.asm.Label(stub.label)
		stub.emit()
	}
}

// Reserved register for self-recursion depth tracking.
const regSelfDepth = X25 // 0 = outermost call, >0 = self-recursive call

// Reserved register for pinning R(0) in self-call functions.
// X19 is callee-saved, so it persists across nested BL calls when
// saved/restored via STP/LDP in emitSelfCall.
const regSelfArg = X19

// Reserved register for pinning R(1) in self-call functions with 2+ parameters.
// X22 is callee-saved. Saved/restored in the self-call frame alongside X19/X30.
const regSelfArg2 = X22

// Compile compiles a FuncProto to native ARM64 code.
func Compile(proto *vm.FuncProto) (*CompiledFunc, error) {
	return CompileWithGlobals(proto, nil)
}

// CompileWithEngine compiles with access to the JIT engine for cross-call slot allocation.
func CompileWithEngine(proto *vm.FuncProto, engine *Engine) (*CompiledFunc, error) {
	cg := &Codegen{
		asm:     NewAssembler(),
		proto:   proto,
		globals: engine.globals,
		engine:  engine,
	}
	return cg.compile()
}

// CompileWithGlobals compiles a FuncProto with access to globals for function inlining.
func CompileWithGlobals(proto *vm.FuncProto, globals map[string]rt.Value) (*CompiledFunc, error) {
	cg := &Codegen{
		asm:     NewAssembler(),
		proto:   proto,
		globals: globals,
	}
	return cg.compile()
}

func (cg *Codegen) compile() (*CompiledFunc, error) {
	cg.analyzeInlineCandidates() // must run first: identifies inline/self-call PCs for data flow

	// If the function has both self-calls AND non-self/non-inline CALL instructions
	// (which become call-exits), disable self-calls. The self-call mechanism pushes
	// frames on the ARM64 stack, but call-exit jumps to the epilogue which assumes
	// a clean stack. Mixing them causes stack corruption when a cross-call exits
	// from within a self-recursive call.
	// Exception: if all non-self CALLs are handled as cross-calls (direct BLR),
	// self-calls remain safe since there are no call-exits to corrupt the stack.
	if cg.hasSelfCalls && cg.hasCrossCallExits() {
		if cg.engine != nil {
			// With cross-call support, detect and register direct BLR calls
			// for the non-self CALL instructions. If ALL cross-calls can be
			// handled as direct BLR, self-calls remain safe.
			cg.analyzeCrossCalls()
			if cg.hasCrossCallExitsExcluding() {
				// Some CALLs can't be handled by cross-call BLR → disable self-calls.
				cg.disableSelfCalls()
			}
		} else {
			// No engine → no cross-call support → disable self-calls.
			cg.disableSelfCalls()
		}
	}

	cg.analyzeKnownIntRegs()
	cg.analyzeForLoops()
	cg.analyzeCallExitPCs()

	// Pin R(0) to X19 for self-call functions: all body reads of the first
	// parameter will use a callee-saved register instead of a memory load.
	// This must happen after analyzeForLoops (which creates pinnedRegs) and
	// before emitPrologue/emitBody (which consume it).
	if cg.hasSelfCalls && cg.proto.NumParams > 0 {
		cg.pinnedRegs[0] = regSelfArg
		// Note: we intentionally do NOT add R(0) to pinnedVars.
		// spillPinnedRegs iterates pinnedRegs directly (not pinnedVars),
		// so R(0) will still be spilled when needed. But we don't want
		// reloadPinnedRegs (used at call-exit resume) to reload R(0)
		// since it's managed by the self-call mechanism, not for-loop pinning.

		// Pin R(1) to X22 for two-parameter self-call functions (e.g., ackermann).
		// Same approach: callee-saved register, saved/restored in self-call frame.
		if cg.proto.NumParams > 1 {
			cg.pinnedRegs[1] = regSelfArg2
		}
	}

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

	// === Cold section: all infrequently-executed code grouped after hot path ===
	// Guard failures, side-exit stubs, call-exit fallbacks, overflow handlers,
	// and loop-exit spill code are placed here to reduce I-cache pollution
	// in the hot loop. (BOLT-style hot/cold code splitting)
	cg.emitColdStubs()

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

	return &CompiledFunc{Code: block, Proto: cg.proto}, nil
}

// disableSelfCalls converts self-call candidates back to regular call-exit patterns.
func (cg *Codegen) disableSelfCalls() {
	for pc, cand := range cg.inlineCandidates {
		if cand.isSelfCall {
			delete(cg.inlineCandidates, pc)
			delete(cg.inlineSkipPCs, cand.getglobalPC)
		}
	}
	cg.hasSelfCalls = false
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
			// Guard failure deferred to cold section.
			cg.deferCold("self_param_guard_fail", func() {
				cg.asm.LoadImm64(X1, 0)
				cg.asm.STR(X1, regCtx, ctxOffExitPC)
				cg.asm.LoadImm64(X0, 1) // ExitCode = 1 (side exit)
				cg.asm.B("epilogue")
			})
		}

		// Outermost entry: load pinned parameters from the Value array
		// (set by interpreter) into callee-saved registers, then fall through to the body.
		a.LDR(regSelfArg, regRegs, regIvalOffset(0))
		if cg.proto.NumParams > 1 {
			a.LDR(regSelfArg2, regRegs, regIvalOffset(1))
		}

		a.Label("self_call_entry")  // self-recursive calls BL here; pinned args already loaded by caller
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
// Also checks for registers set by the immediately preceding LOADINT instruction,
// enabling immediate-form optimizations in self-call function bodies.
func (cg *Codegen) rkSmallIntConst(idx int) int64 {
	if vm.IsRK(idx) {
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
	return -1
}

// regLoadIntConst checks if a VM register holds a known small constant set by
// a LOADINT instruction. Scans backward from currentPC (up to 3 instructions)
// looking for a LOADINT that set the register, verifying no intervening write.
// Returns the constant value (0..4095) or -1 if not found.
func (cg *Codegen) regLoadIntConst(reg, currentPC int) int64 {
	if !cg.hasSelfCalls {
		return -1
	}
	code := cg.proto.Code
	for scanPC := currentPC - 1; scanPC >= 0 && scanPC >= currentPC-3; scanPC-- {
		scanInst := code[scanPC]
		scanOp := vm.DecodeOp(scanInst)
		scanA := vm.DecodeA(scanInst)

		// Check if this is the LOADINT that set our register.
		if scanOp == vm.OP_LOADINT && scanA == reg {
			v := int64(vm.DecodesBx(scanInst))
			if v >= 0 && v <= 4095 {
				return v
			}
			return -1
		}
		// If any intervening instruction writes to our register, give up.
		if scanA == reg {
			switch scanOp {
			case vm.OP_MOVE, vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD,
				vm.OP_UNM, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL, vm.OP_GETGLOBAL,
				vm.OP_GETTABLE, vm.OP_GETFIELD, vm.OP_GETUPVAL:
				return -1
			}
		}
		// Skip instructions that are part of inline patterns (not emitted).
		if cg.inlineSkipPCs[scanPC] || cg.inlineArgSkipPCs[scanPC] {
			continue
		}
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
		// For inline/self-call candidates, the result is placed as TypeInt.
		// If the result destination was traced, mark that register too.
		if cg.inlineCandidates != nil {
			if candidate, ok := cg.inlineCandidates[pc]; ok {
				out |= regBit(candidate.fnReg)
				if candidate.resultDest >= 0 {
					out |= regBit(candidate.resultDest)
				}
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

		case vm.OP_CALL:
			// Detect inlined call accumulator pattern:
			//   MOVE R(fnReg+1) R(accum)  -- arg setup (copies accumulator to arg slot)
			//   ... (other arg setups)
			//   CALL R(fnReg) B=n C=2     -- inlined: result = f(args)
			//   MOVE R(accum) R(fnReg)    -- copy result back to accumulator
			//
			// This is an indirect accumulator through the inlined function.
			if cg.inlineCandidates == nil {
				continue
			}
			candidate, isInline := cg.inlineCandidates[pc]
			if !isInline || candidate.isSelfCall {
				continue
			}
			fnReg := candidate.fnReg
			// Skip loop control registers
			if fnReg >= aReg && fnReg <= aReg+3 {
				continue
			}
			// Check if the next instruction is MOVE R(accum) R(fnReg)
			if pc+1 >= bodyEnd || vm.DecodeOp(code[pc+1]) != vm.OP_MOVE {
				continue
			}
			moveA := vm.DecodeA(code[pc+1])
			moveB := vm.DecodeB(code[pc+1])
			if moveB != fnReg {
				continue
			}
			// moveA is the accumulator candidate. Check that it's used as an argument.
			// Scan backward from the CALL to find MOVE R(fnReg+k) R(moveA).
			isAccum := false
			for scanPC := pc - 1; scanPC >= bodyStart && scanPC >= pc-10; scanPC-- {
				si := code[scanPC]
				if vm.DecodeOp(si) == vm.OP_MOVE {
					srcReg := vm.DecodeB(si)
					dstReg := vm.DecodeA(si)
					if srcReg == moveA && dstReg > fnReg && dstReg <= fnReg+candidate.nArgs {
						isAccum = true
						break
					}
				}
			}
			if isAccum && !(moveA >= aReg && moveA <= aReg+3) {
				counts[moveA]++
				counts[fnReg]++ // pin the result register too
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
			// MOVE R(A) = R(B) — safe if source is known-int at this PC.
			b := vm.DecodeB(inst)
			if cg.knownInt != nil && pc < len(cg.knownInt) && regSetHas(cg.knownInt[pc], b) {
				// Source is known-int, so this MOVE produces an int value — safe for pinning.
			} else {
				unsafe[a] = true
			}
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
			// Inlined calls produce int results — safe for pinning.
			if cg.inlineCandidates != nil {
				if _, inlined := cg.inlineCandidates[pc]; inlined {
					break // safe: inlined call always produces int
				}
			}
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
	cg.inlineArgSkipPCs = make(map[int]bool)

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
					nArgs := b - 1
					candidate := &inlineCandidate{
						getglobalPC: pc,
						callPC:      pc2,
						callee:      vcl.Proto,
						fnReg:       globalA,
						nArgs:       nArgs,
						nResults:    c - 1,
					}

					// Trace argument sources: scan backward from CALL to find
					// MOVE/LOADINT that set up each arg register R(fnReg+1+i).
					candidate.argTraces = make([]inlineArgTrace, vcl.Proto.NumParams)
					for i := 0; i < vcl.Proto.NumParams && i < nArgs; i++ {
						argReg := globalA + 1 + i
						for scanPC := pc2 - 1; scanPC > pc && scanPC >= pc2-10; scanPC-- {
							si := code[scanPC]
							sop := vm.DecodeOp(si)
							sa := vm.DecodeA(si)
							if sa != argReg {
								continue
							}
							if sop == vm.OP_MOVE {
								srcReg := vm.DecodeB(si)
								candidate.argTraces[i] = inlineArgTrace{
									fromReg: srcReg, traced: true, setupPC: scanPC,
								}
							} else if sop == vm.OP_LOADINT {
								sbx := vm.DecodesBx(si)
								candidate.argTraces[i] = inlineArgTrace{
									fromConst: int64(sbx), isConst: true, traced: true, setupPC: scanPC,
								}
							}
							break
						}
					}

					// Mark traced arg setup PCs as skippable
					for _, trace := range candidate.argTraces {
						if trace.traced {
							cg.inlineArgSkipPCs[trace.setupPC] = true
						}
					}

					// Trace result destination: if the next instruction is
					// MOVE R(dest) R(fnReg), write result directly to R(dest).
					candidate.resultDest = -1
					candidate.resultMovePC = -1
					if pc2+1 < len(code) {
						nextInst := code[pc2+1]
						if vm.DecodeOp(nextInst) == vm.OP_MOVE && vm.DecodeB(nextInst) == globalA {
							candidate.resultDest = vm.DecodeA(nextInst)
							candidate.resultMovePC = pc2 + 1
							cg.inlineArgSkipPCs[pc2+1] = true
						}
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

// hasCrossCallExits checks if the function has non-self, non-inline CALL instructions
// that would become call-exits. These are incompatible with the self-call ARM64 stack
// mechanism: call-exit jumps to epilogue which assumes a clean stack, but self-calls
// push frames on the ARM64 stack that would be orphaned.
func (cg *Codegen) hasCrossCallExits() bool {
	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op != vm.OP_CALL {
			continue
		}
		// Skip self-call and inline candidates (they don't go through call-exit).
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		// This CALL instruction will become a call-exit.
		return true
	}
	return false
}

// hasCrossCallExitsExcluding checks if there are CALL instructions that are NOT
// handled by self-call, inline, OR cross-call BLR. Returns true if some CALLs
// would still need call-exit.
func (cg *Codegen) hasCrossCallExitsExcluding() bool {
	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		op := vm.DecodeOp(code[pc])
		if op != vm.OP_CALL {
			continue
		}
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		if _, ok := cg.crossCalls[pc]; ok {
			continue
		}
		return true
	}
	return false
}

// analyzeCrossCalls detects GETGLOBAL + CALL patterns where the global
// resolves to a known VM function. For each detected pattern, allocates
// a cross-call slot in the engine for direct BLR optimization.
func (cg *Codegen) analyzeCrossCalls() {
	cg.crossCalls = make(map[int]*crossCallInfo)
	cg.crossCallSkipPCs = make(map[int]bool)
	if cg.engine == nil || cg.globals == nil {
		return
	}

	code := cg.proto.Code
	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		if vm.DecodeOp(inst) != vm.OP_GETGLOBAL {
			continue
		}
		// Skip if already handled by inline or self-call.
		if cg.inlineSkipPCs[pc] {
			continue
		}

		globalA := vm.DecodeA(inst)
		globalBx := vm.DecodeBx(inst)
		if globalBx >= len(cg.proto.Constants) {
			continue
		}
		name := cg.proto.Constants[globalBx].Str()

		// Look for the CALL that uses R(globalA).
		for pc2 := pc + 1; pc2 < len(code) && pc2 <= pc+10; pc2++ {
			inst2 := code[pc2]
			op2 := vm.DecodeOp(inst2)
			if op2 == vm.OP_CALL && vm.DecodeA(inst2) == globalA {
				// Skip if already handled.
				if _, ok := cg.inlineCandidates[pc2]; ok {
					break
				}

				b := vm.DecodeB(inst2)
				c := vm.DecodeC(inst2)

				// Resolve the callee's proto from globals.
				fnVal, ok := cg.globals[name]
				if !ok || !fnVal.IsFunction() {
					break
				}
				vcl, _ := fnVal.Ptr().(*vm.Closure)
				if vcl == nil {
					break
				}

				// Allocate a cross-call slot.
				slot := cg.engine.allocCrossCallSlot(name, vcl.Proto)

				info := &crossCallInfo{
					getglobalPC: pc,
					callPC:      pc2,
					calleeName:  name,
					calleeProto: vcl.Proto,
					fnReg:       globalA,
					nArgs:       b - 1,
					nResults:    c - 1,
					slot:        slot,
				}
				cg.crossCalls[pc2] = info
				cg.crossCallSkipPCs[pc] = true // skip the GETGLOBAL
				break
			}
			if vm.DecodeA(inst2) == globalA {
				break // register overwritten
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

	// Use pre-computed argument traces from the analysis phase.
	// Traced MOVE args update regMap to point to the actual source register.
	// Traced LOADINT args are recorded as known constants.
	constArgs := make(map[int]int64)
	for i, trace := range candidate.argTraces {
		if !trace.traced {
			continue
		}
		if trace.isConst {
			constArgs[i] = trace.fromConst
		} else {
			// MOVE traced: callee param i → actual source register in caller
			regMap[i] = trace.fromReg
		}
	}

	// Find the return register and map it to the result register.
	// If the result destination was traced (CALL result MOVE'd elsewhere),
	// write directly to the final destination.
	resultReg := fnReg
	if candidate.resultDest >= 0 {
		resultReg = candidate.resultDest
	}
	for _, calleeInst := range callee.Code {
		if vm.DecodeOp(calleeInst) == vm.OP_RETURN {
			retA := vm.DecodeA(calleeInst)
			if candidate.nResults > 0 {
				regMap[retA] = resultReg
			}
			break
		}
	}

	exitLabel := fmt.Sprintf("inline_exit_%d", pc)

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
					cg.storeNilValue(resultReg)
				}
			} else if mappedRetA != resultReg && candidate.nResults > 0 {
				// Copy result to resultReg (respecting pinned registers)
				srcArm, srcPinned := cg.pinnedRegs[mappedRetA]
				dstArm, dstPinned := cg.pinnedRegs[resultReg]
				if srcPinned && dstPinned {
					if srcArm != dstArm {
						cg.asm.MOVreg(dstArm, srcArm)
					}
				} else if srcPinned {
					cg.storeIntValue(resultReg, srcArm)
				} else if dstPinned {
					cg.asm.LDR(dstArm, regRegs, regIvalOffset(mappedRetA))
				} else {
					cg.copyValue(resultReg, mappedRetA)
				}
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

			// Determine if each operand is a known constant.
			// Sources: (1) RK constant from callee pool, (2) traced LOADINT arg
			bIsConst := vm.IsRK(b)
			cIsConst := vm.IsRK(c)
			bConstVal := int64(0)
			cConstVal := int64(0)
			if bIsConst {
				constIdx := vm.RKToConstIdx(b)
				if constIdx < len(callee.Constants) {
					bConstVal = callee.Constants[constIdx].Int()
				}
			} else if cv, ok := constArgs[b]; ok {
				bIsConst = true
				bConstVal = cv
			}
			if cIsConst {
				constIdx := vm.RKToConstIdx(c)
				if constIdx < len(callee.Constants) {
					cConstVal = callee.Constants[constIdx].Int()
				}
			} else if cv, ok := constArgs[c]; ok {
				cIsConst = true
				cConstVal = cv
			}

			// Check pinned status and known-int for each operand
			aArm, aPinned := cg.pinnedRegs[mappedA]
			var bArm, cArm Reg
			var bPinned, cPinned bool
			bKnownInt := bIsConst
			cKnownInt := cIsConst
			if !bIsConst {
				bArm, bPinned = cg.pinnedRegs[mappedB]
				bKnownInt = bPinned || (cg.knownInt != nil && pc < len(cg.knownInt) && regSetHas(cg.knownInt[pc], mappedB))
			}
			if !cIsConst {
				cArm, cPinned = cg.pinnedRegs[mappedC]
				cKnownInt = cPinned || (cg.knownInt != nil && pc < len(cg.knownInt) && regSetHas(cg.knownInt[pc], mappedC))
			}

			// Type guards only for non-known-int, non-pinned, non-constant operands
			if !bKnownInt {
				cg.loadRegTyp(X0, mappedB)
				cg.asm.CMPimmW(X0, TypeInt)
				cg.asm.BCond(CondNE, exitLabel)
			}
			if !cKnownInt {
				cg.loadRegTyp(X0, mappedC)
				cg.asm.CMPimmW(X0, TypeInt)
				cg.asm.BCond(CondNE, exitLabel)
			}

			// Fast path: destination pinned and operands are pinned or constant
			if aPinned && (bPinned || bIsConst) && (cPinned || cIsConst) {
				switch {
				case !bIsConst && !cIsConst:
					// Both in ARM registers
					switch calleeOp {
					case vm.OP_ADD:
						cg.asm.ADDreg(aArm, bArm, cArm)
					case vm.OP_SUB:
						cg.asm.SUBreg(aArm, bArm, cArm)
					case vm.OP_MUL:
						cg.asm.MUL(aArm, bArm, cArm)
					}
				case bIsConst && !cIsConst:
					if calleeOp == vm.OP_ADD && bConstVal >= 0 && bConstVal <= 4095 {
						cg.asm.ADDimm(aArm, cArm, uint16(bConstVal))
					} else {
						cg.asm.LoadImm64(X0, bConstVal)
						switch calleeOp {
						case vm.OP_ADD:
							cg.asm.ADDreg(aArm, X0, cArm)
						case vm.OP_SUB:
							cg.asm.SUBreg(aArm, X0, cArm)
						case vm.OP_MUL:
							cg.asm.MUL(aArm, X0, cArm)
						}
					}
				case !bIsConst && cIsConst:
					if (calleeOp == vm.OP_ADD || calleeOp == vm.OP_SUB) && cConstVal >= 0 && cConstVal <= 4095 {
						switch calleeOp {
						case vm.OP_ADD:
							cg.asm.ADDimm(aArm, bArm, uint16(cConstVal))
						case vm.OP_SUB:
							cg.asm.SUBimm(aArm, bArm, uint16(cConstVal))
						}
					} else {
						cg.asm.LoadImm64(X1, cConstVal)
						switch calleeOp {
						case vm.OP_ADD:
							cg.asm.ADDreg(aArm, bArm, X1)
						case vm.OP_SUB:
							cg.asm.SUBreg(aArm, bArm, X1)
						case vm.OP_MUL:
							cg.asm.MUL(aArm, bArm, X1)
						}
					}
				default:
					// Both constants — fold at compile time
					var result int64
					switch calleeOp {
					case vm.OP_ADD:
						result = bConstVal + cConstVal
					case vm.OP_SUB:
						result = bConstVal - cConstVal
					case vm.OP_MUL:
						result = bConstVal * cConstVal
					}
					cg.asm.LoadImm64(aArm, result)
				}
			} else {
				// Fallback: load operands into scratch registers, compute, store result
				if bIsConst {
					cg.asm.LoadImm64(X0, bConstVal)
				} else if bPinned {
					cg.asm.MOVreg(X0, bArm)
				} else {
					cg.loadRegIval(X0, mappedB)
				}

				if cIsConst {
					cg.asm.LoadImm64(X1, cConstVal)
				} else if cPinned {
					cg.asm.MOVreg(X1, cArm)
				} else {
					cg.loadRegIval(X1, mappedC)
				}

				switch calleeOp {
				case vm.OP_ADD:
					cg.asm.ADDreg(X0, X0, X1)
				case vm.OP_SUB:
					cg.asm.SUBreg(X0, X0, X1)
				case vm.OP_MUL:
					cg.asm.MUL(X0, X0, X1)
				}

				if aPinned {
					cg.asm.MOVreg(aArm, X0)
				} else {
					cg.storeIntValue(mappedA, X0)
				}
			}

		case vm.OP_MOVE:
			a := regMap[vm.DecodeA(calleeInst)]
			b := regMap[vm.DecodeB(calleeInst)]
			srcArm, srcPinned := cg.pinnedRegs[b]
			dstArm, dstPinned := cg.pinnedRegs[a]
			if srcPinned && dstPinned {
				if srcArm != dstArm {
					cg.asm.MOVreg(dstArm, srcArm)
				}
			} else if srcPinned {
				cg.storeIntValue(a, srcArm)
			} else if dstPinned {
				cg.asm.LDR(dstArm, regRegs, regIvalOffset(b))
			} else {
				cg.copyValue(a, b)
			}

		case vm.OP_LOADINT:
			a := regMap[vm.DecodeA(calleeInst)]
			sbx := vm.DecodesBx(calleeInst)
			if armReg, pinned := cg.pinnedRegs[a]; pinned {
				cg.asm.LoadImm64(armReg, int64(sbx))
			} else {
				cg.asm.LoadImm64(X0, int64(sbx))
				cg.storeIntValue(a, X0)
			}

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

	// afterLabel: hot path falls through here (no need for a skip branch).
	// The exit label is deferred to the cold section.
	cg.deferCold(exitLabel, func() {
		cg.spillPinnedRegs()
		cg.asm.LoadImm64(X1, int64(candidate.getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
	})

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
		if cg.crossCallSkipPCs[pc] {
			continue
		}
		if _, ok := cg.inlineCandidates[pc]; ok {
			continue
		}
		if _, ok := cg.crossCalls[pc]; ok {
			continue
		}
		if !cg.isSupported(op) && isCallExitOp(op) {
			cg.callExitPCs = append(cg.callExitPCs, pc)
		}
		// GETFIELD/SETFIELD/GETTABLE/SETTABLE are "supported" (native fast path) but still need
		// call-exit resume entries for the fallback slow path.
		if op == vm.OP_GETFIELD || op == vm.OP_SETFIELD || op == vm.OP_GETTABLE || op == vm.OP_SETTABLE {
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

		// Skip GETGLOBAL instructions that are part of an inline candidate or cross-call
		if cg.inlineSkipPCs[pc] || cg.crossCallSkipPCs[pc] {
			continue
		}

		// Skip argument setup instructions (MOVE/LOADINT) that were traced
		// through by inline call optimization. The inline code reads the
		// actual source directly, so these stores are dead.
		if cg.inlineArgSkipPCs[pc] {
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

		// Handle cross-call CALL instructions (direct BLR to compiled callee)
		if ccInfo, ok := cg.crossCalls[pc]; ok {
			if err := cg.emitCrossCall(pc, ccInfo); err != nil {
				return fmt.Errorf("pc %d (cross-call): %w", pc, err)
			}
			continue
		}

		if !cg.isSupported(op) {
			if callExitSet[pc] {
				// Call-exit: jump to cold stub for spill+exit, then resume inline.
				coldLabel := fmt.Sprintf("cold_callexit_%d", pc)
				capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
				for k, v := range cg.pinnedRegs {
					capturedPinned[k] = v
				}
				cg.asm.B(coldLabel)
				cg.deferCold(coldLabel, func() {
					for vmReg, armReg := range capturedPinned {
						cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
						cg.asm.MOVimm16W(X9, TypeInt)
						cg.storeRegTyp(X9, vmReg)
					}
					cg.asm.LoadImm64(X1, int64(pc))
					cg.asm.STR(X1, regCtx, ctxOffExitPC)
					cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit, resumable)
					cg.asm.B("epilogue")
				})

				// Resume label: re-entry point after executor handles the instruction.
				cg.asm.Label(resumeLabel(pc))
				cg.asm.LDR(regRegs, regCtx, ctxOffRegs) // reload in case regs were reallocated
				cg.reloadPinnedRegs()
				continue // next pc label is emitted by the loop
			}

			// Permanent side exit: jump to cold stub.
			coldLabel := fmt.Sprintf("cold_sideexit_%d", pc)
			capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
			for k, v := range cg.pinnedRegs {
				capturedPinned[k] = v
			}
			cg.asm.B(coldLabel)
			cg.deferCold(coldLabel, func() {
				for vmReg, armReg := range capturedPinned {
					cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
					cg.asm.MOVimm16W(X9, TypeInt)
					cg.storeRegTyp(X9, vmReg)
				}
				cg.asm.LoadImm64(X1, int64(pc))
				cg.asm.STR(X1, regCtx, ctxOffExitPC)
				cg.asm.LoadImm64(X0, 1)
				cg.asm.B("epilogue")
			})
			continue
		}

		if err := cg.emitInstruction(pc, inst); err != nil {
			return fmt.Errorf("pc %d: %w", pc, err)
		}

		// For ops with native fast path + call-exit fallback,
		// emit the resume label after the native code. The fast path
		// must skip the resume reload (which would corrupt pinned regs).
		if (op == vm.OP_GETFIELD || op == vm.OP_SETFIELD || op == vm.OP_GETTABLE || op == vm.OP_SETTABLE) && callExitSet[pc] {
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
		vm.OP_GETFIELD, vm.OP_SETFIELD,
		vm.OP_GETTABLE,
		vm.OP_SETTABLE:
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
		return cg.emitLoadInt(pc, inst)
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
	case vm.OP_SETFIELD:
		return cg.emitSetField(pc, inst)
	case vm.OP_GETTABLE:
		return cg.emitGetTable(pc, inst)
	case vm.OP_SETTABLE:
		return cg.emitSetTable(pc, inst)
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

func (cg *Codegen) emitLoadInt(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	sbx := vm.DecodesBx(inst)

	// For self-call functions: skip the Value array write if the constant will
	// be consumed as an immediate by the next instruction (LT, LE, SUB, ADD).
	// The constant value is still available via regLoadIntConst for the consumer.
	if cg.hasSelfCalls && sbx >= 0 && sbx <= 4095 {
		if cg.isLoadIntDeadStore(pc, aReg) {
			return nil
		}
	}

	cg.asm.LoadImm64(X0, int64(sbx))
	cg.storeIntValue(aReg, X0)
	return nil
}

// isLoadIntDeadStore checks if a LOADINT at pc is a dead store whose value
// will only be consumed via immediate form by subsequent instructions.
// Returns true if the LOADINT's Value array write can be safely elided.
func (cg *Codegen) isLoadIntDeadStore(pc, reg int) bool {
	code := cg.proto.Code
	// Scan forward to find all uses of this register before it's overwritten.
	for scanPC := pc + 1; scanPC < len(code); scanPC++ {
		scanInst := code[scanPC]
		scanOp := vm.DecodeOp(scanInst)
		scanA := vm.DecodeA(scanInst)

		// If the register is overwritten (destination = our reg), the store is dead.
		switch scanOp {
		case vm.OP_LOADINT, vm.OP_LOADK, vm.OP_LOADNIL, vm.OP_LOADBOOL,
			vm.OP_MOVE, vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_DIV, vm.OP_MOD,
			vm.OP_UNM, vm.OP_GETGLOBAL, vm.OP_GETTABLE, vm.OP_GETFIELD, vm.OP_GETUPVAL:
			if scanA == reg {
				return true // register overwritten before any memory read
			}
		case vm.OP_CALL:
			// Self-call writes result to fnReg (=A), check if it overwrites our reg.
			if scanA == reg {
				return true
			}
		}

		// If the register is read as a source operand, check if the consumer
		// will use the immediate form (regLoadIntConst). If not, we need the store.
		switch scanOp {
		case vm.OP_LT, vm.OP_LE:
			b := vm.DecodeB(scanInst)
			c := vm.DecodeC(scanInst)
			if b == reg || c == reg {
				// The LT/LE emitter will detect this via regLoadIntConst and use CMPimm.
				// Safe to skip the LOADINT store.
				return true
			}
		case vm.OP_ADD, vm.OP_SUB:
			b := vm.DecodeB(scanInst)
			c := vm.DecodeC(scanInst)
			if b == reg || c == reg {
				// The arithmetic emitter will detect this via regLoadIntConst and use ADDimm/SUBimm.
				return true
			}
		case vm.OP_MOVE:
			b := vm.DecodeB(scanInst)
			if b == reg {
				return false // MOVE reads from Value array — need the store
			}
		case vm.OP_RETURN:
			// RETURN A B reads R(A)..R(A+B-2)
			retA := vm.DecodeA(scanInst)
			retB := vm.DecodeB(scanInst)
			if retB > 0 && reg >= retA && reg < retA+retB-1 {
				return false // returned value — need the store
			}
			if retB == 0 && reg >= retA {
				return false // variable return — need the store
			}
		case vm.OP_JMP:
			// Branch target could loop back and read the register — be conservative.
			return false
		case vm.OP_FORLOOP, vm.OP_FORPREP:
			return false // loop instructions — be conservative
		}

		// Skip instructions that are part of inline patterns.
		if cg.inlineSkipPCs[scanPC] {
			continue
		}
	}
	// Reached end of function without finding a reader or overwriter — store is dead.
	return true
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

		// Guard failure deferred to cold section.
		capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
		for k, v := range cg.pinnedRegs {
			capturedPinned[k] = v
		}
		cg.deferCold(exitLabel, func() {
			for vmReg, armReg := range capturedPinned {
				cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
				cg.asm.MOVimm16W(X9, TypeInt)
				cg.storeRegTyp(X9, vmReg)
			}
			cg.asm.LoadImm64(X1, int64(pc))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
		})
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

		// Compute known constant values for C and B operands.
		// Check both RK constants and registers set by recent LOADINT.
		cImmVal := cg.rkSmallIntConst(cIdx)
		if cImmVal < 0 && !vm.IsRK(cIdx) {
			cImmVal = cg.regLoadIntConst(cIdx, pc)
		}
		bImmVal := cg.rkSmallIntConst(bIdx)
		if bImmVal < 0 && !vm.IsRK(bIdx) {
			bImmVal = cg.regLoadIntConst(bIdx, pc)
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
		} else if (arithOp == "ADD" || arithOp == "SUB") && cImmVal >= 0 {
			// ADD/SUB with small integer constant: use immediate form.
			// SUB R(A), R(B), imm → SUBimm X0, X0, #imm
			cg.loadRKIval(X0, bIdx)
			switch arithOp {
			case "ADD":
				cg.asm.ADDimm(X0, X0, uint16(cImmVal))
			case "SUB":
				cg.asm.SUBimm(X0, X0, uint16(cImmVal))
			}
			cg.storeIntValue(aReg, X0)
		} else if (arithOp == "ADD" || arithOp == "SUB") && arithOp == "ADD" && bImmVal >= 0 {
			// ADD R(A), imm, R(C) → ADDimm X0, X0, #imm (commutative)
			cg.loadRKIval(X0, cIdx)
			cg.asm.ADDimm(X0, X0, uint16(bImmVal))
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

		// Guard failure deferred to cold section.
		capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
		for k, v := range cg.pinnedRegs {
			capturedPinned[k] = v
		}
		cg.deferCold(exitLabel, func() {
			for vmReg, armReg := range capturedPinned {
				cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
				cg.asm.MOVimm16W(X9, TypeInt)
				cg.storeRegTyp(X9, vmReg)
			}
			cg.asm.LoadImm64(X1, int64(pc))
			cg.asm.STR(X1, regCtx, ctxOffExitPC)
			cg.asm.LoadImm64(X0, 1)
			cg.asm.B("epilogue")
		})
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
	// Check both RK constants and registers set by a recent LOADINT.
	cImm := cg.rkSmallIntConst(cIdx)
	if cImm < 0 && !vm.IsRK(cIdx) {
		cImm = cg.regLoadIntConst(cIdx, pc)
	}
	bImm := cg.rkSmallIntConst(bIdx)
	if bImm < 0 && !vm.IsRK(bIdx) {
		bImm = cg.regLoadIntConst(bIdx, pc)
	}

	if cImm >= 0 {
		cg.loadRKIval(X0, bIdx)
		cg.asm.CMPimm(X0, uint16(cImm))
	} else if bImm >= 0 {
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
	// Check both RK constants and registers set by a recent LOADINT.
	cImmLE := cg.rkSmallIntConst(cIdx)
	if cImmLE < 0 && !vm.IsRK(cIdx) {
		cImmLE = cg.regLoadIntConst(cIdx, pc)
	}
	bImmLE := cg.rkSmallIntConst(bIdx)
	if bImmLE < 0 && !vm.IsRK(bIdx) {
		bImmLE = cg.regLoadIntConst(bIdx, pc)
	}

	if cImmLE >= 0 {
		// B <= C with C as immediate: CMP B, #C then check LE.
		cg.loadRKIval(X0, bIdx)
		cg.asm.CMPimm(X0, uint16(cImmLE))
	} else if bImmLE >= 0 {
		// B <= C with B constant: CMP C, #B, then reverse condition.
		// B <= C ⟺ C >= B
		cg.loadRKIval(X0, cIdx)
		cg.asm.CMPimm(X0, uint16(bImmLE))
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
	// Capture current pinning state for the cold stub and resume stubs.
	capturedVars := make([]int, len(cg.pinnedVars))
	copy(capturedVars, cg.pinnedVars)
	capturedRegs := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedRegs[k] = v
	}

	// Guard failure deferred to cold section.
	cg.deferCold(exitLabel, func() {
		for vmReg, armReg := range capturedRegs {
			cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // call-exit: executor handles non-integer comparison and resumes
		cg.asm.B("epilogue")
	})

	// Defer resume stubs with captured pinning state.
	// These will be emitted after the main instruction loop.
	cmpStub := fmt.Sprintf("cmp_resume_%d", pc)
	cg.cmpResumeStubs = append(cg.cmpResumeStubs,
		cmpResumeStub{cmpStub + "_1", pcLabel(pc + 1), capturedVars, capturedRegs},
		cmpResumeStub{cmpStub + "_2", pcLabel(pc + 2), capturedVars, capturedRegs},
	)

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

	// Guard failure deferred to cold section (no pinning active at FORPREP).
	cg.deferCold(exitLabel, func() {
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
	})

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
			// Fall-through: loop done → jump to cold exit stub.
			cg.asm.B(exitFor)
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
			// Fall-through: loop done → jump to cold exit stub.
			cg.asm.B(exitFor)
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

		// Loop exit: spill code deferred to cold section.
		// Capture pinning state before clearing (spillPinnedRegs uses pinnedRegs).
		capturedPinnedRegs := make(map[int]Reg, len(cg.pinnedRegs))
		for k, v := range cg.pinnedRegs {
			capturedPinnedRegs[k] = v
		}
		nextPC := pcLabel(pc + 1)
		cg.deferCold(exitFor, func() {
			for vmReg, armReg := range capturedPinnedRegs {
				cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
				cg.asm.MOVimm16W(X9, TypeInt)
				cg.storeRegTyp(X9, vmReg)
			}
			cg.asm.B(nextPC) // jump back to hot path after loop
		})
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
	// svals[i] is at X7 + i * ValueSize
	EmitMulValueSize(asm, X8, X6, X5) // X8 = i * ValueSize
	asm.ADDreg(X7, X7, X8)            // X7 = &svals[i]

	// Copy Value (ValueSize bytes) from svals[i] to R(A)
	aOff := a * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, X7, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	// Fallback deferred to cold section.
	capturedPinnedGF := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedGF[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedGF {
			cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit)
		cg.asm.B("epilogue")
	})

	return nil
}

// emitSetField compiles OP_SETFIELD R(A)[Constants[B]] = RK(C) natively.
// Fast path: R(A) is TypeTable, no metatable, key found in flat skeys.
// Slow path: falls through to call-exit for non-table, metatable, or smap cases.
func (cg *Codegen) emitSetField(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst) // constant index for field name
	cidx := vm.DecodeC(inst) // RK(C) = value to write
	asm := cg.asm

	// Label for call-exit fallback
	fallbackLabel := fmt.Sprintf("setfield_fallback_%d", pc)


	// --- Step 1: Type check R(A).typ == TypeTable ---
	aTypOff := regTypOffset(a)
	if aTypOff <= 4095 {
		asm.LDRB(X0, regRegs, aTypOff)
	} else {
		asm.LoadImm64(X0, int64(aTypOff))
		asm.LDRBreg(X0, regRegs, X0)
	}
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// --- Step 2: Load *Table from R(A).ptr.data ---
	aPtrDataOff := a*ValueSize + OffsetPtrData
	if aPtrDataOff <= 32760 {
		asm.LDR(X0, regRegs, aPtrDataOff) // X0 = *Table
	} else {
		asm.LoadImm64(X1, int64(aPtrDataOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	asm.CBZ(X0, fallbackLabel) // nil table check

	// --- Step 3: Check metatable == nil (has __newindex → fallback) ---
	asm.LDR(X1, X0, TableOffMetatable) // X1 = table.metatable
	asm.CBNZ(X1, fallbackLabel)        // has metatable → fallback

	// --- Step 4: Load skeys slice (ptr, len) ---
	asm.LDR(X1, X0, TableOffSkeys)    // X1 = skeys base pointer
	asm.LDR(X2, X0, TableOffSkeysLen) // X2 = skeys.len
	asm.CBZ(X2, fallbackLabel)         // no skeys → fallback (might be in smap)

	// Save table pointer for later svals access
	asm.MOVreg(X9, X0) // X9 = *Table (preserved)

	// --- Step 5: Load constant key string (field name from Constants[B]) ---
	bPtrDataOff := b*ValueSize + OffsetPtrData
	if bPtrDataOff <= 32760 {
		asm.LDR(X3, regConsts, bPtrDataOff) // X3 = pointer to string header
	} else {
		asm.LoadImm64(X4, int64(bPtrDataOff))
		asm.ADDreg(X4, regConsts, X4)
		asm.LDR(X3, X4, 0)
	}
	asm.LDR(X4, X3, 0) // X4 = key string data ptr
	asm.LDR(X5, X3, 8) // X5 = key string len

	// --- Step 6: Linear scan of skeys to find matching field ---
	loopLabel := fmt.Sprintf("setfield_scan_%d", pc)
	nextLabel := fmt.Sprintf("setfield_next_%d", pc)
	foundLabel := fmt.Sprintf("setfield_found_%d", pc)
	cmpLoopLabel := fmt.Sprintf("setfield_cmp_%d", pc)

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

	// --- Step 7: Found - write RK(C) value to svals[i] ---
	asm.Label(foundLabel)
	// svals base is at Table + TableOffSvals
	asm.LDR(X7, X9, TableOffSvals) // X7 = svals base pointer
	// svals[i] is at X7 + i * ValueSize
	EmitMulValueSize(asm, X8, X6, X5) // X8 = i * ValueSize
	asm.ADDreg(X7, X7, X8)            // X7 = &svals[i]

	// Copy Value (ValueSize bytes) from RK(C) to svals[i]
	if cidx >= vm.RKBit {
		// Value comes from constants
		constIdx := vm.RKToConstIdx(cidx)
		valOff := constIdx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regConsts, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regConsts, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X7, w*8)
		}
	} else {
		// Value comes from register
		valOff := cidx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regRegs, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regRegs, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X7, w*8)
		}
	}
	// Fallback deferred to cold section.
	capturedPinnedSF := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedSF[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedSF {
			cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // ExitCode = 2 (call-exit)
		cg.asm.B("epilogue")
	})

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
	EmitMulValueSize(asm, X4, X2, X5) // X4 = key * ValueSize
	asm.ADDreg(X3, X3, X4)            // X3 = &array[key]

	aOff := a * ValueSize
	for w := 0; w < ValueSize/8; w++ {
		asm.LDR(X0, X3, w*8)
		asm.STR(X0, regRegs, aOff+w*8)
	}
	// Fallback deferred to cold section.
	capturedPinnedGT := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedGT[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedGT {
			cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2)
		cg.asm.B("epilogue")
	})

	return nil
}

// emitSetTable compiles OP_SETTABLE R(A)[RK(B)] = RK(C) natively.
// Fast path: R(A) is TypeTable, no metatable, RK(B) is TypeInt, key in array range.
// Slow path: call-exit for non-table, metatable, non-int keys, or out-of-bounds.
func (cg *Codegen) emitSetTable(pc int, inst uint32) error {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)
	asm := cg.asm

	fallbackLabel := fmt.Sprintf("settable_fallback_%d", pc)


	// --- Step 1: Type check R(A).typ == TypeTable ---
	aTypOff := regTypOffset(a)
	if aTypOff <= 4095 {
		asm.LDRB(X0, regRegs, aTypOff)
	} else {
		asm.LoadImm64(X0, int64(aTypOff))
		asm.LDRBreg(X0, regRegs, X0)
	}
	asm.CMPimmW(X0, TypeTable)
	asm.BCond(CondNE, fallbackLabel)

	// --- Step 2: Load *Table from R(A).ptr ---
	aPtrDataOff := a*ValueSize + OffsetPtrData
	if aPtrDataOff <= 32760 {
		asm.LDR(X0, regRegs, aPtrDataOff)
	} else {
		asm.LoadImm64(X1, int64(aPtrDataOff))
		asm.ADDreg(X1, regRegs, X1)
		asm.LDR(X0, X1, 0)
	}
	asm.CBZ(X0, fallbackLabel)

	// --- Step 3: Check metatable == nil ---
	asm.LDR(X1, X0, TableOffMetatable)
	asm.CBNZ(X1, fallbackLabel)

	// --- Step 4: Load key from RK(B) ---
	// Check key type == TypeInt
	var keyTypOff, keyDataOff int
	if bidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(bidx)
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
		keyTypOff = regTypOffset(bidx)
		keyDataOff = bidx*ValueSize + OffsetData
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
	// Check: key >= 1 && key < array.len
	asm.CMPimm(X2, 1) // key >= 1?
	asm.BCond(CondLT, fallbackLabel)

	asm.LDR(X3, X0, TableOffArray+8) // X3 = array.len
	asm.CMPreg(X2, X3)               // key < array.len?
	asm.BCond(CondGE, fallbackLabel)

	// --- Step 6: Compute &array[key] and copy value ---
	asm.LDR(X3, X0, TableOffArray)     // X3 = array.ptr
	EmitMulValueSize(asm, X4, X2, X5)  // X4 = key * ValueSize
	asm.ADDreg(X3, X3, X4)             // X3 = &array[key]

	// Load value from RK(C) and store to array[key] (24-byte copy: 3 words)
	if cidx >= vm.RKBit {
		constIdx := vm.RKToConstIdx(cidx)
		valOff := constIdx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regConsts, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regConsts, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X3, w*8)
		}
	} else {
		valOff := cidx * ValueSize
		for w := 0; w < ValueSize/8; w++ {
			if valOff+w*8 <= 32760 {
				asm.LDR(X0, regRegs, valOff+w*8)
			} else {
				asm.LoadImm64(X1, int64(valOff+w*8))
				asm.ADDreg(X1, regRegs, X1)
				asm.LDR(X0, X1, 0)
			}
			asm.STR(X0, X3, w*8)
		}
	}
	// Fallback deferred to cold section.
	capturedPinnedST := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinnedST[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinnedST {
			cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}
		cg.asm.LoadImm64(X1, int64(pc))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2)
		cg.asm.B("epilogue")
	})

	return nil
}

func (cg *Codegen) emitReturnOp(pc int, inst uint32) error {
	aReg := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	// For self-call functions, pinned registers (R(0)→X19, optionally R(1)→X22)
	// don't need to be in the Value array for nested returns (the caller restores
	// them from the ARM64 stack). The outermost return in emitSelfCallReturn
	// handles writing type tags for the return register explicitly.
	// Skip spillPinnedRegs to eliminate wasted instructions per nested return.
	if cg.hasSelfCalls {
		return cg.emitSelfCallReturn(pc, aReg, b)
	}

	// Spill pinned registers before returning (return values must be in memory).
	if len(cg.pinnedRegs) > 0 {
		cg.spillPinnedRegs()
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
// For 1-parameter functions: saves LR (X30) + X19 in a 16-byte frame.
// For 2+-parameter functions: saves LR (X30) + X19 + X22 in a 32-byte frame.
// regRegs (X26) is restored by subtraction after the call.
// The depth counter (X25) is managed via increment/decrement.
func (cg *Codegen) emitSelfCall(pc int, candidate *inlineCandidate) error {
	a := cg.asm
	fnReg := candidate.fnReg
	hasArg2 := cg.proto.NumParams > 1

	overflowLabel := fmt.Sprintf("self_overflow_%d", pc)


	// Increment depth counter (before stack push, so overflow unwind is simpler).
	a.ADDimm(regSelfDepth, regSelfDepth, 1)

	// Check depth limit — side exit if too deep.
	a.CMPimm(regSelfDepth, maxSelfRecursionDepth)
	a.BCond(CondGE, overflowLabel)

	// Save callee-saved registers on the ARM64 stack.
	// SP must remain 16-byte aligned.
	if hasArg2 {
		// 32-byte frame: [SP] = {X30, X19}, [SP+16] = {X22, padding}
		a.STPpre(X30, regSelfArg, SP, -32) // SP -= 32; [SP] = {X30, X19}
		a.STR(regSelfArg2, SP, 16)         // [SP+16] = X22
	} else {
		// 16-byte frame: [SP] = {X30, X19}
		a.STPpre(X30, regSelfArg, SP, -16) // SP -= 16; [SP] = {X30, X19}
	}

	// Advance regRegs to callee's register window.
	// Callee's R(0) = Caller's R(fnReg+1).
	offset := (fnReg + 1) * ValueSize
	if offset <= 4095 {
		a.ADDimm(regRegs, regRegs, uint16(offset))
	} else {
		a.LoadImm64(X0, int64(offset))
		a.ADDreg(regRegs, regRegs, X0)
	}

	// Load callee's pinned parameters from the new register window.
	// The callee at self_call_entry skips these loads since args are already loaded.
	a.LDR(regSelfArg, regRegs, regIvalOffset(0))
	if hasArg2 {
		a.LDR(regSelfArg2, regRegs, regIvalOffset(1))
	}

	// BL to self_call_entry (re-enters the function body).
	a.BL("self_call_entry")

	// After return: X0 = result (ival).
	// Restore callee-saved registers from stack.
	if hasArg2 {
		a.LDR(regSelfArg2, SP, 16)            // X22 = [SP+16]
		a.LDPpost(X30, regSelfArg, SP, 32)    // {X30, X19} = [SP]; SP += 32
	} else {
		a.LDPpost(X30, regSelfArg, SP, 16)    // {X30, X19} = [SP]; SP += 16
	}

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

	// For variable-return self-calls (C=0), update ctx.Top so subsequent
	// B=0 CALL instructions know the arg range. Top = fnReg + 1.
	if candidate.nResults < 0 {
		a.LoadImm64(X1, int64(fnReg+1))
		a.STR(X1, regCtx, ctxOffTop)
	}

	// Overflow handler deferred to cold section.
	getglobalPC := candidate.getglobalPC
	cg.deferCold(overflowLabel, func() {
		cg.asm.MOVreg(SP, X29)                       // unwind all self-call stack frames
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)      // restore original regRegs from context
		cg.asm.MOVimm16(regSelfDepth, 0)             // reset depth
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
	})

	return nil
}

// Maximum cross-call depth before falling back to interpreter.
const maxCrossCallDepthNative = 200

// emitCrossCall emits ARM64 code for a direct BLR to a compiled callee function.
// Uses a shared JITContext approach: modifies the caller's JITContext to point to
// the callee's register window, BLR to callee (whose prologue/epilogue handles
// all register saving), then restores the caller's context fields.
//
// The callee's prologue saves ALL callee-saved registers (X19-X28, X29, X30)
// in its own 96-byte stack frame. The epilogue restores them. So after BLR returns,
// all caller registers are automatically restored. The caller only needs to
// save/restore the JITContext fields (Regs, Constants).
func (cg *Codegen) emitCrossCall(pc int, info *crossCallInfo) error {
	a := cg.asm
	fnReg := info.fnReg
	slotAddr := uintptr(unsafe.Pointer(info.slot))

	fallbackLabel := fmt.Sprintf("xcall_fallback_%d", pc)

	depthLabel := fmt.Sprintf("xcall_depth_%d", pc)
	calleeExitLabel := fmt.Sprintf("xcall_exit_%d", pc)

	// Load callee's code pointer from the cross-call slot.
	a.LoadImm64(X0, int64(slotAddr))
	a.LDR(X1, X0, 0) // X1 = slot.codePtr

	// If code pointer is 0 (not compiled), fall back to call-exit.
	a.CBZ(X1, fallbackLabel)

	// Check depth limit (uses X25 for combined self+cross depth tracking).
	a.ADDimm(regSelfDepth, regSelfDepth, 1)
	a.CMPimm(regSelfDepth, maxCrossCallDepthNative)
	a.BCond(CondGE, depthLabel)

	// Load callee's constants pointer from the slot.
	a.LDR(X2, X0, 8) // X2 = slot.constantsPtr

	// Save caller's Regs and Constants pointers on the ARM64 stack.
	// Also save X1 (callee code ptr) since the callee's prologue will clobber it.
	// 16-byte frame: [SP+0] = regRegs (caller), [SP+8] = regConsts (caller)
	a.STPpre(regRegs, regConsts, SP, -16)

	// Compute callee's register window address.
	// Callee's R(0) = Caller's R(fnReg+1).
	calleeOffset := (fnReg + 1) * ValueSize
	if calleeOffset <= 4095 {
		a.ADDimm(X3, regRegs, uint16(calleeOffset))
	} else {
		a.LoadImm64(X3, int64(calleeOffset))
		a.ADDreg(X3, regRegs, X3)
	}

	// Update the shared JITContext to point to callee's register window and constants.
	a.STR(X3, regCtx, ctxOffRegs)
	a.STR(X2, regCtx, ctxOffConstants)
	// Clear ResumePC so callee starts from the beginning.
	a.STR(XZR, regCtx, ctxOffResumePC)

	// X0 = JITContext pointer, X1 = callee code. BLR to callee.
	a.MOVreg(X0, regCtx)
	a.BLR(X1)

	// After callee returns: X0 = exit code.
	// The callee's epilogue restored all callee-saved registers to the values
	// they had before our BLR, including regCtx (X28).
	// Read RetBase and RetCount from the context.
	a.MOVreg(X3, X0) // X3 = exit code
	a.LDR(X4, regCtx, ctxOffRetBase)  // X4 = RetBase
	a.LDR(X5, regCtx, ctxOffRetCount) // X5 = RetCount

	// Restore caller's Regs and Constants from the stack.
	a.LDPpost(regRegs, regConsts, SP, 16)

	// Restore the JITContext to point to caller's register window.
	a.STR(regRegs, regCtx, ctxOffRegs)
	a.STR(regConsts, regCtx, ctxOffConstants)

	a.SUBimm(regSelfDepth, regSelfDepth, 1) // depth--

	// Check exit code: if not 0, callee had an issue.
	a.CBNZ(X3, calleeExitLabel)

	// Normal return: copy result from callee's register window to caller's R(fnReg).
	// The callee wrote results to the register array at calleeBase + RetBase*ValueSize.
	// Source address: regRegs + calleeOffset + RetBase * ValueSize
	EmitMulValueSize(a, X4, X4, X6) // X4 = RetBase * ValueSize
	a.LoadImm64(X6, int64(calleeOffset))
	a.ADDreg(X4, X4, X6)           // X4 = calleeOffset + RetBase*ValueSize
	a.ADDreg(X4, regRegs, X4)      // X4 = source address in register array

	// Destination: regRegs + fnReg * ValueSize
	dstOff := fnReg * ValueSize
	a.LoadImm64(X6, int64(dstOff))
	a.ADDreg(X6, regRegs, X6)      // X6 = destination address

	// Copy 24 bytes (one Value) from source to destination.
	a.LDR(X7, X4, 0)
	a.STR(X7, X6, 0)
	a.LDR(X7, X4, 8)
	a.STR(X7, X6, 8)
	a.LDR(X7, X4, 16)
	a.STR(X7, X6, 16)

	// Update ctx.Top for subsequent B=0 calls.
	// Top = fnReg + RetCount
	a.LoadImm64(X7, int64(fnReg))
	a.ADDreg(X7, X7, X5) // X7 = fnReg + RetCount
	a.STR(X7, regCtx, ctxOffTop)

	// All three cold paths deferred to cold section.
	getglobalPC := info.getglobalPC

	// Callee returned non-zero exit code.
	cg.deferCold(calleeExitLabel, func() {
		cg.asm.MOVreg(SP, X29)
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
		cg.asm.LDR(regConsts, regCtx, ctxOffConstants)
		cg.asm.MOVimm16(regSelfDepth, 0)
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1) // side exit
		cg.asm.B("epilogue")
	})

	// Fallback: callee not compiled, use call-exit for the GETGLOBAL.
	capturedPinned := make(map[int]Reg, len(cg.pinnedRegs))
	for k, v := range cg.pinnedRegs {
		capturedPinned[k] = v
	}
	cg.deferCold(fallbackLabel, func() {
		for vmReg, armReg := range capturedPinned {
			cg.asm.STR(armReg, regRegs, regIvalOffset(vmReg))
			cg.asm.MOVimm16W(X9, TypeInt)
			cg.storeRegTyp(X9, vmReg)
		}
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 2) // call-exit
		cg.asm.B("epilogue")
	})

	// Depth exceeded: side exit.
	cg.deferCold(depthLabel, func() {
		cg.asm.SUBimm(regSelfDepth, regSelfDepth, 1)
		cg.asm.MOVreg(SP, X29)
		cg.asm.LDR(regRegs, regCtx, ctxOffRegs)
		cg.asm.MOVimm16(regSelfDepth, 0)
		cg.asm.LoadImm64(X1, int64(getglobalPC))
		cg.asm.STR(X1, regCtx, ctxOffExitPC)
		cg.asm.LoadImm64(X0, 1)
		cg.asm.B("epilogue")
	})

	return nil
}
