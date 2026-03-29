// pass_constprop_test.go tests the constant propagation pass.
// Tests build IR manually with known constants and verify that
// arithmetic on constant operands is folded at compile time.

package methodjit

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestConstProp_AddConsts verifies that 1 + 2 is folded to 3.
func TestConstProp_AddConsts(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "addconst"},
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

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	// The add should have been rewritten to ConstInt 3.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Op != OpConstInt {
				t.Errorf("expected OpConstInt, got %s", instr.Op)
			}
			if instr.Aux != 3 {
				t.Errorf("expected constant 3, got %d", instr.Aux)
			}
			if len(instr.Args) != 0 {
				t.Errorf("expected 0 args after folding, got %d", len(instr.Args))
			}
		}
	}
}

// TestConstProp_Chain verifies that 1 + 2 + 3 is folded to 6.
func TestConstProp_Chain(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "chain"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	c3 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}
	add1 := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	add2 := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{add1.Value(), c3.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add2.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, c3, add1, add2, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	// add2 should be folded to ConstInt 6.
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add2.ID {
			if instr.Op != OpConstInt {
				t.Errorf("expected OpConstInt for chained add, got %s", instr.Op)
			}
			if instr.Aux != 6 {
				t.Errorf("expected constant 6, got %d", instr.Aux)
			}
		}
	}
}

// TestConstProp_NoChange verifies that a + b (non-const) is unchanged.
func TestConstProp_NoChange(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "nochange", NumParams: 2},
		NumRegs: 2,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	p1 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	p2 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 1, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{p1.Value(), p2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{p1, p2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	// Add should remain unchanged (non-constant operands).
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Op != OpAddInt {
				t.Errorf("expected OpAddInt to remain, got %s", instr.Op)
			}
			if len(instr.Args) != 2 {
				t.Errorf("expected 2 args, got %d", len(instr.Args))
			}
		}
	}
}

// TestConstProp_MixedConstNonConst verifies that a + 1 (one const, one non-const) is unchanged.
func TestConstProp_MixedConstNonConst(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "mixed", NumParams: 1},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	p1 := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{p1.Value(), c1.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{p1, c1, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Op != OpAddInt {
				t.Errorf("expected OpAddInt to remain, got %s", instr.Op)
			}
		}
	}
}

// TestConstProp_GenericAdd verifies that generic OpAdd with two int constants is folded.
func TestConstProp_GenericAdd(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "genericadd"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 10, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 20, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Op != OpConstInt {
				t.Errorf("expected OpConstInt, got %s", instr.Op)
			}
			if instr.Aux != 30 {
				t.Errorf("expected 30, got %d", instr.Aux)
			}
		}
	}
}

// TestConstProp_FloatArithmetic verifies folding of float constants.
func TestConstProp_FloatArithmetic(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "floatadd"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat,
		Aux: int64(math.Float64bits(1.5)), Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat,
		Aux: int64(math.Float64bits(2.5)), Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Op != OpConstFloat {
				t.Errorf("expected OpConstFloat, got %s", instr.Op)
			}
			got := math.Float64frombits(uint64(instr.Aux))
			if got != 4.0 {
				t.Errorf("expected 4.0, got %f", got)
			}
		}
	}
}

// TestConstProp_SubMulMod verifies folding of SubInt, MulInt, ModInt.
func TestConstProp_SubMulMod(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "submulmod"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 10, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}

	sub := &Instr{ID: fn.newValueID(), Op: OpSubInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	mul := &Instr{ID: fn.newValueID(), Op: OpMulInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpModInt, Type: TypeInt,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{sub.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, sub, mul, mod, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	expected := map[int]int64{
		sub.ID: 7,
		mul.ID: 30,
		mod.ID: 1,
	}

	for _, instr := range result.Entry.Instrs {
		if want, ok := expected[instr.ID]; ok {
			if instr.Op != OpConstInt {
				t.Errorf("v%d: expected OpConstInt, got %s", instr.ID, instr.Op)
			}
			if instr.Aux != want {
				t.Errorf("v%d: expected %d, got %d", instr.ID, want, instr.Aux)
			}
		}
	}
}

// TestConstProp_ValidatorPass verifies that the output passes validation.
func TestConstProp_ValidatorPass(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "validate"},
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

	result, err := ConstPropPass(fn)
	if err != nil {
		t.Fatalf("ConstPropPass error: %v", err)
	}

	errs := Validate(result)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}
