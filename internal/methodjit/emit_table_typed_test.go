//go:build darwin && arm64

// emit_table_typed_test.go tests Tier 2 correctness for typed array
// (bool, float) GetTable/SetTable operations. These establish a correctness
// baseline — the current JIT handles these via exit-resume fallback.
// Native ARM64 paths added later must preserve these results.

package methodjit

import (
	"encoding/binary"
	"strings"
	"testing"

	"golang.org/x/arch/arm64/arm64asm"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTier2_GetTableArrayBool(t *testing.T) {
	src := `
func count_true(n) {
    arr := {true, false, true, true, false}
    count := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 5; j++ {
            if arr[j] {
                count = count + 1
            }
        }
    }
    return count
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = count_true(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_SetTableArrayBool(t *testing.T) {
	src := `
func toggle_bools(n) {
    arr := {true, false, true}
    for i := 1; i <= n; i++ {
        for j := 1; j <= 3; j++ {
            if arr[j] {
                arr[j] = false
            } else {
                arr[j] = true
            }
        }
    }
    count := 0
    for j := 1; j <= 3; j++ {
        if arr[j] { count = count + 1 }
    }
    return count
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = toggle_bools(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_GetTableArrayFloat(t *testing.T) {
	src := `
func sum_floats(n) {
    arr := {1.5, 2.5, 3.5, 4.5}
    total := 0.0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 4; j++ {
            total = total + arr[j]
        }
    }
    return total
}
result := 0.0
for iter := 1; iter <= 5; iter++ {
    result = sum_floats(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_TableArrayNestedFloatLoad(t *testing.T) {
	src := `
func nested_sum(rows, n) {
    total := 0.0
    for i := 0; i < n; i++ {
        total = total + rows[i][1]
    }
    return total
}

rows := {}
for i := 0; i < 8; i++ {
    row := {}
    row[0] = i * 1.0
    row[1] = i * 2.0 + 0.5
    rows[i] = row
}

result := 0.0
for iter := 0; iter < 40; iter++ {
    result = nested_sum(rows, 8)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_DenseMatrixRowAppendFastPathReducesSetTableExits(t *testing.T) {
	src := `
func build(n) {
    m := {}
    for i := 0; i < n; i++ {
        row := {}
        for j := 0; j < 16; j++ {
            row[j] = i * 100.0 + j
        }
        m[i] = row
    }
    return m[n - 1][3]
}
`
	top := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fnVal := v.GetGlobal("build")
	if fnVal.IsNil() {
		t.Fatal("build function not found")
	}
	if _, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(20)}); err != nil {
		t.Fatalf("warm build: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	fnProto := findProtoByName(top, "build")
	if fnProto == nil {
		t.Fatal("build proto not found")
	}
	if err := tm.CompileTier2(fnProto); err != nil {
		t.Fatalf("CompileTier2(build): %v", err)
	}
	got, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(20)})
	if err != nil {
		t.Fatalf("Tier2 build: %v", err)
	}
	if len(got) != 1 || !got[0].IsFloat() || got[0].Float() != 1903 {
		t.Fatalf("build result = %v, want float 1903", got)
	}

	var setTableExits uint64
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "build" && site.ExitName == "ExitTableExit" && site.Reason == "SetTable" {
			setTableExits += site.Count
		}
	}
	if setTableExits >= 20 {
		t.Fatalf("dense row appends should not exit per row, SetTable exits=%d sites=%#v", setTableExits, tm.ExitStats().Sites)
	}
}

// TestKindSpecialize_IntArray tests kind-specialized GetTable on ArrayInt.
func TestKindSpecialize_IntArray(t *testing.T) {
	src := `
func sum_ints(n) {
    arr := {10, 20, 30, 40, 50}
    total := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 5; j++ {
            total = total + arr[j]
        }
    }
    return total
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = sum_ints(10)
}
`
	compareTier2Result(t, src, "result")
}

// TestKindSpecialize_Sieve tests kind-specialized GetTable/SetTable on ArrayBool.
func TestKindSpecialize_Sieve(t *testing.T) {
	src := `
func sieve(n) {
    is_prime := {}
    for i := 0; i <= n; i++ {
        is_prime[i] = true
    }
    count := 0
    for i := 2; i <= n; i++ {
        if is_prime[i] {
            count = count + 1
            for j := i + i; j <= n; j = j + i {
                is_prime[j] = false
            }
        }
    }
    return count
}
result := 0
for iter := 1; iter <= 5; iter++ {
    result = sieve(1000)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_SetTableArrayFloat(t *testing.T) {
	src := `
func scale_floats(n) {
    arr := {1.0, 2.0, 3.0}
    for i := 1; i <= n; i++ {
        for j := 1; j <= 3; j++ {
            arr[j] = arr[j] * 1.1
        }
    }
    sum := 0.0
    for j := 1; j <= 3; j++ {
        sum = sum + arr[j]
    }
    return sum
}
result := 0.0
for iter := 1; iter <= 5; iter++ {
    result = scale_floats(10)
}
`
	compareTier2Result(t, src, "result")
}

func TestTier2_TableArrayLoadFloatUsesDirectFPLoad(t *testing.T) {
	src := `
func sum_floats(arr, n) {
    total := 0.0
    for i := 0; i < n; i++ {
        total = total + arr[i]
    }
    return total
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "sum_floats")
	if proto == nil {
		t.Fatal("sum_floats proto not found")
	}
	seedFloatTableFeedback(proto)

	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(sum_floats): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableArrayLoad") {
		t.Fatalf("expected typed table-array lowering:\n%s", art.IRAfter)
	}

	foundFloatLoad := false
	for _, entry := range art.SourceMap {
		if entry.IROp != "TableArrayLoad" || entry.IRType != "float" || entry.CodeStart < 0 || entry.CodeEnd <= entry.CodeStart {
			continue
		}
		if rangeHasDirectFPLoad(art.CompiledCode, entry.CodeStart, entry.CodeEnd) {
			foundFloatLoad = true
			break
		}
	}
	if !foundFloatLoad {
		t.Fatalf("float TableArrayLoad did not emit direct FP register-offset load")
	}
}

func TestTier2_TableArrayNestedLoadFloatUsesDirectFPLoad(t *testing.T) {
	src := `
func nested_sum(rows, n) {
    total := 0.0
    for i := 0; i < n; i++ {
        total = total + rows[i][1]
    }
    return total
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "nested_sum")
	if proto == nil {
		t.Fatal("nested_sum proto not found")
	}
	seedNestedTableFloatFeedback(proto)

	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(nested_sum): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableNestedLoad") {
		t.Fatalf("expected nested typed table-array lowering:\n%s", art.IRAfter)
	}

	foundFloatLoad := false
	for _, entry := range art.SourceMap {
		if entry.IROp != "TableNestedLoad" || entry.IRType != "float" || entry.CodeStart < 0 || entry.CodeEnd <= entry.CodeStart {
			continue
		}
		if rangeHasDirectFPLoad(art.CompiledCode, entry.CodeStart, entry.CodeEnd) {
			foundFloatLoad = true
			break
		}
	}
	if !foundFloatLoad {
		t.Fatalf("float TableNestedLoad did not emit direct FP register-offset load")
	}
}

func TestTier2_TableArrayHeaderKnownTableSkipsNilCheck(t *testing.T) {
	src := `
func read_row(rows, i, j) {
    row := rows[i]
    return row[j] + row[j + 1]
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "read_row")
	if proto == nil {
		t.Fatal("read_row proto not found")
	}
	seedNestedTableFloatFeedback(proto)

	fn := BuildGraph(proto)
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(read_row): %v", err)
	}

	var headerID int
	foundHeader := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpTableArrayHeader || len(instr.Args) < 1 || instr.Args[0] == nil || instr.Args[0].Def == nil {
				continue
			}
			load := instr.Args[0].Def
			if load.Op == OpTableArrayLoad && load.Type == TypeTable {
				headerID = instr.ID
				foundHeader = true
				break
			}
		}
		if foundHeader {
			break
		}
	}
	if !foundHeader {
		t.Fatalf("expected nested TableArrayHeader fed by TableArrayLoad : table:\n%s", Print(fn))
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile(read_row): %v", err)
	}
	defer cf.Code.Free()

	foundRange := false
	for _, r := range cf.InstrCodeRanges {
		if r.InstrID == headerID && r.Pass == "normal" && r.CodeEnd > r.CodeStart {
			foundRange = true
			break
		}
	}
	if !foundRange {
		t.Fatalf("compiled header v%d has no normal code range", headerID)
	}
	if got := countMatchingIRInstr(cf, headerID, isARM64CBZ); got != 0 {
		t.Fatalf("known-table TableArrayHeader emitted %d CBZ nil check(s), want 0", got)
	}
}

