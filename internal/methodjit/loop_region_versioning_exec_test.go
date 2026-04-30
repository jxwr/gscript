//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTier2_LoopRegionVersionedTableArrayStoreExecutes(t *testing.T) {
	fn, store := loopRegionVersioningExecFixture(t)

	out, err := LoopRegionVersioningPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if out.TableArrayUpperBoundSafe == nil || !out.TableArrayUpperBoundSafe[store.ID] {
		t.Fatalf("expected checked store to reuse loop-region upper-bound fact:\n%s", Print(out))
	}

	alloc := AllocateRegisters(out)
	cf, err := Compile(out, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	tbl := runtime.NewTableSizedKind(4, 0, runtime.ArrayInt)
	for i := int64(0); i < 4; i++ {
		tbl.RawSetInt(i, runtime.IntValue(i+1))
	}

	if _, err := cf.Execute([]runtime.Value{runtime.TableValue(tbl)}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for i := int64(0); i < 4; i++ {
		got := tbl.RawGetInt(i)
		want := i + 11
		if !got.IsInt() || got.Int() != want {
			t.Fatalf("tbl[%d]=%v, want %d", i, got, want)
		}
	}
}

func loopRegionVersioningExecFixture(t *testing.T) (*Function, *Instr) {
	t.Helper()

	fn := &Function{
		Proto:   &vm.FuncProto{Name: "loop_region_exec", NumParams: 1},
		NumRegs: 4,
	}
	entry, header, body, exit := buildSimpleLoop(fn)

	tbl := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	arrHeader := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value()}, Block: entry}
	arrLen := &Instr{ID: fn.newValueID(), Op: OpTableArrayLen, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{arrHeader.Value()}, Block: entry}
	arrData := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{arrHeader.Value()}, Block: entry}
	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	entryJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: entry, Aux: int64(header.ID)}
	entry.Instrs = []*Instr{tbl, arrHeader, arrLen, arrData, seed, entryJump}

	iPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpLtInt, Type: TypeBool, Args: []*Value{iPhi.Value(), arrLen.Value()}, Block: header}
	headerBranch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown,
		Args: []*Value{cond.Value()}, Aux: int64(body.ID), Aux2: int64(exit.ID), Block: header}
	header.Instrs = []*Instr{iPhi, cond, headerBranch}

	load := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{arrData.Value(), arrLen.Value(), iPhi.Value()}, Block: body}
	ten := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 10, Block: body}
	updated := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{load.Value(), ten.Value()}, Block: body}
	store := &Instr{ID: fn.newValueID(), Op: OpTableArrayStore, Type: TypeUnknown, Aux: int64(vm.FBKindInt),
		Args: []*Value{tbl.Value(), arrData.Value(), arrLen.Value(), iPhi.Value(), updated.Value()}, Block: body}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: body}
	next := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt,
		Args: []*Value{iPhi.Value(), one.Value()}, Block: body}
	bodyJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: body, Aux: int64(header.ID)}
	body.Instrs = []*Instr{load, ten, updated, store, one, next, bodyJump}
	iPhi.Args = []*Value{seed.Value(), next.Value()}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{seed.Value()}, Block: exit}
	exit.Instrs = []*Instr{ret}

	assertValidates(t, fn, "loop-region exec fixture")
	return fn, store
}
