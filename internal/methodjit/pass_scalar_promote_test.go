// pass_scalar_promote_test.go exercises LoopScalarPromotionPass by
// constructing synthetic SSA graphs with loop-carried (obj, field)
// pairs and asserting that the pass hoists them into a header phi,
// inserts a pre-header init load, and inserts an exit-block store.
//
// Tests construct the loop, run LICMPass (which creates the pre-header
// structure required by ScalarPromotionPass), then run the pass and
// inspect the mutated IR.

package methodjit

import (
	"testing"
)

// buildPromoteFixture builds a loop fixture shared by the tests:
//
//	b0 (entry): bi = LoadSlot 0 (TypeTable); Jump b1
//	b1 (header): iphi = Phi(TypeInt, [zero, iphi_next]);
//	             cond = ConstBool true; Branch cond b2 b3
//	b2 (body):  bi_vx = GetField(bi, 7) TypeFloat
//	            delta = ConstFloat 0
//	            new_vx = SubFloat(bi_vx, delta)
//	            SetField(bi, 7, new_vx)
//	            iphi_next = AddInt(iphi, one)   [for the int phi back-edge]
//	            Jump b1
//	b3 (exit): Return bi
//
// The int phi + AddInt gives b1 a phi and a live back-edge so that
// computeLoopInfo recognizes b1 as a loop header.
//
// Returns the function and pointers to the four original blocks.
func buildPromoteFixture(t *testing.T) (*Function, *Block, *Block, *Block, *Block) {
	t.Helper()
	fn := &Function{NumRegs: 4}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0: bi = LoadSlot 0 (TypeTable); zero = ConstInt 0; one = ConstInt 1; jump b1
	bi := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	zero := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 1}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{bi, zero, one, b0Term}

	// b1: iphi = Phi(TypeInt) placeholder args; cond = ConstBool 1; branch cond b2 b3
	iphi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{iphi, cond, b1Term}

	// b2: bi_vx = GetField(bi, 7); delta = ConstFloat 0; new_vx = SubFloat(bi_vx, delta);
	//     SetField(bi, 7, new_vx); iphi_next = AddInt(iphi, one); jump b1
	biVx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat, Block: b2,
		Args: []*Value{bi.Value()}, Aux: 7, Aux2: 111}
	delta := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b2, Aux: 0}
	newVx := &Instr{ID: fn.newValueID(), Op: OpSubFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{biVx.Value(), delta.Value()}}
	setF := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown, Block: b2,
		Args: []*Value{bi.Value(), newVx.Value()}, Aux: 7, Aux2: 222}
	iphiNext := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{iphi.Value(), one.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{biVx, delta, newVx, setF, iphiNext, b2Term}

	// Wire iphi args now that iphi_next exists.
	iphi.Args = []*Value{zero.Value(), iphiNext.Value()}

	// b3: return bi
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{bi.Value()}}
	b3.Instrs = []*Instr{b3Term}

	assertValidates(t, fn, "fixture input")
	return fn, b0, b1, b2, b3
}

// countOpInBlock returns the number of instructions in b matching op. If
// matchAux != nil it also filters by Aux.
func countOpInBlock(b *Block, op Op, matchAux *int64) int {
	n := 0
	for _, instr := range b.Instrs {
		if instr.Op != op {
			continue
		}
		if matchAux != nil && instr.Aux != *matchAux {
			continue
		}
		n++
	}
	return n
}

// countPhis returns the number of leading OpPhi instructions in b.
func countPhis(b *Block) int {
	n := 0
	for _, instr := range b.Instrs {
		if instr.Op != OpPhi {
			break
		}
		n++
	}
	return n
}

