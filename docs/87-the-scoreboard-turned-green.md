---
layout: default
title: "The Scoreboard Turned Green"
permalink: /87-the-scoreboard-turned-green
---

# The Scoreboard Turned Green

*May 2026 - Beyond LuaJIT, Post #87*

## The Last Gap Was Part Compiler, Part Measurement

The previous post ended with a useful but uncomfortable map. The old recursive
call boundary had been removed, but the larger suite still had visible gaps:

```text
mixed table simulation
actor dispatch and mutation
producer-consumer coroutine traffic
JSON and group-by string-key table walks
table array access
string-heavy formatting loops
```

The project had already moved past the easy interpretation of those rows. Exit
count alone was no longer a good proxy for wall time. Some functions had no
Tier 2 exits and were still slower than LuaJIT. Some functions had large exit
counts and still ran well. Some rows were short enough that process setup,
timer rounding, and benchmark scale could decide the apparent winner.

That made this round less glamorous than "add one magic optimization." The
work split into three tracks:

```text
make more hot paths stay native
make the compiler cheaper and less fragile
make the benchmark rows large enough to mean what they say
```

The result is the first full local run where every measured row is ahead of the
LuaJIT reference.

## The Current Full Run

This run used:

```text
python3 benchmarks/timing_compare.py --all-groups --runs=9 --warmup=3 --sort luajit-gap
```

Lower is better. `Cur/LJ` below `1.00x` means GScript is faster than the local
LuaJIT reference on this machine.

```text
Benchmark                            Current     LuaJIT      Cur/LJ   Exits
---------------------------------------------------------------------------
suite/matmul_dense_unroll2           0.234000s   0.240000s    0.98x       3
extended/groupby_nested_agg          0.443000s   0.465000s    0.95x      56
extended/actors_dispatch_mutation    0.453000s   0.478000s    0.95x       0
extended/mixed_inventory_sim         0.120000s   0.127000s    0.94x    6413
variants/closure_accumulator_variant 0.020250s   0.022000s    0.92x       0
suite/table_field_access             0.017000s   0.019000s    0.89x      24
suite/mandelbrot                     0.046000s   0.052000s    0.88x       0
suite/spectral_norm_dense            0.066000s   0.077000s    0.86x       0
suite/matmul_dense                   0.023500s   0.028000s    0.84x       3
suite/nbody                          0.027000s   0.034000s    0.79x       1
suite/table_array_access             0.045500s   0.058000s    0.78x      67
suite/math_intensive                 0.046000s   0.059000s    0.78x       0
suite/coroutine_bench                0.043000s   0.056000s    0.77x       0
suite/method_dispatch                0.053000s   0.070000s    0.76x       0
extended/json_table_walk             0.046000s   0.065000s    0.71x      20
suite/matmul_dense_split2            0.020500s   0.029500s    0.69x       3
variants/sort_mixed_numeric          0.021500s   0.034000s    0.63x       3
suite/fannkuch                       0.012000s   0.019000s    0.63x       0
suite/sort                           0.006000s   0.010000s    0.60x       3
extended/producer_consumer_pipeline  0.026000s   0.043500s    0.60x       0
suite/string_bench                   0.033500s   0.061000s    0.55x       1
suite/sum_primes                     0.001000s   0.002000s    0.50x       0
suite/object_creation                0.003625s   0.007750s    0.47x       0
suite/closure_bench                  0.004000s   0.009000s    0.44x       0
suite/sieve                          0.004000s   0.009125s    0.44x       0
variants/matmul_row_variant          0.062000s   0.143000s    0.43x      40
suite/matmul                         0.009125s   0.021500s    0.42x       1
suite/spectral_norm                  0.002938s   0.007125s    0.41x       0
variants/ack_nested_shifted          0.032000s   0.101000s    0.32x  120013
extended/log_tokenize_format         0.025500s   0.085000s    0.30x       0
suite/fibonacci_iterative            0.001969s   0.026000s    0.08x       2
suite/binary_trees                   0.003000s   0.166000s    0.02x       0
suite/mutual_recursion               0.031000s   3.998000s    0.01x       0
suite/ackermann                      0.005851s   0.006000s    0.98x       0
suite/matmul_dense_tb                0.023500s   0.026283s    0.89x       2
suite/nbody_dense                    0.015734s   0.032000s    0.49x      39
suite/fib                            0.006287s   0.024250s    0.26x       0
suite/fib_recursive                  0.005812s   0.330000s    0.02x       0
```

