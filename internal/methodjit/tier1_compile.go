//go:build darwin && arm64

// tier1_compile.go is the entry point for the Tier 1 baseline compiler.
// It walks bytecodes linearly and emits a fixed ARM64 template for each one.
// No SSA, no optimization, no register allocation.
//
// Register convention:
//   X19: ExecContext pointer (pinned)
//   X21: NaN-boxed self-closure cache (pinned)
//   X22: VM register R(0) pinned for fast slot-0 access (pinned)
//   X24: NaN-boxing int tag 0xFFFE000000000000 (pinned)
//   X25: NaN-boxing bool tag 0xFFFD000000000000 (pinned)
//   X26: VM register base pointer (pinned)
//   X27: constants pointer (pinned)
//   X0-X7: scratch registers for computation
//   D0-D3: scratch float registers
//
// Every value in the VM register file is NaN-boxed (8 bytes).
// The baseline compiler reads/writes NaN-boxed values directly.

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/vm"
)

// BaselineFunc holds the generated native code for a baseline-compiled function.
type BaselineFunc struct {
	Code              *jit.CodeBlock // executable memory
	Proto             *vm.FuncProto  // source function
	Labels            map[int]int    // bytecodePC -> code offset (for resume after exit)
	HasFieldOps       bool           // true if proto has GETFIELD/SETFIELD (skip syncFieldCache otherwise)
	GlobalValCache    []uint64       // per-PC NaN-boxed global value cache (0 = not cached)
	CallCache         []uint64       // per-PC CALL IC: boxed closure, direct entry, closure ptr, proto ptr
	CachedGlobalGen   uint64         // engine.globalCacheGen at time of last cache population
	DirectEntryOffset int            // byte offset of the direct entry point (for native BLR calls)
}

// Baseline frame size: save FP/LR + callee-saved GPRs (X19-X28) = 12 regs = 96 bytes.
// Rounded up to 16-byte alignment = 96.
const baselineFrameSize = 96

// pcLabel returns a label name for a given bytecode PC.
func pcLabel(pc int) string {
	return fmt.Sprintf("pc_%d", pc)
}

