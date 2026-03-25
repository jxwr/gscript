//go:build darwin && arm64

package jit

import (
	"math"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// compileAndRunVM compiles GScript source, runs it with the bytecode VM only (no JIT).
func compileAndRunVM(t *testing.T, src string) (map[string]runtime.Value, string) {
	t.Helper()

	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := vm.Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	var buf strings.Builder
	globals := runtime.NewInterpreterGlobals()
	globals["print"] = runtime.FunctionValue(&runtime.GoFunction{
		Name: "print",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = a.String()
			}
			buf.WriteString(strings.Join(parts, "\t"))
			buf.WriteString("\n")
			return nil, nil
		},
	})

	v := vm.New(globals)
	// No JIT — pure bytecode VM
	_, err = v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return globals, buf.String()
}

// runVMGetInt runs src in VM-only mode and returns the named global as int64.
func runVMGetInt(t *testing.T, src, key string) int64 {
	t.Helper()
	globals, _ := compileAndRunVM(t, src)
	v, ok := globals[key]
	if !ok {
		t.Fatalf("VM: global %q not found", key)
	}
	if !v.IsInt() {
		t.Fatalf("VM: global %q: expected int, got %v (type=%d)", key, v, v.Type())
	}
	return v.Int()
}

// runJITGetInt runs src with JIT and returns the named global as int64.
func runJITGetInt(t *testing.T, src, key string) int64 {
	t.Helper()
	globals, _ := compileAndRunJIT(t, src)
	v, ok := globals[key]
	if !ok {
		t.Fatalf("JIT: global %q not found", key)
	}
	if !v.IsInt() {
		t.Fatalf("JIT: global %q: expected int, got %v (type=%d)", key, v, v.Type())
	}
	return v.Int()
}

// runVMGetFloat runs src in VM-only mode and returns the named global as float64.
func runVMGetFloat(t *testing.T, src, key string) float64 {
	t.Helper()
	globals, _ := compileAndRunVM(t, src)
	v, ok := globals[key]
	if !ok {
		t.Fatalf("VM: global %q not found", key)
	}
	if !v.IsFloat() {
		t.Fatalf("VM: global %q: expected float, got %v (type=%d)", key, v, v.Type())
	}
	return v.Float()
}

// runJITGetFloat runs src with JIT and returns the named global as float64.
func runJITGetFloat(t *testing.T, src, key string) float64 {
	t.Helper()
	globals, _ := compileAndRunJIT(t, src)
	v, ok := globals[key]
	if !ok {
		t.Fatalf("JIT: global %q not found", key)
	}
	if !v.IsFloat() {
		t.Fatalf("JIT: global %q: expected float, got %v (type=%d)", key, v, v.Type())
	}
	return v.Float()
}

