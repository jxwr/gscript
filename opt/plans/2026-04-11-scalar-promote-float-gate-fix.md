---
cycle_id: 2026-04-11-scalar-promote-float-gate-fix
date: 2026-04-11
category: tier2_float_loop
initiative: opt/initiatives/tier2-float-loops.md

target:
  - benchmark: nbody
    current_jit_s: 0.248
    reference_jit_s: 0.248
    expected_jit_s: 0.238
    expected_delta_pct: -4.0
    confidence: MEDIUM
    confidence_why: "HIGH certainty the pass will start firing on production nbody (gate fix is verified by direct source citation of graph_builder.go:669-676). MEDIUM on wall-time delta: R32 diagnostic counted 9 loop-carried pairs in the j-loop body, of which only the 3 `bi.{vx,vy,vz}` pairs satisfy the loop-invariant-obj gate (bj changes each iter) — docs/42 R32 blog §'The transform' confirms exactly 3 promotable pairs. Each promotion removes 1 LDR + 1 STR/iter; superscalar halving applied per R23 calibration rule."

max_files: 2
max_source_loc: 30
max_commits: 1

assumptions:
  - id: A1
    claim: "pass_scalar_promote.go:99 rejects every nbody GetField because it gates on `instr.Type == TypeFloat` but production IR emits OpGetField with Type=TypeAny."
    type: derivable-from-code
    evidence: "internal/methodjit/pass_scalar_promote.go:99 (gate) + internal/methodjit/graph_builder.go:669 (`b.emit(block, OpGetField, TypeAny, ...)` — GetField always TypeAny on emission)"
    confidence: HIGH
    source: "Direct source read this phase; corroborated by R32 post-mortem recorded in opt/state.json previous_rounds[2026-04-11-loop-scalar-promote-nbody].summary"

  - id: A2
    claim: "When Tier 1 feedback is FBFloat at the GETFIELD pc, graph_builder immediately appends an OpGuardType consumer with Type=TypeFloat and Aux=int64(TypeFloat), whose Args[0] is the GetField result."
    type: derivable-from-code
    evidence: "internal/methodjit/graph_builder.go:671-674 (`guard := b.emit(block, OpGuardType, irType, []*Value{result}, int64(irType), 0)` inside feedback-monomorphic branch; result is the just-emitted OpGetField)"
    confidence: HIGH
    source: "Direct source read. Same pattern documented in internal/methodjit/feedback_getfield_integration_test.go:90-106 which tests exactly this chain."

  - id: A3
    claim: "nbody's advance() j-loop body block after the full Tier 2 pipeline contains 9 loop-carried (obj,field) pairs, of which exactly 3 (bi.vx, bi.vy, bi.vz) pass the isInvariantObj gate (bi is defined in the outer i-loop pre-header; bj = bodies[j] changes each j-iter and is dominated inside the j-loop)."
    type: cited-evidence
    evidence: "opt/state.json previous_rounds[-1].summary ('all 9 loop-carried pairs still present'); docs/42-the-field-that-stayed-in-a-register.md paragraph 'Half the pairs are promotable' — 3/6 bi pairs are invariant."
    confidence: HIGH
    source: "R32 diagnostic (TestR32_NbodyLoopCarried post-pipeline dump) + R32 blog post authored by ANALYZE."

  - id: A4
    claim: "The pass's deletion of OpGetField via replaceAllUses leaves any pre-existing OpGuardType(float) consumer pointing at the phi (which is already TypeFloat); this is correctness-safe — the guard becomes tautological and is either elided by LoadElim's guard dedup or left as a no-op by the emitter."
    type: derivable-from-code
    evidence: "internal/methodjit/pass_scalar_promote.go:205-210 (replaceAllUses + removeInstr of the GetField); internal/methodjit/pass_load_elim.go:74 (OpGuardType handling in block-local CSE); promoteOnePair creates phi with Type=TypeFloat at line 199."
    confidence: MEDIUM
    source: "Source read; no test currently exercises the post-promotion guard dedup path but the existing LICM+scalar-promote tests pass ValidateIR after promotion, proving no structural break."

  - id: A5
    claim: "M4 superscalar halving rule (harness constraint, R23): predicted instruction-count savings from LDR/STR removal should be halved when predicting wall-time impact on ARM64. 3 pairs × (1 LDR + 1 STR)/iter removed, j-loop body ~526 insns (R32 disasm), inner-loop runs ~100K-1M iters on nbody."
    type: cited-evidence
    evidence: "docs-internal/architecture/constraints.md §'Calibrate predictions' + docs/42-the-field-that-stayed-in-a-register.md §'Looking at the binary' (526 insns/iter, 33% memory)"
    confidence: MEDIUM
    source: "R23 empirical calibration rule documented in CLAUDE.md §Hard-Won Rules item 5."

