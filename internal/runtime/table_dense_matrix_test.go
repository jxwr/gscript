// table_dense_matrix_test.go — R42 Phase 1 correctness tests.

package runtime

import "testing"

func TestNewDenseMatrix_Basic(t *testing.T) {
	m := NewDenseMatrix(3, 4)
	// Outer looks like a plain ArrayMixed table of size 3.
	if m == nil {
		t.Fatal("NewDenseMatrix returned nil")
	}
	if m.arrayKind != ArrayMixed {
		t.Fatalf("outer arrayKind = %d, want ArrayMixed(0)", m.arrayKind)
	}
	if len(m.array) != 3 {
		t.Fatalf("outer array len = %d, want 3", len(m.array))
	}
	// Each row is an ArrayFloat table of size 4.
	for i := 0; i < 3; i++ {
		row := m.array[i].Table()
		if row == nil || row.arrayKind != ArrayFloat {
			t.Fatalf("row[%d] arrayKind = %d, want ArrayFloat(2)", i, row.arrayKind)
		}
		if len(row.floatArray) != 4 {
			t.Fatalf("row[%d] floatArray len = %d, want 4", i, len(row.floatArray))
		}
	}
}

func TestDenseMatrix_RowsShareBacking(t *testing.T) {
	// The core Phase 1 invariant: ALL rows alias one flat []float64.
	m := NewDenseMatrix(2, 3)

	r0 := m.RawGetInt(0).Table()
	r1 := m.RawGetInt(1).Table()

	r0.RawSetInt(0, FloatValue(1.5))
	r0.RawSetInt(1, FloatValue(2.5))
	r0.RawSetInt(2, FloatValue(3.5))
	r1.RawSetInt(0, FloatValue(10.0))
	r1.RawSetInt(1, FloatValue(20.0))
	r1.RawSetInt(2, FloatValue(30.0))

	// Inspect the shared backing via row concatenation.
	backing := DenseMatrixBackingByRows(m)
	if len(backing) != 6 {
		t.Fatalf("backing len %d, want 6", len(backing))
	}
	want := []float64{1.5, 2.5, 3.5, 10.0, 20.0, 30.0}
	for i, v := range want {
		if backing[i] != v {
			t.Errorf("backing[%d] = %v, want %v", i, backing[i], v)
		}
	}

	// Core invariant: row slices are memory-adjacent (one shared backing).
	if !DenseMatrixRowsShareBacking(m) {
		t.Error("rows of NewDenseMatrix should share a single contiguous backing")
	}
}

func TestDenseMatrix_MatmulCorrectness(t *testing.T) {
	// A 10×10 matmul through the nested API. Checks that row wrappers
	// behave identically to a plain nested table for computation.
	const n = 10
	a := NewDenseMatrix(n, n)
	b := NewDenseMatrix(n, n)
	c := NewDenseMatrix(n, n)

	for i := 0; i < n; i++ {
		ar := a.RawGetInt(int64(i)).Table()
		br := b.RawGetInt(int64(i)).Table()
		for j := 0; j < n; j++ {
			ar.RawSetInt(int64(j), FloatValue(float64(i+j+1)))
			br.RawSetInt(int64(j), FloatValue(float64(i*j+1)))
		}
	}

	for i := 0; i < n; i++ {
		ci := c.RawGetInt(int64(i)).Table()
		ai := a.RawGetInt(int64(i)).Table()
		for j := 0; j < n; j++ {
			sum := 0.0
			for k := 0; k < n; k++ {
				aik := ai.RawGetInt(int64(k)).Float()
				bkj := b.RawGetInt(int64(k)).Table().RawGetInt(int64(j)).Float()
				sum += aik * bkj
			}
			ci.RawSetInt(int64(j), FloatValue(sum))
		}
	}

	// c[0][0] = sum_k (0+k+1)(0*k+1) = sum_{k=0..9}(k+1) = 55
	c00 := c.RawGetInt(0).Table().RawGetInt(0).Float()
	if c00 != 55.0 {
		t.Errorf("c[0][0] = %v, want 55", c00)
	}
	// c[1][1] = sum_k (1+k+1)(1*k+1) = sum(k+2)(k+1)/k=0..9 = 440
	c11 := c.RawGetInt(1).Table().RawGetInt(1).Float()
	if c11 != 440.0 {
		t.Errorf("c[1][1] = %v, want 440", c11)
	}
}

func TestDenseMatrix_DegenerateDims(t *testing.T) {
	for _, dims := range [][2]int{{0, 5}, {5, 0}, {-1, 5}, {5, -1}, {0, 0}} {
		m := NewDenseMatrix(dims[0], dims[1])
		if m == nil {
			t.Errorf("NewDenseMatrix%v returned nil; should degenerate to empty Table", dims)
			continue
		}
		if DenseMatrixBackingByRows(m) != nil {
			t.Errorf("NewDenseMatrix%v should degenerate (no flat backing)", dims)
		}
	}
}
