---
layout: default
title: "The Exit Edge Finally Split"
permalink: /62-the-exit-edge-finally-split
---

# The Exit Edge Finally Split

*April 2026 - Beyond LuaJIT, Post #62*

`nbody` had the opposite problem from `fibonacci_iterative`.

Fibonacci was missing a specialized execution strategy for a narrow arithmetic
shape. Once the compiler recognized the recurrence and emitted a raw prefix with
a float continuation, the benchmark moved dramatically.

`nbody` already had the right general idea in the compiler: scalar promote the
float fields of the body records, keep them in SSA values through the hot loop,
and write them back only when control leaves the promoted region. That is a
standard optimization. The bug was more ordinary and more frustrating. The pass
could do the transformation on clean synthetic IR, but the production function
had an exit block shape the pass refused to rewrite.

The focused result after this round was modest but real:

```
nbody          before ~0.082s / ~0.073s    after ~0.074s / ~0.075s
spectral_norm 0.024s                       no regression
mandelbrot    0.051s                       no regression
```

This is not the LuaJIT finish line. Local LuaJIT runs are still around
0.033s to 0.034s for the same benchmark family, so the remaining gap is roughly
2.2x. But the nature of the gap changed. Before this patch, the method JIT was
leaving a known scalar-promotion opportunity unused because the CFG was a little
less polite than the unit test. After this patch, that specific blocker is gone,
and the production test now proves it stays gone.

## The Hot Shape

The benchmark stores each body as a table-like record with fields such as
position, velocity, and mass. The inner loop repeatedly reads and writes float
fields:

```
dx = body1.x - body2.x
dy = body1.y - body2.y
dz = body1.z - body2.z
...
body1.vx = body1.vx - dx * mag
body2.vx = body2.vx + dx * mag
```

If every `x`, `y`, `z`, `vx`, `vy`, and `vz` access goes through a table field
operation, the machine spends too much time rediscovering stable facts. The
shape of the body records is stable. The fields are numeric. The loop mutates a
small set of fields many times before the values need to be visible outside the
loop. Scalar promotion is exactly the right optimization here:

```
field load before loop
loop-carried float phi
float arithmetic in the loop
field store on exits
```

The compiler already had a pass that could reason about this. It could identify
the field, create the promoted float value, wire loop-carried phis, and arrange
stores on exit. The earlier tests made that look solved.

The tests were too clean.

## The Edge That Was Not Clean

In the production `nbody` CFG, one loop exit did not go to a block that belonged
only to that exit. It landed in a block that also had outside predecessors. That
matters because scalar promotion has to insert a writeback on the edge leaving
the promoted loop.

Conceptually the transformation wants this:

```
loop block -> exit block
```

to become this:

```
loop block -> writeback block -> exit block
```

The writeback block is where the promoted SSA values become table fields again.
If the original exit block has only the loop as predecessor, the pass can place
the stores at the top of the exit block. If the exit block also receives control
from somewhere outside the promoted region, that is no longer correct. Those
outside paths did not run the promoted loop and therefore do not have the same
promoted live values. Placing writebacks in the shared block would either use
the wrong values or require extra guards and phis that the pass did not build.

The old pass chose the conservative answer: if the exit block was shared, do not
promote that field across the exit.

That was safe, but it left `nbody` stuck. The production CFG had exactly the
shared-exit shape that the pass rejected, so the benchmark kept paying for field
loads and stores that the architecture intended to eliminate.

## Splitting One Edge

The fix is not to make the pass less careful. The fix is to give the pass the
CFG shape it needs.

When there is exactly one loop-exit edge to a shared destination, the compiler
now creates a dedicated landing block for that edge:

```
before:

  loop latch ----\
                 +--> shared exit
  outside pred --/

after:

  loop latch ------> promoted writeback block --> shared exit
  outside pred -------------------------------/
```

Only control that actually leaves the promoted loop flows through the new block.
The writeback stores are therefore placed on the right path, and the shared exit
still receives normal control from the outside predecessor.

This is the same principle as critical-edge splitting in a conventional SSA
compiler. The important detail is that the split is not a general CFG cleanup
pass. It is scoped to the scalar-promotion need: one loop-exit edge, one
dedicated place to materialize promoted fields, no change to unrelated control
flow.

That matters because scalar promotion is already manipulating values, phis, and
field operations. A broad CFG rewrite would make the pass harder to validate
and easier to blame for unrelated block-order bugs. A targeted split keeps the
contract small:

