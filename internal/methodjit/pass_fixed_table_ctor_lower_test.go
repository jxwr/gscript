//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
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

func TestFixedTableConstructorMetadata_NewObjectN(t *testing.T) {
	top := compileProto(t, `
func makeTriple(a, b, c) {
    return {alpha: a, beta: b, gamma: c}
}
result := makeTriple(1, 2, 3)
`)
	makeTriple := findProtoByName(top, "makeTriple")
	if makeTriple == nil {
		t.Fatal("makeTriple proto missing")
	}
	fn := BuildGraph(makeTriple)

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
			if fact.CtorNIndex < 0 || fact.CtorNIndex >= len(makeTriple.TableCtorsN) {
				t.Fatalf("bad ctorN index: %#v", fact)
			}
			if len(fact.FieldNames) != 3 || fact.FieldNames[0] != "alpha" || fact.FieldNames[1] != "beta" || fact.FieldNames[2] != "gamma" {
				t.Fatalf("unexpected constructor fields: %#v", fact.FieldNames)
			}
		}
	}
	if !found {
		t.Fatalf("expected OP_NEWOBJECTN lowering metadata\nIR:\n%s", Print(fn))
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

func TestFixedTableConstructorLowering_RewritesSurvivingCtorN(t *testing.T) {
	top := compileProto(t, `
func makeTriple(a, b, c) {
    return {alpha: a, beta: b, gamma: c}
}
result := makeTriple(1, 2, 3)
`)
	makeTriple := findProtoByName(top, "makeTriple")
	if makeTriple == nil {
		t.Fatal("makeTriple proto missing")
	}
	fn := BuildGraph(makeTriple)
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
				if instr.Aux2 != 3 || len(instr.Args) != 3 {
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

	result, err := Interpret(out, []runtime.Value{runtime.IntValue(11), runtime.NilValue(), runtime.IntValue(33)})
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
	if got := tbl.RawGetString("gamma"); !got.IsInt() || got.Int() != 33 {
		t.Fatalf("gamma = %v, want 33", got)
	}
	if got := tbl.SkeysLen(); got != 2 {
		t.Fatalf("nil-valued field should be omitted, skeys=%d", got)
	}
}

func TestFixedTableConstructorLowering_InlinedCtorN(t *testing.T) {
	top := compileProto(t, `
func makeDoc(i) {
    kind := "article"
    if i % 2 == 0 {
        kind = "event"
    }
    return {id: i, kind: kind, user: {id: i, tier: 1, region: 2}}
}
func build(n) {
    docs := {}
    seed := {id: 0, tier: 1, region: 2}
    docs[0] = seed
    for i := 1; i <= n; i++ {
        docs[i] = makeDoc(i)
    }
    return docs
}
result := build(10)
`)
	makeDoc := findProtoByName(top, "makeDoc")
	build := findProtoByName(top, "build")
	if makeDoc == nil || build == nil {
		t.Fatalf("missing protos: makeDoc=%v build=%v", makeDoc != nil, build != nil)
	}
	fn := BuildGraph(build)
	inlined, err := InlinePassWith(InlineConfig{
		Globals: map[string]*vm.FuncProto{"makeDoc": makeDoc},
		MaxSize: 200,
	})(fn)
	if err != nil {
		t.Fatalf("InlinePass: %v", err)
	}
	out, err := FixedTableConstructorLoweringPass(inlined)
	if err != nil {
		t.Fatalf("FixedTableConstructorLoweringPass: %v", err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}
	newFixedN := 0
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewFixedTable && instr.Aux2 > 2 {
				newFixedN++
			}
		}
	}
	if newFixedN < 2 {
		t.Fatalf("expected caller and inlined N-field constructors to lower, got %d\nIR:\n%s", newFixedN, Print(out))
	}
}

func TestFixedTableConstructorLowering_CallInterleavedMaterializedCtorN(t *testing.T) {
	top := compileProto(t, `
func fmt_tag(i) {
    return i + 10
}
func makeTags(i) {
    return {
        first: fmt_tag(i),
        second: fmt_tag(i + 1),
        third: fmt_tag(i + 2),
    }
}
result := makeTags(3)
`)
	makeTags := findProtoByName(top, "makeTags")
	if makeTags == nil {
		t.Fatal("makeTags proto missing")
	}
	fn := BuildGraph(makeTags)
	out, err := FixedTableConstructorLoweringPass(fn)
	if err != nil {
		t.Fatalf("FixedTableConstructorLoweringPass: %v", err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}

	var sawFixedN bool
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewFixedTable && instr.Aux2 == 3 {
				sawFixedN = true
			}
			if instr.Op == OpSetField {
				t.Fatalf("call-interleaved constructor still has SetField after lowering\nIR:\n%s", Print(out))
			}
		}
	}
	if !sawFixedN {
		t.Fatalf("expected call-interleaved constructor to lower\nIR:\n%s", Print(out))
	}
}

func TestFixedTableConstructorLowering_DuplicateKeyMaterializedCtorNNotLowered(t *testing.T) {
	top := compileProto(t, `
func fmt_tag(i) {
    return i + 10
}
func makeTags(i) {
    return {
        first: fmt_tag(i),
        first: fmt_tag(i + 1),
        second: fmt_tag(i + 2),
    }
}
result := makeTags(3)
`)
	makeTags := findProtoByName(top, "makeTags")
	if makeTags == nil {
		t.Fatal("makeTags proto missing")
	}
	fn := BuildGraph(makeTags)
	out, err := FixedTableConstructorLoweringPass(fn)
	if err != nil {
		t.Fatalf("FixedTableConstructorLoweringPass: %v", err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}

	newFixedN := 0
	setFields := 0
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewFixedTable && instr.Aux2 > 2 {
				newFixedN++
			}
			if instr.Op == OpSetField {
				setFields++
			}
		}
	}
	if newFixedN != 0 {
		t.Fatalf("duplicate-key materialized constructor lowered unexpectedly\nIR:\n%s", Print(out))
	}
	if setFields != 3 {
		t.Fatalf("duplicate-key constructor should retain three ordered stores, got %d\nIR:\n%s", setFields, Print(out))
	}
}

