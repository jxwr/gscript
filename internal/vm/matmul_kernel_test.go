package vm

import "testing"

func TestMatmulKernelRecognizesStructuralProto(t *testing.T) {
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
	if !isNestedMatmulProto(proto.Protos[0]) {
		t.Fatal("nested matmul proto not recognized")
	}
}

func TestMatmulKernelCorrectness(t *testing.T) {
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

func TestMatmulKernelFallsBackForMetatableRows(t *testing.T) {
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
