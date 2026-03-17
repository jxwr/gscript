// Benchmark: Sieve of Eratosthenes
// Tests: array access, integer arithmetic, conditional branching
// Expected: 78498 primes up to 1,000,000

func sieve(n) {
    is_prime := {}
    for i := 2; i <= n; i++ {
        is_prime[i] = true
    }
    i := 2
    for i * i <= n {
        if is_prime[i] {
            j := i * i
            for j <= n {
                is_prime[j] = false
                j = j + i
            }
        }
        i = i + 1
    }
    count := 0
    for i := 2; i <= n; i++ {
        if is_prime[i] { count = count + 1 }
    }
    return count
}

N := 1000000
REPS := 3

t0 := time.now()
result := 0
for r := 1; r <= REPS; r++ {
    result = sieve(N)
}
elapsed := time.since(t0)

print(string.format("sieve(%d) = %d primes", N, result))
print(string.format("Time: %.3fs (%d reps)", elapsed, REPS))
