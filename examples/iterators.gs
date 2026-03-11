// iterators.gs - Iterator patterns in GScript
// Demonstrates: custom range, filtered, mapped, zip, chained, stateful iterators

print("=== Iterator Patterns ===")
print()

// Helper: collect all values from an iterator function into a table
func collect(iter) {
    result := {}
    for val := range iter {
        table.insert(result, val)
    }
    return result
}

// Helper: join table values
func joinValues(tbl) {
    parts := {}
    for i := 1; i <= #tbl; i++ {
        table.insert(parts, tostring(tbl[i]))
    }
    return table.concat(parts, ", ")
}

// -------------------------------------------------------
// 1. Custom range iterator
// -------------------------------------------------------
print("--- Range Iterator ---")

// Basic range: yields start, start+step, ..., up to (but not exceeding) stop
func rangeIter(start, stop, step) {
    if step == nil { step = 1 }
    i := start
    return func() {
        if step > 0 && i > stop { return nil }
        if step < 0 && i < stop { return nil }
        val := i
        i = i + step
        return val
    }
}

vals := collect(rangeIter(1, 10, 1))
print("  range(1, 10, 1):", joinValues(vals))

vals = collect(rangeIter(0, 20, 5))
print("  range(0, 20, 5):", joinValues(vals))

vals = collect(rangeIter(10, 1, -2))
print("  range(10, 1, -2):", joinValues(vals))

// Count iterator: infinite counter (use with take!)
func countFrom(start) {
    i := start
    return func() {
        val := i
        i = i + 1
        return val
    }
}

// Take: limit an iterator to N values
func take(iter, n) {
    count := 0
    return func() {
        if count >= n { return nil }
        val := iter()
        if val == nil { return nil }
        count = count + 1
        return val
    }
}

vals = collect(take(countFrom(100), 5))
print("  take(countFrom(100), 5):", joinValues(vals))
print()

// -------------------------------------------------------
// 2. Filtered iterator
// -------------------------------------------------------
print("--- Filtered Iterator ---")

func filterIter(iter, pred) {
    return func() {
        for true {
            val := iter()
            if val == nil { return nil }
            if pred(val) { return val }
        }
    }
}

// Filter even numbers from range 1..20
evens := collect(filterIter(rangeIter(1, 20, 1), func(x) { return x % 2 == 0 }))
print("  filter(range(1,20), even):", joinValues(evens))

// Filter multiples of 3
mult3 := collect(filterIter(rangeIter(1, 30, 1), func(x) { return x % 3 == 0 }))
print("  filter(range(1,30), %3==0):", joinValues(mult3))

// Filter primes (simple trial division)
func isPrime(n) {
    if n < 2 { return false }
    if n < 4 { return true }
    if n % 2 == 0 { return false }
    i := 3
    for i * i <= n {
        if n % i == 0 { return false }
        i = i + 2
    }
    return true
}

primes := collect(filterIter(rangeIter(2, 50, 1), isPrime))
print("  Primes up to 50:", joinValues(primes))
print()

// -------------------------------------------------------
// 3. Mapped iterator
// -------------------------------------------------------
print("--- Mapped Iterator ---")

func mapIter(iter, fn) {
    return func() {
        val := iter()
        if val == nil { return nil }
        return fn(val)
    }
}

// Square numbers
squared := collect(mapIter(rangeIter(1, 10, 1), func(x) { return x * x }))
print("  map(range(1,10), x*x):", joinValues(squared))

// Format numbers as strings
formatted := collect(mapIter(rangeIter(1, 5, 1), func(x) { return "[" .. tostring(x) .. "]" }))
print("  map(range(1,5), format):", joinValues(formatted))

// Combine map and filter: squares of even numbers
pipeline := mapIter(
    filterIter(rangeIter(1, 10, 1), func(x) { return x % 2 == 0 }),
    func(x) { return x * x }
)
result := collect(pipeline)
print("  map(filter(range(1,10), even), square):", joinValues(result))
print()

// -------------------------------------------------------
// 4. Zip iterator
// -------------------------------------------------------
print("--- Zip Iterator ---")

// Zip: combines two iterators into pairs
func zipIter(iter1, iter2) {
    return func() {
        a := iter1()
        b := iter2()
        if a == nil || b == nil { return nil }
        return {a, b}
    }
}

// Zip numbers with their squares
iter := zipIter(rangeIter(1, 5, 1), mapIter(rangeIter(1, 5, 1), func(x) { return x * x }))
print("  zip(1..5, squares):")
for pair := range iter {
    print(string.format("    %d -> %d", pair[1], pair[2]))
}

