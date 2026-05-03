---
layout: default
title: "The Call Stopped Leaving The Loop"
permalink: /86-the-call-stopped-leaving-the-loop
---

# The Call Stopped Leaving The Loop

*May 2026 - Beyond LuaJIT, Post #86*

## The Round Started With A Wider Net

This optimization round did not start with one hypothesis.

It started with a full benchmark pass, six worktrees, and separate agents
assigned to the largest remaining shapes:

```text
mixed inventory and string/table runtime paths
actor dispatch
coroutine producer-consumer handoff
table array access
JSON and group-by object walks
recursive protocol calls
```

That was deliberate. The remaining LuaJIT gaps were no longer all the same
kind of problem. A single main-thread hunch would have been too slow and too
biased.

The full run made the map clear:

```text
extended/mixed_inventory_sim:
  about 6-7x slower than LuaJIT, with zero Tier 2 exits

extended/actors_dispatch_mutation:
  about 3x slower than LuaJIT, with a small number of exits

extended/producer_consumer_pipeline:
  about 3x slower than LuaJIT, with coroutine and table payload pressure

extended/json_table_walk:
  about 2x slower than LuaJIT, with many string-format exits

suite/table_array_access:
  about 2x slower than LuaJIT

variants/ack_nested_shifted:
  call-exit dominated before the recursion fix
```

The useful result of the parallel pass was not that every branch produced a
patch. Most did not. The useful result was that the project could test several
plausible explanations at once and merge only the ones that survived.

## The Patches That Did Not Land Were Important

Several branches produced correct-looking optimizations that were not merged.

The actor branch shortened a polymorphic field path, but the target benchmark
did not move:

```text
actors_dispatch_mutation:
  current about 0.043s
  HEAD about 0.042s
```

The coroutine branch added cached coroutine builtin access and tightened
fixed-shape table handoff allocation, but the producer-consumer benchmark
barely moved:

```text
producer_consumer_pipeline:
  about 0.147s vs HEAD about 0.149s
```

The main thread tried direct fixed-field constructor wrappers for three- to
eight-field table literals. The idea was generic and reasonable: avoid slice
and loop overhead in `NewTableFromCtorN`. The measurement rejected it:

```text
producer_consumer_pipeline:
  no stable gain

mixed_inventory_sim:
  slightly worse
```

The string-format cache experiment was also rejected. Increasing the single
integer result fast cache from 64 slots to 8192 looked attractive for
`SKU%05d`-style repeated keys, but the larger atomic cache made
`mixed_inventory_sim` slightly slower.

Those rejects are part of the story. They prevented the tree from collecting
low-signal code just because the idea sounded like an optimization.

## The First Merged Win Was Small And Boring

The `mixed_inventory_sim` profile had an important property:

```text
Tier 2 exits: 0
```

That means the gap was not caused by repeated deopt. It was runtime overhead
inside the accepted path.

One small branch found a safe improvement there: once a native callsite is
stable, the VM no longer needs to keep re-observing the same callee identity
and string argument details on every hot iteration. It still keeps arity checks
and count updates, but after 64 full observations it stops doing the expensive
native-call feedback refresh.

The patch was tiny:

```text
internal/vm/feedback.go
```

The result was also modest but real:

```text
mixed_inventory_sim:
  about 0.161s -> about 0.152-0.153s
```

That is not a headline win. It matters because it is safe, generic, and points
at the correct class of remaining work: hot stdlib and native-call overhead,
not exit storms.

## Table Array Access Got A More General Inference

The table-array branch improved a different problem.

Some programs build arrays of typed rows and then read them through table
accesses that have not yet observed enough GETTABLE feedback. Earlier lowering
could miss those rows even when the stores had already made the typed shape
obvious.

The new pass infers table-of-typed-row reads from local typed stores:

```text
row tables created locally
numeric stores establish the row kind
outer table stores establish table-of-row structure
later row reads can lower to typed TableArrayLoad
```

It also lets `TableArrayNestedLoad` handle int rows, not only float rows.

The merged commit was:

```text
ee1e869 methodjit: infer typed table row array access
```

The focused row now sits around:

```text
suite/table_array_access:
  about 0.020s in the focused default run
  exits: 32
```

This did not close the LuaJIT gap. The full hot-scaled run still shows
`table_array_access` around 2.1x behind LuaJIT. But the mechanism is the right
kind: infer a typed table representation from program structure and keep the
normal fallback.

## The Big Win Was Recursive Call Protocol

The recursion branch changed the round.

Before this patch, a stable recursive protocol call could still pay the
generic call-exit path over and over:

```text
native/JIT caller
exit to Go call dispatcher
execute or resume callee
return through the generic continuation path
repeat inside a recursive or driver loop
```

For recursion-heavy programs, that overhead is catastrophic. It is not just
one slow call. It is a slow call boundary multiplied by the shape of the call
tree.

The previous implementation had tried to constant-fold protocol calls, but the
fallback was unsafe: guard failure could replay a call after rebinding or after
side effects. That is exactly the class of bug this JIT cannot tolerate.

The new version restored the optimization with a different recovery contract:

```text
specialize stable protocol const calls
guard the callee and stable int globals
on guard failure, deopt
do not replay the call through call-exit
```

