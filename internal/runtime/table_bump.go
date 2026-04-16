// table_bump.go — R9 bump allocator for *Table structs.
//
// Replaces per-call `&Table{}` Go-heap allocation with pointer-bump from
// a pre-allocated []Table backing array. Each backing is one Go-heap
// object; *Table pointers taken into its elements are interior pointers
// that Go GC treats correctly (whole backing stays live while any
// interior pointer is reachable).
//
// NOT thread-safe — mirrors the rest of the heap. The GScript VM runs
// single-threaded.

package runtime

// tableSlabSize: Tables per backing block. A block of 1024 Tables at
// ~240 B/each ≈ 240 KB of Go heap per refill. Enough to amortize the
// mallocgc cost across ~1000 NEWTABLEs.
const tableSlabSize = 1024

// tableSlab is a bump allocator for *Table. Holds a current backing
// []Table and an index pointing at the next free slot. On exhaustion,
// allocates a fresh backing; old backings stay alive via the interior
// *Table pointers handed out from them (GC tracks those).
type tableSlab struct {
	backing []Table
	idx     int
	// retained prevents the GC from collecting backings whose *Table
	// pointers are all still live (they won't be collected regardless),
	// and also keeps the Heap a stable root for backings while the Heap
	// exists. Redundant but explicit about intent.
	retained []*[]Table
}

// allocTable returns a zero-initialized *Table pointing into the current
// backing. On overflow, allocates a fresh backing before handing out a slot.
func (s *tableSlab) allocTable() *Table {
	if s.idx >= len(s.backing) {
		s.refill()
	}
	t := &s.backing[s.idx]
	s.idx++
	return t
}

func (s *tableSlab) refill() {
	next := make([]Table, tableSlabSize)
	s.retained = append(s.retained, &s.backing)
	s.backing = next
	s.idx = 0
}

// AllocTable returns a fresh, zero-initialized *Table from the bump slab.
// Caller is responsible for any field initialization beyond the zero value.
func (h *Heap) AllocTable() *Table {
	if h.tableSlab.backing == nil {
		h.tableSlab.refill()
	}
	return h.tableSlab.allocTable()
}
