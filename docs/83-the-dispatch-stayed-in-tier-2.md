---
layout: default
title: "The Dispatch Stayed In Tier 2"
permalink: /83-the-dispatch-stayed-in-tier-2
---

# The Dispatch Stayed In Tier 2

*May 2026 - Beyond LuaJIT, Post #83*

## The Tier 1 Fix Was Real, But It Was Not The End

The first successful `actors_dispatch_mutation` fix did not come from Tier 2.

It came from accepting what the profile was saying: a dynamic method-dispatch
benchmark with several receiver shapes was spending too much time in the
baseline exit path. Tier 1 learned a small polymorphic field cache for
`GETFIELD` and `SELF`, and that was enough to move the benchmark from the
old slow path to roughly:

```text
actors_dispatch_mutation JIT:  about 0.147-0.151s
Tier 1 compiled:               7 functions
Tier 2 compiled:               0 functions
```

That was a useful repair. It also exposed the next ceiling.

The program was no longer dominated by one obvious Tier 1 field miss, but the
hot loop still could not become optimized native code. The execution stayed in
baseline code, so every actor update still paid for the general baseline method
call shape:

```text
load receiver field
load method
shuffle self and arguments
dispatch through the baseline closure-call protocol
return through the VM-shaped frame window
repeat for every actor and every tick
```

The earlier result proved the inline cache was necessary. It did not prove
that baseline dispatch was the right steady state.

## Why This Was Not A Benchmark Kernel

The tempting wrong fix would be to recognize the benchmark's actor loop and
replace it with one native driver. That would make the number smaller, but it
would not answer the real compiler question.

The useful pattern is broader:

```text
one hot caller
several stable receiver shapes
method name loaded through SELF or GETFIELD
callee closures that are themselves JIT-able
normal fallback when the shape or method changes
```

That is ordinary dynamic method dispatch. It appears in actor systems, object
simulations, table-backed components, and callback-heavy scripts. If the method
JIT wants to compete with LuaJIT without becoming a trace JIT, this path has to
be a first-class optimized method path, not a script recognizer.

The new work therefore moved the boundary one level lower:

```text
make polymorphic method dispatch a Tier 2 operation
keep the fallback/resume protocol exact
publish direct callee entries only when the caller can survive callee exits
```

## Admission Had To Stop Rejecting The Real Loop

Before this round, the Tier 2 admission logic was deliberately conservative
around calls in loops. That was mostly correct. A native caller cannot blindly
replay a side-effecting call after a callee exits. If the callee has already
mutated program state, replaying the entire call duplicates effects.

But `actors_dispatch_mutation` was no longer a mystery call site. The profile
contained enough structure:

```text
the call target comes from a stable method field
the receiver table shapes are polymorphic but bounded
the callees are known closures
the fallback can resume at the call continuation
```

The admission rule changed from "dynamic method dispatch in a loop is too
risky" to "dynamic method dispatch in a loop is allowed when the caller has the
native call protocol and feedback needed to resume correctly."

That distinction matters. It keeps the fast path general while preserving the
old refusal for call sites that cannot yet describe their recovery contract.

## The Call Cache Became Polymorphic

The Tier 2 call lowering now has a small polymorphic inline cache for dynamic
method calls. A hot call site can remember a bounded set of observed receiver
or method outcomes instead of pretending every dispatch is monomorphic.

The native success path is the usual inline-cache shape:

```text
load receiver table
validate table shape
load method field
validate closure target
branch to the callee's native entry
```

If any guard fails, the code exits through the normal call fallback. That
fallback sees the real VM registers, performs the language-level call, updates
feedback, and resumes at the continuation. The optimized path is not a separate
semantics engine.

The same idea applies to the table field side. Tier 1 already had a
polymorphic field cache for `GETFIELD` and `SELF`; this round made sure the
feedback needed by Tier 2 also stays populated. Static field access and dynamic
string-key feedback now feed the optimized caller instead of only making
baseline exits cheaper.

## The Callee Exit Contract Was The Hard Part

