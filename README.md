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
| **fib(20) warm** | **19.4us** | 24.7us | **21% faster** |
| **fn calls warm** | **2.66us** | 2.6us | **parity** |
| ackermann(3,4) warm | 18.9us | 12.0us | 1.6x |
| fib(35) | 0.026s | 0.025s | ~parity |
| mandelbrot(1000) | 0.155s | 0.058s | 2.7x |
| sieve(1M x3) | 0.080s | 0.011s | 7.3x |
| ackermann(3,4 x500) | 0.009s | 0.006s | 1.5x |
| sort(50K x3) | 0.158s | 0.012s | 13.2x |
| sum_primes(100K) | 0.022s | 0.002s | 11.0x |
| nbody(500K) | 2.376s | 0.037s | 64.2x |
| spectral_norm(500) | 0.660s | 0.008s | 82.5x |
| matmul(300) | 1.120s | 0.022s | 50.9x |
| fannkuch(9) | 0.588s | 0.019s | 30.9x |
| mutual_recursion | 0.103s | 0.005s | 20.6x |
| method_dispatch(100K) | 0.080s | 0.001s | 80.0x |
| closure_bench | 0.046s | 0.009s | 5.1x |
| string_bench | 0.046s | 0.010s | 4.6x |
| binary_trees | 1.255s | 0.17s | 7.4x |

### GScript Trace JIT vs Interpreter

| Benchmark | VM | Trace | Speedup |
|-----------|-----|-------|---------|
| fib(35) | 0.882s | **0.028s** | **x31.5** |
| ackermann(3,4 x500) | 0.153s | **0.010s** | **x15.3** |
| mandelbrot(1000) | 1.397s | **0.155s** | **x9.0** |
| sieve(1M x3) | 0.308s | **0.100s** | **x3.1** |
| HeavyLoop warm | 735.5us | **25.8us** | **x28.5** |
| FibRecursive(20) warm | 642.8us | **19.4us** | **x33.1** |
| FunctionCalls(10K) warm | 245.6us | **2.66us** | **x92.3** |
| Ackermann(3,4) warm | 302.0us | **18.9us** | **x16.0** |
| FibIterative(30) warm | 505.5ns | **207.9ns** | **x2.4** |
| nbody(500K) | 2.405s | 2.422s | x1.0 |
| sort(50K x3) | 0.158s | 0.174s | x0.9 |
| matmul(300) | 1.163s | 1.444s | x0.8 |
| binary_trees | 1.255s | 1.871s | x0.7 |

### Key Takeaways

- **fib(20) 21% faster than LuaJIT**, fn calls at parity, fib(35) at parity
- **Warm JIT**: 2.4x-92x speedup over interpreter on tight loops and recursion
- **Trace JIT**: fib(35) x31.5, ackermann x15.3, mandelbrot x9.0 vs interpreter
- **15 benchmarks complete** including binary_trees (was crashing, now fixed)
- **Table-heavy benchmarks**: 5-80x behind LuaJIT due to 24B Value vs 8B NaN-boxed TValue

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1 (2026-03-21)

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