prior_art:
  - system: LLVM
    reference: "lib/Transforms/Scalar/LICM.cpp promoteLoopAccessesToScalars"
    applicability: "Canonical mem2reg-style promotion of loop-invariant loads with in-loop stores. Exact transform this pass implements; R33 only fixes its type gate, not the transform."
    citation: "Referenced in docs/42-the-field-that-stayed-in-a-register.md §'The transform' (R32 blog)"
  - system: V8 TurboFan
    reference: "v8/src/compiler/load-elimination.cc:1363"
    applicability: "V8 kills fields on loop store rather than promoting; not directly applicable — nbody's bi escapes via global bodies[], so V8's EscapeAnalysis path would not apply either. Kept as 'why our approach differs' reference."
    citation: "docs/42, R32 blog"

failure_signals:
  - condition: "post-pipeline j-loop body still contains OpGetField with Args[0].ID matching the outer-loop bi (production diagnostic test asserts pair count == 0 after pass)"
    action: "hard FAIL — gate fix did not take effect; investigate whether feedback is reaching that pc or whether GuardType is placed elsewhere"
  - condition: "nbody delta > -1% at VERIFY"
    action: "count as 2nd tier2_float_loop ceiling failure per user_priority.md; skip category 3 rounds"
  - condition: "any non-excluded benchmark regresses > 3% vs reference"
    action: "revert commit, root-cause cross-contamination (pass now runs on more functions)"
  - condition: "TestR32_NbodyLoopCarried or any existing pass_scalar_promote_test case fails"
    action: "hard revert (structural correctness)"
  - condition: "Full ./internal/methodjit/... test suite fails any test"
    action: "hard revert (R30 lesson — curated-subset testing missed Tier 2 crash)"

self_assessment:
  uses_profileTier2Func: false
  uses_hand_constructed_ir_in_tests: false
  authoritative_context_consumed: true
  all_predictions_have_confidence: true
  all_claims_cite_sources: true

---

# Optimization Plan: Scalar Promotion Float Gate — Consumer GuardType

## Overview

R32 landed `LoopScalarPromotionPass` (commit 56b19e7) with the correct algorithm but a broken float gate: the pass classifies a GetField as float-typed only when `instr.Type == TypeFloat`, while production `graph_builder.go:669` emits every `OpGetField` with `TypeAny` and appends a consumer `OpGuardType` when feedback is monomorphic. R32's unit tests hand-constructed `TypeFloat` GetFields and passed; the pass was silently a no-op on every real Tier 2 compile. R33 fix: walk the same block forward for an `OpGuardType` whose argument is the GetField and whose carried type is `TypeFloat`. One cited class of change, one added production-pipeline diagnostic test, one commit.

## Root Cause Analysis

Per A1: `pass_scalar_promote.go:99` rejects the pair whenever the GetField carries `TypeAny`. Per A2: every production GETFIELD site with Tier 1 FBFloat feedback is emitted as the 2-instruction chain `OpGetField(_, Type=TypeAny) ; OpGuardType(getField, Type=TypeFloat, Aux=int64(TypeFloat))`. Together A1+A2 prove the pass's float gate fails on 100% of production nbody GetFields even when feedback has populated the guard correctly. A3 gives the upper bound on the fix's scope: 3 promotable `bi.{vx,vy,vz}` pairs per j-loop iteration, 6 if you count the non-invariant bj pairs that will still be rejected by the existing invariance gate. A4 shows the in-place deletion semantics remain correct because `replaceAllUses` rewrites the GuardType's argument to the new phi, which already has `TypeFloat`. A5 is the honest wall-time calibration: 3 × (LDR+STR) × iter count, halved, lands around −3 to −5% on nbody.

