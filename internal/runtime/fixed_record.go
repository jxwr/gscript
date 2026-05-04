package runtime

import (
	"sync/atomic"
	"unsafe"
)

const fixedRecordInlineCap = 5

// FixedRecord is an immutable fixed-shape table payload until generic table
// semantics are required. It is a guardable scalar-replacement carrier for
// object literals whose fields are read before mutation or iteration.
type FixedRecord struct {
	ctor         *SmallTableCtorN
	materialized *Table
	shapeID      uint32
	n            uint8
	values       [fixedRecordInlineCap]Value
}

const fixedRecordSlabSize = 8192

type fixedRecordSlab struct {
	backing []FixedRecord
}

const fixedRecordSlotSize = uintptr(unsafe.Sizeof(FixedRecord{}))

func (s *fixedRecordSlab) alloc(h *Heap) *FixedRecord {
	for {
		if fr := h.tryAllocFixedRecordFast(); fr != nil {
			return fr
		}
		s.refill(h)
	}
}

func (s *fixedRecordSlab) refill(h *Heap) {
	if h != nil {
		atomic.StoreUintptr(&h.fixedRecordSlabNext, 0)
	}
	next := make([]FixedRecord, fixedRecordSlabSize)
	s.backing = next
	if h != nil {
		h.publishFixedRecordSlab(next)
	}
}

func (h *Heap) publishFixedRecordSlab(backing []FixedRecord) {
	if len(backing) == 0 {
		atomic.StoreUintptr(&h.fixedRecordSlabNext, 0)
		atomic.StoreUintptr(&h.fixedRecordSlabStart, 0)
		atomic.StoreUintptr(&h.fixedRecordSlabEnd, 0)
		return
	}
	root := unsafe.Pointer(&backing[0])
	start := uintptr(root)
	end := start + uintptr(len(backing))*fixedRecordSlotSize
	keepAlive(root, nil)

	atomic.StoreUintptr(&h.fixedRecordSlabNext, 0)
	atomic.StoreUintptr(&h.fixedRecordSlabStart, 0)
	atomic.StoreUintptr(&h.fixedRecordSlabEnd, end)
	atomic.StoreUintptr(&h.fixedRecordSlabStart, start)
	atomic.StoreUintptr(&h.fixedRecordSlabNext, start)
}

//go:nocheckptr
func (h *Heap) tryAllocFixedRecordFast() *FixedRecord {
	if h == nil {
		return nil
	}
	for {
		next := atomic.LoadUintptr(&h.fixedRecordSlabNext)
		if next == 0 {
			return nil
		}
		end := atomic.LoadUintptr(&h.fixedRecordSlabEnd)
		if end == 0 || next > end-fixedRecordSlotSize {
			return nil
		}
		if atomic.CompareAndSwapUintptr(&h.fixedRecordSlabNext, next, next+fixedRecordSlotSize) {
			return (*FixedRecord)(unsafe.Pointer(next))
		}
	}
}

