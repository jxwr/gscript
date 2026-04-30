package methodjit

import "testing"

func TestNestedMatmulUsesWholeCallKernelInsteadOfJIT(t *testing.T) {
	top := compileTop(t, `
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
	target := findProtoByName(top, "product")
	if target == nil {
		t.Fatal("product proto not found")
	}
	target.CallCount = BaselineCompileThreshold
	if got := NewTieringManager().TryCompile(target); got != nil {
		t.Fatalf("TryCompile returned %T, want nil whole-call-kernel routing", got)
	}
	if !target.JITDisabled {
		t.Fatal("nested matmul proto was not marked JITDisabled")
	}
}
