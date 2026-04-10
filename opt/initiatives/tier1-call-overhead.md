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
| 1 | Drop NativeCallDepth on self-call fast path + dead `ctx.Constants` STR (R26) | tier1_dispatch | ack −6–10% | **In progress (R26)** |
| 2 | Drop NativeCallDepth on normal-call path (requires audit of Go unwind) | tier1_dispatch | obj_creation −5%, binary_trees −3% | Queued |
| 3 | Drop `ctx.Regs` STR on both paths via exit-lazy flush (audit ~10 exit sites) | tier1_dispatch | ack −3%, broad 2–3% | Queued |
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
| R26 | Item 1 | TBD | First round of the initiative |
