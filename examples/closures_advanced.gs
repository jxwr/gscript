// closures_advanced.gs - Advanced closure patterns in GScript
// Demonstrates: memoization, currying, composition, once, throttle, pipeline

print("=== Advanced Closure Patterns ===")
print()

// -------------------------------------------------------
// 1. Memoization - cache expensive function results
// -------------------------------------------------------
print("--- Memoization ---")

func memoize(f) {
    cache := {}
    return func(x) {
        if cache[x] != nil {
            return cache[x]
        }
        result := f(x)
        cache[x] = result
        return result
    }
}

// Slow recursive fibonacci
func slowFib(n) {
    if n < 2 { return n }
    return slowFib(n - 1) + slowFib(n - 2)
}

// Memoized version (note: memoizes only the wrapper, inner calls still recurse)
fastFib := memoize(func(n) {
    if n < 2 { return n }
    return fastFib(n - 1) + fastFib(n - 2)
})

// Show it works
for i := 0; i <= 15; i++ {
    print(string.format("  fib(%d) = %d", i, fastFib(i)))
}
print()

// -------------------------------------------------------
// 2. Currying / Partial Application
// -------------------------------------------------------
print("--- Currying ---")

// Curry a two-argument function
func curry(f) {
    return func(a) {
        return func(b) {
            return f(a, b)
        }
    }
}

add := curry(func(a, b) { return a + b })
add5 := add(5)
add10 := add(10)
print("  add5(3) =", add5(3))
print("  add5(7) =", add5(7))
print("  add10(20) =", add10(20))

mul := curry(func(a, b) { return a * b })
double := mul(2)
triple := mul(3)
print("  double(4) =", double(4))
print("  triple(4) =", triple(4))
print()

// Partial application (more general)
print("--- Partial Application ---")

func partial(f, a) {
    return func(b) {
        return f(a, b)
    }
}

pow2 := partial(math.pow, 2)
print("  pow2(10) =", pow2(10))
print("  pow2(8) =", pow2(8))
print()

// -------------------------------------------------------
// 3. Function Composition
// -------------------------------------------------------
print("--- Function Composition ---")

// Compose two functions: (f . g)(x) = f(g(x))
func compose2(f, g) {
    return func(x) {
        return f(g(x))
    }
}

negate := func(x) { return -x }
inc := func(x) { return x + 1 }
square := func(x) { return x * x }

negateAndInc := compose2(inc, negate)
print("  negateAndInc(5) =", negateAndInc(5))

squareThenNegate := compose2(negate, square)
print("  squareThenNegate(4) =", squareThenNegate(4))
print()

// -------------------------------------------------------
// 4. Once - call a function only once
// -------------------------------------------------------
print("--- Once (run only once) ---")

func once(f) {
    called := false
    result := nil
    return func() {
        if !called {
            result = f()
            called = true
        }
        return result
    }
}

callCount := 0
expensive := once(func() {
    callCount = callCount + 1
    print("  (computing expensive result...)")
    return 42
})

print("  First call:", expensive())
print("  Second call:", expensive())
print("  Third call:", expensive())
print("  expensive() was actually computed", callCount, "time(s)")
print()

// -------------------------------------------------------
// 5. Throttle simulation (limits call frequency with counter)
// -------------------------------------------------------
print("--- Throttle (counter-based) ---")

// Allow calling f at most once every N calls
func throttle(f, interval) {
    lastCall := -interval
    callNum := 0
    return func(x) {
        callNum = callNum + 1
        if callNum - lastCall >= interval {
            lastCall = callNum
            return f(x)
        }
        return nil
    }
}

throttled := throttle(func(x) {
    return "processed: " .. tostring(x)
}, 3)

for i := 1; i <= 10; i++ {
    result := throttled(i)
    if result != nil {
        print("  call #" .. i .. ": " .. result)
    } else {
        print("  call #" .. i .. ": (throttled)")
    }
}
print()

// -------------------------------------------------------
// 6. Pipeline pattern - chain transformations
// -------------------------------------------------------
print("--- Pipeline ---")

func pipeline(value, fns) {
    result := value
    for _, f := range fns {
        result = f(result)
    }
    return result
}

// Transform a number through several steps
result := pipeline(5, {
    func(x) { return x * 2 },       // 10
    func(x) { return x + 3 },       // 13
    func(x) { return x * x },       // 169
    func(x) { return tostring(x) .. "!" }  // "169!"
})
print("  pipeline(5, [*2, +3, ^2, tostring..!]) =", result)

// Pipeline with string transformations
result = pipeline("  Hello, World!  ", {
    func(s) { return string.gsub(s, "^%s+", "") },    // trim leading
    func(s) { return string.gsub(s, "%s+$", "") },    // trim trailing
    func(s) { return string.upper(s) },
    func(s) { return "<<" .. s .. ">>" }
})
print("  string pipeline =", result)
print()

// -------------------------------------------------------
// 7. Accumulator - closure over mutable state
// -------------------------------------------------------
print("--- Accumulator ---")

func makeAccumulator(initial) {
    sum := initial
    return {
        add: func(n) { sum = sum + n; return sum },
        sub: func(n) { sum = sum - n; return sum },
        get: func() { return sum },
        reset: func() { sum = initial; return sum }
    }
}

acc := makeAccumulator(0)
print("  add(10):", acc.add(10))
print("  add(20):", acc.add(20))
print("  add(5):", acc.add(5))
print("  sub(3):", acc.sub(3))
print("  get():", acc.get())
print("  reset():", acc.reset())
print()

// -------------------------------------------------------
// 8. Iterator factory using closures
// -------------------------------------------------------
print("--- Iterator Factory ---")

func rangeIter(start, stop, step) {
    i := start
    return func() {
        if i >= stop { return nil }
        val := i
        i = i + step
        return val
    }
}

print("  Counting by 3 from 0 to 15:")
items := {}
for v := range rangeIter(0, 15, 3) {
    table.insert(items, tostring(v))
}
print("  " .. table.concat(items, ", "))
