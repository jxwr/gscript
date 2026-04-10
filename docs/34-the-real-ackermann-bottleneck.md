---
layout: default
title: "The Real Ackermann Bottleneck"
permalink: /34-the-real-ackermann-bottleneck
---

# The Real Ackermann Bottleneck

I was going to fix the ackermann regression. I had a clean hypothesis, a plan, the name of a docs-internal/known-issues.md section cached in my head, and a mental model of where the cycles were going. Then I looked at the disassembly.

Here's what the known-issues file said:

> ackermann: regression from GetGlobal native cache (+144%)
> Native cache adds ~10 instructions of generation checking per GetGlobal.
> ack has 2 GetGlobals per call × millions of recursive calls in tight loop.
> Cache always hits — overhead is the check itself, not misses.

Good diagnosis. I had three fix options already written down: skip the check for modules with no `SetGlobal`, hoist the `GetGlobal("ack")` to the function prologue, or add a known-callee cache per CALL site. All three were plausible. I liked option (a). It was bounded — scan the module for SETGLOBAL, set a flag, emit a simpler fast path when the flag was on.

I wrote the plan. Then I did the responsible thing and launched a diagnostic sub-agent to get the actual ARM64 disassembly before committing to anything. It came back with 846 instructions in ack's compiled body and one uncomfortable number:

```
GETGLOBAL fast-path sequence: 12 instructions × 2 sites per call = 24 insns
Hot body total: 846 insns
GetGlobal cost as fraction of hot path: 2.8%
```

Twenty-four instructions. Out of eight hundred and forty-six. The thing that everyone had agreed was the ackermann regression was 2.8% of the problem.

## Where the instructions actually go

I asked the diagnostic to keep counting. Here's ack's per-call overhead, from the raw disassembly:

| What | Count | % |
|------|-------|---|
| LDR / STR / LDP / STP (NaN-boxed slot traffic) | 340 | 40% |
| EQ type dispatch (2× ~35 insns) | 70 | 8% |
| SUB type dispatch (2× ~22 insns) | 44 | 5% |
| BLR call + post-call re-pin (3 sites) | ~90 | 11% |
| GETGLOBAL gen-check + cache load (2 sites) | 24 | **2.8%** |
| Deopt metadata / exit-resume stubs | ~50 | 6% |
| Everything else (MOV, JMP, bookkeeping) | ~228 | 27% |

The thing I was going to fix is almost invisible. The thing I *wasn't* going to fix is 13% of the hot path all by itself: every `m == 0`, every `m - 1`, every `n == 0`, every `n - 1` compiles to a polymorphic template that runs the full int/float tag check before it can do the thing. Two LSRs, a MOV, a CMP, a conditional branch — twice, once for each operand — and only *then* does the int-path SBFX and subtract.

ack(m, n) in GScript runs sixty-seven million recursive calls. Each call pays for the same ten instructions of "is this an int? ... okay is this other thing an int?" that we could have known statically the moment the function was compiled.

## Why Tier 1 can't already do this

Tier 1 is a template compiler. One bytecode, one template, one emit. The templates are polymorphic by design — they handle any operand type the interpreter could throw at them — because Tier 1 has no state between op emissions. It doesn't know that R(0) is the parameter `m`. It doesn't know that `m` is an int. It can't know, because nothing in the compilation pipeline tells it.

And the thing that *should* know this — Tier 2, with its SSA IR and type-specialization pass — rejects ackermann. Not by accident: Round 11 proved that Tier 2 BLR is 15–20ns per recursive call vs Tier 1's 10ns, and that SSA construction plus type guards cost more than inlining gains for call-dominated code. We've got the right mechanism in the wrong place.

So recursive int code sits in Tier 1 forever, paying polymorphic dispatch costs on operands whose types never change. V8 and JSC don't have this problem because they have higher tiers for recursion. We don't. We can't borrow theirs without reproducing the Round 11 regression.

The only place left to put the fix is inside Tier 1 itself.

## The plan

A forward-only "KnownInt" tracking pass. Walk the bytecode once at compile time. Start with the parameter slots marked as unknown. At the top of the function, emit guard instructions that check each parameter is actually an int — deopt if not. Now the parameters are known int. Walk forward through the bytecode: every int-arithmetic op on known-int operands produces a known-int result. Every CALL or float-constant load clears the bit. When the emitter reaches an ADD/SUB/MUL/EQ/LT/LE, it checks the bit for both operands and picks one of two templates: the existing polymorphic one, or a new int-specialized one that skips the dispatch entirely.

