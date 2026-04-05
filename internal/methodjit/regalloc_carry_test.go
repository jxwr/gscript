//go:build darwin && arm64

// regalloc_carry_test.go verifies that non-header loop-body blocks do not
// re-use the physical FPR assigned to a loop-header float phi for SSA values
// defined in the body. If they did, the body's arithmetic would clobber the
// loop-carried value at runtime and force per-use slot reloads (see
// emit_loop.go's computeSafeHeaderFPRegs conservatism).
//
// The test uses two routes:
//   1. A handwritten minimal IR (header with float phi → body with MulFloat)
//      to pin down the core invariant deterministically.
//   2. The real mandelbrot proto from ../../benchmarks/suite/mandelbrot.gs,
//      where the inner-loop body currently collides with the loop-carried
//      float phi (v64/v65 in current numbering).

package methodjit

import (
	"os"
	"testing"
)

// TestRegallocCarriesLoopHeaderPhis_Synthetic constructs a minimal loop IR by
// hand and asserts that the body block's MulFloat result is NOT placed in the
// same FPR as the header's float phi.
func TestRegallocCarriesLoopHeaderPhis_Synthetic(t *testing.T) {
	fn := &Function{NumRegs: 2}

	// Blocks:
	//   b0 (entry) -> b1 (header, phi) -> b2 (body, MulFloat) -> b1 (back-edge)
	//                                    \-> b3 (exit) -- no, keep simple:
	// Use header that branches to either body or exit.
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	b1 := &Block{ID: 1, defs: make(map[int]*Value)}
	b2 := &Block{ID: 2, defs: make(map[int]*Value)}
	b3 := &Block{ID: 3, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b2}
	b1.Succs = []*Block{b2, b3}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b1}
	b3.Preds = []*Block{b1}

	// b0: v_seed = ConstFloat 1.5 : float
	vSeed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	// b0: jump to b1
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0,
		Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{vSeed, b0Term}

	// b1: phi(seed, body_result) : float  (header phi)
	// b1: cond = ConstBool true : bool
	// b1: branch cond, b2, b3
	vPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1, Aux: 0}
	// Args will be wired after we create the body's MulFloat.
	vCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{vCond.Value()},
		Aux:  int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{vPhi, vCond, b1Term}

	// b2 (body): v_body = MulFloat(vPhi, vPhi) : float
	// b2: jump b1
	vBody := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{vPhi.Value(), vPhi.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2,
		Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{vBody, b2Term}

	// Wire phi args: from b0 -> vSeed, from b2 -> vBody.
	vPhi.Args = []*Value{vSeed.Value(), vBody.Value()}

	// b3 (exit): return vPhi
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{vPhi.Value()}}
	b3.Instrs = []*Instr{b3Term}

	alloc := AllocateRegisters(fn)

	phiReg, ok := alloc.ValueRegs[vPhi.ID]
	if !ok {
		t.Fatalf("phi v%d has no register assignment", vPhi.ID)
	}
	if !phiReg.IsFloat {
		t.Fatalf("phi v%d expected FPR, got GPR X%d", vPhi.ID, phiReg.Reg)
	}

	bodyReg, ok := alloc.ValueRegs[vBody.ID]
	if !ok {
		t.Fatalf("body MulFloat v%d has no register assignment", vBody.ID)
	}
	if !bodyReg.IsFloat {
		t.Fatalf("body MulFloat v%d expected FPR, got GPR X%d", vBody.ID, bodyReg.Reg)
	}

	if phiReg.Reg == bodyReg.Reg {
		t.Fatalf("body MulFloat v%d was assigned D%d, same as loop-header phi v%d (D%d); "+
			"this clobbers the loop-carried value",
			vBody.ID, bodyReg.Reg, vPhi.ID, phiReg.Reg)
	}
}

// TestRegallocCarriesLoopHeaderPhis_Mandelbrot runs the real mandelbrot inner
// loop through BuildGraph + TypeSpec + ConstProp + DCE + AllocateRegisters,
// then asserts that no SSA value defined in a non-header loop-body block is
// assigned to the same FPR as one of the innermost loop-header's float phis.
func TestRegallocCarriesLoopHeaderPhis_Mandelbrot(t *testing.T) {
	srcBytes, err := os.ReadFile("../../benchmarks/suite/mandelbrot.gs")
	if err != nil {
		t.Fatalf("read mandelbrot.gs: %v", err)
	}
	top := compileTop(t, string(srcBytes))

	target := findProtoByName(top, "mandelbrot")
	if target == nil {
		t.Fatalf("mandelbrot proto not found")
	}

	fn := BuildGraph(target)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)

	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		t.Fatalf("mandelbrot has no loops — unexpected")
	}

	// Collect every header's float-phi FPR assignment.
	// Check every non-header loop block: its non-phi, non-terminator
	// float-producing instructions must not share an FPR with the innermost
	// enclosing header's phis.
	checkedBodies := 0
	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] || li.loopHeaders[block.ID] {
			continue
		}
		innerHeader, ok := li.blockInnerHeader[block.ID]
		if !ok {
			continue
		}
		// Collect phi FPRs for this innermost header.
		phiFPRs := make(map[int]int) // regNum -> phiID
		for _, phiID := range li.loopPhis[innerHeader] {
			if pr, ok := alloc.ValueRegs[phiID]; ok && pr.IsFloat {
				phiFPRs[pr.Reg] = phiID
			}
		}
		if len(phiFPRs) == 0 {
			continue
		}
		// Walk non-phi, non-terminator instructions.
		foundFloatOp := false
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi || instr.Op.IsTerminator() {
				continue
			}
			pr, ok := alloc.ValueRegs[instr.ID]
			if !ok || !pr.IsFloat {
				continue
			}
			foundFloatOp = true
			if phiID, clash := phiFPRs[pr.Reg]; clash {
				t.Errorf("block B%d: v%d (%s) assigned D%d, same as header B%d phi v%d — clobbers loop-carried value",
					block.ID, instr.ID, instr.Op, pr.Reg, innerHeader, phiID)
			}
		}
		if foundFloatOp {
			checkedBodies++
		}
	}
	if checkedBodies == 0 {
		t.Fatalf("no loop-body block with float operations was examined — test vacuous")
	}
	t.Logf("checked %d non-header loop-body blocks for FPR clashes", checkedBodies)
}
