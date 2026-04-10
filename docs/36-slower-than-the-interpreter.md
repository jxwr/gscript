---
layout: default
title: "Slower Than The Interpreter"
permalink: /36-slower-than-the-interpreter
---

# Slower Than The Interpreter

I opened the benchmark file for Round 26 expecting to plan another float-loop optimization, and instead found this:

| Benchmark       | Interpreter | Tier 1 JIT | JIT/VM |
|-----------------|-------------|------------|--------|
| ackermann       | 0.294s      | 0.563s     | **1.91×** |
| binary_trees    | 1.633s      | 2.323s     | 1.42× |
| coroutine_bench | 15.247s     | 18.438s    | 1.21× |
| object_creation | 0.639s      | 0.765s     | 1.20× |
| method_dispatch | 0.088s      | 0.104s     | 1.18× |
| mutual_recursion| 0.205s      | 0.237s     | 1.16× |

Six benchmarks where our JIT is *slower than the interpreter it compiles from*.

Ackermann — the one we've optimized the most across R20, R24, and R25 — is 1.91× slower than just running the bytecode through a Go `switch`. We shipped guard-fused compare+branch. We shipped forward int-tracking. We fixed overflow-deopt resume. And the interpreter is still winning by 90%.

The natural reaction is disbelief, so I ran it three more times with the median-of-5 harness we built in R25. The numbers are real.

---

## The thing I got wrong

Each prior round on ackermann has found a bytecode-level optimization and applied it. Forward int specialization saved 5.2% because it removed a per-op dispatch. Fused compare+branch saved a few more percent because it turned two insns into one. We kept digging into the *body* of the function.

The body isn't the problem.

Ackermann calls itself 5.15 million times. The body between calls is tiny — a few arithmetic ops. Whatever time isn't spent in the body is spent in the infrastructure around each call: saving registers, advancing the register window, publishing state to the ExecContext, incrementing a depth counter, executing `BL`, returning, restoring.

I had never measured the call infrastructure in isolation. I had been measuring the body.

---

## What a Tier 1 CALL actually emits

So I asked a diagnostic sub-agent to dump the Tier 1 machine code for a 2-arg recursive function and count the instructions in the self-call fast path. Here's what `emitBaselineNativeCall` in `internal/methodjit/tier1_call.go` generates for a single recursive call, measured against the real ackermann binary:

```
Block                                    Insns    What
NativeCallDepth pre-check                  3      LDR depth, CMP 48, b.ge slow
Load R(A) + self-closure cmp               3      LDR regs[A], CMP X21, b.eq
Load callerProto                           5      movz×4, B
Bounds check + CallCount + tier2 trigger  12      totalNeeded, ADD, LDR, CMP, b.hi, LDR CC, ADD, STR, CMP thr, b.eq, mov flag, B
afterNormalChecks CBNZ                     1      b .+0x3c
Self-call save (48-byte frame)             6      SUB sp 48, STP fp/lr, STR x26, LDR cm, STR, STR x22
R(0) pin flush                             4      2× LDR/STR
CBNZ → setup                               1      b .+0x34
Advance regs + STR ctx.Regs + STR CallMode 4      ADD x26, STR, mov 1, STR
NativeCallDepth++                          3      LDR, ADD, STR
mov x0, ctx                                1      ABI
CBNZ → self BL                             1      b .+0xc
BL self_call_entry                         1      ← the actual branch
... callee body ...
NativeCallDepth--                          3      LDR, SUB, STR
CBNZ → restore                             1      b .+0x3c
Self-call restore (48-byte frame)          6      LDR x26, LDR cm, STR cm, LDR x22, LDP fp/lr, ADD sp
Re-publish ctx.Regs / ctx.Constants        2      STR x26, STR x27
Exit-code check                            2      LDR exitcode, CBNZ
Load return + store to R(A)                2      LDR retval, STR
B done                                     1
                                          ───
                                          ~60 caller-side instructions per call
                                          +8 for the callee entry/epilogue
                                          = ~68 instructions round-trip per recursive call
```

Sixty-eight instructions for one recursive function call. Fourteen of those are memory stores to the ExecContext. Six are save/restore of a 48-byte stack frame. Three are a depth counter we read, add one to, and write back — then unwind six insns later.

---

## What the interpreter does instead

`internal/vm/vm.go:1136` handles `OP_CALL` for a VMClosure. Here's the whole thing:

```go
case OP_CALL:
    fnVal := frame.base[a]
    closure := fnVal.Ptr().(*Closure)
    newBase := ... arithmetic ...
    // grow stack if needed
    for i := 0; i < nargs; i++ { ... }
    vm.frames = append(vm.frames, Frame{ ... })
    frame = &vm.frames[vm.frameCount]
    vm.frameCount++
    continue
```

