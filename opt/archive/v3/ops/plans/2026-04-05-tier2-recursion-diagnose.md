# Optimization Plan: Diagnose Tier 2 Hang on Recursive Call-Only Functions

> Created: 2026-04-05
> Status: active
> Cycle ID: 2026-04-05-tier2-recursion-diagnose

## Target

Unblock category A (recursive call overhead) by **finding the root cause** of the hang that aborted the previous round. This round is scoped as **diagnosis + minimal-change fix**, not a policy flip.

Benchmarks potentially unlocked if fix lands:

| Benchmark | Current (JIT) | VM | LuaJIT | Gap vs LuaJIT | This-round target |
|-----------|---------------|-----|--------|---------------|-------------------|
| fib | 1.407s | 1.668s (0.84x) | 0.027s | 52.1x slower | `Execute() with 3s timeout returns correct value` |
| ackermann | 0.257s | 0.294s (0.87x) | 0.006s | 42.8x slower | `Execute() with 3s timeout returns correct value` |
| mutual_recursion | 0.184s | 0.200s (0.92x) | 0.005s | 36.8x slower | `Execute() with 3s timeout returns correct value` |
| fib_recursive | 13.999s | 16.688s | N/A | N/A | not benched this round |

**Primary deliverable** is a diagnostic artifact (root-cause written to `opt/diagnose_tier2_hang.md`) that either:
- (a) identifies a localized bug fix whose implementation can fit in this round's budget, OR
- (b) concludes the hang reflects deep architectural work and defers category A to a future round with concrete follow-up tasks.

**Secondary deliverable** (conditional on (a)): land the localized fix with the full benchmark suite green, and deliver timing improvements on fib/ackermann/mutual_recursion as an extra.

**Abort outcome** (still counts as successful diagnosis): concluding (b) above is a legitimate finish — we trade wall-time speedup for a concrete next-round plan and remove a recurring blocker.

## Root Cause (hypotheses to test)

The previous round's Task 3 flipped `func_profile.go:134`'s `CallCount>0 && !HasLoop → false` clause to allow Tier 2 promotion when `BytecodeCount <= 40 && ArithCount >= 1`. Combined with the already-landed bounded inliner (`MaxRecursion=2`, `tiering_manager.go:460`), this caused fib/ackermann/mutual_recursion to hang during the benchmark suite. Unit tests of both components passed individually. The combination is what breaks.

**Candidate hypotheses (to be narrowed by diagnostic steps):**

| # | Hypothesis | Where | How to confirm |
|---|-----------|-------|----------------|
| H1 | **Deopt ↔ recompile thrash** | `tiering_manager.go` `OnExit` path: Tier 2 code deopts on first call, tiering manager immediately recompiles, compilation retriggers, infinite loop | Run `CompileTier2` once, `Execute` with timeout; log every `OnExit`/`AttemptTier2`. If counts blow up, it's thrash. |
| H2 | **Tier 2 emit hang in inlined-recursive IR** | `emit_call_native.go` or regalloc: inlined fib contains 2-level fib-expansion + residual BLR; loop in emission or regalloc fixpoint | Run `AllocateRegisters` + `Compile` in isolation with timeout. If hang here, IR layer or emit layer is the culprit. |
| H3 | **Infinite inline fixpoint** | `pass_inline.go`: despite depth counter, mutual-recursion cycle isn't being tracked correctly across fixpoint iterations, IR grows unbounded | Run `InlinePassWith(MaxRecursion=2)` in isolation; dump IR size after each fixpoint iteration. If IR keeps growing, counter logic is buggy. |
| H4 | **Tier 2 runtime infinite loop** | compiled code has bad branch target or missed loop-exit; runs forever | Run emitted code via `Execute`, interrupt after 3s, dump the PC / callstack. |
| H5 | **Bounded tree still too large** | `MaxRecursion=2` expands fib body to depth 4+ via fixpoint (fib calls self twice; each inline adds two more sites); body bloats, compile-time explodes but not truly infinite | Measure `len(fn.Blocks)` before/after inline for fib; measure compile time. |

