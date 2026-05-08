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

	set := func(name string, gf *GoFunction) {
		gf.Name = "matrix." + name
		t.RawSet(StringValue(name), FunctionValue(gf))
	}

	// matrix.dense(rows, cols) — create a DenseMatrix (shared flat backing).
	set("dense", &GoFunction{
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("matrix.dense: need 2 arguments (rows, cols)")
			}
			v, err := matrixDenseValue(args[0], args[1])
			if err != nil {
				return nil, err
			}
			return []Value{v}, nil
		},
		FastArg2: matrixDenseValue,
	})

	// matrix.getf(m, i, j) — fast direct access into DenseMatrix flat
	// backing. R43 Phase 2: the JIT recognises this pattern and emits
	// a 3-insn ARM64 sequence skipping the row wrapper. Go fallback
	// below is used when the JIT isn't active (Tier 0, tests).
	set("getf", &GoFunction{
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 3 {
				return nil, fmt.Errorf("matrix.getf: need 3 arguments (m, i, j)")
			}
			v, err := matrixGetfValue(args[0], args[1], args[2])
			if err != nil {
				return nil, err
			}
			return []Value{v}, nil
		},
		FastArg3: matrixGetfValue,
	})

	// matrix.setf(m, i, j, v) — fast direct write into DenseMatrix flat
	// backing. Same Phase 2 treatment as getf.
	set("setf", &GoFunction{
		Fn: func(args []Value) ([]Value, error) {
			if len(args) < 4 {
				return nil, fmt.Errorf("matrix.setf: need 4 arguments (m, i, j, v)")
			}
			if _, err := matrixSetfValue(args[0], args[1], args[2], args[3]); err != nil {
				return nil, err
			}
			return nil, nil
		},
		FastArg4: matrixSetfValue,
	})

	return t
}

func matrixDenseValue(rowsValue, colsValue Value) (Value, error) {
	if !rowsValue.IsInt() || !colsValue.IsInt() {
		return NilValue(), fmt.Errorf("matrix.dense: rows and cols must be integers")
	}
	rows := int(rowsValue.Int())
	cols := int(colsValue.Int())
	return TableValue(NewDenseMatrix(rows, cols)), nil
}

func matrixGetfValue(matrixValue, rowValue, colValue Value) (Value, error) {
	m, i, j, stride, err := matrixDenseAccess(matrixValue, rowValue, colValue, "matrix.getf")
	if err != nil {
		return NilValue(), err
	}
	flat := (*[1 << 30]float64)(m.dmFlat)
	return FloatValue(flat[i*stride+j]), nil
}

func matrixSetfValue(matrixValue, rowValue, colValue, value Value) (Value, error) {
	m, i, j, stride, err := matrixDenseAccess(matrixValue, rowValue, colValue, "matrix.setf")
	if err != nil {
		return NilValue(), err
	}
	var f float64
	switch {
	case value.IsFloat():
		f = value.Float()
	case value.IsInt():
		f = float64(value.Int())
	default:
		return NilValue(), fmt.Errorf("matrix.setf: value must be numeric")
	}
	flat := (*[1 << 30]float64)(m.dmFlat)
	flat[i*stride+j] = f
	return NilValue(), nil
}

func matrixDenseAccess(matrixValue, rowValue, colValue Value, name string) (*Table, int, int, int, error) {
	if !matrixValue.IsTable() {
		return nil, 0, 0, 0, fmt.Errorf("%s: argument 1 must be a matrix", name)
	}
	m := matrixValue.Table()
	if m.dmStride <= 0 {
		return nil, 0, 0, 0, fmt.Errorf("%s: argument 1 is not a DenseMatrix", name)
	}
	i := int(rowValue.Int())
	j := int(colValue.Int())
	stride := int(m.dmStride)
	// Bounds check would be via dmRows; we stored stride but not rows. Validate
	// through the outer array length, matching the semantic fallback.
	if i < 0 || i >= len(m.array) || j < 0 || j >= stride {
		return nil, 0, 0, 0, fmt.Errorf("%s: index out of range", name)
	}
	return m, i, j, stride, nil
}
