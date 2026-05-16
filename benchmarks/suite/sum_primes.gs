// Benchmark: Sum of Primes (trial division)
// Tests: integer arithmetic, nested loops, conditional branching
// A balanced mix of computation patterns

func is_prime(n) {
    if n < 2 { return 0 }
    if n < 4 { return 1 }
    if n % 2 == 0 { return 0 }
    if n % 3 == 0 { return 0 }
    i := 5
    for i * i <= n {
        if n % i == 0 { return 0 }
        if n % (i + 2) == 0 { return 0 }
        i = i + 6
    }
    return 1
}

func sum_primes(limit) {
    sum := 0
    count := 0
    for i := 2; i <= limit; i++ {
        if is_prime(i) != 0 {
            sum = sum + i
            count = count + 1
        }
    }
    return {sum: sum, count: count}
}

N := 100000
REPS := 20

warm := sum_primes(N)

t0 := time.now()
result := {sum: 0, count: 0}
for rep := 1; rep <= REPS; rep++ {
    result = sum_primes(N)
}
elapsed := time.since(t0)

print(string.format("sum_primes(%d)x%d: %d primes, sum=%d", N, REPS, result.count, result.sum))
print(string.format("Time: %.3fs", elapsed))