func TestTier2_SetTableArrayMixedAppend(t *testing.T) {
	src := `
func mixed_append(n) {
    arr := {"seed"}
    x := 42
    for i := 2; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr[n]
}
mixed_append(50)
result := mixed_append(50)
`
	compareTier2Result(t, src, "result")
}

func TestTier2_TypedArrayAppendKeepsPairsDirty(t *testing.T) {
	src := `
func mutate(arr, n, k) {
    for i := 1; i <= n; i++ {
        arr[1] = i
    }
    arr[k] = k
    return arr[k]
}

arr := {}
for i := 1; i <= 20; i++ { arr[i] = i }

pre := 0
for k, v := range pairs(arr) { pre = pre + 1 }

for r := 1; r <= 5; r++ { mutate(arr, 100, 20) }

mid := 0
for k, v := range pairs(arr) { mid = mid + 1 }

mutate(arr, 100, 21)

post := 0
for k, v := range pairs(arr) { post = post + 1 }

result := pre * 10000 + mid * 100 + post
`
	compareTier2Result(t, src, "result")
}

func TestTier2_SetTableArrayIntSparseWithinCapacityStaysNative(t *testing.T) {
	src := `
func fill(n) {
    arr := {}
    for i := 1; i <= n; i++ {
        arr[i] = i
    }
    return arr[1] + arr[n]
}
`
	top := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fnVal := v.GetGlobal("fill")
	if fnVal.IsNil() {
		t.Fatal("fill function not found")
	}
	if _, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(16)}); err != nil {
		t.Fatalf("warm fill: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	fnProto := findProtoByName(top, "fill")
	if fnProto == nil {
		t.Fatal("fill proto not found")
	}
	if err := tm.CompileTier2(fnProto); err != nil {
		t.Fatalf("CompileTier2(fill): %v", err)
	}
	got, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(16)})
	if err != nil {
		t.Fatalf("Tier2 fill: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 17 {
		t.Fatalf("fill result = %v, want 17", got)
	}
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "fill" && site.ExitName == "ExitTableExit" && site.Reason == "SetTable" {
			t.Fatalf("capacity-present typed sparse store should stay native, saw exit site %#v", site)
		}
	}
}

