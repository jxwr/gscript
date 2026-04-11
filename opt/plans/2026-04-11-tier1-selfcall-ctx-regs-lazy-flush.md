# R28 Plan — Tier 1 self-call ctx.Regs exit-lazy flush

**Cycle ID**: `2026-04-11-tier1-selfcall-ctx-regs-lazy-flush`
**Category**: `tier1_dispatch`
**Initiative**: `tier1-call-overhead` (Item 3, narrow variant — self-call only)
**Date**: 2026-04-11
**Budget**: 1 Coder sub-agent, ≤15 tool calls, ≤30 LOC, ≤3 files touched.

## Target

Drop the `STR mRegRegs, ctx.Regs` at `internal/methodjit/tier1_call.go:389` on the **self-call setup path only**, and compensate by adding one matching STR inside the shared Tier 1 baseline op-exit helper `emitBaselineOpExitCommon` at `internal/methodjit/tier1_compile.go:468`.

Net: −1 STR per self-call fast path (ackermann runs ~67M self-calls), +1 STR per baseline op-exit (slow path, rare).

## Why this is safe (dead-store proof, read from source)

The invariant the removal depends on: **between the moment a self-call's setup stores ctx.Regs at line 389 and the moment the callee takes a slow exit, no code reads ctx.Regs memory**. If that holds, the line-389 store only matters as a "publish window before the callee might exit," and that publication can be deferred to the exit site itself.

**Self-call entry prologue** (`tier1_call.go:501-516`, `emitSelfCallEntryPrologue`): explicitly skips `LDR X26, ctx.Regs` — comment says "already set by caller's step 6." The mRegRegs *register* is inherited from the caller (advanced at line 384 *before* the store at line 389). Only the memory cell would be stale without line 389. Register is correct.

**Callee body — normal ops**: All Tier 1 ops manipulate the register file via `mRegRegs` (X26) register loads/stores. None of them read `ctx.Regs` memory. Verified by `grep -n execCtxOffRegs internal/methodjit/tier1_*.go` — the only reads are in entry prologues and restore blocks, none inside per-op templates.

**Callee body — nested native calls**: If the callee makes another native call (self or normal), the *nested* setup writes its own `STR mRegRegs, ctx.Regs` at lines 260 or (after this change) the lazy-flush site. The nested path is self-contained.

**Callee body — baseline op-exit**: Currently `emitBaselineOpExitCommon` (`tier1_compile.go:468`) sets exit descriptors and branches to `baseline_exit`/`direct_exit` without touching ctx.Regs. It **relies on ctx.Regs having been published at call setup**. After this change, it publishes ctx.Regs itself. The Go handler on the other side reads ctx.Regs to locate the register window; it will see the same value it does today.

**Callee body — native-call exit**: Already publishes ctx.Regs itself (`emit_call_native.go:163, 206`). Unaffected.

**Callee body — deopt to Tier 2**: Tier 1 baseline functions do not deopt to Tier 2 in the middle of execution; the Tier 2 promotion path is gated by CallCount and happens at the next call-site entry, not mid-body. No concern.

**Slow-path exits inside tier1_call.go's own CALL emission** (the `slowLabel` branch taken by `DirectEntryPtr=0`, bounds check, or Tier 2 threshold hit): all three branches take `slowLabel` *before* line 389 would have executed (lines 317, 325, 332). At `slowLabel`, ctx.Regs still holds the caller's window, which is correct — this is the pre-call state. `slowLabel` itself calls `emitBaselineOpExitCommon` (line 460), so with the new lazy flush in place, it will also re-publish, which is redundant but not wrong.

**Call restore join at line 413**: unchanged — `STR mRegRegs, ctx.Regs` at the shared `restoreDoneLabel` remains, because after return we have to re-publish the caller's window (the callee may have written its own window into ctx.Regs via a nested call, and we need to reset). Line 413 is NOT the target of this round.

