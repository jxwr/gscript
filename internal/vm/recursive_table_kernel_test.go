package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestRecursiveTableKernelRecognizesStructuralBuilderAndFold(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, recursiveTableKernelProgram)
	defer vm.Close()

	makePair := findVMTestProtoByName(proto, "makePair")
	if makePair == nil {
		t.Fatal("makePair proto not found")
	}
	if !IsFixedRecursiveTableBuilderKernelProto(makePair) {
		dumpVMTestProtoBytecode(t, makePair)
		t.Fatal("makePair should qualify for VM recursive table builder kernel")
	}

	countPair := findVMTestProtoByName(proto, "countPair")
	if countPair == nil {
		t.Fatal("countPair proto not found")
	}
	if !IsFixedRecursiveTableFoldKernelProto(countPair) {
		dumpVMTestProtoBytecode(t, countPair)
		t.Fatal("countPair should qualify for VM recursive table fold kernel")
	}
}

func TestRecursiveTableKernelUsesBytecodeSelfGlobalInsteadOfProtoName(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, recursiveTableKernelProgram)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	vm.SetMethodJIT(recursiveTableKernelFakeJIT{})

	makePair, ok := closureFromValue(vm.GetGlobal("makePair"))
	if !ok {
		t.Fatal("makePair closure not found")
	}
	countPair, ok := closureFromValue(vm.GetGlobal("countPair"))
	if !ok {
		t.Fatal("countPair closure not found")
	}
	makePair.Proto.Name = "renamedBuilderForDebug"
	countPair.Proto.Name = "renamedFoldForDebug"
	makePair.Proto.Tier2Promoted = true
	countPair.Proto.Tier2Promoted = true

	if !IsFixedRecursiveTableBuilderKernelProto(makePair.Proto) {
		t.Fatal("builder recognizer should use recursive GETGLOBAL, not proto.Name")
	}
	if !IsFixedRecursiveTableFoldKernelProto(countPair.Proto) {
		t.Fatal("fold recognizer should use recursive GETGLOBAL, not proto.Name")
	}
	handled, roots, err := vm.tryRunValueWholeCallKernel(makePair, []runtime.Value{runtime.IntValue(3)})
	if err != nil || !handled || len(roots) != 1 {
		t.Fatalf("renamed builder kernel = handled=%v values=%v err=%v", handled, roots, err)
	}
	handled, results, err := vm.tryRunValueWholeCallKernel(countPair, roots)
	if err != nil || !handled || len(results) != 1 || !results[0].IsInt() || results[0].Int() != 77 {
		t.Fatalf("renamed fold kernel = handled=%v values=%v err=%v, want 77", handled, results, err)
	}
}

func TestRecursiveTableKernelWholeCallCorrectness(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, recursiveTableKernelProgram)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	vm.SetMethodJIT(recursiveTableKernelFakeJIT{})

	makePair, ok := closureFromValue(vm.GetGlobal("makePair"))
	if !ok {
		t.Fatal("makePair closure not found")
	}
	makePair.Proto.Tier2Promoted = true
	handled, roots, err := vm.tryRunValueWholeCallKernel(makePair, []runtime.Value{runtime.IntValue(4)})
	if err != nil {
		t.Fatalf("builder kernel error: %v", err)
	}
	if !handled || len(roots) != 1 || !roots[0].IsTable() {
		t.Fatalf("builder kernel result = handled=%v values=%v, want one table", handled, roots)
	}
	if _, _, _, ok := roots[0].Table().LazyRecursiveTablePureInfo(); !ok {
		t.Fatal("builder kernel should return a pure lazy recursive table")
	}

	countPair, ok := closureFromValue(vm.GetGlobal("countPair"))
	if !ok {
		t.Fatal("countPair closure not found")
	}
	countPair.Proto.Tier2Promoted = true
	handled, results, err := vm.tryRunValueWholeCallKernel(countPair, roots)
	if err != nil {
		t.Fatalf("fold kernel error: %v", err)
	}
	if !handled || len(results) != 1 || !results[0].IsInt() || results[0].Int() != 157 {
		t.Fatalf("fold kernel result = handled=%v values=%v, want 157", handled, results)
	}
	if _, _, _, ok := roots[0].Table().LazyRecursiveTableInfo(); !ok {
		t.Fatal("fold kernel should consume the lazy tree without materializing it")
	}
}