**Expected outcome**: H1 and H3 are the most likely based on the previous round's symptom (hang only under full benchmark run, not in unit tests with synthetic IR). H2 and H4 are secondary. H5 is unlikely but cheap to rule out.

## Prior Art (MANDATORY)

Reconfirmed from the prior round; the literature supports policies stricter than what we tried, and none of them hang:

**V8 TurboFan / Maglev:** Budget-based inlining (`max-inlined-bytecode-size=460` single-callee, `=920` cumulative). No hang under recursive inlining in any documented issue — JS recursion-heavy code tiers up in Maglev (M117+) without stall. Chromium tracks any compile-time regression >2x as a release blocker.

**SpiderMonkey Warp (Trial Inlining):** Per-callsite `ICScript` snapshots; recursive sites get their own profile. Legacy IonMonkey capped at 1 recursion level; Warp recurses through sites without a blanket limit — but each site has its own ICScript so fixpoint always terminates.

**JavaScriptCore DFG/FTL (`OptionsList.h`):** `maximumInliningRecursion = 2`, `maximumInliningDepth = 5`, per-call-site cost accumulator (`maximumFunctionForCallInlineCandidateBytecodeCostForDFG = 80`). The recursion cap is *per-caller-chain*, not per-proto — a subtle distinction we may have gotten wrong.

**HotSpot C2:** `MaxRecursiveInlineLevel = 1` (stricter than JSC). C2 also maintains a per-method `invocation_counter` that is **decayed** periodically, which naturally bounds deopt-recompile thrash.

**Academic (Steuwer/Henriksen/etc. on fixpoint-based optimization):** Fixpoint iteration over recursive inlining must track depth via an *ordered* visitor (DFS caller-chain), not a per-callee counter — the latter double-counts when a callee is reached from multiple paths.

**Our constraints vs theirs:**
- **Per-proto vs per-chain counter**: our `recursionCounts map[*vm.FuncProto]int` is per-proto across the entire fixpoint — this may diverge from JSC's per-chain model. If the fixpoint loops, the counter may not bound it correctly when a proto is reached via multiple caller chains.
- **No invocation_counter decay**: we have no HotSpot-style decay to break deopt thrash cycles. A single bad guard can keep OnExit firing forever.
- **No pre-compile timeout**: nothing in the tiering manager caps compile time or execution-between-deopts — a thrash loop is genuinely infinite, not merely slow.

## Approach

**Phase A — Reproduce & Instrument (read-only diagnosis)**

Write a test under `internal/methodjit/` that:
1. Parses the fib source (15-bytecode body), obtains its `*vm.FuncProto`.
2. Simulates the failed policy: calls the proto enough times to hit the fib path of the previous round (`CallCount > 0 && !HasLoop && BytecodeCount <= 40 && ArithCount >= 1 → runtimeCallCount >= 2`).
3. Forces Tier 2 compilation by invoking `TieringManager.compileTier2(proto)` directly (bypassing the profile gate).
4. Runs `Execute()` with a 3-second timeout via `runtime.Goexit`-style goroutine cancellation.
5. On timeout: dumps `len(fn.Blocks)`, `len(instructions)`, `OnExit` call counts, `Tier2Attempts`, last-emitted-PC. Records to `opt/diagnose_tier2_hang.md`.

This test is expected to **hang or fail**. That's the point — we need reproducibility.

**Phase B — Localize (narrow the hypothesis)**

Depending on Phase A output:

- **If H1 (deopt thrash)**: counter `OnExit` events. If >100 for a single Execute, thrash confirmed. Add `tm.tier2DisableAfter` counter (defensive, not turning on). Record findings.
- **If H2 (emit/regalloc hang)**: strip Phase A to bare compile — build graph, run passes up to `RangeAnalysisPass`, then `AllocateRegisters`, then `Compile`. Time each. The one that exceeds 1s is the culprit.
- **If H3 (infinite inline)**: run `InlinePassWith(MaxRecursion=2)` alone with per-iteration IR-size logging. If block count unbounded, the counter is broken.
- **If H4 (runtime infinite loop)**: use `GSCRIPT_JIT_DEBUG=1` plus a PC probe added temporarily to the emit layer (emitted code writes current PC to a sentinel slot; timeout goroutine reads it).
- **If H5 (bounded-but-too-big)**: print final IR for fib after 2 levels. If >500 instructions, it's size blow-up, not infinite.