**Symmetry with R27**: R27 moved `ctx.Constants` STR out of the shared join into the normal-call branch only (safe because self-call callee's constants are identical to caller's). R28 does the symmetric move for `ctx.Regs` on the setup side: the self-call setup STR is safe to drop because the callee-side prologue doesn't read it and all exit sites that would read it will now publish it themselves.

## Expected wall-time impact

Per-self-call arithmetic: −1 STR out of ~60 caller-side insns ≈ 1.7% per self-call. Ackermann runs ~67M self-calls through this site, so the theoretical savings are ~14 ms on a ~0.53 s run (2.6%). Halved for ARM64 superscalar store-buffer coalescing: **0.5% to 1.3% on ackermann**. Fib and mutual_recursion get similar but smaller signals (fewer self-calls).

**If this lands at zero**: confirms the M4 store buffer coalesces these STRs to the same cache line (ctx is one object, adjacent fields). Same hypothesis R23 settled for branches. Would retire the entire Items 1–4 class of peephole STR removal and pivot the initiative to Item 5 (BL→B tail-thread) as the only remaining lever.

**If this lands at 0.5–1.3%**: combined with R27 (another 0.5–1.3%), the initiative is showing linear returns from dead-store removal. Next round can attempt the symmetric change on line 413 (restore-join ctx.Regs) via the same lazy-flush mechanism, or move to Item 4 (CallMode RETURN variants).

## Baseline note (IMPORTANT)

`benchmarks/data/baseline.json` was captured at commit `a388f78` (pre-R27). Since then two non-optimization commits have landed:

- `2748fb2` — R27's ctx.Constants move (−1 STR per self-call, presumed small win).
- `598bc1e` — DirectEntryPtr check (**+2 insns per self-call site × 3 sites = +10 insns total**), a correctness fix for deep-recursion goroutine stack overflow. This alone should slightly regress ackermann vs the stored baseline.
- `39b5ef3` — Shape-system refactor, GC root scan, empty-loop test fix. Should be neutral on tight recursive benchmarks but unverified.

**VERIFY phase MUST re-baseline against HEAD before running the comparison.** Otherwise the +10-insn correctness regression from 598bc1e will be attributed to R28. Plan Task 0: re-baseline.

## IMPLEMENT Result: ALL TASKS DONE (2026-04-11)

- Task 0: DONE (commit 4b321fb) — 3 test infrastructure files committed
- Task 1: DONE (commit 144c1a4) — ctx.Regs lazy flush, all tests pass
- Blog: DONE (commit 5b5336c) — docs/38-lazy-flush.md updated
- Static insn count: +3 (−3 self-call sites, +6 op-exit sites — 6 sites not 3 as predicted)
- ackTotalInsnBaseline updated: 933 → 936
- Targeted tests all pass: RegsLazyFlush, ConstantsStrMoved, AckermannBody, Fib, DeepRecursion

## Tasks (1 Coder sub-agent, ≤15 tool calls)

### Task 0 — Commit untracked infrastructure tests [DONE — commit 4b321fb] [pre-flight, orchestrator, NOT a Coder task]

Three untracked test files are load-bearing and must be committed before Coder runs, or VERIFY will crash:

- `internal/methodjit/main_test.go` (81 lines) — `TestMain` that re-execs with `GODEBUG=asyncpreemptoff=1 GOGC=off` to work around JIT-frame unwinder crashes ("traceback did not unwind completely"). Without it, `TestDeepRecursionRegression` and any deep-recursion test SIGSEGVs.
- `internal/methodjit/offset_check_test.go` (48 lines) — post-Shape-refactor struct-offset sanity diagnostic.
- `internal/methodjit/quicksort_asm_test.go` (82 lines) — bytecode/disasm dump fixture for quicksort regression probe.

Orchestrator action: `git add` and commit these as one infrastructure commit **before** spawning the Coder. Commit message: `infra: commit load-bearing test fixtures (main_test GODEBUG workaround, offset check, quicksort probe)`. Flagged by R27 review, deferred from R27 IMPLEMENT.

### Task 1 — ctx.Regs lazy flush [DONE — commit 144c1a4] [1 Coder sub-agent]

