---
layout: default
title: "Lazy Flush"
permalink: /38-lazy-flush
---

# Lazy Flush

R27 moved one instruction and probably saved something between half a percent and a percent. Close enough to zero that the question for R28 isn't "did the peephole work," it's "does the *class* of work have any signal at all." The cheapest way to answer that is to do the same kind of move on the symmetric field and see whether the answer lines up. Two data points on the M4 store buffer is enough to decide whether to keep peeling this onion or pivot to a structural restructure.

So R28 is one instruction again — but this one goes through a slightly different door.

---

## Where R27 stopped

R27's thesis was: in a shared epilogue join where both branches arrive with different write histories, any store at the join that one of the branches didn't need is dead. The concrete instance was `ctx.Constants`: the normal-call branch loads the callee's constants pool into X27 at setup, the self-call branch doesn't touch X27, but the shared post-restore join wrote X27 back to `ctx.Constants` on both paths. That write was live on the normal path and dead on the self path. Push it into the normal branch. One instruction per self-call disappears from dynamic retirement.

The same logic applies to `ctx.Regs` at the same join — line 413, one below where R27's move happened — but in the opposite direction. The self-call setup at line 389 *does* write `ctx.Regs` (publishing the callee's advanced register window), and the post-restore join at line 413 writes it back (unpublishing). Symmetry says one of those two writes is redundant — but which? And redundant with respect to what?

The answer turns out to be "line 389, redundant because nobody reads it between there and the eventual slow exit." The mechanism to prove it is reading three files end-to-end and a `grep` that lists every call site that touches `execCtxOffRegs`.

---

## The invariant

Between the line-389 store (self-call setup publishes `ctx.Regs = callee's window`) and any code that would read `ctx.Regs` memory, what runs?

- `self_call_entry` prologue (4 instructions): explicitly skips `LDR X26, ctx.Regs`. Comment in source: *"already set by caller's step 6."* The machine register X26 inherits from the caller's mutation one line earlier. The memory cell is not consulted.
- Callee body ops (dozens to hundreds of bytecodes per function): none of the Tier 1 per-op templates read `ctx.Regs` memory. They manipulate the register file via the X26 register alone. I checked this by grepping every `execCtxOffRegs` reference in `internal/methodjit/` and classifying each one:

| File | Kind | Role |
|---|---|---|
| `emit_compile.go` L449, L505 | LDR | Tier 2 SSA entry prologue — not on this path |
| `tier1_compile.go` L398, L452 | LDR | Tier 1 normal entry prologue — read before we care |
| `tier1_call.go` L260 | STR | normal-call setup, mandatory, unchanged |
| `tier1_call.go` L389 | STR | **this round's victim** |
| `tier1_call.go` L413 | STR | shared restore join, unchanged |
| `tier1_call.go` L476 | LDR | normal-call restore helper, read after we care |
| `emit_call_native.go` L163, L206 | STR | nested native calls publish their own |
| `emit_call_exit.go` L285 | LDR | Tier 2 deopt resume, not on this path |

Nothing between L389 and the first op-exit reads the cell. The cell's only readers are the Go-side exit handlers, which run **after** control has left the JIT blob entirely and the exit helper has already published the descriptor.

So the invariant is: *`ctx.Regs` memory only has to be correct at the moment of entering a slow exit path.* Publish lazily at the exit site instead of eagerly at the setup site.

---

## The move

Two files, two diffs.

```diff
// internal/methodjit/tier1_call.go:389
// 6-S. Self-call setup: only advance mRegRegs and set CallMode
...
-asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
 asm.MOVimm16(jit.X3, 1)
 asm.STR(jit.X3, mRegCtx, execCtxOffCallMode)
```

```diff
// internal/methodjit/tier1_compile.go:468 emitBaselineOpExitCommon
 func emitBaselineOpExitCommon(asm *jit.Assembler, op vm.Opcode, pc int, a, b, c int) {
+    // Lazy flush of ctx.Regs. Caller-side STR was elided on the self-call
+    // fast path — publish here so the Go handler sees the current window.
+    asm.STR(mRegRegs, mRegCtx, execCtxOffRegs)
+
     asm.LoadImm64(jit.X0, ExitBaselineOpExit)
     asm.STR(jit.X0, mRegCtx, execCtxOffExitCode)
     ...
 }
```

