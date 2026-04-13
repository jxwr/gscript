# gs-opt-loop Harness v2 Design

**Inputs**: `opt/knowledge/harness-engineering-principles.md` + `opt/knowledge/harness-audit-2026-04-11.md`
**Constraint**: 1 day implementation budget, 6 staged commits, each independently rollback-able.
**Meta-principle**: layered — outer enforces what inner can violate. Add mechanisms, don't rewrite.
**Explicit non-goals**: no new phases beyond `plan_check`, no rewrite of existing phases, no scope beyond the 5 critical gaps in the audit.

---

## Target state

```
             ┌──────────────────────────────────────┐
             │  L1 orchestrator (optimize.sh)       │
             │  phases: review → analyze →          │
             │    >>> plan_check <<< (NEW)          │
             │    → implement → verify → sanity     │
             └──────────────────────────────────────┘
                            ↑
             ┌──────────────┴──────────────────────┐
             │  L4 data                             │
             │  state.json (+ stall_count,          │
             │               escalated,             │
             │               reference_baseline)    │
             │  opt/prediction_ledger.jsonl  (NEW) │
             │  benchmarks/data/reference.json (NEW) │
             │  opt/round_NN.manifest.json     (optional) │
             └──────────────────────────────────────┘
```

**New failure modes that become legible:**
- silent-no-op (pass landed, production IR unchanged)
- cumulative drift (reference regression >2% even if per-round ≤1%)
- prediction miscalibration (mean |pred − actual| > 3×)
- structural stall (≥3 consecutive no_change with no category change)

---

## Mechanisms (6 commits, in dependency order)

### M1. Reference baseline + sanity R7 cumulative drift check

**Closes**: Gap 2 (P3)
**Prevents**: the R28–R32 cumulative 3-7% slow drift on nbody/sieve/matmul/spectral/mandelbrot that no single sanity run flagged.

**Files**:
- **new**: `benchmarks/data/reference.json` — frozen snapshot. Choose R25 baseline (`a388f78`) but explicitly exclude fib / fib_recursive / mutual_recursion / ackermann (the 4 benchmarks dominated by 598bc1e known regression) from the cumulative check.
- **edit**: `.claude/prompts/sanity.md` — add R7 red-flag check:
  > R7 (cumulative drift): read `benchmarks/data/reference.json`. For each benchmark NOT in `reference.json._excluded`, compute `(latest - reference) / reference`. If any ≥ +2% → **FLAG**. If any ≥ +5% → **FAIL** (hard halt). Report the top 3 drifters.
- **edit**: `state.json` schema — add `reference_baseline: {commit, timestamp, excluded: [...]}`

**Rollback**: `git revert` the commit. reference.json is a data file, safe to remove.

**Smoke test**: write reference.json with current latest as fake reference where nbody is 0.100; manually run sanity; expect R7 to FAIL on nbody +148%.

---

### M2. Outcome enum extension + silent-no-op detection

**Closes**: Gap 4 (P5)
**Prevents**: R31 SimplifyPhisPass + R32 LoopScalarPromotion filed under generic `no_change` so REVIEW couldn't count them as a class.

**Files**:
- **edit**: `.claude/prompts/verify.md` — extend outcome enum with `silent-no-op` (pass shipped, IR unchanged on production proto), `scope-violation` (mechanical file/LOC cap exceeded), `prediction-failure` (|predicted − measured| > 3×). Add logic: VERIFY determines outcome in this order: `regressed` (any bench ≥ +5%) → `silent-no-op` (pass-touching round + production IR unchanged per plan_check evidence) → `improved` (target bench ≥ −3% AND no regressions) → `no_change` (default) → `scope-violation` (overrides no_change if scope exceeded) → `prediction-failure` (overrides no_change if |pred-actual| > 3×).
- **edit**: `.claude/prompts/sanity.md` — R4 becomes stricter: if plan touches `pass_*.go`, require plan_check.md to have run a "post-pipeline IR delta test" and report whether IR changed. No evidence → flag R4.

