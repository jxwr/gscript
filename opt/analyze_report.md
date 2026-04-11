# ANALYZE Report — R33: Scalar Promotion Float Gate Fix

**Cycle**: 2026-04-11-scalar-promote-float-gate-fix
**Category**: tier2_float_loop (ceiling override authorized by user_priority.md)
**Target**: nbody (initiative `opt/initiatives/tier2-float-loops.md` Phase 13)

## User Priority Honored

`opt/user_priority.md` (updated 2026-04-11 20:30 post-R32) explicitly authorizes this round:

> R33 MUST: 1. Apply the one-line fix: walk consumers of each GetField to find a GuardType float (the same pattern LICM's whitelist uses). 2. Add a production-pipeline diagnostic test that runs the pass through RunTier2Pipeline on a real nbody proto and asserts the pair count > 0.

The file explicitly grants a one-time **ceiling override** on `tier2_float_loop` (which is at 2 category failures). Ceiling decay resumes if R33 still shows 0% on nbody. Budget cap: ≤30 functional LOC, 1 Coder, no pass-algorithm expansion.

Post-R33 priority order recorded: tier2_float_loop continues (matmul/spectral_norm next), then tier1_dispatch (earliest R35, fresh approach not peephole STR), then field_access (not SimplifyPhisPass).

## Architecture Audit

`rounds_since_arch_audit = 2` → scheduled full audit. Executed `bash scripts/arch_check.sh` and performed quick source walk:

- **File size ⚠ CRITICAL (unchanged from R28 audit)**: `emit_dispatch.go` 971, `graph_builder.go` 955, `tier1_arith.go` 903, `tier1_table.go` 829. None of these are touched by R33. Queued split work remains valid: `emit_branch.go` extract, `graph_builder_feedback.go` extract, `tier1_arith_intspec.go` extract. Not a blocker for this round; re-flag when the next plan touches any of these files.
- **Tier 2 pipeline**: unchanged — still `BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit`. `LoopScalarPromotionPass` is wired after LICM in both `RunTier2Pipeline` and `NewTier2Pipeline` (committed R32/56b19e7). No pipeline order change needed.
- **pass_scalar_promote.go**: 264 lines. Well under the 800 soft cap. No split needed. Algorithm audited in R32 sanity; only the gate at line 99 is wrong (A1).
- **Tech-debt markers**: 2 (unchanged). No new TODO/HACK added since R28.
- **Test coverage**: 27 source files still without test files (same count as R21). R33 adds a new test file for pass_scalar_promote's production path — does not resolve any of the 27 gaps, not a regression.
- **Pattern across R31/R32**: two consecutive rounds landed unit-green passes that were silent no-ops on production IR (R31 SimplifyPhisPass, R32 LoopScalarPromotionPass). The diagnostic gap has been formalized: `user_priority.md` §R33 REVIEW items mandates that every new Tier 2 pass requires a real-pipeline diagnostic test via `RunTier2Pipeline` or `compileTier2()`. This plan's Task 1 test is the first under that rule.

No updates required to `docs-internal/architecture/constraints.md` — nothing structural changed since R28, and R32's unit-test-vs-production drift finding is already captured in user_priority.md's REVIEW items for harness enforcement rather than as an architecture constraint.

## Gap Classification

From `benchmarks/data/reference.json` vs `benchmarks/data/latest.json` (median-of-5, frozen baseline per P5):

| Category | Benchmarks (non-excluded) | Ratio vs LuaJIT | Ceiling? |
|----------|---------------------------|-----------------|----------|
| tier2_float_loop | nbody 0.248/0.035 (7.1×), spectral_norm 0.046/0.007 (6.6×), matmul 0.120/0.023 (5.2×), mandelbrot 0.063/0.057 (1.1×) | aggregate 5-7× | **2 failures — overridden by user** |
| field_access | sieve | small | 2 failures |
| tier1_dispatch | — | — | 3 failures (all decayed from prior wins) |
| allocation_heavy | object_creation 1.053/ref 0.764 (+37.8% drift), binary_trees | large drift | 0 |
| other | sort +19%, coroutine_bench +18.4% (both drift vs reference) | — | 0 |

**`opt/authoritative-context.json` drift selection**: CONTEXT_GATHER's drift-driven top-3 were `object_creation` (+37.8%), `sort` (+19%), `coroutine_bench` (+18.4%) — none in the user-prioritized float-loop category. Per harness User Priority Rule, user_priority.md overrides automatic ROI selection but NOT the Ceiling Rule; user_priority.md explicitly grants the ceiling override for R33 only. Drift candidates are documented for future rounds but deferred.

## Blocked Categories

- tier1_dispatch (3 failures, decaying — earliest re-entry R35+ per user_priority.md)
- field_access (2 failures; decaying — earliest re-entry R34+ per user_priority.md)

## Active Initiatives

- **tier2-float-loops** (paused → reactivated R32, Phase 13 in progress). R33 is the gate-fix follow-up to R32's infrastructure landing.
- **tier1-call-overhead**: inactive (last touched R29 root-cause + R30 revert).

## Initiative Retrospective (tier2-float-loops)

`tier2-float-loops` has 2 `no_change` outcomes in its last 4 rounds (R23 guard-hoist, R32 scalar-promote). Per harness rule this would normally trigger a "continue or close" decision. R33 is explicitly a **surgical continuation** per user_priority.md: R32's algorithm was correct and the miss was a 1-line type-gate bug, not an approach failure. Data-backed justification: the R32 post-round re-run observed all 9 loop-carried pairs still present in post-pipeline IR, confirming the pass ran zero transformations — i.e., the "no_change" was not "the transform didn't help" but "the transform didn't execute." R33 converts R32 from a silent no-op to a measurable transform; if the wall-time delta is still ≤1% after R33, THAT is the signal to count a real category failure and pivot.

## Selected Target

- **Category**: tier2_float_loop
- **Initiative**: `opt/initiatives/tier2-float-loops.md` (Phase 13)
- **Reason**: user_priority.md R33 directive + ceiling override. Constraints check: `pass_scalar_promote.go` is 264 LOC (under cap), no file touched by R33 is ⚠-flagged.
- **Benchmarks**: nbody (primary). Secondary: if the fix generalizes, spectral_norm / matmul / mandelbrot could see effects on their inner float-field loops, but none predicted HIGH confidence.

## Architectural Insight

**Design-level observation**: this bug is the second instance of a broader pattern — a Tier 2 pass's classifier reading `instr.Type` directly rather than consulting the "shape-on-the-wire" that the graph builder actually produces. R31 (SimplifyPhisPass) missed because production `compileTier2` already collapses trivial phis upstream; R32 missed because production GetField is `TypeAny` with a trailing GuardType. In both cases, the pass's hand-written fixture skipped a real graph-builder phase. Structural fix: R33's production-pipeline diagnostic test is the first enforcement of the "every new Tier 2 pass needs a real-pipeline diagnostic test" rule recorded in user_priority.md for the harness. This is a template for future passes, not a local fix only.

Not architectural enough to warrant a new pass/IR/constraint this round; the local one-line fix is the right granularity per user directive.

## Prior Art Research

**Research sub-agent NOT spawned** this round. Rationale: (a) user_priority.md specifies the exact fix and algorithm, (b) knowledge-base-first rule — `opt/initiatives/tier2-float-loops.md` already documents LLVM `promoteLoopAccessesToScalars` prior art from R32, (c) 50-tool-call token guard: Research agents were the #1 waste vector in R17/R32. The authoritative references for the consumer-GuardType detection pattern are in the project source itself (`pass_licm.go:575`, `feedback_getfield_integration_test.go:90-106`, `nbody_production_diag_test.go:153`), read directly in Step 3.

### Knowledge Base

No new KB file. The fix is a gate-classifier change, not a new technique. `opt/knowledge/` already carries the scalar-promotion background from R32.

## Source Code Findings

### Files Read
- `internal/methodjit/pass_scalar_promote.go` (full, 264 lines) — confirms A1, A4.
- `internal/methodjit/graph_builder.go` lines 610-700 — confirms A2 exactly: OpGetField is unconditionally emitted with TypeAny at line 669, and an OpGuardType consumer is appended at line 673 when feedback is monomorphic. Same pattern for OpGetTable at lines 628/632.
- `internal/methodjit/pass_scalar_promote_test.go` — confirms the R32 unit tests construct `OpGetField, Type: TypeFloat` directly (line 54), side-stepping the production gate bug.
- `internal/methodjit/r32_nbody_loop_carried_test.go` — template for the production-pipeline diagnostic test R33 adds; already shows the TieringManager → RunTier2Pipeline → pair-count pattern.
- `internal/methodjit/nbody_production_diag_test.go` — further template for real-pipeline diagnostics.
- `internal/methodjit/feedback_getfield_integration_test.go:90-106` — the existing test that checks "OpGuardType appears after OpGetField in the IR". Confirms the consumer-scan pattern A2 describes is already established in the codebase.
- `internal/methodjit/pass_licm.go` — confirms OpGuardType is in the hoist whitelist (line 575).

### Diagnostic Data

**Diagnostic sub-agent NOT spawned.** Authoritative evidence for the gate bug is:
1. **Direct file:line read** of the two relevant code sites (pass_scalar_promote.go:99, graph_builder.go:669-676). P2 evidence: deterministic and reproducible.
2. **R32 post-round re-run** of `TestR32_NbodyLoopCarried` recorded in `opt/state.json` previous_rounds[2026-04-11-loop-scalar-promote-nbody].summary: "post-pipeline IR shows all 9 loop-carried pairs still present". This IS production-pipeline evidence (RunTier2Pipeline, not profileTier2Func), written by the R32 round itself.
3. **docs/42-the-field-that-stayed-in-a-register.md** (R32 blog): "nbody's j-loop body has six loop-carried (obj,field) pairs. Three of them — bi.vx, bi.vy, bi.vz — are on bi, which is loop-invariant across the j-loop."

`opt/authoritative-context.json` was generated this round but selected top-3 regressed benchmarks by drift (object_creation, sort, coroutine_bench) not R33's target. Per harness: user_priority.md overrides drift selection, and direct file:line source reads satisfy P2. Not a violation of P3 because `authoritative-context.json` is consulted, its selection logic is acknowledged in the plan assumptions, and the plan's evidence chain uses derivable-from-code + cited-evidence types (both accepted by plan_check).

### Actual Bottleneck (data-backed, prior-round production evidence)

- j-loop body = 526 ARM64 instructions (R32 disasm, production path).
- 33% memory, 22% MOV/MOVK, 16% box/unbox, 15% branches, 7% guards, **only 5.5% float compute** (R32 disasm category breakdown).
- 6 loop-carried pairs observed; 3 promotable (bi-invariant); 3 non-promotable (bj changes per j-iter).
- Each promoted pair removes 1 LDR + 1 STR per j-iter. 3 pairs × 2 = 6 LDR/STR removed per j-iter = ~1.1% of the loop body by instruction count. Halved for M4 superscalar (R23 calibration) ≈ −2 to −5% wall-time. Confidence MEDIUM (A5).

## Plan Summary

One-line-class change to the `OpGetField` classification in `pass_scalar_promote.go` (walk same-block consumers for an `OpGuardType(TypeFloat)` whose arg is this GetField) + one new production-pipeline diagnostic test (`TestR33_ScalarPromoteFiresOnNbody`) asserting ≥3 loop-carried pairs are promoted on real nbody IR after `RunTier2Pipeline`. Budget: ≤30 functional LOC, 1 Coder, 1 commit, 2 files. Expected: nbody −4% MEDIUM confidence; HIGH confidence the pass starts firing at all. Key risk: M4 superscalar may still hide the 6-LDR/STR savings per iter (consistent with R23), in which case R33 counts as a real tier2_float_loop category failure and the category sits for 3 rounds.
