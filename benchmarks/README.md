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

| Benchmark | GScript JIT | LuaJIT | Gap |
|-----------|------------|--------|-----|
| **fib(20) warm** | **24us** | 25us | **🏆 2% faster** |
| fn calls warm | 5.1us | 2.7us | 1.9x |
| ackermann(3,4) | 0.016s | 0.006s | 2.7x |
| mandelbrot(1000) | 0.229s | 0.060s | 3.8x |
| sort(50K) | 0.147s | 0.011s | 13x |
| sieve(1M×3) | 0.117s | 0.013s | 9.0x |
| sum_primes(100K) | 0.023s | 0.002s | 12x |
| nbody(500K) | 2.60s | 0.036s | 72x |
| spectral_norm(500) | 0.94s | 0.008s | 118x |
| matmul(300) | 1.56s | 0.023s | 68x |
| mutual_recursion | 0.305s | 0.005s | 61x |

## GScript JIT vs Interpreter

| Benchmark | VM | Best JIT | Speedup |
|-----------|-----|----------|---------|
| mandelbrot(1000) | 1.36s | **0.229s** | **×5.9** |
| fib(20) warm | — | **24us** | **×26** |
| fn calls warm | 248us | **5.1us** | **×49** |
| ackermann warm | 302us | **30us** | **×10** |
| nbody(500K) | 2.42s | 2.60s | ×0.93 |
| sieve(1M×3) | 0.111s | 0.117s | ×0.95 |
| sum_primes(100K) | 0.023s | — | — |
| sort(50K) | 0.147s | — | — |
| mutual_recursion | 0.267s | 0.305s | ×0.87 |
| method_dispatch(100K) | 0.122s | 0.128s | ×0.95 |
| spectral_norm(500) | 0.787s | 0.943s | ×0.84 |
| matmul(300) | 1.192s | 1.561s | ×0.76 |

### Key Takeaways
- **Compute + recursion**: JIT excels (6-49x speedup)
- **Table-heavy**: JIT at parity or slight regression (32B Value overhead)
- **Gap to LuaJIT**: 1x on fib, 2-4x on compute, 13-118x on table-heavy (needs NaN-boxing)

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1