**Files touched** (exactly 2):
1. `internal/methodjit/tier1_call.go` — delete the line currently at 389: `asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)`. The surrounding `// 6-S. Self-call setup` comment should be updated to note "no ctx.Regs flush — lazy-flushed at op-exit."
2. `internal/methodjit/tier1_compile.go` — inside `emitBaselineOpExitCommon` (starts line 468), insert `asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)` as the **first instruction** in the body, before the ExitCode store at line 474. Comment: "Lazy flush of ctx.Regs — caller-side STR was elided on self-call fast path."

**Tests** (TDD, mandatory):

- **New test**: `internal/methodjit/tier1_selfcall_lazyflush_test.go`. Structural check: emit a simple self-recursive Tier 1 function, scan the ARM64 byte stream, assert that:
  (a) between the `BL self_call_entry` and the nearest preceding `ADD X26, X26, #...` (the window advance), there is **no** `STR X26, [X19, #execCtxOffRegs]`, AND
  (b) inside the op-exit helper emission (find via the `LoadImm64` of `ExitBaselineOpExit=7`), the first store is `STR X26, [X19, #execCtxOffRegs]`. Use the existing `asm.Instructions()` or byte-scanning pattern from the R27 regression test (`tier1_constants_restore_test.go`).

- **Existing test to update**: any insn-count fixture that asserts a specific number for self-call-body emission. If `TestDumpTier1_AckermannBody` exists and asserts a specific insn count, decrement by 1 and add a comment noting the R28 change.

- **Smoke tests**: `go test ./internal/methodjit/...` must pass. In particular `TestDeepRecursionRegression`, `TestTier1Ackermann`, `TestTier1Fib`, and any Tier 1 correctness tests for recursive callees with mid-body op-exits.

**Verification hook**: If the Coder cannot find a clean place to add the STR in `emitBaselineOpExitCommon` (e.g., because scratch X0 is live there and needs saving), STOP and report — do not get creative. The STR uses X26 and X19 only, both callee-saved, so no scratch is needed; this hook should not fire.

**Hard stop**: If `go test ./internal/methodjit/...` fails after the change, the Coder's budget is 2 fix attempts. Third failure → failure report, abandon the round as `failed`, keep Task 0 commit (it's independent).

## Out of scope (explicit)

- Line 413 (restore-join ctx.Regs STR) — keep, touch in R29 if R28 lands positive.
- Line 260 (normal-call setup ctx.Regs STR) — mandatory, the normal-call callee prologue reads it. Cannot drop without changing entry prologue shape.
- NativeCallDepth — still ceiling-blocked by goroutine stack constraint.
- CallMode writes — Item 4, requires RETURN variant split.
- tier1_arith.go 903-line split — no arith work this round; split only when that file is next touched.

## Ceilings & checks

- `recursive_call` category: 2 failures, BLOCKED. This round is `tier1_dispatch` (0 failures).
- `tier1_dispatch` category: 0 failures after R27 success. Safe to spend.
- Initiative active: `tier1-call-overhead` Item 3 (narrow variant). R26 was premise-error on Item 1, R27 landed Item 1a, R28 tackles Item 3's self-call half.
- Budget rule: 1 Coder, ≤15 tool calls (same cap as R27 post-review). Orchestrator enforces.

## Success criteria

- Task 0 commit lands, all tests pass after commit.
- Task 1 commit lands, `go test ./internal/methodjit/...` passes.
- Re-baselined benchmarks show no regression ≥1% on any benchmark.
- Primary metric: ackermann median wall time moves by ≥0 (non-regression) with target −0.5% or better.

## If R28 lands at zero

Record `no_change` with classification rationale: "store-buffer coalescing hypothesis confirmed; further peephole STR removal on the call path is dead ROI." Initiative backlog Items 3/4 (ctx.Regs restore-join, ctx.CallMode) get re-classified as "pivot or defer" and the next round moves to Item 5 (tail-thread) research or the Item 2 goroutine-stack exploration.
