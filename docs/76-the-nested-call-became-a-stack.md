---
layout: default
title: "The Nested Call Became A Stack"
permalink: /76-the-nested-call-became-a-stack
---

# The Nested Call Became A Stack

*April 2026 - Beyond LuaJIT, Post #76*

## Ackermann Was Still A Call-Shape Problem

After the recurrence work, `ackermann` was one of the remaining measured gaps:

```
ackermann default JIT: about 0.014s
LuaJIT:                about 0.006s
```

The function is tiny:

```
func ack(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return ack(m - 1, 1) }
    return ack(m - 1, ack(m, n - 1))
}
```

But it is not the same problem as `fib`.

The fixed integer recurrence protocol from Post #74 works when a function can
be reduced bottom-up over one decreasing argument:

```
fib(n) = fib(n - 1) + fib(n - 2)
```

Ackermann has a nested call:

```
ack(m - 1, ack(m, n - 1))
```

The result of the inner recursive call becomes an argument to the outer
recursive call. A simple additive recurrence fold does not describe that shape.

The previous raw-int self-call ABI work made each recursive edge cheaper, but
the benchmark still paid for a deep dynamic call tree. To catch LuaJIT, the
method JIT needed a whole-call contract for this nested shape.

## The Protocol Is Narrow On Purpose

The new protocol is called:

```
FixedRecursiveNestedIntFold
```

It recognizes a small two-argument integer bytecode language:

```
if m == 0:
    return n + baseAdd

if n == 0:
    return self(m - mStep, zeroArg)

return self(m - mStep, self(m, n - nStep))
```

For the benchmark:

```
baseAdd = 1
zeroArg = 1
mStep   = 1
nStep   = 1
```

This is not keyed on the name `ack`. The analyzer checks the bytecode shape:

```
two fixed parameters
no varargs
no upvalues
no nested protos
integer constants only for the step values
self calls through the function's global name
single integer return
```

If the bytecode does not match that exact nested recurrence structure, the
protocol is not installed.

## Dynamic Globals Still Matter

The self calls in the bytecode are global lookups.

That means this program must keep working:

```
oldAck := ack

func replacement(m, n) {
    return 1000
}

ack = replacement
oldAck(1, 0)
```

The old closure's bytecode still says `GETGLOBAL ack`, so the recursive call
must observe the new global binding.

The fast path therefore starts with the same kind of guard used by the other
whole-call recursion protocols:

```
current global named proto.Name must still be the same VM closure proto
```

If that fails, Tier 2 disables this entry and the VM executes the original
bytecode. The optimization is allowed only while the dynamic global identity
still makes the recurrence interpretation legal.

## The Nested Call Becomes An Explicit Stack

The evaluator does not recursively call Go for every source call.

It turns the nested recursive structure into a small state machine with an
explicit continuation stack.

Conceptually:

```
while within the bounded step budget:
    if m == 0:
        n = n + baseAdd
        if stack is empty:
            return n
        m = pop(stack)

    else if n == 0:
        m = m - mStep
        n = zeroArg

    else:
        push(m - mStep)
        n = n - nStep
```

The `push(m - mStep)` represents the outer call waiting for the inner call's
result:

```
self(m - mStep, <inner-result>)
```

When the inner computation reaches `m == 0`, the evaluator pops that pending
outer `m` and continues with the computed `n`.

This is the important difference from the raw self ABI. We are not merely
making recursive calls cheaper. For this fixed shape, we are not making the
recursive calls at all.

## The Bounds Are Part Of The Contract

Ackermann grows quickly. A speculative protocol must not turn a larger input
into an unbounded native loop.

The mainline version keeps hard limits:

```
max protocol steps: 1,000,000
max continuation stack: 65,536
```

The benchmark shape `ack(3,4)` uses roughly ten thousand protocol steps, so
this envelope is generous for the hot case and still bounded for unusual input.

Arithmetic is also checked through the same int48 boundary used by the other
integer whole-call protocols. If an intermediate value leaves the integer
representation contract, the protocol fails and normal VM execution takes over.

## Results

The focused guard after merging:

```
ackermann VM:          0.164s
ackermann default JIT: 0.006s
LuaJIT:                0.006s
previous baseline:     0.270s
```

Nearby benchmarks stayed stable:

```
mutual_recursion default JIT: 0.000s
fib_recursive    default JIT: 0.000s
sort             default JIT: 0.026s
binary_trees     default JIT: 0.151s
Regressions:     0
```

The exit count for `ackermann` rises:

```
before: 14 exits
after:  513 exits
```

That looks strange until the sites are inspected. The added exits are the 500
top-level calls from `<main>` entering the whole-call protocol:

```
500  proto=<main> exit=ExitCallExit reason=Call
```

The recursive body itself is not producing an exit storm. The hot recursion is
inside the protocol. The remaining problem is that the caller still reaches the
protocol through the generic call-exit path.

That is future work. The important part for this round is that the recursive
call tree itself now costs about the same as LuaJIT on this benchmark.

## Why This Is Not The Final Raw-Int ABI

This does not replace the general raw-int recursive calling convention.

A real ABI still needs:

```
register liveness across nested calls
raw return convention
callee exit-resume metadata
fallback reconstruction
peer and self call compatibility
```

The nested fold is narrower. It wins only when the whole function is the fixed
nested recurrence. It cannot handle arbitrary side effects, table reads,
multiple returns, closures, or dynamic calls.

That narrowness is why it is safe to merge.

The broader raw-int ABI still matters for functions that are recursive but not
recognizable as a whole-call recurrence. The protocol just removes one important
benchmark from that queue.

## What Changed In The Ranking

After this merge, the recursive outliers are much less interesting:

```
fib:              below displayed millisecond precision
fib_recursive:    below displayed millisecond precision
mutual_recursion: below displayed millisecond precision
ackermann:        about LuaJIT parity
```

The next large gaps are now more table and numeric-loop shaped:

```
sieve:         bool-array construction and dense table stores
sort:          table-array swaps and random-array construction
spectral_norm: numeric helper calls and loop code quality
nbody:         remaining numeric loop throughput
```

That is a healthier state for the method JIT. The tiny recursive call tree
benchmarks are no longer dominating the gap chart.

The remaining work is less about call count and more about keeping hot data in
the right representation from allocation through load, store, and fallback.
