// Benchmark: Fannkuch-Redux
// Tests: array permutation, conditional branching, integer heavy
// A classic benchmark from the Computer Language Benchmarks Game

func fannkuch(n) {
    perm := {}
    perm1 := {}
    count := {}
    for i := 1; i <= n; i++ {
        perm1[i] = i
        count[i] = i
    }

    maxFlips := 0
    checksum := 0
    nperm := 0

    for {
        // Copy perm1 to perm
        for i := 1; i <= n; i++ {
            perm[i] = perm1[i]
        }

        // Count flips
        flips := 0
        k := perm[1]
        for k != 1 {
            // Reverse first k elements
            lo := 1
            hi := k
            for lo < hi {
                t := perm[lo]
                perm[lo] = perm[hi]
                perm[hi] = t
                lo = lo + 1
                hi = hi - 1
            }
            flips = flips + 1
            k = perm[1]
        }
        if flips > maxFlips { maxFlips = flips }
        if nperm % 2 == 0 {
            checksum = checksum + flips
        } else {
            checksum = checksum - flips
        }
        nperm = nperm + 1

        // Next permutation (Johnson-Trotter)
        done := true
        for i := 2; i <= n; i++ {
            // Rotate perm1[1..i] left by one
            t := perm1[1]
            for j := 1; j < i; j++ {
                perm1[j] = perm1[j + 1]
            }
            perm1[i] = t

            count[i] = count[i] - 1
            if count[i] > 0 {
                done = false
                break
            }
            count[i] = i
        }
        if done { break }
    }

    return {maxFlips: maxFlips, checksum: checksum}
}

N := 9
t0 := time.now()
result := fannkuch(N)
elapsed := time.since(t0)

print(string.format("fannkuch(%d) = %d flips, checksum %d", N, result.maxFlips, result.checksum))
print(string.format("Time: %.3fs", elapsed))
