# R28 Analyze Report

**Cycle ID**: `2026-04-11-tier1-selfcall-ctx-regs-lazy-flush`
**Date**: 2026-04-11
**Phase**: ANALYZE + PLAN
**Previous round**: R27 `2026-04-11-tier1-selfcall-constants-str`, outcome `improved` (commit `2748fb2`).

## Step 0 — Architecture audit (full, rounds_since_arch_audit=2)

Ran `scripts/arch_check.sh`. File-size report:

| File | Lines | Status |
|---|---|---|
| emit_dispatch.go | 971 | ⚠ split (unchanged since R25) |
| graph_builder.go | 955 | ⚠ split (unchanged) |
| tier1_arith.go | 903 | ⚠ split — **do not touch this round** |
| tier1_table.go | 829 | ⚠ split (unchanged) |
| tiering_manager.go | 743 | ok |
| pass_inline.go | 726 | ok |
| tier1_handlers.go | 697 | ok |
| emit_table_array.go | 696 | ok |
| regalloc.go | 684 | ok |
| emit_compile.go | 640 | ok |
| pass_licm.go | 594 | ok |
| interp.go | 562 | ok |
| tier1_compile.go | 539 | ok (target this round, +3 lines safe) |
| emit_call.go | 534 | ok |
| tier1_call.go | **529** | ok (target this round, −1 line) |

`tier1_call.go` shrunk from 554 (R26 audit) to 529 after R27 — one line deleted, plus the shared-join STR move. No file grew over 1000. Four files hover in the 800–971 "pending-split" band; none are touched by R28, so split can continue to defer.

Tech-debt markers: 2 total, both benign (doc notes). Test-coverage gaps unchanged from R26 audit (19 `emit_*.go` and `ir*.go` files without direct `_test.go`; covered indirectly via end-to-end tier1/tier2 tests).

Pass pipeline order (from `tiering_manager.go`) is unchanged:
`BuildGraph → Validate → TypeSpec → Intrinsic → TypeSpec → Inline → TypeSpec → ConstProp → LoadElim → DCE → RangeAnalysis → LICM → Validate → RegAlloc → Emit`.

**Audit verdict**: no new structural constraints. `tier1_call.go` has 471 lines of headroom. `tier1_arith.go` is the nearest risk but is not in R28 scope. Counter `rounds_since_arch_audit` will reset to 0.

### Constraint update — 598bc1e is now load-bearing

Commit `598bc1e` added the `LDR X3, [X1, funcProtoOffDirectEntryPtr]; CBZ X3, slowLabel` pair at the self-call exec label (tier1_call.go:316-317). This is a **correctness constraint**, not an optimization: when `handleNativeCallExit` clears `DirectEntryPtr = 0` to force the slow exit-resume path, self-calls must not bypass it. Without this check, nested `handleNativeCallExit → executeInner` chains overflow the goroutine stack.

Added to `docs-internal/architecture/constraints.md` in this round (see "Constraints file update" below). Cost: +2 insns/self-call × 3 sites = **+10 insns per self-call** relative to the R26 `923` fixture. New fixture baseline should be `933`. VERIFY must re-baseline.

## Step 1 — Gap classification + target selection

### Classification table (post-R27, unverified against re-baselined latest.json)

| Benchmark | Last JIT median | LuaJIT | VM | JIT/LuaJIT | JIT/VM | Category | Ceiling |
|---|---|---|---|---|---|---|---|
| ackermann | 0.529 | 0.006 | 0.278 | 88× | 1.91 (slower) | recursive_call / tier1_dispatch | recursive_call BLOCKED; tier1_dispatch open |
| mutual_recursion | 0.228 | 0.004 | 0.192 | 57× | 1.19 (slower) | recursive_call / tier1_dispatch | same |
| fib | 0.128 | 0.024 | ~0.11 | 5.3× | ~1.16 | recursive_call / tier1_dispatch | same |
| nbody | 0.236 | 0.033 | — | 7.2× | — | tier2_float_loop | open (1 failure) |
| binary_trees | 2.215 | — | 1.527 | — | 1.45 (slower) | allocation_heavy | open |
| method_dispatch | 0.096 | — | 0.081 | — | 1.19 (slower) | gofunction_overhead / call_ic | open |

