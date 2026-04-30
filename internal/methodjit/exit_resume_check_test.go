//go:build darwin && arm64

package methodjit

import (
	"os"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func withExitResumeCheck(t *testing.T, fn func()) {
	t.Helper()
	old, had := os.LookupEnv(exitResumeCheckEnv)
	if err := os.Setenv(exitResumeCheckEnv, "1"); err != nil {
		t.Fatalf("Setenv(%s): %v", exitResumeCheckEnv, err)
	}
	defer func() {
		if had {
			_ = os.Setenv(exitResumeCheckEnv, old)
		} else {
			_ = os.Unsetenv(exitResumeCheckEnv)
		}
	}()
	fn()
}

func TestExitResumeCheck_FPRLiveAcrossTableExit(t *testing.T) {
	withExitResumeCheck(t, func() {
		src := `
func f() {
    acc := 1.25 + 2.5
    t := {}
    x := t.missing
    if x {
        acc = acc + 100.0
    }
    return acc + 3.0
}
`
		top := compileTop(t, src)
		assertExitResumeCheckSite(t, top, "f", func(site *exitResumeCheckSite) bool {
			for _, live := range site.LiveSlots {
				if live.RawFloat {
					return true
				}
			}
			return false
		})
		vmResults := runVMByName(t, src, "f", nil)
		jitResults, entered := runForcedTier2ByName(t, top, "f", []string{"f"}, nil)
		assertRawIntSelfResultsEqual(t, "f", jitResults, vmResults)
		if entered["f"] == 0 {
			t.Fatalf("f did not enter Tier 2")
		}
	})
}

func TestExitResumeCheck_TableExitRawIntLiveness(t *testing.T) {
	withExitResumeCheck(t, func() {
		src := `
func f() {
    a := 40 + 2
    t := {}
    t[100] = a
    return a
}
`
		top := compileTop(t, src)
		assertExitResumeCheckSite(t, top, "f", func(site *exitResumeCheckSite) bool {
			for _, live := range site.LiveSlots {
				if live.RawInt {
					return true
				}
			}
			return false
		})
		vmResults := runVMByName(t, src, "f", nil)
		jitResults, entered := runForcedTier2ByName(t, top, "f", []string{"f"}, nil)
		assertRawIntSelfResultsEqual(t, "f", jitResults, vmResults)
		if entered["f"] == 0 {
			t.Fatalf("f did not enter Tier 2")
		}
	})
}

func TestExitResumeCheck_NativeCallSpillsRawIntAndRawFloat(t *testing.T) {
	withExitResumeCheck(t, func() {
		src := `
func id(x) {
    return x
}
func f(n) {
    a := 40 + 2
    b := 1.25 + 2.5
    c := id(n)
    return a + c + b
}
`
		top := compileTop(t, src)
		assertExitResumeCheckSite(t, top, "f", func(site *exitResumeCheckSite) bool {
			if site.Key.ExitCode != ExitCallExit {
				return false
			}
			hasRawInt := false
			hasRawFloat := false
			for _, live := range site.LiveSlots {
				hasRawInt = hasRawInt || live.RawInt
				hasRawFloat = hasRawFloat || live.RawFloat
			}
			return hasRawInt && hasRawFloat
		})
		args := []runtime.Value{runtime.IntValue(5)}
		vmResults := runVMByName(t, src, "f", args)
		jitResults, entered := runForcedTier2ByName(t, top, "f", []string{"id", "f"}, args)
		assertRawIntSelfResultsEqual(t, "f", jitResults, vmResults)
		if entered["f"] == 0 {
			t.Fatalf("f did not enter Tier 2")
		}
	})
}

func TestExitResumeCheck_CacheableLoopNewTableMarksResultSlot(t *testing.T) {
	withExitResumeCheck(t, func() {
		src := `
func f(n) {
    total := 0
    for i := 1; i <= n; i++ {
        row := {}
        row[0] = true
        if row[0] {
            total = total + 1
        }
    }
    return total
}
`
		top := compileTop(t, src)
		proto := findProtoByName(top, "f")
		if proto == nil {
			t.Fatal("function \"f\" not found")
		}
		fn := BuildGraph(proto)
		optimized, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{})
		if err != nil {
			t.Fatalf("pipeline f: %v", err)
		}
		newTableIDs := make(map[int]bool)
		for _, block := range optimized.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpNewTable && tier2NewTableLoopCandidateIsSafe(instr) {
					newTableIDs[instr.ID] = true
				}
			}
		}
		if len(newTableIDs) == 0 {
			t.Fatalf("expected a cacheable NewTable in optimized IR:\n%s", Print(optimized))
		}
		assertExitResumeCheckSite(t, top, "f", func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitTableExit &&
				newTableIDs[site.Key.InstrID] &&
				len(site.ModifiedSlots) == 1
		})
		vmResults := runVMByName(t, src, "f", []runtime.Value{runtime.IntValue(40)})
		jitResults, entered := runForcedTier2ByName(t, top, "f", []string{"f"}, []runtime.Value{runtime.IntValue(40)})
		assertRawIntSelfResultsEqual(t, "f", jitResults, vmResults)
		if entered["f"] == 0 {
			t.Fatalf("f did not enter Tier 2")
		}
	})
}

