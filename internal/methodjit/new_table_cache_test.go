//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
)

func TestNewTableCacheRefillsDenseTypedSite(t *testing.T) {
	cf := &CompiledFunction{NewTableCaches: make([]newTableCacheEntry, 4)}
	ctx := &ExecContext{
		TableOp:     TableOpNewTable,
		TableSlot:   0,
		TableAux:    16,
		TableAux2:   packNewTableAux2(0, runtime.ArrayFloat),
		TableExitID: 2,
	}
	regs := []runtime.Value{runtime.NilValue()}

	if err := cf.executeTableExit(ctx, regs); err != nil {
		t.Fatalf("executeTableExit: %v", err)
	}

	tbl := regs[0].Table()
	if tbl == nil {
		t.Fatal("NewTable exit did not write a table")
	}
	if got := tbl.GetArrayKind(); got != runtime.ArrayFloat {
		t.Fatalf("allocated kind = %d, want %d", got, runtime.ArrayFloat)
	}

	entry := cf.NewTableCaches[2]
	wantCached := newTableCacheBatchSize(&Instr{
		Op:   OpNewTable,
		Aux:  16,
		Aux2: packNewTableAux2(0, runtime.ArrayFloat),
	}) - 1
	if len(entry.Values) != wantCached {
		t.Fatalf("cached values = %d, want %d", len(entry.Values), wantCached)
	}
	if len(entry.Roots) == 0 || len(entry.Roots) >= wantCached {
		t.Fatalf("cached compact roots = %d, want between 1 and %d", len(entry.Roots), wantCached-1)
	}
	if entry.Pos != 0 {
		t.Fatalf("cache pos = %d, want 0 after refill", entry.Pos)
	}
	if len(entry.Values) > 0 {
		cached := entry.Values[0].Table()
		if cached == nil {
			t.Fatal("cached value is not a table")
		}
		if wantRoot := runtime.TableGCRoot(cached); entry.Roots[0] != wantRoot {
			t.Fatalf("cached root = %p, want slab root %p", entry.Roots[0], wantRoot)
		}
		if cached == tbl {
			t.Fatal("current allocation was also stored in cache")
		}
		if got := cached.GetArrayKind(); got != runtime.ArrayFloat {
			t.Fatalf("cached kind = %d, want %d", got, runtime.ArrayFloat)
		}
	}
}

func TestNewTableCacheRefillsEmptyMixedSite(t *testing.T) {
	cf := &CompiledFunction{NewTableCaches: make([]newTableCacheEntry, 4)}
	ctx := &ExecContext{
		TableOp:     TableOpNewTable,
		TableSlot:   0,
		TableAux:    0,
		TableAux2:   packNewTableAux2(0, runtime.ArrayMixed),
		TableExitID: 2,
	}
	regs := []runtime.Value{runtime.NilValue()}

	if err := cf.executeTableExit(ctx, regs); err != nil {
		t.Fatalf("executeTableExit: %v", err)
	}

	tbl := regs[0].Table()
	if tbl == nil {
		t.Fatal("NewTable exit did not write a table")
	}
	if got := tbl.GetArrayKind(); got != runtime.ArrayMixed {
		t.Fatalf("allocated kind = %d, want %d", got, runtime.ArrayMixed)
	}

	entry := cf.NewTableCaches[2]
	wantCached := newTableCacheBatchSize(&Instr{
		Op:   OpNewTable,
		Aux:  0,
		Aux2: packNewTableAux2(0, runtime.ArrayMixed),
	}) - 1
	if len(entry.Values) != wantCached {
		t.Fatalf("cached values = %d, want %d", len(entry.Values), wantCached)
	}
	if len(entry.Roots) == 0 || len(entry.Roots) >= wantCached {
		t.Fatalf("cached compact roots = %d, want between 1 and %d", len(entry.Roots), wantCached-1)
	}
	if entry.Pos != 0 {
		t.Fatalf("cache pos = %d, want 0 after refill", entry.Pos)
	}
	if len(entry.Values) > 0 {
		cached := entry.Values[0].Table()
		if cached == nil {
			t.Fatal("cached value is not a table")
		}
		if wantRoot := runtime.TableGCRoot(cached); entry.Roots[0] != wantRoot {
			t.Fatalf("cached root = %p, want slab root %p", entry.Roots[0], wantRoot)
		}
		if cached == tbl {
			t.Fatal("current allocation was also stored in cache")
		}
		if got := cached.GetArrayKind(); got != runtime.ArrayMixed {
			t.Fatalf("cached kind = %d, want %d", got, runtime.ArrayMixed)
		}
	}
}

