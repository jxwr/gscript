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

	// DenseMatrix descriptor (R43 Phase 2). When dmStride > 0, this
	// Table is a DenseMatrix outer whose rows share the backing at
	// dmFlat. The JIT fast path for `matrix.getf(m, i, j)` loads
	// `*((*float64)(dmFlat) + i*dmStride + j)` in 3 ARM64 insns.
	// dmFlat keeps the backing alive via a Go-managed reference
	// through the row tables; this field aliases the same memory.
	dmFlat   unsafe.Pointer
	dmStride int32

	// arrayHint carries large array capacity hints until the first typed-array
	// promotion. Keep this after all JIT-verified fields.
	arrayHint int
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
	t := DefaultHeap.AllocTable()
	t.keysDirty = true
	return t
}

// NewTableSized creates a table with pre-allocated capacity hints.
func NewTableSized(arrayHint, hashHint int) *Table {
	return NewTableSizedKind(arrayHint, hashHint, ArrayMixed)
}

// NewTableSizedKind creates a table with pre-allocated capacity hints and, for
// scalar array builders, an optional typed-array backing. The mixed layout keeps
// the historical length-1 sentinel allocation; typed arrays start at length 0 so
// key 0 can use the native append path.
func NewTableSizedKind(arrayHint, hashHint int, kind ArrayKind) *Table {
	if arrayHint == 0 && hashHint > 0 && hashHint <= smallFieldCap && kind == ArrayMixed {
		t, svals := DefaultHeap.AllocTableWithSvals(hashHint)
		t.keysDirty = true
		t.svals = svals
		return t
	}
	t := DefaultHeap.AllocTable()
	t.keysDirty = true
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

// RawGetInt retrieves a value by integer key (fast path, no Value boxing).
func (t *Table) RawGetInt(key int64) Value {
	if t.mu != nil {
		t.mu.RLock()
		defer t.mu.RUnlock()
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

// RawGetString retrieves a value by string key (fast path, no Value boxing).
func (t *Table) RawGetString(key string) Value {
	if t.mu != nil {
		t.mu.RLock()
		defer t.mu.RUnlock()
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
		t.mu.RLock()
		defer t.mu.RUnlock()
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

// RawSetStringCached assigns a value by string key using an inline cache hint.
// Uses shapeID-based cache to find existing keys faster on cache hit.
func (t *Table) RawSetStringCached(key string, val Value, cache *FieldCacheEntry) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	t.keysDirty = true

	// ShapeID-based cache: if shape matches, the field index is valid
	idx := cache.FieldIdx
	if t.shapeID != 0 && cache.ShapeID == t.shapeID && idx >= 0 && idx < len(t.svals) {
		if val.IsNil() {
			t.deleteSmallStringField(idx)
			cache.FieldIdx = 0 // reset cache
			cache.ShapeID = 0
		} else {
			t.svals[idx] = val
		}
		return
	}

	if !val.IsNil() &&
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
			if val.IsNil() {
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
		if val.IsNil() {
			delete(t.smap, key)
		} else {
			t.smap[key] = val
		}
		return
	}

	if !val.IsNil() {
		if len(t.skeys) < smallFieldCap {
			preShapeID := t.shapeID
			idx := len(t.svals)
			t.appendSmallStringField(key, val)
			cache.FieldIdx = idx
			cache.ShapeID = t.shapeID
			cache.AppendShapeID = preShapeID
			cache.AppendShape = t.shape
		} else {
			t.smap = make(map[string]Value, len(t.skeys)+1)
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
			t.smap = make(map[string]Value, len(t.skeys)+1)
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

// Next returns the next key/value pair after the given key.
func (t *Table) Next(key Value) (Value, Value, bool) {
	if t.mu != nil {
		t.mu.RLock()
		defer t.mu.RUnlock()
	}
	if t.keysDirty {
		t.rebuildKeys()
	}
	if len(t.keys) == 0 {
		return NilValue(), NilValue(), false
	}
	if key.IsNil() {
		k := t.keys[0]
		return k, t.RawGet(k), true
	}
	for i, k := range t.keys {
		if k.Equal(key) {
			if i+1 < len(t.keys) {
				nk := t.keys[i+1]
				return nk, t.RawGet(nk), true
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

// DMStride returns the DenseMatrix stride; 0 for non-DenseMatrix tables.
// Used by tests and feedback-driven intrinsic gating.
func (t *Table) DMStride() int32 { return t.dmStride }
