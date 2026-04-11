# gs-opt-loop Harness Audit — 2026-04-11

**Framework**: 14 principles from `opt/knowledge/harness-engineering-principles.md`
**Evidence window**: R28–R32 (5 rounds, all no_change or regressed, verified via INDEX.md + previous_rounds + bench delta)
**Purpose**: concrete gap list that drives v2 design

---

## Current layers (what exists)

| Layer | Component | Role |
|---|---|---|
| L0 | Claude Code CLI | shell / tool runtime |
| L1 | `.claude/optimize.sh` | 4-phase orchestrator, monitor, phase_log writer |
| L2 | `.claude/prompts/{review,analyze,implement,verify,sanity}.md` | per-phase Opus 4.6 system prompts |
| L3 | sub-agents spawned by Opus: Coder, Diagnostic, Evaluator, Research | bounded specialist tasks |
| L4 | data: state.json, INDEX.md, previous_rounds, phase_log.jsonl, workflow_log.jsonl, sanity_report.md, opt/plans/, opt/reviews/, opt/knowledge/, opt/initiatives/ | artifact ledger |
| L5 | benchmark infra: `benchmarks/data/{latest,baseline,history}.json`, `benchmarks/run_all.sh --runs=5` | measurement |

**Current outcomes enum**: `improved / no_change / regressed / abandoned / data-premise-error`
**Current sanity checks**: R1 physics, R2 prediction-gap, R3 phase-closeout, R4 mandated-steps, R5 baseline-staleness, R6 scope

---

## Principle-by-principle scoring

Scoring: `present` / `partial` / `missing` / `contradicted`

### P1. Feedforward + Feedback — **partial (80% feedback)**

Feedforward (before action):
- ✓ plan_template.md constrains plan shape
- ✓ analyze.md rules + user_priority.md constrain target selection
- ✗ no schema-validation of plan (Opus can write any markdown)
- ✗ no "does the plan cite evidence" check before IMPLEMENT
- ✗ no mechanical scope budget (declared as prose, not enforced)

Feedback (after action):
- ✓ sanity 6 checks, evaluator sub-agent, verify full-package test
- ✓ benchmark median-of-5
- ✓ phase_log.jsonl

**Gap**: Ashby imbalance. Almost all our "learning" is post-hoc. Novel failure modes (R31 unit-pass-production-inert, R32 synthetic-IR type gate) bypass all feedback because they're internally consistent — sanity R1–R6 all PASS while the round is still a production no-op.

### P2. Ashby's Law of Requisite Variety — **mismatched**

- System variety: 22 benchmarks × (Tier 1 | Tier 2 | VM) × (call / loop / alloc / arith / branch / memory) ≈ dozens of bottleneck classes
- Regulator variety: 4 phases + 6 sanity checks + 5 outcome values + 5 category buckets

**Evidence**: R30/R31/R32 each failed in a way the regulator had never catalogued (wrong Tier2 crosscut, stale diagnostic tool, synthetic unit IR). Each failure got its patch added AFTER the round (via REVIEW), but by then the next round had already picked a new class nobody had catalogued either. The regulator is always one failure behind.

### P3. Convergence invariant + reference baseline — **contradicted**

- ✗ `set_baseline.sh` re-points `benchmarks/data/baseline.json` to each round's HEAD in VERIFY
- ✗ sanity R5 explicitly checks `baseline.commit == latest.commit` → PASS always
- ✗ no reference snapshot; no cumulative metric

**Evidence**: current nbody 0.248 vs R25 0.236 (+5%), sieve 0.086 vs 0.083 (+3.6%), matmul 0.120 vs 0.116 (+3.4%), spectral 0.046 vs 0.043 (+7%), mandelbrot 0.063 vs 0.060 (+5%). Each was "within noise" per that round's sanity R1. Cumulative: 6-benchmark slow drift, invisible to the regulator.

### P4. Dual-loop architecture — **missing**

- ✗ no outer loop
- ✗ no stall_mode
- ✗ REVIEW runs every round (interval=1) but only sees 1 round of data at a time; it patches the latest failure, never restructures
- partial: R31 flagged `profileTier2Func` as debt, R32 repeated similar failure class anyway — the REVIEW's own audit was localized