// CompileBaseline compiles a FuncProto to native ARM64 code using the baseline
// template compiler. Returns a BaselineFunc or error.
func CompileBaseline(proto *vm.FuncProto) (*BaselineFunc, error) {
	asm := jit.NewAssembler()
	code := proto.Code

	// Reset the global label counter for this compilation.
	baselineLabelID = 0

	// Track which PCs need resume stubs (for op-exit resume).
	var resumePCs []int

	// Emit prologue.
	emitBaselinePrologue(asm)

	// Set CallMode = 0 (normal entry) in prologue. This is needed because
	// the ExecContext is reused across calls, and a previous direct call
	// might have set CallMode = 1.
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffCallMode)

	// Jump past the direct entry (which is only reached via BLR from caller).
	asm.B("pc_0")

	// Emit the direct entry point for native BLR calls.
	// This is a lightweight prologue that only saves FP+LR (16 bytes).
	emitDirectEntryPrologue(asm)

	// Emit the self-call entry point: even lighter than direct_entry.
	// Skips MOVreg X19,X0 and LDR for Regs/Constants (same function).
	emitSelfCallEntryPrologue(asm)

	// Int-spec analysis: compute per-PC KnownInt slot bitmap. If eligible,
	// emit the param-entry guard so all three entry paths (normal, direct,
	// self-call) flow through it on their way to pc_0. Protos marked by
	// DisableIntSpec (from a prior deopt) skip analysis.
	var intInfo *knownIntInfo
	intSpecEnabled := false
	if !IsIntSpecDisabled(proto) {
		intInfo, intSpecEnabled = computeKnownIntSlots(proto)
	}
	guardEmitted := false
	if intSpecEnabled && intInfo.guardedParams != 0 {
		// All three entry prologues end with `B pc_0`. Place the guard at
		// the pc_0 label so every entry flows through it. We skip the usual
		// `asm.Label(pcLabel(0))` at pc==0 in the bytecode loop below.
		asm.Label(pcLabel(0))
		emitParamIntGuards(asm, intInfo.guardedParams)
		guardEmitted = true
	}

	// Walk bytecodes linearly.
	for pc := 0; pc < len(code); pc++ {
		// Label for this PC (used as jump target within JIT code).
		// Skip pc==0 when we already labeled it for the int-spec guard.
		if !(pc == 0 && guardEmitted) {
			asm.Label(pcLabel(pc))
		}

		inst := code[pc]
		op := vm.DecodeOp(inst)

		switch op {
		// ---- Constants & Loads ----
		case vm.OP_LOADNIL:
			emitBaselineLoadNil(asm, inst)
		case vm.OP_LOADBOOL:
			emitBaselineLoadBool(asm, inst, pc, code)
		case vm.OP_LOADINT:
			emitBaselineLoadInt(asm, inst)
		case vm.OP_LOADK:
			emitBaselineLoadK(asm, inst)

		// ---- Variables ----
		case vm.OP_MOVE:
			emitBaselineMove(asm, inst)

		// ---- Arithmetic (native) ----
		case vm.OP_ADD:
			if intSpecEligible(intSpecEnabled, intInfo, pc, inst, proto) {
				emitBaselineArithIntSpec(asm, inst, "add", pc)
			} else {
				emitBaselineArith(asm, inst, "add")
			}
		case vm.OP_SUB:
			if intSpecEligible(intSpecEnabled, intInfo, pc, inst, proto) {
				emitBaselineArithIntSpec(asm, inst, "sub", pc)
			} else {
				emitBaselineArith(asm, inst, "sub")
			}
		case vm.OP_MUL:
			if intSpecEligible(intSpecEnabled, intInfo, pc, inst, proto) {
				emitBaselineArithIntSpec(asm, inst, "mul", pc)
			} else {
				emitBaselineArith(asm, inst, "mul")
			}
		case vm.OP_DIV:
			emitBaselineDiv(asm, inst)
		case vm.OP_MOD:
			emitBaselineMod(asm, inst)
		case vm.OP_UNM:
			emitBaselineUnm(asm, inst)
		case vm.OP_NOT:
			emitBaselineNot(asm, inst)

		// ---- Comparison (native) ----
		case vm.OP_EQ:
			if intSpecEligible(intSpecEnabled, intInfo, pc, inst, proto) {
				emitBaselineEQIntSpec(asm, inst, pc, code)
			} else {
				emitBaselineEQ(asm, inst, pc, code)
			}
		case vm.OP_LT:
			if intSpecEligible(intSpecEnabled, intInfo, pc, inst, proto) {
				emitBaselineLTIntSpec(asm, inst, pc, code)
				// Spec path can't exit to Go — no resume PCs needed.
			} else {
				emitBaselineLT(asm, inst, pc, code)
				// LT has a string-operand slow path that exits to Go.
				// The handler resumes at pc+1 (no skip) or pc+2 (skip next).
				resumePCs = append(resumePCs, pc+1)
				if pc+2 <= len(code) {
					resumePCs = append(resumePCs, pc+2)
				}
			}
		case vm.OP_LE:
			if intSpecEligible(intSpecEnabled, intInfo, pc, inst, proto) {
				emitBaselineLEIntSpec(asm, inst, pc, code)
			} else {
				emitBaselineLE(asm, inst, pc, code)
				// LE has a string-operand slow path that exits to Go.
				resumePCs = append(resumePCs, pc+1)
				if pc+2 <= len(code) {
					resumePCs = append(resumePCs, pc+2)
				}
			}

		// ---- Logical test ----
		case vm.OP_TEST:
			emitBaselineTest(asm, inst, pc, code)
		case vm.OP_TESTSET:
			emitBaselineTestSet(asm, inst, pc, code)

		// ---- Jump ----
		case vm.OP_JMP:
			emitBaselineJmp(asm, inst, pc)

		// ---- For loop (native) ----
		case vm.OP_FORPREP:
			emitBaselineForPrep(asm, inst, pc)
		case vm.OP_FORLOOP:
			emitBaselineForLoop(asm, inst, pc)

		// ---- Return (native) ----
		case vm.OP_RETURN:
			emitBaselineReturn(asm, inst)

		// ---- Complex ops (exit to Go) ----
		// All op-exits need a resume stub at pc+1.
		case vm.OP_CALL:
			emitBaselineNativeCall(asm, inst, pc, proto)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_GETGLOBAL:
			emitBaselineGetGlobal(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_SETGLOBAL:
			emitBaselineOpExitABx(asm, inst, pc, vm.OP_SETGLOBAL)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_NEWTABLE:
			emitBaselineOpExit(asm, inst, pc, vm.OP_NEWTABLE)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_GETTABLE:
			emitBaselineGetTable(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1) // slow path may exit
		case vm.OP_SETTABLE:
			emitBaselineSetTable(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_GETFIELD:
			emitBaselineGetField(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_SETFIELD:
			emitBaselineSetField(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_SETLIST:
			emitBaselineOpExit(asm, inst, pc, vm.OP_SETLIST)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_APPEND:
			emitBaselineOpExit(asm, inst, pc, vm.OP_APPEND)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_CONCAT:
			emitBaselineOpExit(asm, inst, pc, vm.OP_CONCAT)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_LEN:
			emitBaselineLen(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_POW:
			emitBaselineOpExit(asm, inst, pc, vm.OP_POW)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_CLOSURE:
			emitBaselineOpExitABx(asm, inst, pc, vm.OP_CLOSURE)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_CLOSE:
			emitBaselineOpExit(asm, inst, pc, vm.OP_CLOSE)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_GETUPVAL:
			emitBaselineGetUpval(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_SETUPVAL:
			emitBaselineSetUpval(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_SELF:
			emitBaselineSelf(asm, inst, pc)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_VARARG:
			emitBaselineOpExit(asm, inst, pc, vm.OP_VARARG)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_TFORCALL:
			emitBaselineOpExit(asm, inst, pc, vm.OP_TFORCALL)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_TFORLOOP:
			emitBaselineTForLoop(asm, inst, pc)
		case vm.OP_GO:
			emitBaselineOpExit(asm, inst, pc, vm.OP_GO)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_MAKECHAN:
			emitBaselineOpExit(asm, inst, pc, vm.OP_MAKECHAN)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_SEND:
			emitBaselineOpExit(asm, inst, pc, vm.OP_SEND)
			resumePCs = append(resumePCs, pc+1)
		case vm.OP_RECV:
			emitBaselineOpExit(asm, inst, pc, vm.OP_RECV)
			resumePCs = append(resumePCs, pc+1)

		default:
			// Unknown opcode: emit an exit for safety.
			emitBaselineOpExit(asm, inst, pc, op)
			resumePCs = append(resumePCs, pc+1)
		}
	}

	// Emit a label for one-past-end.
	asm.Label(pcLabel(len(code)))

	// Emit epilogues: normal and direct.
	emitBaselineEpilogue(asm)
	emitDirectExitEpilogue(asm)

	// Deduplicate resume PCs.
	uniqueResume := make(map[int]bool, len(resumePCs))
	var dedupPCs []int
	for _, rpc := range resumePCs {
		if !uniqueResume[rpc] {
			uniqueResume[rpc] = true
			dedupPCs = append(dedupPCs, rpc)
		}
	}
	resumePCs = dedupPCs

	// Emit resume stubs for each op-exit. Each resume stub is a separate
	// entry point with its own prologue that re-establishes pinned registers,
	// then jumps to the continuation pcLabel.
	for _, rpc := range resumePCs {
		resumeLabel := fmt.Sprintf("resume_%d", rpc)
		asm.Label(resumeLabel)
		emitBaselineResumePrologue(asm)
		asm.B(pcLabel(rpc))
	}

	// Finalize.
	machineCode, err := asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("baseline: finalize error: %w", err)
	}

	// Allocate executable memory and write the code.
	block, err := jit.AllocExec(len(machineCode))
	if err != nil {
		return nil, fmt.Errorf("baseline: alloc error: %w", err)
	}
	if err := block.WriteCode(machineCode); err != nil {
		block.Free()
		return nil, fmt.Errorf("baseline: write error: %w", err)
	}

	// Build the Labels map: bytecodePC -> resume stub offset.
	// The Go-side Execute loop looks up Labels[resumePC] to find the
	// resume entry point for re-entering JIT code after an op-exit.
	labels := make(map[int]int, len(resumePCs))
	for _, rpc := range resumePCs {
		resumeLabel := fmt.Sprintf("resume_%d", rpc)
		off := asm.LabelOffset(resumeLabel)
		if off >= 0 {
			labels[rpc] = off
		}
	}

	// Scan bytecodes for field ops and GETGLOBAL to set flags.
	hasFieldOps := false
	hasGetGlobal := false
	hasCall := false
	for _, inst := range code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_GETFIELD || op == vm.OP_SETFIELD || op == vm.OP_SELF {
			hasFieldOps = true
		}
		if op == vm.OP_GETGLOBAL {
			hasGetGlobal = true
		}
		if op == vm.OP_CALL {
			hasCall = true
		}
	}

	// Allocate per-PC global value cache if any GETGLOBAL instructions exist.
	var globalValCache []uint64
	if hasGetGlobal {
		globalValCache = make([]uint64, len(code))
	}
	var callCache []uint64
	if hasCall {
		callCache = make([]uint64, len(code)*4)
	}

	// Get the direct entry offset for native BLR calls.
	directEntryOff := asm.LabelOffset("direct_entry")

	return &BaselineFunc{
		Code:              block,
		Proto:             proto,
		Labels:            labels,
		HasFieldOps:       hasFieldOps,
		GlobalValCache:    globalValCache,
		CallCache:         callCache,
		DirectEntryOffset: directEntryOff,
	}, nil
}

// emitBaselinePrologue emits the ARM64 function prologue for a baseline function.
// Saves callee-saved registers and sets up pinned registers.
func emitBaselinePrologue(asm *jit.Assembler) {
	// Allocate stack frame (must be 16-byte aligned).
	asm.SUBimm(jit.SP, jit.SP, baselineFrameSize)
	// Save FP/LR.
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	// Set FP = SP.
	asm.ADDimm(jit.X29, jit.SP, 0)
	// Save callee-saved GPRs.
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)

	// Set up pinned registers.
	// X0 holds ExecContext pointer (from callJIT trampoline).
	asm.MOVreg(mRegCtx, jit.X0)                       // X19 = ctx
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)        // X26 = ctx.Regs
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants) // X27 = ctx.Constants
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))    // X24 = 0xFFFE000000000000
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))  // X25 = 0xFFFD000000000000

	// Cache NaN-boxed self-closure value for fast self-call detection.
	// BaselineClosurePtr is set by the execution loop before calling JIT.
	asm.LDR(mRegSelfClosure, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LoadImm64(jit.X3, nbClosureTagBits)
	asm.ORRreg(mRegSelfClosure, mRegSelfClosure, jit.X3)

	// Pin R(0) to X22 for fast slot-0 access.
	asm.LDR(mRegR0, mRegRegs, 0)
}

