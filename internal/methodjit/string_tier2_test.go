//go:build darwin && arm64

package methodjit

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func runStringFuncVM(t *testing.T, src, fnName string, args []runtime.Value) []runtime.Value {
	t.Helper()

	top := compileTop(t, src)
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("VM execute top: %v", err)
	}
	fn := v.GetGlobal(fnName)
	results, err := v.CallValue(fn, args)
	if err != nil {
		t.Fatalf("VM CallValue(%s): %v", fnName, err)
	}
	return results
}

func runStringFuncForcedTier2(t *testing.T, src, fnName string, args []runtime.Value, noFilter bool) []runtime.Value {
	t.Helper()
	results, _, _ := runStringFuncForcedTier2WithManager(t, src, fnName, args, noFilter)
	return results
}

func runStringFuncForcedTier2WithManager(t *testing.T, src, fnName string, args []runtime.Value, noFilter bool) ([]runtime.Value, *TieringManager, *vm.FuncProto) {
	t.Helper()
	if noFilter {
		t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")
	}

	top := compileTop(t, src)
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("JIT execute top: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	proto := findProtoByName(top, fnName)
	if proto == nil {
		t.Fatalf("proto %q not found", fnName)
	}
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(%s): %v", fnName, err)
	}

	fn := v.GetGlobal(fnName)
	results, err := v.CallValue(fn, args)
	if err != nil {
		t.Fatalf("Tier2 CallValue(%s): %v", fnName, err)
	}
	if proto.EnteredTier2 == 0 {
		t.Fatalf("%s did not enter Tier2", fnName)
	}
	return results, tm, proto
}

func requireOneString(t *testing.T, label string, values []runtime.Value) string {
	t.Helper()
	if len(values) != 1 {
		t.Fatalf("%s result count=%d, want 1: %v", label, len(values), values)
	}
	if !values[0].IsString() {
		t.Fatalf("%s result=%v (%s), want string", label, values[0], values[0].TypeName())
	}
	return values[0].Str()
}

func requireOneInt(t *testing.T, label string, values []runtime.Value) int64 {
	t.Helper()
	if len(values) != 1 {
		t.Fatalf("%s result count=%d, want 1: %v", label, len(values), values)
	}
	if !values[0].IsInt() {
		t.Fatalf("%s result=%v (%s), want int", label, values[0], values[0].TypeName())
	}
	return values[0].Int()
}

func TestTier2_ConcatExit_AllOperands(t *testing.T) {
	src := `
func concat3(a, b, c) {
    return a .. b .. c
}
`
	args := []runtime.Value{
		runtime.StringValue("alpha"),
		runtime.StringValue("-"),
		runtime.StringValue("omega"),
	}
	want := requireOneString(t, "VM", runStringFuncVM(t, src, "concat3", args))
	got := requireOneString(t, "Tier2", runStringFuncForcedTier2(t, src, "concat3", args, false))
	if got != want {
		t.Fatalf("concat3 Tier2=%q, want VM=%q", got, want)
	}
}

func TestTier2_ConstStringFastPath_NoOpExit(t *testing.T) {
	src := `
func literal() {
    return "alpha"
}
`
	gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "literal", nil, true)
	got := requireOneString(t, "literal", gotValues)
	if got != "alpha" {
		t.Fatalf("literal=%q, want alpha", got)
	}
	if exits := gotTM.ExitStats().ByExitCode["ExitOpExit"]; exits != 0 {
		t.Fatalf("string literal load should stay native, ExitOpExit=%d", exits)
	}
}

func TestTier2_StringFormatFieldLoadUsesStringMapCache(t *testing.T) {
	src := `
func format_many(n) {
    total := 0
    for i := 1; i <= n; i++ {
        s := string.format("key%d", i % 10)
        total = total + #s
    }
    return total
}
`
	args := []runtime.Value{runtime.IntValue(40)}
	want := requireOneInt(t, "VM", runStringFuncVM(t, src, "format_many", args))
	gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "format_many", args, true)
	got := requireOneInt(t, "Tier2", gotValues)
	if got != want {
		t.Fatalf("format_many Tier2=%d, want VM=%d", got, want)
	}
	if exits := gotTM.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
		t.Fatalf("narrow string.format lowering should avoid call exits, ExitCallExit=%d", exits)
	}

	var getFieldExits uint64
	for _, site := range gotTM.ExitStats().Sites {
		if site.ExitName == "ExitTableExit" && site.Reason == "GetField" {
			getFieldExits += site.Count
		}
	}
	if getFieldExits > 2 {
		t.Fatalf("string.format field load should hit native string-map cache after warmup, GetField exits=%d", getFieldExits)
	}
}

