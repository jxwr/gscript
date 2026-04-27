---
layout: default
title: "The Load Split Into Four"
permalink: /57-the-load-split-into-four
---

# The Load Split Into Four

The dense-table work did not begin with a new table representation.

That had already happened.

Post 54 made fresh dense tables become typed before feedback had time to catch
up. A table born from a list of integers could allocate an integer array. A
table born from floats could allocate a float array. Tier 1 append stores could
extend those typed arrays when capacity was available, and Tier 2 could see the
type hints soon enough to avoid treating every array access as a dynamic hash
operation.

Post 53 made matmul's rows visible as tables. The compiler could prove that
`a[i]` and `b[k]` were row tables rather than arbitrary values.

Those two posts created the opportunity. They did not cash it in.

The remaining problem was that Tier 2 still represented a dense table read as
one large operation:

```text
v = GetTable table, key
```

That is a convenient IR node for the frontend. It is not a good IR node for an
optimizer.

## The node was too large

A dense array load has several distinct facts inside it.

First, the table must be a table.

Then the array backing has a shape: integer array, float array, boolean array,
mixed array, or no useful dense representation.

Then the integer key must be in bounds.

Then the backing pointer is loaded.

Then the element is loaded.

In the old IR, all of those facts were trapped behind `GetTable`. Load
elimination could not share the table header with the next access. LICM could
not hoist the dense-array pointer out of an inner loop, because the pointer was
not a value. Bounds checks could not be separated from the actual load.

The machine code had fast paths for typed array reads. The optimizer could not
name the components of those fast paths.

That is the whole shape of the round: stop asking one node to describe four
different effects.

## The split

The new lowering pass rewrites monomorphic dense array loads into four smaller
operations:

```text
TableArrayHeader table
TableArrayLen    header
TableArrayData   header
TableArrayLoad   data, key
```

The exact names are less important than the ownership boundary.

`TableArrayHeader` is the guarded table-array fact. It says that this object is
a table with the dense backing kind the feedback promised.

`TableArrayLen` is the array length.

`TableArrayData` is the pointer to the element storage.

`TableArrayLoad` is the actual indexed element read.

Once those become separate SSA values, existing compiler machinery starts
working without a benchmark-specific rule. Load elimination can see that two
loads from the same table have the same header. LICM can hoist the header, len,
and data values when the loop does not mutate the table in a way that invalidates
them. Codegen can consume the same lowered shape for int, float, bool, and mixed
array paths instead of growing a separate optimization for every benchmark.

That is the important part. The pass is not "optimize matmul." It is "make the
dense array protocol visible to the optimizer."

## Why this was blocked before

The earlier typed-table patches had already reduced a large amount of dynamic
work. They taught the runtime and feedback layers better facts.

But they stopped at the call into Tier 2. Once the graph was built, the compiler
still had to reason about an opaque `GetTable`. The feedback said the access was
monomorphic. The emitter could generate a typed fast path. In between, the
middle-end could not share or move any part of the access.

This matters most in nested loops.

In `table_array_access`, the same array backing is touched many times. Reloading
the same table header and length in the hot loop is pure waste.

In `matmul`, row tables are loaded outside or at the edge of the inner loop, and
then element loads happen repeatedly. The compiler needs to keep the row and
backing-array facts stable while the arithmetic runs.

In `sieve`, table stores complicate the picture. Some accesses can still be
lowered, but LICM has to be conservative because writes can mutate the same
table. Splitting the node helps the analysis say "this part is stable" and "this
part still depends on the current key" instead of treating the entire access as
one indivisible effect.

## The pass boundary

The lowering pass runs after the compiler already has enough feedback to know
the array kind.

It does not invent type information. It consumes it.

That keeps the blast radius small. A table access that is not monomorphic stays
as `GetTable`. A table access whose key is not a usable integer index stays on
the existing path. A table access that might require dynamic fallback still has
the same exit-resume machinery available.

The new IR ops were then added to the existing shared passes:

```text
LoadElim
LICM
direct-deopt classification
interpreter fallback
ARM64 emitter
```

This was the part that mattered for architecture. If the split nodes only
worked in one emitter function, the pass would be another island. Instead, they
became ordinary IR values with the same responsibilities as other typed loads:
validate, optimize, emit, and resume.

That is also why the change had to touch the interpreter. The interpreter path
is not the performance path, but every IR op needs a semantic definition. If a
future diagnostic or test executes the lowered IR without native codegen, it
must mean the same thing.

## The number that mattered

The focused guard after the patch looked like this:

```text
matmul                 0.095s
table_array_access     0.030s
sieve                  0.027s
nbody                  0.076s
fannkuch               0.041s
math_intensive         0.057s
fibonacci_iterative    0.231s
sort                   0.050s
regressions            0
```

The full guard was consistent:

```text
matmul                 0.094s
table_array_access     0.030s
sieve                  0.027s
nbody                  0.075s
fannkuch               0.041s
math_intensive         0.056s
ackermann              0.017s
binary_trees           1.008s
regressions            0
```

