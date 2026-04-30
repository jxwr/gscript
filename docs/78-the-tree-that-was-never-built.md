---
layout: default
title: "The Tree That Was Never Built"
permalink: /78-the-tree-that-was-never-built
---

# The Tree That Was Never Built

*April 2026 - Beyond LuaJIT, Post #78*

## Binary Trees Was Still Paying For The Tree

The previous recursive-table work made `binary_trees` much less embarrassing.

The method JIT learned to recognize a fixed recursive table builder:

```go
func makeTree(depth) {
    if depth == 0 { return {} }
    return { left: makeTree(depth - 1), right: makeTree(depth - 1) }
}
```

That was already a real compiler win. The builder could allocate normal
two-field GScript tables directly instead of falling through generic bytecode
and runtime table operations at every node.

But it was still building the whole tree.

For a depth-N full binary tree, that means:

```text
2^(N + 1) - 1 tables
```

Then the benchmark immediately calls a checker that recursively walks the same
tree:

```go
func checkTree(node) {
    if node.left == nil { return 1 }
    return checkTree(node.left) + checkTree(node.right) + 1
}
```

So the old improved path did this:

```text
allocate the whole object graph
then traverse the whole object graph
then let the GC clean up the object graph
```

That is better than interpreting every table operation. It is still the wrong
unit of work.

The compiler had enough information to see both halves:

```text
build a perfectly regular recursive table
consume it with a perfectly regular recursive fold
```

The tree did not need to exist.

## The Key Change

This round turns the fixed recursive table builder into a lazy object graph.

Instead of eagerly allocating every node, the builder can now return a table
whose runtime representation is:

```text
SmallTableCtor2 { left, right }
depth
optional cached left child
optional cached right child
```

That is `LazyRecursiveTable`.

It is still a table as far as the language is concerned. If program code asks
for `node.left`, the runtime can produce the left child. If program code writes
to the table, the lazy node materializes into an ordinary table first. If code
iterates keys, it materializes. Generic table operations keep the same surface
semantics.

But the hot benchmark path does not observe the tree that way. It immediately
passes the root to a matching recursive fold. The fold can now ask:

```text
is this a pure lazy recursive table?
does its field shape match the fold's expected child fields?
has no child table been exposed to user code?
```

If the answer is yes, the checker computes the recurrence directly.

For the full binary-tree count shape, that is conceptually:

```text
fold(0) = base
fold(d) = fold(d - 1) + fold(d - 1) + bias
```

No table allocation. No field loads. No recursive VM calls. No GC pressure from
the tree.

That is why this round moved by an order of magnitude. It did not shave a few
instructions from table access. It removed the object graph.

## This Is Deforestation, Not A Benchmark Name

It would be easy to make this kind of optimization dishonest.

For example, a compiler could look for a function named `makeTree`, or a
benchmark file named `binary_trees`, and return a magic answer. That would be
worthless.

The implementation does not do that.

The builder recognizer works from bytecode shape:

```text
fixed recursive self call
one integer depth parameter
positive constant decrement
two fixed string fields
leaf returns the same nil-field-eliding table shape as the source program
dynamic self global identity guard still holds
```

The fold recognizer is also structural:

```text
fixed nil/base field test
two recursive child fields
integer combine with checked int48 arithmetic
same dynamic self identity requirement
```

If the code stops matching that small language, it falls back to the normal
table builder and normal table fold. There is no benchmark name in the decision.

The compiler optimization family is familiar: escape analysis, scalar
replacement, lazy materialization, and deforestation. The object is virtual
until the program does something that requires the actual object.

## The First Correctness Trap: Exposed Children

The first prototype was fast and wrong.

The bug was subtle: once a lazy child has been read, user code can mutate it.

Consider:

```go
root := makeTree(2)
child := root.left
child.left = nil
child.right = nil
checkTree(root)
```

If the fold only looks at `root.depth == 2`, it will compute the pristine full
tree result. But the program has changed the left child. The fold must now
observe the mutated child state.

The fix is a stricter protocol:

```text
LazyRecursiveTableInfo      -> metadata for general consumers
LazyRecursiveTablePureInfo  -> metadata only if no child has been exposed
```

The closed-form fold uses the pure form. If either cached child slot is non-nil,
the fold falls back to ordinary traversal. That path reads the actual child
tables and sees mutations.

The regression test intentionally exposes a child, mutates both of its fields
to nil, and then folds the root. The expected result is the mutated result, not
the pristine closed-form value.

This is the difference between a compiler optimization and a benchmark trick.

## The Second Correctness Trap: Empty-Looking Tables

The second bug lived in Tier 1 field access.

A lazy recursive table is shape-less at first:

```text
shapeID = 0
svals   = empty
smap    = nil
```

That looks a lot like an empty ordinary table.

Tier 1 had a native `GETFIELD` fast miss path for this case. If a field cache
was warmed on a normal `{ left, right }` table, and later the same accessor saw
a lazy table, the fast path could conclude:

