// R54: spectral_norm ported to DenseMatrix vectors.
// Each length-N vector is stored as matrix.dense(N, 1) — 1 column of
// flat float64 backing. v[j] → matrix.getf(v, j, 0); v[j] = x →
// matrix.setf(v, j, 0, x). No NaN-box / shape-IC overhead; direct
// LDR/STR into flat backing.

func A(i, j) {
    return 1.0 / ((i + j) * (i + j + 1) / 2 + i + 1)
}

func multiplyAv(n, v, av) {
    for i := 0; i < n; i++ {
        sum := 0.0
        for j := 0; j < n; j++ {
            sum = sum + A(i, j) * matrix.getf(v, j, 0)
        }
        matrix.setf(av, i, 0, sum)
    }
}

func multiplyAtv(n, v, atv) {
    for i := 0; i < n; i++ {
        sum := 0.0
        for j := 0; j < n; j++ {
            sum = sum + A(j, i) * matrix.getf(v, j, 0)
        }
        matrix.setf(atv, i, 0, sum)
    }
}

func multiplyAtAv(n, v, u, atav) {
    for i := 0; i < n; i++ { matrix.setf(u, i, 0, 0.0) }
    multiplyAv(n, v, u)
    multiplyAtv(n, u, atav)
}

N := 1500

WARM_N := 64
warm_u := matrix.dense(WARM_N, 1)
warm_v := matrix.dense(WARM_N, 1)
warm_tmp := matrix.dense(WARM_N, 1)
for i := 0; i < WARM_N; i++ {
    matrix.setf(warm_u, i, 0, 1.0)
    matrix.setf(warm_v, i, 0, 0.0)
}
multiplyAtAv(WARM_N, warm_u, warm_tmp, warm_v)
multiplyAtAv(WARM_N, warm_v, warm_tmp, warm_u)

warm_big_u := matrix.dense(N, 1)
warm_big_v := matrix.dense(N, 1)
warm_big_tmp := matrix.dense(N, 1)
for i := 0; i < N; i++ {
    matrix.setf(warm_big_u, i, 0, 1.0)
    matrix.setf(warm_big_v, i, 0, 0.0)
}
multiplyAtAv(N, warm_big_u, warm_big_tmp, warm_big_v)

t0 := time.now()

u := matrix.dense(N, 1)
v := matrix.dense(N, 1)
tmp := matrix.dense(N, 1)   // scratch for multiplyAtAv
for i := 0; i < N; i++ {
    matrix.setf(u, i, 0, 1.0)
    matrix.setf(v, i, 0, 0.0)
}

for iter := 0; iter < 10; iter++ {
    multiplyAtAv(N, u, tmp, v)
    multiplyAtAv(N, v, tmp, u)
}

vBv := 0.0
vv := 0.0
for i := 0; i < N; i++ {
    ui := matrix.getf(u, i, 0)
    vi := matrix.getf(v, i, 0)
    vBv = vBv + ui * vi
    vv = vv + vi * vi
}

result := math.sqrt(vBv / vv)
elapsed := time.since(t0)

print(string.format("spectral_norm_dense(%d) = %.9f", N, result))
print(string.format("Time: %.3fs", elapsed))
