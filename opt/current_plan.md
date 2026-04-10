# R26 Plan — Tier 1 self-call fast path overhead reduction

- **Cycle ID**: 2026-04-11-tier1-selfcall-overhead
- **Category**: `tier1_dispatch` (0 failures — safe)
- **Initiative**: `opt/initiatives/tier1-call-overhead.md` (new, this round starts it)
- **Baseline commit**: `cec1e151` (post-R25)
- **Budget**: 3 hours agent time / ≤120 LOC touched / 3 tasks
- **Predicted deliverable**: ackermann −6 to −10% wall-time, object_creation unchanged this round (normal-path fixes deferred), mutual_recursion at worst neutral.

---

## 1. Root cause (data-backed)

Tier 1 JIT is **slower than the interpreter** on 6 benchmarks:

| Benchmark | VM | JIT | JIT/VM |
|---|---|---|---|
| ackermann | 0.294s | 0.563s | **1.91×** |
| binary_trees | 1.633s | 2.323s | 1.42× |
| coroutine_bench | 15.247s | 18.438s | 1.21× |
| object_creation | 0.639s | 0.765s | 1.20× |
| method_dispatch | 0.088s | 0.104s | 1.18× |
| mutual_recursion | 0.205s | 0.237s | 1.16× |

All CALL/RETURN-dominant. Ackermann makes ~5.15M recursive self-calls.

R26 diagnostic (sub-agent, disasm of `/tmp/gscript_ack_tier1.bin`) measured the **hot self-call fast path** in `internal/methodjit/tier1_call.go` and counted **~60 caller-side insns per call** (not counting 8-insn callee entry + 4-insn epilogue). Breakdown of removable work:

| # | Bytes | Insns | What | Removable? |
|---|---|---|---|---|
| A | 608–616 | 3 | NativeCallDepth pre-check (LDR/CMPimm/b.ge 48) | **Yes** — replace with SP-floor check at prologue |
| B | 1000–1008 | 3 | NativeCallDepth++ (LDR/ADD/STR) | **Yes** on self-call path |
| C | 1032–1040 | 3 | NativeCallDepth-- (LDR/SUB/STR) | **Yes** on self-call path |
| D | 438 (`tier1_call.go`) | 1 | `STR mRegConsts → ctx.Constants` in shared restore after self-call | **Yes** — X27 is preserved across self-calls |
| E | 370–372 | 2 | `STR mRegRegs → ctx.Regs` + `STR 1 → ctx.CallMode` in self-call setup | **Partial** — Regs is dead unless a slow-exit fires in the callee (deferred); CallMode is REQUIRED (RETURN at `tier1_control.go:224` reads it to pick direct_epilogue vs baseline_epilogue) |

**Savings this round** (A+B+C+D): **10 insns per self-call on the fast path**, of which 9 are dependent memory ops (LDR→ADD→STR chains to the same cache line — not IPC-hidden).

Predicted wall-time (halved per Rule 5 for ARM64 superscalar): 10 insns × 5.15M calls × 0.5 / (3 IPC × 4 GHz) ≈ **0.0021 s** raw → but the LDR/ADD/STR forward chains serialize through L1, so the halving is conservative. Expected ackermann: **−0.03 to −0.06 s (−5 to −10%)**. Target: 0.563 → **0.510 s**.

Why NOT CallMode / ctx.Regs removal this round: RETURN at `tier1_control.go:224` loads `ctx.CallMode` and branches on it, so we cannot drop the `STR 1 → CallMode` on self-call setup without a deeper RETURN refactor. ctx.Regs writes are load-bearing for slow-exit code; deferring them to the exit handlers is a larger change. Both documented in `opt/knowledge/tier1-call-overhead.md` for a future round.

---

## 2. Pre-plan checklist (mandatory, filled)

- [x] Diagnostic run — `/tmp/gscript_ack_tier1.bin`, disasm offsets 608–1176 counted
- [x] Hypothesis cross-checked — CallMode write NOT removable (constraint found via `tier1_control.go:224` read); plan scope shrunk accordingly
- [x] File-size budget — `tier1_call.go` 554 → ≤620 after edits (<1000 limit)
- [x] Ceiling rule — `tier1_dispatch` = 0 failures. Even if R26 fails with no_change, category goes to 1 (not blocked)
- [x] Source read before planning — `tier1_call.go:95-488`, `tier1_control.go:199-227`
- [x] Superscalar halving applied — 10 insns → ~5 insn-equivalents at wall time
- [x] Correctness floor — SP-based runaway-recursion check replaces NativeCallDepth guarantee

---

## 3. Tasks

### Task 0 — Pin the baseline (diagnostic fixture) ✓ DONE (878e64a)
**Scope**: `internal/methodjit/tier1_ack_dump_test.go` (new, ~60 LOC) — the R26 diagnostic agent already wrote this; promote it to a committed test so future rounds can measure insn-count delta.

**Deliverables**:
- Test function `TestDumpTier1_AckermannBody` that dumps Tier 1 machine code for a 2-arg recursive function and asserts the caller-side self-call-fast-path insn count ≤ current value.
- Record current count in a constant (`selfCallFastInsnBaseline = 60`, or whatever the test measures after rebuild).

**Done when**: test passes on main HEAD; prints the insn count on verbose mode.

### Task 1 — Remove NativeCallDepth bookkeeping on self-call path ✗ FAILED — PREMISE ERROR (see opt/premise_error.md)
**Files**: `internal/methodjit/tier1_call.go` lines 116-120 (pre-check), 380-382 (inc), 395-397 (dec).

