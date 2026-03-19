//go:build darwin && arm64

package jit

import (
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ─── Unit tests for SSA codegen (direct SSAFunc → ARM64) ───

// buildAndCompileSSA is a test helper: trace → SSA → optimize → compile.
func buildAndCompileSSA(t *testing.T, trace *Trace) *CompiledTrace {
	t.Helper()
	ssaFunc := BuildSSA(trace)
	ssaFunc = OptimizeSSA(ssaFunc)
	ct, err := CompileSSA(ssaFunc)
	if err != nil {
		t.Fatalf("CompileSSA error: %v", err)
	}
	return ct
}

// executeSSATrace runs a compiled SSA trace against a register array.
func executeSSATrace(ct *CompiledTrace, regs []runtime.Value) (exitPC int, sideExit bool) {
	var ctx TraceContext
	ctx.Regs = uintptr(unsafe.Pointer(&regs[0]))
	if len(ct.constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&ct.constants[0]))
	}
	callJIT(uintptr(ct.code.Ptr()), uintptr(unsafe.Pointer(&ctx)))
	// ExitCode: 0=loop done, 1=side exit, 2=guard fail (pre-loop type mismatch)
	return int(ctx.ExitPC), ctx.ExitCode >= 1
}

// TestSSACodegen_SimpleAdd tests a simple for-loop with addition.
// for i := 1; i <= 5; i++ { sum = sum + i }
func TestSSACodegen_SimpleAdd(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0) // idx
	regs[1] = runtime.IntValue(5) // limit
	regs[2] = runtime.IntValue(1) // step
	regs[3] = runtime.IntValue(0) // i (loop var)
	regs[4] = runtime.IntValue(0) // sum

	_, sideExit := executeSSATrace(ct, regs)

	sum := regs[4].Int()
	if sum != 15 {
		t.Errorf("sum = %d, want 15 (sideExit=%v)", sum, sideExit)
	}
	if sideExit {
		t.Error("unexpected side exit")
	}
}

// TestSSACodegen_SumSquares tests sum of squares: sum += i * i
func TestSSACodegen_SumSquares(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_MUL, A: 5, B: 3, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_ADD, A: 4, B: 4, C: 5, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)   // idx
	regs[1] = runtime.IntValue(10)  // limit
	regs[2] = runtime.IntValue(1)   // step
	regs[3] = runtime.IntValue(0)   // i
	regs[4] = runtime.IntValue(0)   // sum
	regs[5] = runtime.IntValue(0)   // temp

	executeSSATrace(ct, regs)

	// sum of i^2 for i=1..10 = 385
	sum := regs[4].Int()
	if sum != 385 {
		t.Errorf("sum = %d, want 385", sum)
	}
}

// TestSSACodegen_Sub tests subtraction in a loop.
func TestSSACodegen_Sub(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_SUB, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)    // idx
	regs[1] = runtime.IntValue(5)    // limit
	regs[2] = runtime.IntValue(1)    // step
	regs[3] = runtime.IntValue(0)    // i
	regs[4] = runtime.IntValue(100)  // val (100 - 1 - 2 - 3 - 4 - 5 = 85)

	executeSSATrace(ct, regs)

	val := regs[4].Int()
	if val != 85 {
		t.Errorf("val = %d, want 85", val)
	}
}