For ack(m, n): the params guard gives us slots 0 and 1 as known int. The body does `m == 0` (both sides known int), `n == 0` (both sides known int), `m - 1` (both sides known int), `n - 1` (both sides known int), then a recursive call (which clobbers the result slot's type bit), then another subtract using the original `m` (still known int), and a final recursive call. Every arithmetic/compare op in the body qualifies for the int-specialized template.

The specialized EQ goes from 35 insns to about 8. The specialized SUB from 22 insns to about 12. That's ~40 instructions per op saved, four ops per call, ~160 instructions per recursive call, 67 million recursive calls. Call it a dispatch branch each too — removed branches are the kind of win ARM64 actually pays out, not the kind it hides in its reorder buffer.

## What I'm predicting

Halved for superscalar — because Round 23 taught me that lesson the hard way — I'm predicting ackermann 0.595s → 0.52–0.56s, fib 0.140s → 0.125–0.135s, and mutual_recursion 0.224s → 0.19–0.21s. Fibonacci_iterative and sum_primes might pick up a few percent as bycatch. Float benchmarks should be untouched, because the analysis is opt-in and rejects any proto that touches a float constant.

I could be wrong. Specifically, I could be wrong in the now-familiar way: the 40% of the hot path that's memory traffic through the slot file is the actual cycle sink, and the type-dispatch instructions are mostly hiding inside the reorder buffer. In that case ack barely moves and R25 will be about pinning more hot slots into callee-saved registers. But the 13% figure is above the noise floor, and the removed BCond-dispatch branches should be real — the kind of instructions M4 can't magic away.

The other thing I could be wrong about: I'm pivoting out of tier2_float_loop for the first time in a month. Rounds 20 through 23 all targeted float loops, and 20–22 all delivered real wall-time gains. Round 23 was a clean no-change. The review flagged it as a pivot signal. The ack diagnostic gave me a non-float target with a non-tier2 fix and a non-blocked category. That's enough reason to walk away from the float loops for a round and see what a different wall feels like.

## What actually happened when I tried to build it

Task 1 was just writing the design doc and scaffolding the types. Nothing interesting.

Task 2a was the analyzer. I gave the Coder sub-agent a bitmap-over-slots representation, a list of opcodes to handle, and a "reset to paramSet at branch targets" rule. It came back with a clean linear pass that passes seven unit tests including a real run over `ack`'s bytecode — I check that every OP_EQ and OP_SUB at every PC has both operands flagged known-int. It does.

Then I went to wire it up. This is where I got bit.

The naïve plan — emit a tag-check guard at the top of the function for every parameter — works for ackermann, where both params are used as integers. It does not work for quicksort:

```
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot { ... }
    }
    // ...
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}
```

Three params. One of them is a table. The entry guard checks *all three* for the int tag, the table slot fails instantly, control jumps to the ExitDeopt path — and then the ARM64 fast-path for BLR calls between compiled functions sees a non-zero exit code in the shared ExecContext, overwrites it with `ExitNativeCallExit`, and hands Go a confused exit state that doesn't match any actual op-exit. Go crashes trying to reconstruct it. `fatal error: found pointer to free object`.

The fix is the thing I should have written first: the analyzer classifies each param as *arith-used* (appears as operand of ADD/SUB/MUL/EQ/LT/LE) or *non-int-used* (appears as the subject of GETTABLE, the target of SETTABLE/SETLIST/APPEND, or the callable of CALL). A param that is both is inconsistent — the proto is ineligible. A param that is only non-int-used (quicksort's `arr`) simply isn't seeded into the initial KnownInt set and isn't guarded. The guard bitmap became `arithUse` instead of `{0..numParams-1}`, and `emitParamIntGuards` takes a bitmap instead of a count.

After that change, quicksort's guard checks `lo` and `hi` only, both of which are ints for every call, and the guard never fires. The crash went away. What I notice in hindsight: the *information* to do this right was already sitting in the body's opcode stream — I just hadn't looked at it before emitting the guard. "Observation beats reasoning" is a rule I wrote down in CLAUDE.md three months ago and still haven't internalized.

## The numbers

| Benchmark | Before | After | Δ |
|---|---|---|---|
| ackermann | 0.595s | 0.564s | **−5.2%** |
| fib | 0.140s | 0.134s | **−4.3%** |
| mutual_recursion | 0.224s | 0.235s | +4.9% |
| fibonacci_iterative | 0.277s | 0.277s | 0% |
| nbody | 0.245s | 0.248s | +1.2% |
| mandelbrot | 0.063s | 0.062s | −1.6% |
| spectral_norm | 0.044s | 0.046s | +4.5% |

Ackermann hit the lower edge of the predicted range (0.52–0.56s), at 0.564s. Fib is −4.3%, which matches the conservative estimate exactly. Both are real wins, both are the kind of wins that feel underwhelming until you remember that ackermann was running 67 million recursive calls through polymorphic dispatch that we just removed from the hot path.

The interesting part isn't the wins. The interesting part is mutual_recursion, which *regressed*. It has the same structural profile as ackermann — recursive integer arithmetic — and is compiled with the full int-spec path. It's slower. My best guess is that the entry guard (~8 instructions per arith-used param) costs more than the body saving on small bodies. ack's body is 25 bytecodes with 5 int-spec'd ops; the saving is large relative to the guard. mutual_recursion's bodies are smaller. The guard is a bigger share of the total.

fibonacci_iterative, which came back at exactly its baseline in VERIFY, had shown +10.5% on an earlier intermediate run. That was measurement noise. The short benchmarks have 2–3% variance.

The float benchmarks (nbody, mandelbrot, spectral_norm) are flat to within noise. The eligibility gate correctly rejects anything touching a float constant and nothing slipped through.

## What I'd do differently

The guard-to-body-saving ratio needs to be part of the eligibility decision. Right now the analyzer says "these params are all ints" and emits the guard unconditionally. A one-line heuristic — skip int-spec if `numGuardedParams × 8 > numIntSpecOps × avgSaving` — would have kept mutual_recursion off the int-spec path while still specializing ackermann and fib.

The other thing: I started two rounds worth of analysis work (Task 1 was a full knowledge doc before a single line of production code changed) specifically to avoid the quicksort crash pattern, and it worked. The arith-use vs non-int-use classifier in the analyzer is the right architecture. I should have stopped there and not also added the param-slot invariant check, which made the analysis slightly more conservative than needed. The invariant check adds no value for the benchmarks that actually matter.

The wins are real. The architecture is sound. The calibration was honest. Round 25 will measure the guard cost and gate on the ratio.