func TestTier2_SetTableArrayBoolNilSparseFallsBack(t *testing.T) {
	src := `
func clear_sparse(v) {
    arr := {}
    for i := 1; i <= 16; i++ {
        arr[i] = true
    }
    arr[20] = v
    return arr[1]
}
`
	top := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fnVal := v.GetGlobal("clear_sparse")
	if fnVal.IsNil() {
		t.Fatal("clear_sparse function not found")
	}
	if _, err := v.CallValue(fnVal, []runtime.Value{runtime.NilValue()}); err != nil {
		t.Fatalf("warm clear_sparse: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	fnProto := findProtoByName(top, "clear_sparse")
	if fnProto == nil {
		t.Fatal("clear_sparse proto not found")
	}
	if err := tm.CompileTier2(fnProto); err != nil {
		t.Fatalf("CompileTier2(clear_sparse): %v", err)
	}
	got, err := v.CallValue(fnVal, []runtime.Value{runtime.NilValue()})
	if err != nil {
		t.Fatalf("Tier2 clear_sparse: %v", err)
	}
	if len(got) != 1 || !got[0].IsBool() || !got[0].Bool() {
		t.Fatalf("clear_sparse result = %v, want true", got)
	}
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "clear_sparse" && site.ExitName == "ExitTableExit" && site.Reason == "SetTable" {
			return
		}
	}
	t.Fatalf("nil sparse bool write should fall back through SetTable, sites=%#v", tm.ExitStats().Sites)
}

func TestTier2_TableArrayLoadExitDoesNotReplayPriorSetTable(t *testing.T) {
	src := `
func bump_then_read(arr, key) {
    arr[1] = arr[1] + 1
    return arr[key]
}
`
	top := compileTop(t, src)
	fnProto := findProtoByName(top, "bump_then_read")
	if fnProto == nil {
		t.Fatal("bump_then_read proto not found")
	}
	seedIntTableFeedback(fnProto)

	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}
	fnVal := v.GetGlobal("bump_then_read")
	if fnVal.IsNil() {
		t.Fatal("function bump_then_read not found")
	}

	tbl := runtime.NewTable()
	tbl.RawSetInt(1, runtime.IntValue(10))
	tbl.RawSetInt(-1, runtime.IntValue(20))
	for i := 0; i < 8; i++ {
		if _, err := v.CallValue(fnVal, []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(1)}); err != nil {
			t.Fatalf("warm CallValue: %v", err)
		}
	}

	fn := BuildGraph(fnProto)
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if got := countOps(fn)[OpTableArrayLoad]; got == 0 {
		t.Fatalf("SetTable-before-read should keep typed TableArrayLoad lowering:\n%s", Print(fn))
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	cf.CallVM = v
	cf.DeoptFunc = func(args []runtime.Value) ([]runtime.Value, error) {
		return v.CallValue(fnVal, args)
	}

	before := tbl.RawGetInt(1).Int()
	got, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl), runtime.IntValue(-1)})
	if err != nil {
		t.Fatalf("Tier2 Execute: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 20 {
		t.Fatalf("Tier2 Execute result = %v, want 20", got)
	}
	after := tbl.RawGetInt(1).Int()
	if delta := after - before; delta != 1 {
		t.Fatalf("table write replayed across TableArrayLoad exit: delta=%d, want 1", delta)
	}
}

