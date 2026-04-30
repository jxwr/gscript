//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTableArrayStoreLoopVersion_LowersLocalBoolMutationLoop(t *testing.T) {
	fn, _, body, _ := tableArrayStoreLoopFixture(t, true)

	out, err := TableArrayStoreLoopVersionPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	assertValidates(t, out, "store loop versioned")

	counts := countOps(out)
	if counts[OpSetTable] != 0 || counts[OpTableArrayStore] != 1 {
		t.Fatalf("expected loop SetTable to lower to one TableArrayStore, counts=%v\n%s", counts, Print(out))
	}
	if counts[OpTableArrayHeader] != 1 || counts[OpTableArrayLen] != 1 || counts[OpTableArrayData] != 1 {
		t.Fatalf("expected one preheader typed-array fact set, counts=%v\n%s", counts, Print(out))
	}
	if blockHasOp(body, OpTableArrayHeader) || blockHasOp(body, OpTableArrayLen) || blockHasOp(body, OpTableArrayData) {
		t.Fatalf("typed-array facts should be loop-scoped in the preheader, not rebuilt in the body:\n%s", Print(out))
	}
}

func TestTableArrayStoreLoopVersion_RejectsNonLocalTable(t *testing.T) {
	fn, _, _, _ := tableArrayStoreLoopFixture(t, false)

	out, err := TableArrayStoreLoopVersionPass(fn)
	if err != nil {
		t.Fatal(err)
	}
	counts := countOps(out)
	if counts[OpSetTable] != 1 || counts[OpTableArrayStore] != 0 {
		t.Fatalf("non-local tables must not get speculative preheader guards, counts=%v\n%s", counts, Print(out))
	}
}

func TestTableArrayStoreLoopVersion_DiagnosticsCoversSieveStoreLoop(t *testing.T) {
	proto := compileProto(t, `
func sieve_like(n) {
    flags := {}
    for i := 2; i <= n; i++ { flags[i] = true }
    j := 4
    for j <= n {
        flags[j] = false
        j = j + 2
    }
    if flags[n] { return 1 }
    return 0
}
result := sieve_like(20)
`)
	art, err := NewTieringManager().CompileForDiagnostics(findProtoByName(proto, "sieve_like"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.IRAfter, "TableArrayStore") {
		t.Fatalf("expected sieve-like bool mutation loop to use typed TableArrayStore:\n%s", art.IRAfter)
	}
	if !strings.Contains(art.IRAfter, "TableArrayHeader") {
		t.Fatalf("expected loop-scoped typed-array facts in optimized IR:\n%s", art.IRAfter)
	}
}

func tableArrayStoreLoopFixture(t *testing.T, localTypedTable bool) (*Function, *Block, *Block, *Instr) {
	t.Helper()

	fn := &Function{Proto: &vm.FuncProto{Name: "table_array_store_loop"}, NumRegs: 3}
	entry, header, body, exit := buildSimpleLoop(fn)

	var tbl *Instr
	if localTypedTable {
		tbl = &Instr{ID: fn.newValueID(), Op: OpNewTable, Type: TypeTable, Aux2: packNewTableAux2(0, runtime.ArrayBool), Block: entry}
	} else {
		tbl = &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeTable, Aux: 0, Block: entry}
	}
	seed := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 0, Block: entry}
	fillEnd := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 8, Block: entry}
	fill := &Instr{ID: fn.newValueID(), Op: OpTableBoolArrayFill, Type: TypeUnknown, Aux: 2,
		Args: []*Value{tbl.Value(), seed.Value(), fillEnd.Value()}, Block: entry}
	jump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(header.ID), Block: entry}
	entry.Instrs = []*Instr{tbl, seed, fillEnd, fill, jump}

	iPhi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeInt, Block: header}
	bound := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 8, Block: header}
	cond := &Instr{ID: fn.newValueID(), Op: OpLtInt, Type: TypeBool, Args: []*Value{iPhi.Value(), bound.Value()}, Block: header}
	branch := &Instr{ID: fn.newValueID(), Op: OpBranch, Type: TypeUnknown, Args: []*Value{cond.Value()}, Aux: int64(body.ID), Aux2: int64(exit.ID), Block: header}
	header.Instrs = []*Instr{iPhi, bound, cond, branch}

	falseVal := &Instr{ID: fn.newValueID(), Op: OpConstBool, Type: TypeBool, Aux: 0, Block: body}
	store := &Instr{ID: fn.newValueID(), Op: OpSetTable, Type: TypeUnknown, Aux2: int64(vm.FBKindBool),
		Args: []*Value{tbl.Value(), iPhi.Value(), falseVal.Value()}, Block: body}
	one := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: body}
	next := &Instr{ID: fn.newValueID(), Op: OpAddInt, Type: TypeInt, Args: []*Value{iPhi.Value(), one.Value()}, Block: body}
	bodyJump := &Instr{ID: fn.newValueID(), Op: OpJump, Type: TypeUnknown, Aux: int64(header.ID), Block: body}
	body.Instrs = []*Instr{falseVal, store, one, next, bodyJump}
	iPhi.Args = []*Value{seed.Value(), next.Value()}

	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{seed.Value()}, Block: exit}
	exit.Instrs = []*Instr{ret}

	assertValidates(t, fn, "table array store loop fixture")
	return fn, entry, body, store
}
