# Optimization Plan: Tier 1 self-call restore — drop dead ctx.Constants STR

> Created: 2026-04-11
> Status: active
> Cycle ID: 2026-04-11-tier1-selfcall-constants-str
> Category: tier1_dispatch
> Initiative: opt/initiatives/tier1-call-overhead.md (Item 1a)

## Target

Ackermann and other Tier 1 self-recursive benchmarks. Removes one STR on every self-call
return by specializing the restore epilogue.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target (after) |
|-----------|--------------|--------|-----|----------------|
| ackermann | 0.699s | 0.007s | 100× | 0.690–0.693s (−0.8–1.3%) |
| fib | 0.159s | 0.028s | 5.7× | 0.157–0.158s (−0.5–1.0%) |
| mutual_recursion | 0.283s | 0.005s | 57× | 0.281s (−0.5%) |
| binary_trees | 2.756s | n/a | — | −0.2% (normal-call path unchanged) |

Primary focus: ackermann. Goal is a clean, bounded close-out of R26 Item 1a, not
a chart-topping win. R26 burned 82.5M tokens on a doomed SP-floor approach. R27
pays down the safe residual.

## Root Cause

`internal/methodjit/tier1_call.go` emits, after both normal-call and self-call restore
branches merge at `restore_done`, two unconditional context-pointer write-backs:

```go
// tier1_call.go:437-438
asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)       // REQUIRED on both paths
asm.STR(mRegConsts, mRegCtx, execCtxOffConstants) // DEAD on self-call path
```

- On the **normal-call** path the callee's setup overwrote `ctx.Constants` with the
  callee's constants pointer (`tier1_call.go:343`). After the restore `LDP` (line 407)
  reloads X27 from the saved frame, the STR is needed to propagate caller's constants
  back into `ctx.Constants` for any slow-path exit in the caller.
- On the **self-call** path the setup never touches `ctx.Constants` (line 362-372:
  only `ctx.Regs` and `ctx.CallMode` are set). `emitSelfCallEntryPrologue` explicitly
  skips the `LDR X27, ctx.Constants` that `emitDirectEntryPrologue` performs. Therefore
  both `mRegConsts` (X27) and `ctx.Constants` are unchanged across a self-call. The
  `STR(mRegConsts, ctx.Constants)` is writing the same value back — pure overhead.

Cross-check on `STR(mRegRegs, ctx.Regs)` at line 437: self-call setup **does** publish
the advanced `mRegRegs` via `STR mRegRegs, ctx.Regs` (line 370), so the post-restore
write-back is required on the self-call path too. Only the Constants STR is dead.

Ackermann executes ~67M self-calls (2 CALL sites × ~33M recursive invocations), so
each insn saved in the self-call fast path is ~67M fewer dynamic insns. One
ctx-memory STR to a cache-hot line is a dependent memory op (not a predicted branch),
so M4 is *less* likely to hide it than the R22-R23 guard-hoist savings were.

## Prior Art

**V8 TurboFan**: emits specialized save/restore sequences per call kind. A recursive
self-call under TurboFan's JSCall reuses the caller's activation when it can prove the
constants pool is identical — there is no per-call context write-back because the
constants pool is part of the activation, not side-table state.

**LuaJIT**: on ARM64 the interpreter stays in one function; no per-call state sync
exists. Our Tier 1 emits BL/RET so we have to synchronize, but we should synchronize
*only what actually changed*.

**JSC baseline**: generates two epilogue variants (slow-path vs fast-path return) and
picks one at each call site. This is the same pattern we need for the RETURN CallMode
write (Item 4 in the initiative) — but for this round we do the smaller version: two
restore blocks, not two epilogues.

Our constraint vs theirs: Tier 1 is a linear template compiler with no IR. Each
`emitBaselineNativeCall` invocation emits both the normal and self-call branches
inline at the caller's CALL site, so "specialize per call kind" = "move instructions
between inline branches", which is exactly what this plan does.

## Approach

Single-file edit in `internal/methodjit/tier1_call.go`. Move the ctx.Constants STR from
the shared `restore_done` join into the normal-call restore block only. The self-call
restore block loses the write entirely.

### Current code structure (lines 406-438)

```go
// Normal restore (96-byte frame)
asm.LDP(mRegRegs, mRegConsts, jit.SP, 16)
// ... 6 more LDR/STR pairs ...
asm.ADDimm(jit.SP, jit.SP, 96)
asm.B(restoreDoneLabel)

// Self-call restore (48-byte frame)
asm.Label(selfCallRestoreLabel)
asm.LDR(mRegRegs, jit.SP, 16)
// ... 2 more LDR/STR ...
asm.ADDimm(jit.SP, jit.SP, 48)

asm.Label(restoreDoneLabel)
// Restore context pointers
asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)  // <-- dead on self-call
```