func TestTier2_StringFormatLookupPreservesPositiveDivisorModuloSemantics(t *testing.T) {
	src := `
func format_case(x) {
    return string.format("key%d", x % 10)
}
`
	cases := []struct {
		name string
		arg  int64
	}{
		{
			name: "negative",
			arg:  -1,
		},
		{
			name: "positive",
			arg:  11,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []runtime.Value{runtime.IntValue(tc.arg)}
			want := requireOneString(t, "VM", runStringFuncVM(t, src, "format_case", args))
			gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "format_case", args, true)
			got := requireOneString(t, "Tier2", gotValues)
			if got != want {
				t.Fatalf("format_case(%d) Tier2=%q, want VM=%q", tc.arg, got, want)
			}
			if exits := gotTM.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
				t.Fatalf("modulo string.format lookup should avoid call exits, ExitCallExit=%d", exits)
			}
			if exits := gotTM.ExitStats().ByExitCode["ExitOpExit"]; exits == 0 {
				t.Fatal("string.format int helper should use precise op-exit fallback while native arena is disabled")
			}
		})
	}
}

func TestTier2_StringFormatIntLoweringCoversGenericSingleIntPatterns(t *testing.T) {
	cases := []struct {
		name string
		src  string
		arg  int64
	}{
		{
			name: "bare_decimal",
			src: `
func format_case(i) {
    return string.format("%d", i)
}
`,
			arg: 42,
		},
		{
			name: "non_modulo_argument",
			src: `
func format_case(i) {
    return string.format("key%d", i)
}
`,
			arg: 7,
		},
		{
			name: "padded_format",
			src: `
func format_case(i) {
    return string.format("key%05d", i % 10)
}
`,
			arg: 7,
		},
		{
			name: "zero_padded_negative",
			src: `
func format_case(i) {
    return string.format("%05d", i)
}
`,
			arg: -42,
		},
		{
			name: "padded_negative_with_suffix",
			src: `
func format_case(i) {
    return string.format("pre%04d_suf", i)
}
`,
			arg: -7,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			top := compileTop(t, tc.src)
			proto := findProtoByName(top, "format_case")
			if proto == nil {
				t.Fatal("proto format_case not found")
			}
			fn, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
			if err != nil {
				t.Fatalf("RunTier2Pipeline: %v", err)
			}
			if got := countOpHelper(fn, OpStringFormatInt); got != 1 {
				t.Fatalf("string.format int lowering count=%d, want 1", got)
			}

			args := []runtime.Value{runtime.IntValue(tc.arg)}
			want := requireOneString(t, "VM", runStringFuncVM(t, tc.src, "format_case", args))
			gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, tc.src, "format_case", args, true)
			got := requireOneString(t, "Tier2", gotValues)
			if got != want {
				t.Fatalf("format_case Tier2=%q, want VM=%q", got, want)
			}
			if exits := gotTM.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
				t.Fatalf("string.format int lowering should avoid call exits, ExitCallExit=%d", exits)
			}
		})
	}
}

func TestTier2_StringFormatIntMinInt64FallsBackPrecisely(t *testing.T) {
	src := `
func format_case(i) {
    return string.format("%d", i)
}
`
	args := []runtime.Value{runtime.FloatValue(float64(math.MinInt64))}
	want := requireOneString(t, "VM", runStringFuncVM(t, src, "format_case", args))
	if want != "-9223372036854775808" {
		t.Fatalf("VM MinInt64 result=%q", want)
	}
	gotValues, _, _ := runStringFuncForcedTier2WithManager(t, src, "format_case", args, true)
	got := requireOneString(t, "Tier2", gotValues)
	if got != want {
		t.Fatalf("format_case Tier2=%q, want VM=%q", got, want)
	}
}