After the semantic-entry fix landed on top, the focused guard still held:

```text
math_intensive         0.059s   baseline 0.070s   -15.7%
fannkuch               0.039s   baseline 0.049s   -20.4%
matmul                 0.091s   baseline 0.123s   -26.0%
table_array_access     0.031s   baseline 0.097s   -68.0%
ackermann              0.016s   baseline 0.270s   -94.1%
fibonacci_iterative    0.243s   baseline 0.291s   -16.5%
regressions            0
```

`table_array_access` is the cleanest proof that the abstraction is right. The
benchmark is mostly dense array traffic. Making dense array facts visible to the
middle-end moves it from the old 0.09s range to about 0.03s.

`matmul` is the more useful number. It still has a large LuaJIT gap, but it
moved in the right direction without a matmul-only rule. The compiler can now
see row tables, split their dense loads, and let general passes reuse those
facts.

That is exactly the kind of progress this project needs. LuaJIT is not beaten by
adding one trick per benchmark. It is beaten by turning runtime facts into
compiler facts early enough that the ordinary optimizer can use them.

## The bug found next

The same round also found a serious wrong-code in `math_intensive`.

At first it looked like a raw-int recursive ABI bug. The symptom was dramatic:

```text
VM:  gcd(10000) total=4135964
JIT: gcd(10000) total=-120460332615027
```

That was alarming because the previous raw-int work had just opened a path for
integer recursive kernels. It would have been easy to blame the new ABI.

The dump said otherwise.

`gcd_bench` was not using the raw peer path. Its call result was still typed as
`any`, so the raw peer emitter rejected it. The failing path was the boxed native
direct entry into `gcd`.

The optimized `gcd` graph no longer had its semantic entry at `B0`:

```text
B3 (entry):
    LoadSlot a
    LoadSlot b
    Jump -> B4

B4:
    ConstInt 0
    Jump -> B0

B0:
    loop header
```

LICM had introduced a preheader. The entry block was `B3`. The direct-entry
emitter still jumped to literal `B0`.

So direct calls skipped the parameter loads and the zero constant setup, then
entered the loop with stale register state. The fix was not a modulo fix and not
a gcd special case. It was to make every Tier 2 entry branch to `Function.Entry`,
the same semantic entry that numeric entry already used.

That fix belongs in this blog because it explains the real rule for this stage
of the project: every optimization pass that rewrites control flow must update
the assumptions at the native ABI boundary. If codegen thinks `B0` means
"entry", and the optimizer thinks `Entry` means "entry", the compiler is already
wrong before the first instruction runs.

## The hardening

The raw-int side still got a hardening patch.

When forcing a boxed raw-int kernel into the raw ABI, integer modulo is now
lowered to `ModInt` when all operands are `TypeInt`. After forcing, the compiler
rejects the raw-int ABI if residual generic numeric ops remain:

```text
Add
Sub
Mul
Div
Mod
Unm
```

That is deliberately conservative. If the raw-int ABI claims that a function is
operating on raw integers, the IR should not contain a generic numeric operation
that expects boxed values or mixed int/float behavior. Either the operation is
lowered into the raw-int protocol, or the function stays out of that ABI.

This is not the final peer-call optimization. In fact, the dump proved that
`gcd_bench` still did not enter the intended raw peer path because `OpCall`
remains `TypeAny`. The next version needs a general call-result protocol:
resolved stable callee, fixed integer parameters, integer return, guardable
callee identity, boxed fallback frame on miss, and exact exit-resume metadata.

But that is a later patch. The correct order is:

1. make boxed direct entry correct for all control-flow shapes;
2. harden raw-int kernel IR so the ABI cannot lie;
3. only then mark eligible stable calls as raw-int returns.

That order is slower than forcing the fast path immediately. It is also the only
order that keeps the compiler trustworthy.

## Where this leaves the LuaJIT gap

The dense-array split closes one kind of gap: repeated table-array metadata
loads that LuaJIT's trace compiler naturally hoists out of the trace.

Method JIT has to earn that effect differently. It cannot rely on a single
linear trace seeing the exact hot path. It has to expose the facts in the IR so
block-based passes can move and share them safely.

That is why this patch matters more than its line count. It gives method JIT a
better internal language for dense tables.

The next table work is not mysterious:

```text
matmul: reduce remaining array exits and row-load overhead
fannkuch: separate mutation-stable reads from mutation-sensitive writes
sort: prove or recover around table swaps without replaying side effects
```

The next call work is also clear:

```text
stable int-return call typing
raw peer success path
precise fallback materialization
callee exit attribution
```

The dense table load split does not beat LuaJIT by itself. It removes a layer of
opacity that prevented method JIT from competing on table-heavy loops.

That is the pattern worth keeping: make the runtime fact explicit, give it a
small IR shape, let ordinary passes optimize it, and only then measure whether
the machine code got closer to LuaJIT.
