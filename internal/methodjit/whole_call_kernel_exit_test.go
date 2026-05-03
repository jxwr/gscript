//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const spectralWholeCallKernelExitSrc = `
func A(i, j) {
	return 1.0 / ((i + j) * (i + j + 1) / 2 + i + 1)
}

func multiplyAv(n, v, av) {
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum = sum + A(i, j) * v[j]
		}
		av[i] = sum
	}
}

func multiplyAtv(n, v, atv) {
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum = sum + A(j, i) * v[j]
		}
		atv[i] = sum
	}
}

func multiplyAtAv(n, v, atav) {
	u := {}
	for i := 0; i < n; i++ { u[i] = 0.0 }
	multiplyAv(n, v, u)
	multiplyAtv(n, u, atav)
}

N := 64
u := {}
v := {}
for i := 0; i < N; i++ {
	u[i] = 1.0
	v[i] = 0.0
}

for iter := 0; iter < 10; iter++ {
	multiplyAtAv(N, u, v)
	multiplyAtAv(N, v, u)
}

result := u[0] + v[0]
`

func TestWholeCallKernelExitLowersStableNoResultCall(t *testing.T) {
	t.Skip("spectral whole-call no-result lowering is disabled until its fallback contract preserves VM semantics")
	top := compileProto(t, spectralWholeCallKernelExitSrc)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsFloat() || result.Float() == 0 {
		t.Fatalf("unexpected result global: %v", result)
	}

	snap := tm.ExitStats()
	if cf := tm.tier2Compiled[top]; cf == nil || len(cf.WholeCallNoResultBatches) == 0 {
		t.Fatalf("missing whole-call batch metadata: cf=%#v", cf)
	}
	if got := snap.ByExitCode["ExitOpExit"]; got == 0 {
		t.Fatalf("stable whole-call kernel did not execute through op-exit path: %#v", snap)
	}
	hotCallOpExits := uint64(0)
	for _, site := range snap.Sites {
		if site.Proto == "<main>" && site.Reason == "Call" && site.ExitName == "ExitCallExit" && site.Count >= 10 {
			t.Fatalf("hot no-result whole-call kernel still used CallExit: site=%#v all=%#v", site, snap.Sites)
		}
		if site.Proto == "<main>" && site.Reason == "Call" && site.ExitName == "ExitOpExit" {
			hotCallOpExits += site.Count
		}
	}
	if hotCallOpExits > 3 {
		t.Fatalf("whole-call kernel loop was not batched: Call OpExit=%d sites=%#v", hotCallOpExits, snap.Sites)
	}
}

func TestWholeCallKernelExitUsesStableRuntimeFeedback(t *testing.T) {
	top := compileProto(t, `
total := 0

func sink(x) {
	total = total + x
}

func caller(f, x) {
	f(x)
}

for i := 0; i < 3; i++ {
	caller(sink, 1)
}
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("missing caller proto")
	}
	caller.EnsureFeedback()

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("warm execute: %v", err)
	}

	fn, _, err := RunTier2Pipeline(BuildGraph(caller), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline(caller): %v", err)
	}
	if len(fn.WholeCallNoResultKernels) != 1 {
		t.Fatalf("stable runtime no-result call was not annotated: %#v", fn.WholeCallNoResultKernels)
	}
}

func TestWholeCallKernelExitRejectsPolymorphicRuntimeFeedback(t *testing.T) {
	top := compileProto(t, `
total := 0

func sinkA(x) {
	total = total + x
}

func sinkB(x) {
	total = total - x
}

func caller(f, x) {
	f(x)
}

caller(sinkA, 1)
caller(sinkB, 1)
`)
	caller := findProtoByName(top, "caller")
	if caller == nil {
		t.Fatal("missing caller proto")
	}
	caller.EnsureFeedback()

	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("warm execute: %v", err)
	}

	fn, _, err := RunTier2Pipeline(BuildGraph(caller), nil)
	if err != nil {
		t.Fatalf("RunTier2Pipeline(caller): %v", err)
	}
	if len(fn.WholeCallNoResultKernels) != 0 {
		t.Fatalf("polymorphic runtime call should not be annotated: %#v", fn.WholeCallNoResultKernels)
	}
}

func TestWholeCallKernelOpExitGuardMissFallsBackToGenericCall(t *testing.T) {
	top := compileProto(t, `
total := 0

func inc(x) {
	total = total + x
}
`)
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	fnVal := v.GetGlobal("inc")
	regs := []runtime.Value{fnVal, runtime.IntValue(7)}
	tm := NewTieringManager()
	tm.callVM = v
	ctx := &ExecContext{
		OpExitOp:   int64(OpCall),
		OpExitSlot: 0,
		OpExitArg1: 1,
		OpExitArg2: 0,
	}
	if err := tm.executeOpExit(ctx, regs, 0, top); err != nil {
		t.Fatalf("executeOpExit fallback: %v", err)
	}
	if got := v.GetGlobal("total"); !got.IsInt() || got.Int() != 7 {
		t.Fatalf("generic fallback did not run side effect: total=%v", got)
	}
}
