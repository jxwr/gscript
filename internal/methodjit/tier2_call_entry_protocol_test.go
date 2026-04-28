//go:build darwin && arm64

package methodjit

import (
	"os"
	"testing"
	"unsafe"

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

func TestTier1CallICDispatchesThroughTier2AfterDirectEntryVersionChange(t *testing.T) {
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

	fnInc := v.GetGlobal("inc")
	fnCaller := v.GetGlobal("caller")
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	for i := 0; i < 3; i++ {
		results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)})
		if err != nil {
			t.Fatalf("warm CallValue(caller) #%d: %v", i+1, err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 42 {
			t.Fatalf("warm caller result=%v, want int 42", results)
		}
	}

	tier1 := tm.tier1
	callerBF := tier1.compiled[caller]
	if callerBF == nil {
		t.Fatal("caller was not compiled at Tier 1")
	}
	incPtr := uint64(uintptr(unsafe.Pointer(inc)))
	cachedEntry := uint64(0)
	for i := 0; i+baselineCallCacheStride-1 < len(callerBF.CallCache); i += baselineCallCacheStride {
		if callerBF.CallCache[i+baselineCallCacheProtoOff/8] == incPtr {
			cachedEntry = callerBF.CallCache[i+baselineCallCacheEntryOff/8]
			break
		}
	}
	if cachedEntry == 0 {
		t.Fatal("caller Tier 1 call IC did not cache inc")
	}
	if uintptr(cachedEntry) != inc.DirectEntryPtr {
		t.Fatalf("cached baseline entry=%#x, inc DirectEntryPtr=%#x", cachedEntry, inc.DirectEntryPtr)
	}

	if err := tm.CompileTier2(inc); err != nil {
		t.Fatalf("CompileTier2(inc): %v", err)
	}
	if uintptr(cachedEntry) == inc.DirectEntryPtr {
		t.Fatalf("Tier 2 promotion did not publish a new direct entry: %#x", inc.DirectEntryPtr)
	}

	inc.EnteredTier2 = 0
	results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("CallValue(caller): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 42 {
		t.Fatalf("caller result=%v, want int 42", results)
	}
	if inc.EnteredTier2 == 0 {
		t.Fatalf("Tier 1 call IC reused stale baseline direct entry %#x after version change", cachedEntry)
	}
}