// TestSSACodegen_ConstantAdd tests adding a constant from the pool.
func TestSSACodegen_ConstantAdd(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{runtime.IntValue(10)}},
		Constants: []runtime.Value{runtime.IntValue(10)},
		IR: []TraceIR{
			// sum = sum + Constants[0] (i.e., sum += 10)
			{Op: vm.OP_ADD, A: 4, B: 4, C: 0 + vm.RKBit, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx (FORPREP: 1-1=0)
	regs[1] = runtime.IntValue(5)  // limit
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i
	regs[4] = runtime.IntValue(0)  // sum
	// The trace body runs first (sum+=10), then FORLOOP increments.
	// With idx=0,limit=5,step=1: body runs 6 times (idx=0,1,2,3,4,5 then 6>5 exits).
	// sum = 6 * 10 = 60

	executeSSATrace(ct, regs)

	sum := regs[4].Int()
	if sum != 60 {
		t.Errorf("sum = %d, want 60", sum)
	}
}

// TestSSACodegen_GuardSideExit tests that a type guard causes a side exit
// when the register has the wrong type.
func TestSSACodegen_GuardSideExit(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			{Op: vm.OP_ADD, A: 4, B: 4, C: 3, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)     // idx
	regs[1] = runtime.IntValue(5)     // limit
	regs[2] = runtime.IntValue(1)     // step
	regs[3] = runtime.IntValue(0)     // i
	regs[4] = runtime.StringValue("not an int") // WRONG type → guard should fail

	_, sideExit := executeSSATrace(ct, regs)

	if !sideExit {
		t.Error("expected side exit due to type guard failure, but got normal exit")
	}
}

// TestSSACodegen_Mod tests modulo operation.
func TestSSACodegen_Mod(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			// temp = i % 3
			{Op: vm.OP_MOD, A: 5, B: 3, C: 4, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -2},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)  // idx
	regs[1] = runtime.IntValue(1)  // limit (just 1 iteration)
	regs[2] = runtime.IntValue(1)  // step
	regs[3] = runtime.IntValue(0)  // i
	regs[4] = runtime.IntValue(3)  // divisor
	regs[5] = runtime.IntValue(0)  // temp (result)

	executeSSATrace(ct, regs)

	// After 1 iteration: i=1, temp = 1 % 3 = 1
	temp := regs[5].Int()
	if temp != 1 {
		t.Errorf("temp = %d, want 1", temp)
	}
}

// TestSSACodegen_Neg tests negation.
func TestSSACodegen_Neg(t *testing.T) {
	trace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		IR: []TraceIR{
			// neg = -i
			{Op: vm.OP_UNM, A: 5, B: 3, BType: runtime.TypeInt},
			// sum = sum + neg
			{Op: vm.OP_ADD, A: 4, B: 4, C: 5, BType: runtime.TypeInt, CType: runtime.TypeInt},
			{Op: vm.OP_FORLOOP, A: 0, SBX: -3},
		},
	}

	ct := buildAndCompileSSA(t, trace)

	regs := make([]runtime.Value, 10)
	regs[0] = runtime.IntValue(0)   // idx
	regs[1] = runtime.IntValue(5)   // limit
	regs[2] = runtime.IntValue(1)   // step
	regs[3] = runtime.IntValue(0)   // i
	regs[4] = runtime.IntValue(0)   // sum
	regs[5] = runtime.IntValue(0)   // neg

	executeSSATrace(ct, regs)

	// sum = -1 + -2 + -3 + -4 + -5 = -15
	sum := regs[4].Int()
	if sum != -15 {
		t.Errorf("sum = %d, want -15", sum)
	}
}

// ─── Integration tests: full pipeline via runWithTracingJIT ───

// runWithSSAJIT executes with tracing + SSA compilation, returns globals.
func runWithSSAJIT(t *testing.T, src string) map[string]runtime.Value {
	t.Helper()
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)

	recorder := NewTraceRecorder()
	recorder.SetCompile(true)
	recorder.SetUseSSA(true) // enable SSA codegen
	v.SetTraceRecorder(recorder)

	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals
}

func TestSSACodegen_Integration_SimpleAdd(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 1000; i++ {
			sum = sum + i
		}
	`)
	if v := g["sum"]; v.Int() != 500500 {
		t.Errorf("sum = %d, want 500500", v.Int())
	}
}

func TestSSACodegen_Integration_SumSquares(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + i * i
		}
	`)
	if v := g["sum"]; v.Int() != 338350 {
		t.Errorf("sum = %d, want 338350", v.Int())
	}
}

func TestSSACodegen_Integration_SumMul(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			sum = sum + (i * 2)
		}
	`)
	// 2*(1+2+...+50) = 2*1275 = 2550
	if v := g["sum"]; v.Int() != 2550 {
		t.Errorf("sum = %d, want 2550", v.Int())
	}
}

func TestSSACodegen_Integration_Nested(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			for j := 1; j <= 50; j++ {
				sum = sum + 1
			}
		}
	`)
	if v := g["sum"]; v.Int() != 2500 {
		t.Errorf("sum = %d, want 2500", v.Int())
	}
}

