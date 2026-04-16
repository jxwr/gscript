---
title: Workflow v5 — program loop, not round loop
status: active
supersedes: docs-internal/archive/workflow-v4-plan.md
authored: 2026-04-17
---

# Workflow v5

v4 produced 1 win in 6 rounds (R1–R6). v5 is the diagnosis and replacement. It is not "more process" — it's a different loop shape.

## v4's failure mode, precisely

v4 is a **round loop**: diag → KB check → direction → act → verify → KB update, repeat. Every step reads **project state** (`diag/`, `kb/`). No step reads **problem-space state** (what have we tried, what class of hypothesis has failed, what's the expected gain of each untaken route).

Consequence: R1 and R2 both reverted with the same cost-model-error class, and the workflow had no mechanism to flag the repetition. R6's commit is a self-autopsy of why Gate B didn't compose with Gate A — the most valuable artifact of the round — and nothing reads it next round. `direction.md` didn't exist on 04-17 because step 3 was either skipped or uncommitted; the gates don't catch workflow-step omission. The 1-win / 6-rounds ratio is invisible because nothing computes it.

v4 was also designed for a 200K-token model. Opus 4.7's 1M context can hold the entire problem-space state per round and reason over it directly — v4 leaves that capacity unused.

## What v5 is

A **program loop** wrapping a round loop, with three persistent problem-space artifacts and a 1M-context reading discipline that makes them load-bearing every round.

```
program loop (persistent)
  ├── program/ledger.yaml       hypothesis classes + priors (all rounds)
  ├── program/targets.yaml      benchmark DAG + route bookkeeping   (Wave 2)
  └── rounds/NNN.yaml           round cards (structured, per round)
       │
       └── round loop (per session, 7 steps)
             0. Recap       read ledger + last N round cards + recent autopsies
             1. Diag        scripts/diag.sh all
             2. KB check    scripts/kb_check.sh
             3. Direction   hypothesis-class lookup → round card → direction.md
             4. Pre-flight  microbench / oracle → must match expected cost shape  (Wave 3)
             5. Act         TDD
             6. Verify      median-of-N bench + diag diff
             7. Close       round card outcome + ledger update + autopsy if revert
```

## Three pillars

### Pillar 1 — harness: from round loop to program loop

Three persistent artifacts, each **read every round, written every round**. Not dashboards, not sediment — I/O.

- **`rounds/NNN.yaml`** — one structured card per round. Schema enforced by `rounds/TEMPLATE.yaml`. Replaces `docs-internal/round-direction.md` (which was overwritten every round, losing history).
- **`program/ledger.yaml`** — hypothesis classes aggregated across all rounds. Fields: `class`, `attempts`, `reverts`, `wins`, `prior_reject_rate`, `mitigation_required`. Updated at Step 7 of every round.
- **`program/targets.yaml`** — *Wave 2*. Per-benchmark: current wall, gap factor vs LuaJIT, attempted routes, untaken routes with estimated gain, rounds_consumed, rounds_budget. Triggers per-target strategy rounds.

### Pillar 2 — agent: front-load failure

R1/R2/R6 all failed *after commit*. v5 shifts failure cost forward with four behaviors, each mandatory:

1. **Hypothesis-class lookup (Step 3).** Before writing `direction.md`, agent greps `ledger.yaml` for the hypothesis class. If `prior_reject_rate > 50%` (≥ 3 attempts), the round card must explicitly list the new mitigation that distinguishes this attempt; otherwise the round type flips to `strategy` and no code is written.
2. **Pre-flight evidence (Step 4, Wave 3).** Any round that modifies the pipeline writes a microbench or Diagnose-oracle first, showing the mechanism produces the predicted cost shape. R6 would have been killed here: a Tier 1 → Tier 0 call-cost microbench would show ~700 ns, not the assumed ~100 ns.
3. **Revert autopsy schema.** A revert commit fills the round card's `revert_class` field from a fixed enum (`cost-model-error`, `compose-failure`, `data-premise-error`, `correctness`, `scope-breach`). `ledger.yaml` priors update from this enum — no free-text interpretation.
4. **Commit schema.** Every round-closing commit: `round N [win|revert|hold|diag|KB|meta]: <one-liner>`. Makes the ledger computable by `grep`, not by reading prose.

### Pillar 3 — model: 1M context as first-class

v4's Step 3 reads two files. v5's Step 3 reads the full problem-space state in one shot (~300K tokens, well within 1M):

- All 28 KB cards (~200K)
- `diag/summary.md` + 3–5 relevant benchmark `*.ir.txt` + `*.asm.txt` (~50K)
- Last 20 round cards (~20K)
- `program/ledger.yaml` + `program/targets.yaml` (~10K)
- Last 5 revert autopsies (~10K)

This enables three things v4 couldn't do:

- **Cross-benchmark mechanism synthesis.** Pick the change that moves the most benchmarks, not the biggest single-target drift.
- **20-round pattern match.** R6-style "Gate B doesn't compose with Gate A because the caller is Tier 1" is a non-scriptable insight. Only the model can extract it — and only when the full context is in frame.
- **Counterfactual simulation.** Before pre-flight evidence, agent writes in `direction.md`: "Given this change, I expect sieve to move X%, nbody Y%, object_creation Z%." Past commits show the agent can accurately predict 3/5 of revert cases in writing — making this explicit is the cheapest revert filter.

These are **instruction-level changes**, not code. They live in `CLAUDE.md`'s Step 3 reading list and pre-act discipline.

## Wave-gated rollout

v3 failed by building too much scaffolding up front. v5 only builds Wave N+1 when Wave N's artifacts have ≥ 3 real-round data points, so no piece exists unused.

### Wave 1 (R7-meta — this round)

- `rounds/TEMPLATE.yaml` + `rounds/README.md`
- `rounds/R001.yaml` – `rounds/R006.yaml` backfilled from commit messages
- `program/ledger.yaml` initial population from R1–R6
- `CLAUDE.md` rewritten: Step 0 Recap, Step 3 expanded reading list, Step 7 close-out, commit schema rule
- `scripts/round.sh`: block if latest `rounds/NNN.yaml` is older than `diag/summary.md` at Step 5 (Act)
- `docs-internal/workflow-v4-plan.md` → `docs-internal/archive/`

Wave 1 enters Wave 2 when: ≥ 3 rounds have been run under v5 **and** the ledger has ≥ 6 distinct hypothesis classes **and** at least one round consulted the ledger in direction.md.

### Wave 2 (conditional, R10–R13 range)

- `program/targets.yaml` — per-benchmark DAG + route bookkeeping
- Per-target rounds budget with auto-strategy-round trigger
- Hypothesis-class lookup gate formalized into round card (with `mitigation_required` field becoming mandatory when `prior_reject_rate > 50%`)

Enters Wave 3 when: ledger has ≥ 10 distinct classes and at least one auto-strategy-round has fired.

### Wave 3 (conditional, R15+)

- Pre-flight evidence gate mandatory for pipeline-modifying rounds
- Revert autopsy schema frozen
- Auto meta-round trigger (revert-rate > 50% over last 8 rounds → forced meta round)

## Non-goals

v5 deliberately does **not**:

- Introduce dashboards, plots, or visualizations. Bottleneck is cross-round reading, not visibility.
- Add human approval gates. Agent remains fully autonomous.
- Create per-round prose files other than the structured `rounds/NNN.yaml` card.
- Run multi-session rounds. Rule 17 (one session per round) is unchanged.
- Rewrite `kb/` structure. Existing 28-card system is fine; only the reading discipline changes.

## Success criteria for v5 itself

v5 is evaluated at R15 on three metrics, measured against v4's R1–R6:

| Metric | v4 (R1–R6) | v5 target |
|--------|-----------:|----------:|
| Wins / rounds | 1 / 6 | ≥ 3 / 8 |
| Same-class reverts | 1 (R2 repeats R1) | 0 |
| Rounds with pre-committed hypothesis class | 0 | ≥ 6 / 8 |

If v5 misses all three at R15, it is reverted and a v6 meta-round begins from the learnings — sunk cost is not a reason (CLAUDE.md rule 20).

## File layout delta

```
+ rounds/
+   TEMPLATE.yaml
+   README.md
+   R001.yaml ... R006.yaml (backfilled)
+   R007.yaml (this round)
+ program/
+   ledger.yaml
  docs-internal/
+   archive/
+     workflow-v4-plan.md (moved)
+   decisions/
+     adr-workflow-v5.md
-   round-direction.md  (deprecated; last version preserved via git history)
  CLAUDE.md               (rewritten — v5 round shape)
  scripts/round.sh        (+ direction gate)
```

No changes to `kb/`, `benchmarks/`, `internal/`, or any production code in Wave 1.
