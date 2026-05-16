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
    result := 0
    for r := 1; r <= reps; r++ {
        result = fib_iter(n)
    }
    return result
}

N := 70
REPS := 1000000

t0 := time.now()
result := bench_fib_iter(N, REPS)
elapsed := time.since(t0)

print(string.format("fibonacci_iterative(%d) x %d reps", N, REPS))
print(string.format("fib(%d) = %d", N, result))
print(string.format("Time: %.3fs", elapsed))
