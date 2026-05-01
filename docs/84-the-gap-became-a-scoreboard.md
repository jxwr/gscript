---
layout: default
title: "The Gap Became A Scoreboard"
permalink: /84-the-gap-became-a-scoreboard
---

# The Gap Became A Scoreboard

*May 2026 - Beyond LuaJIT, Post #84*

## The Old Gap Report Is Now Historical

The April gap reports were useful because they made the remaining LuaJIT
distance concrete. On April 29, the largest comparable rows were still plain:

```text
matmul:            0.081s vs LuaJIT 0.021s
fib:               0.088s vs LuaJIT 0.025s
spectral_norm:     0.024s vs LuaJIT 0.007s
sort:              0.033s vs LuaJIT 0.010s
mutual_recursion:  0.015s vs LuaJIT 0.005s
```

That was the right diagnosis at the time. It is not the right description of
the current tree.

The current main worktree guard on 2026-05-02 shows a different regime. The
same suite is no longer mostly a LuaJIT gap report. It is a scoreboard with a
few remaining misses.

## The Current Median-Of-3 Run

The run used median-of-3 timings for GScript default JIT and the local LuaJIT
reference. Lower is better.

A concise dated report is also in
[`docs/perf/luajit-gap-report-2026-05-02.md`](perf/luajit-gap-report-2026-05-02).

`0.000s` in this table does not mean the program took literally no time. It
means the benchmark completed below the harness display precision. Those rows
should be read as "less than one millisecond as printed by this harness," not
as zero-cost execution.

| Benchmark | GScript JIT | LuaJIT | Read |
| --- | ---: | ---: | --- |
| fib | 0.000s | 0.025s | below timer precision |
| fib_recursive | 0.000s | 0.324s | below timer precision |
| sieve | 0.004s | 0.010s | GScript about 2.5x faster |
| mandelbrot | 0.047s | 0.052s | GScript slightly faster |
| ackermann | 0.000s | 0.006s | below timer precision |
| matmul | 0.008s | 0.021s | GScript about 2.6x faster |
| spectral_norm | 0.003s | 0.007s | GScript about 2.3x faster |
| nbody | 0.026s | 0.033s | GScript about 1.3x faster |
| fannkuch | 0.011s | 0.019s | GScript about 1.7x faster |
| sort | 0.005s | 0.010s | GScript about 2.0x faster |
| sum_primes | 0.001s | 0.002s | GScript about 2.0x faster |
| mutual_recursion | 0.001s | 0.005s | GScript about 5.0x faster |
| binary_trees | 0.003s | 0.166s | GScript about 55x faster |
| table_field_access | 0.019s | 0.019s | parity |
| table_array_access | 0.019s | 0.010s | GScript about 1.9x slower |
| coroutine_bench | 0.019s | 0.009s | GScript about 2.1x slower |
| closure_bench | 0.014s | 0.009s | GScript about 1.6x slower |
| string_bench | 0.015s | 0.009s | GScript about 1.7x slower |
| fibonacci_iterative | 0.024s | 0.026s | GScript slightly faster |
| math_intensive | 0.048s | 0.062s | GScript about 1.3x faster |
| object_creation | 0.003s | 0.008s | GScript about 2.7x faster |

The important part is not one headline number. It is the distribution. Most of
the original "close the LuaJIT gap" targets have crossed over:

```text
recursive integer kernels: below display precision or clearly ahead
numeric kernels: ahead
table-heavy whole-call kernels: ahead
sorting and sieve: ahead
field access: parity
array access, coroutine, closure, and string microbenches: still behind
```

## What Actually Changed

No single patch explains this table.

The April 29 report correctly pointed at `matmul`, `fib`, `spectral_norm`,
`sort`, `mutual_recursion`, `sieve`, and `ackermann` as the visible comparable
gaps. The following posts then moved different boundaries for different
program shapes:

