//go:build darwin && arm64

// tier3_compile.go is the entry point for the Tier 3 register-allocated compiler.
// Tier 3 = Tier 2 pipeline (TypeSpec, ConstProp, DCE) + register allocation.
//
// Values assigned to physical registers by AllocateRegisters stay in those
// registers across instructions within a block, eliminating LDR/STR traffic.
// Spilled values fall back to memory-to-memory (same as Tier 2).
//
// The register allocator assigns values to X20-X23, X28 (GPR) and D4-D11 (FPR).
// At block boundaries, all register-resident values are stored back to memory
// and reloaded at the target block entry. This is simpler than the full emit.go
// approach which tracks active registers across blocks.
//
// Architecture:
//   Bytecode -> BuildGraph -> TypeSpec -> ConstProp -> DCE -> AllocateRegisters -> Tier3Compile -> ARM64

package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/jit"
)

// tier3Context holds transient state during Tier 3 code generation.
// It extends tier2Context with register allocation state.
type tier3Context struct {
	fn       *Function
	asm      *jit.Assembler
	slotMap  map[int]int // SSA value ID -> home slot index in VM register file
	nextSlot int         // next available temp slot
	alloc    *RegAllocation

	// activeRegs tracks which value IDs currently have valid data in their
	// allocated physical register. Reset at each block boundary.
	activeRegs map[int]bool

	// crossBlockLive is the set of value IDs used in a different block than
	// where they are defined. These values must be written through to memory.
	crossBlockLive map[int]bool

	// exitIDs tracks instruction IDs that need resume entry points.
	exitIDs []int

	// deferredResumes tracks resume labels to emit after the epilogue.
	deferredResumes []tier2Resume
}

// Tier3Compile takes a Function (after optimization passes) and a register
// allocation, then produces executable ARM64 code. Values with register
// assignments use physical registers; others fall back to memory slots.
func Tier3Compile(fn *Function, alloc *RegAllocation) (*Tier2CompiledFunc, error) {
	crossBlockLive := computeCrossBlockLive(fn)

	tc := &tier3Context{
		fn:             fn,
		asm:            jit.NewAssembler(),
		slotMap:        make(map[int]int),
		nextSlot:       fn.NumRegs,
		alloc:          alloc,
		activeRegs:     make(map[int]bool),
		crossBlockLive: crossBlockLive,
	}

	// Assign home slots for all SSA values (same as Tier 2).
	tc.t3AssignSlots()

	// Emit prologue.
	tc.t3EmitPrologue()

	// Emit each basic block in RPO.
	for _, block := range fn.Blocks {
		tc.t3EmitBlock(block)
	}

	// Emit epilogue.
	tc.t3EmitEpilogue()

	// Emit deferred resume entry points.
	tc.t3EmitDeferredResumes()

	// Finalize: resolve labels.
	code, err := tc.asm.Finalize()
	if err != nil {
		return nil, fmt.Errorf("tier3: finalize error: %w", err)
	}

	// Allocate executable memory and write code.
	cb, err := jit.AllocExec(len(code) + 512)
	if err != nil {
		return nil, fmt.Errorf("tier3: alloc exec error: %w", err)
	}
	if err := cb.WriteCode(code); err != nil {
		cb.Free()
		return nil, fmt.Errorf("tier3: write code error: %w", err)
	}

	// Resolve resume addresses.
	resumes := make(map[int]int)
	for _, id := range tc.exitIDs {
		label := fmt.Sprintf("t3_resume_%d", id)
		off := tc.asm.LabelOffset(label)
		if off >= 0 {
			resumes[id] = off
		}
	}

	return &Tier2CompiledFunc{
		Code:     cb,
		Proto:    fn.Proto,
		NumSlots: tc.nextSlot,
		Resumes:  resumes,
	}, nil
}

// t3AssignSlots assigns a home slot for every SSA value.
// Same logic as Tier 2: LoadSlot values keep their original VM slot,
// all others get temp slots.
func (tc *tier3Context) t3AssignSlots() {
	for _, block := range tc.fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			if instr.Op == OpLoadSlot {
				tc.slotMap[instr.ID] = int(instr.Aux)
			} else {
				tc.slotMap[instr.ID] = tc.nextSlot
				tc.nextSlot++
			}
		}
	}
}

// t3EmitPrologue emits the ARM64 function prologue, then loads initial values
// into their allocated registers.
func (tc *tier3Context) t3EmitPrologue() {
	asm := tc.asm

	// Standard prologue: save callee-saved registers, set up pinned regs.
	asm.SUBimm(jit.SP, jit.SP, tier2FrameSize)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)

	asm.MOVreg(mRegCtx, jit.X0)
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))
}