func TestTier2_TableArrayLoadPreciseDeoptDoesNotReplayPriorSetTable(t *testing.T) {
	src := `
func bump_then_read(arr, key) {
    arr[1] = arr[1] + 1
    return arr[key]
}

arr := {10, 20}
for i := 1; i <= 40; i++ {
    bump_then_read(arr, 1)
}
before := arr[1]
miss := bump_then_read(arr, 99)
after := arr[1]
result := after - before
`
	proto := compileTop(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 1 {
		t.Fatalf("result = %v, want 1", result)
	}
	miss := v.GetGlobal("miss")
	if !miss.IsNil() {
		t.Fatalf("miss = %v, want nil", miss)
	}
}

func TestTier2_TableArrayPointerTempsSurviveExitResume(t *testing.T) {
	src := `
func miss_then_sum(arr, missKey) {
    miss := arr[missKey]
    first := arr[1]
    second := arr[2]
    if miss == nil {
        return first + second
    }
    return first + second + miss
}
`
	top := compileTop(t, src)
	fnProto := findProtoByName(top, "miss_then_sum")
	if fnProto == nil {
		t.Fatal("miss_then_sum proto not found")
	}
	seedIntTableFeedback(fnProto)

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fnVal := v.GetGlobal("miss_then_sum")
	if fnVal.IsNil() {
		t.Fatal("function miss_then_sum not found")
	}

	tbl := runtime.NewTable()
	tbl.RawSetInt(1, runtime.IntValue(10))
	tbl.RawSetInt(2, runtime.IntValue(32))
	for i := 0; i < 8; i++ {
		if _, err := v.CallValue(fnVal, []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(1)}); err != nil {
			t.Fatalf("warm CallValue: %v", err)
		}
	}

	fn := BuildGraph(fnProto)
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if got := countOps(fn)[OpTableArrayLoad]; got < 3 {
		t.Fatalf("expected typed table-array loads, got %d\n%s", got, Print(fn))
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	cf.CallVM = v
	cf.DeoptFunc = func(args []runtime.Value) ([]runtime.Value, error) {
		return v.CallValue(fnVal, args)
	}

	got, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl), runtime.IntValue(99)})
	if err != nil {
		t.Fatalf("Tier2 Execute: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 42 {
		t.Fatalf("miss_then_sum result = %v, want 42", got)
	}
}

