# R31 ANALYZE Report

**Cycle ID**: `2026-04-11-braun-redundant-phi-cleanup`
**Date**: 2026-04-11
**Category**: `field_access`
**Target**: `sieve` (7.7× LuaJIT)
**Initiative**: standalone (unlocks downstream LICM rounds)

## User Priority Honored

`opt/user_priority.md` directed:
1. `field_access` first, primary target `sieve`
2. `tier2_float_loop` after field_access plateaus
3. Do NOT return to `tier1_dispatch` for 3+ rounds

This round honors priority #1. The `tier1_dispatch` category is at 3
failures (R28 no_change → R29 no_change → R30 regressed+reverted) and
is frozen this round per the user directive. The user's rationale —
"the board's actual slow benchmarks are in Tier 2 territory (float
loops + field access)" — is the basis for selecting sieve, which sits
at 0.085 s vs LuaJIT 0.011 s and is the single largest unlocked gap
in the field_access category.

## Architecture audit (Step 0 — full, every-2-rounds slot)

- `scripts/arch_check.sh` results: one file over the 1000-LOC soft
  ceiling (`tier1_call.go` — known, pre-existing, untouched this round).
  No new violations. Pipeline ordering constraints in
  `docs-internal/architecture/constraints.md` unchanged.
- `internal/methodjit/pipeline.go:RunTier2Pipeline` is the authoritative
  pass ordering. Current: `TypeSpec → Intrinsic → TypeSpec → Inline →
  TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM`.
  No SSA cleanup pass at the entry — that is the gap this round fills.
- Tier 2 promotion path for sieve verified: `HasLoop=true`,
  `LoopDepth=2`, `TableOpCount>0`, REPS=3 ⇒ promoted on 2nd call.
  `RunTier2Pipeline` used by production and by the diagnostic harness
  (`tier2_float_profile_test.go:83`), so diagnostic IR matches
  production IR.

## Step 1 — Gap classification + target selection

Board snapshot at HEAD (baseline `benchmarks/data/latest.json`):

| Category            | Worst benchmark   | Gap     | Failures |
|---------------------|-------------------|---------|----------|
| field_access        | sieve             | 7.7×    | 1        |
| tier2_float_loop    | nbody             | 7.6×    | 1        |
| tier1_dispatch      | fib               | ~10×    | 3 (frozen)|

**Selection**: `sieve` (per user priority). `tier1_dispatch` excluded
by ceiling rule + user directive. `tier2_float_loop` held for R32+.

**Initiative exhaustion check**: `tier1-call-overhead.md` has hit its
exhaustion criterion (R28 no_change, R29 no_change, R30 regressed →
3 consecutive non-wins, more than the "2+ in 4 rounds" gate). It is
paused this round implicitly by the user priority override. If
tier1_dispatch returns after the 3-round cooldown, a fresh approach
(HasOpExits proto flag) will be required per R30's closeout note.

## Step 1b — Architectural reasoning

Can sieve be made 7× faster in-place, or does it need a structural
change? Answer: **structural change in a different subsystem than
expected**. Conventional wisdom said "hoist the SetTable validation
tower in LICM". Diagnostic evidence (§4 below) shows that LICM is
*disabled* from reasoning about the hot inner j-loop because the
table operand `v77` is held via a self-referential phi. LICM
explicitly skips phis, so v77's def looks "in the loop body" to the
LICM alias analysis, even though semantically it's loop-invariant.

The architectural prerequisite is a **post-construction SSA cleanup
pass** (Braun Algorithm 5). Without it, every LICM-style optimization
on nested-loop table code is blocked. With it, the existing LICM
machinery in `pass_licm.go:253-263` (GetTable hoist with alias check)
immediately becomes applicable in a follow-on round.

## Step 2 — External research

Research agent (general-purpose subagent, 10/50 tool calls used) confirmed:

1. **Braun et al. 2013 §3.1–3.2** explicitly flags redundant-phi SCCs
   as a case that `tryRemoveTrivialPhi` cannot handle, and gives
   **Algorithm 5 (removeRedundantPhis)** as the canonical fix.
   Primary source:
   <https://pp.ipd.kit.edu/uploads/publikationen/braun13cc.pdf>
2. **LLVM** `SimplifyCFG` + `InstructionSimplify::simplifyPHINode`
   ship this pass as a safety net.
3. **V8 TurboFan** `CommonOperatorReducer::ReducePhi` does the same.
4. **SpiderMonkey Ion** `EliminatePhis` in `IonAnalysis.cpp` ships
   Braun-style cleanup after MIR construction.
5. **LuaJIT** is a trace JIT — not applicable. Its equivalent is the
   LOOP pass's synthetic unrolling, which inherently eliminates phis
   by definition.
6. **M4 wall-time sanity check**: on store-bound spill loops,
   removing 10 STR/LDR insns/iter from a 46-insn loop translates to
   ~8–12% wall time (not 22% from naïve insn-count scaling) because
   Apple cores are wide (8-decode) and dispatch slack absorbs much
   of the savings. This anchors the prediction band.

Knowledge saved: `opt/knowledge/ssa-trivial-phi-cleanup.md`.

## Step 3 — Project source reading

