# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 JIT compiler. This is an AI-agent-driven experiment to build a JIT compiler that surpasses LuaJIT -- the entire compiler was designed, implemented, optimized, and documented by Claude.

## Blog: "Beyond LuaJIT"

**[jxwr.github.io/gscript](https://jxwr.github.io/gscript/)** -- the full story of building a tracing JIT compiler from scratch, from first trace compilation to beating LuaJIT on fib(20).

## Quick Start

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

### GScript JIT vs LuaJIT

| Benchmark | GScript (best) | LuaJIT | Gap |
|-----------|---------------|--------|-----|
| **fib(20) warm** | **19.1us** | 26.2us | **27% faster** |
| **fn calls warm** | **2.6us** | 2.6us | **parity** |
| ackermann(3,4) warm | 18.6us | 12.5us | 1.5x |
| mandelbrot(1000) | 0.142s | 0.057s | 2.5x |
| fib(35) | 0.026s | 0.026s | ~1.0x |
| sieve(1M x3) | 0.113s | 0.012s | 9.4x |
| ackermann(3,4 x500) | 0.009s | 0.006s | 1.5x |
| sort(50K x3) | 0.146s | 0.012s | 12.2x |
| sum_primes(100K) | 0.021s | 0.002s | 10.5x |
| nbody(500K) | 2.363s | 0.036s | 65.6x |
| spectral_norm(500) | 0.654s | 0.007s | 93.4x |
| matmul(300) | 1.186s | 0.021s | 56.5x |
| fannkuch(9) | 0.524s | 0.016s | 32.8x |
| mutual_recursion | 0.229s | 0.004s | 57.3x |
| method_dispatch(100K) | 0.113s | <0.001s | >100x |
| closure_bench | 0.053s | 0.009s | 5.9x |
| string_bench | 0.043s | 0.009s | 4.8x |

### GScript Trace JIT vs Interpreter

| Benchmark | VM | Trace | Speedup |
|-----------|-----|-------|---------|
| mandelbrot(1000) | 1.357s | **0.142s** | **x9.6** |
| HeavyLoop warm | 725.9us | **25.2us** | **x28.8** |
| FibRecursive(20) warm | 621.4us | **19.1us** | **x32.5** |
| FunctionCalls(10K) warm | 241.3us | **2.6us** | **x93.0** |
| Ackermann(3,4) warm | 297.1us | **18.6us** | **x15.9** |
| FibIterative(30) warm | 499.7ns | **197.7ns** | **x2.5** |
| fib(35) | 0.026s | 0.026s | x1.0 |
| sieve(1M x3) | 0.114s | 0.113s | x1.0 |
| nbody(500K) | 2.419s | 2.363s | x1.0 |
| sort(50K x3) | 0.146s | 0.147s | x0.99 |
| matmul(300) | 1.186s | 1.432s | x0.83 |

### Key Takeaways

- **fib(20) 27% faster than LuaJIT**, fn calls at parity
- **Warm JIT**: 2.5x-93x speedup over interpreter on tight loops and recursion
- **Mandelbrot x9.6 trace speedup**: 0.142s trace vs 1.357s VM (2.5x gap to LuaJIT)
- **Table-heavy benchmarks**: 10-100x behind LuaJIT due to 24B Value vs 8B NaN-boxed TValue

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1 (2026-03-20)

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
