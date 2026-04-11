// pass_simplify_phis_test.go exercises SimplifyPhisPass by constructing
// synthetic SSA graphs containing trivially-redundant phi nodes and
// phi SCCs (self-referential chains) and asserting that the pass
// collapses them to their unique outer operand while preserving a
// valid CFG.
//
// Implements the test cases enumerated in the R31 plan for the
// Braun et al. 2013 Algorithm 5 (remove redundant phi SCCs) cleanup
// pass. The pass unblocks LICM on sieve-shaped nested loops where
// the per-phi cleanup run during SSA construction leaves SCC-redundant
// phi chains intact.

package methodjit

import (
	"testing"
)

// findPhiByID reports whether any block still contains a phi with the
// given value ID.
func findPhiByID(fn *Function, id int) bool {
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.ID == id && instr.Op == OpPhi {
				return true
			}
		}
	}
	return false
}

// findArgIDs returns the arg IDs of the instruction with the given ID
// (searches all blocks), or nil if not found.
func findArgIDs(fn *Function, id int) []int {
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.ID == id {
				ids := make([]int, len(instr.Args))
				for i, a := range instr.Args {
					if a != nil {
						ids[i] = a.ID
					} else {
						ids[i] = -1
					}
				}
				return ids
			}
		}
	}
	return nil
}

// ---------- Test 1: self-referential phi ----------
//
// CFG:
//   B0 -> B1
//   B1 (hdr, preds B0,B2): phi v1 = Phi(B0:v0, B2:v1); Branch cond -> B2,B3
//   B2 -> B1 (back-edge)
//   B3: use v1; Return
//
// After pass: v1 removed; use.Args[0].ID == v0.ID.
func TestSimplifyPhis_SelfReferential(t *testing.T) {
	fn := &Function{NumRegs: 2}
	b0 := newBlock(0)
	b1 := newBlock(1)
	b2 := newBlock(2)
	b3 := newBlock(3)
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b2}
	b1.Succs = []*Block{b2, b3}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b1}
	b3.Preds = []*Block{b1}

	// b0: v0 = ConstInt 42, Jump b1
	v0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 42}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{v0, b0Term}

	// b1: v1 = Phi(B0:v0, B2:v1 — set after), cond = ConstBool true, Branch
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1, Aux: 1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}
	phi.Args = []*Value{v0.Value(), phi.Value()} // self-ref back edge

	// b2: Jump b1
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{b2Term}

	// b3: use v1 (via AddInt with self to make the ID reachable), Return use
	use := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b3,
		Args: []*Value{phi.Value(), phi.Value()}}
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{use.Value()}}
	b3.Instrs = []*Instr{use, b3Term}

	assertValidates(t, fn, "input")

	phiID := phi.ID
	v0ID := v0.ID
	useID := use.ID

	_, err := SimplifyPhisPass(fn)
	if err != nil {
		t.Fatalf("SimplifyPhisPass error: %v", err)
	}
	assertValidates(t, fn, "after SimplifyPhis")

	if findPhiByID(fn, phiID) {
		t.Fatalf("self-referential phi v%d should have been removed", phiID)
	}
	gotArgs := findArgIDs(fn, useID)
	if len(gotArgs) != 2 {
		t.Fatalf("use instr v%d not found or wrong arity: %v", useID, gotArgs)
	}
	for i, id := range gotArgs {
		if id != v0ID {
			t.Fatalf("use.Args[%d] should have been replaced by v%d (got v%d)", i, v0ID, id)
		}
	}
}

