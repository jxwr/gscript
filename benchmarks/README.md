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

## GScript JIT vs LuaJIT

| Benchmark | GScript (best) | LuaJIT | Gap |
|-----------|---------------|--------|-----|
| **fib(20) warm** | **26.1us** | 31.5us | **17% faster** |
| **fn calls warm** | **2.8us** | 2.5us | **1.1x** |
| ackermann(3,4) warm | 32.8us | 15.1us | 2.2x |
| mandelbrot(1000) | 0.259s | 0.063s | 4.1x |
| fib(35) | 0.033s | 0.031s | 1.1x |
| sieve(1M x3) | 0.140s | 0.013s | 10.8x |
| sort(50K x3) | 0.159s | 0.012s | 13.3x |
| sum_primes(100K) | 0.025s | 0.002s | 12.5x |
| nbody(500K) | 2.666s | 0.047s | 56.7x |
| spectral_norm(500) | 0.872s | 0.010s | 87.2x |
| matmul(300) | 1.330s | 0.031s | 42.9x |
| fannkuch(9) | 0.562s | 0.025s | 22.5x |
| mutual_recursion | 0.280s | 0.005s | 56.0x |
| method_dispatch(100K) | 0.130s | <0.001s | >100x |
| closure_bench | 0.074s | 0.011s | 6.7x |
| string_bench | 0.045s | 0.010s | 4.5x |

## GScript Trace JIT vs Interpreter

| Benchmark | VM | Trace | Speedup |
|-----------|-----|-------|---------|
| mandelbrot(1000) | 1.540s | **0.259s** | **x5.9** |
| HeavyLoop warm | 868.8us | **27.7us** | **x31.4** |
| FibRecursive(20) warm | 747.1us | **26.1us** | **x28.7** |
| FunctionCalls(10K) warm | 308.1us | **2.8us** | **x108.9** |
| Ackermann(3,4) warm | 365.3us | **32.8us** | **x11.1** |
| FibIterative(30) warm | 605ns | **237ns** | **x2.6** |
| fib(35) | 0.037s | 0.033s | x1.1 |
| sieve(1M x3) | 0.140s | 0.140s | x1.0 |
| ackermann(3,4 x500) | 0.016s | 0.017s | x0.94 |
| nbody(500K) | 2.666s | 3.340s | x0.80 |
| sort(50K x3) | 0.159s | 0.327s | x0.49 |
| sum_primes(100K) | 0.025s | 0.062s | x0.40 |
| mutual_recursion | 0.280s | 0.662s | x0.42 |
| method_dispatch(100K) | 0.130s | 0.273s | x0.48 |
| spectral_norm(500) | 0.872s | 0.956s | x0.91 |
| matmul(300) | 1.330s | 1.704s | x0.78 |
| fannkuch(9) | 0.562s | timeout | — |
| closure_bench | 0.074s | 0.156s | x0.47 |
| string_bench | 0.045s | 0.096s | x0.47 |

### Key Takeaways
- **Warm JIT (compiled, no startup)**: Excels at recursion + tight loops (2.6-109x speedup)
- **fn_calls x108.9**: Biggest JIT-vs-VM speedup; now within 1.1x of LuaJIT (was 2.0x)
- **Trace JIT (cold start)**: Mandelbrot x5.9 speedup; most others at parity or regression
- **Table-heavy**: JIT at parity or regression (32B Value overhead)
- **Gap to LuaJIT**: 17% ahead on fib, ~parity on fn_calls, 2-4x on compute, 10-100x on table-heavy (needs NaN-boxing)

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1
Date: 2026-03-20