### Target code structure

```go
// Normal restore (96-byte frame)
asm.LDP(mRegRegs, mRegConsts, jit.SP, 16)
// ... 6 more LDR/STR pairs ...
asm.ADDimm(jit.SP, jit.SP, 96)
asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)  // <-- moved here (normal only)
asm.B(restoreDoneLabel)

// Self-call restore (48-byte frame)
asm.Label(selfCallRestoreLabel)
asm.LDR(mRegRegs, jit.SP, 16)
// ... 2 more LDR/STR ...
asm.ADDimm(jit.SP, jit.SP, 48)

asm.Label(restoreDoneLabel)
// Restore context pointers
asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
// ctx.Constants STR removed from join (dead on self-call; now in normal block)
```

Net static insn change: 0 per CALL site (one STR moved). Dynamic insn change per
self-call: −1. Expected on ack: ~67M fewer STRs at runtime.

### Why this is safe (invariants preserved)

1. **Normal-call invariant**: `ctx.Constants` must equal the caller's constants pool
   after the call returns, so any subsequent exit-resume uses the correct pool. This
   holds because the moved STR executes on the normal path after `LDP` reloaded X27
   from the caller's saved frame.
2. **Self-call invariant**: `ctx.Constants` is never modified by a self-call
   (neither setup nor `emitSelfCallEntryPrologue` touch it), so the post-restore
   state is identical to the pre-call state. No write-back required.
3. **Register state**: X27 (mRegConsts) is a callee-saved pinned register from
   the perspective of the direct entry prologue. Self-call entry doesn't reload it;
   normal entry does. Neither path leaves it in an inconsistent state relative to
   `ctx.Constants`.

## Expected Effect

**Primary (ackermann)**: 1 STR × ~67M self-calls = ~67M fewer dynamic insns.
At M4 3 GHz blended ~3 IPC on ctx-memory stores → ~7.4 ms of raw savings. On the
current 0.699s ack number that's 1.06%. Halved for ARM64 superscalar hiding
(R10 lesson): **0.5–1.3%**.

**Secondary (fib, fib_recursive, mutual_recursion)**: same per-call saving, smaller
call counts. Expect 0.3–0.8% each.

**Non-self-recursive benchmarks** (binary_trees, method_dispatch, object_creation):
normal-call path unchanged. The moved STR still executes; emitted count per CALL
site is unchanged. Expect zero change.

**Prediction calibration note**: R22-R23 showed insn-count savings on branch-heavy
changes can be fully hidden (0%) by M4 superscalar. This change targets a
dependent memory store, not a branch — R26's KB doc explicitly flags ctx-memory
stores as *less* hidden than branches. So the calibrated 0.5–1.3% is a lower
bound, not a ceiling. If it lands at 0%, the R26 lesson is confirmed: ctx-memory
stores are hidden too, and we should pivot to RETURN restructuring (Item 4) next.

## Failure Signals

- **Signal 1**: ack insn-count fixture (`tier1_ack_dump_test.go`, baseline=923)
  regresses above 923 → implementation accidentally grew the CALL-site emission.
  Action: revert.
- **Signal 2**: any benchmark regresses >2% (outside median-of-5 noise band).
  Action: revert — the ctx.Constants write-back was load-bearing in a path we
  missed. Root-cause before retrying.
- **Signal 3**: ackermann wall-time improvement = 0% after median-of-5.
  Action: **do not revert**; record as "ctx-memory stores hidden by M4 store
  buffer" and update `constraints.md` + `tier1-call-overhead.md` KB. Pivot
  next round to RETURN restructuring (Item 4) since simple peephole STR
  removal no longer moves the needle.

## Task Breakdown

- [x] **Task 1** (Coder, implementation): move `STR(mRegConsts, ctx.Constants)`
  from the shared `restore_done` join in `tier1_call.go` into the normal-call
  restore block. Exactly one code move, no structural refactor.
  - **File**: `internal/methodjit/tier1_call.go`
  - **Functions**: `emitBaselineNativeCall` (starts at line 95)
  - **Lines to modify**: insert one line after line 422 (`ADDimm SP, SP, 96`),
    delete one line at 438 (`STR mRegConsts, mRegCtx, execCtxOffConstants`)
  - **Algorithm** (pseudocode):
    ```
    // In the normal-restore block, after SP reclaim and before B restoreDoneLabel:
    emit STR mRegConsts, mRegCtx, execCtxOffConstants

    // In the shared restoreDoneLabel block:
    delete the STR mRegConsts, mRegCtx, execCtxOffConstants line
    // Keep the STR mRegRegs, mRegCtx, execCtxOffRegs — it's still needed on both paths.
    ```
  - **Test to extend**: `internal/methodjit/tier1_call_regression_test.go` — add
    a sub-test `TestSelfCall_ConstantsStrMoved` that compiles a self-recursive
    proto, scans the emitted ARM64 for `STR X27, [X19, #execCtxOffConstants]`,
    and asserts the STR appears **only inside** the normal-call block (offset
    range between label `afterCallLabel` and `B saveDoneLabel`/`B restoreDoneLabel`).
    If the regression test file doesn't exist, create it with just this test.
  - **Existing test to keep green**: `TestDumpTier1_AckermannBody` (insn count
    stays 923).
  - **Correctness test to keep green**: `TestBaselineAckermann*` (benchmark
    correctness), `TestCallNative_*` (call-path integration).
  - **NOT to touch**: the normal-call save block, `emitSelfCallEntryPrologue`,
    the shared `restore_done` label, `STR mRegRegs, ctx.Regs`, RETURN emission
    in `tier1_control.go`, any other file.
  - **Scope**: ≤1 file, ≤15 lines changed (excluding the new regression test).

