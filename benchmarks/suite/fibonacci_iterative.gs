// Benchmark: Iterative Fibonacci
// Tests: tight loop performance, integer arithmetic, variable swap pattern
// No recursion -- purely tests loop + arithmetic throughput

func fib_iter(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}

// Run many iterations of fib to accumulate measurable time
func bench_fib_iter(n, reps) {
    checksum := 0
    for r := 1; r <= reps; r++ {
        m := n - (r % 8)
        checksum = checksum + (fib_iter(m) % 997)
        if checksum >= 1000000000 {
            checksum = checksum - 1000000000
        }
    }
    return checksum
}

N := 45
REPS := 1000000

warm := bench_fib_iter(N, 1000)

t0 := time.now()
result := bench_fib_iter(N, REPS)
elapsed := time.since(t0)

print(string.format("fibonacci_iterative(%d) x %d reps", N, REPS))
print(string.format("fib(%d) = %d", N, result))
print(string.format("Time: %.3fs", elapsed))
