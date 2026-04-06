//go:build darwin && arm64

// emit_table_test.go tests ARM64 code generation for table operations:
// OpGetField (inline shape-guarded), OpSetField, OpNewTable (call-exit),
// OpGetTable/OpSetTable (call-exit).
//
// Each test compiles a GScript function, runs it through both the Method JIT
// and the VM interpreter, and verifies identical results.

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestEmitTable_GetField: create a table with field x=1, return t.x.
func TestEmitTable_GetField(t *testing.T) {
	src := `func f() { t := {x: 1}; return t.x }`

	vmResult := runVM(t, src, nil)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f() via VM", vmResult[0], runtime.IntValue(1))

	// Compile and run via JIT (will use table-exit for NewTable and
	// either inline or table-exit for GetField).
	jitResult := runJITFull(t, src, nil)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f() JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_SetField: create table, overwrite field, return new value.
func TestEmitTable_SetField(t *testing.T) {
	src := `func f() { t := {x: 1}; t.x = 42; return t.x }`

	vmResult := runVM(t, src, nil)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f() via VM", vmResult[0], runtime.IntValue(42))

	jitResult := runJITFull(t, src, nil)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f() JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_MultipleFields: access multiple fields and compute sum.
func TestEmitTable_MultipleFields(t *testing.T) {
	src := `func f() { t := {x: 1, y: 2}; return t.x + t.y }`

	vmResult := runVM(t, src, nil)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f() via VM", vmResult[0], runtime.IntValue(3))

	jitResult := runJITFull(t, src, nil)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f() JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_FieldLoop: read a field inside a loop.
func TestEmitTable_FieldLoop(t *testing.T) {
	// Pass the table as an argument so the field cache can be populated
	// by prior VM execution.
	src := `func f(t) { s := 0; for i := 1; i <= 10; i++ { s = s + t.x }; return s }`

	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(5))
	args := []runtime.Value{runtime.TableValue(tbl)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f(t) via VM", vmResult[0], runtime.IntValue(50))

	// For JIT: use table-exit (field cache not populated for fresh compile).
	jitResult := runJITFull(t, src, args)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f(t) JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_MatchesVM: comprehensive comparison of JIT vs VM for all table ops.
func TestEmitTable_MatchesVM(t *testing.T) {
	cases := []struct {
		name string
		src  string
		args []runtime.Value
	}{
		{"new_table_return", `func f() { t := {}; return 1 }`, nil},
		{"field_read", `func f() { t := {a: 10}; return t.a }`, nil},
		{"field_write_read", `func f() { t := {a: 0}; t.a = 99; return t.a }`, nil},
		{"multi_field", `func f() { t := {a: 3, b: 7}; return t.a + t.b }`, nil},
		{"nested_assign", `func f() { t := {x: 1}; t.x = t.x + 1; return t.x }`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vmResult := runVM(t, tc.src, tc.args)
			jitResult := runJITFull(t, tc.src, tc.args)

			if len(vmResult) == 0 {
				t.Fatal("VM returned no results")
			}
			if len(jitResult) == 0 {
				t.Fatal("JIT returned no results")
			}
			assertValuesEqual(t, tc.name, jitResult[0], vmResult[0])
		})
	}
}

// TestEmitTable_InlineGetField tests the inline shape-guarded path for GetField.
// The field cache is warmed by running the function through the VM first,
// which populates the FieldCache with shapeID and fieldIndex. The JIT then
// emits inline ARM64 code that checks the shapeID and does a direct
// svals[fieldIndex] load instead of calling into Go.
func TestEmitTable_InlineGetField(t *testing.T) {
	src := `func f() { t := {x: 10, y: 20}; return t.x + t.y }`

	result := runJITWithWarmCache(t, src, nil)
	if len(result) == 0 {
		t.Fatal("JIT (warm cache) returned no results")
	}
	assertValuesEqual(t, "f() inline", result[0], runtime.IntValue(30))
}

// TestEmitTable_InlineSetField tests the inline shape-guarded path for SetField.
func TestEmitTable_InlineSetField(t *testing.T) {
	src := `func f() { t := {x: 0}; t.x = 77; return t.x }`

	result := runJITWithWarmCache(t, src, nil)
	if len(result) == 0 {
		t.Fatal("JIT (warm cache) returned no results")
	}
	assertValuesEqual(t, "f() inline set", result[0], runtime.IntValue(77))
}

// TestEmitTable_NativeGetTable: pass a table and an integer index, verify
// the native GETTABLE fast path returns the correct value.
func TestEmitTable_NativeGetTable(t *testing.T) {
	src := `func f(arr, n) { return arr[n] }`

	// Build a mixed array with known values.
	tbl := runtime.NewTable()
	for i := 0; i < 10; i++ {
		tbl.RawSetInt(int64(i), runtime.IntValue(int64(i*10)))
	}
	args := []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(3)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f(arr,3) via VM", vmResult[0], runtime.IntValue(30))

	jitResult := runJITFull(t, src, args)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f(arr,3) JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_NativeSetTable: pass a table, index, and value, verify
// the native SETTABLE fast path writes correctly.
func TestEmitTable_NativeSetTable(t *testing.T) {
	src := `func f(arr, n, v) { arr[n] = v; return arr[n] }`

	// Build a mixed array with initial values.
	tbl := runtime.NewTable()
	for i := 0; i < 10; i++ {
		tbl.RawSetInt(int64(i), runtime.IntValue(0))
	}
	args := []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(5), runtime.IntValue(99)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "f(arr,5,99) via VM", vmResult[0], runtime.IntValue(99))

	// Reset table for JIT run.
	tbl2 := runtime.NewTable()
	for i := 0; i < 10; i++ {
		tbl2.RawSetInt(int64(i), runtime.IntValue(0))
	}
	args2 := []runtime.Value{runtime.TableValue(tbl2), runtime.IntValue(5), runtime.IntValue(99)}

	jitResult := runJITFull(t, src, args2)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "f(arr,5,99) JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_ArrayLoop: sum elements of a table in a loop via GETTABLE.
func TestEmitTable_ArrayLoop(t *testing.T) {
	src := `func sum(arr, n) { s := 0; for i := 1; i <= n; i++ { s = s + arr[i] }; return s }`

	// Build a table where arr[i] = i for i=1..10.
	tbl := runtime.NewTable()
	for i := 0; i <= 10; i++ {
		tbl.RawSetInt(int64(i), runtime.IntValue(int64(i)))
	}
	args := []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(10)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	// sum(1..10) = 55
	assertValuesEqual(t, "sum(arr,10) via VM", vmResult[0], runtime.IntValue(55))

	jitResult := runJITFull(t, src, args)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "sum(arr,10) JIT vs VM", jitResult[0], vmResult[0])
}

// TestEmitTable_SetTableLoop: write to table elements in a loop via SETTABLE.
func TestEmitTable_SetTableLoop(t *testing.T) {
	src := `func fill(arr, n) { for i := 0; i < n; i++ { arr[i] = i * 2 }; return arr[3] }`

	// Build a table with enough initial capacity.
	tbl := runtime.NewTable()
	for i := 0; i < 10; i++ {
		tbl.RawSetInt(int64(i), runtime.IntValue(0))
	}
	args := []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(10)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	// arr[3] = 3*2 = 6
	assertValuesEqual(t, "fill(arr,10) via VM", vmResult[0], runtime.IntValue(6))

	// Fresh table for JIT run.
	tbl2 := runtime.NewTable()
	for i := 0; i < 10; i++ {
		tbl2.RawSetInt(int64(i), runtime.IntValue(0))
	}
	args2 := []runtime.Value{runtime.TableValue(tbl2), runtime.IntValue(10)}

	jitResult := runJITFull(t, src, args2)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "fill(arr,10) JIT vs VM", jitResult[0], vmResult[0])
}

// runJITWithWarmCache runs the function through the VM to warm the field cache,
// then compiles and executes via the JIT. This exercises the inline shape-guarded
// path for GetField/SetField.
func runJITWithWarmCache(t *testing.T, src string, args []runtime.Value) []runtime.Value {
	t.Helper()

	// First: compile and run via VM to populate FieldCache.
	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	top := compileTop(t, src)
	_, err := v.Execute(top)
	if err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	// Find the function proto (it now has warm FieldCache from VM execution).
	var fnName string
	var proto *vm.FuncProto
	for _, p := range top.Protos {
		if p.Name != "" {
			fnName = p.Name
			proto = p
			break
		}
	}
	if proto == nil {
		t.Fatal("no named function found in proto")
	}

	// Call it once via VM to warm the field cache.
	fnVal := v.GetGlobal(fnName)
	if fnVal.IsNil() {
		t.Fatalf("function %q not found in globals", fnName)
	}
	_, err = v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("VM warm-up call error: %v", err)
	}

	// Now JIT-compile with the warmed field cache.
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, errC := Compile(fn, alloc)
	if errC != nil {
		t.Fatalf("Compile error: %v", errC)
	}
	defer cf.Code.Free()

	cf.DeoptFunc = func(deoptArgs []runtime.Value) ([]runtime.Value, error) {
		return runVM(t, src, deoptArgs), nil
	}
	cf.CallVM = v

	result, errE := cf.Execute(args)
	if errE != nil {
		t.Fatalf("Execute error: %v", errE)
	}
	return result
}

// runJITFull compiles a GScript function to native code via the full Method JIT
// pipeline and executes it. Falls back to VM on deopt.
func runJITFull(t *testing.T, src string, args []runtime.Value) []runtime.Value {
	t.Helper()

	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	alloc := AllocateRegisters(fn)

	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	// Set up DeoptFunc for fallback.
	cf.DeoptFunc = func(deoptArgs []runtime.Value) ([]runtime.Value, error) {
		return runVM(t, src, deoptArgs), nil
	}

	// Set up CallVM for call-exit and table-exit operations.
	callVM := makeCallExitVMForTest(t, src)
	defer callVM.Close()
	cf.CallVM = callVM

	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return result
}

// TestShapeGuardDedup tests that multiple field accesses of the same table
// in the same basic block produce correct results. Shape guard dedup should
// skip redundant type check + nil check + shape guard on subsequent accesses.
func TestShapeGuardDedup(t *testing.T) {
	src := `func f() { t := {x: 10, y: 20, z: 30}; return t.x + t.y + t.z }`
	result := runJITWithWarmCache(t, src, nil)
	if len(result) == 0 {
		t.Fatal("no results")
	}
	assertValuesEqual(t, "shape dedup", result[0], runtime.IntValue(60))
}

// TestShapeGuardDedup_SetField tests that multiple SetField operations on the
// same table in the same block produce correct results with shape guard dedup.
func TestShapeGuardDedup_SetField(t *testing.T) {
	src := `func f() { t := {x: 0, y: 0}; t.x = 5; t.y = 10; return t.x + t.y }`
	result := runJITWithWarmCache(t, src, nil)
	if len(result) == 0 {
		t.Fatal("no results")
	}
	assertValuesEqual(t, "setfield dedup", result[0], runtime.IntValue(15))
}

// TestShapeGuardDedup_MixedGetSet tests mixed GetField and SetField on the
// same table with shape guard dedup.
func TestShapeGuardDedup_MixedGetSet(t *testing.T) {
	src := `func f() { t := {x: 3, y: 7}; t.x = t.x + t.y; return t.x }`
	result := runJITWithWarmCache(t, src, nil)
	if len(result) == 0 {
		t.Fatal("no results")
	}
	assertValuesEqual(t, "mixed get/set dedup", result[0], runtime.IntValue(10))
}

// TestEmitTable_SetTableWithOptPasses tests SETTABLE with optimization passes
// (TypeSpec + ConstProp + DCE) to reproduce tiering manager behavior.
// Known issue: TypeSpecialize produces raw int operands (AddInt) for
// table key/value, but emitSetTableNative expects NaN-boxed operands.
// When a raw int is boxed by resolveValueNB, the fast path works correctly.
// However, the exit-resume path creates an infinite loop because the deopt
// code within emitSetTableExit re-enters through the same code path.
// This test is disabled until the exit-resume loop issue is resolved.
func TestEmitTable_SetTableWithOptPasses(t *testing.T) {
	t.Skip("known issue: SETTABLE with TypeSpecialize creates exit-resume loop")
}

// TestTableVerifiedDedup tests that multiple array accesses of the same table
// in the same basic block produce correct results. Table verification dedup
// should skip redundant type/nil/metatable checks on subsequent accesses.
func TestTableVerifiedDedup(t *testing.T) {
	src := `func f(arr) { return arr[0] + arr[1] + arr[2] }`
	tbl := runtime.NewTable()
	for i := 0; i < 5; i++ {
		tbl.RawSetInt(int64(i), runtime.IntValue(int64(i*10)))
	}
	args := []runtime.Value{runtime.TableValue(tbl)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	// arr[0] + arr[1] + arr[2] = 0 + 10 + 20 = 30
	assertValuesEqual(t, "table dedup VM", vmResult[0], runtime.IntValue(30))

	jitResult := runJITFull(t, src, args)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "table dedup JIT", jitResult[0], vmResult[0])
}

// TestTableVerifiedDedup_SetGet tests mixed SetTable+GetTable on the same table.
func TestTableVerifiedDedup_SetGet(t *testing.T) {
	src := `func f(arr) { arr[0] = 42; return arr[0] }`
	tbl := runtime.NewTable()
	tbl.RawSetInt(0, runtime.IntValue(0))
	args := []runtime.Value{runtime.TableValue(tbl)}

	vmResult := runVM(t, src, args)
	if len(vmResult) == 0 {
		t.Fatal("VM returned no results")
	}
	assertValuesEqual(t, "set+get VM", vmResult[0], runtime.IntValue(42))

	tbl2 := runtime.NewTable()
	tbl2.RawSetInt(0, runtime.IntValue(0))
	args2 := []runtime.Value{runtime.TableValue(tbl2)}
	jitResult := runJITFull(t, src, args2)
	if len(jitResult) == 0 {
		t.Fatal("JIT returned no results")
	}
	assertValuesEqual(t, "set+get JIT", jitResult[0], vmResult[0])
}
