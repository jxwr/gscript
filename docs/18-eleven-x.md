# Eleven X

We deleted 13,000 lines of JIT compiler, wrote 7,000 new ones, and got 11.3x on table field access. This is the story of the rewrite.

## The Numbers

| Benchmark | Interpreter | JIT | Speedup |
|-----------|-------------|-----|---------|
| table_field_access | 0.77s | 0.07s | **11.3x** |
| mandelbrot | 1.43s | 0.26s | **5.5x** |
| fibonacci_iterative | 1.12s | 0.35s | **3.2x** |
| table_array_access | 0.42s | 0.14s | **3.0x** |
| sieve | 0.26s | 0.14s | **1.9x** |
| fannkuch | 0.60s | 0.50s | **1.2x** |

6 of 21 benchmarks accelerated. All 21 produce correct results. 136 tests pass. Zero hangs.

## What Changed

The old JIT had two tiers (Method JIT + Trace JIT) sharing infrastructure but fighting each other. The new JIT has one tier: trace-only, like LuaJIT.

### Architecture

```
Interpreter → hot loop detected → record trace → build SSA IR
→ classify slots → allocate registers → emit ARM64 → execute
→ side-exit on guard fail → interpreter resumes → re-enter trace
```

Key design decisions:

**1. No store-back before call-exit.** When the trace hits an operation it can't compile (function call, global access), it side-exits to the interpreter. Memory retains the interpreter's state. No need to synchronize registers with memory.

**2. Break-exit vs side-exit.** Conditional breaks (mandelbrot's escape check) use ExitCode=4, distinct from regular side-exits (ExitCode=1). Break-exits don't count toward blacklisting.

**3. While-loop compilation.** Loops with `for condition { }` syntax (JMP back-edge, not FORLOOP) now compile with an AuxInt=-2 exit sentinel.

**4. Native table operations.** GETTABLE/SETTABLE compile to inline ARM64 with bounds checks, supporting all four array kinds (Mixed, Int, Float, Bool). GETFIELD/SETFIELD compile with shape-based field indexing.

### Bug Hall of Fame

| Bug | Symptom | Lines to fix |
|-----|---------|-------------|
| ARM64 LDRreg/STRreg S-bit=0 | Array access reads from wrong address | 2 |
| ExitPC overwritten by loopPC | All side-exits resume at FORLOOP, not guard | 1 |
| SETFIELD uses C for field (should be B) | All field writes go to wrong field | 1 |
| LOAD_FIELD uses dest slot as table slot | Table guard always fails on non-table dest | 1 |
| STORE_FIELD clobbers X0 before use | Float field writes produce garbage | 3 |
| Break-exit counted as side-exit | Mandelbrot blacklisted after 11 pixels | 1 |
| Trace overshoot past FORLOOP exit | Fannkuch compiled mixed loop bodies | 5 |

Seven bugs. Most were one-line fixes. Finding them was the hard part.

## The Testing Framework

The key innovation wasn't the JIT architecture — it was the testing methodology.

### Four layers

| Layer | Tests | What it catches |
|-------|-------|----------------|
| Codegen Micro | 21 | ARM64 instruction correctness |
| Trace Execution | 31 | Exit state, guard behavior, hangs |
| Opcode Matrix | 48 | VM vs JIT per-opcode correctness |
| Invariant Tests | 36 | First-principles: JIT result = VM result |

**136 tests total.** Every bug fix turns a failing test green. No more end-to-end benchmark debugging.

The invariant tests are the most valuable. They don't test opcodes or JIT internals — they test one thing: *does JIT produce the same result as the interpreter?* Organized by interaction patterns (loop × type × exit × reentry), not by implementation details.

## What's Left

The trace JIT compiles loops with native arithmetic, table access, and field access. It can't compile:

- **Recursive functions** (fib, ackermann) — need function-entry traces
- **2D table access** (matmul) — `a[i][k]` loads a table, then indexes it
- **Global variable access** (nbody) — `bodies` is a global, needs GETGLOBAL optimization

These are the next 3-5x. The architecture supports them; they're implementation work, not design work.

## Lessons

**Delete more.** The rewrite produced less code that does more. Every line has a purpose.

**Test from invariants, not from code.** The testing framework catches bugs the author didn't think of, because it tests properties (JIT=VM), not implementations.

**One-line bugs hide behind architecture.** The ARM64 S-bit bug was 2 characters. It took a complete rewrite to create the conditions where it could be found and fixed.

**Side-exit is the right default.** When in doubt, exit to the interpreter. It's always correct. Optimize specific operations to native only when you have tests proving correctness.
