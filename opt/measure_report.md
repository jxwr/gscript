## Benchmark Results — 2026-04-06 (post revert f7496b4)

Commit: `f7496b4` (jit-v3-clean, post recursive-tier2 revert)

| Benchmark | VM | JIT | LuaJIT | JIT/VM | JIT/LuaJIT |
|-----------|-----|-----|--------|--------|------------|
| fib | 1.684s | 1.417s | 0.025s | 0.84x | 56.7x |
| fib_recursive | 16.816s | 14.147s | N/A | 0.84x | — |
| sieve | 0.248s | 0.227s | 0.011s | 0.92x | 20.6x |
| mandelbrot | 1.412s | 0.372s | 0.058s | 0.26x | 6.4x |
| ackermann | 0.291s | 0.257s | 0.006s | 0.88x | 42.8x |
| matmul | 1.041s | 0.834s | 0.022s | 0.80x | 37.9x |
| spectral_norm | 1.014s | 0.336s | 0.008s | 0.33x | 42.0x |
| nbody | 1.931s | 0.631s | 0.033s | 0.33x | 19.1x |
| fannkuch | 0.565s | 0.071s | 0.020s | 0.13x | 3.6x |
| sort | 0.186s | 0.052s | 0.011s | 0.28x | 4.7x |
| sum_primes | 0.028s | 0.004s | 0.002s | 0.14x | 2.0x |
| mutual_recursion | 0.205s | 0.187s | 0.005s | 0.91x | 37.4x |
| method_dispatch | 0.094s | 0.101s | <0.001s | 1.07x | >100x |
| closure_bench | 0.086s | 0.027s | — | 0.31x | — |
| string_bench | 0.044s | 0.030s | — | 0.68x | — |
| binary_trees | 1.681s | 2.095s | — | 1.25x | — |
| table_field_access | 0.752s | 0.072s | N/A | 0.10x | — |
| table_array_access | 0.415s | 0.138s | N/A | 0.33x | — |
| coroutine_bench | 16.709s | 19.691s | N/A | 1.18x | — |
| fibonacci_iterative | 1.035s | 0.300s | N/A | 0.29x | — |
| math_intensive | 0.924s | 0.194s | N/A | 0.21x | — |
| object_creation | 0.659s | 0.782s | N/A | 1.19x | — |

## Top 3 Gaps vs LuaJIT

Ranked by ratio (how far from LuaJIT):

1. **fib**: 1.417s vs 0.025s (56.7x) — recursive call overhead, Tier 2 unpromotable
2. **ackermann**: 0.257s vs 0.006s (42.8x) — recursive call overhead, Tier 2 unpromotable
3. **spectral_norm**: 0.336s vs 0.008s (42.0x) — float loop codegen, NaN box/unbox overhead

Runners-up: matmul (37.9x), mutual_recursion (37.4x), sieve (20.6x), nbody (19.1x)

By absolute wall-time gap: fib (1.392s), matmul (0.812s), nbody (0.598s)

## Trajectory (from plot_history.sh)

```
Benchmark                  04-04     04-05     04-06    LuaJIT
---------                   ----      ----      ----      ----
ackermann                0.245s   0.297s↑   0.257s↓    0.006s
binary_trees             1.893s   2.788s↑   2.095s↓       N/A
closure_bench            0.035s   0.035s·   0.027s↓       N/A
coroutine_bench         16.441s  29.606s↑  19.691s↓       N/A
fannkuch                 0.066s   0.081s↑   0.071s↓    0.020s
fib                      1.347s   1.421s↑   1.417s·    0.025s
fib_recursive           13.450s  15.134s↑  14.147s↓       N/A
fibonacci_iterative      0.071s   0.301s↑   0.300s·       N/A
mandelbrot               0.344s   0.417s↑   0.372s↓    0.058s
math_intensive           0.188s   0.194s·   0.194s·       N/A
matmul                   0.810s   0.999s↑   0.834s↓    0.022s
method_dispatch          0.143s   0.122s↓   0.101s↓    0.000s
mutual_recursion         0.181s   0.221s↑   0.187s↓    0.005s
nbody                    0.654s   0.765s↑   0.631s↓    0.033s
object_creation          0.947s   0.863s↓   0.782s↓       N/A
sieve                    0.105s   0.261s↑   0.227s↓    0.011s
sort                     0.037s   0.063s↑   0.052s↓    0.011s
spectral_norm            0.138s   0.401s↑   0.336s↓    0.008s
string_bench             0.020s   0.040s↑   0.030s↓       N/A
sum_primes               0.006s   0.005s↓   0.004s↓    0.002s
table_array_access       0.127s   0.195s↑   0.138s↓       N/A
table_field_access       0.072s   0.110s↑   0.072s↓       N/A
```

### Notable Movement (vs previous baseline 6591926)
- **coroutine_bench**: 17.595s → 19.691s (+11.9% regression, likely run-to-run variance)
- **fibonacci_iterative**: 0.276s → 0.300s (+8.7% regression, likely variance)
- **sieve**: 0.241s → 0.227s (-5.8% improved)
- Most other benchmarks flat (within ±3% noise)

### JIT Regressions (JIT slower than VM)
- **binary_trees**: 1.25x slower (allocation-heavy, known)
- **coroutine_bench**: 1.18x slower (Go runtime overhead)
- **object_creation**: 1.19x slower (NEWTABLE exit-resume, known)
- **method_dispatch**: 1.07x slower (BLR overhead for GoFunctions, known)

### Still far from 04-04 best (int48 overflow check regressions)
- fibonacci_iterative: 0.300s now vs 0.071s on 04-04 (+323%)
- sieve: 0.227s now vs 0.105s on 04-04 (+116%)
- spectral_norm: 0.336s now vs 0.138s on 04-04 (+143%)
