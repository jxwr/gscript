package runtime

import (
	"sync"
	"unsafe"
)

// ArrayKind indicates the type specialization of a table's array part.
type ArrayKind uint8

const (
	ArrayMixed ArrayKind = 0 // []Value (current, default)
	ArrayInt   ArrayKind = 1 // []int64 (int and bool values)
	ArrayFloat ArrayKind = 2 // []float64
	ArrayBool  ArrayKind = 3 // []byte (1 byte per bool, no GC pointers)
)

// smallFieldCap is the threshold for using flat slices vs maps for string keys.
const smallFieldCap = 12
const initialStringMapCap = 64

// SmallFieldCap is the maximum string-field count retained in the small shaped
// table representation.
const SmallFieldCap = smallFieldCap

// Table is GScript's associative array / object type.
// Tables have an optimized array part for sequential integer keys 1..n,
// flat slices for small string-keyed tables (most GScript objects),
// and maps for larger tables.
//
// Tables start WITHOUT a mutex (fast single-threaded path). When shared
// across goroutines, call SetConcurrent(true) to enable locking.
type Table struct {
	mu    *sync.RWMutex   // nil for single-threaded tables (fast default)
	array []Value         // 0-indexed: array[0] is usable by user code
	imap  map[int64]Value // integer keys not in array range
	// String keys: small tables use canonical Shape.FieldKeys plus per-table
	// values, large tables use map. Do not mutate skeys in place: it may be
	// shared by every table with the same shape.
	skeys     []string         // parallel with svals for small tables
	svals     []Value          // parallel with skeys for small tables
	smap      map[string]Value // only for tables with >smallFieldCap string keys
	hash      map[Value]Value  // everything else (bool, float, table, function keys)
	metatable *Table
	keys      []Value // ordered keys for Next() iteration
	keysDirty bool
	// Type-specialized array fields (placed at end to preserve existing offsets)
	arrayKind  ArrayKind
	shapeID    uint32 // shape identifier for field cache validation
	intArray   []int64
	floatArray []float64
	boolArray  []byte // 1 byte per bool, no GC pointers → zero GC scan
	// Encoding: 0 = nil/unset, 1 = false, 2 = true
	// shape is the hidden-class descriptor for the string-keyed fields.
	// Always nil when shapeID == 0 (empty table or hash-mode table).
	// Kept in sync with shapeID by applyShape / clearShape.
	shape *Shape

	// DenseMatrix descriptor. When dmStride > 0, this Table is a DenseMatrix
	// outer whose rows share the backing at dmFlat. The JIT fast path for
	// nested float loads reads *((*float64)(dmFlat) + i*dmStride + j).
	// Backing storage is kept alive by dmMeta and by row floatArray slices;
	// dmFlat is only the JIT load address.
	dmFlat   unsafe.Pointer
	dmStride int32

	// arrayHint carries large array capacity hints until the first typed-array
	// promotion. Keep this after all JIT-verified fields.
	arrayHint int
	// dmMeta is a cold DenseMatrix side pointer. DenseMatrix outers and adopted
	// rows share one metadata object so every Table pays one pointer, not a
	// backing slice header plus parent pointer.
	dmMeta *denseMatrixMeta

	// lazyTree is a semantics-preserving deferred representation for qualified
	// fixed recursive two-field table builders. Generic table operations
	// materialize it before mutation or iteration.
	lazyTree *LazyRecursiveTable
}

// SetConcurrent enables or disables mutex protection for concurrent access.
func (t *Table) SetConcurrent(on bool) {
	if on && t.mu == nil {
		t.mu = &sync.RWMutex{}
	}
}

// cleanHashKey normalizes a Value for use as a Go map key.
// With NaN-boxing, Value is uint64 so map keys compare by bits.
// We still normalize float/int/string to ensure consistent hashing
// (e.g., -0.0 vs 0.0, or equivalent int/float representations).
func cleanHashKey(key Value) Value {
	switch key.Type() {
	case TypeInt:
		return IntValue(key.Int())
	case TypeFloat:
		return FloatValue(key.Float())
	case TypeString:
		return StringValue(key.Str())
	default:
		return key
	}
}

// NewTable creates a new empty table (non-concurrent by default).
func NewTable() *Table {
	return NewEmptyTable()
}

// NewEmptyTable creates an empty table with a clean iteration-key cache.
// Mutating operations mark keysDirty before adding or removing entries, so a
// fresh table does not need an initial rebuild for pairs/Next semantics.
func NewEmptyTable() *Table {
	t := DefaultHeap.AllocTable()
	return t
}

// NewTableSized creates a table with pre-allocated capacity hints.
func NewTableSized(arrayHint, hashHint int) *Table {
	if arrayHint == 0 && hashHint == 0 {
		return NewEmptyTable()
	}
	return NewTableSizedKind(arrayHint, hashHint, ArrayMixed)
}

// NewTableSizedKind creates a table with pre-allocated capacity hints and, for
// scalar array builders, an optional typed-array backing. The mixed layout keeps
// the historical length-1 sentinel allocation; typed arrays start at length 0 so
// key 0 can use the native append path.
func NewTableSizedKind(arrayHint, hashHint int, kind ArrayKind) *Table {
	if arrayHint == 0 && hashHint == 0 {
		return NewEmptyTable()
	}
	if arrayHint == 0 && hashHint > 0 && hashHint <= smallFieldCap && kind == ArrayMixed {
		t, svals := DefaultHeap.AllocTableWithSvals(hashHint)
		t.svals = svals
		return t
	}
	t := DefaultHeap.AllocTable()
	if arrayHint > 0 {
		capHint := arrayHint + 1
		switch kind {
		case ArrayInt:
			t.arrayKind = ArrayInt
			t.intArray = DefaultHeap.AllocInt64s(0, capHint)
		case ArrayFloat:
			t.arrayKind = ArrayFloat
			t.floatArray = DefaultHeap.AllocFloat64s(0, capHint)
		case ArrayBool:
			t.arrayKind = ArrayBool
			t.boolArray = DefaultHeap.AllocByteSlice(0, capHint)
		default:
			if capHint <= sparseArrayMax+1 {
				t.array = DefaultHeap.AllocValues(1, capHint)
			} else {
				t.array = DefaultHeap.AllocValues(1, sparseArrayMax+1)
				t.arrayHint = capHint
			}
		}
	}
	if hashHint > 0 && hashHint <= smallFieldCap {
		t.svals = DefaultHeap.AllocValues(0, hashHint)
	}
	return t
}

