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

func TestRegallocCarriesRawIntIntoSinglePredBlock(t *testing.T) {
	fn := &Function{NumRegs: 1}

	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	b1 := &Block{ID: 1, defs: make(map[int]*Value)}
	b2 := &Block{ID: 2, defs: make(map[int]*Value)}
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2}

	b0.Succs = []*Block{b1, b2}
	b1.Preds = []*Block{b0}
	b2.Preds = []*Block{b0}

	vN := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 42}
	vCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b0, Aux: 1}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b0,
		Args: []*Value{vCond.Value()},
		Aux:  int64(b1.ID), Aux2: int64(b2.ID)}
	b0.Instrs = []*Instr{vN, vCond, b0Term}

	vDummyFn := &Instr{ID: fn.newValueID(), Op: OpConstNil, Type: TypeUnknown, Block: b1}
	vDummyCall := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeInt, Block: b1,
		Args: []*Value{vDummyFn.Value(), vN.Value()}}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b1,
		Args: []*Value{vN.Value()}}
	b1.Instrs = []*Instr{vDummyFn, vDummyCall, b1Term}

	vOne := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b2, Aux: 1}
	vSub := &Instr{ID: fn.newValueID(), Op: OpSubInt, Type: TypeInt, Block: b2,
		Args: []*Value{vN.Value(), vOne.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b2,
		Args: []*Value{vSub.Value()}}
	b2.Instrs = []*Instr{vOne, vSub, b2Term}

	alloc := AllocateRegisters(fn)
	nReg, ok := alloc.ValueRegs[vN.ID]
	if !ok || nReg.IsFloat {
		t.Fatalf("carried raw int v%d has no GPR assignment: %+v", vN.ID, alloc.ValueRegs[vN.ID])
	}
	oneReg, ok := alloc.ValueRegs[vOne.ID]
	if !ok || oneReg.IsFloat {
		t.Fatalf("successor const v%d has no GPR assignment: %+v", vOne.ID, alloc.ValueRegs[vOne.ID])
	}
	if oneReg.Reg == nReg.Reg {
		t.Fatalf("single-predecessor successor reused X%d for v%d before using live-in raw int v%d",
			nReg.Reg, vOne.ID, vN.ID)
	}
	subReg, ok := alloc.ValueRegs[vSub.ID]
	if !ok || subReg.IsFloat {
		t.Fatalf("successor sub v%d has no GPR assignment: %+v", vSub.ID, alloc.ValueRegs[vSub.ID])
	}
}

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