**Evidence**: REVIEW produced distinct localized patches each round (R28: ceiling decay, R29: analyze sub-agent rules, R30: implement full-package gate, R31: analyze forbid profileTier2Func, R32: not yet). None proposed a structural dual-loop. The REVIEW prompt doesn't ask for it.

### P5. Outcome classification + silent-no-op detection — **partial**

- ✓ 5-value enum
- ✗ missing `silent-no-op` (covers R31 SimplifyPhisPass + R32 LoopScalarPromotion — both classified as `no_change` but structurally they're "pass built + zero production effect", a distinct class)
- ✗ missing `scope-violation` (R31 shipped 687 LOC vs ≤300 declared; sanity R6 flagged but didn't enforce an outcome category)
- ✗ missing `prediction-failure` (|pred − actual| > 3×)

**Evidence**: R31 and R32 both `no_change`, but the underlying class is "pass landed, production IR didn't touch it." Without a distinct label, REVIEW can't count occurrences across rounds and can't trigger a meta-rule at threshold 2.

### P6. Write scope mechanical enforcement — **missing**

- ✓ plans declare "≤N files, ≤M LOC" in prose
- ✗ no hook/script enforces it mechanically
- ✗ sanity R6 post-hoc flags but doesn't fail the round

**Evidence**: R31 declared ≤300 LOC, shipped 687. R32 declared ≤350, shipped 560 functional + 254 diagnostic (814 total). Both soft-flagged. Neither escalated.

### P7. Filesystem-first audit trail — **partial / good**

- ✓ opt/plans/, opt/reviews/, opt/sanity_report.md, opt/phase_log.jsonl, opt/workflow_log.jsonl
- ✓ INDEX.md cross-round summary
- ✗ no per-round manifest aggregating (plan + diff stat + bench delta + sanity + outcome) in one file
- ✗ previous_rounds in state.json is the only cross-round ledger but is trimmed to 10 for dump — older history only in INDEX.md prose

**Gap**: REVIEW consumes these by hand-scraping. No structured cross-round query mechanism.

### P8. Cross-agent verification of plans — **missing** ⚠️ CRITICAL

- ✓ sanity phase is an independent session (fresh context)
- ✗ sanity checks CLOSEOUT, not PLAN LOGIC
- ✗ nothing verifies ANALYZE's assumptions against production reality before IMPLEMENT runs
- ✗ no "evidence cited per claim" requirement in plan_template.md

**Evidence**: This is the single biggest gap driving R30/R31/R32 failures.
- R30: ANALYZE wrote "handleNativeCallExit only fires on cold GETGLOBAL miss" — true under normal execution, false under Tier2→Tier1 crosscut. Nothing verified the premise in a live Tier 2 compile. IMPLEMENT landed, full-package test caught it, revert.
- R31: ANALYZE used `profileTier2Func` output as evidence. The test harness was stale. No phase verified "this evidence comes from the production pipeline."
- R32: ANALYZE wrote "pass_scalar_promote.go must check `instr.Type == TypeFloat`" — assumed shape without verifying against `compileTier2()` output. Coder built unit test with hand-constructed TypeFloat nodes. Silent no-op in production.

Three rounds, same class: **plans with unverified assumptions slip through because no phase is tasked with verifying plan premises before implementation**.

### P9. Calibration ledger — **missing**

- ✗ no prediction_ledger.jsonl
- ✓ plans write `Expected Effect: ...` numbers
- ✓ VERIFY records measured delta
- ✗ nothing computes mean |pred − actual| or flags systematic miscalibration

**Evidence**: R28 predicted −0.5 to −1.3% on ack (ack's true delta was not even attributable to R28 — it was a pre-existing 598bc1e side-effect). R31 predicted sieve −8 to −12%, got −1.2%. R32 predicted nbody −4%, got 0%. Opus is miscalibrated optimistic by ~5-10× on Tier 2 float-loop plans. No mechanism surfaces this.

### P10. Reflexion / episodic verbal memory — **partial**

- ✓ opt/knowledge/ has technical notes
- ✓ opt/reviews/ has per-round harness reviews
- ✓ sanity_report.md has verdict + data
- ✗ no first-person "what I would do differently" reflection per round
- ✗ no mechanism that forces REVIEW to read last N reflections together

**Gap**: current structure treats each round as isolated. Reflexion pattern (verbal reflection as episodic memory consumed by next round) isn't implemented.

### P11. Voyager automatic curriculum — **missing**

- ✗ no explore mode
- ✓ user_priority.md can manually direct target selection
- ✗ ANALYZE's target picker is pure exploit (ROI + ceiling)

**Gap**: the loop can't autonomously decide "I don't have enough data to pick a target, do a measurement-only exploration round." This is what would have helped R32 (which went into nbody blind).

### P12. Capability-ceiling acknowledgement + escalation — **missing**

- ✗ no `escalated` flag in state.json
- ✗ no halt-and-notify mechanism if the loop can't make progress
- partial: category_failures caps at 2-per-category but immediately routes to another category

**Evidence**: 5 rounds zero wins. The loop has no way to say "I'm stuck, wake the user." User (you) had to notice and intervene manually. That's exactly the failure mode "harness must self-evolve" is supposed to prevent.

### P13. Progressive scaffolding removal — **missing**

- ✗ no retirement conditions on any rule/phase
- ✓ scaffolding accumulates (R27 small-task cap, R30 full-package gate, R31 profileTier2Func ban, ...)

**Gap**: prompts grow monotonically. No mechanism checks "is this rule still load-bearing or can we remove it." Token cost compounds.

### P14. Meta-Harness = harness optimizing itself — **partial**

- ✓ REVIEW phase exists and is the intended meta-loop
- ✗ REVIEW is per-round (interval=1) and local-patch oriented
- ✗ no "probe round" mechanism to validate harness changes
- ✗ no outcome classification for harness changes themselves ("did this REVIEW's patch actually reduce a failure mode?")

---

## The 5 critical gaps (ranked by evidence strength)

### Gap 1 — P8 cross-agent plan verification [severity: CRITICAL]
Three consecutive rounds (R30, R31, R32) failed because ANALYZE's plan premises were never mechanically checked against production reality before IMPLEMENT ran. **Fix: new phase `plan_check` between ANALYZE and IMPLEMENT.**

### Gap 2 — P3 rolling baseline hides cumulative drift [severity: HIGH]
6 non-recursive benchmarks drifted 3-7% worse over 5 rounds, none flagged individually. **Fix: freeze `benchmarks/data/reference.json`, add sanity R7 cumulative check.**

### Gap 3 — P4/P12 no stall detector + no escalation [severity: HIGH]
5 no-progress rounds, loop kept running. User had to notice and halt manually. **Fix: REVIEW stall_mode activated after 3 consecutive no_change rounds + capability-ceiling escalation.**

### Gap 4 — P5 missing silent-no-op outcome classification [severity: MEDIUM]
R31 and R32 were structurally the same failure class but filed under generic `no_change`. REVIEW couldn't count the pattern. **Fix: extend outcome enum, sanity detects silent-no-op via IR-change check.**

### Gap 5 — P9 prediction calibration missing [severity: MEDIUM]
Opus plans optimistic by 5-10× on Tier 2 float rounds. No visibility. **Fix: `opt/prediction_ledger.jsonl` + pessimistic mode trigger at drift > 3×.**

Gaps 6-10 (lower priority, document for later): P6 scope enforcement, P7 per-round manifest, P10 reflexion, P11 explore mode, P13 retirement conditions.

---

## What NOT to fix (explicitly defer)

- **P2 Ashby match in full**: enumerating every failure mode is unbounded. We accept "always one failure behind" for now, and use P4 stall_mode to catch accumulated novelty.
- **P14 full Meta-Harness 8-step**: outer-loop validation of harness changes is too complex for this milestone. Stall_mode is the MVP.
- **P13 retirement**: not blocking any current failure. Defer.

---

## Confidence statement

This audit is based on observed R28–R32 evidence and documented principles. I have **high confidence** in Gaps 1 and 2 (direct evidence). I have **medium confidence** in Gaps 3, 4, 5 (inferential — the proposed mechanism would have helped, but other fixes might also). I have **low confidence** that fixing these 5 alone will produce the first real win; at best, they'll make the next failure **legible** instead of silent, which is the prerequisite for direction correction.

**Explicit acknowledgement**: adding mechanisms ≠ solving the underlying problem. If the remaining pass-level gaps are genuinely exhausted (Option 3 from earlier — workflow is honest, no wins exist), even perfect harness v2 won't produce wins. It will produce a clear "no tractable single-round fix" signal, which is a different (and more useful) output than silent stalls.