// NewSequentialArrayTable creates a table whose 1-based array part has exactly
// length slots ready for direct sequential fill by runtime builders.
func NewSequentialArrayTable(length int) *Table {
	if length <= 0 {
		return NewEmptyTable()
	}
	t := DefaultHeap.AllocTable()
	t.array = DefaultHeap.AllocValues(length+1, length+1)
	return t
}

// RawGet retrieves a value by key, bypassing metamethods.
func (t *Table) RawGet(key Value) Value {
	if key.IsNil() {
		return NilValue()
	}
	if key.Type() == TypeInt {
		return t.RawGetInt(key.Int())
	}
	if key.Type() == TypeString {
		return t.RawGetString(key.Str())
	}
	// General hash for other types
	if t.mu != nil {
		t.mu.RLock()
		defer t.mu.RUnlock()
	}
	if t.hash != nil {
		if val, ok := t.hash[cleanHashKey(key)]; ok {
			return val
		}
	}
	return NilValue()
}

func (t *Table) rawGetForNextLocked(key Value) Value {
	if key.IsNil() {
		return NilValue()
	}
	if key.Type() == TypeInt {
		k := key.Int()
		if t.lazyTree != nil {
			return NilValue()
		}
		switch t.arrayKind {
		case ArrayInt:
			if k >= 0 && k < int64(len(t.intArray)) {
				return IntValue(t.intArray[k])
			}
		case ArrayFloat:
			if k >= 0 && k < int64(len(t.floatArray)) {
				return FloatValue(t.floatArray[k])
			}
		case ArrayBool:
			if k >= 0 && k < int64(len(t.boolArray)) {
				b := t.boolArray[k]
				if b == 0 {
					return NilValue()
				}
				return BoolValue(b == 2)
			}
		default:
			if k >= 0 && k < int64(len(t.array)) {
				return t.array[k]
			}
		}
		if t.imap != nil {
			if v, ok := t.imap[k]; ok {
				return v
			}
		}
		return NilValue()
	}
	if key.Type() == TypeString {
		k := key.Str()
		if t.lazyTree != nil {
			return t.lazyTree.get(t, k)
		}
		for i, field := range t.skeys {
			if field == k {
				return t.svals[i]
			}
		}
		if t.smap != nil {
			if v, ok := t.smap[k]; ok {
				return v
			}
		}
		return NilValue()
	}
	if t.hash != nil {
		if val, ok := t.hash[cleanHashKey(key)]; ok {
			return val
		}
	}
	return NilValue()
}

// RawGetInt retrieves a value by integer key (fast path, no Value boxing).
func (t *Table) RawGetInt(key int64) Value {
	if t.mu != nil {
		t.mu.RLock()
		defer t.mu.RUnlock()
	}
	if t.lazyTree != nil {
		return NilValue()
	}
	switch t.arrayKind {
	case ArrayInt:
		if key >= 0 && key < int64(len(t.intArray)) {
			return IntValue(t.intArray[key])
		}
	case ArrayFloat:
		if key >= 0 && key < int64(len(t.floatArray)) {
			return FloatValue(t.floatArray[key])
		}
	case ArrayBool:
		if key >= 0 && key < int64(len(t.boolArray)) {
			b := t.boolArray[key]
			if b == 0 { // nil/unset
				return NilValue()
			}
			return BoolValue(b == 2) // 1=false, 2=true
		}
	default:
		if key >= 0 && key < int64(len(t.array)) {
			return t.array[key]
		}
	}
	if t.imap != nil {
		if v, ok := t.imap[key]; ok {
			return v
		}
	}
	return NilValue()
}

// FieldIndex returns the index of a string key in the skeys slice, or -1 if not found.
// Used by the trace JIT to capture field positions at recording time.
func (t *Table) FieldIndex(key string) int {
	for i, k := range t.skeys {
		if k == key {
			return i
		}
	}
	return -1
}

// SkeysLen returns the length of the skeys slice.
func (t *Table) SkeysLen() int {
	return len(t.skeys)
}

// SvalsGet returns the value at index i in the svals slice.
// Used by the SSA interpreter (golden model) to access fields by index.
func (t *Table) SvalsGet(i int) Value {
	if i >= 0 && i < len(t.svals) {
		return t.svals[i]
	}
	return NilValue()
}

// SvalsSet sets the value at index i in the svals slice.
// Used by the SSA interpreter (golden model) to write fields by index.
func (t *Table) SvalsSet(i int, v Value) {
	if i >= 0 && i < len(t.svals) {
		t.svals[i] = v
		t.keysDirty = true
	}
}

// HasMetatable returns true if the table has a metatable.
func (t *Table) HasMetatable() bool {
	return t.metatable != nil
}

