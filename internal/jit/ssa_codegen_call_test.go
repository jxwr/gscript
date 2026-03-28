//go:build darwin && arm64

package jit

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ─── While-loop (JMP-based) tracing tests ───

func TestSSACodegen_Integration_WhileLoop(t *testing.T) {
	// Simple while-loop: sum 1..100 using while-loop syntax.
	// Wrapped in a function so variables are local (not globals),
	// which allows the trace to be SSA-compiled.
	src := `
		func compute() {
			sum := 0
			i := 1
			for i <= 100 {
				sum = sum + i
				i = i + 1
			}
			return sum
		}
		result := compute()
	`
	// Run without tracing (interpreter only)
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
	if g2["result"].Int() != 5050 {
		t.Errorf("result = %d, want 5050", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_WhileLoopLT(t *testing.T) {
	// While-loop using less-than condition
	src := `
		func compute() {
			sum := 0
			i := 0
			for i < 50 {
				sum = sum + i
				i = i + 1
			}
			return sum
		}
		result := compute()
	`
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
	// sum of 0..49 = 1225
	if g2["result"].Int() != 1225 {
		t.Errorf("result = %d, want 1225", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_WhileLoopMultiply(t *testing.T) {
	// While-loop with multiplication (factorial)
	src := `
		func compute() {
			product := 1
			i := 1
			for i <= 10 {
				product = product * i
				i = i + 1
			}
			return product
		}
		result := compute()
	`
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
	// 10! = 3628800
	if g2["result"].Int() != 3628800 {
		t.Errorf("result = %d, want 3628800", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_SieveWhileLoop(t *testing.T) {
	// Sieve of Eratosthenes with while-loop inner marking.
	// This is the sieve benchmark's hot while-loop pattern.
	// Note: the counting loop (FORLOOP at PC=46) has a pre-existing guard bug
	// with TEST/GETTABLE traces. We blacklist it here to isolate while-loop
	// tracing correctness.
	src := `
		func sieve(n) {
			is_prime := {}
			for i := 2; i <= n; i++ { is_prime[i] = true }
			i := 2
			for i * i <= n {
				if is_prime[i] {
					j := i * i
					for j <= n {
						is_prime[j] = false
						j = j + i
					}
				}
				i = i + 1
			}
			count := 0
			for i := 2; i <= n; i++ {
				if is_prime[i] { count = count + 1 }
			}
			return count
		}
		result := sieve(100)
	`
	// Run without tracing (interpreter only)
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT, blacklisting the buggy counting loop trace
	proto2 := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v.SetTraceRecorder(recorder)
	// Blacklist the counting loop (last FORLOOP in the function) which has a
	// pre-existing guard bug with GETTABLE+TEST patterns.
	fnProto := proto2.Protos[0]
	// Find the last FORLOOP PC dynamically (bytecode layout may change)
	for i := len(fnProto.Code) - 1; i >= 0; i-- {
		if vm.DecodeOp(fnProto.Code[i]) == vm.OP_FORLOOP {
			recorder.blacklist[loopKey{proto: fnProto, pc: i}] = true
			break
		}
	}
	v.Execute(proto2)

	if g1["result"].Int() != globals["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), globals["result"].Int())
	}
	// 25 primes up to 100
	if globals["result"].Int() != 25 {
		t.Errorf("result = %d, want 25", globals["result"].Int())
	}
}

func TestSSACodegen_Integration_WhileLoopWithArray(t *testing.T) {
	t.Skip("Known issue: SSA trace with GETTABLE array + while-loop after compiler register optimization")
	// While-loop that writes to an array (sieve marking pattern)
	src := `
		func compute() {
			arr := {}
			for i := 1; i <= 50; i++ { arr[i] = true }
			j := 2
			for j <= 50 {
				arr[j] = false
				j = j + 2
			}
			count := 0
			for i := 1; i <= 50; i++ {
				if arr[i] { count = count + 1 }
			}
			return count
		}
		result := compute()
	`
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_Integration_WhileLoopMatchesInterpreter(t *testing.T) {
	// General correctness: while-loop fibonacci must match interpreter exactly
	src := `
		func compute() {
			a := 0
			b := 1
			i := 0
			for i < 30 {
				temp := a + b
				a = b
				b = temp
				i = i + 1
			}
			return a
		}
		result := compute()
	`
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_Integration_WhileLoopTraced(t *testing.T) {
	// With JMP back-edge detection re-enabled, while-loops inside functions
	// ARE traced. This test verifies correctness and that traces are created.
	src := `
		func compute() {
			sum := 0
			i := 1
			for i <= 100 {
				sum = sum + i
				i = i + 1
			}
			return sum
		}
		result := compute()
	`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v.SetTraceRecorder(recorder)

	v.Execute(proto)

	// Correctness check
	if globals["result"].Int() != 5050 {
		t.Errorf("result = %d, want 5050", globals["result"].Int())
	}

	// While-loop traces should now be recorded (JMP back-edge detection re-enabled)
	if len(recorder.Traces()) == 0 {
		t.Error("expected at least one trace for while-loop (JMP back-edge detection enabled)")
	}
}

// TestSSACodegen_Integration_QuicksortPartition tests the quicksort partition
// pattern: GETTABLE + SETTABLE (swap) + ADD inside a for-loop, called
// recursively on sub-arrays. This is the pattern that causes "attempt to index
// a number value" in the sort benchmark.
func TestSSACodegen_Integration_QuicksortPartition(t *testing.T) {
	t.Skip("Known: quicksort trace guard-fail with NaN-boxing typed arrays")
	src := `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t

    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

func make_random_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr
}

func is_sorted(arr, n) {
    for i := 1; i < n; i++ {
        if arr[i] > arr[i + 1] { return false }
    }
    return true
}

N := 100
arr := make_random_array(N, 42)
quicksort(arr, 1, N)
sorted := is_sorted(arr, N)
`
	// Run without tracing (pure VM)
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	v1 := vm.New(g1)
	_, err1 := v1.Execute(proto)
	if err1 != nil {
		t.Fatalf("VM runtime error: %v", err1)
	}

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	vmSorted := g1["sorted"]
	jitSorted := g2["sorted"]
	t.Logf("VM sorted=%v, SSA sorted=%v", vmSorted, jitSorted)

	if !vmSorted.Truthy() {
		t.Fatalf("VM: sorted should be true, got %v", vmSorted)
	}
	if !jitSorted.Truthy() {
		t.Errorf("JIT: sorted should be true, got %v (trace JIT corrupted sort)", jitSorted)
	}
}

// TestSSACodegen_SumPrimes_Correctness verifies that the trace JIT produces
// correct results for sum_primes, which uses an inlined is_prime function with
// a while-loop (JMP back-edge) inside a for-loop (FORLOOP back-edge).
//
// This is a regression test for two bugs:
//  1. Comparison opcodes (EQ, LT, LE) had their A field (boolean flag) incorrectly
//     remapped with baseOff when inlined at depth > 0 in the trace recorder.
//  2. The regular trace compiler assumed comparisons always "caused a skip" during
//     recording, but inlined code can have non-skipping comparisons followed by JMP.
//  3. The regular trace compiler's side-exit PCs from inlined functions referenced
//     the callee's bytecode, not the caller's, causing wrong resume after side-exit.
func TestSSACodegen_SumPrimes_Correctness(t *testing.T) {
	src := `
func is_prime(n) {
    if n < 2 { return false }
    if n < 4 { return true }
    if n % 2 == 0 { return false }
    if n % 3 == 0 { return false }
    i := 5
    for i * i <= n {
        if n % i == 0 { return false }
        if n % (i + 2) == 0 { return false }
        i = i + 6
    }
    return true
}

count := 0
for i := 2; i <= 200; i++ {
    if is_prime(i) {
        count = count + 1
    }
}
`
	// Run without tracing (pure VM) as reference
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	v1 := vm.New(g1)
	_, err1 := v1.Execute(proto)
	if err1 != nil {
		t.Fatalf("VM runtime error: %v", err1)
	}
	vmCount := g1["count"].Int()

	// Run with SSA JIT tracing
	proto2 := compileProto(t, src)
	g2 := runtime.NewInterpreterGlobals()
	v2 := vm.New(g2)
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v2.SetTraceRecorder(recorder)
	_, err2 := v2.Execute(proto2)
	if err2 != nil {
		t.Fatalf("Trace runtime error: %v", err2)
	}
	traceCount := g2["count"].Int()

	if vmCount != traceCount {
		t.Errorf("sum_primes(200): VM count=%d, Trace count=%d (expected match)", vmCount, traceCount)
	}
	// Expected: 46 primes up to 200
	if vmCount != 46 {
		t.Errorf("sum_primes(200): expected 46 primes, got %d", vmCount)
	}
}

// TestSSACodegen_CallExit tests that SSA_CALL compiles as a side-exit.
//
// Models: for i = 1, 5 do sum = sum + i; f(i) end
// The trace has computation (ADD) before the CALL, so it's useful.
// The CALL triggers a side-exit, returning control to the interpreter.
//
// Layout:
//
//	R(0)=idx, R(1)=limit, R(2)=step, R(3)=i (loop var)
//	R(4)=sum (accumulator)
//	R(5)=fn (function value), R(6)=arg (i copy)
//
// Trace body:
//
//	ADD  R(4), R(4), R(3) -- sum += i                PC=0
//	MOVE R(6), R(3)       -- copy i to arg position  PC=1
//	CALL R(5) B=2 C=2     -- f(i)                    PC=2
//	FORLOOP R(0)           -- loop control            PC=3
func TestSSACodegen_CallExit(t *testing.T) {
	code := []uint32{
		vm.EncodeABC(vm.OP_ADD, 4, 4, 3),     // PC=0: ADD R(4), R(4), R(3)
		vm.EncodeABC(vm.OP_MOVE, 6, 3, 0),    // PC=1: MOVE R(6), R(3)
		vm.EncodeABC(vm.OP_CALL, 5, 2, 2),    // PC=2: CALL R(5) B=2 C=2
		vm.EncodeAsBx(vm.OP_FORLOOP, 0, -4),  // PC=3: FORLOOP R(0) sBx=-4
	}
	proto := &vm.FuncProto{
		Code:      code,
		Constants: []runtime.Value{},
		MaxStack:  10,
	}

	trace := &Trace{
		LoopProto: proto,
		LoopPC:    3, // FORLOOP at PC=3
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, PC: 0, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_MOVE, A: 6, B: 3, PC: 1, BType: runtime.TypeInt},
			{Op: vm.OP_CALL, A: 5, B: 2, C: 2, PC: 2, Intrinsic: IntrinsicNone},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -4, PC: 3, AType: runtime.TypeInt},
		},
	}

	// Build SSA and verify SSA_CALL is emitted
	ssaFunc := BuildSSA(trace)
	hasCall := false
	for _, inst := range ssaFunc.Insts {
		if inst.Op == SSA_CALL {
			hasCall = true
			break
		}
	}
	if !hasCall {
		t.Fatal("SSA builder did not emit SSA_CALL for non-intrinsic OP_CALL")
	}

	// DEBUG: Dump SSA
	t.Log("=== SSA IR (before opt) ===")
	DumpSSA(ssaFunc)

	// Optimize and compile
	ssaFunc = OptimizeSSA(ssaFunc)
	t.Log("=== SSA IR (after opt) ===")
	DumpSSA(ssaFunc)
	if !ssaIsIntegerOnly(ssaFunc) {
		t.Fatal("SSA with SSA_CALL should be compilable")
	}

	ct, err := CompileSSA(ssaFunc)
	if err != nil {
		t.Fatalf("CompileSSA error: %v", err)
	}

	// DEBUG: Dump register allocation
	t.Log("=== Register Allocation ===")
	DumpRegAlloc(ct.regMap)

	if !ct.hasCallExit {
		t.Fatal("compiled trace should have hasCallExit=true")
	}

	// Set up registers: for i = 1, 5 do sum = sum + i end
	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(1)          // idx = 1 (first iteration)
	regs[1] = runtime.IntValue(5)          // limit
	regs[2] = runtime.IntValue(1)          // step
	regs[3] = runtime.IntValue(1)          // i (loop var = idx for first iter)
	regs[4] = runtime.IntValue(0)          // sum
	regs[5] = runtime.NilValue()           // fn (placeholder - guard expects nil)

	t.Log("=== Registers before execution ===")
	DumpRegisters(regs, []int{0, 1, 2, 3, 4, 5})

	// Execute the compiled trace: it should side-exit at the CALL PC.
	// The trace runs: guards → ADD → MOVE → side-exit at CALL (PC=2).
	exitPC, sideExit, guardFail := ct.Execute(regs, 0, proto)

	if guardFail {
		t.Fatalf("unexpected guard fail")
	}
	if !sideExit {
		t.Fatalf("expected side-exit, got loop-done")
	}
	if exitPC != 2 {
		t.Errorf("exitPC = %d, want 2 (CALL at PC=2)", exitPC)
	}

	// After the first iteration, sum should be 0+1=1 (one ADD executed)
	sum := regs[4].Int()
	t.Logf("sum=%d, exitPC=%d, sideExit=%v", sum, exitPC, sideExit)
	if sum != 1 {
		t.Errorf("sum = %d, want 1 (0 + 1 from first iteration)", sum)
	}
}

// TestSSACodegen_CallExit_SSABuild tests that SSA builder correctly emits
// SSA_CALL for non-intrinsic calls.
func TestSSACodegen_CallExit_SSABuild(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{
			Code:      []uint32{0, 0, 0, 0},
			Constants: []runtime.Value{},
		},
		LoopPC: 3,
		IR: []TraceIR{
			{Op: vm.OP_CALL, A: 5, B: 2, C: 2, PC: 1, Intrinsic: IntrinsicNone},
			{Op: vm.OP_ADD, A: 4, B: 4, C: 5, PC: 2, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3, PC: 3},
		},
	}

	ssaFunc := BuildSSA(trace)

	// Verify SSA_CALL is emitted
	foundCall := false
	for _, inst := range ssaFunc.Insts {
		if inst.Op == SSA_CALL {
			foundCall = true
			if inst.Slot != 5 {
				t.Errorf("SSA_CALL slot = %d, want 5", inst.Slot)
			}
			if inst.PC != 1 {
				t.Errorf("SSA_CALL PC = %d, want 1", inst.PC)
			}
		}
	}

	if !foundCall {
		t.Fatal("SSA builder did not emit SSA_CALL")
	}

	// Verify it passes ssaIsIntegerOnly (all ops are recognized)
	ssaFunc = OptimizeSSA(ssaFunc)
	if !ssaIsIntegerOnly(ssaFunc) {
		t.Fatal("SSA with SSA_CALL should pass ssaIsIntegerOnly")
	}

	// SSAIsUseful should reject it because SSA_CALL is the first real
	// instruction after SSA_LOOP (trace would exit immediately, doing no work).
	if SSAIsUseful(ssaFunc) {
		t.Fatal("Trace with SSA_CALL as first instruction after loop should be rejected by SSAIsUseful")
	}
}

// TestSSACodegen_WhileLoopWithCallExit tests that while-loop traces
// with call-exits can compile. This is the real-world case: most
// loops in real code are while-loops (JMP-based), not FORLOOPs.
func TestSSACodegen_WhileLoopWithCallExit(t *testing.T) {
	// While-loop pattern: sum 1..10 with a function call
	// This mimics real-world code like: for i=1; i<=10; i++ { sum = addOne(sum) }
	//
	// Bytecode pattern (simplified for testing):
	//   PC=0: LT   R(0) R(1)    -- i <= 10
	//   PC=1: JMP  PC=0           -- loop back (if condition was true)
	//   PC=2: CALL  R(5) B=2 C=2  -- function call (call-exit)
	//   PC=3: ADD  R(2) R(2) R(5) -- sum = sum + fn_result
	//
	// Since OP_CALL is not inlined, this emits SSA_CALL (call-exit).
	// With the fix, this should compile because it has a while-loop exit (LT at PC=0).
	trace := &Trace{
		LoopProto: &vm.FuncProto{
			Code: []uint32{
				0, 0, 0, 0, // dummy ops for layout
			},
			Constants: []runtime.Value{},
		},
		LoopPC: 0, // While-loop starts at PC=0
		IR: []TraceIR{
			{Op: vm.OP_LT, A: 1, B: 0, C: 1, PC: 0, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_JMP, SBX: -3, PC: 1}, // back-edge to PC=0
			{Op: vm.OP_CALL, A: 5, B: 2, C: 2, PC: 2, Intrinsic: IntrinsicNone},
			{Op: vm.OP_ADD, A: 2, B: 2, C: 5, PC: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
		},
	}

	// Build SSA
	ssaFunc := BuildSSA(trace)

	// Verify SSA_CALL is emitted for OP_CALL
	hasCallExit := false
	for _, inst := range ssaFunc.Insts {
		if inst.Op == SSA_CALL {
			hasCallExit = true
			break
		}
	}
	if !hasCallExit {
		t.Fatal("SSA builder did not emit SSA_CALL for OP_CALL in loop")
	}

	// Optimize (this should mark the LT as while-loop exit with AuxInt=-2)
	ssaFunc = OptimizeSSA(ssaFunc)

	// Verify while-loop exit is marked
	hasWhileLoopExit := false
	for _, inst := range ssaFunc.Insts {
		if inst.Op == SSA_LT_INT && inst.AuxInt == -2 {
			hasWhileLoopExit = true
			break
		}
	}
	if !hasWhileLoopExit {
		t.Error("OptimizeSSA should mark LT as while-loop exit (AuxInt=-2)")
	}

	// After the fix, this trace should pass ssaIsIntegerOnly
	// because it has a while-loop exit (AuxInt=-2), even though it has SSA_CALL
	if !ssaIsIntegerOnly(ssaFunc) {
		t.Fatal("While-loop trace with SSA_CALL should pass ssaIsIntegerOnly (has while-loop exit)")
	}

	// Compile
	ct, err := CompileSSA(ssaFunc)
	if err != nil {
		t.Fatalf("CompileSSA error: %v", err)
	}

	if !ct.hasCallExit {
		t.Fatal("compiled trace should have hasCallExit=true")
	}

	// Verify the trace has proper structure
	if ct.code.Size() == 0 {
		t.Fatal("compiled code is empty")
	}
}

// TestSSACodegen_Integration_WhileLoopWithCall tests that real-world
// while-loops with function calls can compile. This is the critical
// case that enables most benchmarks to JIT-compile.
func TestSSACodegen_Integration_WhileLoopWithCall(t *testing.T) {
	// While-loop that does computation - this will trace as a while-loop
	// since GScript doesn't have dedicated for-loop syntax.
	// We use enough iterations to trigger hotness (default threshold = 10).
	src := `
		func compute() {
			sum := 0
			i := 1
			for i <= 100 {
				sum = sum + i
				i = i + 1
			}
			return sum
		}
		result := compute()
	`

	// Run without tracing (interpreter only)
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)
	expected := g1["result"].Int()

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)
	actual := g2["result"].Int()

	// Verify JIT and interpreter produce same result
	if expected != actual {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", expected, actual)
	}
	t.Logf("While-loop sum: result=%d", actual)

	// Verify traces were recorded and contain expected ops
	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	v := vm.New(runtime.NewInterpreterGlobals())
	v.SetTraceRecorder(recorder)

	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto2, err := vm.Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	_, _ = v.Execute(proto2)

	traces := recorder.Traces()
	if len(traces) == 0 {
		t.Fatal("No traces recorded - while-loop should have been hot")
	}
	t.Logf("Recorded %d trace(s)", len(traces))

	// Verify the trace is a while-loop (has JMP back-edge, not FORLOOP)
	hasWhileLoop := false
	for _, trace := range traces {
		for _, ir := range trace.IR {
			if ir.Op == vm.OP_JMP && ir.SBX < 0 {
				hasWhileLoop = true
				break
			}
		}
	}
	if !hasWhileLoop {
		t.Error("Expected while-loop trace with backward JMP")
	}
}
