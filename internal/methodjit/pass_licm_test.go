// pass_licm_test.go exercises LICMPass by constructing synthetic
// SSA graphs and asserting that hoist-eligible instructions end up in
// a newly created pre-header block while ineligible instructions stay
// in the loop body. The validator is run after every hoist so invalid
// CFG rewrites fail loud.

package methodjit

import (
	"testing"
)

// ---------- helpers ----------

// findInstrByID returns the (block, instr) containing the given value
// ID, or (nil, nil) if not found.
func findInstrByID(fn *Function, id int) (*Block, *Instr) {
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if instr.ID == id {
				return b, instr
			}
		}
	}
	return nil, nil
}

// assertValidates runs Validate on the function and fails the test if
// any errors are reported.
func assertValidates(t *testing.T, fn *Function, where string) {
	t.Helper()
	if errs := Validate(fn); len(errs) > 0 {
		t.Fatalf("%s: IR failed validation:\n%v", where, errs)
	}
}

// newBlock creates a fresh block with a defs map initialized.
func newBlock(id int) *Block {
	return &Block{ID: id, defs: make(map[int]*Value)}
}

// ---------- Test 1: no loops → no-op ----------

func TestLICM_NoLoops_Noop(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b0 := newBlock(0)
	b1 := newBlock(1)
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}

	// b0: ConstInt 42, Jump b1
	v0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 42}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{v0, b0Term}
	// b1: Return v0
	b1Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b1,
		Args: []*Value{v0.Value()}}
	b1.Instrs = []*Instr{b1Term}

	assertValidates(t, fn, "input")
	origBlocks := len(fn.Blocks)

	out, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass error: %v", err)
	}
	if out != fn {
		t.Fatalf("LICMPass should return same fn when no loops present")
	}
	if len(fn.Blocks) != origBlocks {
		t.Fatalf("LICMPass must not add blocks to loopless fn (was %d, now %d)",
			origBlocks, len(fn.Blocks))
	}
	assertValidates(t, fn, "after LICM")
}

// ---------- Test 2: hoist ConstFloat ----------

// buildSimpleLoop constructs:
//   b0 (entry) -> b1 (header, phi) ->[true] b2 (body) ->(jump) b1
//                                   \->[false] b3 (exit)
// Caller fills b2's body instrs and b1's branch cond.
func buildSimpleLoop(fn *Function) (entry, header, body, exit *Block) {
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
	return b0, b1, b2, b3
}

func TestLICM_HoistConstFloat(t *testing.T) {
	fn := &Function{NumRegs: 2}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0: seed = ConstFloat 1.0, jump b1
	seed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	// b1: phi(seed, body_mul), cond = ConstBool true, branch cond b2 b3
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: k = ConstFloat 2.0, mul = MulFloat(phi, k), jump b1
	k := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b2, Aux: 0}
	mul := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{phi.Value(), k.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{k, mul, b2Term}

	phi.Args = []*Value{seed.Value(), mul.Value()}

	// b3: return phi
	b3Term := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{phi.Value()}}
	b3.Instrs = []*Instr{b3Term}

	assertValidates(t, fn, "input")

	kID := k.ID
	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass error: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// k should have moved OUT of b2.
	for _, instr := range b2.Instrs {
		if instr.ID == kID {
			t.Fatalf("ConstFloat k (v%d) should have been hoisted out of body block B%d",
				kID, b2.ID)
		}
	}
	// k should be in the pre-header, which is the new unique predecessor
	// of the original header that is itself not the header.
	phBlock, _ := findInstrByID(fn, kID)
	if phBlock == nil {
		t.Fatalf("hoisted k (v%d) not found anywhere in fn", kID)
	}
	// Preheader should be a direct predecessor of b1.
	if len(b1.Preds) < 1 || b1.Preds[0] != phBlock {
		t.Fatalf("expected hoisted constant to live in a pre-header that is b1.Preds[0], "+
			"got b1.Preds=%v, phBlock=B%d", blockIDs(b1.Preds), phBlock.ID)
	}
}

