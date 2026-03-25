# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 JIT compiler. This is an AI-agent-driven experiment to build a JIT compiler that surpasses LuaJIT -- the entire compiler was designed, implemented, optimized, and documented by Claude.

## Blog: "Beyond LuaJIT"

We are documenting the entire journey of building this JIT compiler as a series of technical blog posts. Each post covers a major optimization milestone -- what we tried, what worked, what failed, and the hard data behind every decision. The blog is as much a part of the project as the code itself.

Read the full series at **[jxwr.github.io/gscript](https://jxwr.github.io/gscript/)**.

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

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7. Updated **2026-03-26**.

### Benchmark suite (21 benchmarks, JIT rewrite branch)

| Benchmark | VM | JIT | Speedup |
|-----------|-----|-----|---------|
| table_field_access | 0.77s | 0.07s | 11.3x |
| mandelbrot(1000) | 1.43s | 0.26s | 5.5x |
| fibonacci_iterative(70x1M) | 1.12s | 0.35s | 3.2x |
| table_array_access | 0.42s | 0.14s | 3.0x |
| sieve(1M x3) | 0.26s | 0.14s | 1.9x |
| fannkuch(9) | 0.60s | 0.50s | 1.2x |
| fib(35) | 1.74s | 1.77s | 1.0x |
| ackermann | 0.30s | 0.37s | 0.8x |
| nbody(500K) | 1.95s | 1.98s | 1.0x |
| sort(50K x3) | 0.19s | 0.19s | 1.0x |
| sum_primes(100K) | 0.03s | 0.03s | 1.0x |
| binary_trees(15) | 1.73s | 1.75s | 1.0x |
| matmul(300) | 1.07s | 1.54s | 0.7x |
| spectral_norm(500) | 1.03s | 1.12s | 0.9x |
| mutual_recursion | 0.22s | 0.26s | 0.9x |
| method_dispatch | 0.09s | 0.11s | 0.9x |
| closure_bench | 0.008s | 0.009s | 0.9x |
| string_bench | 0.026s | 0.033s | 0.8x |
| object_creation | 0.68s | 0.73s | 0.9x |
| math_intensive | 0.97s | 0.98s | 1.0x |
| coroutine_bench | 5.74s | 5.68s | 1.0x |

### Compiler Optimization Techniques

**Trace JIT (loop-level compilation)**
- Trace recording: hot loop detection via FORLOOP/JMP back-edge counting
- SSA IR: value-based (not slot-based), with snapshots for precise deoptimization
- Native table operations: GETTABLE/SETTABLE compiled to ARM64 with bounds checks
- Native field operations: GETFIELD/SETFIELD with shape-based indexing
- While-loop compilation: JMP back-edge loops with AuxInt=-2 exit sentinel
- Break-exit distinction: ExitCode=4 for conditional breaks (no blacklisting)
- Side-exit for calls: function calls exit to interpreter, FORLOOP re-enters trace
- Float register allocation: linear-scan ref-level allocator for D4-D11
- Intrinsics: math.sqrt compiled to FSQRT

**Runtime optimizations**
- NaN-boxing: Value from 24B struct to 8B uint64 (Season 2)
- Type-specialized arrays: ArrayInt (`[]int64`), ArrayFloat (`[]float64`), ArrayBool (`[]byte`)
- Inline field cache: per-instruction hint for O(1) field access
- DivNums fast path: bypass metamethod dispatch for numeric division

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) -- 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, and more).
- **Architecture decisions**: [docs/decisions/](docs/decisions/) -- ADRs covering bytecode design, JIT strategy, and coroutine implementation.
- **Examples**: [examples/](examples/) -- from fibonacci to a full Chinese Chess AI with Raylib GUI.

## License

MIT
