---
layout: default
title: "The Pause Was Not The Problem"
permalink: /71-the-pause-was-not-the-problem
---

# The Pause Was Not The Problem

*April 2026 - Beyond LuaJIT, Post #71*

Post #70 ended with a clean but unsatisfying shape:

```
the fast paths got smaller
the remaining gaps got more specific
```

That is a useful place to be in a compiler project. It is also a dangerous
place. Once the obvious polymorphic dispatch and boxed call-boundary costs are
gone, every remaining subsystem looks guilty. The call ABI looks guilty. The
register allocator looks guilty. The table representation looks guilty. And
for allocation-heavy benchmarks, Go's garbage collector looks very guilty.

The question this round was simple:

```
is Go GC the problem?
```

The answer is more precise than yes or no:

```
Go GC pauses are not the current wall.
Go heap allocation, table layout, and hidden-pointer root maintenance are.
```

That distinction matters because it changes the next optimization from
"twiddle GOGC" to "stop making every tiny script object look like a Go heap
object plus a root-log entry."

## The First Bad Hypothesis

The full guard after the latest round had one loud outlier:

```
binary_trees: VM 0.733s, Default JIT 0.763s
```

That is not a typo. The method JIT was slightly slower than the VM on the
benchmark that allocates and traverses millions of tiny `{left, right}` tables.

The tempting explanation is Go GC. `binary_trees` is a classic allocation and
collector benchmark. LuaJIT is good at it. GScript stores tables as Go objects,
wraps them in NaN-boxed values, and keeps hidden Go pointers alive through a
root log. If anything should expose Go GC as a problem, it is this benchmark.

So I ran the blunt test first:

```
GOGC=off
```

The result was not what the simple story predicted:

```
binary_trees       default=0.763s GOGC=off=0.778s
object_creation    default=0.003s GOGC=off=0.003s
closure_bench      default=0.026s GOGC=off=0.025s
ackermann          default=0.015s GOGC=off=0.015s
matmul             default=0.082s GOGC=off=0.083s
```

Turning off collection did not make `binary_trees` faster. It got slightly
slower on that sample.

That does not exonerate memory management. It only rejects the first bad
hypothesis: the benchmark is not mostly waiting on stop-the-world pauses.

## What The Collector Said

The `gctrace` run was also clarifying:

```
binary_trees time=0.767s gc_events=16
gc 1  @0.000s ... 8->10->9 MB
gc 2  @0.004s ... 18->21->21 MB
gc 3  @0.010s ... 38->40->40 MB
...
gc 14 @0.619s ... 97->102->101 MB
gc 15 @0.661s ... 195->206->203 MB
gc 16 @0.752s ... 392->393->133 MB
```

There are collections. They are real. But they do not add up to the whole
wall-clock gap. The pause times are not 700 milliseconds. The runtime is not
standing still while the collector stops the world.

Then the allocation profile made the real shape obvious:

```
Type: alloc_space

1529 MB  runtime.(*tableSlab).refill
 128 MB  runtime.gcCompact
  57 MB  methodjit.(*BaselineJITEngine).executeInner
  31 MB  runtime.ScanValueRoots
  20 MB  runtime.gcLogGrow

total: 1749 MB
```

And by object count:

```
Type: alloc_objects

3,735,605 objects  methodjit.(*BaselineJITEngine).executeInner
```

The table slab did what it was designed to do: amortize individual table
allocation over backing blocks. But the profile still says the workload is
mostly "make tables, publish table pointers, scan table roots, compact the
hidden-pointer log, repeat."

That is not a GC-pause problem. It is an allocation architecture problem.

## Why This Matters For LuaJIT

LuaJIT does not win `binary_trees` because it has a magic peephole for a
recursive tree function. It wins because the object model, allocator, write
barriers, collector, and machine code all belong to the same runtime.

GScript currently has a split personality:

```
the JIT wants raw machine-level values
the language runtime stores objects on the Go heap
NaN-boxed Values hide Go pointers from the collector
the root log makes those pointers visible again
gcCompact periodically reconstructs the live hidden-pointer set
```

That is a workable design. It is not a LuaJIT-class allocation design.

For numeric benchmarks, that split is tolerable. `ackermann`, `fib`, `nbody`,
`matmul`, and `spectral_norm` are not primarily waiting on Go GC. Their current
walls are call-boundary metadata, register pressure, bounds checks, table-array
headers, and boxed-slot traffic.

For object-heavy benchmarks, the split is the wall.

The JIT can save two instructions from a field load and still lose because the
program is allocating millions of Go-visible tables that also require hidden
pointer bookkeeping.

## The Raw Recursion Budget

The same round still moved the recursive numeric path forward.

Before this patch, a successful multi-argument raw self call still touched
`ctx.NativeCallDepth` on every recursive call:

```
load depth
compare max depth
increment depth
store depth
BL numeric self entry
load depth
decrement depth
store depth
```

That was conservative, but it was the wrong success-path cost for Ackermann.
Raw self recursion already moves through a VM register window. The native
callee frame has a known size. So multi-argument raw self recursion now uses a
per-entry cap:

```
ctx.RawSelfRegsEnd
```

The recursive call checks whether the next raw self frame would exceed that
cap. If it fits, the call proceeds without touching `NativeCallDepth`. If it
does not fit, the existing fallback path takes over.

