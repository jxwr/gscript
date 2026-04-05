## Benchmark Results — 2026-04-05

Commit: 1932fbbb (jit-v3-clean)

| Benchmark            | VM       | JIT      | LuaJIT   | JIT/VM | JIT/LuaJIT |
|----------------------|----------|----------|----------|--------|------------|
| fib                  | 1.621s   | 1.387s   | 0.023s   | 0.86x  | 60.3x      |
| fib_recursive        | 16.116s  | 13.919s  | N/A      | 0.86x  | N/A        |
| sieve                | 0.238s   | 0.230s   | 0.010s   | 0.97x  | 23.0x      |
| mandelbrot           | 1.341s   | 0.378s   | 0.052s   | 0.28x  | 7.3x       |
| ackermann            | 0.282s   | 0.253s   | 0.006s   | 0.90x  | 42.2x      |
| matmul               | 1.002s   | 0.801s   | 0.021s   | 0.80x  | 38.1x      |
| spectral_norm        | 0.998s   | 0.329s   | 0.007s   | 0.33x  | 47.0x      |
| nbody                | 1.867s   | 0.598s   | 0.033s   | 0.32x  | 18.1x      |
| fannkuch             | 0.549s   | 0.070s   | 0.019s   | 0.13x  | 3.7x       |
| sort                 | 0.177s   | 0.048s   | 0.010s   | 0.27x  | 4.8x       |
| sum_primes           | 0.027s   | 0.004s   | 0.002s   | 0.15x  | 2.0x       |
| mutual_recursion     | 0.199s   | 0.184s   | 0.005s   | 0.92x  | 36.8x      |
| method_dispatch      | 0.085s   | 0.099s   | ~0.000s  | 1.16x  | N/A        |
| closure_bench        | 0.085s   | 0.026s   | -        | 0.31x  | -          |
| string_bench         | 0.041s   | 0.032s   | -        | 0.78x  | -          |
| binary_trees         | 1.570s   | 2.002s   | -        | 1.27x  | -          |
| table_field_access   | 0.722s   | 0.070s   | N/A      | 0.10x  | -          |
| table_array_access   | 0.408s   | 0.131s   | N/A      | 0.32x  | -          |
| coroutine_bench      | 15.147s  | 12.922s  | N/A      | 0.85x  | -          |
| fibonacci_iterative  | 1.018s   | 0.282s   | N/A      | 0.28x  | -          |
| math_intensive       | 0.918s   | 0.185s   | N/A      | 0.20x  | -          |
| object_creation      | 0.636s   | 0.749s   | N/A      | 1.18x  | -          |

## Top 3 Gaps vs LuaJIT
1. **fib** — 60.3x slower (1.387s vs 0.023s, gap 1.364s). JIT barely faster than VM (0.86x). Recursive call overhead dominates.
2. **matmul** — 38.1x slower (0.801s vs 0.021s, gap 0.780s). JIT only 0.80x of VM. Nested-loop table access, likely GETTABLE/SETTABLE bounds-check overhead.
3. **spectral_norm** — 47.0x slower (0.329s vs 0.007s, gap 0.322s). JIT good vs VM (0.33x). Float-heavy hot loop — suspected FPR spill / guard overhead per known-issues.md.

## Honorable mentions
- **ackermann** (42.2x), **mutual_recursion** (36.8x): deep recursive call overhead; same class as fib.
- **nbody** (18.1x, gap 0.565s): float-heavy with function calls; large absolute gap.
- **binary_trees** (1.27x regression vs VM), **object_creation** (1.18x regression): JIT hurts allocation-heavy code.