**Change**:
1. Move the pre-check out of `emitBaselineNativeCall` into `emitDirectEntryPrologue` **and** `emitSelfCallEntryPrologue` as a **SP-floor guard**:
   ```
   // in prologue, after STPpre:
   //   LDR  X3, [ctx, execCtxOffStackFloor]   (new ctx field)
   //   CMP  SP, X3
   //   b.lo  stack_overflow_slow               (→ exit-resume ExitStackOverflow)
   ```
   The floor is initialised once by the Go side when the ExecContext is created (base SP minus ~2 MB).
2. Delete the per-site `LDR/CMPimm/b.ge 48` at lines 118-120.
3. Delete the per-site `LDR/ADD/STR` at 380-382 and 395-397 **on the self-call fast path only**. Normal calls keep the counter for now (Go-side unwinding depends on it — Task 3 of a future round).
4. Add a new ExecContext field `StackFloor uintptr` set in `vm.NewExecContext` (or wherever the ExecContext is born) to `currentSP - 2*1024*1024`.
5. Add a new `ExitStackOverflow` constant and Go handler that reports a Lua-style `"stack overflow"` runtime error.

**Insn delta per self-call**: −3 (pre-check) −3 (inc) −3 (dec) = **−9 insns**
**Prologue cost added**: +3 insns per function entry. Net for ackermann which averages ~1 BL per function body = −9 + 3 = **−6 insns per call**.

**Tests**:
- `mutual_recursion_overflow_test.go`: a deeply-recursive function that used to trip the depth-48 limit must now trigger `ExitStackOverflow` from the SP guard instead. Verify the error message.
- Correctness: all existing benchmarks must still pass (`go test ./internal/methodjit/... ./internal/vm/...`).

**Done when**: insn count from Task 0's fixture drops by ≥6; mutual_recursion still passes; stack-overflow test produces a clean error instead of SIGSEGV.

### Task 2 — Split the shared restore: drop dead `ctx.Constants` STR on self-call path ⊘ SKIPPED (Task 1 failed; abort protocol)
**Files**: `internal/methodjit/tier1_call.go` lines 435-438.

Current (shared between normal and self-call paths):
```go
asm.Label(restoreDoneLabel)
asm.STR(mRegRegs,   mRegCtx, execCtxOffRegs)
asm.STR(mRegConsts, mRegCtx, execCtxOffConstants)
```

Self-call never clobbered X27 (`mRegConsts`) — the callee is the same function, same constants. The STR to `ctx.Constants` is pure bookkeeping for an invariant. Move it into the normal-path restore branch only.

**Change**: delete line 438 from the shared epilogue and move `STR mRegConsts → ctx.Constants` to inside the normal-path restore block (after line 422, before `B restoreDoneLabel`).

**Insn delta per self-call**: **−1**.

**Tests**: existing benchmark + correctness suite.

**Done when**: insn count drops by 1 additional; all tests pass.

### Task 3 — Verify and stop. ⊘ SKIPPED (abort protocol — data-premise-error)
**Scope**: run the full benchmark suite via the median-of-5 evaluator:
```
bash scripts/run_benchmarks.sh --runs=5 --median
```
Compare against `benchmarks/data/latest.json`. Commit per-task.

**Done when**:
- ackermann improves by ≥3% (floor; stretch −10%)
- mutual_recursion does not regress (>2%)
- no other benchmark regresses by >3%
- insn count in Task 0's fixture matches the sum of Task 1 + Task 2 deltas

If **ackermann doesn't move by ≥3%** after both tasks land and insn count drops as predicted → STOP, document in `opt/knowledge/tier1-call-overhead.md` as "insn savings absorbed by M4 IPC headroom on CALL/RETURN path" and mark the round `no_change`. This is the calibration path — the diagnostic expects dependent LDR/STR chains to be visible, but if the M4 store buffer absorbs them (like R22 showed for predicted branches), we learn that as new knowledge and shift to a different Tier 1 target next round.

---

## 4. Out of scope this round (queued)

- **Normal-path NativeCallDepth removal** — needed for object_creation / binary_trees / method_dispatch. Blocked on audit of Go-side unwind code that reads `ctx.NativeCallDepth`. Next round.
- **CallMode STR removal** — blocked on restructuring RETURN emission to not read `ctx.CallMode`. Two approaches noted in knowledge base: (a) encode direct-vs-baseline in LR target, (b) compile two body variants. Pick in a future round.
- **ctx.Regs exit-lazy flush** — defer ctx.Regs publication to exit handlers only. Requires auditing every slow-exit site to insert a flush. Touches ~10 files. Future round.
- **Tail-call threading (LuaJIT-style)** — convert self-recursion to back-edge branch reusing the same stack frame. Biggest win (removes BL/RET entirely) but requires a dedicated compilation mode. Multi-round.

---

## 5. Risks & mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| Insn savings absorbed by M4 superscalar (R22 lesson) | Medium | Halved prediction already applied; Task 3's stop condition treats `no_change` as a calibration data point, not a failure to force a fix |
| Stack overflow from removed NativeCallDepth | Low | SP-floor prologue guard added; overflow test written |
| Removing `ctx.Constants` STR breaks slow-exit that reads it | Low-medium | Grep `execCtxOffConstants` before landing Task 2; if any exit handler reads it post-return, revert just Task 2 |
| RETURN path discovers CallMode ordering bug | Low | Not touching CallMode writes this round |
| File size exceeds 1000 lines | Low | 554 + ~60 = 614, plenty of headroom |

---

## 6. Commit plan

1. `test: tier1 self-call fast path insn-count fixture (R26 Task 0)`
2. `perf: drop NativeCallDepth on tier1 self-call fast path; SP-floor guard at prologue (R26 Task 1)`
3. `perf: drop dead ctx.Constants STR on tier1 self-call restore (R26 Task 2)`
4. `verify: R26 baseline refresh — ackermann −X%` (Task 3, single commit with latest.json update)