func TestTier2_TableArrayLoadFailThenSetKeepsSetBoundsCheck(t *testing.T) {
	src := `
func read_then_set(arr, key, val) {
    old := arr[key]
    arr[key] = val
    return old
}
`
	top := compileTop(t, src)
	fnProto := findProtoByName(top, "read_then_set")
	if fnProto == nil {
		t.Fatal("read_then_set proto not found")
	}
	seedIntTableFeedback(fnProto)

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fnVal := v.GetGlobal("read_then_set")
	if fnVal.IsNil() {
		t.Fatal("read_then_set function not found")
	}

	tbl := runtime.NewTable()
	tbl.RawSetInt(1, runtime.IntValue(10))
	tbl.RawSetInt(2000, runtime.IntValue(20))
	for i := 0; i < 8; i++ {
		if _, err := v.CallValue(fnVal, []runtime.Value{runtime.TableValue(tbl), runtime.IntValue(1), runtime.IntValue(11)}); err != nil {
			t.Fatalf("warm CallValue: %v", err)
		}
	}

	fn := BuildGraph(fnProto)
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if got := countOps(fn)[OpTableArrayLoad]; got == 0 {
		t.Fatalf("expected typed TableArrayLoad lowering:\n%s", Print(fn))
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	cf.CallVM = v
	cf.DeoptFunc = func(args []runtime.Value) ([]runtime.Value, error) {
		return v.CallValue(fnVal, args)
	}

	got, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl), runtime.IntValue(2000), runtime.IntValue(77)})
	if err != nil {
		t.Fatalf("Tier2 Execute: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 20 {
		t.Fatalf("read_then_set result = %v, want old value 20", got)
	}
	if got := tbl.RawGetInt(2000); !got.IsInt() || got.Int() != 77 {
		t.Fatalf("sparse set after failed load = %v, want 77", got)
	}
}

func TestTier2_TableArrayLoadSuccessFactFeedsNextSetBoundsGuard(t *testing.T) {
	src := `
func read_then_set(arr, key, val) {
    old := arr[key]
    arr[key] = val
    return old
}
`
	top := compileTop(t, src)
	fnProto := findProtoByName(top, "read_then_set")
	if fnProto == nil {
		t.Fatal("read_then_set proto not found")
	}
	seedIntTableFeedback(fnProto)

	fn := BuildGraph(fnProto)
	var err error
	fn, _, err = RunTier2Pipeline(fn, &Tier2PipelineOpts{})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	var storeID int
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpTableArrayStore {
				storeID = instr.ID
				break
			}
		}
		if storeID != 0 {
			break
		}
	}
	if storeID == 0 {
		t.Fatalf("expected checked TableArrayStore after lowering:\n%s", Print(fn))
	}

	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	if got := countMatchingIRInstr(cf, storeID, isARM64CBZX17); got != 0 {
		t.Fatalf("checked TableArrayStore should use its typed len operand instead of X17 success flag")
	}
}

