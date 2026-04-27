---
layout: default
title: "The Allocation Exit Became a Cache"
permalink: /58-the-allocation-exit-became-a-cache
---

# The Allocation Exit Became a Cache

*April 2026 - Beyond LuaJIT, Post #58*

The last table round made a useful distinction that the benchmark table alone
does not show. A Tier 2 exit can be a correctness boundary, a cold miss, or a
real hot-path tax. Treating all exits as the same number leads to bad work. The
matmul profile had been reporting hundreds of exits, and the obvious story was
that every row allocation was still bouncing out of native code. That story was
true, but incomplete. We removed most of those exits. The wall time barely moved.

That is still progress. It means one bottleneck was converted from an unknown
fallback storm into a bounded mechanism, and the next bottleneck is now exposed
instead of hidden behind allocation exits.

Before this round, matmul had about 912 Tier 2 table exits in a focused guard
run. After the typed NewTable cache, the number fell to 41. After mixed dense
preallocation for row tables, it fell again to 32. `table_array_access` dropped
from roughly 747 exits to 457, and `spectral_norm` from roughly 71 to 43. The
important non-result is that matmul still sits around 0.09s while LuaJIT is
around 0.021s on the same suite. We removed a lot of fallback traffic, but not
the thing dominating the remaining native loop.

That is the right kind of disappointment. It narrows the search.

## Why NewTable Was Still Exiting

The existing table preallocation pass already knew how to make new dense tables
less naive. If a local table is filled through integer keys, the compiler can
attach an array hint to the `OpNewTable`. If the stores prove int, float, or bool
values, it can also choose a typed backing. That helped `sieve` and simple array
builders, but matmul constructs a more complicated shape:

```
row := {}
row[j] = ...
c[i] = row
```

The inner `row` tables are dense typed arrays. The outer `c` table is a dense
mixed array whose elements are tables. Before this round, the inner rows could
eventually become typed, but allocation still began at a table-exit boundary.
The outer table also missed a preallocation opportunity: a table value is not
int, float, or bool, so the pass did not infer "mixed dense array" from
`c[i] = row` without runtime feedback.

The result was a strange split. We had enough information to know that the site
was a dense table allocation, but not enough machinery to allocate it in native
code. So the native code exited, Go created a table, and the function resumed.
Do that for every row and the exit counter looks catastrophic.

The fix was not to pretend table allocation is pure. GScript tables are heap
objects with shapes, dirty-key state, array kind, typed array storage, optional
hash fields, and GC-visible references. Native code should not start hand-making
those structures unless the runtime contract is small and obvious. The safer
MVP was a per-site cache: let the existing Go table-exit path allocate and
refill a bounded batch, then let native code pop already boxed tables from that
site-specific cache on the next iterations.

## The Cache Shape

Each compiled function now owns `NewTableCaches`, indexed by IR instruction ID.
A cache entry is just:

```
values []runtime.Value
pos    int64
```

The values are already boxed table values. Native code for a cacheable
`OpNewTable` embeds the address of its cache entry, checks whether the slice is
present and not exhausted, loads `values[pos]`, increments `pos`, stores the
boxed table into the SSA result slot, and jumps past the table-exit code.

On a miss, nothing clever happens in native code. The existing table-exit path
runs. It creates the table requested by the packed `arrayHint`, `hashHint`, and
`ArrayKind`. Then, if the site is eligible, it refills the cache with a bounded
number of additional tables of the same shape. The current exit still returns
the table it was asked for; the cache holds only future tables.

That matters for correctness. A cache miss is still the normal, resumable table
operation. The fast path is only a pop from a runtime-created pool of equivalent
fresh objects. We are not duplicating the table constructor in ARM64. We are
moving repeated constructor calls out of the loop one batch at a time.

The eligibility rule is intentionally conservative:

```
arrayHint > 0
hashHint == 0
kind != ArrayMixed
arrayHint <= capped dense threshold
```

Small typed arrays get a batch of 32. Larger ones get smaller batches. Mixed
arrays are deliberately excluded from this cache because retaining many large
mixed tables can keep more GC-visible references alive than expected. That is
why the cache hit rate improved the row allocations first, while the outer
mixed table needed a different fix.

Exit diagnostics now print the preallocation shape too:

```
NewTable(array=1024,hash=0,kind=float,cache_batch=32)
```

