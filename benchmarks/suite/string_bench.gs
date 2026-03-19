// Benchmark: String Operations
// Tests: concatenation, length checks, string comparison

// Test 1: String concatenation (building a long string)
func test_concat() {
    s := ""
    for i := 1; i <= 10000; i++ {
        s = s .. "x"
    }
    return #s
}

// Test 2: String formatting (sprintf-style)
func test_format() {
    total := 0
    for i := 1; i <= 50000; i++ {
        s := string.format("item_%d_value_%d", i, i * 2)
        total = total + #s
    }
    return total
}

// Test 3: String comparison (sorting strings)
func test_compare() {
    arr := {}
    for i := 1; i <= 1000; i++ {
        arr[i] = string.format("key_%05d", (i * 7) % 1000)
    }
    // Simple bubble sort (tests string comparison)
    n := #arr
    for i := 1; i <= n - 1; i++ {
        for j := 1; j <= n - i; j++ {
            if arr[j] > arr[j + 1] {
                t := arr[j]
                arr[j] = arr[j + 1]
                arr[j + 1] = t
            }
        }
    }
    return arr[1] .. " .. " .. arr[n]
}

t0 := time.now()
r1 := test_concat()
t1 := time.since(t0)

t0 = time.now()
r2 := test_format()
t2 := time.since(t0)

t0 = time.now()
r3 := test_compare()
t3 := time.since(t0)

print(string.format("concat:  %.3fs (len=%d)", t1, r1))
print(string.format("format:  %.3fs (total=%d)", t2, r2))
print(string.format("compare: %.3fs (first..last=%s)", t3, r3))
