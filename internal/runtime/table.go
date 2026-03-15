package runtime

import (
	"math"
	"sync"
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
	array     []Value          // 1-indexed: array[0] is unused padding
	imap      map[int64]Value  // integer keys not in array range
	// String keys: small tables use flat slices, large tables use map
	skeys     []string         // parallel with svals for small tables
	svals     []Value          // parallel with skeys for small tables
	smap      map[string]Value // only for tables with >smallFieldCap string keys
	hash      map[Value]Value  // everything else (bool, float, table, function keys)
	metatable *Table
	keys      []Value // ordered keys for Next() iteration
	keysDirty bool
}

// SetConcurrent enables or disables mutex protection for concurrent access.
func (t *Table) SetConcurrent(on bool) {
	if on && t.mu == nil {
		t.mu = &sync.RWMutex{}
	}
}

// cleanHashKey normalizes a Value for use as a Go map key.
// Clears stale fields left by SetInt and similar partial-update methods.
func cleanHashKey(key Value) Value {
	switch key.typ {
	case TypeInt:
		return Value{typ: TypeInt, data: key.data} // clear stale ptr
	case TypeFloat:
		return Value{typ: TypeFloat, data: key.data} // clear stale ptr
	case TypeString:
		return Value{typ: TypeString, ptr: key.ptr} // clear stale data
	default:
		return key
	}
}

// NewTable creates a new empty table (non-concurrent by default).
func NewTable() *Table {
	return &Table{
		array:     []Value{NilValue()},
		keysDirty: true,
	}
}

// NewTableSized creates a table with pre-allocated capacity hints.
func NewTableSized(arrayHint, hashHint int) *Table {
	t := &Table{keysDirty: true}
	if arrayHint > 0 {
		t.array = make([]Value, 1, arrayHint+1)
		t.array[0] = NilValue()
	} else {
		t.array = []Value{NilValue()}
	}
	if hashHint > 0 && hashHint <= smallFieldCap {
		t.skeys = make([]string, 0, hashHint)
		t.svals = make([]Value, 0, hashHint)
	}
	return t
}

// RawGet retrieves a value by key, bypassing metamethods.
func (t *Table) RawGet(key Value) Value {
	if key.IsNil() {
		return NilValue()
	}
	if key.typ == TypeInt {
		return t.RawGetInt(int64(key.data))
	}
	if key.typ == TypeString {
		return t.RawGetString(key.ptr.(string))
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
	if key >= 1 && key < int64(len(t.array)) {
		return t.array[key]
	}
	if t.imap != nil {
		if v, ok := t.imap[key]; ok {
			return v
		}
	}
	return NilValue()
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

// RawSet assigns a value by key, bypassing metamethods.
func (t *Table) RawSet(key, val Value) {
	if key.IsNil() {
		return
	}
	if key.typ == TypeFloat && floatIsInt(math.Float64frombits(key.data)) {
		key = IntValue(int64(math.Float64frombits(key.data)))
	}
	if key.typ == TypeInt {
		t.RawSetInt(int64(key.data), val)
		return
	}
	if key.typ == TypeString {
		t.RawSetString(key.ptr.(string), val)
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

// RawSetInt assigns a value by integer key (fast path).
func (t *Table) RawSetInt(key int64, val Value) {
	if t.mu != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
	}
	t.keysDirty = true
	if key >= 1 && key <= int64(len(t.array)) {
		if key == int64(len(t.array)) {
			t.array = append(t.array, val)
			t.absorbKeys()
			return
		}
		t.array[key] = val
		return
	}
	if val.IsNil() {
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
			t.svals = append(t.svals, val)
		} else {
			t.smap = make(map[string]Value, len(t.skeys)+1)
			for i, k := range t.skeys {
				t.smap[k] = t.svals[i]
			}
			t.smap[key] = val
			t.skeys = nil
			t.svals = nil
		}
	}
}

// absorbKeys moves consecutive integer keys from imap/hash into the array part.
// Must be called with lock held (if mu != nil).
func (t *Table) absorbKeys() {
	for {
		nextIdx := int64(len(t.array))
		if t.imap != nil {
			if val, ok := t.imap[nextIdx]; ok && !val.IsNil() {
				t.array = append(t.array, val)
				delete(t.imap, nextIdx)
				continue
			}
		}
		if t.hash != nil {
			key := IntValue(nextIdx)
			val, ok := t.hash[key]
			if ok && !val.IsNil() {
				t.array = append(t.array, val)
				delete(t.hash, key)
				continue
			}
		}
		break
	}
}

// Length returns the length of the array part (the # operator).
func (t *Table) Length() int {
	n := len(t.array) - 1
	for n > 0 && t.array[n].IsNil() {
		n--
	}
	return n
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
	for i := 1; i < len(t.array); i++ {
		if !t.array[i].IsNil() {
			t.keys = append(t.keys, IntValue(int64(i)))
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