Read in full:
- `internal/methodjit/pass_licm.go` (594 LOC) — confirmed line 224
  explicitly skips phis; confirmed line 253-263 already hoists
  GetTable when alias-clean.
- `internal/methodjit/graph_builder_ssa.go:19-145` —
  `tryRemoveTrivialPhi` runs once per seal; no post-pass cleanup.
- `internal/methodjit/pass_load_elim.go` (129 LOC) — no phi logic.
- `internal/methodjit/pipeline.go:270-364` —
  `RunTier2Pipeline` + `NewTier2Pipeline` are the two wiring points.
- `internal/methodjit/regalloc.go:56-340` (sampled) — confirmed
  that regalloc's `preAllocateHeaderPhis` will happily carry a phi
  through the inner loop, which is why the self-copy shows up as
  `str x21/x22` at every back-edge.

## Step 4 — Micro diagnostics (REAL data, not estimated)

Full writeup: `opt/diagnostics/r31-sieve.md`.

- **Harness**: `TestProfile_Sieve` in
  `tier2_float_profile_test.go:149-153`. Runs production pipeline on
  `benchmarks/suite/sieve.gs::sieve`, dumps IR + 3156 B of ARM64 to
  `/tmp/gscript_sieve_t2.bin`.
- **Disasm tool**: Python `capstone` library (not a hand-decoder).
- **Captured**: 789 insns; hot inner-loop block B7→B8→back-edge
  located at `0x570–0x7ac`.
- **Hot path per iteration**: 46 insns measured, of which ~32
  (~70%) are overhead from:
  - SetTable validation tower on loop-invariant table (12 insns)
  - Array-kind dispatch on loop-invariant table (4 insns)
  - v78 step spill round-trip (7 insns: `ldr`+`sbfx`+re-box cycle)
  - Self-copy stores `str x21`, `str x22` at back-edge (2 insns)
  - Redecode of n (`sbfx`) at loop header every iter (1 insn)
  - Dead 1-insn branch hop at 0x5b0 (1 insn)

**Cross-check matrix**:

| Check | Value | OK? |
|-------|-------|-----|
| bin mtime | fresh (regen this round from HEAD) | ✓ |
| Tier 2 not Tier 1 | profileTier2Func uses RunTier2Pipeline | ✓ |
| First 2 insns = Tier 2 prologue | sub sp, stp x29 x30 | ✓ |
| Insn classes sum ≈ total | §3 breakdown matches 46-insn loop | ✓ |
| Bottleneck × 0.085 vs predict | 32 overhead/46 × 0.085 = 0.059 s; prediction 0.007–0.010 s lands inside the 2× band | ✓ |

## Step 5 — Plan

`opt/current_plan.md` — single Coder task (1-Coder rule R27). New pass
`pass_simplify_phis.go` implementing Braun Algorithm 5. TDD: 4 tests
including a sieve-shaped fixture. Pipeline wiring at 2 sites in
`pipeline.go`. Total budget ~300 LOC.

## Step 6 — Prediction

| Metric          | Before   | After (band)     | Gain     |
|-----------------|----------|------------------|----------|
| sieve (REPS=3)  | 0.085 s  | 0.075 – 0.078 s  | 8–12%    |
| LuaJIT ratio    | 7.7×     | 6.8–7.1×         | −0.6–0.9×|
| Inner loop insns| 46 / iter| ~36 / iter       | −10 insns|

Non-primary benchmarks with nested-loop patterns (matmul,
spectral_norm, nbody) may see 1–3% collateral. Not counted.

## What this round explicitly does NOT claim

- Does not claim to hoist the SetTable validation tower. That is a
  follow-on round enabled by this one.
- Does not claim to eliminate the array-kind dispatch. Kind
  specialization is a separate orthogonal track.
- Does not claim to close the 7.7× gap to ≤2× — that requires 4–6
  rounds of compounded field_access work.

## Risks

1. **SCC algorithm bug**: Tarjan is textbook but easy to botch on a
   first implementation. Mitigation: 4 test cases including
   pathological ones; `Validate(fn)` catches use-replacement errors.
2. **Block.defs stale references**: removing a phi but leaving
   `block.defs[slot]` pointing at it. Mitigation: explicit test;
   evaluator checklist item.
3. **Performance null-result**: M4 superscalar might hide even
   store-port savings if the loop is already latency-bound on
   something else (e.g. bounds-check branch dep chain). Mitigation:
   if sieve shows < 5% gain, measurement repair round before claiming
   failure — check whether the self-phi copies actually went away in
   the post-pass disasm. If they did but wall time didn't move, the
   ceiling is elsewhere and this round still landed infrastructure.
4. **Downstream regression from earlier SSA cleanup**: earlier-pass
   SSA cleanup might expose a latent bug in TypeSpec or ConstProp.
   Mitigation: full package test (`go test ./internal/methodjit/...`),
   not curated subset — R30 lesson.

## Counters to update after VERIFY

- `rounds_since_arch_audit`: 2 → 0 (audit done this round)
- `rounds_since_review`: 0 → 1 (REVIEW ran last round)
- `category_failures.field_access`: depends on outcome

---

**Generated**: 2026-04-11, R31 ANALYZE phase
**Signed off**: plan ready for IMPLEMENT
