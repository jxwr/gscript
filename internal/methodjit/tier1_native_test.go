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

func TestTier1_DisabledFeedbackOmitsTableInstrumentation(t *testing.T) {
	src := `
func f(arr, i, v) {
    x := arr[i]
    arr[i] = v
    return x
}
`
	withFeedback := compileFunction(t, src)
	enabledBF, err := CompileBaseline(withFeedback)
	if err != nil {
		t.Fatalf("CompileBaseline(with feedback): %v", err)
	}
	defer enabledBF.Code.Free()

	withoutFeedback := compileFunction(t, src)
	protoFeedbackCollectionDisabled[withoutFeedback] = true
	defer delete(protoFeedbackCollectionDisabled, withoutFeedback)
	disabledBF, err := CompileBaseline(withoutFeedback)
	if err != nil {
		t.Fatalf("CompileBaseline(without feedback): %v", err)
	}
	defer disabledBF.Code.Free()

	if disabledBF.Code.Size() >= enabledBF.Code.Size() {
		t.Fatalf("disabled feedback code size = %d, want less than enabled size %d",
			disabledBF.Code.Size(), enabledBF.Code.Size())
	}
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

func TestTier1_NativeGetFieldEmptyShapeMissReturnsNil(t *testing.T) {
	compareVMvsJIT(t, `
func f(t) {
    if t.left == nil { return 1 }
    return 2
}
full := {left: 99}
empty := {}
result := 0
for i := 1; i <= 200; i++ {
    result = result + f(full)
    result = result + f(empty)
}
`, "result")
}

func TestTier1_NativeNewObject2FastPath(t *testing.T) {
	src := `
func pair(a, b) {
    return {left: a, right: b}
}

result := 0
for i := 1; i <= 200; i++ {
    t := pair(i, i + 1)
    result = result + t.left + t.right
}

nilpair := pair(nil, 7)
if nilpair.left == nil {
    result = result + nilpair.right
}
`
	proto := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 40407 {
		t.Fatalf("result = %v, want 40407", result)
	}

	pairProto := findProtoByName(proto, "pair")
	if pairProto == nil {
		t.Fatal("pair proto not found")
	}
	bf := engine.compiled[pairProto]
	if bf == nil {
		t.Fatal("pair was not baseline compiled")
	}

	newObjectPC := -1
	for pc, inst := range pairProto.Code {
		if vm.DecodeOp(inst) == vm.OP_NEWOBJECT2 {
			newObjectPC = pc
			if !baselineNewObject2Cacheable(pairProto, inst) {
				t.Fatalf("NEWOBJECT2 at pc %d was not cacheable", pc)
			}
			break
		}
	}
	if newObjectPC < 0 {
		t.Fatal("pair did not compile to OP_NEWOBJECT2")
	}
	if len(bf.NewTableCaches) == 0 {
		t.Fatal("baseline NEWOBJECT2 cache slots were not allocated")
	}
	entry := bf.NewTableCaches[newObjectPC]
	if len(entry.Values) == 0 || len(entry.Roots) == 0 {
		t.Fatalf("NEWOBJECT2 cache was not populated: values=%d roots=%d", len(entry.Values), len(entry.Roots))
	}
	if entry.Pos <= 0 {
		t.Fatalf("NEWOBJECT2 cache was populated but never consumed: pos=%d", entry.Pos)
	}
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

func TestTier1_GoFunctionFixedArgFastPath(t *testing.T) {
	src := `
func run(n) {
    sum := 0
    for i := 1; i <= n; i++ {
        sum = sum + fast1(i) + fast2(i, 3)
    }
    return sum
}
result := 0
for i := 1; i <= 200; i++ {
    result = run(4)
}
`
	proto := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)

	fast1Calls := 0
	fast2Calls := 0
	fast3Calls := 0
	v.SetGlobal("fast1", runtime.FunctionValue(&runtime.GoFunction{
		Name: "fast1",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			return []runtime.Value{runtime.IntValue(args[0].Int() + 10)}, nil
		},
		FastArg1: func(a runtime.Value) (runtime.Value, error) {
			fast1Calls++
			return runtime.IntValue(a.Int() + 10), nil
		},
	}))
	v.SetGlobal("fast2", runtime.FunctionValue(&runtime.GoFunction{
		Name: "fast2",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			return []runtime.Value{runtime.IntValue(args[0].Int() + args[1].Int())}, nil
		},
		FastArg2: func(a, b runtime.Value) (runtime.Value, error) {
			fast2Calls++
			return runtime.IntValue(a.Int() + b.Int()), nil
		},
	}))
	v.SetGlobal("fast3", runtime.FunctionValue(&runtime.GoFunction{
		Name: "fast3",
		Fn: func(args []runtime.Value) ([]runtime.Value, error) {
			return []runtime.Value{runtime.IntValue(args[0].Int() + args[1].Int() + args[2].Int())}, nil
		},
		FastArg3: func(a, b, c runtime.Value) (runtime.Value, error) {
			fast3Calls++
			return runtime.IntValue(a.Int() + b.Int() + c.Int()), nil
		},
	}))

	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT runtime error: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 72 {
		t.Fatalf("result = %v, want 72", result)
	}
	if fast1Calls == 0 || fast2Calls == 0 {
		t.Fatalf("fixed-arg fast paths were not used: fast1=%d fast2=%d fast3=%d", fast1Calls, fast2Calls, fast3Calls)
	}
	if _, err := v.Execute(compileTop(t, `result := fast3(1, 2, 3)`)); err != nil {
		t.Fatalf("JIT fast3 runtime error: %v", err)
	}
	if got := v.GetGlobal("result"); !got.IsInt() || got.Int() != 6 {
		t.Fatalf("fast3 result = %v, want 6", got)
	}
	if fast3Calls == 0 {
		t.Fatalf("FastArg3 path was not used")
	}
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

