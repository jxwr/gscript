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
// The method JIT can detect dmStride on the outer Table and compile a
// direct t[i][j] load from dmFlat, skipping row-wrapper verification.

package runtime

import "unsafe"

const autoDenseMatrixMinStride = 16

// AutoDenseMatrixMinStride is the public mirror of the runtime auto-adoption
// gate used by method-JIT guards.
const AutoDenseMatrixMinStride = autoDenseMatrixMinStride

type denseMatrixMeta struct {
	backing []float64
	parent  *Table
}

// uintptrOf is a tiny helper for test memory-adjacency checks.
func uintptrOf(p *float64) uintptr { return uintptr(unsafe.Pointer(p)) }

// SetDenseMatrixMeta stamps (flatPtr, stride) on the outer Table so
// the method JIT can skip row-wrapper indirection for nested float loads.
func (t *Table) setDenseMatrixMeta(flat []float64, stride int) {
	if len(flat) == 0 || stride <= 0 {
		return
	}
	t.dmMeta = &denseMatrixMeta{backing: flat, parent: t}
	t.dmFlat = unsafe.Pointer(&flat[0])
	t.dmStride = int32(stride)
}

func (t *Table) clearDenseMatrixMeta() {
	t.dmFlat = nil
	t.dmStride = 0
	t.dmMeta = nil
}

func (t *Table) maybeClearDenseParentForWrite(key int64, val Value) {
	meta := t.dmMeta
	if meta == nil || meta.parent == nil || meta.parent == t {
		return
	}
	parent := meta.parent
	if parent.dmMeta != meta || parent.dmStride <= 0 {
		t.dmMeta = nil
		return
	}
	if t.arrayKind == ArrayFloat &&
		int(parent.dmStride) == len(t.floatArray) &&
		key >= 0 &&
		key < int64(len(t.floatArray)) &&
		val.Type() == TypeFloat {
		return
	}
	t.dmMeta = nil
	parent.clearDenseMatrixMeta()
}

// observeDenseMatrixRowStore keeps ordinary table-of-float-rows layouts
// compatible with DenseMatrix fast paths. It is called after ArrayMixed integer
// stores. Compatible row tables are rebound to slices of one contiguous backing,
// so later in-bounds row writes update the same memory read by dmFlat.
func (t *Table) observeDenseMatrixRowStore(key int64, val Value, oldLen int64) {
	if key < 0 || t.arrayKind != ArrayMixed || t.metatable != nil || t.hash != nil || t.imap != nil {
		if t.dmStride > 0 {
			t.clearDenseMatrixMeta()
		}
		return
	}
	row := val.Table()
	if row == nil || row.arrayKind != ArrayFloat || row.metatable != nil || row.hash != nil || row.imap != nil || len(row.floatArray) < autoDenseMatrixMinStride {
		if t.dmStride > 0 {
			t.clearDenseMatrixMeta()
		}
		return
	}
	if t.dmStride > 0 && key != oldLen {
		t.clearDenseMatrixMeta()
		return
	}
	stride := len(row.floatArray)
	if t.dmStride != 0 && int(t.dmStride) != stride {
		t.clearDenseMatrixMeta()
		return
	}
	src := row.floatArray
	if t.dmStride == 0 {
		if key != 0 || len(t.array) != 1 {
			return
		}
		rowsCap := typedArrayCapFor(int(key) + 1)
		backing := make([]float64, (int(key)+1)*stride, rowsCap*stride)
		t.setDenseMatrixMeta(backing, stride)
	} else {
		if t.dmMeta == nil {
			t.clearDenseMatrixMeta()
			return
		}
		oldRows := int(oldLen)
		if int(key) > oldRows || (int(key) == oldRows && len(t.array) != oldRows+1) {
			t.clearDenseMatrixMeta()
			return
		}
		t.ensureDenseMatrixRows(int(key) + 1)
	}
	if t.dmMeta == nil {
		return
	}

	start := int(key) * stride
	copy(t.dmMeta.backing[start:start+stride], src)
	row.floatArray = t.dmMeta.backing[start : start+stride : start+stride]
	if row.dmMeta != nil && row.dmMeta.parent != nil && row.dmMeta.parent != t {
		row.dmMeta.parent.clearDenseMatrixMeta()
	}
	row.dmMeta = t.dmMeta
}

func (t *Table) ensureDenseMatrixRows(rows int) {
	stride := int(t.dmStride)
	if stride <= 0 || t.dmMeta == nil {
		return
	}
	need := rows * stride
	if len(t.dmMeta.backing) >= need {
		return
	}
	if cap(t.dmMeta.backing) < need {
		nextRows := growTypedArrayCap(cap(t.dmMeta.backing)/stride, rows)
		next := make([]float64, need, nextRows*stride)
		copy(next, t.dmMeta.backing)
		t.dmMeta.backing = next
		t.rebindDenseMatrixRows()
	} else {
		t.dmMeta.backing = t.dmMeta.backing[:need]
	}
	t.dmFlat = unsafe.Pointer(&t.dmMeta.backing[0])
}

func (t *Table) rebindDenseMatrixRows() {
	stride := int(t.dmStride)
	if stride <= 0 || t.dmMeta == nil {
		return
	}
	maxRows := len(t.dmMeta.backing) / stride
	for i := 0; i < len(t.array) && i < maxRows; i++ {
		row := t.array[i].Table()
		if row == nil || row.arrayKind != ArrayFloat || len(row.floatArray) != stride {
			continue
		}
		start := i * stride
		row.floatArray = t.dmMeta.backing[start : start+stride : start+stride]
		row.dmMeta = t.dmMeta
	}
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
		row.dmMeta = outer.dmMeta
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