func TestFixedTable2CacheRefillsEmptyNilLane(t *testing.T) {
	proto := compileFunction(t, `func f(a, b) { return {foo: a, bar: b} }`)
	if len(proto.TableCtors2) != 1 {
		t.Fatalf("table ctors = %d, want 1", len(proto.TableCtors2))
	}
	ctor := &proto.TableCtors2[0].Runtime
	cf := &CompiledFunction{
		Proto:          proto,
		NewTableCaches: make([]newTableCacheEntry, 4),
	}
	ctx := &ExecContext{
		TableOp:      TableOpNewFixedTable2,
		TableSlot:    0,
		TableKeySlot: 1,
		TableValSlot: 2,
		TableAux:     0,
		TableExitID:  2,
	}
	regs := []runtime.Value{runtime.NilValue(), runtime.NilValue(), runtime.NilValue()}

	if err := cf.executeTableExit(ctx, regs); err != nil {
		t.Fatalf("executeTableExit: %v", err)
	}
	if !cacheableSmallCtor2(ctor) {
		t.Fatal("test constructor should be cacheable")
	}
	tbl := regs[0].Table()
	if tbl == nil {
		t.Fatal("NewFixedTable exit did not write a table")
	}
	if got := tbl.SkeysLen(); got != 0 {
		t.Fatalf("nil,nil constructor skeys=%d, want 0", got)
	}

	entry := cf.NewTableCaches[2]
	if len(entry.EmptyValues) != fixedTableCacheBatch-1 {
		t.Fatalf("empty cached values = %d, want %d", len(entry.EmptyValues), fixedTableCacheBatch-1)
	}
	if len(entry.EmptyRoots) == 0 || len(entry.EmptyRoots) >= len(entry.EmptyValues) {
		t.Fatalf("empty compact roots = %d, want between 1 and %d", len(entry.EmptyRoots), len(entry.EmptyValues)-1)
	}
	if len(entry.Values) != 0 || len(entry.Roots) != 0 {
		t.Fatalf("nil,nil constructor should not refill full lane: values=%d roots=%d", len(entry.Values), len(entry.Roots))
	}
	if entry.EmptyPos != 0 {
		t.Fatalf("empty cache pos = %d, want 0 after refill", entry.EmptyPos)
	}
	cached := entry.EmptyValues[0].Table()
	if cached == nil {
		t.Fatal("cached empty value is not a table")
	}
	if got := cached.SkeysLen(); got != 0 {
		t.Fatalf("cached nil,nil constructor skeys=%d, want 0", got)
	}
	if wantRoot := runtime.TableGCRoot(cached); entry.EmptyRoots[0] != wantRoot {
		t.Fatalf("cached empty root = %p, want slab root %p", entry.EmptyRoots[0], wantRoot)
	}
	if cached == tbl {
		t.Fatal("current empty allocation was also stored in cache")
	}
}

func TestNewTableCacheFastPathPopsDuringNativeExecution(t *testing.T) {
	src := `
func f(n) {
    rows := {}
    for i := 0; i < n; i++ {
        row := {}
        for j := 0; j < 4; j++ {
            row[j] = 1.5
        }
        rows[i] = row
    }
    last := rows[n - 1]
    return last[3]
}
`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	optimized, _, err := RunTier2Pipeline(fn, nil)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if len(newTableCacheSlotsForFunction(optimized)) == 0 {
		var sites []string
		for _, block := range optimized.Blocks {
			for _, instr := range block.Instrs {
				if instr.Op == OpNewTable {
					hashHint, kind := unpackNewTableAux2(instr.Aux2)
					sites = append(sites, fmt.Sprintf("id=%d aux=%d hash=%d kind=%s batch=%d",
						instr.ID, instr.Aux, hashHint, newTableCacheKindName(kind), newTableCacheBatchSize(instr)))
				}
			}
		}
		t.Fatalf("expected at least one cacheable NewTable site (%v) in optimized IR:\n%s", sites, Print(optimized))
	}
	cf, err := Compile(optimized, AllocateRegisters(optimized))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()
	cf.DeoptFunc = func(args []runtime.Value) ([]runtime.Value, error) {
		return runVM(t, src, args), nil
	}
	cf.CallVM = makeCallExitVMForTest(t, src)
	defer cf.CallVM.Close()

	result, err := cf.Execute([]runtime.Value{runtime.IntValue(40)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result) != 1 || !result[0].IsNumber() || result[0].Number() != 1.5 {
		t.Fatalf("result = %v, want 1.5", result)
	}

	var popped bool
	for _, entry := range cf.NewTableCaches {
		if entry.Pos > 0 {
			popped = true
			break
		}
	}
	if !popped {
		t.Fatalf("no NewTable cache entry was consumed; caches=%#v", cf.NewTableCaches)
	}
}

func TestNewTableExitReasonCarriesPreallocAndCacheMetadata(t *testing.T) {
	instr := &Instr{Op: OpNewTable, Aux: 1024, Aux2: packNewTableAux2(0, runtime.ArrayFloat)}
	reason := newTableExitReason(instr)
	for _, want := range []string{"NewTable(", "array=1024", "hash=0", "kind=float", "cache_batch=511"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason %q missing %q", reason, want)
		}
	}
}

