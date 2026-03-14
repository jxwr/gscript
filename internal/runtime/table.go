package runtime

import "sync"

// Table is GScript's associative array / object type.
// It has an optimized array part for sequential integer keys 1..n,
// and a hash part for everything else.
type Table struct {
	mu        sync.RWMutex
	hash      map[Value]Value
	array     []Value // 1-indexed: array[0] is unused padding, real data at [1]..[len-1]
	metatable *Table
	keys      []Value // ordered keys for Next() iteration
	keysDirty bool
}

// cleanHashKey normalizes a Value for use as a Go map key.
// This ensures that two logically equal values (e.g., two IntValues with ival=0)
// map to the same key even if they have stale leftover fields from
// partial-update optimizations like SetInt.
func cleanHashKey(key Value) Value {
	switch key.typ {
	case TypeInt:
		return Value{typ: TypeInt, ival: key.ival}
	case TypeFloat:
		return Value{typ: TypeFloat, fval: key.fval}
	case TypeString:
		return Value{typ: TypeString, sval: key.sval}
	default:
		// nil, bool, table, function, coroutine, channel — use as-is
		return key
	}
}

// NewTable creates a new empty table.
func NewTable() *Table {
	return &Table{
		hash:      make(map[Value]Value),
		array:     []Value{NilValue()}, // index 0 is unused
		keysDirty: true,
	}
}

// RawGet retrieves a value by key, bypassing metamethods.
func (t *Table) RawGet(key Value) Value {
	if key.IsNil() {
		return NilValue()
	}
	t.mu.RLock()
	// Try array part for integer keys
	if key.IsInt() {
		idx := key.Int()
		if idx >= 1 && idx < int64(len(t.array)) {
			v := t.array[idx]
			t.mu.RUnlock()
			return v
		}
	}
	// Fall through to hash part
	val, ok := t.hash[cleanHashKey(key)]
	t.mu.RUnlock()
	if !ok {
		return NilValue()
	}
	return val
}

// RawSet assigns a value by key, bypassing metamethods.
// Setting to nil removes the entry.
func (t *Table) RawSet(key, val Value) {
	if key.IsNil() {
		return // ignore nil keys (Lua semantics)
	}
	// Float key that is an exact integer should be treated as integer key
	if key.IsFloat() && floatIsInt(key.Float()) {
		key = IntValue(int64(key.Float()))
	}

	t.mu.Lock()
	t.keysDirty = true

	// Try array part for integer keys
	if key.IsInt() {
		idx := key.Int()
		if idx >= 1 && idx <= int64(len(t.array)) {
			// idx within existing array or one past the end
			if idx == int64(len(t.array)) {
				// Extend the array
				t.array = append(t.array, val)
				// Absorb any hash keys that now fit contiguously
				t.absorbHashKeys()
				t.mu.Unlock()
				return
			}
			t.array[idx] = val
			t.mu.Unlock()
			return
		}
	}
	// Hash part
	ck := cleanHashKey(key)
	if val.IsNil() {
		delete(t.hash, ck)
	} else {
		t.hash[ck] = val
	}
	t.mu.Unlock()
}

// absorbHashKeys moves consecutive integer keys from hash into array.
func (t *Table) absorbHashKeys() {
	for {
		nextIdx := int64(len(t.array))
		key := IntValue(nextIdx)
		val, ok := t.hash[key]
		if !ok || val.IsNil() {
			break
		}
		t.array = append(t.array, val)
		delete(t.hash, key)
	}
}

// Length returns the length of the array part (the # operator).
// This is the number of consecutive non-nil entries starting at index 1.
func (t *Table) Length() int {
	// The array part goes from index 1 to len(t.array)-1
	n := len(t.array) - 1 // subtract the unused index 0
	// Walk backward to find the border (first non-nil from the end)
	for n > 0 && t.array[n].IsNil() {
		n--
	}
	return n
}

// Len returns the length of the array part (alias for Length, used by VM).
func (t *Table) Len() int {
	return t.Length()
}

// Append adds a value to the end of the array part (equivalent to table.insert(t, v)).
func (t *Table) Append(v Value) {
	n := t.Length()
	t.RawSet(IntValue(int64(n+1)), v)
}

// rebuildKeys rebuilds the ordered key list for iteration.
func (t *Table) rebuildKeys() {
	t.keys = t.keys[:0]
	// Array part first (1..n)
	for i := 1; i < len(t.array); i++ {
		if !t.array[i].IsNil() {
			t.keys = append(t.keys, IntValue(int64(i)))
		}
	}
	// Hash part
	for k, v := range t.hash {
		if !v.IsNil() {
			t.keys = append(t.keys, k)
		}
	}
	t.keysDirty = false
}

// Next returns the next key/value pair after the given key.
// If key is nil, returns the first entry.
// Returns (key, value, true) or (nil, nil, false) when iteration is done.
func (t *Table) Next(key Value) (Value, Value, bool) {
	if t.keysDirty {
		t.rebuildKeys()
	}
	if len(t.keys) == 0 {
		return NilValue(), NilValue(), false
	}
	if key.IsNil() {
		// Start of iteration
		k := t.keys[0]
		return k, t.RawGet(k), true
	}
	// Find the current key and return the next one
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
