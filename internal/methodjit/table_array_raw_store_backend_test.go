//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTableArrayRawStoreBackend_AppendUpdatesLen(t *testing.T) {
	cf := compileRawStoreBackendFixture(t, "raw_store_append", int64(vm.FBKindInt), 1, 42, tableArrayStoreFlagAllowGrow)
	defer cf.Code.Free()

	tbl := runtime.NewTableSizedKind(4, 0, runtime.ArrayInt)
	tbl.RawSetInt(0, runtime.IntValue(0))
	if _, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)}); err != nil {
		t.Fatalf("Execute append fixture: %v", err)
	}
	if got := tbl.Len(); got != 1 {
		t.Fatalf("table len after native append = %d, want 1", got)
	}
	got := tbl.RawGetInt(1)
	if !got.IsInt() || got.Int() != 42 {
		t.Fatalf("tbl[1] after native append = %v, want 42", got)
	}
}

func TestTableArrayRawStoreBackend_SparseWithinCapacityUpdatesLen(t *testing.T) {
	cf := compileRawStoreBackendFixture(t, "raw_store_sparse", int64(vm.FBKindInt), 3, 99, tableArrayStoreFlagAllowGrow)
	defer cf.Code.Free()

	tbl := runtime.NewTableSizedKind(4, 0, runtime.ArrayInt)
	if _, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)}); err != nil {
		t.Fatalf("Execute sparse fixture: %v", err)
	}
	if got := tbl.Len(); got != 3 {
		t.Fatalf("table len after native sparse grow = %d, want 3", got)
	}
	got := tbl.RawGetInt(3)
	if !got.IsInt() || got.Int() != 99 {
		t.Fatalf("tbl[3] after native sparse grow = %v, want 99", got)
	}
}

func TestTableArrayRawStoreBackend_BoolSparseWithinCapacity(t *testing.T) {
	valueInstr := func(fn *Function, b *Block) *Instr {
		return &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 1, Block: b}
	}
	cf := compileRawStoreBackendFixtureWithValue(t, "raw_store_bool_sparse", int64(vm.FBKindBool), 3,
		tableArrayStoreFlagAllowGrow, 1, valueInstr)
	defer cf.Code.Free()

	tbl := runtime.NewTableSizedKind(4, 0, runtime.ArrayBool)
	if _, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)}); err != nil {
		t.Fatalf("Execute bool sparse fixture: %v", err)
	}
	if got := tbl.Len(); got != 3 {
		t.Fatalf("bool table len after native sparse grow = %d, want 3", got)
	}
	got := tbl.RawGetInt(3)
	if !got.IsBool() || !got.Bool() {
		t.Fatalf("tbl[3] after bool sparse grow = %v, want true", got)
	}
}

func TestTableArrayRawStoreBackend_MissFallsBackThroughExitResume(t *testing.T) {
	withExitResumeCheck(t, func() {
		cf := compileRawStoreBackendFixture(t, "raw_store_miss", int64(vm.FBKindInt), 5, 77,
			tableArrayStoreFlagAllowGrow|tableArrayStoreFlagExitResumeOnMiss)
		defer cf.Code.Free()

		tbl := runtime.NewTableSizedKind(1, 0, runtime.ArrayInt)
		if _, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)}); err != nil {
			t.Fatalf("Execute miss fixture: %v", err)
		}
		got := tbl.RawGetInt(5)
		if !got.IsInt() || got.Int() != 77 {
			t.Fatalf("tbl[5] after exit-resume fallback = %v, want 77", got)
		}
		assertCompiledExitResumeCheckSite(t, cf, func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitTableExit && site.RequireTableInputs
		})
	})
}

func TestTableArrayRawStoreBackend_GuardFailureFallsBackThroughExitResume(t *testing.T) {
	withExitResumeCheck(t, func() {
		cf := compileRawStoreBackendParamFixture(t, "raw_store_guard_miss", int64(vm.FBKindInt), 0,
			tableArrayStoreFlagAllowGrow|tableArrayStoreFlagExitResumeOnMiss)
		defer cf.Code.Free()

		tbl := runtime.NewTableSizedKind(1, 0, runtime.ArrayInt)
		tbl.RawSetInt(0, runtime.IntValue(1))
		if _, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl), runtime.StringValue("x")}); err != nil {
			t.Fatalf("Execute guard miss fixture: %v", err)
		}
		got := tbl.RawGetInt(0)
		if !got.IsString() || got.Str() != "x" {
			t.Fatalf("tbl[0] after value guard fallback = %v, want string x", got)
		}
		assertCompiledExitResumeCheckSite(t, cf, func(site *exitResumeCheckSite) bool {
			return site.Key.ExitCode == ExitTableExit && site.RequireTableInputs
		})
	})
}

func assertCompiledExitResumeCheckSite(t *testing.T, cf *CompiledFunction, pred func(*exitResumeCheckSite) bool) {
	t.Helper()
	if cf == nil || cf.ExitResumeCheck == nil {
		t.Fatal("compiled function missing exit-resume check metadata")
	}
	for _, site := range cf.ExitResumeCheck.Sites {
		if pred(site) {
			return
		}
	}
	t.Fatalf("compiled exit-resume check metadata did not contain requested site; sites=%d", len(cf.ExitResumeCheck.Sites))
}

func compileRawStoreBackendFixture(t *testing.T, name string, kind int64, key, value int64, flags int64) *CompiledFunction {
	t.Helper()
	valueInstr := func(fn *Function, b *Block) *Instr {
		return &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: value, Block: b}
	}
	return compileRawStoreBackendFixtureWithValue(t, name, kind, key, flags, 1, valueInstr)
}

func compileRawStoreBackendParamFixture(t *testing.T, name string, kind int64, key int64, flags int64) *CompiledFunction {
	t.Helper()
	valueInstr := func(fn *Function, b *Block) *Instr {
		return &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 1, Block: b}
	}
	return compileRawStoreBackendFixtureWithValue(t, name, kind, key, flags, 2, valueInstr)
}

func compileRawStoreBackendFixtureWithValue(t *testing.T, name string, kind int64, key int64, flags int64, numParams int, makeValue func(*Function, *Block) *Instr) *CompiledFunction {
	t.Helper()

	fn := &Function{
		Proto:   &vm.FuncProto{Name: name, NumParams: numParams},
		NumRegs: numParams + 4,
	}
	entry := newBlock(0)
	fn.Entry = entry
	fn.Blocks = []*Block{entry}

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	header := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeTable, Aux: kind,
		Args: []*Value{tbl.Value()}, Block: entry}
	length := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: kind,
		Args: []*Value{header.Value()}, Block: entry}
	data := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: kind,
		Args: []*Value{header.Value()}, Block: entry}
	keyInstr := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: key, Block: entry}
	valueInstr := makeValue(fn, entry)
	store := &Instr{ID: fn.newValueID(), Op: OpTableArrayStore, Type: TypeUnknown, Aux: kind, Aux2: flags,
		Args: []*Value{tbl.Value(), data.Value(), length.Value(), keyInstr.Value(), valueInstr.Value()}, Block: entry}
	retVal := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{retVal.Value()}, Block: entry}
	entry.Instrs = []*Instr{tbl, header, length, data, keyInstr, valueInstr, store, retVal, ret}

	assertValidates(t, fn, name)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile(%s): %v", name, err)
	}
	return cf
}