The key requirement was not just "call native code."

The hard requirement was:

```text
if a Tier 2 caller jumps into a native callee,
and that callee exits,
the caller must not replay a side-effecting call,
and the callee must keep its Tier 2 entry for the next iteration.
```

That is the same boundary that showed up in earlier native-call work. There
are two different direct-entry contracts:

```text
DirectEntryPtr
  safe for callers that can replay the call boundary

Tier2DirectEntryPtr
  safe for optimized callers that need callee-exit resume
```

This round tightened the actor path around the second contract. The optimized
caller can call a native callee, let the callee's own exit handler run, and then
continue at the caller's call continuation. At the same time, a successful
callee exit does not erase the useful native entry that the next iteration
needs.

That last part was visible in the profile. If native method entries are dropped
too aggressively after one fallback, the benchmark oscillates back into
baseline dispatch. Keeping the stable Tier 2 entry published is what lets the
method loop stay optimized.

## The Result

The focused guard before this round, after the Tier 1 polymorphic field cache,
looked like this:

```text
actors_dispatch_mutation:
  0.147s
  0.149s
  0.146s
  0.148s
  0.149s

Tier 1 compiled:   7 functions
Tier 2 compiled:   0 functions
```

After the Tier 2 dispatch path landed:

```text
actors_dispatch_mutation:
  0.045s
  0.045s
  0.045s
  0.046s
  0.045s

Tier 1 compiled:   7 functions
Tier 2 attempted:  4 functions
Tier 2 compiled:   4 functions
Tier 2 entered:    4 functions
```

The entered Tier 2 functions are the actual actor work:

```text
run_world
step_cache
step_io
step_worker
```

The merged mainline check reported:

```text
actors_dispatch_mutation actors=5000 ticks=200
checksum: 32431665
Time: 0.046s

JIT Statistics:
  Tier 1 compiled: 7 functions
  Tier 2 attempted: 4
  Tier 2 compiled: 4
  Tier 2 entered:  4
  Tier 2 failed:   0
```

That is a little more than a 3x improvement over the already-fixed Tier 1
path. Compared with the older pre-cache result around 0.295s, it is about a
6x improvement.

## Regression Checks

The important nearby tests stayed flat in repeated focused runs:

```text
groupby_nested_agg:          about 0.020-0.021s
json_table_walk:             about 0.075-0.082s
table_field_access:          about 0.018-0.020s
producer_consumer_pipeline:  about 0.221-0.234s
method_dispatch:             about 0.001s
```

The groupby result matters because the previous round had just moved dynamic
string-key table access into Tier 2. The actor work touched some of the same
cache context plumbing, so it had to prove it did not undo the string-key
breakthrough.

The producer result matters for the opposite reason: it did not move. That
benchmark still needs a separate coroutine or closure-call architecture win.
It should not be hidden behind a whole-call pipeline kernel.

## What This Changed Architecturally

This round changed the method JIT's answer to dynamic dispatch.

Before:

```text
Tier 1 can make dynamic method dispatch cheap enough to survive.
Tier 2 should avoid the loop if the call cannot be proven simple.
```

After:

```text
Tier 1 collects polymorphic dispatch feedback.
Tier 2 can consume that feedback through a guarded native call cache.
The fallback protocol is precise enough to resume after callee exits.
```

That is a better method-JIT story. It still does not require trace recording,
but it accepts the same basic truth that trace JITs exploit: dynamic dispatch is
often stable at run time even when it is not statically monomorphic.

The next useful step is to apply the same discipline to the remaining large
gaps:

```text
producer_consumer_pipeline:
  coroutine/resume/call overhead, not a benchmark kernel

json_table_walk:
  table construction and string-format/native builtin lowering

raw recursive calls:
  register-only success path plus exact fallback metadata
```

The actor loop is now evidence that the method-JIT architecture can handle a
polymorphic dynamic workload without falling back to a trace compiler. The
remaining work is to keep moving those generic boundaries until the slow cases
stop looking special.
