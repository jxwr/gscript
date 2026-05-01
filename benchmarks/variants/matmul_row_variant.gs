// Structural variant: table-of-tables matrix multiplication with a different
// size and row construction shape.

func matgen_attached_rows(n, scale) {
    m := {}
    for i := 0; i < n; i++ {
        m[i] = {}
    }
    for i := 0; i < n; i++ {
        row := m[i]
        base := i * n
        for j := 0; j < n; j++ {
            row[j] = (base + j + scale) / (n * n)
        }
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

N := 360
REPS := 4
t0 := time.now()

half := math.floor(N / 2)
checksum := 0.0
for rep := 1; rep <= REPS; rep++ {
    a := matgen_attached_rows(N, rep + 2.0)
    b := matgen_attached_rows(N, rep + 6.0)
    c := matmul(a, b, N)
    checksum = checksum + c[0][0] + c[half][half] + c[N - 1][N - 1]
}
elapsed := time.since(t0)

print(string.format("matmul_row_variant(%d) x %d checksum = %.6f", N, REPS, checksum))
print(string.format("Time: %.3fs", elapsed))
