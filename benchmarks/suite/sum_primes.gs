// Benchmark: Sum of Primes (trial division)
// Tests: integer arithmetic, nested loops, conditional branching
// A balanced mix of computation patterns

func is_prime(n) {
    if n < 2 { return false }
    if n < 4 { return true }
    if n % 2 == 0 { return false }
    if n % 3 == 0 { return false }
    i := 5
    for i * i <= n {
        if n % i == 0 { return false }
        if n % (i + 2) == 0 { return false }
        i = i + 6
    }
    return true
}

N := 100000

t0 := time.now()
sum := 0
count := 0
for i := 2; i <= N; i++ {
    if is_prime(i) {
        sum = sum + i
        count = count + 1
    }
}
elapsed := time.since(t0)

print(string.format("sum_primes(%d): %d primes, sum=%d", N, count, sum))
print(string.format("Time: %.3fs", elapsed))
