---
layout: default
title: "The Benchmarks Became A Lab"
permalink: /85-the-benchmarks-became-a-lab
---

# The Benchmarks Became A Lab

*May 2026 - Beyond LuaJIT, Post #85*

## The Problem Was No Longer A Missing Benchmark

The old benchmark directory had accumulated the usual scars.

There were useful scripts, one-off scripts, historical scripts, extended
scripts, Lua mirrors, warmup helpers, exit profilers, and a few shell entry
points whose names no longer described the current workflow. They had all been
reasonable at the moment they were written. Together, they made it too easy to
answer the wrong question.

The project had crossed an important threshold: individual optimizations were
now small enough that startup noise, timer precision, benchmark scaling, exit
profiles, and LuaJIT comparison mode could change the conclusion.

At that point "run the benchmark" was not a process. It was a liability.

The benchmark work in this round was therefore not about adding more rows. It
was about making the measurement system behave like a lab:

```text
one manifest of benchmark identity
one timing comparison path
one way to scale tiny programs into measurable hot runs
one place to compare GScript, previous HEAD, and LuaJIT
one diagnostic path from timing row to exits, pprof, and warm dumps
```

That sounds less exciting than a 20x optimization. It is also what makes the
20x optimization believable.

## A Benchmark Is Now An Object, Not A File Name

The first change was conceptual. A benchmark is no longer just:

```text
benchmarks/foo.gs
```

It is an entry with identity:

```text
id
group
name
gscript_path
lua_path
scale parameters
tags
notes about what the row is supposed to stress
```

That matters because the suite is no longer a flat list. There are at least
three different jobs:

```text
suite:
  the older comparable core workloads

extended:
  ordinary scripting shapes such as dynamic maps, coroutine payloads,
  dispatch mutation, formatted keys, and nested object walks

variants:
  shifted or adversarial forms of existing kernels, used to test whether
  a mechanism generalizes
```

The manifest lets the harness treat those groups uniformly while still making
the distinction visible. A row such as `extended/mixed_inventory_sim` is not
accidentally compared as if it were the same kind of evidence as
`suite/fib_recursive`.

That also made cleanup possible. Once the manifest is the source of truth,
old shell scripts such as group-local `run_all.sh` entry points can be judged
by whether they still provide a unique workflow. If they do not, they are just
alternate doors into the same room.

## The Timer Had To Stop Lying By Rounding

The earlier harness printed script-reported seconds. That was good enough when
rows were comfortably above a millisecond and gaps were several times larger
than noise.

It stopped being enough once recursive and numeric kernels crossed below the
display precision. A printed row like this is not useful by itself:

```text
Current: 0.000s
HEAD:    0.000s
LuaJIT:  0.103s
```

It proves the current program is fast. It does not tell us whether a change
made it faster, slower, or identical.

The timing harness therefore grew a higher-resolution wall-clock mode and
repeat scaling:

```text
--time-source wall
--min-sample-seconds 0.2
--warmup 2
--runs 7
--head-ref <commit>
```

The important part is not the exact flags. The important part is the policy:

```text
if script timing is below useful precision,
repeat the program until the wall-clock sample is large enough to compare
```

That is why the recursion optimization can be described as:

```text
ack_nested_shifted:
  0.154428s -> 0.007423s

fib_recursive:
  0.551447s -> 0.006884s
```

instead of as a pair of zero-looking script rows. The optimization did not
become real because the wall timer said so. The wall timer made the already
real optimization measurable.

## Startup Noise Became A First-Class Enemy

Startup noise is not just process launch time. In this project it can include:

```text
parser and compiler setup
JIT warmup
first-call feedback collection
stdlib initialization
GC timing
LuaJIT process startup
benchmark script output formatting
```

For a long-running extended benchmark, most of that disappears into the noise
floor. For a tiny recursive program after a successful JIT optimization, it can
dominate the row.

The harness now has two explicit tools for this:

```text
warmup runs:
  throw away early samples that mostly measure setup

repeat scaling:
  run the benchmark enough times inside the measurement envelope to make the
  hot body visible
```

That distinction matters. Warmup removes cold-start distortion. Repeat scaling
fixes timer resolution. They are related but not the same lever.

This also changed how we read regressions. A 2% difference on a row with a
7% confidence interval is not the same evidence as a 20x difference with low
variance. The harness prints CV and CI95 because the conclusion should be
attached to the quality of the measurement.

## The Harness Compares Three Things

A useful optimization run usually needs three reference points:

```text
Current:
  the worktree under test

HEAD or --head-ref:
  the baseline commit

LuaJIT:
  the external local reference
```

Those answer different questions.

`Current/LuaJIT` says whether the project is ahead or behind the outside
reference on this machine. That is useful for direction. It is not enough to
decide whether a patch should land.

`Current/HEAD` answers the landing question. It catches the embarrassing case
where a row still looks good against LuaJIT but got worse against yesterday's
tree.

This round produced several examples where that distinction prevented bad
code from landing:

```text
direct fixed-table constructor wrappers:
  no stable win on producer_consumer_pipeline or mixed_inventory_sim

larger string.format fast result cache:
  slightly worse mixed_inventory_sim due to cache pressure

polymorphic actor field PIC update:
  correct, but no stable target-row gain

safe json-only extraction from a larger branch:
  tests passed, but timing stayed around 1.00x
```

Those are not failures of the infrastructure. They are exactly what the
infrastructure is supposed to do.

## Diagnostics Had To Connect To The Same Names

Timing alone says where to look. It does not explain why.

The benchmark cleanup therefore tied the performance rows to diagnostic tools:

```text
exit profiles:
  which Tier 2 exits dominate the row

runtime path stats:
  whether coroutine, string.format, native-call, or table paths are hot

warm dumps:
  what Tier 2 admitted, rejected, lowered, and emitted

pprof:
  where time goes when exits are not the problem

strict guards:
  small focused benchmark gates that compare both timing and exits
```

This is how the project avoided treating every slow row as a JIT admission
problem. `mixed_inventory_sim` had zero exits and was still much slower than
LuaJIT. That pointed away from deopt and toward runtime call feedback,
string-format helpers, and string-key table lookup.

`json_table_walk`, by contrast, still had about 54,000 string-format exits in
one branch. That pointed toward native string-format lowering, but the branch
also touched the previously unsafe native string arena. The diagnosis was
useful even though that particular fast path was not merged.

## The New Rule For Optimization Work

The refactor changed the engineering rule from:

```text
make the benchmark faster
```

to:

```text
make a benchmark faster, show the mechanism, compare against a pinned
baseline, check LuaJIT, inspect exits or profiles, and reject the patch if the
gain is noise or the safety story is weak
```

That sounds bureaucratic until a bad optimization looks plausible.

This round had several plausible patches that were not merged. It also had
three that were:

```text
typed table row array access:
  a real table-array lowering improvement

hot native call feedback throttling:
  a small but stable mixed_inventory_sim improvement

guarded protocol const recursion folds:
  a large recursive-call win with guard/deopt instead of replay
```

The difference was not taste. The difference was measurement.

## Why This Matters For The Next Phase

The largest remaining gaps are now ordinary-program gaps:

```text
groupby_nested_agg
mixed_inventory_sim
actors_dispatch_mutation
producer_consumer_pipeline
table_array_access
string_bench
```

These are not all one problem. Some are table shape problems. Some are call
dispatch problems. Some are coroutine handoff problems. Some are string and
stdlib overhead.

The benchmark infrastructure now makes those differences visible enough to
work on them without guessing. That is the point of the refactor.

The suite is no longer just a scoreboard. It is an instrument panel.