**Rollback**: `git revert`. Enum values stay backward-compatible since they map to severity (regressed > silent-no-op > no_change).

**Smoke test**: retroactively classify R31 and R32 using the new logic from their existing artifacts. Expect both → `silent-no-op`.

---

### M3. Prediction calibration ledger

**Closes**: Gap 5 (P9)
**Prevents**: Opus consistently optimistic by 5-10× on Tier 2 float rounds without visibility.

**Files**:
- **new**: `opt/prediction_ledger.jsonl` (empty to start). One row per benchmark per round: `{round, cycle_id, benchmark, predicted_delta_pct, measured_delta_pct, category, target_flag, ts}`.
- **edit**: `.claude/prompts/verify.md` — VERIFY Step 2 extracts `Expected Effect:` numbers from `opt/current_plan.md` and appends rows to the ledger. If plan has no numeric predictions for the target, write a row with `predicted_delta_pct: null` and a comment.
- **edit**: `.claude/prompts/review.md` — REVIEW Step 1 reads last 10 ledger rows for `category = current category`. Computes mean |pred − actual|. Writes calibration summary to opt/reviews/<round>.md. If mean drift > 3× → sets `state.json.pessimistic_mode = true`.
- **edit**: `.claude/prompts/analyze.md` — if `pessimistic_mode = true`, ANALYZE must halve all numeric predictions in the plan AND must attach the text "PESSIMISTIC MODE: predictions halved due to calibration drift" to the plan's Expected Effect section. Resets to false after 1 improved round.

**Rollback**: `git revert` + delete opt/prediction_ledger.jsonl. No schema dependencies.

**Smoke test**: seed the ledger with 3 synthetic rows (pred=10%, actual=1% each), run a mock REVIEW, expect pessimistic_mode = true.

---

### M4. REVIEW stall_mode + longitudinal analysis template

**Closes**: Gap 3 (P4)
**Prevents**: REVIEW patching the last failure every round without ever zooming out to multi-round patterns.

**Files**:
- **edit**: `state.json` schema — add `stall_count: 0`, `stall_mode: false`, `escalated: false`. VERIFY increments stall_count on every non-`improved` outcome, resets on `improved`.
- **edit**: `.claude/prompts/review.md` — at start, if `stall_count >= 3`, set `stall_mode = true` and switch to a different prompt template:
  > **STALL MODE ACTIVE** (3+ consecutive non-improved rounds)
  > You MUST produce a longitudinal analysis, not per-round patches.
  > Read the last 5 rounds' plans + sanity reports + outcomes. Answer these 3 questions exactly:
  > 1. What structural pattern connects the 5 failures? (Not "R32 had a type gate bug" — "what CLASS of error did R28-R32 all fall into")
  > 2. What ONE structural change (not 5 small patches) would have prevented the class? Cite P1-P14 from harness-engineering-principles.md.
  > 3. Does this change fit in ≤30 LOC across ≤3 files, or does it require a new phase/data structure?
  > Output: ONE structural proposal in `opt/stall_proposal_R<N>.md`. Mark in review.md: `status: stall_mode — proposal written, user approval required`. Do NOT apply the proposal automatically.
- **edit**: `.claude/optimize.sh` — if REVIEW finished with `stall_mode=true`, halt the cycle with exit 2 and print: "Stall mode triggered. Review opt/stall_proposal_R<N>.md. Approve to continue."

**Rollback**: `git revert`. Orphaned state.json fields are ignored.

**Smoke test**: manually set stall_count=3 in state.json, launch one REVIEW, expect stall_mode output.

---

### M5. plan_check phase (the big one)

**Closes**: Gap 1 (P8) — **the critical one**
**Prevents**: R30 wrong Tier2 crosscut premise, R31 stale profileTier2Func evidence, R32 synthetic-IR type gate. Three consecutive failures, same class, all bypassed existing sanity.

