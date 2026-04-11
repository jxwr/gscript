---
round: R29
cycle_id: 2026-04-11-fib-regression-root-cause
category: tier1_dispatch
initiative: opt/initiatives/tier1-call-overhead.md
target_item: 8
date: 2026-04-11
model: opus-4-6
---

# R29 Analyze Report — Fib regression root cause

## 0. Architecture audit (quick-read; full audit was R28)

`scripts/arch_check.sh` — no new violations since R28. File-size offenders unchanged (`tier1_call.go` 554 LOC, under 1000 cap). `rounds_since_arch_audit` advances to 1 (full audit every 2 rounds). No structural drift to report.

## 1. Gap classification & target selection

**Category**: `tier1_dispatch`. `category_failures[tier1_dispatch] = 1`, well under the ceiling of 2.

**Pattern detector** (`opt/INDEX.md`): item 8 is a **pivot**, not a recurrence. R28 discovered the 598bc1e pivot via user-led bisect after a stale-baseline comparison. No prior round has grappled with the fib regression because it was only surfaced four days ago.

**Target**: item 8 on `opt/initiatives/tier1-call-overhead.md` — root-cause the fib +988% regression introduced by 598bc1e. The initiative explicitly assigns R29 to analysis, R30+ to the fix. This is the **single largest recovery opportunity on the current benchmark board** (fib 1.443s → expected ~0.131s = −1.3s wall time on a 5-benchmark headline suite).

**Ceiling rule**: not engaged. Item 8 has no prior failure count.
**Initiative rule**: followed — R29 is picked because the active initiative explicitly schedules this work.

## 2. Architectural reasoning

598bc1e was a +136/−159 line rewrite of `emitBaselineNativeCall`, framed by its commit message as a 2-insn correctness patch but in reality an entire restructure of the self-call vs normal-call layout. The old code used a shared exit path selected at runtime via a flag register; the new code emits two separate paths inline and adds a `LDR` + `CBZ` guard on the self-call exec label.

The architectural question: **is the `DirectEntryPtr` check on the self-call path intrinsically necessary, or is it defensive code for a bug that the normal-call path's guard already handles?**