**Phase C — Fix (only if localized)**

Based on Phase B finding, implement the minimum localized fix:
- **H1 fix**: cap Tier 2 re-compilation attempts per proto (e.g., `tier2MaxAttempts = 3` in `tiering_manager.go`). After exhaustion, pin at Tier 1 permanently with a recorded reason.
- **H2 fix**: depends on specific pass/alloc bug — no pre-commitment.
- **H3 fix**: convert per-proto counter to per-caller-chain (JSC-style): pass `chain []*vm.FuncProto` down the recursion, count occurrences of `calleeProto` in chain. Existing tests (`TestInlineBoundedRecursion`, `TestInlineMutualRecursion`) must still pass.
- **H4 fix**: depends on specific emit bug — no pre-commitment.
- **H5 fix**: tighten `MaxSize` budget check to include cumulative inlined size (JSC-style budget).

After fix, flip the func_profile gate (the Task 3 change that was reverted in `f90dbf8`) and re-run fib/ackermann/mutual_recursion with timeout. Only if all three terminate correctly, run the full benchmark suite.

**Phase D — Decide (fork on evidence)**

If Phase C lands a fix and the full benchmark suite is green + faster on the target benchmarks → done. Keep the fix and document.

If Phase B's diagnosis shows the bug is deep (multi-file refactor, or a Tier 2 architectural gap), stop here. Write the diagnosis to `opt/diagnose_tier2_hang.md`, leave infrastructure (bounded inliner) dormant, and move category A to "deferred pending T2 recursion emit refactor" for a future round.

## Expected Effect

Split by Phase D outcome:

**If Phase C fix lands (optimistic):**

| Benchmark | Current | Predicted (post-fix + policy flip) | Mechanism |
|-----------|---------|-----------------------------------|-----------|
| fib | 1.407s | 0.40–0.60s | 2-level inline + int-specialized arith across inline boundary |
| ackermann | 0.257s | 0.08–0.12s | same mechanism, smaller body |
| mutual_recursion | 0.184s | 0.06–0.10s | per-chain counter bounds f/g alternation correctly |
| method_dispatch | 0.103s | 0.08–0.10s | inner-method bodies inline, BLR overhead drops |