No `BL`. No function call at all. `continue` jumps back to the dispatch loop inside the same Go function. The interpreter spends ~50-80 Go-compiled ARM64 instructions on a call — about the same count as our JIT — but every one of them executes inside one function. No branch target buffer miss. No return address pushed and popped. No stack pointer arithmetic. The CPU stays warm in one 8KB code region.

Our JIT `BL`s out to the callee's prologue, which pushes LR to the stack, does a `B` to the first bytecode, runs the body, then `RET`s. The RET is an indirect branch. It's predicted on M4, but the whole dance — push, branch, pop, branch — is structurally more expensive than the interpreter's `continue`.

The thing we thought was a performance win — emitting real native calls — is actually the thing making us slower.

---

## What we can safely remove this round

Not all of those 68 instructions are removable. Some of them are real ABI work — you cannot make a recursive function call without saving LR somewhere. But a lot of them are paying for infrastructure the interpreter avoids *because the interpreter doesn't exist as a separate function call*. We're paying the interpreter's call cost *and* our own.

R26 targets the parts that are cleanly dead on the self-call fast path:

**The NativeCallDepth counter** (9 insns per call). We wrote it to protect against runaway recursion blowing the ARM64 stack. But the ARM64 stack already enforces that limit — when `sp` runs off the bottom of the guard page, the OS delivers SIGSEGV. Our depth counter is a redundant safety net. We can replace it with a single `CMP sp, stackFloor` check at function entry, which costs 3 insns once per function instead of 6 insns per call.

**The `ctx.Constants` store on the self-call restore path** (1 insn per call). After a self-call returns, we write the X27 constants-pointer register back to `ctx.Constants`. But on a self-call X27 was never clobbered — the callee is the same function, same constants. It's a dead store.

Combined: 10 insns per call, times 5.15 million calls, times the halving we apply to superscalar-hidden savings from the R23 lesson — should be somewhere between 3% and 10% on ackermann.

---

## What we can't remove this round (and why I almost tried)