The diagnostic data below suggests the latter: the guard is load-bearing only for the **normal-call path** (`BLR X2` through a foreign proto's function pointer). The self-call path jumps to `self_call_entry`, a static label in the currently-executing binary. The `DirectEntryPtr` the self-call guard reads is *the same proto the caller is already executing*. If we trusted the caller's own liveness, we could remove the check.

The catch: `handleNativeCallExit` zeroes `DirectEntryPtr` as a signal to the next entry that "this proto has op-exits; use the slow path." Pre-598bc1e, no callers checked that signal on the self-call path — so when a `BL self_call_entry` self-call exited and its exit handler was re-invoked, it would enter `e.Execute()` again and BL again, building a deep Go-goroutine stack. The 598bc1e check breaks that loop by forcing the slow path once `DirectEntryPtr=0`.

But the slow path is ~100 insns per call with Go dispatch. For fib(35) with ~29M calls, that's the regression.

## 3. External research

Not needed this round. The problem is self-contained to `tier1_call.go` + `tier1_handlers.go`, and the previous rounds' work on tier1 dispatch (R24–R28) already surfaced the relevant concepts. Knowledge base covers the shape of the problem. Skipping external web search (per harness "skip if KB covers").

## 4. Project source reading

Files read end-to-end during R29:

- `internal/methodjit/tier1_call.go:95-463` (the full `emitBaselineNativeCall` and the self-call exec label path)
- `internal/methodjit/tier1_handlers.go:590-670` (`handleNativeCallExit` — confirmed `calleeProto.DirectEntryPtr = 0` at line 637)
- `internal/methodjit/tier1_manager.go:140-450` (`Execute` / `executeInner` / `EvictCompiled` / int-spec deopt recovery)
- `internal/methodjit/tier1_ack_dump_test.go` (existing fixture structure — to clone for fib)
- `benchmarks/suite/fib.gs` and `benchmarks/suite/ackermann.gs` (to understand the call patterns)
- The `598bc1e` diff to identify the exact added lines

## 5. Micro diagnostics (sub-agent)

Full report: `opt/knowledge/r29-fib-root-cause.md`.

**Data collected by the diagnostic sub-agent** (sonnet, instrumented counters added + reverted):

| measurement | fib(35) | ack(3,4) |
|---|---|---|
| `handleNativeCallExit` fires | **1** | **1** |
| `DirectEntryPtr` transitions (non-zero → 0) | 1 | 1 |
| `proto.DirectEntryPtr` before `Execute()` | `0x12c960054` | `0x12c968054` |
| `proto.DirectEntryPtr` after `Execute()` | `0x0` | `0x0` |
| int-spec deopt fires | 0 | 0 |
| Trigger | `OP_GETGLOBAL` miss at pc=5 (cold start) | `OP_GETGLOBAL` miss at pc=9 (cold start) |
| Subsequent calls hitting slow path | ~29M | ~thousands |

**Causal chain**:

1. First self-call: `BL self_call_entry` executes the callee's Tier 1 code
2. Callee hits `OP_GETGLOBAL` with empty value cache (cold start) → exits with code 7 (`ExitBaselineOpExit`)
3. Caller's exit-code dispatch upgrades code 7 to code 8 (`ExitNativeCallExit`) for the BLR self-call case
4. `handleNativeCallExit` runs: sets `calleeProto.DirectEntryPtr = 0`, re-executes callee via `e.Execute()`, resumes the exit-resume op
5. `e.globalCacheGen` is bumped — subsequent GETGLOBAL hits now cache
6. **Every future self-call from the caller**: `LDR X3, DirectEntryPtr; CBZ X3, slowLabel` → slow path

For ack, step 6 fires only a few thousand times (negligible). For fib, step 6 fires ~29M times, each one a full Go/JIT roundtrip.

**Why ack's pre-598bc1e path was broken**: pre-fix, step 6 did not exist. After the first `handleNativeCallExit`, the next call BL'd again, exited again, `handleNativeCallExit` recursed. Ack's `ack(3,4)` call tree has nested `ack(m, ack(m, n-1))` forms — the inner arg evaluation could chain the exits into deep recursion that overflowed the 8KB goroutine stack. Fib was fast pre-fix because its base case `n<2` has no GETGLOBAL, so the chain bounded out at depth ~35.

## 6. Plan summary

Full plan: `opt/current_plan.md`.

R29 is pure analysis. Single task:

- **Task 0** (infra, Coder): clone `tier1_ack_dump_test.go` → `tier1_fib_dump_test.go` to install a fib insn-count sentinel. 6 tool calls max, 90 LOC max.

**No Task 1**. The initiative file commits R29 to root-cause; R30 implements the fix. Splitting hypothesis from experiment is a harness discipline (ref: R23's conceptual complexity cap, R27's 1-Coder rule).

**Predictions**:

- 0 wall-time change (no production code touched)
- Fixture captures current fib insn count as a future delta anchor
- Round outcome label: `diagnostic`

## 7. Risks & anti-drift

- **Risk**: a future Coder "helpfully" edits `tier1_call.go` inside Task 0. **Mitigation**: plan explicitly forbids it in the Task 0 scope block.
- **Risk**: the R30 fix candidates turn out to both reintroduce the goroutine stack overflow. **Mitigation**: fixture test in R29 lets R30 iterate on insn-count without running the full benchmark suite.
- **Risk**: the diagnostic data itself is wrong (instrumentation artifact). **Mitigation**: sub-agent added + reverted counters; the numbers match the observed wall-time regression exactly (~1.5s for ~29M slow-path calls ≈ 50ns per Go/JIT roundtrip, matches known overhead).
- **Anti-drift**: `rounds_since_arch_audit` = 1 (R28 did full audit). Full audit scheduled for R30 again.

## 8. Artifacts

- `opt/current_plan.md` — R29 plan, Task 0 only
- `opt/knowledge/r29-fib-root-cause.md` — sub-agent diagnostic (already written)
- `opt/analyze_report.md` — this file
- `docs/39-*.md` — blog draft
- `opt/initiatives/tier1-call-overhead.md` — round log updated to mark R29 in_progress on item 8
