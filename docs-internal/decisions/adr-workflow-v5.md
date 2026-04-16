# ADR: Workflow v5 — Program Loop, Structured Round Cards, 1M-Context Reading Discipline

**Date**: 2026-04-17
**Status**: Accepted (Wave 1)
**Supersedes**: `docs-internal/archive/workflow-v4-plan.md` (moved out of `docs-internal/`)

## Context

Workflow v4 was adopted at 2026-04-13 after v3's multi-session stage chaining produced more bookkeeping than forward motion. v4 explicitly rejected persistent per-round state and collapsed a round to a single Claude session with 6 steps. The first six rounds ran under v4 (2026-04-13 through 2026-04-14):

| Round | Target | Outcome | Net change |
|------:|--------|---------|------------|
| R1 | object_creation | **revert** — hypothesis wrong (GC scan isn't the cost) | 0 |
| R2 | object_creation | **revert** — same-class mistake as R1 | 0 |
| R3 | sieve LuaJIT gap | diagnostic, no code change | 0 |
| R4 | KB correction | meta, rewrote 3 runtime cards | 0 (docs) |
| R5 | binary_trees | **win** −21.4%, Tier 0 gate for tiny recursive allocators | +1 |
| R6 | method_dispatch | **revert** — Gate B doesn't compose with Gate A | 0 |

Aggregate: 1 win, 3 reverts, 1 diagnostic, 1 meta in 6 rounds. Three days of silence followed (R7 never started) — the workflow produced no next direction on its own.

## Problem

Three pathologies are visible in the R1–R6 record. None are implementation bugs; all are workflow shape.

### Pathology 1: Same-class reverts are invisible

R1 (shape-pointer removal for object_creation) and R2 (initial `vm.regs` shrink for the same benchmark) both failed on the identical hypothesis class — "reduce Go GC scan cost by shrinking scanned regions." The R1 commit message explicitly noted this class did not close the drift; the R2 commit message confirms "same-class mistake as round 1." v4 has no mechanism to flag class repetition before R2 was attempted.

### Pathology 2: Round learnings are write-only

R6's commit message contains a detailed cost-model breakdown: Tier 1 BLR → Tier 0 callee = ~700 ns; assumed cost was ~100 ns. This is exactly the input a future round needs to avoid similar compose-failures. It sits in `git log` — no round reads it.

### Pathology 3: Workflow-step omission is silent

`docs-internal/round-direction.md` was last written at R6. When R7 should have started (2026-04-14 through 04-17), Step 3 simply was not invoked. The v4 gates (`kb_check.sh` freshness, `reference.json` drift, test failure, scope-budget breach) are all code-level gates; none detect "no one ran the workflow."

## Decision

Adopt Workflow v5. Three pillars, each addressing one pathology.

### 1. Harness: wrap the round loop in a program loop

Persist the cross-round state v4 lost:

- **`rounds/NNN.yaml`** replaces the overwrite-every-round `docs-internal/round-direction.md`. One structured card per round, committed with the round's closing commit. Schema in `rounds/TEMPLATE.yaml`.
- **`program/ledger.yaml`** aggregates hypothesis classes across all rounds: `class`, `attempts`, `reverts`, `wins`, `prior_reject_rate`, `mitigation_required`. Updated at the close of every round. Addresses Pathology 1.
- **`program/targets.yaml`** — deferred to Wave 2 (see `workflow-v5-plan.md`). Tracks per-benchmark attempted and untaken routes with expected gain.

### 2. Agent: front-load failure cost

v4's reverts were all post-commit. v5 mandates four behaviors that shift the cost forward:

- **Hypothesis-class lookup at Step 3** — agent greps `ledger.yaml`; same-class repetition (`prior_reject_rate > 50%` over ≥ 3 attempts) forces the round type to `strategy` unless a new mitigation is listed.
- **Pre-flight evidence at Step 4** — *Wave 3*. Microbench or Diagnose-oracle must confirm the predicted cost shape before pipeline changes land.
- **Revert autopsy schema** — fixed enum for `revert_class` so the ledger updates mechanically. Addresses Pathology 2.
- **Commit schema** — `round N [win|revert|hold|diag|KB|meta]: <one-liner>`, making the ledger grep-computable.

### 3. Model: 1M context as first-class citizen

v4 Step 3 reads 2 files. v5 Step 3 reads ~300K tokens of problem-space state (all KB cards, recent diag dumps, last 20 round cards, ledger, autopsies). This is well within Opus 4.7's 1M context window and enables:

- Cross-benchmark mechanism synthesis (pick the change that moves multiple benchmarks).
- 20-round pattern match (the R6-style non-scriptable compose-failure insight).
- Counterfactual simulation in writing before pre-flight.

These are instruction-level changes in `CLAUDE.md` — no new scripts, no new parsers.

## Scope — Wave 1 only

This ADR covers Wave 1 (R7-meta). Wave 2 and Wave 3 have mechanical enter-conditions defined in `workflow-v5-plan.md`; they are not part of this ADR and will not be built until those conditions trigger.

Wave 1 deliverables:
- `rounds/` directory with template, README, R001–R006 backfilled, R007 for this round
- `program/ledger.yaml` populated from R1–R6
- `CLAUDE.md` rewritten with v5 round shape
- `scripts/round.sh` with direction-gate check
- `docs-internal/workflow-v4-plan.md` moved to `docs-internal/archive/`

No `internal/` or `kb/` changes.

## Rollback criteria

v5 is reverted at R15 if **all three** of the following hold:

1. Wins / rounds ratio over R7–R15 ≤ 1 / 6 (v4's rate).
2. Any same-class revert occurs (the ledger didn't prevent it).
3. Rounds-with-pre-committed-hypothesis-class < 6 / 8 (the discipline wasn't adopted).

Partial misses trigger a meta-round, not a rollback. Sunk cost is not a reason to keep v5 if all three fail (CLAUDE.md rule 20).

## Alternatives considered

- **Stay on v4, just try harder.** Rejected: R1/R2 demonstrate the class-repetition failure is structural, not motivational.
- **Return to v3-style multi-stage orchestration.** Rejected: v3's archive is the evidence against this. v5 adds one artifact shape (`rounds/NNN.yaml`) and one aggregate (`ledger.yaml`), not a stage machine.
- **Add dashboards and metrics.** Rejected: the bottleneck is cross-round reading, not visibility. A metric nobody reads is another pathology-3 case.
- **Enforce pre-flight evidence in Wave 1.** Rejected: premature. Wave 1 must prove the round card + ledger mechanism works before layering a pre-flight gate on top.

## References

- `docs-internal/workflow-v5-plan.md` — design, three-pillar rationale, wave criteria
- `rounds/README.md` — round card schema and lifecycle
- `program/ledger.yaml` — populated artifact
- `docs-internal/archive/workflow-v4-plan.md` — the v4 plan, retained for historical reference
