package runtime

import (
	"testing"
)

// ==================================================================
// Tests for inline field cache optimization (FieldCacheEntry)
// ==================================================================

// TestFieldCacheBasicLookup verifies that RawGetStringCached returns correct values.
func TestFieldCacheBasicLookup(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	tbl.RawSetString("z", FloatValue(3.0))
	tbl.RawSetString("vx", FloatValue(4.0))
	tbl.RawSetString("vy", FloatValue(5.0))

	var cache FieldCacheEntry

	// First access: cache miss, should do linear scan and populate cache
	if v := tbl.RawGetStringCached("x", &cache); v.Float() != 1.0 {
		t.Errorf("expected x=1.0, got %v", v)
	}

	// Second access: cache hit
	if v := tbl.RawGetStringCached("x", &cache); v.Float() != 1.0 {
		t.Errorf("expected x=1.0 (cached), got %v", v)
	}

	// Different field uses different cache entry
	var cache2 FieldCacheEntry
	if v := tbl.RawGetStringCached("vy", &cache2); v.Float() != 5.0 {
		t.Errorf("expected vy=5.0, got %v", v)
	}

	// Missing key
	var cache3 FieldCacheEntry
	if v := tbl.RawGetStringCached("missing", &cache3); !v.IsNil() {
		t.Errorf("expected nil for missing key, got %v", v)
	}
}

// TestFieldCacheSmallTable verifies correctness for tables with few fields.
func TestFieldCacheSmallTable(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetString("a", IntValue(1))
	tbl.RawSetString("b", IntValue(2))

	var cache FieldCacheEntry
	if v := tbl.RawGetStringCached("a", &cache); v.Int() != 1 {
		t.Errorf("expected a=1, got %v", v)
	}
	if v := tbl.RawGetStringCached("a", &cache); v.Int() != 1 {
		t.Errorf("expected a=1 (cached), got %v", v)
	}
}

// TestFieldCacheInvalidationOnAdd verifies cache invalidation when keys are added.
func TestFieldCacheInvalidationOnAdd(t *testing.T) {
	tbl := NewTable()
	for i := 0; i < 5; i++ {
		tbl.RawSetString(string(rune('a'+i)), IntValue(int64(i)))
	}

	var cache FieldCacheEntry
	_ = tbl.RawGetStringCached("a", &cache) // populate cache

	// Add a new field — skeys may reallocate (cache should handle this)
	tbl.RawSetString("new_field", IntValue(99))

	// Lookup should still work (cache miss triggers re-scan)
	if v := tbl.RawGetStringCached("new_field", &cache); v.Int() != 99 {
		t.Errorf("expected new_field=99, got %v", v)
	}

	// Old field lookup with same cache should adapt
	var cacheA FieldCacheEntry
	if v := tbl.RawGetStringCached("a", &cacheA); v.Int() != 0 {
		t.Errorf("expected a=0, got %v", v)
	}
}

// TestFieldCacheInvalidationOnDelete verifies cache handles deletion correctly.
func TestFieldCacheInvalidationOnDelete(t *testing.T) {
	tbl := NewTable()
	for i := 0; i < 6; i++ {
		tbl.RawSetString(string(rune('a'+i)), IntValue(int64(i)))
	}

	var cache FieldCacheEntry
	_ = tbl.RawGetStringCached("c", &cache) // populate cache

	// Delete key "c"
	tbl.RawSetString("c", NilValue())

	// After deletion, skeys is shorter and possibly reordered.
	// Cache should detect shape change and re-scan.
	if v := tbl.RawGetStringCached("c", &cache); !v.IsNil() {
		t.Errorf("expected nil for deleted key c, got %v", v)
	}

	// Other keys still work
	var cacheA FieldCacheEntry
	if v := tbl.RawGetStringCached("a", &cacheA); v.Int() != 0 {
		t.Errorf("expected a=0, got %v", v)
	}
}

// TestFieldCacheOverwrite verifies that overwriting a value works with cache.
func TestFieldCacheOverwrite(t *testing.T) {
	tbl := NewTable()
	for i := 0; i < 5; i++ {
		tbl.RawSetString(string(rune('a'+i)), IntValue(int64(i)))
	}

	var cache FieldCacheEntry
	if v := tbl.RawGetStringCached("b", &cache); v.Int() != 1 {
		t.Errorf("expected b=1, got %v", v)
	}

	// Overwrite "b" — skeys layout doesn't change, only svals
	tbl.RawSetString("b", IntValue(42))

	// Cache should still hit (skeys pointer/length unchanged for overwrite)
	if v := tbl.RawGetStringCached("b", &cache); v.Int() != 42 {
		t.Errorf("expected b=42 (via cache), got %v", v)
	}
}

