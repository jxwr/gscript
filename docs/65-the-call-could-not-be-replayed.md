---
layout: default
title: "The Call Could Not Be Replayed"
permalink: /65-the-call-could-not-be-replayed
---

# The Call Could Not Be Replayed

*April 2026 - Beyond LuaJIT, Post #65*

The method JIT has been getting more aggressive about native calls:

```
Tier 1 -> Tier 1 direct entry
Tier 1 -> Tier 2 direct entry
Tier 2 -> Tier 2 native call
Tier 2 static self-call
raw-int self-recursive entry
raw peer numeric entry
```

Each one removes interpreter call overhead. Each one also creates a sharper
contract. This round fixed a contract violation that was easy to miss because
the fast path usually worked until an exit happened in the callee.

The bug was not "the callee exits." Exits are normal.

The bug was that a native caller could not resume the callee at the callee's own
exit-resume point. If the callee exited, the caller recovered by replaying the
whole call from the beginning. That is only correct if the callee has not
performed a visible side effect before the exit.

If it has, replaying the call duplicates the side effect.

## The Shape

The minimal cross-call case:

```
state := {x: 0}

func bump_then_exit(t) {
    t.x = t.x + 1
    tmp := {}
    tmp[1] = 7
    return t.x + tmp[1] - 7
}

func caller(f, t) {
    return f(t)
}
```

The table field update is visible:

```
t.x = t.x + 1
```

The later table allocation/store can exit:

```
tmp := {}
tmp[1] = 7
```

If `caller` enters `bump_then_exit` through a native direct entry and the callee
exits after incrementing `t.x`, the caller does not know how to resume inside
`bump_then_exit`. It can only fall back around the call site. That means it
calls `bump_then_exit` again from PC 0.

Before the fix, the result was:

```
state.x == 2
```

The correct result is:

```
state.x == 1
```

The recursive case has the same problem. A self-recursive native call can replay
the recursive call after the caller has already mutated state. The test that
used to fail returned three increments where two were correct.

## Why Normal Tier 2 Is Still Fine

This patch does not say "functions with side effects cannot be Tier 2."

They can. The important distinction is the entry protocol.

The normal TieringManager path owns the full execution protocol. It can enter a
compiled function, observe an exit, run the correct fallback, and resume or
return through the machinery that understands that compiled function.

A native direct-entry caller has a narrower contract. It jumps to the callee and
expects either:

```
the callee succeeds and returns normally
```

or:

```
fallback at the caller's call site can safely replay the call
```

That second line is the key. Replaying a call is not the same thing as resuming
inside the callee. It is only legal when the callee's partial execution has not
made a visible change.

So the patch keeps unsafe functions compileable but stops publishing their
direct BLR entries:

```
Tier 2 code exists
normal execute path can use it
DirectEntryPtr stays zero
Tier2DirectEntryPtr stays zero
```

Native callers then go through the safer route instead of jumping directly into
a callee whose exits they cannot resume precisely.

## The Analysis

The new replay-safety analysis is intentionally conservative. It walks the Tier
2 IR and tracks whether a path has seen a native-visible side effect. If a later
instruction on that path may exit, the function is not safe for native direct
entry publication.

In rough form:

```
seenSideEffect = false

for each path:
    if seenSideEffect and op may exit:
        native direct entry is unsafe
    if op has visible side effect:
        seenSideEffect = true
```

Visible side effects include operations such as:

```
SetTable
SetField
SetGlobal
SetUpval
matrix stores
channel send/receive
go statement
```

There is one important exception. Stores into tables that are proven local
allocations do not count as externally visible in this context. A freshly
allocated temporary table that has not escaped can be mutated before a later
exit without duplicating user-visible state.

That exception matters because many optimized functions allocate scratch tables
or locally build arrays. Treating every local store as globally visible would
turn the gate into a broad performance veto.

The analysis is still conservative about may-exit operations:

```
calls
table accesses
field accesses
allocations
guards
integer overflowable arithmetic
global/upvalue operations
channel operations
```

That is deliberate. This is a correctness gate on entry publication, not an
optimization pass trying to prove every possible safe case.

## Static Self-Calls

The same rule applies to static self-call native lowering.

It is tempting to think self-calls are different because the caller and callee
are the same function. They are not different for this bug. If a recursive call
enters the native self-entry and exits after a side effect, the caller still
cannot resume the callee at the callee's internal exit point. Replaying the
recursive call can still duplicate visible work.

So `emitOpCall` now checks the same replay-safety flag:

```
if static self-call and function is not replay-safe:
    use the call exit path
else:
    native self-call lowering may proceed
```

This preserves the fast raw recursion path for functions such as `ackermann`
that do not perform visible side effects before exits. The focused guard after
the patch still showed:

```
ackermann    0.016s
```

That is the right outcome. The gate blocks unsafe replay, not recursion itself.

## The Tests

Two regression tests were added.

The cross-call test proves a callee with:

```
visible table field update
later exit-capable table operation
```

does not publish direct entries, and that the caller observes one increment
instead of two.

The recursive test proves the same property for static recursion. It also
asserts that unsafe recursive direct entries are not published.

These tests are more important than the benchmark numbers. Without them, later
work on sort, fannkuch, table mutation gates, or direct-entry caching could
reopen the same class of bug while looking like a performance win.

## The Result

Focused guard after the merge candidate:

```
ackermann             0.016s
fib_recursive         0.663s
mutual_recursion      0.016s
closure_bench         0.027s
sort                  0.046s
method_dispatch       0.001s
fibonacci_iterative   0.025s
```

No regressions were flagged. The important bit is that the recursion-heavy raw
paths stayed open where they are safe.

The no-filter checks that originally exposed this family of bugs now have a
clear invariant:

```
native direct entry is an ABI contract, not a permission slip
```

If the callee cannot be resumed precisely after an exit, native entry is only
legal when replaying from the call boundary cannot duplicate visible effects.

## The Lesson

The method JIT can keep moving toward LuaJIT without becoming a trace JIT, but
only if its entry protocols are explicit. A trace recorder naturally owns a
linearized exit point. A method JIT has to build that precision through call
ABIs, liveness, fallback materialization, and resume rules.

This patch is one of those rules:

```
publish direct entries only when caller-boundary replay is semantically valid
```

It does not make a headline benchmark faster today. It makes the next set of
performance patches safer. That is not secondary work. It is what allows the
compiler to keep opening faster native paths without hiding wrong-code behind a
good timing number.