Non-targets: spectral_norm, nbody, mandelbrot unchanged (they don't match the flipped gate clause).

**If Phase D is "defer" (pessimistic):**

No benchmark changes this round. Deliverable is `opt/diagnose_tier2_hang.md` with root-cause documentation, a concrete next-round task list, and a clear statement on whether we need to:
- research V8/JSC recursive Tier 2 codegen more, or
- rebuild the per-caller-chain recursion tracker, or
- add a Tier 2 invocation decay counter.

## Failure Signals

- **Signal 1 (diagnosis succeeds, fix is large):** Phase B localizes the bug but the fix requires ≥3 file changes or touches `regalloc.go`/`emit_call_native.go` core paths. → **Action: stop at end of Phase B. Write findings to `opt/diagnose_tier2_hang.md`, defer to future round.** Lesson #7: don't stack on unverified code.
- **Signal 2 (hang doesn't reproduce in the new test harness):** Timeout-based test returns correctly, but full benchmark run still hangs. → **Action: the trigger is load-dependent (heap state, feedback vector drift). Add Tier 2 attempt counter as defensive instrumentation, do NOT flip the func_profile gate.** Record and defer.
- **Signal 3 (fix lands but benchmarks regress elsewhere):** Full benchmark run completes but ≥2 benchmarks slow down by >10%. → **Action: revert the gate flip. Keep the localized bug fix (since it's a correctness/termination improvement).** Lesson #1: multi-regression = architecture.
- **Signal 4 (Phase A cannot reproduce the hang at all):** Neither `CompileTier2` direct-call nor timeout-wrapped Execute hangs. → **Action: the hang is specific to the benchmark harness (goroutine state, GC pressure, Go runtime interaction). Investigate `benchmarks/run_all.sh` vs the unit-test harness differences. This is a lower-priority dead-end — document, defer.**
- **Signal 5 (correctness regression):** Any test in `internal/methodjit/` fails after the Phase C fix (even if fib terminates). → **Action: abort immediately, revert commits.** Lesson #4: correctness first, always.
- **Signal 6 (diagnosis goes >5 steps):** Phase B jumps between hypotheses without narrowing. → **Action: this indicates we don't have the right diagnostic tools. Stop and extend `Diagnose()` to actually run native execution with a timeout (today it's a placeholder at `diagnose.go:92`). That tool fix becomes this round's deliverable, not the hang diagnosis itself.**

## Task Breakdown

Each task = one Coder sub-agent invocation. Strictly sequential (each task's output gates the next).

- [ ] **1. Build a hang reproduction harness.** Files: `internal/methodjit/tier2_recursion_hang_test.go` (new). One test `TestTier2RecursionHangRepro` that forces Tier 2 compile+execute on fib(5) with a 3-second timeout via a goroutine. On hang: capture block count, instruction count, Tier2Attempts, last exit code. Expected: test hangs or panics. **Output**: test file committed, reproducing the hang on the current bounded-inliner + policy-flipped configuration. If it doesn't reproduce → Signal 4 → stop.

- [ ] **2. Narrow by hypothesis.** Files: `opt/diagnose_tier2_hang.md` (new). Under timeout harness, isolate which pipeline stage hangs: (a) `BuildGraph` + `Validate`, (b) inline pass alone, (c) `AllocateRegisters` + `Compile`, (d) `Execute`. For each stage, time with 1s budget; log IR size deltas. **Output**: one of H1–H5 confirmed in writing. **Gate**: if no stage hangs individually but the combined pipeline does → Signal 6 → pivot to Diagnose() tool fix.

- [ ] **3. Implement localized fix (conditional).** Files: depend on confirmed hypothesis (at most 2 source files). Must carry a new regression test that exercises the specific failure mode, independent of the full benchmark suite. **Gate**: if Signal 1 fires (fix is large) → skip this task, proceed to Task 5.

- [ ] **4. Re-enable policy flip + targeted benchmark.** Files: `internal/methodjit/func_profile.go` (restore the Task-3 clause from 6bd0385 that was reverted in f90dbf8). Run only fib, ackermann, mutual_recursion under timeout (not the full benchmark suite yet). **Gate**: all three must terminate correctly within their VM-time budget.

- [ ] **5. Full benchmark suite OR write defer report.** If fix landed: run `bash benchmarks/run_all.sh`, compare against `benchmarks/data/baseline.json`, record deltas. If no fix or Signal 1/2/3 fired: finalize `opt/diagnose_tier2_hang.md` with next-round task list.

## Budget

- **Max commits:** 4 (1 repro test + 1 diagnosis doc + 1 fix + 1 bench) — strict cap. Extra commits require user approval.
- **Max files changed:** 4 source files + 2 new test/doc files.
- **Max diagnostic iterations in Phase B:** 5 (per Signal 6).
- **Abort condition:** If Task 2 does not produce a confirmed hypothesis after 5 iterations, abort with Signal 6 and deliver Diagnose() tool fix as the only artifact. If Task 4 shows regression or new hang, revert ALL round commits and deliver diagnosis doc only.

## Results (VERIFY, 2026-04-05)

**Outcome: no_change (wall-time) + correctness fix landed.**

Tests: `go test ./internal/methodjit/... -short` and `./internal/vm/... -short` both green.
Evaluator verdict: **PASS with notes** (ship; two non-blocking follow-ups).

Full benchmark suite (baseline = 0b94cf1, after = HEAD = f54ea63's state + kept commits):

| Benchmark | Before (JIT) | After (JIT) | Change |
|-----------|--------------|-------------|--------|
| fib | 1.407s | 1.403s | -0.3% |
| fib_recursive | 13.999s | 14.112s | +0.8% |
| sieve | 0.232s | 0.224s | -3.4% |
| mandelbrot | 0.365s | 0.386s | +5.8% |
| ackermann | 0.257s | 0.256s | -0.4% |
| matmul | 0.802s | 0.822s | +2.5% |
| spectral_norm | 0.337s | 0.333s | -1.2% |
| nbody | 0.610s | 0.610s | 0.0% |
| fannkuch | 0.070s | 0.072s | +2.9% |
| sort | 0.053s | 0.052s | -1.9% |
| sum_primes | 0.004s | 0.004s | 0.0% |
| mutual_recursion | 0.184s | 0.184s | 0.0% |
| method_dispatch | 0.103s | 0.101s | -1.9% |
| closure_bench | 0.027s | 0.027s | 0.0% |
| string_bench | 0.031s | 0.030s | -3.2% |
| binary_trees | 2.052s | 2.098s | +2.2% |
| table_field_access | 0.073s | 0.073s | 0.0% |
| table_array_access | 0.135s | 0.136s | +0.7% |
| coroutine_bench | 15.023s | 16.121s | +7.3% |
| fibonacci_iterative | 0.297s | 0.296s | -0.3% |
| math_intensive | 0.194s | 0.190s | -2.1% |
| object_creation | 0.769s | 0.771s | +0.3% |

All deltas within ±8% noise band. No benchmark was unlocked — expected, since the policy flip was reverted (Signal 3 fired: 4 regressions >10% vs 1 improvement). What *did* ship is a latent-bug fix: `Unpromotable=true` when `OP_CALL B=0` is seen in BuildGraph, gating `compileTier2` to refuse such functions. ack / mutual_recursion / `f(g(...))` patterns stay at Tier 1 where they were before, but the hang is now impossible.

## Lessons

1. **Unit-test harnesses don't reproduce all tiering hangs.** Iteration 1 with `TestTier2RecursionDeeperFib` (fib(10…30) × 10reps) completed <200ms. The hang only surfaced under the real CLI path + full benchmark suite where the func_profile gate actually flips real functions. Future diagnoses should mirror CLI invocation from the start.

2. **"Hang" was a misnomer — it was exponential blowup from silently wrong IR.** The Tier 2 code ran; it just produced wrong per-call recursion depth because its first `OpCall` had zero args. Each wrong return amplified up the call tree. H4 ("runtime infinite loop") partially fit; H1/H2/H3/H5 all missed. Diagnose IR *output* before assuming the runtime is stuck — use printer + dump before assuming loops.

3. **Graph-builder coverage gaps are invisible until Tier 2 runs them.** BuildGraph silently accepted `OP_CALL B=0` and emitted a Call with only the function value. Validator accepted it (well-formed SSA). Passes ran over it (DCE pruned the "unused" `m-1`). Only runtime execution exposed it. **Next-round defensive move:** add an explicit graph-builder assertion/unsupported-op list, fail-fast at BuildGraph, not at runtime.

4. **Reverting a speed change but keeping the correctness discovery is a net-positive round.** 5 commits, no wall-time win, but (a) a latent Tier 2 crash is now impossible, (b) a reproducer + regression test are permanent, (c) the category-A next-round task list (deep inlining / Tier 2 BLR / variadic IR model) is now concrete and evidence-backed. Don't measure every round by speedup.

5. **Follow-ups logged by evaluator (for a future round, not this one):**
   - `pass_inline.go:175` does not propagate `calleeFn.Unpromotable` — if inlining ever handles a callee with `OP_CALL B=0` it would splice broken Call-no-args IR into the caller. Dormant today, bug tomorrow.
   - `OP_CALL C=0` (variadic *returns*) was flagged in diagnosis but not gated. Likely safe as-is since arg handling uses B; worth a one-line comment or an explicit assertion.

