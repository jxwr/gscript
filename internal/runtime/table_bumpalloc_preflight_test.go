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
