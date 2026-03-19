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
| **fib(20) warm** | **24.2us** | 24.5us | **1% faster** |
| fn calls warm | 5.1us | 2.5us | 2.0x |
| ackermann(3,4) warm | 30.4us | 12.1us | 2.5x |
| mandelbrot(1000) | 0.233s | 0.057s | 4.1x |
| fib(35) | 0.032s | 0.025s | 1.3x |
| sieve(1M x3) | 0.112s | 0.011s | 10.2x |
| sort(50K x3) | 0.148s | 0.011s | 13.5x |
| sum_primes(100K) | 0.023s | 0.002s | 11.5x |
| nbody(500K) | 2.397s | 0.034s | 70.5x |
| spectral_norm(500) | 0.785s | 0.008s | 98x |
| matmul(300) | 1.209s | 0.026s | 46.5x |
| fannkuch(9) | 0.525s | 0.017s | 30.9x |
| mutual_recursion | 0.266s | 0.006s | 44.3x |
| method_dispatch(100K) | 0.123s | <0.001s | >100x |
| closure_bench | 0.069s | 0.009s | 7.7x |
| string_bench | 0.042s | 0.009s | 4.7x |

## GScript Trace JIT vs Interpreter

| Benchmark | VM | Trace | Speedup |
|-----------|-----|-------|---------|
| mandelbrot(1000) | 1.356s | **0.233s** | **x5.8** |
| HeavyLoop warm | 725.6us | **25.3us** | **x28.7** |
| FibRecursive(20) warm | 637.2us | **24.2us** | **x26.3** |
| FunctionCalls(10K) warm | 248.8us | **5.1us** | **x48.8** |
| Ackermann(3,4) warm | 301.5us | **30.4us** | **x9.9** |
| FibIterative(30) warm | 502ns | **212ns** | **x2.4** |
| fib(35) | 0.033s | 0.032s | x1.0 |
| nbody(500K) | 2.421s | 2.397s | x1.01 |
| sieve(1M x3) | 0.112s | 0.118s | x0.95 |
| ackermann(3,4 x500) | 0.015s | 0.015s | x1.0 |
| sort(50K x3) | 0.148s | 0.149s | x0.99 |
| sum_primes(100K) | 0.023s | 0.028s | x0.82 |
| mutual_recursion | 0.266s | 0.307s | x0.87 |
| method_dispatch(100K) | 0.123s | 0.124s | x0.99 |
| spectral_norm(500) | 0.785s | 0.808s | x0.97 |
| matmul(300) | 1.209s | 1.447s | x0.84 |
| fannkuch(9) | 0.525s | timeout | — |
| closure_bench | 0.069s | 0.070s | x0.99 |
| string_bench | 0.042s | 0.044s | x0.95 |

### Key Takeaways
- **Warm JIT (compiled, no startup)**: Excels at recursion + tight loops (2-49x speedup)
- **Trace JIT (cold start)**: Mandelbrot x5.8 speedup; most others at parity or slight regression
- **Table-heavy**: JIT at parity or slight regression (32B Value overhead)
- **Gap to LuaJIT**: 1% ahead on fib, 2-4x on compute, 10-100x on table-heavy (needs NaN-boxing)

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1
Date: 2026-03-20