func TestTier1_NativeSetTableSequentialAppend(t *testing.T) {
	compareVMvsJIT(t, `
func f(n) {
    t := {}
    for i := 1; i <= n; i++ {
        t[i] = i * 3
    }
    return t[1] + t[n]
}
result := 0
for i := 1; i <= 200; i++ { result = f(64) }
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

func TestTier1_NativeDynamicStringCacheHitRecordsTableAccessFeedback(t *testing.T) {
	src := `
func f(t, k, v) {
    old := t[k]
    t[k] = v
    return old
}

t := {name: 1, age: 2}
key := "name"
result := 0
for i := 1; i <= 240; i++ {
    result = result + f(t, key, i)
}
`
	proto := compileTop(t, src)
	fProto := findProtoByName(proto, "f")
	if fProto == nil {
		t.Fatal("f proto not found")
	}
	fProto.EnsureFeedback()
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	engine := NewBaselineJITEngine()
	v.SetMethodJIT(engine)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("JIT runtime error: %v", err)
	}

	if engine.compiled[fProto] == nil {
		t.Fatal("f was not baseline compiled")
	}

	var checked int
	for pc, inst := range fProto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETTABLE, vm.OP_SETTABLE:
			fb := fProto.TableKeyFeedback[pc]
			key, shapeID, fieldIdx, ok := fb.StableStringShapeField()
			if !ok || key != "name" || shapeID == 0 || fieldIdx < 0 {
				t.Fatalf("pc=%d stable string shape field = key=%q shape=%d field=%d ok=%v feedback=%#v",
					pc, key, shapeID, fieldIdx, ok, fb)
			}
			if fb.Count <= 1 {
				t.Fatalf("pc=%d feedback count=%d, want native cache hits to keep recording beyond first miss; feedback=%#v", pc, fb.Count, fb)
			}
			checked++
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d dynamic table ops, want 2", checked)
	}
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

func TestTier1_NativeCall_MutableUpvalueCalleeKeepsDirectBLR(t *testing.T) {
	src := `
func make_counter() {
    count := 0
    func inc(delta) {
        count = count + delta
        return count
    }
    return inc
}
counter := make_counter()
result := 0
for i := 1; i <= 200; i++ {
    result = result + counter(1)
}
`
	vmGlobals := runVMFull(t, src)
	v, proto := runTier1ProgramForTest(t, src)
	defer v.Close()
	assertValueEq(t, "result", v.GetGlobal("result"), vmGlobals["result"])

	inc := findProtoByName(proto, "inc")
	if inc == nil {
		t.Fatal("inc proto not found")
	}
	if inc.CompiledCodePtr == 0 {
		t.Fatal("mutable upvalue callee was not compiled")
	}
	if inc.DirectEntryPtr == 0 {
		t.Fatal("mutable upvalue callee DirectEntryPtr is 0; direct BLR was disabled too broadly")
	}
}

func TestTier1_AccumulatorClosureFastPath_FloatFallback(t *testing.T) {
	compareVMvsJIT(t, `
func make_counter() {
    count := 0.5
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

func TestTier1_NativeCall_UpvalueSideEffectBeforeExitStillGated(t *testing.T) {
	src := `
func make_counter() {
    count := 0
    func inc() {
        count = count + 1
        tmp := {}
        return count
    }
    return inc
}
counter := make_counter()
for i := 1; i <= 200; i++ {
    result = counter()
}
`
	vmGlobals := runVMFull(t, src)
	v, proto := runTier1ProgramForTest(t, src)
	defer v.Close()
	assertValueEq(t, "result", v.GetGlobal("result"), vmGlobals["result"])

	inc := findProtoByName(proto, "inc")
	if inc == nil {
		t.Fatal("inc proto not found")
	}
	if inc.CompiledCodePtr == 0 {
		t.Fatal("upvalue callee was not compiled")
	}
	if inc.DirectEntryPtr != 0 {
		t.Fatalf("unsafe upvalue callee DirectEntryPtr=%#x, want 0", inc.DirectEntryPtr)
	}
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

func TestTier1_FieldCacheLazyRecursiveTableFallsBack(t *testing.T) {
	proto := compileByName(t, `func getLeft(node) { return node.left }`, "getLeft")
	pc := -1
	for i, inst := range proto.Code {
		if vm.DecodeOp(inst) == vm.OP_GETFIELD {
			pc = i
			break
		}
	}
	if pc < 0 {
		t.Fatal("GETFIELD not found")
	}
	ensureFieldCache(proto)
	ctor := runtime.NewSmallTableCtor2("left", "right")
	normal := runtime.NewTableFromCtor2NonNil(
		&ctor,
		runtime.FreshTableValue(runtime.NewEmptyTable()),
		runtime.FreshTableValue(runtime.NewEmptyTable()),
	)
	if got := normal.RawGetStringCached("left", &proto.FieldCache[pc]); !got.IsTable() {
		t.Fatalf("warm cache get = %v, want table", got)
	}

	bf, err := CompileBaseline(proto)
	if err != nil {
		t.Fatalf("CompileBaseline(getLeft): %v", err)
	}
	lazy := runtime.NewLazyRecursiveTable(&ctor, 1)
	regs := make([]runtime.Value, proto.MaxStack+1)
	regs[0] = runtime.FreshTableValue(lazy)
	engine := NewBaselineJITEngine()
	results, err := engine.Execute(bf, regs, 0, proto)
	if err != nil {
		t.Fatalf("Execute(getLeft): %v", err)
	}
	if len(results) != 1 || !results[0].IsTable() {
		t.Fatalf("getLeft(lazy) = %v, want lazy child table", results)
	}
	if got2 := lazy.RawGetString("left"); !got2.IsTable() || got2.Table() != results[0].Table() {
		t.Fatalf("lazy child identity mismatch: result=%v later=%v", results[0], got2)
	}
}

func TestTier1_DynamicStringKeyTableCache(t *testing.T) {
	compareVMvsJIT(t, `
func f(n) {
    keys := {"na", "eu", "apac", "latam", "mea"}
    totals := {}
    sum := 0
    for i := 1; i <= n; i++ {
        k := keys[(i * 3) % #keys + 1]
        cur := totals[k]
        if cur == nil {
            cur = 0
        }
        cur = cur + i
        totals[k] = cur
        sum = sum + totals[k]
    }
    return sum + totals["na"] + totals["mea"]
}
result := 0
for i := 1; i <= 80; i++ { result = f(40) }
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
// Self-call direct branch optimization
// ---------------------------------------------------------------------------

func TestTier1_SelfCallDirect_Fib(t *testing.T) {
	// Fibonacci is the canonical self-recursive function. The self-call
	// optimization skips DirectEntryPtr load, bounds check, and CallCount
	// increment, using BL direct_entry instead of BLR X2.
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(15)
`, "result")
}

func TestTier1_SelfCallDirect_Factorial(t *testing.T) {
	// Single-branch recursion (only one self-call per invocation).
	compareVMvsJIT(t, `
func fact(n) {
    if n <= 1 { return 1 }
    return n * fact(n-1)
}
result := fact(12)
`, "result")
}

func TestTier1_SelfCallDirect_DeepRecursion(t *testing.T) {
	// Deeper recursion to stress the native call stack. NativeCallDepth
	// limit (48) should not be hit for depth=40.
	compareVMvsJIT(t, `
func sum_to(n) {
    if n <= 0 { return 0 }
    return n + sum_to(n-1)
}
result := sum_to(40)
`, "result")
}

func TestTier1_SelfCallDirect_MutualNotSelf(t *testing.T) {
	// Mutual recursion is NOT a self-call. This test verifies that the
	// normal (non-self) path still works correctly when self-call
	// detection is active.
	compareVMvsJIT(t, `
func is_even(n) {
    if n == 0 { return true }
    return is_odd(n - 1)
}
func is_odd(n) {
    if n == 0 { return false }
    return is_even(n - 1)
}
result := is_even(20)
`, "result")
}

func TestTier1_SelfCallDirect_MixedCalls(t *testing.T) {
	// A function that calls both itself (self-call) and another function
	// (normal call) in the same body.
	compareVMvsJIT(t, `
func double(x) { return x * 2 }
func f(n) {
    if n <= 0 { return 0 }
    return double(n) + f(n-1)
}
result := f(10)
`, "result")
}

func TestTier1_SelfTailNoReturn_QuicksortDescending(t *testing.T) {
	// The second quicksort call is a no-return self tail call. Descending input
	// makes that right-side tail path hot while still exercising table swaps.
	compareVMvsJIT(t, `
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

N := 200
arr := {}
for i := 1; i <= N; i++ {
    arr[i] = N + 1 - i
}
quicksort(arr, 1, N)
result := true
for i := 1; i < N; i++ {
    if arr[i] > arr[i + 1] { result = false }
}
`, "result")
}

// ---------------------------------------------------------------------------
// Self-call lightweight save/restore (register pass optimization)
// ---------------------------------------------------------------------------

func TestSelfCallRegisterPass(t *testing.T) {
	// Verify fib(20) correctness with the lightweight self-call path.
	// This exercises the optimized save/restore/setup that skips
	// Constants, ClosurePtr, GlobalCache, and GlobalCachedGen for
	// self-recursive calls.
	compareVMvsJIT(t, `
func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(20)
`, "result")
}

func TestSelfCallRegisterPass_DeepFactorial(t *testing.T) {
	// Single-branch deep recursion to stress the lightweight frame.
	compareVMvsJIT(t, `
func fact(n) {
    if n <= 1 { return 1 }
    return n * fact(n-1)
}
result := fact(15)
`, "result")
}

func TestSelfCallRegisterPass_MixedSelfAndNonSelf(t *testing.T) {
	// A function that makes both self-calls (lightweight path) and
	// non-self calls (normal path) in the same body. Verifies that
	// the X20 flag correctly distinguishes the two cases.
	compareVMvsJIT(t, `
func helper(x) { return x + 1 }
func f(n) {
    if n <= 0 { return 0 }
    return helper(n) + f(n-1)
}
result := f(20)
`, "result")
}

func TestSelfCallRegisterPass_WithGlobals(t *testing.T) {
	// Self-recursive function that also accesses globals.
	// Verifies GlobalCache/CachedGen preservation is correct.
	compareVMvsJIT(t, `
counter := 0
func fib(n) {
    counter = counter + 1
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
result := fib(15)
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

func TestBaselineFeedback_GetTable_MixedTableResult(t *testing.T) {
	src := `
func f() {
    rows := {}
    row := {}
    row[0] = 1.0
    rows[0] = row
    return rows[0]
}
result := nil
for i := 1; i <= 5; i++ { result = f() }
`
	_, proto := compileAndRunForFeedback(t, src)
	foundTable := false
	for pc, inst := range proto.Code {
		if vm.DecodeOp(inst) == vm.OP_GETTABLE && proto.Feedback != nil && pc < len(proto.Feedback) {
			fb := proto.Feedback[pc]
			if fb.Result == vm.FBTable && fb.Kind == vm.FBKindMixed {
				foundTable = true
			}
		}
	}
	if !foundTable {
		t.Errorf("expected mixed GETTABLE to record FBTable result feedback")
	}
}

func TestBaselineFeedback_GetField_Float(t *testing.T) {
	src := `
func f() {
    t := {x: 1.5, y: 2.5, z: 3.5}
    return t.x + t.y + t.z
}
result := 0.0
for i := 1; i <= 5; i++ { result = f() }
`
	_, proto := compileAndRunForFeedback(t, src)
	foundFloat := false
	for pc, inst := range proto.Code {
		op := vm.DecodeOp(inst)
		if op == vm.OP_GETFIELD && proto.Feedback != nil && pc < len(proto.Feedback) {
			fb := proto.Feedback[pc]
			if fb.Result == vm.FBFloat {
				foundFloat = true
			}
		}
	}
	if !foundFloat {
		t.Errorf("expected at least one GETFIELD with FBFloat feedback, got none")
	}
}