func TestRecursiveTableKernelFallsBackWhenSelfGlobalChanges(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, recursiveTableKernelProgram+`
func replacement(node) {
	return 100
}
`)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	vm.SetMethodJIT(recursiveTableKernelFakeJIT{})

	makePair, ok := closureFromValue(vm.GetGlobal("makePair"))
	if !ok {
		t.Fatal("makePair closure not found")
	}
	makePair.Proto.Tier2Promoted = true
	handled, roots, err := vm.tryRunValueWholeCallKernel(makePair, []runtime.Value{runtime.IntValue(2)})
	if err != nil || !handled || len(roots) != 1 {
		t.Fatalf("builder kernel = handled=%v values=%v err=%v, want root", handled, roots, err)
	}

	oldCountPair, ok := closureFromValue(vm.GetGlobal("countPair"))
	if !ok {
		t.Fatal("countPair closure not found")
	}
	oldCountPair.Proto.Tier2Promoted = true
	vm.SetGlobal("countPair", vm.GetGlobal("replacement"))
	handled, _, err = vm.tryRunValueWholeCallKernel(oldCountPair, roots)
	if err != nil {
		t.Fatalf("fallback probe error: %v", err)
	}
	if handled {
		t.Fatal("fold kernel must fall back after the recursive self global is rebound")
	}
}

func TestRecursiveTableBuildFoldRegionComputesWithoutLazyRoot(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, recursiveTableKernelProgram+`
func fused(depth) {
	return countPair(makePair(depth))
}
`)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	vm.SetMethodJIT(recursiveTableKernelFakeJIT{})

	fused := findVMTestProtoByName(proto, "fused")
	if fused == nil {
		t.Fatal("fused proto not found")
	}
	if len(fused.Code) != 7 || DecodeOp(fused.Code[3]) != OP_CALL {
		dumpVMTestProtoBytecode(t, fused)
		t.Fatal("unexpected fused bytecode shape")
	}

	makePair, ok := closureFromValue(vm.GetGlobal("makePair"))
	if !ok {
		t.Fatal("makePair closure not found")
	}
	makePair.Proto.Tier2Promoted = true
	countPairClosure, ok := closureFromValue(vm.GetGlobal("countPair"))
	if !ok {
		t.Fatal("countPair closure not found")
	}
	countPairClosure.Proto.Tier2Promoted = true
	countPair := vm.GetGlobal("countPair")
	vm.regs[0] = runtime.IntValue(4)
	vm.regs[1] = countPair
	vm.regs[3] = vm.GetGlobal("makePair")
	vm.regs[4] = runtime.IntValue(4)
	frame := &CallFrame{closure: &Closure{Proto: fused}, pc: 4}

	handled, err := vm.tryRecursiveTableBuildFoldRegion(frame, 0, makePair, 3, 1, 2)
	if err != nil {
		t.Fatalf("region kernel error: %v", err)
	}
	if !handled {
		t.Fatal("region kernel did not handle countPair(makePair(depth))")
	}
	if got := vm.regs[1]; !got.IsInt() || got.Int() != 157 {
		t.Fatalf("region result = %v, want 157", got)
	}
	if frame.pc != 6 {
		t.Fatalf("region pc = %d, want 6", frame.pc)
	}
	if vm.regs[3].IsTable() {
		t.Fatal("region kernel should not materialize a lazy root in the builder result slot")
	}
}

const recursiveTableKernelProgram = `
func makePair(depth) {
	if depth == 0 {
		return {first: nil, second: nil}
	}
	return {first: makePair(depth - 1), second: makePair(depth - 1)}
}

func countPair(node) {
	if node.first == nil { return 7 }
	return 3 + countPair(node.first) + countPair(node.second)
}
`

func findVMTestProtoByName(proto *FuncProto, name string) *FuncProto {
	if proto == nil {
		return nil
	}
	if proto.Name == name {
		return proto
	}
	for _, child := range proto.Protos {
		if found := findVMTestProtoByName(child, name); found != nil {
			return found
		}
	}
	return nil
}

func dumpVMTestProtoBytecode(t *testing.T, proto *FuncProto) {
	t.Helper()
	for pc, inst := range proto.Code {
		t.Logf("[%02d] %s A=%d B=%d C=%d Bx=%d sBx=%d", pc, OpName(DecodeOp(inst)),
			DecodeA(inst), DecodeB(inst), DecodeC(inst), DecodeBx(inst), DecodesBx(inst))
	}
}

type recursiveTableKernelFakeJIT struct{}

func (recursiveTableKernelFakeJIT) TryCompile(*FuncProto) interface{} { return nil }

func (recursiveTableKernelFakeJIT) Execute(interface{}, []runtime.Value, int, *FuncProto) ([]runtime.Value, error) {
	return nil, nil
}

func (recursiveTableKernelFakeJIT) SetCallVM(*VM) {}