This changed the diagnostic loop. Before, "NewTable exit" was a vague reason.
Now the exit report says whether the compiler thought the site was typed, how
large it was, and whether a cache batch exists. That is the difference between
"allocation is slow" and "this specific site is not cacheable because it is a
mixed array with this hint".

## Mixed Dense Values

The second fix was smaller and more direct. The table preallocation pass used to
infer typed array kinds from stores whose values were known int, float, or bool.
That misses a common dense mixed array pattern: integer keys storing tables,
strings, or functions. Those values cannot use scalar typed backing, but they
still prove that the table wants an array part rather than a hash-only shape.

The new rule is:

```
if key is TypeInt and value is TypeTable, TypeString, or TypeFunction:
    infer mixed dense array preallocation
```

This is not a matmul special case. It is the general "array of objects" pattern.
In matmul it removes the `SetTable` exits from `c[i] = row`. In the focused
diagnosis, total matmul exits went from 41 to 32. The remaining exits are mostly
cache refills and a few mixed allocation misses.

There is a real tradeoff. A sparse program with misleading local shape could
allocate more array capacity than before. The rule is gated on a local table,
a locally typed integer key, and a known value category, so it is not guessing
from names or benchmark files. Still, preallocation is a memory/time trade, not
a proof of future density. The cap remains important.

## The Load Cleanup After LICM

The third change was not an allocation change at all. Dense table loads had
already been split into four IR nodes:

```
header = TableArrayHeader(t)
len    = TableArrayLen(header)
data   = TableArrayData(header)
value  = TableArrayLoad(data, len, key)
```

That split lets load elimination and LICM see that header, length, and data are
stable facts. The first version handled repeated reads in one block and simple
loop hoists. The missed case was cross-block hoisting: two equivalent header
facts can originate in different loop blocks, get hoisted by LICM into the same
preheader, and then remain duplicated because the earlier load elimination pass
had already run.

The pipeline now runs a second block-local load elimination pass after LICM,
followed by DCE. This is deliberately modest. It does not add a global value
numbering pass. It just reuses the existing CSE pass at the point where LICM has
made more values local to the same preheader.

That pass also learned an invalidation rule: `SetTable`, `Append`, and `SetList`
invalidate typed table-array facts for the mutated object. Header, length, and
data are only reusable while the table shape they describe is still valid. This
is the precise version of a tempting but bad fix.

The bad fix would be: if a function mutates any table, never lower table reads
to `TableArrayLoad`. That protects replay safety, but it also destroys matmul's
read path because matmul reads from `a` and `b` while writing into `row` and
`c`. A function-level mutation bit is too coarse. The right direction is
object-level invalidation and, later, path-sensitive replay metadata.

This round kept the precise invalidation and rejected the coarse gate after it
regressed matmul in local validation.

## What The Numbers Say

The focused guard after these changes showed:

```
matmul              ~0.093s, exits 32
table_array_access  ~0.028s, exits 457
sieve               ~0.028s, exits 22
spectral_norm       ~0.025s, exits 43
```

The exact wall-clock numbers move between single runs, but the exit changes are
not noise. Matmul previously had roughly 912 exits before the NewTable cache,
then 41 after typed cache refill, then 32 after mixed dense preallocation. That
is a real structural change.

It is also not enough.

LuaJIT is still around 0.021s on matmul. If removing hundreds of exits leaves
the benchmark near 0.09s, the dominant gap is now inside the native success path:
row table representation, array metadata loads, register pressure, bounds and
kind guards, or simply the cost of building many GScript table objects even when
the exit is batched. The next optimization round has to measure native code
shape, not just exit count.

This is why the debugging tools matter. `-exit-stats` told us the allocation
exits were real. The IR/ASM diagnostics now need to tell us why the success path
is still four times slower than LuaJIT.

## The Rule

The rule coming out of this round is simple:

Do not optimize a fallback count unless you know what replaces it.

The NewTable cache replaced repeated exits with a bounded native pop and a
runtime refill protocol. Mixed dense preallocation replaced first-store misses
with an IR-level shape inference. Post-LICM load cleanup replaced duplicated
hoisted facts with existing CSE plus explicit mutation invalidation.

All three are infrastructure, not benchmark branches. All three keep fallback
semantics intact. And all three leave a clearer remaining problem than the one
we started with.

That is the kind of progress a method JIT needs. Not every round will move the
headline time. A useful round can also delete a misleading explanation.