func TestTier2_StringFormatIntReboundCalleeFallsBackPrecisely(t *testing.T) {
	src := `
func replacement(pattern, n) {
    return "rebased:" .. pattern .. ":" .. n
}

func format_case(i) {
    string.format = replacement
    return string.format("key%03d", i)
}
`
	args := []runtime.Value{runtime.IntValue(7)}
	want := requireOneString(t, "VM", runStringFuncVM(t, src, "format_case", args))
	gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "format_case", args, true)
	got := requireOneString(t, "Tier2", gotValues)
	if got != want {
		t.Fatalf("format_case Tier2=%q, want VM=%q", got, want)
	}
	if exits := gotTM.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
		t.Fatalf("string.format int precise fallback should avoid call exits, ExitCallExit=%d", exits)
	}
}

func TestTier2_StringFormatIntFeedbackDynamicPatternGuardsPattern(t *testing.T) {
	src := `
func format_case(pattern, i) {
    return string.format(pattern, i)
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "format_case")
	if proto == nil {
		t.Fatal("proto format_case not found")
	}
	proto.EnsureFeedback()
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	fnVal := v.GetGlobal("format_case")
	warmArgs := []runtime.Value{runtime.StringValue("dyn%04d"), runtime.IntValue(7)}
	for i := 0; i < 2; i++ {
		if _, err := v.CallValue(fnVal, warmArgs); err != nil {
			t.Fatalf("warm CallValue: %v", err)
		}
	}

	optimized, _, err := RunTier2Pipeline(BuildGraph(proto), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline: %v", err)
	}
	if got := countOpHelper(optimized, OpStringFormatInt); got != 1 {
		t.Fatalf("feedback-derived dynamic pattern lowering count=%d, want 1", got)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2: %v", err)
	}
	gotValues, err := v.CallValue(fnVal, warmArgs)
	if err != nil {
		t.Fatalf("native dynamic pattern CallValue: %v", err)
	}
	if got := requireOneString(t, "native dynamic pattern", gotValues); got != "dyn0007" {
		t.Fatalf("native dynamic pattern result=%q", got)
	}
	matchingExits := tm.ExitStats().ByExitCode["ExitOpExit"]
	if matchingExits == 0 {
		t.Fatal("matching dynamic pattern should use precise op-exit fallback while native arena is disabled")
	}

	otherArgs := []runtime.Value{runtime.StringValue("alt%d"), runtime.IntValue(8)}
	gotValues, err = v.CallValue(fnVal, otherArgs)
	if err != nil {
		t.Fatalf("fallback dynamic pattern CallValue: %v", err)
	}
	if got := requireOneString(t, "fallback dynamic pattern", gotValues); got != "alt8" {
		t.Fatalf("fallback dynamic pattern result=%q", got)
	}
	if exits := tm.ExitStats().ByExitCode["ExitOpExit"]; exits <= matchingExits {
		t.Fatal("mismatched dynamic pattern should add a precise fallback op exit")
	}
}

func TestTier2_StringCompareFastPath_MatchesVM(t *testing.T) {
	src := `
func sort_last() {
    arr := {}
    for i := 1; i <= 40; i++ {
        arr[i] = string.format("key_%03d", (i * 7) % 40)
    }
    n := #arr
    for i := 1; i <= n - 1; i++ {
        for j := 1; j <= n - i; j++ {
            if arr[j] > arr[j + 1] {
                t := arr[j]
                arr[j] = arr[j + 1]
                arr[j + 1] = t
            }
        }
    }
    return arr[n]
}
`
	want := requireOneString(t, "VM", runStringFuncVM(t, src, "sort_last", nil))
	got := requireOneString(t, "Tier2", runStringFuncForcedTier2(t, src, "sort_last", nil, true))
	if got != want {
		t.Fatalf("sort_last Tier2=%q, want VM=%q", got, want)
	}
}

func TestTier2_StringCompareFastPath_NoOpExit(t *testing.T) {
	src := `
func cmp(a, b) {
    if a < b {
        return 1
    }
    if a <= b {
        return 2
    }
    return 3
}
`
	cases := []struct {
		a, b string
		want int64
	}{
		{"alpha", "beta", 1},
		{"same", "same", 2},
		{"zeta", "beta", 3},
	}

	for _, tc := range cases {
		gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "cmp", []runtime.Value{
			runtime.StringValue(tc.a),
			runtime.StringValue(tc.b),
		}, true)
		got := requireOneInt(t, tc.a+"_"+tc.b, gotValues)
		if got != tc.want {
			t.Fatalf("cmp(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
		if exits := gotTM.ExitStats().ByExitCode["ExitOpExit"]; exits != 0 {
			t.Fatalf("cmp(%q,%q) should stay native, ExitOpExit=%d", tc.a, tc.b, exits)
		}
	}
}

func TestTier2_StringEqualityFastPath_NoOpExit(t *testing.T) {
	src := `
func eq(a, b) {
    if a == b {
        return 1
    }
    return 0
}
`
	cases := []struct {
		a, b string
		want int64
	}{
		{"same", "same", 1},
		{"alpha", "beta", 0},
		{"prefix", "prefix-long", 0},
	}

	for _, tc := range cases {
		gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "eq", []runtime.Value{
			runtime.StringValue(tc.a),
			runtime.StringValue(tc.b),
		}, true)
		got := requireOneInt(t, tc.a+"_"+tc.b, gotValues)
		if got != tc.want {
			t.Fatalf("eq(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
		if exits := gotTM.ExitStats().ByExitCode["ExitOpExit"]; exits != 0 {
			t.Fatalf("eq(%q,%q) should stay native, ExitOpExit=%d", tc.a, tc.b, exits)
		}
	}
}

func TestTier2_DynamicStringKeyCacheGetTable_NoLoopTableExit(t *testing.T) {
	src := `
func lookup(n) {
    keys := {"a", "b", "c", "d"}
    totals := {a: 1, b: 2, c: 3, d: 4}
    sum := 0
    for i := 1; i <= n; i++ {
        k := keys[(i % 4) + 1]
        sum = sum + totals[k]
    }
    return sum
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "lookup")
	if proto == nil {
		t.Fatal("lookup proto not found")
	}
	proto.EnsureFeedback()

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("VM execute top: %v", err)
	}
	fnVal := v.GetGlobal("lookup")
	wantValues, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(80)})
	if err != nil {
		t.Fatalf("warm lookup: %v", err)
	}
	want := requireOneInt(t, "VM lookup", wantValues)
	if !protoHasAnyDynamicStringKeyCache(proto) {
		t.Fatal("warmup did not populate dynamic string-key cache")
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(lookup): %v", err)
	}
	gotValues, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(80)})
	if err != nil {
		t.Fatalf("Tier2 lookup: %v", err)
	}
	got := requireOneInt(t, "Tier2 lookup", gotValues)
	if got != want {
		t.Fatalf("lookup Tier2=%d, want VM=%d", got, want)
	}

	var getTableExits uint64
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "lookup" && site.ExitName == "ExitTableExit" && site.Reason == "GetTable" {
			getTableExits += site.Count
		}
	}
	if getTableExits != 0 {
		t.Fatalf("dynamic string-key lookup should stay native, GetTable exits=%d sites=%#v", getTableExits, tm.ExitStats().Sites)
	}
}