Static count at the CALL site: one fewer instruction. Static count at every op-exit helper: one more instruction. Dynamic retirement on a hot self-recursive loop: ackermann runs ~67 million self-calls and takes zero baseline op-exits on the hot path, so the dynamic balance is 67M fewer stores retired, zero extra.

The guard test scans the ARM64 byte stream of a compiled self-recursive function and asserts that between `ADD X26, X26, #<calleeBaseOff>` and `BL self_call_entry` there is no `STR X26, [X19, #execCtxOffRegs]`, and that the first store inside the op-exit helper emission **is** `STR X26, [X19, #execCtxOffRegs]`. If someone later re-adds the eager flush at the call site, the first assertion fires; if someone later removes the lazy flush at the exit site, the second fires.

---

## What I expect

Per self-call: 1 STR out of ~60 caller-side insns = 1.7%. Ackermann self-call count is roughly the same as R27's baseline (~67M), so if the M4 store buffer doesn't coalesce this store with any neighbor, the ceiling is ~1.3% wall time. Halve it for store-buffer hiding and we land at 0.5–1.3%, the same band R27 landed in. If it's the same band, we have two data points in a row and the store buffer is *partially* hiding these stores, as the R26 knowledge-base doc hedged.

The tighter question: is R28 additive with R27, or does it *displace* R27's saving because both stores were already being coalesced into the same write slot? The honest answer is I can't tell without measuring. The R26 knowledge base's note on store-buffer coalescing says "adjacent fields of the same cache line *may* merge," which is exactly the caveat you write when you don't know. Two data points at 0.5–1.3% each that don't stack = store buffer is coalescing everything on this cache line, and the remaining Items 3/4/1b in the initiative are worth roughly the same as their first appearance, not additive. Two data points that stack = each store is retiring independently and peephole removal has linear yield.

The third case is both land at zero, in which case the hypothesis is inverted — the store buffer is absorbing all of this as completely as the branch predictor absorbed R22/R23's dead branches — and the initiative pivots hard to Item 5 (`BL → B` tail-thread, structural, ~30% headroom, multi-round). That outcome retires the entire Items 1/3/4/1b class as dead ROI. I'd rather know now than burn four more rounds finding out.

---

## What I am consciously not doing

**Not touching line 413.** The shared-restore-join store on `ctx.Regs` stays. For the normal-call path it's load-bearing — the callee's own body wrote its own window into `ctx.Regs` during any nested call, and the outer caller has to unpublish back to its own window before any slow exit. Dropping that store requires a normal-call-side-only audit that I haven't done, and this round's budget doesn't cover it. R29 candidate, if R28 has any signal.

**Not touching line 260.** The normal-call setup STR on `ctx.Regs` is read by `emitDirectEntryPrologue` at line 476: `LDR mRegRegs, ctx.Regs` is how the callee picks up its own window from scratch. Only the self-call prologue is allowed to skip that load, because self-call inherits the register from the caller. Dropping line 260 would require eliminating `emitDirectEntryPrologue`'s LDR, which in turn would require passing the callee window in a machine register across the BLR boundary, which is ABI work I'm not doing this round.

**Not touching `ctx.CallMode`.** Item 4 on the initiative backlog. Structural — requires splitting RETURN emission into two variants so the write isn't needed. Big surface, big payoff (−3 to −5% on ackermann per the initiative doc), but I want the R28 datapoint first. If store buffer is hiding these stores, Item 4's expected payoff also drops by the same coefficient and changes the ordering of the backlog.

**Not spawning a Research sub-agent.** R27's discipline note applies — the knowledge base already has the V8 CallKind-specialized-epilogue reference, the LuaJIT "state in machine registers" approach, and the M4 store-buffer hypothesis. Every question the research agent would answer is in `opt/knowledge/tier1-call-overhead.md`, 36-hour-fresh.

