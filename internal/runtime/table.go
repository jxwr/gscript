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
	mu        *sync.RWMutex    // nil for single-threaded tables (fast default)
	array     []Value          // 0-indexed: array[0] is usable by user code
	imap      map[int64]Value  // integer keys not in array range
	// String keys: small tables use flat slices, large tables use map
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
	arr := DefaultHeap.AllocValues(1, 1)
	return &Table{
		array:     arr,
		keysDirty: true,
	}
}

// NewTableSized creates a table with pre-allocated capacity hints.
func NewTableSized(arrayHint, hashHint int) *Table {
	t := &Table{keysDirty: true}
	if arrayHint > 0 {
		t.array = DefaultHeap.AllocValues(1, arrayHint+1)
	} else {
		t.array = DefaultHeap.AllocValues(1, 1)
	}
	if hashHint > 0 && hashHint <= smallFieldCap {
		t.skeys = make([]string, 0, hashHint)
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
	FieldIdx int    // cached index into skeys/svals (-1 = not cached)
	ShapeID  uint32 // shapeID when cache was populated
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
			last := len(t.skeys) - 1
			if idx != last {
				t.skeys[idx] = t.skeys[last]
				t.svals[idx] = t.svals[last]
			}
			t.skeys = t.skeys[:last]
			t.svals = t.svals[:last]
			t.shapeID = GetShapeID(t.skeys)
			cache.FieldIdx = 0 // reset cache
			cache.ShapeID = 0
		} else {
			t.svals[idx] = val
		}
		return
	}

	// Fall back to normal path
	for i, k := range t.skeys {
		if k == key {
			if val.IsNil() {
				last := len(t.skeys) - 1
				t.skeys[i] = t.skeys[last]
				t.svals[i] = t.svals[last]
				t.skeys = t.skeys[:last]
				t.svals = t.svals[:last]
				t.shapeID = GetShapeID(t.skeys)
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
			t.skeys = append(t.skeys, key)
			arenaAppendValue(DefaultHeap, &t.svals, val)
			t.shapeID = GetShapeID(t.skeys)
			cache.FieldIdx = len(t.skeys) - 1
			cache.ShapeID = t.shapeID
		} else {
			t.smap = make(map[string]Value, len(t.skeys)+1)
			for i, k := range t.skeys {
				t.smap[k] = t.svals[i]
			}
			t.smap[key] = val
			t.skeys = nil
			t.svals = nil
			t.shapeID = 0
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

// sparseArrayMax is the maximum array size for auto-expansion of sparse keys.
// Keys like board[col*100+row] (range 101-910) will use array instead of imap.
const sparseArrayMax = 1024

// classifyValueForArray returns the ArrayKind that a value would require.
// TypeInt maps to ArrayInt; TypeFloat maps to ArrayFloat;
// TypeBool maps to ArrayBool ([]byte, 1B/element, no GC pointers);
// everything else maps to ArrayMixed.
func classifyValueForArray(val Value) ArrayKind {
	switch val.Type() {
	case TypeInt:
		return ArrayInt
	case TypeFloat:
		return ArrayFloat
	case TypeBool:
		return ArrayBool
	default:
		return ArrayMixed
	}
}

// demoteToMixed converts a typed array (intArray, floatArray, or boolArray) back to the
// generic []Value array. Must be called with lock held (if mu != nil).
func (t *Table) demoteToMixed() {
	switch t.arrayKind {
	case ArrayInt:
		n := len(t.intArray)
		t.array = DefaultHeap.AllocValues(n, n)
		for i := 0; i < n; i++ {
			t.array[i] = IntValue(t.intArray[i])
		}
		t.intArray = nil
	case ArrayFloat:
		n := len(t.floatArray)
		t.array = DefaultHeap.AllocValues(n, n)
		for i := 0; i < n; i++ {
			t.array[i] = FloatValue(t.floatArray[i])
		}
		t.floatArray = nil
	case ArrayBool:
		n := len(t.boolArray)
		t.array = DefaultHeap.AllocValues(n, n)
		for i := 0; i < n; i++ {
			switch t.boolArray[i] {
			case 0: // nil/unset
				t.array[i] = NilValue()
			case 1: // false
				t.array[i] = BoolValue(false)
			default: // 2 = true
				t.array[i] = BoolValue(true)
			}
		}
		t.boolArray = nil
	}
	t.arrayKind = ArrayMixed
}

// typedArrayLen returns the length of whichever backing array is active.
func (t *Table) typedArrayLen() int {
	switch t.arrayKind {
	case ArrayInt:
		return len(t.intArray)
	case ArrayFloat:
		return len(t.floatArray)
	case ArrayBool:
		return len(t.boolArray)
	default:
		return len(t.array)
	}
}

// valueToInt64 converts a Value to int64 for storage in intArray.
func valueToInt64(val Value) int64 {
	return val.Int()
}

// RawSetInt assigns a value by integer key (fast path).
func (t *Table) RawSetInt(key int64, val Value) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	t.keysDirty = true

	arrLen := int64(t.typedArrayLen())

	// --- Fast path: key within existing array bounds (not append) ---
	if key >= 0 && key < arrLen {
		switch t.arrayKind {
		case ArrayInt:
			vk := classifyValueForArray(val)
			if vk == ArrayInt {
				t.intArray[key] = valueToInt64(val)
				return
			}
			// Type mismatch or nil → demote
			t.demoteToMixed()
			t.array[key] = val
			return
		case ArrayFloat:
			if val.Type() == TypeFloat {
				t.floatArray[key] = val.Float()
				return
			}
			t.demoteToMixed()
			t.array[key] = val
			return
		case ArrayBool:
			if val.Type() == TypeBool {
				if val.Bool() {
					t.boolArray[key] = 2 // true
				} else {
					t.boolArray[key] = 1 // false
				}
				return
			}
			if val.IsNil() {
				t.boolArray[key] = 0 // nil/unset
				return
			}
			// Type mismatch → demote
			t.demoteToMixed()
			t.array[key] = val
			return
		default:
			// ArrayMixed: check if this is the first real write to a fresh table
			// (array has only the sentinel at [0]). If so, try to promote to typed array.
			// This handles 0-indexed code like `row[0] = 3.14` on a new table.
			if arrLen == 1 && key == 0 && !val.IsNil() {
				vk := classifyValueForArray(val)
				switch vk {
				case ArrayInt:
					t.arrayKind = ArrayInt
					t.intArray = make([]int64, 1, 1)
					t.intArray[0] = valueToInt64(val)
					t.array = nil
					return
				case ArrayFloat:
					t.arrayKind = ArrayFloat
					t.floatArray = make([]float64, 1, 1)
					t.floatArray[0] = val.Float()
					t.array = nil
					return
				case ArrayBool:
					t.arrayKind = ArrayBool
					t.boolArray = make([]byte, 1, 1)
					if val.Bool() {
						t.boolArray[0] = 2
					} else {
						t.boolArray[0] = 1
					}
					t.array = nil
					return
				}
			}
			t.array[key] = val
			return
		}
	}

	// --- Append path: key == arrLen (next sequential slot) ---
	if key >= 0 && key == arrLen {
		switch t.arrayKind {
		case ArrayInt:
			vk := classifyValueForArray(val)
			if vk == ArrayInt {
				t.intArray = append(t.intArray, valueToInt64(val))
				t.absorbKeys()
				return
			}
			t.demoteToMixed()
			arenaAppendValue(DefaultHeap, &t.array, val)
			t.absorbKeys()
			return
		case ArrayFloat:
			if val.Type() == TypeFloat {
				t.floatArray = append(t.floatArray, val.Float())
				t.absorbKeys()
				return
			}
			t.demoteToMixed()
			arenaAppendValue(DefaultHeap, &t.array, val)
			t.absorbKeys()
			return
		case ArrayBool:
			if val.Type() == TypeBool {
				var b byte = 1 // false
				if val.Bool() {
					b = 2 // true
				}
				t.boolArray = append(t.boolArray, b)
				t.absorbKeys()
				return
			}
			t.demoteToMixed()
			arenaAppendValue(DefaultHeap, &t.array, val)
			t.absorbKeys()
			return
		default:
			// ArrayMixed: first non-nil write to an empty array → try to specialize
			if len(t.array) == 1 {
				// array has only the [0] slot → first append (key=1)
				// array[0] may have been written by 0-indexed code; preserve it.
				vk := classifyValueForArray(val)
				a0 := t.array[0]
				a0Compatible := a0.IsNil() || classifyValueForArray(a0) == vk
				if a0Compatible {
					switch vk {
					case ArrayInt:
						t.arrayKind = ArrayInt
						t.intArray = make([]int64, 1, 8)
						if !a0.IsNil() {
							t.intArray[0] = valueToInt64(a0)
						}
						t.intArray = append(t.intArray, valueToInt64(val))
						t.array = nil
						t.absorbKeys()
						return
					case ArrayFloat:
						t.arrayKind = ArrayFloat
						t.floatArray = make([]float64, 1, 8)
						if !a0.IsNil() {
							t.floatArray[0] = a0.Float()
						}
						t.floatArray = append(t.floatArray, val.Float())
						t.array = nil
						t.absorbKeys()
						return
					case ArrayBool:
						t.arrayKind = ArrayBool
						t.boolArray = make([]byte, 1, 8) // 0 = nil sentinel
						if !a0.IsNil() {
							if a0.Bool() {
								t.boolArray[0] = 2
							} else {
								t.boolArray[0] = 1
							}
						}
						var b byte = 1 // false
						if val.Bool() {
							b = 2 // true
						}
						t.boolArray = append(t.boolArray, b)
						t.array = nil
						t.absorbKeys()
						return
					}
				}
			}
			arenaAppendValue(DefaultHeap, &t.array, val)
			t.absorbKeys()
			return
		}
	}

	// --- Sparse expansion path ---
	if key > arrLen && key < sparseArrayMax && !val.IsNil() {
		needed := int(key) + 1
		switch t.arrayKind {
		case ArrayInt:
			vk := classifyValueForArray(val)
			if vk == ArrayInt {
				if needed > cap(t.intArray) {
					newSlice := make([]int64, len(t.intArray), needed)
					copy(newSlice, t.intArray)
					t.intArray = newSlice
				}
				t.intArray = t.intArray[:needed]
				t.intArray[key] = valueToInt64(val)
				t.absorbKeys()
				return
			}
			t.demoteToMixed()
			// Fall through to mixed sparse expansion
		case ArrayFloat:
			if val.Type() == TypeFloat {
				if needed > cap(t.floatArray) {
					newSlice := make([]float64, len(t.floatArray), needed)
					copy(newSlice, t.floatArray)
					t.floatArray = newSlice
				}
				t.floatArray = t.floatArray[:needed]
				t.floatArray[key] = val.Float()
				t.absorbKeys()
				return
			}
			t.demoteToMixed()
			// Fall through to mixed sparse expansion
		case ArrayBool:
			if val.Type() == TypeBool {
				if needed > cap(t.boolArray) {
					newSlice := make([]byte, len(t.boolArray), needed)
					copy(newSlice, t.boolArray)
					t.boolArray = newSlice
				}
				t.boolArray = t.boolArray[:needed]
				if val.Bool() {
					t.boolArray[key] = 2 // true
				} else {
					t.boolArray[key] = 1 // false
				}
				t.absorbKeys()
				return
			}
			t.demoteToMixed()
			// Fall through to mixed sparse expansion
		case ArrayMixed:
			// First write with sparse key on empty/sentinel array → try to specialize
			if len(t.array) <= 1 {
				vk := classifyValueForArray(val)
				// Check if array[0] is compatible with the target type
				var a0 Value
				if len(t.array) == 1 {
					a0 = t.array[0]
				}
				a0Compatible := a0.IsNil() || classifyValueForArray(a0) == vk
				if a0Compatible {
					switch vk {
					case ArrayInt:
						t.arrayKind = ArrayInt
						t.intArray = make([]int64, needed)
						if !a0.IsNil() {
							t.intArray[0] = valueToInt64(a0)
						}
						t.intArray[key] = valueToInt64(val)
						t.array = nil
						t.absorbKeys()
						return
					case ArrayFloat:
						t.arrayKind = ArrayFloat
						t.floatArray = make([]float64, needed)
						if !a0.IsNil() {
							t.floatArray[0] = a0.Float()
						}
						t.floatArray[key] = val.Float()
						t.array = nil
						t.absorbKeys()
						return
					case ArrayBool:
						t.arrayKind = ArrayBool
						t.boolArray = make([]byte, needed) // zeros = nil sentinel
						if !a0.IsNil() {
							if a0.Bool() {
								t.boolArray[0] = 2
							} else {
								t.boolArray[0] = 1
							}
						}
						if val.Bool() {
							t.boolArray[key] = 2 // true
						} else {
							t.boolArray[key] = 1 // false
						}
						t.array = nil
						t.absorbKeys()
						return
					}
				}
			}
		}
		// Mixed sparse expansion
		if needed > cap(t.array) {
			newArr := DefaultHeap.AllocValues(needed, needed)
			copy(newArr, t.array)
			t.array = newArr
		} else {
			oldLen := len(t.array)
			t.array = t.array[:needed]
			// Fill newly exposed slots with nil (Go zero = float64(0.0), not nil)
			nv := NilValue()
			for i := oldLen; i < needed; i++ {
				t.array[i] = nv
			}
		}
		t.array[key] = val
		t.absorbKeys()
		return
	}

	// --- imap path (key out of array range or negative) ---
	if val.IsNil() {
		// For nil values on expanded typed array slots, demote and set
		if key >= 0 && key < arrLen {
			if t.arrayKind != ArrayMixed {
				t.demoteToMixed()
			}
			t.array[key] = val
			return
		}
		if t.imap != nil {
			delete(t.imap, key)
		}
	} else {
		if t.imap == nil {
			t.imap = make(map[int64]Value)
		}
		t.imap[key] = val
	}
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
				last := len(t.skeys) - 1
				t.skeys[i] = t.skeys[last]
				t.svals[i] = t.svals[last]
				t.skeys = t.skeys[:last]
				t.svals = t.svals[:last]
				t.shapeID = GetShapeID(t.skeys)
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
			t.skeys = append(t.skeys, key)
			arenaAppendValue(DefaultHeap, &t.svals, val)
			t.shapeID = GetShapeID(t.skeys)
		} else {
			t.smap = make(map[string]Value, len(t.skeys)+1)
			for i, k := range t.skeys {
				t.smap[k] = t.svals[i]
			}
			t.smap[key] = val
			t.skeys = nil
			t.svals = nil
			t.shapeID = 0
		}
	}
}