func TestNewTableCacheBatchScalesByPayloadBudget(t *testing.T) {
	if got := newTableCacheBatchSizeForHints(1024, 0, runtime.ArrayFloat); got <= 32 {
		t.Fatalf("float row cache batch = %d, want larger than old fixed batch", got)
	}
	if got := newTableCacheBatchSizeForHints(tier2NewTableCacheMaxArrayHint, 0, runtime.ArrayFloat); got != 0 {
		t.Fatalf("large typed table cache batch = %d, want disabled under byte budget", got)
	}
	if got := newTableCacheBatchSizeForHints(4096, 0, runtime.ArrayBool); got <= newTableCacheBatchSizeForHints(4096, 0, runtime.ArrayFloat) {
		t.Fatalf("bool batch should exceed float batch for same hint, got bool=%d float=%d",
			got, newTableCacheBatchSizeForHints(4096, 0, runtime.ArrayFloat))
	}
	if got := newTableCacheBatchSizeForHints(tier2NewTableCacheMaxArrayHint, 0, runtime.ArrayMixed); got <= 1 {
		t.Fatalf("large mixed sparse table cache batch = %d, want enabled", got)
	}
}

func TestNewTableCachePrewarmPopulatesCacheableNewTableSites(t *testing.T) {
	fn := &Function{NumRegs: 1}
	b := &Block{ID: 0, defs: make(map[int]*Value)}
	fn.Entry = b
	fn.Blocks = []*Block{b}
	newTable := &Instr{
		ID:    fn.newValueID(),
		Op:    OpNewTable,
		Type:  TypeTable,
		Block: b,
		Aux:   1024,
		Aux2:  packNewTableAux2(0, runtime.ArrayInt),
	}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Block: b, Args: []*Value{newTable.Value()}}
	b.Instrs = []*Instr{newTable, ret}

	caches := newTableCacheSlotsForFunction(fn)
	prewarmNewTableCachesForFunction(fn, caches)

	if len(caches) <= newTable.ID {
		t.Fatalf("missing cache slot for NewTable id=%d", newTable.ID)
	}
	entry := caches[newTable.ID]
	if len(entry.Values) != newTableCacheBatchSize(newTable) {
		t.Fatalf("prewarmed values len = %d, want batch %d", len(entry.Values), newTableCacheBatchSize(newTable))
	}
	if entry.Pos != 0 {
		t.Fatalf("prewarmed pos = %d, want 0", entry.Pos)
	}
	if len(entry.Roots) == 0 {
		t.Fatalf("prewarmed cache has no roots")
	}
	if !entry.Values[0].IsTable() {
		t.Fatalf("prewarmed value is not a table: %v", entry.Values[0])
	}
}

func BenchmarkNewTableCacheRefillRoots(b *testing.B) {
	const (
		instrID   = 0
		arrayHint = 1024
		hashHint  = 0
	)
	b.Run("compact_slab_roots", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			caches := make([]newTableCacheEntry, 1)
			_ = allocateNewTableWithCache(caches, instrID, arrayHint, hashHint, runtime.ArrayFloat)
		}
	})
	b.Run("legacy_per_table_roots", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = legacyAllocateNewTableCacheForBenchmark(arrayHint, hashHint, runtime.ArrayFloat)
		}
	})
}

func BenchmarkNewTableCacheRootMetadata(b *testing.B) {
	const keep = newObject2CacheBatch - 1
	tables := make([]*runtime.Table, keep)
	for i := range tables {
		tables[i] = runtime.NewTable()
	}
	b.Run("compact_slab_roots", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			entry := newTableCacheEntry{Roots: make([]unsafe.Pointer, 0, 4)}
			for _, tbl := range tables {
				entry.addRoot(tbl)
			}
			benchmarkRootSink = entry.Roots
		}
	})
	b.Run("legacy_per_table_roots", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			roots := make([]*runtime.Table, keep)
			for j, tbl := range tables {
				roots[j] = tbl
			}
			benchmarkRootSink = roots
		}
	})
}

var benchmarkRootSink any

type legacyNewTableCacheEntryForBenchmark struct {
	Values []runtime.Value
	Roots  []*runtime.Table
}

func legacyAllocateNewTableCacheForBenchmark(arrayHint, hashHint int, kind runtime.ArrayKind) legacyNewTableCacheEntryForBenchmark {
	keep := newTableCacheBatchSizeForHints(int64(arrayHint), hashHint, kind) - 1
	entry := legacyNewTableCacheEntryForBenchmark{
		Values: make([]runtime.Value, keep),
		Roots:  make([]*runtime.Table, keep),
	}
	for i := range entry.Values {
		t := runtime.NewTableSizedKind(arrayHint, hashHint, kind)
		entry.Roots[i] = t
		entry.Values[i] = runtime.FreshTableValue(t)
	}
	return entry
}
