---
layout: default
title: "The Globals Learned Their Indexes"
permalink: /56-the-globals-learned-their-indexes
---

# The Globals Learned Their Indexes

The first global optimization was easy to explain.

Tier 2 had a native `GETGLOBAL` cache. The dispatch table was not using it.
Wire the op to the native emitter, populate the cache on the first miss, and
nbody stops exiting to Go millions of times just to read `bodies`.

That was post 30: the highway existed, and nobody had connected the on-ramp.

This round was more subtle. The highway was connected. The cache existed. The
benchmarks were still paying global-exit costs in places where the machine code
already had enough information to avoid them.

The problem was not "global lookup is slow." The problem was that global lookup
had not become part of the Tier 2 calling convention.

## The shape of the bug

The bad path showed up while testing the indexed-global experiment on four
benchmarks:

```text
ackermann
mutual_recursion
nbody
sum_primes
```

`sum_primes` was the reason to open the work. It has a top-level reduction:

```text
sum := 0
for n := 2; n <= limit; n++ {
    if is_prime(n) {
        sum = sum + n
    }
}
```

The loop reads and writes globals at shallow loop depth. The old gate rejected
that shape because Tier 2 could not prove global state was cheap and coherent
after a native write. That was conservative and correct. It also kept a simple
integer loop out of the optimizing tier.

The first patch made the VM prepare an indexed global table:

```text
constant index -> VM.globalArray index
```

Then Tier 2 could lower global reads and top-level writes to indexed array
operations guarded by `VM.globalVer`.

On paper, that is exactly what we want. GScript already uses indexed globals in
the VM. The method JIT should not hash string names in hot code either.

## The main-only trap

The first version was too broad. It enabled indexed globals for every Tier 2
function but only prepared the index map for the function that entered through
`executeTier2`.

That distinction matters because method-JIT direct calls do not re-enter Go.
They switch the current `ExecContext` and branch straight into the callee:

```text
caller Tier 2 code
  -> BLR callee.Tier2DirectEntryPtr
     -> callee Tier 2 code
```

The caller and callee have different constant pools. Constant slot 3 in the
caller might be `"bodies"`. Constant slot 3 in the callee might be `"math"` or
`"F"` or `"M"`.

If the callee uses the caller's `const index -> global index` map, the best
case is a guard failure. The worst case is reading the wrong global with a valid
array index.

The emergency fix was to enable indexed globals only for `<main>`.

That protected nbody. It also exposed the real missing protocol immediately:

```text
ackermann default:        0.018s, total exits 1013
mutual_recursion default: 0.040s, total exits 28015
```

Those exits were not random. They were `GETGLOBAL` exits inside recursive
callees. `ack` had to read the global named `ack`. `F` had to read `M`, and `M`
had to read `F`. By making only `<main>` eligible, we forced the recursive
callee bodies back through the old global-exit path.

That was the useful failure. It proved the right abstraction:

> indexed globals are not a property of the top-level entry. They are a
> per-compiled-function context field, just like constants and global caches.

## The actual protocol

The final protocol has four owners.

The VM owns the global storage:

```text
globalArray []Value
globalIndex map[string]int
globalVer   uint32
```

`globalVer` changes when the structure changes: a new global name appears, the
VM switches out of the single-threaded no-lock mode, or anything else makes the
old array/index pairing unsafe.

Each compiled function owns its own index map:

```text
GlobalIndexByConst []int32
```

This is sized like the function's constant pool. Unused constants get `-1`.
Only actual `GETGLOBAL` sites and native top-level `SETGLOBAL` sites are
resolved. That matters because constant pools also contain field names and
string literals. Treating every string constant as a global is wrong and can
move `globalVer` just by compiling code.

`FuncProto` publishes the pointer direct callers need:

```text
Tier2GlobalIndexPtr uintptr
```

That is the missing call-boundary field. Direct BLR setup already loaded the
callee's constants, closure pointer, baseline global cache, Tier 2 global value
cache, and global-cache generation pointer. Now it also loads the callee's
indexed-global map.

Finally, `ExecContext` carries the currently active indexed-global state:

```text
Tier2GlobalArray
Tier2GlobalIndex
Tier2GlobalVerPtr
Tier2GlobalVer
```

The emitted fast path is then simple:

```text
array := ctx.Tier2GlobalArray
indexMap := ctx.Tier2GlobalIndex
verPtr := ctx.Tier2GlobalVerPtr

if array == nil || indexMap == nil || verPtr == nil:
    slow
if *verPtr != ctx.Tier2GlobalVer:
    slow

globalIndex := indexMap[constIndex]
if globalIndex == -1:
    slow

value := array[globalIndex]
```

On slow path, the old exit-resume machinery runs. The important part is that
the common path no longer asks Go to resolve a string name.

## The direct-call detail

The non-tail direct call path now saves and restores the caller's
`Tier2GlobalIndex` around the BLR, exactly like it already does for other
caller context fields.

The static self-call case skips that work. Same proto means same constant pool,
same compiled function, same index map. This is the same reason static self
calls skip the Tier 2 global-cache switch.

Tail calls are different because they do not return to the caller frame. The
tail-call path sets the callee context and jumps. There is no restore; the
callee becomes the current function. That path now publishes the callee's Tier 2
global cache and indexed-global map before the final branch.

That is the part the main-only experiment could not fake. A direct callee must
carry its own map, or recursive code will either exit forever or read through
the wrong constant-pool interpretation.

