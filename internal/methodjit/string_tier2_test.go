//go:build darwin && arm64

package methodjit

import (
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