// emitBaselineEpilogue emits the ARM64 function epilogue.
func emitBaselineEpilogue(asm *jit.Assembler) {
	// Normal return: set exit code = 0.
	asm.Label("baseline_epilogue")
	asm.MOVimm16(jit.X0, 0) // ExitNormal
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	// Shared register restore and return.
	asm.Label("baseline_exit")
	// Restore callee-saved GPRs.
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	// Restore FP, LR.
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	// Deallocate stack frame.
	asm.ADDimm(jit.SP, jit.SP, baselineFrameSize)
	// Return.
	asm.RET()
}

// emitBaselineResumePrologue emits a resume prologue for re-entering JIT code
// after an op-exit. This is called via callJIT(resumeAddr, ctxPtr) where
// X0 = ctxPtr. It must save callee-saved registers and re-establish pinned regs.
func emitBaselineResumePrologue(asm *jit.Assembler) {
	// Same as full prologue: save callee-saved, set up pinned regs.
	asm.SUBimm(jit.SP, jit.SP, baselineFrameSize)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)

	// Re-establish pinned registers from context.
	asm.MOVreg(mRegCtx, jit.X0)
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))

	// Cache NaN-boxed self-closure value for fast self-call detection.
	asm.LDR(mRegSelfClosure, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.LoadImm64(jit.X3, nbClosureTagBits)
	asm.ORRreg(mRegSelfClosure, mRegSelfClosure, jit.X3)

	// Pin R(0) to X22 for fast slot-0 access.
	asm.LDR(mRegR0, mRegRegs, 0)
}

