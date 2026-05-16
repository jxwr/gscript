// Benchmark: Fibonacci (Recursive)
// Tests: recursive function call overhead, integer arithmetic
// Expected: fib(35) = 9227465

func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

N := 35
REPS := 36

warm := fib(N)

t0 := time.now()
result := 0
for rep := 1; rep <= REPS; rep++ {
    result = fib(N)
}
elapsed := time.since(t0)

print(string.format("fib(%d) = %d", N, result))
print(string.format("Time: %.3fs (%d reps)", elapsed, REPS))
