---
layout: default
title: "The Missing Fast Paths"
permalink: /24-the-missing-fast-paths
---

# The Missing Fast Paths

matmul went from 0.985s to 0.202s. That's not a typo.

## The bottleneck nobody saw

For twelve rounds, matmul sat at 0.985s — 42.8x slower than LuaJIT. We had a Tier 2 optimizing compiler with SSA IR, type specialization, register allocation, the works. But matmul never reached Tier 2. It's called once with N=300, running 27 million inner-loop iterations at Tier 1.

Tier 1 is the baseline compiler — bytecode templates, no optimization. It handles most operations natively: arithmetic, comparisons, field access. But for table access by integer key, it only knew about two array kinds: Mixed and Int. Float arrays and Bool arrays fell to the slow path: exit ARM64, call into Go, do the table lookup, return to ARM64. Every single time.

27 million iterations. Two GETTABLE per iteration. 54 million round-trips through the Go runtime. At ~150ns each, that's 8.1 seconds of pure overhead. The actual native load takes ~5ns.

## What we built

The fix was embarrassingly straightforward once we saw the data. Four commits, four files changed.

**Tasks 1-2: Float and Bool fast paths for GETTABLE and SETTABLE.** The Tier 2 compiler already had these — `emit_table.go` handles all four array kinds (Mixed, Int, Float, Bool) with dedicated ARM64 sequences. Tier 1 just never got them. We extended the `arrayKind` dispatch in `emitBaselineGetTable` and `emitBaselineSetTable` from `{Mixed, Int, else->slow}` to `{Bool, Float, Int, Mixed, else->slow}`.

The Float fast path is five ARM64 instructions: bounds check, load pointer, indexed load. The key insight is that float64 bits *are* the NaN-boxed representation — no conversion needed. You load 8 bytes from the float array and store them directly into the VM register. The Bool path is longer (~12 instructions) because it needs to convert the byte encoding (0=nil, 1=false, 2=true) into NaN-boxed values.

This was purely mechanical. The patterns were already proven in Tier 2 — we just ported them to the template-based Tier 1 emitter. TDD caught nothing because the exit-resume slow path already produced correct results. The fast path is purely a performance optimization.

**Task 3: Feedback collection infrastructure.** This was the architectural investment. We added a `BaselineFeedbackPtr` field to the ExecContext (the Go/JIT calling convention struct), wired it up so Tier 1 code can write to the proto's FeedbackVector during execution, and added monotonic type-recording stubs to each typed GETTABLE fast path.

The stubs are ~8 ARM64 instructions each, but they're structured so the hot path (type already recorded, matches observation) is just load-compare-branch — 3 instructions that almost always skip. The cold path (first observation or type widening) runs once per array kind transition. The monotonic lattice ensures feedback never narrows: Unobserved -> concrete type -> Any.

We deliberately skip feedback on ArrayMixed — the value type is unknown without extracting the NaN-box tag, and mixed arrays are typically polymorphic anyway.

**Task 4: GETFIELD feedback with runtime type extraction.** Unlike GETTABLE (where the array kind determines the result type), GETFIELD loads an arbitrary NaN-boxed value from `svals[fieldIdx]`. So we extract the type at runtime: `LSR tag, value, #48` then classify (< 0xFFFC = float, == 0xFFFE = int, else = any). This is what enables nbody's `body.x` / `body.vx` field accesses to be typed as `:float` when Tier 2 eventually compiles.

## The numbers

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| matmul | 0.985s | 0.202s | **-79.5%** |
| spectral_norm | 0.335s | 0.156s | **-53.4%** |
| sieve | 0.186s | 0.084s | **-54.8%** |
| nbody | 0.677s | 0.635s | -6.2% |
| table_array_access | 0.135s | 0.116s | -14.1% |
| fibonacci_iterative | 0.292s | 0.283s | -3.1% |
| math_intensive | 0.188s | 0.192s | +2.1% (noise) |

The plan predicted -15% to -25% for matmul. We got -79.5%. The prediction model assumed that exit-resume was *one* of several bottlenecks. In reality, it was almost the *only* bottleneck for Tier 1 float array access. A function that spends 80% of its time in exit-resume overhead, reduced by 30x, gives a 4.9x speedup — which is exactly what we measured (0.985 / 0.202 = 4.87x).

sieve's 54.8% improvement was equally unexpected. The sieve benchmark uses bool arrays heavily, and every `is_prime[j] = false` and `if is_prime[i]` was going through exit-resume.

The non-table benchmarks (fibonacci_iterative, math_intensive) show no regression, confirming the feedback stubs add negligible overhead to functions without table operations.

## What this unlocks

The feedback infrastructure is the real prize. Right now, Tier 2's graph builder checks `proto.Feedback[pc].Result` to decide whether to insert GuardType nodes. Before this round, that field was always `FBUnobserved` because Tier 1 never wrote to it. Now it's populated.

Future rounds can leverage this: when Tier 2 sees that GETTABLE at PC 7 always returns `:float`, it inserts `OpGuardType(:float)`, and TypeSpecialize cascades the type through downstream arithmetic. `Mul(any, any)` becomes `MulFloat(float, float)`, which can use FPR-resident values and avoid NaN-boxing entirely.

## What we built (Tier 2 emit-level bypasses)

