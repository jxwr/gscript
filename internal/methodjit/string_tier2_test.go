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
	return results
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

func TestTier2_StringCompareFallback_MatchesVM(t *testing.T) {
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
