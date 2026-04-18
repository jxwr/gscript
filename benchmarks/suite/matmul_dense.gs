// Benchmark: Matrix multiplication using matrix.dense + matrix.getf/setf
// R42 + R43 DenseMatrix path: shared flat backing + JIT intrinsic direct access.
// Target: close LuaJIT gap on matmul from 5.5× (nested) to <2.5×.

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
            for k := 0; k < n; k++ {
                sum = sum + matrix.getf(a, i, k) * matrix.getf(b, k, j)
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

// Sanity check: spot read of c[0][0].
print(string.format("matmul_dense(%d) c[0][0] = %.6f", N, matrix.getf(c, 0, 0)))
print(string.format("Time: %.3fs", elapsed))
