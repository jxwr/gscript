// Structural variant: quicksort with negative ints and mixed integral floats.
// Tests numeric comparison and radix/fallback guards outside the original
// positive-int sort workload.

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

func make_negative_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1103515245 + 12345) % 2147483648
        arr[i] = (x % 200001) - 100000
    }
    return arr
}

func make_mixed_integral_array(n, seed) {
    arr := {}
    x := seed
    for i := 1; i <= n; i++ {
        x = (x * 1664525 + 1013904223) % 2147483648
        v := (x % 300001) - 150000
        if i % 3 == 0 {
            arr[i] = v + 0.0
        } else {
            arr[i] = v
        }
    }
    return arr
}

func is_sorted(arr, n) {
    for i := 1; i < n; i++ {
        if arr[i] > arr[i + 1] { return false }
    }
    return true
}

func sample_checksum(arr, n) {
    total := 0.0
    for i := 1; i <= n; i = i + 997 {
        total = total + arr[i] * ((i % 89) + 1)
    }
    return total
}

N := 60000
REPS := 4
negative_checksum := 0.0
mixed_checksum := 0.0
negative_sorted := false
mixed_sorted := false

t0 := time.now()
for rep := 1; rep <= REPS; rep++ {
    neg := make_negative_array(N, rep * 91)
    quicksort(neg, 1, N)
    negative_checksum = negative_checksum + sample_checksum(neg, N)
    negative_sorted = is_sorted(neg, N)

    mixed := make_mixed_integral_array(N, rep * 137)
    quicksort(mixed, 1, N)
    mixed_checksum = mixed_checksum + sample_checksum(mixed, N)
    mixed_sorted = is_sorted(mixed, N)
}
elapsed := time.since(t0)

print(string.format("negative sorted: %s checksum=%.1f", negative_sorted, negative_checksum))
print(string.format("mixed integral sorted: %s checksum=%.1f", mixed_sorted, mixed_checksum))
print(string.format("Time: %.3fs", elapsed))
