//go:build darwin && arm64

package methodjit

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTier2_FieldShapeFactWarmLoadAvoidsTableExit(t *testing.T) {
	src := `
func sum_x(t, n) {
    s := 0.0
    for i := 1; i <= n; i++ {
        s = s + t.x
    }
    return s
}
`
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(3))

	got, tm, proto := runFieldShapeTier2(t, src, "sum_x", []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(8)}, []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(1)})
	requireFloatValue(t, got, 24)
	if proto.FieldCache == nil {
		t.Fatal("warm call did not allocate FieldCache")
	}
	if exits := getFieldTableExits(tm); exits != 0 {
		t.Fatalf("warm field shape load should stay on guarded native path, GetField exits=%d", exits)
	}
}

func TestTier2_FieldShapeColdNumericLoadUsesDynamicCacheAfterFirstExit(t *testing.T) {
	src := `
func sum_x(t, n) {
    s := 0.0
    for i := 1; i <= n; i++ {
        s = s + t.x
    }
    return s
}
`
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(4))

	got, tm, _ := runFieldShapeTier2(t, src, "sum_x", []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(8)}, nil)
	requireFloatValue(t, got, 32)
	if exits := getFieldTableExits(tm); exits > 1 {
		t.Fatalf("cold fused field load should populate FieldCache once then use native dynamic path, GetField exits=%d", exits)
	}
}

func TestTier2_FieldShapeMismatchFallsBackOnceThenUsesDynamicCache(t *testing.T) {
	src := `
func sum_x(t, n) {
    s := 0.0
    for i := 1; i <= n; i++ {
        s = s + t.x
    }
    return s
}
`
	warm := runtime.NewTable()
	warm.RawSetString("x", runtime.IntValue(1))

	mismatch := runtime.NewTable()
	mismatch.RawSetString("y", runtime.IntValue(99))
	mismatch.RawSetString("x", runtime.IntValue(5))

	got, tm, proto := runFieldShapeTier2(t, src, "sum_x", []runtime.Value{runtime.TableValue(mismatch), runtime.IntValue(8)}, []runtime.Value{runtime.TableValue(warm), runtime.IntValue(1)})
	requireFloatValue(t, got, 40)
	if exits := getFieldTableExits(tm); exits > 1 {
		t.Fatalf("shape mismatch should take precise fallback once, then native dynamic cache; GetField exits=%d", exits)
	}
	if exits := tm.ExitStats().ByExitCode["ExitDeopt"]; exits != 0 {
		t.Fatalf("shape mismatch field fallback should not deopt, ExitDeopt=%d", exits)
	}
	if proto.FieldCache == nil {
		t.Fatal("FieldCache missing after mismatch fallback")
	}
}

func TestTier2_FieldDynamicCacheBoundsMissFallsBack(t *testing.T) {
	src := `func get_x(t) { return t.x }`
	top := compileTop(t, src)
	proto := findProtoByName(top, "get_x")
	if proto == nil {
		t.Fatal("proto get_x not found")
	}
	fn := BuildGraph(proto)
	getField := findInstrByOp(fn, OpGetField)
	if getField == nil {
		t.Fatalf("OpGetField not found in IR:\n%s", Print(fn))
	}
	if getField.SourcePC < 0 {
		t.Fatalf("OpGetField has no SourcePC: %+v", getField)
	}

	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(123))
	ensureFieldCache(proto)
	proto.FieldCache[getField.SourcePC] = runtime.FieldCacheEntry{
		FieldIdx: 99,
		ShapeID:  tbl.ShapeID(),
	}

	got, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)})
	if err != nil {
		t.Fatalf("Execute with stale FieldCache: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 123 {
		t.Fatalf("result with stale FieldCache = %v, want 123", got)
	}
}

func runFieldShapeTier2(t *testing.T, src, fnName string, args, warmArgs []runtime.Value) ([]runtime.Value, *TieringManager, *vm.FuncProto) {
	t.Helper()
	top := compileTop(t, src)
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	proto := findProtoByName(top, fnName)
	if proto == nil {
		t.Fatalf("proto %q not found", fnName)
	}
	proto.EnsureFeedback()
	fn := v.GetGlobal(fnName)
	if warmArgs != nil {
		if _, err := v.CallValue(fn, warmArgs); err != nil {
			t.Fatalf("warm CallValue(%s): %v", fnName, err)
		}
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(%s): %v", fnName, err)
	}
	results, err := v.CallValue(fn, args)
	if err != nil {
		t.Fatalf("Tier2 CallValue(%s): %v", fnName, err)
	}
	if proto.EnteredTier2 == 0 {
		t.Fatalf("%s did not enter Tier2", fnName)
	}
	return results, tm, proto
}

func requireFloatValue(t *testing.T, values []runtime.Value, want float64) {
	t.Helper()
	if len(values) != 1 || !values[0].IsFloat() || math.Abs(values[0].Float()-want) > 1e-9 {
		t.Fatalf("result=%v, want float %.6f", values, want)
	}
}

func getFieldTableExits(tm *TieringManager) uint64 {
	if tm == nil {
		return 0
	}
	var exits uint64
	for _, site := range tm.ExitStats().Sites {
		if site.ExitName == "ExitTableExit" && site.Reason == "GetField" {
			exits += site.Count
		}
	}
	return exits
}

func findInstrByOp(fn *Function, op Op) *Instr {
	if fn == nil {
		return nil
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr != nil && instr.Op == op {
				return instr
			}
		}
	}
	return nil
}
