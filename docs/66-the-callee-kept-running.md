---
layout: default
title: "The Callee Kept Running"
permalink: /66-the-callee-kept-running
---

# The Callee Kept Running

*April 2026 - Beyond LuaJIT, Post #66*

## Where We Left Off

In [Post #65](65-the-call-could-not-be-replayed), the method JIT learned a
hard rule about native calls: a caller may not replay a callee after the callee
has already performed visible work.

That fix was necessary. It closed a real correctness hole. A native direct
call could enter a callee, the callee could mutate a table, and a later exit
could make the caller run the entire callee again from bytecode PC 0. The
program still returned a value, but it had duplicated a side effect.

The conservative fix was to stop publishing native direct entries for functions
that were not replay-safe:

```
function has side effect before a later possible exit
    => do not publish DirectEntryPtr
```

That was correct. It was also expensive.

The next benchmark run made the cost impossible to ignore:

```
nbody(500K):
  before the gate:     0.066s
  after the gate:      1.526s
  ExitCallExit count:  500013
```

The correctness patch had turned nbody into a call-exit benchmark. Every loop
iteration crossed back into Go around the same hot call.

The bug was fixed. The architecture was not finished.

## Replay Is Not Resume

The key mistake was treating two different properties as one property.

The first property is replay safety:

```
If the callee exits, can the caller safely run the callee again from the start?
```

That is the property Post #65 guarded. If the callee has already mutated visible
state, the answer is no.

The second property is resume safety:

```
If the callee exits, can the runtime handle the callee's own exit and continue
from the callee's own resume point?
```

That is a much weaker requirement. A callee that is not replay-safe may still
be resume-safe. In fact, that is the normal compiled-function contract. Tier 2
code exits all the time. The TieringManager knows the exit descriptor, runs the
fallback for that specific bytecode operation, and resumes after the operation.

The problem was not that the callee exited. The problem was that the native
caller threw away the callee context and only knew how to recover at the
caller's call site.

Before this round, the native call protocol looked like this:

```
caller Tier 2 code
    BLR callee direct entry
        callee Tier 2 code
            exit inside callee
    caller sees "the call failed"
caller falls back around the CALL instruction
caller calls callee again from PC 0
```

That protocol requires replay safety.

The new protocol is:

```
caller Tier 2 code
    BLR callee Tier2DirectEntryPtr
        callee Tier 2 code
            exit inside callee
    caller sees "the callee exited"
runtime handles the callee's original exit descriptor
runtime resumes the callee at the callee resume point
runtime resumes the caller at the call continuation
```

That protocol requires resume safety.

The callee did not need to be replayed. It needed to keep running.

## Two Direct Entries

The implementation starts by splitting the entry pointers.

The old field was:

```
DirectEntryPtr
```

It meant too many things. A baseline caller could use it. A Tier 2 caller could
use it. A caller that only knew how to replay the call could use it. A caller
that had enough metadata to resume the callee could also use it. Those are not
the same ABI.

The new model has two public entry categories:

```
DirectEntryPtr
Tier2DirectEntryPtr
```

`DirectEntryPtr` remains the conservative entry. It is only published when the
callee is replay-safe. Any caller that enters through this pointer may recover
by falling back at the call site and replaying the call.

`Tier2DirectEntryPtr` is narrower. It is only for Tier 2 native callers that
can report and handle a callee-native exit. Those callers are not allowed to
pretend a callee exit is just a failed call. If the callee exits, the caller
must preserve enough information for the TieringManager to handle the callee's
own exit first.

This split is why the fix is not a nbody whitelist. It is a protocol split:

```
replay-safe caller path     => DirectEntryPtr
callee-resume caller path   => Tier2DirectEntryPtr
```

The same distinction will matter for recursive raw-int calls, peer numeric
calls, and future direct calls that skip more VM frame setup.

## The Exit Packet

When a Tier 2 caller uses the Tier2-only entry and the callee exits, the caller
now returns a distinct exit reason:

```
ExitNativeCallExit
```

That exit is not a generic "call failed" signal. It is a packet that says:

```
the native callee exited
here is the callee closure
here is the callee frame base
here is the callee's original exit code
here is the caller call site
here is the caller resume continuation
```

The Go side then handles the two levels in the right order.

First, it handles the callee exit as if the callee had been entered through the
normal TieringManager path. That means the existing exit descriptor remains the
source of truth. Table exits, allocation exits, arithmetic exits, guards, and
other fallback points still use the callee's own metadata.

Second, once the callee has produced its return value, the manager resumes the
caller at the call continuation. The caller does not replay the call. It
continues from the point where the native call result is expected.

The important architectural point is that fallback is still owned by the code
that emitted the exiting operation. The caller does not invent a new fallback
for the callee.

## Why Baseline Direct Calls Stay Conservative

One tempting simplification would be to make every direct entry use the new
callee-resume protocol. That would be wrong.

Baseline direct-entry callers and replay-safe native callers do not have the
same metadata shape as Tier 2 native callers. Some of them are intentionally
simple: enter the callee, and if anything goes wrong, fall back around the call.
That simplicity is exactly why `DirectEntryPtr` must remain replay-safe.

So the runtime distinguishes the source of the call:

```
caller used DirectEntryPtr
    => legacy replay-safe contract

caller used Tier2DirectEntryPtr
    => callee-resume contract
```

This detail fixed a subtle correctness problem during the patch. A baseline
caller can see a function with both entries populated, but it must not
accidentally receive a callee-native exit it cannot interpret. The call record
therefore tracks whether the caller used the Tier2-only path.

The pointer split alone is not enough. The exit convention has to remember
which pointer was used.

## The Context Gate

Resume safety is broader than replay safety, but it is not unlimited.

A Tier 2 caller can cache pieces of execution context. For example, it may have
assumptions about globals, upvalues, or concurrency state that are valid across
ordinary calls but not necessarily valid across a callee exit that mutates that
context before resuming.

So this patch keeps a second conservative gate for functions that mutate
context the native caller may have cached:

```
SetGlobal
SetUpval
Close
Go
Send
Recv
```

Those functions may still compile. They may still run through the normal
TieringManager path. They simply do not get the Tier2-only native direct entry
yet.

This is a temporary conservative boundary, but it is not a hack. It marks the
next missing protocol piece: if a native caller wants to survive callee exits
that mutate shared context, the exit packet must also describe which caller
assumptions need invalidation or reload.

Until that metadata exists, the correct fast path is:

```
side-effectful but callee-resume-safe
    => Tier2DirectEntryPtr allowed

context-mutating with caller-cache interaction
    => Tier2DirectEntryPtr withheld
```

That boundary preserved the regression test for global-shape changes while
restoring the hot nbody path.

## The Nbody Result

After the protocol split, nbody stopped falling out of compiled code on every
iteration:

```
nbody(500K):
  before the replay gate:          0.066s
  after replay-only gate:          1.526s
  after callee-resume protocol:    0.066s

ExitCallExit:
  after replay-only gate:          500013
  after callee-resume protocol:    15

total exits:
  after callee-resume protocol:    80

LuaJIT:
  nbody(500K):                     0.034s
```

This does not beat LuaJIT. It does something more important for the current
stage: it removes an architectural false wall.

The replay gate was correct, but it made a large class of native calls look
impossible. The callee-resume protocol shows the right split:

```
unsafe to replay
    does not imply
unsafe to call directly
```

It only implies that the caller must know how to let the callee finish.

## What This Changes For Raw Self Recursion

This also clarifies the raw-int self-recursive ABI discussion.

The raw self path should not be an ackermann-specific shortcut. It needs the
same four pieces as the native callee-resume path:

```
register liveness
exit-resume metadata
fallback reconstruction
return convention
```

The current raw self path still keeps too much VM-shaped state alive on the
success path. It preserves frame windows and fallback arguments even when the
recursive call succeeds entirely in registers. That is safe, but it leaves
performance behind.

The next version should treat the recursive raw call like a smaller version of
the Tier2-only native call:

```
success path:
    pass raw int args in fixed registers
    return raw int result in a fixed register
    keep only live caller values in registers

exit path:
    materialize the VM frame from metadata
    restore the exact live register set
    run the callee fallback at the callee exit point
    resume the caller continuation
```

That design is general. Ackermann benefits because it is pure integer
recursion, but the protocol is not "if function name is ack." It is "if the
function's liveness, exit, fallback, and return metadata prove the raw ABI can
be reconstructed."

The nbody fix is a proof of the same principle at the function-call level.
Performance work has to move state off the VM frame on the success path, but
correctness still needs a precise way to rebuild that state on exits.

## The Lesson

The method JIT is not becoming a trace JIT. It is still compiling methods, not
recording hot traces. But to get close to LuaJIT, the method JIT has to learn
one of the lessons that makes trace JITs fast: the optimized path and the
deoptimized path do not need to carry the same representation.

The optimized path wants registers, direct branches, raw integers, and no
interpreter-shaped call frames.

The deoptimized path wants exact metadata: which values are live, where they
live, which operation exited, what side effects already happened, and where
execution should resume.

This round did not remove the VM frame from native calls. It did something just
as necessary first: it taught the caller that a callee exit belongs to the
callee.

That is the contract the next optimizations can build on.

