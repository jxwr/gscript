// table_bump.go — R9 bump allocator for *Table structs.
//
// Replaces per-call `&Table{}` Go-heap allocation with pointer-bump from
// a pre-allocated []Table backing array. Each backing is one Go-heap
// object; *Table pointers taken into its elements are interior pointers
// that Go GC treats correctly (whole backing stays live while any
// interior pointer is reachable).
//
// Hot allocations reserve slots through an atomic cursor. Refills are still
// serialized by Heap before publishing a new backing.

package runtime

import (
	"sort"
	"sync"
	"sync/atomic"
	"unsafe"
)

// tableSlabSize: Tables per backing block. A block of 8192 Tables at
// ~240 B/each is roughly 2 MB of Go heap per refill. Allocation-heavy
// workloads such as tree construction create millions of short-lived tables;
// using page-scale slabs amortizes Go heap object setup, root-log entries, and
// slab-range metadata without changing individual table identity.
const tableSlabSize = 8192

// tableSlab is a bump allocator for *Table. Holds the current backing []Table;
// the next free slot is published in Heap.tableSlabNext so readers can reserve
// without taking the heap lock. On exhaustion, refill allocates a fresh backing.
// Old backings stay alive exactly while an interior *Table pointer from that
// backing is reachable; Go's GC tracks those interior pointers, so the slab
// must not retain every old backing.
type tableSlab struct {
	backing []Table
}

type tableSlabRange struct {
	start uintptr
	end   uintptr
}

var tableSlabRanges struct {
	sync.RWMutex
	ranges []tableSlabRange
}

// allocTable returns a zero-initialized *Table pointing into the current
// backing. It is called with the heap lock held and may refill the slab before
// handing out a slot.
func (s *tableSlab) allocTable(h *Heap) *Table {
	for {
		if t := h.tryAllocTableFast(); t != nil {
			return t
		}
		s.refill(h)
	}
}

func (s *tableSlab) refill(h *Heap) {
	if h != nil {
		atomic.StoreUintptr(&h.tableSlabNext, 0)
	}
	next := make([]Table, tableSlabSize)
	s.backing = next
	if h != nil {
		h.publishTableSlab(next)
	}
}

func (h *Heap) publishTableSlab(backing []Table) {
	if len(backing) == 0 {
		atomic.StoreUintptr(&h.tableSlabNext, 0)
		atomic.StoreUintptr(&h.tableSlabStart, 0)
		atomic.StoreUintptr(&h.tableSlabEnd, 0)
		return
	}
	root := unsafe.Pointer(&backing[0])
	start := uintptr(root)
	end := start + uintptr(len(backing))*unsafe.Sizeof(backing[0])
	registerTableSlabRange(start, end)
	keepAlive(root, nil)

	atomic.StoreUintptr(&h.tableSlabNext, 0)
	atomic.StoreUintptr(&h.tableSlabStart, 0)
	atomic.StoreUintptr(&h.tableSlabEnd, end)
	atomic.StoreUintptr(&h.tableSlabStart, start)
	atomic.StoreUintptr(&h.tableSlabNext, start)
}

func registerTableSlabRange(start, end uintptr) {
	if start == 0 || end <= start {
		return
	}
	tableSlabRanges.Lock()
	defer tableSlabRanges.Unlock()

	i := sort.Search(len(tableSlabRanges.ranges), func(i int) bool {
		return tableSlabRanges.ranges[i].start >= start
	})
	if i < len(tableSlabRanges.ranges) &&
		tableSlabRanges.ranges[i].start == start &&
		tableSlabRanges.ranges[i].end == end {
		return
	}
	tableSlabRanges.ranges = append(tableSlabRanges.ranges, tableSlabRange{})
	copy(tableSlabRanges.ranges[i+1:], tableSlabRanges.ranges[i:])
	tableSlabRanges.ranges[i] = tableSlabRange{start: start, end: end}
}

func tableSlabRootForPointer(p unsafe.Pointer) unsafe.Pointer {
	addr := uintptr(p)
	tableSlabRanges.RLock()
	defer tableSlabRanges.RUnlock()

	i := sort.Search(len(tableSlabRanges.ranges), func(i int) bool {
		return tableSlabRanges.ranges[i].start > addr
	})
	for j := i - 1; j >= 0; j-- {
		r := tableSlabRanges.ranges[j]
		if addr >= r.start && addr < r.end {
			return unsafe.Pointer(r.start)
		}
		if addr >= r.end || r.start < addr {
			break
		}
	}
	return nil
}

func visitCurrentTableSlabRoot(visitor func(unsafe.Pointer)) {
	if DefaultHeap == nil {
		return
	}
	start := atomic.LoadUintptr(&DefaultHeap.tableSlabStart)
	if start != 0 {
		visitor(unsafe.Pointer(start))
	}
	start = atomic.LoadUintptr(&DefaultHeap.tableSvalsSlabStart)
	if start != 0 {
		visitor(unsafe.Pointer(start))
	}
}

func (h *Heap) tablePointerInCurrentSlab(addr uintptr) bool {
	if h == nil {
		return false
	}
	start := atomic.LoadUintptr(&h.tableSlabStart)
	if start != 0 && addr >= start {
		end := atomic.LoadUintptr(&h.tableSlabEnd)
		if addr < end {
			return true
		}
	}
	start = atomic.LoadUintptr(&h.tableSvalsSlabStart)
	if start != 0 && addr >= start {
		end := atomic.LoadUintptr(&h.tableSvalsSlabEnd)
		return addr < end
	}
	return false
}

const tableSlabElemSize = uintptr(unsafe.Sizeof(Table{}))

const tableSvalsSlabSize = 8192

