// Benchmark: Quicksort
// Tests: array operations, recursion, comparison callbacks, partition patterns

func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            t := arr[i]
            arr[i] = arr[j]
            arr[j] = t
            i = i + 1
        }
    }
    t := arr[i]
    arr[i] = arr[hi]
    arr[hi] = t

    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}

// Generate pseudo-random array using LCG
func make_random_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = x
    }
    return arr
}

func is_sorted(arr, n) {
    for i := 1; i < n; i++ {
        if arr[i] > arr[i + 1] { return false }
    }
    return true
}

N := 50000
REPS := 3

t0 := time.now()
for rep := 1; rep <= REPS; rep++ {
    arr := make_random_array(N, rep * 42)
    quicksort(arr, 1, N)
}
elapsed := time.since(t0)

// Verify correctness on last sort
arr := make_random_array(N, REPS * 42)
quicksort(arr, 1, N)
sorted := is_sorted(arr, N)

print(string.format("quicksort(%d) x %d reps", N, REPS))
print(string.format("Sorted correctly: %s", sorted))
print(string.format("Time: %.3fs", elapsed))