// emitBaselineOpExitCommon is the shared exit sequence for baseline op-exits.
// It stores the exit descriptor and jumps to the exit epilogue.
func emitBaselineOpExitCommon(asm *jit.Assembler, op vm.Opcode, pc int, a, b, c int) {
	// Lazy flush: publish ctx.Regs — caller-side STR was elided on self-call fast path.
	asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)

	// Note: no need to flush pinned R(0) — storeSlot always writes to memory
	// first, so memory is always in sync. Resume prologue reloads X22.

	// Set ExitCode = ExitBaselineOpExit (7)
	asm.LoadImm64(jit.X0, ExitBaselineOpExit)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	// Store op descriptor.
	asm.LoadImm64(jit.X0, int64(op))
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineOp)

	// Resume PC = pc+1 (the next bytecode instruction).
	asm.LoadImm64(jit.X0, int64(pc+1))
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselinePC)

	asm.LoadImm64(jit.X0, int64(a))
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineA)

	asm.LoadImm64(jit.X0, int64(b))
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineB)

	asm.LoadImm64(jit.X0, int64(c))
	asm.STR(jit.X0, mRegCtx, execCtxOffBaselineC)

	// Check CallMode to choose the right exit path.
	// CallMode == 0: normal entry, use baseline_exit (96-byte frame)
	// CallMode == 1: direct entry, use direct_exit (16-byte frame)
	asm.LDR(jit.X0, mRegCtx, execCtxOffCallMode)
	asm.CBNZ(jit.X0, "direct_exit")
	asm.B("baseline_exit")
}

