// Benchmark: Fibonacci (Recursive)
// Tests: recursive function call overhead, integer arithmetic
// Expected: fib(35) = 9227465

func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

t0 := time.now()
result := fib(35)
elapsed := time.since(t0)

print(string.format("fib(35) = %d", result))
print(string.format("Time: %.3fs", elapsed))
