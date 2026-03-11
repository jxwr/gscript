// coroutines.gs - Advanced coroutine patterns in GScript
// Demonstrates: generators, producer-consumer, cooperative multitasking,
//               coroutine-based state machine, pipeline with coroutines

print("=== Advanced Coroutine Patterns ===")
print()

// -------------------------------------------------------
// 1. Generator functions
// -------------------------------------------------------
print("--- Generators ---")

// Range generator: yields numbers from start to stop with step
func rangeGen(start, stop, step) {
    return coroutine.create(func() {
        i := start
        for i <= stop {
            coroutine.yield(i)
            i = i + step
        }
    })
}

// Helper to collect all values from a generator
func collect(gen) {
    result := {}
    for true {
        ok, val := coroutine.resume(gen)
        if !ok || val == nil {
            break
        }
        table.insert(result, val)
    }
    return result
}

// Helper to join table values as string
func joinValues(tbl) {
    parts := {}
    for i := 1; i <= #tbl; i++ {
        table.insert(parts, tostring(tbl[i]))
    }
    return table.concat(parts, ", ")
}

r := collect(rangeGen(1, 10, 2))
print("  range(1, 10, 2):", joinValues(r))

r = collect(rangeGen(0, 20, 5))
print("  range(0, 20, 5):", joinValues(r))

// Fibonacci generator: yields an infinite fibonacci sequence
func fibGen() {
    return coroutine.create(func() {
        a := 0
        b := 1
        for true {
            coroutine.yield(a)
            a, b = b, a + b
        }
    })
}

// Take N values from a generator
func take(gen, n) {
    result := {}
    for i := 1; i <= n; i++ {
        ok, val := coroutine.resume(gen)
        if !ok || val == nil { break }
        table.insert(result, val)
    }
    return result
}

fibs := take(fibGen(), 15)
print("  First 15 fibonacci:", joinValues(fibs))
print()

// -------------------------------------------------------
// 2. Producer-Consumer pattern
// -------------------------------------------------------
print("--- Producer-Consumer ---")

func producer(items) {
    return coroutine.create(func() {
        for i := 1; i <= #items; i++ {
            print("    [producer] sending: " .. tostring(items[i]))
            coroutine.yield(items[i])
        }
        print("    [producer] done")
    })
}

func consumer(prod) {
    consumed := {}
    for true {
        ok, item := coroutine.resume(prod)
        if !ok || item == nil { break }
        print("    [consumer] received: " .. tostring(item))
        table.insert(consumed, item)
    }
    return consumed
}

items := {"apple", "banana", "cherry", "date"}
prod := producer(items)
result := consumer(prod)
print("  Consumed " .. #result .. " items")
print()

// -------------------------------------------------------
// 3. Cooperative multitasking simulation
// -------------------------------------------------------
print("--- Cooperative Multitasking ---")

// Task scheduler that round-robins between coroutines
func scheduler() {
    tasks := {}
    sched := {}

    sched.addTask = func(name, fn) {
        table.insert(tasks, {
            name: name,
            co: coroutine.create(fn)
        })
    }

    sched.run = func() {
        step := 0
        for #tasks > 0 {
            step = step + 1
            // Process the first task
            task := tasks[1]
            table.remove(tasks, 1)

            ok, msg := coroutine.resume(task.co)
            status := coroutine.status(task.co)

            if msg != nil {
                print(string.format("    [step %d] %s: %s", step, task.name, tostring(msg)))
            }

            // If coroutine is still alive, put it back
            if status == "suspended" {
                table.insert(tasks, task)
            } else {
                print(string.format("    [step %d] %s: finished", step, task.name))
            }
        }
    }

    return sched
}

sched := scheduler()

sched.addTask("TaskA", func() {
    coroutine.yield("working on step 1")
    coroutine.yield("working on step 2")
    coroutine.yield("working on step 3")
})

sched.addTask("TaskB", func() {
    coroutine.yield("processing data")
    coroutine.yield("saving results")
})

sched.addTask("TaskC", func() {
    coroutine.yield("downloading")
    coroutine.yield("parsing")
    coroutine.yield("indexing")
    coroutine.yield("complete")
})

sched.run()
print()

// -------------------------------------------------------
// 4. Coroutine-based state machine
// -------------------------------------------------------
print("--- Coroutine State Machine ---")

// A traffic light state machine using coroutines
func trafficLight() {
    return coroutine.create(func() {
        for true {
            // Green state
            coroutine.yield("GREEN - Go!")
            coroutine.yield("GREEN - Go!")
            coroutine.yield("GREEN - Go!")
            // Yellow state
            coroutine.yield("YELLOW - Slow down!")
            // Red state
            coroutine.yield("RED - Stop!")
            coroutine.yield("RED - Stop!")
            coroutine.yield("RED - Stop!")
        }
    })
}

