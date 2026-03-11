package runtime

import (
	"fmt"
	"sort"
	"strings"
)

// buildTableLib creates the "table" standard library table.
func buildTableLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "table." + name,
			Fn:   fn,
		}))
	}

	// table.insert(t, [pos,] value)
	set("insert", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.insert' (table expected)")
		}
		tbl := args[0].Table()
		length := int64(tbl.Length())

		if len(args) == 2 {
			// Append at end
			tbl.RawSet(IntValue(length+1), args[1])
			return nil, nil
		}
		// Insert at position
		pos := toInt(args[1])
		value := args[2]
		// Shift elements right
		for i := length; i >= pos; i-- {
			tbl.RawSet(IntValue(i+1), tbl.RawGet(IntValue(i)))
		}
		tbl.RawSet(IntValue(pos), value)
		return nil, nil
	})

	// table.remove(t [, pos]) -> removed value
	set("remove", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.remove' (table expected)")
		}
		tbl := args[0].Table()
		length := int64(tbl.Length())
		pos := length // default: remove last element
		if len(args) >= 2 {
			pos = toInt(args[1])
		}

		removed := tbl.RawGet(IntValue(pos))

		// Shift elements left
		for i := pos; i < length; i++ {
			tbl.RawSet(IntValue(i), tbl.RawGet(IntValue(i+1)))
		}
		tbl.RawSet(IntValue(length), NilValue())

		return []Value{removed}, nil
	})

	// table.concat(t [, sep [, i [, j]]]) -> string
	set("concat", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.concat' (table expected)")
		}
		tbl := args[0].Table()
		sep := ""
		if len(args) >= 2 && args[1].IsString() {
			sep = args[1].Str()
		}
		i := int64(1)
		j := int64(tbl.Length())
		if len(args) >= 3 {
			i = toInt(args[2])
		}
		if len(args) >= 4 {
			j = toInt(args[3])
		}

		parts := make([]string, 0, j-i+1)
		for k := i; k <= j; k++ {
			v := tbl.RawGet(IntValue(k))
			parts = append(parts, v.String())
		}
		return []Value{StringValue(strings.Join(parts, sep))}, nil
	})

	// table.sort(t [, comp])
	set("sort", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.sort' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()

		// Extract array elements
		elems := make([]Value, length)
		for i := 0; i < length; i++ {
			elems[i] = tbl.RawGet(IntValue(int64(i + 1)))
		}

		var sortErr error
		if len(args) >= 2 && args[1].IsFunction() {
			comp := args[1]
			sort.SliceStable(elems, func(a, b int) bool {
				if sortErr != nil {
					return false
				}
				// The comparator is stored but we need the interpreter to call it.
				// We use GoFunction's Fn directly if possible.
				var results []Value
				if gf := comp.GoFunction(); gf != nil {
					var err error
					results, err = gf.Fn([]Value{elems[a], elems[b]})
					if err != nil {
						sortErr = err
						return false
					}
				} else {
					// For closure-based comparators, we can't call them here
					// without access to the interpreter. Return a default ordering.
					// This is a limitation; table.sort with closure comparators
					// will be handled via the interpreter's callFunction.
					sortErr = fmt.Errorf("table.sort comparator must be a Go function or use default ordering")
					return false
				}
				if len(results) > 0 {
					return results[0].Truthy()
				}
				return false
			})
		} else {
			// Default sort: numbers before strings, then by value
			sort.SliceStable(elems, func(a, b int) bool {
				va, vb := elems[a], elems[b]
				less, ok := va.lessThan(vb)
				if ok {
					return less
				}
				return false
			})
		}

		if sortErr != nil {
			return nil, sortErr
		}

		// Write back
		for i, v := range elems {
			tbl.RawSet(IntValue(int64(i+1)), v)
		}
		return nil, nil
	})

	// table.unpack(t [, i [, j]]) -> values
	set("unpack", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.unpack' (table expected)")
		}
		tbl := args[0].Table()
		i := int64(1)
		j := int64(tbl.Length())
		if len(args) >= 2 {
			i = toInt(args[1])
		}
		if len(args) >= 3 {
			j = toInt(args[2])
		}
		var result []Value
		for k := i; k <= j; k++ {
			result = append(result, tbl.RawGet(IntValue(k)))
		}
		return result, nil
	})

	// table.move(a1, f, e, t [, a2]) -> a2
	set("move", func(args []Value) ([]Value, error) {
		if len(args) < 4 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument to 'table.move'")
		}
		a1 := args[0].Table()
		f := toInt(args[1])
		e := toInt(args[2])
		tPos := toInt(args[3])
		a2 := a1
		if len(args) >= 5 && args[4].IsTable() {
			a2 = args[4].Table()
		}

		if e >= f {
			count := e - f + 1
			// Copy in appropriate direction to avoid overwrites
			if tPos <= f || a1 != a2 {
				for i := int64(0); i < count; i++ {
					a2.RawSet(IntValue(tPos+i), a1.RawGet(IntValue(f+i)))
				}
			} else {
				for i := count - 1; i >= 0; i-- {
					a2.RawSet(IntValue(tPos+i), a1.RawGet(IntValue(f+i)))
				}
			}
		}

		return []Value{TableValue(a2)}, nil
	})

	// table.pack(...) -> table
	set("pack", func(args []Value) ([]Value, error) {
		tbl := NewTable()
		for i, v := range args {
			tbl.RawSet(IntValue(int64(i+1)), v)
		}
		tbl.RawSet(StringValue("n"), IntValue(int64(len(args))))
		return []Value{TableValue(tbl)}, nil
	})

	// table.keys(t) -- return array table of all keys (any type)
	set("keys", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.keys' (table expected)")
		}
		tbl := args[0].Table()
		result := NewTable()
		idx := int64(1)
		k, _, ok := tbl.Next(NilValue())
		for ok {
			result.RawSet(IntValue(idx), k)
			idx++
			k, _, ok = tbl.Next(k)
		}
		return []Value{TableValue(result)}, nil
	})

	// table.values(t) -- return array table of all values
	set("values", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.values' (table expected)")
		}
		tbl := args[0].Table()
		result := NewTable()
		idx := int64(1)
		k, v, ok := tbl.Next(NilValue())
		for ok {
			result.RawSet(IntValue(idx), v)
			idx++
			k, v, ok = tbl.Next(k)
		}
		return []Value{TableValue(result)}, nil
	})

	// table.contains(t, v) -- bool: linear search for value v
	set("contains", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.contains' (table expected)")
		}
		tbl := args[0].Table()
		target := args[1]
		k, v, ok := tbl.Next(NilValue())
		for ok {
			if v.Equal(target) {
				return []Value{BoolValue(true)}, nil
			}
			k, v, ok = tbl.Next(k)
		}
		return []Value{BoolValue(false)}, nil
	})

	// table.indexOf(t, v) -- int key of first occurrence of v, or nil
	set("indexOf", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.indexOf' (table expected)")
		}
		tbl := args[0].Table()
		target := args[1]
		length := tbl.Length()
		for i := int64(1); i <= int64(length); i++ {
			if tbl.RawGet(IntValue(i)).Equal(target) {
				return []Value{IntValue(i)}, nil
			}
		}
		return []Value{NilValue()}, nil
	})

	// table.copy(t) -- shallow copy (new table with same key-value pairs)
	set("copy", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.copy' (table expected)")
		}
		src := args[0].Table()
		dst := NewTable()
		k, v, ok := src.Next(NilValue())
		for ok {
			dst.RawSet(k, v)
			k, v, ok = src.Next(k)
		}
		return []Value{TableValue(dst)}, nil
	})

	// table.merge(t1, t2) -- copy all entries from t2 into t1 (in-place), return t1
	set("merge", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsTable() {
			return nil, fmt.Errorf("bad argument to 'table.merge' (table expected)")
		}
		t1 := args[0].Table()
		t2 := args[1].Table()
		k, v, ok := t2.Next(NilValue())
		for ok {
			t1.RawSet(k, v)
			k, v, ok = t2.Next(k)
		}
		return []Value{TableValue(t1)}, nil
	})

	// table.count(t) -- count ALL entries including non-integer keys (unlike #t)
	set("count", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.count' (table expected)")
		}
		tbl := args[0].Table()
		count := int64(0)
		k, _, ok := tbl.Next(NilValue())
		for ok {
			count++
			k, _, ok = tbl.Next(k)
		}
		return []Value{IntValue(count)}, nil
	})

	// table.toArray(t) -- convert hash-table to array by taking values in pairs order
	set("toArray", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.toArray' (table expected)")
		}
		src := args[0].Table()
		result := NewTable()
		idx := int64(1)
		k, v, ok := src.Next(NilValue())
		for ok {
			result.RawSet(IntValue(idx), v)
			idx++
			k, v, ok = src.Next(k)
		}
		return []Value{TableValue(result)}, nil
	})

	// table.fromArray(arr, keyFn) -- convert array to table using keyFn(v) as key
	// Note: keyFn must be a GoFunction (no interp needed)
	set("fromArray", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
			return nil, fmt.Errorf("bad argument to 'table.fromArray'")
		}
		arr := args[0].Table()
		keyFn := args[1]
		gf := keyFn.GoFunction()
		if gf == nil {
			return nil, fmt.Errorf("table.fromArray: keyFn must be a Go function (use table library with interp for closures)")
		}
		result := NewTable()
		length := arr.Length()
		for i := int64(1); i <= int64(length); i++ {
			v := arr.RawGet(IntValue(i))
			keys, err := gf.Fn([]Value{v})
			if err != nil {
				return nil, err
			}
			if len(keys) > 0 {
				result.RawSet(keys[0], v)
			}
		}
		return []Value{TableValue(result)}, nil
	})

	// table.unique(t) -- remove duplicate values from array, return new array
	set("unique", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.unique' (table expected)")
		}
		tbl := args[0].Table()
		result := NewTable()
		seen := make(map[Value]bool)
		length := tbl.Length()
		idx := int64(1)
		for i := int64(1); i <= int64(length); i++ {
			v := tbl.RawGet(IntValue(i))
			if !seen[v] {
				seen[v] = true
				result.RawSet(IntValue(idx), v)
				idx++
			}
		}
		return []Value{TableValue(result)}, nil
	})

	// table.flatten(t [, depth]) -- flatten nested arrays to depth levels (default: all)
	set("flatten", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.flatten' (table expected)")
		}
		maxDepth := -1 // -1 means unlimited
		if len(args) >= 2 {
			maxDepth = int(toInt(args[1]))
		}
		result := NewTable()
		idx := int64(1)
		var flattenHelper func(tbl *Table, depth int)
		flattenHelper = func(tbl *Table, depth int) {
			length := tbl.Length()
			for i := int64(1); i <= int64(length); i++ {
				v := tbl.RawGet(IntValue(i))
				if v.IsTable() && (maxDepth < 0 || depth < maxDepth) {
					flattenHelper(v.Table(), depth+1)
				} else {
					result.RawSet(IntValue(idx), v)
					idx++
				}
			}
		}
		flattenHelper(args[0].Table(), 0)
		return []Value{TableValue(result)}, nil
	})

	// table.zip(t1, t2) -- zip two arrays: {{t1[1],t2[1]}, {t1[2],t2[2]}, ...}
	set("zip", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsTable() {
			return nil, fmt.Errorf("bad argument to 'table.zip' (table expected)")
		}
		t1 := args[0].Table()
		t2 := args[1].Table()
		len1 := t1.Length()
		len2 := t2.Length()
		minLen := len1
		if len2 < minLen {
			minLen = len2
		}
		result := NewTable()
		for i := int64(1); i <= int64(minLen); i++ {
			pair := NewTable()
			pair.RawSet(IntValue(1), t1.RawGet(IntValue(i)))
			pair.RawSet(IntValue(2), t2.RawGet(IntValue(i)))
			result.RawSet(IntValue(i), TableValue(pair))
		}
		return []Value{TableValue(result)}, nil
	})

	// table.reverse(t) -- reverse array in place, return t
	set("reverse", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'table.reverse' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()
		for i, j := int64(1), int64(length); i < j; i, j = i+1, j-1 {
			vi := tbl.RawGet(IntValue(i))
			vj := tbl.RawGet(IntValue(j))
			tbl.RawSet(IntValue(i), vj)
			tbl.RawSet(IntValue(j), vi)
		}
		return []Value{TableValue(tbl)}, nil
	})

	// table.slice(t, from [, to]) -- return new array (1-based, from..to inclusive)
	set("slice", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument to 'table.slice'")
		}
		tbl := args[0].Table()
		from := toInt(args[1])
		to := int64(tbl.Length())
		if len(args) >= 3 {
			to = toInt(args[2])
		}
		result := NewTable()
		idx := int64(1)
		for i := from; i <= to; i++ {
			result.RawSet(IntValue(idx), tbl.RawGet(IntValue(i)))
			idx++
		}
		return []Value{TableValue(result)}, nil
	})

	return t
}

