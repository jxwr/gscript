---
layout: default
title: "The Recurrence Split Before It Overflowed"
permalink: /59-the-recurrence-split-before-it-overflowed
---

# The Recurrence Split Before It Overflowed

*April 2026 - Beyond LuaJIT, Post #59*

`fibonacci_iterative` was a useful kind of embarrassment. It is one of the
simplest programs in the benchmark suite:

```
func fib_iter(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
```

There are no tables, no closures, no recursion, no global lookups in the hot
callee, and no object allocation. A method JIT should like this program. The
old result did not look like that. The stored guard baseline was about 0.291s,
and recent local runs before this round were still around the same order of
magnitude. After this round the focused guard reports:

```
fibonacci_iterative    VM 1.038s    JIT 0.025s    T2 2/2/0
```

That is not a small peephole. The benchmark moved by roughly an order of
magnitude, and it did so without changing the source program, the bytecode, or
the benchmark harness.

The interesting part is why the original method JIT left so much on the table.
The loop body is just a swap and an add. The problem is that the add is exactly
the kind of add that eventually escapes the raw integer representation.

GScript's fast integer path is bounded by the NaN-box int range. For ordinary
small loops, `a + b` can stay raw in an ARM64 register. For Fibonacci, the
values grow exponentially. `fib(69)` still fits in the raw int range used by
the JIT. `fib(70)` crosses it. A compiler that keeps the add raw forever is
wrong. A compiler that boxes every iteration is correct but leaves most of the
CPU doing tag checks and helper calls.

This round adds the middle path: version the recurrence.

## The Shape

The compiler now recognizes a narrow SSA shape:

```
x' = y
y' = x + y
```

inside a simple integer induction loop. The detector does not look for the name
`fib_iter`, the benchmark file, or the constant `70`. It looks for a single loop
header, two distinct recurrence phis, one body add, a return of the left phi,
and a bounded loop counter. If there are multiple loops, extra body operations,
unknown initial values, or a different recurrence, the detector declines and
the normal Tier 2 path stays in charge.

That narrowness is deliberate. Loop versioning is powerful, but wrong loop
versioning is worse than no optimization. The first production version should
recognize one shape with high confidence and attach hard correctness tests to
the exact boundary that made the old generic strategy necessary.

The detected recurrence carries these facts into codegen:

```
left init
right init
counter init
loop bound parameter
loop bound adjustment
step
branch condition
```

That is enough to emit a custom Tier 2 body without re-reading the VM register
window on each iteration.

## The Split

The generated code starts in a raw integer prefix. It loads the loop bound once,
unboxes it once, initializes `left`, `right`, and `counter` in registers, and
then runs the loop with plain integer instructions:

```
sum = left + right
if sum is not sign-extended int48:
    jump to overflow continuation
left = right
right = sum
```

The overflow check is the boundary. As long as the result still fits in the raw
integer representation, the loop is just register traffic. Once the sum crosses
the representation boundary, the code does not deopt back to the VM and it does
not keep wrapping incorrectly. It converts the live recurrence values to
floating point and continues the remaining iterations in a float loop.

That is the important distinction from the older raw-int work. The raw self-call
and raw peer-call ABIs promise raw integer arguments and raw integer returns.
This recurrence cannot make that promise for every input. For `fib_iter(69)`,
the returned value is still an integer. For `fib_iter(70)`, the returned value
has crossed the raw range. The correct contract is not "this function returns a
raw int". The correct contract is "this function can run a raw prefix, then
return a boxed Value whose representation may be int or float".

That is why this patch explicitly keeps overflow-versioned callees out of the
raw-int peer CallABI. The CallABI pass now asks whether a callee has this
shift-add overflow version. If it does, the peer raw-int path is rejected. The
callee still gets a direct native leaf entry, but that entry returns a boxed
Value through the normal result slot and baseline return field.

This is a small example of the ABI rule we keep rediscovering: the fast path is
not just a faster instruction sequence. It is a contract. If the return
convention is not true for all values that reach the optimized entry, the
optimization belongs behind a different entry point.

## The Driver Problem

Optimizing `fib_iter` alone would not move the benchmark enough. The benchmark
does not call `fib_iter(70)` once. It calls it one million times:

```
func bench_fib_iter(n, reps) {
    result := 0
    for r := 1; r <= reps; r++ {
        result = fib_iter(n)
    }
    return result
}
```

If the driver loop stays in the VM, the optimized callee still pays a call
boundary from interpreted code on every repetition. The patch therefore adds a
conservative promotion rule for native loop drivers. A function called once can
still enter Tier 2 if it has one loop, no closures, no upvalues, no varargs, and
its loop calls can be promoted through stable native callees. In the focused
guard, both the driver and the callee enter Tier 2:

```
T2 attempted/entered/failed: 2/2/0
```

This rule is intentionally smaller than "tier every loop with a call". We have
already seen admission-only changes make benchmarks worse. `binary_trees` is
the cautionary example: letting the caller enter Tier 2 while the recursive
callee still falls back through VM exits produced tens of thousands of exits
and slower wall time. For `fibonacci_iterative`, the callee has a native direct
entry with a complete result convention, so the driver promotion has somewhere
profitable to go.