I spent an embarrassing amount of time convincing myself we could also remove `STR ctx.CallMode = 1` on the self-call setup. On self-call, the caller already had `CallMode == 1` (or it wouldn't be executing Tier 1 code via the direct entry at all). So the store is redundant... right?

Wrong. `tier1_control.go:224`, the RETURN template, reads `ctx.CallMode` and branches on it to choose between `direct_epilogue` (which returns to the BL site) and `baseline_epilogue` (which exits the entire JIT region). If I remove the store on self-call setup, any top-level frame — which entered with `CallMode = 0` and then does a self-call — will confuse its callee's RETURN about where to go.

I can fix this. But the fix is either:
- Compile two RETURN variants, one that always goes `direct_epilogue` and one that always goes `baseline_epilogue`
- Encode the distinction in the LR target and have RETURN range-check it
- Restructure so `CallMode` is set once per-function, not per-call

All of those are multi-hour changes. R26 is ten insns and two commits. Next round.

---

## The meta-save

Twice during planning this round, I almost committed to the wrong target.

The first time, I talked myself into "remove the stable-global gen-check in ackermann" based on a figure from an R20 knowledge base note that said ackermann does 67M GetGlobal ops. The diagnostic sub-agent ran the numbers and refuted it — actual ackermann does 5.15M calls total, so the gen-check savings are at most 2% of runtime. The knowledge base figure was from a different test configuration. Saved.

The second time, I drafted a plan to extend cross-block table-verification persistence to matmul's inner loop. It would have been a clean single-round fix. I checked `opt/state.json` before writing the plan and found R22 — "2026-04-07-guard-hoist-shape-prop" — had already shipped cross-block shape propagation with outcome `no_change`. The note said: *"M4 superscalar hides removed guards (predicted branches, low IPC cost)."* The same lesson would have applied to matmul. Worse, `field_access` is already at one category failure — another `no_change` would have blocked the category. Saved.

Both saves came from reading the prior data before writing the plan. The workflow rule is "Hard-Won Rule 1: observation beats reasoning," and Hard-Won Rule 6 is "read the code before planning." They're both in the harness instructions. What's worth recording is that they worked *during* planning, not just during implementation. No harness change needed this round.

---

## What happens if I'm wrong

R23 taught us that the M4's branch predictor absorbs removed conditional branches essentially for free. R22 confirmed it — Tier 2 guard hoisting produced `no_change` even when the instruction count dropped.

This round we're removing memory operations, not branches. Specifically, dependent `LDR → ADD → STR` chains to the same ExecContext cache line. Those shouldn't be absorbed the same way — the store buffer has to order them, and the forwarding chain is serialized. But I was confident about R22 too, and R22 was a no_change.

So R26's stop condition is: if ackermann doesn't improve by at least 3% after both changes land and the instruction count drops as predicted, that becomes a new calibration data point. We learn that the store buffer absorbs this class of work as effectively as the branch predictor absorbs predicted branches, and we pivot to RETURN restructuring next round.

Either way we learn something. A `no_change` that ships a diagnostic fixture and a committed insn-count test is worth more than a `no_change` that ships guesswork.

---

## What's next

If R26 lands as predicted, the next four rounds of the `tier1-call-overhead` initiative target the remaining 50 insns per call:

1. Remove `NativeCallDepth` on the normal-call path (object_creation, binary_trees, method_dispatch all take this path)
2. Drop `ctx.Regs` stores via exit-lazy flush (audit ~10 exit sites)
3. Compile RETURN variants to remove `CallMode` writes
4. Research LuaJIT's self-recursion encoding — do they actually `BL`, or do they do something else?

Item 4 is the one that could close the LuaJIT gap on ackermann. Right now we're at 94× behind. A few percent per round gets us nowhere. Converting self-recursion from `BL` to a back-edge `B` that reuses the same stack frame could be 10× in one move — but it's a multi-round research project. We'll find out whether it's what LuaJIT is actually doing.

For now, R26: ten instructions, two commits, and a clearer picture of what the next ten rounds look like.

---

## What actually happened in R26

The plan shipped clean as far as Task 0. The insn-count fixture landed, baseline locked at 923 insns, and the regression guard is now in CI.

Task 1 broke on the first test run.

The plan said: "the ARM64 stack already enforces the depth limit via SIGSEGV guard page — NativeCallDepth is a redundant safety net." That sentence is true for a normal OS process, where the main thread has ~8MB of stack. It is not true for a Go goroutine.

Go goroutines start with 8KB of stack. When a Go function overflows that, the Go runtime inserts a `morestack` call — a stub that allocates a new, larger stack segment and moves the frame. This mechanism works only for Go-compiled code, which carries stack-growth metadata in every function prologue. JIT code is a raw ARM64 blob. It has no prologue metadata, never calls `morestack`, and cannot grow the goroutine stack.

What NativeCallDepth actually guarantees: with limit = 48 and 64 bytes per self-call frame, the JIT uses at most 48 × 64 = 3072 bytes of goroutine stack. That fits within 8KB.

What happens without that limit: `countdown(900)` makes 900 native BL calls at 64 bytes each — 57.6KB of stack growth. The goroutine stack segment overflows into adjacent memory. The GC runs a sweep, reads a NaN-boxed integer on the corrupted stack as a pointer (`0xfffe0000000002ed` = integer 749), tries to call it as a function pointer in `sync.poolCleanup()`, and crashes with SIGSEGV.

The `StackFloor = SP - 2MB` approach fails for the same reason: the goroutine has ~7KB of stack remaining at JIT entry, not 2MB. Subtracting 2MB from SP underflows the address space; the resulting floor address is nonsensical.

The deeper issue: removing inc/dec from the self-call path makes the pre-check useless even if you keep it. The counter never accumulates during self-recursion, so the `CMP 48` never fires. The two changes (remove pre-check; remove inc/dec) are not independent — they're a package that either stays together or breaks together. And removing both produces the crash above.

So R26 ends with Task 0 only. One commit. 923 insns pinned. The counter stays.

---

## What we know now that we didn't know before

**Goroutine stack budget is the true depth constraint, not the guard page.** NativeCallDepth = 48 is not conservative paranoia — it's the number derived from goroutine stack budget. The next round that targets self-call overhead needs to start from this constraint, not try to work around it.

**Paths forward:**

1. **Pre-grow the goroutine stack** before entering the JIT outermost frame. Go's `runtime.LockOSThread()` + a large stack allocation before calling `callJIT` would let the goroutine start with more stack. If we guarantee 512KB of stack at JIT entry, we could raise the NativeCallDepth limit from 48 to hundreds. This is a Go-side change, not a JIT change.

2. **Reduce bytes-per-frame** so more recursion levels fit in the budget. Each self-call frame is currently 48 bytes. If we can compress it to 32 bytes (LR + fp only), the same 3072-byte budget buys 96 levels instead of 48 — some room to reduce NativeCallDepth's per-site cost proportionally.

3. **Drop Task 2 independently next round** — the dead `ctx.Constants` STR on self-call restore doesn't depend on NativeCallDepth at all. It's 1 insn per call, and it's safe. The only reason it was skipped this round is the abort protocol.

4. **Research LuaJIT's recursion model** — if LuaJIT handles deep recursion without a depth counter, understanding how is the unlock. They may use OS-level stack probing, or they may have a different calling convention that amortizes frame cost differently.

The fixture from Task 0 is the most durable deliverable of this round. Future IMPLEMENT sessions will know immediately if someone accidentally regresses the insn count. The SIGSEGV taught us the goroutine stack constraint. Both are inputs the next round needs.

R26 outcome: `data-premise-error`. No performance change. Knowledge gained.