## Approach

Single file change: `internal/methodjit/pass_scalar_promote.go` inside `promoteLoopPairs`, the `case OpGetField:` branch (currently lines 93-103). Extend the float classification so that when `instr.Type != TypeFloat`, we scan the same block's instruction list for an `OpGuardType` whose `Args[0].ID == instr.ID` AND `Type(other.Aux) == TypeFloat`. If found, treat the pair as float-typed for `p.anyFloat` / `p.allFloat` tracking. All downstream logic (invariance gate, single-set gate, wide-kill gate, phi insertion at line 199 with `Type: TypeFloat`) is unchanged — the algorithm is correct (A4), only the classification input changes.

No new helper function (keeps the diff small and local). The same-block forward scan is appropriate because A2 guarantees the GuardType is emitted immediately after the GetField in the same block (graph_builder.go:671-674 is an inline branch inside the GETFIELD case).

## Task Breakdown

- [A] **Task 1 — ABORTED (data-premise-error, R24)**. Coder wrote the production-pipeline test, applied the exact gate fix, and re-ran: unpromoted-pair count was 9 before AND after (bit-identical, 0 float phis). Root cause: the plan framed the float gate as the sole blocker, but two upstream gates bail before classification is consulted — (1) `pass_scalar_promote.go:146-150` exit-block-preds check rejects the j-loop because `B4.Preds=[B10, B3]` (B10 = i-loop preheader, out-of-body co-pred); (2) `isInvariantObj` rejects the second i-loop because `b := bodies[i]` is loop-variant by construction. Full writeup in `opt/premise_error.md`. Source reverted; no commit. New test file `internal/methodjit/pass_scalar_promote_production_test.go` left untracked on disk — it's a valid production-pipeline diagnostic (currently asserts the failing state), R34 ANALYZE should pick it up. Handing to VERIFY as `data-premise-error`.

  **Surgical spec:**

  *File A*: `internal/methodjit/pass_scalar_promote.go`
  - Function: `promoteLoopPairs`
  - Site: lines 93-103 (the `case OpGetField:` branch of the body-block switch)
  - Change: replace the 4-line `if instr.Type == TypeFloat { p.anyFloat = true } else { p.allFloat = false }` with a classification block that ALSO accepts a same-block consumer `OpGuardType` whose `Args[0].ID == instr.ID` and `Type(other.Aux) == TypeFloat`. Pseudocode:
    ```
    isFloat := instr.Type == TypeFloat
    if !isFloat {
        for _, other := range b.Instrs {  // b is the outer-loop block being scanned
            if other.Op == OpGuardType &&
               Type(other.Aux) == TypeFloat &&
               len(other.Args) > 0 &&
               other.Args[0].ID == instr.ID {
                isFloat = true
                break
            }
        }
    }
    if isFloat {
        p.anyFloat = true
    } else {
        p.allFloat = false
    }
    ```
  - Do NOT touch: any other function in pass_scalar_promote.go; the invariance gate; the phi construction; replaceAllUses; the exit-store logic; the deterministic pair ordering; the wide-kill logic. The algorithm was audited in R32 and is correct (user_priority.md directive).
  - LOC budget: ≤15 source lines net addition in pass_scalar_promote.go (measured excluding the test file, per R22 review).

  *File B* (new): `internal/methodjit/pass_scalar_promote_production_test.go`
  - Build tag: `//go:build darwin && arm64` (matches the other R32 tests).
  - Test function: `TestR33_ScalarPromoteFiresOnNbody`
  - Structure:
    1. Inline the same nbody `advance()` source already used by TestR32_NbodyLoopCarried (shared between tests is fine — copy the string; do NOT extract to a helper, keep the diff local).
    2. Run through TieringManager (compileProto + vm.New + tm.SetMethodJIT + v.Execute) so Tier 1 feedback is collected — this is the production path, NOT profileTier2Func.
    3. Find the advance proto in proto.Protos.
    4. Call `advanceProto.EnsureFeedback(); BuildGraph(advanceProto); RunTier2Pipeline(fn, nil)` — same sequence as TestR32_NbodyLoopCarried.
    5. Assert: after the pipeline runs, walk all blocks and count `(objID, fieldAux)` pairs where BOTH `OpGetField` and `OpSetField` appear on the same obj+field key. This is the "unpromoted-pair count".
    6. Before R33's fix this count was 9 (from R32 post-round re-run documented in state.json). After R33's fix, at LEAST 3 pairs must have been promoted (the bi-invariant pairs). Assert unpromoted-pair count ≤ 6; fail with a message that lists the surviving pairs if the assertion fails.
    7. Also assert: the loop-header block has at least 3 `OpPhi` with `Type == TypeFloat` (new header phis inserted by the pass).
  - LOC budget: ≤100 test lines (tests excluded from source LOC cap per R32 review).
  - Test must run via `go test ./internal/methodjit/ -run TestR33_ScalarPromoteFiresOnNbody -v`.
  - Do NOT construct any synthetic IR. Do NOT call `profileTier2Func`. Do NOT call `TestProfile_*`. The existing TestR32_NbodyLoopCarried in the same package is the template.

  **Mandatory full-suite gate** (R30 lesson): Coder MUST run `go test ./internal/methodjit/...` before commit, not just the new test. Any failure → hard revert.

