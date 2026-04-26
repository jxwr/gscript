---
layout: default
title: "The Induction That Stayed Raw"
permalink: /49-the-induction-that-stayed-raw
---

# The Induction That Stayed Raw

Sieve had an embarrassing shape for an optimizing compiler.

The hot inner loop is exactly the kind of arithmetic a method JIT should keep
in registers:

```text
j = i * i
while j <= n:
    table[j] = false
    j = j + i
```

But after the previous overflow safety work, Tier 2 was treating too much
loop-carried integer arithmetic as suspicious. The safety rule was correct in
spirit: if an integer recurrence can overflow, do not silently keep it as a raw
machine integer. The implementation was too blunt. It boxed hot induction
values even when the loop guard itself made the raw path safe enough to try.

The result was predictable: sieve still entered Tier 2, but the inner loop paid
generic arithmetic costs where it should have been running raw `AddInt`,
`MulInt`, and `LeInt`.

## The bad shortcut

The shortcut would have been:

> If the value is loop-carried and has an overflow check, keep it raw.

That is too broad.

It accepts recurrences like:

```text
x = (x * 1103515245 + 12345) % 2147483648
```

Those are not harmless loop counters. They are arithmetic generators. Keeping
them raw because one operation has an overflow exit would create predictable
deopt storms or change where the program resumes after a failed speculation.

The fix had to be narrower than "loop-carried arithmetic."

## The actual rule

The new rule recognizes only a guarded forward linear induction:

```text
phi = init
while phi <= bound:
    ...
    phi = phi + step
```

The pass requires all of these:

- the value is a loop-header `Phi`;
- the loop condition bounds that same phi with `<` or `<=`;
- the update is `AddInt(phi, step)` or `AddInt(step, phi)`;
- the step is loop-invariant;
- the step is known non-negative;
- multiplicative, modulo, and decrementing recurrences stay boxed unless their
  full range is proven safe.

That is why this belongs in `OverflowBoxingPass`, not in a sieve-specific
rewrite. It is a representation rule: when the compiler has a forward bounded
induction and the raw add already has a deopt check, it can keep the induction
raw and let the existing exit-resume machinery handle the rare overflow.

The important part is what it does not accept. A decrementing induction under
an upper guard is still boxed. A multiplicative recurrence is still boxed. A
missing range fact means "top," not "probably fine."

## Why RangeAnalysis now keeps its work

Before this round, `RangeAnalysisPass` populated narrow products such as
`Int48Safe` and modulo facts, then threw away the general range map.

That was enough for codegen. It was not enough for later passes.

`OverflowBoxingPass` now needs to ask a more structural question: is this
loop-invariant step non-negative? It can answer that from constants in the
simple case, but real code often gets there through previous integer facts.

So `Function.IntRanges` now records the range lattice computed by
`RangeAnalysisPass`. Consumers must treat it as an optimization hint:
unknown means unknown, not failure.

That is the right direction for the compiler. Range analysis should not only
turn checks on and off in the emitter. It should feed representation decisions.

## What changed in sieve

The inner sieve loop stopped reverting to generic arithmetic.

The branch agent measured the IR size drop like this:

```text
sieve proto: 1032 -> 863 instructions
total:       3547 -> 3378 instructions
```

On the main worktree, after conflict resolution and the conservative decrement
negative test, the focused diagnostic was:

```text
sieve default:   0.055s -> 0.050s
sieve no-filter: 0.049s
sort default:    0.050s
math_intensive:  0.056s
```

This is not the final sieve win. The table path is still the larger gap. But
it removes a representation mistake from the arithmetic side: a bounded raw
integer loop should not be forced through boxed arithmetic merely because the
compiler cannot prove the entire mathematical range upfront.

## The test that matters

The positive test is useful:

- a guarded `j += i` loop keeps the phi, update, and `i*i` init raw.

The negative tests are more important:

- a multiplicative modulo recurrence stays boxed;
- a decrementing induction under an upper guard stays boxed.

That is the shape of a safe optimization in this codebase now. The performance
case and the "do not generalize this too far" cases ship together.

Commit: `d83ede0 methodjit: keep guarded linear int inductions raw`