func seedIntTableFeedback(proto *vm.FuncProto) {
	fb := proto.EnsureFeedback()
	for pc, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETTABLE:
			fb[pc].Left = vm.FBTable
			fb[pc].Right = vm.FBInt
			fb[pc].Result = vm.FBInt
			fb[pc].Kind = vm.FBKindInt
		case vm.OP_SETTABLE:
			fb[pc].Left = vm.FBTable
			fb[pc].Right = vm.FBInt
			fb[pc].Result = vm.FBInt
			fb[pc].Kind = vm.FBKindInt
		}
	}
}

func seedFloatTableFeedback(proto *vm.FuncProto) {
	fb := proto.EnsureFeedback()
	for pc, inst := range proto.Code {
		switch vm.DecodeOp(inst) {
		case vm.OP_GETTABLE:
			fb[pc].Left = vm.FBTable
			fb[pc].Right = vm.FBInt
			fb[pc].Result = vm.FBFloat
			fb[pc].Kind = vm.FBKindFloat
		case vm.OP_SETTABLE:
			fb[pc].Left = vm.FBTable
			fb[pc].Right = vm.FBInt
			fb[pc].Result = vm.FBFloat
			fb[pc].Kind = vm.FBKindFloat
		}
	}
}

func seedNestedTableFloatFeedback(proto *vm.FuncProto) {
	fb := proto.EnsureFeedback()
	getIndex := 0
	for pc, inst := range proto.Code {
		if vm.DecodeOp(inst) != vm.OP_GETTABLE {
			continue
		}
		fb[pc].Left = vm.FBTable
		fb[pc].Right = vm.FBInt
		if getIndex == 0 {
			fb[pc].Result = vm.FBTable
			fb[pc].Kind = vm.FBKindMixed
		} else {
			fb[pc].Result = vm.FBFloat
			fb[pc].Kind = vm.FBKindFloat
		}
		getIndex++
	}
}

func TestTier2_GetFieldDynamicCacheWarmsWithoutRecompile(t *testing.T) {
	src := `
func readRight(t) {
    return t.right
}

obj := {left: 1, right: 42}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	readRight := findProtoByName(top, "readRight")
	if readRight == nil {
		t.Fatal("readRight proto not found")
	}
	fn := v.GetGlobal("readRight")
	obj := v.GetGlobal("obj")
	if fn.IsNil() || !obj.IsTable() {
		t.Fatalf("missing globals: readRight=%v obj=%v", fn, obj)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(readRight); err != nil {
		t.Fatalf("CompileTier2(readRight): %v", err)
	}
	for i := 0; i < 2; i++ {
		got, err := v.CallValue(fn, []runtime.Value{obj})
		if err != nil {
			t.Fatalf("CallValue #%d: %v", i+1, err)
		}
		if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 42 {
			t.Fatalf("CallValue #%d got %v, want int 42", i+1, got)
		}
	}
	if exits := tm.ExitStats().ByExitCode["ExitTableExit"]; exits != 1 {
		t.Fatalf("dynamic field cache exits=%d, want exactly first-call miss", exits)
	}
}

func isARM64CBZ(insn uint32) bool {
	return insn&0x7E000000 == 0x34000000 && insn&0x01000000 == 0
}

func rangeHasDirectFPLoad(code []byte, start, end int) bool {
	if start < 0 || end > len(code) || start >= end {
		return false
	}
	for off := start; off+4 <= end; off += 4 {
		word := binary.LittleEndian.Uint32(code[off : off+4])
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], word)
		inst, err := arm64asm.Decode(buf[:])
		if err != nil {
			continue
		}
		text := inst.String()
		if strings.HasPrefix(text, "LDR D") && strings.Contains(text, "LSL #3") {
			return true
		}
	}
	return false
}

func isARM64CBZX17(insn uint32) bool {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], insn)
	inst, err := arm64asm.Decode(buf[:])
	if err != nil {
		return false
	}
	return strings.HasPrefix(inst.String(), "CBZ X17")
}
