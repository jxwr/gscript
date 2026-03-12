package runtime

import (
	"fmt"
	"sort"
)

// buildSortLib creates the "sort" standard library table.
// Provides sorting utilities, binary search, partitioning, and order checks.
// Inspired by Odin's sort package and Go's slices package.
func buildSortLib(interp *Interpreter) *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "sort." + name,
			Fn:   fn,
		}))
	}

	// Helper to extract array elements from a table
	extractArray := func(tbl *Table) []Value {
		length := tbl.Length()
		elems := make([]Value, length)
		for i := 0; i < length; i++ {
			elems[i] = tbl.RawGet(IntValue(int64(i + 1)))
		}
		return elems
	}

	// Helper to write elements back to a table
	writeBack := func(tbl *Table, elems []Value) {
		for i, v := range elems {
			tbl.RawSet(IntValue(int64(i+1)), v)
		}
	}

	// sort.asc(table) - sort array in ascending order (in place), returns the table
	set("asc", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.asc' (table expected)")
		}
		tbl := args[0].Table()
		elems := extractArray(tbl)
		sort.SliceStable(elems, func(a, b int) bool {
			less, ok := elems[a].LessThan(elems[b])
			return ok && less
		})
		writeBack(tbl, elems)
		return []Value{args[0]}, nil
	})

	// sort.desc(table) - sort array in descending order (in place), returns the table
	set("desc", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.desc' (table expected)")
		}
		tbl := args[0].Table()
		elems := extractArray(tbl)
		sort.SliceStable(elems, func(a, b int) bool {
			less, ok := elems[b].LessThan(elems[a])
			return ok && less
		})
		writeBack(tbl, elems)
		return []Value{args[0]}, nil
	})

	// sort.by(table, fn) - sort using a comparison function fn(a, b) -> bool
	set("by", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
			return nil, fmt.Errorf("bad arguments to 'sort.by' (table and function expected)")
		}
		tbl := args[0].Table()
		comp := args[1]
		elems := extractArray(tbl)
		var sortErr error
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
		if sortErr != nil {
			return nil, sortErr
		}
		writeBack(tbl, elems)
		return []Value{args[0]}, nil
	})

	// sort.byKey(table, fn) - sort using a key extraction function fn(elem) -> key
	set("byKey", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
			return nil, fmt.Errorf("bad arguments to 'sort.byKey' (table and function expected)")
		}
		tbl := args[0].Table()
		keyFn := args[1]
		elems := extractArray(tbl)

		// Pre-compute keys
		keys := make([]Value, len(elems))
		for i, elem := range elems {
			results, err := interp.callFunction(keyFn, []Value{elem})
			if err != nil {
				return nil, err
			}
			if len(results) > 0 {
				keys[i] = results[0]
			} else {
				keys[i] = NilValue()
			}
		}

		var sortErr error
		sort.SliceStable(elems, func(a, b int) bool {
			if sortErr != nil {
				return false
			}
			less, ok := keys[a].LessThan(keys[b])
			return ok && less
		})
		if sortErr != nil {
			return nil, sortErr
		}
		writeBack(tbl, elems)
		return []Value{args[0]}, nil
	})

	// sort.isSorted(table) - check if array is sorted in ascending order
	set("isSorted", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.isSorted' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()
		for i := 1; i < length; i++ {
			a := tbl.RawGet(IntValue(int64(i)))
			b := tbl.RawGet(IntValue(int64(i + 1)))
			less, ok := b.LessThan(a)
			if ok && less {
				return []Value{BoolValue(false)}, nil
			}
		}
		return []Value{BoolValue(true)}, nil
	})

	// sort.reverse(table) - reverse array in place, returns the table
	set("reverse", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.reverse' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()
		for i, j := 1, length; i < j; i, j = i+1, j-1 {
			vi := tbl.RawGet(IntValue(int64(i)))
			vj := tbl.RawGet(IntValue(int64(j)))
			tbl.RawSet(IntValue(int64(i)), vj)
			tbl.RawSet(IntValue(int64(j)), vi)
		}
		return []Value{args[0]}, nil
	})

	// sort.bsearch(table, value) - binary search in a sorted array
	// Returns the index (1-based) if found, or nil if not found
	set("bsearch", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad arguments to 'sort.bsearch' (sorted table and value expected)")
		}
		tbl := args[0].Table()
		target := args[1]
		length := tbl.Length()

		// Binary search
		lo, hi := 1, length
		for lo <= hi {
			mid := lo + (hi-lo)/2
			midVal := tbl.RawGet(IntValue(int64(mid)))
			if midVal.Equal(target) {
				return []Value{IntValue(int64(mid))}, nil
			}
			less, ok := midVal.LessThan(target)
			if ok && less {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		return []Value{NilValue()}, nil
	})

	// sort.unique(table) - remove consecutive duplicates from a sorted array
	// Returns a new table with unique elements
	set("unique", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.unique' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()
		result := NewTable()
		if length == 0 {
			return []Value{TableValue(result)}, nil
		}

		idx := int64(1)
		prev := tbl.RawGet(IntValue(1))
		result.RawSet(IntValue(idx), prev)
		for i := 2; i <= length; i++ {
			curr := tbl.RawGet(IntValue(int64(i)))
			if !curr.Equal(prev) {
				idx++
				result.RawSet(IntValue(idx), curr)
				prev = curr
			}
		}
		return []Value{TableValue(result)}, nil
	})

	// sort.partition(table, fn) - partition array into two tables based on predicate
	// Returns (trueTable, falseTable) where trueTable has elements where fn(elem) is truthy
	set("partition", func(args []Value) ([]Value, error) {
		if len(args) < 2 || !args[0].IsTable() || !args[1].IsFunction() {
			return nil, fmt.Errorf("bad arguments to 'sort.partition' (table and function expected)")
		}
		tbl := args[0].Table()
		pred := args[1]
		length := tbl.Length()

		trueResult := NewTable()
		falseResult := NewTable()
		trueIdx := int64(1)
		falseIdx := int64(1)

		for i := 1; i <= length; i++ {
			v := tbl.RawGet(IntValue(int64(i)))
			results, err := interp.callFunction(pred, []Value{v})
			if err != nil {
				return nil, err
			}
			if len(results) > 0 && results[0].Truthy() {
				trueResult.RawSet(IntValue(trueIdx), v)
				trueIdx++
			} else {
				falseResult.RawSet(IntValue(falseIdx), v)
				falseIdx++
			}
		}
		return []Value{TableValue(trueResult), TableValue(falseResult)}, nil
	})

	// sort.min(table [, fn]) - find the minimum element
	// Optional fn is a key function: fn(elem) -> comparable key
	set("min", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.min' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()
		if length == 0 {
			return []Value{NilValue()}, nil
		}

		hasKeyFn := len(args) >= 2 && args[1].IsFunction()
		best := tbl.RawGet(IntValue(1))
		bestKey := best
		if hasKeyFn {
			results, err := interp.callFunction(args[1], []Value{best})
			if err != nil {
				return nil, err
			}
			if len(results) > 0 {
				bestKey = results[0]
			}
		}

		for i := 2; i <= length; i++ {
			v := tbl.RawGet(IntValue(int64(i)))
			vKey := v
			if hasKeyFn {
				results, err := interp.callFunction(args[1], []Value{v})
				if err != nil {
					return nil, err
				}
				if len(results) > 0 {
					vKey = results[0]
				}
			}
			less, ok := vKey.LessThan(bestKey)
			if ok && less {
				best = v
				bestKey = vKey
			}
		}
		return []Value{best}, nil
	})

	// sort.max(table [, fn]) - find the maximum element
	// Optional fn is a key function: fn(elem) -> comparable key
	set("max", func(args []Value) ([]Value, error) {
		if len(args) < 1 || !args[0].IsTable() {
			return nil, fmt.Errorf("bad argument #1 to 'sort.max' (table expected)")
		}
		tbl := args[0].Table()
		length := tbl.Length()
		if length == 0 {
			return []Value{NilValue()}, nil
		}

		hasKeyFn := len(args) >= 2 && args[1].IsFunction()
		best := tbl.RawGet(IntValue(1))
		bestKey := best
		if hasKeyFn {
			results, err := interp.callFunction(args[1], []Value{best})
			if err != nil {
				return nil, err
			}
			if len(results) > 0 {
				bestKey = results[0]
			}
		}

		for i := 2; i <= length; i++ {
			v := tbl.RawGet(IntValue(int64(i)))
			vKey := v
			if hasKeyFn {
				results, err := interp.callFunction(args[1], []Value{v})
				if err != nil {
					return nil, err
				}
				if len(results) > 0 {
					vKey = results[0]
				}
			}
			less, ok := bestKey.LessThan(vKey)
			if ok && less {
				best = v
				bestKey = vKey
			}
		}
		return []Value{best}, nil
	})

	return t
}
