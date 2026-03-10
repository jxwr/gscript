// Coroutine with wrap
gen := coroutine.wrap(func() {
    for i := 1; i <= 5; i++ {
        coroutine.yield(i * i)
    }
})

for {
    v := gen()
    if v == nil { break }
    print(v)
}
