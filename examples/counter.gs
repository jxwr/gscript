func makeCounter(name, start) {
    n := start
    return {
        inc: func() { n = n + 1; return n },
        dec: func() { n = n - 1; return n },
        get: func() { return n },
        reset: func() { n = start },
        name: name
    }
}

c := makeCounter("myCounter", 0)
print(c.name, c.inc())
print(c.name, c.inc())
print(c.name, c.inc())
print(c.name, c.dec())
c.reset()
print(c.name, c.get())
