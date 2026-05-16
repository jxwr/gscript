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

N := 320
REPS := 2

t0 := time.now()

c := {}
for rep := 1; rep <= REPS; rep++ {
    a := matgen(N)
    b := matgen(N)
    c = matmul(a, b, N)
}

// Checksum: the structural matmul kernel returns a dense matrix, so use the
// matrix API instead of a float-keyed nested table lookup.
result := matrix.getf(c, 0, 0)
elapsed := time.since(t0)
if result <= 0.0 {
    print("matmul invalid: non-positive checksum")
    elapsed = 999.0
}

print(string.format("matmul(%d) x %d center = %.6f", N, REPS, result))
print(string.format("Time: %.3fs", elapsed))