// blockIDs returns the IDs of a slice of blocks as a printable slice.
func blockIDs(bs []*Block) []int {
	out := make([]int, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

// ---------- Test 3: hoist LoadSlot with no store ----------

func TestLICM_HoistLoadSlot_NoStore(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0: seed = ConstFloat 0, jump b1
	seed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	// b1: phi, cond, branch
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: ls = LoadSlot 5, add = AddFloat(phi, ls), jump b1
	ls := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Block: b2, Aux: 5}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{phi.Value(), ls.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{ls, add, b2Term}

	phi.Args = []*Value{seed.Value(), add.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	lsID := ls.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// LoadSlot should be hoisted out of b2.
	for _, instr := range b2.Instrs {
		if instr.ID == lsID {
			t.Fatalf("LoadSlot v%d should have been hoisted out of body B%d", lsID, b2.ID)
		}
	}
}

// ---------- Test 4: no-hoist LoadSlot when store to same slot ----------

func TestLICM_NoHoistLoadSlot_WhenStored(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0
	seed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	// b1
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: ls = LoadSlot 5, add = AddFloat(phi, ls), StoreSlot 5 = add, jump b1
	ls := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeFloat, Block: b2, Aux: 5}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{phi.Value(), ls.Value()}}
	store := &Instr{ID: fn.newValueID(), Op: OpStoreSlot, Type: TypeUnknown, Block: b2, Aux: 5,
		Args: []*Value{add.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{ls, add, store, b2Term}

	phi.Args = []*Value{seed.Value(), add.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	lsID := ls.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// LoadSlot must stay in b2.
	foundInBody := false
	for _, instr := range b2.Instrs {
		if instr.ID == lsID {
			foundInBody = true
			break
		}
	}
	if !foundInBody {
		t.Fatalf("LoadSlot v%d of slot 5 should NOT be hoisted (StoreSlot 5 in same loop)", lsID)
	}
}

// ---------- Test 5: hoist GuardType with invariant operand ----------

func TestLICM_HoistGuardType(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GuardType(seed, TypeInt) — args are invariant, guard should be hoisted.
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeInt, Block: b2,
		Args: []*Value{seed.Value()}, Aux: int64(TypeInt)}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{guard, b2Term}

	phi.Args = []*Value{seed.Value(), seed.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	guardID := guard.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// Guard should be hoisted OUT of b2.
	for _, instr := range b2.Instrs {
		if instr.ID == guardID {
			t.Fatalf("OpGuardType v%d should have been hoisted out of body B%d", guardID, b2.ID)
		}
	}
	// Should be in the pre-header now.
	phBlock, _ := findInstrByID(fn, guardID)
	if phBlock == nil {
		t.Fatalf("hoisted GuardType v%d not found anywhere in fn", guardID)
	}
	if len(b1.Preds) < 1 || b1.Preds[0] != phBlock {
		t.Fatalf("expected hoisted GuardType to live in pre-header (b1.Preds[0]), "+
			"got b1.Preds=%v, phBlock=B%d", blockIDs(b1.Preds), phBlock.ID)
	}
}

// ---------- Test 6: hoist GetField when no store and no call ----------

func TestLICM_HoistGetField_NoStoreNoCall(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{tbl, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GetField(tbl), jump b1 — no SetField, no Call in loop
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b2,
		Args: []*Value{tbl.Value()}, Aux: 0}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gf, b2Term}

	phi.Args = []*Value{tbl.Value(), gf.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	gfID := gf.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetField should be hoisted OUT of b2 (no store/call in loop).
	for _, instr := range b2.Instrs {
		if instr.ID == gfID {
			t.Fatalf("OpGetField v%d should have been hoisted out of body B%d", gfID, b2.ID)
		}
	}
	// Should be in the pre-header now.
	phBlock, _ := findInstrByID(fn, gfID)
	if phBlock == nil {
		t.Fatalf("hoisted GetField v%d not found anywhere in fn", gfID)
	}
	if len(b1.Preds) < 1 || b1.Preds[0] != phBlock {
		t.Fatalf("expected hoisted GetField to live in pre-header (b1.Preds[0]), "+
			"got b1.Preds=%v, phBlock=B%d", blockIDs(b1.Preds), phBlock.ID)
	}
}

// ---------- Test 6b: no-hoist GetField when SetField on same (obj, field) ----------

func TestLICM_NoHoistGetField_WhenStored(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{tbl, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GetField(tbl, field=0), SetField(tbl, field=0, val), jump b1
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b2,
		Args: []*Value{tbl.Value()}, Aux: 0}
	val := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b2, Aux: 42}
	sf := &Instr{ID: fn.newValueID(), Op: OpSetField, Type: TypeUnknown, Block: b2,
		Args: []*Value{tbl.Value(), val.Value()}, Aux: 0}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gf, val, sf, b2Term}

	phi.Args = []*Value{tbl.Value(), gf.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	gfID := gf.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetField must stay in b2 because SetField writes the same (obj, field).
	found := false
	for _, instr := range b2.Instrs {
		if instr.ID == gfID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("OpGetField v%d should NOT be hoisted (SetField on same obj+field in loop)", gfID)
	}
}

// ---------- Test 6c: no-hoist GetField when call in loop ----------

func TestLICM_NoHoistGetField_WhenCallInLoop(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	callee := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Block: b0, Aux: 1}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{tbl, callee, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GetField(tbl, field=0), Call(callee), jump b1
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b2,
		Args: []*Value{tbl.Value()}, Aux: 0}
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny, Block: b2,
		Args: []*Value{callee.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gf, call, b2Term}

	phi.Args = []*Value{tbl.Value(), gf.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	gfID := gf.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetField must stay in b2 because a Call in the loop may mutate any field.
	found := false
	for _, instr := range b2.Instrs {
		if instr.ID == gfID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("OpGetField v%d should NOT be hoisted (Call in loop body)", gfID)
	}
}

// ---------- Test 7: pre-header phi reorder ----------

func TestLICM_PreHeaderPhiReorder(t *testing.T) {
	fn := &Function{NumRegs: 2}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	seed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// Body: add a ConstFloat that will be hoisted, so a pre-header is created.
	k := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b2, Aux: 0}
	mul := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{phi.Value(), k.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{k, mul, b2Term}

	// phi.Args ordered per hdr.Preds = [b0 (outside), b2 (inside)].
	phi.Args = []*Value{seed.Value(), mul.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	seedID := seed.ID
	mulID := mul.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// hdr.Preds[0] should be the new pre-header; hdr.Preds[1] should be b2.
	if len(b1.Preds) != 2 {
		t.Fatalf("expected header to have 2 preds, got %d", len(b1.Preds))
	}
	ph := b1.Preds[0]
	if ph == b0 || ph == b2 || ph == b3 || ph == b1 {
		t.Fatalf("expected b1.Preds[0] to be a fresh pre-header, got B%d", ph.ID)
	}
	if b1.Preds[1] != b2 {
		t.Fatalf("expected b1.Preds[1] == body (B%d), got B%d", b2.ID, b1.Preds[1].ID)
	}
	// phi.Args[0] should refer to the outside initial value (seed), because
	// there was only one outside pred so no fresh phi was needed.
	if len(phi.Args) != 2 {
		t.Fatalf("expected phi to have 2 args after LICM, got %d", len(phi.Args))
	}
	if phi.Args[0] == nil || phi.Args[0].ID != seedID {
		t.Fatalf("phi.Args[0] should reference seed (v%d), got %+v", seedID, phi.Args[0])
	}
	if phi.Args[1] == nil || phi.Args[1].ID != mulID {
		t.Fatalf("phi.Args[1] should reference body mul (v%d), got %+v", mulID, phi.Args[1])
	}
}

// ---------- Test 8: nested loops (hoist inside-out) ----------

// Shape:
//   b0 -> b1 (outer header, phi)
//   b1 -> b2 (inner header, phi)
//   b2 -> b3 (inner body) -> b2 (back)
//   b2 -> b4 (after inner, in outer body) -> b1 (back)
//   b1 -> b5 (exit)
func TestLICM_NestedLoop_InsideOut(t *testing.T) {
	fn := &Function{NumRegs: 4}
	b0 := newBlock(0)
	b1 := newBlock(1) // outer header
	b2 := newBlock(2) // inner header
	b3 := newBlock(3) // inner body
	b4 := newBlock(4) // outer body tail (after inner loop exits)
	b5 := newBlock(5) // overall exit
	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1, b2, b3, b4, b5}

	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0, b4}
	b1.Succs = []*Block{b2, b5}
	b2.Preds = []*Block{b1, b3}
	b2.Succs = []*Block{b3, b4}
	b3.Preds = []*Block{b2}
	b3.Succs = []*Block{b2}
	b4.Preds = []*Block{b2}
	b4.Succs = []*Block{b1}
	b5.Preds = []*Block{b1}

	// b0: seedOuter = ConstFloat 0, jump b1
	seedOuter := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seedOuter, b0Term}

	// b1: phiOuter(seedOuter, b4Tail) : float; cond; branch b2 b5
	phiOuter := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1}
	kOuter := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b1, Aux: 0}
	_ = kOuter // we'll add it as a body instr instead.
	// To hoist an "outer ConstFloat", we need it inside the OUTER loop body —
	// that is, inside a block that is reached only via b1 but not inside the
	// inner loop. b4 fits: it's in the outer body, not in inner body.
	condOuter := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{condOuter.Value()}, Aux: int64(b2.ID), Aux2: int64(b5.ID)}
	b1.Instrs = []*Instr{phiOuter, condOuter, b1Term}

	// b2: phiInner(phiOuter, b3Body) : float; condInner; branch b3 b4
	phiInner := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b2}
	condInner := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b2, Aux: 1}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b2,
		Args: []*Value{condInner.Value()}, Aux: int64(b3.ID), Aux2: int64(b4.ID)}
	b2.Instrs = []*Instr{phiInner, condInner, b2Term}

	// b3: kInner = ConstFloat 2.0, mulInner = MulFloat(phiInner, kInner), jump b2
	kInner := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b3, Aux: 0}
	mulInner := &Instr{ID: fn.newValueID(), Op: OpMulFloat, Type: TypeFloat, Block: b3,
		Args: []*Value{phiInner.Value(), kInner.Value()}}
	b3Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b3, Aux: int64(b2.ID)}
	b3.Instrs = []*Instr{kInner, mulInner, b3Term}

	// b4: kOuterBody = ConstFloat 3.0, addOuter = AddFloat(phiInner, kOuterBody), jump b1
	kOuterBody := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b4, Aux: 0}
	addOuter := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b4,
		Args: []*Value{phiInner.Value(), kOuterBody.Value()}}
	b4Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b4, Aux: int64(b1.ID)}
	b4.Instrs = []*Instr{kOuterBody, addOuter, b4Term}

	// Wire phi args.
	phiOuter.Args = []*Value{seedOuter.Value(), addOuter.Value()}
	phiInner.Args = []*Value{phiOuter.Value(), mulInner.Value()}

	// b5: return phiOuter
	b5.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b5,
			Args: []*Value{phiOuter.Value()}},
	}

	assertValidates(t, fn, "input")

	kInnerID := kInner.ID
	kOuterBodyID := kOuterBody.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// kInner must have left b3.
	for _, instr := range b3.Instrs {
		if instr.ID == kInnerID {
			t.Fatalf("kInner v%d should have been hoisted from inner body", kInnerID)
		}
	}
	// kOuterBody must have left b4.
	for _, instr := range b4.Instrs {
		if instr.ID == kOuterBodyID {
			t.Fatalf("kOuterBody v%d should have been hoisted from outer body", kOuterBodyID)
		}
	}
	// Two new pre-headers should exist: one feeding b2 (inner), one feeding b1 (outer).
	// Each hoisted const should live in a block whose ONLY successor is its loop's header.
	innerKBlock, _ := findInstrByID(fn, kInnerID)
	if innerKBlock == nil {
		t.Fatalf("kInner v%d not found anywhere after LICM", kInnerID)
	}
	if len(innerKBlock.Succs) != 1 {
		t.Fatalf("inner pre-header B%d should have 1 successor, has %d",
			innerKBlock.ID, len(innerKBlock.Succs))
	}
	outerKBlock, _ := findInstrByID(fn, kOuterBodyID)
	if outerKBlock == nil {
		t.Fatalf("kOuterBody v%d not found anywhere after LICM", kOuterBodyID)
	}
	if len(outerKBlock.Succs) != 1 {
		t.Fatalf("outer pre-header B%d should have 1 successor, has %d",
			outerKBlock.ID, len(outerKBlock.Succs))
	}
}

