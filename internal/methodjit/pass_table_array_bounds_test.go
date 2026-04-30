package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTableArrayBoundsCheckHoist_MarksHeaderBoundedLoad(t *testing.T) {
	fn, load := tableArrayBoundsLoopFixture(t, false, false)

	out, err := TableArrayBoundsCheckHoistPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if out.TableArrayUpperBoundSafe == nil || !out.TableArrayUpperBoundSafe[load.ID] {
		t.Fatalf("expected loop-header guard to prove TableArrayLoad upper bound:\n%s", Print(out))
	}
}

func TestTableArrayBoundsCheckHoist_RejectsLoopTableMutation(t *testing.T) {
	fn, load := tableArrayBoundsLoopFixture(t, true, false)

	out, err := TableArrayBoundsCheckHoistPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if out.TableArrayUpperBoundSafe != nil && out.TableArrayUpperBoundSafe[load.ID] {
		t.Fatalf("table mutation in loop must keep TableArrayLoad bounds check:\n%s", Print(out))
	}
}

func TestTableArrayBoundsCheckHoist_RejectsDifferentLoopBound(t *testing.T) {
	fn, load := tableArrayBoundsLoopFixture(t, false, true)

	out, err := TableArrayBoundsCheckHoistPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	if out.TableArrayUpperBoundSafe != nil && out.TableArrayUpperBoundSafe[load.ID] {
		t.Fatalf("different loop bound must not prove TableArrayLoad upper bound:\n%s", Print(out))
	}
}

func tableArrayBoundsLoopFixture(t *testing.T, withMutation, differentBound bool) (*Function, *Instr) {
	t.Helper()

	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_bounds"}, NumRegs: 3}
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
	bound := arrLen
	if differentBound {
		bound = &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeInt, Aux: 1, Block: header}
	}
	cond := &Instr{ID: fn.newValueID(), Op: OpLtInt, Type: TypeBool, Block: header,
		Args: []*Value{iPhi.Value(), bound.Value()}}
	headerBranch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Block: header,
		Args: []*Value{cond.Value()}, Aux: int64(body.ID), Aux2: int64(exit.ID)}
	if differentBound {
		header.Instrs = []*Instr{iPhi, bound, cond, headerBranch}
	} else {
		header.Instrs = []*Instr{iPhi, cond, headerBranch}
	}

	load := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeInt, Aux: int64(vm.FBKindInt),
		Args: []*Value{arrData.Value(), arrLen.Value(), iPhi.Value()}, Block: body}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: body}
	next := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{iPhi.Value(), one.Value()}, Block: body}
	bodyInstrs := []*Instr{load, one, next}
	if withMutation {
		set := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown,
			Args: []*Value{tbl.Value(), iPhi.Value(), load.Value()}, Block: body}
		bodyInstrs = append(bodyInstrs, set)
	}
	bodyJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Block: body, Aux: int64(header.ID)}
	bodyInstrs = append(bodyInstrs, bodyJump)
	body.Instrs = bodyInstrs

	iPhi.Args = []*Value{seed.Value(), next.Value()}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{seed.Value()}, Block: exit}
	exit.Instrs = []*Instr{ret}

	assertValidates(t, fn, "table array bounds fixture")
	return fn, load
}
