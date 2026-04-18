// stdlib_matrix.go — "matrix" builtin exposing NewDenseMatrix to GScript.
//
// Usage from GScript:
//   m := matrix.dense(rows, cols)   -- rows×cols matrix, rows share flat backing
//   m[i][j] = 1.5                    -- normal nested-table assignment
//   x := m[i][j]                     -- normal nested-table read
//
// R42 DenseMatrix Phase 1: user-visible API for the shared-backing
// matrix constructor. Memory layout optimization wins via cache
// locality; JIT-level indexing fast path lands in Phase 2.

package runtime

import "fmt"

// buildMatrixLib creates the "matrix" standard library table.
func buildMatrixLib() *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "matrix." + name,
			Fn:   fn,
		}))
	}

	// matrix.dense(rows, cols) — create a DenseMatrix (shared flat backing).
	set("dense", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("matrix.dense: need 2 arguments (rows, cols)")
		}
		if !args[0].IsInt() || !args[1].IsInt() {
			return nil, fmt.Errorf("matrix.dense: rows and cols must be integers")
		}
		rows := int(args[0].Int())
		cols := int(args[1].Int())
		return []Value{TableValue(NewDenseMatrix(rows, cols))}, nil
	})

	// matrix.getf(m, i, j) — fast direct access into DenseMatrix flat
	// backing. R43 Phase 2: the JIT recognises this pattern and emits
	// a 3-insn ARM64 sequence skipping the row wrapper. Go fallback
	// below is used when the JIT isn't active (Tier 0, tests).
	set("getf", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("matrix.getf: need 3 arguments (m, i, j)")
		}
		if !args[0].IsTable() {
			return nil, fmt.Errorf("matrix.getf: argument 1 must be a matrix")
		}
		m := args[0].Table()
		if m.dmStride <= 0 {
			return nil, fmt.Errorf("matrix.getf: argument 1 is not a DenseMatrix")
		}
		i := int(args[1].Int())
		j := int(args[2].Int())
		stride := int(m.dmStride)
		// Bounds check would be via dmRows; we stored stride but not
		// rows. For Phase 2 we validate via the outer array len.
		if i < 0 || i >= len(m.array) || j < 0 || j >= stride {
			return nil, fmt.Errorf("matrix.getf: index out of range")
		}
		flat := (*[1 << 30]float64)(m.dmFlat)
		return []Value{FloatValue(flat[i*stride+j])}, nil
	})

	// matrix.setf(m, i, j, v) — fast direct write into DenseMatrix flat
	// backing. Same Phase 2 treatment as getf.
	set("setf", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("matrix.setf: need 4 arguments (m, i, j, v)")
		}
		if !args[0].IsTable() {
			return nil, fmt.Errorf("matrix.setf: argument 1 must be a matrix")
		}
		m := args[0].Table()
		if m.dmStride <= 0 {
			return nil, fmt.Errorf("matrix.setf: argument 1 is not a DenseMatrix")
		}
		i := int(args[1].Int())
		j := int(args[2].Int())
		stride := int(m.dmStride)
		if i < 0 || i >= len(m.array) || j < 0 || j >= stride {
			return nil, fmt.Errorf("matrix.setf: index out of range")
		}
		var v float64
		switch {
		case args[3].IsFloat():
			v = args[3].Float()
		case args[3].IsInt():
			v = float64(args[3].Int())
		default:
			return nil, fmt.Errorf("matrix.setf: value must be numeric")
		}
		flat := (*[1 << 30]float64)(m.dmFlat)
		flat[i*stride+j] = v
		return nil, nil
	})

	return t
}
