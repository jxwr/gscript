//go:build darwin && arm64

package methodjit

import (
	"fmt"
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

// TestTier2_MandelbrotDumpIR dumps the mandelbrot IR after type specialization
// for debugging.
func TestTier2_MandelbrotDumpIR(t *testing.T) {
	proto := compileFunction(t, mandelbrotSrc)
	fn := BuildGraph(proto)
	t.Log("=== Before TypeSpec ===")
	t.Log(Print(fn))

	fn, _ = TypeSpecializePass(fn)
	t.Log("=== After TypeSpec ===")
	t.Log(Print(fn))

	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	t.Log("=== After ConstProp+DCE ===")
	t.Log(Print(fn))
}

// TestTier2_MandelbrotSizes verifies correctness of Tier 2 compiled mandelbrot
// at multiple sizes. Compares Tier 2 native output against IR interpreter.
func TestTier2_MandelbrotSizes(t *testing.T) {
	for _, size := range []int{3, 10, 50, 100} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			proto := compileFunction(t, mandelbrotSrc)

			// IR interpreter result (correctness oracle).
			fn1 := BuildGraph(proto)
			fn1, _ = TypeSpecializePass(fn1)
			fn1, _ = ConstPropPass(fn1)
			fn1, _ = DCEPass(fn1)
			expected, err := Interpret(fn1, []runtime.Value{runtime.IntValue(int64(size))})
			if err != nil {
				t.Fatalf("IR interpret error: %v", err)
			}

			// Tier 2 native result.
			fn2 := BuildGraph(proto)
			fn2, _ = TypeSpecializePass(fn2)
			fn2, _ = ConstPropPass(fn2)
			fn2, _ = DCEPass(fn2)
			alloc := AllocateRegisters(fn2)
			cf, err := Compile(fn2, alloc)
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}
			defer cf.Code.Free()

			result, err := cf.Execute([]runtime.Value{runtime.IntValue(int64(size))})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}

			// Also compare against VM.
			vmResult := runVM(t, mandelbrotSrc, []runtime.Value{runtime.IntValue(int64(size))})

			t.Logf("size=%d: Tier2=%v, IR=%v, VM=%v", size, result, expected, vmResult)

			if len(result) == 0 || len(expected) == 0 {
				t.Fatalf("size=%d: empty result: Tier2=%v, IR=%v", size, result, expected)
			}

			// Compare Tier 2 vs IR interpreter.
			if uint64(result[0]) != uint64(expected[0]) {
				t.Errorf("size=%d: Tier2 vs IR MISMATCH: Tier2=%v, IR=%v", size, result[0], expected[0])
			}
			// Compare Tier 2 vs VM.
			if len(vmResult) > 0 && uint64(result[0]) != uint64(vmResult[0]) {
				t.Errorf("size=%d: Tier2 vs VM MISMATCH: Tier2=%v, VM=%v", size, result[0], vmResult[0])
			}
		})
	}
}

// BenchmarkTier2Reg_Mandelbrot100 benchmarks Tier 2 mandelbrot at size=100
// to check scaling behavior.
func BenchmarkTier2Reg_Mandelbrot100(b *testing.B) {
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
	args := []runtime.Value{runtime.IntValue(100)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cf.Execute(args)
	}
}
