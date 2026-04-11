# User Strategic Priority (updated 2026-04-11 19:20 after R31)

**This file overrides ANALYZE Step 1's automatic gap classification when present.**

## Priority order for upcoming rounds

1. ~~**field_access**~~ — **attempted R31, hit ceiling (2 failures)**.
   - R31 landed SimplifyPhisPass (commit c375913) but production IR path already collapses the targeted phis; sieve moved -1.2% (below 5% floor).
   - Ceiling rule: skip for 3 rounds, then eligible with a fresh approach.
   - If retried later, the fresh approach MUST NOT use `profileTier2Func` as evidence — that diagnostic test reads a stale pipeline (R19 and R31 both wasted a round on it).

2. **tier2_float_loop (PRIMARY NOW)**
   - Targets in ROI order: `nbody` (7.6× LuaJIT, 0.251s), `matmul` (5.7×, 0.119s), `spectral_norm` (5.6×, 0.045s), `mandelbrot` (1.1×, 0.063s — almost there).
   - Initiative `opt/initiatives/tier2-float-loops.md` was paused 7 rounds. **User has un-paused it.** ANALYZE must re-read the initiative's backlog and pick the next phase with a fresh approach.
   - **Constraint (hard)**: do NOT use `profileTier2Func` as root-cause evidence. If the analysis needs production IR, instrument `compileTier2()` end-to-end or read ARM64 disasm from a real run, not from the stale diagnostic.

3. **DO NOT** return to `tier1_dispatch` (fib/ack work) — category at 3 failures.

## Required REVIEW item (for next REVIEW session)

**Diagnostic tool debt: `profileTier2Func` is load-bearing AND stale.** Two rounds (R19 table-kind-specialize, R31 Braun phi cleanup) have now wasted an ANALYZE + IMPLEMENT cycle because this diagnostic test reads a pre-production IR pipeline. Options:
- (a) Delete `profileTier2Func` and force ANALYZE to use real `compileTier2()` output.
- (b) Rewrite `profileTier2Func` to call the full production pipeline.
- (c) Gate it behind `//go:build diagnostic_stale` and have ANALYZE refuse to treat its output as evidence.

Pick one before R34 (when field_access becomes eligible again).

## How ANALYZE should use this file

- Read this file BEFORE Step 1 gap classification.
- Honor the numbered priority order.
- The Ceiling Rule still applies (don't pick a category with `category_failures >= 2`).
- If user priority conflicts with ceiling, pick the next unblocked priority.
- Mention this file in the analyze report under `## User Priority Honored`.
- Delete the file when the user's stated priority list is exhausted OR when the user writes a new directive.
