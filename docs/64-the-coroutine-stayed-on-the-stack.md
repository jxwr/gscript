---
layout: default
title: "The Coroutine Stayed On The Stack"
permalink: /64-the-coroutine-stayed-on-the-stack
---

# The Coroutine Stayed On The Stack

*April 2026 - Beyond LuaJIT, Post #64*

`coroutine_bench` was not a JIT benchmark in the usual sense. The focused guard
reported:

```
T2 attempted/entered/failed: 0/0/0
```

The method JIT was not failing to compile a hot numeric loop. The VM was paying
for the coroutine implementation itself. Before this round, a long-lived
coroutine used a child VM running behind a goroutine/channel handoff. Every
`resume` and every `yield` crossed that handoff.

That design was simple and correct, but the benchmark made its cost impossible
to ignore:

```
before:
  coroutine_bench VM       1.581s
  coroutine_bench Default  1.629s

after:
  coroutine_bench VM       0.028s
  coroutine_bench Default  0.030s
```

This is a runtime optimization, not a Tier 2 optimization. That matters. If the
goal is to approach LuaJIT, the method JIT cannot carry runtime overhead that
LuaJIT does not pay. Some wins need to happen below the compiler.

## The Old Shape

The old long-lived coroutine path worked like this:

```
resume caller
  send resume arguments over channel
  wait on yield channel

coroutine goroutine
  receive resume arguments
  run child VM until coroutine.yield or return
  send yielded values over channel
  block until next resume
```

That model makes `yield` feel like a blocking operation in Go. It also gives the
child VM a real Go stack to sit on while suspended. The implementation is easy
to reason about because the child VM is simply paused by the channel receive.

But the cost is paid at the worst possible frequency. `coroutine_bench` has two
hot sections that yield and resume hundreds of thousands of times. The work per
iteration is tiny. The channel handoff dominates everything else.

The coroutine stats made that visible:

```
created:          50002
resumes:         250000
yields:          200000
leaf fast path:   50000
goroutine starts:     2
```

Only two long-lived coroutine VMs were needed, but they crossed the channel
boundary two hundred thousand times. The benchmark was not asking for faster
creation. It was asking for cheaper suspension and resumption.

## The New Shape

The new path keeps the long-lived coroutine paused inside its child VM.

When the child VM executes `coroutine.yield`, the VM does not send values to a
goroutine channel. It records:

```
yielded values
destination register of the yield call
result count expected by the yield call
```

Then it returns an internal sentinel error:

```
errCoroutineYield
```

That sentinel is not exposed as a program error. It is a control-flow signal
inside the VM. The parent `resume` sees it, marks the coroutine suspended, and
returns the yielded values to the caller.

On the next `resume`, the parent writes the resume arguments directly into the
saved yield call's result registers:

```
co.vm.writeCallResults(co.yieldDst, co.yieldC, resumeArgs)
```

Then it calls `co.vm.run()` again. The child VM continues from the bytecode
right after the yield call.

The conceptual model changed from:

```
pause a goroutine that owns a VM
```

to:

```
pause a VM frame and resume it explicitly
```

That is why the benchmark moved. A resume/yield pair is no longer a scheduler
handoff plus channel traffic. It is a small amount of VM state bookkeeping.

## Why This Was Not Just A Fast Path

The first version of this idea was too narrow. It removed most of the channel
traffic, but coroutine semantics have several less obvious edges:

```
cached coroutine.yield
cached coroutine.isyieldable
Value.Ptr compatibility
child VM root scanning
yield inside nested calls
multi-value resume into yield
error after yield
```

Those are the places where a "fast path" can quietly become a different
language.

The hardened version keeps the public coroutine API intact. A VM coroutine
value still round-trips through `Value.Ptr()` for callers using the public
runtime API, while the hot VM path can recover the raw pointer without paying
the interface lookup cost.

Cached `coroutine.yield` needed special care. Consider:

```
yfn := coroutine.yield

co := coroutine.create(func() {
    return yfn(7)
})
```

The cached `GoFunction` was created from the parent VM's coroutine library, but
the call executes while a child coroutine VM is running. If the implementation
only compared function pointers against the child VM's own `coroutineYieldFn`,
the cached function would miss the fast path and call the parent-bound closure.
That would report "cannot yield from outside a coroutine" even though the
program is inside one.

The fix is to recognize the VM-native coroutine functions by their stable names
as well as by pointer identity:

```
coroutine.resume
coroutine.yield
coroutine.isyieldable
```

This keeps cached standard-library function values behaving like the table
lookup form.

## The Frame Boundary

Yield can happen inside a nested call:

```
func helper() {
    x := coroutine.yield(10)
    return x + 1
}

co := coroutine.create(func() {
    y := helper()
    return y * 2
})
```

The child VM may have more than one inline frame active when the yield occurs.
The new path intentionally preserves that frame stack. The old error-cleanup
logic would reset `frameCount` on any error; the sentinel yield must not be
treated like a normal error. If the VM threw away the nested frame, the next
resume would continue from the wrong place.

So the run loop now treats `errCoroutineYield` specially:

```
ordinary error: reset frames to the entry boundary
yield sentinel: preserve frames and return to resume caller
```

When the coroutine later returns or errors for real, the child VM is released
and unregistered from the runtime root scanner. That prevents completed
coroutines from leaving child VMs in the active GC root set.

## The Tests

The tests now cover the cases that were easy to miss:

```
basic yield/resume
passing values into yield
multiple yielded values
yield inside nested function calls
cached coroutine.yield
cached coroutine.isyieldable
nested coroutine resume
Value.Ptr compatibility
error after yield marks coroutine dead
coroutine.wrap for-range
leaf no-call coroutine fast path
```

The important tests are not only the happy-path generator examples. The cached
function tests prove the optimization respects standard-library function values
as first-class values. The error-after-yield test proves that the internal
sentinel does not swallow real errors and that the coroutine becomes dead after
the error. The `Value.Ptr()` test protects host-side compatibility.

## The Result

The final focused guard after hardening:

```
coroutine_bench    VM 0.028s    Default 0.030s    NoFilter 0.030s
sieve              Default 0.028s
method_dispatch    Default 0.001s
closure_bench      Default 0.028s
```

The standalone stats run showed the cost distribution after the change:

```
yield_loop(100000):    0.008s
create_resume(50000):  0.013s
generator(100000):     0.007s
Time:                  0.028s
```

The leaf no-call path still matters for the create/resume section, but the huge
win is the long-lived yield/resume path. It moved from channel handoff cost to
VM frame-resume cost.

The stats still report two long-lived coroutine starts because the counter name
comes from the old implementation. At this point it means "started a long-lived
child VM path" more than "started a Go goroutine." That name should probably be
cleaned up in a later diagnostics pass, but the counter remains useful for
separating leaf fast paths from long-lived coroutine paths.

## The Lesson

Not every performance cliff belongs in Tier 2.

`coroutine_bench` became about fifty times faster without compiling a single
Tier 2 function in that benchmark. The VM was doing the wrong primitive
operation for the language feature. Once the coroutine stayed on the VM stack,
the JIT no longer had to compensate for a runtime handoff that should not have
been there.

This is part of the same larger rule as the method-JIT work: the fast path is a
contract. For coroutines, the contract is not "yield blocks a goroutine." The
contract is "yield returns values to the resumer, then later receives resume
values and continues from the same VM frame." The new implementation encodes
that contract directly.

That is why this one moved so much.
