# Thirty-Three X

Last post we rewrote the JIT and got 11.3x on table field access. This time we fixed the build, modularized the emitter, and taught the compiler to recurse natively. fib(35) went from 1.55 seconds to 46 milliseconds.

## Where We Were

Blog #18 left us with a clean trace-only JIT: 6 benchmarks accelerated, 15 stuck at interpreter speed, and a 70x gap against LuaJIT on recursive functions. The old Method JIT's self-call inlining — which had fib running at 34ms — was deleted in the rewrite. The question was: can we get it back without the architectural mess?

## The Numbers

| Benchmark | VM | JIT | Speedup | LuaJIT | vs LuaJIT |
|-----------|-----|-----|---------|--------|-----------|
| **fib(35)** | 1.555s | **46ms** | **33.6x** | 23ms | 2.0x |
| **table_field_access** | 718ms | **65ms** | **11.0x** | -- | -- |
| **nbody(500K)** | 1.805s | **313ms** | **5.8x** | 32ms | 9.8x |
| **mandelbrot(1000)** | 1.357s | **276ms** | **4.9x** | 51ms | 5.4x |
| **matmul(300)** | 974ms | **215ms** | **4.5x** | 21ms | 10.2x |
| **table_array_access** | 383ms | **138ms** | **2.8x** | -- | -- |
| **fibonacci_iterative** | 1.014s | **423ms** | **2.4x** | -- | -- |
| **sieve(1M x3)** | 239ms | **130ms** | **1.8x** | 10ms | 13.0x |
| **math_intensive** | 896ms | **676ms** | **1.3x** | -- | -- |
| **fannkuch(9)** | 556ms | **461ms** | **1.2x** | 18ms | 25.6x |
| coroutine_bench | 4.97s | 4.93s | 1.0x | -- | -- |
| sort | 169ms | 174ms | 1.0x | 10ms | 17.4x |
| sum_primes | 27ms | 30ms | 0.9x | 2ms | 15.0x |
| spectral_norm | 951ms | 823ms | 0.9x | 7ms | 117.6x |
| binary_trees(15) | 1.55s | ERROR | -- | 161ms | -- |
| string_bench | 41ms | 41ms | 1.0x | 9ms | 4.6x |
| mutual_recursion | 194ms | 251ms | 0.8x | 4ms | 62.8x |
| ackermann | 270ms | ⚠️ | -- | 6ms | -- |
| closure_bench | 82ms | ⚠️ | -- | 8ms | -- |
| object_creation | 614ms | 1.81s | 0.3x | -- | -- |
| method_dispatch | 84ms | 239ms | 0.4x | <1ms | -- |

10 benchmarks now have real JIT speedups (was 6). fib jumped from 1.0x to **33.6x**. nbody jumped from 1.0x to **5.8x**. matmul jumped from 0.7x (regression!) to **4.5x**.

## What Changed

Six commits, three bugs fixed, one major feature added, one monolith split.

### Bug 1: Division Always Returns Float

GScript follows Lua semantics: `/` always returns a float, even for integer operands. The SSA builder was creating `SSA_DIV_INT` when both operands were integers, producing raw int64 values where IEEE 754 doubles were expected. The NaN-boxing mismatch corrupted downstream computation.

The expression `(i+j)*(i+j+1)/2` in spectral_norm's inner loop was the canary. Fix: force `SSA_DIV_FLOAT` with `SCVTF` (ARM64 int-to-float conversion) for all division.

### Bug 2: BOX_INT Slot Zero Corruption