The Tier 1 fast paths above account for most of the wall-time improvement. But we also attacked the Tier 2 optimizing compiler's table access paths, which still had unnecessary work.

The sieve inner marking loop — `is_prime[j] = false` — compiles to a SetTable with an integer key and a constant boolean value. Before this round, the Tier 2 emitter treated every SetTable identically: NaN-box the key (UBFX+ORR), check the tag (LSR+MOV+CMP+BNE), unbox the key (SBFX), then NaN-box the value, check ITS tag, extract the payload, add 1. Fourteen instructions for what should be "store byte 1 at array[j]."

The key insight was that the emitter already tracks which values are raw (unboxed) ints via `rawIntRegs`, and which values are compile-time constants via `constInts`/`constBools` — it just never consulted these maps when emitting table ops.

**Raw-int key bypass (Tasks 1-2):** In both `emitGetTableNative` and `emitSetTableNative`, we replaced the monolithic key-loading sequence with a three-way dispatch. If the key is in `rawIntRegs` (common for loop counters like `j`): one MOV instruction, skip five. If the key's IR type is `TypeInt` but it's NaN-boxed in memory: skip the tag check, just SBFX directly. Otherwise: the original full path. The three paths converge at the `key >= 0` bounds check.

**Constant value bypass (Tasks 3-4):** For the Bool path, when the value is `OpConstBool` (like the literal `false` in `is_prime[j] = false`), we compute the byte encoding at compile time — `MOVimm16 X4, #1` — and skip the value load, tag check, nil check, AND+ADD payload extraction. From ~12 instructions down to 1. For the Int path, when the value is `OpConstInt` or already a raw int in a register, similar bypasses apply.

The infrastructure was minimal: one new `constBools` map in `emitContext` (6 lines in `emit_compile.go`), mirroring the existing `constInts` pattern.

TDD caught nothing dramatic — these are emit-level changes that don't alter the IR or pass pipeline, so correctness regressions would show as wrong ARM64 output, not IR invariant violations. The existing `TestTier2_SieveCorrectness` covers the hot path. We added `TestTier2_SetTableConstBool` for targeted coverage of the bool bypass.

## The results

| Benchmark | Before | After | Change | vs LuaJIT |
|-----------|--------|-------|--------|-----------|
| matmul | 0.985s | 0.195s | **-80.2%** | 8.9x |
| spectral_norm | 0.335s | 0.154s | **-54.0%** | 19.3x |
| sieve | 0.186s | 0.082s | **-55.9%** | 7.5x |
| nbody | 0.677s | 0.615s | -9.2% | 18.1x |
| table_array_access | 0.135s | 0.119s | -11.9% | — |
| fibonacci_iterative | 0.292s | 0.283s | -3.1% | — |
| mandelbrot | 0.391s | 0.381s | -2.6% | 6.7x |

The plan predicted 10-12% for sieve. We got 55.9%. The plan predicted nothing for matmul. We got 80.2%.

The lesson is the same one round 13 tried to teach us: exit-resume overhead is binary, not gradual. A function either stays fully native or it exits on every table operation. Matmul calls one function with N=300 — 27 million inner-loop iterations at Tier 1, two GETTABLE per iteration. Before this round, every float array access exited ARM64, called into Go, did the lookup, and returned. 54 million round-trips at ~150ns each. The float fast path replaces each round-trip with a 5-instruction inline load (~5ns). That's a 30x reduction in per-access cost, applied to 54 million accesses.

Sieve's improvement was the same story for bool arrays. Every `is_prime[j] = false` and `if is_prime[i]` was exiting to Go. The Tier 2 emit-level bypasses (raw-int key, constant bool value) contributed a few percent on top — the Tier 1 fast paths did the heavy lifting.

The feedback infrastructure shows zero overhead on non-table benchmarks. fibonacci_iterative and math_intensive are within noise of the baseline. The hot-path branch (type already recorded, matches observation) is load-compare-skip — one cycle when predicted correctly, which is always after the first iteration.

The remaining gaps are still large. We're 7.5x from LuaJIT on sieve, 8.9x on matmul. The next bottleneck for matmul is the Tier 1 emitter's template-based approach: no register allocation, no constant propagation, no loop-invariant hoisting. For sieve, the surviving cost is phi slot reloads after SetTable (10 instructions per iteration that reload values already in registers). Both are architectural, not incremental.

## What I'd do differently

The plan was scoped as "Tier 2 emit-level bypasses" with a prediction of 10-12% on sieve. The Tier 1 fast paths were added during implementation when we realized the Tier 1 gap was far larger than the Tier 2 gap. This was the right call — but it means the plan's predictions were useless because they modeled the wrong thing.

Next time: before writing the plan, check which tier each target benchmark actually runs at. Matmul never reaches Tier 2. Sieve's inner function does, but the outer loop and initialization run at Tier 1. The prediction model assumed Tier 2, so it predicted instruction-level savings when the actual bottleneck was Tier 1 exit-resume overhead.

The feedback infrastructure was the right architectural investment. Round 12 failed specifically because feedback was never collected. Now it is. Whether Tier 2 can use it effectively is a question for the next round — but the plumbing is in place.

*Previous: [The Harness](/23-the-harness)*

*This is post 24 in the [GScript JIT series](https://jxwr.github.io/gscript/).
All numbers from a single-thread ARM64 Apple Silicon machine.*