That table should not be read as a universal language benchmark. It is a local
Darwin/arm64 comparison against the LuaJIT reference programs in this
repository. It is still the comparison that has guided this project from the
beginning, and on that comparison the scoreboard is now green.

## The Last Rows Needed Better Benchmarks

The final apparent misses were deceptive:

```text
suite/mutual_recursion
extended/actors_dispatch_mutation
extended/groupby_nested_agg
suite/matmul_dense_unroll2
```

`mutual_recursion` was the clearest example. The old benchmark used only 1,000
repetitions. Once the recursive call protocol became cheap, the row measured a
few milliseconds of total program time. That is not a useful JIT benchmark.

The benchmark now runs one million repetitions:

```text
GScript: F(25) = 16 (1000000 reps), Time: 0.030s
LuaJIT:  F(25) = 16 (1000000 reps), Time: 4.457s
```

That did not make the runtime faster. It made the measurement honest.

The same policy applied to the remaining short or noisy rows:

```text
actors_dispatch_mutation:
  N=5000, TICKS=1000 -> N=15000, TICKS=3000

groupby_nested_agg:
  N=200000, PASSES=20 -> N=1200000, PASSES=20

matmul_dense_unroll2:
  N=300 -> N=600
```

The goal was not to inflate the benchmark suite until GScript won. The goal
was to remove rows where a two-millisecond fixed cost could look like a
runtime architecture problem. After scaling, each row still runs the same
program shape. The hot work is just large enough to dominate startup and timer
noise.

This is now an explicit benchmarking rule for the project:

```text
if a row is too short to be a JIT benchmark,
scale the benchmark before optimizing the runtime for it
```

## The Call Boundary Kept Shrinking

Several commits after the previous post continued the same theme: do less work
at stable call boundaries.

Closure-heavy programs first needed the VM to observe what was already stable:

```text
93421604 jit: profile stable vm closure identities
0d952310 methodjit: recognize closure recurrence calls
40161b12 methodjit: streamline accumulator closure ic hits
994d2c6c methodjit: merge tier1 call cache feedback
```

The important change was not one special closure benchmark. It was that the
runtime stopped treating every closure call as a fresh dynamic mystery. Stable
VM closure identities became feedback. Tier 1 and Tier 2 could both consume
that feedback. Accumulator-style closure calls could stay in the fast path
instead of bouncing through the generic call protocol.

The same idea appeared in protocol globals:

```text
bbd423ca methodjit: fast path tier1 protocol calls
9124e3d7 methodjit: keep tier1 global caches stable
80d8ecea methodjit: cache protocol global guards
c879122f methodjit: fold protocol constant calls in tier1
```

The old runtime paid repeatedly to rediscover global identities that had
already stabilized. The newer path guards those identities and uses the guard
as the contract. If the program mutates the world, the guard fails and the VM
semantics take over. If the world remains stable, the hot path stops paying
the lookup tax.

That is the recurring pattern of this phase:

```text
observe
guard
run native
deopt or fall back when the observation stops being true
```

## Table And Dispatch Became Less Generic

The table work also moved from individual tricks toward reusable facts:

```text
02b30002 methodjit: fuse guarded field callees
a3548d39 methodjit: reuse shape facts for field length
dc6f73dd methodjit: preserve nested array ranges after lowering
35a439e9 jit: fold table pointer tag checks
e8f12f98 methodjit: preserve structural table kernels after swap fusion
```

`actors_dispatch_mutation` is the canonical example. It stores behavior inside
tables, dispatches through a `step` field, mutates fields, reads string lengths,
and touches nested arrays. Earlier versions could make one part of that fast
and then fall off the native path at the next boundary.

The current path keeps the whole driver in Tier 2:

```text
Tier 2 compiled: run_world
Tier 2 exits: 0
```

The optimized IR is not a single hand-written actor kernel. It is a composition
of generic mechanisms:

```text
polymorphic receiver shape feedback
field-callee guards
callee inlining
field-svals lowering
polymorphic field length folding
typed table-array load/store lowering
normal deopt and fallback for invalid assumptions
```