// ---------- Test 10: hoist Int48Safe AddInt ----------

func TestLICM_HoistInt48SafeAddInt(t *testing.T) {
	fn := &Function{NumRegs: 8, Int48Safe: make(map[int]bool)}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0
	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	// b1
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: two invariant LoadSlots from distinct slots (no stores), AddInt(a,b),
	// then accumulate into phi.
	la := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Block: b2, Aux: 5}
	lb := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Block: b2, Aux: 6}
	addInv := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{la.Value(), lb.Value()}}
	// body uses the invariant result: accum = AddInt(phi, addInv)
	accum := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{phi.Value(), addInv.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{la, lb, addInv, accum, b2Term}
	fn.Int48Safe[addInv.ID] = true
	// accum is not Int48Safe intentionally (we only want addInv hoisted).

	phi.Args = []*Value{seed.Value(), accum.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	addInvID := addInv.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	for _, instr := range b2.Instrs {
		if instr.ID == addInvID {
			t.Fatalf("Int48Safe AddInt v%d should have been hoisted", addInvID)
		}
	}
}

// ---------- Test 11: no-hoist AddInt when not Int48Safe ----------

func TestLICM_NoHoistAddInt_NotInt48Safe(t *testing.T) {
	fn := &Function{NumRegs: 8} // no Int48Safe map at all
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	la := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Block: b2, Aux: 5}
	lb := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Block: b2, Aux: 6}
	addInv := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{la.Value(), lb.Value()}}
	accum := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Block: b2,
		Args: []*Value{phi.Value(), addInv.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{la, lb, addInv, accum, b2Term}

	phi.Args = []*Value{seed.Value(), accum.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	addInvID := addInv.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// addInv must stay in b2.
	found := false
	for _, instr := range b2.Instrs {
		if instr.ID == addInvID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("AddInt v%d should NOT be hoisted without Int48Safe", addInvID)
	}
}

// ---------- Test 12: hoist GetGlobal when no SetGlobal and no Call ----------

func TestLICM_HoistGetGlobal_NoSetNoCall(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GetGlobal (Aux=5, no args), no SetGlobal, no Call
	gg := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Block: b2, Aux: 5}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gg, b2Term}

	phi.Args = []*Value{seed.Value(), gg.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	ggID := gg.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetGlobal should be hoisted out of b2.
	for _, instr := range b2.Instrs {
		if instr.ID == ggID {
			t.Fatalf("OpGetGlobal v%d should have been hoisted out of body B%d", ggID, b2.ID)
		}
	}
	// Should be in the pre-header.
	phBlock, _ := findInstrByID(fn, ggID)
	if phBlock == nil {
		t.Fatalf("hoisted GetGlobal v%d not found anywhere in fn", ggID)
	}
	if len(b1.Preds) < 1 || b1.Preds[0] != phBlock {
		t.Fatalf("expected hoisted GetGlobal in pre-header, got B%d", phBlock.ID)
	}
}

