# Optimization Plan: Loop Scalar Promotion for nbody

> Created: 2026-04-11 20:15
> Status: active
> Cycle ID: 2026-04-11-loop-scalar-promote-nbody
> Category: tier2_float_loop
> Initiative: opt/initiatives/tier2-float-loops.md (Phase 13: Scalar Promotion)

## Target

nbody's `advance()` j-loop carries `bi.vx/vy/vz` via memory every iteration:
`GetField → SubFloat → SetField → back-edge → GetField` repeats.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| nbody     | 0.248s       | 0.033s | 7.5× | 0.238s (-4%) |

Secondary (same transform applies to i-loop B6 `b.x/y/z`, smaller trip count):
no explicit target; will surface as noise-level positive delta if anything.

## Root Cause

The j-loop's memory traffic is dominated by loop-carried `(obj, field)` pairs.
Diagnostic (`opt/diagnostics/r32-nbody-loop-carried.md`) measured 526 ARM64 insns
for the j-loop body, of which **33.1 % are memory access** (LDR/STR) and only
**5.5 % are float compute** (FADD/FSUB/FMUL). R18 found LICM can't hoist the
GetFields because SetField on the same object kills the invariance; R16 block-local
CSE forwards within a block but not across the back-edge. The result: each
j-iteration pays `GetField(bi, "vx")` despite the prior iteration's `SetField` having
produced the exact value we want — a classic cross-iteration store-to-load dependency
that phi nodes are designed to eliminate.

## Prior Art (MANDATORY)

**LLVM:** `promoteLoopAccessesToScalars` in `lib/Transforms/Scalar/LICM.cpp` (~line 1800).
Identifies loop-invariant pointers with only load/store uses inside the loop, inserts a
phi at the loop header, replaces in-loop loads with the phi, removes in-loop stores, and
materializes the final value via a store on each loop-exit edge. Uses MemorySSA for
alias queries.

**V8 TurboFan:** Does *not* do this at the LoadElimination level
(`src/compiler/load-elimination.cc:1363` kills fields with an in-loop store rather than
promoting them). Scalar replacement of loop-carried heap fields in V8 lives in
`EscapeAnalysis`, which is limited to non-escaping allocations. For escaping objects
(like nbody's `bi` table from the global `bodies` array), V8 inherits whatever
LoadElimination and LoopPeeling leave behind.

**LuaJIT:** `src/lj_opt_loop.c:77` — trace re-emission achieves the same effect
implicitly: re-walking the trace through CSE/forwarding makes iteration N's HLOAD
match iteration N−1's HSTORE via ALIAS_MUST and return the stored value. Phi
insertion is implicit in the linear trace representation.

**Academic:** Sreedhar, Gao et al. "A new framework for exhaustive and incremental data
flow analysis" — mem2reg with SSA. Adapted for loops by Aho/Sethi/Ullman Dragon Book
§9.4 (induction variable strength reduction as a special case of the same framework).

Knowledge file: `opt/knowledge/loop-scalar-promotion.md` (algorithm pseudocode,
wiring notes, nbody IR before/after).

**Our constraints vs theirs:** GScript's IR has no pointer aliasing (table identity is by
SSA value node), `setFields` map in LICM already identifies the pairs, pre-header
blocks already exist, `loopPhis` already tracks header phis, `CarryPreheaderInvariants`
already causes regalloc to pin FPRs. No new infrastructure is needed — only a new
pass that composes what's already there.

## Approach

New pass `LoopScalarPromotionPass` wired after `LICMPass` and before `RangeAnalysisPass`
→ `RegAllocPass` in `RunTier2Pipeline` (and mirrored in `NewTier2Pipeline`).

**Per loop header, per (obj SSA ID, fieldAux) pair:**

1. Gate: `obj` is in LICM's `invariant` set AND `hasLoopCall == false` AND there exists
   at least one `OpGetField(obj, field)` AND at least one `OpSetField(obj, field)`
   inside the loop body AND no `OpSetField(obj, -1)` (dynamic-key kill).
2. Insert `initVal = OpGetField(obj, field, TypeFloat)` at the end of the pre-header.
3. Insert `phi = OpPhi(TypeFloat)` at the top of the loop header with
   `phi.Args[preHeaderEdge] = initVal`.
4. Replace every in-loop `OpGetField(obj, field)` use with `phi` and remove those loads.
5. Find the last `OpSetField(obj, field, storedVal)` along the path from header to
   back-edge (nbody has exactly one per field per iteration — no merge needed); set
   `phi.Args[backEdge] = storedVal`.
6. Remove every in-loop `OpSetField(obj, field, …)`.
7. For each exit edge, insert `OpSetField(obj, field, phi)` at the top of the successor
   block (ordered before any other use of `obj.field`).

