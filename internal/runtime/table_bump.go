// table_bump.go — R9 bump allocator for *Table structs.
//
// Replaces per-call `&Table{}` Go-heap allocation with pointer-bump from
// a pre-allocated []Table backing array. Each backing is one Go-heap
// object; *Table pointers taken into its elements are interior pointers
// that Go GC treats correctly (whole backing stays live while any
// interior pointer is reachable).
//
// tableSlab itself is not thread-safe. Heap serializes public allocation
// entry points before calling into it.

package runtime

import "unsafe"

// tableSlabSize: Tables per backing block. A block of 1024 Tables at
// ~240 B/each ≈ 240 KB of Go heap per refill. Enough to amortize the
// mallocgc cost across ~1000 NEWTABLEs.
const tableSlabSize = 1024

// tableSlab is a bump allocator for *Table. Holds a current backing
// []Table and an index pointing at the next free slot. On exhaustion,
// allocates a fresh backing. Old backings stay alive exactly while an
// interior *Table pointer from that backing is reachable; Go's GC tracks
// those interior pointers, so the slab must not retain every old backing.
type tableSlab struct {
	backing []Table
	idx     int
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
	s.backing = next
	s.idx = 0
}

// AllocTable returns a fresh, zero-initialized *Table from the bump slab.
// Caller is responsible for any field initialization beyond the zero value.
func (h *Heap) AllocTable() *Table {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tableSlab.backing == nil {
		h.tableSlab.refill()
	}
	return h.tableSlab.allocTable()
}

// AllocTableWithSvals returns a fresh table and an empty, arena-backed svals
// slice with the requested capacity. This keeps the common object-literal path
// to one heap lock instead of separately locking for the Table and svals.
func (h *Heap) AllocTableWithSvals(capacity int) (*Table, []Value) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tableSlab.backing == nil {
		h.tableSlab.refill()
	}
	t := h.tableSlab.allocTable()
	if capacity <= 0 {
		return t, nil
	}
	bytes := capacity * int(unsafe.Sizeof(Value(0)))
	p := h.allocBytesLocked(bytes)
	return t, unsafe.Slice((*Value)(p), capacity)[:0]
}