// floatClose returns true if a and b are within epsilon.
func floatClose(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// ---------- Integer Arithmetic: SSA_ADD_INT, SSA_SUB_INT, SSA_MUL_INT, SSA_MOD_INT, SSA_NEG_INT, SSA_DIV_INT ----------

func TestOpMatrix_IntArith(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"ADD_INT",
			`func f() { s:=0; for i:=1;i<=10;i++ { s=s+i }; return s }; result = f()`,
			"result", 55,
		},
		{
			"SUB_INT",
			`func f() { s:=100; for i:=1;i<=10;i++ { s=s-i }; return s }; result = f()`,
			"result", 45,
		},
		{
			"MUL_INT",
			`func f() { s:=1; for i:=1;i<=5;i++ { s=s*i }; return s }; result = f()`,
			"result", 120,
		},
		{
			"MOD_INT",
			`func f() { s:=0; for i:=1;i<=10;i++ { s=s+i%3 }; return s }; result = f()`,
			"result", 10, // i%3 for i=1..10: 1+2+0+1+2+0+1+2+0+1 = 10
		},
		{
			"NEG_INT",
			`func f() { s:=0; for i:=1;i<=5;i++ { s=s+(-i) }; return s }; result = f()`,
			"result", -15,
		},
		{
			// Combined: subtraction + multiplication (another MUL_INT pattern)
			"MUL_SUB_combined",
			`func f() { s:=0; for i:=1;i<=10;i++ { s=s+i*i-i }; return s }; result = f()`,
			"result", 330, // sum(i^2-i) for i=1..10 = 385-55 = 330
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- Float Arithmetic: SSA_ADD_FLOAT, SSA_SUB_FLOAT, SSA_MUL_FLOAT, SSA_DIV_FLOAT, SSA_NEG_FLOAT ----------

func TestOpMatrix_FloatArith(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want float64
	}{
		{
			"ADD_FLOAT",
			`func f() { s:=0.0; for i:=1;i<=10;i++ { s=s+0.5 }; return s }; result = f()`,
			"result", 5.0,
		},
		{
			"SUB_FLOAT",
			`func f() { s:=10.0; for i:=1;i<=5;i++ { s=s-0.5 }; return s }; result = f()`,
			"result", 7.5,
		},
		{
			"MUL_FLOAT",
			`func f() { s:=1.0; for i:=1;i<=5;i++ { s=s*2.0 }; return s }; result = f()`,
			"result", 32.0,
		},
		{
			"DIV_FLOAT",
			`func f() { s:=1024.0; for i:=1;i<=5;i++ { s=s/2.0 }; return s }; result = f()`,
			"result", 32.0,
		},
		{
			"NEG_FLOAT",
			`func f() { s:=0.0; for i:=1;i<=5;i++ { s=s+(-1.5) }; return s }; result = f()`,
			"result", -7.5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetFloat(t, tt.src, tt.key)
			jitResult := runJITGetFloat(t, tt.src, tt.key)
			if !floatClose(vmResult, jitResult, 1e-9) {
				t.Errorf("VM=%f, JIT=%f — mismatch", vmResult, jitResult)
			}
			if !floatClose(vmResult, tt.want, 1e-9) {
				t.Errorf("VM result %f != expected %f", vmResult, tt.want)
			}
			if !floatClose(jitResult, tt.want, 1e-9) {
				t.Errorf("JIT result %f != expected %f", jitResult, tt.want)
			}
		})
	}
}

// ---------- FMADD/FMSUB: SSA_FMADD, SSA_FMSUB ----------

func TestOpMatrix_FusedMulAdd(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want float64
	}{
		{
			// a*b+c pattern triggers FMADD
			"FMADD",
			`func f() { s:=0.0; for i:=1;i<=100;i++ { s = s + 2.0*3.0 }; return s }; result = f()`,
			"result", 600.0,
		},
		{
			// body.x = body.x + body.vx * dt is a typical FMADD pattern
			"FMADD_accumulate",
			`func f() {
				x := 0.0
				vx := 1.5
				dt := 0.1
				for i:=1;i<=100;i++ { x = x + vx * dt }
				return x
			}; result = f()`,
			"result", 15.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetFloat(t, tt.src, tt.key)
			jitResult := runJITGetFloat(t, tt.src, tt.key)
			if !floatClose(vmResult, jitResult, 1e-6) {
				t.Errorf("VM=%f, JIT=%f — mismatch", vmResult, jitResult)
			}
			if !floatClose(jitResult, tt.want, 1e-6) {
				t.Errorf("JIT result %f != expected %f", jitResult, tt.want)
			}
		})
	}
}

// ---------- Comparisons: SSA_EQ_INT, SSA_LT_INT, SSA_LE_INT, SSA_LT_FLOAT, SSA_LE_FLOAT, SSA_GT_FLOAT ----------

