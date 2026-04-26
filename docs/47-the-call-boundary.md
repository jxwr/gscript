---
layout: default
title: "The Call Boundary"
permalink: /47-the-call-boundary
---

# The Call Boundary

Ackermann was supposed to be the clean Tier 2 win.

It is recursive. It is integer-only. It has tiny bodies and millions of calls.
It is the kind of benchmark where a method JIT with SSA, type knowledge, and a
native recursive call path ought to embarrass the interpreter. Instead, for a
long time, it did the opposite: the VM beat the JIT, and LuaJIT stayed far out
in front.

That failure was useful because it forced the question that the earlier rounds
kept dodging:

> What exactly does "Tier 2" mean if the call boundary is still boxed?

The answer was uncomfortable. Tier 2 was optimizing the body, then paying the
VM calling convention every time it called itself.

## The wrong rule

I had been treating tiering like a ladder: Tier 2 should be faster than Tier 1
because it is a higher tier. That is not how optimizing runtimes work.

V8 does not make Maglev or TurboFan faster by naming them higher tiers. They
win when the optimized tier owns enough of the execution to make speculation,
guards, deopt metadata, and representation choices cheaper than the exits they
create. A higher tier with the wrong boundary is just a more elaborate way to
lose.

Ackermann had exactly that shape. The hot loop in `<main>` called `ack(3, 4)`
hundreds of times. The recursive function could compile, but `<main>` often
stayed in Tier 1 because the loop-call filter could not prove the global
function target yet. Even when the recursive function entered Tier 2, the call
boundary still shuffled boxed values through the VM register file.

So the program kept crossing a bad boundary:

```
Tier 1 loop
  -> Tier 2 recursive function
     -> boxed recursive call
        -> boxed return
```

The body was not the whole problem. The boundary was.

## The first fix was not the raw ABI

The first production fix was more boring than a new calling convention:
`<main>` had to know that top-level function declarations exist.

Before this round, `ack` is created by a straight-line
`OP_CLOSURE; OP_SETGLOBAL` prefix. At compile time, the global table does not
yet contain that binding, so the conservative Tier 2 loop filter saw a loop
with an unresolved call and rejected it.

The fix was a lexical prefix scanner:

- recognize only the initial declaration sequence;
- stop at the first executable instruction;
- use the result only for the loop-call safety filter;
- do not feed it into general inlining.

That last point matters. A previous experiment tried to make top-level lexical
globals available to the inliner and caused loop-callee regressions. This round
kept the information at the narrow layer that needed it.

Once `<main>` could stay native, the benchmark stopped doing the worst possible
thing: running the hot outer loop in Tier 1 while repeatedly crossing into Tier
2.

## The second fix was stack reality

The next blocker was a Go runtime constraint I had already hit once.

Tier 2 recursive frames are raw ARM64 frames. The Go runtime cannot see them.
Go goroutine stacks start small and grow only through Go-compiled function
prologues with stack-split metadata. JIT code has none of that metadata. If a
recursive JIT chain grows the goroutine stack too far, the runtime cannot move
or grow those frames safely.

Earlier rounds learned this the hard way by trying to remove the recursive
depth counter. That produced memory corruption, not speed.

The stable fix here was to reserve native stack budget before entering Tier 2.
That made deeper recursive native calls safe enough to raise the native call
depth limit and eliminated the repeated-call corruption canary.

This still was not the raw ABI. It was the prerequisite that made a raw ABI
safe to exercise.

## The ABI that was only analysis

At this point there was a file that looked promising:
`specialized_abi.go`.

It recognized raw-int self-recursive candidates: fixed integer parameters,
integer return, static self calls, and no side-effecting bytecodes. Ackermann
and fib qualified. Non-recursive or side-effecting functions did not.

But analysis is not a calling convention.

For a while the descriptor was only saying "this function could use a raw-int
ABI." Codegen still used the boxed VM ABI. The recursive call still stored
boxed arguments in the VM register file, entered the normal direct entry, and
read the boxed return value through `ctx.BaselineReturnValue`.

The analyzer was correct. It just was not connected to the machine code that
mattered.

## What a raw self ABI actually needs

The tempting implementation is tiny:

```
X0..X(N-1) = raw integer args
BL t2_numeric_self_entry_N
result = X0
```

That is not an ABI. That is a happy path.

The real ABI needed four contracts.

**Register liveness.** The caller and callee both use the same Tier 2 register
allocator. The raw call path must know which SSA values are live across the
call, which values are currently raw ints, and what representation should exist
at the merge point after success or fallback.

