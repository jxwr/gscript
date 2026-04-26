// table_int.go contains integer-key and typed-array operations for Table.
// Extracted from table.go to keep both files under the 1000-line limit.

package runtime

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
			if len(t.array) <= 1 {
				// array is empty or has only the [0] slot. Preserve an existing
				// 0-indexed value, otherwise treat the missing sentinel as nil.
				vk := classifyValueForArray(val)
				if key == 0 && len(t.array) == 0 {
					switch vk {
					case ArrayInt:
						t.arrayKind = ArrayInt
						t.intArray = make([]int64, 1, 8)
						t.intArray[0] = valueToInt64(val)
						t.absorbKeys()
						return
					case ArrayFloat:
						t.arrayKind = ArrayFloat
						t.floatArray = make([]float64, 1, 8)
						t.floatArray[0] = val.Float()
						t.absorbKeys()
						return
					case ArrayBool:
						t.arrayKind = ArrayBool
						t.boolArray = make([]byte, 1, 8)
						if val.Bool() {
							t.boolArray[0] = 2
						} else {
							t.boolArray[0] = 1
						}
						t.absorbKeys()
						return
					}
				}
				a0 := NilValue()
				if len(t.array) == 1 {
					a0 = t.array[0]
				}
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
				a0 := NilValue()
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
