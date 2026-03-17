# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 JIT compiler.

```go
func makeCounter(start) {
    n := start
    return {
        inc: func() { n = n + 1; return n },
        get: func() { return n },
    }
}

c := makeCounter(0)
print(c.inc())  // 1
print(c.inc())  // 2
```

Tables, metatables, closures, coroutines, goroutines, channels -- all with Go-flavored syntax.

## Getting Started

```bash
git clone https://github.com/jxwr/gscript
cd gscript
go build -o gscript ./cmd/gscript/
```

```bash
./gscript examples/fib.gs          # tree-walker
./gscript --vm examples/fib.gs     # bytecode VM (3-5x faster)
./gscript --jit examples/fib.gs    # ARM64 JIT (Apple Silicon)
./gscript -e 'print("hello")'      # eval a string
./gscript                           # REPL
```

## Performance

On Apple M4 Max, the ARM64 JIT reaches **62x faster** than the VM on hot loops and **17x faster** than gopher-lua on recursive workloads. Cold-start fib(25) completes in 663 us; warm JIT brings it down to 55 us.

Full benchmark tables and methodology: [benchmarks/README.md](benchmarks/README.md)

## Documentation

- **Blog -- "Beyond LuaJIT"**: [jxwr.github.io/gscript](https://jxwr.github.io/gscript/) -- deep dives on the JIT compiler, SSA IR, optimization strategy, and benchmark analysis.
- **Standard library reference**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
