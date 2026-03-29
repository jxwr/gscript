//go:build darwin && arm64

// emit_call_exit_test.go tests the call-exit mechanism for OpCall.
// Instead of deopting the entire function when a CALL is encountered,
// call-exit stores registers to memory, returns to Go with ExitCode=3,
// Go executes the call via the VM, then re-enters the JIT at a resume point.
//
// This enables the JIT to handle functions that contain calls without
// falling back to the interpreter for the entire function.

package methodjit

import (
	"fmt"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// makeCallExitVM creates a VM with all globals from the source set up.
// Used by call-exit to execute calls via the VM interpreter.
func makeCallExitVM(t *testing.T, src string) *vm.VM {
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

// TestCallExit_SimpleCall: func add(a,b){return a+b}; func f(x){return add(x,1)}
// f(5) should return 6.
func TestCallExit_SimpleCall(t *testing.T) {
	src := `func add(a, b) { return a + b }; func f(x) { return add(x, 1) }`
	proto := compileByName(t, src, "f")
	proto.EnsureFeedback()
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	// Set up call-exit VM for executing calls.
	callVM := makeCallExitVM(t, src)
	defer callVM.Close()
	cf.CallVM = callVM

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
	if !result[0].IsInt() || result[0].Int() != 6 {
		t.Errorf("expected 6, got %v", result[0])
	}
}

// TestCallExit_TwoCalls: func add(a,b){return a+b}; func f(a,b){return add(a,1)+add(b,2)}
// f(3,4) should return 4+6=10.
func TestCallExit_TwoCalls(t *testing.T) {
	src := `func add(a, b) { return a + b }; func f(a, b) { return add(a, 1) + add(b, 2) }`
	proto := compileByName(t, src, "f")
	proto.EnsureFeedback()
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	callVM := makeCallExitVM(t, src)
	defer callVM.Close()
	cf.CallVM = callVM

	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVMByName(t, src, "f", args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(3,4)", result[0], vmResult[0])
}

// TestCallExit_Fib: func fib(n){if n<2{return n}; return fib(n-1)+fib(n-2)}
// fib(10) should return 55.
func TestCallExit_Fib(t *testing.T) {
	src := `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	proto := compileFunction(t, src)
	proto.EnsureFeedback()
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	callVM := makeCallExitVM(t, src)
	defer callVM.Close()
	cf.CallVM = callVM

	// For recursive calls, the JIT will call-exit back to Go,
	// and Go will call cf.Execute recursively.
	// We need the DeoptFunc as a fallback too.
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

// TestCallExit_LoopWithCall: func add(a,b){return a+b}; func f(n){s:=0; for i:=1;i<=n;i++{s=s+add(i,0)}; return s}
// f(10) should return 55 (sum 1..10).
func TestCallExit_LoopWithCall(t *testing.T) {
	src := `func add(a, b) { return a + b }; func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + add(i, 0) }; return s }`
	proto := compileByName(t, src, "f")
	proto.EnsureFeedback()
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	callVM := makeCallExitVM(t, src)
	defer callVM.Close()
	cf.CallVM = callVM

	args := []runtime.Value{runtime.IntValue(10)}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVMByName(t, src, "f", args)
	if len(vmResult) == 0 || len(result) == 0 {
		t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
	}
	assertValuesEqual(t, "f(10)", result[0], vmResult[0])
}

// TestCallExit_MatchesVM: comprehensive comparison for all call patterns.
func TestCallExit_MatchesVM(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		fnName string
		args   []runtime.Value
	}{
		{
			name:   "simple_call",
			src:    `func add(a, b) { return a + b }; func f(x) { return add(x, 1) }`,
			fnName: "f",
			args:   []runtime.Value{runtime.IntValue(5)},
		},
		{
			name:   "two_calls",
			src:    `func add(a, b) { return a + b }; func f(a, b) { return add(a, 1) + add(b, 2) }`,
			fnName: "f",
			args:   []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)},
		},
		{
			name:   "nested_call",
			src:    `func add(a, b) { return a + b }; func f(x) { return add(add(x, 1), 2) }`,
			fnName: "f",
			args:   []runtime.Value{runtime.IntValue(5)},
		},
		{
			name:   "call_with_const",
			src:    `func id(x) { return x }; func f() { return id(42) }`,
			fnName: "f",
			args:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proto := compileByName(t, tc.src, tc.fnName)
			proto.EnsureFeedback()
			fn := BuildGraph(proto)
			alloc := AllocateRegisters(fn)

			cf, err := Compile(fn, alloc)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}
			defer cf.Code.Free()

			callVM := makeCallExitVM(t, tc.src)
			defer callVM.Close()
			cf.CallVM = callVM
			cf.DeoptFunc = makeDeoptFunc(t, tc.src, tc.fnName)

			result, err := cf.Execute(tc.args)
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}

			vmResult := runVMByName(t, tc.src, tc.fnName, tc.args)
			if len(vmResult) == 0 || len(result) == 0 {
				t.Fatalf("empty result: JIT=%v, VM=%v", result, vmResult)
			}
			assertValuesEqual(t, fmt.Sprintf("%s(%v)", tc.fnName, tc.args), result[0], vmResult[0])
		})
	}
}
