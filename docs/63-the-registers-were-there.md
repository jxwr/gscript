---
layout: default
title: "The Registers Were There"
permalink: /63-the-registers-were-there
---

# The Registers Were There

*April 2026 - Beyond LuaJIT, Post #63*

The previous `nbody` round fixed a compiler legality problem. Scalar promotion
wanted to keep the hot float fields of the body records in SSA, but one
production loop exit landed in a shared block. The pass learned to split that
exit edge and materialize promoted fields on a dedicated path.

That made the IR finally look like the optimization we wanted.

The next profile said the machine still disagreed.

After scalar promotion, the production warm dump for `nbody.advance` still had
five spill slots in the hot loop. The values were promoted, but the register
allocator did not have enough float registers to keep them all resident. The
result was an awkward middle state: table field traffic was reduced, but the
loop was now paying a different memory tax through float spills and reloads.

This round removes that bottleneck:

```
nbody  7-run focused guard, same machine

before: 0.077s
after:  0.064s
```

The worker's interleaved 30-run measurement saw the same shape:

```
nbody: 0.076s -> 0.065s
```

Local LuaJIT for the same guard was still around 0.033s, so this is not the end
of the chase. But the gap moved from roughly 2.3x to roughly 1.9x on this run,
and the change is not a benchmark-name branch. It is a register-allocation and
field-codegen correction that other float-heavy method-JIT code can use.

## The Spill Was The New Table Access

The scalar-promotion work changed the form of the problem. Before it, too many
hot `body.x` and `body.vx` values lived behind field loads and stores. After it,
those values were loop-carried float phis.

That is the right representation for optimization, but it is also a demand on
the allocator. `nbody.advance` has a dense cluster of live float values:

```
positions
velocities
distance deltas
inverse distance
mass-scaled updates
loop-carried promoted fields
```

The Tier 2 allocator only had eight allocatable FPRs:

```
D4-D11
```

That was enough for smaller numeric loops. It was not enough for the promoted
`nbody` loop. Once the float phis landed in SSA, the allocator had to spill five
values. The production dump made the problem concrete:

```
spill slots: 5
```

A spill is less obvious than a table lookup in an IR dump, but it is still a
memory operation in the hot path. In a loop whose whole purpose is to turn table
fields into register-resident floats, leaving five promoted values in memory is
a sign that the compiler stopped one step too early.

## Widening The FPR Pool

The fix widens the Tier 2 floating-point allocation pool:

```
before: D4-D11
after:  D4-D11, D16-D23
```

That doubles the allocator's float budget from eight to sixteen registers. The
production `nbody.advance` dump after the patch:

```
spill slots: 5 -> 0
insns:       2037 -> 1971
code bytes:  8148 -> 7884
loads:        290 -> 279
stores:       391 -> 386
```

The high registers are not magic. The safety question is the ARM64 calling
convention. `D8-D15` are callee-saved. `D16-D23` are caller-saved. If a compiled
function uses `D16-D23`, it cannot assume a normal call preserves them.

That sounds risky until you look at the actual boundaries in this JIT.

The ordinary Tier 2 prologue already saves the callee-saved float registers it
uses:

```
D8-D11
```

It does not need to save `D16-D23` in the prologue because the ABI does not
require callees to preserve caller-saved registers for their caller. The code
that matters is the code around native calls emitted inside Tier 2. Those paths
already compute the set of live SSA values across the call and selectively spill
the corresponding GPR and FPR allocations before the `BLR`, then reload them
afterwards. That machinery is value-based, not register-number based:

```
active FPR SSA value -> physical FPR -> home slot
```

So widening the pool does not require a new "save D16-D23 everywhere" rule. It
requires the existing call-boundary spill/reload to keep working for any FPR in
the allocation set. The exit-resume path has the same property: it stores active
float SSA values through their physical FPR and reloads by value ID when
execution resumes.

That is the architectural reason this patch is acceptable. It is not simply
"there are more registers on the chip." It is "the compiler's call and exit
protocol is already expressed in terms of live SSA values, so the physical FPR
set can grow without changing the semantic boundary."

## A Smaller Field-Codegen Cleanup

The same patch also removes redundant table checks from field access code when
the IR producer already proved the value is a table.

The field access fast path needs two different facts:

```
the value is a table pointer
the table has the expected shape
```

The old field emitter often rechecked both. That was conservative, but once
earlier lowering has produced a `TypeTable` value, the table-ness proof is
already part of the IR type. The field emitter now has a single helper that
prepares the raw table pointer and returns whether the shape was already
verified in the current block:

```
TypeTable producer: skip redundant tag/subtype table check
same shape already verified: skip the shape guard too
otherwise: check table, extract pointer, check shape
```

The helper still checks the shape before direct field access unless the same
shape was already verified. It also still falls back to deopt on mismatch. This
is a code-size and branch-count cleanup, not a semantic shortcut.

That distinction matters. `TypeTable` says the value is a table. It does not
say the table has the field shape that a cached `GetField` or `SetField` wants.
The patch keeps those two facts separate.

## Why This Came After Scalar Promotion

It would have been premature to widen the FPR pool before scalar promotion was
working on the production CFG. The allocator only showed the real problem once
the field values were promoted into SSA. Before that, the hot loop was dominated
by table access and exits. More registers would not have fixed the missing
promotion.

This ordering is a useful compiler pattern:

```
first, make the optimized representation legal
then, make the optimized representation cheap
```

The exit-edge split made the representation legal. The widened FPR pool made it
cheap enough to matter.

The same rule applies in the other direction too. If widening the register pool
had caused native calls or exits to lose live float state, the faster `nbody`
number would not be a valid win. A JIT speedup that survives only on call-free
benchmarks is not infrastructure. The reason this can be infrastructure is that
the existing boundary protocol is live-value based.

## The Result

The focused guard on the merge machine:

```
before:
  nbody        0.077s
  mandelbrot   0.051s
  matmul       0.091s

after:
  nbody        0.064s
  mandelbrot   0.050s
  matmul       0.091s to 0.094s
```

The 3-run wider guard also kept the nearby numeric benchmarks stable:

```
spectral_norm       0.025s
table_array_access  0.031s
math_intensive      0.053s
```

`matmul` still shows the usual local noise, but the regression guard did not
flag a regression and the focused 7-run comparison kept it in the same band.
The important change is `nbody`: the median moved by about 17% on the local
7-run guard and by about 14.5% in the worker's interleaved measurement.

The production dump matches the timing:

```
spill slots disappeared
instruction count fell
loads and stores fell
```

That is the kind of evidence I trust more than a single timing number. The
number improved because the machine code stopped doing work the IR no longer
needed.

## The Remaining Gap

LuaJIT is still faster. On this run:

```
GScript method JIT: 0.064s
LuaJIT:             0.033s
```

That is now close enough that the remaining work is less about one obvious
blocked pass and more about accumulated success-path quality:

```
float scheduling
field materialization minimization
loop-carried value placement
native branch layout
table-shape proof propagation across more block boundaries
```

The encouraging part is that the method-JIT direction is still holding. We did
not need a trace compiler to find this win. We needed the method compiler to
carry table and float facts far enough that a normal register allocator could
see the pressure, then give that allocator the hardware registers the target
already had.

The registers were there. The compiler just had to be allowed to use them.