**Ceiling rule application**: `recursive_call` has 2 failures (R4 abandoned, R11 no_change) — BLOCKED. `tier2_float_loop` has 1 failure (R23 no_change). `tier1_dispatch` has 0 failures after R27 success. `field_access` has 1 failure (R19 no_change). No category is newly blocked this round.

**Initiative rule application**: `tier1-call-overhead` is active with R26 (data-premise-error) and R27 (improved). Backlog has 7 items queued, including Item 3 (exit-lazy `ctx.Regs` flush), Item 4 (RETURN variants), Item 5 (BL→B tail-thread), Item 6 (inline hot leaves), Item 7 (ProtoPtr IC). The initiative is **not exhausted**. Under the initiative rule, R28 continues on the initiative rather than opening a new direction.

### Target selection

Two live candidates within the initiative:

- **(A)** Item 3 — `ctx.Regs` exit-lazy flush on self-call setup path. 1 STR removed per self-call fast path, symmetric to R27's ctx.Constants move. 2 files, <30 LOC, one new test. Predicted 0.5–1.3% on ackermann.
- **(B)** Item 4 — compile two RETURN variants to drop `ctx.CallMode` STRs. Requires splitting the RETURN epilogue emission into `ret_normal`/`ret_direct` paths. Bigger surface, more risky, blocked by frame-layout coupling (tier1_control.go:224 branches on CallMode to pick direct_epilogue vs baseline_epilogue).

Choice: **(A)**. Reasons:
1. R27 landed `improved` on the same symmetric pattern; A is the lowest-surface next step.
2. Tests the same M4 store-buffer-coalescing hypothesis from R23 against stores (R23 settled it for branches). Outcome is informative either way.
3. Fits the 1-Coder, ≤15-tool-call, ≤30-LOC cap from the R27 review.
4. B requires a RETURN-path restructure that I want to de-risk by first confirming whether peephole STR removal has any signal at all. If A lands at zero, B's expected yield also drops and we pivot.

Target: **drop `STR mRegRegs, ctx.Regs` at tier1_call.go:389 (self-call setup), add matching STR to `emitBaselineOpExitCommon` at tier1_compile.go:468**.

## Step 1b — Architectural reasoning

The persistent call-overhead pattern in GScript's Tier 1 is that every CALL site inline-emits a "publication phase" — a sequence of `STR` instructions that flush JIT-register state into fields of the global `ExecContext`, so that the callee and its potential slow-exit handlers can read them. The fields flushed are `ctx.Regs`, `ctx.Constants`, `ctx.CallMode`, `ctx.BaselineClosurePtr`, `ctx.BaselineGlobalCache`, `ctx.BaselineGlobalCachedGen`. In the normal-call path there are ~8 such stores; in the self-call path (post-R27) there are ~5.

**V8's TurboFan** resolves this by emitting a specialized epilogue *per CallKind* — normal, tail, self-tail, construct — each one publishing only the fields its call kind touches. **LuaJIT** sidesteps the problem entirely by not having an `ExecContext` at all; its interpreter state is kept in machine registers and the generated code uses a GOT-style frame walk on slow exits. **JavaScriptCore's LLInt** uses the same "state lives in machine registers, materialize on demand at slow exits" approach as LuaJIT.

GScript's Tier 1 is a linear template compiler with no IR. It cannot structurally specialize epilogues per CallKind the way TurboFan does. What it *can* do is the equivalent at emission time: at every CALL site, inline-emit two branches (normal-call, self-call) and inline-emit their setups and restores separately, dropping from each branch the stores that branch doesn't need. R27 did this for ctx.Constants at the restore join. R28 does it for ctx.Regs at the self-call *setup* via a dual mechanism: delete the setup store, and make every possible reader (the baseline op-exit helper) publish ctx.Regs itself before reading it.

