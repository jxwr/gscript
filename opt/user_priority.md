# User Strategic Priority (2026-04-11 18:11)

**This file overrides ANALYZE Step 1's automatic gap classification when present.**

## Priority order for upcoming rounds

1. **field_access** category FIRST
   - Primary target: `sieve` (currently 7.7× LuaJIT, 0.085s vs 0.011s)
   - Secondary: `table_field_access`, `table_array_access`, `method_dispatch` if sieve fully closed
   - This category has `category_failures=1`, not blocked. Initiative: standalone or new.

2. **tier2_float_loop** category AFTER field_access plateaus
   - Primary targets: `nbody` (7.6×), `matmul` (5.7×), `spectral_norm` (5.6×)
   - Initiative `tier2-float-loops.md` has been paused 6 rounds — R29 REVIEW flagged this.
   - User has now un-paused it. ANALYZE should re-read the initiative's backlog and pick the next phase.

3. **DO NOT** return to `tier1_dispatch` (fib/ack work) until both above have had 3+ rounds of effort. That category is at 3 failures and should stay cold.

## Rationale

User explicit directive: stop grinding fib/ack peephole. The board's actual slow benchmarks are in Tier 2 territory (float loops + field access). The 598bc1e fib regression is a known issue; a revisit requires a fresh approach (probably HasOpExits proto flag), not more R30-style guard tweaks.

## How ANALYZE should use this file

- Read this file BEFORE Step 1 gap classification.
- Treat the numbered priority as overriding the automatic category ROI ranking.
- The Ceiling Rule still applies (don't pick a category with `category_failures >= 2`).
- If the user's priority conflicts with a blocked category, pick the next unblocked priority.
- Delete this file when the user's priority list is exhausted OR when the user writes a new directive.
- Mention this file in the analyze report under `## User Priority Honored` section so it's visible.