// TestScalarPromotion_FloatField_HoistsAcrossBackEdge builds a simple
// float-field loop and verifies the pass promotes it to a header phi.
func TestScalarPromotion_FloatField_HoistsAcrossBackEdge(t *testing.T) {
	fn, _, b1, b2, b3 := buildPromoteFixture(t)

	// Run LICM first to create the pre-header.
	if _, err := LICMPass(fn); err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// Record phi count in header pre-promotion.
	prePhis := countPhis(b1)

	// Run the promotion pass.
	if _, err := ScalarPromotionPass(fn); err != nil {
		t.Fatalf("ScalarPromotionPass: %v", err)
	}
	assertValidates(t, fn, "after ScalarPromotion")

	// Body (b2) should have 0 GetField(bi,7) and 0 SetField(bi,7).
	field7 := int64(7)
	if n := countOpInBlock(b2, OpGetField, &field7); n != 0 {
		t.Fatalf("body still has %d OpGetField(bi,7); expected 0", n)
	}
	if n := countOpInBlock(b2, OpSetField, &field7); n != 0 {
		t.Fatalf("body still has %d OpSetField(bi,7); expected 0", n)
	}

	// Header should have one more phi than before, of TypeFloat.
	if got := countPhis(b1); got != prePhis+1 {
		t.Fatalf("expected header phi count %d, got %d", prePhis+1, got)
	}
	// Find the new float phi and verify its Type.
	var floatPhi *Instr
	for _, instr := range b1.Instrs {
		if instr.Op != OpPhi {
			break
		}
		if instr.Type == TypeFloat {
			floatPhi = instr
			break
		}
	}
	if floatPhi == nil {
		t.Fatalf("no TypeFloat phi in header after promotion")
	}

	// Pre-header block is b1.Preds[0]; must contain exactly one new
	// GetField(bi, 7).
	if len(b1.Preds) < 1 {
		t.Fatalf("header has no predecessors")
	}
	ph := b1.Preds[0]
	if n := countOpInBlock(ph, OpGetField, &field7); n != 1 {
		t.Fatalf("pre-header has %d OpGetField(bi,7); expected 1", n)
	}
	var initGet *Instr
	for _, instr := range ph.Instrs {
		if instr.Op == OpGetField && instr.Aux == 7 {
			initGet = instr
			break
		}
	}
	if initGet == nil || initGet.Aux2 != 111 {
		t.Fatalf("pre-header GetField did not preserve Aux2=111: %+v", initGet)
	}

	// Exit block (b3) should have a new SetField(bi, 7, phi) at the
	// top (before the return terminator). Its Args[1] must reference
	// the new float phi.
	if n := countOpInBlock(b3, OpSetField, &field7); n != 1 {
		t.Fatalf("exit block has %d OpSetField(bi,7); expected 1", n)
	}
	var exitSet *Instr
	for _, instr := range b3.Instrs {
		if instr.Op == OpSetField && instr.Aux == 7 {
			exitSet = instr
			break
		}
	}
	if exitSet == nil {
		t.Fatalf("exit SetField not found")
	}
	if exitSet.Aux2 != 222 {
		t.Fatalf("exit SetField did not preserve Aux2=222: %+v", exitSet)
	}
	if len(exitSet.Args) < 2 || exitSet.Args[1] == nil || exitSet.Args[1].ID != floatPhi.ID {
		t.Fatalf("exit SetField Args[1] should reference the new phi v%d, got %+v",
			floatPhi.ID, exitSet.Args)
	}

	// The in-loop OpSubFloat should now take the phi as its Args[0].
	var sub *Instr
	for _, instr := range b2.Instrs {
		if instr.Op == OpSubFloat {
			sub = instr
			break
		}
	}
	if sub == nil {
		t.Fatalf("body OpSubFloat was removed unexpectedly")
	}
	if len(sub.Args) < 1 || sub.Args[0] == nil || sub.Args[0].ID != floatPhi.ID {
		t.Fatalf("OpSubFloat Args[0] should reference phi v%d, got %+v",
			floatPhi.ID, sub.Args)
	}
}

func TestScalarPromotion_SplitsCriticalExitEdge(t *testing.T) {
	fn, b0, b1, b2, b3 := buildPromoteFixture(t)

	// Make the loop exit block also reachable from outside the loop, matching
	// nbody's inner-loop exit into the outer-loop header.
	startCond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b0, Aux: 1}
	branch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b0,
		Args: []*Value{startCond.Value()}, Aux: int64(b1.ID), Aux2: int64(b3.ID)}
	b0.Instrs = append(b0.Instrs[:len(b0.Instrs)-1], startCond, branch)
	b0.Succs = []*Block{b1, b3}
	b3.Preds = append(b3.Preds, b0)
	assertValidates(t, fn, "critical-exit input")

	if _, err := LICMPass(fn); err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	preBlocks := len(fn.Blocks)
	if _, err := ScalarPromotionPass(fn); err != nil {
		t.Fatalf("ScalarPromotionPass: %v", err)
	}
	assertValidates(t, fn, "after ScalarPromotion")
	if len(fn.Blocks) != preBlocks+1 {
		t.Fatalf("expected one split exit block, blocks before=%d after=%d\n%s", preBlocks, len(fn.Blocks), Print(fn))
	}

	field7 := int64(7)
	if n := countOpInBlock(b2, OpGetField, &field7); n != 0 {
		t.Fatalf("body still has %d OpGetField(bi,7); expected 0", n)
	}
	if n := countOpInBlock(b2, OpSetField, &field7); n != 0 {
		t.Fatalf("body still has %d OpSetField(bi,7); expected 0", n)
	}
	if n := countOpInBlock(b3, OpSetField, &field7); n != 0 {
		t.Fatalf("original exit block should not receive split-edge stores, got %d\n%s", n, Print(fn))
	}

	var split *Block
	for _, pred := range b3.Preds {
		if pred != b0 {
			split = pred
			break
		}
	}
	if split == nil {
		t.Fatalf("split exit block not found in exit preds: %+v", b3.Preds)
	}
	if n := countOpInBlock(split, OpSetField, &field7); n != 1 {
		t.Fatalf("split exit block has %d OpSetField(bi,7); expected 1\n%s", n, Print(fn))
	}
	var splitSet *Instr
	for _, instr := range split.Instrs {
		if instr.Op == OpSetField && instr.Aux == 7 {
			splitSet = instr
			break
		}
	}
	if splitSet == nil || splitSet.Aux2 != 222 {
		t.Fatalf("split SetField did not preserve Aux2=222: %+v", splitSet)
	}
	last := split.Instrs[len(split.Instrs)-1]
	if last.Op != OpJump || last.Aux != int64(b3.ID) {
		t.Fatalf("split exit block should jump to original exit, got %s Aux=%d", last.Op, last.Aux)
	}
}