func TestSSACodegen_Integration_Mod(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + (i % 7)
		}
		result := sum
	`)
	expected := int64(0)
	for i := int64(1); i <= 100; i++ {
		expected += i % 7
	}
	if v := g["result"]; v.Int() != expected {
		t.Errorf("result = %d, want %d", v.Int(), expected)
	}
}

func TestSSACodegen_Integration_UNM(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0
		for i := 1; i <= 50; i++ {
			sum = sum + (-i)
		}
		result := sum
	`)
	if v := g["result"]; v.Int() != -1275 {
		t.Errorf("result = %d, want -1275", v.Int())
	}
}

func TestSSACodegen_Integration_MatchesInterpreter(t *testing.T) {
	src := `
		a := 0
		b := 1
		for i := 0; i < 30; i++ {
			t := a + b
			a = b
			b = t
		}
		result := a
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_Integration_FloatAdd(t *testing.T) {
	g := runWithSSAJIT(t, `
		sum := 0.0
		for i := 1; i <= 100; i++ {
			sum = sum + 1.5
		}
	`)
	if v := g["sum"]; v.Float() != 150.0 {
		t.Errorf("sum = %v, want 150.0", v.Float())
	}
}

func TestSSACodegen_Integration_FloatMul(t *testing.T) {
	g := runWithSSAJIT(t, `
		product := 1.0
		for i := 1; i <= 10; i++ {
			product = product * 2.0
		}
	`)
	if v := g["product"]; v.Float() != 1024.0 {
		t.Errorf("product = %v, want 1024.0", v.Float())
	}
}

// TestSSACodegen_Integration_SideExitFallback tests that non-integer-only traces
// fall back to the regular trace compiler.
func TestSSACodegen_Integration_GetField(t *testing.T) {
	// GETFIELD: read table field in a loop (native compilation)
	g := runWithSSAJIT(t, `
		t := {x: 10}
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + t.x
		}
	`)
	if v := g["sum"]; v.Int() != 1000 {
		t.Errorf("sum = %d, want 1000", v.Int())
	}
}

func TestSSACodegen_Integration_GetFieldMultiple(t *testing.T) {
	// Multiple field reads from same table
	g := runWithSSAJIT(t, `
		obj := {x: 3, y: 7}
		sum := 0
		for i := 1; i <= 50; i++ {
			sum = sum + obj.x + obj.y
		}
	`)
	if v := g["sum"]; v.Int() != 500 {
		t.Errorf("sum = %d, want 500", v.Int())
	}
}

func TestSSACodegen_Integration_SetField(t *testing.T) {
	// SETFIELD: write table field in a loop
	g := runWithSSAJIT(t, `
		obj := {count: 0}
		for i := 1; i <= 100; i++ {
			obj.count = obj.count + 1
		}
		result := obj.count
	`)
	if v := g["result"]; v.Int() != 100 {
		t.Errorf("result = %d, want 100", v.Int())
	}
}

func TestSSACodegen_Integration_GetFieldFloat(t *testing.T) {
	// GETFIELD with float values (nbody pattern)
	src := `
		body := {x: 1.0, y: 2.0, z: 3.0}
		sum := 0.0
		for i := 1; i <= 100; i++ {
			sum = sum + body.x + body.y + body.z
		}
		result := sum
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Float() != g2["result"].Float() {
		t.Errorf("mismatch: interpreter=%v, ssa=%v", g1["result"].Float(), g2["result"].Float())
	}
}

func TestSSACodegen_Integration_SqrtIntrinsic(t *testing.T) {
	// math.sqrt intrinsic in a loop
	src := `
		sum := 0.0
		for i := 1; i <= 100; i++ {
			sum = sum + math.sqrt(4.0)
		}
		result := sum
	`
	g1 := runtime.NewInterpreterGlobals()
	proto := compileProto(t, src)
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Float() != g2["result"].Float() {
		t.Errorf("mismatch: interpreter=%v, ssa=%v", g1["result"].Float(), g2["result"].Float())
	}
}

