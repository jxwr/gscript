// Benchmark: Closure Creation and Invocation
// Tests: closure allocation, upvalue capture, higher-order functions

// Test 1: Create and call closures in a loop
func test_closure_call() {
    func make_adder(x) {
        return func(y) { return x + y }
    }
    sum := 0
    for i := 1; i <= 100000; i++ {
        add5 := make_adder(5)
        sum = sum + add5(i)
    }
    return sum
}

// Test 2: Accumulator pattern (closure with mutable upvalue)
func test_accumulator() {
    func make_counter() {
        count := 0
        return func() {
            count = count + 1
            return count
        }
    }
    total := 0
    counter := make_counter()
    for i := 1; i <= 1000000; i++ {
        total = total + counter()
    }
    return total
}

// Test 3: Higher-order function (map pattern)
func test_map() {
    arr := {}
    for i := 1; i <= 1000; i++ {
        arr[i] = i
    }
    func map_array(a, f) {
        result := {}
        n := #a
        for i := 1; i <= n; i++ {
            result[i] = f(a[i])
        }
        return result
    }
    total := 0
    for rep := 1; rep <= 100; rep++ {
        mapped := map_array(arr, func(x) { return x * 2 + 1 })
        total = total + mapped[500]
    }
    return total
}

t0 := time.now()
r1 := test_closure_call()
t1 := time.since(t0)

t0 = time.now()
r2 := test_accumulator()
t2 := time.since(t0)

t0 = time.now()
r3 := test_map()
t3 := time.since(t0)

total := t1 + t2 + t3
print(string.format("closure_call:  %.3fs (result=%d)", t1, r1))
print(string.format("accumulator:   %.3fs (result=%d)", t2, r2))
print(string.format("map_array:     %.3fs (result=%d)", t3, r3))
print(string.format("Time: %.3fs", total))
