// Benchmark: Coroutine Performance
// Tests: coroutine create/resume/yield overhead

// Test 1: Simple yield ping-pong
func test_yield_loop(n) {
    co := coroutine.create(func() {
        for i := 1; i <= n; i++ {
            coroutine.yield(i)
        }
        return n
    })
    sum := 0
    for i := 1; i <= n; i++ {
        ok, val := coroutine.resume(co)
        sum = sum + val
    }
    return sum
}

// Test 2: Many short-lived coroutines
func test_create_resume(n) {
    total := 0
    for i := 1; i <= n; i++ {
        co := coroutine.create(func() {
            return i * 2
        })
        ok, val := coroutine.resume(co)
        total = total + val
    }
    return total
}

// Test 3: Coroutine as generator (wrap pattern)
func test_generator(n) {
    gen := coroutine.wrap(func() {
        for i := 1; i <= n; i++ {
            coroutine.yield(i * i)
        }
    })
    sum := 0
    for i := 1; i <= n; i++ {
        val := gen()
        if val == nil { break }
        sum = sum + val
    }
    return sum
}

N1 := 100000
N2 := 50000
N3 := 100000

t0 := time.now()
r1 := test_yield_loop(N1)
t1 := time.since(t0)

t0 = time.now()
r2 := test_create_resume(N2)
t2 := time.since(t0)

t0 = time.now()
r3 := test_generator(N3)
t3 := time.since(t0)

total := t1 + t2 + t3

print(string.format("yield_loop(%d):    %.3fs (sum=%d)", N1, t1, r1))
print(string.format("create_resume(%d): %.3fs (sum=%d)", N2, t2, r2))
print(string.format("generator(%d):     %.3fs (sum=%d)", N3, t3, r3))
print(string.format("Time: %.3fs", total))