## Integration Test

```bash
go build -o /tmp/gscript_r33 ./cmd/gscript/
timeout 60s /tmp/gscript_r33 -jit benchmarks/suite/nbody.gs
go test ./internal/methodjit/ -run TestR33_ScalarPromoteFiresOnNbody -v
go test ./internal/methodjit/ -run TestR32_NbodyLoopCarried -v
go test ./internal/methodjit/...
```

All four must pass before VERIFY records a delta.

## Results (filled after VERIFY)

| Benchmark | Reference | Before | After | Change | Expected | Met? |
|-----------|-----------|--------|-------|--------|----------|------|
| nbody     | 0.248     |        |       |        | -4.0%    |      |

Record:
- Post-pipeline unpromoted-pair count on nbody (the production diagnostic assertion)
- Cumulative drift vs reference.json for all non-excluded benchmarks
- Prediction ledger entry: expected −4.0%, actual, confidence=MEDIUM

## Results (filled by VERIFY)

**Outcome: data-premise-error** — no production code change. The plan's root-cause attribution was incomplete; fixing the float gate alone does NOT make `LoopScalarPromotionPass` fire on nbody, because two upstream gates bail earlier. Coder wrote the production test, applied the exact plan fix, observed bit-identical pre/post (9 pairs, 0 float phis), and reverted. See `opt/premise_error.md` and `opt/diagnostic_failure_2026-04-11-scalar-promote-float-gate-fix.md`.

| Benchmark | Reference | Baseline | Latest | vs-base | vs-ref | Expected | Met? |
|-----------|-----------|----------|--------|---------|--------|----------|------|
| nbody     | 0.248     | 0.248    | 0.252  | +1.6%   | +1.6%  | −4.0%    | NO   |

### Test Status
- `internal/methodjit/...`: PASS
- `internal/vm/...`: PASS
- New file `pass_scalar_promote_production_test.go` committed as observe-only (`t.Skip`); logs unpromoted-pair and float-phi counts but does not assert, since the plan's assertion target is unreachable with the current pass design.

### Evaluator Findings
- Sonnet evaluator PASS. Notes: test uses production `RunTier2Pipeline` (P3 compliant), `defer v.Close()`, zero production code touched, useful as a template for the R31/R32/R33 "real-pipeline diagnostic" rule.