// Zip names with scores
names := {"Alice", "Bob", "Charlie", "Diana"}
scores := {95, 87, 92, 78}

func tableIter(tbl) {
    i := 0
    return func() {
        i = i + 1
        if i > #tbl { return nil }
        return tbl[i]
    }
}

zipped := zipIter(tableIter(names), tableIter(scores))
print("  zip(names, scores):")
for pair := range zipped {
    print(string.format("    %s: %d", pair[1], pair[2]))
}
print()

// -------------------------------------------------------
// 5. Chained iterators
// -------------------------------------------------------
print("--- Chained Iterators ---")

// Chain: concatenate multiple iterators
func chainIter(...) {
    iters := {...}
    idx := 1
    return func() {
        for idx <= #iters {
            val := iters[idx]()
            if val != nil { return val }
            idx = idx + 1
        }
        return nil
    }
}

// Chain three ranges together
chained := collect(chainIter(
    rangeIter(1, 3, 1),
    rangeIter(10, 13, 1),
    rangeIter(100, 103, 1)
))
print("  chain(1..3, 10..13, 100..103):", joinValues(chained))

// Chain with different types
chained2 := collect(chainIter(
    tableIter({"hello", "world"}),
    tableIter({"foo", "bar", "baz"})
))
print("  chain strings:", joinValues(chained2))
print()

// -------------------------------------------------------
// 6. Stateful iterator with close
// -------------------------------------------------------
print("--- Stateful Iterator with Close ---")

// An iterator that tracks usage and can be "closed"
func trackedIter(source) {
    count := 0
    closed := false

    iter := {}

    iter.next = func() {
        if closed { return nil }
        val := source()
        if val == nil {
            closed = true
            return nil
        }
        count = count + 1
        return val
    }

    iter.close = func() {
        closed = true
        print(string.format("    Iterator closed after %d items", count))
    }

    iter.count = func() { return count }
    iter.isClosed = func() { return closed }

    return iter
}

tracked := trackedIter(rangeIter(1, 100, 1))

// Read only 5 items
results := {}
for i := 1; i <= 5; i++ {
    val := tracked.next()
    if val != nil { table.insert(results, val) }
}
print("  Read 5 items:", joinValues(results))
print("  Items consumed:", tracked.count())
tracked.close()
print("  Is closed:", tracked.isClosed())
print("  Read after close:", tracked.next())
print()

// -------------------------------------------------------
// 7. Enumerate iterator
// -------------------------------------------------------
print("--- Enumerate Iterator ---")

func enumerate(iter, start) {
    if start == nil { start = 1 }
    idx := start - 1
    return func() {
        val := iter()
        if val == nil { return nil }
        idx = idx + 1
        return {idx, val}
    }
}

fruits := {"apple", "banana", "cherry", "date"}
print("  enumerate(fruits):")
for pair := range enumerate(tableIter(fruits), 1) {
    print(string.format("    %d: %s", pair[1], pair[2]))
}
print()

// -------------------------------------------------------
// 8. Reduce with iterators
// -------------------------------------------------------
print("--- Reduce with Iterators ---")

func reduceIter(iter, fn, init) {
    acc := init
    for val := range iter {
        acc = fn(acc, val)
    }
    return acc
}

sum := reduceIter(rangeIter(1, 100, 1), func(a, b) { return a + b }, 0)
print("  sum(1..100):", sum)

product := reduceIter(rangeIter(1, 10, 1), func(a, b) { return a * b }, 1)
print("  product(1..10):", product)

// Find max of squares of odd numbers 1..20
maxOddSquare := reduceIter(
    mapIter(filterIter(rangeIter(1, 20, 1), func(x) { return x % 2 != 0 }), func(x) { return x * x }),
    func(a, b) { if b > a { return b }; return a },
    0
)
print("  max of squares of odds in 1..20:", maxOddSquare)
print()

// -------------------------------------------------------
// 9. Putting it all together - word processing pipeline
// -------------------------------------------------------
print("--- Iterator Pipeline Example ---")

sentence := "the quick brown fox jumps over the lazy dog and the fox runs fast"
words2 := string.split(sentence, " ")

// Pipeline: get unique words longer than 3 chars, sorted
seen := {}
uniqueLong := collect(
    filterIter(
        filterIter(
            tableIter(words2),
            func(w) { return #w > 3 }
        ),
        func(w) {
            if seen[w] { return false }
            seen[w] = true
            return true
        }
    )
)
table.sort(uniqueLong)
print("  Sentence: \"" .. sentence .. "\"")
print("  Unique words > 3 chars (sorted):", joinValues(uniqueLong))
print()

print("=== Done ===")
