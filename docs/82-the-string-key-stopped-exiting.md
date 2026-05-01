---
layout: default
title: "The String Key Stopped Exiting"
permalink: /82-the-string-key-stopped-exiting
---

# The String Key Stopped Exiting

*May 2026 - Beyond LuaJIT, Post #82*

## The Extended Benchmarks Changed The Shape Of The Problem

The core benchmark suite had started to look deceptively good.

On the current guard, most of the original kernels were already at parity or
ahead of the local LuaJIT reference:

```text
mandelbrot:          0.051s vs LuaJIT 0.061s
matmul:              0.010s vs LuaJIT 0.022s
sort:                0.006s vs LuaJIT 0.011s
math_intensive:      0.051s vs LuaJIT 0.065s
fibonacci_iterative: 0.024s vs LuaJIT 0.028s
object_creation:     0.004s vs LuaJIT 0.008s
```

That table is useful, but it can also hide the next problem. The extended
suite is more like ordinary scripting code: dynamic string keys, nested
aggregate maps, formatted strings, coroutine-style pipelines, object dispatch,
and tables that are neither pure arrays nor fixed two-field records.

The worst `groupby_nested_agg` profile before this round was not subtle:

```text
JIT time:       about 0.170s
VM time:        about 0.276s
LuaJIT:         about 0.010s
Tier 2 exits:   1,680,372
```

The method JIT had finally made the program faster than the interpreter, but it
was still nowhere near LuaJIT. More importantly, the exit profile was telling
us exactly why:

```text
960,000 table exits from dynamic string-key group lookups
720,000 op exits from string equality checks
```

This was the wrong place for another whole-call kernel.

`groupby_nested_agg` is not a closed numeric recurrence like `fib`. It is not a
pure virtual table fold like the binary tree work. It is not a small complete
algorithm such as sieve or fannkuch where the whole function can be replaced by
a bounded native kernel.

The missing piece was more ordinary and more important:

```text
dynamic string-key table access had to stop leaving Tier 2
string equality had to stop leaving Tier 2
```

## The Bug Was A Classification Mistake

The hot aggregate loop uses tables in two different ways.

Some table operations really are array-like:

```text
events[i]
rows[j]
```

Those should go through the typed array lowering path.

But the hot group maps are different:

```text
groups[channel]
bucket[kind]
```

The key is dynamic, but it is still a string. Earlier feedback had enough data
for the VM and Tier 1 to cache these lookups, but Tier 2 mostly saw a generic
`GetTable`. The table-array lowering pass could then treat a mixed table site
as if the profitable native path was array-oriented. When the key was not an
integer, the native path had no correct fast case and fell out to the table
exit handler.

That produced the exit storm.

The fix was to make the lowering boundary respect the key:

```text
if the site has dynamic string-key cache feedback
and the key is not proven integer
then do not rewrite it as an array load
```

This is small, but it matters. A mixed table is not automatically an array
table. The key representation has to participate in the decision.

## The Cache Became A Tier 2 Input

The runtime already had a per-PC polymorphic dynamic string-key cache. It keeps
small shaped-table hits cheap by remembering:

```text
shape ID
field index
key data and length
the key string itself, to keep the identity safe across GC and reuse
```

Before this round, that cache mainly helped VM and Tier 1 paths. Tier 2 could
update it through exits, but the hot native body still did not have a direct
success path for the common string-key lookup.

The new Tier 2 path does three things.

First, it passes the prototype's dynamic string-key cache into the Tier 2
execution context alongside the existing static field cache:

```text
BaselineFieldCache
BaselineTableStringKeyCache
```

Second, `GetTable` and `SetTable` exits now update that dynamic string-key
cache when the key is a string. Cold misses still use the normal runtime table
semantics. The cache is not a separate truth; it is feedback learned from the
same operations the language would have performed anyway.

Third, the native `GetTable` emitter gets a string-key success path before the
integer-key array path:

