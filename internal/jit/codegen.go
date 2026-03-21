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
	fromReg    int    // source register (if traced through MOVE)
	fromConst  int64  // constant value (if traced through LOADINT)
	isConst    bool   // true if fromConst is valid
	traced     bool   // true if successfully traced
	setupPC    int    // PC of the MOVE/LOADINT/SUB that set up this arg (for skipping)
	arithOp    string // "SUB" or "ADD" if traced through arithmetic with const
	arithSrc   int    // source register for arithmetic (B operand of SUB/ADD)
	auxSetupPC int    // PC of the LOADINT that feeds the SUB/ADD C operand (-1 if none)
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
		// Reload pinned registers: load NaN-boxed value and unbox int.
		for _, vmReg := range stub.pinnedVars {
			if armReg, ok := stub.pinnedRegs[vmReg]; ok {
				cg.asm.LDR(armReg, regRegs, regValOffset(vmReg))
				EmitUnboxInt(cg.asm, armReg, armReg)
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
				EmitGuardType(a, regRegs, i, TypeInt, "self_param_guard_fail")
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
		a.LDR(regSelfArg, regRegs, regValOffset(0))
		EmitUnboxInt(a, regSelfArg, regSelfArg)
		if cg.proto.NumParams > 1 {
			a.LDR(regSelfArg2, regRegs, regValOffset(1))
			EmitUnboxInt(a, regSelfArg2, regSelfArg2)
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
// Value access helpers (NaN-boxing: 8-byte values)
// ──────────────────────────────────────────────────────────────────────────────

// regValOffset returns the byte offset of R(i) from regRegs (each Value = 8 bytes).
func regValOffset(i int) int {
	return i * ValueSize
}

// regIvalOffset returns the byte offset of R(i) from regRegs.
// With NaN-boxing, the int data is embedded in the Value itself.
// This returns the same offset as regValOffset for backward compatibility.
// NOTE: Code using this must be aware that loading from this offset returns
// a NaN-boxed value, NOT a raw int. Use EmitUnboxInt after loading.
func regIvalOffset(i int) int {
	return i * ValueSize
}

// regTypOffset returns the byte offset of R(i) from regRegs.
// With NaN-boxing there is no separate type field. This returns the same
// offset as regValOffset. Code using this must extract the tag via LSR #48.
func regTypOffset(i int) int {
	return i * ValueSize
}

// regFvalOffset returns the byte offset of R(i) from regRegs.
// With NaN-boxing, a float IS the raw bits of the Value.
func regFvalOffset(i int) int {
	return i * ValueSize
}

// loadRegTyp loads the NaN-boxed Value of R(reg) into dst, then extracts the
// tag into dst via LSR #48. After return, dst holds the top 16 bits of the value.
// Callers compare with NB_TagXxxShr48 constants.
// NOTE: the full value is lost; if you need both tag and value, load separately.
func (cg *Codegen) loadRegTyp(dst Reg, reg int) {
	off := regValOffset(reg)
	if off <= 32760 {
		cg.asm.LDR(dst, regRegs, off)
	} else {
		cg.asm.LoadImm64(X10, int64(off))
		cg.asm.ADDreg(X10, regRegs, X10)
		cg.asm.LDR(dst, X10, 0)
	}
	cg.asm.LSRimm(dst, dst, 48)
}

// storeRegTyp is a no-op placeholder for NaN-boxing.
// With NaN-boxing, the type is encoded in the value itself -- there is no
// separate type byte to store. Callers that used this must box values instead.
func (cg *Codegen) storeRegTyp(src Reg, reg int) {
	// No-op: NaN-boxing encodes the type in the value itself.
}

// loadRegIval loads the unboxed int from R(reg) into dst.
// For pinned registers, uses register-to-register MOV.
// For memory, loads the NaN-boxed value and sign-extends the 48-bit payload.
func (cg *Codegen) loadRegIval(dst Reg, reg int) {
	if armReg, ok := cg.pinnedRegs[reg]; ok {
		if dst != armReg {
			cg.asm.MOVreg(dst, armReg)
		}
		return
	}
	off := regValOffset(reg)
	cg.asm.LDR(dst, regRegs, off)
	EmitUnboxInt(cg.asm, dst, dst)
}

// storeRegIval stores a raw int64 as a NaN-boxed IntValue into R(reg).
// For pinned registers, uses register-to-register MOV (pinned regs hold raw ints).
func (cg *Codegen) storeRegIval(src Reg, reg int) {
	if armReg, ok := cg.pinnedRegs[reg]; ok {
		if src != armReg {
			cg.asm.MOVreg(armReg, src)
		}
		return
	}
	// Box the int and store the full NaN-boxed value
	EmitBoxInt(cg.asm, X10, src, X11)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// loadRegFval loads R(reg) as a float64 into the ARM64 FP register dst.
// With NaN-boxing, the float IS the raw value bits, so just FLDRd.
func (cg *Codegen) loadRegFval(dst FReg, reg int) {
	off := regValOffset(reg)
	cg.asm.FLDRd(dst, regRegs, off)
}

// storeRegFval stores a float64 into R(reg).
// With NaN-boxing, float bits ARE the value -- single FSTRd.
func (cg *Codegen) storeRegFval(src FReg, reg int) {
	off := regValOffset(reg)
	cg.asm.FSTRd(src, regRegs, off)
}

// storeIntValue stores a complete NaN-boxed IntValue to R(reg).
// valReg holds the raw int64 value. This boxes it and writes.
// For pinned registers, only updates the ARM register (no memory write).
func (cg *Codegen) storeIntValue(reg int, valReg Reg) {
	if armReg, pinned := cg.pinnedRegs[reg]; pinned {
		if !cg.hasSelfCalls {
			// Pinned regs hold raw ints, no boxing needed
		}
		if valReg != armReg {
			cg.asm.MOVreg(armReg, valReg)
		}
		return
	}
	// Box and store
	EmitBoxInt(cg.asm, X10, valReg, X11)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// storeNilValue stores NaN-boxed nil in R(reg).
func (cg *Codegen) storeNilValue(reg int) {
	EmitBoxNil(cg.asm, X10)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// storeBoolValue stores a NaN-boxed BoolValue to R(reg).
// valReg should contain 0 (false) or 1 (true).
func (cg *Codegen) storeBoolValue(reg int, valReg Reg) {
	EmitBoxBool(cg.asm, X10, valReg, X11)
	off := regValOffset(reg)
	cg.asm.STR(X10, regRegs, off)
}

// spillPinnedRegNB spills a pinned register as a NaN-boxed IntValue to memory.
// Used before side-exits and returns where the interpreter needs valid Values.
func (cg *Codegen) spillPinnedRegNB(vmReg int, armReg Reg) {
	EmitBoxInt(cg.asm, X10, armReg, X11)
	off := regValOffset(vmReg)
	cg.asm.STR(X10, regRegs, off)
}

// emitCmpTag compares the tag value in dst (after LSR #48) with a NaN-boxing tag.
// Uses X10 as scratch for the tag constant.
func (cg *Codegen) emitCmpTag(dst Reg, tagShr48 uint16) {
	cg.asm.MOVimm16(X10, tagShr48)
	cg.asm.CMPreg(dst, X10)
}

// ──────────────────────────────────────────────────────────────────────────────
// RK value loading (register or constant) -- NaN-boxing
// ──────────────────────────────────────────────────────────────────────────────

// loadRKTyp loads the tag of RK(idx) into dst (via LSR #48).
// After return, dst holds the top 16 bits for comparison with NB_TagXxxShr48.
func (cg *Codegen) loadRKTyp(dst Reg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx * ValueSize
		if off <= 32760 {
			cg.asm.LDR(dst, regConsts, off)
		} else {
			cg.asm.LoadImm64(X10, int64(off))
			cg.asm.ADDreg(X10, regConsts, X10)
			cg.asm.LDR(dst, X10, 0)
		}
		cg.asm.LSRimm(dst, dst, 48)
	} else {
		cg.loadRegTyp(dst, idx)
	}
}

// loadRKIval loads the unboxed int from RK(idx) into dst.
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
		// Load NaN-boxed value and unbox
		off := constIdx * ValueSize
		cg.asm.LDR(dst, regConsts, off)
		EmitUnboxInt(cg.asm, dst, dst)
	} else {
		cg.loadRegIval(dst, idx)
	}
}

// loadRKFval loads RK(idx) as float64 into dst.
// With NaN-boxing, the float IS the value bits -- just FLDRd from the slot.
func (cg *Codegen) loadRKFval(dst FReg, idx int) {
	if vm.IsRK(idx) {
		constIdx := vm.RKToConstIdx(idx)
		off := constIdx * ValueSize
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


// ──────────────────────────────────────────────────────────────────────────────
// Copy a full Value (8 bytes NaN-boxed) between registers.
// ──────────────────────────────────────────────────────────────────────────────

// copyValue copies the full NaN-boxed Value (8 bytes) from src to dst.
func (cg *Codegen) copyValue(dstReg, srcReg int) {
	srcOff := srcReg * ValueSize
	dstOff := dstReg * ValueSize
	a := cg.asm
	a.LDR(X0, regRegs, srcOff)
	a.STR(X0, regRegs, dstOff)
}

// copyRKValue copies a full NaN-boxed Value from RK(idx) to R(dst).
func (cg *Codegen) copyRKValue(dstReg, rkIdx int) {
	dstOff := dstReg * ValueSize
	a := cg.asm
	if vm.IsRK(rkIdx) {
		constIdx := vm.RKToConstIdx(rkIdx)
		srcOff := constIdx * ValueSize
		a.LDR(X0, regConsts, srcOff)
		a.STR(X0, regRegs, dstOff)
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

	// Load pinned registers from memory (NaN-boxed → unbox int).
	for _, vmReg := range cg.pinnedVars {
		armReg := cg.pinnedRegs[vmReg]
		cg.asm.LDR(armReg, regRegs, regValOffset(vmReg))
		EmitUnboxInt(cg.asm, armReg, armReg)
	}

	return true
}

// spillPinnedRegs stores all pinned ARM registers back to VM register memory.
// With NaN-boxing, boxes the raw int and writes a single 8-byte NaN-boxed Value.
func (cg *Codegen) spillPinnedRegs() {
	for vmReg, armReg := range cg.pinnedRegs {
		cg.spillPinnedRegNB(vmReg, armReg)
	}
}

// reloadPinnedRegs loads all pinned ARM registers from VM register memory.
// With NaN-boxing, loads the NaN-boxed value and unboxes the 48-bit int.
func (cg *Codegen) reloadPinnedRegs() {
	for _, vmReg := range cg.pinnedVars {
		armReg := cg.pinnedRegs[vmReg]
		off := regValOffset(vmReg)
		cg.asm.LDR(armReg, regRegs, off)
		EmitUnboxInt(cg.asm, armReg, armReg)
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
						cg.spillPinnedRegNB(vmReg, armReg)
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
					cg.spillPinnedRegNB(vmReg, armReg)
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

