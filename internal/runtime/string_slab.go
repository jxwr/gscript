// string_slab.go — R14 bump allocator for small []string slices (skeys).
//
// `skeys` is a []string parallel to svals for small string-keyed tables.
// In NewTableSized, `make([]string, 0, hashHint)` was a per-call
// mallocgc. This slab amortizes that cost: hand out zero-length sub-
// slices from a shared Go-heap []string backing. Growth past the
// original cap escapes back to Go (rare for small tables — vec3 uses
// cap=3 and never appends past it).
//
// Cannot use arena (mmap) because []string contains GC pointers (string
// data). Backing must be Go-heap so GC scans string contents.
//
// NOT thread-safe — mirrors the rest of the runtime.

package runtime

const stringSlabSize = 4096 // 4096 * 16 B = 64 KB per backing refill

type stringSlab struct {
	backing []string
	idx     int
	// Retained older backings stay alive via their outstanding sub-slices;
	// this field just documents intent and keeps a slab-level reference
	// while the Heap exists.
	retained []*[]string
}

// allocStringKeys returns a zero-length []string with the requested capacity,
// backed by the current slab. Falls back to `make` for requests too large
// for a single slab slot.
func (s *stringSlab) allocStringKeys(capacity int) []string {
	if capacity <= 0 {
		return nil
	}
	if capacity > stringSlabSize/4 {
		// Large requests: Go-heap directly. Not worth dedicating most of
		// a backing to one table.
		return make([]string, 0, capacity)
	}
	if s.backing == nil || s.idx+capacity > len(s.backing) {
		s.refill()
	}
	out := s.backing[s.idx : s.idx : s.idx+capacity]
	s.idx += capacity
	return out
}

func (s *stringSlab) refill() {
	next := make([]string, stringSlabSize)
	if s.backing != nil {
		s.retained = append(s.retained, &s.backing)
	}
	s.backing = next
	s.idx = 0
}

// AllocStringKeys returns a zero-length []string with the requested
// capacity, backed by the Heap's string slab. Suitable for
// NewTableSized's skeys initialization.
func (h *Heap) AllocStringKeys(capacity int) []string {
	return h.stringSlab.allocStringKeys(capacity)
}
