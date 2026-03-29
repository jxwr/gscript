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
