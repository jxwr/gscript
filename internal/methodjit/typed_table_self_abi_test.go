//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTypedTableSelfABI_CheckTreeUsesNativeRecursiveCalls(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

root := makeTree(5)
`
	const want = 63
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		t.Fatal("checkTree proto not found")
	}
	checkTree.EnsureFeedback()
	fn := v.GetGlobal("checkTree")
	root := v.GetGlobal("root")
	if fn.IsNil() || root.IsNil() {
		t.Fatalf("missing globals: checkTree=%v root=%v", fn, root)
	}

	warm, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("warm checkTree: %v", err)
	}
	if len(warm) != 1 || !warm[0].IsInt() || warm[0].Int() != want {
		t.Fatalf("warm checkTree(root)=%v, want int %d", warm, want)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(checkTree); err != nil {
		t.Fatalf("CompileTier2(checkTree): %v", err)
	}
	cf := tm.tier2Compiled[checkTree]
	if cf == nil {
		t.Fatal("missing Tier 2 compiled checkTree")
	}
	if !cf.TypedSelfABI.Eligible {
		t.Fatalf("compiled checkTree typed ABI rejected: %s", cf.TypedSelfABI.RejectWhy)
	}
	if cf.TypedEntryOffset == 0 {
		t.Fatal("checkTree compiled without typed self entry")
	}

	checkTree.EnteredTier2 = 0
	got, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("Tier2 checkTree: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != want {
		t.Fatalf("Tier2 checkTree(root)=%v, want int %d", got, want)
	}
	if checkTree.EnteredTier2 == 0 {
		t.Fatal("checkTree did not enter Tier 2")
	}
	if exits := tm.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
		t.Fatalf("typed self recursion used boxed call exit %d times", exits)
	}
	if exits := tm.ExitStats().ByExitCode["ExitDeopt"]; exits != 0 {
		t.Fatalf("typed self recursion deoptimized %d times", exits)
	}
}

func TestTypedTableSelfABI_CheckTreeColdFieldExitKeepsRecursiveFrame(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

root := makeTree(5)
`
	const want = 63
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		t.Fatal("checkTree proto not found")
	}
	checkTree.EnsureFeedback()
	fn := v.GetGlobal("checkTree")
	root := v.GetGlobal("root")
	if fn.IsNil() || root.IsNil() {
		t.Fatalf("missing globals: checkTree=%v root=%v", fn, root)
	}

	warm, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("warm checkTree: %v", err)
	}
	if len(warm) != 1 || !warm[0].IsInt() || warm[0].Int() != want {
		t.Fatalf("warm checkTree(root)=%v, want int %d", warm, want)
	}
	checkTree.FieldCache = nil
	checkTree.FieldAccessFeedback = nil

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(checkTree); err != nil {
		t.Fatalf("CompileTier2(checkTree): %v", err)
	}
	got, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("Tier2 cold-cache checkTree: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != want {
		t.Fatalf("Tier2 cold-cache checkTree(root)=%v, want int %d", got, want)
	}
	if exits := tm.ExitStats().ByExitCode["ExitTableExit"]; exits == 0 {
		t.Fatal("expected cold field-cache table exits")
	}
	if exits := tm.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
		t.Fatalf("typed self recursion used boxed call exit %d times", exits)
	}
	if exits := tm.ExitStats().ByExitCode["ExitDeopt"]; exits != 0 {
		t.Fatalf("typed self recursion deoptimized %d times", exits)
	}
}

func TestTypedTableSelfABI_NestedShapeMissUsesNativeExitStack(t *testing.T) {
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {leaf: 1}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

root := makeTree(4)
`
	const want = 31
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		t.Fatal("checkTree proto not found")
	}
	checkTree.EnsureFeedback()
	fn := v.GetGlobal("checkTree")
	root := v.GetGlobal("root")
	cl, ok := vmClosureFromValue(fn)
	if !ok || cl == nil {
		t.Fatalf("checkTree global is not a closure: %v", fn)
	}
	warm, err := v.CallValue(fn, []runtime.Value{root})
	if err != nil {
		t.Fatalf("warm checkTree: %v", err)
	}
	if len(warm) != 1 || !warm[0].IsInt() || warm[0].Int() != want {
		t.Fatalf("warm checkTree(root)=%v, want int %d", warm, want)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(checkTree); err != nil {
		t.Fatalf("CompileTier2(checkTree): %v", err)
	}
	cf := tm.tier2Compiled[checkTree]
	if cf == nil || !cf.TypedSelfABI.Eligible {
		t.Fatalf("compiled checkTree missing typed ABI: cf=%v", cf)
	}

	checkTree.FieldCache = nil
	regs := v.EnsureRegs(cf.numRegs + 1)
	regs[0] = root
	if !v.PushFrame(cl, 0) {
		t.Fatal("PushFrame(checkTree) failed")
	}
	got, err := tm.executeTier2(cf, regs, 0, checkTree)
	v.PopFrame()
	if err != nil {
		t.Fatalf("direct Tier2 checkTree: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != want {
		t.Fatalf("direct Tier2 checkTree(root)=%v, want int %d", got, want)
	}
}

func TestTypedTableSelfABI_QuicksortZeroResultUsesNativeRecursiveCalls(t *testing.T) {
	src := `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

func make_random_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr
}

