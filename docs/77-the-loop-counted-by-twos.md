---
layout: default
title: "The Loop Counted By Twos"
permalink: /77-the-loop-counted-by-twos
---

# The Loop Counted By Twos

*April 2026 - Beyond LuaJIT, Post #77*

## Matmul Was No Longer A Table Problem

The earlier matrix work changed the shape of `matmul`.

Before that work, the benchmark spent most of its time walking ordinary
table-of-row objects:

```
m[i][j]
```

The method JIT had to prove the outer table, load the row, prove the row, load
the inner element, and repeat that pattern through the innermost loop.

Post #73 moved ordinary sequential float rows into a DenseMatrix backing. A
nested table load could become:

```
flat[i * stride + j]
```

Later, dense matrix address calculation learned to use ARM64 `MADD`, so the
address was no longer emitted as:

```
MUL
ADD
```

At that point, the remaining gap was not table indirection. It was loop shape.

The current focused guard before this round was roughly:

```
matmul default JIT: 0.037s
LuaJIT:             0.022s
```

The matrix path was already native and typed. The question was whether the
method JIT could make the loop itself cheaper without turning into a trace JIT.

## More FMA Was Not The Answer

One tempting answer was to fuse more floating-point operations.

That had already helped in narrow places. `FloatStrengthReduction` can rewrite
some exact divisions by powers of two into multiplications, and a second FMA
fusion pass can then see fresh:

```
MulFloat + AddFloat
```

patterns.

But the larger experiment was negative. Trying to force phi-accumulator FMA
fusion in `spectral_norm` made the benchmark slower. That result matters:
fusing an operation is not free when it lengthens a loop-carried dependency
chain.

For a reduction like:

```
sum = sum + a[k] * b[k]
```

the critical path is the carried `sum`, not only the multiply and add. If every
iteration waits on the previous iteration's accumulator, a prettier instruction
sequence can still serialize the loop.

The next useful transform had to reduce hot back-edge traffic, not just replace
one arithmetic spelling with another.

## A Narrow Unroll, Not A Trace

The new pass keeps the historical name:

```
UnrollAndJamPass
```

But the implementation is deliberately narrower than a general unroll-and-jam
optimizer. It recognizes a single-block float reduction loop:

```
acc = phi(initial, updated)
i   = phi(start, i + step)

updated = acc + value(i) * other(i)
branch i <= limit
```

The transform clones the body once and advances the induction variable by two
steps in the hot loop.

Conceptually:

```
while i + step <= limit:
    acc = acc + f(i)
    acc = acc + f(i + step)
    i = i + 2 * step

if i <= limit:
    acc = acc + f(i)
```

The final `if` is the scalar tail for odd trip counts.

This is still method JIT compilation. The method has one optimized CFG. There
is no runtime trace recorder, no side exit chain describing one observed path,
and no benchmark-name special case. The pass is just a conservative CFG rewrite
inside Tier 2.

## The Safety Contract

The pass rejects most loops.

It currently requires:

```
one loop header
one body block
one float accumulator phi
one integer induction phi
positive integer step
one simple <= loop bound
no calls
no stores
no exit phis
no extra phis
no accumulator uses outside the body and exit
```

The body may contain cloneable numeric and already-lowered load operations,
including matrix loads:

```
MatrixLoadFAt
TableArrayLoad
TableArrayNestedLoad
floating arithmetic
integer induction arithmetic
guards
```

It rejects table mutation and calls because cloning them would duplicate
observable side effects. That is why the pass helps `matmul` but does not try
to rewrite store-heavy `nbody` or `sort` loops.

The pass runs late:

```
MatrixLower
LoadElimination
DCE
FMAFusion
FloatStrengthReduction
FMAFusion again
LICM
LoadElimination
DCE
UnrollAndJam
```

That order is intentional. By the time unroll runs, matrix flat pointers,
strides, and other loop-invariant facts have already been hoisted or CSE'd.
The clone is therefore small. It duplicates the real per-element work, not all
of the proof machinery that made the load legal.

## Preserving Reduction Semantics

The pass does not split the accumulator into two independent sums.

That would be faster for some floating-point programs, but it changes
rounding order:

```
(a + b) + c
```

is not always bit-identical to:

```
a + (b + c)
```

The current transform preserves the original left-to-right order inside the
unrolled body:

```
acc = acc + f(i)
acc = acc + f(i + step)
```

For odd trip counts, the scalar tail performs the final original iteration.

This is a conservative choice. It gives less instruction-level parallelism
than a dual-accumulator reduction, but it makes the optimization much easier to
reason about and keeps it suitable as a default Tier 2 pass.

## Why It Helps Matmul

After DenseMatrix lowering, the inner `k` loop in matrix multiplication is a
classic reduction:

```
sum = sum + a[i][k] * b[k][j]
```

The table work has already been reduced to matrix primitive loads. The loop is
single-block, side-effect-free until the final store, and the induction variable
is simple.

That is exactly the shape this pass accepts.

The transformed hot loop now does two multiply-add contributions per back edge.
It still carries one accumulator, but it cuts branch and induction overhead and
lets the already-hoisted matrix facts feed two element loads per trip.

## Results

The local focused guard after merging the pass:

```
Benchmark          Default JIT   LuaJIT   JIT/LuaJIT
----------------------------------------------------
matmul             0.030s        0.022s   1.36x
spectral_norm      0.016s        0.008s   2.00x
nbody              0.048s        0.034s   1.41x
math_intensive     0.053s        missing  -
mandelbrot         0.050s        0.058s   0.86x
```

Before the unroll pass, current-main runs were around:

```
matmul default JIT: 0.037s
```

So the merged result is roughly:

```
0.037s -> 0.030s
about 19% faster
```

The worker's isolated median on the same current main reported:

```
0.037s -> 0.032s
```

The local post-merge run was a little better. The important point is not the
last millisecond digit; it is that this is a loop-shape win that survived
rebasing onto the DenseMatrix and `MADD` changes.

Full tests also passed:

```
go test ./... -short -count=1 -timeout=300s
go test ./... -count=1 -timeout=240s
```

## What Did Not Move

This pass does not solve `spectral_norm`.

That benchmark still has a larger LuaJIT gap, but the negative phi-FMA
experiment suggests it needs a different transform. The likely work is a
multi-reduction or scheduling pass that avoids serializing accumulator phis,
not a blind attempt to emit more fused instructions.

It also does not solve `sort` or `fannkuch`. Those are table-array mutation
problems. The promising direction there is still loop-level table-array
versioning:

```
guard kind/len/backing pointer once
reuse raw backing pointer in the loop
store through typed array facts
fall back before mutation if a guard fails
```

The current unroll pass intentionally rejects stores, so it leaves that work to
a different protocol.

## The Larger Pattern

The big wins in this compiler have not come from pretending every benchmark is
the same.

They came from finding the right contract for the hot shape:

```
fib:             fixed integer recurrence
ackermann:       nested recurrence stack
binary_trees:    fixed recursive table builder/fold
matmul:          dense matrix backing + counted reduction unroll
```

This round adds one more contract: a method can keep its method-JIT identity
and still get a loop-level rewrite when the loop is simple enough to prove.

That is the useful middle ground between a boxed VM call path and a trace JIT.