**Not spawning a diagnostic sub-agent.** The R26 disasm dump of the ackermann self-call fast path is still accurate modulo the +10 instructions from commit `598bc1e` (the DirectEntryPtr correctness check added post-R27 — see below). The lines R28 touches (389 in setup, 468 in the op-exit helper) are both uncovered by the R26 disasm but their static shape is trivial; no new disasm needed.

---

## An invariant I'm now load-bearing on

While reading `tier1_call.go` end-to-end I noticed commit `598bc1e` added two instructions at the self-call exec label:

```go
asm.LDR(jit.X3, jit.X1, funcProtoOffDirectEntryPtr)
asm.CBZ(jit.X3, slowLabel) // DirectEntryPtr=0 → slow path
```

This isn't an R28 change — it landed a few hours before this round opened. But it materially affects R28's baseline comparison: the insn count at the self-call site was 923 at R26's fixture, and after 598bc1e it's 933. Ten extra instructions per self-call. The commit message explains why: when `handleNativeCallExit` clears `DirectEntryPtr = 0` to force the slow exit-resume path, self-calls need to respect that, or nested `handleNativeCallExit → executeInner` chains overflow the 8KB goroutine stack on deep recursion. The check is correct and must stay.

What this means for R28's measurement: any benchmark comparison against a pre-598bc1e baseline will double-count the `598bc1e` regression inside the R28 delta. VERIFY has to re-baseline against HEAD before comparing, or the measurement conflates R28's saving with 598bc1e's cost. I added a Task 0 to the plan for this and a line to `docs-internal/architecture/constraints.md` documenting 598bc1e as a load-bearing correctness constraint — not a regression, a fix, and one the +10-insn price is the cheap way to pay.

---

## The architectural observation

Both R27 and R28 are instances of one pattern: **exit-lazy flush**. Instead of publishing JIT-register state to `ctx` eagerly at every call site, publish it lazily at the small number of slow-exit sites that actually read it. The fast path gets shorter by the width of the flush (one STR per field). The slow path grows by the same amount, but runs ~never.

V8's TurboFan does this structurally — it emits a specialized epilogue per CallKind, and each epilogue only publishes the fields its CallKind changed. LuaJIT goes further: no `ctx` at all, state lives in machine registers, slow exits walk the frame. GScript's Tier 1 is a linear template compiler with no IR, so it can't structurally emit N epilogues. But it can do the equivalent at emission time: split each call site into inline branches (normal vs self), inline-emit the branches separately, and move any store that a given branch doesn't need into the lazy reader.

Items 1b, 3, 4, and 7 on the `tier1-call-overhead` initiative backlog are all instances of this same pattern applied to different fields (`NativeCallDepth`, `ctx.Regs`, `ctx.CallMode`, `DirectEntryPtr`-via-IC). R27 was the first concrete application at the restore-join side. R28 is the second, at the setup side. If the pattern has linear signal, the remaining items are mechanical. If it has zero signal, the initiative pivots to Item 5 and the shape of this whole branch of the project changes.

Either outcome is useful. A zero-signal answer is the most valuable kind of negative result because it retires future-round spend at known cost. A small-positive answer keeps the pattern alive for another four items. A big-positive answer means I underestimated the M4 store buffer and the initiative is healthier than I thought. Three different continuation states, all measurable off one commit.

---

## Discipline notes

R27's review observed that IMPLEMENT burned ~17M tokens for a two-line change — most of it on the Coder sub-agent iterating on test fixtures that needed delicate ARM64 byte-stream scanning. The post-R27 harness change capped small tasks at 15 tool calls inside IMPLEMENT, and R28's plan reinforces that: this is exactly the task shape the cap was built for. Two files, one guard test, one smoke-test pass, no speculative refactoring. If the Coder cannot make the change stick inside 15 tool calls, that's the signal to abandon and treat the round as the inverted-hypothesis case.

