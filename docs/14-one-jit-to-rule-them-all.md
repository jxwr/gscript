# One JIT to Rule Them All

We had three JIT compilers. Now we have two — and they actually work together.

## The Problem

GScript accumulated three separate compilation backends over time:

1. **Method JIT** (`codegen*.go`, 5.6k lines) — compiles entire functions to ARM64. Fast function calls, self-recursion optimization, register pinning. No float support.

2. **Old Trace Compiler** (`trace_compile.go`, 1.7k lines) — records hot loop execution, compiles the trace directly to ARM64. Simple but limited. Superseded by SSA.

3. **SSA Trace Compiler** (`ssa*.go`, 6k lines) — same recording, but builds SSA IR first, runs optimization passes (constant hoisting, CSE, FMA fusion), does proper register allocation, then emits ARM64. Float support via D4-D11.

Each had its own opcode handlers, register allocators, and codegen paths. Integer arithmetic was implemented three times. Table operations were implemented three times. The CLI had two flags (`-jit` and `-trace`) that users had to choose between.

## What We Did

### Deleted the Old Trace Compiler

The SSA compiler is strictly better — it produces faster code and handles more patterns (nested loops, function calls via call-exit). We extracted the shared infrastructure (TraceContext, CompiledTrace, executeTrace) into `trace_exec.go` and deleted everything else.

**-2,801 lines of code. 6 files deleted, 1 new file.**

### Unified the Flags

`-jit` now enables both tiers:
- Method JIT compiles every function on first call
- SSA Trace JIT records and compiles hot loops in functions the Method JIT can't handle

The old `-trace` flag is gone.

### Made Them Coexist

This was the hard part. Enabling both JITs simultaneously exposed an interaction bug: Method JIT would side-exit to the interpreter (e.g., when hitting GETGLOBAL without a globals accessor), the interpreter would run the loop, the trace recorder would compile it, and the compiled trace would produce wrong results on subsequent calls.

```
Call 1: outer(10) = 220  ✓  (first run, interpreter finishes the work)
Call 2: outer(10) = 52   ✗  (trace compiled from call 1, bad results)
Call 3: outer(10) = 10   ✗  (even worse — state leaks between trace runs)
```

Root cause: the main JIT engine was missing `globalsAcc` and `callHandler`, so GETGLOBAL call-exits failed immediately, falling back to the interpreter. The trace recorder then picked up loops that were supposed to be handled by Method JIT.

The fix has two parts:
1. **JITEntry guard**: trace recorder only activates for functions NOT compiled by Method JIT (`proto.JITEntry == nil`). This prevents the trace JIT from interfering with functions the Method JIT already handles.
2. **Let each tier do what it's good at**: Method JIT handles function calls and integer loops natively. When it side-exits (e.g., for GETGLOBAL), the interpreter finishes. The trace JIT only activates for top-level scripts and interpreted functions where Method JIT never fires.

## Architecture After

```
gscript -jit foo.gs

  ┌─────────────────────────────────────────────┐
  │ Method JIT (codegen*.go)                    │
  │ Compiles functions on first call            │
  │ Self-recursion, inlining, register pinning  │
  │ Handles: int arithmetic, for-loops, calls   │
  │ Exits to interpreter for: GETGLOBAL, etc.   │
  └─────────────────────────────────────────────┘
                        +
  ┌─────────────────────────────────────────────┐
  │ SSA Trace JIT (ssa*.go + trace.go)          │
  │ Records hot loops in interpreter            │
  │ SSA IR → ConstHoist → CSE → FMA → RegAlloc  │
  │ Handles: float arithmetic, type guards      │
  │ Only for functions WITHOUT Method JIT       │
  └─────────────────────────────────────────────┘
                        +
  ┌─────────────────────────────────────────────┐
  │ Shared (assembler.go, trace_exec.go,        │
  │         memory.go, executor.go)             │
  └─────────────────────────────────────────────┘
```

## The Elephant in the Room

The current architecture has a fundamental limitation: the two tiers don't collaborate on the same function. If Method JIT compiles a function, the trace JIT never sees its loops. This means float-heavy loops inside compiled functions (mandelbrot, matmul, spectral_norm, nbody) never get SSA-optimized.

The numbers tell the story:

| Benchmark | JIT | LuaJIT | Gap |
|-----------|-----|--------|-----|
| fib | 0.034s | 0.032s | 1.0x |
| sieve | 0.022s | 0.010s | 2.2x |
| mandelbrot | 1.386s | 0.052s | **27x** |
| matmul | 1.022s | 0.022s | **47x** |
| spectral_norm | 0.923s | 0.007s | **132x** |
| nbody | 1.937s | 0.033s | **59x** |

Every benchmark with a >10x gap is float-heavy. The SSA Trace JIT already knows how to handle floats (mandelbrot in trace-only mode: 0.15s, just 3x off LuaJIT). But it can't activate because Method JIT has already claimed the function.

## Next Step

The path to surpassing LuaJIT runs through float computation. Two possible approaches:

**A. Add float support to Method JIT.** Emit `FADD`/`FSUB`/`FMUL`/`FDIV` directly in the Method JIT codegen, with type guards to detect float operands. This is straightforward but means reimplementing what the SSA Trace JIT already does.

**B. Let the Trace JIT activate inside Method JIT functions.** When Method JIT detects a hot loop with float operations it can't handle natively, hand off that specific loop to the SSA Trace JIT. The Method JIT handles the function skeleton (calls, control flow), the Trace JIT handles the compute-intensive inner loop.

Option B is more elegant and avoids code duplication, but requires solving the register state interaction that caused bugs in this session. Option A is simpler but means maintaining float codegen in two places.

Either way, the unified architecture makes the path forward clearer. One codebase, two complementary tiers, one flag.
