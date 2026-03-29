# GScript Method JIT Performance Report (M6)

**Date**: 2026-03-28
**Platform**: Apple M4 Max, macOS Darwin 24.2.0, arm64
**Go version**: go1.25.7 darwin/arm64
**LuaJIT version**: LuaJIT 2.1.1772148810

## Work Overview

GScript's Method JIT has been built across six milestones (M1--M6). M1 established
the CFG SSA IR and graph builder (bytecode to SSA via Braun et al. 2013). M2 added
the IR interpreter and structural validator. M3 introduced type feedback and a type
specialization pass. M4 implemented register allocation and ARM64 code generation
with a memory-to-memory strategy (all SSA values stored in NaN-boxed VM register
slots, loaded/stored around each instruction). M5 wired tiering and deoptimization:
unsupported operations (calls, globals, tables, strings) bail to the VM interpreter.
M6 added optimization passes (constant propagation, dead code elimination) and
float-aware arithmetic codegen. The Method JIT currently compiles integer/float
arithmetic, comparisons, branches, and for-loops to native ARM64. Function calls,
table operations, globals, and closures trigger deopt.

---

## Method JIT Micro-Benchmarks

All results from `go test -bench` with `-count=3`. Best (minimum) ns/op from 3 runs
reported. Method JIT allocates 136--152 B/op for the VM register slice; the VM reuses
its pre-allocated frame and shows 0 B/op.

| Benchmark | Method JIT | VM | Speedup | Notes |
|---|---:|---:|---:|---|
| **RetConst** `return 42` | 26.1 ns | 32.8 ns | 1.26x | Minimal function: prologue + store + epilogue |
| **Add** `a + b` (int) | 27.4 ns | 30.9 ns | 1.13x | Integer add, NaN-box/unbox overhead |
| **Sub** `a - b` (int) | 27.7 ns | 33.1 ns | 1.20x | Integer subtract |
| **Mul** `a * b` (int) | 26.7 ns | 35.1 ns | 1.31x | Integer multiply |
| **Div** `a / b` (int->float) | 27.0 ns | 32.7 ns | 1.21x | Division with int-to-float conversion |
| **FloatAdd** `a + b` (float) | 26.9 ns | 32.6 ns | 1.21x | Float addition via type-generic path |
| **Neg** `-a` (int) | 28.0 ns | 36.8 ns | 1.32x | Unary negate with int-check branch |
| **MulChain** `a*b*a*b` | 27.6 ns | 39.1 ns | 1.42x | Three multiplications chained |
| **Branch** `if n > 0` | 27.1 ns | 40.9 ns | 1.51x | Single branch + multiply |
| **NestedIf** `if > 10 > 20` | 26.7 ns | 44.5 ns | 1.67x | Two levels of branching |
| **Sum10** `for 1..10` | 121.5 ns | 173.6 ns | 1.43x | Short loop, 10 iterations |
| **Sum100** `for 1..100` | 396.8 ns | 1,083 ns | 2.73x | Medium loop, 100 iterations |
| **Sum1000** `for 1..1000` | 3,560 ns | 10,186 ns | 2.86x | Long loop, 1000 iterations |
| **Sum10000** `for 1..10000` | 35,192 ns | 100,865 ns | 2.87x | Tight loop, 10k iterations |

### Key Observations

- **Single-op functions** (Add, Mul, Div) show 1.1--1.3x speedup. The per-call
  overhead dominates: the Method JIT allocates a `[]runtime.Value` register slice
  (136 B, 2 allocs) on every `Execute` call. The VM reuses its stack frame. This
  overhead is ~20 ns/call and limits speedup for trivial functions.

- **Branching** benefits more (1.5--1.7x) because the VM's interpreter loop has
  higher per-opcode dispatch overhead that compounds through branches.

- **Loops** are the big win: **2.7--2.9x** for tight integer loops. The native
  ARM64 loop body (load, unbox, add, box, store, compare, branch) runs at ~3.5
  ns/iteration vs ~10 ns/iteration for the VM's bytecode dispatch loop.

- **Call overhead is the bottleneck.** The `Execute` method's `make([]Value, n)`
  allocation dominates short functions. Pre-allocating or pooling this slice could
  add 1.3--1.5x on top.

---

## IR Interpreter Comparison

The IR interpreter (Go-level SSA execution) is included for reference. It validates
correctness but is not competitive for performance.

| Benchmark | Method JIT | IR Interp | VM | JIT/IR Ratio |
|---|---:|---:|---:|---:|
| Add | 27 ns | 57 ns | 31 ns | 2.1x faster |
| NestedIf | 27 ns | 99 ns | 45 ns | 3.7x faster |
| MulChain | 28 ns | 71 ns | 39 ns | 2.5x faster |
| Sum100 | 397 ns | 11,224 ns | 1,083 ns | 28x faster |
| Sum10000 | 35,192 ns | 1,080,303 ns | 100,865 ns | 31x faster |

The Method JIT is 28--31x faster than the IR interpreter on loops, confirming the
value of native code generation over Go-level SSA interpretation.

---

## Full Benchmark Suite (VM / Trace JIT / LuaJIT)

These are the standard benchmark scripts. LuaJIT times are for equivalent Lua
implementations. Missing entries use a different output format or no Lua equivalent
exists.

