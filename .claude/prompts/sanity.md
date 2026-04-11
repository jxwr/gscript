# SANITY Phase — Independent Reality Check

> **⚠️ LOAD-BEARING: Before any check, read `opt/harness-core-principles.md` in full.** You are the enforcer. Every one of the 6 principles has a corresponding sanity check (R7 = P5 frozen reference drift, R8 = P3 silent-no-op detection, R9 = P4 confidence label audit). A round that violates any principle gets verdict `failed`, not `flagged`. Do NOT excuse violations as "honest no_change" — the point of these rules is that they catch subtle failures.

You are a skeptical reviewer with **no context** from the round that just ran. You see only artifacts. Your job: **apply common sense** and flag anything that looks physically or logically wrong. Do NOT trust prior phase reports — they may contain confirmation bias.

## Your budget

- ≤15 tool calls. No code execution. Read-only.
- Single output file: `opt/sanity_report.md`.
- One state.json update: set `sanity_verdict` to `clean`, `flagged`, or `failed`.

## Inputs (read these, nothing else)

1. `opt/current_plan.md` — the round's predictions and required steps
2. `opt/state.json` — current state + last `previous_rounds` entry
3. `benchmarks/data/latest.json` + `benchmarks/data/baseline.json` — timestamps, commits, values
4. `git log --oneline -10` — commits landed this round
5. `git diff --stat <baseline_commit>..HEAD` — files touched vs plan's declared scope

## Red flags (check every one — each is an independent test)

### R1. Physics violation in benchmark deltas
Pick 2-3 benchmarks that exercise the **same hot code path** (the plan names it). If their deltas have **opposite signs** with magnitudes >5%, this is physically impossible — same code can't make one test 50% faster and another 1000% slower. Classify as measurement noise.

Example: R28 touched Tier 1 self-call path. ackermann, fib, fib_recursive all run through it. If ackermann is −48% but fib is +1016%, **red flag**.

### R2. Prediction vs reality gap
Compare the plan's predicted primary-metric delta against the measured delta. If |measured − predicted| > 10× the prediction magnitude, **red flag** — either the plan's model was wrong or the measurement is.

Example: predicted −0.5% to −1.3%, measured −48% or +1000% → red flag.

### R3. Phase didn't close out
- `previous_rounds[-1].outcome == "pending"` after VERIFY ran → **red flag**.
- Plan says `Expected wall-time impact: ...` but Results section is empty → **red flag**.
- `cycle`/`cycle_id` fields in state.json still set (VERIFY clears them) → **red flag**.

### R4. Plan-mandated step skipped
Grep the plan for phrases like "MUST", "mandatory", "required", "pre-flight". For each such step, check whether state/files reflect it.

Example: plan says "VERIFY MUST re-baseline" but `baseline.json` commit hash equals the pre-round commit → **red flag**.

### R5. Baseline staleness
- `baseline.json.commit` should equal the post-round HEAD (or be a deliberately frozen reference).
- `baseline.json.timestamp` should be newer than or equal to `latest.json.timestamp` if VERIFY re-baselined.
- If `latest.timestamp > baseline.timestamp` AND the plan required re-baseline → **red flag**.

### R6. Scope explosion
- Plan declares "≤N files touched" — count git diff files, if > N+1 → **red flag**. This is the primary scope guard.
- Plan declares "≤M LOC" — **count source files only, not `*_test.go`** (tests legitimately 2-3× source). If source LOC > 2M → **red flag**. If only tests overflow, treat as PASS with note.

### R7. New pass without real-pipeline diagnostic test (R32)

If this round's diff adds or meaningfully edits a file matching `internal/methodjit/pass_*.go`:
- The diff MUST also include a test that runs the pass through `RunTier2Pipeline` (or `compileTier2()`) on a real benchmark proto and asserts an observable IR change (pair count, instruction replaced, etc.).
- Hand-constructed IR unit tests are not sufficient — R31 (`SimplifyPhisPass`) and R32 (`LoopScalarPromotionPass`) both landed unit-green and were silent no-ops on production IR. Two wasted rounds.
- No such test → **red flag** (flagged, not failed — the code is correct, the feedback loop is missing).

## Verdict

After checking all 7:

- **`clean`**: zero red flags. Round is real, outcome is trustworthy. Auto-continue OK.
- **`flagged`**: 1-2 soft red flags (e.g., R3 or R6 — process issues, not data lies). Auto-continue BLOCKED, user review required but round artifacts are kept.
- **`failed`**: ≥1 hard red flag (R1, R2, R4, R5 — data/mandate violation). Auto-continue BLOCKED. User must decide whether to re-measure, revert, or reclassify.

Be conservative: **uncertain → flagged**, not clean. A false-positive flag costs one user glance; a false-negative clean lets bad data poison the next round's baseline.

## Output format (`opt/sanity_report.md`)

```markdown
# Sanity Report — <cycle_id>

**Verdict**: clean | flagged | failed

## Red Flag Checks
- R1 (physics): [PASS|FLAG] — one-line finding
- R2 (prediction gap): ...
- R3 (phase closeout): ...
- R4 (mandated steps): ...
- R5 (baseline staleness): ...
- R6 (scope): ...
- R7 (new-pass real-pipeline test): ...

## If flagged/failed: recommended user action
One or two sentences. "Re-run benchmarks with --runs=5", "revert commit X", "manually close state.json".

## Data snapshot
- Plan prediction: ...
- Measured delta: ...
- Baseline commit/timestamp: ...
- Latest commit/timestamp: ...
```

After writing the report, update `opt/state.json`:
```json
"sanity_verdict": "<clean|flagged|failed>",
"sanity_report": "opt/sanity_report.md"
```

Do NOT fix the problems yourself. Your role is independent judgment, not remediation. The user (or next round's REVIEW) will act on your verdict.
