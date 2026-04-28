---
layout: default
title: "The Live Value Never Boxed"
permalink: /69-the-live-value-never-boxed
---

# The Live Value Never Boxed

*April 2026 - Beyond LuaJIT, Post #69*

## Where We Left Off

In [Post #68](68-the-result-stayed-raw), the method JIT stopped dropping a few
important facts at pass boundaries:

```
raw self-call results stay TypeInt
raw self ABI facts live in compiled-function metadata
typed array loads preserve their element type after lowering
pure typed numeric expressions can be reused inside a block
small closures avoid a second upvalue-slice allocation
```

That round made the contracts clearer. This round used one of those contracts
to remove more work from the recursive success path.

The problem was not the self-call arguments. Those were already raw:

```
X0..X3 -> callee raw-int parameters
X0     -> callee raw-int return
```

The problem was everything that stayed live across the recursive call.

## The Old Safe Path

For a normal native call, Tier 2 has to protect live SSA values across the BLR.
The callee is free to use the same machine registers. So the caller spills any
live register value to its VM home before the call, then reloads it afterward.

For boxed values, that is natural:

```
store NaN-boxed Value to regs[slot]
call callee
load NaN-boxed Value from regs[slot]
```

For raw integer values, the old raw self-call path did this:

```
raw int in GPR
box raw int into NaN-boxed Value
store boxed Value to VM home
call raw self entry
reload boxed Value
unbox back to raw int
```

That is correct. It also defeats the point of the raw self-call path when the
same live values survive across many recursive calls.

The call arguments stayed raw. The live temporaries did not.

## The New Rule

The new rule is:

```
if a live-across-call value is already a raw int in an allocated GPR
and it has a VM home slot for fallback materialization
then spill the raw register to the raw self-call stack frame
do not box it into the VM register file on the hot path
```

The hot path becomes:

```
STR raw GPR -> raw self stack spill
BL t2_numeric_self_entry_N
LDR raw GPR <- raw self stack spill
```

No NaN-boxing. No VM home write. No reload-and-unbox.

The fallback path still has the full boxed contract:

```
publish caller ctx.Regs
load raw spill from stack
box raw int
store boxed Value to regs[slot]
materialize raw self-call arguments
enter ExitCallExit / numeric resume path
```

That is the important separation. The success path is register/stack raw. The
fallback path reconstructs the boxed VM ABI only when Go is about to observe
the context.

## Why Stack, Not VM Homes?

The VM register file is the interpreter ABI. It stores boxed `runtime.Value`
entries. It is the right recovery surface for exit-resume and fallback.

It is not the cheapest place to preserve a raw native temporary across a known
raw-native recursive call.

The raw self-call shim already has a tiny native stack frame for fallback
arguments:

```
raw arg 0
raw arg 1
raw arg 2
raw arg 3
```

This patch extends that frame with raw live spills:

```
raw args
raw live spill 0
raw live spill 1
...
```

The frame size remains aligned, and the list of raw live spills is sorted by
SSA value id to make emission deterministic. Values that are not raw ints,
not active in a GPR, or do not have a VM home stay on the older boxed spill
path. Floating-point live values also stay on the existing path for now.

That fallback restriction is deliberate. A raw stack spill is only useful if
the runtime can still reconstruct the boxed context on a miss.

## The Exit Contract

This patch builds on the lazy `ctx.Regs` rule from Post #68.

The success path still avoids publishing the caller base:

```
restore caller mRegRegs from callee base
reload raw live stack spills
pop raw self shim frame
restore boxed-spilled lives
store raw return
continue
```

The callee-exit and fallback paths do more work:

```
restore caller mRegRegs
publish ctx.Regs
box raw live stack spills into VM homes
box raw call arguments into the call frame
emit ExitCallExit with numeric resume
```

This keeps the invariant clean:

```
native success:
  may keep raw values out of the VM register file

Go-visible fallback/exit:
  must expose a coherent boxed VM frame
```

That is the protocol we need for a general raw-int self-recursive ABI. The
hot path should not pay for recovery state eagerly, but the recovery state
must still be exactly reconstructible.

## The Tests

The new test uses a recursive function with a live raw local across the self
call:

```
func carrydown(n, acc) {
    if n == 0 { return acc }
    carry := acc + 3
    r := carrydown(n - 1, acc + 1)
    return r + carry
}
```

Then it forces enough depth to exercise fallback pressure:

```
n = maxRawSelfCallDepth + 8
```

The test checks two things.

First, the JIT result must match the VM result after the fallback path has
materialized the raw live spill into its boxed VM home.

Second, the emitted raw self-call shim must contain a raw stack store for a
live allocated GPR before the BL, without also storing that live value to the
VM register file on the hot side.

The existing tests also had to be generalized. Raw self-call frame size is no
longer only a function of arity. It can grow when there are raw live spills:

```
frame size = aligned(args area + live raw spill area)
```

So the tests now accept a range of valid raw self frame sizes, bounded by the
number of allocatable GPRs.

## The Numbers

Focused guard after the merge:

```
fib:
  default JIT: 0.089s
  LuaJIT:      0.025s

fib_recursive:
  default JIT: 0.615s
  no-filter:   0.600s

ackermann:
  default JIT: 0.016s
  LuaJIT:      0.006s

mutual_recursion:
  default JIT: 0.017s
  LuaJIT:      0.005s

nbody:
  default JIT: 0.065s
  LuaJIT:      0.034s
```

This does not close the recursive gap by itself. It does remove a class of
avoidable work from the raw self-call success path, and it does so without
weakening fallback correctness.

The benchmark that showed the clearest movement in this run was
`fib_recursive`, down into the low `0.6s` range. `ackermann` is still limited
by deeper structural costs around nested calls and fallback metadata.

## The Same Round Also Touched Matmul

The other patch in this round was smaller and more local: typed float table
array loads now load directly into an FPR.

Before:

```
LDR  X0, [data + index*8]
FMOV D0, X0
```

After:

```
LDR  D0, [data + index*8]
```

The assembler gained register-offset FP load/store helpers, and the typed
array emitter uses them for `FBKindFloat` loads when the IR result is
`TypeFloat`. Stores also use an FP store when the value is already resident in
an FPR.

Focused guard:

```
matmul:
  before this patch family: about 0.096s
  after direct FP load:     about 0.087s
  LuaJIT:                   about 0.021s

table_array_access:
  stayed around 0.030s

spectral_norm:
  stayed around 0.022s

nbody:
  stayed around 0.065s
```

This is not the matmul breakthrough. It is a correct local cleanup after
typed array lowering finally preserved float element types. The next matmul
work is still row-table residency, array-header/data reuse, bounds-check
structure, and register pressure.

## What This Round Proved

The method JIT can keep moving without switching to trace compilation, but the
contracts must keep getting sharper.

The old recursive path paid for fallback all the time:

```
box live raw values now
maybe need fallback later
```

The new path pays only when fallback is real:

```
keep live raw values raw on success
materialize boxed homes on fallback/exit
```

That is the general pattern.

For raw self recursion, the next step is not another special case for
ackermann. It is extending this protocol:

```
more precise live value classes
raw return propagation through nested call shapes
less per-call depth/bookkeeping traffic
fallback descriptors that materialize only the slots actually observed
```

For typed arrays, the same lesson applies:

```
keep typed values in the representation the next instruction wants
fall back to boxed Values only at the boundary
```

The live value did not need to become a boxed VM value just to survive a raw
self call.

Now it does not.
