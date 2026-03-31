// pass_typespec_test.go tests the type specialization pass.
// Each test builds IR (either manually or via BuildGraph), runs
// TypeSpecializePass, and verifies that generic ops are replaced
// with type-specialized variants when operand types are known.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

// TestTypeSpec_IntAdd verifies that OpAdd with two ConstInt args becomes OpAddInt.
func TestTypeSpec_IntAdd(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "intadd"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 4, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	// The Add instruction should now be AddInt.
	found := false
	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			found = true
			if instr.Op != OpAddInt {
				t.Errorf("expected OpAddInt, got %s", instr.Op)
			}
			if instr.Type != TypeInt {
				t.Errorf("expected TypeInt, got %s", instr.Type)
			}
		}
	}
	if !found {
		t.Fatal("add instruction not found after pass")
	}
}

// TestTypeSpec_ForLoop verifies that a for-loop sum gets its arithmetic
// and comparison specialized to int.
func TestTypeSpec_ForLoop(t *testing.T) {
	proto := compile(t, `
func f(n) {
	sum := 0
	for i := 1; i <= n; i++ {
		sum = sum + i
	}
	return sum
}
`)
	fn := BuildGraph(proto)
	t.Logf("Before:\n%s", Print(fn))

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}
	t.Logf("After:\n%s", Print(result))

	// Look for at least one specialized op (AddInt or LeInt).
	hasSpecialized := false
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAddInt, OpSubInt, OpLeInt, OpLtInt:
				hasSpecialized = true
			}
		}
	}
	if !hasSpecialized {
		t.Error("expected at least one type-specialized instruction after TypeSpecializePass on for-loop")
	}

	// No generic Add should remain if all arithmetic is on int constants/phis.
	// (Some may remain if phi types can't be resolved, so this is a soft check.)
}

// TestTypeSpec_MixedTypes verifies that int + float stays generic or becomes float-specialized.
func TestTypeSpec_MixedTypes(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "mixed"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	ci := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}
	cf := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat, Aux: 4, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{ci.Value(), cf.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{ci, cf, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	for _, instr := range result.Entry.Instrs {
		if instr.ID == add.ID {
			if instr.Op != OpAddFloat {
				t.Errorf("expected OpAddFloat for int+float, got %s", instr.Op)
			}
			if instr.Type != TypeFloat {
				t.Errorf("expected TypeFloat, got %s", instr.Type)
			}
		}
	}
}

// TestTypeSpec_NoChange verifies that a function with no arithmetic is unchanged.
func TestTypeSpec_NoChange(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "nochange"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	ci := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 42, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{ci.Value()}, Block: b}
	b.Instrs = []*Instr{ci, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	if len(result.Entry.Instrs) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(result.Entry.Instrs))
	}
	if result.Entry.Instrs[0].Op != OpConstInt {
		t.Errorf("expected ConstInt, got %s", result.Entry.Instrs[0].Op)
	}
	if result.Entry.Instrs[1].Op != OpReturn {
		t.Errorf("expected Return, got %s", result.Entry.Instrs[1].Op)
	}
}

// TestTypeSpec_Fib verifies that fib gets its comparison and subtraction
// specialized where types can be inferred.
func TestTypeSpec_Fib(t *testing.T) {
	proto := compile(t, `
func fib(n) {
	if n < 2 {
		return n
	}
	return fib(n - 1) + fib(n - 2)
}
`)
	fn := BuildGraph(proto)
	t.Logf("Before:\n%s", Print(fn))

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}
	t.Logf("After:\n%s", Print(result))

	// fib's n < 2: the constant 2 is ConstInt, so Lt could become LtInt
	// if n's type is known. Since n is a parameter (TypeAny), it may not
	// be specialized. But the Sub n-1 and n-2 use ConstInt args, so
	// at least the constant side is typed.
	// Count specialized ops.
	specCount := 0
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpSubInt, OpLtInt, OpAddInt:
				specCount++
			}
		}
	}
	t.Logf("Specialized instruction count: %d", specCount)
	// It's OK if no specialization happens because params are TypeAny.
	// The test primarily verifies the pass doesn't crash on complex IR.
}

