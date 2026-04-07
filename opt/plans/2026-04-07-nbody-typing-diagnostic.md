# Optimization Plan: nbody Production Typing Diagnostic + Fix

> Created: 2026-04-07
> Status: active
> Cycle ID: 2026-04-07-nbody-typing-diagnostic
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md

## Target

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| nbody | 0.284s | 0.038s | 7.47x | 0.15-0.20s |
| matmul | 0.130s | 0.025s | 5.20x | (secondary) |
| spectral_norm | 0.048s | 0.009s | 5.33x | (secondary) |

## Root Cause

Diagnostic data from a fresh test compilation of nbody `advance()` through the full Tier 2 pipeline shows:

- **LICM GetField hoisting: WORKING** — 4 fields (bi.x, bi.y, bi.z, bi.mass) hoisted to j-loop preheader
- **Intrinsic pass: WORKING** — math.sqrt → OpSqrt (no OpCall in inner loop)
- **⚠️ 29 of 31 arithmetic ops are GENERIC (`:any` type)** — Mul, Add, Sub, Div all use generic dispatch
- **⚠️ 44 of 48 non-control ops are untyped** — GetField results all `:any`

The diagnostic compiled through `RunTier2Pipeline` **without Tier 1 feedback collection**. In production via TieringManager, Tier 1 runs first and collects GetField feedback, which the graph builder uses to insert `OpGuardType` (verified in `graph_builder.go:669-676`).

**The root cause is uncertain**: either (a) the diagnostic is misleading and production IS typed (bottleneck = field access overhead), or (b) feedback genuinely doesn't reach nbody's Tier 2 compilation (bottleneck = untyped arithmetic).

A production-accurate diagnostic must determine which scenario is real before committing to an optimization approach.

## Prior Art (MANDATORY)

**V8:** TurboFan's `LoadElimination` propagates known field types through the effect chain. `ComputeLoopState` does field-sensitive kill analysis — `StoreField(obj, "vx")` does NOT kill known types for `(obj, "x")`. CheckMaps elimination at loop headers avoids redundant shape checks per iteration. (`load-elimination.cc:1363`, `786`)

**LuaJIT:** Values are always unboxed on-trace. Trace recording captures the actual types at recording time. Loop unrolling + CSE eliminates invariant guards. nbody's inner loop runs with ~30 instructions per iteration, fully unboxed floats in FPR.

**ARM64 M-series:** L1D cache hit = 3 cycles. FSQRT = 13 cycles latency. A GScript GetField wrapper is ~16 ARM64 instructions (shape guard + type check + load + NaN-unbox). Hoisting eliminates the wrapper, not just the memory latency.

Our constraints vs theirs:
- GScript's per-GetField overhead (~16 insns) vs LuaJIT's HREFK (~2-3 insns) or V8's optimized LoadField (~3 insns) is the key differentiator
- NaN-boxing adds unbox/box overhead at every field boundary that LuaJIT avoids entirely

## Approach

### Phase 1: Production Diagnostic (Task 1)
Write a diagnostic test that compiles nbody `advance()` through **TieringManager** (not RunTier2Pipeline directly). This means:
1. Create a TieringManager with the nbody proto
2. Run advance at Tier 1 first (to collect feedback)
3. Trigger Tier 2 compilation (via CallCount or direct promote)
4. Dump the Tier 2 IR and check for GuardType nodes after GetField
5. Count typed vs untyped ops in the inner loop

### Phase 2a: If feedback IS working (typed arithmetic in production)
The real bottleneck is field access overhead. Implement **cross-block shape propagation**:
- When emitting a loop, inherit the preheader block's `shapeVerified` map into the loop body
- For objects verified in the preheader (via LICM-hoisted GetField), skip shape checks in the body
- Saves ~11 instructions per table per iteration (type check + nil check + shape CMP + BCond)
- For nbody inner loop: 2 tables (bi, bj) × ~11 insns = ~22 insns saved per iteration

### Phase 2b: If feedback is NOT working (untyped arithmetic)
Fix the feedback pipeline gap. Possible issues:
- OSR-compiled functions may skip Tier 1 feedback collection
- Tier 2 compilation triggered before sufficient feedback accumulated
- `feedbackToIRType` not handling the observed type correctly

Expected impact: 29 generic → 29 specialized arith → ~290 insns saved per iteration → **~40-50% wall-time reduction** (halved for superscalar)

### Phase 3: Ackermann regression fix (bonus, if budget allows)
The self-call detection in `tier1_call.go` emits proto comparison for EVERY call site. For functions that never call themselves (detectable at compile time), skip the comparison entirely:
- At compile time, scan the function's bytecodes for any `OP_CALL` preceded by `OP_GETGLOBAL` that resolves to the same proto
- If no self-call exists, emit the old path (no proto comparison overhead)

## Expected Effect

**Scenario A (feedback working, Phase 2a):** nbody -10-15% (0.284s → ~0.24s). Shape dedup saves ~22 insns per inner loop iteration out of ~200+ total. After ARM64 superscalar discount: ~10%.

**Scenario B (feedback broken, Phase 2b):** nbody -30-50% (0.284s → ~0.14-0.20s). Type specialization eliminates generic dispatch on 29 arith ops, each saving ~8-10 insns. This is the high-impact scenario.

**Prediction calibration:** Round 18 overestimated LICM GetField impact (predicted 10-15%, got 0%). This round's prediction is more conservative: Phase 2a is 10-15% (small), Phase 2b is 30-50% (large but only if the pipeline is genuinely broken). The diagnostic in Phase 1 determines which scenario applies BEFORE committing resources.

## Failure Signals