// buildTableSortWithInterp creates a table.sort that can call closure comparators.
// This is registered separately because it needs access to the interpreter.
func buildTableSortWithInterp(interp *Interpreter, tblLib *Table) {
	tblLib.RawSet(StringValue("sort"), FunctionValue(&GoFunction{
		Name: "table.sort",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 1 || !args[0].IsTable() {
				return nil, fmt.Errorf("bad argument #1 to 'table.sort' (table expected)")
			}
			tbl := args[0].Table()
			length := tbl.Length()

			elems := make([]Value, length)
			for i := 0; i < length; i++ {
				elems[i] = tbl.RawGet(IntValue(int64(i + 1)))
			}

			var sortErr error
			if len(args) >= 2 && args[1].IsFunction() {
				comp := args[1]
				sort.SliceStable(elems, func(a, b int) bool {
					if sortErr != nil {
						return false
					}
					results, err := interp.callFunction(comp, []Value{elems[a], elems[b]})
					if err != nil {
						sortErr = err
						return false
					}
					if len(results) > 0 {
						return results[0].Truthy()
					}
					return false
				})
			} else {
				sort.SliceStable(elems, func(a, b int) bool {
					va, vb := elems[a], elems[b]
					less, ok := va.lessThan(vb)
					if ok {
						return less
					}
					return false
				})
			}

			if sortErr != nil {
				return nil, sortErr
			}

			for i, v := range elems {
				tbl.RawSet(IntValue(int64(i+1)), v)
			}
			return nil, nil
		},
	}))
}