func TestTier2_DynamicStringMapValueCacheGetTable_NoLoopTableExit(t *testing.T) {
	src := `
func lookup(tbl, keys, n) {
    sum := 0
    for i := 1; i <= n; i++ {
        k := keys[(i % 4) + 1]
        sum = sum + tbl[k]
    }
    return sum
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "lookup")
	if proto == nil {
		t.Fatal("lookup proto not found")
	}
	proto.EnsureFeedback()

	tbl := runtime.NewTable()
	for i := int64(0); i < 16; i++ {
		tbl.RawSetString("k"+runtime.IntValue(i).String(), runtime.IntValue(i))
	}
	keys := runtime.NewTable()
	keys.RawSetInt(1, runtime.StringValue("k12"))
	keys.RawSetInt(2, runtime.StringValue("k13"))
	keys.RawSetInt(3, runtime.StringValue("k14"))
	keys.RawSetInt(4, runtime.StringValue("k15"))
	args := []runtime.Value{runtime.TableValue(tbl), runtime.TableValue(keys), runtime.IntValue(80)}

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("VM execute top: %v", err)
	}
	fnVal := v.GetGlobal("lookup")
	wantValues, err := v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("warm lookup: %v", err)
	}
	want := requireOneInt(t, "VM lookup", wantValues)
	if proto.Feedback == nil {
		t.Fatal("warmup did not retain feedback")
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(lookup): %v", err)
	}
	gotValues, err := v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("Tier2 lookup: %v", err)
	}
	got := requireOneInt(t, "Tier2 lookup", gotValues)
	if got != want {
		t.Fatalf("lookup Tier2=%d, want VM=%d", got, want)
	}

	var getTableExits uint64
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "lookup" && site.ExitName == "ExitTableExit" && site.Reason == "GetTable" {
			getTableExits += site.Count
		}
	}
	if getTableExits != 0 {
		t.Fatalf("dynamic string-map lookup should stay native, GetTable exits=%d sites=%#v", getTableExits, tm.ExitStats().Sites)
	}
}

func TestTier2_DynamicStringSmallShapeMissingKey_NoLoopTableExit(t *testing.T) {
	src := `
func lookup(n) {
    keys := {"missing_a", "missing_b", "missing_c"}
    totals := {present: 1}
    misses := 0
    for i := 1; i <= n; i++ {
        k := keys[(i % 3) + 1]
        if totals[k] == nil {
            misses = misses + 1
        }
    }
    return misses
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "lookup")
	if proto == nil {
		t.Fatal("lookup proto not found")
	}
	proto.EnsureFeedback()

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("VM execute top: %v", err)
	}
	fnVal := v.GetGlobal("lookup")
	wantValues, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(90)})
	if err != nil {
		t.Fatalf("warm lookup: %v", err)
	}
	want := requireOneInt(t, "VM lookup", wantValues)

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(lookup): %v", err)
	}
	gotValues, err := v.CallValue(fnVal, []runtime.Value{runtime.IntValue(90)})
	if err != nil {
		t.Fatalf("Tier2 lookup: %v", err)
	}
	got := requireOneInt(t, "Tier2 lookup", gotValues)
	if got != want {
		t.Fatalf("lookup Tier2=%d, want VM=%d", got, want)
	}

	var getTableExits uint64
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "lookup" && site.ExitName == "ExitTableExit" && site.Reason == "GetTable" {
			getTableExits += site.Count
		}
	}
	if getTableExits != 0 {
		t.Fatalf("missing small-shape string lookup should stay native, GetTable exits=%d sites=%#v", getTableExits, tm.ExitStats().Sites)
	}
}