// TestFieldCacheNbodyPattern simulates the nbody access pattern:
// multiple tables with same field layout, repeated field access.
func TestFieldCacheNbodyPattern(t *testing.T) {
	// Create 5 bodies with same field layout
	bodies := make([]*Table, 5)
	for i := range bodies {
		body := NewTable()
		body.RawSetString("x", FloatValue(float64(i)*1.0))
		body.RawSetString("y", FloatValue(float64(i)*2.0))
		body.RawSetString("z", FloatValue(float64(i)*3.0))
		body.RawSetString("vx", FloatValue(float64(i)*0.1))
		body.RawSetString("vy", FloatValue(float64(i)*0.2))
		body.RawSetString("vz", FloatValue(float64(i)*0.3))
		body.RawSetString("mass", FloatValue(float64(i+1)*100.0))
		bodies[i] = body
	}

	// Each field access in the inner loop uses the same cache entry
	// (simulates the same GETFIELD instruction accessing different bodies)
	var cacheX, cacheY, cacheMass FieldCacheEntry

	for iter := 0; iter < 50; iter++ {
		for i, body := range bodies {
			x := body.RawGetStringCached("x", &cacheX)
			y := body.RawGetStringCached("y", &cacheY)
			mass := body.RawGetStringCached("mass", &cacheMass)

			if x.Float() != float64(i)*1.0 {
				t.Fatalf("body[%d].x = %v, want %v", i, x.Float(), float64(i)*1.0)
			}
			if y.Float() != float64(i)*2.0 {
				t.Fatalf("body[%d].y = %v, want %v", i, y.Float(), float64(i)*2.0)
			}
			if mass.Float() != float64(i+1)*100.0 {
				t.Fatalf("body[%d].mass = %v, want %v", i, mass.Float(), float64(i+1)*100.0)
			}
		}
	}
}

// TestFieldCacheSetCached verifies RawSetStringCached works correctly.
func TestFieldCacheSetCached(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	tbl.RawSetString("z", FloatValue(3.0))

	var getCache, setCache FieldCacheEntry

	// First read to populate get cache
	if v := tbl.RawGetStringCached("x", &getCache); v.Float() != 1.0 {
		t.Errorf("expected x=1.0, got %v", v)
	}

	// Set via cached path
	tbl.RawSetStringCached("x", FloatValue(99.0), &setCache)

	// Read back (cached)
	if v := tbl.RawGetStringCached("x", &getCache); v.Float() != 99.0 {
		t.Errorf("expected x=99.0, got %v", v)
	}

	// Set again (cache hit path)
	tbl.RawSetStringCached("x", FloatValue(200.0), &setCache)
	if v := tbl.RawGetStringCached("x", &getCache); v.Float() != 200.0 {
		t.Errorf("expected x=200.0, got %v", v)
	}
}

func TestFieldCacheCachedConstructorAppendAcrossTables(t *testing.T) {
	var cacheX, cacheY FieldCacheEntry

	t1 := NewTable()
	t1.RawSetStringCached("x", IntValue(1), &cacheX)
	t1.RawSetStringCached("y", IntValue(2), &cacheY)

	t2 := NewTable()
	t2.RawSetStringCached("x", IntValue(10), &cacheX)
	t2.RawSetStringCached("y", IntValue(20), &cacheY)

	if t1.ShapeID() == 0 || t1.ShapeID() != t2.ShapeID() {
		t.Fatalf("tables should share non-zero constructor shape: %d vs %d", t1.ShapeID(), t2.ShapeID())
	}
	if got := t2.RawGetStringCached("x", &cacheX); got.Int() != 10 {
		t.Fatalf("t2.x = %d, want 10", got.Int())
	}
	if got := t2.RawGetStringCached("y", &cacheY); got.Int() != 20 {
		t.Fatalf("t2.y = %d, want 20", got.Int())
	}
}

func TestNewTableFromCtor2NonNilMatchesCtorShape(t *testing.T) {
	ctor := NewSmallTableCtor2("left", "right")
	left := TableValue(NewEmptyTable())
	right := TableValue(NewEmptyTable())

	tbl := NewTableFromCtor2NonNil(&ctor, left, right)
	if got := tbl.RawGetString("left"); got != left {
		t.Fatalf("left = %v, want %v", got, left)
	}
	if got := tbl.RawGetString("right"); got != right {
		t.Fatalf("right = %v, want %v", got, right)
	}
	if tbl.ShapeID() == 0 || tbl.ShapeID() != ctor.Shape.ID {
		t.Fatalf("shapeID = %d, want ctor shape %d", tbl.ShapeID(), ctor.Shape.ID)
	}
	if got := tbl.SkeysLen(); got != 2 {
		t.Fatalf("skeys=%d, want 2", got)
	}
}