1. If an exit destination is shared, isolate the loop-exit edge.
2. Insert promoted field materialization in the isolated block.
3. Continue into the original destination unchanged.

The pass still declines shapes it cannot model safely.

## The Metadata Bug That Would Have Hidden The Win

Splitting the edge was necessary, but it was not sufficient.

The field operations created by scalar promotion are not ordinary field
operations from the source program. They are synthetic loads and stores emitted
by the optimizer to preserve the program's observable table state. They still
need the same field-cache metadata as the original operations.

In this codebase that metadata lives in `Aux2`. It carries the cached field
layout information that lets the native path avoid falling back to a generic
table access. If a synthetic `GetField` or `SetField` loses that metadata, the
compiler can appear to have promoted a field while the generated code quietly
falls back on the materialization path. That kind of bug is especially
misleading: the IR dump looks more optimized, but the machine path is not.

This round preserves the `Aux2` field-cache metadata when scalar promotion
creates the synthetic field materialization operations. That is the difference
between "we inserted the writebacks" and "the writebacks still use the fast
field path."

It also gives the test a sharper failure mode. If a future change drops the
metadata, the pass may still produce a plausible block graph, but the native
field path will no longer match the intended architecture.

## The Test Changed From Observation To Assertion

The most important testing change was not a new toy example. It was promoting
the production benchmark shape from "print what happened" to "this must happen."

The production `nbody` scalar-promotion test now asserts that the compiled
advance function keeps the expected scalar-promotion structure:

```
unpromoted <= 6
floatPhis >= 3
```

Those numbers are intentionally structural rather than timing based. A unit
test should not depend on whether this laptop is busy or whether the OS decided
to move a process. It should assert the compiler fact that makes the benchmark
fast: most of the hot float fields became promoted loop values, and enough float
phis exist to prove the loop-carried field values are actually in SSA.

There is still a synthetic critical-exit test, because a minimal CFG is the best
way to isolate the edge-splitting behavior. But it is no longer the only proof.
The production test is the guardrail that would have caught the original bug.

That is the lesson from this round. Compiler tests that only exercise friendly
IR can validate a transformation algorithm without validating the benchmark
pipeline. For performance work, both are necessary:

```
synthetic test:  prove the local transformation
production test: prove the real pipeline reaches that transformation
```

Before this patch, the first line existed and the second line was too weak.

## Why This Is Still Method JIT Work

It is tempting to compare `nbody` to trace JIT behavior. A trace compiler would
naturally see the hot field path, record stable field accesses, and keep values
in registers across the recorded loop. That is one of the reasons LuaJIT is so
strong on this family of benchmarks.

But the method JIT can get the same class of win without becoming a trace JIT.
It just has to carry the facts through a different representation:

```
record/table shape fact
field-cache metadata
SSA loop value
exit materialization
precise fallback path
```

The hard part is not believing that `body.x` is usually a float. The hard part
is placing the materialization exactly where all exits can observe the right
value and no non-loop path observes a fabricated one. That is why the CFG split
belongs in the foundation rather than in a benchmark-specific patch.

This is also why "Tier 2 should always be faster than Tier 1" was the wrong
mental model. Tier 2 is only faster when its stronger assumptions line up with
a complete exit and materialization contract. If Tier 2 compiles a loop but
falls back on every field access or misplaces the writeback, it can easily be
slower than a simpler Tier 1 path. The tier is not the guarantee. The completed
contract is the guarantee.

## What Remains

The remaining `nbody` gap is still large enough that this cannot be called done.
The next wins are likely on the success path, not only on exit reduction:

```
keep more body fields register-resident across the full inner loop
reduce redundant field writebacks when the value is not observed
improve float load/store scheduling around promoted values
avoid rebuilding table access context when metadata already proves the shape
```

Those are harder than the edge split because they are closer to codegen quality.
The edge split was a correctness-shaped performance bug: the optimizer knew what
it wanted but lacked a legal block to put it in. The next layer is about how
well the generated ARM64 uses the promoted values once the IR is finally right.

Still, this round removed a real blocker. The production compiler can now handle
a shared exit block in the scalar-promotion path, preserve the field metadata
needed by native materialization, and assert that the actual `nbody` pipeline
keeps enough float state in SSA.

That is the kind of foundation a method JIT needs if it is going to keep
closing the LuaJIT gap without switching to traces.
