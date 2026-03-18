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