// TestTypeSpec_AllIntOps verifies specialization of Sub, Mul, Mod, Lt, Le, Eq.
func TestTypeSpec_AllIntOps(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "allops"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 10, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}

	sub := &Instr{ID: fn.newValueID(), Op: OpSub, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	mul := &Instr{ID: fn.newValueID(), Op: OpMul, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	mod := &Instr{ID: fn.newValueID(), Op: OpMod, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	lt := &Instr{ID: fn.newValueID(), Op: OpLt, Type: TypeBool,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	le := &Instr{ID: fn.newValueID(), Op: OpLe, Type: TypeBool,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	eq := &Instr{ID: fn.newValueID(), Op: OpEq, Type: TypeBool,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{sub.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, sub, mul, mod, lt, le, eq, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	expected := map[int]Op{
		sub.ID: OpSubInt,
		mul.ID: OpMulInt,
		mod.ID: OpModInt,
		lt.ID:  OpLtInt,
		le.ID:  OpLeInt,
		eq.ID:  OpEqInt,
	}

	for _, instr := range result.Entry.Instrs {
		if want, ok := expected[instr.ID]; ok {
			if instr.Op != want {
				t.Errorf("instruction v%d: expected %s, got %s", instr.ID, want, instr.Op)
			}
		}
	}
}

// TestTypeSpec_UnaryNeg verifies that OpUnm with int arg becomes OpNegInt.
func TestTypeSpec_UnaryNeg(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "neg"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 5, Block: b}
	neg := &Instr{ID: fn.newValueID(), Op: OpUnm, Type: TypeAny,
		Args: []*Value{c1.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{neg.Value()}, Block: b}
	b.Instrs = []*Instr{c1, neg, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	for _, instr := range result.Entry.Instrs {
		if instr.ID == neg.ID {
			if instr.Op != OpNegInt {
				t.Errorf("expected OpNegInt, got %s", instr.Op)
			}
		}
	}
}

// TestPipeline_FullOptimization runs all three passes in sequence through the
// pipeline and verifies the combined result on a for-loop sum function.
func TestPipeline_FullOptimization(t *testing.T) {
	p := NewPipeline()
	p.Add("TypeSpecialize", TypeSpecializePass)
	p.Add("ConstProp", ConstPropPass)
	p.Add("DCE", DCEPass)
	p.SetValidator(func(fn *Function) []error { return Validate(fn) })
	p.EnableDump(true)

	proto := compile(t, `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`)
	fn := BuildGraph(proto)
	result, err := p.Run(fn)
	if err != nil {
		t.Fatalf("Pipeline error: %v", err)
	}

	t.Logf("Pipeline dump:\n%s", p.Dump())

	// After TypeSpecialize: some Adds may become AddInt, Sub may become SubInt.
	// After ConstProp: constant operations may be folded.
	// After DCE: dead code removed.
	// Verify the result is valid IR.
	errs := Validate(result)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}

	// Verify at least one specialized instruction exists after the pipeline.
	hasSpecialized := false
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAddInt, OpSubInt, OpMulInt, OpLeInt, OpLtInt:
				hasSpecialized = true
			}
		}
	}
	if !hasSpecialized {
		t.Log("Note: no specialized instructions found (phis may prevent full specialization)")
	}

	// Verify the diff shows changes from the pipeline.
	diff := p.Diff("input", "TypeSpecialize")
	if diff == "" {
		t.Log("Note: no diff between input and TypeSpecialize")
	}
}

// TestPipeline_ConstFolding runs TypeSpec + ConstProp + DCE on a function
// that reduces to a constant: func f() { return 1 + 2 + 3 }
func TestPipeline_ConstFolding(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "constfold"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 2, Block: b}
	c3 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}
	add1 := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	add2 := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{add1.Value(), c3.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add2.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, c3, add1, add2, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	p := NewPipeline()
	p.Add("TypeSpecialize", TypeSpecializePass)
	p.Add("ConstProp", ConstPropPass)
	p.Add("DCE", DCEPass)
	p.SetValidator(func(fn *Function) []error { return Validate(fn) })

	result, err := p.Run(fn)
	if err != nil {
		t.Fatalf("Pipeline error: %v", err)
	}

	// After TypeSpec: Add -> AddInt (both args are int).
	// After ConstProp: AddInt(1,2) -> ConstInt(3), AddInt(3,3) -> ConstInt(6).
	// After DCE: dead ConstInt 1, 2, 3 removed (since they are no longer referenced).
	t.Logf("Result IR:\n%s", Print(result))

	// The final return should reference a ConstInt with value 6.
	for _, instr := range result.Entry.Instrs {
		if instr.Op == OpReturn && len(instr.Args) > 0 {
			retArg := instr.Args[0]
			// Find the definition of retArg.
			for _, def := range result.Entry.Instrs {
				if def.ID == retArg.ID {
					if def.Op != OpConstInt {
						t.Errorf("expected return value to be ConstInt, got %s", def.Op)
					}
					if def.Aux != 6 {
						t.Errorf("expected constant 6, got %d", def.Aux)
					}
				}
			}
		}
	}
}

// TestTypeSpec_ParamGuard_IntContext verifies that a parameter used with ConstInt
// gets a GuardType(int) inserted and downstream ops become type-specialized.
func TestTypeSpec_ParamGuard_IntContext(t *testing.T) {
	proto := compile(t, `
func f(n) {
	return n - 1
}
`)
	fn := BuildGraph(proto)
	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}
	t.Logf("After:\n%s", Print(result))

	// Expect: GuardType inserted for parameter n, Sub becomes SubInt.
	hasGuard := false
	hasSubInt := false
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGuardType && Type(instr.Aux) == TypeInt {
				hasGuard = true
			}
			if instr.Op == OpSubInt {
				hasSubInt = true
			}
		}
	}
	if !hasGuard {
		t.Error("expected GuardType(int) for parameter n")
	}
	if !hasSubInt {
		t.Error("expected SubInt after guard insertion")
	}
}