func TestOpMatrix_Comparisons(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"EQ_INT",
			`func f() { c:=0; for i:=0;i<20;i++ { if i==10 { c=c+1 } }; return c }; result = f()`,
			"result", 1,
		},
		{
			"LT_INT",
			`func f() { c:=0; for i:=0;i<20;i++ { if i<5 { c=c+1 } }; return c }; result = f()`,
			"result", 5,
		},
		{
			"LE_INT",
			`func f() { c:=0; for i:=0;i<20;i++ { if i<=5 { c=c+1 } }; return c }; result = f()`,
			"result", 6,
		},
		{
			"GT_FLOAT",
			`func f() { c:=0; s:=0.0; for i:=1;i<=20;i++ { s=s+0.5; if s>4.0 { c=c+1 } }; return c }; result = f()`,
			"result", 12,
		},
		{
			"LT_FLOAT",
			`func f() { c:=0; s:=10.0; for i:=1;i<=20;i++ { s=s-0.5; if s<5.0 { c=c+1 } }; return c }; result = f()`,
			"result", 10, // s goes 9.5,9.0,...,0.0 — first <5.0 at s=4.5 (i=12), so 20-12+1=9... let me recalc
			// s=10-0.5*i. s<5 when i>10, so i=11..20 -> 10 times
		},
		{
			"LE_FLOAT",
			`func f() { c:=0; s:=10.0; for i:=1;i<=20;i++ { s=s-0.5; if s<=5.0 { c=c+1 } }; return c }; result = f()`,
			"result", 11, // s<=5 when i>=10, so i=10..20 -> 11 times
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- Table Operations: SSA_LOAD_FIELD, SSA_STORE_FIELD, SSA_LOAD_ARRAY, SSA_STORE_ARRAY, SSA_LOAD_GLOBAL, SSA_TABLE_LEN ----------

func TestOpMatrix_TableOps(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"LOAD_FIELD",
			`func f(tbl) { s:=0; for i:=1;i<=5;i++ { s=s+tbl.x }; return s }; t:={x: 10}; result = f(t)`,
			"result", 50,
		},
		{
			"STORE_FIELD",
			`func f(tbl) { for i:=1;i<=5;i++ { tbl.x=tbl.x+1 }; return tbl.x }; t:={x: 0}; result = f(t)`,
			"result", 5,
		},
		{
			"LOAD_ARRAY",
			`func f(a) { s:=0; for i:=1;i<=3;i++ { s=s+a[i] }; return s }; a:={10,20,30}; result = f(a)`,
			"result", 60,
		},
		{
			"STORE_ARRAY",
			`func f(a) { for i:=1;i<=5;i++ { a[i]=i*10 }; return a[3] }; a:={0,0,0,0,0}; result = f(a)`,
			"result", 30,
		},
		{
			"TABLE_LEN",
			`func f(a) { s:=0; n:=#a; for i:=1;i<=n;i++ { s=s+1 }; return s }; a:={10,20,30,40,50}; result = f(a)`,
			"result", 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- LOAD_GLOBAL: SSA_LOAD_GLOBAL ----------

func TestOpMatrix_LoadGlobal(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"GETGLOBAL",
			`g = 42; func f() { s:=0; for i:=1;i<=5;i++ { s=s+g }; return s }; result = f()`,
			"result", 210,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
		})
	}
}

// ---------- Control Flow: SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_LOOP, SSA_SIDE_EXIT ----------

func TestOpMatrix_ControlFlow(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"GUARD_TRUTHY",
			`func f() { c:=0; for i:=1;i<=10;i++ { if i>5 { c=c+1 } }; return c }; result = f()`,
			"result", 5,
		},
		{
			"BREAK_side_exit",
			`func f() { c:=0; for i:=1;i<=100;i++ { if i>10 { break }; c=c+1 }; return c }; result = f()`,
			"result", 10,
		},
		{
			"NESTED_LOOP",
			`func f() { s:=0; for i:=1;i<=5;i++ { for j:=1;j<=5;j++ { s=s+1 } }; return s }; result = f()`,
			"result", 25,
		},
		{
			"LOOP_countdown",
			`func f() { c:=0; for i:=100;i>0;i-- { c=c+1 }; return c }; result = f()`,
			"result", 100,
		},
		{
			"WHILE_loop",
			`func f() { n:=27; steps:=0; for n!=1 { if n%2==0 { n=n/2 } else { n=n*3+1 }; steps=steps+1 }; return steps }; result = f()`,
			"result", 111, // Collatz(27) = 111 steps
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- MOVE / PHI: SSA_MOVE, SSA_PHI ----------

func TestOpMatrix_Move(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"MOVE",
			`func f() { a:=0; b:=0; for i:=1;i<=10;i++ { a=i; b=a }; return b }; result = f()`,
			"result", 10,
		},
		{
			// PHI is exercised by loop-carried variables (a,b in fibonacci)
			"PHI_fib",
			`func f() { a:=0; b:=1; for i:=0;i<20;i++ { tmp:=a+b; a=b; b=tmp }; return a }; result = f()`,
			"result", 6765, // fib(20)
		},
		{
			// swap pattern exercises MOVE
			"MOVE_swap",
			`func f() { a:=1; b:=2; for i:=1;i<=10;i++ { tmp:=a; a=b; b=tmp }; return a }; result = f()`,
			"result", 2, // even iterations: a=1,b=2 -> a=2,b=1 -> a=1,b=2 ... 10 is even so a=1? No: start a=1,b=2, after 1: a=2,b=1, after 2: a=1,b=2, ...after 10: a=1,b=2
			// Wait: 10 swaps on initial (1,2): even number of swaps returns to original. a=1
		},
	}
	// Fix MOVE_swap: after 10 swaps starting from a=1,b=2: even count => a=1, b=2
	tests[2].want = 1

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- Constants: SSA_CONST_INT, SSA_CONST_FLOAT, SSA_CONST_NIL, SSA_CONST_BOOL ----------

