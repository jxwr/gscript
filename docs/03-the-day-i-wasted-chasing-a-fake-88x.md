---
layout: page
title: "The Day I Wasted Chasing a Fake 88x"
permalink: /03-the-day-i-wasted-chasing-a-fake-88x
---

# The Day I Wasted Chasing a Fake 88x

*March 2026 — Beyond LuaJIT, Post #3*

## Where We Left Off

In [Post #2](02-introducing-ssa-ir), we introduced SSA IR with type specialization and integer unboxing. The benchmark table on our blog proudly claimed:

| Benchmark | Before | After | Speedup |
|-----------|--------|-------|---------|
| mandelbrot | 4.782s | **0.054s** | **×88.6** |
| sieve | 2.502s | 0.182s | ×13.7 |

×88 on mandelbrot. LuaJIT territory. Ship it.

Except it was wrong. The ×88 was a lie.

## The Unraveling

It started with a sieve bug. `sieve(100)` was returning 93 primes instead of 25. That's not an off-by-one — that's "the entire computation is broken" territory. While investigating, I ran `mandelbrot(10)` and got 45 pixels. The interpreter gives 45. OK, seems fine. Then I tried `mandelbrot(1000)`:

```
mandelbrot(1000) = 22 pixels in set     ← trace JIT
mandelbrot(1000) = 396940 pixels in set  ← interpreter
```

22 vs 396,940. The trace was computing garbage at high speed. The ×88 "speedup" was just the JIT skipping 99.99% of the actual work.

## The Bug Avalanche

What followed was a full day of bug-fixing. Not one bug — **eight interconnected bugs**, each masking or depending on the others:

### Bug 1: SSA MOVE was a no-op

The SSA builder turned `MOVE R(0), R(5)` into a ref alias (`slotDefs[0] = slotDefs[5]`) without emitting any code. This worked for single-iteration analysis but broke loop-carried values: on the next iteration, R(0)'s register still held the stale value from loop entry because no register copy was ever emitted.

**Result**: `sum = sum + i` for i=1..15 returned 66 instead of 120. The trace ran but the accumulator never updated.

### Bug 2: TEST guard was inverted

The `emitTrTest` function for `OP_TEST A C` had C=0 and C=1 swapped. A truthiness guard that should have side-exited on falsy values was instead side-exiting on truthy values. The comments were wrong, and the implementation faithfully followed the wrong comments.

**Result**: `sieve(100)` returned 93 instead of 25. Every `if is_prime[i]` check was passing when it should have failed.

### Bug 3: Dead code in writtenSlots

The `ssaWrittenSlots` set was computed by scanning ALL SSA instructions after the LOOP marker, including unreachable code after unconditional `SSA_SIDE_EXIT`. When the store-back ran, it wrote stale register values for slots that the trace never actually modified.

**Result**: Variables from the outer loop scope got corrupted by the trace's store-back.

### Bug 4: Float traces compiled but always side-exited

The regular trace compiler compiled float-heavy traces (like mandelbrot's inner loop) even though its type guards only check for `TypeInt`. Every iteration: trace enters → first type guard fails (TypeFloat ≠ TypeInt) → side-exit → interpreter runs body → FORLOOP → trace enters again. Infinite overhead.

**Result**: mandelbrot(1000) took 92 seconds (vs 1.6s interpreter) before I added a blacklist.

### Bug 5: Pre-loop guard exits to wrong PC

When an SSA pre-loop type guard failed, it side-exited to the instruction's PC — which was in the MIDDLE of the loop body. The interpreter resumed there with partially initialized registers.

**Result**: The second invocation of any function with a traced loop would crash or produce wrong results.

### Bugs 6-8: Float slot allocation, constant rematerialization, store-back TypeInt on float slots

Each of these was discovered by the same pattern: fix one bug, run the test, discover a new failure. The float slot allocation bug was particularly insidious — `buildSSASlotRefs` added float slots to the frequency count, bypassing the `floatSlots` filter that was supposed to keep them out of integer register allocation. The store-back then wrote `TypeInt` over `TypeFloat`, turning every float into garbage.

## The Turning Point: Observation-Driven Debugging

The first few hours were wasted on guess-and-check debugging. Read the code, form a theory, make a change, run the test. The code has ~5 layers of indirection (SSA builder → slot mapper → register allocator → codegen → store-back), and reasoning about correctness across all layers simultaneously is futile.

The breakthrough came when I switched to **observation-driven debugging**: dump the exact register values before and after trace execution.

```go
fmt.Printf("[TRACE-PRE] R(0)={typ=%d,data=%d}\n",
    vm.regs[base+0].RawType(), vm.regs[base+0].RawInt())
// ... execute trace ...
fmt.Printf("[TRACE-POST] R(0)={typ=%d,data=%d}\n",
    vm.regs[base+0].RawType(), vm.regs[base+0].RawInt())
```

Output:
```
[TRACE-PRE]  R(0)={typ=2,data=66}
[TRACE-POST] R(0)={typ=2,data=66}   ← unchanged! sum never updated
```

One print statement. One test run. Root cause found in seconds. The SSA MOVE was generating no code.

Every subsequent bug was found the same way: dump state, observe discrepancy, trace to root cause. No guessing.

## The Real Numbers

After fixing all eight bugs and implementing float SSA support (SIMD register allocation with D4-D11, expression forwarding, GUARD_TRUTHY for TEST, native GETTABLE/SETTABLE):

| Benchmark | Interpreter | Trace JIT | Speedup |
|-----------|-------------|-----------|---------|
| mandelbrot | 1.569s | 1.142s | **×1.37** |
| fib | 0.843s | 0.827s | ×1.02 |
| sieve | 0.283s | 0.350s | ×0.81 |
| spectral_norm | 0.836s | 0.973s | ×0.86 |
| ackermann | 0.148s | 0.205s | ×0.72 |

×1.37 on mandelbrot. Not ×88. Not even ×2. One point three seven.

Every result is **correct**. `mandelbrot(1000) = 396940`, `sieve(1000000) = 78498`. The sieve and other benchmarks are slightly slower due to tracing overhead (the `OnLoopBackEdge` call on every FORLOOP iteration costs ~20ns even when no trace runs).

## Where the Time Goes

I instrumented the mandelbrot inner loop's compiled ARM64 output. Per iteration:

| Category | Instructions | Eliminable? |
|----------|-------------|-------------|
| Float computation (FMUL, FADD, FSUB) | 7 | No (the actual work) |
| Constant rematerialization (2.0, 4.0) | 6-10 | **Yes: hoist out of loop** |
| Memory roundtrips for temps | 4-8 | **Yes: better register allocation** |
| Guard PC loading | 2-4 | **Yes: snapshots** |
| FORLOOP overhead | 3-4 | Mostly no |
| Register moves | 2-3 | Partially |
| **Total** | **~30** | vs ~12 ideal |

LuaJIT would generate ~12 instructions. We generate ~30. The 2.5x instruction overhead explains the modest speedup — we eliminate the interpreter's dispatch overhead but replace it with codegen overhead.

The two biggest wastes:
1. **Constant rematerialization**: `2.0` and `4.0` are loaded fresh every iteration (5 instructions each: `MOVZ + 3×MOVK + FMOV`) because the bytecode compiler reuses the same temp slot for constants and arithmetic results.
2. **Memory roundtrips for temporaries**: `zr*zr` is computed into a temp slot, written to memory, then loaded back for the subtraction. With only 8 D registers and 10 float slots, 2 temps spill.

## The Architectural Root Cause

All eight bugs trace to the same design flaw: **the SSA builder and codegen are not cleanly separated.**

`CompileSSA()` is a 700-line function that simultaneously does:
- Slot frequency analysis
- Float slot identification
- Register allocation (integer AND float)
- Pre-loop guard emission with special-case ExitCode=2
- Loop body code generation with inline forwarding analysis
- Store-back set computation (the source of 3 bugs)
- Epilogue emission

Change any one of these and you risk breaking the others. The `writtenSlots` bugs alone consumed hours because the tracking is ad-hoc: manually scan the SSA IR for certain opcodes, with special cases for `SSA_SIDE_EXIT` boundaries, FORLOOP aliases, and float type tags.

The correct architecture is a **pass pipeline**:

```
BuildSSA(trace)    → SSAFunc
ConstHoist(f)      → SSAFunc    // move 2.0, 4.0 out of loop
CSE(f)             → SSAFunc    // eliminate redundant zr*zr
DCE(f)             → SSAFunc    // remove dead code
RegAlloc(f)        → RegMap     // int→X regs, float→D regs
EmitARM64(f, map)  → []byte     // pure mechanical translation
```

Each pass: input SSA → transform → output SSA. Add a new optimization? Insert a new pass. The emitter becomes trivial — it just looks up the register map and writes instructions. No analysis, no special cases, no `writtenSlots`.

## Lessons Learned

1. **Never optimize wrong results.** The ×88 was exciting but meaningless. I should have run correctness checks before celebrating. Rule: *if the benchmark result doesn't match the interpreter, the speedup is zero.*

2. **Observation beats reasoning.** Five hours of reading code and guessing. Five minutes of register dumps to find the root cause. Always instrument first.

3. **Architecture compounds.** Each bug fix was "small" (5-20 lines), but finding it required understanding the entire pipeline. In a clean pass architecture, each bug would have been isolated to one pass.

4. **The slot-reuse problem is fundamental.** The bytecode compiler uses the same register for `zr*zr` (float), then `temp` (float), then `4.0` (float constant). The SSA builder sees one "slot 21" with three different roles. This prevents constant hoisting and wastes registers. Fixing this requires decoupling SSA refs from VM slots — i.e., building a real SSA IR with its own numbering, like LuaJIT does.

## What's Next

The path from ×1.37 to ×10 is clear but steep:

| Step | Speedup | Days | What |
|------|---------|------|------|
| Pass pipeline refactor | — | 3-5 | Prerequisite for everything else |
| Constant hoisting | ×1.2 | 1-2 | Load 2.0, 4.0 once before loop |
| Full float register allocation | ×1.3 | 3-5 | Eliminate all temp memory roundtrips |
| CSE | ×1.1 | 2-3 | Reuse zr*zr, zi*zi |
| Nested loop tracing | ×2.5 | 10-15 | The big jump: trace outer loops too |
| Snapshots | ×1.15 | 5-7 | Replace writtenSlots with LuaJIT-style snapshots |

Estimated total: ×5-6 in 25-35 days. ×10 requires nested loop tracing — without it, the per-pixel Go→JIT→Go function call overhead caps the speedup at ~4x.

Today was humbling. But the bugs are fixed, the architecture is understood, and the roadmap is honest. The ×88 was fake; the work ahead is real.

---

*[Beyond LuaJIT](./) — a series about building a JIT compiler from scratch.*
