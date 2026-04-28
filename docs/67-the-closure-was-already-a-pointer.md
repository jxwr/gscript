---
layout: default
title: "The Closure Was Already a Pointer"
permalink: /67-the-closure-was-already-a-pointer
---

# The Closure Was Already a Pointer

*April 2026 - Beyond LuaJIT, Post #67*

## Where We Left Off

In [Post #66](66-the-callee-kept-running), the method JIT split native call
entries into two contracts:

```
DirectEntryPtr       => replay-safe callers
Tier2DirectEntryPtr  => Tier 2 callers that can resume a callee exit
```

That fixed the nbody regression created by the replay-safety gate. The next
large win came from a lower layer, not from Tier 2 codegen.

The benchmark that exposed it was `binary_trees`.

After all the JIT work, `binary_trees` was still strange:

```
binary_trees:
  VM:          about 0.88s
  default JIT: about 0.89s
  no-filter:  about 0.98s
```

The JIT was not helping. Sometimes it was worse. That usually means the
benchmark is not spending most of its time in arithmetic instructions. It is
spending time in the runtime contract around the code: allocation, dispatch,
table representation, call frames, and value decoding.

This round chased one of those contracts.

## The Expensive Question

GScript values are tagged. A function value can be a VM closure. The hot call
path asks a simple question many times:

```
is this Value a VM closure, and if so, which *vm.Closure is it?
```

Before this patch, the common helper path effectively answered through the
generic pointer interface:

```
Value
  -> Ptr()
  -> runtime object / interface-root lookup
  -> type assertion to *vm.Closure
```

That is a good compatibility path. It handles legacy interface-backed values,
foreign pointer-shaped values, and the cases where the caller really does not
know what pointer subtype it is looking at.

It is not the right fast path for VM closures.

The value already carries a pointer payload with a VM-closure subtype tag. The
call path was asking a generic question when the tag had already answered the
specific one.

The new fast path asks the specific question first:

```
if value is tagged ptrSubVMClosure:
    return the raw *vm.Closure payload
else:
    fall back to the old Ptr() path
```

That became `Value.VMClosurePointer()`.

## Why This Matters

At first glance, this looks too small to matter. A pointer helper changed. A
few call sites switched from the generic path to the VM-closure path. No
compiler pass was added. No ARM64 instruction sequence got rewritten.

But `binary_trees` and `closure_bench` are call-heavy and allocation-heavy.
They do not need one heroic inner-loop instruction. They need the runtime to
stop charging a small toll on every closure dispatch.

The old call path looked roughly like this:

```
func callValue(v Value, args []Value) {
    p := v.Ptr()
    cl, ok := p.(*vm.Closure)
    if ok {
        callVMClosure(cl, args)
    }
}
```

That has two avoidable costs in the common case:

```
decode through the generic pointer representation
perform an interface type assertion
```

The new common path is:

```
func vmClosureFromValue(v Value) *Closure {
    if cl := v.VMClosurePointer(); cl != nil {
        return cl
    }
    return legacyClosureFallback(v)
}
```

The compatibility path remains. The hot VM closure case no longer pays for it.

This is the same kind of lesson as [Post #11](11-eight-bytes-that-change-everything)
and [Post #12](12-the-box-unbox-toll): representation details dominate when
the operation is small and repeated often enough. A call dispatch that happens
millions of times is not "just one helper."

## The Places That Needed It

The patch did not change one central function and hope everything else used it.
Closure extraction existed in multiple layers:

```
VM CallValue
coroutine resume/yield handoff
method JIT interpreter helpers
Tier 1 call-exit handlers
TieringManager call paths
Tier 2 exit handlers
```

That split matters. If only `vm.CallValue` gets the fast path, then a JIT
call-exit can still fall back into the generic pointer path. If only the
method JIT gets the fast path, pure VM benchmarks still pay the old cost.

The new helper therefore lives at the value/runtime boundary and has thin
wrappers in VM and method JIT packages. The ownership is:

```
runtime.Value:
    knows how to decode a VM-closure payload by tag

vm:
    owns the normal closure extraction helper and legacy fallback

methodjit:
    uses the same fast extraction in exit and tiering paths
```

This keeps the unsafe pointer operation small and testable. The rest of the
runtime asks for a closure, not for bit layout.

## The Safety Contract

The risk in a change like this is not performance. The risk is pretending that
every pointer-shaped value is a VM closure.

The fast path therefore has a narrow contract:

```
only return a closure when the Value tag says ptrSubVMClosure
otherwise return nil and use the old fallback
```

The tests cover three shapes:

```
raw VM-closure tagged value returns the closure pointer
legacy interface-backed closure still works
non-closure pointer-shaped values are rejected
```

That last case matters. A faster helper that accepts the wrong pointer subtype
would be a correctness bug, not an optimization.

## The Numbers

On the merge machine, after the previous Post 66 and post-escape TypeSpec
work, the focused guard moved like this:

```
binary_trees:
  before: default JIT about 0.888s
  after:  default JIT about 0.745s
  change: about 16% faster

closure_bench:
  before: default JIT about 0.027s
  after:  default JIT about 0.025s
  change: about 7% faster

method_dispatch:
  VM side: about 0.050s -> 0.043s in the worker run
  JIT side: already near the timer floor

nbody:
  stayed stable, around 0.064-0.065s
```

The worker comparison also showed the pure VM side of `binary_trees` moving:

```
binary_trees VM:
  0.892s -> 0.718s
```

That is the most important part of the result. This was not a Tier 2 trick. It
reduced the runtime cost that both the interpreter and JIT fallback paths pay.

## Why The JIT Still Does Not Win Binary Trees

After this patch, `binary_trees` still looks like a VM-shaped benchmark:

```
binary_trees:
  VM:          about 0.72s
  default JIT: about 0.75s
```

That is better, but it is not the final state. The benchmark allocates many
small tree nodes and recursively calls tiny functions. The method JIT can
compile the code, but the dominant work is still:

```
allocate a table-like node
store fields
call recursively
return and traverse
```

If the table representation and allocation path stay VM-shaped, native
arithmetic does not decide the benchmark. The next larger wins likely come from:

```
specialized node allocation
shorter table field initialization
less GC/root scanning per tiny object
faster recursive closure entry
```

The closure-pointer fast path removes one dispatch tax. It does not remove the
node representation tax.

## The Connection To Post-Escape TypeSpec

The previous code commit in the same optimization round was also about timing,
but at a compiler-pass level. Escape analysis rewrites virtual object fields
into scalar SSA values. Before the patch, TypeSpecialize had already run, so
some newly exposed post-escape numeric values stayed generic until codegen.

The fix was to rerun TypeSpecialize after escape rewrites only when the current
SSA type lattice says it can still change the graph:

```
EscapeAnalysis
DCE
if TypeSpec can still rewrite generic typed ops:
    TypeSpecialize
RangeAnalysis
OverflowBoxing
...
```

That moved `object_creation` from roughly:

```
0.007s -> 0.003-0.004s
```

These two commits look unrelated, but they are the same kind of optimization.
They both remove stale abstraction boundaries:

```
post-escape scalar values were still treated as generic boxed math
VM closure values were still treated as generic interface-backed pointers
```

In both cases, the information already existed. The pipeline simply failed to
use it at the point where cost was paid.

## The Lesson

The easy story about beating LuaJIT is "generate better native code." That is
true, but incomplete.

A method JIT also has to make the VM cheaper because method JIT execution is
not a sealed world. Calls cross boundaries. Exits resume in the runtime.
Closure values move through interpreter and compiled frames. Allocation-heavy
benchmarks spend more time in the object model than in arithmetic.

That means there are two optimization tracks:

```
make Tier 2 success paths more native
make the shared VM/runtime contracts cheaper
```

Post 66 was the first track: native caller/callee resume.

This patch is the second track: closure dispatch should use the representation
it already has.

To get closer to LuaJIT, both tracks have to keep moving.