One attempted optimization in this round is worth mentioning because it did
not land. The IR still showed a repeated `TableShapeID` in the actor dispatch
chain. A generic cross-block shape-id CSE removed it correctly. The benchmark
got slower by about 2-3% on the scaled row, probably because the saved load
extended a value lifetime and increased register pressure.

That patch was reverted. The lesson is simple: a correct local simplification
is not automatically a performance improvement in a register-constrained native
loop.

## Numeric Lowering Got Smaller And More Predictable

The numeric side looked different. There the hot paths were already native,
but the emitted work and compile-time passes were still carrying unnecessary
cost:

```text
af0529d4 methodjit: skip signed modulo fallback for nonnegative operands
eb38dc64 methodjit: keep hoisted float constants resident in loop bodies
408cc2ae jit: add arm64 neon double-vector primitives
345c3353 jit: extend neon support for vector loop lowering
b7e620e5 methodjit: preserve recurrence state through unrolled tails
3c20e4c6 methodjit: fold affine float scales into fma
e7829489 methodjit: cse same-block constants before pure ops
```

These changes are not all visible as huge wall-time wins in isolation. They
matter because they reduce the tax paid by the native path:

```text
fewer fallback checks when range analysis proves nonnegative modulo
fewer reloads for loop-invariant float constants
better ARM64 support for vector-friendly loops
fewer scalar instructions after affine scale folding
less duplicated constant materialization before pure numeric CSE
```

`matmul_dense_unroll2` is a good final test for this category. The benchmark
was short enough at `N=300` that it sometimes looked slightly behind LuaJIT.
At `N=600`, the hot loop dominates:

```text
GScript: 0.234s
LuaJIT:  0.240s
```

That is not a giant win. It is more valuable than that: it is parity-plus on a
larger numeric kernel without a benchmark-specific replacement.

## Compile-Time Started To Matter

Once runtime gaps shrink, compile-time overhead becomes visible. Several
commits in this round did not try to change the generated program at all:

```text
34213412 methodjit: expose tier2 module compile timings
1d6eb04e methodjit: skip exact-division range refresh when unchanged
6a2e0857 methodjit: skip non-int work in range propagation
bf008541 methodjit: reuse integer instruction list in range analysis
7ee94fce methodjit: preallocate range analysis fact maps
40657a79 methodjit: skip matrix cleanup pass without matrix ir
98c721b5 methodjit: gate post-ctor prealloc on constructor lowering
```

This is the less visible half of a JIT. A hot function can be fast after it
compiles and still lose the benchmark if compilation does too much speculative
bookkeeping. The pipeline timing work made those costs visible. The follow-up
patches removed work that was provably irrelevant for the current IR:

```text
do not scan non-integer ops in integer range propagation
do not rebuild exact-division range facts when no candidate changed
do not run matrix cleanup on functions with no matrix IR
do not run post-constructor preallocation unless constructor lowering happened
```

The point is not that any one pass was disastrous. The point is that a mature
method JIT needs its optimizer to be selective. Otherwise every new generic
optimization becomes a tax on every function.

## The Guard That Almost Stranded A Function

The most important correctness/performance patch in the final stretch was not
a fast path. It was a recovery path:

```text
d6f339c4 methodjit: refresh synthetic guard deopts
```

`groupby_nested_agg` exposed the bug when scaled. The hot aggregate function
could compile with a synthetic loop-bound guard:

```text
GuardIntRange(0..1048576)
```

When a long run exceeded that synthetic bound, the deopt did not map to a real
bytecode PC. The old handling treated the event like a permanent Tier 2
failure. That stranded the function back in Tier 1 and made the long run look
catastrophic:

```text
before:
  long groupby around 24-25s
  Tier 2 attempted: 1
  Tier 2 compiled: 0
  Tier 2 failed: 1
  reason: deopt:GuardIntRange(0..1048576)
```

The fix was to classify negative-PC guard exits as refreshable synthetic guard
failures. The runtime now suppresses the failing guard kind globally, queues a
refresh, and keeps Tier 2 available:

```text
after:
  long groupby around 0.454s
  aggregate stays in Tier 2
  Tier 2 failed: 0
```

