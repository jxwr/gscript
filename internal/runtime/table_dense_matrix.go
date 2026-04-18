// table_dense_matrix.go — R42 DenseMatrix Phase 1 (JIT-compatible).
//
// DESIGN: NewDenseMatrix returns a plain table-of-tables where all row
// tables share a SINGLE flat []float64 backing. The outer Table is
// arrayKind=ArrayMixed (looks ordinary to the JIT); each row Table
// is arrayKind=ArrayFloat with floatArray aliased to a slice of the
// flat backing. JIT emits existing ArrayMixed + ArrayFloat fast paths
// without any new ArrayKind case.
//
// Why not a new ArrayKind: adding one requires new emit_table_array.go
// cases; otherwise the existing 4-way kind guard deopts on unknown
// kinds. Phase 1 stays inside existing JIT assumptions.
//
// Memory benefit: one N×M float64 allocation for the backing vs N
// separate row allocations in a normally-built matrix. Cache locality:
// flat backing fits in fewer cache lines; the JIT-emitted ArrayFloat
// load lands on the shared storage.
//
// Phase 2 (future rounds) can add a fast path: detect the
// DenseMatrixBuilt flag on the outer Table and compile a single-load
// shortcut for t[i][j] that skips the row indirection. That's the
// 3.8× microbench upper bound.

package runtime

import "unsafe"

// uintptrOf is a tiny helper for test memory-adjacency checks.
func uintptrOf(p *float64) uintptr { return uintptr(unsafe.Pointer(p)) }

// SetDenseMatrixMeta stamps (flatPtr, stride) on the outer Table so
// the R43 Phase 2 JIT intrinsic `matrix.getf(m, i, j)` can skip
// the row-wrapper indirection. Set at construction; reset if the user
// later replaces a row (rare; we conservatively do NOT invalidate
// automatically — Phase 2 emit does a dmStride != 0 guard per call).
func (t *Table) setDenseMatrixMeta(flat []float64, stride int) {
	if len(flat) == 0 || stride <= 0 {
		return
	}
	t.dmFlat = unsafe.Pointer(&flat[0])
	t.dmStride = int32(stride)
}

// NewDenseMatrix allocates a rows×cols float64 matrix stored as
// flat storage shared by row wrappers. The returned Table looks like
// a normal nested table to the JIT — you can t[i][j] as usual — but
// all rows alias ONE contiguous []float64 backing.
//
// Both rows and cols must be positive. For degenerate dims, returns a
// plain empty Table.
func NewDenseMatrix(rows, cols int) *Table {
	if rows <= 0 || cols <= 0 {
		return NewTable()
	}
	// One flat backing shared by all rows.
	backing := make([]float64, rows*cols)

	outer := DefaultHeap.AllocTable()
	outer.array = DefaultHeap.AllocValues(rows, rows)
	outer.keysDirty = true
	// R43 Phase 2: stamp the DenseMatrix descriptor for JIT fast path.
	outer.setDenseMatrixMeta(backing, cols)

	// Each row: ArrayFloat table whose floatArray IS a sub-slice of
	// the shared backing. Capacity-bounded (start:end:end) so a Grow
	// doesn't reallocate in place and shatter the aliasing.
	for i := 0; i < rows; i++ {
		start := i * cols
		end := start + cols
		row := DefaultHeap.AllocTable()
		row.arrayKind = ArrayFloat
		row.floatArray = backing[start:end:end]
		row.keysDirty = true
		outer.array[i] = TableValue(row)
	}
	return outer
}

// DenseMatrixBackingByRows is a test/debug helper that reconstructs
// the logical flat backing of a DenseMatrix by concatenating row
// contents. Uses only the public row iteration path so it works
// regardless of slice-capacity clamping. Returns nil for tables not
// built by NewDenseMatrix.
func DenseMatrixBackingByRows(t *Table) []float64 {
	if t == nil || len(t.array) == 0 {
		return nil
	}
	first := t.array[0]
	if !first.IsTable() {
		return nil
	}
	firstRow := first.Table()
	if firstRow == nil || firstRow.arrayKind != ArrayFloat {
		return nil
	}
	stride := len(firstRow.floatArray)
	rows := len(t.array)
	out := make([]float64, 0, rows*stride)
	for i := 0; i < rows; i++ {
		rv := t.array[i]
		if !rv.IsTable() {
			return nil
		}
		row := rv.Table()
		if row == nil || row.arrayKind != ArrayFloat {
			return nil
		}
		out = append(out, row.floatArray...)
	}
	return out
}

// DenseMatrixRowsShareBacking checks whether two row wrappers of a
// DenseMatrix point into the same underlying array. Returns true iff
// both rows are ArrayFloat and the tail of row[i] is immediately
// followed in memory by the head of row[i+1].
func DenseMatrixRowsShareBacking(t *Table) bool {
	if t == nil || len(t.array) < 2 {
		return false
	}
	for i := 0; i < len(t.array)-1; i++ {
		a := t.array[i].Table()
		b := t.array[i+1].Table()
		if a == nil || b == nil {
			return false
		}
		if a.arrayKind != ArrayFloat || b.arrayKind != ArrayFloat {
			return false
		}
		if len(a.floatArray) == 0 || len(b.floatArray) == 0 {
			return false
		}
		// Last element of a and first element of b should be adjacent in memory.
		lastPtr := &a.floatArray[len(a.floatArray)-1]
		firstBPtr := &b.floatArray[0]
		// Ptr-diff via unsafe.Sizeof(float64)=8.
		expected := uintptrOf(lastPtr) + 8
		if uintptrOf(firstBPtr) != expected {
			return false
		}
	}
	return true
}