## Why writes are still narrower

Reads can be broad. A `GETGLOBAL` fast path either sees the same structural
version and loads from `globalArray`, or it exits.

Writes are harder.

A native `SETGLOBAL` has to do three things:

1. write the new value into `globalArray`;
2. bump the shared global value-cache generation so old cached `GETGLOBAL`
   values miss;
3. mirror the value back into the legacy `globals` map before the VM or public
   API reads it through the old surface.

The top-level compiled function can do this safely. `executeTier2` knows which
top-level global constants were written natively, and it syncs those array slots
back to the map after every JIT return.

Direct callees are not enabled for native global writes yet. That is not a
missing optimization by accident; it is a deliberately closed door. If a callee
writes globals natively, the top-level execute loop would need to know that the
callee ran and which callee constants it wrote. That is a broader writeback
protocol. Until it exists, callee `SETGLOBAL` uses the existing exit path.

This round makes reads general and writes top-level only.

That is the smallest protocol that fixes the benchmark class without inventing
a silent stale-map bug.

## The invalidation cases

Two details are easy to miss.

First, missing globals cannot be represented by the zero `Value`.

The old experimental patch used the default zero value when resolving a missing
name into `globalArray`. That is not the same as script `nil`. The final code
stores `runtime.NilValue()` for new unresolved names. If the global is later
assigned, the normal global array slot is updated.

Second, `OP_GO` invalidates the protocol.

The VM starts in a no-lock single-threaded mode. A goroutine spawn switches the
VM into the locked global mode and marks global tables concurrent. Any native
Tier 2 code that captured an array pointer before that transition must not keep
using it as if nothing changed. The fix is simple: when `OP_GO` flips
`noGlobalLock` off, it increments `globalVer`. Tier 2 sees the version mismatch
and falls back.

That is the theme of the whole patch: fast path by pointer, safety by structural
version.

## The tests that mattered

Unit tests alone were not enough here. The broken version passed local reasoning
until `nbody` and recursive benchmarks contradicted it.

The final gate was:

```text
go test ./internal/methodjit -count=1
go test ./benchmarks ./gscript ./internal/... ./cmd/... -count=1 -timeout=300s
bash benchmarks/diagnose_tier2.sh ackermann mutual_recursion nbody sum_primes
bash benchmarks/regression_guard.sh --runs=3 --timeout=90 \
  --bench ackermann \
  --bench mutual_recursion \
  --bench nbody \
  --bench sum_primes \
  --bench method_dispatch \
  --bench fannkuch \
  --bench math_intensive \
  --bench fibonacci_iterative
```

The focused diagnostic after the direct-call fix:

```text
ackermann        default 0.016s, exits 14
mutual_recursion default 0.016s, exits 20
nbody            default 0.079s, exits 80
sum_primes       default 0.002s, exits 15
```

The full median-of-3 guard after the commit showed no regressions:

```text
fib_recursive       default 0.662s
sieve               default 0.028s
mandelbrot          default 0.052s
ackermann           default 0.017s
matmul              default 0.129s
nbody               default 0.080s
sum_primes          default 0.002s
mutual_recursion    default 0.016s
method_dispatch     default 0.002s
table_field_access  default 0.027s
table_array_access  default 0.035s
fibonacci_iterative default 0.239s
math_intensive      default 0.063s
object_creation     default 0.005s
```

`matmul` is still noisy and still behind LuaJIT. It was +4.9% against the
checked-in baseline in that guard, below the regression threshold, and the
dense-table work is the next likely place to attack it.

## What changed conceptually

The interesting part is not that `sum_primes` got a faster global reduction.

The interesting part is that globals became a real method-JIT ABI component.
Before this patch, Tier 2 carried:

- current register window;
- current constants pointer;
- current closure pointer;
- call mode;
- global value-cache pointer;
- call cache pointer;
- exit-resume metadata.

Now it also carries the current function's global-index map. That sounds small,
but it closes a class of bugs where code generation was correct only for the
entry function and not for the function currently executing.

That matters because the project is explicitly staying method-JIT based. There
is no trace recorder stitching hot caller and callee frames into one linear
trace. If method-JIT code wants trace-like speed, the call boundary has to carry
the facts each callee needs.

This is the same lesson as the raw self-recursive ABI, just for globals instead
of integers:

> A fast path that only works until the first direct call is not a fast path.
> It is an entry-point optimization.

## Still behind LuaJIT

This patch does not make the suite beat LuaJIT.

The full guard still shows:

```text
ackermann        GScript 0.017s   LuaJIT 0.006s
mutual_recursion GScript 0.016s   LuaJIT 0.004s
nbody            GScript 0.080s   LuaJIT 0.034s
fannkuch         GScript 0.046s   LuaJIT 0.020s
matmul           GScript 0.129s   LuaJIT 0.022s
sort             GScript 0.050s   LuaJIT 0.010s
```

The remaining gaps are not global lookup gaps anymore. They are:

- recursive frame and call-boundary overhead in Ackermann and mutual recursion;
- dense table header/data/length reloads in table-heavy loops;
- table mutation recovery for sort;
- loop code shape and boxed recurrence overhead in fibonacci-style loops.

That is still a lot of work. But it is a better list than "globals are slow."

This round turned a vague bottleneck into a concrete calling-convention field,
then proved the field survives direct calls, tail calls, version changes, and
the existing exit-resume path.

Commit:

- `8705b16 methodjit: add indexed global fast path`
