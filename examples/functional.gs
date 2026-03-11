// functional.gs - Functional programming patterns in GScript
// Demonstrates: map, filter, reduce, compose, curry, lazy evaluation, pipeline, memoize

print("=== Functional Programming Patterns ===")
print()

// -------------------------------------------------------
// 1. Map - transform every element of a table
// -------------------------------------------------------
print("--- Map ---")

func map(tbl, f) {
    result := {}
    for i := 1; i <= #tbl; i++ {
        table.insert(result, f(tbl[i]))
    }
    return result
}

nums := {1, 2, 3, 4, 5}
doubled := map(nums, func(x) { return x * 2 })
print("  nums:", table.concat(map(nums, tostring), ", "))
print("  doubled:", table.concat(map(doubled, tostring), ", "))

squared := map(nums, func(x) { return x * x })
print("  squared:", table.concat(map(squared, tostring), ", "))
print()

// -------------------------------------------------------
// 2. Filter - keep only elements that match a predicate
// -------------------------------------------------------
print("--- Filter ---")

func filter(tbl, pred) {
    result := {}
    for i := 1; i <= #tbl; i++ {
        if pred(tbl[i]) {
            table.insert(result, tbl[i])
        }
    }
    return result
}

evens := filter(nums, func(x) { return x % 2 == 0 })
odds := filter(nums, func(x) { return x % 2 != 0 })
print("  evens:", table.concat(map(evens, tostring), ", "))
print("  odds:", table.concat(map(odds, tostring), ", "))

// Filter strings by length
words := {"hi", "hello", "yo", "goodbye", "ok", "wonderful"}
longWords := filter(words, func(w) { return #w > 3 })
print("  long words:", table.concat(longWords, ", "))
print()

// -------------------------------------------------------
// 3. Reduce - fold a table into a single value
// -------------------------------------------------------
print("--- Reduce ---")

func reduce(tbl, f, init) {
    acc := init
    for i := 1; i <= #tbl; i++ {
        acc = f(acc, tbl[i])
    }
    return acc
}

sum := reduce(nums, func(a, b) { return a + b }, 0)
product := reduce(nums, func(a, b) { return a * b }, 1)
print("  sum of {1..5}:", sum)
print("  product of {1..5}:", product)

// Find maximum using reduce
maximum := reduce(nums, func(a, b) {
    if b > a { return b }
    return a
}, nums[1])
print("  max of {1..5}:", maximum)

// Concatenate strings with reduce
joined := reduce({"Hello", " ", "World", "!"}, func(a, b) { return a .. b }, "")
print("  joined:", joined)
print()

// -------------------------------------------------------
// 4. Compose - chain functions together
// -------------------------------------------------------
print("--- Compose ---")

// Compose two functions: compose(f, g)(x) = f(g(x))
func compose(f, g) {
    return func(x) {
        return f(g(x))
    }
}

// Compose many functions: composeN(f1, f2, f3)(x) = f1(f2(f3(x)))
func composeN(...) {
    fns := {...}
    return func(x) {
        result := x
        // Apply from right to left
        for i := #fns; i >= 1; i-- {
            result = fns[i](result)
        }
        return result
    }
}

inc := func(x) { return x + 1 }
double := func(x) { return x * 2 }
square := func(x) { return x * x }

incThenDouble := compose(double, inc)
print("  incThenDouble(3) =", incThenDouble(3))   // (3+1)*2 = 8

doubleThenInc := compose(inc, double)
print("  doubleThenInc(3) =", doubleThenInc(3))   // 3*2+1 = 7

transform := composeN(tostring, inc, square, double)
print("  tostring(inc(square(double(3)))) =", transform(3))  // 3*2=6, 36, 37, "37"
print()

// -------------------------------------------------------
// 5. Curry - convert multi-arg function to chain of single-arg functions
// -------------------------------------------------------
print("--- Curry ---")

// Curry a two-argument function
func curry2(f) {
    return func(a) {
        return func(b) {
            return f(a, b)
        }
    }
}

// Curry a three-argument function
func curry3(f) {
    return func(a) {
        return func(b) {
            return func(c) {
                return f(a, b, c)
            }
        }
    }
}

add := curry2(func(a, b) { return a + b })
add5 := add(5)
add10 := add(10)
print("  add5(3) =", add5(3))
print("  add10(20) =", add10(20))

// Curried string formatter
formatName := curry3(func(title, first, last) {
    return title .. " " .. first .. " " .. last
})
mrFormatter := formatName("Mr.")
print("  mrFormatter('John')('Doe') =", mrFormatter("John")("Doe"))
print("  formatName('Dr.')('Jane')('Smith') =", formatName("Dr.")("Jane")("Smith"))
print()

