// Benchmark: matmul_dense with source-level 2-way sum-splitting.
// R48 pre-flight. Goal: test whether breaking the serial reduction
// chain at the source level produces measurable speedup vs
// matmul_dense's single accumulator. Informs whether an IR-level
// unroll-and-jam pass is worth building.

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
            sum0 := 0.0
            sum1 := 0.0
            k := 0
            // 2-way unrolled reduction; two independent Phi chains.
            for k + 1 < n {
                sum0 = sum0 + matrix.getf(a, i, k)     * matrix.getf(b, k,     j)
                sum1 = sum1 + matrix.getf(a, i, k + 1) * matrix.getf(b, k + 1, j)
                k = k + 2
            }
            // tail for odd n
            sum := sum0 + sum1
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

print(string.format("matmul_dense_split2(%d) c[0][0] = %.6f", N, matrix.getf(c, 0, 0)))
print(string.format("Time: %.3fs", elapsed))