// FieldCacheEntry is a hint-based inline cache entry for field access.
// It caches the index of a field name in a table's skeys slice and the
// table's shapeID when the cache was populated. On lookup, if the table's
// shapeID matches the cached shapeID, the field index is valid without
// needing string comparison. Works across different tables with the
// same field layout (e.g., all nbody body tables).
type FieldCacheEntry struct {
	FieldIdx      int    // cached index into skeys/svals (-1 = not cached)
	ShapeID       uint32 // shapeID when cache was populated for existing-field access
	AppendShapeID uint32 // pre-append shapeID for constructor-style SETFIELD
	AppendShape   *Shape // result shape for constructor-style SETFIELD
}

// FieldPolyCacheWays is the number of polymorphic static string-field cache
// entries assigned to each bytecode PC.
const FieldPolyCacheWays = 4

// FieldPolyCacheEntry caches a static string-field lookup by table shape.
// It complements FieldCacheEntry's monomorphic fast path for object dispatch
// sites that alternate among a small number of stable shapes.
type FieldPolyCacheEntry struct {
	FieldIdx int
	ShapeID  uint32
}

// FieldPolyCacheSlot returns the cache ways for one bytecode PC.
func FieldPolyCacheSlot(cache []FieldPolyCacheEntry, pc int) []FieldPolyCacheEntry {
	if pc < 0 {
		return nil
	}
	start := pc * FieldPolyCacheWays
	end := start + FieldPolyCacheWays
	if start < 0 || end > len(cache) {
		return nil
	}
	return cache[start:end]
}

// TableStringKeyCacheWays is the number of polymorphic dynamic string-key
// table cache entries assigned to each bytecode PC.
const TableStringKeyCacheWays = 8

// TableStringKeyCacheEntry caches a dynamic string-key table lookup by string
// backing pointer/length plus table shape. It is a hint only: callers must
// fall back to the normal table path on miss.
type TableStringKeyCacheEntry struct {
	Key      string
	KeyData  uintptr
	KeyLen   int
	FieldIdx int
	ShapeID  uint32
}

// TableStringKeyCacheSlot returns the cache ways for one bytecode PC.
func TableStringKeyCacheSlot(cache []TableStringKeyCacheEntry, pc int) []TableStringKeyCacheEntry {
	if pc < 0 {
		return nil
	}
	start := pc * TableStringKeyCacheWays
	end := start + TableStringKeyCacheWays
	if start < 0 || end > len(cache) {
		return nil
	}
	return cache[start:end]
}

func stringCacheKey(key string) (uintptr, int) {
	if len(key) == 0 {
		return 0, 0
	}
	return uintptr(unsafe.Pointer(unsafe.StringData(key))), len(key)
}

func dynamicStringCacheReplaceIndex(data uintptr, keyLen, n int) int {
	if n <= 1 {
		return 0
	}
	h := (data >> 4) ^ (data >> 12) ^ uintptr(keyLen)
	return int(h % uintptr(n))
}

func fieldPolyCacheReplaceIndex(shapeID uint32, n int) int {
	if n <= 1 {
		return 0
	}
	return int(shapeID % uint32(n))
}

func (t *Table) rememberFieldPolyCacheLocked(fieldIdx int, cache []FieldPolyCacheEntry) {
	if t.shapeID == 0 || fieldIdx < 0 || fieldIdx >= len(t.svals) || len(cache) == 0 {
		return
	}
	empty := -1
	for i := range cache {
		entry := &cache[i]
		if entry.ShapeID == t.shapeID {
			entry.FieldIdx = fieldIdx
			return
		}
		if empty < 0 && entry.ShapeID == 0 {
			empty = i
		}
	}
	if empty < 0 {
		empty = fieldPolyCacheReplaceIndex(t.shapeID, len(cache))
	}
	cache[empty] = FieldPolyCacheEntry{
		FieldIdx: fieldIdx,
		ShapeID:  t.shapeID,
	}
}

func (t *Table) lookupDynamicStringCacheLocked(data uintptr, keyLen int, cache []TableStringKeyCacheEntry) (int, bool) {
	shapeID := t.shapeID
	if shapeID == 0 || len(cache) == 0 {
		return 0, false
	}
	for i := range cache {
		entry := &cache[i]
		if entry.ShapeID == shapeID && entry.KeyData == data && entry.KeyLen == keyLen {
			idx := entry.FieldIdx
			if idx >= 0 && idx < len(t.svals) {
				return idx, true
			}
			return 0, false
		}
	}
	return 0, false
}

func (t *Table) rememberDynamicStringCacheLocked(key string, data uintptr, keyLen, fieldIdx int, cache []TableStringKeyCacheEntry) {
	if t.shapeID == 0 || fieldIdx < 0 || fieldIdx >= len(t.svals) || len(cache) == 0 {
		return
	}
	empty := -1
	for i := range cache {
		entry := &cache[i]
		if entry.ShapeID == t.shapeID && entry.KeyData == data && entry.KeyLen == keyLen {
			entry.FieldIdx = fieldIdx
			return
		}
		if empty < 0 && entry.ShapeID == 0 {
			empty = i
		}
	}
	if empty < 0 {
		empty = dynamicStringCacheReplaceIndex(data, keyLen, len(cache))
	}
	cache[empty] = TableStringKeyCacheEntry{
		Key:      key,
		KeyData:  data,
		KeyLen:   keyLen,
		FieldIdx: fieldIdx,
		ShapeID:  t.shapeID,
	}
}

// SmallTableCtor2 caches the final shape for a static two-string-field table
// constructor. It is stored on bytecode prototypes, not on Table, so the common
// object-literal allocation path can skip per-instance shape transitions
// without growing every table.
type SmallTableCtor2 struct {
	Key1 string
	Key2 string

	Shape *Shape

	shapeID   uint32
	fieldKeys []string
	single1   smallCtorShape
	single2   smallCtorShape
}

