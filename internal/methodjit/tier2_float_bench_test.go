//go:build darwin && arm64

package methodjit

import (
	"testing"
	"github.com/gscript/gscript/internal/runtime"
)

const mandelbrotSrc = `func mandelbrot(size) {
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
}`

func BenchmarkTier2Reg_Mandelbrot10(b *testing.B) {
	proto := compileFunctionB(b, mandelbrotSrc)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		b.Fatalf("Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(10)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}

func BenchmarkAll_VM_Mandelbrot10(b *testing.B) {
	allBenchVM(b, mandelbrotSrc, []runtime.Value{runtime.IntValue(10)})
}

func BenchmarkAll_T1_Mandelbrot10(b *testing.B) {
	allBenchTier1(b, mandelbrotSrc, []runtime.Value{runtime.IntValue(10)})
}

func BenchmarkTier2Mem_Mandelbrot10(b *testing.B) {
	proto := compileFunctionB(b, mandelbrotSrc)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	cf, err := Tier2Compile(fn)
	if err != nil {
		b.Fatalf("Tier2Compile error: %v", err)
	}
	b.Cleanup(func() { cf.Code.Free() })
	args := []runtime.Value{runtime.IntValue(10)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}