| Benchmark | VM | Trace JIT | LuaJIT | Trace/VM | LuaJIT/VM |
|---|---:|---:|---:|---:|---:|
| fib (recursive) | 1.739 s | 1.708 s | 0.027 s | 1.0x | 64x |
| sieve | 0.248 s | 0.139 s | 0.013 s | 1.8x | 19x |
| mandelbrot | 1.462 s | 0.299 s | 0.064 s | 4.9x | 23x |
| ackermann | 0.296 s | 0.301 s | 0.010 s | 1.0x | 30x |
| matmul | 1.041 s | 0.225 s | 0.024 s | 4.6x | 43x |
| spectral_norm | 1.019 s | 1.089 s | 0.009 s | 0.9x | 113x |
| nbody | 1.966 s | 0.321 s | 0.038 s | 6.1x | 52x |
| fannkuch | 0.595 s | 0.507 s | 0.021 s | 1.2x | 28x |
| sort | 0.186 s | 0.189 s | 0.013 s | 1.0x | 14x |
| sum_primes | 0.029 s | 0.030 s | 0.002 s | 1.0x | 14x |
| mutual_recursion | 0.206 s | 0.208 s | 0.008 s | 1.0x | 26x |
| method_dispatch | 0.089 s | 0.092 s | 0.000 s | 1.0x | -- |
| binary_trees | 1.809 s | 2.348 s | -- | 0.8x | -- |
| table_field_access | 0.989 s | 0.082 s | -- | 12.1x | -- |
| table_array_access | 0.526 s | 0.227 s | -- | 2.3x | -- |
| fibonacci_iterative | 1.419 s | 0.542 s | -- | 2.6x | -- |
| math_intensive | 1.158 s | 0.921 s | -- | 1.3x | -- |

### Trace JIT Highlights

The trace JIT excels at tight inner loops with table access:
- **table_field_access**: 12.1x (hot field access loop)
- **nbody**: 6.1x (float-intensive loop with table fields)
- **mandelbrot**: 4.9x (nested loops with float arithmetic)
- **matmul**: 4.6x (triple-nested loop)

It struggles with recursive and call-heavy benchmarks (fib, ackermann, mutual_recursion
at 1.0x) because trace compilation cannot follow call trees.

### GScript vs LuaJIT Gap

LuaJIT is 14--113x faster than GScript's VM. The largest gaps are in float-intensive
(spectral_norm 113x, nbody 52x, matmul 43x) and recursive (fib 64x) benchmarks.
LuaJIT's method-level compilation, aggressive inlining, and allocation sinking are
the primary drivers. The Method JIT's loop speedups (2.7--2.9x over VM) would close
the gap to roughly 5--40x on compute-heavy benchmarks once tiering is fully
integrated and the call overhead is eliminated.

---

## What the Method JIT Can and Cannot Compile

### Compiles natively (no deopt)
- Integer arithmetic: `+`, `-`, `*`, `%` (via `OpAddInt`, `OpSubInt`, `OpMulInt`, `OpModInt`)
- Float-aware arithmetic: `+`, `-`, `*` with auto int/float dispatch
- Division: always returns float (via `OpDiv`/`OpDivFloat`)
- Unary negate: `-a` for int and float
- Logical not: `!a`
- Comparisons: `<`, `<=`, `==` for integers
- Constants: int, float, bool, nil
- Branches: `if/else` via `OpBranch`
- For loops: `for i := init; cond; step { body }` via phi nodes
- Return values

### Triggers deopt (falls back to VM interpreter)
- **Function calls**: `OpCall`, `OpSelf` -- all calls bail immediately
- **Global access**: `OpGetGlobal`, `OpSetGlobal`
- **Upvalue access**: `OpGetUpval`, `OpSetUpval`
- **Table operations**: `OpNewTable`, `OpGetTable`, `OpSetTable`, `OpGetField`, `OpSetField`, `OpSetList`, `OpAppend`
- **String operations**: `OpConcat`, `OpConstString`
- **Other**: `OpLen`, `OpPow`, `OpClosure`, `OpClose`, `OpForPrep`/`OpForLoop` (numeric for), varargs, goroutines/channels, guards

### Where deopt fires in practice
- **fib**: deopt on every recursive call (the Method JIT compiles the base case and comparison but bails on `fib(n-1) + fib(n-2)`)
- **Any benchmark using globals**: immediate deopt at first global access
- **Table-heavy benchmarks**: immediate deopt at first table operation

---

## Expectations for Next Phase

### High-leverage next steps

1. **Eliminate per-call allocation.** The `Execute` method allocates `[]runtime.Value`
   on every call (~20 ns, 136 B). Using a sync.Pool or pre-allocated frame would
   improve all benchmarks by 1.3--1.5x, especially short functions.

2. **Self-recursive calls.** Compiling `fib(n-1)` as a direct `BL` to the same
   native code (instead of deopt) would unlock the recursive benchmarks. With native
   self-recursion, fib(35) could go from 1.7s to ~0.1--0.3s.

3. **Inlining small functions.** Monomorphic call sites with small callees can be
   inlined at the IR level, eliminating call overhead entirely.

4. **Global access in native code.** Emit loads/stores to a globals table pointer
   instead of deopt. This unlocks sum_primes, sort, and many real-world patterns.

5. **Table field access.** JIT-compile field access with inline caches. This is the
   path to beating LuaJIT on table_field_access.

### Performance targets

| Metric | Current | Target (M7) | Target (M8+) |
|---|---:|---:|---:|
| Simple ops (Add) | 1.1x over VM | 2x (pool alloc) | 3--5x |
| Tight loops (Sum10k) | 2.9x over VM | 4x (pool + type spec) | 10--20x |
| Recursive (fib) | deopt (1.0x) | 5x (self-call) | 20--50x |
| vs LuaJIT (loops) | ~35x slower | ~12x slower | 1--3x slower |
