---
layout: default
title: "The Mutation Stayed Behind the Gate"
permalink: /55-the-mutation-stayed-behind-the-gate
---

# The Mutation Stayed Behind the Gate

Sort is still not solved.

The benchmark now sits around:

```text
sort default:    0.048s
sort no-filter:  0.049s
```

The failures are also unchanged at the top level:

```text
<main>:    performance-blocked Call inside loop
quicksort: self-recursive residual SetTable
```

The useful change is that the second line is no longer opaque.

## From a flat gate to recovery classes

The old recursive table-mutation gate was simple:

```text
self-recursive loop + SetTable = block Tier 2
```

That is safe, but it gives the optimizer no path forward. A swap loop, an
idempotent overwrite, and an append all look the same.

The new analysis classifies mutation sites:

- `idempotent-overwrite`: `t[k] = t[k]`, replay-safe;
- `read-backed-overwrite`: a same-table/same-key read exists, but the stored
  value differs;
- `none`: append, setlist, field writes, or stores without a witness.

Only the idempotent class is admitted today, and only where the hard gate is
already in diagnostic/no-filter territory. Quicksort's swap is classified as
`read-backed-overwrite`, so it remains blocked.

That is the correct answer. A swap is not restart-safe just because both keys
were read. If the first store mutates the table and a later exit replays the
loop, the table can change twice.

## The protocol still missing

Opening quicksort needs one of these infrastructure pieces:

- all table/key/shape/bounds guards for the swap checked before either store;
- mutation undo records for paired overwrites;
- or a stronger native read witness that proves the prior read could not have
  gone through an exit-resume path with different semantics.

Until then, `SetTable` in self-recursive loops stays behind the gate.

## Runtime side work

This round also cleaned up string concatenation and simple `string.format`
integer cases:

- VM concat and JIT op-exit concat now share `runtime.ConcatValues`, which
  computes total string length before one builder growth.
- `%d`, `%i`, `%u`, `%x`, `%X`, `%o`, and plain `%s` avoid `fmt.Sprintf` when
  the format is simple enough.

The focused diagnostic after the cleanup:

```text
string_bench default:    0.028s
string_bench no-filter:  0.020s
coroutine_bench default: 1.62s
```

The coroutine benchmark did not move. That confirms it is a coroutine dispatch
problem, not a string formatting problem.

## What changed

The important outcome is not that sort became fast. It did not.

The important outcome is that the compiler now has a vocabulary for table
mutation recovery. That makes the next sort optimization concrete: prove a
bounded swap can run all guards before mutation, or add recovery metadata that
can undo it.

Commits:

- `ff43210 methodjit: classify recursive table mutation recovery`
- `fb38a49 runtime: speed concat and simple string formats`
