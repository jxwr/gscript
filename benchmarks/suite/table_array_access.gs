// Benchmark: Table Array Access
// Tests: array indexing patterns (int arrays, float arrays, mixed read/write)
// Stresses GETTABLE/SETTABLE with integer keys, type-specialized array performance

// Test 1: Integer array sum (sequential access)
func int_array_sum(n) {
    arr := {}
    for i := 1; i <= n; i++ {
        arr[i] = i
    }
    sum := 0
    for i := 1; i <= n; i++ {
        sum = sum + arr[i]
    }
    return sum
}

// Test 2: Float array dot product
func float_dot_product(n) {
    a := {}
    b := {}
    for i := 1; i <= n; i++ {
        a[i] = 1.0 * i / n
        b[i] = 2.0 * (n - i + 1) / n
    }
    dot := 0.0
    for i := 1; i <= n; i++ {
        dot = dot + a[i] * b[i]
    }
    return dot
}

// Test 3: Array swap pattern (like sort operations)
func array_swap_bench(n, reps) {
    arr := {}
    for i := 1; i <= n; i++ {
        arr[i] = n - i + 1
    }
    // Repeatedly swap pairs
    for r := 1; r <= reps; r++ {
        for i := 1; i < n; i = i + 2 {
            t := arr[i]
            arr[i] = arr[i + 1]
            arr[i + 1] = t
        }
    }
    return arr[1]
}

// Test 4: 2D array access (matrix-like, row-major)
func array_2d_access(size) {
    rows := {}
    for i := 1; i <= size; i++ {
        row := {}
        for j := 1; j <= size; j++ {
            row[j] = i * size + j
        }
        rows[i] = row
    }
    // Sum all elements
    sum := 0
    for i := 1; i <= size; i++ {
        row := rows[i]
        for j := 1; j <= size; j++ {
            sum = sum + row[j]
        }
    }
    return sum
}

N := 100000
REPS := 100
MATRIX_SIZE := 300

t0 := time.now()
r1 := 0
for rep := 1; rep <= 10; rep++ {
    r1 = int_array_sum(N)
}
t1 := time.since(t0)

t0 = time.now()
r2 := 0.0
for rep := 1; rep <= 10; rep++ {
    r2 = float_dot_product(N)
}
t2 := time.since(t0)

t0 = time.now()
r3 := array_swap_bench(N, REPS)
t3 := time.since(t0)

t0 = time.now()
r4 := array_2d_access(MATRIX_SIZE)
t4 := time.since(t0)

total := t1 + t2 + t3 + t4

print(string.format("int_array_sum:    %.3fs (result=%d)", t1, r1))
print(string.format("float_dot:        %.3fs (result=%.6f)", t2, r2))
print(string.format("array_swap:       %.3fs (result=%d)", t3, r3))
print(string.format("array_2d:         %.3fs (result=%d)", t4, r4))
print(string.format("Time: %.3fs", total))