// tableSvalsSlot is a fresh-allocation layout for fixed small-field table
// constructors. The Table remains a normal Go-visible *Table, while its first
// one or two svals live in the same backing object. Slots are bump-only and
// never recycled, so no live table can observe another table's old contents.
type tableSvalsSlot struct {
	table Table
	svals [2]Value
}

type tableSvalsSlab struct {
	backing []tableSvalsSlot
}

const tableSvalsSlotSize = uintptr(unsafe.Sizeof(tableSvalsSlot{}))

func (s *tableSvalsSlab) allocSlot(h *Heap) *tableSvalsSlot {
	for {
		if slot := h.tryAllocTableSvalsFast(); slot != nil {
			return slot
		}
		s.refill(h)
	}
}

func (s *tableSvalsSlab) refill(h *Heap) {
	if h != nil {
		atomic.StoreUintptr(&h.tableSvalsSlabNext, 0)
	}
	next := make([]tableSvalsSlot, tableSvalsSlabSize)
	s.backing = next
	if h != nil {
		h.publishTableSvalsSlab(next)
	}
}

func (h *Heap) publishTableSvalsSlab(backing []tableSvalsSlot) {
	if len(backing) == 0 {
		atomic.StoreUintptr(&h.tableSvalsSlabNext, 0)
		atomic.StoreUintptr(&h.tableSvalsSlabStart, 0)
		atomic.StoreUintptr(&h.tableSvalsSlabEnd, 0)
		return
	}
	root := unsafe.Pointer(&backing[0])
	start := uintptr(root)
	end := start + uintptr(len(backing))*tableSvalsSlotSize
	registerTableSlabRange(start, end)
	keepAlive(root, nil)

	atomic.StoreUintptr(&h.tableSvalsSlabNext, 0)
	atomic.StoreUintptr(&h.tableSvalsSlabStart, 0)
	atomic.StoreUintptr(&h.tableSvalsSlabEnd, end)
	atomic.StoreUintptr(&h.tableSvalsSlabStart, start)
	atomic.StoreUintptr(&h.tableSvalsSlabNext, start)
}

// tryAllocTableSvalsFast reserves a fresh Table+svals slot by absolute
// address. The slot is never reused, so the zero-filled backing from make is
// sufficient for a clean Table and empty inline Value storage.
//
//go:nocheckptr
func (h *Heap) tryAllocTableSvalsFast() *tableSvalsSlot {
	if h == nil {
		return nil
	}
	for {
		next := atomic.LoadUintptr(&h.tableSvalsSlabNext)
		if next == 0 {
			return nil
		}
		end := atomic.LoadUintptr(&h.tableSvalsSlabEnd)
		if end == 0 || next > end-tableSvalsSlotSize {
			return nil
		}
		if atomic.CompareAndSwapUintptr(&h.tableSvalsSlabNext, next, next+tableSvalsSlotSize) {
			return (*tableSvalsSlot)(unsafe.Pointer(next))
		}
	}
}

func (h *Heap) allocTableSvalsSlot() *tableSvalsSlot {
	if slot := h.tryAllocTableSvalsFast(); slot != nil {
		return slot
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tableSvalsSlab.allocSlot(h)
}

// tryAllocTableFast reserves a slot by absolute address. The backing []Table is
// kept alive by tableSlab.backing and the slab root log, but checkptr cannot
// track pointer provenance through the atomic address cursor.
//
//go:nocheckptr
func (h *Heap) tryAllocTableFast() *Table {
	if h == nil {
		return nil
	}
	for {
		next := atomic.LoadUintptr(&h.tableSlabNext)
		if next == 0 {
			return nil
		}
		end := atomic.LoadUintptr(&h.tableSlabEnd)
		if end == 0 || next > end-tableSlabElemSize {
			return nil
		}
		if atomic.CompareAndSwapUintptr(&h.tableSlabNext, next, next+tableSlabElemSize) {
			return (*Table)(unsafe.Pointer(next))
		}
	}
}

// AllocTable returns a fresh, zero-initialized *Table from the bump slab.
// Caller is responsible for any field initialization beyond the zero value.
func (h *Heap) AllocTable() *Table {
	if t := h.tryAllocTableFast(); t != nil {
		return t
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tableSlab.allocTable(h)
}

// AllocTableWithSvals returns a fresh table and an empty svals slice with the
// requested capacity. One- and two-slot constructors use the inline-svals slab;
// larger constructors keep using the arena-backed Value storage.
func (h *Heap) AllocTableWithSvals(capacity int) (*Table, []Value) {
	if capacity == 1 || capacity == 2 {
		slot := h.allocTableSvalsSlot()
		t := &slot.table
		if capacity == 1 {
			return t, slot.svals[:0:1]
		}
		return t, slot.svals[:0:2]
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.tableSlab.allocTable(h)
	if capacity <= 0 {
		return t, nil
	}
	bytes := capacity * int(unsafe.Sizeof(Value(0)))
	p := h.allocBytesLocked(bytes)
	return t, unsafe.Slice((*Value)(p), capacity)[:0]
}

// AllocTableWithSvals1 returns a fresh table and a one-slot svals slice. It is
// the fixed-shape object constructor hot path, avoiding the generic size-class
// lookup used by AllocTableWithSvals.
func (h *Heap) AllocTableWithSvals1() (*Table, []Value) {
	slot := h.allocTableSvalsSlot()
	return &slot.table, slot.svals[:1:1]
}

// AllocTableWithSvals2 returns a fresh table and a two-slot svals slice for
// static two-field object literals.
func (h *Heap) AllocTableWithSvals2() (*Table, []Value) {
	slot := h.allocTableSvalsSlot()
	return &slot.table, slot.svals[:2:2]
}