// absorbKeys moves consecutive integer keys from imap/hash into the array part.
// Must be called with lock held (if mu != nil).
func (t *Table) absorbKeys() {
	for {
		nextIdx := int64(t.typedArrayLen())
		absorbed := false
		if t.imap != nil {
			if val, ok := t.imap[nextIdx]; ok && !val.IsNil() {
				t.appendToTypedArray(val)
				delete(t.imap, nextIdx)
				absorbed = true
			}
		}
		if !absorbed && t.hash != nil {
			key := IntValue(nextIdx)
			val, ok := t.hash[key]
			if ok && !val.IsNil() {
				t.appendToTypedArray(val)
				delete(t.hash, key)
				absorbed = true
			}
		}
		if !absorbed {
			break
		}
	}
}

// appendToTypedArray appends a value to the active typed array, demoting if needed.
func (t *Table) appendToTypedArray(val Value) {
	switch t.arrayKind {
	case ArrayInt:
		vk := classifyValueForArray(val)
		if vk == ArrayInt {
			t.intArray = append(t.intArray, valueToInt64(val))
		} else {
			t.demoteToMixed()
			arenaAppendValue(DefaultHeap, &t.array, val)
		}
	case ArrayFloat:
		if val.Type() == TypeFloat {
			t.floatArray = append(t.floatArray, val.Float())
		} else {
			t.demoteToMixed()
			arenaAppendValue(DefaultHeap, &t.array, val)
		}
	case ArrayBool:
		if val.Type() == TypeBool {
			var b byte = 1 // false
			if val.Bool() {
				b = 2 // true
			}
			t.boolArray = append(t.boolArray, b)
		} else {
			t.demoteToMixed()
			arenaAppendValue(DefaultHeap, &t.array, val)
		}
	default:
		arenaAppendValue(DefaultHeap, &t.array, val)
	}
}

// Length returns the length of the array part (the # operator).
func (t *Table) Length() int {
	switch t.arrayKind {
	case ArrayInt:
		// All slots are valid (no nil concept for int64), length is always full.
		return len(t.intArray) - 1
	case ArrayFloat:
		// All slots are valid for float64 as well.
		return len(t.floatArray) - 1
	case ArrayBool:
		// Scan backwards past nil sentinels (0 = unset)
		n := len(t.boolArray) - 1
		for n > 0 && t.boolArray[n] == 0 {
			n--
		}
		return n
	default:
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
