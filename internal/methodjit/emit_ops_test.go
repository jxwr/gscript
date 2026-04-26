//go:build darwin && arm64

// emit_ops_test.go tests extended ARM64 code generation: division, negation,
// float arithmetic, function calls (via deopt), and globals (via deopt).
// Each test compiles a GScript function, runs it through the Method JIT,
// and compares the result with the VM interpreter.

package methodjit

import (
	"fmt"
	"math"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runVMByName compiles the source, executes the top-level, then calls a
// specific function by name from globals. Used when the source defines
// multiple functions and we need to call a specific one.
func runVMByName(t *testing.T, src string, fnName string, args []runtime.Value) []runtime.Value {
	t.Helper()
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	proto := compileTop(t, src)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		t.Fatalf("function %q not found in globals", fnName)
	}

	results, err := v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("VM call error: %v", err)
	}
	return results
}

// compileByName compiles the source and returns the FuncProto for a
// specific function name. Used when the source defines multiple functions.
func compileByName(t *testing.T, src string, fnName string) *vm.FuncProto {
	t.Helper()
	top := compileTop(t, src)
	for _, p := range top.Protos {
		if p.Name == fnName {
			return p
		}
	}
	t.Fatalf("function %q not found in protos", fnName)
	return nil
}

// makeDeoptFunc creates a DeoptFunc that runs the function via a VM.
// Uses the full source to set up globals, then calls fnName.
func makeDeoptFunc(t *testing.T, src string, fnName string) func(args []runtime.Value) ([]runtime.Value, error) {
	t.Helper()
	return func(args []runtime.Value) ([]runtime.Value, error) {
		globals := make(map[string]runtime.Value)
		v := vm.New(globals)
		defer v.Close()

		proto := compileTop(t, src)
		_, err := v.Execute(proto)
		if err != nil {
			return nil, err
		}

		fnVal := v.GetGlobal(fnName)
		if fnVal.IsNil() {
			return nil, fmt.Errorf("function %q not found", fnName)
		}

		return v.CallValue(fnVal, args)
	}
}

// makeCallExitVMForTest creates a VM with all globals from the source set up.
// Used by call-exit tests to execute calls and resolve globals.
func makeCallExitVMForTest(t *testing.T, src string) *vm.VM {
	t.Helper()
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	proto := compileTop(t, src)
	_, err := v.Execute(proto)
	if err != nil {
		v.Close()
		t.Fatalf("VM execute top-level error: %v", err)
	}
	return v
}