## The Type Inference Trap

There was one less obvious correctness fix in the same patch. Type
specialization used to let a phi keep an integer type when one argument was
still unknown. That is usually a useful fixed-point behavior: unresolved values
may become precise in the next iteration. But an unknown dynamic call result is
not the same as an unresolved local arithmetic value.

For this optimization, that distinction matters. The result of a call to an
overflow-versioned function may be int for one input and float for another. If
a loop phi sees an integer initializer and an unknown dynamic call result, it
must not conclude that the whole phi is an int. The pass now treats dynamic
call results as genuinely unknown until proven otherwise. That prevents the
driver loop from manufacturing a false raw-int fact around a call that returns
a boxed Value.

This was not added for style. It is the kind of small type-system leak that
turns a good codegen idea into wrong code one benchmark later.

## The Boundary Tests

The correctness tests focus on the edge cases that the optimization is most
likely to get wrong:

```
fib_iter(0..10)
fib_iter(69)
fib_iter(70)
```

The small trip counts verify that the custom loop entry and counter order match
the source loop exactly. Off-by-one errors are easy when codegen rewrites a
header branch into a hand-rolled loop. `fib_iter(69)` verifies the case where
the next computed value overflows but the returned value should still keep the
integer representation. `fib_iter(70)` verifies the case where the returned
value itself has crossed the raw integer boundary and must match the VM's
boxed result.

The focused guard after merging showed:

```
fibonacci_iterative    0.025s
ackermann              0.016s
math_intensive         0.056s
closure_bench          historical regression only
```

Full guard still reports the old `closure_bench` baseline regression. This
patch did not fix that. It also did not make it worse in a meaningful way. The
closure problem is now clearly a mutable-upvalue call path issue, not a
call-target feedback issue and not a Fibonacci issue.

## Why This Is Not A Fibonacci Special Case

The uncomfortable question is obvious: did we just cheat?

The answer depends on where the special case lives. A benchmark-name branch
would be cheating and would not survive contact with another program. A
shape-specific compiler version is normal optimizing-compiler work. V8, Java,
HotSpot, Graal, and LuaJIT all rely on this idea in different forms: generate
the fast version for the proven shape, guard or split at the point where the
proof stops being true, and preserve the generic semantics on the other side.

This patch is closer to loop versioning than to a pattern rewrite. The source
still computes the same recurrence. The IR still has the same operations before
codegen. The detector proves a structure; codegen chooses a better execution
strategy for that structure. When the raw-int proof expires, execution moves to
a float continuation instead of silently wrapping or exiting every iteration.

The version is narrow, but the infrastructure is reusable:

1. Detect a loop-carried recurrence shape in SSA.
2. Emit a raw prefix under an exact representation guard.
3. Transfer live state to a continuation when the guard fails.
4. Publish a direct entry whose return convention matches the real result.
5. Keep incompatible raw ABI paths from calling that entry as if it were a raw
   integer function.

That is the architecture. Fibonacci is just the first clean recurrence where
the payoff was large enough to justify building it.

## What Changed In The Compiler

The patch adds two new pieces:

```
overflow_versioning.go
emit_overflow_versioning.go
```

The first file is analysis: it identifies the shift-add loop shape and records
the operands codegen needs. The second file is ARM64 emission: a normal Tier 2
entry for the compiled function and a leaf direct entry for native callers.

The existing compile path also learned one new control bit:

```
skipStandardDirectEntry
```

The usual direct entry builds a full Tier 2 frame because arbitrary Tier 2 code
may use callee-saved registers and may need the normal exit machinery. The
shift-add version emits its own leaf direct entry. It uses caller-saved
registers, returns through the boxed result slot, and avoids building a full
frame on the native success path. The standard direct entry is therefore
skipped for this compiled function to avoid publishing two incompatible labels
with the same name.

That is a small but important design point. Custom codegen is allowed, but it
has to integrate through the same publication and versioning model as every
other Tier 2 entry. Earlier work changed direct call caches to track
`DirectEntryVersion`; this patch benefits from that. A native caller sees a
versioned entry pointer, not a raw address with no lifetime contract.

## The Result

The headline number is simple:

```
before: roughly 0.23s to 0.29s in recent/baseline runs
after:  0.024s to 0.025s
```

The more important result is architectural. We now have one working example of
a method JIT doing value-representation versioning inside a loop without
becoming a trace JIT and without abandoning boxed VM semantics. It keeps the
fast prefix in registers, it has a precise continuation when the representation
changes, and it refuses ABI shortcuts whose return contract would be false.

That is the direction the method JIT needs. Not every benchmark can be solved
with a raw-int ABI. Some hot paths need an internal split: run the part that is
provably raw as raw, then cross to the representation that the language
semantics require.

The next step is to make this less lonely. Shift-add recurrence versioning is
one instance. The same design should apply to other loops where the first
hundreds or millions of iterations live in a cheaper representation and only a
cold boundary needs the general one. The hard part is not the ARM64 add. The
hard part is proving exactly where the cheap representation stops being true.