func TestFieldCacheSharedShapeDeleteDoesNotMutatePeerKeys(t *testing.T) {
	t1 := NewTable()
	t1.RawSetString("x", IntValue(1))
	t1.RawSetString("y", IntValue(2))
	t1.RawSetString("z", IntValue(3))

	t2 := NewTable()
	t2.RawSetString("x", IntValue(10))
	t2.RawSetString("y", IntValue(20))
	t2.RawSetString("z", IntValue(30))

	if t1.ShapeID() == 0 || t1.ShapeID() != t2.ShapeID() {
		t.Fatalf("tables should share initial shape: %d vs %d", t1.ShapeID(), t2.ShapeID())
	}

	t1.RawSetString("y", NilValue())

	var cacheY, cacheZ FieldCacheEntry
	if got := t1.RawGetStringCached("y", &cacheY); !got.IsNil() {
		t.Fatalf("deleted t1.y = %v, want nil", got)
	}
	if got := t2.RawGetStringCached("y", &cacheY); got.Int() != 20 {
		t.Fatalf("t2.y = %d, want 20", got.Int())
	}
	if got := t2.RawGetStringCached("z", &cacheZ); got.Int() != 30 {
		t.Fatalf("t2.z = %d, want 30", got.Int())
	}
}

// TestFieldCacheTransitionToSmap verifies cache handles smap transition.
func TestFieldCacheTransitionToSmap(t *testing.T) {
	tbl := NewTable()
	var cache FieldCacheEntry

	// Add keys up to smap transition
	for i := 0; i <= smallFieldCap; i++ {
		key := string(rune('a' + i))
		tbl.RawSetString(key, IntValue(int64(i)))
	}

	// After transition, skeys is nil, smap is used
	// Cache should still return correct values (falls through to smap)
	if v := tbl.RawGetStringCached("a", &cache); v.Int() != 0 {
		t.Errorf("expected a=0, got %v", v)
	}
}

// TestFieldCacheWithIteration verifies cache doesn't break Next() iteration.
func TestFieldCacheWithIteration(t *testing.T) {
	tbl := NewTable()
	tbl.RawSetString("x", IntValue(10))
	tbl.RawSetString("y", IntValue(20))
	tbl.RawSetString("z", IntValue(30))
	tbl.RawSetString("w", IntValue(40))
	tbl.RawSetString("v", IntValue(50))

	// Use cached access
	var cache FieldCacheEntry
	_ = tbl.RawGetStringCached("x", &cache)

	// Iterate and collect all values
	sum := int64(0)
	count := 0
	k, v, ok := tbl.Next(NilValue())
	for ok {
		sum += v.Int()
		count++
		k, v, ok = tbl.Next(k)
	}

	if count != 5 {
		t.Errorf("expected 5 entries, got %d", count)
	}
	if sum != 150 {
		t.Errorf("expected sum=150, got %d", sum)
	}
}

// ==================================================================
// Benchmarks: cached vs uncached field access
// ==================================================================

// BenchmarkTableRawGetString7Fields benchmarks uncached RawGetString with 7 fields.
func BenchmarkTableRawGetString7Fields(b *testing.B) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	tbl.RawSetString("z", FloatValue(3.0))
	tbl.RawSetString("vx", FloatValue(4.0))
	tbl.RawSetString("vy", FloatValue(5.0))
	tbl.RawSetString("vz", FloatValue(6.0))
	tbl.RawSetString("mass", FloatValue(7.0))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.RawGetString("x")
		_ = tbl.RawGetString("y")
		_ = tbl.RawGetString("z")
		_ = tbl.RawGetString("vx")
		_ = tbl.RawGetString("vy")
		_ = tbl.RawGetString("vz")
		_ = tbl.RawGetString("mass")
	}
}

// BenchmarkTableRawGetStringCached7Fields benchmarks cached access with 7 fields.
func BenchmarkTableRawGetStringCached7Fields(b *testing.B) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	tbl.RawSetString("z", FloatValue(3.0))
	tbl.RawSetString("vx", FloatValue(4.0))
	tbl.RawSetString("vy", FloatValue(5.0))
	tbl.RawSetString("vz", FloatValue(6.0))
	tbl.RawSetString("mass", FloatValue(7.0))

	var cX, cY, cZ, cVX, cVY, cVZ, cMass FieldCacheEntry
	// Warm up caches
	_ = tbl.RawGetStringCached("x", &cX)
	_ = tbl.RawGetStringCached("y", &cY)
	_ = tbl.RawGetStringCached("z", &cZ)
	_ = tbl.RawGetStringCached("vx", &cVX)
	_ = tbl.RawGetStringCached("vy", &cVY)
	_ = tbl.RawGetStringCached("vz", &cVZ)
	_ = tbl.RawGetStringCached("mass", &cMass)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.RawGetStringCached("x", &cX)
		_ = tbl.RawGetStringCached("y", &cY)
		_ = tbl.RawGetStringCached("z", &cZ)
		_ = tbl.RawGetStringCached("vx", &cVX)
		_ = tbl.RawGetStringCached("vy", &cVY)
		_ = tbl.RawGetStringCached("vz", &cVZ)
		_ = tbl.RawGetStringCached("mass", &cMass)
	}
}