func is_sorted(arr, n) {
    for i := 1; i < n; i++ {
        if arr[i] > arr[i + 1] { return false }
    }
    return true
}
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}

	quicksort := findProtoByName(top, "quicksort")
	if quicksort == nil {
		t.Fatal("quicksort proto not found")
	}
	quicksort.EnsureFeedback()
	qsFn := v.GetGlobal("quicksort")
	makeFn := v.GetGlobal("make_random_array")
	sortedFn := v.GetGlobal("is_sorted")
	if qsFn.IsNil() || makeFn.IsNil() || sortedFn.IsNil() {
		t.Fatalf("missing globals: quicksort=%v make=%v sorted=%v", qsFn, makeFn, sortedFn)
	}

	const n = int64(64)
	warmArr, err := v.CallValue(makeFn, []runtime.Value{runtime.IntValue(n), runtime.IntValue(42)})
	if err != nil || len(warmArr) != 1 {
		t.Fatalf("make warm array: results=%v err=%v", warmArr, err)
	}
	if _, err := v.CallValue(qsFn, []runtime.Value{warmArr[0], runtime.IntValue(1), runtime.IntValue(n)}); err != nil {
		t.Fatalf("warm quicksort: %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(quicksort); err != nil {
		t.Fatalf("CompileTier2(quicksort): %v", err)
	}
	cf := tm.tier2Compiled[quicksort]
	if cf == nil || !cf.TypedSelfABI.Eligible {
		t.Fatalf("compiled quicksort missing typed ABI: cf=%v", cf)
	}
	if cf.TypedSelfABI.Return != SpecializedABIReturnNone {
		t.Fatalf("quicksort typed return=%d want none", cf.TypedSelfABI.Return)
	}

	arr, err := v.CallValue(makeFn, []runtime.Value{runtime.IntValue(n), runtime.IntValue(99)})
	if err != nil || len(arr) != 1 {
		t.Fatalf("make jit array: results=%v err=%v", arr, err)
	}
	quicksort.EnteredTier2 = 0
	if _, err := v.CallValue(qsFn, []runtime.Value{arr[0], runtime.IntValue(1), runtime.IntValue(n)}); err != nil {
		t.Fatalf("Tier2 quicksort: %v", err)
	}
	if quicksort.EnteredTier2 == 0 {
		t.Fatal("quicksort did not enter Tier 2")
	}
	sorted, err := v.CallValue(sortedFn, []runtime.Value{arr[0], runtime.IntValue(n)})
	if err != nil {
		t.Fatalf("is_sorted: %v", err)
	}
	if len(sorted) != 1 || !sorted[0].IsBool() || !sorted[0].Bool() {
		t.Fatalf("sorted=%v, want true", sorted)
	}
	if exits := tm.ExitStats().ByExitCode["ExitCallExit"]; exits != 0 {
		t.Fatalf("zero-result typed self recursion used boxed call exit %d times", exits)
	}
}

func TestTypedTableSelfABI_TypedEntryPublishesParamHomeForExits(t *testing.T) {
	cf := compileCheckTreeTypedSelf(t)
	code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())

	sawHomeStore := false
	sawBodyBranch := false
	for pc := cf.TypedEntryOffset; pc+4 <= len(code) && pc < cf.TypedEntryOffset+240; pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		if isSTRToMRegRegsSlot(word, 0) {
			sawHomeStore = true
		}
		if isUnconditionalB(word) {
			sawBodyBranch = true
			break
		}
	}
	if !sawBodyBranch {
		t.Fatal("typed self entry did not branch to body within scan window")
	}
	if !sawHomeStore {
		t.Fatal("typed self entry must publish param slot 0 before exit-resumable body")
	}
}

func TestTypedTableSelfABI_EnsureRegisterBudgetPregrowsTypedFrames(t *testing.T) {
	cf := compileCheckTreeTypedSelf(t)
	if !cf.TypedSelfABI.Eligible {
		t.Fatalf("checkTree typed ABI rejected: %s", cf.TypedSelfABI.RejectWhy)
	}
	if cf.numRegs <= 0 {
		t.Fatalf("compiled numRegs=%d, want positive", cf.numRegs)
	}

	tm := NewTieringManager()
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	v.SetMethodJIT(tm)

	const base = 3
	regs := runtime.MakeNilSlice(base + cf.numRegs)
	grown := tm.ensureTier2RegisterBudget(cf, regs, base, cf.Proto)
	want := base + cf.numRegs*(maxNativeCallDepth+2) + 1
	if len(grown) < want {
		t.Fatalf("typed self register budget len=%d, want at least %d", len(grown), want)
	}
}