light := trafficLight()
print("  Traffic light simulation:")
for i := 1; i <= 14; i++ {
    ok, state := coroutine.resume(light)
    print(string.format("    tick %2d: %s", i, state))
}
print()

// Vending machine state machine
func vendingMachine() {
    return coroutine.create(func() {
        for true {
            // Idle state - waiting for coins
            event := coroutine.yield("IDLE: Insert coins")

            balance := 0
            if type(event) == "int" || type(event) == "float" || type(event) == "number" {
                balance = balance + event
            }

            // Collecting coins state
            for balance < 100 {
                event = coroutine.yield("COLLECTING: Balance=" .. tostring(balance) .. " (need 100)")
                if type(event) == "int" || type(event) == "float" || type(event) == "number" {
                    balance = balance + event
                }
            }

            // Dispensing state
            coroutine.yield("DISPENSING: Enjoy your item! Change=" .. tostring(balance - 100))
        }
    })
}

vm := vendingMachine()
print("  Vending machine simulation:")
ok, state := coroutine.resume(vm)
print("    " .. state)
ok, state = coroutine.resume(vm, 25)
print("    " .. state)
ok, state = coroutine.resume(vm, 25)
print("    " .. state)
ok, state = coroutine.resume(vm, 50)
print("    " .. state)
ok, state = coroutine.resume(vm, 25)
print("    " .. state)
print()

// -------------------------------------------------------
// 5. Pipeline with coroutines
// -------------------------------------------------------
print("--- Coroutine Pipeline ---")

// Each stage is a coroutine that processes and yields values
// Stage 1: Generate numbers
func generate(start, stop) {
    return coroutine.create(func() {
        for i := start; i <= stop; i++ {
            coroutine.yield(i)
        }
    })
}

// Stage 2: Filter (keep only values matching predicate)
func filterStage(source, pred) {
    return coroutine.create(func() {
        for true {
            ok, val := coroutine.resume(source)
            if !ok || val == nil { break }
            if pred(val) {
                coroutine.yield(val)
            }
        }
    })
}

// Stage 3: Map (transform each value)
func mapStage(source, fn) {
    return coroutine.create(func() {
        for true {
            ok, val := coroutine.resume(source)
            if !ok || val == nil { break }
            coroutine.yield(fn(val))
        }
    })
}

// Build a pipeline: generate 1..20, keep evens, square them
source := generate(1, 20)
evens := filterStage(source, func(x) { return x % 2 == 0 })
squared := mapStage(evens, func(x) { return x * x })

results := collect(squared)
print("  Pipeline: generate(1..20) -> filter(even) -> map(square)")
print("  Result:", joinValues(results))
print()

// A more complex pipeline
source2 := generate(1, 50)
step1 := filterStage(source2, func(x) { return x % 3 == 0 })   // multiples of 3
step2 := mapStage(step1, func(x) { return x * 2 })               // double them
step3 := filterStage(step2, func(x) { return x < 50 })           // keep under 50

results2 := collect(step3)
print("  Pipeline: gen(1..50) -> filter(%3==0) -> map(*2) -> filter(<50)")
print("  Result:", joinValues(results2))
print()

// -------------------------------------------------------
// 6. Coroutine as iterator
// -------------------------------------------------------
print("--- Coroutine as Iterator ---")

// Wrap a coroutine to work with for-range
func iterFromCoroutine(co) {
    return func() {
        if coroutine.status(co) == "dead" { return nil }
        ok, val := coroutine.resume(co)
        if !ok || val == nil { return nil }
        return val
    }
}

// Permutations generator
func permutations(arr) {
    return coroutine.create(func() {
        if #arr <= 1 {
            coroutine.yield(arr)
            return nil
        }

        for i := 1; i <= #arr; i++ {
            // Create rest without element i
            rest := {}
            for j := 1; j <= #arr; j++ {
                if j != i {
                    table.insert(rest, arr[j])
                }
            }

            // Get permutations of rest
            subPerms := permutations(rest)
            for true {
                ok, perm := coroutine.resume(subPerms)
                if !ok || perm == nil { break }
                // Prepend arr[i]
                result := {arr[i]}
                for k := 1; k <= #perm; k++ {
                    table.insert(result, perm[k])
                }
                coroutine.yield(result)
            }
        }
    })
}

print("  Permutations of {1, 2, 3}:")
perms := permutations({1, 2, 3})
iter := iterFromCoroutine(perms)
for perm := range iter {
    print("    " .. joinValues(perm))
}
print()

print("=== Done ===")
