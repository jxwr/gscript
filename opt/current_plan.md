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

## Results (filled after VERIFY)

| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| nbody     | 0.248s |       |        |
| matmul    | 0.119s |       |        |
| spectral_norm | 0.045s |   |        |
| mandelbrot | 0.062s |      |        |

## Lessons (filled after completion/abandonment)

_TBD_