// SmallTableCtorN caches the final shape for static small string-field table
// constructors with more than two fields. It is the generic counterpart to
// SmallTableCtor2; nil runtime values still take the sequential RawSetString
// fallback so table literal omission semantics are preserved.
type SmallTableCtorN struct {
	Keys []string

	Shape *Shape

	shapeID   uint32
	fieldKeys []string
}

type smallCtorShape struct {
	shape     *Shape
	shapeID   uint32
	fieldKeys []string
}

func newSmallCtorShape(shape *Shape) smallCtorShape {
	if shape == nil {
		return smallCtorShape{}
	}
	return smallCtorShape{
		shape:     shape,
		shapeID:   shape.ID,
		fieldKeys: shape.FieldKeys,
	}
}

func NewSmallTableCtor2(key1, key2 string) SmallTableCtor2 {
	ctor := SmallTableCtor2{Key1: key1, Key2: key2}
	ctor.single1 = newSmallCtorShape(getOrCreateSingleFieldShape(key1))
	ctor.single2 = newSmallCtorShape(getOrCreateSingleFieldShape(key2))
	if key1 != key2 {
		ctor.Shape = GetShape([]string{key1, key2})
		ctor.shapeID = ctor.Shape.ID
		ctor.fieldKeys = ctor.Shape.FieldKeys
	}
	return ctor
}

func NewSmallTableCtorN(keys []string) SmallTableCtorN {
	owned := append([]string(nil), keys...)
	ctor := SmallTableCtorN{Keys: owned}
	if len(owned) == 0 {
		return ctor
	}
	seen := make(map[string]struct{}, len(owned))
	for _, key := range owned {
		if _, ok := seen[key]; ok {
			return ctor
		}
		seen[key] = struct{}{}
	}
	ctor.Shape = GetShape(owned)
	if ctor.Shape != nil {
		ctor.shapeID = ctor.Shape.ID
		ctor.fieldKeys = ctor.Shape.FieldKeys
	}
	return ctor
}

// NewTableFromCtor2 constructs a small two-field string table in one pass.
// Runtime nil values omit their fields just like sequential SETFIELD bytecode.
func NewTableFromCtor2(ctor *SmallTableCtor2, val1, val2 Value) *Table {
	if ctor != nil {
		shape := ctor.Shape
		if shape != nil && !val1.IsNil() && !val2.IsNil() {
			return newTableFromCtor2Shape(ctor, shape, val1, val2)
		}
	}
	return newTableFromCtor2Fallback(ctor, val1, val2)
}

// NewTableFromCtor2NonNil constructs a cacheable two-field string table when
// the caller has already proven both values are non-nil. It is equivalent to
// NewTableFromCtor2 for valid cacheable constructors and non-nil values, but
// avoids the generic nil/duplicate-key fallback checks in native constructor
// protocols.
func NewTableFromCtor2NonNil(ctor *SmallTableCtor2, val1, val2 Value) *Table {
	if ctor == nil || ctor.Shape == nil || val1.IsNil() || val2.IsNil() {
		return NewTableFromCtor2(ctor, val1, val2)
	}
	return newTableFromCtor2Shape(ctor, ctor.Shape, val1, val2)
}

func newTableFromCtor2Shape(ctor *SmallTableCtor2, shape *Shape, val1, val2 Value) *Table {
	t, svals := DefaultHeap.AllocTableWithSvals2()
	t.svals = svals
	t.svals[0] = val1
	t.svals[1] = val2
	t.shape = shape
	t.shapeID = ctor.shapeID
	t.skeys = ctor.fieldKeys
	return t
}

// NewTableFromCtorN constructs a fixed-shape small string table in one pass
// when all runtime values are non-nil. If any value is nil, it falls back to
// sequential RawSetString so omitted fields and duplicate-key behavior match
// ordinary table literal execution.
func NewTableFromCtorN(ctor *SmallTableCtorN, vals []Value) *Table {
	if ctor != nil && ctor.Shape != nil && len(vals) >= len(ctor.Keys) {
		n := len(ctor.Keys)
		t, svals := DefaultHeap.AllocTableWithSvals(n)
		t.svals = svals[:n]
		for i := 0; i < n; i++ {
			v := vals[i]
			if v.IsNil() {
				return newTableFromCtorNFallback(ctor, vals)
			}
			t.svals[i] = v
		}
		t.shape = ctor.Shape
		t.shapeID = ctor.shapeID
		t.skeys = ctor.fieldKeys
		return t
	}
	return newTableFromCtorNFallback(ctor, vals)
}

func newTableFromCtorNFallback(ctor *SmallTableCtorN, vals []Value) *Table {
	if ctor == nil || len(ctor.Keys) == 0 {
		return NewEmptyTable()
	}
	t := NewTableSized(0, len(ctor.Keys))
	for i, key := range ctor.Keys {
		if i >= len(vals) {
			break
		}
		t.RawSetString(key, vals[i])
	}
	return t
}

func newTableFromCtor2Fallback(ctor *SmallTableCtor2, val1, val2 Value) *Table {
	if ctor == nil {
		return NewTableSized(0, 2)
	}
	if ctor.Key1 == ctor.Key2 || ctor.Shape == nil {
		t := NewTableSized(0, 2)
		t.RawSetString(ctor.Key1, val1)
		t.RawSetString(ctor.Key2, val2)
		return t
	}

	val1Nil := val1.IsNil()
	val2Nil := val2.IsNil()
	if val1Nil {
		if val2Nil {
			return NewTableSized(0, 0)
		}
		return newTableFromCtorShape1(ctor.single2, val2)
	}
	if val2Nil {
		return newTableFromCtorShape1(ctor.single1, val1)
	}

	t := NewTableSized(0, 2)
	t.RawSetString(ctor.Key1, val1)
	t.RawSetString(ctor.Key2, val2)
	return t
}

