# R30 Analyze Report

**Round**: 30
**Date**: 2026-04-11
**Cycle ID**: 2026-04-11-transient-op-exit-classification
**Analyst model**: Opus 4.6
**Predecessor**: R29 (`no_change`, diagnostic round — identified root cause of fib +988% regression from commit 598bc1e)

## 1. Architecture audit (quick read; full audit next round)

`rounds_since_arch_audit = 1` → quick read from `scripts/arch_check.sh` output.

**Top file-size offenders** (unchanged from R29):

| File | Lines | Cap | Status |
|---|---|---|---|
| emit_dispatch.go | 971 | 1000 | watch |
| graph_builder.go | 955 | 1000 | watch |
| tier1_arith.go | 903 | 1000 | watch |
| tier1_table.go | 829 | 1000 | ok |
| tier1_call.go | 529 | 1000 | ok |
| tier1_handlers.go | 698 | 1000 | ok (R30 target file) |
| tier1_manager.go | 440 | 1000 | ok (R30 target file) |

No splits required for R30. The R30 plan adds ≤25 LOC across two files; neither crosses the 800-line split-point threshold. Full audit deferred to R31 (arch_audit cadence = every 2 rounds; counter was reset at R29 by design).

## 2. Gap classification + target selection

### Current gap vs LuaJIT (recursive-heavy subset)

| Benchmark | JIT (R29 latest.json) | LuaJIT | Gap | Delta vs pre-598bc1e |
|---|---|---|---|---|
| fib | 1.434s | 0.025s | **57×** | **+10.9× regression** |
| ackermann | 0.270s | (tracked sep.) | — | −50% improvement (retained) |
| fib_recursive | 14.285s | — | huge | ~100× regressed |
| mutual_recursion | (check) | — | — | — |

The fib regression is the single largest optimization opportunity on the board — literally a ~1.3s wall-time recovery for a surgical 2-file change. No other candidate target comes within an order of magnitude.

### Ceiling rule check

- `category_failures.tier1_dispatch = 2` (R26 data-premise, R28 peephole no-change). Would normally block.
- R29 was a diagnostic round (no_change, not a failure) that uncovered a fresh architectural opportunity.
- Per the constraints-are-cost-not-block rule and ceiling-as-temp-deprioritize rule: a newly-diagnosed, root-caused, surgical fix for a +988% regression is **not** a grind — it is a distinct opportunity. Proceed.

### Initiative exhaustion check

Read `opt/initiatives/tier1-call-overhead.md`:
- Items 1, 2, 3, 3a, 3b, 4, 5, 6, 7 — all queued or blocked, none ready for immediate low-risk execution.
- **Item 8 (fib regression from 598bc1e)** is the only in-progress, research-complete, plan-ready item.

Target: **Item 8**, R30 — pick between R29's two proposed candidates.

## 3. Architectural reasoning (candidate selection)

R29's knowledge file offered two candidates for R30:

| Candidate | Description | Verdict |
|---|---|---|
| A | Drop the self-call CBZ guard (`tier1_call.go:316-317`) | **REJECTED** — re-breaks `TestDeepRecursionRegression/quicksort_5000`, the exact test 598bc1e was written to fix. The guard blocks nested `handleNativeCallExit → Execute → BLR` chains on deep recursion at `NativeCallDepth=48`. |
| B | Add `HasOpExits bool` to `FuncProto`; CBZ guard reads new field | **REFINED** — adding a field decouples the signal from the address, but *by itself* does not fix fib because the handler still sets the signal on every cold GETGLOBAL miss. The actual fix lives in the handler, not the field. |

### The missed third option

Re-reading `handleNativeCallExit` (`tier1_handlers.go:600-685`) and the dispatch site (`tier1_manager.go:334-382`) surfaced a point R29 glossed over:

**Two** actions make the first op-exit permanent — not one:

1. Line 637: `calleeProto.DirectEntryPtr = 0`  (the CBZ-visible signal)
2. Line 354: `e.globalCacheGen++`  (invalidates all freshly-warmed IC slots)

For a transient cold-cache miss like `OP_GETGLOBAL`, both actions are overreach. The cache miss is a one-shot cold-start event; re-executing the callee warms it; if we then let subsequent BLRs hit the warm cache, the exit path is never re-entered.

For a persistent exit like `OP_CALL` (depth-limit at `NativeCallDepth=48`), both actions are load-bearing: every recursive call would re-exit, and the nested `handleNativeCallExit → Execute` chain would blow the goroutine stack without the guard.

**Candidate C (selected)**: gate both actions by a tiny predicate `isTransientOpExit(op vm.Opcode) bool` that currently whitelists only `OP_GETGLOBAL`. No new `FuncProto` field. No `tier1_call.go` changes. The CBZ guard continues to function correctly — it reads `DirectEntryPtr`, which we now preserve for transient exits so the fast path stays fast.

This is simultaneously:
- More minimal than Candidate B (no schema change)
- Correct where Candidate A is broken (persistent CALL exits still force slow path)
- One predicate, two guards, ~25 LOC total

## 4. External research + knowledge base

Consulted `opt/knowledge/r29-fib-root-cause.md` and the R27/R28 retrospectives. Relevant prior art:

- **V8's `BaselineCode::FlushBytecode` pattern**: baselines invalidate per-cache-entry, not per-proto-wide. We're doing the same spirit at a coarser granularity.
- **LuaJIT's trace-exit machinery**: side exits invalidate only the specific trace; other traces for the same function remain live. This is the same "don't over-invalidate" principle.
- No external reference engine implements "op-exit classification" exactly because most JITs do not have a BLR-in-JIT recursion path with a nested Execute fallback. Our architecture is unusual; the fix is ours to design.

Knowledge entries that informed the decision:
- `r29-fib-root-cause.md` — diagnostic data confirming exactly-one op-exit fire and the zeroing mechanism.
- `constraints.md` — NativeCallDepth=48 budget (goroutine stack 8KB, JIT cannot call morestack).

## 5. Source reading (what actually changes)

Files read in full during this phase:

- `internal/methodjit/tier1_call.go` (529 lines) — CBZ guard at 316-317 and normal-path guard at 171. Confirmed the self-call guard reads `DirectEntryPtr` as a BOOLEAN signal (the actual branch target is the static `self_call_entry` label, not this pointer).
- `internal/methodjit/tier1_handlers.go:600-698` — `handleNativeCallExit` implementation. Line 637 is the zeroing point. Comment at line 611 describing "this only happens ONCE per callee" is the smoking gun: it describes the CURRENT overly-conservative behavior that we're loosening for transient exits.
- `internal/methodjit/tier1_manager.go:300-400` — Execute loop. Line 354 is the gen-bump point (the **second** overreach missed in R29's analysis).
- `internal/methodjit/tier1_compile.go:466-528` — `emitBaselineOpExitCommon`, confirming that `BaselineOp` (not `OpExitOp`) is the field that carries the exiting opcode through `ExitCode=7 → ExitCode=8` restoration.
- `internal/methodjit/emit.go:55-85` — `ExecContext` field layout. `BaselineOp int64` at offset-by-name.
- `internal/methodjit/test_deep_recursion_test.go` — the two correctness gates: `TestDeepRecursionRegression/{linear_recursion_500,quicksort_5000}` and `TestDeepRecursionSimple`, `TestQuicksortSmall`.

Call-site inventory of `emitBaselineOpExitCommon`:

| Op | Category | Source site |
|---|---|---|
| OP_GETGLOBAL | **transient** (cache-backed) | tier1_table.go:74 |
| OP_GETFIELD | cache-backed but write-adjacent | tier1_table.go:147 |
| OP_SETFIELD | write | tier1_table.go:215 |
| OP_GETTABLE | cache-backed | tier1_table.go:346 |
| OP_SETTABLE | write | tier1_table.go:494 |
| OP_LEN / SELF / GETUPVAL / SETUPVAL | misc | tier1_table.go |
| OP_LT / OP_LE | branch compare | tier1_arith.go |
| OP_NEWTABLE / OP_SETLIST / OP_APPEND / OP_CONCAT / OP_POW / OP_CLOSURE / OP_CLOSE / OP_VARARG / OP_TFORCALL / OP_GO / OP_MAKECHAN / OP_SEND / OP_RECV / OP_SETGLOBAL | **persistent** | tier1_compile.go |
| OP_CALL | **persistent** (depth-limit slow path) | tier1_call.go:460 |

**R30 whitelist = `{OP_GETGLOBAL}` only.** Widening is a future decision.

## 6. Micro diagnostic cross-check

No new diagnostic run needed — R29 already collected the instrumented-counter data:

- `handleNativeCallExit` fires exactly **1** time for both fib(35) and ack(3,4)
- Triggered exit op = `OP_GETGLOBAL`
- `DirectEntryPtr` transition: non-zero → 0 (confirmed mechanism)
- No `EvictCompiled` fires; no int-spec deopt

This directly validates the transient-classification hypothesis for the target benchmarks: both exits are GETGLOBAL → both will hit the transient whitelist → both recover fast path.

The R30 fixture (fib Tier 1 body = 635 insns, landed at commit `3a512b7`) will detect any accidental emitter change in VERIFY.

## 7. Plan summary

See `opt/current_plan.md` for the full plan. One-paragraph summary:

One Coder task. Add `isTransientOpExit(op) bool` helper returning `true` for `OP_GETGLOBAL`. Gate `tier1_handlers.go:637` (`DirectEntryPtr = 0`) and `tier1_manager.go:354` (`globalCacheGen++` and `ctx.BaselineGlobalCachedGen` update) on `!isTransientOpExit(vm.Opcode(ctx.BaselineOp))`. No schema changes, no emitter changes, CBZ guard untouched. Correctness gate: `TestDeepRecursionRegression`, `TestDeepRecursionSimple`, `TestQuicksortSmall`, and the fib-insn-count fixture (635). Performance gate: fib ≤0.20s, ack ≤0.29s, no regression >5% elsewhere.

## 8. Uncertainty and abort conditions

- **What if nested Execute doesn't warm the IC for the top-level caller?** The IC slots are per-proto (`BaselineFunc.GlobalValCache`), shared across all callers of that proto. Warming inside nested Execute warms them for everyone. Verified by reading `tier1_manager.go:302-304` (`syncFieldCache`) and the IC-slot storage in `BaselineFunc`.
- **What if `globalCacheGen` matters for correctness beyond our reasoning?** The current comment claims it's for SETGLOBAL safety ("callee may have SETGLOBAL'd during re-execution"). If SETGLOBAL's own op-exit handler bumps the gen (it does — see `handleSetGlobal`), the bump at line 354 is redundant for write-only concerns. For read-only transient exits (GETGLOBAL), skipping it is strictly safer.
- **Abort if**: any correctness gate red, fib stays >0.5s, any non-fib benchmark regresses >5%. Revert and reopen.

## 9. Counter updates

| Counter | Before R30 | After R30 |
|---|---|---|
| rounds_since_review | 0 | 0 (REVIEW runs every round) |
| rounds_since_arch_audit | 1 | 2 → full audit in R31 ANALYZE |
| category_failures.tier1_dispatch | 2 | (VERIFY updates based on outcome) |
| sanity_verdict | clean | (SANITY writes) |

## 10. Initiative update

`opt/initiatives/tier1-call-overhead.md` Item 8 status: `in_progress (R30)`. Will be `closed` on VERIFY green, or `retry` on abort.