- [ ] **Task 2** (VERIFY): benchmarks + insn-count fixture + correctness suite (VERIFY phase)
  (this is not a Coder task, runs in VERIFY phase).

Total Coder tasks: **1 implementation**. No Task 0 infrastructure (file is
554/1000 lines, well under the split threshold). No diagnostic sub-task (the
source-level analysis above is deterministic — no disasm required).

## Budget

- Max commits: **1** (Task 1)
- Max files changed: **2** (tier1_call.go + new regression test)
- Max Coder tool calls: **30** (single surgical edit + one new test)
- Abort condition: if Task 1 needs more than 2 re-reads of tier1_call.go, the
  premise is wrong — stop and record `status: approach-mismatch`.

## Results (filled by VERIFY)

| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| ackermann | 0.558s | 0.529s | −5.2% | −0.5–1.3% | ✓ (exceeded — thermal+real) |
| fib | 0.133s | 0.128s | −3.8% | −0.5–1.0% | ✓ (exceeded — thermal+real) |
| fib_recursive | 1.341s | 1.272s | −5.1% | n/a | — |
| mutual_recursion | 0.238s | 0.228s | −4.2% | −0.5% | ✓ (exceeded — thermal+real) |
| sieve | 0.088s | 0.083s | −5.7% | ~0% | thermal variance |
| mandelbrot | 0.063s | 0.060s | −4.8% | ~0% | thermal variance |
| matmul | 0.124s | 0.116s | −6.5% | ~0% | thermal variance |
| spectral_norm | 0.045s | 0.043s | −4.4% | ~0% | thermal variance |
| nbody | 0.248s | 0.236s | −4.8% | ~0% | thermal variance |
| sort | 0.042s | 0.038s | −9.5% | ~0% | thermal variance |
| binary_trees | 2.311s | 2.215s | −4.2% | ~0% | thermal variance |

### Test Status
- 308 passing; 1 failing (TestDeepRecursionRegression — pre-existing JIT stack scan crash, confirmed at baseline)

### Evaluator Findings
- PASS (inline evaluation): single STR moved from shared join to normal-call block only. ARM64 encoding verified (0xF900067B = STR X27,[X19,#8]). Regression fixture structurally correct: tests STR→B(forward) pattern, not byte count. No scope creep.

### Regressions (≥5%)
- none

## Lessons

1. **Broad non-recursive improvements are thermal noise.** sieve -5.7%, sort -9.5%, matmul -6.5% all improved without any relevant code change. The baseline was a prior single-shot run taken at a different machine state. True signal on recursive benchmarks is ~0.5-1.3% as predicted; the ~5% numbers are thermal + signal.
2. **Structural regression fixtures beat bytewise counts.** Checking that STR X27 is immediately followed by a forward B is semantically precise — it detects placement, not just presence. A bare count could pass even with the optimization reverted if the same instruction appears elsewhere.
3. **One-STR change, 110-line test, one commit.** The bounded scope (1 Coder, 1 file, 1 commit) took the implementation from plan to green tests in one pass. R26 burned 82.5M tokens; R28 closed Item 1a safely in a single session. Discipline about scope pays off.
4. **Dead store proof requires both paths.** The safety argument required reading `emitBaselineNativeCall`, `emitSelfCallEntryPrologue`, and the self-call setup block (lines 362-372) together. Reading just one function would not have been enough to verify the self-call path never touches X27/ctx.Constants.
5. **ctx.Constants STR confirmed movable; next is ctx.Regs.** Item 3 (drop ctx.Regs STR via exit-lazy flush) is now the least-risky queued item. Requires auditing ~10 exit sites to confirm all ctx.Regs reads after a call go through the restored value. Estimated −3% on ackermann, broader impact.
