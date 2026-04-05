# Optimization Plan: Bounded Recursive Inlining + Tier-up for Call-Heavy Functions

> Created: 2026-04-05
> Status: active
> Cycle ID: 2026-04-05-recursive-inlining

## Target

Unlock Tier 2 for recursive, call-heavy benchmarks. Today they stay at Tier 1 forever and run *worse than the interpreter*.

| Benchmark | Current (JIT) | VM | LuaJIT | Gap vs LuaJIT | Target |
|-----------|---------------|-----|--------|---------------|--------|
| fib | 1.387s | 1.593s (0.87x) | 0.023s | **60.3x slower** | ≤ 0.50s |
| ackermann | 0.253s | 0.295s (0.86x) | 0.011s | **23x slower** | ≤ 0.10s |
| mutual_recursion | 0.184s | 0.217s (0.85x) | 0.006s | **30x slower** | ≤ 0.08s |
| fib_recursive (10 reps) | 13.919s | 15.9s | n/a | — | ≤ 5.0s |
| method_dispatch | 0.099s | 0.085s (1.16x **regression**) | 0.008s | 12x slower | ≤ 0.08s (no regression) |

**Aggregate:** ~1.8s of wall-time gap concentrated in these 5 benchmarks, all sharing the same architectural blocker.

## Root Cause

Two structural barriers prevent recursive/call-heavy functions from reaching Tier 2:

1. **`func_profile.go:128–136` (shouldPromoteTier2):** Any function with `CallCount > 0 && !HasLoop` is hard-excluded from Tier 2. The justification in the comment ("non-loop functions don't benefit enough from type specialization") is an untested hypothesis. It directly contradicts JSC's tier-up model, where *calls weigh 15× a loop iteration*. Pure-call functions should tier up **faster**, not be blocked entirely.

2. **`pass_inline.go:137` (`isRecursive` veto):** The inliner rejects any callee that references its own name via GETGLOBAL. This means fib → fib, ackermann → ackermann, mutual_recursion pairs, and every `f(...); f(...)`-style body is excluded from inlining even one level. We have no bounded-recursion mechanism at all.

The compounding effect: even if we removed barrier #1, fib at Tier 2 without inlining would still emit a full BLR + NaN-box/unbox per recursive call — essentially Tier 1 with extra overhead. The two fixes are complementary.

## Prior Art (MANDATORY)

Research synthesis (see detail in conversation). All four modern production compilers do bounded recursive inlining and none of them gate tier-up on loop presence.

**V8 TurboFan:**
- `--max-inlined-bytecode-size` = 460 (single callee)
- `--max-inlined-bytecode-size-cumulative` = 920 (per caller)
- Depth capped via budget, not a separate recursion flag
- Maglev (M117+) explicitly designed to tier up *any* hot function fast, call-heavy or not

