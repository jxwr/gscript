# Tier 1 Baseline JIT Benchmark Results

**Date**: 2026-03-31
**Platform**: Apple M4 Max, darwin/arm64, Go 1.25.7, LuaJIT 2.1
**Engine**: BaselineJITEngine — 1:1 bytecode→ARM64, inline field cache, GETGLOBAL cache, native BLR call, ArrayInt fast path

## Full Suite (22 benchmarks)

| # | Benchmark | VM | Tier1 JIT | T1/VM | LuaJIT |
|---|-----------|-----|-----------|-------|--------|
| 1 | fib(35) | 1.650s | 1.327s | **1.24x** | 0.026s |
| 2 | fib_recursive(35×10) | 16.318s | 13.381s | **1.22x** | -- |
| 3 | sieve(1M×3) | 0.244s | 0.205s | **1.19x** | 0.010s |
| 4 | mandelbrot(1000) | 1.394s | 0.365s | **3.82x** | 0.055s |
| 5 | ackermann(3,4×500) | 0.289s | 0.241s | **1.20x** | 0.006s |
| 6 | matmul(300) | 1.027s | 0.804s | **1.28x** | 0.022s |
| 7 | spectral_norm(500) | 0.996s | 0.320s | **3.11x** | 0.007s |
| 8 | nbody(500K) | 1.894s | 0.658s | **2.88x** | 0.033s |
| 9 | fannkuch(9) | 0.549s | 0.068s | **8.07x** | 0.019s |
| 10 | sort(50K×3) | 0.179s | 0.036s | **4.97x** | 0.010s |
| 11 | sum_primes(100K) | 0.026s | 0.006s | **4.33x** | 0.002s |
| 12 | mutual_recursion | 0.202s | 0.175s | **1.15x** | 0.005s |
| 13 | method_dispatch | 0.087s | 0.133s | 0.65x | <0.001s |
| 14 | closure_bench | 0.079s | 0.032s | **2.47x** | -- |
| 15 | string_bench | 0.043s | 0.019s | **2.26x** | -- |
| 16 | binary_trees(15) | 1.565s | 1.901s | 0.82x | -- |
| 17 | table_field_access | 0.732s | 0.117s | **6.26x** | -- |
| 18 | table_array_access | 0.399s | 0.129s | **3.09x** | -- |
| 19 | coroutine_bench | 16.467s | 15.802s | **1.04x** | -- |
| 20 | fibonacci_iterative | 1.012s | 0.209s | **4.84x** | -- |
| 21 | math_intensive | 0.903s | 0.183s | **4.93x** | -- |
| 22 | object_creation | 0.632s | 0.979s | 0.65x | -- |

**Faster than VM: 19/22**

## Summary

**>2x (12 benchmarks):**
fannkuch 8.07x, table_field_access 6.26x, sort 4.97x, math_intensive 4.93x, fibonacci_iterative 4.84x, sum_primes 4.33x, mandelbrot 3.82x, spectral_norm 3.11x, table_array_access 3.09x, nbody 2.88x, closure_bench 2.47x, string_bench 2.26x

**1-2x (7 benchmarks):**
matmul 1.28x, fib 1.24x, fib_recursive 1.22x, ackermann 1.20x, sieve 1.19x, mutual_recursion 1.15x, coroutine_bench 1.04x

**<1x (3 benchmarks):**
method_dispatch 0.65x, object_creation 0.65x, binary_trees 0.82x — all dominated by NEWTABLE exit-resume

## Optimizations in Tier 1

| Optimization | Round | Impact |
|---|---|---|
| ExecContext pool + escapeToHeap | R1 | Eliminate per-call allocation |
| BaselineReturnValue | R1 | Fix upvalue corruption |
| EQ int/float fallback | R1 | Fix mixed-type comparison |
| Inline field cache (FieldCache) | R2 | table_field 0.36x→6.26x |
| ARM64 double-scaling fix | R2 | Enable inline cache |
| handleCallFast | R2 | Skip CallValue/vm.call |
| GETGLOBAL per-PC cache | R3 | Eliminate GETGLOBAL exit |
| HasFieldOps skip | R3 | Reduce per-exit overhead |
| Native BLR CALL | R4 | fib 0.74x→1.24x, spectral 0.95x→3.11x |
| Two entry points (normal+direct) | R4 | 96B frame vs 16B frame |
| B=0/C=0 native BLR | R4 | Enable variable arg/ret calls |
| EQ label mismatch fix | R5 | sum_primes 0.84x→4.33x, math 1.18x→4.93x |
| ArrayInt GETTABLE/SETTABLE | R5 | fannkuch 0.65x→8.07x, table_array 0.92x→3.09x |
| BLR auto-disable on callee exit | R5 | sort ERROR→4.97x |
| NativeCallDepth limit | R5 | Prevent native stack overflow |
| ClosurePtr + GlobalCache save/restore | R5 | Fix cross-function corruption |

## Known Remaining Issues

- **method_dispatch (0.65x)**: Many small calls + NEWTABLE exit per iteration
- **object_creation (0.65x)**: ~2M NEWTABLE exits dominate execution
- **binary_trees (0.82x)**: NEWTABLE + recursive calls
- **BLR-to-Go blocked**: Go's morestack can't handle JIT frames, preventing NEWTABLE BL-to-helper
- **fib BLR auto-disabled**: When quicksort disables fib's BLR (per-process global state), fib drops from ~10x to ~1.2x