// ---------- Test 13: no-hoist GetGlobal when SetGlobal on same name ----------

func TestLICM_NoHoistGetGlobal_WhenSetGlobal(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GetGlobal(Aux=5) + SetGlobal(Aux=5) — same name, can't hoist
	gg := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Block: b2, Aux: 5}
	sg := &Instr{ID: fn.newValueID(), Op: OpSetGlobal, Type: TypeUnknown, Block: b2,
		Args: []*Value{gg.Value()}, Aux: 5}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gg, sg, b2Term}

	phi.Args = []*Value{seed.Value(), gg.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	ggID := gg.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetGlobal must stay in b2 (SetGlobal on same name in loop).
	found := false
	for _, instr := range b2.Instrs {
		if instr.ID == ggID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GetGlobal v%d should NOT be hoisted when SetGlobal on same name in loop", ggID)
	}
}

// ---------- Test 14: no-hoist GetGlobal when Call in loop ----------

func TestLICM_NoHoistGetGlobal_WhenCallInLoop(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Block: b0, Aux: 0}
	funcVal := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Block: b0, Aux: 1}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{seed, funcVal, b0Term}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: GetGlobal(Aux=5) + Call — call can modify globals
	gg := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Block: b2, Aux: 5}
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny, Block: b2,
		Args: []*Value{funcVal.Value()}, Aux: 1, Aux2: 1}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gg, call, b2Term}

	phi.Args = []*Value{seed.Value(), gg.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	ggID := gg.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetGlobal must stay in b2 (Call in loop can modify globals).
	found := false
	for _, instr := range b2.Instrs {
		if instr.ID == ggID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GetGlobal v%d should NOT be hoisted when Call in loop", ggID)
	}
}

