// pass_dce_test.go tests the dead code elimination pass.
// Tests build IR with unused instructions and verify that DCE removes
// dead values while preserving live values and side-effectful operations.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestDCE_UnusedValue verifies that a dead value (not referenced) is removed.
func TestDCE_UnusedValue(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "unused"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	// x := 1 + 2 (unused)
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	dead := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	// return 42 (alive, does not use x)
	alive := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 42, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{alive.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, dead, alive, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	// The dead AddInt and its two ConstInt operands (if unreferenced) should be removed.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == dead.ID {
			t.Error("dead AddInt instruction should have been removed")
		}
	}

	// The alive ConstInt and Return should remain.
	hasAlive := false
	hasRet := false
	for _, instr := range result.Entry.Instrs {
		if instr.ID == alive.ID {
			hasAlive = true
		}
		if instr.Op == OpReturn {
			hasRet = true
		}
	}
	if !hasAlive {
		t.Error("alive ConstInt should remain")
	}
	if !hasRet {
		t.Error("Return should remain")
	}
}

// TestDCE_UsedValue verifies that a used value is NOT removed.
func TestDCE_UsedValue(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "used"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	// All instructions should remain (all are used).
	if len(result.Entry.Instrs) != 4 {
		t.Errorf("expected 4 instructions (all used), got %d", len(result.Entry.Instrs))
	}
}

// TestDCE_Chain verifies that a dead chain (x := 1; y := x + 2; return a)
// removes both dead instructions.
func TestDCE_Chain(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "chain"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	// Dead chain: c1 -> add (unused)
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	deadAdd := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	// c1 is also used by deadAdd, but deadAdd is dead, so after removing deadAdd,
	// c1 becomes dead too.
	// Alive: return a constant.
	alive := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 99, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{alive.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, deadAdd, alive, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	// After fixed-point iteration, c1, c2, and deadAdd should all be removed.
	for _, instr := range result.Entry.Instrs {
		switch instr.ID {
		case c1.ID, c2.ID, deadAdd.ID:
			t.Errorf("dead instruction v%d (%s) should have been removed", instr.ID, instr.Op)
		}
	}

	// Should only have alive + ret = 2 instructions.
	if len(result.Entry.Instrs) != 2 {
		t.Errorf("expected 2 instructions after DCE, got %d", len(result.Entry.Instrs))
		for _, instr := range result.Entry.Instrs {
			t.Logf("  v%d %s", instr.ID, instr.Op)
		}
	}
}

// TestDCE_SideEffect verifies that a call with unused result is NOT removed.
func TestDCE_SideEffect(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "sideeffect"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	fnVal := &Instr{ID: fn.newValueID(), Op: OpGetGlobal, Type: TypeAny, Aux: 0, Block: b}
	call := &Instr{ID: fn.newValueID(), Op: OpCall, Type: TypeAny,
		Args: []*Value{fnVal.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Block: b}
	b.Instrs = []*Instr{fnVal, call, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	// Call should remain (side-effectful), even though its result is unused.
	hasCall := false
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpCall {
			hasCall = true
		}
	}
	if !hasCall {
		t.Error("Call instruction should not be removed (side effect)")
	}
}

// TestDCE_GuardKept verifies that guards are not removed even if unused.
func TestDCE_GuardKept(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "guard"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	p := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	guard := &Instr{ID: fn.newValueID(), Op: OpGuardType, Type: TypeInt,
		Args: []*Value{p.Value()}, Aux: int64(TypeInt), Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{p.Value()}, Block: b}
	b.Instrs = []*Instr{p, guard, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	hasGuard := false
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpGuardType {
			hasGuard = true
		}
	}
	if !hasGuard {
		t.Error("GuardType should not be removed (side effect)")
	}
}

// TestDCE_StoreKept verifies that store operations are not removed.
func TestDCE_StoreKept(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "store"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 5, Block: b}
	store := &Instr{ID: fn.newValueID(), Op: OpSetGlobal, Type: TypeUnknown,
		Args: []*Value{c.Value()}, Aux: 0, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Block: b}
	b.Instrs = []*Instr{c, store, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	hasStore := false
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpSetGlobal {
			hasStore = true
		}
	}
	if !hasStore {
		t.Error("SetGlobal should not be removed (side effect)")
	}
}

// TestDCE_ValidatorPass verifies that the output passes validation.
func TestDCE_ValidatorPass(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "validate"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	dead := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	alive := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 42, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{alive.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, dead, alive, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	errs := Validate(result)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}

// TestDCE_PhiKept verifies that phi nodes are not removed by DCE.
func TestDCE_PhiKept(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "phi"},
		NumRegs: 1,
	}
	b0 := &Block{ID: 0, defs: make(map[int]*Value)}
	b1 := &Block{ID: 1, defs: make(map[int]*Value)}

	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b0}
	jmp := &Instr{ID: fn.newValueID(), Op: OpJump, Block: b0}
	b0.Instrs = []*Instr{c1, jmp}
	b0.Succs = []*Block{b1}
	b1.Preds = []*Block{b0}

	phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt,
		Args: []*Value{c1.Value()}, Block: b1}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{phi.Value()}, Block: b1}
	b1.Instrs = []*Instr{phi, ret}

	fn.Entry = b0
	fn.Blocks = []*Block{b0, b1}

	result, err := DCEPass(fn)
	if err != nil {
		t.Fatalf("DCEPass error: %v", err)
	}

	hasPhi := false
	for _, blk := range result.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpPhi {
				hasPhi = true
			}
		}
	}
	if !hasPhi {
		t.Error("Phi node should not be removed by DCE")
	}
}

// TestDCE_MatrixStoreFAtNotDropped is the R52 regression: DCE was dropping
// OpMatrixStoreFAt / OpMatrixSetF / OpMatrixStoreFRow because they had no
// hasSideEffect entry, so JIT-compiled matrix.setf calls became no-ops and
// matmul_dense/nbody_dense produced stale zeroed output. The sibling Table
// store (OpSetField / OpSetTable) has always been side-effect-marked; the
// DenseMatrix stores were added in R43/R45/R46 without the corresponding
// DCE registration.
func TestDCE_MatrixStoresNotDropped(t *testing.T) {
	ops := []Op{OpMatrixSetF, OpMatrixStoreFAt, OpMatrixStoreFRow}
	for _, op := range ops {
		fn := &Function{Proto: &vm.FuncProto{Name: "setf-live"}, NumRegs: 1}
		b := &Block{ID: 0, defs: make(map[int]*Value)}
		// A minimal matrix mutation: m = LoadSlot, i = const 0, j = const 0, v = const 1.
		m := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
		c0 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: b}
		c1 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Block: b}
		store := &Instr{
			ID: fn.newValueID(), Op: op, Type: TypeUnknown,
			Args: []*Value{m.Value(), c0.Value(), c0.Value(), c1.Value()}, Block: b,
		}
		ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Block: b}
		b.Instrs = []*Instr{m, c0, c1, store, ret}
		fn.Entry = b
		fn.Blocks = []*Block{b}

		if _, err := DCEPass(fn); err != nil {
			t.Fatalf("%s: DCEPass failed: %v", op.String(), err)
		}
		found := false
		for _, instr := range b.Instrs {
			if instr.Op == op {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: DCE dropped a matrix store instruction; must be preserved as side-effectful", op.String())
		}
	}
}
