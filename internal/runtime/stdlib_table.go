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
