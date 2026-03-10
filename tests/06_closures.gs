// Closures
func makeCounter(start) {
    n := start
    return func() {
        n = n + 1
        return n
    }
}

c1 := makeCounter(0)
c2 := makeCounter(10)
print(c1())
print(c1())
print(c2())
print(c1())
print(c2())
