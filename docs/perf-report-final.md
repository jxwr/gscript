# GScript Final Performance Report

**Date**: 2026-03-28
**Platform**: Apple M4 Max, macOS 15.2, Darwin 24.2.0, arm64
**Go version**: go1.25.7 darwin/arm64
**LuaJIT version**: LuaJIT 2.1.1772148810

---

## Method JIT Micro-Benchmarks

From `go test ./internal/methodjit/ -bench=. -benchtime=3s -count=1`.
Method JIT compiles bytecode to native ARM64 via CFG SSA IR. VM is the bytecode interpreter.

### Method JIT vs VM (Direct Paired Benchmarks)

| Benchmark | Method JIT (ns/op) | VM (ns/op) | Speedup |
|---|---:|---:|---:|
| **RetConst** `return 42` | 32.64 | 32.82 | 1.01x |
| **Add** `a + b` (int) | 34.30 | 34.79 | 1.01x |
| **Sub** `a - b` (int) | 31.44 | 34.86 | 1.11x |
| **Mul** `a * b` (int) | 32.00 | 38.36 | 1.20x |
| **Div** `a / b` (int->float) | 31.95 | 35.35 | 1.11x |
| **FloatAdd** `a + b` (float) | 31.94 | 40.33 | 1.26x |
| **Neg** `-a` (int) | 40.66 | 41.09 | 1.01x |
| **MulChain** `a*b*a*b` | 32.37 | 42.72 | 1.32x |
| **Branch** `if n > 0` | 31.89 | 47.21 | 1.48x |
| **NestedIf** `if > 10 > 20` | 32.38 | 47.01 | 1.45x |
| **Sum10** `for 1..10` | 44.82 | 155.3 | 3.46x |
| **Sum100** `for 1..100` | 182.0 | 1,053 | 5.79x |
| **Sum1000** `for 1..1000` | 1,339 | 9,849 | 7.36x |
| **Sum10000** `for 1..10000` | 12,958 | 99,547 | 7.68x |

### IR Interpreter Benchmarks (Reference)

| Benchmark | IR Interp (ns/op) | VM (ns/op) | IR Interp vs VM |
|---|---:|---:|---:|
| Add | 43.80 | 35.58 | 0.81x (slower) |
| Fib10 | 75,577 | 10,048 | 0.13x (slower) |
| Fib20 | 10,287,327 | 1,246,042 | 0.12x (slower) |
| Sum100 | 9,467 | 1,048 | 0.11x (slower) |
| Sum10000 | 912,826 | 98,758 | 0.11x (slower) |
| NestedIf | 85.08 | 46.91 | 0.55x (slower) |
| MulChain | 70.41 | 46.26 | 0.66x (slower) |
| ForLoop10 | 1,218 | 155.5 | 0.13x (slower) |

The IR interpreter is a correctness oracle, not a performance tier. It is 5-10x slower than the VM, confirming the native codegen provides real value.

### Compiled Emission Benchmark

| Benchmark | EmitReg (ns/op) | VM (ns/op) | Speedup |
|---|---:|---:|---:|
| Sum10000 | 12,963 | 98,758 | 7.62x |

EmitReg (register-allocated ARM64 emission) matches the Method JIT numbers exactly, confirming no overhead in the tiering path.

### Key Observations

- **Single-op functions** (Add, RetConst, Neg) show ~1.0-1.1x speedup. Per-call overhead (register slice allocation) dominates; the Method JIT's advantage is consumed by prologue/epilogue cost for trivial functions.

- **Multi-op arithmetic** (MulChain, FloatAdd) reaches 1.2-1.3x as native instructions amortize call overhead.

- **Branching** (Branch, NestedIf) shows 1.4-1.5x. The VM's opcode dispatch loop pays a per-branch tax that native code eliminates.

- **Loops are the clear win**: 3.5x at 10 iterations, scaling to **7.7x at 10,000 iterations**. The native loop body runs at ~1.3 ns/iteration vs ~10 ns/iteration in the VM. This is up from M6's 2.9x, a significant improvement.

---

## Full Benchmark Suite

### GScript VM vs Trace JIT vs LuaJIT

| Benchmark | VM | Trace JIT | LuaJIT | JIT/VM | LuaJIT/JIT |
|---|---:|---:|---:|---:|---:|
| **fib** (recursive fib(35)) | 1.703s | 2.032s | 0.025s | 0.84x | 81x faster |
| **sieve** (3 reps) | 0.250s | 0.163s | 0.011s | 1.53x | 15x faster |
| **mandelbrot** | 1.772s | 0.298s | 0.056s | 5.94x | 5.3x faster |
| **matmul** (matrix multiply) | 1.285s | 0.317s | 0.021s | 4.05x | 15x faster |
| **spectral_norm** | 1.087s | 1.098s | 0.008s | 0.99x | 137x faster |
| **nbody** | 1.974s | 0.328s | 0.034s | 6.02x | 9.6x faster |
| **fannkuch** | 0.587s | 0.497s | 0.020s | 1.18x | 25x faster |
| **sort** | 0.183s | 0.186s | 0.010s | 0.98x | 19x faster |
| **sum_primes** | 0.028s | 0.030s | 0.002s | 0.93x | 15x faster |
| **binary_trees** | 1.662s | 1.714s | n/a | 0.97x | n/a |
| **table_field_access** | 0.754s | 0.067s | n/a | 11.25x | n/a |
| **table_array_access** | 0.418s | 0.145s | n/a | 2.88x | n/a |
| **fibonacci_iterative** | 1.131s | 0.422s | n/a | 2.68x | n/a |
| **math_intensive** | 0.958s | 0.757s | n/a | 1.27x | n/a |