For globals that are not written by the current function, the guard can be
hoisted to the Tier 2 entry. For globals that may be rebound inside the
function, the guard stays at the call site.

That distinction is what makes the optimization general. It is not "if this is
ack, do the ack thing." It is:

```text
if the call target is stable,
and the required globals have guardable identities,
then the native body may use the constant protocol;
otherwise fall back by deoptimizing to the normal VM semantics
```

The merged commit was:

```text
1718c0d opt: guard protocol const recursion folds
```

## The Numbers Were Not Subtle

Script-level timing rounded the current rows too aggressively, so the final
comparison used wall-clock repeat scaling with a pinned pre-recursion baseline:

```text
--time-source wall
--min-sample-seconds 0.2
--head-ref 0283c11
```

The result:

```text
variants/ack_nested_shifted:
  0.154428s -> 0.007423s
  exits: 12
  LuaJIT: 0.104641s

suite/fib_recursive:
  0.551447s -> 0.006884s
  exits: 10
  LuaJIT: 0.340348s

suite/mutual_recursion:
  0.006308s -> 0.005953s
  exits: 11
  LuaJIT: 0.006182s
```

So the large rows were not just closed. They crossed over:

```text
ack_nested_shifted:
  about 20.8x faster than the previous tree
  about 14x faster than the local LuaJIT reference

fib_recursive:
  about 80x faster than the previous tree
  about 49x faster than the local LuaJIT reference
```

The exits tell the same story. `ack_nested_shifted` used to produce roughly
60,000 exits. The new path reports 12. The optimization did not make exits
cheaper. It removed the reason to exit in the hot recursive call protocol.

## Why This Is Not A Case-Specific Trick

The tempting criticism is obvious: recursion benchmarks are easy to cheat.

This patch is not a benchmark recognizer. It does not inspect the benchmark
name. It does not replace `fib` or `ack` with a hand-written answer. It does
not assume one particular recurrence.

It changes the call protocol available to optimized code:

```text
stable callee identity
stable guarded globals
known result convention
guard failure goes to deopt
callee marking keeps recursive entry behavior coherent
```

That is a compiler mechanism. Recursion magnifies it because recursion is
mostly calls, but the same contract can apply to other stable call patterns
where replay would be wrong and deopt is the correct recovery path.

This also explains why the win is so large. The old path paid a fixed generic
call boundary cost at a high-frequency site. The new path pays a small guard
cost at the boundary and then stays inside the optimized protocol.

When the inner operation is cheap and the call count is high, removing the
boundary dominates everything else.

## The Unsafe String Win Stayed Out

One branch did find a visible `json_table_walk` improvement by reopening the
native `string.format(<single %d pattern>, int)` path:

```text
json_table_walk:
  exits about 54079 -> 27
  timing roughly 0.038s -> 0.034-0.036s
```

That patch was not merged.

The reason is not that the mechanism is uninteresting. It is that this area
touches the native string arena, and an earlier version had already produced a
real memory failure around later runtime initialization. A fast path that can
create invalid Go-visible string headers is not an optimization. It is a
delayed crash.

The safe parts of that branch were extracted and measured separately. They did
not move the target rows. So they were not kept.

That is the standard this phase needs:

```text
large win plus weak safety story:
  investigate, do not merge

small win plus strong safety story:
  merge if it is generic and measured

large win plus strong guard/deopt story:
  merge and make it the new baseline
```

## The Full Run After The Merge

After the merged patches, `go test ./...` passed.

The full benchmark sampling still shows the remaining frontier clearly:

```text
extended/mixed_inventory_sim:
  about 0.151604s vs LuaJIT 0.025373s

extended/actors_dispatch_mutation:
  about 0.046763s vs LuaJIT 0.013778s

extended/producer_consumer_pipeline:
  about 0.144591s vs LuaJIT 0.046532s

suite/table_array_access:
  about 0.077875s hot-scaled vs LuaJIT 0.035909s

suite/coroutine_bench:
  about 0.206889s hot-scaled vs LuaJIT 0.097696s

suite/string_bench:
  about 0.020023s vs LuaJIT 0.011083s
```

Those rows are not solved by the recursion patch. They are the next map.

But the recursive rows changed category:

```text
variants/ack_nested_shifted:
  about 0.006586s vs LuaJIT 0.103675s in the full run

suite/fib_recursive:
  about 0.006705s vs LuaJIT 0.334850s in the full run

suite/ackermann:
  about 0.006749s vs LuaJIT 0.008155s
```

The old recursive call boundary is no longer the bottleneck.

## What This Round Taught

The useful lesson is not "recursion is fast now."

The useful lesson is narrower and more portable:

```text
when a hot path is dominated by a generic boundary,
and runtime observation has proven a stable shape,
the best optimization is often to specialize the boundary itself,
guard the assumptions,
and deopt instead of replaying side effects
```

That is the same direction the project has been moving in several areas:

```text
table access:
  cache shape and key facts, then lower the access

method dispatch:
  cache bounded receiver/callee facts, then call native entries

recursion:
  cache stable callee/global facts, then keep the call protocol native
```

The mechanism is broader than the benchmark. The benchmark just made the
cost visible enough that the correct boundary became impossible to ignore.

