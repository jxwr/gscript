package vm

import "testing"

func TestMatrixMultiplyKernelRecognizesStructuralProto(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
		func product(left, right, size) {
			c := {}
			for i := 0; i < size; i++ {
				row := {}
				ai := left[i]
				for j := 0; j < size; j++ {
					sum := 0.0
					for k := 0; k < size; k++ {
						sum = sum + ai[k] * right[k][j]
					}
					row[j] = sum
				}
				c[i] = row
			}
			return c
		}
	`)
	defer vm.Close()
	if !isMatrixMultiplyProto(proto.Protos[0]) {
		t.Fatal("nested matrix multiply proto not recognized")
	}
}

func TestMatrixMultiplyKernelRecognizesDenseUnroll2Proto(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
		func dense_product(a, b, n) {
			c := matrix.dense(n, n)
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					sum := 0.0
					k := 0
					for k + 1 < n {
						sum = sum + matrix.getf(a, i, k) * matrix.getf(b, k, j)
						sum = sum + matrix.getf(a, i, k + 1) * matrix.getf(b, k + 1, j)
						k = k + 2
					}
					for k < n {
						sum = sum + matrix.getf(a, i, k) * matrix.getf(b, k, j)
						k = k + 1
					}
					matrix.setf(c, i, j, sum)
				}
			}
			return c
		}
	`)
	defer vm.Close()
	if !isMatrixMultiplyProto(proto.Protos[0]) {
		t.Fatal("dense unroll2 matrix multiply proto not recognized")
	}
}

func TestMatrixMultiplyKernelRecognizesDenseTransposedProto(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
		func dense_product(a, bT, c, n) {
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					sum := 0.0
					for k := 0; k < n; k++ {
						sum = sum + matrix.getf(a, i, k) * matrix.getf(bT, j, k)
					}
					matrix.setf(c, i, j, sum)
				}
			}
		}
	`)
	defer vm.Close()
	if !isDenseMatrixMultiplyTransposedProto(proto.Protos[0]) {
		t.Fatal("dense transposed matrix multiply proto not recognized")
	}
}

func TestMatrixMultiplyKernelCorrectness(t *testing.T) {
	globals := compileAndRun(t, `
		func product(left, right, size) {
			c := {}
			for i := 0; i < size; i++ {
				row := {}
				ai := left[i]
				for j := 0; j < size; j++ {
					sum := 0.0
					for k := 0; k < size; k++ {
						sum = sum + ai[k] * right[k][j]
					}
					row[j] = sum
				}
				c[i] = row
			}
			return c
		}
		a := {}
		a0 := {}
		a0[0] = 1.0
		a0[1] = 2.0
		a[0] = a0
		a1 := {}
		a1[0] = 3.0
		a1[1] = 4.0
		a[1] = a1
		b := {}
		b0 := {}
		b0[0] = 5.0
		b0[1] = 6.0
		b[0] = b0
		b1 := {}
		b1[0] = 7.0
		b1[1] = 8.0
		b[1] = b1
		c := product(a, b, 2)
		result := c[0][0] + c[0][1] + c[1][0] + c[1][1]
	`)
	expectGlobalFloat(t, globals, "result", 134.0)
}

func TestMatmulDenseWholeCallKernelCorrectness(t *testing.T) {
	globals := compileAndRun(t, `
		func dense_product(a, b, n) {
			c := matrix.dense(n, n)
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					sum := 0.0
					k := 0
					for k + 1 < n {
						sum = sum + matrix.getf(a, i, k) * matrix.getf(b, k, j)
						sum = sum + matrix.getf(a, i, k + 1) * matrix.getf(b, k + 1, j)
						k = k + 2
					}
					for k < n {
						sum = sum + matrix.getf(a, i, k) * matrix.getf(b, k, j)
						k = k + 1
					}
					matrix.setf(c, i, j, sum)
				}
			}
			return c
		}
		a := matrix.dense(2, 2)
		b := matrix.dense(2, 2)
		matrix.setf(a, 0, 0, 1.0)
		matrix.setf(a, 0, 1, 2.0)
		matrix.setf(a, 1, 0, 3.0)
		matrix.setf(a, 1, 1, 4.0)
		matrix.setf(b, 0, 0, 5.0)
		matrix.setf(b, 0, 1, 6.0)
		matrix.setf(b, 1, 0, 7.0)
		matrix.setf(b, 1, 1, 8.0)
		c := dense_product(a, b, 2)
		result := matrix.getf(c, 0, 0) + matrix.getf(c, 0, 1) + matrix.getf(c, 1, 0) + matrix.getf(c, 1, 1)
	`)
	expectGlobalFloat(t, globals, "result", 134.0)
}

func TestMatmulDenseTransposedWholeCallKernelCorrectness(t *testing.T) {
	globals := compileAndRun(t, `
		func dense_product(a, bT, c, n) {
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					sum := 0.0
					for k := 0; k < n; k++ {
						sum = sum + matrix.getf(a, i, k) * matrix.getf(bT, j, k)
					}
					matrix.setf(c, i, j, sum)
				}
			}
		}
		a := matrix.dense(2, 2)
		bT := matrix.dense(2, 2)
		c := matrix.dense(2, 2)
		matrix.setf(a, 0, 0, 1.0)
		matrix.setf(a, 0, 1, 2.0)
		matrix.setf(a, 1, 0, 3.0)
		matrix.setf(a, 1, 1, 4.0)
		matrix.setf(bT, 0, 0, 5.0)
		matrix.setf(bT, 0, 1, 7.0)
		matrix.setf(bT, 1, 0, 6.0)
		matrix.setf(bT, 1, 1, 8.0)
		dense_product(a, bT, c, 2)
		result := matrix.getf(c, 0, 0) + matrix.getf(c, 0, 1) + matrix.getf(c, 1, 0) + matrix.getf(c, 1, 1)
	`)
	expectGlobalFloat(t, globals, "result", 134.0)
}

func TestMatrixMultiplyKernelFallsBackForMetatableRows(t *testing.T) {
	globals := compileAndRun(t, `
		func product(left, right, size) {
			c := {}
			for i := 0; i < size; i++ {
				row := {}
				ai := left[i]
				for j := 0; j < size; j++ {
					sum := 0.0
					for k := 0; k < size; k++ {
						sum = sum + ai[k] * right[k][j]
					}
					row[j] = sum
				}
				c[i] = row
			}
			return c
		}
		a := {}
		a0 := {}
		a0[0] = 1.0
		a0[1] = 1.0
		a[0] = a0
		a1 := {}
		a1[0] = 1.0
		a1[1] = 1.0
		a[1] = a1
		b := {}
		b0 := {}
		b0[0] = 2.0
		b[0] = b0
		b1 := {}
		b1[0] = 3.0
		b[1] = b1
		calls := 0
		mt := {}
		mt["__index"] = func(t, key) {
			calls = calls + 1
			return 10.0
		}
		setmetatable(b0, mt)
		setmetatable(b1, mt)
		c := product(a, b, 2)
		result := c[0][1]
	`)
	expectGlobalInt(t, globals, "calls", 4)
	expectGlobalFloat(t, globals, "result", 20.0)
}
