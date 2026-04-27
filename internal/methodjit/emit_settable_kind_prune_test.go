//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestEmitSetTableKnownKindPrunesUnreachableStoreKinds(t *testing.T) {
	unknownSize := compileSingleSetTableSize(t, 0)
	intSize := compileSingleSetTableSize(t, int64(vm.FBKindInt))
	mixedSize := compileSingleSetTableSize(t, int64(vm.FBKindMixed))

	if intSize >= unknownSize {
		t.Fatalf("known int SetTable code size = %d, want smaller than unknown-kind size %d", intSize, unknownSize)
	}
	if mixedSize >= intSize {
		t.Fatalf("known mixed SetTable code size = %d, want smaller than known-int size %d", mixedSize, intSize)
	}
}

func TestEmitSetTableKnownIntKeepsMixedFallback(t *testing.T) {
	src := `func f(arr, n, v) { arr[n] = v; return arr[n] }`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	forceSetTableKind(t, fn, vm.FBKindInt)

	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	cf.DeoptFunc = makeDeoptFunc(t, src, "f")
	callVM := makeCallExitVMForTest(t, src)
	defer callVM.Close()
	cf.CallVM = callVM

	tbl := runtime.NewTable()
	tbl.RawSetInt(1, runtime.IntValue(1))
	tbl.RawSetInt(2, runtime.StringValue("mixed"))
	if got := tbl.GetArrayKind(); got != runtime.ArrayMixed {
		t.Fatalf("test setup array kind = %d, want ArrayMixed", got)
	}

	result, err := cf.Execute([]runtime.Value{
		runtime.TableValue(tbl),
		runtime.IntValue(1),
		runtime.IntValue(77),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result) == 0 || !result[0].IsInt() || result[0].Int() != 77 {
		t.Fatalf("result = %v, want 77", result)
	}
	if got := tbl.RawGetInt(1); !got.IsInt() || got.Int() != 77 {
		t.Fatalf("table[1] = %v, want 77", got)
	}
}

func compileSingleSetTableSize(t *testing.T, kind int64) int {
	t.Helper()

	fn := &Function{
		Proto:   &vm.FuncProto{Name: "settable_kind_prune", NumParams: 3, MaxStack: 3},
		NumRegs: 3,
	}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b
	fn.Blocks = []*Block{b}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: b}
	key := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: b}
	val := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 2, Block: b}
	set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), key.Value(), val.Value()}, Aux2: kind, Block: b}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Block: b}
	b.Instrs = []*Instr{tbl, key, val, set, ret}

	cf, err := Compile(fn, AllocateRegisters(fn))
	if err != nil {
		t.Fatalf("Compile(kind=%d): %v", kind, err)
	}
	defer cf.Code.Free()
	return cf.Code.Size()
}

func forceSetTableKind(t *testing.T, fn *Function, kind uint8) {
	t.Helper()
	seen := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpSetTable {
				instr.Aux2 = int64(kind)
				seen = true
			}
		}
	}
	if !seen {
		t.Fatal("test source did not produce OpSetTable")
	}
}
