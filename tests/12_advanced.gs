// Memoization
func memoize(f) {
    cache := {}
    return func(n) {
        if cache[n] != nil {
            return cache[n]
        }
        result := f(n)
        cache[n] = result
        return result
    }
}

func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}

mfib := memoize(fib)
print(mfib(30))
