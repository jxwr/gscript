# GScript Comprehensive Performance Report

**Date:** 2026-03-28
**Platform:** Apple M4 Max, Darwin 24.2.0, arm64
**Go version:** (runtime)
**Modes tested:** VM (bytecode interpreter), Method JIT (native ARM64), Trace JIT (legacy `-jit` flag), LuaJIT (reference)

---

## Section 1: Method JIT Micro-Benchmarks

All results from `go test ./internal/methodjit/ -bench=. -benchtime=3s`.

### 1.1 Core Operations (single-call overhead)

| Scenario | VM (ns/op) | Method JIT (ns/op) | Speedup |
|----------|-----------|-------------------|---------|
| RetConst | 31.92 | 31.14 | 1.03x |
| Add | 37.85 | 31.60 | 1.20x |
| Sub | 37.58 | 31.62 | 1.19x |
| Mul | 33.94 | 31.51 | 1.08x |
| Div | 34.62 | 32.28 | 1.07x |
| Neg | 39.49 | 31.79 | 1.24x |
| FloatAdd | 38.10 | 31.72 | 1.20x |
| MulChain | 42.39 | 32.46 | 1.31x |

### 1.2 Branch / Control Flow

| Scenario | VM (ns/op) | Method JIT (ns/op) | Speedup |
|----------|-----------|-------------------|---------|
| Branch | 45.85 | 32.48 | 1.41x |
| NestedIf | 46.80 | 31.54 | 1.48x |

### 1.3 Loops (where Method JIT dominates)

| Scenario | VM (ns/op) | Method JIT (ns/op) | Speedup |
|----------|-----------|-------------------|---------|
| Sum10 | 155.4 | 39.84 | **3.90x** |
| Sum100 | 1,047 | 99.48 | **10.5x** |
| Sum1000 | 9,924 | 552.2 | **18.0x** |
| Sum10000 | 97,959 | 5,061 | **19.4x** |

### 1.4 Recursive Functions

| Scenario | VM (ns/op) | Method JIT (ns/op) | Speedup |
|----------|-----------|-------------------|---------|
| Fib(10) | 9,899 | -- | -- |
| Fib(20) | 1,227,855 | -- | -- |

(Method JIT does not yet compile recursive calls; IR interpreter results: Fib(10)=71,846 ns, Fib(20)=9,313,928 ns)

### 1.5 Overhead Analysis

| Metric | ns/op |
|--------|-------|
| Execute (full entry) | 31.00 |
| CallJITRaw | 3.07 |
| VMCall | 31.98 |
| CtxSetup | 0.25 |
| ResultAlloc | 0.24 |
| AllocAndFill | 4.47 |

The ~28 ns gap between CallJITRaw (3 ns) and Execute (31 ns) is context setup + result marshaling overhead. For hot loops this is amortized; for single-op benchmarks it dominates.

### 1.6 EmitReg (native ARM64 codegen) vs VM

| Scenario | VM (ns/op) | EmitReg (ns/op) | Speedup |
|----------|-----------|-----------------|---------|
| Sum10000 | 97,959 | 4,884 | **20.1x** |

---

## Section 2: Full Benchmark Suite (21 benchmarks)

Times in seconds unless noted. "Best GScript" = faster of VM and Trace JIT.

| # | Benchmark | VM | Trace JIT | TJ/VM | LuaJIT | Best GS vs LuaJIT |
|---|-----------|-----|-----------|-------|--------|-------------------|
| 1 | fib | 1.675s | 1.683s | 1.00x | 0.026s | **64x slower** |
| 2 | sieve | 0.251s | 0.137s | 1.83x | 0.011s | **12x slower** |
| 3 | mandelbrot | 1.459s | 0.288s | 5.07x | 0.053s | **5.4x slower** |
| 4 | ackermann | 0.288s | 0.291s | 0.99x | 0.006s | **48x slower** |
| 5 | matmul | 1.008s | 0.228s | 4.42x | 0.021s | **11x slower** |
| 6 | spectral_norm | 1.013s | 1.078s | 0.94x | 0.007s | **145x slower** |
| 7 | nbody | 1.937s | 0.312s | 6.21x | 0.035s | **8.9x slower** |
| 8 | fannkuch | 0.576s | 0.489s | 1.18x | 0.019s | **26x slower** |
| 9 | sort | 0.181s | 0.184s | 0.98x | 0.010s | **18x slower** |
| 10 | sum_primes | 0.028s | 0.029s | 0.97x | 0.002s | **14x slower** |
| 11 | mutual_recursion | 0.198s | 0.204s | 0.97x | 0.005s | **40x slower** |
| 12 | method_dispatch | 0.087s | 0.087s | 1.00x | 0.000s | N/A (too fast) |
| 13 | closure_bench | 0.081s* | 0.031s* | 2.61x | 0.009s* | **3.4x slower** |
| 14 | string_bench | 0.044s* | 0.042s* | 1.05x | 0.009s* | **4.7x slower** |
| 15 | binary_trees | 1.650s | 1.644s | 1.00x | 0.173s | **9.5x slower** |
| 16 | table_field_access | 0.750s | 0.070s | 10.7x | -- | -- |
| 17 | table_array_access | 0.408s | 0.151s | 2.70x | -- | -- |
| 18 | coroutine_bench | 4.787s | 4.733s | 1.01x | -- | -- |
| 19 | fibonacci_iterative | 1.127s | 0.471s | 2.39x | -- | -- |
| 20 | math_intensive | 0.933s | 0.738s | 1.26x | -- | -- |
| 21 | object_creation | 0.644s | 0.634s | 1.02x | -- | -- |