func newTableFromCtorShape1(shape smallCtorShape, val Value) *Table {
	if shape.shape == nil {
		return NewTableSized(0, 0)
	}
	t, svals := DefaultHeap.AllocTableWithSvals1()
	t.svals = svals
	t.svals[0] = val
	t.shape = shape.shape
	t.shapeID = shape.shapeID
	t.skeys = shape.fieldKeys
	return t
}

// RawGetString retrieves a value by string key (fast path, no Value boxing).
func (t *Table) RawGetString(key string) Value {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		return t.lazyTree.get(t, key)
	}
	for i, k := range t.skeys {
		if k == key {
			return t.svals[i]
		}
	}
	if t.smap != nil {
		if v, ok := t.smap[key]; ok {
			return v
		}
	}
	return NilValue()
}

// RawGetStringCached retrieves a value by string key using an inline cache hint.
// The cache stores the field index and the table's shapeID from a previous lookup.
// On cache hit (shapeID match), avoids both string comparison and O(n) scan.
// Works across different tables sharing the same field layout.
func (t *Table) RawGetStringCached(key string, cache *FieldCacheEntry) Value {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		return t.lazyTree.get(t, key)
	}
	// ShapeID-based cache: if shape matches, the field index is valid
	idx := cache.FieldIdx
	if t.shapeID != 0 && cache.ShapeID == t.shapeID && idx >= 0 && idx < len(t.svals) {
		return t.svals[idx]
	}
	// Cache miss — linear scan and update cache
	for i, k := range t.skeys {
		if k == key {
			cache.FieldIdx = i
			cache.ShapeID = t.shapeID
			return t.svals[i]
		}
	}
	if t.smap != nil {
		if v, ok := t.smap[key]; ok {
			return v
		}
	}
	return NilValue()
}

// RawGetStringCachedPoly retrieves a static string field and also populates a
// small polymorphic shape cache for the bytecode PC. The monomorphic cache is
// still updated because it remains the shortest native fast path.
func (t *Table) RawGetStringCachedPoly(key string, cache *FieldCacheEntry, poly []FieldPolyCacheEntry) Value {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		return t.lazyTree.get(t, key)
	}
	idx := cache.FieldIdx
	if t.shapeID != 0 && cache.ShapeID == t.shapeID && idx >= 0 && idx < len(t.svals) {
		t.rememberFieldPolyCacheLocked(idx, poly)
		return t.svals[idx]
	}
	for i, k := range t.skeys {
		if k == key {
			cache.FieldIdx = i
			cache.ShapeID = t.shapeID
			t.rememberFieldPolyCacheLocked(i, poly)
			return t.svals[i]
		}
	}
	if t.smap != nil {
		if v, ok := t.smap[key]; ok {
			return v
		}
	}
	return NilValue()
}

// RawGetStringDynamicCached retrieves a dynamic string key using a small
// polymorphic per-PC cache. The cache is valid only for shaped small-string
// tables; misses and large string maps use the normal path.
func (t *Table) RawGetStringDynamicCached(key string, cache []TableStringKeyCacheEntry) Value {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		return t.lazyTree.get(t, key)
	}
	data, keyLen := stringCacheKey(key)
	if idx, ok := t.lookupDynamicStringCacheLocked(data, keyLen, cache); ok {
		return t.svals[idx]
	}
	for i, k := range t.skeys {
		if k == key {
			t.rememberDynamicStringCacheLocked(key, data, keyLen, i, cache)
			return t.svals[i]
		}
	}
	if t.smap != nil {
		if v, ok := t.smap[key]; ok {
			return v
		}
	}
	return NilValue()
}

// RawSetStringCached assigns a value by string key using an inline cache hint.
// Uses shapeID-based cache to find existing keys faster on cache hit.
func (t *Table) RawSetStringCached(key string, val Value, cache *FieldCacheEntry) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		t.materializeLazyTreeLocked()
	}
	valIsNil := val.IsNil()
	if valIsNil && t.shapeID == 0 && len(t.skeys) == 0 && t.smap == nil {
		return
	}
	t.keysDirty = true

	// ShapeID-based cache: if shape matches, the field index is valid
	idx := cache.FieldIdx
	if t.shapeID != 0 && cache.ShapeID == t.shapeID && idx >= 0 && idx < len(t.svals) {
		if valIsNil {
			t.deleteSmallStringField(idx)
			cache.FieldIdx = 0 // reset cache
			cache.ShapeID = 0
		} else {
			t.svals[idx] = val
		}
		return
	}

	if !valIsNil &&
		t.smap == nil &&
		cache.AppendShapeID == t.shapeID &&
		idx == len(t.svals) &&
		idx == len(t.skeys) &&
		idx < smallFieldCap {
		if cache.AppendShape != nil {
			t.appendSmallStringValue(val)
			t.applyShape(cache.AppendShape)
		} else {
			t.appendSmallStringField(key, val)
			cache.AppendShape = t.shape
		}
		cache.ShapeID = t.shapeID
		return
	}

	// Fall back to normal path
	for i, k := range t.skeys {
		if k == key {
			if valIsNil {
				t.deleteSmallStringField(i)
			} else {
				t.svals[i] = val
				cache.FieldIdx = i
				cache.ShapeID = t.shapeID
			}
			return
		}
	}

	if t.smap != nil {
		if valIsNil {
			delete(t.smap, key)
		} else {
			t.smap[key] = val
		}
		return
	}

	if !valIsNil {
		if len(t.skeys) < smallFieldCap {
			preShapeID := t.shapeID
			idx := len(t.svals)
			t.appendSmallStringField(key, val)
			cache.FieldIdx = idx
			cache.ShapeID = t.shapeID
			cache.AppendShapeID = preShapeID
			cache.AppendShape = t.shape
		} else {
			t.smap = make(map[string]Value, initialStringMapCap)
			for i, k := range t.skeys {
				t.smap[k] = t.svals[i]
			}
			t.smap[key] = val
			t.skeys = nil
			t.svals = nil
			t.setShape(nil)
		}
	}
}