func TestTypedTableSelfABI_TypedSelfCallSavesArgsOnStackBeforeBL(t *testing.T) {
	cf := compileCheckTreeTypedSelf(t)
	code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())

	selfBLs := 0
	for pc := 0; pc+4 <= len(code); pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		target, ok := blTargetOffset(word, pc)
		if !ok || target != cf.TypedEntryOffset {
			continue
		}
		selfBLs++
		sawArgStackSave := false
		start := pc - 220
		if start < 0 {
			start = 0
		}
		for scan := start; scan < pc; scan += 4 {
			scanWord := binary.LittleEndian.Uint32(code[scan : scan+4])
			if isSTRArgToSP(scanWord) {
				sawArgStackSave = true
				break
			}
		}
		if !sawArgStackSave {
			t.Fatalf("typed self-call BL at %#x did not save typed args on native stack", pc)
		}
	}
	if selfBLs == 0 {
		t.Fatal("expected at least one typed self-call BL")
	}
}

func TestTypedTableSelfABI_TypedSelfCallGuardsEntryShapeBeforeBL(t *testing.T) {
	cf := compileIDNodeTypedSelfWithEntryGuard(t)
	code := unsafe.Slice((*byte)(cf.Code.Ptr()), cf.Code.Size())

	selfBLs := 0
	for pc := 0; pc+4 <= len(code); pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc : pc+4])
		target, ok := blTargetOffset(word, pc)
		if !ok || target != cf.TypedEntryOffset {
			continue
		}
		selfBLs++
		sawShapeGuard := false
		start := pc - 180
		if start < 0 {
			start = 0
		}
		for scan := start; scan < pc; scan += 4 {
			scanWord := binary.LittleEndian.Uint32(code[scan : scan+4])
			if isLDRWTableShapeID(scanWord) {
				sawShapeGuard = true
				break
			}
		}
		if !sawShapeGuard {
			t.Fatalf("typed self-call BL at %#x did not guard the table parameter shape before entry", pc)
		}
	}
	if selfBLs == 0 {
		t.Fatal("expected at least one typed self-call BL")
	}
}

