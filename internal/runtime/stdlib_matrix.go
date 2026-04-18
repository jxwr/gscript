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

	return t
}