// RawSetStringDynamicCached assigns a dynamic string key and updates the
// per-PC polymorphic cache when the key resolves to a small shaped-table field.
func (t *Table) RawSetStringDynamicCached(key string, val Value, cache []TableStringKeyCacheEntry) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		t.materializeLazyTreeLocked()
	}
	valIsNil := val.IsNil()
	if valIsNil && t.shapeID == 0 && len(t.skeys) == 0 && t.smap == nil {
		return
	}
	t.keysDirty = true
	data, keyLen := stringCacheKey(key)

	if !valIsNil {
		if idx, ok := t.lookupDynamicStringCacheLocked(data, keyLen, cache); ok {
			t.svals[idx] = val
			return
		}
	}

	for i, k := range t.skeys {
		if k == key {
			if valIsNil {
				t.deleteSmallStringField(i)
			} else {
				t.svals[i] = val
				t.rememberDynamicStringCacheLocked(key, data, keyLen, i, cache)
			}
			return
		}
	}

	if t.smap != nil {
		if valIsNil {
			delete(t.smap, key)
		} else {
			t.smap[key] = val
		}
		return
	}

	if !valIsNil {
		if len(t.skeys) < smallFieldCap {
			idx := len(t.svals)
			t.appendSmallStringField(key, val)
			t.rememberDynamicStringCacheLocked(key, data, keyLen, idx, cache)
		} else {
			t.smap = make(map[string]Value, initialStringMapCap)
			for i, k := range t.skeys {
				t.smap[k] = t.svals[i]
			}
			t.smap[key] = val
			t.skeys = nil
			t.svals = nil
			t.setShape(nil)
		}
	}
}

// RawSet assigns a value by key, bypassing metamethods.
func (t *Table) RawSet(key, val Value) {
	if key.IsNil() {
		return
	}
	kt := key.Type()
	if kt == TypeFloat && floatIsInt(key.Float()) {
		key = IntValue(int64(key.Float()))
		kt = TypeInt
	}
	if kt == TypeInt {
		t.RawSetInt(key.Int(), val)
		return
	}
	if kt == TypeString {
		t.RawSetString(key.Str(), val)
		return
	}
	// General hash
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		t.materializeLazyTreeLocked()
	}
	t.keysDirty = true
	if t.hash == nil {
		if val.IsNil() {
			return
		}
		t.hash = make(map[Value]Value)
	}
	ck := cleanHashKey(key)
	if val.IsNil() {
		delete(t.hash, ck)
	} else {
		t.hash[ck] = val
	}
}

// setShape updates both t.shape and t.shapeID from the current skeys.
// Pass nil/empty skeys to clear (hash-mode or empty table).
// Must be called with lock held (if mu != nil).
func (t *Table) setShape(skeys []string) {
	t.applyShape(GetShape(skeys))
}

func (t *Table) applyShape(s *Shape) {
	t.shape = s
	if s != nil {
		t.shapeID = s.ID
		t.skeys = s.FieldKeys
	} else {
		t.shapeID = 0
		t.skeys = nil
	}
}

// appendShape advances the hidden-class descriptor for the common case where a
// new string field is appended. It avoids rebuilding the full joined shape key
// for every object with the same field insertion order.
func (t *Table) appendShape(key string) {
	var s *Shape
	if t.shape != nil {
		s = t.shape.Transition(key)
	} else {
		s = getOrCreateSingleFieldShape(key)
	}
	t.shape = s
	if s != nil {
		t.shapeID = s.ID
		t.skeys = s.FieldKeys
	} else {
		t.shapeID = 0
		t.skeys = nil
	}
}

func (t *Table) appendSmallStringField(key string, val Value) {
	t.appendSmallStringValue(val)
	t.appendShape(key)
}

func (t *Table) appendSmallStringValue(val Value) {
	if len(t.svals) < cap(t.svals) {
		n := len(t.svals)
		t.svals = t.svals[:n+1]
		t.svals[n] = val
	} else {
		arenaAppendValue(DefaultHeap, &t.svals, val)
	}
}

// deleteSmallStringField removes skeys[idx]/svals[idx] from a small string
// table. skeys may alias an immutable Shape.FieldKeys slice, so this must not
// mutate the key slice in place. Value order follows the historical swap-delete
// behavior used by RawSetString.
func (t *Table) deleteSmallStringField(idx int) {
	last := len(t.skeys) - 1
	if idx < 0 || idx > last {
		return
	}
	if idx != last {
		t.svals[idx] = t.svals[last]
	}
	t.svals = t.svals[:last]
	if last == 0 {
		t.setShape(nil)
		return
	}
	keys := make([]string, last)
	copy(keys, t.skeys[:last])
	if idx != last {
		keys[idx] = t.skeys[last]
	}
	t.setShape(keys)
}

