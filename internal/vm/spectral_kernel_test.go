package vm

import (
	"math"
	"testing"

	"github.com/gscript/gscript/internal/lexer"
	"github.com/gscript/gscript/internal/parser"
	"github.com/gscript/gscript/internal/runtime"
)

func compileSpectralKernelTestProgram(t *testing.T, src string) (*FuncProto, *VM) {
	t.Helper()
	tokens, err := lexer.New(src).Tokenize()
	if err != nil {
		t.Fatalf("lexer error: %v", err)
	}
	prog, err := parser.New(tokens).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	proto, err := Compile(prog)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	vm := New(runtime.NewInterpreterGlobals())
	return proto, vm
}

func TestSpectralKernelRecognizesStructuralProtos(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, `
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
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute definitions: %v", err)
	}
	if !isSpectralAProto(proto.Protos[0]) {
		t.Fatal("A proto not recognized")
	}
	if got := classifySpectralMultiplyProto(proto.Protos[1]); got != spectralAv {
		t.Fatalf("multiplyAv classified as %v", got)
	}
	if got := classifySpectralMultiplyProto(proto.Protos[2]); got != spectralAtv {
		t.Fatalf("multiplyAtv classified as %v", got)
	}
	if !vm.isSpectralAtAvProto(proto.Protos[3]) {
		t.Fatal("multiplyAtAv proto not recognized")
	}
}

func TestSpectralKernelWholeAtAvCorrectness(t *testing.T) {
	globals := compileAndRun(t, `
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
		v := {}
		out := {}
		for i := 0; i < 4; i++ {
			v[i] = 1.0
			out[i] = 0.0
		}
		multiplyAtAv(4, v, out)
		result := out[0] + out[1] + out[2] + out[3]
	`)
	got := globals["result"].Number()
	if got < 4.37 || got > 4.38 {
		t.Fatalf("result = %.12f, want spectral AtAv sum near 4.375", got)
	}
}

func TestSpectralCoefficientCacheMatchesAAndBoundsLargeN(t *testing.T) {
	var cache spectralKernelCache
	a, at, ok := cache.coefficients(4)
	if !ok {
		t.Fatal("small spectral coefficient cache rejected")
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			want := spectralA(i, j)
			if got := a[i*4+j]; got != want {
				t.Fatalf("a[%d,%d] = %.17g, want %.17g", i, j, got, want)
			}
			if got := at[j*4+i]; got != want {
				t.Fatalf("at[%d,%d] = %.17g, want %.17g", j, i, got, want)
			}
		}
	}

	if _, _, ok := cache.coefficients(maxSpectralCoefficientFloats); ok {
		t.Fatal("oversized spectral coefficient cache should fall back")
	}
}

func TestSpectralMatrixVectorNoAliasMatchesRowMajor(t *testing.T) {
	var cache spectralKernelCache
	const n = 7
	a, _, ok := cache.coefficients(n)
	if !ok {
		t.Fatal("coefficient cache rejected small n")
	}
	v := []float64{1.0, -2.5, 3.25, 4.0, -5.5, 6.75, 0.5}
	want := make([]float64, n)
	got := make([]float64, n)
	floatMatrixVectorRowMajor(a, n, v, want)
	floatMatrixVectorNoAlias(a, n, v, got)
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("row %d = %.17g, want %.17g", i, got[i], want[i])
		}
	}
}

func TestSpectralMatrixVectorKeepsAliasOrder(t *testing.T) {
	var cache spectralKernelCache
	const n = 6
	a, _, ok := cache.coefficients(n)
	if !ok {
		t.Fatal("coefficient cache rejected small n")
	}
	values := []float64{1.0, 2.0, -3.0, 4.5, -5.25, 6.75}
	want := append([]float64(nil), values...)
	got := append([]float64(nil), values...)
	for i := 0; i < n; i++ {
		row := a[i*n : (i+1)*n]
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += row[j] * want[j]
		}
		want[i] = sum
	}
	spectralMatrixVector(a, n, got, got)
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("alias row %d = %.17g, want %.17g", i, got[i], want[i])
		}
	}
}

func TestSpectralKernelCachedDirectMultiplyAliasCorrectness(t *testing.T) {
	values := []float64{1.0, 2.0, -3.0, 4.5}
	want := append([]float64(nil), values...)
	for i := 0; i < len(want); i++ {
		sum := 0.0
		for j := 0; j < len(want); j++ {
			sum += spectralA(i, j) * want[j]
		}
		want[i] = sum
	}
	globals := compileAndRun(t, `
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
		warmV := {}
		warmOut := {}
		for i := 0; i < 4; i++ {
			warmV[i] = 1.0
			warmOut[i] = 0.0
		}
		multiplyAtAv(4, warmV, warmOut)
		v := {}
		v[0] = 1.0
		v[1] = 2.0
		v[2] = -3.0
		v[3] = 4.5
		multiplyAv(4, v, v)
		r0 := v[0]
		r1 := v[1]
		r2 := v[2]
		r3 := v[3]
	`)
	for i, name := range []string{"r0", "r1", "r2", "r3"} {
		got := globals[name].Number()
		if math.Float64bits(got) != math.Float64bits(want[i]) {
			t.Fatalf("%s = %.17g, want %.17g", name, got, want[i])
		}
	}
}

func TestSpectralKernelFallsBackWhenCoefficientFunctionRebound(t *testing.T) {
	globals := compileAndRun(t, `
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
		calls := 0
		A = func(i, j) {
			calls = calls + 1
			return 1.0
		}
		v := {}
		out := {}
		for i := 0; i < 3; i++ {
			v[i] = 2.0
			out[i] = 0.0
		}
		multiplyAv(3, v, out)
		result := out[0] + out[1] + out[2]
	`)
	expectGlobalInt(t, globals, "calls", 9)
	expectGlobalFloat(t, globals, "result", 18.0)
}

func TestSpectralKernelFallsBackWhenCalleeRebound(t *testing.T) {
	globals := compileAndRun(t, `
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
		calls := 0
		multiplyAv = func(n, v, av) {
			calls = calls + 1
			for i := 0; i < n; i++ { av[i] = 1.0 }
		}
		v := {}
		out := {}
		for i := 0; i < 3; i++ {
			v[i] = 2.0
			out[i] = 0.0
		}
		multiplyAtAv(3, v, out)
		result := out[0] + out[1] + out[2]
	`)
	expectGlobalInt(t, globals, "calls", 1)
	got := globals["result"].Number()
	if got < 2.76 || got > 2.77 {
		t.Fatalf("result = %.12f, want fallback result near 2.763", got)
	}
}
