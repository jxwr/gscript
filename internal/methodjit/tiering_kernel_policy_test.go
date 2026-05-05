//go:build darwin && arm64

package methodjit

import "testing"

func TestStructuralKernelTieringDecisionUsesPolicyObject(t *testing.T) {
	top := compileTop(t, `
		func A(i, j) {
			return 1.0 / ((i + j) * (i + j + 1) / 2 + i + 1)
		}

		func multiplyAv(n, v, av) {
			for i := 0; i < n; i++ {
				sum := 0.0
				for j := 0; j < n; j++ {
					sum = sum + A(i, j) * v[j]
				}
				av[i] = sum
			}
		}
	`)
	target := findProtoByName(top, "multiplyAv")
	if target == nil {
		t.Fatal("multiplyAv proto not found")
	}
	tm := NewTieringManager()
	decision, ok := tm.structuralKernelTieringDecision(target)
	if !ok {
		t.Fatal("expected structural kernel tiering decision")
	}
	if decision.reason != "whole_call_structural_kernel" {
		t.Fatalf("reason = %q, want whole_call_structural_kernel", decision.reason)
	}
	if decision.kernel != "spectral_multiply_av" {
		t.Fatalf("kernel = %q, want spectral_multiply_av", decision.kernel)
	}
	if decision.route != "whole_call_no_result" {
		t.Fatalf("route = %q, want whole_call_no_result", decision.route)
	}
}