func TestFixedTableConstructorLowering_CFGSplitMaterializedCtorN(t *testing.T) {
	top := compileProto(t, `
func makeDoc(i) {
    active := true
    if i % 2 == 0 {
        active = false
    }
    return {id: i, active: active, a: 1, b: 2, c: 3, d: 4}
}
result := makeDoc(2)
`)
	makeDoc := findProtoByName(top, "makeDoc")
	if makeDoc == nil {
		t.Fatal("makeDoc proto missing")
	}
	fn := BuildGraph(makeDoc)
	out, err := FixedTableConstructorLoweringPass(fn)
	if err != nil {
		t.Fatalf("FixedTableConstructorLoweringPass: %v", err)
	}
	out, err = DCEPass(out)
	if err != nil {
		t.Fatalf("DCEPass: %v", err)
	}

	var sawFixedN bool
	for _, block := range out.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op == OpNewFixedTable && instr.Aux2 == 6 {
				sawFixedN = true
			}
			if instr.Op == OpSetField {
				t.Fatalf("CFG-split constructor still has SetField after lowering\nIR:\n%s", Print(out))
			}
		}
	}
	if !sawFixedN {
		t.Fatalf("expected CFG-split constructor to lower\nIR:\n%s", Print(out))
	}

	result, err := Interpret(out, []runtime.Value{runtime.IntValue(2)})
	if err != nil {
		t.Fatalf("Interpret lowered IR: %v", err)
	}
	if len(result) != 1 || !result[0].IsTable() {
		t.Fatalf("lowered result = %#v, want one table", result)
	}
	tbl := result[0].Table()
	if got := tbl.RawGetString("id"); !got.IsInt() || got.Int() != 2 {
		t.Fatalf("id = %v, want 2", got)
	}
	if got := tbl.RawGetString("active"); !got.IsBool() || got.Bool() {
		t.Fatalf("active = %v, want false", got)
	}
	if got := tbl.SkeysLen(); got != 6 {
		t.Fatalf("skeys=%d, want 6", got)
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

func TestEmitNewFixedTable2EmptyCacheFastPath(t *testing.T) {
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

	args := []runtime.Value{runtime.NilValue(), runtime.NilValue()}
	result, err := cf.Execute(args)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	assertPairTable(t, result, runtime.NilValue(), runtime.NilValue(), 0)
	if entry := cf.NewTableCaches[newFixedID]; len(entry.EmptyValues) == 0 || entry.EmptyPos != 0 {
		t.Fatalf("first miss did not refill empty fixed table cache: %#v", entry)
	}

	result, err = cf.Execute(args)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	assertPairTable(t, result, runtime.NilValue(), runtime.NilValue(), 0)
	if entry := cf.NewTableCaches[newFixedID]; entry.EmptyPos != 1 {
		t.Fatalf("empty cache fast path did not pop one table: %#v", entry)
	}
}

func TestEmitNewFixedTableNCacheFastPath(t *testing.T) {
	proto := compileFunction(t, `func f(a, b, c) { return {foo: a, bar: b, baz: c} }`)
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
	if slots := cf.FixedTableArgSlots[newFixedID]; len(slots) != 3 {
		t.Fatalf("fixed N arg slots = %#v, want 3 slots", slots)
	}

	result, err := cf.Execute([]runtime.Value{runtime.IntValue(10), runtime.IntValue(20), runtime.IntValue(30)})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	assertTripleTable(t, result, runtime.IntValue(10), runtime.IntValue(20), runtime.IntValue(30), 3)
	if entry := cf.NewTableCaches[newFixedID]; len(entry.Values) == 0 || entry.Pos != 0 {
		t.Fatalf("first miss did not refill fixed table cache: %#v", entry)
	}

	result, err = cf.Execute([]runtime.Value{runtime.IntValue(40), runtime.IntValue(50), runtime.IntValue(60)})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	assertTripleTable(t, result, runtime.IntValue(40), runtime.IntValue(50), runtime.IntValue(60), 3)
	if entry := cf.NewTableCaches[newFixedID]; entry.Pos != 1 {
		t.Fatalf("cache fast path did not pop one table: %#v", entry)
	}

	result, err = cf.Execute([]runtime.Value{runtime.IntValue(70), runtime.NilValue(), runtime.IntValue(90)})
	if err != nil {
		t.Fatalf("nil Execute: %v", err)
	}
	assertTripleTable(t, result, runtime.IntValue(70), runtime.NilValue(), runtime.IntValue(90), 2)
	if entry := cf.NewTableCaches[newFixedID]; entry.Pos != 1 {
		t.Fatalf("nil fallback should not consume shaped cache entry: %#v", entry)
	}
}

func assertTripleTable(t *testing.T, result []runtime.Value, foo, bar, baz runtime.Value, wantSkeys int) {
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
	if got := tbl.RawGetString("baz"); !got.Equal(baz) {
		t.Fatalf("baz = %v, want %v", got, baz)
	}
	if got := tbl.SkeysLen(); got != wantSkeys {
		t.Fatalf("skeys=%d, want %d", got, wantSkeys)
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
