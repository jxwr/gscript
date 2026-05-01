// pass_licm_hoist_test.go tests LICM hoisting of OpSqrt and OpGetTable.
// Split from pass_licm_test.go to stay within the 1000-line file limit.

package methodjit

import "testing"

// licmLoop builds entry→header→body→exit. eI go in b0, bI in b2 (IDs/Blocks assigned).
// phi.Args = [eI[0], bI[0]]. Returns fn, header, body.
func licmLoop(t *testing.T, eI, bI []*Instr) (*Function, *Block, *Block) {
	t.Helper()
	fn := &Function{NumRegs: 8}
	b0, b1, b2, b3 := buildSimpleLoop(fn)
	for _, i := range eI {
		i.ID, i.Block = fn.newValueID(), b0
	}
	jmp0 := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b0, Aux: int64(b1.ID)}
	b0.Instrs = append(append([]*Instr{}, eI...), jmp0)
	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeAny, Block: b1}
	cond := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Block: b1, Aux: 1}
	br := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: b1,
		Args: []*Value{cond.Value()}, Aux: int64(b2.ID), Aux2: int64(b3.ID)}
	b1.Instrs = []*Instr{phi, cond, br}
	for _, i := range bI {
		i.ID, i.Block = fn.newValueID(), b2
	}
	jmp2 := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: b2, Aux: int64(b1.ID)}
	b2.Instrs = append(append([]*Instr{}, bI...), jmp2)
	phi.Args = []*Value{eI[0].Value(), bI[0].Value()}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b3,
		Args: []*Value{phi.Value()}}
	b3.Instrs = []*Instr{ret}
	assertValidates(t, fn, "input")
	return fn, b1, b2
}

func TestLICM_Sqrt(t *testing.T) {
	seed := &Instr{Op: OpConstFloat, Type: TypeFloat}
	fv := &Instr{Op: OpConstFloat, Type: TypeFloat, Aux: 1}
	sq := &Instr{Op: OpSqrt, Type: TypeFloat, Args: []*Value{fv.Value()}}
	fn, b1, b2 := licmLoop(t, []*Instr{seed, fv}, []*Instr{sq})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == sq.ID {
			t.Fatal("OpSqrt should have been hoisted")
		}
	}
	if pb, _ := findInstrByID(fn, sq.ID); pb == nil || b1.Preds[0] != pb {
		t.Fatal("Sqrt not in pre-header")
	}
}

func TestLICM_GetTable(t *testing.T) {
	tbl := &Instr{Op: OpLoadSlot, Type: TypeTable}
	key := &Instr{Op: OpConstInt, Type: TypeInt, Aux: 1}
	gt := &Instr{Op: OpGetTable, Type: TypeAny, Args: []*Value{tbl.Value(), key.Value()}}
	fn, b1, b2 := licmLoop(t, []*Instr{tbl, key}, []*Instr{gt})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == gt.ID {
			t.Fatal("OpGetTable should have been hoisted")
		}
	}
	if pb, _ := findInstrByID(fn, gt.ID); pb == nil || b1.Preds[0] != pb {
		t.Fatal("GetTable not in pre-header")
	}
}

func TestLICM_Len(t *testing.T) {
	tbl := &Instr{Op: OpLoadSlot, Type: TypeTable}
	ln := &Instr{Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}}
	fn, b1, b2 := licmLoop(t, []*Instr{tbl}, []*Instr{ln})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == ln.ID {
			t.Fatal("OpLen should have been hoisted")
		}
	}
	if pb, _ := findInstrByID(fn, ln.ID); pb == nil || b1.Preds[0] != pb {
		t.Fatal("Len not in pre-header")
	}
}

func TestLICM_NoHoistGetTable_WhenSetTable(t *testing.T) {
	tbl := &Instr{Op: OpLoadSlot, Type: TypeTable}
	key := &Instr{Op: OpConstInt, Type: TypeInt, Aux: 1}
	gt := &Instr{Op: OpGetTable, Type: TypeAny, Args: []*Value{tbl.Value(), key.Value()}}
	v := &Instr{Op: OpConstInt, Type: TypeInt, Aux: 99}
	st := &Instr{Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), key.Value(), v.Value()}}
	fn, _, b2 := licmLoop(t, []*Instr{tbl, key}, []*Instr{gt, v, st})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == gt.ID {
			return // still in body — correct
		}
	}
	t.Fatal("GetTable should NOT be hoisted when SetTable on same obj")
}

func TestLICM_NoHoistLen_WhenSetTable(t *testing.T) {
	tbl := &Instr{Op: OpLoadSlot, Type: TypeTable}
	key := &Instr{Op: OpConstInt, Type: TypeInt, Aux: 1}
	ln := &Instr{Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}}
	v := &Instr{Op: OpConstInt, Type: TypeInt, Aux: 99}
	st := &Instr{Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), key.Value(), v.Value()}}
	fn, _, b2 := licmLoop(t, []*Instr{tbl, key}, []*Instr{ln, v, st})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == ln.ID {
			return // still in body — correct
		}
	}
	t.Fatal("Len should NOT be hoisted when SetTable may change table length")
}

func TestLICM_NoHoistGetTable_WhenCallInLoop(t *testing.T) {
	tbl := &Instr{Op: OpLoadSlot, Type: TypeTable}
	key := &Instr{Op: OpConstInt, Type: TypeInt, Aux: 1}
	fv := &Instr{Op: OpLoadSlot, Type: TypeAny, Aux: 2}
	gt := &Instr{Op: OpGetTable, Type: TypeAny, Args: []*Value{tbl.Value(), key.Value()}}
	call := &Instr{Op: OpCall, Type: TypeAny, Args: []*Value{fv.Value()}, Aux: 1, Aux2: 1}
	fn, _, b2 := licmLoop(t, []*Instr{tbl, key, fv}, []*Instr{gt, call})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == gt.ID {
			return // still in body — correct
		}
	}
	t.Fatal("GetTable should NOT be hoisted when Call in loop")
}

func TestLICM_NoHoistLen_WhenCallInLoop(t *testing.T) {
	tbl := &Instr{Op: OpLoadSlot, Type: TypeTable}
	fv := &Instr{Op: OpLoadSlot, Type: TypeAny, Aux: 2}
	ln := &Instr{Op: OpLen, Type: TypeInt, Args: []*Value{tbl.Value()}}
	call := &Instr{Op: OpCall, Type: TypeAny, Args: []*Value{fv.Value()}, Aux: 1, Aux2: 1}
	fn, _, b2 := licmLoop(t, []*Instr{tbl, fv}, []*Instr{ln, call})
	if _, err := LICMPass(fn); err != nil {
		t.Fatal(err)
	}
	for _, i := range b2.Instrs {
		if i.ID == ln.ID {
			return // still in body — correct
		}
	}
	t.Fatal("Len should NOT be hoisted when Call in loop")
}