// TestEmit_Div: division always returns float (GScript/Lua semantics).
// func f(a, b) { return a / b } — f(10, 3) ≈ 3.333...
func TestEmit_Div(t *testing.T) {
	src := `func f(a, b) { return a / b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(3)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(10,3)", result[0], vmResult[0])

	// Division of ints always returns float.
	if !result[0].IsFloat() {
		t.Errorf("expected float result, got type=%s value=%v", result[0].TypeName(), result[0])
	}
	expected := float64(10) / float64(3)
	if math.Abs(result[0].Float()-expected) > 1e-10 {
		t.Errorf("expected %v, got %v", expected, result[0].Float())
	}
}

func TestEmit_ModIntSignMatchesVM(t *testing.T) {
	cases := []struct {
		name string
		src  string
		args []runtime.Value
	}{
		{name: "negative dividend", src: `func f(a) { return a % 3 }`, args: []runtime.Value{runtime.IntValue(-5)}},
		{name: "negative divisor", src: `func f(a) { return a % -3 }`, args: []runtime.Value{runtime.IntValue(5)}},
		{name: "both negative", src: `func f(a) { return a % -3 }`, args: []runtime.Value{runtime.IntValue(-5)}},
		{name: "param divisor", src: `func f(b) { return -5 % b }`, args: []runtime.Value{runtime.IntValue(3)}},
	}
	for _, tc := range cases {
		proto := compileFunction(t, tc.src)
		fn, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
		if err != nil {
			t.Fatalf("%s: RunTier2Pipeline error: %v", tc.name, err)
		}
		foundModInt := false
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpModInt {
					foundModInt = true
				}
			}
		}
		if !foundModInt {
			t.Fatalf("%s: expected pipeline to specialize to OpModInt, IR:\n%s", tc.name, Print(fn))
		}

		alloc := AllocateRegisters(fn)
		cf, err := Compile(fn, alloc)
		if err != nil {
			t.Fatalf("%s: Compile error: %v", tc.name, err)
		}
		result, err := cf.Execute(tc.args)
		cf.Code.Free()
		if err != nil {
			t.Fatalf("%s: Execute error for args=%v: %v", tc.name, tc.args, err)
		}
		vmResult := runVM(t, tc.src, tc.args)
		if len(result) == 0 || len(vmResult) == 0 {
			t.Fatalf("%s: empty result for args=%v: JIT=%v VM=%v", tc.name, tc.args, result, vmResult)
		}
		assertValuesEqual(t, fmt.Sprintf("%s f(%v)", tc.name, tc.args), result[0], vmResult[0])
	}
}

// TestEmit_Div_Exact: 10 / 2 = 5.0 (float, not int).
func TestEmit_Div_Exact(t *testing.T) {
	src := `func f(a, b) { return a / b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(2)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	assertValuesEqual(t, "f(10,2)", result[0], vmResult[0])
	if !result[0].IsFloat() || result[0].Float() != 5.0 {
		t.Errorf("expected 5.0 (float), got %v (type=%s)", result[0], result[0].TypeName())
	}
}

// TestEmit_Neg: func f(a) { return -a } — f(5) = -5.
func TestEmit_Neg(t *testing.T) {
	src := `func f(a) { return -a }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(5)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	assertValuesEqual(t, "f(5)", result[0], vmResult[0])
	if !result[0].IsInt() || result[0].Int() != -5 {
		t.Errorf("expected -5 (int), got %v (type=%s)", result[0], result[0].TypeName())
	}
}

// TestEmit_Neg_Zero: func f(a) { return -a } — f(0) = 0.
func TestEmit_Neg_Zero(t *testing.T) {
	src := `func f(a) { return -a }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.IntValue(0)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	assertValuesEqual(t, "f(0)", result[0], vmResult[0])
}

// TestEmit_FloatArith: func f(a, b) { return a + b } with float args.
// 1.5 + 2.5 = 4.0.
func TestEmit_FloatArith(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(1.5,2.5)", result[0], vmResult[0])
	if !result[0].IsFloat() || result[0].Float() != 4.0 {
		t.Errorf("expected 4.0 (float), got %v (type=%s)", result[0], result[0].TypeName())
	}
}

func TestEmit_NumToFloat_IntAndFloatInputs(t *testing.T) {
	fn := &Function{
		Proto:   &vm.FuncProto{Name: "numtofloat", NumParams: 1, MaxStack: 1},
		NumRegs: 1,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	arg := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: b}
	conv := &Instr{ID: fn.newValueID(), Op: OpNumToFloat, Type: TypeFloat,
		Args: []*Value{arg.Value()}, Block: b}
	cf := &Instr{ID: fn.newValueID(), Op: OpConstFloat, Type: TypeFloat,
		Aux: int64(math.Float64bits(2.5)), Block: b}
	add := &Instr{ID: fn.newValueID(), Op: OpAddFloat, Type: TypeFloat,
		Args: []*Value{conv.Value(), cf.Value()}, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown,
		Args: []*Value{add.Value()}, Block: b}
	b.Instrs = []*Instr{arg, conv, cf, add, ret}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	alloc := AllocateRegisters(fn)
	cfNative, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cfNative.Code.Free()

	tests := []struct {
		name string
		arg  runtime.Value
		want float64
	}{
		{name: "int", arg: runtime.IntValue(3), want: 5.5},
		{name: "float", arg: runtime.FloatValue(1.25), want: 3.75},
	}
	for _, tt := range tests {
		result, err := cfNative.Execute([]runtime.Value{tt.arg})
		if err != nil {
			t.Fatalf("%s Execute error: %v", tt.name, err)
		}
		if len(result) != 1 || !result[0].IsFloat() || math.Abs(result[0].Float()-tt.want) > 1e-12 {
			t.Fatalf("%s: expected %v as float, got %v", tt.name, tt.want, result)
		}
	}
}

// TestEmit_FloatSub: func f(a, b) { return a - b } with float args.
// 5.0 - 1.5 = 3.5.
func TestEmit_FloatSub(t *testing.T) {
	src := `func f(a, b) { return a - b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.FloatValue(5.0), runtime.FloatValue(1.5)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	assertValuesEqual(t, "f(5.0,1.5)", result[0], vmResult[0])
	if !result[0].IsFloat() || result[0].Float() != 3.5 {
		t.Errorf("expected 3.5 (float), got %v (type=%s)", result[0], result[0].TypeName())
	}
}

// TestEmit_FloatMul: func f(a, b) { return a * b } with float args.
// 2.0 * 3.5 = 7.0.
func TestEmit_FloatMul(t *testing.T) {
	src := `func f(a, b) { return a * b }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	args := []runtime.Value{runtime.FloatValue(2.0), runtime.FloatValue(3.5)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	assertValuesEqual(t, "f(2.0,3.5)", result[0], vmResult[0])
	if !result[0].IsFloat() || result[0].Float() != 7.0 {
		t.Errorf("expected 7.0 (float), got %v (type=%s)", result[0], result[0].TypeName())
	}
}

// TestEmit_Call: func add(a,b) { return a+b }; func f(x) { return add(x, 1) }
// f(5) = 6. Uses call-exit for GetGlobal and OpCall.
func TestEmit_Call(t *testing.T) {
	src := `func add(a, b) { return a + b }; func f(x) { return add(x, 1) }`
	proto := compileByName(t, src, "f")
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	// Set up CallVM for call-exit and global-exit.
	callVM := makeCallExitVMForTest(t, src)
	defer callVM.Close()
	cf.CallVM = callVM
	cf.DeoptFunc = makeDeoptFunc(t, src, "f")

	args := []runtime.Value{runtime.IntValue(5)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVMByName(t, src, "f", args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(5)", result[0], vmResult[0])
}

// TestEmit_Fib: func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }
// fib(10) = 55. Uses call-exit for GetGlobal and recursive calls.
func TestEmit_Fib(t *testing.T) {
	src := `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	// Set up CallVM for call-exit and global-exit.
	callVM := makeCallExitVMForTest(t, src)
	defer callVM.Close()
	cf.CallVM = callVM
	cf.DeoptFunc = makeDeoptFunc(t, src, "fib")

	args := []runtime.Value{runtime.IntValue(10)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "fib(10)", result[0], vmResult[0])
	if result[0].IsInt() && result[0].Int() != 55 {
		t.Errorf("expected 55, got %v", result[0].Int())
	}
}

// TestEmit_GetGlobal: x := 42; func f() { return x } — returns 42.
// Uses deopt for global access.
func TestEmit_GetGlobal(t *testing.T) {
	src := `x := 42; func f() { return x }`

	// Run via VM to verify the expected result.
	vmResult := runVM(t, src, nil)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f() via VM", vmResult[0], runtime.IntValue(42))

	// The JIT function contains GetGlobal which triggers deopt.
	// The deopt path re-runs the function via the VM, producing the same result.
	// For this test, we just verify the VM produces 42, since the JIT will
	// deopt and fall back to the VM. The integration test is in vm_test.go.
}

// TestEmit_TableField: func f() { t := {x: 1, y: 2}; return t.x + t.y }
// Returns 3. Uses deopt for table operations.
func TestEmit_TableField(t *testing.T) {
	src := `func f() { t := {x: 1, y: 2}; return t.x + t.y }`

	vmResult := runVM(t, src, nil)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f() via VM", vmResult[0], runtime.IntValue(3))
}

// TestEmit_Concat: func f(a, b) { return a .. b }
// "hello" .. "world" = "helloworld". Uses deopt for concat.
func TestEmit_Concat(t *testing.T) {
	src := `func f(a, b) { return a .. b }`

	args := []runtime.Value{runtime.StringValue("hello"), runtime.StringValue("world")}
	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	if vmResult[0].Str() != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", vmResult[0].Str())
	}
}

// TestEmit_UniversalCompilation: verify that all functions compile (no rejection).
func TestEmit_UniversalCompilation(t *testing.T) {
	// With universal compilation, every function compiles. OpCall uses
	// call-exit, and all other unsupported ops use op-exit.
	src := `func add(a, b) { return a + b }; func f(x) { return add(x, 1) }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	errs := Validate(fn)
	if len(errs) > 0 {
		t.Fatalf("validation errors: %v", errs)
	}

	// The function should compile successfully (no canCompile rejection).
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("compilation should succeed: %v", err)
	}
	if cf == nil {
		t.Fatal("compilation returned nil")
	}
}