func TestOpMatrix_Constants(t *testing.T) {
	t.Run("CONST_INT", func(t *testing.T) {
		src := `func f() { s:=0; for i:=1;i<=10;i++ { s=s+42 }; return s }; result = f()`
		vmResult := runVMGetInt(t, src, "result")
		jitResult := runJITGetInt(t, src, "result")
		if vmResult != 420 || jitResult != 420 {
			t.Errorf("VM=%d, JIT=%d, want 420", vmResult, jitResult)
		}
	})

	t.Run("CONST_FLOAT", func(t *testing.T) {
		src := `func f() { s:=0.0; for i:=1;i<=10;i++ { s=s+3.14 }; return s }; result = f()`
		vmResult := runVMGetFloat(t, src, "result")
		jitResult := runJITGetFloat(t, src, "result")
		if !floatClose(vmResult, 31.4, 1e-9) || !floatClose(jitResult, 31.4, 1e-9) {
			t.Errorf("VM=%f, JIT=%f, want 31.4", vmResult, jitResult)
		}
	})
}

// ---------- SSA_CALL (side-exit) ----------

func TestOpMatrix_CallExit(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"CALL_FUNC",
			`func double(x) { return x*2 }; func f() { s:=0; for i:=1;i<=5;i++ { s=s+double(i) }; return s }; result = f()`,
			"result", 30, // 2+4+6+8+10=30
		},
		{
			"CALL_RECURSIVE",
			`func fib(n) { if n<2 { return n }; return fib(n-1)+fib(n-2) }; result = fib(15)`,
			"result", 610,
		},
		{
			"CALL_MULTI_RETURN",
			`func swap(a,b) { return b,a }; func f() { x,y := swap(1,2); return x*10+y }; result = f()`,
			"result", 21,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- Intrinsics: SSA_INTRINSIC (math.sqrt) ----------

func TestOpMatrix_Intrinsics(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want float64
	}{
		{
			"SQRT",
			`func f() { s:=0.0; for i:=1;i<=100;i++ { s=s+math.sqrt(4.0) }; return s }; result = f()`,
			"result", 200.0, // sqrt(4)=2, 100*2=200
		},
		{
			"SQRT_varying",
			`func f() {
				s:=0.0
				for i:=1;i<=4;i++ {
					x := i * 1.0
					s = s + math.sqrt(x * x)
				}
				return s
			}; result = f()`,
			"result", 10.0, // sqrt(1)+sqrt(4)+sqrt(9)+sqrt(16) = 1+2+3+4=10
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetFloat(t, tt.src, tt.key)
			jitResult := runJITGetFloat(t, tt.src, tt.key)
			if !floatClose(vmResult, jitResult, 1e-9) {
				t.Errorf("VM=%f, JIT=%f — mismatch", vmResult, jitResult)
			}
			if !floatClose(jitResult, tt.want, 1e-6) {
				t.Errorf("JIT result %f != expected %f", jitResult, tt.want)
			}
		})
	}
}

