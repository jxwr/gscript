//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"testing"
	"unsafe"

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