// t3EmitEpilogue emits the ARM64 function epilogue. Before returning,
// stores all register-resident values back to memory so the caller can read them.
func (tc *tier3Context) t3EmitEpilogue() {
	asm := tc.asm

	asm.Label("t3_epilogue")
	asm.MOVimm16(jit.X0, 0)
	asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)

	asm.Label("t3_exit")

	// Restore callee-saved GPRs.
	asm.LDP(jit.X27, jit.X28, jit.SP, 80)
	asm.LDP(jit.X25, jit.X26, jit.SP, 64)
	asm.LDP(jit.X23, jit.X24, jit.SP, 48)
	asm.LDP(jit.X21, jit.X22, jit.SP, 32)
	asm.LDP(jit.X19, jit.X20, jit.SP, 16)
	asm.LDP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.SP, jit.SP, tier2FrameSize)
	asm.RET()
}

// t3EmitDeferredResumes emits resume entry points after the epilogue.
func (tc *tier3Context) t3EmitDeferredResumes() {
	for _, r := range tc.deferredResumes {
		label := fmt.Sprintf("t3_resume_%d", r.instrID)
		tc.asm.Label(label)
		tc.t3EmitResumePrologue()
		tc.asm.B(r.continueLabel)
	}
}

// t3EmitResumePrologue emits a resume prologue for re-entering JIT code.
func (tc *tier3Context) t3EmitResumePrologue() {
	asm := tc.asm

	asm.SUBimm(jit.SP, jit.SP, tier2FrameSize)
	asm.STP(jit.X29, jit.X30, jit.SP, 0)
	asm.ADDimm(jit.X29, jit.SP, 0)
	asm.STP(jit.X19, jit.X20, jit.SP, 16)
	asm.STP(jit.X21, jit.X22, jit.SP, 32)
	asm.STP(jit.X23, jit.X24, jit.SP, 48)
	asm.STP(jit.X25, jit.X26, jit.SP, 64)
	asm.STP(jit.X27, jit.X28, jit.SP, 80)

	asm.MOVreg(mRegCtx, jit.X0)
	asm.LDR(mRegRegs, mRegCtx, execCtxOffRegs)
	asm.LDR(mRegConsts, mRegCtx, execCtxOffConstants)
	asm.LoadImm64(mRegTagInt, nb64(jit.NB_TagInt))
	asm.LoadImm64(mRegTagBool, nb64(jit.NB_TagBool))
}

// t3EmitBlock emits ARM64 code for a single basic block.
// At block entry, resets active register tracking and loads values that have
// register allocations from their memory slots.
func (tc *tier3Context) t3EmitBlock(block *Block) {
	tc.asm.Label(fmt.Sprintf("t3_B%d", block.ID))

	// Reset active registers at block boundary.
	tc.activeRegs = make(map[int]bool)

	// Load register-allocated values that are live-in to this block.
	// For the entry block (block 0), LoadSlot values get loaded into registers.
	// For other blocks, phi results and values from predecessors are loaded
	// from their memory slots into allocated registers.
	tc.t3LoadBlockLiveIns(block)

	for _, instr := range block.Instrs {
		tc.t3EmitInstr(instr, block)
	}
}

// t3LoadBlockLiveIns loads values that are live-in to a block into their
// allocated physical registers. This handles:
// - LoadSlot values in the entry block
// - Phi results at the start of any block (their memory slots were written
//   by predecessor block phi moves)
func (tc *tier3Context) t3LoadBlockLiveIns(block *Block) {
	for _, instr := range block.Instrs {
		if instr.Op == OpLoadSlot {
			// If this LoadSlot has a register allocation, load it.
			if pr, ok := tc.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
				slot := int(instr.Aux)
				tc.asm.LDR(jit.Reg(pr.Reg), mRegRegs, t2SlotOffset(slot))
				tc.activeRegs[instr.ID] = true
			}
		} else if instr.Op == OpPhi {
			// Phi results: the predecessor wrote to the phi's memory slot.
			// Load into the allocated register.
			if pr, ok := tc.alloc.ValueRegs[instr.ID]; ok && !pr.IsFloat {
				slot, slotOk := tc.slotMap[instr.ID]
				if slotOk {
					tc.asm.LDR(jit.Reg(pr.Reg), mRegRegs, t2SlotOffset(slot))
					tc.activeRegs[instr.ID] = true
				}
			}
		} else {
			break // Only LoadSlot and Phi are at the start.
		}
	}
}
