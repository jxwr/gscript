// table_bumpalloc_preflight_test.go — R8 pre-flight microbench.
//
// Compares per-alloc cost of `NewTableSized(0,0)` (Go mallocgc per call)
// against a bump allocator that hands out pre-allocated *Table pointers
// from a []Table backing array.
//
// Acceptance gate (from rounds/R008.yaml pre_flight): bump must be at
// least 5x faster per-alloc, or the round halts.

package runtime

import (
	stdruntime "runtime"
	"sync"
	"testing"
	"unsafe"
)

// benchBumpSlab is a minimal bump allocator modeling the R8 plan.
// The backing []Table owns the Table structs so Go GC scans them
// (and their interior map/slice/shape pointers) as a single root.
// Handing out *Table = pointer-bump + zero-init.
type benchBumpSlab struct {
	backing []Table
	ptrs    []*Table
	idx     int
}

func newBenchBumpSlab(n int) *benchBumpSlab {
	s := &benchBumpSlab{
		backing: make([]Table, n),
		ptrs:    make([]*Table, n),
	}
	for i := range s.backing {
		s.ptrs[i] = &s.backing[i]
	}
	return s
}

// get returns the next *Table and zeros its fields. Resets idx on overflow
// so the benchmark stays hot without needing a refill path; production
// will exit-resume on exhaustion.
func (s *benchBumpSlab) get() *Table {
	if s.idx >= len(s.ptrs) {
		// Reset — microbench only; production needs refill.
		for i := range s.backing {
			s.backing[i] = Table{}
		}
		s.idx = 0
	}
	t := s.ptrs[s.idx]
	s.idx++
	// Zero out fields (Go escape: struct assignment on interior pointer).
	*t = Table{keysDirty: true}
	t.array = DefaultHeap.AllocValues(1, 1)
	return t
}

// BenchmarkNewTableSizedCurrent measures the current Go-heap path.
func BenchmarkNewTableSizedCurrent(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewTableSized(0, 0)
	}
}

func TestNewEmptyTableStartsWithCleanKeys(t *testing.T) {
	tbl := NewTableSized(0, 0)
	if tbl.keysDirty {
		t.Fatal("fresh empty table should not require an iteration-key rebuild")
	}
	if key, val, ok := tbl.Next(NilValue()); ok || !key.IsNil() || !val.IsNil() {
		t.Fatalf("empty Next = (%v, %v, %v), want nil nil false", key, val, ok)
	}

	tbl.RawSetString("x", IntValue(42))
	if !tbl.keysDirty {
		t.Fatal("string mutation did not dirty iteration keys")
	}
	key, val, ok := tbl.Next(NilValue())
	if !ok || !key.IsString() || key.Str() != "x" || !val.IsInt() || val.Int() != 42 {
		t.Fatalf("mutated Next = (%v, %v, %v), want x 42 true", key, val, ok)
	}
}

func TestAllocTableFastPathConcurrentUnique(t *testing.T) {
	oldHeap := DefaultHeap
	DefaultHeap = NewHeap()
	defer func() {
		DefaultHeap = oldHeap
	}()

	const goroutines = 8
	const perG = tableSlabSize * 2
	ptrs := make(chan uintptr, goroutines*perG)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				tbl := NewTableSized(0, 0)
				if tbl.keysDirty {
					t.Errorf("fresh empty table keysDirty = true")
				}
				ptrs <- uintptr(unsafe.Pointer(tbl))
			}
		}()
	}
	wg.Wait()
	close(ptrs)

	seen := make(map[uintptr]struct{}, goroutines*perG)
	for p := range ptrs {
		if _, ok := seen[p]; ok {
			t.Fatalf("duplicate table pointer allocated: %#x", p)
		}
		seen[p] = struct{}{}
	}
}

func TestNewTableFromCtor2PopulatesSmallFields(t *testing.T) {
	ctor := NewSmallTableCtor2("left", "right")
	tbl := NewTableFromCtor2(&ctor, IntValue(11), IntValue(22))
	if len(tbl.svals) != 2 || cap(tbl.svals) != 2 {
		t.Fatalf("svals len/cap = %d/%d, want 2/2", len(tbl.svals), cap(tbl.svals))
	}
	if got := tbl.RawGetString("left"); !got.IsInt() || got.Int() != 11 {
		t.Fatalf("left = %v, want 11", got)
	}
	if got := tbl.RawGetString("right"); !got.IsInt() || got.Int() != 22 {
		t.Fatalf("right = %v, want 22", got)
	}
	if tbl.smap != nil {
		t.Fatal("two-field constructor should remain in small-field storage")
	}
	if tbl.keysDirty {
		t.Fatal("two-field constructor should defer iteration-key rebuild until Next")
	}
	seen := make(map[string]int64, 2)
	key := NilValue()
	for {
		k, v, ok := tbl.Next(key)
		if !ok {
			break
		}
		if !k.IsString() || !v.IsInt() {
			t.Fatalf("Next returned (%v, %v), want string/int", k, v)
		}
		seen[k.Str()] = v.Int()
		key = k
	}
	if len(seen) != 2 || seen["left"] != 11 || seen["right"] != 22 {
		t.Fatalf("Next fields = %v, want left=11 right=22", seen)
	}
}