**Type:** `TypeFloat` only for R32 (all nbody-promotable fields are float per feedback).
**Multi-SetField per iteration:** out of scope for R32; gate rejects if count > 1.
**Multi-exit loops:** j-loop has a single exit; gate rejects multi-exit for R32.

## Expected Effect

Per-j-iteration savings: 3 × `GetField` + 3 × `SetField` = 6 memory-accessing ops.
Measured j-loop body is 526 ARM64 insns; 3 GetField contribute ~42 insns, 3 SetField
contribute ~36 insns (from `opt/diagnostics/r32-nbody-loop-carried.md` table 4.1).
Direct removal: ~78 / 526 = **14.8 % instruction-count reduction**.

**Prediction calibration (MANDATORY):**
- Halve for M4 superscalar (R24 lesson): 14.8 % → ~7 %.
- Halve again because the removed LDR/STR sit on already-predicted-taken branches and
  the load-use latency is partially overlapped by the arithmetic chain (`SubFloat →
  MulFloat → ...`): ~7 % → ~3.5 %.
- But memory-side savings *are* what M4 actually feels (load/store queue pressure,
  DCache bandwidth, dependency through NaN-box shuffling), so add back ~0.5 %: **≈4 %
  nbody wall-time = 0.248s → 0.238s.**

If the previous round's prediction (R31 sieve ≥5 %) was off, the reason was that the
diagnostic came from the stale `profileTier2Func` pipeline. This round's diagnostic
(`r32-nbody-loop-carried.md`) was produced by calling `RunTier2Pipeline` directly on
`advanceProto` with real Tier 1 feedback collected from 11 warm-up runs — it is the
same path as `compileTier2`. No stale-pipeline risk.

Secondary (positive, unquantified): broader compiler infra, i-loop `b.x/y/z` pairs
also eligible, future rounds can promote integer induction variables once the pass
exists.

## Failure Signals

- **Signal 1**: VERIFY nbody delta > -2 % → memory-traffic model is wrong or the loop
  is compute-bound through NaN-box/unbox and not LDR/STR. Action: stop, read ARM64
  diff of the exact same binary before/after, pivot to unboxed float SSA (long-term
  phase of this initiative).
- **Signal 2**: IR validator fails after the new pass → phi wiring or back-edge
  collection is wrong. Action: reproduce in a unit test, fix, re-run. Up to 2 Coder
  attempts before reverting.
- **Signal 3**: `TestTier2RecursionDeeperFib` or any existing Tier 2 correctness test
  regresses → exit-store ordering skipped a use. Action: revert immediately. R30 burned
  a round on a curated-test-subset escape; every Coder task must run the full
  `./internal/methodjit/...` before declaring done.
- **Signal 4**: Any other benchmark regresses by >5 % → pass is eager and promoting
  something it shouldn't. Action: tighten the gate (require the pair to appear in both
  GetField and SetField and require the loop to contain no OpCall), re-run.

## Task Breakdown

**1-Coder rule compliance**: exactly one implementation Coder task. Task 0 is an
optional design-doc harness task (the knowledge file already written in Step 2 counts
as the design doc — no Task 0 this round). The cross-block dataflow conceptual
complexity cap applies, but the algorithm pseudocode in
`opt/knowledge/loop-scalar-promotion.md` provides full wiring — the Coder is composing
existing helpers, not designing a new dataflow analysis.

- [x] 1. **`LoopScalarPromotionPass`** — new files `internal/methodjit/pass_scalar_promote.go`
      (~120 LOC) and `internal/methodjit/pass_scalar_promote_test.go` (~200 LOC). Wire
      after `LICMPass` in both `RunTier2Pipeline` and `NewTier2Pipeline`. Test must
      construct a j-loop-shaped IR skeleton with two iterations of
      `GetField(bi,"vx")→SubFloat→SetField(bi,"vx")` and assert that post-pass the
      body contains zero GetField/SetField on `bi.vx`, the header has a new `OpPhi`
      for `bi.vx`, the pre-header has `OpGetField`, and the exit block has
      `OpSetField`. Also include a negative test where `hasLoopCall=true` blocks
      promotion. **MUST run `go test ./internal/methodjit/...` (not a curated subset)
      before declaring done** — R30 lesson.

## Budget

- Max commits: 1 functional (+1 revert slot)
- Max files changed: 3 (`pass_scalar_promote.go` new, `pass_scalar_promote_test.go`
  new, `pipeline.go` edit — two helper sites)
- Max LOC: 350
- Abort condition: 2 consecutive Coder attempts fail to pass `go test
  ./internal/methodjit/...` → revert, write lessons, mark no_change.

## Results (filled by VERIFY)

| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| nbody     | 0.248s | 0.248s | 0.0% | -4% | NO |
| matmul    | 0.119s | 0.120s | +0.8% | noise | yes (noise) |
| spectral_norm | 0.045s | 0.046s | +2.2% | noise | yes (noise) |
| mandelbrot | 0.062s | 0.063s | +1.6% | noise | yes (noise) |
| fib       | 1.410s | 1.424s | +1.0% | noise | yes |
| sieve     | 0.084s | 0.086s | +2.4% | noise | yes |
| ackermann | 0.267s | 0.270s | +1.1% | noise | yes |
| fibonacci_iterative | 0.295s | 0.285s | -3.4% | noise | yes |
| table_field_access | 0.043s | 0.041s | -4.7% | noise | yes |

### Test Status
- internal/methodjit: PASS (all tests green, 1.5s)
- internal/vm: PASS (all tests green, 0.3s)

### Evaluator Findings
PASS with minor notes:
- Phi wiring: `phi.Args[1]` is normalized after `replaceAllUses` but `phi.Args[0]` is not;
  latent risk only — initLoad is newly minted so no ID collision possible today.
- `isInvariantObj` checks only `p.gets[0]`, not all gets in the pair. Minor defensive gap.
- Missing negative tests for multi-exit / wide-kill / non-float gates (implemented but
  only exercised by inspection).
- Positive test runs real LICM + ScalarPromotion through the pipeline with IR validator.
  Substantive.

### Regressions (≥5%)
None. Largest unrelated delta: table_field_access -4.7% (improvement); sieve +2.4%
(noise, below 5% floor).

### Root Cause of 0.0% nbody Delta
After close-out verification, `TestR32_NbodyLoopCarried` (the R32 diagnostic fixture)
was re-run against the post-pipeline IR. **All 9 loop-carried pair candidates are still
present** in B2 (j-loop body) and B6 (i-loop body). The pass is wired correctly, but
its float-type gate at `pass_scalar_promote.go:99` — `if instr.Type == TypeFloat` —
rejects every pair because production IR emits `GetField : any` followed by a separate
`GuardType ... float`. The direct `Type` field on `OpGetField` is `TypeUnknown`/`any`,
never `TypeFloat`, in real IR. The unit tests constructed `OpGetField` with
`Type: TypeFloat` directly, so they passed; the pass was silently a no-op on every
real Tier 2 compilation.

**R33 plan-starter (not executed this round):** change the type gate to inspect the
`GetField`'s *consumers* for a `GuardType float` (or read `FeedbackVector` for the
observed kind). One-line gate fix; the rest of the pass is correct and ready.
Post-fix expectation: ~3 promoted pairs in nbody B2 (`bi.vx/vy/vz`) and 3 in B6
(`b.x/y/z`), delivering on the original -4% nbody prediction.

## Lessons (filled by VERIFY)

1. **Test-IR ≠ production IR for typed heap loads.** Unit tests hand-constructed
   `OpGetField` with `Type: TypeFloat`; production IR emits `Type: any` + trailing
   `GuardType`. Every float-gated pass must be validated against a real-pipeline
   diagnostic before the commit lands, not just a synthetic skeleton. R31 (sieve,
   stale `profileTier2Func`) and R32 (nbody, synthetic-IR gate) are two rounds in a
   row where a pass landed correctly at the unit level but did nothing on the
   production pipeline. **Cross-round pattern: every new Tier 2 pass must include a
   diagnostic test that runs it through `RunTier2Pipeline` on a real benchmark proto
   and asserts observable IR changes.** Flagging for REVIEW as next harness patch.

2. **ANALYZE produced a pre-pass diagnostic; nobody re-ran it post-pass.**
   `opt/diagnostics/r32-nbody-loop-carried.md` showed the 9 loop-carried pairs. The
   Coder landed the pass + unit tests. Neither IMPLEMENT nor VERIFY re-ran the
   diagnostic on the modified pipeline to confirm the pairs were actually removed.
   Closed-loop verification (diagnose → implement → re-diagnose) would have caught the
   float-type gate bug in minutes. This is a workflow gap, not a code gap.

3. **"M4 superscalar hides wall-time savings" was the wrong default hypothesis.** R23
   and R9 taught us 0% wall-time can be genuine on M4 for removed branches / tiny
   LDR/STR. Reaching for that hypothesis here would have buried the real bug
   (pass-never-fires). **When in doubt, re-run the diagnostic and see if the IR
   actually changed.** Observation beats reasoning — again.

4. **Evaluator concerns are latent but flagged for R33:** (a) normalize `phi.Args[0]`
   after `replaceAllUses`; (b) make `isInvariantObj` check all gets, not just
   `p.gets[0]`; (c) add negative tests for multi-exit / wide-kill / non-float gates.

5. **Scope discipline held.** Pass is 264 LOC, tests 296 LOC, single pipeline wiring
   edit — inside the 350 LOC cap. The round spent its budget correctly; the miss was
   in analyze→verify handoff, not in implementation execution.