// ---------- Box/Unbox: SSA_UNBOX_INT, SSA_UNBOX_FLOAT, SSA_BOX_INT, SSA_BOX_FLOAT ----------
// Box/unbox ops are implicit (generated by the SSA builder when values cross type boundaries).
// We test them indirectly via mixed-type operations that require boxing/unboxing.

func TestOpMatrix_BoxUnbox(t *testing.T) {
	t.Run("INT_box_unbox", func(t *testing.T) {
		// Loop accumulates int, stores to table (requires boxing), reads back (requires unboxing)
		src := `func f(tbl) { for i:=1;i<=10;i++ { tbl.val = tbl.val + i }; return tbl.val }; t:={val: 0}; result = f(t)`
		vmResult := runVMGetInt(t, src, "result")
		jitResult := runJITGetInt(t, src, "result")
		if vmResult != 55 || jitResult != 55 {
			t.Errorf("VM=%d, JIT=%d, want 55", vmResult, jitResult)
		}
	})

	t.Run("FLOAT_box_unbox", func(t *testing.T) {
		// Loop accumulates float, stores to table (requires boxing), reads back (requires unboxing)
		src := `func f(tbl) { for i:=1;i<=10;i++ { tbl.val = tbl.val + 0.5 }; return tbl.val }; t:={val: 0.0}; result = f(t)`
		vmResult := runVMGetFloat(t, src, "result")
		jitResult := runJITGetFloat(t, src, "result")
		if !floatClose(vmResult, 5.0, 1e-9) || !floatClose(jitResult, 5.0, 1e-9) {
			t.Errorf("VM=%f, JIT=%f, want 5.0", vmResult, jitResult)
		}
	})
}

// ---------- Guard: SSA_GUARD_TYPE, SSA_GUARD_TRUTHY, SSA_GUARD_NNIL, SSA_GUARD_NOMETA ----------
// Guards are exercised implicitly by all typed operations. We test specific guard patterns here.

func TestOpMatrix_Guards(t *testing.T) {
	t.Run("GUARD_TYPE_int", func(t *testing.T) {
		// Type guard fires on every loop iteration when operating on ints
		src := `func f() { s:=0; for i:=1;i<=100;i++ { s=s+i }; return s }; result = f()`
		vmResult := runVMGetInt(t, src, "result")
		jitResult := runJITGetInt(t, src, "result")
		if vmResult != 5050 || jitResult != 5050 {
			t.Errorf("VM=%d, JIT=%d, want 5050", vmResult, jitResult)
		}
	})

	t.Run("GUARD_TYPE_float", func(t *testing.T) {
		// Type guard fires for float operations
		src := `func f() { s:=0.0; for i:=1;i<=100;i++ { s=s+1.0 }; return s }; result = f()`
		vmResult := runVMGetFloat(t, src, "result")
		jitResult := runJITGetFloat(t, src, "result")
		if !floatClose(vmResult, 100.0, 1e-9) || !floatClose(jitResult, 100.0, 1e-9) {
			t.Errorf("VM=%f, JIT=%f, want 100.0", vmResult, jitResult)
		}
	})

	t.Run("GUARD_TRUTHY_nested", func(t *testing.T) {
		// Multiple nested if-conditions exercise chained truthy guards
		src := `func f() {
			c:=0
			for i:=1;i<=100;i++ {
				if i%2==0 {
					if i%3==0 {
						c=c+1
					}
				}
			}
			return c
		}; result = f()`
		vmResult := runVMGetInt(t, src, "result")
		jitResult := runJITGetInt(t, src, "result")
		want := int64(16) // multiples of 6 in 1..100: 6,12,...,96 = 16
		if vmResult != want || jitResult != want {
			t.Errorf("VM=%d, JIT=%d, want %d", vmResult, jitResult, want)
		}
	})
}

// ---------- Inner Trace / Sub-trace: SSA_CALL_INNER_TRACE, SSA_INNER_LOOP ----------