// TestScalarPromotion_NoHoist_WhenCallInLoop verifies that the presence
// of any OpCall in the loop body disables promotion (calls can alias).
func TestScalarPromotion_NoHoist_WhenCallInLoop(t *testing.T) {
	fn, _, b1, b2, _ := buildPromoteFixture(t)

	// Add an OpCall in b2 before the terminator: call bi() with no args.
	// Insert before b2's last instruction.
	callInstr := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny, Block: b2,
		Args: []*Value{b2.Instrs[0].Args[0]}} // bi value (from the GetField's arg)
	nInstrs := len(b2.Instrs)
	b2.Instrs = append(b2.Instrs[:nInstrs-1], callInstr, b2.Instrs[nInstrs-1])

	if _, err := LICMPass(fn); err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	prePhis := countPhis(b1)

	if _, err := ScalarPromotionPass(fn); err != nil {
		t.Fatalf("ScalarPromotionPass: %v", err)
	}
	assertValidates(t, fn, "after ScalarPromotion")

	// Both GetField and SetField for field 7 should still be in the body.
	field7 := int64(7)
	if n := countOpInBlock(b2, OpGetField, &field7); n != 1 {
		t.Fatalf("body should still have 1 OpGetField(bi,7); got %d", n)
	}
	if n := countOpInBlock(b2, OpSetField, &field7); n != 1 {
		t.Fatalf("body should still have 1 OpSetField(bi,7); got %d", n)
	}
	if got := countPhis(b1); got != prePhis {
		t.Fatalf("header phi count changed: before %d, after %d", prePhis, got)
	}
}

// TestScalarPromotion_NoHoist_WhenNoSetField verifies that a pair with
// only an in-loop load and no store is not promoted by this pass
// (it's LICM's job to hoist a pure load).
func TestScalarPromotion_NoHoist_WhenNoSetField(t *testing.T) {
	fn := &Function{NumRegs: 4}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0: bi = LoadSlot 0; zero = ConstInt 0; one = ConstInt 1; jump b1
	bi := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	zero := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 1}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{bi, zero, one, b0Term}

	// b1: iphi; cond; branch
	iphi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{iphi, cond, b1Term}

	// b2: bi_vx = GetField(bi, 7) TypeFloat; (no SetField!)
	//     iphi_next = AddInt(iphi, one); jump b1
	biVx := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeFloat, Block: b2,
		Args: []*Value{bi.Value()}, Aux: 7}
	iphiNext := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{iphi.Value(), one.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{biVx, iphiNext, b2Term}

	iphi.Args = []*Value{zero.Value(), iphiNext.Value()}

	// b3: return biVx (use the load so DCE wouldn't remove it)
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{biVx.Value()}}
	b3.Instrs = []*Instr{b3Term}

	assertValidates(t, fn, "no-set input")

	if _, err := LICMPass(fn); err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	prePhis := countPhis(b1)

	if _, err := ScalarPromotionPass(fn); err != nil {
		t.Fatalf("ScalarPromotionPass: %v", err)
	}
	assertValidates(t, fn, "after ScalarPromotion")

	// Phi count in b1 must be unchanged by this pass (LICM may have
	// hoisted the load out to the pre-header, but that's not our
	// concern — our pass must not add a phi for a pair with no store).
	if got := countPhis(b1); got != prePhis {
		t.Fatalf("header phi count changed: before %d, after %d", prePhis, got)
	}
}
