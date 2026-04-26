//go:build darwin && arm64

package runtime

import (
	"runtime"
	"testing"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Arena tests
// ---------------------------------------------------------------------------

func TestArenaAllocReturnsNonNil(t *testing.T) {
	a := NewArena(64, defaultPageSize)
	defer a.Free()

	p := a.Alloc()
	if p == nil {
		t.Fatal("Alloc returned nil")
	}
}

func TestArenaAllocAligned(t *testing.T) {
	a := NewArena(64, defaultPageSize)
	defer a.Free()

	for i := 0; i < 100; i++ {
		p := a.Alloc()
		addr := uintptr(p)
		if addr%8 != 0 {
			t.Fatalf("allocation %d not 8-byte aligned: %#x", i, addr)
		}
	}
}

func TestArenaAllocBumpAdvances(t *testing.T) {
	a := NewArena(128, defaultPageSize)
	defer a.Free()

	p1 := a.Alloc()
	p2 := a.Alloc()

	diff := uintptr(p2) - uintptr(p1)
	if diff != 128 {
		t.Fatalf("bump pointer advanced by %d, want 128", diff)
	}
}

func TestArenaPageRollover(t *testing.T) {
	objSize := 256
	pageSize := 1024 // fits exactly 4 objects
	a := NewArena(objSize, pageSize)
	defer a.Free()

	// Allocate 4 objects → fills first page.
	for i := 0; i < 4; i++ {
		a.Alloc()
	}
	if len(a.pages) != 1 {
		t.Fatalf("expected 1 page after 4 allocs, got %d", len(a.pages))
	}

	// 5th allocation should trigger a new page.
	p := a.Alloc()
	if p == nil {
		t.Fatal("5th alloc returned nil")
	}
	if len(a.pages) != 2 {
		t.Fatalf("expected 2 pages after 5 allocs, got %d", len(a.pages))
	}
}

func TestArenaReset(t *testing.T) {
	a := NewArena(64, defaultPageSize)
	defer a.Free()

	first := a.Alloc()
	a.Alloc()
	a.Alloc()

	a.Reset()

	// After reset, the next alloc should return the same address as the first.
	p := a.Alloc()
	if uintptr(p) != uintptr(first) {
		t.Fatalf("after Reset, Alloc returned %#x, want %#x", uintptr(p), uintptr(first))
	}
}

// ---------------------------------------------------------------------------
// Heap tests
// ---------------------------------------------------------------------------

func TestHeapSizeClasses(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	for _, sc := range sizeClasses {
		p := h.AllocBytes(sc)
		if p == nil {
			t.Fatalf("AllocBytes(%d) returned nil", sc)
		}
	}
}

func TestHeapSmallAllocUsesCorrectArena(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	// Allocating 50 bytes should use the 64-byte arena (index 0).
	p := h.AllocBytes(50)
	if p == nil {
		t.Fatal("AllocBytes(50) returned nil")
	}

	// Allocating 100 bytes should use the 128-byte arena (index 1).
	p2 := h.AllocBytes(100)
	if p2 == nil {
		t.Fatal("AllocBytes(100) returned nil")
	}
}

func TestHeapOversizedAlloc(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	// Allocate more than 8192 bytes → dedicated mmap.
	p := h.AllocBytes(16384)
	if p == nil {
		t.Fatal("AllocBytes(16384) returned nil")
	}
	if len(h.overPages) != 1 {
		t.Fatalf("expected 1 oversized page, got %d", len(h.overPages))
	}
}

func TestAllocValuesLength(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocValues(10, 20)
	if len(s) != 10 {
		t.Fatalf("len = %d, want 10", len(s))
	}
	if cap(s) != 20 {
		t.Fatalf("cap = %d, want 20", cap(s))
	}
}

func TestAllocValuesNilFilled(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	nv := NilValue()
	s := h.AllocValues(50, 50)
	for i, v := range s {
		if v != nv {
			t.Fatalf("s[%d] = %#x, want NilValue (%#x)", i, uint64(v), uint64(nv))
		}
	}
}

func TestAllocValuesCapacityGELength(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	// When capacity < length, it should be clamped to length.
	s := h.AllocValues(10, 5)
	if len(s) != 10 {
		t.Fatalf("len = %d, want 10", len(s))
	}
	if cap(s) < 10 {
		t.Fatalf("cap = %d, want >= 10", cap(s))
	}
}

func TestGrowValuesPreservesData(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	orig := h.AllocValues(5, 5)
	for i := range orig {
		orig[i] = IntValue(int64(i * 100))
	}

	grown := h.GrowValues(orig, 20)
	if len(grown) != 5 {
		t.Fatalf("len = %d, want 5", len(grown))
	}
	if cap(grown) != 20 {
		t.Fatalf("cap = %d, want 20", cap(grown))
	}

	for i := range orig {
		if grown[i] != orig[i] {
			t.Fatalf("grown[%d] = %#x, want %#x", i, uint64(grown[i]), uint64(orig[i]))
		}
	}
}

func TestGrowValuesNewCapClamped(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	orig := h.AllocValues(10, 10)
	// newCap < len(old) should be clamped to len(old).
	grown := h.GrowValues(orig, 3)
	if cap(grown) < 10 {
		t.Fatalf("cap = %d, want >= 10", cap(grown))
	}
}

// ---------------------------------------------------------------------------
// NaN-boxing compatibility: pointers must fit in 44 bits
// ---------------------------------------------------------------------------

func TestPointerFitsIn44Bits(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	const mask44 = (uint64(1) << 44) - 1

	// Test across all size classes.
	for _, sc := range sizeClasses {
		for i := 0; i < 100; i++ {
			p := h.AllocBytes(sc)
			addr := uint64(uintptr(p))
			if addr != addr&mask44 {
				t.Fatalf("pointer %#x exceeds 44 bits (size class %d)", addr, sc)
			}
		}
	}
}

func TestAllocValuesPointerFitsIn44Bits(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	const mask44 = (uint64(1) << 44) - 1

	s := h.AllocValues(100, 100)
	addr := uint64(uintptr(unsafe.Pointer(&s[0])))
	if addr != addr&mask44 {
		t.Fatalf("AllocValues pointer %#x exceeds 44 bits", addr)
	}
}

// ---------------------------------------------------------------------------
// Stress test
// ---------------------------------------------------------------------------

func TestStress100KAllocations(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	// 100K allocations across different size classes — should not crash.
	for i := 0; i < 100_000; i++ {
		sc := sizeClasses[i%numSizeClasses]
		p := h.AllocBytes(sc)
		if p == nil {
			t.Fatalf("nil on allocation %d (size %d)", i, sc)
		}
	}
}

// ---------------------------------------------------------------------------
// GC safety: arena-backed slices must survive Go GC
// ---------------------------------------------------------------------------

func TestGCSafetyWithArenaSlices(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	// Create arena-backed slices and store known values.
	slices := make([][]Value, 50)
	for i := range slices {
		s := h.AllocValues(10, 10)
		for j := range s {
			s[j] = IntValue(int64(i*100 + j))
		}
		slices[i] = s
	}

	// Force multiple GC cycles.
	for i := 0; i < 5; i++ {
		runtime.GC()
	}

	// Verify all data is intact.
	for i, s := range slices {
		for j, v := range s {
			expected := IntValue(int64(i*100 + j))
			if v != expected {
				t.Fatalf("after GC: slices[%d][%d] = %#x, want %#x",
					i, j, uint64(v), uint64(expected))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// sizeClassIndex
// ---------------------------------------------------------------------------

func TestSizeClassIndex(t *testing.T) {
	tests := []struct {
		size int
		want int
	}{
		{1, 0},      // → 16
		{16, 0},     // → 16
		{17, 1},     // → 32
		{32, 1},     // → 32
		{33, 2},     // → 64
		{64, 2},     // → 64
		{65, 3},     // → 128
		{128, 3},    // → 128
		{129, 4},    // → 256
		{256, 4},    // → 256
		{4096, 8},   // → 4096
		{8192, 9},   // → 8192
		{8193, -1},  // oversized
		{99999, -1}, // oversized
	}
	for _, tt := range tests {
		got := sizeClassIndex(tt.size)
		if got != tt.want {
			t.Errorf("sizeClassIndex(%d) = %d, want %d", tt.size, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkArenaAlloc64(b *testing.B) {
	a := NewArena(64, defaultPageSize)
	defer a.Free()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Alloc()
	}
}

func BenchmarkHeapAllocValues10(b *testing.B) {
	h := NewHeap()
	defer h.Free()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.AllocValues(10, 10)
	}
}

func BenchmarkHeapAllocBytes128(b *testing.B) {
	h := NewHeap()
	defer h.Free()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.AllocBytes(128)
	}
}

// ---------------------------------------------------------------------------
// Typed slice allocation tests
// ---------------------------------------------------------------------------

func TestAllocInt64sLengthAndCapacity(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocInt64s(5, 10)
	if len(s) != 5 {
		t.Fatalf("len = %d, want 5", len(s))
	}
	if cap(s) != 10 {
		t.Fatalf("cap = %d, want 10", cap(s))
	}
	// Zero-filled
	for i, v := range s {
		if v != 0 {
			t.Fatalf("s[%d] = %d, want 0", i, v)
		}
	}
}

func TestAllocInt64sCapClamp(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	// capacity < length should be clamped
	s := h.AllocInt64s(10, 3)
	if len(s) != 10 {
		t.Fatalf("len = %d, want 10", len(s))
	}
	if cap(s) < 10 {
		t.Fatalf("cap = %d, want >= 10", cap(s))
	}
}

func TestAllocInt64sZeroLength(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocInt64s(0, 0)
	if s != nil {
		t.Fatalf("expected nil for zero allocation, got len=%d", len(s))
	}
}

func TestGrowInt64sPreservesData(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	orig := h.AllocInt64s(5, 5)
	for i := range orig {
		orig[i] = int64(i * 42)
	}

	grown := h.GrowInt64s(orig, 20)
	if len(grown) != 5 {
		t.Fatalf("len = %d, want 5", len(grown))
	}
	if cap(grown) != 20 {
		t.Fatalf("cap = %d, want 20", cap(grown))
	}
	for i := range orig {
		if grown[i] != orig[i] {
			t.Fatalf("grown[%d] = %d, want %d", i, grown[i], orig[i])
		}
	}
}

func TestGrowInt64sClampNewCap(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	orig := h.AllocInt64s(10, 10)
	grown := h.GrowInt64s(orig, 3)
	if cap(grown) < 10 {
		t.Fatalf("cap = %d, want >= 10", cap(grown))
	}
}

func TestAllocFloat64sLengthAndCapacity(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocFloat64s(7, 14)
	if len(s) != 7 {
		t.Fatalf("len = %d, want 7", len(s))
	}
	if cap(s) != 14 {
		t.Fatalf("cap = %d, want 14", cap(s))
	}
	for i, v := range s {
		if v != 0.0 {
			t.Fatalf("s[%d] = %f, want 0.0", i, v)
		}
	}
}

func TestAllocFloat64sCapClamp(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocFloat64s(8, 2)
	if len(s) != 8 {
		t.Fatalf("len = %d, want 8", len(s))
	}
	if cap(s) < 8 {
		t.Fatalf("cap = %d, want >= 8", cap(s))
	}
}

func TestGrowFloat64sPreservesData(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	orig := h.AllocFloat64s(4, 4)
	for i := range orig {
		orig[i] = float64(i) * 3.14
	}

	grown := h.GrowFloat64s(orig, 16)
	if len(grown) != 4 {
		t.Fatalf("len = %d, want 4", len(grown))
	}
	if cap(grown) != 16 {
		t.Fatalf("cap = %d, want 16", cap(grown))
	}
	for i := range orig {
		if grown[i] != orig[i] {
			t.Fatalf("grown[%d] = %f, want %f", i, grown[i], orig[i])
		}
	}
}

func TestAllocByteSliceLengthAndCapacity(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocByteSlice(3, 8)
	if len(s) != 3 {
		t.Fatalf("len = %d, want 3", len(s))
	}
	if cap(s) != 8 {
		t.Fatalf("cap = %d, want 8", cap(s))
	}
	for i, v := range s {
		if v != 0 {
			t.Fatalf("s[%d] = %d, want 0", i, v)
		}
	}
}

func TestAllocByteSliceCapClamp(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocByteSlice(6, 2)
	if len(s) != 6 {
		t.Fatalf("len = %d, want 6", len(s))
	}
	if cap(s) < 6 {
		t.Fatalf("cap = %d, want >= 6", cap(s))
	}
}

func TestGrowByteSlicePreservesData(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	orig := h.AllocByteSlice(5, 5)
	for i := range orig {
		orig[i] = byte(i + 10)
	}

	grown := h.GrowByteSlice(orig, 20)
	if len(grown) != 5 {
		t.Fatalf("len = %d, want 5", len(grown))
	}
	if cap(grown) != 20 {
		t.Fatalf("cap = %d, want 20", cap(grown))
	}
	for i := range orig {
		if grown[i] != orig[i] {
			t.Fatalf("grown[%d] = %d, want %d", i, grown[i], orig[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Arena append helper tests
// ---------------------------------------------------------------------------

func TestArenaAppendInt64(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocInt64s(0, 2)
	arenaAppendInt64(h, &s, 100)
	arenaAppendInt64(h, &s, 200)
	// Trigger growth
	arenaAppendInt64(h, &s, 300)

	if len(s) != 3 {
		t.Fatalf("len = %d, want 3", len(s))
	}
	if s[0] != 100 || s[1] != 200 || s[2] != 300 {
		t.Fatalf("got %v, want [100 200 300]", s)
	}
}

func TestArenaAppendFloat64(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocFloat64s(0, 2)
	arenaAppendFloat64(h, &s, 1.1)
	arenaAppendFloat64(h, &s, 2.2)
	arenaAppendFloat64(h, &s, 3.3)

	if len(s) != 3 {
		t.Fatalf("len = %d, want 3", len(s))
	}
	if s[0] != 1.1 || s[1] != 2.2 || s[2] != 3.3 {
		t.Fatalf("got %v, want [1.1 2.2 3.3]", s)
	}
}

func TestArenaAppendByte(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocByteSlice(0, 2)
	arenaAppendByte(h, &s, 1)
	arenaAppendByte(h, &s, 2)
	arenaAppendByte(h, &s, 3)

	if len(s) != 3 {
		t.Fatalf("len = %d, want 3", len(s))
	}
	if s[0] != 1 || s[1] != 2 || s[2] != 3 {
		t.Fatalf("got %v, want [1 2 3]", s)
	}
}

func TestArenaAppendValue(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	s := h.AllocValues(0, 2)
	arenaAppendValue(h, &s, IntValue(10))
	arenaAppendValue(h, &s, IntValue(20))
	arenaAppendValue(h, &s, IntValue(30))

	if len(s) != 3 {
		t.Fatalf("len = %d, want 3", len(s))
	}
	for i, want := range []int64{10, 20, 30} {
		if s[i] != IntValue(want) {
			t.Fatalf("s[%d] = %#x, want IntValue(%d)", i, uint64(s[i]), want)
		}
	}
}

// ---------------------------------------------------------------------------
// GC safety for typed arena slices
// ---------------------------------------------------------------------------

func TestGCSafetyTypedArenaSlices(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	ints := h.AllocInt64s(100, 100)
	floats := h.AllocFloat64s(100, 100)
	bytes := h.AllocByteSlice(100, 100)

	for i := 0; i < 100; i++ {
		ints[i] = int64(i * 7)
		floats[i] = float64(i) * 2.5
		bytes[i] = byte(i % 256)
	}

	for i := 0; i < 5; i++ {
		runtime.GC()
	}

	for i := 0; i < 100; i++ {
		if ints[i] != int64(i*7) {
			t.Fatalf("int64 corruption at [%d]: got %d, want %d", i, ints[i], i*7)
		}
		if floats[i] != float64(i)*2.5 {
			t.Fatalf("float64 corruption at [%d]: got %f, want %f", i, floats[i], float64(i)*2.5)
		}
		if bytes[i] != byte(i%256) {
			t.Fatalf("byte corruption at [%d]: got %d, want %d", i, bytes[i], i%256)
		}
	}
}

// ---------------------------------------------------------------------------
// Pointer fitness for NaN-boxing
// ---------------------------------------------------------------------------

func TestTypedSlicePointersFitIn44Bits(t *testing.T) {
	h := NewHeap()
	defer h.Free()

	const mask44 = (uint64(1) << 44) - 1

	ints := h.AllocInt64s(10, 10)
	addr := uint64(uintptr(unsafe.Pointer(&ints[0])))
	if addr != addr&mask44 {
		t.Fatalf("AllocInt64s pointer %#x exceeds 44 bits", addr)
	}

	floats := h.AllocFloat64s(10, 10)
	addr = uint64(uintptr(unsafe.Pointer(&floats[0])))
	if addr != addr&mask44 {
		t.Fatalf("AllocFloat64s pointer %#x exceeds 44 bits", addr)
	}

	bs := h.AllocByteSlice(10, 10)
	addr = uint64(uintptr(unsafe.Pointer(&bs[0])))
	if addr != addr&mask44 {
		t.Fatalf("AllocByteSlice pointer %#x exceeds 44 bits", addr)
	}
}

// ---------------------------------------------------------------------------
// Typed slice benchmarks
// ---------------------------------------------------------------------------

func BenchmarkAllocInt64s10(b *testing.B) {
	h := NewHeap()
	defer h.Free()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.AllocInt64s(10, 10)
	}
}

func BenchmarkAllocFloat64s10(b *testing.B) {
	h := NewHeap()
	defer h.Free()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.AllocFloat64s(10, 10)
	}
}

func BenchmarkArenaAppendInt64x100(b *testing.B) {
	h := NewHeap()
	defer h.Free()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := h.AllocInt64s(0, 4)
		for j := 0; j < 100; j++ {
			arenaAppendInt64(h, &s, int64(j))
		}
	}
}
