// Functions and recursion
func add(a, b) {
    return a + b
}
print(add(3, 4))

func divmod(a, b) {
    return a / b, a % b
}
q, r := divmod(17, 5)
print(q)
print(r)

func fib(n) {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
print(fib(10))