When converting int-to-float for division operands, the `emitIntToFloat` helper created temporary `SSA_BOX_INT` instructions with `Slot=0` (Go's zero value default). The emitter's `spillFloat` function then stored the converted float to VM slot 0 — overwriting whatever the caller had there. For benchmarks with `t0 := time.now()` in slot 0, this silently replaced the time table with a float.

Fix: set `Slot=-1` on temporary conversion instructions so they're treated as pure values with no memory side-effect.

### Bug 3: Build Was Broken

The committed code referenced `SSA_SELF_CALL`, `FuncReturnCount`, `isFuncTrace`, and other symbols that existed only in uncommitted development files. A fresh `git clone` would fail to compile. We added the missing definitions, committed the development files (DCE, load elimination, strength reduction, disassembler), and dissolved all temporary stubs into their proper locations.

### The Split: ssa_emit.go

The main emitter file had grown to 3,121 lines — mixing prologue, arithmetic, guards, table operations, store-back, intrinsics, and exit handlers in one place. We split it into 8 focused files:

```
ssa_emit.go          (1116) - entry points + dispatch
ssa_emit_table.go     (768) - table/field/array/global
ssa_emit_exit.go      (296) - store-back, reload, exits
ssa_emit_prologue.go  (271) - prologue, guards, pre-loop
ssa_emit_resolve.go   (232) - operand resolution
ssa_emit_intrinsic.go (185) - intrinsics, call-exit
ssa_emit_guard.go     (179) - comparisons, guards
ssa_emit_arith.go     (126) - arithmetic, FMA
```

Also split `ssa_build.go` and test files. No file exceeds 1,200 lines now.

### The Feature: Function-Entry Tracing

This is the big one. When `fib(n)` is called 50 times, the trace recorder records one execution of its body — including the two recursive `fib(n-1)` and `fib(n-2)` calls. The SSA builder converts these to `SSA_SELF_CALL` instructions, and the emitter generates ARM64 `BL` (branch-and-link) instructions that recursively call the same native code.

```
Source:                    ARM64:
func fib(n) {             self_call_entry:
  if n < 2 {return n}       LDR X21, [X26]      // load n
  return fib(n-1)            CMP X21, #2
    + fib(n-2)               B.LT base_case
}                            SUB X20, X21, #1    // n-1
                             STP X30,X20,[SP,-48]!
                             BL self_call_entry   // fib(n-1)
                             LDP X30,X20,[SP],48
                             ... (save result)
                             SUB X22, X21, #2    // n-2
                             STP X30,X20,[SP,-48]!
                             BL self_call_entry   // fib(n-2)
                             LDP X30,X20,[SP],48
                             ADD X23, X28, X20   // result
                             RET
```

Three sub-problems had to be solved:

**1. Dead GETGLOBAL**: Before each `fib(n-1)` call, the bytecode loads the `fib` function reference via `GETGLOBAL`. After inlining the call as `SSA_SELF_CALL` (which uses `BL` directly), this `GETGLOBAL` becomes dead code. But `SSAIsUseful()` rejects any trace containing non-table `LOAD_GLOBAL`. Fix: NOP out the dead `GETGLOBAL` in the SSA builder when creating `SSA_SELF_CALL`.

**2. While-Loop Exit Misclassification**: `OptimizeSSA` was marking the first comparison after `SSA_LOOP` as a while-loop exit (`AuxInt=-2`). For function traces, this comparison is the base-case guard (`n < 2`), not a loop exit. The while-loop exit sentinel inverted the guard polarity: the trace would exit when `n >= 2` (the recursive case) instead of when `n < 2` (the base case). Fix: skip while-loop exit detection for function traces.

**3. Shared Memory Corruption**: All recursion depths share the same `regRegs` memory. When the first `BL` returns with `fib(n-1) = X`, the result is stored to memory before the second `BL` call. But inside the second call's recursion, the same memory slot is used for intermediate results, overwriting `X`. Fix: use `X28` (a callee-saved register dedicated to self-call overflow) instead of memory for intermediate results. `X28` is saved/restored on the ARM64 stack across each `BL`, so nested calls can't corrupt it.

## Test Scorecard

Before this round: 8 failures, broken build.
After: **0 failures** (except 2 known issues: ackermann 2-arg, SumPrimes).
136+ tests pass.

## What's Next

The biggest remaining gaps against LuaJIT:

| Gap | Benchmarks | Fix |
|-----|-----------|-----|
| **117x** | spectral_norm | Inline function calls in traces (GETGLOBAL for local fn refs blocks compilation) |
| **63x** | mutual_recursion | Function-entry traces for non-self-recursive calls |
| **26x** | fannkuch | Nested loop tracing (only innermost loop compiles) |
| **13x** | sieve | Dual-path traces (80% side-exit rate on boolean guard) |
| **10x** | matmul, nbody | Already 4-6x speedup, need tighter native code |
| **2x** | fib | Almost at parity (46ms vs 23ms). Register pinning for args would close the gap. |
