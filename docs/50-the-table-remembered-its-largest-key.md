---
layout: default
title: "The Table Remembered Its Largest Key"
permalink: /50-the-table-remembered-its-largest-key
---

# The Table Remembered Its Largest Key

The table optimizer knew what kind of values were going into an array. It did
not know how large the array was going to get.

That is a strange blind spot for benchmarks like sieve and table-array access.
Tier 1 watches the table operations before Tier 2 compiles. It already records
type feedback and typed-array kind feedback. It can see that a site keeps
writing non-negative integer keys. But the optimizing tier was still mostly
guessing at capacity.

The result was a familiar shape: the code became typed, but still hit avoidable
growth and exit paths around table arrays.

## Type feedback was not enough

Before this round, a table store could communicate:

```text
this SETTABLE writes bool values
this SETTABLE writes float values
this site is polymorphic
```

That is enough to choose `ArrayBool`, `ArrayFloat`, or `ArrayInt`.

It is not enough to size the array part. A boolean sieve table and a tiny
temporary boolean table have the same value kind and wildly different capacity
needs.

The first version of the preallocation hint used fixed constants:

- 1024 for normal feedback-driven array builders;
- 64K when a table allocated outside a loop is filled inside the loop.

Those constants were useful, but they were not evidence. The runtime had better
evidence: it had seen the keys.

## A second feedback vector

The new piece is `TableKeyFeedback`.

It lives beside the existing `FeedbackVector`, indexed by bytecode PC. It
records only one fact:

```text
largest non-negative integer key observed at this table access site
```

It deliberately does not live inside `TypeFeedback`. The existing type/kind
feedback is compact and hot. Key range feedback is table-specific, so it gets
its own vector.

Tier 1 and the interpreter now update it on `GETTABLE` and `SETTABLE` when the
key is a non-negative integer. Tier 2's `TablePreallocHintPass` then merges
three sources:

- the default table-array hint;
- the outer-loop fill hint;
- the observed max integer key plus one.

The pass takes the max and caps feedback-driven capacity at `1 << 20`.

That makes the design general. It is not "sieve should allocate N." It is
"table sites that have already demonstrated large integer keys can carry that
fact into the next tier."

## The lazy mixed-array detail

There was one trap: a large mixed array hint should not immediately allocate a
large `[]Value`.

Typed arrays are cheap enough when the value kind is known. A huge mixed value
array is expensive because every element is a full boxed `Value`. Allocating it
early can turn a capacity hint into a memory regression.

So `Table` now has a small `arrayHint` field after the JIT-verified layout
fields. For large mixed hints, `NewTableSizedKind` allocates only the normal
sparse sentinel capacity and remembers the large hint. If the first real write
promotes the table to a typed array, that typed array uses the remembered
capacity.

That keeps the success path fast without forcing the fallback layout to pay for
capacity it may never use.

## What the numbers said

The focused guard after integration:

```text
sieve default:              0.051s
sieve exits:                43
table_array_access default: 0.042s
table_array_access exits:   803
matmul default:             0.135s
math_intensive default:     0.056s
```

The important result is not just wall time. `sieve` exits dropped into the low
40s, and `table_array_access` stayed safely faster than the current baseline.
`matmul` remained under the 10% regression threshold in the guard run.

This is infrastructure, not a benchmark patch. Future table optimizations now
have a place to read observed key ranges, and the runtime has a way to carry
large capacity hints without eagerly allocating large mixed arrays.

## The invariant

The test coverage checks the two things that would be easy to break later:

- observed max int key becomes an array hint while preserving typed-array kind
  packing in `OpNewTable.Aux2`;
- large mixed hints are lazy, but first typed promotion still uses the large
  capacity.

The next table wins should build on this. A JIT that wants to catch LuaJIT on
array-heavy code needs table operations to be native, typed, and sized from
real feedback.

Commit: `e7ec96f methodjit: fold table key feedback into prealloc hints`
