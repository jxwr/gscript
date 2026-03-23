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

// Reserved register for the NaN-boxing int tag constant (0xFFFE000000000000).
// Pinned once in the prologue, used by EmitBoxIntFast to avoid reloading the tag
// on every box operation (saves 2+ instructions per EmitBoxInt call).
const regTagInt = X24

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
	a.LoadImm64(regTagInt, nb_i64(NB_TagInt)) // x24 = 0xFFFE000000000000 (int tag)

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

// pcLabel returns the label for a bytecode PC.
func pcLabel(pc int) string {
	return fmt.Sprintf("pc_%d", pc)
}

// Callee-saved registers available for pinning.
// NOTE: X24 (regTagInt) is excluded — it holds the NaN-boxing int tag constant.
var pinPool = []Reg{X19, X20, X21, X22, X23, X25}