// TestRegalloc_PreheaderInvariantPinned constructs a synthetic IR with a
// pre-header containing two ConstFloat definitions, a loop header with a float
// phi, and a body that uses both constants and the phi. With
// CarryPreheaderInvariants enabled, the two ConstFloat values should be pinned
// in FPRs across loop-body blocks — each getting a distinct FPR that does not
// collide with the body's arithmetic result or with each other.
//
// CFG shape:
//
//	entry(b0) → preheader(b1) → header(b2) → body(b3) → header (back-edge)
//	                                        → exit(b4)
func TestRegalloc_PreheaderInvariantPinned(t *testing.T) {
	fn := &Function{NumRegs: 2, CarryPreheaderInvariants: true}

	b0 := &Block{ID: 0, defs: make(map[int]*Value)} // entry
	b1 := &Block{ID: 1, defs: make(map[int]*Value)} // pre-header
	b2 := &Block{ID: 2, defs: make(map[int]*Value)} // loop header
	b3 := &Block{ID: 3, defs: make(map[int]*Value)} // loop body
	b4 := &Block{ID: 4, defs: make(map[int]*Value)} // exit
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3, b4}

	// entry → preheader
	b0.Succs = []*Block{b1}
	// preheader → header (single successor, qualifies as pre-header)
	b1.Preds = []*Block{b0}
	b1.Succs = []*Block{b2}
	// header ← preheader, body (back-edge)
	b2.Preds = []*Block{b1, b3}
	b2.Succs = []*Block{b3, b4}
	// body ← header, → header (back-edge)
	b3.Preds = []*Block{b2}
	b3.Succs = []*Block{b2}
	// exit ← header
	b4.Preds = []*Block{b2}

	// b0 (entry): jump to b1
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0,
		Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{b0Term}

	// b1 (pre-header): two ConstFloat definitions + initial value for phi
	vConst1 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b1}
	vConst2 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b1}
	vSeed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b1,
		Aux: int64(b2.ID)}
	b1.Instrs = []*Instr{vConst1, vConst2, vSeed, b1Term}

	// b2 (header): phi(seed from b1, bodyResult from b3) : float
	vPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b2}
	// phi args wired after body value is created
	vCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b2, Aux: 1}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b2,
		Args: []*Value{vCond.Value()},
		Aux:  int64(b3.ID), Aux2: int64(b4.ID)}
	b2.Instrs = []*Instr{vPhi, vCond, b2Term}

	// b3 (body): uses both consts and the phi
	// vMul = MulFloat(vPhi, vConst1)
	// vAdd = AddFloat(vMul, vConst2)
	vMul := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{vPhi.Value(), vConst1.Value()}}
	vAdd := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{vMul.Value(), vConst2.Value()}}
	b3Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b3,
		Aux: int64(b2.ID)}
	b3.Instrs = []*Instr{vMul, vAdd, b3Term}

	// Wire phi: from b1 → vSeed, from b3 → vAdd
	vPhi.Args = []*Value{vSeed.Value(), vAdd.Value()}

	// b4 (exit): return vPhi
	b4Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b4,
		Args: []*Value{vPhi.Value()}}
	b4.Instrs = []*Instr{b4Term}

	alloc := AllocateRegisters(fn)

	// Both ConstFloat values should have FPR assignments.
	regConst1, ok1 := alloc.ValueRegs[vConst1.ID]
	if !ok1 {
		t.Fatalf("pre-header invariant vConst1 (v%d) has no register assignment", vConst1.ID)
	}
	if !regConst1.IsFloat {
		t.Fatalf("pre-header invariant vConst1 (v%d) expected FPR, got GPR X%d", vConst1.ID, regConst1.Reg)
	}

	regConst2, ok2 := alloc.ValueRegs[vConst2.ID]
	if !ok2 {
		t.Fatalf("pre-header invariant vConst2 (v%d) has no register assignment", vConst2.ID)
	}
	if !regConst2.IsFloat {
		t.Fatalf("pre-header invariant vConst2 (v%d) expected FPR, got GPR X%d", vConst2.ID, regConst2.Reg)
	}

	// The two invariants must have distinct FPRs.
	if regConst1.Reg == regConst2.Reg {
		t.Fatalf("both pre-header invariants assigned same FPR D%d", regConst1.Reg)
	}

	// Body's arithmetic results (vMul, vAdd) must not collide with the invariants.
	regMul, mulOk := alloc.ValueRegs[vMul.ID]
	regAdd, addOk := alloc.ValueRegs[vAdd.ID]

	if mulOk && regMul.IsFloat {
		if regMul.Reg == regConst1.Reg || regMul.Reg == regConst2.Reg {
			t.Errorf("body vMul (v%d) D%d collides with pinned invariant", vMul.ID, regMul.Reg)
		}
	}
	if addOk && regAdd.IsFloat {
		if regAdd.Reg == regConst1.Reg || regAdd.Reg == regConst2.Reg {
			t.Errorf("body vAdd (v%d) D%d collides with pinned invariant", vAdd.ID, regAdd.Reg)
		}
	}

	t.Logf("vConst1=D%d vConst2=D%d vPhi=D%d", regConst1.Reg, regConst2.Reg,
		alloc.ValueRegs[vPhi.ID].Reg)
	if mulOk {
		t.Logf("vMul=D%d", regMul.Reg)
	}
	if addOk {
		t.Logf("vAdd=D%d", regAdd.Reg)
	}
}