**Files**:
- **new**: `.claude/prompts/plan_check.md` — fresh Opus session, read-only, strictly bounded:
  > You are plan_check. You have NO context from ANALYZE or IMPLEMENT. Your job: read `opt/current_plan.md` and verify each load-bearing assumption against production reality.
  >
  > **Inputs (read these, nothing else)**:
  > - opt/current_plan.md
  > - opt/knowledge/harness-engineering-principles.md (for P8 rationale)
  > - the source files named in the plan (read-only)
  > - the ability to run `go test -run <targetedDiagnose>` or `Diagnose()` via `compileTier2()` — NOT the forbidden `TestProfile_*` / `profileTier2Func`
  >
  > **Procedure**:
  > 1. Extract the plan's "Root Cause" and "Approach" sections. List every declarative claim as an assumption.
  > 2. For each assumption, classify: (a) cited-evidence (plan gives file:line or diagnostic file path), (b) derivable-from-code (you can confirm by reading a function), (c) requires-live-run (you must run a diagnostic on production pipeline), (d) unverifiable.
  > 3. For (a): spot-check the cite actually says what the plan claims. 
  > 4. For (b): read the cited function; confirm or refute.
  > 5. For (c): run ONE production-pipeline diagnostic (e.g. `Diagnose()` on the target function), verify the IR shape / dispatch path / instruction count matches the plan's claim.
  > 6. For (d): flag.
  >
  > **Output**: `opt/plan_check.md` with verdict (`verified` / `flagged` / `failed`) + per-assumption results + any disproved assumption's counter-evidence.
  >
  > **Budget**: ≤15 tool calls, ≤5 file reads, 1 live diagnostic run. Fresh session, no memory from prior phases.
  >
  > **IMPORTANT**: you do NOT propose fixes. If you find a disproved assumption, you flag it and HALT. ANALYZE must rewrite the plan; plan_check runs again on the rewrite. Max 3 rewrite cycles before abandoning the round.
- **edit**: `.claude/optimize.sh` — insert `plan_check` phase between `analyze` and `implement`. Update PHASES array. Add gate: if `plan_check.md` verdict is not `verified`, return exit 2 and print the failed assumptions. Allow retry with `--from=analyze` (ANALYZE re-runs with the plan_check output as extra input).
- **edit**: `.claude/prompts/analyze.md` — when `opt/plan_check.md` exists and verdict != `verified`, ANALYZE reads it and rewrites the plan addressing the flagged assumptions. Also: plan template gains a mandatory section "Assumptions (each with evidence cite)" that plan_check consumes directly.
- **edit**: `opt/plan_template.md` — add the Assumptions section.

**Cost**: ~5M tokens per plan_check run. Offset: prevents 20-30M token wasted rounds. Net positive.

**Rollback**: `git revert` removes plan_check.md prompt + optimize.sh wiring. analyze.md gains extra context consumption but degrades gracefully.

**Smoke test**: manually construct a plan with a clearly false assumption (e.g. "function foo returns int" when foo returns string), run plan_check, expect `failed`.

---

### M6. Capability-ceiling escalation

**Closes**: Gap 3 part 2 (P12)
**Prevents**: loop running indefinitely with no progress after stall_mode has already fired without user response.

**Files**:
- **edit**: `state.json` schema — add `escalated: false`, `escalation_reason: ""`
- **edit**: `.claude/prompts/review.md` stall_mode section — if stall_mode has fired 2 times in a row AND the second stall's structural proposal is substantially similar to the first, set `escalated=true` with reason.
- **edit**: `.claude/optimize.sh` — if `escalated=true`, halt with exit 3 and print:
  > "Harness has reached capability ceiling. Last 2 stall proposals converged to the same structural issue that the loop cannot autonomously fix. User decision required. Read opt/stall_proposal_R<N>.md."
- No auto-retry; user manually clears `escalated` flag after deciding.

**Rollback**: `git revert`.