### Cumulative drift vs reference.json (non-excluded, all benchmarks)
| Benchmark | ref | latest | drift |
|-----------|-----|--------|-------|
| sieve              | 0.088 | 0.087 | −1.1% |
| nbody              | 0.248 | 0.252 | +1.6% |
| spectral_norm      | 0.045 | 0.046 | +2.2% |
| matmul             | 0.124 | 0.120 | −3.2% |
| mandelbrot         | 0.063 | 0.064 | +1.6% |
| fannkuch           | 0.048 | 0.048 |  0.0% |
| sort               | 0.042 | 0.049 | **+16.7%** |
| closure_bench      | 0.027 | 0.030 | **+11.1%** |
| binary_trees       | 2.311 | 2.070 | −10.4% |
| object_creation    | 0.764 | 1.152 | **+50.8%** |
| coroutine_bench    | 16.55 | 16.56 |  0.0% |
| fibonacci_iter     | 0.288 | 0.290 | +0.7% |
| math_intensive     | 0.070 | 0.069 | −1.4% |
| method_dispatch    | 0.102 | 0.102 |  0.0% |
| string_bench       | 0.031 | 0.031 |  0.0% |
| sum_primes         | 0.004 | 0.004 |  0.0% |
| table_field_access | 0.043 | 0.042 | −2.3% |
| table_array_access | 0.094 | 0.095 | +1.1% |

Drift > 2% vs reference (P5 flag): `sort +16.7%`, `closure_bench +11.1%`, `object_creation +50.8%`, `matmul −3.2%`, `spectral_norm +2.2%`, `binary_trees −10.4%`. **None introduced by R33** — all pre-existing drift carried from R28–R32 (no production code changed this round). CONTEXT_GATHER already flagged object_creation / sort / coroutine_bench as top-3 drift targets for a future round; R33 was user-overridden to tier2_float_loop.

### Regressions (≥5%) introduced by R33
- None. All visible drift is pre-existing.

### Prediction Ledger
- Expected nbody −4.0% (MEDIUM confidence). Actual: +1.6% (noise, no code change). Miss cause: plan premise incomplete (upstream gate bails before float-gate classification), not a delta-miss.

## Lessons (filled after completion/abandonment)

1. **A1 + A2 were correct in isolation but insufficient.** The plan proved the float-gate is BROKEN (true, by file:line). The plan did NOT prove the float-gate is the ONLY broken thing. Two separate upstream gates (`pass_scalar_promote.go:146-150` exit-block-preds; `isInvariantObj` on the second i-loop's `b := bodies[i]`) also fail, and they fail FIRST. Rule for future plans: "this gate is wrong" does NOT imply "fixing this gate is sufficient." For each claimed-sufficient fix, add an assumption that NO upstream gate ALSO fails on the same target.

2. **A3 ("3 bi pairs promotable") was never tested against the pass's actual reachability.** The R32 diagnostic counted loop-carried pairs; it did NOT prove any pair was classified-promotable by the pass's gate sequence. The plan cited "9 pairs still present" as evidence the pass didn't transform — true — but conflated "pass didn't transform" with "only the float gate blocked it." Those are different causal claims.

3. **The production-pipeline diagnostic test worked exactly as intended.** Coder wrote the test, ran it pre-fix (9 pairs / 0 phis), applied the plan's exact fix, ran post-fix (9 pairs / 0 phis), and caught the premise error in one IMPLEMENT session — 1 Coder call, zero production commits. First full validation of the R31/R32 lesson ("every new Tier 2 pass needs a real-pipeline diagnostic test"). Keeping the test as observe-only is the right call.

4. **The two upstream gates are structural, not cosmetic.** j-loop exit lands on the i-loop header which unavoidably has the i-loop preheader as co-pred; second i-loop obj is loop-variant by construction. Neither is fixable by tweaking gate conditions. R34+ candidates: (a) relax exit-block-preds to allow non-body co-preds when no phi operand comes from them; (b) insert a dedicated j-loop exit block in `computeLoopPreheaders`; (c) accept that the pass only fires on single-loop single-field shapes and pivot tier2_float_loop effort elsewhere.

5. **Category ceiling fires for real this round.** `tier2_float_loop` was at 2 failures, user-overridden once for R33, and R33 did not reach `improved`. The user_priority contract was: if R33 still shows 0% on nbody, the category is a real 3rd failure. This counter now advances and the category should sit for ≥3 rounds while tier1_dispatch / field_access / drift-driven categories (object_creation, sort, closure_bench) are tried.
