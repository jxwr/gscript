// Benchmark: Spectral Norm
// Tests: floating-point computation, array indexing, nested loops, function calls
// Computes spectral norm of an infinite matrix using power iteration

func A(i, j) {
    return 1.0 / ((i + j) * (i + j + 1) / 2 + i + 1)
}

func multiplyAv(n, v, av) {
    for i := 0; i < n; i++ {
        sum := 0.0
        for j := 0; j < n; j++ {
            sum = sum + A(i, j) * v[j]
        }
        av[i] = sum
    }
}

func multiplyAtv(n, v, atv) {
    for i := 0; i < n; i++ {
        sum := 0.0
        for j := 0; j < n; j++ {
            sum = sum + A(j, i) * v[j]
        }
        atv[i] = sum
    }
}

func multiplyAtAv(n, v, atav) {
    u := {}
    for i := 0; i < n; i++ { u[i] = 0.0 }
    multiplyAv(n, v, u)
    multiplyAtv(n, u, atav)
}

N := 500

t0 := time.now()

u := {}
v := {}
for i := 0; i < N; i++ {
    u[i] = 1.0
    v[i] = 0.0
}

for iter := 0; iter < 10; iter++ {
    multiplyAtAv(N, u, v)
    multiplyAtAv(N, v, u)
}

vBv := 0.0
vv := 0.0
for i := 0; i < N; i++ {
    vBv = vBv + u[i] * v[i]
    vv = vv + v[i] * v[i]
}

result := math.sqrt(vBv / vv)
elapsed := time.since(t0)

print(string.format("spectral_norm(%d) = %.9f", N, result))
print(string.format("Time: %.3fs", elapsed))
