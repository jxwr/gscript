// R48 control: 2-way unroll WITHOUT sum splitting.
// Same serial dependency chain as R45 baseline, just unrolled.
// Isolates whether the regression in matmul_dense_split2 comes
// from the sum-split itself or from doubling the getf call count.

func matgen(n) {
    m := matrix.dense(n, n)
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            matrix.setf(m, i, j, (i * n + j + 1.0) / (n * n))
        }
    }
    return m
}

func matmul(a, b, n) {
    c := matrix.dense(n, n)
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            sum := 0.0
            k := 0
            for k + 1 < n {
                sum = sum + matrix.getf(a, i, k)     * matrix.getf(b, k,     j)
                sum = sum + matrix.getf(a, i, k + 1) * matrix.getf(b, k + 1, j)
                k = k + 2
            }
            for k < n {
                sum = sum + matrix.getf(a, i, k) * matrix.getf(b, k, j)
                k = k + 1
            }
            matrix.setf(c, i, j, sum)
        }
    }
    return c
}

N := 300
t0 := time.now()
a := matgen(N)
b := matgen(N)
c := matmul(a, b, N)
elapsed := time.since(t0)
print(string.format("matmul_dense_unroll2(%d) c[0][0] = %.6f", N, matrix.getf(c, 0, 0)))
print(string.format("Time: %.3fs", elapsed))