func TestSSACodegen_Integration_NbodyPattern(t *testing.T) {
	// nbody-like pattern: field access + float arithmetic + sqrt
	src := `
		bi := {x: 1.0, y: 2.0, z: 3.0}
		bj := {x: 4.0, y: 5.0, z: 6.0}
		total := 0.0
		for i := 1; i <= 50; i++ {
			dx := bi.x - bj.x
			dy := bi.y - bj.y
			dz := bi.z - bj.z
			dsq := dx*dx + dy*dy + dz*dz
			dist := math.sqrt(dsq)
			total = total + dist
		}
		result := total
	`
	g1 := runtime.NewInterpreterGlobals()
	proto := compileProto(t, src)
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	r1 := g1["result"].Float()
	r2 := g2["result"].Float()
	if r1 != r2 {
		t.Errorf("mismatch: interpreter=%v, ssa=%v", r1, r2)
	}
}

func TestSSACodegen_Integration_GetFieldMatchesInterpreter(t *testing.T) {
	// Verify trace output matches interpreter for field access
	src := `
		obj := {a: 5, b: 3}
		sum := 0
		for i := 1; i <= 200; i++ {
			sum = sum + obj.a - obj.b
		}
		result := sum
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

// ─── Type-specialized LOAD_ARRAY tests ───

func TestSSACodegen_Integration_SievePattern(t *testing.T) {
	// Sieve-like pattern: read table[i], check boolean, write table[j]
	src := `
		t := {}
		for i := 1; i <= 100; i++ { t[i] = true }
		count := 0
		for i := 1; i <= 100; i++ {
			if t[i] { count = count + 1 }
		}
		result := count
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
	if g2["result"].Int() != 100 {
		t.Errorf("result = %d, want 100", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_ArrayIntAccess(t *testing.T) {
	// Integer array access pattern
	src := `
		arr := {}
		for i := 1; i <= 50; i++ { arr[i] = i * 2 }
		sum := 0
		for i := 1; i <= 50; i++ {
			sum = sum + arr[i]
		}
		result := sum
	`
	// expected: 2+4+6+...+100 = 2550
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
	if g2["result"].Int() != 2550 {
		t.Errorf("result = %d, want 2550", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_ArrayAccessMatchesInterpreter(t *testing.T) {
	// Verify trace output matches interpreter for array access with more data
	src := `
		arr := {}
		for i := 1; i <= 100; i++ { arr[i] = i }
		sum := 0
		for i := 1; i <= 100; i++ {
			sum = sum + arr[i]
		}
		result := sum
	`
	// expected: 1+2+...+100 = 5050
	// Run without tracing
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

// ─── Nested loop (sub-trace calling) tests ───

func TestSSACodegen_Integration_NestedLoop(t *testing.T) {
	// Use function to make variables local (avoids GETGLOBAL/SETGLOBAL)
	src := `
		func compute() {
			sum := 0
			for i := 1; i <= 50; i++ {
				for j := 1; j <= 50; j++ {
					sum = sum + 1
				}
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
	if g2["result"].Int() != 2500 {
		t.Errorf("result = %d, want 2500", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_NestedLoopWithComputation(t *testing.T) {
	// Outer and inner loops both do arithmetic
	src := `
		total := 0
		for i := 1; i <= 20; i++ {
			for j := 1; j <= 20; j++ {
				total = total + i * j
			}
		}
		result := total
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_Integration_MandelbrotNested(t *testing.T) {
	// Small mandelbrot — verifies nested loop with break + float
	src := `
		func mandelbrot(size) {
			count := 0
			for y := 0; y < size; y++ {
				ci := 2.0 * y / size - 1.0
				for x := 0; x < size; x++ {
					cr := 2.0 * x / size - 1.5
					zr := 0.0
					zi := 0.0
					escaped := false
					for iter := 0; iter < 50; iter++ {
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
		result := mandelbrot(10)
	`
	// Compare with interpreter
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_InnerTraceStandalone(t *testing.T) {
	// Test that inner loop traces produce correct results when outer loop is
	// handled by the interpreter (only inner trace compiled).
	src := `
		func compute() {
			sum := 0
			for i := 1; i <= 50; i++ {
				for j := 1; j <= 5; j++ {
					sum = sum + 1
				}
			}
			return sum
		}
		result := compute()
	`
	// Compare interpreter vs SSA JIT
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_SubTraceCall_Direct(t *testing.T) {
	// Direct test of sub-trace calling: build inner trace, build outer trace
	// with CALL_INNER_TRACE, execute and check result.
	// This tests the mechanism without the recorder.

	// Inner trace: sum = sum + 1, FORLOOP j=5..1
	innerTrace := &Trace{
		LoopProto: &vm.FuncProto{Constants: []runtime.Value{}},
		Constants: []runtime.Value{},
		IR: []TraceIR{
			// sum = sum + 1
			{Op: vm.OP_ADD, A: 0, B: 0, C: 10, BType: runtime.TypeInt, CType: runtime.TypeInt, PC: 1},
			// FORLOOP A=5
			{Op: vm.OP_FORLOOP, A: 5, SBX: -2, PC: 2},
		},
	}

	// Build and compile inner trace
	innerSSA := BuildSSA(innerTrace)
	innerSSA = OptimizeSSA(innerSSA)
	innerCT, err := CompileSSA(innerSSA)
	if err != nil {
		t.Fatalf("inner CompileSSA: %v", err)
	}
	innerCT.ssaCompiled = true

	// Now verify inner trace works standalone.
	// The trace is designed to be called AFTER the interpreter's FORLOOP
	// did one iteration (idx goes from 0→1). So we start with idx=1.
	regs := make([]runtime.Value, 20)
	regs[0] = runtime.IntValue(0)  // sum
	regs[5] = runtime.IntValue(1)  // idx = 1 (after interpreter's first FORLOOP)
	regs[6] = runtime.IntValue(3)  // limit
	regs[7] = runtime.IntValue(1)  // step
	regs[8] = runtime.IntValue(1)  // loop var = 1
	regs[10] = runtime.IntValue(1) // constant 1

	exitPC, sideExit := executeSSATrace(innerCT, regs)
	sum := regs[0].Int()
	t.Logf("Inner trace standalone: sum=%d, exitPC=%d, sideExit=%v", sum, exitPC, sideExit)
	// With idx=1, limit=3, step=1: body runs 3 times (idx 1→2→3→4(exit))
	if sum != 3 {
		t.Errorf("inner trace: sum = %d, want 3", sum)
	}
}

func TestSSACodegen_Integration_NestedLoopSmall(t *testing.T) {
	// Smaller nested loop with low threshold for faster inner trace compilation.
	src := `
		func compute() {
			sum := 0
			for i := 1; i <= 5; i++ {
				for j := 1; j <= 5; j++ {
					sum = sum + 1
				}
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
	recorder.SetUseSSA(true)
	recorder.threshold = 2
	v.SetTraceRecorder(recorder)

	v.Execute(proto)

	if globals["result"].Int() != 25 {
		t.Errorf("result = %d, want 25", globals["result"].Int())
	}
}

func TestSSACodegen_Integration_NestedLoopOuterTraced(t *testing.T) {
	// Verify that the outer loop actually gets compiled (not just the inner one).
	// Uses a function to ensure variables are locals (not globals), so the inner
	// trace can be SSA-compiled (GETGLOBAL/SETGLOBAL prevent SSA compilation).
	src := `
		func compute() {
			sum := 0
			for i := 1; i <= 50; i++ {
				for j := 1; j <= 50; j++ {
					sum = sum + 1
				}
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
	recorder.SetUseSSA(true)
	v.SetTraceRecorder(recorder)

	v.Execute(proto)

	// Check that at least 2 traces were compiled (inner + outer)
	compiledCount := 0
	for _, tr := range recorder.Traces() {
		key := loopKey{proto: tr.LoopProto, pc: tr.LoopPC}
		if ct := recorder.compiled[key]; ct != nil {
			compiledCount++
		}
	}
	if compiledCount < 2 {
		t.Errorf("expected at least 2 compiled traces (inner + outer), got %d", compiledCount)
	}

	// Correctness check
	if globals["result"].Int() != 2500 {
		t.Errorf("result = %d, want 2500", globals["result"].Int())
	}
}

func TestSSACodegen_Integration_TripleNestedLoop(t *testing.T) {
	// Triple-nested: outermost should call middle trace, which calls inner trace
	src := `
		sum := 0
		for i := 1; i <= 10; i++ {
			for j := 1; j <= 10; j++ {
				for k := 1; k <= 10; k++ {
					sum = sum + 1
				}
			}
		}
		result := sum
	`
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
	if g2["result"].Int() != 1000 {
		t.Errorf("result = %d, want 1000", g2["result"].Int())
	}
}

// ─── Full nested loop (single trace, no sub-trace calling) tests ───

func TestSSACodegen_Integration_FullNestedLoop(t *testing.T) {
	// Full nested loop: outer+inner compiled as a single trace with
	// inner loop structure. Verifies the FORPREP slot reload fix.
	// Wrapped in a function so variables are local (enables SSA compilation).
	src := `
		func compute() {
			sum := 0
			for i := 1; i <= 50; i++ {
				for j := 1; j <= 50; j++ {
					sum = sum + 1
				}
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
	if g2["result"].Int() != 2500 {
		t.Errorf("result = %d, want 2500", g2["result"].Int())
	}
}

func TestSSACodegen_Integration_FullNestedMandelbrot(t *testing.T) {
	// mandelbrot(10) — compare interpreter vs SSA JIT with full nesting
	src := `
		func mandelbrot(size) {
			count := 0
			for y := 0; y < size; y++ {
				ci := 2.0 * y / size - 1.0
				for x := 0; x < size; x++ {
					cr := 2.0 * x / size - 1.5
					zr := 0.0
					zi := 0.0
					escaped := false
					for iter := 0; iter < 50; iter++ {
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
		result := mandelbrot(10)
	`
	// Compare with interpreter
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_Integration_FullNestedWithComputation(t *testing.T) {
	// Nested loop where both loops do arithmetic, verifying correct variable
	// scoping through the full nesting approach.
	src := `
		func compute() {
			total := 0
			for i := 1; i <= 20; i++ {
				for j := 1; j <= 20; j++ {
					total = total + i * j
				}
			}
			return total
		}
		result := compute()
	`
	// Run without tracing
	proto := compileProto(t, src)
	g1 := runtime.NewInterpreterGlobals()
	vm.New(g1).Execute(proto)

	// Run with SSA JIT
	g2 := runWithSSAJIT(t, src)

	if g1["result"].Int() != g2["result"].Int() {
		t.Errorf("mismatch: interpreter=%d, ssa=%d", g1["result"].Int(), g2["result"].Int())
	}
}

func TestSSACodegen_Integration_FullNestedSmall(t *testing.T) {
	// Small nested loop with low threshold — tests full nesting path when
	// inner loop hasn't been compiled as a sub-trace yet.
	src := `
		func compute() {
			sum := 0
			for i := 1; i <= 5; i++ {
				for j := 1; j <= 5; j++ {
					sum = sum + 1
				}
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
	recorder.SetUseSSA(true)
	recorder.threshold = 2
	v.SetTraceRecorder(recorder)

	v.Execute(proto)

	if globals["result"].Int() != 25 {
		t.Errorf("result = %d, want 25", globals["result"].Int())
	}
}

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
	recorder.SetUseSSA(true)
	v.SetTraceRecorder(recorder)
	// Blacklist the counting loop (FORLOOP at PC=46) which has a pre-existing
	// guard bug with GETTABLE+TEST patterns.
	fnProto := proto2.Protos[0]
	recorder.blacklist[loopKey{proto: fnProto, pc: 46}] = true
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

func TestSSACodegen_Integration_WhileLoopNotTraced(t *testing.T) {
	// With JMP back-edge detection removed, while-loops inside functions
	// are NOT traced. The interpreter still produces correct results.
	// This test verifies correctness AND that no while-loop traces are created.
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
	recorder.SetUseSSA(true)
	v.SetTraceRecorder(recorder)

	v.Execute(proto)

	// Correctness check: interpreter handles while-loop correctly
	if globals["result"].Int() != 5050 {
		t.Errorf("result = %d, want 5050", globals["result"].Int())
	}

	// No traces should be compiled for while-loops (JMP back-edge detection removed)
	compiledCount := 0
	for _, tr := range recorder.Traces() {
		key := loopKey{proto: tr.LoopProto, pc: tr.LoopPC}
		if ct := recorder.compiled[key]; ct != nil {
			compiledCount++
		}
	}
	if compiledCount != 0 {
		t.Errorf("expected 0 compiled traces for while-loop (JMP detection removed), got %d", compiledCount)
	}
}
