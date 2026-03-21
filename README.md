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

> **Status (2026-03-21):** NaN-boxing landed (Value 24B -> 8B), but JIT codegen is not yet adapted. Most benchmarks regressed. sieve improved thanks to better cache utilization from 8B array stride. This is the expected "break everything, then fix" phase.

### GScript JIT vs LuaJIT

| Benchmark | GScript (best) | LuaJIT | Gap |
|-----------|---------------|--------|-----|
| fib(35) | 0.036s | 0.032s | 1.1x |
| sieve(1M x3) | **0.025s** | 0.014s | 1.8x |
| ackermann(3,4 x500) | 0.011s | 0.008s | 1.4x |
| fib(20) warm | 47.9us | 32.0us | 1.5x |
| fn calls warm | 4.14us | 3.1us | 1.3x |
| ackermann(3,4) warm | 40.2us | 15.2us | 2.6x |
| mandelbrot(1000) | 1.500s | 0.072s | 20.8x |
| sort(50K x3) | 0.207s | 0.016s | 12.9x |
| sum_primes(100K) | 0.027s | 0.002s | 13.5x |
| nbody(500K) | 2.469s | 0.043s | 57.4x |
| spectral_norm(500) | 0.762s | 0.009s | 84.7x |
| matmul(300) | 1.161s | 0.029s | 40.0x |
| fannkuch(9) | 0.662s | 0.025s | 26.5x |
| mutual_recursion | 0.150s | 0.005s | 30.0x |
| method_dispatch(100K) | 0.093s | 0.000s | ~230x |
| closure_bench | 0.071s | 0.012s | 5.9x |
| string_bench | 0.051s | 0.010s | 5.1x |
| binary_trees | 2.385s | 0.17s | 14.0x |

### GScript JIT vs Interpreter (warm)

| Benchmark | JIT | VM | Speedup |
|-----------|-----|-----|---------|
| FunctionCalls(10K) | **4.14us** | 586.3us | **x141.6** |
| HeavyLoop | **38.1us** | 2201.0us | **x57.8** |
| FibRecursive(20) | **47.9us** | 1669.2us | **x34.8** |
| Ackermann(3,4) | **40.2us** | 734.7us | **x18.3** |
| FibIterative(30) | **283.5ns** | 1110ns | **x3.9** |

### Key Takeaways

- **NaN-boxing (Value 8B) landed** -- fundamental refactor complete, optimization phase ahead
- **JIT vs VM speedup increased**: FunctionCalls x141.6 (was x92.3), HeavyLoop x57.8 (was x28.5)
- **sieve improved** 0.080s -> 0.025s: 8B array stride = better L1 cache utilization
- **Regressions expected**: JIT codegen emits old-layout code, NaN-box tag/untag overhead in VM loop
- **Trace JIT broken** on mandelbrot, fannkuch, sort, binary_trees (timeouts/debug spam)
- **Next step**: adapt JIT codegen for NaN-box layout to recover pre-NaN-box performance

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1 (2026-03-21)

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
