// Benchmark: Mandelbrot Set
// Tests: floating-point loops, conditional branching, nested iteration
// Counts pixels in the Mandelbrot set for an NxN grid

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

N := 1000

t0 := time.now()
result := mandelbrot(N)
elapsed := time.since(t0)

print(string.format("mandelbrot(%d) = %d pixels in set", N, result))
print(string.format("Time: %.3fs", elapsed))