**SpiderMonkey Warp (Firefox 83+):**
- Trial Inlining: per-callsite specialized `ICScript`, profile-guided
- Legacy IonMonkey: 1 recursion level max
- Warp Trial Inlining recurses *through* multiple nesting levels (what IonBuilder couldn't do)

**JavaScriptCore DFG/FTL (from `OptionsList.h`):**
- `maximumFunctionForCallInlineCandidateBytecodeCostForDFG` = **80**
- `maximumFunctionForCallInlineCandidateBytecodeCostForFTL` = **170**
- `maximumInliningDepth` = **5**, `maximumInliningRecursion` = **2**
- Tier-up score: **call = 15pts, loop iter = 1pt** (LLInt→Baseline @500, Baseline→DFG @1000, DFG→FTL @100K)

**HotSpot C2:**
- `MaxInlineSize` = 35 bytecodes (cold), `FreqInlineSize` = 325 (hot)
- `MaxInlineLevel` = 15, `MaxRecursiveInlineLevel` = **1**
- Method compilation fires on invocation count alone (~10K), no loop requirement

**Convergent findings:**
- Every production JIT inlines recursive calls with a bound (JSC=2, HotSpot=1, legacy IonMonkey=1)
- None gate tier-up on loop presence; JSC weights calls **more heavily** than loop iterations
- Inlining size thresholds are small (JSC: 80, HotSpot cold: 35, V8: 460 bytecodes)
- fib/ackermann/mutual_recursion leaf bodies are ~10–30 bytecodes — well inside every threshold

**Our constraints vs theirs:**
- We have a single optimizing tier (Tier 2), not DFG→FTL — so we must handle recursion bound in one tier
- Our inliner resolves callees by GETGLOBAL name + `InlineConfig.Globals` map (no IC-backed feedback yet)
- No tail-call optimization; deep unbounded inlining would explode code size for fib
- Our NaN-boxing overhead at call boundaries is larger than JSC's typed boxed calls, so the *inlining win* should actually be bigger

## Approach

Two file changes + tests. The work is bounded and tightly scoped.

### Change 1 — `pass_inline.go`: bounded recursive inlining

Replace the `isRecursive` hard veto with a depth counter.

- Add `recursionCounts map[*vm.FuncProto]int` to the pass's per-invocation state (passed into `inlineCalls`).
- Add `InlineConfig.MaxRecursion` (default **2**, matching JSC's `maximumInliningRecursion`).
- When considering a callee:
  - Let `depth = recursionCounts[calleeProto]` (zero if not yet seen in this caller).
  - If `depth >= MaxRecursion`: skip (falls back to runtime BLR, bounded tree).
  - Otherwise: inline, then `recursionCounts[calleeProto]++`.
- The `isRecursive` helper is deleted (no longer needed).
- Size budget `MaxSize` stays at 30 bytecodes (fib=15, ackermann=~18, mutual_recursion pair=~12 each — all fit).

For mutual recursion (f→g→f), the same counter applies: we track per-proto occurrences across the entire inline fixpoint for a single compile, so f→g→f stops at depth=2 for each.

### Change 2 — `func_profile.go`: enable Tier-2 for call-heavy no-loop funcs

Replace the `CallCount > 0 && !HasLoop → false` clause with a JSC-style gate:

```go
// Call-heavy, no-loop, small: candidates for recursive inlining at Tier 2.
// Inlining + type specialization eliminate per-call NaN-box/unbox overhead.
if profile.CallCount > 0 && !profile.HasLoop &&
    profile.ArithCount >= 1 &&
    profile.BytecodeCount <= 40 {
    return runtimeCallCount >= 2
}
```

Keep the threshold at 2 calls (same as loop-based clauses). Bytecode cap 40 targets leaf recursive bodies and excludes large orchestration funcs. `ArithCount >= 1` ensures we only promote functions that have work beyond a bare delegation.

For method_dispatch: its inner method bodies are tiny compute (arithmetic + return). Same clause hits them. BLR overhead in the caller loop will be replaced by inlined code.

### Change 3 — tests

- `pass_inline_test.go`: add `TestInlineBoundedRecursion` — build a synthetic recursive proto with MaxRecursion=2, verify the inliner emits exactly 2 levels and then leaves a live CALL.
- `pass_inline_test.go`: add `TestInlineMutualRecursion` — two protos f and g that call each other; verify depth cap applies across the pair.
- `func_profile_test.go` (add if missing): test the new `shouldPromoteTier2` clause with a small call-heavy no-loop profile.

## Expected Effect

Quantified per-benchmark predictions:

| Benchmark | Current | Predicted | Mechanism |
|-----------|---------|-----------|-----------|
| fib | 1.387s | **0.40–0.55s** | 2 levels inlined → 75% of BLRs eliminated; int-specialized arith across inline boundary |
| ackermann | 0.253s | **0.08–0.12s** | same mechanism; smaller body compounds better |
| mutual_recursion | 0.184s | **0.05–0.08s** | f/g collapse into one specialized body for 2 levels |
| fib_recursive | 13.9s | **4–6s** | ×10 loop around fib — linear scaling with fib fix |
| method_dispatch | 0.099s | **0.07–0.09s** | tiny method bodies inline → loop calls become register moves |

Secondary effects to watch:
- fib_iterative, sum_primes (already Tier 2 via loop clause): unchanged
- mandelbrot, spectral_norm, nbody (float-heavy loops): unchanged (don't match new clause)
- binary_trees (allocation-heavy, recursive): might improve slightly if tree-walk hot funcs now tier-up

## Failure Signals

- **Signal 1:** Any of the 22 benchmarks produces wrong results after the fixpoint loop. → **Action: abort, revert immediately.** Correctness is non-negotiable (lesson #4). Diagnose using `Diagnose()` on a minimal reproducer.
- **Signal 2:** 2 or more benchmarks regress by >10% (e.g., Tier 2 compile time spike, inlined body bloats, phi regalloc clash on unrolled graphs). → **Action: architecture problem per lesson #1.** Tighten budget (`MaxRecursion=1` or `MaxSize=20`) before pivot.
- **Signal 3:** fib stays above 1.0s despite both changes landing and Tier 2 firing. → **Action: the bottleneck isn't call overhead after all.** Profile the Tier 2 emitted code for fib, look for NaN-box/unbox inside the inlined body (int spec not happening) or per-iteration deopt guards. Pivot to investigation, not more inlining.
- **Signal 4:** Tier 2 fails to compile fib (graph builder rejects, validator fires, regalloc fails). → **Action: the graph builder assumptions broke under inlined self-recursion.** Use `Diagnose()` on the synthesized IR; fix the specific pass that fails.
- **Signal 5:** Validator errors after inline pass (invariant broken by recursive inlining). → **Action: bug in per-proto counter or remap logic.** This is the highest-risk area given past inline-pass bugs (object_creation phi fix, 2026-04-04).

## Task Breakdown

Each task = one Coder sub-agent invocation. Order is sequential: tests first, inliner change second, profile change third, then integration.

- [x] 1. **Write failing tests first (TDD).** Commit ee269e3. Added TestInlineBoundedRecursion, TestInlineMutualRecursion, and 2 profile-policy tests. Tests compiled and failed with clean assertions.

- [x] 2. **Implement bounded recursive inlining.** Commit 1c71784. Added `isRecursiveOrMutualCached` DFS, threaded `recursionCounts` through the inline pass, wired `MaxRecursion=2` in `tiering_manager.go`. New inline tests pass. `MaxRecursion=0` preserves legacy behavior.

- [x] 3. **Enable Tier 2 for small call-heavy no-loop funcs.** Commit 6bd0385. **REVERTED** in f90dbf8. Ran fine in unit tests but caused fib/ackermann/mutual_recursion to HANG indefinitely at Tier 2 during full benchmark run.

- [x] 4. **Integration check + full benchmark suite.** ABORTED — Signals 1, 2, 3 triggered. Suite hung during BenchmarkGScriptJITAckermannWarm. Reverted Task 3 (commit f90dbf8), dropped the stale tier-up profile tests (commit 30f38d8). Spot checks after revert: ackermann 0.256s, fib 1.408s, mutual_recursion 0.184s (all at pre-change baseline). **Full benchmark suite NOT completed.**

## Budget

- **Max commits:** 5 (3 functional + 1 benchmarks + 1 fix-up)
- **Max files changed:** 5 (`pass_inline.go`, `pass_inline_test.go`, `func_profile.go`, `func_profile_test.go`, benchmark data)
- **Abort condition:** 2 commits without measurable improvement on fib, OR any correctness regression, OR validator errors after inline pass.

## Results (filled after VERIFY)

**ABORTED in IMPLEMENT phase. Task 3 reverted. No full benchmark run completed.**

Spot checks post-revert (pre-change baseline restored):

| Benchmark | Before (baseline) | With Task 2+3 (hung) | After revert |
|-----------|-------------------|----------------------|--------------|
| fib | 1.387s | 2.092s (+51%) | 1.408s |
| ackermann | 0.253s | HANG (>60s) | 0.256s |
| mutual_recursion | 0.184s | HANG (>20s) | 0.184s |

## Lessons (filled after completion/abandonment)

**2026-04-05 — aborted at Task 4 integration.**

- Task 2 (bounded inliner) alone is safe — its tests pass, it remains as dormant infrastructure.
- Task 3 (policy flip) alone turns out to be catastrophic: promoting fib/ackermann/mutual_recursion to Tier 2 caused hangs. The combination with Task 2's inliner (2-level expansion) did not help — may have made the Tier 2 emit path worse.
- Root cause is UNKNOWN. Unit tests passed; only the full benchmark run exposed the hang. Plan anticipated Signal 3 ("fib stays > 1.0s — profile the Tier 2 emitted code"), but the observed failure is stronger: ackermann/mutual_recursion hang forever. Deopt thrashing or a codegen bug in Tier 2's compile path for inlined recursive bodies.
- **Lesson for next round:** Before flipping the tier-up policy, write an integration test that compiles fib at Tier 2, runs it once, and checks it terminates within a small budget. Don't rely on the benchmark harness to catch Tier 2 regressions.
- **Lesson for next round:** The plan's "Signal 2 → tighten budget" advice is not sufficient when the failure is a hang. Tighter `MaxRecursion=1` might still hang if the Tier 2 emit path has a bug on inlined recursive IR. We need to diagnose BEFORE retrying with different knobs.
- **Next investigation step** (for ANALYZE phase of the next round): use `Diagnose()` on fib's Tier 2 compile, then run the emitted code with a timeout. Identify whether the hang is in compile, in codegen, or in execution.