**Exit resume.** A numeric body may still exit. The resume label must be
pass-specific, because a call inside the numeric body must resume into the
numeric continuation, not the boxed body.

**Fallback.** Go-side call handlers are boxed-only. If the raw call cannot run
or the raw callee exits, the caller has to materialize a normal VM call frame:

```
regs[funcSlot]     = boxed function
regs[funcSlot + 1] = box(rawArg0)
regs[funcSlot + 2] = box(rawArg1)
...
```

Only then can it take the existing `ExitCallExit` path. On resume, the result is
tag-checked before rejoining the raw continuation. If the fallback returns a
non-int, the raw continuation is invalid and the JIT deopts.

**Return convention.** Boxed direct entries return through `regs[0]` and
`ctx.BaselineReturnValue`. Raw numeric entries return through `X0`. Mixing those
two by accident gives you a program that works until the first nested recursive
call resumes through the wrong convention.

That is why the raw path is separate now. Generic `emitCallNative` remains the
boxed VM ABI. Raw self recursion goes through `emitCallNativeRawIntSelf`.

Two call conventions. Two return conventions. One explicit boundary.

## The weird global lookup

One bug looked unrelated until it was not.

The numeric body still had a `GETGLOBAL ack` instruction in it. In the boxed
body, that is fine: look up the global, check the cache generation, get the
closure. In the raw self-recursive body, the call boundary has already proven
the closure. Taking a global cache exit in the middle of numeric recursion only
creates a storm of fallback work.

So numeric self `GETGLOBAL` does not do a global lookup anymore. It materializes
the current closure from `ctx.BaselineClosurePtr`, boxes it, and keeps the
numeric body native.

This is not an Ackermann special case. It is a property of a static self-call
ABI: inside the raw self body, "the function named by my own global" is the
current closure supplied by the call boundary.

## The numbers

On this machine, the progression for the suite benchmark was:

| Mode | Time |
|------|-----:|
| VM, `benchmarks/suite/ackermann.gs` | ~0.287s |
| JIT before the `<main>` fix | ~0.41s |
| JIT after boxed static self-call path | ~0.027-0.030s |
| JIT after raw self ABI v1 | ~0.017-0.019s |
| LuaJIT, `benchmarks/lua/ackermann.lua` | ~0.006s |

The Go benchmark tells the same story:

| Benchmark | Time |
|-----------|-----:|
| `BenchmarkGScriptVMAckermannWarm` | ~573-577 us/op |
| `BenchmarkGScriptJITAckermannWarm` | ~32.1-32.3 us/op |

So Ackermann moved from "JIT slower than VM" to roughly 15-18x faster than the
VM. The CLI gap to LuaJIT is now about 3x, not orders of magnitude.

That is still not "beat LuaJIT." But the shape of the remaining gap changed.
The easy boxed argument and boxed return traffic is gone from the raw recursive
path. What remains is mostly frame and call overhead.

## What is still expensive

The v1 raw ABI intentionally reuses the full Tier 2 frame.

That was the right correctness choice. The first version needed to prove:

- raw args enter through `X0..X3`;
- raw return leaves through `X0`;
- caller state survives;
- boxed fallback works;
- numeric resume labels are correct;
- nested Ackermann recursion does not corrupt the runtime.

But a full frame is not LuaJIT-class recursion. It saves and restores far more
state than a narrow self-recursive ABI should need. It still carries fallback
materialization machinery close to the hot path. It still has a generic `OpCall`
shape where the compiler really wants a first-class static self-call lowering.

The next wins are not another arithmetic peephole. They are boundary work:

1. make static self calls explicit before codegen;
2. shrink `t2_numeric_self_entry_N` from a full frame to a verified thin frame;
3. move fallback-only boxed materialization away from the fast path;
4. carry the `SpecializedABI` descriptor directly instead of asking old helper
   predicates at every layer;
5. remeasure against LuaJIT after each frame reduction.

## The lesson

The earlier question was whether Tier 2 should always beat Tier 1.

No. A tier does not win by existing. It wins when its assumptions survive across
the hot boundaries.

For Ackermann, the winning assumption was not "integer arithmetic is fast." We
already knew that. The winning assumption was:

> This function calls itself, with fixed raw integer parameters, and the result
> is a raw integer.

Once that assumption became a real ABI instead of an analysis note, Tier 2
started to behave like an optimizing tier.

The benchmark is not done. LuaJIT is still faster. But the remaining problem is
finally the right one: make the recursive call boundary smaller, not make the
body cleverer.
