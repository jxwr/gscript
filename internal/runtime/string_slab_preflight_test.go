// string_slab_preflight_test.go — R14 pre-flight microbench.
//
// Goal: measure `make([]string, 0, 3)` (as used in pre-R14 NewTableSized)
// vs the new bump slab. The naive `_ = make(...)` form is elided by the
// Go compiler; we use a package-level sink to force escape.

package runtime

import "testing"

// Package-level sink forces the allocation to escape so the compiler
// can't optimize the make/AllocStringKeys call away.
var sinkStrings []string

func BenchmarkMakeStringKeys3(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStrings = make([]string, 0, 3)
	}
}

func BenchmarkSlabStringKeys3(b *testing.B) {
	h := NewHeap()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkStrings = h.AllocStringKeys(3)
	}
}

// Also measure the full NewTableSized(0,3) path to see the end-to-end
// effect of R14's change.
func BenchmarkNewTableSized_0_3_WithStringSlab(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewTableSized(0, 3)
	}
}
