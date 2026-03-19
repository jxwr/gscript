// Benchmark: Mutual Recursion (Hofstadter sequences)
// Tests: non-self recursive function calls, method JIT call-exit overhead
// Female/Male Hofstadter sequences: F(n) = n - M(F(n-1)), M(n) = n - F(M(n-1))

func F(n) {
    if n == 0 { return 1 }
    return n - M(F(n - 1))
}

func M(n) {
    if n == 0 { return 0 }
    return n - F(M(n - 1))
}

N := 25
REPS := 1000

t0 := time.now()
result := 0
for rep := 1; rep <= REPS; rep++ {
    result = F(N)
}
elapsed := time.since(t0)

print(string.format("F(%d) = %d (%d reps)", N, result, REPS))
print(string.format("Time: %.3fs", elapsed))