**The architectural insight to record**: *In a linear template JIT without per-call-kind specialization, "exit-lazy flush" is the structural equivalent of TurboFan's specialized epilogues.* Instead of paying the publication cost unconditionally at every call site, pay it lazily at the small number of slow-exit sites that actually read the published state. The trade is "pay many times on the fast path" → "pay few times on the slow path, and pay a branch predictor on the fast path to avoid it." For call-heavy benchmarks where the slow path is ~never taken, this is strictly cheaper.

Items 3, 4, 1b (when unblocked), and 7 are all instances of this single pattern applied to different fields. R28 is the first application; if it lands, the pattern generalizes and the initiative's remaining items are structural mechanizations of the same idea.

## Step 2 — External research

**Skipped by design**, per R27's discipline note and the R26 token-overrun lesson. The `opt/knowledge/tier1-call-overhead.md` knowledge base already contains:

- V8/TurboFan CallKind-specialized epilogue reference with file pointers in the V8 source tree (indexed R26)
- LuaJIT's "state in registers, frame walk on exit" approach (indexed R20)
- Full 60-insn disassembly breakdown of the ackermann self-call fast path from the R26 fixture dump
- Discussion of M4 store-buffer coalescing hypothesis from R23's branch-hiding observation

No new research question for R28. The exact dead-store argument is derivable from reading `tier1_call.go` end-to-end (Step 3) and from R27's precedent. Spawning a research sub-agent would burn ~10k tokens to return "yes, this is the standard approach, as already documented in opt/knowledge/tier1-call-overhead.md."

## Step 3 — Project source reading

Files read end-to-end or in targeted sections this round:

1. `internal/methodjit/tier1_call.go` (lines 1–529, full): normal-call setup at 253–297, self-call setup at 305–409, shared restore join at 411–463, direct/self entry prologues at 465–516, direct-exit epilogue at 518–529.
2. `internal/methodjit/tier1_control.go` (lines 1–251, targeted): RETURN emission at 202–227 — confirms `LDR X1, ctx.CallMode; CBNZ X1, direct_epilogue; B baseline_epilogue` is the current mechanism that forces the ctx.CallMode STR to remain. This is what Item 4 will eventually restructure.
3. `internal/methodjit/tier1_compile.go` (lines 390–524, targeted): entry prologue at ~395–460, `emitBaselineOpExitCommon` at 466–499, `emitBaselineOpExit` / `emitBaselineOpExitABx` at 501–524. Confirmed the helper currently touches X0 only (via `LoadImm64`) and has no conflict with adding an X26→ctx.Regs STR at the head.
4. `internal/methodjit/emit_op_exit.go` (lines 30–90, targeted): Tier 2 path, not Tier 1 — confirmed R28 does not affect it.
5. `internal/methodjit/emit_call_native.go` lines 160–210 (confirmed via grep): native-call exits at lines 163 and 206 already `STR mRegRegs, ctx.Regs` themselves, so the lazy-flush change doesn't need to touch them.

**Grep confirming the invariant** — every `execCtxOffRegs` site in the `internal/methodjit` tree:

| File | Line | Kind | Role after R28 |
|---|---|---|---|
| emit_compile.go | 449, 505 | LDR | entry prologue — unchanged |
| emit.go | 161, 210, 211 | constant | offset defs |
| emit_call_exit.go | 285 | LDR | Tier 2 deopt resume — unchanged |
| tier1_compile.go | 398, 452 | LDR | Tier 1 entry prologue — unchanged |
| tier1_call.go | 184, 218, 260, 323, 347, 413, 425, 476 | LDR / STR | line 260 (normal setup) kept; line 413 (restore join) kept; others are bounds/resize logic, unchanged |
| tier1_call.go | **389** | STR | **deleted this round** |
| emit_call_native.go | 122, 163, 206 | LDR/STR | native-call path, unchanged |