func TestNewTableFromCtor2OmitsRuntimeNilFields(t *testing.T) {
	ctor := NewSmallTableCtor2("left", "right")

	leftOnly := NewTableFromCtor2(&ctor, IntValue(11), NilValue())
	if got := leftOnly.RawGetString("left"); !got.IsInt() || got.Int() != 11 {
		t.Fatalf("left-only left = %v, want 11", got)
	}
	if got := leftOnly.RawGetString("right"); !got.IsNil() {
		t.Fatalf("left-only right = %v, want nil", got)
	}
	if len(leftOnly.skeys) != 1 || leftOnly.skeys[0] != "left" || len(leftOnly.svals) != 1 {
		t.Fatalf("left-only storage skeys=%v svals=%d, want one left field", leftOnly.skeys, len(leftOnly.svals))
	}
	if leftOnly.smap != nil {
		t.Fatal("left-only constructor should remain in small-field storage")
	}

	rightOnly := NewTableFromCtor2(&ctor, NilValue(), IntValue(22))
	if got := rightOnly.RawGetString("left"); !got.IsNil() {
		t.Fatalf("right-only left = %v, want nil", got)
	}
	if got := rightOnly.RawGetString("right"); !got.IsInt() || got.Int() != 22 {
		t.Fatalf("right-only right = %v, want 22", got)
	}
	if len(rightOnly.skeys) != 1 || rightOnly.skeys[0] != "right" || len(rightOnly.svals) != 1 {
		t.Fatalf("right-only storage skeys=%v svals=%d, want one right field", rightOnly.skeys, len(rightOnly.svals))
	}
	if rightOnly.smap != nil {
		t.Fatal("right-only constructor should remain in small-field storage")
	}

	empty := NewTableFromCtor2(&ctor, NilValue(), NilValue())
	if len(empty.skeys) != 0 || len(empty.svals) != 0 || empty.smap != nil {
		t.Fatalf("empty storage skeys=%v svals=%d smap=%v, want empty small storage", empty.skeys, len(empty.svals), empty.smap)
	}
}

func TestNewTableFromCtor2DuplicateKeyKeepsSequentialSemantics(t *testing.T) {
	ctor := NewSmallTableCtor2("same", "same")
	tbl := NewTableFromCtor2(&ctor, IntValue(11), IntValue(22))
	if got := tbl.RawGetString("same"); !got.IsInt() || got.Int() != 22 {
		t.Fatalf("duplicate key value = %v, want second value 22", got)
	}

	deleted := NewTableFromCtor2(&ctor, IntValue(11), NilValue())
	if got := deleted.RawGetString("same"); !got.IsNil() {
		t.Fatalf("duplicate key deleted value = %v, want nil", got)
	}
	if _, _, ok := deleted.Next(NilValue()); ok {
		t.Fatal("duplicate key nil overwrite should leave an empty table")
	}
}

func TestTableValueUsesCurrentSlabRoot(t *testing.T) {
	oldHeap := DefaultHeap
	DefaultHeap = NewHeap()
	defer func() {
		DefaultHeap = oldHeap
	}()

	before := GCRootLogSize()
	values := make([]Value, 0, 16)
	for i := 0; i < 16; i++ {
		values = append(values, TableValue(NewTable()))
	}
	after := GCRootLogSize()
	if delta := after - before; delta != 1 {
		t.Fatalf("root log grew by %d entries, want exactly one slab root", delta)
	}
	stdruntime.KeepAlive(values)
}

func TestFreshTableValueDoesNotGrowRootLogForHeapTable(t *testing.T) {
	oldHeap := DefaultHeap
	DefaultHeap = NewHeap()
	defer func() {
		DefaultHeap = oldHeap
	}()

	tbl := NewTable()
	before := GCRootLogSize()
	v := FreshTableValue(tbl)
	after := GCRootLogSize()
	if delta := after - before; delta != 0 {
		t.Fatalf("fresh table value root log delta = %d, want 0", delta)
	}
	if !v.IsTable() || v.Table() != tbl {
		t.Fatal("fresh table value did not round-trip table pointer")
	}
	stdruntime.KeepAlive(tbl)
}

