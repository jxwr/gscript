// Benchmark: Recursive Fibonacci
// Tests: self-recursive function calls, call-exit overhead
// Expected: With function-entry traces, should see 3-10x speedup

func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

N := 35
REPS := 10

result := 0
t0 := time.now()
for rep := 1; rep <= REPS; rep++ {
    result = fib(N)
}
elapsed := time.since(t0)

print(string.format("fib(%d) = %d", N, result))
print(string.format("Time: %.3fs (%d reps)", elapsed, REPS))
