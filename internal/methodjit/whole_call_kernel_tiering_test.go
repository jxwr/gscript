package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	gvm "github.com/gscript/gscript/internal/vm"
)

func TestRecognizedWholeCallKernelStaysOutOfTier2(t *testing.T) {
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

		func multiplyAtv(n, v, atv) {
			for i := 0; i < n; i++ {
				sum := 0.0
				for j := 0; j < n; j++ {
					sum = sum + A(j, i) * v[j]
				}
				atv[i] = sum
			}
		}

		func multiplyAtAv(n, v, atav) {
			u := {}
			for i := 0; i < n; i++ { u[i] = 0.0 }
			multiplyAv(n, v, u)
			multiplyAtv(n, u, atav)
		}
	`)
	target := findProtoByName(top, "multiplyAv")
	if target == nil {
		t.Fatal("multiplyAv proto not found")
	}
	if _, ok := recognizedWholeCallKernelForTiering(target); !ok {
		t.Fatal("multiplyAv was not recognized as a whole-call kernel")
	}
	target.CallCount = BaselineCompileThreshold
	if got := NewTieringManager().TryCompile(target); got != nil {
		t.Fatalf("TryCompile returned %T, want nil whole-call-kernel routing", got)
	}
	if !target.JITDisabled {
		t.Fatal("whole-call kernel proto was not marked JITDisabled")
	}
}

func TestWholeCallKernelDriverStaysOutOfTier2(t *testing.T) {
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

		func multiplyAtv(n, v, atv) {
			for i := 0; i < n; i++ {
				sum := 0.0
				for j := 0; j < n; j++ {
					sum = sum + A(j, i) * v[j]
				}
				atv[i] = sum
			}
		}

		func multiplyAtAv(n, v, atav) {
			u := {}
			for i := 0; i < n; i++ { u[i] = 0.0 }
			multiplyAv(n, v, u)
			multiplyAtv(n, u, atav)
		}
	`)
	target := findProtoByName(top, "multiplyAtAv")
	if target == nil {
		t.Fatal("multiplyAtAv proto not found")
	}
	v := gvm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	tm := NewTieringManager()
	tm.SetCallVM(v)
	if _, _, ok := tm.wholeCallKernelCalleeForTiering(target); !ok {
		t.Fatal("multiplyAtAv did not find whole-call kernel callees")
	}
	target.CallCount = BaselineCompileThreshold
	if got := tm.TryCompile(target); got != nil {
		t.Fatalf("TryCompile returned %T, want nil whole-call driver routing", got)
	}
	if !target.JITDisabled {
		t.Fatal("whole-call kernel driver proto was not marked JITDisabled")
	}
}