// buildTableHigherOrderWithInterp adds filter, map, reduce to the table library.
// These need the interpreter to call GScript closures.
func buildTableHigherOrderWithInterp(interp *Interpreter, tblLib *Table) {
	// table.filter(t, f) -- return new array of values where f(v, k) is truthy
	tblLib.RawSet(StringValue("filter"), FunctionValue(&GoFunction{
		Name: "table.filter",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
				return nil, fmt.Errorf("bad argument to 'table.filter'")
			}
			tbl := args[0].Table()
			fn := args[1]
			result := NewTable()
			idx := int64(1)
			length := tbl.Length()
			for i := int64(1); i <= int64(length); i++ {
				v := tbl.RawGet(IntValue(i))
				results, err := interp.callFunction(fn, []Value{v, IntValue(i)})
				if err != nil {
					return nil, err
				}
				if len(results) > 0 && results[0].Truthy() {
					result.RawSet(IntValue(idx), v)
					idx++
				}
			}
			return []Value{TableValue(result)}, nil
		},
	}))

	// table.map(t, f) -- return new array/table with f(v, k) applied to each value
	tblLib.RawSet(StringValue("map"), FunctionValue(&GoFunction{
		Name: "table.map",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
				return nil, fmt.Errorf("bad argument to 'table.map'")
			}
			tbl := args[0].Table()
			fn := args[1]
			result := NewTable()
			length := tbl.Length()
			for i := int64(1); i <= int64(length); i++ {
				v := tbl.RawGet(IntValue(i))
				results, err := interp.callFunction(fn, []Value{v, IntValue(i)})
				if err != nil {
					return nil, err
				}
				if len(results) > 0 {
					result.RawSet(IntValue(i), results[0])
				} else {
					result.RawSet(IntValue(i), NilValue())
				}
			}
			return []Value{TableValue(result)}, nil
		},
	}))

	// table.reduce(t, f, init) -- fold: acc = f(acc, v) for each value, return acc
	tblLib.RawSet(StringValue("reduce"), FunctionValue(&GoFunction{
		Name: "table.reduce",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 3 || !args[0].IsTable() || !args[1].IsFunction() {
				return nil, fmt.Errorf("bad argument to 'table.reduce'")
			}
			tbl := args[0].Table()
			fn := args[1]
			acc := args[2]
			length := tbl.Length()
			for i := int64(1); i <= int64(length); i++ {
				v := tbl.RawGet(IntValue(i))
				results, err := interp.callFunction(fn, []Value{acc, v})
				if err != nil {
					return nil, err
				}
				if len(results) > 0 {
					acc = results[0]
				}
			}
			return []Value{acc}, nil
		},
	}))

	// Also add fromArray with interp support for closures
	tblLib.RawSet(StringValue("fromArray"), FunctionValue(&GoFunction{
		Name: "table.fromArray",
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
				return nil, fmt.Errorf("bad argument to 'table.fromArray'")
			}
			arr := args[0].Table()
			keyFn := args[1]
			result := NewTable()
			length := arr.Length()
			for i := int64(1); i <= int64(length); i++ {
				v := arr.RawGet(IntValue(i))
				keys, err := interp.callFunction(keyFn, []Value{v})
				if err != nil {
					return nil, err
				}
				if len(keys) > 0 {
					result.RawSet(keys[0], v)
				}
			}
			return []Value{TableValue(result)}, nil
		},
	}))
}
