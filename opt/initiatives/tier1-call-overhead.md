# Initiative: Tier 1 CALL/RETURN overhead reduction

**Opened**: 2026-04-11 (R26)
**Category**: `tier1_dispatch`
**Motivating data**: Baseline JIT is SLOWER than the interpreter on 6 CALL-heavy benchmarks (R26 `benchmarks/data/latest.json`). Biggest gap: ackermann JIT 0.563s vs VM 0.294s (1.91× slower).

## Goal

Make Tier 1 JIT strictly faster than the interpreter on CALL/RETURN-dominant code. Stretch goal: close half the LuaJIT gap on ackermann (94× → ~10×) over 4-6 rounds.

## Root cause (R26 diagnostic)

Per-call emission in `tier1_call.go:emitBaselineNativeCall`:
- Self-call fast path: **~60 caller-side insns** (plus 8 entry + 4 epilogue = ~72 total per call)
- Normal-call path: **~100 caller-side insns** (full 96-byte frame save/restore)
- Interpreter equivalent (`vm.go:1136`): inline `continue` in the dispatch loop — no BL, no RET, no frame save/restore. Go-compiled to ~50-80 insns all in one function.

The JIT is paying for infrastructure (NativeCallDepth counter, ExecContext shuttling for Regs/Constants/CallMode/ClosurePtr/GlobalCache/GlobalCachedGen, full stack-frame save/restore) that the interpreter avoids by staying in a single function.

## Backlog (ordered by ROI × safety)

| # | Item | Category | Expected | Status |
|---|---|---|---|---|
| 1 | Drop NativeCallDepth on self-call fast path + dead `ctx.Constants` STR (R26) | tier1_dispatch | ack −6–10% | **BLOCKED** — NativeCallDepth is goroutine-stack budget (8KB). SP-floor approach fails. See opt/premise_error.md. |
| 1a | Drop dead `ctx.Constants` STR on self-call restore (independently safe, Task 2 from R26) | tier1_dispatch | ack −1% | **DONE** (R27, commit 2748fb2) |
| 2 | Pre-grow goroutine stack at JIT entry (lockOSThread + large alloc) → raise NativeCallDepth limit | tier1_dispatch | enables deeper recursion + more NCD removal | Research |
| 3 | Drop NativeCallDepth on normal-call path (requires audit of Go unwind) | tier1_dispatch | obj_creation −5%, binary_trees −3% | Queued |
| 3a | Drop `ctx.Regs` STR at self-call setup (tier1_call.go:389), move to `emitBaselineOpExitCommon` lazy flush | tier1_dispatch | ack −0.5–1.3% | **in_progress (R28)** |
| 3b | Drop `ctx.Regs` STR at shared restore-join (tier1_call.go:413) on self-call side | tier1_dispatch | ack −0.5% | Queued (R29 candidate if R28 lands) |
| 4 | Compile two RETURN variants (direct vs baseline epilogue) → remove CallMode write | tier1_dispatch | ack −3–5% | Queued |
| 5 | Self-call BL → B tail-thread (reuse same frame for self-recursion) | tier1_dispatch | ack −30%+ | Multi-round, research |
| 6 | Inline hot leaf callees at Tier 1 (shape-stable single-proto feedback) | tier1_dispatch | method_dispatch −15% | Queued |
| 7 | Avoid `BLR X2` on cross-function calls by caching ProtoPtr→DirectEntry in an IC | tier1_dispatch | obj_creation −8% | Queued |

## Constraints

- Must not regress the `recursive_call` category at Tier 2 — that's BLOCKED (ceiling reached). All work is pure Tier 1.
- Must not regress `mutual_recursion` again — R24 int-spec added +4.9% there and was barely recovered; watch per-round.
- File size: `tier1_call.go` currently 554, cap 1000. Item 5 likely forces a split into `tier1_call_self.go` / `tier1_call_normal.go`.

## Round log

| Round | Item | Outcome | Notes |
|---|---|---|---|
| R26 | Item 1 | data-premise-error | SP-floor cannot replace NativeCallDepth — Go goroutine stack is 8KB, not 2MB. Task 0 (insn-count fixture) committed at 878e64a. Task 2 (ctx.Constants drop) queued as Item 1a. |
| R27 | Item 1a | improved | ctx.Constants STR moved to normal-call block only (2748fb2). ackermann −5.2%, fib −3.8%, mutual_recursion −4.2% vs single-shot baseline. True self-call signal ~0.5-1.3%. Item 1a closed. |
| R28 | Item 3a | no_change | ctx.Regs STR elided + lazy-flushed (144c1a4). Static +3 insns, wall-time ≈0 vs true predecessor 39b5ef3. VERIFY compared against stale a388f78 baseline; user-led bisect exposed 598bc1e as true pivot (see Item 8). Initiative peephole line of work confirmed dead ROI — store-buffer coalescing hypothesis holds. |
| R29 | Item 8 | diagnostic | Root cause confirmed: `handleNativeCallExit` fires once on first self-call (cold GETGLOBAL miss), permanently zeros `proto.DirectEntryPtr`, and the 598bc1e guard at `tier1_call.go:316-317` forces every subsequent self-call through slow Go-dispatch. Fib pays this ~29M times; ack pays it ~thousands. Evidence in `opt/knowledge/r29-fib-root-cause.md`. Fix deferred to R30 — candidate A: drop self-call guard only; candidate B: split `HasOpExits` flag from `DirectEntryPtr`. Fib insn-count fixture landed as Task 0 sentinel. |

## Pivot: new initiative spawned from R28 bisect

**Item 8 — Fib regression from 598bc1e correctness fix** (NEW, high priority)

598bc1e "self-call DirectEntryPtr check" was a +136/−159 line rewrite of `emitBaselineNativeCall` framed as a 2-insn correctness patch. Bisect result:

| benchmark | pre-598bc1e (a388f78) | post-598bc1e | delta |
|---|---|---|---|
| ackermann | 0.549s | 0.275s | **−50%** |
| fib | 0.131s | 1.425s | **+988%** |

Same self-call code path, opposite direction. The regression is **by far the largest single optimization opportunity** on the current benchmark board (fib +1.3s recovery potential without sacrificing ackermann gain).

**Constraint**: The DirectEntryPtr check is load-bearing for `TestDeepRecursionRegression` / `TestQuicksortSmall` — cannot be reverted. Must find a variant that preserves correctness while restoring fib performance.

**Why ackermann vs fib diverge**: unknown. Hypothesis: the rewrite changed the ratio of fast-path vs slow-path transitions, and fib's call pattern (1-arg, binary branch) falls through to slow path more often than ackermann's (2-arg, nested eval). Must verify with `Diagnose()` + ARM64 disasm.

**Status**: Analysis complete (R29). Target R30 for fix — candidate A (drop self-call `CBZ` only) or candidate B (split `HasOpExits` flag). R30 ANALYZE must pick between them after re-reading `tier1_call.go:311-317` and running `TestDeepRecursionRegression` + `TestQuicksortSmall` as the correctness gate.
