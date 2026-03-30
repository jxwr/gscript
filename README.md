# GScript

A scripting language with **Go syntax** and **Lua semantics**, featuring a tree-walking interpreter, register-based bytecode VM, and ARM64 Method JIT compiler. Built entirely by AI agents (Claude) as an experiment in AI-driven compiler development.

## Blog: "Beyond LuaJIT"

The full story of building this JIT — 20+ posts covering architecture, optimization, failures, a complete rewrite, the pivot to Method JIT, and hard performance data.

Read the series at **[jxwr.github.io/gscript](https://jxwr.github.io/gscript/)**.

## Quick Start

```bash
git clone https://github.com/jxwr/gscript
cd gscript
go build -o gscript ./cmd/gscript/
```

```bash
./gscript examples/fib.gs          # tree-walker
./gscript --vm examples/fib.gs     # bytecode VM
./gscript --jit examples/fib.gs    # ARM64 JIT (Apple Silicon)
./gscript -e 'print("hello")'      # eval a string
./gscript                           # REPL
```

## Performance

Measured on Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1. Updated **2026-03-30**.

### Tier 1 Baseline JIT (22 benchmarks)

| Benchmark | VM | Tier1 JIT | T1/VM | LuaJIT | vs LuaJIT |
|-----------|-----|-----------|-------|--------|-----------|
| **fib(35)** | 1.553s | **0.139s** | **11.17x** | 25ms | 5.6x |
| **fib_recursive(35×10)** | 15.48s | **1.265s** | **12.23x** | -- | -- |
| **table_field_access** | 731ms | **114ms** | **6.42x** | -- | -- |
| **fibonacci_iterative** | 1.023s | **217ms** | **4.71x** | -- | -- |
| **mandelbrot(1000)** | 1.349s | **355ms** | **3.80x** | 59ms | 6.0x |
| **spectral_norm(500)** | 982ms | **290ms** | **3.39x** | 8ms | 36.3x |
| **nbody(500K)** | 1.866s | **630ms** | **2.96x** | 34ms | 18.5x |
| **closure_bench** | 85ms | **32ms** | **2.66x** | 9ms | 3.6x |
| **string_bench** | 43ms | **20ms** | **2.15x** | 8ms | 2.5x |
| **matmul(300)** | 1.006s | 792ms | **1.27x** | 22ms | 36.0x |
| **sieve(1M×3)** | 237ms | 201ms | **1.18x** | 11ms | 18.3x |
| **math_intensive** | 898ms | 763ms | **1.18x** | -- | -- |
| **fannkuch(9)** | 547ms | 544ms | **1.01x** | 20ms | 27.2x |
| coroutine_bench | 4.90s | 5.44s | 0.90x | -- | -- |
| ackermann(3,4×500) | 281ms | 295ms | 0.95x | 6ms | 49.2x |
| mutual_recursion | 193ms | 204ms | 0.95x | 4ms | 51.0x |
| binary_trees(15) | 1.583s | 1.663s | 0.95x | -- | -- |
| table_array_access | 402ms | 422ms | 0.95x | -- | -- |
| object_creation | 622ms | 947ms | 0.66x | -- | -- |
| method_dispatch | 86ms | 132ms | 0.65x | <1ms | -- |
| sum_primes(100K) | 26ms | 31ms | 0.84x | 2ms | 15.5x |
| sort(50K×3) | 179ms | ERROR | -- | 11ms | -- |

**9/22 benchmarks at 2x+. 13/22 faster than VM.**

### Architecture

**Tier 1 Baseline JIT** — V8 Sparkplug-style 1:1 bytecode→ARM64 templates:

- **Native BLR calls**: Compiled function calls use ARM64 `BLR` directly (~10ns per call). Two entry points per function: normal (96B frame for Go) + direct (16B frame for JIT-to-JIT calls)
- **Inline field cache**: Shape-guarded per-PC cache. GETFIELD/SETFIELD → direct `svals[idx]` access on shape hit
- **GETGLOBAL cache**: Per-PC value cache with generation-based invalidation. Eliminates exit-resume for global function lookups
- **NaN-boxing**: Every value is uint64. Float64 = raw IEEE 754. Int=0xFFFE, Bool=0xFFFD, Ptr=0xFFFF, Nil=0xFFFC

**Tier 2 Optimizing JIT** (SSA IR pipeline):

- Bytecode → CFG SSA IR (Braun algorithm) → TypeSpecialize → ConstProp → DCE → Inline → RegAlloc → ARM64
- Deoptimization framework for falling back to interpreter when speculation fails

**Runtime**:

- NaN-boxing (8-byte Value: int/float/pointer in one uint64)
- Type-specialized arrays (ArrayInt, ArrayFloat, ArrayBool)
- Inline field cache (per-instruction O(1) field access)

### Testing

```bash
go test ./internal/methodjit/ -count=1 -timeout 120s
```

## More

- **Standard library**: [docs/stdlib/STDLIB.md](docs/stdlib/STDLIB.md) — 30+ libraries (string, table, math, json, fs, net, regexp, http, raylib, etc.)
- **JIT architecture**: [docs-internal/architecture/overview.md](docs-internal/architecture/overview.md)
- **Benchmark details**: [docs-internal/tier1-benchmark-results.md](docs-internal/tier1-benchmark-results.md)
- **Examples**: [examples/](examples/) — from fibonacci to a full Chinese Chess AI with Raylib GUI

## License

MIT