### Additional GScript Benchmarks

| Benchmark | VM | Trace JIT | JIT/VM |
|---|---:|---:|---:|
| **ackermann** | 0.290s | 0.293s | 0.99x |
| **coroutine_bench** | 4.817s | 4.828s | 1.00x |
| **fib_recursive** (10 reps) | 16.921s | 17.694s | 0.96x |
| **method_dispatch** | 0.087s | 0.090s | 0.97x |
| **mutual_recursion** | 0.207s | 0.214s | 0.97x |
| **object_creation** | 0.630s | 0.635s | 0.99x |

### LuaJIT Full Results

| Benchmark | LuaJIT |
|---|---:|
| ackermann | 0.006s |
| fannkuch | 0.020s |
| fib | 0.025s |
| fn_calls | 0.002s |
| mandelbrot | 0.056s |
| matmul | 0.021s |
| method_dispatch | 0.000s |
| mutual_recursion | 0.004s |
| nbody | 0.034s |
| sieve | 0.011s |
| sort | 0.010s |
| spectral_norm | 0.008s |
| sum_primes | 0.002s |

---

## Analysis

### Where the Trace JIT Helps

The Trace JIT delivers strong speedups on **loop-heavy numeric workloads**:

1. **table_field_access: 11.3x** -- The trace JIT excels at inlining table field lookups within hot loops, eliminating hash table dispatch overhead.
2. **nbody: 6.0x** -- Numeric simulation with tight inner loops and floating-point arithmetic. The JIT compiles the hot loop to native ARM64 with register-allocated FP ops.
3. **mandelbrot: 5.9x** -- Nested loops with float arithmetic and early-exit branching. The trace captures the inner loop including the escape test.
4. **matmul: 4.1x** -- Triple-nested loop with array access and float multiply-accumulate.
5. **table_array_access: 2.9x** -- Array-style table indexing within loops.
6. **fibonacci_iterative: 2.7x** -- Simple loop with variable swaps.
7. **sieve: 1.5x** -- Loop with table array writes and conditional logic.

### Where the Trace JIT Does NOT Help

1. **Recursive functions** (fib, ackermann, fib_recursive, mutual_recursion): The Trace JIT does not trace across function calls. Recursive workloads stay in the interpreter. fib actually runs *slower* with JIT enabled (2.032s vs 1.703s) due to tracing overhead with no payoff.

2. **Object-heavy workloads** (binary_trees, object_creation): Table allocation dominates. The JIT cannot optimize GC allocation.

3. **Coroutines** (coroutine_bench): Coroutine yield/resume is not JIT-compiled.

4. **spectral_norm**: Despite being numeric, the function-call-heavy structure (eval_A called millions of times) prevents effective tracing. 1.087s VM vs 1.098s JIT -- no benefit.

5. **sort**: Comparison-heavy with function callbacks. The trace cannot inline comparison functions.

### Method JIT vs Trace JIT

The Method JIT (micro-benchmarks) and Trace JIT (full suite) target different workloads:

- **Method JIT** compiles entire functions to native code. It excels at tight loops within a single function (7.7x on Sum10000). It currently deoptimizes on function calls, table ops, globals, and closures.

- **Trace JIT** records and compiles hot execution paths across bytecodes. It excels at inner loops with table access (11.3x on table_field_access) and floating-point computation (6.0x on nbody).

Neither JIT currently handles recursive functions, which is the single largest gap vs LuaJIT.

### Gap to LuaJIT

LuaJIT is 5-137x faster than GScript's best JIT output across comparable benchmarks. The biggest gaps:

| Gap | Benchmark | Reason |
|---:|---|---|
| 137x | spectral_norm | LuaJIT inlines function calls; GScript cannot |
| 81x | fib | LuaJIT compiles recursion; GScript cannot |
| 25x | fannkuch | LuaJIT optimizes array permutations deeply |
| 19x | sort | LuaJIT inlines comparison callbacks |
| 15x | sieve, matmul, sum_primes | LuaJIT's register allocation + type specialization |
| 5-10x | mandelbrot, nbody | Closest benchmarks -- GScript's FP loop compilation works |

The primary bottlenecks, in priority order:
1. **No function call compilation** -- Recursive and call-heavy benchmarks stay fully interpreted
2. **No inlining** -- Even simple helper functions force deoptimization
3. **Per-call overhead** -- Method JIT allocates register slices on every call (~20ns)
4. **No advanced loop optimizations** -- LICM, unrolling, vectorization absent
5. **NaN-boxing overhead** -- Every operation unboxes/reboxes through memory

---

## Summary

| Tier | Best Speedup | Typical Range | Limitation |
|---|---:|---|---|
| Method JIT (micro) | 7.7x (Sum10000) | 1.0-1.5x single-op, 3-8x loops | No calls, no tables |
| Trace JIT (suite) | 11.3x (table_field) | 1.0-6.0x | No recursion, no call inlining |
| vs LuaJIT | -- | 5-137x behind | Full optimizing compiler gap |

The JIT infrastructure is architecturally sound. The next high-leverage work is **function call compilation** in the Method JIT (eliminating the single largest class of deoptimizations) and **inlining** (collapsing call-heavy benchmarks like spectral_norm from 137x behind to competitive range).
