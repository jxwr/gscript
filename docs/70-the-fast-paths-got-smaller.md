---
layout: default
title: "The Fast Paths Got Smaller"
permalink: /70-the-fast-paths-got-smaller
---

# The Fast Paths Got Smaller

*April 2026 - Beyond LuaJIT, Post #70*

## The Shape Of This Round

Post #69 moved one important boundary:

```
live raw ints can survive a raw self-call without becoming boxed VM Values
```

This round applied the same rule in several smaller places:

```
numeric recursive calls return status in a register
float field stores stay in the FPR file
nested table float loads avoid materializing row SSA chains
no-return self tail calls loop inside Tier 1
two-field object literals construct their final shape directly
```

None of these is the final answer to "beat LuaJIT". They are the kind of
infrastructure changes that make the remaining gap easier to see. The method
JIT was still paying for boundaries that did not need to be crossed on the
successful path.

The result after the full guard:

```
ackermann:        0.016s -> 0.015s
fib_recursive:    0.607s -> 0.597s
matmul:           0.097s -> 0.086s
nbody:            0.065s -> 0.063s
sort:             0.045s -> 0.037s
binary_trees:     0.842s -> 0.751s
regressions:      0
```

The numbers are not equally stable. `sort` and `matmul` are the cleanest wins.
`binary_trees` is allocation-heavy and noisy, but the focused and full guards
both moved in the right direction. `ackermann` only moved by one millisecond,
which is useful but not enough to change the underlying diagnosis.

## Status Should Not Be A Memory Load

The private raw-int numeric entry had this convention:

```
X0..X3  raw integer arguments
X0      raw integer return
ctx.ExitCode records success or exit
```

That last line was too expensive for the success path. A recursive raw call
had to return, reload `ctx.ExitCode`, branch, then continue.

The new convention is:

```
X0..X3  raw integer arguments
X0      raw integer return
X16     numeric call status
```

On success:

```
X16 = 0
```

On deopt or exit:

```
X16 = ctx.ExitCode
```

Raw self and raw peer callers now branch on `X16` immediately after the BL or
BLR. The deopt epilogue still publishes `ctx.Regs` and still leaves the boxed
VM recovery surface coherent. The success path simply stops loading a field
from the execution context when the callee can return the status in a register.

This is a private Tier 2 numeric-entry contract. It should not leak into the
general boxed direct-entry ABI. The test suite now checks that raw self-call
shims branch on `X16` and do not reload `ctx.ExitCode` after the numeric BL.

Focused guard:

```
ackermann:         0.015s
fib:               0.087s
fib_recursive:     0.592s
mutual_recursion:  0.016s
nbody:             0.062s
```

This helped `fib_recursive` more than `ackermann`, which is expected.
`ackermann` still has deeper nested-call and fallback metadata costs.

## A Float Store Should Not Become A GPR Move

`nbody` already had scalar promotion and more FPR registers. The generated code
still had places where a float value resident in an FPR was written to a table
field through a GPR:

```
FMOV Xn, Dm
STR  Xn, [svals + off]
```

The table field is a boxed-value slot, but a float `runtime.Value` stores the
raw IEEE bits. If the value is already in an FPR and the field store has proven
the table shape, the hot store can be:

```
FSTR Dm, [svals + off]
```

The fallback path still boxes through the existing machinery. This is only the
native success path using the representation already in hand.

Focused guard:

```
nbody:              0.062s
table_field_access: 0.024s
binary_trees:       no regression
ackermann:          no regression
```

The production `advance` body is still much larger than LuaJIT's equivalent
loop. This patch removes one unnecessary representation hop, not the remaining
field-shape and loop-structure overhead.

## The Row Did Not Need A Name

`matmul` has a hot shape like:

```
b[k][j]
```

After typed array lowering, the method JIT could express this as a sequence:

```
load outer array data
load row table
load row header
load row length
load row data
load float element
```

That is a good general lowering, but it is more SSA than the inner loop needs
when the row table is single-use in the same block.

The new `TableNestedLoad` pass fuses this narrow shape:

```
mixed row table load -> float row element load
```

It keeps the row table pointer transient in scratch registers and avoids
materializing the row header, length, and data SSA chain. The pass is
intentionally conservative:

```
same block only
single use only
float row element only
side-effect span guarded
no cross-block row residency
```

That means explicit row reuse still stays explicit. The optimization targets
the `b[k][j]` transient form, not all nested arrays.

Focused guard:

```
matmul:             0.083s
table_array_access: 0.030s
spectral_norm:      0.022s
nbody:              0.062s
```

