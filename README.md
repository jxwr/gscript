# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 JIT compiler. This is an AI-agent-driven experiment to build a JIT compiler that surpasses LuaJIT -- the entire compiler was designed, implemented, optimized, and documented by Claude.

## Blog: "Beyond LuaJIT"

**[jxwr.github.io/gscript](https://jxwr.github.io/gscript/)** -- the full story of building a tracing JIT compiler from scratch, from first trace compilation to beating LuaJIT on fib(20), and the Season 2 NaN-boxing revolution.

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
| **fn calls warm** | **2.6us** | 2.6us | **parity** |
| FibRecursive(20) warm | 27.0us | 25us | 1.1x |
| fib(35) | 0.037s | 0.032s | 1.2x |
| ackermann(3,4 x500) | 0.011s | 0.008s | 1.4x |
| Ackermann(3,4) warm | 21.5us | 12us | 1.8x |
| **sieve(1M x3)** | **0.025s** | 0.011s | **2.3x** |
| mandelbrot(1000) | 0.157s | 0.057s | 2.8x |
| closure_bench | 0.071s | 0.012s | 5.9x |
| string_bench | 0.051s | 0.010s | 5.1x |
| binary_trees | 2.385s | 0.17s | 14x |
| sort(50K x3) | 0.207s | 0.016s | 13x |
| sum_primes(100K) | 0.027s | 0.002s | 14x |
| fannkuch(9) | 0.662s | 0.025s | 26x |
| mutual_recursion | 0.150s | 0.005s | 30x |
| matmul(300) | 1.16s | 0.029s | 40x |
| nbody(500K) | 2.47s | 0.043s | 57x |
| spectral_norm(500) | 0.76s | 0.009s | 85x |
| method_dispatch(100K) | 0.093s | ~0.001s | ~93x |

### GScript JIT vs Interpreter (warm)

| Benchmark | JIT | VM | Speedup |
|-----------|-----|-----|---------|
| FunctionCalls(10K) | **2.6us** | 338us | **x130** |
| HeavyLoop | **25.5us** | 1309us | **x51** |
| FibRecursive(20) | **27.0us** | 879us | **x33** |
| Ackermann(3,4) | **21.5us** | 417us | **x19** |
| FibIterative(30) | **182ns** | 637ns | **x3.5** |

### Key Takeaways

- **Season 2: NaN-boxing landed** -- Value shrunk from 24B to 8B (uint64)
- **sieve 3.2x faster** with NaN-boxing (0.080s → 0.025s): 8B array stride = 3x better cache utilization
- **fn calls at LuaJIT parity**: 2.6us vs 2.6us
- **Compute-heavy benchmarks competitive**: fib/ackermann within 1.1-1.8x of LuaJIT
- **Table-heavy benchmarks**: 40-85x gap remains (Go GC overhead + interpreter dispatch)
- **Next**: custom heap + custom GC to fully realize NaN-boxing gains

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1 (2026-03-21)

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