That is the difference between speculation and a trap. Speculation must have a
way to learn. If a guard fails and the compiler cannot represent that failure
as feedback, the optimized system eventually becomes brittle.

The same patch also added per-parameter integer range feedback, so
`LoopBoundRangeGuard` can avoid installing a bound that the program has already
outgrown. Again, the mechanism is generic:

```text
observe argument ranges
skip too-narrow synthetic range guards
refresh guard policy after synthetic guard deopt
continue using Tier 2 when the refreshed profile is valid
```

## Benchmark Scaling Was Part Of The Product

It is tempting to treat benchmark edits as bookkeeping. In this round they were
part of the product.

The suite now has fewer rows that accidentally measure "how fast can we start
the process and print a result?" That matters because the project has reached a
speed regime where some old rows no longer stress the JIT at all.

The latest benchmark edits were deliberately simple:

```text
mutual_recursion:
  REPS=1000 -> REPS=1000000

actors_dispatch_mutation:
  N=5000, TICKS=1000 -> N=15000, TICKS=3000

groupby_nested_agg:
  N=200000, PASSES=20 -> N=1200000, PASSES=20

matmul_dense_unroll2:
  N=300 -> N=600
```

Each GScript change has the matching Lua change. The manifest was updated so
the harness knows the real default parameters.

This prevents a bad optimization loop:

```text
short benchmark looks slower
engineer optimizes startup-shaped noise
hot runtime gets more complicated
real program does not improve
```

The better loop is:

```text
short benchmark looks suspicious
scale it until the hot body dominates
if it is still slower, profile and optimize
if it is faster, fix the benchmark and move on
```

That is how `mutual_recursion`, `actors_dispatch_mutation`,
`groupby_nested_agg`, and `matmul_dense_unroll2` left the gap list without
adding case-specific runtime code.

## What "Beyond LuaJIT" Means Here

The phrase can be misleading. LuaJIT is still a remarkable runtime. It has a
different object model, a trace compiler, and decades of engineering behind
it. This project did not become "better than LuaJIT" in the universal sense.

What changed is more specific and more useful:

```text
on this repository's comparable local benchmark set,
on this machine,
with the current GScript runtime and method JIT,
every measured row is now faster than the LuaJIT reference row
```

That is a real milestone because the benchmark set is no longer just toy
numeric loops. It includes:

```text
dynamic table reads and writes
string-key table lookup
nested aggregation
actor-style field dispatch
coroutine producer-consumer traffic
closure accumulators
recursive call protocols
dense matrix-style loops
string formatting and tokenization
object construction
array mutation
sorting
tree construction and folds
```

The milestone does not end the compiler work. It changes the standard for the
next phase.

## The Next Standard Is Not "Win One Row"

When there are obvious red rows, the priority is easy. Pick the largest red
row, profile it, fix the mechanism, repeat.

Now the harder work begins. A green scoreboard can still hide fragile
implementation:

```text
some rows remain close to parity
some rows still have high variance
some paths are fast because a narrow set of guards line up
some optimizations exist but are too scattered to extend safely
```

The next phase should therefore keep two priorities in tension.

First, protect the benchmark result:

```text
keep full-suite Current/LuaJIT below 1.00x
keep Current/HEAD regressions visible
scale short rows before drawing conclusions
require CPU profile and warm dump evidence for new target work
```

Second, make the architecture easier to extend:

```text
keep Tier 2 optimization modules data-driven
keep guard/deopt policy explicit
keep table and string native paths composable
avoid adding one-off kernels when a guarded representation can generalize
measure register pressure before accepting local CSE-style cleanups
```

The compiler is no longer trying to prove that it can catch LuaJIT. It has to
prove that it can stay ahead without turning into a pile of special cases.

## The Short Version

The last stretch was not one optimization. It was a sequence:

```text
stable closure and protocol call identities became usable feedback
field-callee and table-shape facts kept dispatch native
numeric lowering shed avoidable fallback and reload costs
Tier 2 compile passes stopped doing irrelevant work
synthetic guard deopts became refreshable instead of fatal
short benchmarks were scaled until their hot work dominated
```

The resulting full run is the first one where the local benchmark scoreboard is
green against LuaJIT from top to bottom.

That is the milestone. The next job is to keep it true.
