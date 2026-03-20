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
| **fib(20) warm** | **19.2us** | 24.8us | **23% faster** |
| ackermann(3,4) warm | 18.6us | 12.0us | 1.6x |
| fn calls warm | 3.1us | 2.5us | 1.2x |
| mandelbrot(1000) | 0.214s | 0.057s | 3.8x |
| fib(35) | 0.026s | 0.025s | ~1.0x |
| sieve(1M x3) | 0.111s | 0.011s | 10.1x |
| ackermann(3,4 x500) | 0.009s | 0.006s | 1.5x |
| sort(50K x3) | 0.145s | 0.011s | 13.2x |
| sum_primes(100K) | 0.021s | 0.002s | 10.5x |
| nbody(500K) | 2.373s | 0.033s | 71.9x |
| spectral_norm(500) | 0.685s | 0.008s | 85.6x |
| matmul(300) | 1.187s | 0.024s | 49.5x |
| fannkuch(9) | 0.528s | 0.017s | 31.1x |
| mutual_recursion | 0.229s | 0.004s | 57.3x |
| method_dispatch(100K) | 0.114s | <0.001s | >100x |
| closure_bench | 0.054s | 0.009s | 6.0x |
| string_bench | 0.042s | 0.009s | 4.7x |

## GScript Trace JIT vs Interpreter

| Benchmark | VM | Trace | Speedup |
|-----------|-----|-------|---------|
| mandelbrot(1000) | 1.348s | **0.214s** | **x6.3** |
| HeavyLoop warm | 724.7us | **25.2us** | **x28.8** |
| FibRecursive(20) warm | 633.3us | **19.2us** | **x33.0** |
| FunctionCalls(10K) warm | 249.6us | **3.1us** | **x80.5** |
| Ackermann(3,4) warm | 296.7us | **18.6us** | **x15.9** |
| FibIterative(30) warm | 495.2ns | **196.6ns** | **x2.5** |
| fib(35) | 0.026s | 0.026s | x1.0 |
| sieve(1M x3) | 0.116s | 0.111s | x1.0 |
| ackermann(3,4 x500) | 0.009s | 0.009s | x1.0 |
| nbody(500K) | 2.405s | 2.373s | x1.0 |
| sort(50K x3) | 0.145s | 0.147s | x0.99 |
| sum_primes(100K) | 0.021s | 0.027s | x0.78 |
| mutual_recursion | 0.229s | 0.242s | x0.95 |
| method_dispatch(100K) | 0.114s | 0.115s | x0.99 |
| spectral_norm(500) | 0.685s | 0.701s | x0.98 |
| matmul(300) | 1.187s | 1.433s | x0.83 |
| fannkuch(9) | 0.528s | timeout | — |
| closure_bench | 0.054s | 0.056s | x0.96 |
| string_bench | 0.042s | 0.044s | x0.95 |

### Key Takeaways
- **Warm JIT (compiled, no startup)**: Excels at recursion + tight loops (2.5-80.5x speedup)
- **fib(20) 23% faster than LuaJIT**: Best result so far, up from 14% faster
- **ackermann improving**: Gap narrowed from 1.9x to 1.6x (warm), fib(35) at near-parity
- **Trace JIT (cold start)**: Mandelbrot x6.3 speedup; most others at parity (no regressions on sort/closure/string/etc)
- **Table-heavy**: VM still faster than trace on matmul (32B Value overhead)
- **Gap to LuaJIT**: 23% ahead on fib, 1.2x on fn_calls, 1.6x on ackermann, 3.8x on mandelbrot, 10-100x on table-heavy (needs NaN-boxing)

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1
Date: 2026-03-20