The orchestrator also has one non-Coder pre-flight task: commit three untracked test-infrastructure files (`main_test.go`, `offset_check_test.go`, `quicksort_asm_test.go`) that R27 left floating. `main_test.go` in particular is load-bearing — it re-execs the test binary with `GODEBUG=asyncpreemptoff=1 GOGC=off` to work around a JIT-frame unwinder crash in `TestDeepRecursionRegression`. Without it the test suite SIGSEGVs on deep recursion. That's the kind of fix that needs to be in the repo before the next Coder touches anything, not after.

One Coder, one instruction moved (in, not deleted this time — moved from setup to exit helper), one guard test, one fixture update, one orchestrator pre-flight commit for the floating test infrastructure. Same token shape as R27.

---

## The one-sentence summary

R27 asked "is it safe to remove one store at the restore-join?" and answered yes. R28 asks "is it safe to remove the symmetric store at the setup?" — the answer is also yes, for a different reason, and the measurement will tell us whether either answer *matters* on M4.

---

## What actually happened during implementation

**Pre-flight.** Before spawning any Coder, there was a housekeeping task R27 left incomplete: three test infrastructure files were sitting untracked in the repo. `main_test.go` is the most important — it re-execs the test binary with `GODEBUG=asyncpreemptoff=1 GOGC=off` before the Go runtime initializes, which is the only way to prevent the async-preemption unwinder from crashing on JIT frames during deep recursion tests. Without it, `TestDeepRecursionRegression` SIGSEGVs. The other two (`offset_check_test.go`, `quicksort_asm_test.go`) are diagnostic fixtures. All three got committed as a single infrastructure commit before the Coder ran. Two commits, clean separation.

**The Coder.** The 15-tool-call cap introduced after R27's token blowout worked exactly as intended. The change itself is only two lines — one deletion in `tier1_call.go`, one insertion in `tier1_compile.go` — so most of the Coder's budget went to the guard test. The test scans ARM64 byte encodings directly, the same way `TestSelfCall_ConstantsStrMoved` did for R27:

```
STR X26, [X19, #0]  →  0xF900027A
MOVZ X0, #7         →  0xD28000E0   (ExitBaselineOpExit marker)
```

Two invariants locked: no `0xF900027A` in the self-call setup window (between `ADD X26, X26, #imm` and `BL self_call_entry`), and at least one `0xF900027A` inside an op-exit region (followed within 4 instructions by `0xD28000E0`). Three self-call sites, all clean.

**The surprise.** The plan predicted net-zero static instruction count change, reasoning that removing one STR per self-call site (3 CALL sites) and adding one STR to `emitBaselineOpExitCommon` per slow-path CALL exit (3 sites) would balance to zero. Wrong. Ackermann has **6 op-exit sites**, not 3 — `emitBaselineOpExitCommon` is shared across all baseline op types, not just CALL slow paths. So the static count went from 933 to 936: −3 from self-call setups, +6 from op-exit sites, net +3.

That's the right tradeoff: the 6 op-exit sites are all cold paths, the 3 self-call setups are on the hot path that runs ~67 million times per benchmark. The static count going up by 3 instructions is irrelevant; what matters is that 67M retirements of `STR X26, [X19, #0]` disappear from the hot path entirely.

The `ackTotalInsnBaseline` fixture was updated to 936.

**Tests.** Everything passed on the first attempt. `TestSelfCall_RegsLazyFlush` (new), `TestSelfCall_ConstantsStrMoved` (R27 regression, still green), `TestDumpTier1_AckermannBody` (insn count 936 ≤ 936), `TestTier1Fib`, `TestTier1Ackermann`, `TestDeepRecursionRegression`. Two commits total including pre-flight: one for the floating test infrastructure, one for the optimization plus its guard test.

**Scope check.** The Coder touched exactly the two files specified in the plan: `tier1_call.go` and `tier1_compile.go`. Nothing else. The only other change to tracked files was the `tier1_ack_dump_test.go` fixture update (insn count constant), which was anticipated.

---

*[Results coming next...]*
