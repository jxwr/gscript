// R63: matmul_dense with transposed-b precomputation.
// Original: c[i][j] = sum_k a[i][k] * b[k][j]
// Problem: b[k][j] strides across rows (stride-N access in flat backing).
// Fix: transpose b into bT once, so bT[j][k] is stride-1 in memory.
// Then inner loop reads a[i][k] and bT[j][k] — both stride-1.

func matgen(n) {
    m := matrix.dense(n, n)
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            matrix.setf(m, i, j, (i * n + j + 1.0) / (n * n))
        }
    }
    return m
}

func transpose(src, dst, n) {
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            matrix.setf(dst, j, i, matrix.getf(src, i, j))
        }
    }
}

func matmul(a, bT, c, n) {
    for i := 0; i < n; i++ {
        for j := 0; j < n; j++ {
            sum := 0.0
            for k := 0; k < n; k++ {
                sum = sum + matrix.getf(a, i, k) * matrix.getf(bT, j, k)
            }
            matrix.setf(c, i, j, sum)
        }
    }
}

N := 300

t0 := time.now()

a := matgen(N)
b := matgen(N)
bT := matrix.dense(N, N)
transpose(b, bT, N)
c := matrix.dense(N, N)
matmul(a, bT, c, N)

elapsed := time.since(t0)

print(string.format("matmul_dense_tb(%d) c[0][0] = %.6f", N, matrix.getf(c, 0, 0)))
print(string.format("Time: %.3fs", elapsed))
