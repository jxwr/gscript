---
layout: default
title: "The Table Was Born Typed"
permalink: /54-the-table-was-born-typed
---

# The Table Was Born Typed

The last table win remembered the largest key seen by a hot bytecode PC.

That helped warmed code. It did not help the first optimized shape enough.
Some loops create a fresh table and immediately fill it with one element kind:

```text
t[i] = i
t[i] = false
t[i] = x * 1.5
```

Waiting for feedback to rediscover that kind is unnecessary. The IR already
knows the stored value type.

## The missing local fact

`TablePreallocHintPass` already annotated empty table allocations that feed
observed integer-key stores. The new rule lets local IR types seed the same
typed-array path even when per-PC feedback is still empty.

The rule stays narrow:

- the table must come from a local `NewTable`;
- the store key must be an integer-key table op;
- the stored value type must be monomorphic int, float, or bool;
- polymorphic feedback still wins and falls back to mixed arrays.

Once a candidate is found, the allocation carries a typed-array hint and local
`GetTable` / `SetTable` users get the corresponding kind fact. The pipeline
then reruns type specialization so users see the new `Aux2` kind information.

This is not a benchmark name check. It is a local fact: "this freshly allocated
table is filled like a dense typed array."

## Why append mattered

The companion Tier 1 change handles the common fill pattern:

```text
key == len && key < cap
```

For mixed, int, float, and bool arrays, the native `SETTABLE` path can extend
the typed array length without falling back to Go, as long as the table has no
imap/hash structure that would make `RawSetInt` semantics observable.

That is the difference between a preallocated table that still exits on the
first append and one that actually stays on the native path.

## The result

After integration and the conservative direct-deopt fix, the focused diagnostic
looked like this:

```text
table_array_access default: 0.035s
sieve default:              0.029s
matmul default:             0.121s
```

Earlier in the same optimization cycle, before these local typed-table changes,
the full guard had shown:

```text
sieve default:              0.051s
matmul default:             0.135s
table_array_access default: 0.041s
```

Single runs are noisy, but the direction is not: the dense table path is now
much less dependent on exit-resume warmup.

The LuaJIT gap is still visible. Matmul at `0.121s` is still far from LuaJIT's
roughly `0.022s`. But the remaining work is now more specific: row/header/data
pointer hoisting and denser element codegen, not "why are we exiting while
building arrays?"

## The guardrail

This round also caught a tempting unsafe extension.

I tried to widen loop exit-only phi stores to `GuardType` and `NumToFloat`
loops by adding a broad direct-deopt flush. The focused tests looked fine, but
the full methodjit suite hung in the raw self-call fallback-frame check. That
means the deopt protocol is not uniform enough yet.

So the final tree keeps the direct-deopt rule conservative. Typed table hints
landed. The broader deopt flush did not.

Commits:

- `bfe22d2 methodjit: handle tier1 typed table appends`
- `b980fb6 methodjit: infer local typed table prealloc hints`
- `7e62975 methodjit: keep direct deopt phi stores conservative`
