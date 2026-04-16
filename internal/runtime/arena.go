//go:build darwin && arm64

package runtime

import "unsafe"

// ---------------------------------------------------------------------------
// Arena allocator — bump-pointer allocation from mmap'd pages
// ---------------------------------------------------------------------------
//
// NOT thread-safe. The GScript VM is single-threaded; adding a mutex here
// would add ~20ns per allocation for no benefit. If concurrency is ever
// needed, add a sync.Mutex to Arena and Heap.

const (
	defaultPageSize = 1 << 20 // 1 MB per mmap page
	numSizeClasses  = 8
)

// sizeClasses maps index → fixed object size in bytes.
// Chosen to cover common allocation sizes with minimal internal fragmentation.
var sizeClasses = [numSizeClasses]int{
	64, 128, 256, 512, 1024, 2048, 4096, 8192,
}

// sizeClassIndex returns the arena index for the given allocation size.
// Returns -1 if the size exceeds the largest size class.
func sizeClassIndex(size int) int {
	for i, sc := range sizeClasses {
		if size <= sc {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Arena
// ---------------------------------------------------------------------------

// Arena manages bump-pointer allocation for a single fixed object size.
// Each Arena owns one or more mmap'd pages and allocates sequentially
// within the current page. When the current page is exhausted, a new
// page is mmap'd.
type Arena struct {
	pages    [][]byte // all mmap'd pages
	cursor   uintptr  // current bump pointer (next free byte)
	limit    uintptr  // end of current page
	objSize  int      // fixed object size for this arena
	pageSize int      // size of each mmap page
}

// NewArena creates an arena for objects of the given size.
// The first mmap page is allocated lazily on the first Alloc call.
func NewArena(objSize, pageSize int) *Arena {
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	// Ensure page can hold at least one object.
	if objSize > pageSize {
		pageSize = objSize
	}
	return &Arena{
		objSize:  objSize,
		pageSize: pageSize,
	}
}

// Alloc returns a pointer to objSize bytes of zeroed memory.
// This is the hot path — a single compare-and-add when space remains.
func (a *Arena) Alloc() unsafe.Pointer {
	next := a.cursor + uintptr(a.objSize)
	if next <= a.limit {
		p := unsafe.Pointer(a.cursor)
		a.cursor = next
		return p
	}
	return a.allocSlow()
}

// allocSlow is the cold path: mmap a new page, append it, reset cursor/limit.
func (a *Arena) allocSlow() unsafe.Pointer {
	page, err := mmapAlloc(a.pageSize)
	if err != nil {
		panic("arena: mmap failed: " + err.Error())
	}
	a.pages = append(a.pages, page)
	a.cursor = uintptr(unsafe.Pointer(&page[0]))
	a.limit = a.cursor + uintptr(a.pageSize)

	// Allocate the first object from the new page.
	p := unsafe.Pointer(a.cursor)
	a.cursor += uintptr(a.objSize)
	return p
}

// Reset resets the arena's cursor to the beginning of the first page.
// Existing pages are kept (not munmap'd) for reuse. Memory is NOT zeroed —
// callers must zero objects if needed.
func (a *Arena) Reset() {
	if len(a.pages) > 0 {
		a.cursor = uintptr(unsafe.Pointer(&a.pages[0][0]))
		a.limit = a.cursor + uintptr(a.pageSize)
	} else {
		a.cursor = 0
		a.limit = 0
	}
}

// Free munmaps all pages owned by this arena.
func (a *Arena) Free() {
	for _, page := range a.pages {
		_ = mmapFree(page)
	}
	a.pages = nil
	a.cursor = 0
	a.limit = 0
}

// ---------------------------------------------------------------------------
// Heap — size-class based allocator
// ---------------------------------------------------------------------------

// Heap provides general-purpose allocation using size-class arenas.
// Allocations up to 8192 bytes use the appropriate size-class arena.
// Larger allocations get their own dedicated mmap page.
type Heap struct {
	arenas    [numSizeClasses]*Arena
	overPages [][]byte // dedicated mmap pages for oversized allocations
	tableSlab tableSlab
}

// NewHeap creates a Heap with one Arena per size class.
func NewHeap() *Heap {
	h := &Heap{}
	for i, sc := range sizeClasses {
		h.arenas[i] = NewArena(sc, defaultPageSize)
	}
	return h
}

// AllocBytes allocates at least `size` bytes and returns a pointer.
// For sizes <= 8192, the allocation comes from a size-class arena.
// For larger sizes, a dedicated mmap page is allocated.
func (h *Heap) AllocBytes(size int) unsafe.Pointer {
	idx := sizeClassIndex(size)
	if idx >= 0 {
		return h.arenas[idx].Alloc()
	}
	// Oversized: mmap a dedicated page (rounded up to page size).
	pageSize := (size + defaultPageSize - 1) &^ (defaultPageSize - 1)
	page, err := mmapAlloc(pageSize)
	if err != nil {
		panic("heap: mmap failed for oversized alloc: " + err.Error())
	}
	h.overPages = append(h.overPages, page)
	return unsafe.Pointer(&page[0])
}

// AllocValues allocates a []Value slice backed by arena memory.
// The returned slice has the given length and capacity. Elements [0, length)
// are initialized to NilValue(). Elements [length, capacity) are zeroed.
func (h *Heap) AllocValues(length, capacity int) []Value {
	if capacity < length {
		capacity = length
	}
	bytes := capacity * int(unsafe.Sizeof(Value(0))) // sizeof(Value) == 8
	p := h.AllocBytes(bytes)
	s := unsafe.Slice((*Value)(p), capacity)[:length]
	nv := NilValue()
	for i := 0; i < length; i++ {
		s[i] = nv
	}
	return s
}

// GrowValues allocates a new arena-backed slice with newCap capacity,
// copies old data into it, and returns the new slice.
func (h *Heap) GrowValues(old []Value, newCap int) []Value {
	if newCap < len(old) {
		newCap = len(old)
	}
	s := h.AllocValues(len(old), newCap)
	copy(s, old)
	return s
}

// Free releases all memory (arenas + oversized pages).
func (h *Heap) Free() {
	for _, a := range h.arenas {
		if a != nil {
			a.Free()
		}
	}
	for _, page := range h.overPages {
		_ = mmapFree(page)
	}
	h.overPages = nil
}

// ---------------------------------------------------------------------------
// Typed slice allocation helpers
// ---------------------------------------------------------------------------

// AllocInt64s allocates a []int64 slice backed by arena memory.
// The returned slice has the given length and capacity, zero-filled.
func (h *Heap) AllocInt64s(length, capacity int) []int64 {
	if capacity < length {
		capacity = length
	}
	if capacity == 0 {
		return nil
	}
	bytes := capacity * 8 // sizeof(int64) == 8
	p := h.AllocBytes(bytes)
	return unsafe.Slice((*int64)(p), capacity)[:length]
}

// GrowInt64s allocates a new arena-backed int64 slice with newCap capacity,
// copies old data into it, and returns the new slice.
func (h *Heap) GrowInt64s(old []int64, newCap int) []int64 {
	if newCap < len(old) {
		newCap = len(old)
	}
	s := h.AllocInt64s(len(old), newCap)
	copy(s, old)
	return s
}

// AllocFloat64s allocates a []float64 slice backed by arena memory.
// The returned slice has the given length and capacity, zero-filled.
func (h *Heap) AllocFloat64s(length, capacity int) []float64 {
	if capacity < length {
		capacity = length
	}
	if capacity == 0 {
		return nil
	}
	bytes := capacity * 8 // sizeof(float64) == 8
	p := h.AllocBytes(bytes)
	return unsafe.Slice((*float64)(p), capacity)[:length]
}

// GrowFloat64s allocates a new arena-backed float64 slice with newCap capacity,
// copies old data into it, and returns the new slice.
func (h *Heap) GrowFloat64s(old []float64, newCap int) []float64 {
	if newCap < len(old) {
		newCap = len(old)
	}
	s := h.AllocFloat64s(len(old), newCap)
	copy(s, old)
	return s
}

// AllocByteSlice allocates a []byte slice backed by arena memory.
// The returned slice has the given length and capacity, zero-filled.
// This is distinct from AllocBytes(size int) unsafe.Pointer which is used internally.
func (h *Heap) AllocByteSlice(length, capacity int) []byte {
	if capacity < length {
		capacity = length
	}
	if capacity == 0 {
		return nil
	}
	p := h.AllocBytes(capacity)
	return unsafe.Slice((*byte)(p), capacity)[:length]
}

// GrowByteSlice allocates a new arena-backed byte slice with newCap capacity,
// copies old data into it, and returns the new slice.
func (h *Heap) GrowByteSlice(old []byte, newCap int) []byte {
	if newCap < len(old) {
		newCap = len(old)
	}
	s := h.AllocByteSlice(len(old), newCap)
	copy(s, old)
	return s
}

// ---------------------------------------------------------------------------
// Arena-aware append helpers for typed slices
// ---------------------------------------------------------------------------

// arenaAppendInt64 appends a value to an arena-backed int64 slice,
// growing via the heap if needed. Updates the slice pointer in place.
func arenaAppendInt64(h *Heap, s *[]int64, val int64) {
	old := *s
	if len(old) == cap(old) {
		*s = h.GrowInt64s(old, cap(old)*2+1)
		old = *s
	}
	*s = old[:len(old)+1]
	(*s)[len(*s)-1] = val
}

// arenaAppendFloat64 appends a value to an arena-backed float64 slice,
// growing via the heap if needed. Updates the slice pointer in place.
func arenaAppendFloat64(h *Heap, s *[]float64, val float64) {
	old := *s
	if len(old) == cap(old) {
		*s = h.GrowFloat64s(old, cap(old)*2+1)
		old = *s
	}
	*s = old[:len(old)+1]
	(*s)[len(*s)-1] = val
}

// arenaAppendByte appends a value to an arena-backed byte slice,
// growing via the heap if needed. Updates the slice pointer in place.
func arenaAppendByte(h *Heap, s *[]byte, val byte) {
	old := *s
	if len(old) == cap(old) {
		*s = h.GrowByteSlice(old, cap(old)*2+1)
		old = *s
	}
	*s = old[:len(old)+1]
	(*s)[len(*s)-1] = val
}

// arenaAppendValue appends a Value to an arena-backed Value slice,
// growing via the heap if needed. Updates the slice pointer in place.
func arenaAppendValue(h *Heap, s *[]Value, val Value) {
	old := *s
	if len(old) == cap(old) {
		*s = h.GrowValues(old, cap(old)*2+1)
		old = *s
	}
	*s = old[:len(old)+1]
	(*s)[len(*s)-1] = val
}

// ---------------------------------------------------------------------------
// Global default heap
// ---------------------------------------------------------------------------

// DefaultHeap is the global heap instance used by the runtime.
// It is initialized at package load time.
var DefaultHeap *Heap

func init() {
	DefaultHeap = NewHeap()
}
