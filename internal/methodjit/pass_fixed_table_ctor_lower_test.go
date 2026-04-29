//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestFixedTableConstructorMetadata_NewObject2(t *testing.T) {
	top := compileProto(t, `
func makePair(a, b) {
    return {alpha: a, beta: b}
}
result := makePair(1, 2)
`)
	makePair := findProtoByName(top, "makePair")
	if makePair == nil {
		t.Fatal("makePair proto missing")
	}
	fn := BuildGraph(makePair)

	var found bool
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op != OpNewTable {
				continue
			}
			fact, ok := fn.FixedTableConstructors[instr.ID]
			if !ok {
				continue
			}
			found = true
			if fact.Ctor2Index < 0 || fact.Ctor2Index >= len(makePair.TableCtors2) {
				t.Fatalf("bad ctor index: %#v", fact)
			}
			if len(fact.FieldNames) != 2 || fact.FieldNames[0] != "alpha" || fact.FieldNames[1] != "beta" {
				t.Fatalf("unexpected constructor fields: %#v", fact.FieldNames)
			}
		}
	}
	if !found {
		t.Fatalf("expected OP_NEWOBJECT2 lowering metadata\nIR:\n%s", Print(fn))
	}
}

func TestFixedTableConstructorLowering_RewritesSurvivingCtor2(t *testing.T) {
	top := compileProto(t, `
func makePair(a, b) {
    return {alpha: a, beta: b}
}
result := makePair(1, 2)
`)
	makePair := findProtoByName(top, "makePair")
	if makePair == nil {
		t.Fatal("makePair proto missing")
	}
	fn := BuildGraph(makePair)
	out, err := FixedTableConstructorLoweringPass(fn)
	if err != nil {
		t.Fatalf("FixedTableConstructorLoweringPass: %v", err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}

	var sawNewFixed bool
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpNewFixedTable:
				sawNewFixed = true
				if instr.Aux2 != 2 || len(instr.Args) != 2 {
					t.Fatalf("bad OpNewFixedTable: %+v", instr)
				}
			case OpNewTable, OpSetField:
				t.Fatalf("constructor still has %s after lowering\nIR:\n%s", instr.Op, Print(out))
			}
		}
	}
	if !sawNewFixed {
		t.Fatalf("expected OpNewFixedTable after lowering\nIR:\n%s", Print(out))
	}

	result, err := Interpret(out, []runtime.Value{runtime.IntValue(11), runtime.NilValue()})
	if err != nil {
		t.Fatalf("Interpret lowered IR: %v", err)
	}
	if len(result) != 1 || !result[0].IsTable() {
		t.Fatalf("lowered result = %#v, want one table", result)
	}
	tbl := result[0].Table()
	if got := tbl.RawGetString("alpha"); !got.IsInt() || got.Int() != 11 {
		t.Fatalf("alpha = %v, want 11", got)
	}
	if got := tbl.RawGetString("beta"); !got.IsNil() {
		t.Fatalf("beta = %v, want nil", got)
	}
	if got := tbl.SkeysLen(); got != 1 {
		t.Fatalf("nil-valued field should be omitted, skeys=%d", got)
	}
}

func TestEmitNewFixedTable2CacheFastPath(t *testing.T) {
	proto := compileFunction(t, `func f(a, b) { return {foo: a, bar: b} }`)
	fn := BuildGraph(proto)
	out, err := FixedTableConstructorLoweringPass(fn)
	if err != nil {
		t.Fatalf("FixedTableConstructorLoweringPass: %v", err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}

	newFixedID := -1
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewFixedTable {
				newFixedID = instr.ID
			}
		}
	}
	if newFixedID < 0 {
		t.Fatalf("missing OpNewFixedTable\nIR:\n%s", Print(out))
	}

	cf, err := Compile(out, AllocateRegisters(out))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	if newFixedID >= len(cf.NewTableCaches) {
		t.Fatalf("new fixed table cache missing: id=%d caches=%d", newFixedID, len(cf.NewTableCaches))
	}

	result, err := cf.Execute([]runtime.Value{runtime.IntValue(10), runtime.IntValue(20)})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	assertPairTable(t, result, runtime.IntValue(10), runtime.IntValue(20), 2)
	if entry := cf.NewTableCaches[newFixedID]; len(entry.Values) == 0 || entry.Pos != 0 {
		t.Fatalf("first miss did not refill fixed table cache: %#v", entry)
	}

	result, err = cf.Execute([]runtime.Value{runtime.IntValue(30), runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	assertPairTable(t, result, runtime.IntValue(30), runtime.IntValue(40), 2)
	if entry := cf.NewTableCaches[newFixedID]; entry.Pos != 1 {
		t.Fatalf("cache fast path did not pop one table: %#v", entry)
	}

	result, err = cf.Execute([]runtime.Value{runtime.IntValue(50), runtime.NilValue()})
	if err != nil {
		t.Fatalf("nil Execute: %v", err)
	}
	assertPairTable(t, result, runtime.IntValue(50), runtime.NilValue(), 1)
	if entry := cf.NewTableCaches[newFixedID]; entry.Pos != 1 {
		t.Fatalf("nil fallback should not consume shaped cache entry: %#v", entry)
	}
}

func assertPairTable(t *testing.T, result []runtime.Value, foo, bar runtime.Value, wantSkeys int) {
	t.Helper()
	if len(result) != 1 || !result[0].IsTable() {
		t.Fatalf("result = %#v, want one table", result)
	}
	tbl := result[0].Table()
	if got := tbl.RawGetString("foo"); !got.Equal(foo) {
		t.Fatalf("foo = %v, want %v", got, foo)
	}
	if got := tbl.RawGetString("bar"); !got.Equal(bar) {
		t.Fatalf("bar = %v, want %v", got, bar)
	}
	if got := tbl.SkeysLen(); got != wantSkeys {
		t.Fatalf("skeys=%d, want %d", got, wantSkeys)
	}
}
