# User Strategic Priority (updated 2026-04-11 20:30 after R32)

**This file overrides ANALYZE Step 1's automatic gap classification when present.**

## R33: ceiling override authorized — complete R32's one-line gate fix

R32 landed `LoopScalarPromotionPass` (commit 56b19e7) and the pass infrastructure is correct. The only reason nbody showed 0% is a **one-line type-gate bug** at `pass_scalar_promote.go:99`: the gate checks `instr.Type == TypeFloat` but production IR emits `GetField:any` followed by a trailing `GuardType float`. Unit tests used hand-constructed `TypeFloat` nodes and passed — production saw zero matches.

**R33 MUST:**
1. Apply the one-line fix: walk consumers of each `GetField` to find a `GuardType float` (the same pattern LICM's whitelist uses).
2. Add a **production-pipeline diagnostic test** that runs the pass through `RunTier2Pipeline` on a real nbody proto and asserts the pair count > 0 after the pass runs. This test is the gate against unit/production drift. Second round in a row we've hit this class of bug.
3. Run full-package tests.
4. Benchmark and compare against the R32 baseline (56b19e7).

**Ceiling override**: `tier2_float_loop` is at 2 failures. R33 is authorized to re-enter the category this once because R32 was a known-diagnosed 1-line fix, not an approach failure. If R33 still shows 0% improvement on nbody, THEN count it as a real category failure and skip tier2_float_loop for 3 rounds per the standard decay rule.

**Budget**: ≤30 LOC change (1-line functional + diagnostic test), 1 Coder, tight scope. Do not expand the pass logic itself — the algorithm was audited in R32 and is correct.

## Priority order after R33 (if R33 succeeds)

1. **tier2_float_loop** continues — next phases target matmul (5.7×), spectral_norm (5.6×), and exhaust the float-loop initiative before pivoting.
2. **tier1_dispatch** re-enters after 3+ rounds absent (earliest R35) with a fresh approach — NOT another peephole STR drop. Candidates: Item 5 BL→B tail-thread (research), Item 2 pre-grown goroutine stack.
3. **field_access** re-enters after 3+ rounds absent, NOT via SimplifyPhisPass (R31 proved it's dead ROI on production IR).

## Required REVIEW items (for R33+ REVIEW sessions)

1. **Unit-pass + production-no-op pattern** has now hit two consecutive rounds (R31 stale profileTier2Func, R32 synthetic-IR type gate). Harness rule MUST formalize: every new Tier 2 pass requires a real-pipeline diagnostic test that asserts observable IR changes via `RunTier2Pipeline` or `compileTier2()`. Absence = R4 mandate violation in sanity.
2. **LOC budget miscalibration**: R31 (2.29× overrun) and R32 (plan's own arithmetic was wrong 264+296=560>350). Replace LOC budgets with file-count bounds OR require plan arithmetic check in VERIFY.
3. **`profileTier2Func` structural fix**: R32 review flagged this (rewrite wiring real `FeedbackVector`). Unblock R34+ field_access retry.

## How ANALYZE should use this file

- Read this file BEFORE Step 1 gap classification.
- Honor the numbered priority order.
- The Ceiling Rule still applies UNLESS this file explicitly authorizes an override (R33 is the only current override).
- Mention this file in the analyze report under `## User Priority Honored`.
- Delete this file when R33 completes AND the user writes a new directive OR when the priority list is exhausted.
