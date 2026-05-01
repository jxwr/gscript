//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"math"
	"os"
	"strings"
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

func TestTier2FixedShapeEntryGuardMismatchFallsBackFromNormalEntry(t *testing.T) {
	src := `
func makePair(x, y) {
    return {left: x, right: y}
}
func walk(pair) {
    return pair.left - pair.right
}
func driver() {
    return walk(makePair(10, 20))
}
result := driver()
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	walk := findProtoByName(top, "walk")
	if walk == nil {
		t.Fatal("walk proto not found")
	}
	fnWalk := v.GetGlobal("walk")
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(walk); err != nil {
		t.Fatalf("CompileTier2(walk): %v", err)
	}

	reversed := runtime.NewTable()
	reversed.RawSetString("right", runtime.IntValue(20))
	reversed.RawSetString("left", runtime.IntValue(10))
	if reversed.ShapeID() == runtime.GetShapeID([]string{"left", "right"}) {
		t.Fatal("test table unexpectedly has the guarded shape")
	}

	results, err := v.CallValue(fnWalk, []runtime.Value{runtime.TableValue(reversed)})
	if err != nil {
		t.Fatalf("CallValue(walk mismatched shape): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != -10 {
		t.Fatalf("walk(reversed)=%v, want int -10", results)
	}
	if got := tm.ExitStats().ByExitCode["ExitDeopt"]; got == 0 {
		t.Fatal("fixed-shape entry mismatch did not trip the Tier 2 entry guard")
	}
}

func TestTier2FixedShapeEntryGuardMismatchFallsBackFromNativeCaller(t *testing.T) {
	src := `
func makePair(x, y) {
    return {left: x, right: y}
}
func walk(pair) {
    return pair.left - pair.right
}
func callWalk(f, pair) {
    return f(pair)
}
func driver() {
    return walk(makePair(10, 20))
}
result := driver()
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	walk := findProtoByName(top, "walk")
	callWalk := findProtoByName(top, "callWalk")
	if walk == nil || callWalk == nil {
		t.Fatalf("missing protos: walk=%v callWalk=%v", walk != nil, callWalk != nil)
	}
	fnWalk := v.GetGlobal("walk")
	fnCallWalk := v.GetGlobal("callWalk")
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(walk); err != nil {
		t.Fatalf("CompileTier2(walk): %v", err)
	}
	if err := tm.CompileTier2(callWalk); err != nil {
		t.Fatalf("CompileTier2(callWalk): %v", err)
	}

	reversed := runtime.NewTable()
	reversed.RawSetString("right", runtime.IntValue(20))
	reversed.RawSetString("left", runtime.IntValue(10))

	results, err := v.CallValue(fnCallWalk, []runtime.Value{fnWalk, runtime.TableValue(reversed)})
	if err != nil {
		t.Fatalf("CallValue(callWalk mismatched shape): %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != -10 {
		t.Fatalf("callWalk(walk, reversed)=%v, want int -10", results)
	}
	if got := tm.ExitStats().ByExitCode["ExitCallExit"]; got == 0 {
		t.Fatal("native caller did not materialize the boxed fallback call frame")
	}
}

func TestTier2NativeCalleeTableBoundsMissResumesVMBehavior(t *testing.T) {
	src := `
func read_int(t, i) {
    return t[i]
}
func call_read(f, t, i) {
    return f(t, i)
}
warm := {}
warm[1] = 10
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	readInt := findProtoByName(top, "read_int")
	callRead := findProtoByName(top, "call_read")
	if readInt == nil || callRead == nil {
		t.Fatalf("missing protos: read_int=%v call_read=%v", readInt != nil, callRead != nil)
	}
	fnCaller := v.GetGlobal("call_read")
	warm := v.GetGlobal("warm")
	fnRead := v.GetGlobal("read_int")
	if _, err := v.CallValue(fnRead, []runtime.Value{warm, runtime.IntValue(1)}); err != nil {
		t.Fatalf("warm read_int: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(readInt); err != nil {
		t.Fatalf("CompileTier2(read_int): %v", err)
	}
	if err := tm.CompileTier2(callRead); err != nil {
		t.Fatalf("CompileTier2(call_read): %v", err)
	}
	readInt.CallCount = 100
	setFuncProtoTier2DirectEntries(readInt, 0, readInt.Tier2DirectEntryPtr)
	v.EnsureRegs(1024)

	results, err := v.CallValue(fnCaller, []runtime.Value{fnRead, warm, runtime.IntValue(99)})
	if err != nil {
		t.Fatalf("bounds miss inside native callee should resume VM behavior: %v", err)
	}
	if len(results) != 1 || !results[0].IsNil() {
		t.Fatalf("bounds miss result=%v, want nil", results)
	}
	if readInt.EnteredTier2 == 0 || callRead.EnteredTier2 == 0 {
		t.Fatalf("expected both protos to enter Tier 2, read_int=%d call_read=%d",
			readInt.EnteredTier2, callRead.EnteredTier2)
	}
}

func TestTier2NativeCalleeTableKindMissResumesVMBehavior(t *testing.T) {
	src := `
func read_num(t, i) {
    return t[i] + 1
}
func call_read(f, t, i) {
    return f(t, i) + 0
}
ints := {}
ints[1] = 10
floats := {}
floats[1] = 1.5
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	readNum := findProtoByName(top, "read_num")
	callRead := findProtoByName(top, "call_read")
	if readNum == nil || callRead == nil {
		t.Fatalf("missing protos: read_num=%v call_read=%v", readNum != nil, callRead != nil)
	}
	fnCaller := v.GetGlobal("call_read")
	fnRead := v.GetGlobal("read_num")
	ints := v.GetGlobal("ints")
	floats := v.GetGlobal("floats")
	if _, err := v.CallValue(fnRead, []runtime.Value{ints, runtime.IntValue(1)}); err != nil {
		t.Fatalf("warm read_num: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(readNum); err != nil {
		t.Fatalf("CompileTier2(read_num): %v", err)
	}
	if err := tm.CompileTier2(callRead); err != nil {
		t.Fatalf("CompileTier2(call_read): %v", err)
	}
	readNum.CallCount = 100
	setFuncProtoTier2DirectEntries(readNum, 0, readNum.Tier2DirectEntryPtr)
	v.EnsureRegs(1024)

	results, err := v.CallValue(fnCaller, []runtime.Value{fnRead, floats, runtime.IntValue(1)})
	if err != nil {
		t.Fatalf("kind miss inside native callee should resume VM behavior: %v", err)
	}
	if len(results) != 1 || !results[0].IsFloat() || math.Abs(results[0].Float()-2.5) > 1e-9 {
		t.Fatalf("kind miss result=%v, want float 2.5", results)
	}
	if readNum.EnteredTier2 == 0 || callRead.EnteredTier2 == 0 {
		t.Fatalf("expected both protos to enter Tier 2, read_num=%d call_read=%d",
			readNum.EnteredTier2, callRead.EnteredTier2)
	}
}

func TestTier2NativeCalleeShapeMetatableMissResumesVMBehavior(t *testing.T) {
	src := `
func read_x(t) {
    return t.x + 1
}
func call_read(f, t) {
    return f(t) + 0
}
own := {x: 10}
proxy := {}
setmetatable(proxy, {__index: {x: 41}})
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	readX := findProtoByName(top, "read_x")
	callRead := findProtoByName(top, "call_read")
	if readX == nil || callRead == nil {
		t.Fatalf("missing protos: read_x=%v call_read=%v", readX != nil, callRead != nil)
	}
	fnCaller := v.GetGlobal("call_read")
	fnRead := v.GetGlobal("read_x")
	own := v.GetGlobal("own")
	proxy := v.GetGlobal("proxy")
	if _, err := v.CallValue(fnRead, []runtime.Value{own}); err != nil {
		t.Fatalf("warm read_x: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(readX); err != nil {
		t.Fatalf("CompileTier2(read_x): %v", err)
	}
	if err := tm.CompileTier2(callRead); err != nil {
		t.Fatalf("CompileTier2(call_read): %v", err)
	}
	readX.CallCount = 100
	setFuncProtoTier2DirectEntries(readX, 0, readX.Tier2DirectEntryPtr)
	v.EnsureRegs(1024)

	results, err := v.CallValue(fnCaller, []runtime.Value{fnRead, proxy})
	if err != nil {
		t.Fatalf("shape/metatable miss inside native callee should resume VM behavior: %v", err)
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 42 {
		t.Fatalf("shape/metatable miss result=%v, want int 42", results)
	}
	if readX.EnteredTier2 == 0 || callRead.EnteredTier2 == 0 {
		t.Fatalf("expected both protos to enter Tier 2, read_x=%d call_read=%d",
			readX.EnteredTier2, callRead.EnteredTier2)
	}
}

func TestTier2NativeCalleeNonTableMissResumesVMError(t *testing.T) {
	src := `
func read_x(t) {
    return t.x
}
func call_read(f, t) {
    return f(t)
}
own := {x: 10}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	readX := findProtoByName(top, "read_x")
	callRead := findProtoByName(top, "call_read")
	if readX == nil || callRead == nil {
		t.Fatalf("missing protos: read_x=%v call_read=%v", readX != nil, callRead != nil)
	}
	fnCaller := v.GetGlobal("call_read")
	fnRead := v.GetGlobal("read_x")
	own := v.GetGlobal("own")
	if _, err := v.CallValue(fnRead, []runtime.Value{own}); err != nil {
		t.Fatalf("warm read_x: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(readX); err != nil {
		t.Fatalf("CompileTier2(read_x): %v", err)
	}
	if err := tm.CompileTier2(callRead); err != nil {
		t.Fatalf("CompileTier2(call_read): %v", err)
	}
	readX.CallCount = 100
	setFuncProtoTier2DirectEntries(readX, 0, readX.Tier2DirectEntryPtr)
	v.EnsureRegs(1024)

	_, err := v.CallValue(fnCaller, []runtime.Value{fnRead, runtime.IntValue(7)})
	if err == nil {
		t.Fatal("non-table miss inside native callee returned successfully, want VM table-get error")
	}
	if !strings.Contains(err.Error(), "attempt to index") {
		t.Fatalf("non-table miss error=%v, want VM table-get error", err)
	}
	if readX.EnteredTier2 == 0 || callRead.EnteredTier2 == 0 {
		t.Fatalf("expected both protos to enter Tier 2, read_x=%d call_read=%d",
			readX.EnteredTier2, callRead.EnteredTier2)
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

func TestTier2StaticLeafCallSkipsNativeCallDepthTraffic(t *testing.T) {
	src := `
func leaf(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
func driver(n, reps) {
    result := 0
    for r := 1; r <= reps; r++ {
        result = leaf(n)
    }
    return result
}
`
	top := compileTop(t, src)
	leaf := findProtoByName(top, "leaf")
	driver := findProtoByName(top, "driver")
	if leaf == nil || driver == nil {
		t.Fatalf("missing protos: leaf=%v driver=%v", leaf != nil, driver != nil)
	}

	globals := map[string]*vm.FuncProto{"leaf": leaf}
	fn := BuildGraph(driver)
	fn, _, err := RunTier2Pipeline(fn, &Tier2PipelineOpts{
		InlineGlobals: globals,
		InlineMaxSize: 1,
	})
	if err != nil {
		t.Fatalf("RunTier2Pipeline(driver): %v", err)
	}
	call := singleCallTo(t, fn, "leaf", globals)
	if call == nil {
		t.Fatalf("leaf call not found\nIR:\n%s", Print(fn))
	}

	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile(driver): %v", err)
	}
	defer cf.Code.Free()

	depthAccesses := countNativeCallDepthAccessesForIRInstr(cf, call.ID)
	if depthAccesses != 0 {
		t.Fatalf("static leaf call emitted %d NativeCallDepth access(es), want 0", depthAccesses)
	}
}

func countNativeCallDepthAccessesForIRInstr(cf *CompiledFunction, instrID int) int {
	code := unsafeCodeSlice(cf)
	var count int
	for _, r := range cf.InstrCodeRanges {
		if r.InstrID != instrID || r.Pass != "normal" {
			continue
		}
		start, end := r.CodeStart, r.CodeEnd
		if start < 0 {
			start = 0
		}
		if end > len(code) {
			end = len(code)
		}
		for off := start; off+4 <= end; off += 4 {
			if isCtxNativeCallDepthAccess(binary.LittleEndian.Uint32(code[off : off+4])) {
				count++
			}
		}
	}
	return count
}
