# GScript Benchmark Suite

15 benchmarks covering compute, recursion, table/array, function calls, strings, and closures.

## Run

```bash
# Suite benchmarks (CLI, one-shot timing)
cd benchmarks/suite && bash run_all.sh /path/to/gscript -trace

# Go micro-benchmarks (warm, no compilation overhead)
go test ./benchmarks/ -bench=Warm -benchtime=3s

# Compare with LuaJIT
luajit benchmarks/lua/run_all.lua
```

## vs LuaJIT

| Benchmark | GScript JIT | LuaJIT | Result |
|-----------|------------|--------|--------|
| **fib(20)** | **24us** | 26us | **🏆 9% faster** |
| fn calls (10K) | 5.1us | 2.6us | 2.0x gap |
| ackermann(3,4) | 30us | 12us | 2.5x gap |
| mandelbrot(1000) | 0.23s | 0.056s | 4.0x gap |

## Full Suite

| Benchmark | Category | VM | JIT | Speedup |
|-----------|----------|-----|-----|---------|
| mandelbrot(1000) | float loops | 1.50s | **0.23s** | **×6.6** |
| fib(20) warm | recursion | — | **24us** | **×10** |
| fn calls warm | inlining | 226us | **5.1us** | **×44** |
| ackermann warm | nested recursion | 303us | **30us** | **×10** |
| nbody(500K) | table fields + float | 2.7s | 2.5s | ×1.1 |
| sieve(1M) | table array | 0.17s | 0.17s | ×1.0 |
| spectral_norm(500) | float + calls | 0.82s | 1.0s | ×0.82 |
| matmul(300) | 2D array | 1.26s | 1.63s | ×0.77 |
| fannkuch(9) | permutation | 0.52s | — | — |
| quicksort(50K) | array + recursion | 0.16s | — | — |
| sum_primes(100K) | integer + while-loop | 0.024s | 0.037s | ×0.65 |
| mutual_recursion | cross-function recursion | 0.28s | 0.32s | ×0.88 |
| method_dispatch(100K) | table-as-object | 0.13s | 0.13s | ×1.0 |
| closure_bench | closures + upvalues | — | — | — |
| string_bench | concat + format + sort | — | — | — |

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1
