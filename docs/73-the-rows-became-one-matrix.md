---
layout: default
title: "The Rows Became One Matrix"
permalink: /73-the-rows-became-one-matrix
---

# The Rows Became One Matrix

*April 2026 - Beyond LuaJIT, Post #73*

## The Old Matmul Shape Was Too Honest

The `matmul` benchmark looked optimized for a while.

Tier 2 had already learned how to split table-array access into pieces:

```
header = table-array-header(t)
len    = table-array-len(header)
data   = table-array-data(header)
value  = table-array-load(data, len, key)
```

That shape gave LICM and load elimination something real to work with. It
allowed the compiler to hoist stable table headers, reuse array lengths, and
turn a nested `b[k][j]` read into a fused nested load in the same block.

It was a good representation of the program.

It was also too honest.

The source benchmark builds matrices as ordinary nested tables:

```
a[i] = row
row[j] = value
```

Each row is a table. The outer matrix is a table whose array elements point to
row tables. The hot multiply loop reads through that shape:

```
a[i][k]
b[k][j]
```

The compiler could make each individual step cheaper, but the representation
still required the machine to believe the matrix was a table of row wrappers.
For every nested read, the old fast path still had to prove the outer array
entry was a table, then prove the row table had the expected float array
layout, then load from that row.

That is a good generic table path. It is not a good matrix path.

The result before this round was around:

```
matmul default JIT: about 0.12s
LuaJIT:             about 0.02s
```

The exact number moves with the machine and run count, but the ratio was stable
enough to be useful: the method JIT was roughly four times slower than LuaJIT
on this benchmark, even after several table-array passes had landed.

The next improvement could not come from another small redundant-check removal.
The rows had to stop being independent allocations.

## DenseMatrix Already Existed, But The Benchmark Did Not Use It

GScript already had a `matrix` library path that could build a DenseMatrix:

```
flat backing: []float64
outer table: ArrayMixed rows
row tables:  ArrayFloat slices into the same backing
```

That representation is still a normal table-of-tables from the language's
point of view. A row is still a table. `m[i][j]` still works. The difference is
that every row's `floatArray` points into one contiguous backing allocation.

That matters for two reasons.

First, the memory layout is better. There is one backing array instead of many
separate row arrays.

Second, the outer table can carry a tiny descriptor:

```
dmFlat   = pointer to flat[0]
dmStride = number of columns
```

Once the JIT sees `dmStride != 0`, a nested float load can skip the row-wrapper
path and directly compute:

```
flat[i * stride + j]
```

The problem was that the benchmark did not call the matrix library. It built
ordinary nested tables. Changing the benchmark would not be an optimization.
It would be cheating.

So the runtime now recognizes the ordinary construction pattern.

When an `ArrayFloat` row table is stored sequentially into an `ArrayMixed`
outer table, the runtime can adopt that row into a DenseMatrix backing:

```
outer[0] = float_row_0
outer[1] = float_row_1
outer[2] = float_row_2
...
```

The protocol is intentionally narrow:

```
outer must be a plain ArrayMixed table
row must be a plain ArrayFloat table
row length must be at least the matrix threshold
stores must be sequential while metadata is active
sparse rows, metatables, hash entries, and incompatible rows disable metadata
```

That is not a benchmark name check. It is a layout recognition rule for a
normal table pattern.

## The JIT Path Changed In One Place

The compiler already had a fused instruction for the important hot shape:

```
TableArrayNestedLoad
```

Before this round, the fused path still meant:

```
outer data + i -> row value
check row value is table
check row kind is ArrayFloat
row data + j -> float
```

The new path adds one first branch:

```
if outer.dmStride != 0:
    load outer.dmFlat[i * outer.dmStride + j]
else:
    use the normal row-table path
```

This keeps the generic path intact. A table that is not a DenseMatrix still
uses the old checked row-table load. A DenseMatrix keeps the language-level
table shape but gets the flat load in the hot loop.

The result was the first large `matmul` movement in several rounds:

```
matmul baseline default JIT: 0.123s
matmul after this round:    0.039-0.040s
LuaJIT on the same guard:   0.022s
```

That is roughly a three times improvement over the pre-round baseline for this
guard setup. It also changes the LuaJIT gap from about four times slower to
about 1.8 times slower.

This is still not parity. The benchmark still reports hundreds of table exits,
mostly in the construction side of `matgen`. The multiply loop is much closer
to the shape it needs, but matrix construction is still routed through enough
runtime machinery to be visible.

The important part is that the remaining gap is now a different problem.

Before:

```
hot loop reads still walked row tables
```

After:

```
hot loop reads can use flat matrix addressing
construction still has exit/runtime cost
```

That is a better problem to have.

## Row Adoption Had To Be A Runtime Contract