This is not a benchmark-specific Ackermann trick. It is a better expression of
the raw self ABI:

```
native recursion is bounded by the register window it consumes
fallback remains boxed and resumable
one-argument recursion keeps the older depth path where it measured better
```

Focused numbers were small but real:

```
ackermann:       0.016s -> 0.015s
fib_recursive:   flat
mutual_recursion flat
```

Small wins are still worth taking when they remove the right kind of work.
They also keep the ABI honest: the fast path now says what it actually needs,
not what the old generic native-call path needed.

## The Loop Diagnostic That Points At Registers

Another patch did not speed anything up directly. It made the next speedups
less speculative.

The warm production dump now writes loop pressure diagnostics:

```
proto.loops.txt
manifest.json -> loop_diagnostics
```

The important word is still "production." This is the same compile triggered
by normal execution, not a cold diagnostic pipeline that might see different
feedback or pass ordering.

For each loop, the dump can now say:

```
loop header B3 blocks=[3 4]
  ops: int_arith=4 checked=4 float_add=2 float_mul=1 float_div=2
       table_array_load=1 bounds=1 settable=0 call=0 phis=3
  header clobbers:
    v30 Phi:int X21 clobbered_by=B4/v58 AddInt
  invariant reloads:
    v12 TableArrayData:any X23 used_by=B4/v40 TableArrayLoad
        clobbered_by=B4/v58 AddInt
```

That kind of output matters for `spectral_norm` and `matmul`. Those benchmarks
are no longer dominated by generic dispatch. The remaining cost is more like:

```
the loop header had the value in a register
the body reused that physical register
the use site reloaded from the VM frame
the table-array load repeated a bounds/header shape
```

Without a loop-pressure view, it is too easy to optimize the wrong thing. The
flat instruction histogram might say "fewer FDIVs" or "fewer branches." The
loop diagnostic says whether the hot loop is losing residency.

That is the difference between shaving code size and moving wall time.

## The Row Was Already A Table

`matmul` picked up one of those small, local wins.

The table-array header emitter had an unknown-value path and a known-table
path. The known-table path still extracted the pointer and then checked it for
nil:

```
extract pointer
CBZ pointer, deopt
check metatable
check array kind
```

But a `TypeTable` value reaching that point came from table-producing IR or a
guard. It was already not nil. The dynamic metatable and array-kind checks
still matter; the nil payload branch does not.

The new code keeps the real guards and removes the redundant branch:

```
extract pointer
check metatable
check array kind
```

Focused guard:

```
matmul:             0.083s
table_array_access: 0.029s
spectral_norm:      0.022s
nbody:              0.062s
```

This is not the matmul solution. It is a symptom of the next solution: once
rows are known tables, the compiler should stop rediscovering that fact inside
the inner loop.

## The Sort Exit That Stopped Repeating

`sort` also got a structural cleanup.

When a typed table array demoted back to mixed storage, it used to allocate the
mixed array with length as capacity. That threw away the preallocation hint
that Tier 2 had preserved for the typed representation.

The demotion path now preserves typed capacity:

```
intArray cap   -> mixed array cap
floatArray cap -> mixed array cap
boolArray cap  -> mixed array cap
```

The same patch added a Tier 1 float-float comparison fast path for the generic
`LT`/`LE` fallback case where both operands are already unboxed floats.

The wall-time change was small:

```
sort: 0.037s
```

But the exit count changed in the right direction:

```
sort exits: 46 -> 4
```

That matters because sort is still not a clean Tier 2 recursive table-mutation
benchmark. Reducing fallback churn is not the final optimization, but it makes
the next failure mode easier to isolate.

## The Full Guard

After the round:

```
fib:                 0.088s
fib_recursive:       0.596s
sieve:               0.028s
mandelbrot:          0.053s
ackermann:           0.015s
matmul:              0.085s
spectral_norm:       0.023s
nbody:               0.061s
fannkuch:            0.041s
sort:                0.036s
sum_primes:          0.003s
mutual_recursion:    0.017s
method_dispatch:     0.001s
closure_bench:       0.025s
string_bench:        0.022s
binary_trees:        0.763s
table_field_access:  0.022s
table_array_access:  0.031s
coroutine_bench:     0.032s
fibonacci_iterative: 0.027s
math_intensive:      0.055s
object_creation:     0.004s
regressions:         0
```

The important line is not the fastest one. It is this:

```
binary_trees: VM 0.733s, Default JIT 0.763s
```

That tells us where machine-code-only optimization stops being enough.

## The Next Shape

The next object/allocation work should be runtime architecture, not another
tiny field-load peephole:

```
table arena ownership per VM or per execution epoch
bulk reset or generational reclamation for short-lived script tables
root logging at slab or arena granularity instead of per TableValue
JIT-visible small-object allocation for fixed-shape literals
shape/svals allocation as one object where possible
precise root maps for native frames so gcCompact scans less stale state
```

This does not mean replacing Go's GC tomorrow. It means not making Go's GC
recover information the language runtime already knows.

LuaJIT's advantage here is not just faster generated code. It is that
allocation, object layout, barriers, and traces are part of one machine. A
method JIT can compete with that, but only if its object model stops crossing
the Go runtime boundary for every tiny object on the hot path.

The pause was not the problem.

The object lifetime contract was.