// BenchmarkTableRawGetString3Fields benchmarks uncached RawGetString with 3 fields.
func BenchmarkTableRawGetString3Fields(b *testing.B) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	tbl.RawSetString("z", FloatValue(3.0))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.RawGetString("x")
		_ = tbl.RawGetString("y")
		_ = tbl.RawGetString("z")
	}
}

// BenchmarkTableRawGetStringCached3Fields benchmarks cached access with 3 fields.
func BenchmarkTableRawGetStringCached3Fields(b *testing.B) {
	tbl := NewTable()
	tbl.RawSetString("x", FloatValue(1.0))
	tbl.RawSetString("y", FloatValue(2.0))
	tbl.RawSetString("z", FloatValue(3.0))

	var cX, cY, cZ FieldCacheEntry
	_ = tbl.RawGetStringCached("x", &cX)
	_ = tbl.RawGetStringCached("y", &cY)
	_ = tbl.RawGetStringCached("z", &cZ)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.RawGetStringCached("x", &cX)
		_ = tbl.RawGetStringCached("y", &cY)
		_ = tbl.RawGetStringCached("z", &cZ)
	}
}

// BenchmarkTableFieldIndex8Fields benchmarks FieldIndex with 8 fields.
func BenchmarkTableFieldIndex8Fields(b *testing.B) {
	tbl := NewTable()
	tbl.RawSetString("name", StringValue("sun"))
	tbl.RawSetString("x", FloatValue(0.0))
	tbl.RawSetString("y", FloatValue(0.0))
	tbl.RawSetString("z", FloatValue(0.0))
	tbl.RawSetString("vx", FloatValue(0.0))
	tbl.RawSetString("vy", FloatValue(0.0))
	tbl.RawSetString("vz", FloatValue(0.0))
	tbl.RawSetString("mass", FloatValue(39.478))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.FieldIndex("mass")
		_ = tbl.FieldIndex("x")
		_ = tbl.FieldIndex("vx")
	}
}

// BenchmarkTableRawGetStringCachedMultiBody benchmarks cached access
// across multiple bodies sharing the same field layout (nbody pattern).
func BenchmarkTableRawGetStringCachedMultiBody(b *testing.B) {
	bodies := make([]*Table, 5)
	for i := range bodies {
		body := NewTable()
		body.RawSetString("x", FloatValue(float64(i)))
		body.RawSetString("y", FloatValue(float64(i)))
		body.RawSetString("z", FloatValue(float64(i)))
		body.RawSetString("vx", FloatValue(float64(i)))
		body.RawSetString("vy", FloatValue(float64(i)))
		body.RawSetString("vz", FloatValue(float64(i)))
		body.RawSetString("mass", FloatValue(float64(i)))
		bodies[i] = body
	}

	// One cache per field (shared across bodies, like the same VM instruction)
	var cX, cY, cZ, cVX, cVY, cVZ, cMass FieldCacheEntry

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, body := range bodies {
			_ = body.RawGetStringCached("x", &cX)
			_ = body.RawGetStringCached("y", &cY)
			_ = body.RawGetStringCached("z", &cZ)
			_ = body.RawGetStringCached("vx", &cVX)
			_ = body.RawGetStringCached("vy", &cVY)
			_ = body.RawGetStringCached("vz", &cVZ)
			_ = body.RawGetStringCached("mass", &cMass)
		}
	}
}

// BenchmarkTableRawGetStringMultiBody benchmarks uncached access for comparison.
func BenchmarkTableRawGetStringMultiBody(b *testing.B) {
	bodies := make([]*Table, 5)
	for i := range bodies {
		body := NewTable()
		body.RawSetString("x", FloatValue(float64(i)))
		body.RawSetString("y", FloatValue(float64(i)))
		body.RawSetString("z", FloatValue(float64(i)))
		body.RawSetString("vx", FloatValue(float64(i)))
		body.RawSetString("vy", FloatValue(float64(i)))
		body.RawSetString("vz", FloatValue(float64(i)))
		body.RawSetString("mass", FloatValue(float64(i)))
		bodies[i] = body
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, body := range bodies {
			_ = body.RawGetString("x")
			_ = body.RawGetString("y")
			_ = body.RawGetString("z")
			_ = body.RawGetString("vx")
			_ = body.RawGetString("vy")
			_ = body.RawGetString("vz")
			_ = body.RawGetString("mass")
		}
	}
}
