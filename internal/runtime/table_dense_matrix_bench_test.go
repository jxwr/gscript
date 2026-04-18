// table_dense_matrix_bench_test.go — R42 microbench.
// Compare:
//   - Nested: plain table-of-tables, each row independently allocated
//   - DenseMatrix: table-of-tables whose rows share ONE flat []float64

package runtime

import (
	"testing"
	"unsafe"
)

func unsafePointerAdd(p *float64, offBytes int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(offBytes))
}

func buildNestedMatrix(n int) *Table {
	outer := NewTable()
	for i := 0; i < n; i++ {
		row := NewTable()
		for j := 0; j < n; j++ {
			row.RawSetInt(int64(j), FloatValue(float64(i*n+j)))
		}
		outer.RawSetInt(int64(i), TableValue(row))
	}
	return outer
}

func buildDenseMatrix(n int) *Table {
	m := NewDenseMatrix(n, n)
	for i := 0; i < n; i++ {
		row := m.RawGetInt(int64(i)).Table()
		for j := 0; j < n; j++ {
			row.RawSetInt(int64(j), FloatValue(float64(i*n+j)))
		}
	}
	return m
}

const benchN = 30

var benchSink float64

func BenchmarkNestedMatmulStyleAccess(b *testing.B) {
	m := buildNestedMatrix(benchN)
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		sum := 0.0
		for i := 0; i < benchN; i++ {
			row := m.RawGetInt(int64(i)).Table()
			for j := 0; j < benchN; j++ {
				sum += row.RawGetInt(int64(j)).Float()
			}
		}
		benchSink = sum
	}
}

func BenchmarkDenseMatmulStyleAccess(b *testing.B) {
	m := buildDenseMatrix(benchN)
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		sum := 0.0
		for i := 0; i < benchN; i++ {
			row := m.RawGetInt(int64(i)).Table()
			for j := 0; j < benchN; j++ {
				sum += row.RawGetInt(int64(j)).Float()
			}
		}
		benchSink = sum
	}
}

// BenchmarkDenseDirectFlat: theoretical minimum — direct index into
// the shared flat backing via unsafe pointer arithmetic. This is what
// Phase 2 (JIT emit) could unlock by recognizing the DenseMatrix
// shape at compile time: a single LDR d0, [base, i*stride+j, LSL #3].
func BenchmarkDenseDirectFlat(b *testing.B) {
	m := buildDenseMatrix(benchN)
	// Extract first row to locate the flat backing head.
	row0 := m.array[0].Table()
	head := &row0.floatArray[0]
	stride := benchN
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		sum := 0.0
		for i := 0; i < benchN; i++ {
			base := i * stride
			for j := 0; j < benchN; j++ {
				// Unsafe pointer arithmetic to bypass bounds checks
				// and access the whole flat backing as one region.
				p := (*float64)(unsafePointerAdd(head, (base+j)*8))
				sum += *p
			}
		}
		benchSink = sum
	}
}
