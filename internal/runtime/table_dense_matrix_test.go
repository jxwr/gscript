// table_dense_matrix_test.go — R42 Phase 1 correctness tests.

package runtime

import (
	stdruntime "runtime"
	"testing"
)

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

func TestDenseMatrix_AutoAdoptsOrdinaryFloatRows(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(4, 0, ArrayMixed)
	for i := 0; i < 3; i++ {
		row := NewTableSizedKind(cols, 0, ArrayFloat)
		for j := 0; j < cols; j++ {
			row.RawSetInt(int64(j), FloatValue(float64(i*10+j)))
		}
		m.RawSetInt(int64(i), TableValue(row))
	}
	if m.dmStride != cols || m.dmFlat == nil {
		t.Fatalf("auto dense metadata stride=%d flat=%v, want stride=%d", m.dmStride, m.dmFlat, cols)
	}
	if !DenseMatrixRowsShareBacking(m) {
		t.Fatalf("auto-adopted rows should share backing")
	}
	row1 := m.RawGetInt(1).Table()
	row1.RawSetInt(2, FloatValue(42))
	if got := m.dmMeta.backing[1*cols+2]; got != 42 {
		t.Fatalf("in-bounds row write did not update dense backing: got %v", got)
	}
}

func TestDenseMatrix_AutoAdoptInvalidatesOnRowGrowth(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(2, 0, ArrayMixed)
	row := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		row.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(0, TableValue(row))
	if m.dmStride == 0 {
		t.Fatal("expected auto dense metadata")
	}
	row.RawSetInt(cols, FloatValue(3))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("row growth should invalidate parent dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}
}

func TestDenseMatrix_AutoAdoptInvalidatesOnRowDemotion(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(2, 0, ArrayMixed)
	row := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		row.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(0, TableValue(row))
	if m.dmStride == 0 {
		t.Fatal("expected auto dense metadata")
	}
	row.RawSetInt(3, StringValue("not-float"))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("row demotion should invalidate parent dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}
}

func TestDenseMatrix_AutoAdoptInvalidatesOnRowReplacement(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(2, 0, ArrayMixed)
	row := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		row.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(0, TableValue(row))
	if m.dmStride == 0 {
		t.Fatal("expected auto dense metadata")
	}
	replacement := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		replacement.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(0, TableValue(replacement))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("row replacement should invalidate dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}
}

func TestDenseMatrix_AutoAdoptSameStrideReplacementFallsBackToOrdinaryRows(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(3, 0, ArrayMixed)
	old := NewTableSizedKind(cols, 0, ArrayFloat)
	next := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		old.RawSetInt(int64(j), FloatValue(float64(j)))
		next.RawSetInt(int64(j), FloatValue(float64(100+j)))
	}
	m.RawSetInt(0, TableValue(old))
	m.RawSetInt(1, TableValue(next))
	if m.dmStride == 0 {
		t.Fatal("expected auto dense metadata")
	}

	replacement := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		replacement.RawSetInt(int64(j), FloatValue(float64(200+j)))
	}
	m.RawSetInt(0, TableValue(replacement))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("same-stride replacement should invalidate dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}
	old.RawSetInt(1, FloatValue(999))
	if got := m.RawGetInt(0).Table().RawGetInt(1).Float(); got != 201 {
		t.Fatalf("replacement row was affected by old adopted row write: got %v, want 201", got)
	}
}

func TestDenseMatrix_AutoAdoptInvalidatesOnIncompatibleRowStore(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(3, 0, ArrayMixed)
	row := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		row.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(0, TableValue(row))
	if m.dmStride == 0 {
		t.Fatal("expected auto dense metadata")
	}

	shortRow := NewTableSizedKind(cols-1, 0, ArrayFloat)
	for j := 0; j < cols-1; j++ {
		shortRow.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(1, TableValue(shortRow))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("incompatible row store should invalidate dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}
	if got := m.RawGetInt(1).Table(); got != shortRow {
		t.Fatalf("incompatible row store did not preserve ordinary table store")
	}
}

func TestDenseMatrix_NewDenseMatrixInvalidatesOnRowGrowth(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewDenseMatrix(2, cols)
	if m.dmStride != cols || m.dmFlat == nil {
		t.Fatalf("NewDenseMatrix dense metadata stride=%d flat=%v, want stride=%d", m.dmStride, m.dmFlat, cols)
	}
	row := m.RawGetInt(0).Table()
	row.RawSetInt(cols, FloatValue(1))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("NewDenseMatrix row growth should invalidate dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}
}

func TestDenseMatrix_AutoAdoptBackingSurvivesGC(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(4, 0, ArrayMixed)
	for i := 0; i < 3; i++ {
		row := NewTableSizedKind(cols, 0, ArrayFloat)
		for j := 0; j < cols; j++ {
			row.RawSetInt(int64(j), FloatValue(float64(i*cols+j)))
		}
		m.RawSetInt(int64(i), TableValue(row))
	}
	if m.dmStride != cols || m.dmFlat == nil {
		t.Fatalf("auto dense metadata stride=%d flat=%v, want stride=%d", m.dmStride, m.dmFlat, cols)
	}

	stdruntime.GC()
	for i := 0; i < 256; i++ {
		_ = make([]float64, cols*8)
	}
	stdruntime.GC()

	row := m.RawGetInt(2).Table()
	row.RawSetInt(5, FloatValue(1234))
	if got := m.dmMeta.backing[2*cols+5]; got != 1234 {
		t.Fatalf("dense backing not visible after GC: got %v", got)
	}
	stdruntime.KeepAlive(m)
}

func TestDenseMatrix_AutoAdoptRejectsSparseOuterRows(t *testing.T) {
	const cols = autoDenseMatrixMinStride
	m := NewTableSizedKind(4, 0, ArrayMixed)
	row := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		row.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m.RawSetInt(2, TableValue(row))
	if m.dmStride != 0 || m.dmFlat != nil {
		t.Fatalf("sparse row store should not enable dense metadata, stride=%d flat=%v", m.dmStride, m.dmFlat)
	}

	first := NewTableSizedKind(cols, 0, ArrayFloat)
	for j := 0; j < cols; j++ {
		first.RawSetInt(int64(j), FloatValue(float64(j)))
	}
	m2 := NewTableSizedKind(4, 0, ArrayMixed)
	m2.RawSetInt(0, TableValue(first))
	if m2.dmStride == 0 {
		t.Fatal("expected sequential first row to enable dense metadata")
	}
	m2.RawSetInt(2, TableValue(row))
	if m2.dmStride != 0 || m2.dmFlat != nil {
		t.Fatalf("sparse extension should invalidate dense metadata, stride=%d flat=%v", m2.dmStride, m2.dmFlat)
	}
}