// ---------- Test 15: hoist GuardType on hoisted GetField ----------

func TestLICM_GuardTypeHoist(t *testing.T) {
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)

	// b0: obj = LoadSlot 0 (object from outside the loop), jump b1
	obj := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Block: b0, Aux: 0}
	b0Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = []*Instr{obj, b0Term}

	// b1: phi, cond, branch
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	b1Term := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, b1Term}

	// b2: gf = GetField(obj, field=0), guard = GuardType(gf, TypeFloat),
	//     add = AddFloat(phi, gf), jump b1
	// No SetField/Call in loop, so GetField is invariant.
	// GuardType's arg (gf) is invariant, so guard is also hoisted.
	gf := &Instr{ID: fn.newValueID(), Op: OpGetField, Type: TypeAny, Block: b2,
		Args: []*Value{obj.Value()}, Aux: 0}
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeFloat, Block: b2,
		Args: []*Value{gf.Value()}, Aux: int64(TypeFloat)}
	seed := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b0, Aux: 0}
	// Insert seed into b0 before the terminator.
	b0.Instrs = []*Instr{obj, seed, b0Term}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat, Block: b2,
		Args: []*Value{phi.Value(), gf.Value()}}
	b2Term := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = []*Instr{gf, guard, add, b2Term}

	phi.Args = []*Value{seed.Value(), add.Value()}
	b3.Instrs = []*Instr{
		&Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
			Args: []*Value{phi.Value()}},
	}

	assertValidates(t, fn, "input")
	gfID := gf.ID
	guardID := guard.ID

	_, err := LICMPass(fn)
	if err != nil {
		t.Fatalf("LICMPass error: %v", err)
	}
	assertValidates(t, fn, "after LICM")

	// GetField should NOT be in b2 (body).
	for _, instr := range b2.Instrs {
		if instr.ID == gfID {
			t.Fatalf("GetField v%d should have been hoisted out of body B%d", gfID, b2.ID)
		}
	}
	// GuardType should NOT be in b2 (body).
	for _, instr := range b2.Instrs {
		if instr.ID == guardID {
			t.Fatalf("GuardType v%d should have been hoisted out of body B%d", guardID, b2.ID)
		}
	}

	// Both should be in the pre-header block.
	gfBlock, _ := findInstrByID(fn, gfID)
	if gfBlock == nil {
		t.Fatalf("hoisted GetField v%d not found anywhere in fn", gfID)
	}
	guardBlock, _ := findInstrByID(fn, guardID)
	if guardBlock == nil {
		t.Fatalf("hoisted GuardType v%d not found anywhere in fn", guardID)
	}

	// Both should be in the same pre-header.
	if gfBlock != guardBlock {
		t.Fatalf("GetField (B%d) and GuardType (B%d) should be in the same pre-header",
			gfBlock.ID, guardBlock.ID)
	}
	// The pre-header should be b1.Preds[0].
	if len(b1.Preds) < 1 || b1.Preds[0] != gfBlock {
		t.Fatalf("expected hoisted instrs in pre-header (b1.Preds[0]), "+
			"got b1.Preds=%v, block=B%d", blockIDs(b1.Preds), gfBlock.ID)
	}
}