```text
check key is a string
check table is a plain table
probe the per-PC string-key cache
validate shape ID
load svals[field index]
```

When the cache misses because the site has a few more keys than the cache can
hold, the emitter still has a bounded shaped-table fallback: scan the small
field key list, compare string content, and load the matching field value.
Large hash-mode tables and metatable cases still fall back through the normal
table exit protocol.

The important point is that this is not a benchmark-name check. It is a table
access lowering:

```text
dynamic string key
plain shaped table
per-PC cache feedback
normal VM fallback
```

Any program with that shape can use the same path.

## String Equality Was The Other Half

After the table exits were fixed, one more hot exit class was still obvious:

```text
channel == "error"
kind == "click"
kind == "view"
```

Tier 2 already had native string ordering for `<` and `<=`, because those
operators cannot be treated as pointer comparison. Equality had taken the
conservative route: if two string values were not the same boxed pointer, it
fell out through a generic op exit so the runtime could compare content.

That is correct, but it is too expensive in a hot aggregate loop.

The new equality path is direct:

```text
if the boxed values are identical, true
else check both operands are strings
compare lengths
if backing data pointers match, true
otherwise compare bytes
```

Non-string values still use the generic fallback where needed. Equal-content
strings no longer require an op exit just because they are distinct string
objects.

## The Exit Profile Collapsed

The result was the most useful kind of speedup: the wall time improved because
the diagnostic profile became simpler.

Before:

```text
groupby_nested_agg JIT:  about 0.170s
Tier 2 exits:            1,680,372

480,000 dynamic group lookups at one site
480,000 dynamic group lookups at another site
720,000 string equality op exits
```

After:

```text
groupby_nested_agg JIT:  about 0.023-0.025s
VM:                      about 0.275s
LuaJIT:                  about 0.010s
Tier 2 exits:            412
```

The remaining exits are no longer the steady-state loop body. They are cold
setup and cache-population behavior:

```text
35 exits for first-seen group buckets
single-digit setup exits
3 residual op exits
```

That means the hot aggregate loop is finally staying native.

The neighboring extended workloads stayed in their previous range on the same
run:

```text
json_table_walk:             about 0.101s
log_tokenize_format:         about 0.173s
mixed_inventory_sim:         about 0.169s
producer_consumer_pipeline:  about 0.229s
actors_dispatch_mutation:    about 0.660s
```

The core side checks stayed flat too:

```text
table_field_access:   about 0.019s
table_array_access:   about 0.023s
string_bench:         about 0.015-0.016s
closure_bench:        about 0.019s
```

## What This Says About The Next Gaps

This round also clarified what remains.

`groupby_nested_agg` is no longer a method-JIT embarrassment. It is about
seven times faster than the VM and only a few times behind LuaJIT on the local
run:

```text
GScript JIT:  about 0.023s
LuaJIT:       about 0.010s
```

That is still not the goal, but it is a different problem. The obvious million
exit storm is gone.

The bigger remaining extended gaps are elsewhere:

```text
actors_dispatch_mutation:    Tier 2 is not attempted; method dispatch is too heavy
producer_consumer_pipeline:  coroutine/resume/yield stays on the VM boundary
json_table_walk:             build phase still has NewTable gating and SetField exits
mixed_inventory_sim:         still mostly Tier 1 string/table/format work
```

The lesson is not "add more kernels". In fact, a producer/consumer whole-call
kernel that recognizes the benchmark's exact bytecode would be faster, but it
would also be the wrong abstraction.

The useful lesson is narrower:

```text
when a dynamic operation has stable feedback,
make that feedback visible to Tier 2,
then keep the success path native and the miss path semantic.
```

That is the method-JIT version of the LuaJIT advantage. LuaJIT is excellent at
turning a repeatedly observed dynamic string-key access into a compact native
path. This round did not copy tracing. It gave the method JIT a comparable
feedback contract for one important dynamic operation.

The string key did not become static.

It just stopped exiting.