// TestRegalloc_InvariantBudgetRespected constructs a pre-header with 7
// ConstFloat definitions (more than the FPR budget allows) and verifies
// that the budget limits pinning. Pinned invariants are protected: no body
// instruction reuses their FPR. Unpinned invariants' FPRs (from pre-header
// block allocation) ARE reusable by body instructions, since those values
// are not in the carried map.
//
// Budget = len(allocatableFPRs) - reservedTemps - floatPhisInHeader
//
//	= 8 - 3 - 1 = 4
//
// So 4 invariants are pinned (protected), 3 are not.
func TestRegalloc_InvariantBudgetRespected(t *testing.T) {
	fn := &Function{NumRegs: 2, CarryPreheaderInvariants: true}

	b0 := &Block{ID: 0, defs: make(map[int]*Value)} // entry
	b1 := &Block{ID: 1, defs: make(map[int]*Value)} // pre-header
	b2 := &Block{ID: 2, defs: make(map[int]*Value)} // loop header
	b3 := &Block{ID: 3, defs: make(map[int]*Value)} // loop body
	b4 := &Block{ID: 4, defs: make(map[int]*Value)} // exit
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3, b4}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}
	b1.Succs = []*Block{b2}
	b2.Preds = []*Block{b1, b3}
	b2.Succs = []*Block{b3, b4}
	b3.Preds = []*Block{b2}
	b3.Succs = []*Block{b2}
	b4.Preds = []*Block{b2}

	// b0: jump to b1
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0,
		Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{b0Term}

	// b1 (pre-header): 7 ConstFloat defs + 1 seed for phi
	const numInvariants = 7
	invariants := make([]*Instr, numInvariants)
	b1Instrs := make([]*Instr, 0, numInvariants+2)
	for i := 0; i < numInvariants; i++ {
		inv := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b1}
		invariants[i] = inv
		b1Instrs = append(b1Instrs, inv)
	}
	vSeed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b1}
	b1Instrs = append(b1Instrs, vSeed)
	b1Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b1,
		Aux: int64(b2.ID)}
	b1Instrs = append(b1Instrs, b1Term)
	b1.Instrs = b1Instrs

	// b2 (header): phi(seed, bodyResult) : float
	vPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b2}
	vCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b2, Aux: 1}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b2,
		Args: []*Value{vCond.Value()},
		Aux:  int64(b3.ID), Aux2: int64(b4.ID)}
	b2.Instrs = []*Instr{vPhi, vCond, b2Term}

	// b3 (body): use all 7 invariants + phi. Create parallel partial sums
	// to force more simultaneous FPR usage (not a simple chain that only
	// needs 2 FPRs). Structure:
	//   p0 = AddFloat(invariants[0], invariants[1])
	//   p1 = AddFloat(invariants[2], invariants[3])
	//   p2 = AddFloat(invariants[4], invariants[5])
	//   p3 = AddFloat(invariants[6], vPhi)
	//   q0 = AddFloat(p0, p1)
	//   q1 = AddFloat(p2, p3)
	//   result = AddFloat(q0, q1)
	// This tree shape keeps p0..p3 live simultaneously, requiring 4+ FPRs.
	var bodyOps []*Instr
	bodyInstrs := make([]*Instr, 0, 10)

	vP0 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{invariants[0].Value(), invariants[1].Value()}}
	vP1 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{invariants[2].Value(), invariants[3].Value()}}
	vP2 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{invariants[4].Value(), invariants[5].Value()}}
	vP3 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{invariants[6].Value(), vPhi.Value()}}
	vQ0 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{vP0.Value(), vP1.Value()}}
	vQ1 := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{vP2.Value(), vP3.Value()}}
	vResult := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{vQ0.Value(), vQ1.Value()}}
	bodyOps = append(bodyOps, vP0, vP1, vP2, vP3, vQ0, vQ1, vResult)
	bodyInstrs = append(bodyInstrs, vP0, vP1, vP2, vP3, vQ0, vQ1, vResult)

	b3Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b3,
		Aux: int64(b2.ID)}
	bodyInstrs = append(bodyInstrs, b3Term)
	b3.Instrs = bodyInstrs

	// Wire phi: from b1 → vSeed, from b3 → vResult
	vPhi.Args = []*Value{vSeed.Value(), vResult.Value()}

	// b4 (exit): return vPhi
	b4Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b4,
		Args: []*Value{vPhi.Value()}}
	b4.Instrs = []*Instr{b4Term}

	alloc := AllocateRegisters(fn)

	// Collect body block FPR assignments (the FPRs used by body instructions).
	bodyFPRs := make(map[int]bool) // FPR register numbers used by body ops
	for _, op := range bodyOps {
		if pr, ok := alloc.ValueRegs[op.ID]; ok && pr.IsFloat {
			bodyFPRs[pr.Reg] = true
		}
	}

	// Count invariants whose FPR is NOT reused by any body instruction.
	// These are the truly "pinned" (protected) invariants.
	protectedCount := 0
	for _, inv := range invariants {
		pr, ok := alloc.ValueRegs[inv.ID]
		if !ok || !pr.IsFloat {
			continue
		}
		if !bodyFPRs[pr.Reg] {
			protectedCount++
		}
	}

	// Budget = len(allocatableFPRs) - reservedTemps - floatPhisInHeader
	// = 8 - 3 - 1 = 4
	expectedBudget := len(allocatableFPRs) - 3 - 1
	if expectedBudget < 0 {
		expectedBudget = 0
	}
	t.Logf("invariant budget: %d (%d FPRs - 3 reserved - 1 phi)", expectedBudget, len(allocatableFPRs))
	t.Logf("protected (pinned) invariants: %d / %d", protectedCount, numInvariants)

	if protectedCount > expectedBudget {
		t.Errorf("protected %d invariants, exceeds budget %d", protectedCount, expectedBudget)
	}
	if protectedCount == 0 && expectedBudget > 0 {
		t.Errorf("expected at least 1 protected invariant with budget %d, got 0", expectedBudget)
	}

	// Verify the phi also got an FPR.
	phiReg, phiOk := alloc.ValueRegs[vPhi.ID]
	if !phiOk {
		t.Fatalf("phi v%d has no register assignment", vPhi.ID)
	}
	if !phiReg.IsFloat {
		t.Fatalf("phi v%d expected FPR, got GPR", vPhi.ID)
	}

	// Verify no two pinned invariants share the same FPR.
	seen := make(map[int]int) // regNum → value ID
	for _, inv := range invariants {
		pr, ok := alloc.ValueRegs[inv.ID]
		if !ok || !pr.IsFloat {
			continue
		}
		if !bodyFPRs[pr.Reg] { // only check protected ones
			if prevID, dup := seen[pr.Reg]; dup {
				t.Errorf("protected invariants v%d and v%d both assigned D%d", prevID, inv.ID, pr.Reg)
			}
			seen[pr.Reg] = inv.ID
		}
	}
}
