// Benchmark: Matrix Multiplication
// Tests: nested loops, 2D table-of-tables access, floating-point arithmetic

func matgen(n) {
    m := {}
    for i := 0; i < n; i++ {
        row := {}
        for j := 0; j < n; j++ {
            row[j] = (i * n + j + 1.0) / (n * n)
        }
        m[i] = row
    }
    return m
}

func matmul(a, b, n) {
    c := {}
    for i := 0; i < n; i++ {
        row := {}
        ai := a[i]
        for j := 0; j < n; j++ {
            sum := 0.0
            for k := 0; k < n; k++ {
                sum = sum + ai[k] * b[k][j]
            }
            row[j] = sum
        }
        c[i] = row
    }
    return c
}

N := 300

t0 := time.now()

a := matgen(N)
b := matgen(N)
c := matmul(a, b, N)

// Checksum: center element
half := math.floor(N / 2)
result := c[half][half]
elapsed := time.since(t0)

print(string.format("matmul(%d) center = %.6f", N, result))
print(string.format("Time: %.3fs", elapsed))
