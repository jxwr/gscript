---
layout: default
title: "The Upvalue Was Not An Exit"
permalink: /61-the-upvalue-was-not-an-exit
---

# The Upvalue Was Not An Exit

*April 2026 - Beyond LuaJIT, Post #61*

`closure_bench` had been the stubborn red line in the guard table. It was not a
Tier 2 failure. It was not call-target feedback. It was not a missing method JIT
optimization in the usual sense. The benchmark was losing time in Tier 1 because
the direct native call entry was disabled too broadly for mutable-upvalue
closures.

The focused result after this round:

```
closure_bench    VM 0.070s    JIT 0.028s    baseline 0.028s
```

The worker's section timing was even clearer:

```
before: accumulator 0.050s, total 0.071s
after:  accumulator 0.008s, total 0.028s
```

That is the kind of result that usually tempts a larger explanation. This one
does not need it. The hot closure was already compileable. The call target was
already stable. The missing piece was a replay-safety predicate that could tell
the difference between "may exit" and "looks like a memory access."

## The Benchmark Shape

The hot section is the accumulator closure:

```
func make_accumulator() {
    total := 0
    func add(x) {
        total = total + x
        return total
    }
    return add
}

acc := make_accumulator()
for i := 1; i <= n; i++ {
    result = acc(i)
}
```

The closure mutates `total`, which lives in an upvalue. The callee contains a
small sequence:

```
GETUPVAL total
ADD
SETUPVAL total
RETURN
```

There is no table allocation, no global lookup in the callee, no vararg shape,
and no dynamic call inside the hot closure. The only reason it was not using the
direct native entry was that the replay-safety gate saw an upvalue operation
after a native-visible side effect and treated it as exit-capable.

That gate existed for a good reason. A native BLR direct entry is only safe if a
callee cannot run a side effect and then exit in a way that causes the caller to
replay or recover from the wrong point. If that happens, the VM can observe a
side effect twice or observe partially materialized state. The previous fix
made the gate conservative: after a native-visible side effect, any operation
that might exit blocked direct entry publication.

The problem was that `GETUPVAL` and `SETUPVAL` in a statically valid closure do
not take that slow exit.

## What "Statically Valid" Means

Closures created by `OP_CLOSURE` allocate exactly the upvalue array described by
the function prototype. The bytecode operand for `GETUPVAL` and `SETUPVAL`
selects one slot in that array. If the operand is within `len(proto.Upvalues)`,
the native operation can load or store the upvalue directly. There is no dynamic
table lookup, no global-shape check, and no fallback boundary.

The new predicate is intentionally small:

```
if op is GETUPVAL or SETUPVAL:
    it may exit only when the upvalue slot is not statically valid
otherwise:
    use the old may-exit classification
```

So the replay-safety scan still keeps its core rule:

```
if side effect has happened and a later op may exit:
    do not publish direct entry
```

It just stops misclassifying valid upvalue access as a later exit.

That is enough to publish `DirectEntryPtr` for the accumulator closure. Once the
caller can BLR directly into the Tier 1 native entry, the hot loop stops paying
the full VM call fallback path for every accumulator invocation.

## Why This Was Not A Tier 2 Problem

The guard output still says:

```
T2 attempted/entered/failed: 1/0/1
```

That is fine. This optimization happens below Tier 2. Tier 1 is still valuable
when it can remove VM dispatch and call overhead from small hot callees. A
method JIT does not need every win to be a Tier 2 win. In fact, for closure
microbenchmarks, Tier 1 direct entry is the right level of machinery: the callee
is tiny, stable, and called many times.

Earlier call-target feedback attempts made this benchmark worse because they
added overhead to the hot call path and admitted the wrong code into Tier 2.
The accumulator was not asking for speculative target discovery. It was asking
for the already-known callee to keep its direct entry.

That distinction matters. A feedback mechanism can answer "what is usually
called here?" It cannot fix an entry publication rule that refuses to publish a
safe entry.

## The Safety Test

The positive test is the accumulator:

```
count = count + 1
return count
```

The native callee compiles, `DirectEntryPtr` stays nonzero, and the Tier 1
program result matches the VM.

The negative test adds a real exit-capable operation after the upvalue side
effect:

```
count = count + 1
tmp := {}
return count
```

`NewTable` after the upvalue mutation can still exit. That callee must not
publish a direct entry. The test asserts exactly that: the function may compile,
but `DirectEntryPtr` remains zero.

This is the right shape for replay-safety tests. The positive case proves the
gate is no longer too broad. The negative case proves it did not become a hole.

## The Result

Before this round, the benchmark table made `closure_bench` look like a major
unresolved regression:

```
closure_bench    JIT ~0.070s    baseline 0.028s
```

After the direct-entry admission fix:

```
closure_bench    JIT ~0.027s to 0.028s
```

The important thing is not only that the number improved. The red item now has
an explanation that matches the machine path:

```
old path: caller cannot BLR into mutable-upvalue closure
new path: valid GETUPVAL/SETUPVAL does not block direct entry
```

No sampling feedback was needed. No Tier 2 admission was needed. No closure
benchmark special case was needed.

## The Rule

Replay safety should be precise about exits, not suspicious about opcodes.

`SETUPVAL` mutates program state, so it is a side effect. But a statically valid
upvalue access is not, by itself, a fallback boundary. Conflating those two
properties disabled the exact entry path the benchmark needed.

This is a recurring compiler lesson. Conservative gates are useful when a
system is young, but they become performance bugs once the runtime contract is
better understood. The right next step is not to delete the gate. It is to teach
the gate the contract.

That is what this round did for upvalues.