func (h *Heap) AllocFixedRecord() *FixedRecord {
	if fr := h.tryAllocFixedRecordFast(); fr != nil {
		return fr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fixedRecordSlab.alloc(h)
}

func NewFixedRecordValue(ctor *SmallTableCtorN, vals []Value) (Value, bool) {
	if ctor == nil || ctor.Shape == nil || len(vals) < len(ctor.Keys) {
		return NilValue(), false
	}
	n := len(ctor.Keys)
	if n == 0 || n > fixedRecordInlineCap {
		return NilValue(), false
	}
	var fr *FixedRecord
	if DefaultHeap != nil {
		fr = DefaultHeap.AllocFixedRecord()
	} else {
		fr = &FixedRecord{}
	}
	fr.ctor = ctor
	fr.materialized = nil
	fr.shapeID = ctor.shapeID
	fr.n = uint8(n)
	for i := 0; i < n; i++ {
		v := vals[i]
		if v.IsNil() {
			return NilValue(), false
		}
		fr.values[i] = v
	}
	p := unsafe.Pointer(fr)
	if DefaultHeap == nil {
		keepAlive(p, fr)
	}
	return Value(tagPtr | ptrSubFixedRecord | (uint64(uintptr(p)) & ptrAddrMask)), true
}

func NewFixedRecordValue5(ctor *SmallTableCtorN, v0, v1, v2, v3, v4 Value) (Value, bool) {
	if ctor == nil || ctor.Shape == nil || len(ctor.Keys) != 5 {
		return NilValue(), false
	}
	if v0.IsNil() || v1.IsNil() || v2.IsNil() || v3.IsNil() || v4.IsNil() {
		return NilValue(), false
	}
	var fr *FixedRecord
	if DefaultHeap != nil {
		fr = DefaultHeap.AllocFixedRecord()
	} else {
		fr = &FixedRecord{}
	}
	fr.ctor = ctor
	fr.materialized = nil
	fr.shapeID = ctor.shapeID
	fr.n = 5
	fr.values[0] = v0
	fr.values[1] = v1
	fr.values[2] = v2
	fr.values[3] = v3
	fr.values[4] = v4
	p := unsafe.Pointer(fr)
	if DefaultHeap == nil {
		keepAlive(p, fr)
	}
	return Value(tagPtr | ptrSubFixedRecord | (uint64(uintptr(p)) & ptrAddrMask)), true
}

func (v Value) FixedRecord() *FixedRecord {
	if uint64(v)&tagMask != tagPtr || v.ptrSubType() != ptrSubFixedRecord {
		return nil
	}
	p := v.ptrPayload()
	if p == nil {
		return nil
	}
	return (*FixedRecord)(p)
}

func (v Value) FixedRecordRawGetString(key string) (Value, bool) {
	fr := v.FixedRecord()
	if fr == nil {
		return NilValue(), false
	}
	return fr.rawGetString(key), true
}

func (fr *FixedRecord) rawGetString(key string) Value {
	if fr == nil {
		return NilValue()
	}
	if fr.materialized != nil {
		return fr.materialized.RawGetString(key)
	}
	n := int(fr.n)
	for i := 0; i < n; i++ {
		if fr.ctor.Keys[i] == key {
			return fr.values[i]
		}
	}
	return NilValue()
}

func (fr *FixedRecord) FieldIndex(key string) int {
	if fr == nil || fr.materialized != nil {
		return -1
	}
	n := int(fr.n)
	for i := 0; i < n; i++ {
		if fr.ctor.Keys[i] == key {
			return i
		}
	}
	return -1
}

func (fr *FixedRecord) ShapeID() uint32 {
	if fr == nil {
		return 0
	}
	return fr.shapeID
}

func (fr *FixedRecord) materialize() *Table {
	if fr == nil {
		return nil
	}
	if fr.materialized != nil {
		return fr.materialized
	}
	n := int(fr.n)
	t := NewTableFromCtorN(fr.ctor, fr.values[:n])
	fr.materialized = t
	return t
}

func FixedRecordOffsets() (shapeID, n, values uintptr) {
	var fr FixedRecord
	return unsafe.Offsetof(fr.shapeID), unsafe.Offsetof(fr.n), unsafe.Offsetof(fr.values)
}

func scanFixedRecordRoots(fr *FixedRecord, visitor func(unsafe.Pointer), seen map[uintptr]struct{}) {
	if fr == nil {
		return
	}
	if fr.materialized != nil {
		p := unsafe.Pointer(fr.materialized)
		visitTableRoot(p, visitor)
		if _, already := seen[uintptr(p)]; !already {
			seen[uintptr(p)] = struct{}{}
			scanTableRoots(fr.materialized, visitor, seen)
		}
	}
	n := int(fr.n)
	for i := 0; i < n; i++ {
		ScanValueRoots(fr.values[i], visitor, seen)
	}
}
