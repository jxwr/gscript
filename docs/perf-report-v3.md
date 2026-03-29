# GScript JIT v3 Performance Report

Date: 2026-03-30

## 1. Platform

| Property | Value |
|----------|-------|
| CPU | Apple M4 Max (16 cores) |
| OS | macOS 15.2 (Darwin 24.2.0) |
| Go | go1.25.7 darwin/arm64 |
| LuaJIT | 2.1.1772148810 |
| Architecture | ARM64 |
| Branch | jit-v3-clean |

## 2. Micro-benchmarks: VM vs Tier 1 vs Tier 2 vs Tier 3

All numbers from `go test -bench -benchtime=3s`. Tier 2 = IR pipeline + memory-to-memory emit.
Tier 3 = IR pipeline + register allocation + emit.

| Benchmark | VM (ns/op) | Tier 1 (ns/op) | Tier 2 (ns/op) | Tier 3 (ns/op) | Best speedup vs VM |
|-----------|-----------|----------------|----------------|----------------|-------------------|
| Sum(100) | 1,030 | 248 | 383 | 323 | 4.15x (T1) |
| Sum(1000) | 9,935 | 2,236 | 3,397 | 2,766 | 4.44x (T1) |
| Sum(10000) | 99,308 | 19,079 | 33,847 | 27,228 | 5.21x (T1) |
| Add(3,4) | 33.5 | 35.0 | 29.5 | 29.3 | 1.14x (T3) |
| Fib(10) | 9,934 | 14,648 | -- | -- | 0.68x (T1 slower) |
| Branch | 46.6 | 34.2 | 30.7 | 30.6 | 1.52x (T3) |
| FloatAdd | 35.3 | 35.8 | 29.8 | 29.7 | 1.19x (T3) |

### Notes

- **Fib(10)**: Tier 1 is 1.47x *slower* than VM due to call overhead in the baseline JIT. Tier 2/3 omitted because standalone `Execute()` does not handle recursive calls (would need call-exit support).
- **Sum(N)**: Tier 1 dominates because the baseline JIT runs the full loop in native code via the VM + engine integration, avoiding per-call overhead. Tier 2/3 show the pure compiled-function overhead without VM integration.
- **Add, Branch, FloatAdd**: Tier 2 and Tier 3 are nearly identical (~29-31 ns/op), both faster than VM (~33-47 ns/op). The overhead is dominated by function call setup, not computation.
- **Tier 2 vs Tier 3**: Register allocation (Tier 3) provides a 10-20% improvement over memory-to-memory (Tier 2) on loop-heavy workloads. Minimal difference on single-op benchmarks.

## 3. Full Benchmark Suite: VM vs Tier 1 (CLI) vs LuaJIT

All suite benchmarks run via the CLI (`/tmp/gs_v3 -vm` / `-jit`). Timeout = 20s.

| Benchmark | VM | JIT (Tier 1) | LuaJIT | JIT speedup | vs LuaJIT |
|-----------|-----|-------------|--------|-------------|-----------|
| fib | 1.707s | 1.383s | 0.025s | 1.23x | 55x slower |
| sieve | 0.249s | 0.251s | 0.011s | 1.00x | 23x slower |
| mandelbrot | 1.395s | 1.433s | 0.058s | 0.97x | 25x slower |
| ackermann | 0.297s | 0.001s | 0.006s | 297x | 6x faster |
| matmul | 1.059s | 1.045s | 0.021s | 1.01x | 50x slower |
| spectral_norm | 1.022s | 1.004s | 0.008s | 1.02x | 126x slower |
| nbody | 1.890s | HANG | 0.033s | -- | -- |
| fannkuch | 0.563s | 0.563s | 0.020s | 1.00x | 28x slower |
| sort | 0.189s | HANG | 0.011s | -- | -- |
| sum_primes | 0.027s | 0.065s | 0.002s | 0.42x | 33x slower |
| mutual_recursion | 0.219s | 0.008s | 0.005s | 27x | 1.6x slower |
| method_dispatch | 0.098s | 0.110s | 0.000s | 0.89x | -- |
| closure_bench | NO_TIME | NO_TIME | -- | -- | -- |
| string_bench | NO_TIME | NO_TIME | -- | -- | -- |
| binary_trees | 1.621s | 2.384s | -- | 0.68x | -- |
| table_field_access | 0.730s | 0.150s | -- | 4.87x | -- |
| table_array_access | 0.428s | 0.415s | -- | 1.03x | -- |
| coroutine_bench | 4.728s | 4.720s | -- | 1.00x | -- |
| fibonacci_iterative | 1.022s | 0.174s | -- | 5.87x | -- |
| math_intensive | 0.923s | HANG | -- | -- | -- |
| object_creation | 0.642s | 0.755s | -- | 0.85x | -- |
| fib_recursive | 16.286s | 18.446s | -- | 0.88x | -- |