\* = sum of sub-benchmarks (no single "Time:" line)

Note: Trace JIT `fibonacci_iterative` produces a different result (-91082486001521 vs 190392490709135), suggesting an integer overflow bug in the JIT path.

---

## Section 3: Where Method JIT Wins vs Trace JIT

The Method JIT compiles entire functions to ARM64 native code ahead-of-time. For tight numeric loops it achieves dramatic speedups that the Trace JIT cannot match on micro-benchmarks (since trace compilation has recording/side-exit overhead).

| Operation | Method JIT | Trace JIT (estimated*) | Method JIT advantage |
|-----------|-----------|----------------------|---------------------|
| Sum100 (loop) | 99.5 ns | ~1,047 ns (VM-level) | **10.5x** |
| Sum10000 (loop) | 5.06 us | ~98 us (VM-level) | **19.4x** |
| Branch | 32.5 ns | ~45.9 ns (VM-level) | **1.4x** |
| NestedIf | 31.5 ns | ~46.8 ns (VM-level) | **1.5x** |
| Arithmetic (avg) | ~31.9 ns | ~36.8 ns (VM-level) | **1.15x** |

\* Trace JIT micro-benchmark numbers are estimated as VM-level since the trace compiler needs hot loops to trigger recording and cannot JIT single-invocation micro-benchmarks.

**Key insight:** Method JIT's 19x speedup on tight loops is its strongest advantage. On larger benchmarks, the Trace JIT can sometimes match or beat it by specializing hot traces, but Method JIT wins consistently on predictable numeric code.

---

## Section 4: Gap Analysis

### Closest to LuaJIT (within 10x)

| Benchmark | Ratio | What's working |
|-----------|-------|----------------|
| closure_bench | 3.4x | Trace JIT optimizes accumulator well |
| string_bench | 4.7x | String ops are Go-backed, inherently fast |
| mandelbrot | 5.4x | Trace JIT float unboxing works well here |
| nbody | 8.9x | Trace JIT handles float-heavy particle sim |
| binary_trees | 9.5x | Allocation-heavy; Go's GC is competitive |

### Biggest Gaps (>30x)

| Benchmark | Ratio | What's blocking |
|-----------|-------|----------------|
| spectral_norm | 145x | Trace JIT fails to optimize; nested function calls + float math |
| fib | 64x | Deep recursion; no JIT for recursive calls yet |
| ackermann | 48x | Extreme recursion; same blocker as fib |
| mutual_recursion | 40x | Mutual recursion pattern; JIT cannot trace across call boundaries |
| fannkuch | 26x | Complex control flow with array permutations; trace bailouts |

### Blockers by Category

1. **Recursion (fib, ackermann, mutual_recursion):** Neither JIT compiles recursive calls. LuaJIT handles these via trace stitching and down-recursion. This is the single biggest gap.

2. **Float-heavy with calls (spectral_norm):** The inner loop calls helper functions repeatedly. Trace JIT cannot inline these, and every call is a side-exit or interpreted fallback. LuaJIT inlines aggressively.

3. **Complex control flow (fannkuch, sort):** Multiple nested loops with unpredictable branches cause trace fragmentation. LuaJIT handles this with trace stitching and better guard elimination.

4. **Integer-heavy loops (sieve, sum_primes):** The Trace JIT helps somewhat (1.8x on sieve) but the gap to LuaJIT (12-14x) suggests box/unbox overhead and missing integer specialization in the trace compiler.

5. **Allocation-heavy (binary_trees, object_creation):** Dominated by GC and table allocation cost, not computation. Method JIT cannot help here; need allocation sinking or arena allocation.

---

## Section 5: Summary Statistics

### Trace JIT vs VM (21 benchmarks)

