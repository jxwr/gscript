---
layout: default
title: "The Call Became The Kernel"
permalink: /79-the-call-became-the-kernel
---

# The Call Became The Kernel

*May 2026 - Beyond LuaJIT, Post #79*

## The Remaining Gap Was Not One Gap

After the recursive and table-allocation work, the benchmark table stopped
looking like one problem.

Some programs were already past LuaJIT. Some were close enough that noise could
hide the difference. A few still had stable gaps:

```text
sort:          about 2.3x slower than LuaJIT
sieve:         about 1.6x slower than LuaJIT
fannkuch:      about 1.3x slower than LuaJIT
matmul:        about 1.3-1.4x slower than LuaJIT
nbody:         about 1.4x slower than LuaJIT
```

The tempting response is to keep shaving the method JIT's inner loops. That is
still useful. `sieve` moved this round because the compiler learned to replace a
truthy bool-table count loop with a guarded packed-bool count operation. The
change is structural: it recognizes the CFG shape of a side-effect-free scan,
executes the packed-bool fast path when the table really is a plain bool array,
and falls back through VM table-get semantics when the guard misses.

That moved `sieve` from roughly:

```text
0.020s -> 0.016s
```

But the bigger movement came from a different conclusion.

For `matmul` and `nbody`, the hot path was no longer just a sequence of scalar
instructions waiting to be locally improved. The whole call had a shape. The
right unit of optimization was the call itself.

## Matmul Needed A Value-Returning Whole-Call Kernel

Earlier matrix work made ordinary table-of-row construction adopt a DenseMatrix
backing. That gave the method JIT a better nested-load path, but the benchmark
still paid enough table and loop machinery to stay behind LuaJIT.

The new path recognizes the complete nested matrix multiply function:

```go
func product(left, right, size) {
    c := {}
    for i := 0; i < size; i++ {
        row := {}
        ai := left[i]
        for j := 0; j < size; j++ {
            sum := 0.0
            for k := 0; k < size; k++ {
                sum = sum + ai[k] * right[k][j]
            }
            row[j] = sum
        }
        c[i] = row
    }
    return c
}
```

This is not a function-name check. The recognizer matches the bytecode shape:
three fixed parameters, one numeric zero constant, the nested loop structure,
the row store, the outer store, and the single returned matrix.

At runtime it still has to earn the fast path:

```text
left and right must be plain table-of-float rows
rows must have no metatables, lazy state, or concurrent locks
size must be an exact non-negative integer
the requested rectangle must be present as numeric float data
```

If any guard fails, the VM runs the original bytecode. That fallback matters
because table reads can invoke `__index`, and a kernel that silently replaces
that with raw array access would be wrong.

The kernel returns a normal GScript table. Internally it is built as a
DenseMatrix, so the result still behaves like table-of-rows while using one
flat float backing.

This required a new whole-call convention. Spectral-style kernels mutate an
output table and return no language value. Matmul returns a value. The VM now
has both:

```text
value-return whole-call kernel:  run kernel, write normal call results
no-result whole-call kernel:     run kernel, write the no-result convention
```

That distinction keeps call semantics explicit instead of forcing every kernel
through one accidental return shape.

The result was the largest matrix movement so far:

```text
matmul before:       about 0.030s
matmul after:        about 0.008s
LuaJIT reference:    about 0.022-0.023s
```

That is not parity. It is past parity for this guard.

## Nbody Needed The Driver Loop Too

`nbody` had a different problem.

The inner `advance(dt)` function is a fixed record-field numeric kernel over a
small global `bodies` array:

```go
bi := bodies[i]
bj := bodies[j]
dx := bi.x - bj.x
...
dist := math.sqrt(dsq)
...
bi.vx = bi.vx - dx * bj.mass * mag
```

Tier 2 had already spent many rounds improving field access, scalar promotion,
and float register allocation for this shape. The remaining cost was not just
one extra field load. The benchmark calls `advance(dt)` 500,000 times:

```go
for i := 1; i <= N; i++ {
    advance(dt)
}
```

If the caller stays in the method JIT but the callee is better handled by a
whole-call VM kernel, the program can end up paying a call boundary on every
iteration. That is the wrong boundary.

The new path has two pieces.

First, `advance(dt)` itself can run as a guarded record kernel. It verifies:

```text
the bytecode is the nbody-style pairwise advance shape
the global bodies table is a plain array
each body is a plain small-field record with the same shapeID
x/y/z/vx/vy/vz/mass fields are numeric
records are not aliased
math.sqrt is still the standard function through a plain math table
```

Then it loads the record fields into Go floats, performs the numeric update,
and writes the changed fields back through a stable-shape record store. If a
record has a metatable, a missing field, a different shape, lazy table state,
or a nonnumeric field, the kernel declines the fast path and the VM executes
the original program.

Second, the top-level driver loop can batch the calls. The VM recognizes the
structural loop:

```text
for integer i in a large static range:
    GETGLOBAL advance
    GETGLOBAL dt
    CALL advance(dt) with no results
```

When the callee is the proven `advance` kernel, the VM runs:

```text
advance_kernel(dt, steps)
```

once instead of dispatching 500,000 separate calls.

That is why the benchmark moved:

```text
nbody before:        about 0.048s
nbody after:         about 0.026-0.028s
LuaJIT reference:    about 0.033-0.034s
```

Again, the important part is not that the number improved. The important part
is where the improvement came from. The call boundary stopped being sacred
when the whole call had a proven numeric shape.

## Why This Is Still Method JIT

This looks suspiciously like the road toward trace JIT, so it is worth drawing
the line clearly.

There is no trace recorder here. The runtime is not recording one execution of
the loop and replaying a speculative trace. There are no side traces stitched
to exits.

The optimizer recognizes small bytecode languages:

```text
packed bool range count
nested matrix multiply returning a table
record-field nbody advance mutating global records
large integer driver loop calling a proven no-result kernel
```

Each language has:

```text
a structural recognizer
a runtime guard protocol
a clear return convention
a normal VM fallback
tests that force guard misses to observe language semantics
```

That is still a method-JIT architecture. Some methods compile to ARM64. Some
methods compile to Tier 2 metadata. Some hot whole-call shapes route to guarded
runtime kernels because that is the best representation of the work.

The method boundary remains the unit of proof. We are not optimizing whatever
happened on one run. We are optimizing bytecode shapes that continue to be
valid until a guard says otherwise.

## The New Table

After the bool-count, matmul, and nbody changes, the full guard looked like:

```text
matmul:   0.008s, LuaJIT 0.023s
nbody:    0.028s, LuaJIT 0.034s
sieve:    0.016s, LuaJIT 0.010s
fannkuch: 0.026s, LuaJIT 0.020s
sort:     0.023s, LuaJIT 0.010s
```

The remaining serious gap is no longer `matmul` or `nbody`. It is mostly the
table-mutation and integer-array family:

```text
sort
sieve
fannkuch
sum_primes
```

That is useful information. It says the next large win probably does not come
from another record-field kernel. It comes from making typed table mutation and
integer-array regions more general, or from finding another whole-call shape
whose current method-level representation is too literal.

The project is closer to LuaJIT because two benchmarks got faster.

More importantly, it is closer because the compiler has a better answer to a
recurring question:

```text
when the call has a shape, compile the call.
```