func TestExitResumeCheck_RawIntSelfCallFallbackFrame(t *testing.T) {
	withExitResumeCheck(t, func() {
		src := `func grow(n, x) {
	if n == 0 { return x }
	return grow(n - 1, x + 100000000000000)
}
func caller(n, seed) {
	a := n + 17
	b := seed - 3
	c := n * 1000
	r := grow(n, seed)
	return r + a + b + c
}`
		top := compileTop(t, src)
		assertExitResumeCheckSite(t, top, "grow", func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitCallExit && site.RequireRawIntArgs
		})
		grow := findProtoByName(top, "grow")
		if grow == nil {
			t.Fatal("function \"grow\" not found")
		}
		assertRawIntSpecializedABI(t, AnalyzeSpecializedABI(grow), 2)

		args := []runtime.Value{runtime.IntValue(10), runtime.IntValue(90000000000000)}
		vmResults := runVMByName(t, src, "caller", args)
		jitResults, entered := runForcedTier2ByName(t, top, "caller", []string{"grow", "caller"}, args)
		assertRawIntSelfResultsEqual(t, "caller", jitResults, vmResults)
		if entered["caller"] == 0 || entered["grow"] == 0 {
			t.Fatalf("expected caller and grow to enter Tier 2, entered=%v", entered)
		}
	})
}

func TestExitResumeCheck_MetadataDisabledByDefault(t *testing.T) {
	old, had := os.LookupEnv(exitResumeCheckEnv)
	_ = os.Unsetenv(exitResumeCheckEnv)
	defer func() {
		if had {
			_ = os.Setenv(exitResumeCheckEnv, old)
		}
	}()

	top := compileTop(t, `func f() { t := {}; return t.missing }`)
	proto := findProtoByName(top, "f")
	if proto == nil {
		t.Fatal("function \"f\" not found")
	}
	tm := NewTieringManager()
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2: %v", err)
	}
	cf := tm.tier2Compiled[proto]
	if cf == nil {
		t.Fatal("missing Tier 2 compiled function")
	}
	if cf.ExitResumeCheck != nil {
		t.Fatal("ExitResumeCheck metadata should be nil unless explicitly enabled")
	}
}

func assertExitResumeCheckSite(t *testing.T, top *vm.FuncProto, name string, pred func(*exitResumeCheckSite) bool) {
	t.Helper()
	proto := findProtoByName(top, name)
	if proto == nil {
		t.Fatalf("function %q not found", name)
	}
	tm := NewTieringManager()
	if err := tm.CompileTier2(proto); err != nil {
		t.Fatalf("CompileTier2(%s): %v", name, err)
	}
	cf := tm.tier2Compiled[proto]
	if cf == nil || cf.ExitResumeCheck == nil {
		t.Fatalf("%s missing exit-resume check metadata", name)
	}
	for _, site := range cf.ExitResumeCheck.Sites {
		if pred(site) {
			return
		}
	}
	t.Fatalf("%s exit-resume check metadata did not contain requested site; sites=%d", name, len(cf.ExitResumeCheck.Sites))
}