**Smoke test**: set escalated=true manually, launch optimize.sh, expect halt with exit 3.

---

## Commit plan

| # | Commit title | Files touched | Lines | Risk |
|---|---|---|---|---|
| 1 | `harness: freeze reference baseline + sanity R7 cumulative drift` | reference.json, sanity.md, state.json (schema doc only) | ~80 | low |
| 2 | `harness: extend outcome enum with silent-no-op + scope-violation` | verify.md, sanity.md | ~60 | low |
| 3 | `harness: add prediction calibration ledger + pessimistic mode` | verify.md, review.md, analyze.md, prediction_ledger.jsonl (new) | ~100 | medium |
| 4 | `harness: REVIEW stall_mode + longitudinal analysis template` | review.md, state.json (schema), optimize.sh | ~120 | medium |
| 5 | `harness: plan_check phase — cross-agent plan verification` | plan_check.md (new), optimize.sh, analyze.md, plan_template.md | ~200 | HIGH |
| 6 | `harness: capability-ceiling escalation` | review.md, state.json, optimize.sh | ~40 | low |

**Total**: ~600 lines across 6 commits. Est ~4-6 hours focused work.

**Order rationale**: M1 and M2 are pure additions, safest first. M3 introduces a new data file. M4 changes REVIEW behavior. M5 is the biggest (new phase), lands after infra. M6 is the last safety net.

---

## Validation plan (Task #5 preview)

After all 6 commits land, DO NOT start an autonomous round. Instead run these smoke tests manually, in order:

1. **M1 retroactive**: seed reference.json with R25 data (excluding fib/ack), run sanity alone on current HEAD, expect R7 to FAIL (cumulative drift > 2% on nbody/matmul/spectral).
2. **M2 retroactive**: feed R31 and R32 artifacts to the new outcome classifier manually, expect both → `silent-no-op`.
3. **M3 seed**: populate prediction_ledger with 5 historical rows (R28-R32 plan predictions vs actual), run REVIEW calibration check, expect drift > 3× and pessimistic_mode = true.
4. **M4 trigger**: set stall_count = 3 in state.json, launch REVIEW alone, expect `opt/stall_proposal_R34.md` + exit 2.
5. **M5 probe**: write a known-false plan (e.g. target = "sort", root cause = "Tier 1 not compiled" — easily disprovable), run plan_check alone, expect `failed` verdict.
6. **M6 halt**: set `escalated=true`, launch optimize.sh, expect exit 3.

**Only after all 6 smoke tests pass**, run ONE real round (R34) and observe whether any new mechanism fires. If R34 produces a `silent-no-op` or stall_mode proposal, that's a win for the harness even if the compiler didn't improve.

---

## Explicit confidence statement

**High confidence**: M1, M2, M6 are mechanical. They cannot fail to detect what they're designed to detect (assuming sanity runs them).

**Medium confidence**: M3 (calibration), M4 (stall_mode). These rely on Opus doing something new (longitudinal analysis, calibration awareness). Might not produce great output on first try but the output is an artifact I can iterate on.

**Lower confidence**: M5 (plan_check). This depends on Opus in a fresh session being able to verify assumptions against production reality. The procedure is concrete and scoped, but Opus might still miss subtle production divergence. Mitigations:
- plan_check is READ-ONLY, can't introduce errors, worst case is "PASS when it shouldn't"
- 3 rewrite cycles before abandoning reduces the impact of one false-PASS
- over time, plan_check's failure modes themselves become REVIEW material, and its prompt can tighten

**Key acknowledgement**: even perfect harness v2 doesn't guarantee compiler wins. It guarantees **the next failure will be legible** (classified, cited, cross-verified, halted if novel), which is the prerequisite for informed direction correction. If all the Tier-2 gains at pass level are genuinely exhausted, v2 will produce a clear "escalated" signal after 6-8 rounds instead of silent stalls — and that's a success outcome for v2 even though it's a failure outcome for the compiler's immediate board position.