### Legend

- **HANG**: Process did not complete within 20s timeout (known JIT bugs)
- **NO_TIME**: Benchmark ran but did not print a "Time:" line
- **JIT speedup**: VM_time / JIT_time (>1 means JIT is faster)
- **vs LuaJIT**: How much slower GScript JIT is compared to LuaJIT

## 4. Analysis

### What each tier improves

**Tier 1 (Baseline JIT)**:
- Best at loop-heavy integer code when running through the VM integration (Sum, fibonacci_iterative, table_field_access)
- Exceptional on call-heavy patterns where the JIT can short-circuit: ackermann (297x), mutual_recursion (27x)
- Breaks even or slightly worse on floating-point heavy benchmarks (mandelbrot, spectral_norm)
- Significantly worse on deep recursion (fib_recursive, binary_trees) due to JIT call overhead

**Tier 2 (Memory-to-memory JIT)**:
- Eliminates interpreter dispatch overhead for straight-line code
- 10-15% faster than VM on single operations (Add, FloatAdd, Branch)
- No call support in standalone mode limits applicability

**Tier 3 (Register-allocated JIT)**:
- 10-20% faster than Tier 2 on loop-heavy code (register allocation avoids memory round-trips)
- Negligible difference on short functions (call overhead dominates)
- Sum(10000): 27,228 ns/op vs Tier 2's 33,847 ns/op = 1.24x improvement from regalloc

### Where gaps remain

1. **LuaJIT gap is 20-130x** on most benchmarks. LuaJIT's trace JIT with type specialization, loop unrolling, and SSA optimization is in a different league.

2. **Three benchmarks HANG in JIT mode**: nbody, sort, math_intensive. These likely trigger infinite loops or stack overflows in the baseline JIT's exit-resume mechanism.

3. **JIT regressions**: Several benchmarks are *slower* with JIT (binary_trees 0.68x, fib_recursive 0.88x, sum_primes 0.42x, object_creation 0.85x). The baseline JIT's call/exit overhead exceeds the benefit for these patterns.

4. **No Tier 2/3 integration with VM**: The optimizing tiers (2, 3) can only run standalone functions without calls or globals. Integrating them into the tiered execution would unlock their potential on real workloads.

5. **Float-heavy benchmarks see no JIT benefit**: mandelbrot, spectral_norm, matmul all run at ~1.0x. The baseline JIT doesn't optimize float operations.

### Key wins

- **ackermann**: 297x speedup -- the JIT eliminates VM dispatch on deeply recursive tail-call-like patterns
- **mutual_recursion**: 27x speedup -- same mechanism
- **fibonacci_iterative**: 5.87x -- loop body runs entirely in native code
- **table_field_access**: 4.87x -- inline cache hit for field access

## 5. Summary: Geometric Mean Speedup

### Micro-benchmarks (vs VM)

| Tier | Geo mean speedup | Benchmarks included |
|------|-----------------|-------------------|
| Tier 1 | 1.88x | Sum100/1000/10000, Add, Fib10, Branch, FloatAdd |
| Tier 2 | 1.90x | Sum100/1000/10000, Add, Branch, FloatAdd (no Fib) |
| Tier 3 | 2.10x | Sum100/1000/10000, Add, Branch, FloatAdd (no Fib) |

*Tier 1 geo mean includes Fib10 where it is 0.68x (slower). Without Fib10, Tier 1 = 2.23x.*

### Full suite (CLI, VM vs JIT Tier 1)

Excluding HANG and NO_TIME benchmarks:

| Metric | Value |
|--------|-------|
| Benchmarks faster with JIT | 7 of 16 |
| Benchmarks slower with JIT | 6 of 16 |
| Benchmarks ~same (0.97-1.03x) | 3 of 16 |
| Maximum speedup | 297x (ackermann) |
| Maximum regression | 0.42x (sum_primes) |
| Geometric mean (all 16) | **1.98x** |
| Geometric mean (excl. ackermann outlier) | **1.42x** |

### GScript JIT vs LuaJIT

| Metric | Value |
|--------|-------|
| Benchmarks compared | 9 (excluding method_dispatch: too small to measure) |
| Geometric mean slowdown | **15.4x slower** |
| Best case | ackermann: 6x faster than LuaJIT |
| Worst case | spectral_norm: 126x slower |

The path to competing with LuaJIT requires integrating Tier 2/3 with the VM execution loop, adding type-specialized float arithmetic, loop optimizations, and function inlining. The current architecture has the right pieces; they need to be connected end-to-end.
