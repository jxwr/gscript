//go:build darwin && arm64

// tier1_native_test.go tests the native ARM64 implementations of table,
// field, global, upvalue, len, and self operations in the Tier 1 baseline
// compiler. Each test verifies VM vs JIT correctness.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// ---------------------------------------------------------------------------
// GETFIELD / SETFIELD (shape-guarded inline cache)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetField(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {x: 42}
    return t.x
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeSetField(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {x: 1}
    t.x = 99
    return t.x
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeGetFieldMultiple(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {x: 10, y: 20, z: 30}
    return t.x + t.y + t.z
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeSetFieldThenGet(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {}
    t.a = 100
    t.b = 200
    return t.a + t.b
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// GETTABLE / SETTABLE (array integer fast path)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetTable(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {10, 20, 30}
    return t[1] + t[2]
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeSetTable(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {0, 0, 0}
    t[1] = 42
    t[2] = 58
    return t[1] + t[2]
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeGetTableDynamic(t *testing.T) {
	// Test with dynamic integer key (register, not constant)
	compareVMvsJIT(t, `
func f(idx) {
    t := {10, 20, 30, 40, 50}
    return t[idx]
}
result := 0
for i := 1; i <= 200; i++ { result = f(3) }
`, "result")
}

func TestBaselineGetTable_FloatArray(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {1.5, 2.5, 3.5}
    return t[0] + t[1] + t[2]
}
result := 0.0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestBaselineGetTable_BoolArray(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {true, false, true}
    a := 0
    if t[0] { a = a + 1 }
    if t[1] { a = a + 10 }
    if t[2] { a = a + 100 }
    return a
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestBaselineSetTable_FloatArray(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {1.0, 2.0, 3.0}
    t[0] = 10.5
    t[2] = 30.5
    return t[0] + t[1] + t[2]
}
result := 0.0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestBaselineSetTable_BoolArray(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    t := {true, false, true}
    t[0] = false
    t[2] = false
    a := 0
    if t[0] { a = a + 1 }
    if t[1] { a = a + 10 }
    if t[2] { a = a + 100 }
    return a
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// LEN
// ---------------------------------------------------------------------------

func TestTier1_NativeLen(t *testing.T) {
	// LEN currently falls through to exit-resume for correctness.
	// This test verifies correctness is maintained.
	compareVMvsJIT(t, `
func f() {
    t := {1, 2, 3, 4, 5}
    return #t
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeLenString(t *testing.T) {
	compareVMvsJIT(t, `
func f() {
    s := "hello"
    return #s
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// SELF (method call)
// ---------------------------------------------------------------------------

func TestTier1_NativeSelf(t *testing.T) {
	compareVMvsJIT(t, `
func make_obj() {
    obj := {}
    obj.value = 42
    obj.get_value = func(self) { return self.value }
    return obj
}
o := make_obj()
result := 0
for i := 1; i <= 200; i++ { result = o:get_value() }
`, "result")
}

// ---------------------------------------------------------------------------
// GETUPVAL / SETUPVAL (closure upvalue access)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetUpval(t *testing.T) {
	compareVMvsJIT(t, `
func make_adder(x) {
    func adder(y) { return x + y }
    return adder
}
add10 := make_adder(10)
result := add10(5)
`, "result")
}

func TestTier1_NativeSetUpval(t *testing.T) {
	compareVMvsJIT(t, `
func make_counter() {
    count := 0
    func inc() {
        count = count + 1
        return count
    }
    return inc
}
counter := make_counter()
result := 0
for i := 1; i <= 10; i++ { result = counter() }
`, "result")
}

// ---------------------------------------------------------------------------
// Combined operations (end-to-end)
// ---------------------------------------------------------------------------

func TestTier1_NativeTableAccumulator(t *testing.T) {
	// A loop that reads and writes table fields.
	compareVMvsJIT(t, `
func f() {
    t := {sum: 0}
    for i := 1; i <= 10; i++ {
        t.sum = t.sum + i
    }
    return t.sum
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_NativeTableArrayLoop(t *testing.T) {
	// A loop that reads from an array table.
	compareVMvsJIT(t, `
func sum_array() {
    t := {10, 20, 30, 40, 50}
    s := 0
    for i := 1; i <= 5; i++ {
        s = s + t[i]
    }
    return s
}
result := 0
for i := 1; i <= 200; i++ { result = sum_array() }
`, "result")
}

func TestTier1_NativeFieldAndGlobal(t *testing.T) {
	compareVMvsJIT(t, `
factor := 2
func scale_field() {
    t := {x: 21}
    return t.x * factor
}
result := 0
for i := 1; i <= 200; i++ { result = scale_field() }
`, "result")
}

// ---------------------------------------------------------------------------
// Field cache warming tests
// These verify that GETFIELD/SETFIELD produce correct results after the
// Go-side slow path populates the FieldCache, allowing the native inline
// cache to hit on subsequent calls.
// ---------------------------------------------------------------------------

func TestTier1_FieldCacheWarmColdThenHot(t *testing.T) {
	// First access warms the cache (cold), repeated access uses the cache (hot).
	compareVMvsJIT(t, `
func f() {
    t := {x: 42}
    return t.x
}
result := 0
for i := 1; i <= 10; i++ { result = f() }
`, "result")
}

func TestTier1_FieldCacheMultipleFields(t *testing.T) {
	// Multiple fields at different PCs each get their own cache entry.
	compareVMvsJIT(t, `
func f() {
    t := {a: 1, b: 2, c: 3, d: 4, e: 5}
    return t.a + t.b + t.c + t.d + t.e
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

func TestTier1_FieldCacheDifferentTableShapes(t *testing.T) {
	// Tables with different shapes should still produce correct results.
	// The cache is per-PC, so two different calls to f() create tables
	// with the same shape, and the cache hits correctly.
	compareVMvsJIT(t, `
func sum_fields(t) {
    return t.x + t.y
}
result := 0
for i := 1; i <= 200; i++ {
    t := {x: i, y: i * 2}
    result = sum_fields(t)
}
`, "result")
}

func TestTier1_FieldCacheSetThenGet(t *testing.T) {
	// SETFIELD populates the cache, then GETFIELD uses it.
	compareVMvsJIT(t, `
func f(v) {
    t := {}
    t.val = v
    t.doubled = v * 2
    return t.val + t.doubled
}
result := 0
for i := 1; i <= 200; i++ { result = f(i) }
`, "result")
}

func TestTier1_FieldCacheLoopAccess(t *testing.T) {
	// Repeated field access within a loop (the primary use case).
	compareVMvsJIT(t, `
func f() {
    t := {x: 0, y: 0}
    for i := 1; i <= 50; i++ {
        t.x = t.x + i
        t.y = t.y + i * 2
    }
    return t.x + t.y
}
result := 0
for i := 1; i <= 200; i++ { result = f() }
`, "result")
}

// ---------------------------------------------------------------------------
// GETGLOBAL (native per-PC value cache with generation invalidation)
// ---------------------------------------------------------------------------

func TestTier1_NativeGetGlobal_SelfRecursive(t *testing.T) {
	// Recursive function that loads itself via GETGLOBAL.
	// The cache should hit after the first miss.
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := 0
for i := 1; i <= 200; i++ { result = fib(10) }
`, "result")
}

func TestTier1_NativeGetGlobal_MutualRecursion(t *testing.T) {
	// Mutual recursion: is_even calls is_odd and vice versa.
	compareVMvsJIT(t, `
func is_even(n) {
    if n == 0 { return true }
    return is_odd(n - 1)
}
func is_odd(n) {
    if n == 0 { return false }
    return is_even(n - 1)
}
result := false
for i := 1; i <= 200; i++ { result = is_even(10) }
`, "result")
}

func TestTier1_NativeGetGlobal_SetGlobalInvalidates(t *testing.T) {
	// SETGLOBAL must invalidate the cache. Counter is read and written.
	compareVMvsJIT(t, `
counter := 0
func inc() {
    counter = counter + 1
}
for i := 1; i <= 200; i++ { inc() }
result := counter
`, "result")
}

func TestTier1_NativeGetGlobal_CrossFunctionInvalidation(t *testing.T) {
	// Function A sets a global, function B reads it.
	// The cache in B must be invalidated when A sets the global.
	compareVMvsJIT(t, `
value := 0
func setter(x) { value = x }
func getter() { return value }
result := 0
for i := 1; i <= 200; i++ {
    setter(i)
    result = getter()
}
`, "result")
}

func TestTier1_NativeGetGlobal_FunctionValue(t *testing.T) {
	// Loading a function value global (the primary optimization target).
	compareVMvsJIT(t, `
func double(x) { return x * 2 }
func apply(x) { return double(x) }
result := 0
for i := 1; i <= 200; i++ { result = apply(i) }
`, "result")
}

// ---------------------------------------------------------------------------
// Baseline feedback tests: verify Tier 1 type feedback collection
// ---------------------------------------------------------------------------

// compileAndRunForFeedback compiles and runs a GScript program with the
// baseline JIT, then finds the inner function named "f" and returns its proto
// so the caller can inspect the Feedback vector.
func compileAndRunForFeedback(t *testing.T, src string) (*vm.VM, *vm.FuncProto) {
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
	// Pre-allocate feedback vectors for all child protos so the JIT
	// feedback stubs have somewhere to write during execution.
	for _, child := range proto.Protos {
		child.EnsureFeedback()
	}
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)
	_, err = v.Execute(proto)
	if err != nil {
		t.Fatalf("JIT runtime error: %v", err)
	}
	// Find the inner function proto named "f".
	var fProto *vm.FuncProto
	for _, child := range proto.Protos {
		if child.Name == "f" {
			fProto = child
			break
		}
	}
	if fProto == nil {
		t.Fatalf("could not find inner function 'f' in proto.Protos")
	}
	return v, fProto
}

func TestBaselineFeedback_GetTable_Float(t *testing.T) {
	src := `
func f() {
    t := {1.5, 2.5, 3.5}
    return t[0] + t[1] + t[2]
}
result := 0.0
for i := 1; i <= 5; i++ { result = f() }
`
	_, proto := compileAndRunForFeedback(t, src)
	// Find the GETTABLE instructions and check their feedback.
	foundFloat := false
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_GETTABLE && proto.Feedback != nil && pc < len(proto.Feedback) {
			fb := proto.Feedback[pc]
			if fb.Result == vm.FBFloat {
				foundFloat = true
			}
		}
	}
	if !foundFloat {
		t.Errorf("expected at least one GETTABLE with FBFloat feedback, got none")
	}
}

func TestBaselineFeedback_GetTable_Int(t *testing.T) {
	src := `
func f() {
    t := {}
    for i := 0; i < 10; i++ { t[i] = i }
    return t[0] + t[5] + t[9]
}
result := 0
for i := 1; i <= 5; i++ { result = f() }
`
	_, proto := compileAndRunForFeedback(t, src)
	foundInt := false
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_GETTABLE && proto.Feedback != nil && pc < len(proto.Feedback) {
			fb := proto.Feedback[pc]
			if fb.Result == vm.FBInt {
				foundInt = true
			}
		}
	}
	if !foundInt {
		t.Errorf("expected at least one GETTABLE with FBInt feedback, got none")
	}
}
