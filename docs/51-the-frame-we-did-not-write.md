---
layout: default
title: "The Frame We Did Not Write"
permalink: /51-the-frame-we-did-not-write
---

# The Frame We Did Not Write

The raw-int call boundary had one more boxed habit left.

Raw self calls were already separate from the boxed VM ABI. They pass integer
arguments in `X0..X3`, return the integer in `X0`, and materialize a boxed
fallback frame only when the raw path cannot continue.

Raw peer calls were newer. They let one Tier 2 raw-int function call another
raw-int function directly. The shape was right, but the successful path still
did extra work:

```text
box raw arg 0
store into callee VM window
box raw arg 1
store into callee VM window
...
BLR callee_numeric_entry
```

That VM window is needed for fallback. It is not needed for the native success
path.

The callee is about to read raw arguments from registers. Writing boxed copies
to the VM register file before the call only pays the fallback cost on every
successful call.

## Why it existed

The original implementation was conservative for a good reason. If a raw peer
call fails, the existing call-exit machinery expects a normal boxed call frame:

```text
regs[funcSlot]     = function
regs[funcSlot + 1] = boxed arg 0
regs[funcSlot + 2] = boxed arg 1
...
```

Pre-writing the callee window made fallback simple. The mistake was doing it
before knowing fallback was needed.

That is the same theme as the raw self ABI work: the happy path and fallback
path must have separate contracts. Sharing a boxed VM convention because it is
convenient usually means the optimized path is still paying the interpreter's
tax.

## The new rule

Raw peer success now keeps the arguments in registers until the native call:

```text
X0..X(N-1) = raw args
BLR callee_numeric_entry
result = X0
```

The raw args are still saved in the thin native frame metadata required for
fallback and exit-resume. If the call cannot complete as raw, the fallback path
boxes those saved raw args and materializes the normal VM call frame at that
point.

That distinction matters:

- success path: register-only arguments;
- fallback path: boxed VM frame materialized on demand;
- return path: raw result in `X0`, then the caller's representation checks.

This is not a new benchmark trick. It is one more step toward making the raw
ABI an actual convention instead of an optimized body glued to a boxed call
boundary.

## The test is machine code

The regression test compiles a raw peer-call shape and scans the generated
ARM64 code.

It looks for the raw peer shim frame setup, then verifies that before the
`BLR` instruction there is no store into `mRegRegs`. That directly tests the
property that matters:

> the raw peer success path must not box and store arguments into the callee VM
> window before the native call.

Behavioral tests are still necessary, but they would not catch this kind of
performance regression. The program would compute the same result while quietly
paying unnecessary stores on every call.

## The numbers

The effect is intentionally small and localized. It removes fixed overhead from
raw peer calls; raw self recursion was already cleaner.

Focused diagnostics after integration:

```text
ackermann default:        0.017s
fib_recursive default:    0.659s
mutual_recursion default: 0.016s
math_intensive default:   0.055s
```

The branch comparison against its baseline showed the intended direction:

```text
fib_recursive default: 0.673s -> 0.661s
math_intensive default: 0.056s -> 0.054s
```

This does not close the LuaJIT gap by itself. It removes a piece of boxed
state traffic from a path that should never have been boxed on success.

That is how the remaining call-boundary work should proceed: identify the
fallback state precisely, keep it available, and stop writing it on the common
path.

Commit: `4df86c0 methodjit: defer raw peer arg boxing to fallback`