// -------------------------------------------------------
// 6. Lazy evaluation with closures
// -------------------------------------------------------
print("--- Lazy Evaluation ---")

// A lazy value is a closure that computes its value on first access
func lazy(f) {
    computed := false
    value := nil
    return func() {
        if !computed {
            value = f()
            computed = true
            print("    (computed)")
        }
        return value
    }
}

expensiveCalc := lazy(func() {
    // Simulate expensive computation
    result := 0
    for i := 1; i <= 100; i++ {
        result = result + i
    }
    return result
})

print("  Before first access...")
print("  First access:", expensiveCalc())
print("  Second access:", expensiveCalc())
print("  Third access:", expensiveCalc())
print()

// Lazy sequence - generates values only when needed
func lazyRange(start, stop) {
    i := start
    return func() {
        if i > stop { return nil }
        val := i
        i = i + 1
        return val
    }
}

print("  Lazy range 1..5:")
gen := lazyRange(1, 5)
for v := range gen {
    print("    got:", v)
}
print()

// -------------------------------------------------------
// 7. Pipeline operator pattern
// -------------------------------------------------------
print("--- Pipeline ---")

// Pipeline: chain operations on data in a readable way
func pipeline(value, ...) {
    fns := {...}
    result := value
    for i := 1; i <= #fns; i++ {
        result = fns[i](result)
    }
    return result
}

// Process a list of numbers through a pipeline
data := {1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

result := pipeline(data,
    func(t) { return filter(t, func(x) { return x % 2 == 0 }) },  // keep evens
    func(t) { return map(t, func(x) { return x * x }) },           // square them
    func(t) { return reduce(t, func(a, b) { return a + b }, 0) }   // sum them
)
print("  Sum of squares of evens in {1..10}:", result)

// String processing pipeline
text := "  hello world  "
result2 := pipeline(text,
    func(s) { return string.gsub(s, "^%s+", "") },
    func(s) { return string.gsub(s, "%s+$", "") },
    func(s) { return string.upper(s) },
    func(s) { return "<<" .. s .. ">>" }
)
print("  String pipeline:", result2)
print()

// -------------------------------------------------------
// 8. Memoize - cache function results
// -------------------------------------------------------
print("--- Memoize ---")

func memoize(f) {
    cache := {}
    return func(x) {
        key := tostring(x)
        if cache[key] != nil {
            return cache[key]
        }
        result := f(x)
        cache[key] = result
        return result
    }
}

callCount := 0
expensiveFn := memoize(func(n) {
    callCount = callCount + 1
    // Simulate expensive work
    result := 0
    for i := 1; i <= n; i++ {
        result = result + i
    }
    return result
})

print("  expensiveFn(100) =", expensiveFn(100), "(calls:", callCount .. ")")
print("  expensiveFn(100) =", expensiveFn(100), "(calls:", callCount .. ")")
print("  expensiveFn(50)  =", expensiveFn(50), "(calls:", callCount .. ")")
print("  expensiveFn(100) =", expensiveFn(100), "(calls:", callCount .. ")")
print("  expensiveFn(50)  =", expensiveFn(50), "(calls:", callCount .. ")")

// Memoized fibonacci - dramatic speedup
fibMemo := memoize(func(n) {
    if n < 2 { return n }
    return fibMemo(n - 1) + fibMemo(n - 2)
})
print()
print("  Memoized fibonacci:")
for i := 0; i <= 20; i++ {
    if i % 5 == 0 {
        print(string.format("    fib(%d) = %d", i, fibMemo(i)))
    }
}
print()

// -------------------------------------------------------
// 9. Putting it all together - functional data processing
// -------------------------------------------------------
print("--- Functional Data Processing ---")

people := {
    {name: "Alice", age: 30},
    {name: "Bob", age: 25},
    {name: "Charlie", age: 35},
    {name: "Diana", age: 28},
    {name: "Eve", age: 22}
}

// Find names of people over 25, uppercased
result3 := pipeline(people,
    func(t) { return filter(t, func(p) { return p.age > 25 }) },
    func(t) { return map(t, func(p) { return string.upper(p.name) }) }
)
print("  People over 25 (uppercased):", table.concat(result3, ", "))

// Average age using reduce
totalAge := reduce(people, func(acc, p) { return acc + p.age }, 0)
avgAge := totalAge / #people
print("  Average age:", avgAge)

print()
print("=== Done ===")