func TestOpMatrix_InnerTrace(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			"INNER_LOOP_sum",
			`func f() {
				total := 0
				for i:=1;i<=10;i++ {
					for j:=1;j<=10;j++ {
						total = total + 1
					}
				}
				return total
			}; result = f()`,
			"result", 100,
		},
		{
			"INNER_LOOP_product_sum",
			`func f() {
				total := 0
				for i:=1;i<=10;i++ {
					for j:=1;j<=10;j++ {
						total = total + i*j
					}
				}
				return total
			}; result = f()`,
			"result", 3025, // (sum 1..10)^2 = 55^2 = 3025
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- Snapshot (deopt): SSA_SNAPSHOT ----------
// Snapshots are tested indirectly — they fire when a side-exit occurs and the VM resumes.
// We exercise them by mixing JIT-compilable and non-compilable operations.

func TestOpMatrix_Snapshot(t *testing.T) {
	tests := []struct {
		name string
		src  string
		key  string
		want int64
	}{
		{
			// String concat triggers side-exit + snapshot restore
			"SNAPSHOT_string_exit",
			`func f() {
				s := ""
				for i:=0;i<50;i++ { s = s .. "x" }
				return #s
			}; result = f()`,
			"result", 50,
		},
		{
			// Function call within a hot loop triggers side-exit + snapshot
			"SNAPSHOT_call_in_loop",
			`func inc(x) { return x+1 }
			func f() { s:=0; for i:=1;i<=100;i++ { s=inc(s) }; return s }; result = f()`,
			"result", 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmResult := runVMGetInt(t, tt.src, tt.key)
			jitResult := runJITGetInt(t, tt.src, tt.key)
			if vmResult != jitResult {
				t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
			}
			if vmResult != tt.want {
				t.Errorf("VM result %d != expected %d", vmResult, tt.want)
			}
			if jitResult != tt.want {
				t.Errorf("JIT result %d != expected %d", jitResult, tt.want)
			}
		})
	}
}

// ---------- Comprehensive end-to-end: exercises many opcodes in one program ----------

func TestOpMatrix_Comprehensive(t *testing.T) {
	t.Run("mandelbrot_small", func(t *testing.T) {
		src := `func f() {
			count := 0
			for py := 0; py < 10; py++ {
				for px := 0; px < 10; px++ {
					x0 := px * 0.4 - 2.0
					y0 := py * 0.4 - 2.0
					x := 0.0
					y := 0.0
					iter := 0
					for iter < 50 {
						if x*x + y*y > 4.0 { break }
						xtemp := x*x - y*y + x0
						y = 2.0*x*y + y0
						x = xtemp
						iter = iter + 1
					}
					if iter == 50 { count = count + 1 }
				}
			}
			return count
		}; result = f()`
		vmResult := runVMGetInt(t, src, "result")
		jitResult := runJITGetInt(t, src, "result")
		if vmResult != jitResult {
			t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
		}
		// We don't hardcode the expected count — just verify VM == JIT
	})

	t.Run("sieve_small", func(t *testing.T) {
		// Prime sieve exercises arrays, conditionals, nested loops
		src := `func f() {
			n := 100
			sieve := {}
			for i := 1; i <= n; i++ { sieve[i] = 1 }
			sieve[1] = 0
			for i := 2; i <= n; i++ {
				if sieve[i] == 1 {
					j := i * 2
					for j <= n {
						sieve[j] = 0
						j = j + i
					}
				}
			}
			count := 0
			for i := 1; i <= n; i++ {
				if sieve[i] == 1 { count = count + 1 }
			}
			return count
		}; result = f()`
		vmResult := runVMGetInt(t, src, "result")
		jitResult := runJITGetInt(t, src, "result")
		want := int64(25) // 25 primes below 100
		if vmResult != want {
			t.Errorf("VM result %d != expected %d", vmResult, want)
		}
		if jitResult != want {
			t.Errorf("JIT result %d != expected %d", jitResult, want)
		}
		if vmResult != jitResult {
			t.Errorf("VM=%d, JIT=%d — mismatch", vmResult, jitResult)
		}
	})
}