func TestTier1CallICNoFilterClosureBenchDoesNotCrash(t *testing.T) {
	t.Setenv("GSCRIPT_TIER2_NO_FILTER", "1")

	src, err := os.ReadFile("../../benchmarks/suite/closure_bench.gs")
	if err != nil {
		t.Fatalf("read closure_bench.gs: %v", err)
	}
	top := compileProto(t, string(src))
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	tm := NewTieringManager()
	v.SetMethodJIT(tm)

	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute closure_bench with no-filter JIT: %v", err)
	}
	mapArray := findProtoByName(top, "map_array")
	if mapArray == nil {
		t.Fatal("map_array proto not found")
	}
	if !mapArray.Tier2Promoted || mapArray.EnteredTier2 == 0 {
		t.Fatalf("map_array did not exercise Tier 2 direct-entry version path: promoted=%v entered=%d",
			mapArray.Tier2Promoted, mapArray.EnteredTier2)
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
	setFuncProtoTier2DirectEntries(inc, 0, inc.Tier2DirectEntryPtr)
	inc.CallCount = 100

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

func TestTier2TailCallICUsesTier2EntryWhenGenericEntryCleared(t *testing.T) {
	src := `
func inc(n) {
    return n + 1
}
func caller(f, n) {
    return f(n)
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
	setFuncProtoTier2DirectEntries(inc, 0, inc.Tier2DirectEntryPtr)
	inc.CallCount = 100

	results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("CallValue(caller): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 41 {
		t.Fatalf("caller result=%v, want int 41", results)
	}
	if inc.EnteredTier2 == 0 || caller.EnteredTier2 == 0 {
		t.Fatalf("expected both protos to enter Tier 2, inc=%d caller=%d",
			inc.EnteredTier2, caller.EnteredTier2)
	}
	if got := tm.ExitStats().ByExitCode["ExitCallExit"]; got != 0 {
		t.Fatalf("generic DirectEntryPtr clear forced tail ExitCallExit count=%d; want 0", got)
	}
}

func TestTier2CallICFallsBackWhenEntryVersionCleared(t *testing.T) {
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

	fnInc := v.GetGlobal("inc")
	fnCaller := v.GetGlobal("caller")
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(inc); err != nil {
		t.Fatalf("CompileTier2(inc): %v", err)
	}
	if err := tm.CompileTier2(caller); err != nil {
		t.Fatalf("CompileTier2(caller): %v", err)
	}

	if results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)}); err != nil {
		t.Fatalf("warm CallValue(caller): %v", err)
	} else if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 42 {
		t.Fatalf("warm caller result=%v, want int 42", results)
	}

	inc.EnteredTier2 = 0
	tm.disableTier2AfterRuntimeDeopt(inc, "test")

	results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("CallValue(caller): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 42 {
		t.Fatalf("caller result=%v, want int 42", results)
	}
	if inc.EnteredTier2 != 0 {
		t.Fatalf("stale cached direct entry was used after clear; EnteredTier2=%d", inc.EnteredTier2)
	}
	if got := tm.ExitStats().ByExitCode["ExitCallExit"]; got == 0 {
		t.Fatalf("cleared direct entries did not force call fallback; ExitCallExit=%d", got)
	}
}

func TestTier2TailCallICFallsBackWhenEntryVersionCleared(t *testing.T) {
	src := `
func inc(n) {
    return n + 1
}
func caller(f, n) {
    return f(n)
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

	fnInc := v.GetGlobal("inc")
	fnCaller := v.GetGlobal("caller")
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(inc); err != nil {
		t.Fatalf("CompileTier2(inc): %v", err)
	}
	if err := tm.CompileTier2(caller); err != nil {
		t.Fatalf("CompileTier2(caller): %v", err)
	}

	if results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)}); err != nil {
		t.Fatalf("warm CallValue(caller): %v", err)
	} else if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 41 {
		t.Fatalf("warm caller result=%v, want int 41", results)
	}

	inc.EnteredTier2 = 0
	tm.disableTier2AfterRuntimeDeopt(inc, "test")

	results, err := v.CallValue(fnCaller, []runtime.Value{fnInc, runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("CallValue(caller): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 41 {
		t.Fatalf("caller result=%v, want int 41", results)
	}
	if inc.EnteredTier2 != 0 {
		t.Fatalf("stale cached tail direct entry was used after clear; EnteredTier2=%d", inc.EnteredTier2)
	}
	if got := tm.ExitStats().ByExitCode["ExitCallExit"]; got == 0 {
		t.Fatalf("cleared direct entries did not force tail-call fallback; ExitCallExit=%d", got)
	}
}

func TestTier2NativeCallDoesNotReplayCalleeSideEffectsBeforeExit(t *testing.T) {
	src := `
state := {x: 0}

func bump_then_exit(t) {
    t.x = t.x + 1
    tmp := {}
    tmp[1] = 7
    return t.x + tmp[1] - 7
}

func caller(f, t) {
    return f(t)
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	callee := findProtoByName(top, "bump_then_exit")
	caller := findProtoByName(top, "caller")
	if callee == nil || caller == nil {
		t.Fatalf("missing protos: callee=%v caller=%v", callee != nil, caller != nil)
	}

	fnCallee := v.GetGlobal("bump_then_exit")
	fnCaller := v.GetGlobal("caller")
	state := v.GetGlobal("state")
	if fnCallee.IsNil() || fnCaller.IsNil() || !state.IsTable() {
		t.Fatalf("missing globals: callee=%v caller=%v state=%v", fnCallee, fnCaller, state)
	}
	if _, err := v.CallValue(fnCallee, []runtime.Value{state}); err != nil {
		t.Fatalf("warm callee field cache: %v", err)
	}
	state.Table().RawSetString("x", runtime.IntValue(0))

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(callee); err != nil {
		t.Fatalf("CompileTier2(callee): %v", err)
	}
	if callee.DirectEntryPtr != 0 || callee.Tier2DirectEntryPtr == 0 {
		t.Fatalf("unsafe callee entries published incorrectly: direct=%#x tier2=%#x",
			callee.DirectEntryPtr, callee.Tier2DirectEntryPtr)
	}
	if err := tm.CompileTier2(caller); err != nil {
		t.Fatalf("CompileTier2(caller): %v", err)
	}

	results, err := v.CallValue(fnCaller, []runtime.Value{fnCallee, state})
	if err != nil {
		t.Fatalf("CallValue(caller): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 1 {
		t.Fatalf("caller result=%v, want int 1", results)
	}
	slot := state.Table().RawGetString("x")
	if !slot.IsInt() || slot.Int() != 1 {
		t.Fatalf("state.x=%v, want int 1", slot)
	}
}

func TestTier2RecursiveNativeCallDoesNotReplaySideEffectsBeforeExit(t *testing.T) {
	src := `
state := {x: 0}

func rec_bump_then_exit(t, n) {
    if n <= 0 { return t.x }
    t.x = t.x + 1
    tmp := {}
    tmp[1] = 7
    child := rec_bump_then_exit(t, n - 1)
    return child + tmp[1] - 7
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	rec := findProtoByName(top, "rec_bump_then_exit")
	if rec == nil {
		t.Fatal("rec_bump_then_exit proto not found")
	}
	fnRec := v.GetGlobal("rec_bump_then_exit")
	state := v.GetGlobal("state")
	if fnRec.IsNil() || !state.IsTable() {
		t.Fatalf("missing globals: rec=%v state=%v", fnRec, state)
	}
	if _, err := v.CallValue(fnRec, []runtime.Value{state, runtime.IntValue(1)}); err != nil {
		t.Fatalf("warm recursive field cache: %v", err)
	}
	state.Table().RawSetString("x", runtime.IntValue(0))

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(rec); err != nil {
		t.Fatalf("CompileTier2(rec): %v", err)
	}
	if rec.DirectEntryPtr != 0 || rec.Tier2DirectEntryPtr == 0 {
		t.Fatalf("unsafe recursive entries published incorrectly: direct=%#x tier2=%#x",
			rec.DirectEntryPtr, rec.Tier2DirectEntryPtr)
	}

	results, err := v.CallValue(fnRec, []runtime.Value{state, runtime.IntValue(2)})
	if err != nil {
		t.Fatalf("CallValue(rec): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 2 {
		t.Fatalf("recursive result=%v, want int 2", results)
	}
	slot := state.Table().RawGetString("x")
	if !slot.IsInt() || slot.Int() != 2 {
		t.Fatalf("state.x=%v, want int 2", slot)
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
