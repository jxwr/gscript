package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

const mandelbrotKernelTestSource = `
func mandelbrot(size) {
    count := 0
    for y := 0; y < size; y++ {
        ci := 2.0 * y / size - 1.0
        for x := 0; x < size; x++ {
            cr := 2.0 * x / size - 1.5
            zr := 0.0
            zi := 0.0
            escaped := false
            for iter := 0; iter < 50; iter++ {
                tr := zr * zr - zi * zi + cr
                ti := 2.0 * zr * zi + ci
                zr = tr
                zi = ti
                if zr * zr + zi * zi > 4.0 {
                    escaped = true
                    break
                }
            }
            if !escaped { count = count + 1 }
        }
    }
    return count
}
`

func TestMandelbrotWholeCallKernelRecognizedAndMatchesVM(t *testing.T) {
	proto, vm := compileSpectralKernelTestProgram(t, mandelbrotKernelTestSource+`
result := mandelbrot(100)
`)
	mandel := proto.Protos[0]
	if !isMandelbrotCountProto(mandel) {
		t.Fatalf("mandelbrot kernel not recognized: code=%d const=%d maxstack=%d", len(mandel.Code), len(mandel.Constants), mandel.MaxStack)
	}
	if !cachedWholeCallKernelRecognized(mandel, wholeCallKernelMandelbrotCount) {
		t.Fatal("mandelbrot kernel cache did not mark proto recognized")
	}
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("VM execute: %v", err)
	}
	want := vm.GetGlobal("result")
	handled, got, err := vm.runMandelbrotCountWholeCallKernel(&Closure{Proto: mandel}, []runtime.Value{runtime.IntValue(100)})
	if err != nil {
		t.Fatalf("kernel error: %v", err)
	}
	if !handled || len(got) != 1 || got[0] != want {
		t.Fatalf("kernel result handled=%v got=%v want=%v", handled, got, want)
	}
}
