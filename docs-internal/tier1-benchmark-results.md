# Tier 1 Baseline JIT Benchmark Results

**Date**: 2026-03-30
**Platform**: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1

## Full Suite — Round 4 (native BLR CALL + inline cache + GETGLOBAL cache)

| # | Benchmark | VM | Tier1 | T1/VM | R3 | R1 |
|---|-----------|-----|-------|-------|----|-----|
| 1 | fib(35) | 1.553s | 0.139s | **11.17x** | 1.31x | 0.74x |
| 2 | fib_recursive(35×10) | 15.475s | 1.265s | **12.23x** | 1.25x | 0.73x |
| 3 | sieve(1M×3) | 0.237s | 0.201s | 1.18x | 1.22x | 1.22x |
| 4 | mandelbrot(1000) | 1.349s | 0.355s | **3.80x** | 3.91x | 3.82x |
| 5 | ackermann(3,4×500) | 0.281s | 0.295s | 0.95x | 0.97x | 0.95x |
| 6 | matmul(300) | 1.006s | 0.792s | 1.27x | 1.28x | 1.64x |
| 7 | spectral_norm(500) | 0.982s | 0.290s | **3.39x** | 1.37x | 0.95x |
| 8 | nbody(500K) | 1.866s | 0.630s | **2.96x** | 3.02x | 0.38x |
| 9 | fannkuch(9) | 0.547s | 0.544s | 1.01x | 1.00x | 1.01x |
| 10 | sort(50K×3) | 0.179s | ERROR | -- | 0.79x | 0.79x |
| 11 | sum_primes(100K) | 0.026s | 0.031s | 0.84x | 0.87x | 0.85x |
| 12 | mutual_recursion | 0.193s | 0.204s | 0.95x | 0.96x | 0.96x |
| 13 | method_dispatch | 0.086s | 0.132s | 0.65x | 0.83x | 0.59x |
| 14 | closure_bench | 0.085s | 0.032s | **2.66x** | 1.17x | 0.95x |
| 15 | string_bench | 0.043s | 0.020s | **2.15x** | 2.05x | 2.14x |
| 16 | binary_trees(15) | 1.583s | 1.663s | 0.95x | 0.96x | 0.96x |
| 17 | table_field_access | 0.731s | 0.114s | **6.41x** | 6.42x | 0.36x |
| 18 | table_array_access | 0.402s | 0.422s | 0.95x | 0.98x | 1.02x |
| 19 | coroutine_bench | 4.904s | 5.442s | 0.90x | 0.95x | 0.96x |
| 20 | fibonacci_iterative | 1.023s | 0.217s | **4.71x** | 4.19x | 3.42x |
| 21 | math_intensive | 0.898s | 0.763s | 1.18x | 1.18x | 1.16x |
| 22 | object_creation | 0.622s | 0.947s | 0.66x | 0.88x | 0.61x |

**>=2x: 9/22 | >=1x: 13/22**

## Progress Summary (R1 → R4)

| Category | R1 (start) | R4 (current) | Best |
|----------|-----------|-------------|------|
| **>=2x** | 3 | **9** | mandelbrot, fib_iter, table_field, fib, fib_rec, spectral, nbody, closure, string |
| **>=1x** | 8 | **13** | + sieve, matmul, fannkuch, math_intensive |
| **<1x** | 14 | **9** | sort(ERROR), ack, sum_primes, mutual_rec, method_disp, binary_trees, table_arr, coroutine, object_creation |

## Optimizations Applied

1. **R1**: Correctness foundation (upvalue fix, EQ float, ExecContext pool, escapeToHeap)
2. **R2**: Inline field cache + direct JIT call (Go-side handleCallFast)
3. **R3**: Native GETGLOBAL cache + HasFieldOps skip
4. **R4**: **Native ARM64 BLR call** (two entry points per function, B=0/C=0 support, register file bounds check)

## Remaining Issues

| Issue | Impact | Root Cause |
|-------|--------|-----------|
| sort ERROR | Crash | Stack overflow — native call bounds check may not be working |
| method_dispatch 0.65x | Regression from 0.83x | Native call overhead for many small calls? |
| object_creation 0.66x | Regression from 0.88x | Same — many small NEWTABLE + CALL |
| ackermann 0.95x | Not improved | B=0/C=0 native BLR may not be working correctly |
| mutual_recursion 0.95x | Not improved | Same root cause as ackermann |
| compute benchmarks ~1.2x | Below 2x target | Need register pinning for loop variables |