No site reads `ctx.Regs` memory between a self-call setup and the callee's eventual op-exit, **except** the callee's nested-call setup (which does its own STR). Safety invariant holds.

## Step 4 — Micro diagnostics

**Reused from R26 fixture + R27 audit.** No new diagnostic sub-agent spawned.

Post-598bc1e, the self-call fast-path static insn count is 923 + 10 = **933** (R26 fixture was 923 pre-correctness-fix; the DirectEntryPtr check adds 2 insns × 3 sites). Any fixture test in the tree asserting 923 is already broken — R27 VERIFY likely updated it to 933; this round must verify post-baseline.

Dynamic ackermann: ~67M self-calls on the hot loop (R26 profile).

After R28: static count drops to **932** at the self-call site (one STR removed). The `emitBaselineOpExitCommon` helper grows by one instruction, but that helper is emitted once per op-exit site, not once per call — static impact is spread across the whole function body, not the call site fixture.

M4 store-buffer coalescing hypothesis (R23 retro note, opt/knowledge/tier1-call-overhead.md line ~437): stores to adjacent fields of `ctx` (one object, single cache line) may be merged in the store buffer, so dynamic wall-time savings could be less than instruction-count arithmetic predicts. R27's `improved` result was ~0.5–1.3% on the "true signal" attribution, at the low end of theoretical — consistent with partial coalescing, not full coalescing. R28 is the second data point on this hypothesis.

## Step 5 — Plan

Written to `opt/current_plan.md`. 1 Coder task + 1 orchestrator pre-flight task (commit untracked test fixtures). Budget: ≤15 tool calls in Coder, ≤30 LOC, ≤3 files, TDD mandatory.

## Step 6 — This report.

## Step 7 — Blog draft

Written to `docs/38-lazy-flush.md`.

## Constraints file update (doc-sync rule)

Appending to `docs-internal/architecture/constraints.md`:

- **Tier 1 self-call DirectEntryPtr check is load-bearing** — Commit `598bc1e` (2026-04-11). At the self-call exec label in `tier1_call.go:316-317`, the `LDR X3, [X1, funcProtoOffDirectEntryPtr]; CBZ X3, slowLabel` pair prevents self-calls from bypassing the handleNativeCallExit slow-path gate when DirectEntryPtr=0. Removing this check causes nested `handleNativeCallExit → executeInner` chains that overflow the 8KB goroutine stack on deep recursion. Cost: +2 insns/site × 3 sites = +10 insns per self-call fast path vs. the R26 `923`-insn fixture. New fixture count: `933`.

## State updates (for VERIFY)

- `rounds_since_arch_audit`: 2 → 0 (full audit done).
- `rounds_since_review`: 0 → 1 (next round triggers review at threshold).
- Initiative `tier1-call-overhead` backlog: mark Item 3 as "in_progress (R28)".
- Baseline: **stale**, re-baseline in VERIFY before comparison.
- Untracked test fixtures to commit in Task 0: `main_test.go`, `offset_check_test.go`, `quicksort_asm_test.go`.

## Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Store-buffer coalescing hides the saving → no_change | Medium | Low | Outcome is still informative; initiative pivots to Item 5 |
| Test regression from missed exit site (e.g., table op mid-exec in callee) | Low | Medium | TDD guard test + full methodjit test suite |
| Baseline comparison confusion from 598bc1e's +10 insns | High (if VERIFY skips re-baseline) | Medium | Plan explicitly mandates re-baseline as VERIFY step 1 |
| Coder over-budget from "creative" restructure | Low | Medium | Hard stop 2 fix attempts, explicit no-creativity clause in plan |
| Untracked test commit races with something | Very low | Low | Fixtures are 211 lines, self-contained |
