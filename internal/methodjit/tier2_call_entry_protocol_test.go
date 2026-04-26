//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTier2DirectEntryPtrPublishedAndCleared(t *testing.T) {
	src := `
func inc(n) {
    return n + 1
}
`
	top := compileProto(t, src)
	inc := findProtoByName(top, "inc")
	if inc == nil {
		t.Fatal("inc proto not found")
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(inc); err != nil {
		t.Fatalf("CompileTier2(inc): %v", err)
	}
	cf := tm.tier2Compiled[inc]
	if cf == nil {
		t.Fatal("missing Tier 2 compiled function")
	}
	want := uintptr(cf.Code.Ptr()) + uintptr(cf.DirectEntryOffset)
	if inc.DirectEntryPtr != want {
		t.Fatalf("DirectEntryPtr=%#x want %#x", inc.DirectEntryPtr, want)
	}
	if inc.Tier2DirectEntryPtr != want {
		t.Fatalf("Tier2DirectEntryPtr=%#x want %#x", inc.Tier2DirectEntryPtr, want)
	}

	tm.disableTier2AfterRuntimeDeopt(inc, "test")
	if inc.DirectEntryPtr != 0 || inc.Tier2DirectEntryPtr != 0 {
		t.Fatalf("runtime disable left entries published: direct=%#x tier2=%#x",
			inc.DirectEntryPtr, inc.Tier2DirectEntryPtr)
	}
}

func TestTier2CallICUsesTier2EntryWhenGenericEntryCleared(t *testing.T) {
	src := `
func inc(n) {
    return n + 1
}
func caller(f, n) {
    return f(n) + 1
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	inc := findProtoByName(top, "inc")
	caller := findProtoByName(top, "caller")
	if inc == nil || caller == nil {
		t.Fatalf("missing protos: inc=%v caller=%v", inc != nil, caller != nil)
	}

	// Collect call/argument feedback before compiling caller so the residual
	// dynamic call has stable types but still cannot be inlined by global name.
	fnInc := v.GetGlobal("inc")
	fnCaller := v.GetGlobal("caller")
	if _, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)}); err != nil {
		t.Fatalf("warm CallValue(caller): %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(inc); err != nil {
		t.Fatalf("CompileTier2(inc): %v", err)
	}
	if err := tm.CompileTier2(caller); err != nil {
		t.Fatalf("CompileTier2(caller): %v", err)
	}

	if inc.Tier2DirectEntryPtr == 0 {
		t.Fatal("inc Tier2DirectEntryPtr was not published")
	}
	inc.DirectEntryPtr = 0
	inc.CallCount = 100 // avoid the tier-up threshold slow edge in the call template

	results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("CallValue(caller): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 42 {
		t.Fatalf("caller result=%v, want int 42", results)
	}
	if inc.EnteredTier2 == 0 || caller.EnteredTier2 == 0 {
		t.Fatalf("expected both protos to enter Tier 2, inc=%d caller=%d",
			inc.EnteredTier2, caller.EnteredTier2)
	}
	if got := tm.ExitStats().ByExitCode["ExitCallExit"]; got != 0 {
		t.Fatalf("generic DirectEntryPtr clear forced ExitCallExit count=%d; want 0", got)
	}
}

func TestTier2GlobalCacheInvalidatesByName(t *testing.T) {
	tm := NewTieringManager()
	proto := &vm.FuncProto{
		Name: "uses_globals",
		Constants: []runtime.Value{
			runtime.StringValue("hot"),
			runtime.StringValue("cold"),
		},
	}
	cf := &CompiledFunction{
		Proto:             proto,
		GlobalCache:       []uint64{11, 22},
		GlobalCacheConsts: []int{0, 1},
	}
	tm.tier2Compiled[proto] = cf

	tm.invalidateGlobalValueCaches("hot")
	if cf.GlobalCache[0] != 0 {
		t.Fatalf("hot cache entry=%d want 0", cf.GlobalCache[0])
	}
	if cf.GlobalCache[1] != 22 {
		t.Fatalf("cold cache entry=%d want preserved 22", cf.GlobalCache[1])
	}
}
