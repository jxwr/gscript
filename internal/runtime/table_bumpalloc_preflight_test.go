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
	"testing"
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
