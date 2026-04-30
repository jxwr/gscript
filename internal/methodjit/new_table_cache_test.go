//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"strings"
	"testing"

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
	if len(entry.Roots) != wantCached {
		t.Fatalf("cached roots = %d, want %d", len(entry.Roots), wantCached)
	}
	if entry.Pos != 0 {
		t.Fatalf("cache pos = %d, want 0 after refill", entry.Pos)
	}
	if len(entry.Values) > 0 {
		cached := entry.Values[0].Table()
		if cached == nil {
			t.Fatal("cached value is not a table")
		}
		if entry.Roots[0] != cached {
			t.Fatalf("cached root = %p, want cached table %p", entry.Roots[0], cached)
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
	if len(entry.Roots) != wantCached {
		t.Fatalf("cached roots = %d, want %d", len(entry.Roots), wantCached)
	}
	if entry.Pos != 0 {
		t.Fatalf("cache pos = %d, want 0 after refill", entry.Pos)
	}
	if len(entry.Values) > 0 {
		cached := entry.Values[0].Table()
		if cached == nil {
			t.Fatal("cached value is not a table")
		}
		if entry.Roots[0] != cached {
			t.Fatalf("cached root = %p, want cached table %p", entry.Roots[0], cached)
		}
		if cached == tbl {
			t.Fatal("current allocation was also stored in cache")
		}
		if got := cached.GetArrayKind(); got != runtime.ArrayMixed {
			t.Fatalf("cached kind = %d, want %d", got, runtime.ArrayMixed)
		}
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
	for _, want := range []string{"NewTable(", "array=1024", "hash=0", "kind=float", "cache_batch=127"} {
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
}