// ---------- Test 2: 2-phi SCC collapses ----------
//
// CFG:
//   B0 -> B1
//   B1 (hdr, preds B0,B3): v1 = Phi(B0:v0, B3:v2); Branch cond -> B2,B4
//   B2 (single pred B1): Jump B3
//   B3 (single pred B2): v2 = Phi(B2:v1); Jump B1
//   B4 (single pred B1): use v1 and v2; Return
//
// Note: v2 here is a single-arg phi whose arg is v1, which closes an SCC
// v1 <-> v2. After pass both should collapse to v0.
func TestSimplifyPhis_TwoPhiSCC(t *testing.T) {
	fn := &Function{NumRegs: 2}
	b0 := newBlock(0)
	b1 := newBlock(1)
	b2 := newBlock(2)
	b3 := newBlock(3)
	b4 := newBlock(4)
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3, b4}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b3}
	b1.Succs = []*Block{b2, b4}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b3}
	b3.Preds = []*Block{b2}
	b3.Succs = []*Block{b1}
	b4.Preds = []*Block{b1}

	// b0
	v0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 7}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{v0, b0Term}

	// b1
	v1 := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1, Aux: 0}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b4.ID)}
	b1.Instrs = []*Instr{v1, cond, b1Term}

	// b2: jump b3
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b3.ID)}
	b2.Instrs = []*Instr{b2Term}

	// b3: v2 = Phi(B2:v1); Jump B1
	v2 := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b3, Aux: 0}
	b3Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b3, Aux: int64(b1.ID)}
	b3.Instrs = []*Instr{v2, b3Term}

	// Wire phi args (now that both phis exist):
	v1.Args = []*Value{v0.Value(), v2.Value()}
	v2.Args = []*Value{v1.Value()}

	// b4: use v1 and v2; Return
	useA := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b4,
		Args: []*Value{v1.Value(), v2.Value()}}
	b4Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b4,
		Args: []*Value{useA.Value()}}
	b4.Instrs = []*Instr{useA, b4Term}

	assertValidates(t, fn, "input")

	v0ID := v0.ID
	v1ID := v1.ID
	v2ID := v2.ID
	useID := useA.ID

	_, err := SimplifyPhisPass(fn)
	if err != nil {
		t.Fatalf("SimplifyPhisPass error: %v", err)
	}
	assertValidates(t, fn, "after SimplifyPhis")

	if findPhiByID(fn, v1ID) {
		t.Fatalf("phi v%d should have collapsed (2-phi SCC)", v1ID)
	}
	if findPhiByID(fn, v2ID) {
		t.Fatalf("phi v%d should have collapsed (2-phi SCC)", v2ID)
	}
	args := findArgIDs(fn, useID)
	if len(args) != 2 || args[0] != v0ID || args[1] != v0ID {
		t.Fatalf("use.Args should be [v%d, v%d], got %v", v0ID, v0ID, args)
	}
}

// ---------- Test 3: non-redundant phi must NOT collapse ----------
//
// CFG:
//   B0 (entry): Branch const -> B1, B2
//   B1: v0 = ConstInt 1; Jump B3
//   B2: v1 = ConstInt 2; Jump B3
//   B3: v2 = Phi(B1:v0, B2:v1); Return v2
func TestSimplifyPhis_NonRedundantUnchanged(t *testing.T) {
	fn := &Function{NumRegs: 2}
	b0 := newBlock(0)
	b1 := newBlock(1)
	b2 := newBlock(2)
	b3 := newBlock(3)
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3}

	b0.Succs = []*Block{b1, b2}
	b1.Preds = []*Block{b0}
	b1.Succs = []*Block{b3}
	b2.Preds = []*Block{b0}
	b2.Succs = []*Block{b3}
	b3.Preds = []*Block{b1, b2}

	// b0
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b0, Aux: 1}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b0,
		Args: []*Value{cond.Value()}, Aux: int64(b1.ID), Aux2: int64(b2.ID)}
	b0.Instrs = []*Instr{cond, b0Term}

	// b1
	v0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b1, Aux: int64(b3.ID)}
	b1.Instrs = []*Instr{v0, b1Term}

	// b2
	v1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b2, Aux: 2}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b3.ID)}
	b2.Instrs = []*Instr{v1, b2Term}

	// b3
	v2 := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b3, Aux: 0,
		Args: []*Value{v0.Value(), v1.Value()}}
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{v2.Value()}}
	b3.Instrs = []*Instr{v2, b3Term}

	assertValidates(t, fn, "input")
	phiID := v2.ID

	_, err := SimplifyPhisPass(fn)
	if err != nil {
		t.Fatalf("SimplifyPhisPass error: %v", err)
	}
	assertValidates(t, fn, "after SimplifyPhis")

	if !findPhiByID(fn, phiID) {
		t.Fatalf("non-redundant phi v%d should NOT have been removed", phiID)
	}
	args := findArgIDs(fn, phiID)
	if len(args) != 2 {
		t.Fatalf("phi v%d should retain 2 args, got %v", phiID, args)
	}
}