func TestTier2_DynamicStringGetTable_ColdFeedbackSmallScan(t *testing.T) {
	src := `
func lookup(tbl, key) {
    return tbl[key]
}
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "lookup")
	if proto == nil {
		t.Fatal("lookup proto not found")
	}

	tbl := runtime.NewTable()
	tbl.RawSetString("region", runtime.IntValue(42))
	args := []runtime.Value{runtime.TableValue(tbl), runtime.StringValue("region")}

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("VM execute top: %v", err)
	}
	fnVal := v.GetGlobal("lookup")

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(lookup): %v", err)
	}
	gotValues, err := v.CallValue(fnVal, args)
	if err != nil {
		t.Fatalf("Tier2 lookup: %v", err)
	}
	got := requireOneInt(t, "Tier2 lookup", gotValues)
	if got != 42 {
		t.Fatalf("lookup Tier2=%d, want 42", got)
	}

	var getTableExits uint64
	for _, site := range tm.ExitStats().Sites {
		if site.Proto == "lookup" && site.ExitName == "ExitTableExit" && site.Reason == "GetTable" {
			getTableExits += site.Count
		}
	}
	if getTableExits != 0 {
		t.Fatalf("cold-feedback string lookup should use native small scan, GetTable exits=%d sites=%#v", getTableExits, tm.ExitStats().Sites)
	}
}

func protoHasAnyDynamicStringKeyCache(proto *vm.FuncProto) bool {
	if proto == nil {
		return false
	}
	for pc := range proto.Code {
		if protoHasDynamicStringKeyCacheAt(proto, pc) {
			return true
		}
	}
	return false
}

func TestTier2_StringLenFastPath_NoOpExit(t *testing.T) {
	src := `
func strlen_sum(a, b) {
    return #a + #b
}
`
	gotValues, gotTM, _ := runStringFuncForcedTier2WithManager(t, src, "strlen_sum", []runtime.Value{
		runtime.StringValue("alpha"),
		runtime.StringValue("watermelon"),
	}, true)
	got := requireOneInt(t, "strlen_sum", gotValues)
	if got != int64(len("alpha")+len("watermelon")) {
		t.Fatalf("strlen_sum=%d, want %d", got, len("alpha")+len("watermelon"))
	}
	if exits := gotTM.ExitStats().ByExitCode["ExitOpExit"]; exits != 0 {
		t.Fatalf("string length should stay native, ExitOpExit=%d", exits)
	}
}