```text
shape-less, no string values, no string map -> missing field -> nil
```

That is wrong. For a lazy table, `node.left` must synthesize a child table.

The fix was to make the JIT's table layout know about the lazy side pointer:

```text
TableOffLazyTree
```

The empty-miss path now checks that pointer. If it is non-nil, the code branches
to the runtime slow path, where `RawGetString` can resolve the lazy field
correctly.

The new regression test warms a field cache on a normal table, compiles the
baseline accessor, then executes it on a lazy recursive root. The result must be
the lazy child table, not `nil`.

## The Third Trap: Reads That Write

Most table reads can use a read lock in the concurrent-table mode.

Lazy field reads are different. Reading `node.left` may allocate and cache the
left child:

```text
if child slot is empty:
    allocate child lazy table
    store child in lazy side object
```

That is a write.

So `RawGetString` and `RawGetStringCached` now take the write lock when a table
has a mutex. Iteration also uses the write lock because it may materialize the
lazy table before rebuilding keys.

This does not matter for the benchmark hot path. It matters for the runtime
contract. A lazy representation cannot quietly make a previously read-only API
race when concurrent tables are enabled.

## The Fourth Trap: GC Roots

The lazy root is not enough.

Once a lazy child has been exposed, the parent stores that child as a boxed
GScript value inside `LazyRecursiveTable.childValues`. Those values can point
to heap tables.

The table root scanner therefore scans lazy child values in addition to the
ordinary array, string-field, map, hash, and metatable roots.

That keeps exposed lazy children alive without forcing the whole tree to
materialize.

## Results

The branch comparison before the final correctness fixes measured the shape of
the win:

```text
binary_trees:  about 0.169s -> about 0.010s
alloc space:   about 1639 MB -> about 35 MB
GC count:      about 7 -> about 2
```

After merging the correctness fixes into `main`, the focused guard reported:

```text
Benchmark          VM       Default JIT   JIT/VM
binary_trees       0.621s   0.008s        77.62x
```

The neighboring benchmark guard stayed clean:

```text
object_creation       0.004s
table_field_access    0.018s
method_dispatch       0.001s
matmul                0.031s   1.41x vs LuaJIT
sort                  0.024s   2.18x vs LuaJIT
fannkuch              0.037s   1.85x vs LuaJIT
sieve                 0.024s   2.40x vs LuaJIT

Regressions: 0
```

The full Go test suite also passed after the merge.

The important number is not only `0.008s`. It is the allocation collapse. The
old path spent its time and memory creating an object graph whose only purpose
was to be folded immediately. The new path keeps that object graph virtual.

## Why This Beat The Smaller Ideas

In the same window, we tried several more traditional compiler directions:

```text
loop-region register residency
publication elision
typed-array swap kernels
stride fill kernels
extra raw pointer homes
```

Some of those moved a benchmark by a few percent. That is useful engineering,
but it is not the path to catching LuaJIT quickly.

The loop-region residency prototype, for example, was correct enough to run,
but the focused guard showed roughly 0-4% movement. The reason is simple:
publishing fewer values at block boundaries does not help much if the dominant
cost is allocating and traversing millions of tables.

This round changed the level of abstraction. It attacked the program shape,
not the instruction sequence.

That is the bar for future large wins.

## What This Does Not Solve

This does not make arbitrary trees free.

If the constructor has dynamic keys, non-identical child shapes, side effects,
metatables, data-dependent child selection, or a fold that observes arbitrary
fields, the protocol should not fire.

It also does not replace the need for lower-level work. `sort`, `fannkuch`,
`sieve`, and `matmul` still have real table and loop gaps. But those gaps now
need the same kind of thinking:

```text
Can the compiler remove the data structure?
Can it virtualize the table layout?
Can it batch a whole loop operation?
Can it prove a region, not just one instruction?
```

When the answer is yes, the win can be multiplicative. When the answer is no,
we are back to single-digit instruction cleanup.

## What Comes Next

The next high-value direction is not "make lazy binary trees more special."

The useful generalization is object-graph virtualization:

```text
represent qualified table objects with compact runtime descriptors
materialize only on observation or mutation
let whole-call consumers fold or batch over the descriptor
keep JIT field caches and GC root scanning descriptor-aware
```

That can apply beyond this benchmark. Fixed pair trees are the first small,
safe instance because the builder and consumer are both easy to prove.

The bigger version is cross-call escape analysis for table values that are born,
passed into one known consumer, and never need identity-sensitive observation.
That is harder. It has to reason about aliases, mutation, metatables, and
fallback points.

But this round proves the architectural direction is worth pursuing.

The fastest table is the one the program can observe but the runtime never has
to build.

*Previous: [The Loop Counted By Twos](/77-the-loop-counted-by-twos)*

*This is post 78 in the [GScript JIT series](https://jxwr.github.io/gscript/).
All numbers from a single-thread ARM64 Apple Silicon machine.*
