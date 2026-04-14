---
round: 6 (null result — Gate B reverted)
date: 2026-04-14
follows: round 5 (binary_trees -21.4%)
---

# Round 6 — Gate B reverted: leaf allocator Tier 0 doesn't compose

## Hypothesis

Extend Round 5's `shouldStayTier0` heuristic to also catch tiny leaf allocators like `method_dispatch.gs`'s `new_point` (7 bytecodes, 1 NEWTABLE, 2 SETFIELDs, 0 calls). Route them to Tier 0 to skip exit-resume overhead, just like Round 5 did for `makeTree`.

Two gates:

| Gate | Criteria | Canonical |
|------|----------|-----------|
| A (Round 5) | ≤25 BC, NewTable > 0, no loop, **has calls** | `makeTree` |
| B (Round 6 attempt) | ≤10 BC, NewTable > 0, no loop, **no calls** | `new_point` |

Predicted: `method_dispatch` JIT 0.093s → ~0.080s (matching VM).

## What happened

`method_dispatch` JIT: 0.093s → 0.095s. **No measurable improvement.** Gate B is a null result.

`binary_trees` JIT: 0.075s (unchanged, Gate A still matches).

## Why Gate B doesn't compose

When Tier-1 compiled `test_points` calls `new_point` via `CALL`, the Tier 1 emit lowers it to a native BLR at `tier1_call.go:172`. The BLR bounds check reads `callee.DirectEntryPtr`; if the callee isn't Tier 1 compiled, `CBZ → slowLabel` falls to the exit-to-Go slow path. The slow path costs ~500 ns per call in save/restore + Go dispatch.

For `new_point` at Tier 1:
- Caller's native BLR: ~10 ns
- Callee's NEWTABLE exit: ~150 ns
- Callee's SETFIELD first-miss exits (×2): ~300 ns
- **Per-call total: ~460 ns**

For `new_point` at Tier 0 (Gate B):
- Caller's slow-path exit: ~500 ns
- Tier 0 interpretation of 7 ops: ~200 ns
- **Per-call total: ~700 ns**

Tier 0 is ~50 % slower per call for `new_point` than Tier 1 — exactly the opposite of what Gate B was trying to achieve. 100 K calls × 240 ns = 24 ms of regression, which is why the numbers moved slightly the wrong direction.

Gate A works for `makeTree` because `makeTree`'s recursive self-call is already going to be a slow-path exit (the callee is `makeTree` itself, which isn't compiled once the gate skips it). The caller never gets a fast BLR path. Skipping compilation is net neutral on call overhead but still eliminates the exit-resume cost inside the body.

Gate B can't work in the same way because the caller is `test_points`, which IS Tier 1 compiled and expects to use native BLR. Dropping the callee to Tier 0 breaks the fast path for a non-gated caller.

## What Round 6 actually delivered

Gate B reverted. Single commit in + single commit out, zero lasting production change. But:

- Confirmed a mechanism constraint (the Tier-1 → Tier-0 call path is slow, so routing leaf callees to Tier 0 loses more than it saves)
- Documented this constraint in the `shouldStayTier0` doc comment so future rounds don't re-attempt Gate B
- method_dispatch and object_creation remain JIT-slower-than-VM. The fix for those would require either (a) a fast Tier-1 → Tier-0 call path, or (b) a native NEWTABLE + SETFIELD path in Tier 1 emit. Both are larger, Q2-scoped changes.

## 6-round summary

| Round | Target | Outcome | Net change |
|------:|--------|---------|------------|
| 1 | object_creation via dead-pointer + scan range | reverted | 0 |
| 2 | object_creation via small initial vm.regs | reverted (fannkuch 17×) | 0 |
| 3 | sieve inner-loop asm diagnostic | no local fix exists | 0 |
| 4 | KB update recording Rounds 1–3 negatives | meta | 0 |
| 5 | **binary_trees −21.4% via Tier 0 gate A** | **WIN** | **1 commit** |
| 6 | method_dispatch via Tier 0 gate B | reverted (null) | 0 |

**6 rounds, 1 code win.** Compared to the v3 harness's R28–R35 (8 rounds, 0 wins, 30+ lingering state files), v4's discipline held — failures were cleanly reverted, meta-work was separated from code work, and the one win came from observation-driven work, not narrative-driven work.

Total working time: ~3 hours across all 6 rounds including benchmarks. Each round's failure was contained to that round. No accumulated rubble.