// RawSetString assigns a value by string key (fast path).
func (t *Table) RawSetString(key string, val Value) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.lazyTree != nil {
		t.materializeLazyTreeLocked()
	}
	if val.IsNil() && t.shapeID == 0 && len(t.skeys) == 0 && t.smap == nil {
		return
	}
	t.keysDirty = true

	for i, k := range t.skeys {
		if k == key {
			if val.IsNil() {
				t.deleteSmallStringField(i)
			} else {
				t.svals[i] = val
			}
			return
		}
	}

	if t.smap != nil {
		if val.IsNil() {
			delete(t.smap, key)
		} else {
			t.smap[key] = val
		}
		return
	}

	if !val.IsNil() {
		if len(t.skeys) < smallFieldCap {
			t.appendSmallStringField(key, val)
		} else {
			t.smap = make(map[string]Value, initialStringMapCap)
			for i, k := range t.skeys {
				t.smap[k] = t.svals[i]
			}
			t.smap[key] = val
			t.skeys = nil
			t.svals = nil
			t.setShape(nil)
		}
	}
}

// Length returns the length of the array part (the # operator).
func (t *Table) Length() int {
	switch t.arrayKind {
	case ArrayInt:
		// All slots are valid (no nil concept for int64), length is always full.
		if len(t.intArray) == 0 {
			return 0
		}
		return len(t.intArray) - 1
	case ArrayFloat:
		// All slots are valid for float64 as well.
		if len(t.floatArray) == 0 {
			return 0
		}
		return len(t.floatArray) - 1
	case ArrayBool:
		// Scan backwards past nil sentinels (0 = unset)
		n := len(t.boolArray) - 1
		for n > 0 && t.boolArray[n] == 0 {
			n--
		}
		return n
	default:
		if len(t.array) == 0 {
			return 0
		}
		n := len(t.array) - 1
		for n > 0 && t.array[n].IsNil() {
			n--
		}
		return n
	}
}

// Len returns the length of the array part (alias for Length, used by VM).
func (t *Table) Len() int {
	return t.Length()
}

// Append adds a value to the end of the array part.
func (t *Table) Append(v Value) {
	n := t.Length()
	t.RawSet(IntValue(int64(n+1)), v)
}

// rebuildKeys rebuilds the ordered key list for iteration.
func (t *Table) rebuildKeys() {
	if t.lazyTree != nil {
		t.materializeLazyTreeLocked()
	}
	t.keys = t.keys[:0]
	// Note: typed int/float arrays start from index 1 because we can't
	// distinguish a user-written 0 from the default zero value at index 0.
	// Mixed/bool arrays start from index 0 since we can check for nil.
	switch t.arrayKind {
	case ArrayInt:
		for i := 1; i < len(t.intArray); i++ {
			t.keys = append(t.keys, IntValue(int64(i)))
		}
	case ArrayFloat:
		for i := 1; i < len(t.floatArray); i++ {
			t.keys = append(t.keys, IntValue(int64(i)))
		}
	case ArrayBool:
		for i := 0; i < len(t.boolArray); i++ {
			if t.boolArray[i] != 0 { // skip nil/unset slots
				t.keys = append(t.keys, IntValue(int64(i)))
			}
		}
	default:
		for i := 0; i < len(t.array); i++ {
			if !t.array[i].IsNil() {
				t.keys = append(t.keys, IntValue(int64(i)))
			}
		}
	}
	for k, v := range t.imap {
		if !v.IsNil() {
			t.keys = append(t.keys, IntValue(k))
		}
	}
	// Flat string slices
	for i, k := range t.skeys {
		if !t.svals[i].IsNil() {
			t.keys = append(t.keys, StringValue(k))
		}
	}
	// Large string map
	for k, v := range t.smap {
		if !v.IsNil() {
			t.keys = append(t.keys, StringValue(k))
		}
	}
	for k, v := range t.hash {
		if !v.IsNil() {
			t.keys = append(t.keys, k)
		}
	}
	t.keysDirty = false
}

func (t *Table) needsKeyRebuild() bool {
	if t.lazyTree != nil {
		return true
	}
	if t.keysDirty {
		return true
	}
	if len(t.keys) != 0 {
		return false
	}
	switch t.arrayKind {
	case ArrayInt:
		if len(t.intArray) > 1 {
			return true
		}
	case ArrayFloat:
		if len(t.floatArray) > 1 {
			return true
		}
	case ArrayBool:
		for _, b := range t.boolArray {
			if b != 0 {
				return true
			}
		}
	default:
		for _, v := range t.array {
			if !v.IsNil() {
				return true
			}
		}
	}
	for _, v := range t.imap {
		if !v.IsNil() {
			return true
		}
	}
	for _, v := range t.svals {
		if !v.IsNil() {
			return true
		}
	}
	for _, v := range t.smap {
		if !v.IsNil() {
			return true
		}
	}
	for _, v := range t.hash {
		if !v.IsNil() {
			return true
		}
	}
	return false
}

// Next returns the next key/value pair after the given key.
func (t *Table) Next(key Value) (Value, Value, bool) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	if t.needsKeyRebuild() {
		t.rebuildKeys()
	}
	if len(t.keys) == 0 {
		return NilValue(), NilValue(), false
	}
	if key.IsNil() {
		k := t.keys[0]
		return k, t.rawGetForNextLocked(k), true
	}
	for i, k := range t.keys {
		if k.Equal(key) {
			if i+1 < len(t.keys) {
				nk := t.keys[i+1]
				return nk, t.rawGetForNextLocked(nk), true
			}
			return NilValue(), NilValue(), false
		}
	}
	return NilValue(), NilValue(), false
}

// GetMetatable returns the table's metatable, or nil.
func (t *Table) GetMetatable() *Table {
	return t.metatable
}

// SetMetatable sets the table's metatable.
func (t *Table) SetMetatable(mt *Table) {
	t.metatable = mt
}