The full guard settled at `0.086s`, still well above LuaJIT's roughly `0.021s`.
The next matmul work is not "make this fusion wider" by default. It is bounds
check structure, row/data residency across the real loop shape, and register
pressure.

## The Tail Call Was Just A Loop

`sort` had a second recursive quicksort call with a simple shape:

```
self(args...)
return
```

This call does not need a full recursive native call if all of these are true:

```
static self call
fixed arity
exact parameter count
no vararg complications
followed immediately by a no-value return
runtime callee is still the current closure
```

Tier 1 now has a narrow fast path for that case. It checks the callee identity,
preserves the existing Tier 2 threshold exit, rewrites the current parameter
slots, and branches back to `pc_0`.

It does not reopen the unsafe path that replayed recursive Tier 2 calls after
table mutations. This matters because quicksort mutates the array. A failed
callee direct entry cannot replay a side-effecting partition as if nothing
happened.

Focused guard:

```
sort:               0.038s
table_array_access: 0.031s
fannkuch:           0.041s
ackermann:          0.016s
```

Full guard:

```
sort: 0.037s
```

This is still about 3.7x LuaJIT on the measured run. The remaining gap is not
only call overhead. It is the table-array compare/store path and the broader
recursive partition shape.

## The Object Literal Already Had Its Shape

`binary_trees` constructs many tiny objects:

```
{ left: ..., right: ... }
```

The old bytecode did this as:

```
NEWTABLE
SETFIELD left
SETFIELD right
```

That means each instance goes through two constructor-style field transitions.
The final shape is known statically.

The new bytecode is:

```
NEWOBJECT2 ctor, value0, value1
```

The prototype owns a cached two-field constructor descriptor:

```
key1
key2
final shape
```

The VM handler evaluates both values left to right, then allocates the table
with the final two-field shape in one pass. Runtime nil values still fall back
to normal `RawSetString` semantics, so a nil field is omitted exactly as it
would be with sequential `SETFIELD` bytecode.

Important boundary:

```
Table size did not grow.
```

The constructor descriptor lives on the function prototype, not on every table
instance.

Focused guard:

```
binary_trees:    0.747s
closure_bench:   0.024s
object_creation: 0.004s
nbody:           0.062s
```

Tier 2 currently lowers this opcode back to `NewTable + SetField` IR, so the
main win is VM/Tier 1 construction. That is acceptable for now because the
benchmark does not get a stable Tier 2 body for the allocation-heavy recursive
shape. The next step would be a true Tier 2 small-object allocation lowering,
not more bytecode special cases.

## Full Guard

The full guard after all five patches:

```
fib:                 0.088s
fib_recursive:       0.597s
sieve:               0.027s
mandelbrot:          0.052s
ackermann:           0.015s
matmul:              0.086s
spectral_norm:       0.023s
nbody:               0.063s
fannkuch:            0.042s
sort:                0.037s
sum_primes:          0.003s
mutual_recursion:    0.016s
method_dispatch:     0.001s
closure_bench:       0.029s
string_bench:        0.022s
binary_trees:        0.751s
table_field_access:  0.025s
table_array_access:  0.030s
coroutine_bench:     0.031s
fibonacci_iterative: 0.024s
math_intensive:      0.055s
object_creation:     0.004s
regressions:         0
```

Compared with the previous full guard:

```
sort:          0.045s -> 0.037s
matmul:        0.097s -> 0.086s
nbody:         0.065s -> 0.063s
fib_recursive: 0.607s -> 0.597s
ackermann:     0.016s -> 0.015s
binary_trees:  0.842s -> 0.751s
```

## What This Says About The Remaining Gap

This round did not prove that Tier 2 is automatically faster than Tier 1. It
proved the opposite again: a higher tier only wins when its contracts avoid
work. A method JIT that keeps crossing back into the VM representation will
lose to a lower tier with a tighter hot path.

The useful pattern was consistent:

```
do not load status from memory if the callee can return it in a register
do not move a float through a GPR if the store can consume an FPR
do not name a transient row table if the only use is the next load
do not recurse through a full call boundary when the tail call is a loop
do not replay shape construction when the final small shape is static
```

The next work is also clearer:

```
ackermann:
  nested raw return propagation and less fallback metadata traffic

matmul:
  bounds-check structure, row/data residency, and register pressure

sort:
  table-array compare/store path and safe recursive partition directness

binary_trees:
  real Tier 2 small-object allocation lowering

spectral_norm:
  identify the wall-time bottleneck, not just reduce instruction count
```

The fast paths got smaller. The hard part left is making the larger paths stop
looking like boxed VM execution with native syntax.