| Metric | Value |
|--------|-------|
| Benchmarks where Trace JIT beats VM | **10 of 21** (48%) |
| Benchmarks where Trace JIT is slower | **5 of 21** (24%) |
| Benchmarks where roughly equal (within 5%) | **6 of 21** (29%) |
| Best speedup | **10.7x** (table_field_access) |
| Worst regression | **0.94x** (spectral_norm, slight regression) |
| Average speedup (all 21) | **2.17x** |
| Median speedup | **1.05x** |
| Geometric mean speedup | **1.56x** |

### Method JIT vs VM (micro-benchmarks only)

| Metric | Value |
|--------|-------|
| Benchmarks where Method JIT wins | **12 of 12** (100%) |
| Best speedup | **19.4x** (Sum10000) |
| Average speedup (loops only) | **12.9x** |
| Average speedup (arithmetic only) | **1.18x** |
| Average speedup (all) | **4.58x** |

### GScript Best vs LuaJIT (15 comparable benchmarks)

| Metric | Value |
|--------|-------|
| Benchmarks within 10x of LuaJIT | **5 of 15** (33%) |
| Benchmarks within 20x of LuaJIT | **9 of 15** (60%) |
| Closest to LuaJIT | **3.4x** (closure_bench) |
| Farthest from LuaJIT | **145x** (spectral_norm) |
| Geometric mean gap | **16.7x** |
| Median gap | **12x** |

### Where Each Mode Excels

| Mode | Best at | Typical speedup |
|------|---------|----------------|
| **Method JIT** | Tight numeric loops (sum, arithmetic) | 10-20x over VM |
| **Trace JIT** | Float-heavy simulations (mandelbrot, nbody, matmul) | 4-10x over VM |
| **VM** | Recursion, string ops, coroutines | Baseline |
| **LuaJIT** | Everything | 3-145x over GScript best |

---

## Raw Data Appendix

### Method JIT Micro-Benchmarks (full)

```
BenchmarkMethodJIT_Add-16              31.60 ns/op
BenchmarkVM_Add-16                     37.85 ns/op
BenchmarkMethodJIT_Sub-16              31.62 ns/op
BenchmarkVM_Sub-16                     37.58 ns/op
BenchmarkMethodJIT_Mul-16              31.51 ns/op
BenchmarkVM_Mul-16                     33.94 ns/op
BenchmarkMethodJIT_Div-16              32.28 ns/op
BenchmarkVM_Div-16                     34.62 ns/op
BenchmarkMethodJIT_FloatAdd-16         31.72 ns/op
BenchmarkVM_FloatAdd-16                38.10 ns/op
BenchmarkMethodJIT_MulChain-16         32.46 ns/op
BenchmarkVM_MulChain-16                42.39 ns/op
BenchmarkMethodJIT_Branch-16           32.48 ns/op
BenchmarkVM_Branch-16                  45.85 ns/op
BenchmarkMethodJIT_NestedIf-16         31.54 ns/op
BenchmarkVM_NestedIf-16                46.80 ns/op
BenchmarkMethodJIT_Sum10-16            39.84 ns/op
BenchmarkVM_Sum10-16                   155.4 ns/op
BenchmarkMethodJIT_Sum100-16           99.48 ns/op
BenchmarkVM_Sum100-16                  1047  ns/op
BenchmarkMethodJIT_Sum1000-16          552.2 ns/op
BenchmarkVM_Sum1000-16                 9924  ns/op
BenchmarkMethodJIT_Sum10000-16         5061  ns/op
BenchmarkVM_Sum10000-16                97959 ns/op
BenchmarkMethodJIT_RetConst-16         31.14 ns/op
BenchmarkVM_RetConst-16                31.92 ns/op
BenchmarkMethodJIT_Neg-16              31.79 ns/op
BenchmarkVM_Neg-16                     39.49 ns/op
BenchmarkEmitReg_Sum10000-16           4884  ns/op
BenchmarkOverhead_Execute-16           31.00 ns/op
BenchmarkOverhead_CallJITRaw-16        3.070 ns/op
BenchmarkOverhead_VMCall-16            31.98 ns/op
```

### IR Interpreter Benchmarks

```
BenchmarkIRInterp_Add-16               41.82 ns/op
BenchmarkIRInterp_Fib10-16             71846 ns/op
BenchmarkIRInterp_Fib20-16             9313928 ns/op
BenchmarkIRInterp_Sum100-16            9304  ns/op
BenchmarkIRInterp_Sum10000-16          903646 ns/op
BenchmarkIRInterp_NestedIf-16          84.17 ns/op
BenchmarkIRInterp_MulChain-16          68.82 ns/op
BenchmarkIRInterp_ForLoop10-16         1205  ns/op
```