```text
fib and fib_recursive:
  fixed additive self recurrences stopped paying exponential call overhead

ackermann:
  nested integer self recurrences moved onto an explicit continuation stack

matmul and nbody:
  hot whole-call numeric kernels replaced repeated call and table traffic

sieve:
  a local boolean table became a packed byte-array computation

spectral_norm, fannkuch, and sum_primes:
  each found the correct whole-call or driver-loop boundary

binary_trees:
  recursive table construction plus immediate fold stopped materializing
  the whole tree on the hot path

sort:
  table and call machinery finally became cheap enough for the remaining
  quicksort shape
```

That is why the current result should not be described as "one benchmark
trick." The suite moved because the JIT and VM gained several bounded protocols
for recognizable program shapes, each with normal fallback semantics.

## The Wins Are Real, But The Caveats Matter

The sub-millisecond rows deserve caution. The benchmark harness prints seconds
to three decimals, so `0.000s` compresses all timings below that display
precision into the same text. It proves the old LuaJIT gap is gone for those
rows, but it does not distinguish 900 microseconds from 50 microseconds.

The larger rows are easier to compare. `matmul` at `0.008s` versus LuaJIT
`0.021s`, `nbody` at `0.026s` versus `0.033s`, and `mandelbrot` at `0.047s`
versus `0.052s` are still small benchmarks, but they are above the display
floor and point in the same direction as the broader table.

There is also a benchmarking-policy caveat. This is still a local
Darwin/arm64 LuaJIT comparison, not a portable language shootout. LuaJIT and
GScript are not compiling the same frontend, using the same object model, or
making the same tradeoffs. The comparison is valuable because it has guided the
JIT work for this project, not because it proves a universal runtime ranking.

## The Remaining Gaps

The stale narrative would be to keep saying "matmul is the largest LuaJIT gap"
or "recursion is still several times slower than LuaJIT." The current table no
longer supports that.

The remaining comparable gaps are narrower and more runtime-shaped:

```text
table_array_access:
  GScript 0.019s vs LuaJIT 0.010s

coroutine_bench:
  GScript 0.019s vs LuaJIT 0.009s

closure_bench:
  GScript 0.014s vs LuaJIT 0.009s

string_bench:
  GScript 0.015s vs LuaJIT 0.009s
```

That list is useful because it is no longer dominated by the old numeric and
recursive kernels. A stricter runs=5 guard over the newer extended suite makes
the next map sharper still:

```text
extended/mixed_inventory_sim:
  best GScript 0.152s vs LuaJIT 0.022s

extended/actors_dispatch_mutation:
  best GScript 0.039s vs LuaJIT 0.011s

extended/producer_consumer_pipeline:
  best GScript 0.127s vs LuaJIT 0.043s

extended/json_table_walk:
  best GScript 0.031s vs LuaJIT 0.017s
```

So the immediate performance frontier has moved from the old core numeric
suite to extended, ordinary-program table and dispatch workloads. The
`json_table_walk` row already moved once after the strict-guard pass, by
widening a string-format intrinsic and removing a large no-filter exit stream;
that is the kind of focused, measured change this phase needs. The next work
should look less like "make Tier 2 admit the big loop" and more like:

```text
array table access:
  reduce residual element-load/store overhead without breaking mixed-table
  semantics

coroutines:
  make resume/yield dispatch cheaper instead of hiding it behind a synthetic
  whole-call pipeline

closures:
  keep closure invocation and upvalue access on the fast path across ordinary
  call boundaries

strings:
  continue moving equality, indexing, concatenation, and allocation-heavy
  string paths out of generic exits

extended table and dispatch programs:
  make mixed object/array table traffic, method dispatch, and producer-consumer
  table mutation cheap without depending on benchmark-specific kernels
```

Those are good problems to have. They are ordinary language-runtime problems,
not the old "the JIT is 4x to 6x behind LuaJIT on the core kernels" problem.

## What To Update In Our Heads

The project can stop using the April gap report as the current map. It is now a
history document.

The current state is more precise:

```text
GScript is ahead of the local LuaJIT reference on most of the measured core
suite, at parity on table_field_access, and still behind on array access,
coroutine, closure, and string microbenchmarks. The largest current gaps are
now in extended mixed table and dispatch workloads.
```

That sentence is less dramatic than "we beat LuaJIT" and more useful than "we
still have a LuaJIT gap." It says where the work actually is.
