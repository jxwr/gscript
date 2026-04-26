---
layout: default
title: "The Row Was a Table"
permalink: /53-the-row-was-a-table
---

# The Row Was a Table

Matmul still has the largest obvious gap.

The current JIT is around 0.13s. LuaJIT is around 0.022s. That is not a
one-instruction problem. The hot path is nested table access:

```text
a[i][k]
b[k][j]
c[i][j]
```

The outer array load returns a row table. Tier 2 was not carrying that fact
cleanly enough. It knew the outer container was a mixed array, but the mixed
array result stayed mostly dynamic. The next `GETTABLE` then had to rediscover
that the value was a table.

The row was a table. The compiler just kept forgetting.

## Feedback had the fact, but not on this path

The interpreter already has a type feedback lattice:

```text
Unobserved -> Int / Float / Table / ... -> Any
```

But the Tier 1 mixed-array `GETTABLE` fast path only recorded enough result
type information for int and float-oriented optimizations. A mixed array that
held row tables did not feed `FBTable` back to Tier 2.

That meant matmul's warm profile missed a simple fact:

```text
GETTABLE rows[i] returns table
```

Without that fact, nested array access stayed heavier than it needed to be.

## The change

Tier 1 mixed-array `GETTABLE` now records table result feedback too. Pointer
values are classified by their NaN-box pointer subtype, so strings and
functions still widen to `FBAny`; only table pointers become `FBTable`.

Tier 2 then uses that feedback in two places:

- GraphBuilder marks `GETTABLE` as `TypeTable` when `Result=FBTable` and the
  array kind is mixed.
- The mixed-array load emits a table guard inline before storing the result.

That second point is the contract. `TypeTable` is not a wish. The generated
code checks that the loaded mixed-array value is actually a table. Downstream
table operations can then skip the redundant table tag/subtype check while
still checking metatable state.

This also fixed an old hole: `GuardType(TypeTable)` now emits a real table
guard instead of passing through unsupported guard types.

## What it bought

This is not the big matmul win. It is a prerequisite.

The focused diagnostic after integration:

```text
matmul default:              0.129s
matmul no-filter:            0.128s
table_array_access default:  0.038s
```

The 5-run guard:

```text
matmul default:              0.133s
matmul no-filter:            0.127s
table_array_access default:  0.039s
fannkuch default:            0.046s
```

Matmul stayed under the regression threshold, and no table-array regression
appeared.

The important outcome is that warm dumps now show row loads as table-typed.
That makes the next optimization concrete: hoist row table array headers, len,
and data pointers across the inner loop instead of repeatedly validating and
loading the same row metadata.

## Why this is not a benchmark special case

This applies to any mixed array that stores tables:

```text
rows[i]
objects[i]
children[i]
matrix[k]
```

The feedback source is per bytecode PC, the guard is emitted by the generic
mixed-array load, and the downstream win is normal type propagation. Matmul is
just the benchmark where the missing fact is most visible.

Commit: `e6eefb1 methodjit: propagate table results from mixed arrays`