There was an easy wrong implementation: make the code generator recognize the
benchmark's constructor and emit a special path for that exact program.

That would have produced a nicer number and a worse compiler.

Instead, the adoption happens at the table layer. `RawSetInt` is the place that
already knows whether an integer-keyed store is going into the array part, the
append path, the sparse path, or the map fallback. That is also the place that
can see:

```
value being stored is a table
outer table is still ArrayMixed
row table is ArrayFloat
row table has no metatable/hash/imap side state
```

So the JIT does not duplicate the row-adoption protocol. For mixed array stores
whose value is a table, Tier 2 routes through the runtime store path. That is
cold relative to the `O(n^3)` multiply loop, and it lets one implementation
own the correctness rules.

This means a matrix built by ordinary table syntax can silently become:

```
outer table with dmFlat/dmStride metadata
row floatArray slices sharing one backing
```

But it remains semantically a table of tables. If the program mutates it in a
way that breaks the contract, the metadata is cleared and the JIT falls back to
the normal checked row path.

Examples that invalidate the metadata:

```
replace an existing row
grow a row
write a non-float into a row
store sparse outer rows
store a row with a different length
attach side state that changes table semantics
```

The replacement case matters more than it first appears. If row `r0` is adopted
into the flat backing and the user later executes:

```
m[0] = another_row
```

the old `r0` may still be reachable from user code. If it kept aliasing the
flat backing while the outer table also kept DenseMatrix metadata, writes
through the stale row reference could change what the JIT sees for `m[0][j]`.
That is wrong even if it would be fast.

The fix is conservative: row replacement turns off DenseMatrix metadata.

## The Metadata Also Had To Shrink

The first version of auto-densification kept the flat backing and parent link
directly on every `Table`:

```
dmBacking []float64
dmParent  *Table
```

That worked, but it charged every table in the system for a feature only a few
tables use. The cost showed up immediately in table-heavy benchmarks. After
the first `matmul` merge, `binary_trees` was still inside the guard threshold,
but it had visibly moved in the wrong direction.

The current version uses a cold side object:

```
type denseMatrixMeta struct {
    backing []float64
    parent  *Table
}
```

Each table now pays one pointer:

```
dmMeta *denseMatrixMeta
```

The outer matrix table and adopted rows share the same metadata object. The
outer still keeps `dmFlat` and `dmStride` in the same offsets the JIT verifies.
The backing slice remains Go-visible through the metadata object and through
the row `floatArray` slices.

That brought `binary_trees` back to the expected range:

```
after DenseMatrix merge:        about 0.395s in the focused guard
after metadata shrink:          about 0.376s
matmul after metadata shrink:   about 0.040s
```

This is the part of performance work that is easy to miss. A representation
that accelerates one benchmark can quietly tax every unrelated object in the
runtime. If the project goal is to get close to LuaJIT across the suite, not
just win one graph, those taxes matter.

## The Small Table-Array Fact Fix

The same round also landed a smaller method-JIT change around table-array
loads and stores.

The compiler already knew this source pattern:

```
old = arr[key]
arr[key] = value
```

If the load succeeded natively, then the following store with the same table
and key does not need to repeat the same bounds check. But there is a trap:
`TableArrayLoad` can exit and resume. If the load misses the native path,
execution resumes after the load. The following store still has to do its full
bounds check.

So the new fact is not just:

```
(table, key) was checked
```

It is:

```
(table, key) was checked on the native success path
```

The code generator uses a small success flag. The native load sets it only
after the bounds and type checks pass. The exit-resume continuation clears it.
A following `SetTable` may branch around its redundant bounds check only if the
flag is still set.

That is a small optimization, but it is the right kind of small optimization:
it strengthens the path-sensitive protocol instead of pretending every resume
path is a success path.

The focused guard showed modest wins:

```
table_array_access: improved
fannkuch:           improved
math_intensive:     within noise after repeat checks
matmul:             preserved
binary_trees:       preserved
```

## What Is Left

After this round, `matmul` no longer looks like a row-load problem. It looks
like a construction and remaining-runtime-boundary problem.

The current state is approximately:

```
matmul default JIT: 0.039-0.040s
LuaJIT:             0.022s
remaining gap:      about 1.8x
```

The next work should not be another blind pass over arithmetic. The profile
points at construction-time table exits and row-store metadata population. A
good next optimization would make the construction side understand the same
layout protocol without giving up the centralized runtime invalidation rules.

The hard constraint remains the same:

```
do not specialize the benchmark name
do not silently change table semantics
do not make unrelated table-heavy programs pay for matrix metadata
```

This is how the project gets closer to LuaJIT without becoming a pile of
benchmark exceptions. The rows became one matrix, but only after the table
runtime could prove that was still the same program.