func TestTypedTableSelfABI_RejectsUnknownRecursiveTableArg(t *testing.T) {
	src := `
func walk(node) {
    if node.left == nil {
        return 1
    }
    child := node.payload
    r := walk(child)
    return r + 0
}

goodLeaf := {left: nil, payload: nil}
goodRoot := {left: 1, payload: goodLeaf}
badRoot := {left: 1, payload: 123}
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
	fn := v.GetGlobal("walk")
	goodRoot := v.GetGlobal("goodRoot")
	badRoot := v.GetGlobal("badRoot")
	if _, err := v.CallValue(fn, []runtime.Value{goodRoot}); err != nil {
		t.Fatalf("warm walk(goodRoot): %v", err)
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(walk); err != nil {
		t.Fatalf("CompileTier2(walk): %v", err)
	}
	cf := tm.tier2Compiled[walk]
	if cf == nil {
		t.Fatal("compiled walk missing Tier 2 function")
	}
	if cf.TypedSelfABI.Eligible {
		t.Fatalf("unknown recursive table arg must not get typed ABI: %+v", cf.TypedSelfABI)
	}

	_, err := v.CallValue(fn, []runtime.Value{badRoot})
	if err == nil {
		t.Fatal("walk(badRoot) returned successfully, want VM table-get error for numeric recursive child")
	}
	if !strings.Contains(err.Error(), "attempt to index") {
		t.Fatalf("walk(badRoot) error=%v, want VM table-get error", err)
	}
}

func BenchmarkTypedTableSelfABI_CheckTreeForcedTier2CallValueSteady(b *testing.B) {
	benchTypedTableSelfCheckTree(b, false)
}

func BenchmarkTypedTableSelfABI_CheckTreeForcedTier2ColdFieldExit(b *testing.B) {
	benchTypedTableSelfCheckTree(b, true)
}

func benchTypedTableSelfCheckTree(b *testing.B, coldFieldCache bool) {
	b.Helper()
	top := compileTopB(b, typedTableSelfCheckTreeSource(8))
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		b.Fatalf("execute top: %v", err)
	}

	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		b.Fatal("checkTree proto not found")
	}
	checkTree.EnsureFeedback()
	fn := v.GetGlobal("checkTree")
	root := v.GetGlobal("root")
	if fn.IsNil() || root.IsNil() {
		b.Fatalf("missing globals: checkTree=%v root=%v", fn, root)
	}
	const want = 511
	for i := 0; i < 3; i++ {
		results, err := v.CallValue(fn, []runtime.Value{root})
		if err != nil {
			b.Fatalf("warm checkTree: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != want {
			b.Fatalf("warm checkTree(root)=%v, want int %d", results, want)
		}
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(checkTree); err != nil {
		b.Fatalf("CompileTier2(checkTree): %v", err)
	}
	cf := tm.tier2Compiled[checkTree]
	if cf == nil || !cf.TypedSelfABI.Eligible {
		b.Fatalf("compiled checkTree missing typed ABI: cf=%v", cf)
	}

	args := []runtime.Value{root}
	for i := 0; i < 10; i++ {
		if coldFieldCache {
			checkTree.FieldCache = nil
		}
		results, err := v.CallValue(fn, args)
		if err != nil {
			b.Fatalf("warm Tier2 checkTree: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != want {
			b.Fatalf("warm Tier2 checkTree(root)=%v, want int %d", results, want)
		}
	}
	if checkTree.EnteredTier2 == 0 {
		b.Fatal("forced Tier 2 checkTree was compiled but never entered")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if coldFieldCache {
			checkTree.FieldCache = nil
		}
		results, err := v.CallValue(fn, args)
		if err != nil {
			b.Fatalf("Tier2 checkTree: %v", err)
		}
		if len(results) != 1 || !results[0].IsInt() || results[0].Int() != want {
			b.Fatalf("Tier2 checkTree(root)=%v, want int %d", results, want)
		}
	}
}

func typedTableSelfCheckTreeSource(depth int) string {
	return `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

root := makeTree(` + strconv.Itoa(depth) + `)
`
}

func compileCheckTreeTypedSelf(t *testing.T) *CompiledFunction {
	t.Helper()
	src := `
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

root := makeTree(3)
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	checkTree := findProtoByName(top, "checkTree")
	if checkTree == nil {
		t.Fatal("checkTree proto not found")
	}
	checkTree.EnsureFeedback()
	fn := v.GetGlobal("checkTree")
	root := v.GetGlobal("root")
	if _, err := v.CallValue(fn, []runtime.Value{root}); err != nil {
		t.Fatalf("warm checkTree: %v", err)
	}

	tm := NewTieringManager()
	if err := tm.CompileTier2(checkTree); err != nil {
		t.Fatalf("CompileTier2(checkTree): %v", err)
	}
	cf := tm.tier2Compiled[checkTree]
	if cf == nil || cf.TypedEntryOffset <= 0 {
		t.Fatalf("missing typed entry: cf=%v", cf)
	}
	return cf
}

func compileIDNodeTypedSelfWithEntryGuard(t *testing.T) *CompiledFunction {
	t.Helper()
	src := `
func makePair(x, y) {
    return {left: x, right: y}
}

func idNode(node, depth) {
    tmp := node.left
    if depth == 0 {
        return node
    }
    child := idNode(node, depth - 1)
    if depth < 0 {
        return node
    }
    return child
}

func driver() {
    return idNode(makePair(1, 2), 2)
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
	idNode := findProtoByName(top, "idNode")
	if idNode == nil {
		t.Fatal("idNode proto not found")
	}

	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if err := tm.CompileTier2(idNode); err != nil {
		t.Fatalf("CompileTier2(idNode): %v", err)
	}
	cf := tm.tier2Compiled[idNode]
	if cf == nil {
		t.Fatal("missing Tier 2 compiled idNode")
	}
	if !cf.TypedSelfABI.Eligible {
		t.Fatalf("idNode typed ABI rejected: %s", cf.TypedSelfABI.RejectWhy)
	}
	if cf.TypedEntryOffset == 0 {
		t.Fatal("idNode compiled without typed self entry")
	}
	return cf
}

func isSTRToMRegRegsSlot(word uint32, slot int) bool {
	if slot < 0 || slot > 4095 {
		return false
	}
	return word&0xFFC003E0 == 0xF9000000|uint32(mRegRegs)<<5 &&
		((word>>10)&0xFFF) == uint32(slot)
}

func blTargetOffset(word uint32, pc int) (int, bool) {
	if !isBL(word) {
		return 0, false
	}
	imm := int32(word & 0x03FFFFFF)
	if imm&(1<<25) != 0 {
		imm |= ^int32(0x03FFFFFF)
	}
	return pc + int(imm)*4, true
}

func isLDRWTableShapeID(word uint32) bool {
	return word&0xFFC00000 == 0xB9400000 &&
		((word>>10)&0xFFF) == uint32(jit.TableOffShapeID/4)
}