// TableFieldOffsets returns the byte offsets of key Table fields for JIT verification.
// This allows the JIT to verify its hardcoded offsets match the actual struct layout.
func TableFieldOffsets() (arrayKind, intArray, floatArray, boolArray uintptr) {
	var t Table
	return unsafe.Offsetof(t.arrayKind), unsafe.Offsetof(t.intArray), unsafe.Offsetof(t.floatArray), unsafe.Offsetof(t.boolArray)
}

// TableMapOffsets returns byte offsets of sparse integer/general hash maps for JIT verification.
func TableMapOffsets() (imap, hash uintptr) {
	var t Table
	return unsafe.Offsetof(t.imap), unsafe.Offsetof(t.hash)
}

// TableStringMapOffset returns the byte offset of the large string-key map.
func TableStringMapOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.smap)
}

// TableTypedArrayCapOffsets returns byte offsets of typed-array cap fields for JIT verification.
func TableTypedArrayCapOffsets() (intArrayCap, floatArrayCap, boolArrayCap uintptr) {
	var t Table
	return unsafe.Offsetof(t.intArray) + 16, unsafe.Offsetof(t.floatArray) + 16, unsafe.Offsetof(t.boolArray) + 16
}

// TableKeysDirtyOffset returns the byte offset of the keysDirty field for JIT verification.
func TableKeysDirtyOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.keysDirty)
}

// TableLazyTreeOffset returns the byte offset of the lazy recursive table side
// pointer for JIT guards that must not treat lazy tables as empty shape-less
// tables.
func TableLazyTreeOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.lazyTree)
}

// ShapeID returns the table's shape identifier.
func (t *Table) ShapeID() uint32 { return t.shapeID }

// TableShapeIDOffset returns the offset of shapeID for JIT verification.
func TableShapeIDOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.shapeID)
}

// GetArrayKind returns the array kind for testing/JIT inspection.
func (t *Table) GetArrayKind() ArrayKind {
	return t.arrayKind
}

// PlainFloatArrayForNumericKernel exposes the backing float array for guarded
// whole-function numeric kernels. It is intentionally narrower than RawGetInt:
// callers may only use the slice when ordinary table indexing for keys
// 0..n-1 is known to hit a plain, non-concurrent, non-lazy float array without
// metamethod fallback.
func (t *Table) PlainFloatArrayForNumericKernel(n int) ([]float64, bool) {
	if n < 0 || t == nil || t.mu != nil || t.lazyTree != nil || t.metatable != nil || t.arrayKind != ArrayFloat {
		return nil, false
	}
	if len(t.floatArray) < n {
		return nil, false
	}
	return t.floatArray, true
}

// PlainArrayValuesForRecordKernel exposes the mixed array prefix for guarded
// whole-call record kernels. It is intentionally narrow: callers may only use
// it when ordinary table indexing for keys 1..n is known to hit a plain mixed
// array without metamethod or concurrent-table fallback.
func (t *Table) PlainArrayValuesForRecordKernel(n int) ([]Value, bool) {
	if n < 0 || t == nil || t.mu != nil || t.lazyTree != nil || t.metatable != nil || t.arrayKind != ArrayMixed {
		return nil, false
	}
	if len(t.array) <= n {
		return nil, false
	}
	return t.array, true
}

// LoadFloatRecordForNumericKernel copies numeric string fields from a stable
// small-field record into out. Int fields are accepted with normal numeric
// widening; non-numeric fields make the guard fail.
func (t *Table) LoadFloatRecordForNumericKernel(shapeID uint32, idxs []int, out []float64) bool {
	if t == nil || t.mu != nil || t.lazyTree != nil || t.metatable != nil || t.shapeID == 0 || t.shapeID != shapeID {
		return false
	}
	if len(idxs) > len(out) {
		return false
	}
	for i, idx := range idxs {
		if idx < 0 || idx >= len(t.svals) {
			return false
		}
		v := t.svals[idx]
		switch v.Type() {
		case TypeFloat:
			out[i] = v.Float()
		case TypeInt:
			out[i] = float64(v.Int())
		default:
			return false
		}
	}
	return true
}

// StoreFloatRecordForNumericKernel writes float fields back to a stable
// small-field record. Shape and table-kind guards are repeated so a stale
// cached plan cannot write through a mutated table layout.
func (t *Table) StoreFloatRecordForNumericKernel(shapeID uint32, idxs []int, vals []float64) bool {
	if t == nil || t.mu != nil || t.lazyTree != nil || t.metatable != nil || t.shapeID == 0 || t.shapeID != shapeID {
		return false
	}
	if len(idxs) > len(vals) {
		return false
	}
	for i, idx := range idxs {
		if idx < 0 || idx >= len(t.svals) {
			return false
		}
		t.svals[idx] = FloatValue(vals[i])
	}
	t.keysDirty = true
	return true
}

// MarkArrayMutationForNumericKernel mirrors RawSetInt's observable iteration
// invalidation for guarded kernels that overwrite existing array slots.
func (t *Table) MarkArrayMutationForNumericKernel() {
	t.keysDirty = true
}

// TableDMFlatOffset / TableDMStrideOffset return the byte offsets of
// the DenseMatrix descriptor fields for JIT verification (R43).
func TableDMFlatOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.dmFlat)
}

func TableDMStrideOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.dmStride)
}

func TableDMMetaOffset() uintptr {
	var t Table
	return unsafe.Offsetof(t.dmMeta)
}

func DenseMatrixMetaOffsets() (backingData, backingLen, backingCap, parent uintptr) {
	var m denseMatrixMeta
	backing := unsafe.Offsetof(m.backing)
	return backing, backing + 8, backing + 16, unsafe.Offsetof(m.parent)
}

// DMStride returns the DenseMatrix stride; 0 for non-DenseMatrix tables.
// Used by tests and feedback-driven intrinsic gating.
func (t *Table) DMStride() int32 { return t.dmStride }