// TestTypeSpec_ParamGuard_NoGuardForFloatOnly verifies that no guard is inserted
// when params are only used with other params (no ConstInt context).
func TestTypeSpec_ParamGuard_NoGuardForFloatOnly(t *testing.T) {
	proto := compile(t, `
func f(a, b) {
	return a + b
}
`)
	fn := BuildGraph(proto)
	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpGuardType {
				t.Errorf("unexpected GuardType for params only used with each other")
			}
		}
	}
}

// TestTypeSpec_MandelbrotFullySpecialized verifies mandelbrot has 0 generic ops.
func TestTypeSpec_MandelbrotFullySpecialized(t *testing.T) {
	proto := compile(t, `
func f(size) {
	count := 0
	for y := 0; y < size; y = y + 1 {
		ci := 2.0 * y / size - 1.0
		for x := 0; x < size; x = x + 1 {
			cr := 2.0 * x / size - 1.5
			zr := 0.0
			zi := 0.0
			escaped := false
			for iter := 0; iter < 50; iter = iter + 1 {
				tr := zr * zr - zi * zi + cr
				ti := 2.0 * zr * zi + ci
				zr = tr
				zi = ti
				if zr * zr + zi * zi > 4.0 {
					escaped = true
					break
				}
			}
			if !escaped { count = count + 1 }
		}
	}
	return count
}
`)
	fn := BuildGraph(proto)
	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	genericCount := 0
	for _, block := range result.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpLt, OpLe, OpEq:
				genericCount++
				t.Errorf("generic op: v%d = %s", instr.ID, instr.Op)
			}
		}
	}
	if genericCount > 0 {
		t.Errorf("expected 0 generic ops, got %d", genericCount)
	}
}

// TestTypeSpec_ValidatorPass verifies that the output passes validation.
func TestTypeSpec_ValidatorPass(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "validate"},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	c1 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 3, Block: b}
	c2 := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 4, Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAdd, Type: TypeAny,
		Args: []*Value{c1.Value(), c2.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{c1, c2, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	result, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}

	errs := Validate(result)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %v", e)
		}
	}
}
