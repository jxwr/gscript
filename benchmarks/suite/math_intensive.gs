// Benchmark: Math Intensive
// Tests: pure numeric computation (float and int) without table access
// Stresses arithmetic operations, math.sqrt, loop throughput

// Test 1: Leibniz formula for pi (float arithmetic)
func leibniz_pi(n) {
    sum := 0.0
    sign := 1.0
    for i := 0; i < n; i++ {
        sum = sum + sign / (2.0 * i + 1.0)
        sign = -sign
    }
    return sum * 4.0
}

// Test 2: Integer collatz sequence lengths
func collatz_total(limit) {
    total_steps := 0
    for n := 2; n <= limit; n++ {
        x := n
        steps := 0
        for x != 1 {
            if x % 2 == 0 {
                x = x / 2
            } else {
                x = 3 * x + 1
            }
            steps = steps + 1
        }
        total_steps = total_steps + steps
    }
    return total_steps
}

// Test 3: Distance calculations (float + sqrt)
func distance_sum(n) {
    total := 0.0
    for i := 1; i <= n; i++ {
        x := 1.0 * i / n
        y := 1.0 - x
        z := x * y
        total = total + math.sqrt(x * x + y * y + z * z)
    }
    return total
}

// Test 4: GCD computation (integer division + modulo)
func gcd(a, b) {
    for b != 0 {
        t := b
        b = a % b
        a = t
    }
    return a
}

func gcd_bench(n) {
    total := 0
    for i := 1; i <= n; i++ {
        for j := 1; j <= 100; j++ {
            total = total + gcd(i * 7 + 13, j * 11 + 3)
        }
    }
    return total
}

N_PI := 5000000
N_COLLATZ := 50000
N_DIST := 1000000
N_GCD := 10000

t0 := time.now()
r1 := leibniz_pi(N_PI)
t1 := time.since(t0)

t0 = time.now()
r2 := collatz_total(N_COLLATZ)
t2 := time.since(t0)

t0 = time.now()
r3 := distance_sum(N_DIST)
t3 := time.since(t0)

t0 = time.now()
r4 := gcd_bench(N_GCD)
t4 := time.since(t0)

total := t1 + t2 + t3 + t4

print(string.format("leibniz_pi(%d):  %.3fs (pi=%.10f)", N_PI, t1, r1))
print(string.format("collatz(%d):     %.3fs (total_steps=%d)", N_COLLATZ, t2, r2))
print(string.format("distance(%d):    %.3fs (sum=%.6f)", N_DIST, t3, r3))
print(string.format("gcd(%d):         %.3fs (total=%d)", N_GCD, t4, r4))
print(string.format("Time: %.3fs", total))
