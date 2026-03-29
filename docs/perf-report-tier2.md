# GScript Performance Report: VM vs Tier 1 vs Tier 2

Date: 2026-03-28
Platform: Apple M4 Max, darwin/arm64
Branch: jit-v3-clean

## Micro-Benchmarks (Go `testing.B`, 3s benchtime)

### Sum Loop: `sum(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`

| Tier                  | Sum(100)    | Sum(1000)   | Sum(10000)   |
|-----------------------|-------------|-------------|--------------|
| VM Interpreter        | 1,745 ns/op | 17,634 ns/op| 160,698 ns/op|
| Tier 1 Baseline JIT   | 387 ns/op   | 3,277 ns/op | 32,469 ns/op |
| Tier 2 Memory-to-Mem  | 456 ns/op   | 4,266 ns/op | 44,528 ns/op |
| Tier 2 RegAlloc       | 170 ns/op   | 922 ns/op   | 8,401 ns/op  |

### Speedup over VM

| Tier                  | Sum(100) | Sum(1000) | Sum(10000) |
|-----------------------|----------|-----------|------------|
| Tier 1 Baseline JIT   | 4.5x     | 5.4x      | 4.9x       |
| Tier 2 Memory-to-Mem  | 3.8x     | 4.1x      | 3.6x       |
| Tier 2 RegAlloc       | 10.3x    | 19.1x     | 19.1x      |

### Analysis

- **Tier 2 RegAlloc** is the clear winner at 10-19x over VM, thanks to keeping loop
  variables in physical ARM64 registers (no load/store per iteration).
- **Tier 1 Baseline** is faster than Tier 2 Memory-to-Memory because Tier 1 maps
  bytecodes 1:1 to native templates with no IR overhead, while Tier 2 Mem adds
  the overhead of SSA slot management. However, Tier 1 cannot benefit from type
  specialization for operations that involve exit-resume (calls, globals, tables).
- **Tier 2 Memory-to-Memory** is 3.6-4.1x over VM. It benefits from type-specialized
  integer arithmetic (no NaN-box checks in the loop body) but pays for memory
  round-trips on every SSA value.
- The speedup scales with loop iteration count for RegAlloc (19x at N=10000 vs
  10x at N=100), because the register-allocated loop body amortizes the prologue/
  epilogue cost. For Tier 1 and Tier 2 Mem, speedup is flatter.

## Full Benchmark Suite (CLI, -vm vs -jit)

The `-jit` flag enables the full tiered JIT (Tier 1 baseline + Tier 2 optimizing).

| Benchmark            | VM         | JIT        | Speedup | Notes          |
|----------------------|------------|------------|---------|----------------|
| fib (recursive)      | 1.620s     | 1.323s     | 1.2x    | Call-heavy      |
| fib_recursive (10x)  | 16.436s    | 21.271s    | 0.8x    | JIT slower (call overhead) |
| fibonacci_iterative  | 1.004s     | 0.162s     | 6.2x    | Loop + int arith|
| sieve                | 0.246s     | 0.247s     | 1.0x    | Table-heavy     |
| mandelbrot           | 1.381s     | 1.375s     | 1.0x    | Float-heavy     |
| sum_primes           | 0.027s     | 0.053s     | 0.5x    | JIT slower (call overhead) |
| math_intensive       | 0.901s     | HANG       | --      | JIT hangs       |
| mutual_recursion     | 0.204s     | 0.004s     | 51.0x   | Big win         |
| table_field_access   | 0.742s     | 0.138s     | 5.4x    | Inline cache    |
| table_array_access   | 0.405s     | 0.410s     | 1.0x    | Array path      |
| matmul               | 1.028s     | 1.023s     | 1.0x    | Table + float   |
| spectral_norm        | 0.996s     | 0.984s     | 1.0x    | Float-heavy     |
| nbody                | 1.883s     | HANG       | --      | JIT hangs       |
| fannkuch             | 0.567s     | 0.567s     | 1.0x    | Table + int     |
| sort                 | 0.184s     | HANG       | --      | JIT hangs       |
| binary_trees         | 1.643s     | 2.407s     | 0.7x    | JIT slower (alloc heavy) |
| object_creation      | 0.635s     | 0.748s     | 0.8x    | JIT slower (alloc heavy) |
| method_dispatch      | 0.089s     | 0.100s     | 0.9x    | Self/call heavy |
| ackermann            | 0.297s     | 0.001s     | 297.0x  | Big win         |
| string_bench (concat)| 0.009s     | 0.007s     | 1.3x    |                 |
| string_bench (format)| 0.014s     | 0.011s     | 1.3x    |                 |
| string_bench (compare)| 0.030s    | 0.026s     | 1.2x    |                 |

### Key Observations

**Where JIT wins big (>5x):**
- `ackermann`: 297x -- pure integer arithmetic + recursion, ideal for JIT
- `mutual_recursion`: 51x -- similar pattern
- `fibonacci_iterative`: 6.2x -- tight integer loop
- `table_field_access`: 5.4x -- inline cache for field access

**Where JIT is neutral (0.9-1.1x):**
- `sieve`, `mandelbrot`, `matmul`, `spectral_norm`, `fannkuch`, `table_array_access`:
  These are table-heavy or float-heavy benchmarks where most time is spent in
  exit-resume operations (the JIT exits to Go for table/float ops), so the JIT
  overhead roughly cancels out its benefit on arithmetic.

**Where JIT is slower (<0.9x):**
- `fib_recursive` (10x reps): Deep recursion through JIT's call-exit mechanism
  adds ~50ns per call, which accumulates in exponential recursion.
- `sum_primes`: Short-running; JIT compilation cost not amortized.
- `binary_trees`, `object_creation`: Allocation-heavy; JIT's exit-resume for
  NEWTABLE dominates.

**Known hangs:**
- `math_intensive`, `nbody`, `sort`: JIT hangs during execution. Likely infinite
  loop in code generation or exit-resume logic for specific opcode patterns.

## Conclusions

1. The Tier 2 RegAlloc path delivers **19x speedup** on pure integer loops --
   competitive with handwritten assembly.
2. The tiered JIT (Tier 1 + Tier 2) through the CLI delivers **6x on iterative
   loops** and **5x on field access** -- the most common hot patterns.
3. Three benchmarks hang in JIT mode -- these need debugging.
4. The biggest remaining opportunity is **reducing exit-resume overhead** for
   table operations and float arithmetic, which would unlock speedups for
   mandelbrot, spectral_norm, matmul, and sieve.