// ---------- Test 4: sieve-shaped 3-phi SCC ----------
//
// Nested loops:
//   B0 (entry): v_table = LoadSlot 0, v_n = LoadSlot 1, v_step = ConstInt 2
//               Jump B1 (outer hdr)
//   B1 (outer hdr, preds B0, Bback):
//       v_tbl_outer = Phi(B0:v_table, Bback:v_tbl_outer)
//       Branch cond -> B2 (inner ph), Bexit
//   B2 (inner preheader): Jump B3
//   B3 (inner hdr, preds B2, B4):
//       v_tbl_inner = Phi(B2:v_tbl_outer, B4:v_tbl_inner)
//       v_n_inner   = Phi(B2:v_n,         B4:v_n_inner)
//       v_stp_inner = Phi(B2:v_step,      B4:v_stp_inner)
//       Branch cond2 -> B4, Bafter
//   B4 (inner body, preds B3): use(v_tbl_inner, v_n_inner, v_stp_inner); Jump B3
//   Bafter (preds B3): Jump Bback
//   Bback (preds Bafter): Jump B1
//   Bexit (preds B1): Return
func TestSimplifyPhis_SieveShapedNestedLoops(t *testing.T) {
	fn := &Function{NumRegs: 4}
	b0 := newBlock(0)
	b1 := newBlock(1)
	b2 := newBlock(2)
	b3 := newBlock(3)
	b4 := newBlock(4)
	bAfter := newBlock(5)
	bBack := newBlock(6)
	bExit := newBlock(7)
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3, b4, bAfter, bBack, bExit}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, bBack}
	b1.Succs = []*Block{b2, bExit}
	b2.Preds = []*Block{b1}
	b2.Succs = []*Block{b3}
	b3.Preds = []*Block{b2, b4}
	b3.Succs = []*Block{b4, bAfter}
	b4.Preds = []*Block{b3}
	b4.Succs = []*Block{b3}
	bAfter.Preds = []*Block{b3}
	bAfter.Succs = []*Block{bBack}
	bBack.Preds = []*Block{bAfter}
	bBack.Succs = []*Block{b1}
	bExit.Preds = []*Block{b1}

	// b0 body
	vTable := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Block: b0, Aux: 0}
	vN := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Block: b0, Aux: 1}
	vStep := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 2}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{vTable, vN, vStep, b0Term}

	// b1 (outer hdr)
	vTblOuter := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1, Aux: 0}
	outerCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{outerCond.Value()}, Aux: int64(b2.ID), Aux2: int64(bExit.ID)}
	b1.Instrs = []*Instr{vTblOuter, outerCond, b1Term}

	// b2 (inner preheader)
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b3.ID)}
	b2.Instrs = []*Instr{b2Term}

	// b3 (inner hdr) — 3 phis (will be wired below)
	vTblInner := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b3, Aux: 0}
	vNInner := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b3, Aux: 1}
	vStpInner := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b3, Aux: 2}
	innerCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b3, Aux: 1}
	b3Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b3,
		Args: []*Value{innerCond.Value()}, Aux: int64(b4.ID), Aux2: int64(bAfter.ID)}
	b3.Instrs = []*Instr{vTblInner, vNInner, vStpInner, innerCond, b3Term}

	// b4 inner body
	use := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b4,
		Args: []*Value{vNInner.Value(), vStpInner.Value()}}
	useTbl := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b4,
		Args: []*Value{vTblInner.Value(), vTblInner.Value()}}
	b4Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b4, Aux: int64(b3.ID)}
	b4.Instrs = []*Instr{use, useTbl, b4Term}

	// Wire inner phi back-edges from B4 — they carry the phi itself (loop-invariant).
	vTblInner.Args = []*Value{vTblOuter.Value(), vTblInner.Value()}
	vNInner.Args = []*Value{vN.Value(), vNInner.Value()}
	vStpInner.Args = []*Value{vStep.Value(), vStpInner.Value()}

	// bAfter
	bAfterTerm := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: bAfter, Aux: int64(bBack.ID)}
	bAfter.Instrs = []*Instr{bAfterTerm}

	// bBack
	bBackTerm := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: bBack, Aux: int64(b1.ID)}
	bBack.Instrs = []*Instr{bBackTerm}

	// Wire outer phi back-edge (self-ref via Bback path): the outer phi's
	// second arg is itself (loop-invariant). Final value is vTable.
	vTblOuter.Args = []*Value{vTable.Value(), vTblOuter.Value()}

	// bExit
	bExitTerm := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: bExit}
	bExit.Instrs = []*Instr{bExitTerm}

	assertValidates(t, fn, "input")

	innerTblID := vTblInner.ID
	innerNID := vNInner.ID
	innerStpID := vStpInner.ID
	useID := use.ID
	useTblID := useTbl.ID
	vTableID := vTable.ID
	vNID := vN.ID
	vStepID := vStep.ID

	_, err := SimplifyPhisPass(fn)
	if err != nil {
		t.Fatalf("SimplifyPhisPass error: %v", err)
	}
	assertValidates(t, fn, "after SimplifyPhis")

	for _, id := range []int{innerTblID, innerNID, innerStpID} {
		if findPhiByID(fn, id) {
			t.Fatalf("inner phi v%d should have been collapsed", id)
		}
	}

	// use(v_n_inner, v_stp_inner) should reference v_n and v_step.
	args := findArgIDs(fn, useID)
	if len(args) != 2 || args[0] != vNID || args[1] != vStepID {
		t.Fatalf("use.Args should be [v%d, v%d], got %v", vNID, vStepID, args)
	}
	// useTbl refs v_tbl_inner twice → should resolve to v_table.
	tblArgs := findArgIDs(fn, useTblID)
	if len(tblArgs) != 2 || tblArgs[0] != vTableID || tblArgs[1] != vTableID {
		t.Fatalf("useTbl.Args should be [v%d, v%d], got %v", vTableID, vTableID, tblArgs)
	}
}

// ---------- Test 5: nil function guard ----------

func TestSimplifyPhis_NilFunction(t *testing.T) {
	out, err := SimplifyPhisPass(nil)
	if err != nil {
		t.Fatalf("SimplifyPhisPass(nil) should not error, got %v", err)
	}
	if out != nil {
		t.Fatalf("SimplifyPhisPass(nil) should return nil, got %v", out)
	}
}

// ---------- Test 6: no phis → no-op ----------

func TestSimplifyPhis_NoPhisNoop(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b0 := newBlock(0)
	fn.Entry = b0
	fn.Blocks = []*Block{b0}

	v0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 5}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b0,
		Args: []*Value{v0.Value()}}
	b0.Instrs = []*Instr{v0, ret}

	assertValidates(t, fn, "input")
	_, err := SimplifyPhisPass(fn)
	if err != nil {
		t.Fatalf("SimplifyPhisPass error: %v", err)
	}
	assertValidates(t, fn, "after SimplifyPhis")
	if len(b0.Instrs) != 2 {
		t.Fatalf("expected b0 to still have 2 instrs, got %d", len(b0.Instrs))
	}
}