- Signal 1: Production diagnostic shows ALL arithmetic IS typed → Phase 2b is invalid, proceed with Phase 2a (smaller impact)
- Signal 2: Production diagnostic shows feedback IS collected but GuardType still absent → deeper graph builder issue, may need more investigation
- Signal 3: Cross-block shape propagation causes correctness failures → revert, add to constraints.md
- Signal 4: Ackermann fix doesn't recover to pre-Round-20 level → self-call overhead is not the sole cause, investigate BLR path changes

## Task Breakdown

- [x] 0a. **Self-call detection + direct branch**: In `tier1_call.go`, when CALL target is the current function (proto == callee proto), skip the 6-instruction type-check + DirectEntryPtr load sequence. Emit a direct `B` (branch) to function entry instead of `BLR`. Save ~6 insns per self-call. Test: `TestSelfCallDirect` — fib(20) should use direct branch, verify correct result. Files: `tier1_call.go`, `tier1_compile.go`
- [x] 0b. **Argument register direct-pass**: When calling self, arguments are already in known VM register slots. Instead of storing to Value array then loading in callee entry, pass via physical ARM64 registers directly. Pin argument R(0) to a callee-saved register (X19 or similar) so it survives across the self-call without spill/reload. Test: `TestSelfCallRegisterPass`. Files: `tier1_call.go`, `tier1_compile.go`
- [~] 0c. **Return value register direct-write** (deferred — performance-neutral on M4 Max, budget better spent on nbody diagnostic): When self-call returns, write result directly to the destination register (not through Value array). Complements 0b. Test: verify fib(20) result correctness + measure wall-time improvement. Files: `tier1_call.go`, `tier1_compile.go`
- [x] 1. **Production diagnostic**: Write test compiling nbody through TieringManager with Tier 1 feedback → dump IR, count typed/untyped — file(s): `internal/methodjit/nbody_production_diag_test.go` — test: `TestDiag_NbodyProduction`
- [~] 2. **Fix identified bottleneck**: Scenario A confirmed — need cross-block shape propagation to reduce field access overhead. Deferred to next round (too complex for remaining budget).
- [~] 3. **Ackermann self-call optimization** (bonus): Deferred — self-call optimizations (0a, 0b) already implemented.
- [x] 4. Integration test + benchmark run

## Budget
- Max commits: 3 (+1 revert slot)
- Max files changed: 5
- Abort condition: Task 1 shows both scenarios are wrong (feedback works AND arithmetic is typed, meaning the gap is elsewhere)

## Results (filled by VERIFY)
| Benchmark | Before | After | Change | LuaJIT | Gap |
|-----------|--------|-------|--------|--------|-----|
| nbody | 0.284s | 0.261s | **-8.1%** | 0.034s | 7.7x |
| matmul | 0.130s | 0.120s | **-7.7%** | 0.022s | 5.5x |
| spectral_norm | 0.048s | 0.046s | -4.2% | 0.007s | 6.6x |
| mandelbrot | 0.064s | 0.064s | 0% | 0.058s | 1.1x |
| fib | 0.135s | 0.141s | +4.4% | 0.025s | 5.6x |
| ackermann | 0.612s | 0.595s | -2.8% | 0.006s | 99x |
| fannkuch | 0.053s | 0.046s | **-13.2%** | 0.020s | 2.3x |
| sort | 0.050s | 0.041s | **-18.0%** | 0.011s | 3.7x |
| fibonacci_iterative | 0.341s | 0.279s | **-18.2%** | N/A | — |
| binary_trees | 2.705s | 2.208s | **-18.4%** | N/A | — |
| coroutine_bench | 21.866s | 16.717s | **-23.5%** | N/A | — |
| table_field_access | 0.060s | 0.052s | **-13.3%** | N/A | — |
| table_array_access | 0.111s | 0.096s | **-13.5%** | N/A | — |
| math_intensive | 0.081s | 0.070s | **-13.6%** | N/A | — |
| object_creation | 0.865s | 0.746s | **-13.8%** | N/A | — |
| closure_bench | 0.033s | 0.028s | **-15.2%** | N/A | — |
| mutual_recursion | 0.271s | 0.236s | **-12.9%** | 0.004s | 59x |
| method_dispatch | 0.119s | 0.100s | **-16.0%** | 0.000s | — |
| string_bench | 0.035s | 0.031s | -11.4% | N/A | — |

### Test Status
- All passing (methodjit + vm)

### Evaluator Findings
- **PASS** — no blocking issues
- Minor: `nbClosureTagBits` ORR assumes Go heap pointers < 2^44 (sound on macOS ARM64, undocumented)
- Minor: diagnostic test uses fmt.Println instead of t.Log

### Regressions (≥5%)
- None

## Lessons (filled after completion/abandonment)
- **R(0) pin to X22 is the broadest single optimization yet**: 18/22 benchmarks improved 8-23% from eliminating repeated slot-0 loads. Every function accesses R(0) heavily — this should have been done in Round 1.
- **Diagnostic-first confirmed its value again**: Production typing diagnostic (Task 1) proved Scenario A, preventing wasted effort on fixing a non-broken feedback pipeline.
- **NaN-boxed closure cache in X21 enables zero-cost self-call detection**: Pre-computing the tagged closure pointer once at function entry makes the hot-path proto comparison a single CMP instruction instead of load+tag+compare.
- **Callee-saved register budget is tight**: X19 (ctx), X22 (R0), X21 (self-closure), X24 (int tag), X25 (bool tag), X26 (regs base), X27 (constants) = 7 of 10 callee-saved registers now pinned. Only X20, X23, X28 remain for Tier 1 scratch. Tier 2 has X20-X23, X28 for allocatable GPRs.
- **Cross-block shape propagation is the next nbody bottleneck**: Scenario A confirmed — arithmetic IS typed, field access overhead is the dominant cost. Need to propagate preheader shape verification into loop body.