func TestFreshTableValueScansTableSlabRoot(t *testing.T) {
	oldHeap := DefaultHeap
	DefaultHeap = NewHeap()
	defer func() {
		DefaultHeap = oldHeap
	}()

	tbl := NewTable()
	v := FreshTableValue(tbl)
	root := tableSlabRootForPointer(unsafe.Pointer(tbl))
	if root == nil {
		t.Fatal("fresh table did not resolve to a slab root")
	}

	visited := make(map[uintptr]struct{})
	ScanValueRoots(v, func(p unsafe.Pointer) {
		visited[uintptr(p)] = struct{}{}
	}, make(map[uintptr]struct{}))
	if _, ok := visited[uintptr(unsafe.Pointer(tbl))]; !ok {
		t.Fatal("ScanValueRoots did not visit fresh table pointer")
	}
	if _, ok := visited[uintptr(root)]; !ok {
		t.Fatal("ScanValueRoots did not visit fresh table slab root")
	}
	stdruntime.KeepAlive(tbl)
}

func TestScanValueRootsVisitsTableSlabRoot(t *testing.T) {
	oldHeap := DefaultHeap
	DefaultHeap = NewHeap()
	defer func() {
		DefaultHeap = oldHeap
	}()

	first := NewTable()
	_ = TableValue(first)
	second := NewTable()
	v := TableValue(second)

	root := tableSlabRootForPointer(unsafe.Pointer(second))
	if root == nil {
		t.Fatal("second table did not resolve to a slab root")
	}
	if root == unsafe.Pointer(second) {
		t.Fatal("test expected second table to be an interior slab pointer")
	}

	visited := make(map[uintptr]struct{})
	ScanValueRoots(v, func(p unsafe.Pointer) {
		visited[uintptr(p)] = struct{}{}
	}, make(map[uintptr]struct{}))

	if _, ok := visited[uintptr(unsafe.Pointer(second))]; !ok {
		t.Fatal("ScanValueRoots did not visit the table pointer")
	}
	if _, ok := visited[uintptr(root)]; !ok {
		t.Fatal("ScanValueRoots did not visit the table slab root")
	}
	stdruntime.KeepAlive(first)
	stdruntime.KeepAlive(second)
}

func TestCurrentTableSlabRootIsVisitedForCompaction(t *testing.T) {
	oldHeap := DefaultHeap
	DefaultHeap = NewHeap()
	defer func() {
		DefaultHeap = oldHeap
	}()

	tbl := NewTable()
	root := tableSlabRootForPointer(unsafe.Pointer(tbl))
	if root == nil {
		t.Fatal("table did not resolve to a slab root")
	}

	visited := make(map[uintptr]struct{})
	visitCurrentTableSlabRoot(func(p unsafe.Pointer) {
		visited[uintptr(p)] = struct{}{}
	})
	if _, ok := visited[uintptr(root)]; !ok {
		t.Fatal("current table slab root was not visited")
	}
	stdruntime.KeepAlive(tbl)
}

func TestTableValueFallbackRootsNonSlabTable(t *testing.T) {
	tbl := &Table{keysDirty: true}
	before := GCRootLogSize()
	_ = TableValue(tbl)
	after := GCRootLogSize()
	if delta := after - before; delta != 1 {
		t.Fatalf("non-slab table root log delta = %d, want 1", delta)
	}
	stdruntime.KeepAlive(tbl)
}

func TestTableSlabOldBackingSurvivesViaInteriorPointer(t *testing.T) {
	h := NewHeap()
	first := h.AllocTable()
	first.keysDirty = true
	first.shapeID = 12345

	for i := 0; i < tableSlabSize*3; i++ {
		_ = h.AllocTable()
	}

	stdruntime.GC()

	if !first.keysDirty || first.shapeID != 12345 {
		t.Fatalf("old slab table lost state after GC: keysDirty=%v shapeID=%d", first.keysDirty, first.shapeID)
	}
	stdruntime.KeepAlive(first)
}

// BenchmarkNewTableBumpSlab measures the bump-alloc path.
func BenchmarkNewTableBumpSlab(b *testing.B) {
	// Slab sized to avoid full reset most of the time under b.N.
	slab := newBenchBumpSlab(1 << 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = slab.get()
	}
}