// emitBaselineOpExit emits a baseline op-exit for an ABC-format instruction.
func emitBaselineOpExit(asm *jit.Assembler, inst uint32, pc int, op vm.Opcode) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst)

	// For OP_CALL: pass raw B and C values to the Go handler.
	// The handler decodes them following the same convention as the VM:
	//   B=0: variable args (use vm.top)
	//   C=0: return all values
	//   else: nArgs=B-1, nRets=C-1
	if op == vm.OP_CALL {
		emitBaselineOpExitCommon(asm, op, pc, a, b, c)
		return
	}

	emitBaselineOpExitCommon(asm, op, pc, a, b, c)
}

// emitBaselineOpExitABx emits a baseline op-exit for an ABx-format instruction.
func emitBaselineOpExitABx(asm *jit.Assembler, inst uint32, pc int, op vm.Opcode) {
	a := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)
	emitBaselineOpExitCommon(asm, op, pc, a, bx, 0)
}

// intSpecEligible reports whether an ADD/SUB/MUL/EQ/LT/LE instruction at the
// given PC is eligible for the int-specialized template: both RK operands
// must be statically known to hold int48 values. Returns false when int-spec
// is globally disabled for this proto or the analyzer returned nil.
func intSpecEligible(enabled bool, info *knownIntInfo, pc int, inst uint32, proto *vm.FuncProto) bool {
	if !enabled || info == nil {
		return false
	}
	bidx := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)
	return info.isKnownIntOperand(pc, bidx, proto.Constants) &&
		info.isKnownIntOperand(pc, cidx, proto.Constants)
}
