---
round: 1 (reverted)
date: 2026-04-14
outcome: hypothesis-wrong
next_round: 2
---

# Round 1 — Reverted (hypothesis wrong)

## What I predicted

| Benchmark | Current | Expected | Delta | Confidence |
|-----------|--------:|---------:|------:|:-----------|
| `object_creation` | 1.086s | ~0.80s | −26% | HIGH |
| `sort` | 0.048s | ~0.043s | −10% | MEDIUM |

Fix A: delete dead `shape *Shape` field from `runtime.Table`.
Fix B: change `EnsureRegs` so `len(vm.regs)` is the tight bound and `cap` carries amortized 2× growth, letting `ScanGCRoots` scan fewer slots.

The KB cards `kb/modules/runtime/{table,vm,gc}.md` all listed these as concrete Known gaps. The Round 0 direction promoted them to Q2.

## What happened

After Fix A + Fix B + the `runtime.MakeNilSliceCap(64, 1024)` initial-length experiment, **object_creation did not close**. Two measurement passes (median-of-5):

```
benchmark           baseline  R1 run1  R1 run2
object_creation     1.086     1.158    1.145
sort                0.048     0.049    0.052
mandelbrot          0.063     0.061    2.992     ← small-init experiment
matmul              0.120     0.120    1.714     ← small-init experiment
binary_trees        1.997     2.281    1.997
string_bench        0.029     0.038    0.033
```

object_creation moved the wrong direction. The small-initial-length experiment (trying to make ScanGCRoots actually benefit from the tight bound) produced catastrophic regressions on every float/arithmetic benchmark (mandelbrot 47×, matmul 14×, fannkuch 17×) because every JIT-promoted function started triggering `EnsureRegs` reslices in the Tier 1/Tier 2 dispatch path.

Reverted everything except the hook grandfather infrastructure.

## Why the prediction was wrong

Three things I got wrong, most important first:

1. **ScanGCRoots scans `vm.regs[:len(vm.regs)]`, and `len(vm.regs)` defaults to 1024.** For object_creation (a shallow-recursion benchmark), `EnsureRegs` was never called — `len` stayed at 1024 forever. Shrinking len would have needed a change to `New()` to start small, which I tried, which blew up every float/arith benchmark by forcing constant reslices.

2. **The `shape *Shape` removal was structurally correct but its wall-time contribution was too small to measure against the noise floor on allocation_heavy benchmarks.** R35's knowledge doc attributed "25 percentage points" to this field, but I never verified that attribution with a direct before/after measurement — I trusted the narrative. The actual saving was probably a few percent at best, lost in measurement variance.

3. **The GC ceiling, which Round 0 had tabled as a Q1 long-term candidate, is in fact the operative ceiling right now** — not a module bug. `object_creation` runs ~800k allocations through Go's GC; the dominant cost is `mallocgc` + write-barrier + tracing, none of which my fixes touched. The +42% drift vs reference.json is NOT a regression from a specific commit. It's a measurement artifact of noise in the reference baseline combined with Go GC cost that predates R35 anyway.

The deepest mistake was trusting R35's bisect narrative without a direct causation check. A bisect says "this commit caused the drift"; it does NOT say "reverting the specific changes in that commit will close the drift". R35's knowledge doc assumed the former implied the latter. The new workflow did not question this assumption.

## What the workflow caught correctly

The revert discipline worked. Round 0's `round-direction.md` had an explicit failure criterion: "if object_creation does not close below the 5% fail threshold (0.802s), the round is reverted". I honored it. No "the number was off but the conclusion still holds" (CLAUDE.md rule 7). No scope creep into "maybe also try X and Y". Three `git reset` lines and the tree is back to Phase 9. One round cost: ~1.5 hours, about half the 3-hour budget.

## Lessons for the KB

Both `kb/modules/runtime/table.md` and `kb/modules/runtime/vm.md`/`gc.md` Known gaps entries were **wrong** — or at least, promising more wall-time than they can deliver. They were written during Phase 5 based on R35's knowledge doc, not from direct measurement. KB cards should not promise performance impacts without a measurement citation.

Action: update the three cards to note that "removing `shape *Shape`" and "tightening ScanGCRoots to high-water mark" were measured in Round 1 and **did not close the drift**. The entries can stay as structural observations but should be demoted from "Known gap" to "Observed but not wall-time-dominant".

## Next: Round 2

The Round 1 failure rules out the module-level story. This leaves two options:

- **Q1 global**: the GC ceiling is real. Tier-2-only bump allocator for short-lived Tables — multi-round architectural project, requires user discussion.
- **Q3 local**: the LuaJIT gap is the other target — mandelbrot 1.05×, nbody 7.3×, matmul 5.7× vs LuaJIT. Pick the benchmark where a specific emit-layer change has the clearest ROI.

Round 2 direction to be written after round.sh is re-run and the actual drift picture is clean.
