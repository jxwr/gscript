---
layout: default
title: "The Load That Did Not Replay The Write"
permalink: /60-the-load-that-did-not-replay-the-write
---

# The Load That Did Not Replay The Write

*April 2026 - Beyond LuaJIT, Post #60*

The dangerous table bug was never that the compiler could not lower a table
array load. It could. The dangerous part was what happened when the lowered
load was wrong.

The tempting fix was to disable typed array lowering in any function that also
mutates a table. That would have been safe in the narrowest possible sense, and
it would also have been a performance mistake. Matmul reads from `a` and `b`
while writing to `row` and `c`. Sort mutates one array and reads from the same
array. Many real programs mix reads and writes in the same function. A
function-level "mutation exists" bit throws away too much information.

This round took the harder route: keep the successful typed load path, but make
the miss path precise enough that it does not replay earlier side effects.

The change is not a headline speedup. In fact, the most sensitive array
benchmark moved from roughly 0.030s to 0.032s in local multi-run checks. That
is a cost, and it should be recorded honestly. The reason to take the patch is
that it removes a correctness barrier that would otherwise force a coarse gate.
The next table optimizations need this protocol to exist.

## The Replay Problem

The lowered table array path splits a dynamic `GetTable` into stable pieces:

```
header = TableArrayHeader(t)
len    = TableArrayLen(header)
data   = TableArrayData(header)
value  = TableArrayLoad(data, len, key)
```

That shape lets the compiler hoist `header`, `len`, and `data`, share them
between repeated reads, and keep the element load small. It is the right
success path. The problem is the failure path.

Suppose the original program is:

```
arr[1] = arr[1] + 1
return arr[key]
```

If `arr[key]` misses the typed load guard, the JIT cannot simply restart the
function from the beginning or from an earlier bytecode. Restarting would run
the write again. The result would be a program that increments `arr[1]` twice
because a later read missed a type or bounds guard.

That is the bug class behind the table mutation work. The compiler needs to
know not just how to exit, but where execution resumes and which side effects
have already happened.

## Why The Coarse Gate Was Wrong

One attempted direction was:

```
if function contains SetTable:
    do not lower GetTable into TableArrayLoad
```

It is easy to reason about and terrible for a method JIT. It punishes every
read in a function for the existence of any write, even when the read and write
refer to different objects. In matmul, that means reads from the input matrices
lose the typed array path because the function writes output rows. In a loop
with object arrays, it means a single append can block all typed reads.

The precise invalidation work from the previous round already moved away from
that model. `SetTable`, `Append`, and `SetList` invalidate table-array facts for
the mutated object. They do not poison the entire function. This round extends
the same idea to exits: the failure of a lowered load should recover exactly
the original load, not rewind unrelated earlier effects.

That is the difference between a guard and a recovery protocol.

## Recovering The Original Table

The hot `TableArrayLoad` operand shape stayed unchanged:

```
TableArrayLoad(data, len, key)
```

The load does not carry the table receiver directly. That is intentional; the
hot path wants the data pointer, length, and key. Adding the table as another
hot operand would increase register pressure and make the common path worse.

For the cold miss path, the compiler recovers the receiver from the metadata
chain:

```
data -> header -> table
```

That is enough to reconstruct the original dynamic operation:

```
result = table[key]
```

The miss path stores the table and key into their VM home slots, records a
`TableOpGetTable` exit, and asks the Go table-exit handler to perform exactly
the original lookup. Then the JIT resumes after the typed load instruction.

This is the key point: the fallback operation is not "restart the function" and
not "turn off Tier 2". It is "perform the dynamic table read that this one
lowered instruction represented, write its result into this result slot, and
continue from the instruction after it."

The hot path remains:

```
bounds/type guard
load element
store result
continue
```

The cold path becomes:

```
materialize table/key
ExitTableExit(TableOpGetTable)
Go RawGet
reload live registers
check result representation
continue
```

That is heavier than the old deopt. It is also precise.

## Precise Deopt

There are still typed-load failures that should not use the table-exit
continuation. If the recovered dynamic read returns a value whose representation
does not match the type the optimized code expected, continuing in the same
native frame would be wrong. For example, a site specialized as integer cannot
continue with a table value in the raw-int register convention.

Those failures now use precise interpreter resume. The JIT flushes the current
frame, writes `ExitResumePC`, and returns to Go with `ExitDeopt`. The Tiering
Manager disables the compiled function and asks the VM to resume the current
call frame at the exact bytecode PC of the guard.

That is different from the older runtime deopt behavior, which simply reported
"tier2: deopt" and let the VM fall back from a coarser boundary. The exact PC
matters after a side effect. If a table write has already executed, the VM must
continue at the read that failed, not at the beginning of the function.

The implementation also fixed a small protocol detail while reviewing the
worker patch: bytecode PC `0` is a valid resume point. The guard used to treat
`SourcePC <= 0` as "not precise"; it now treats only negative PCs as invalid.

## The Checker Had To Be Precise Too

The production path was not the only protocol that needed tightening. The
exit-resume checker tracks live slots before and after fallback to prove that
the fallback operation materializes and preserves the right state.

For a `TableArrayLoad` miss, the current load's result is not a live input. It
is an output. Codegen state is tricky here because the fast path has already
been emitted before the cold label is emitted, so the compiler's active-register
map may contain the result value. If the checker treats that value as a
pre-exit live input, it can validate the wrong contract.

The merged version records a table-array-load exit site with the current load
result removed from the live input set and listed as a modified slot. That says
what actually happens:

```
before fallback: table and key must be valid
fallback writes: result slot
after fallback: other live slots must be preserved
```

That sounds minor. It is not. Debug protocols that tolerate imprecise state
eventually become useless, because every failure turns into "probably checker
noise." If the checker says an exit is valid, it should be validating the same
contract production relies on.

## The Tests

The main regression test is intentionally small:

```
func bump_then_read(arr, key) {
    arr[1] = arr[1] + 1
    return arr[key]
}
```

Warm it with an integer key so the compiler keeps typed table-array lowering.
Then call it with a key that misses the optimized load path. The expected
behavior is:

```
arr[1] increases by exactly 1
the miss returns the dynamic table result
```

If the exit path replays the earlier write, `arr[1]` increases by 2. If it
resumes at the wrong point, the return value or side effect count diverges. The
test runs both the standalone compiled-function path and the production
TieringManager path, with `GSCRIPT_EXIT_RESUME_CHECK=1` covering the precise
state protocol.

The lowering test also asserts the architectural decision directly:

```
SetTable before same-table read still lowers to TableArrayLoad
```

That is the point of this round. We did not make mutation a global veto. We
made the recovery path precise enough that mutation can coexist with typed
loads.

## The Numbers

Focused validation after merging:

```
table_array_access    ~0.032s, exits 457
sort                  ~0.046s to 0.048s, exits 17
matmul                ~0.091s to 0.097s, exits 32
fibonacci_iterative   ~0.025s
```

The full guard produced one noisy `sort` red run at 0.061s, then a five-run
focused guard brought it back to 0.046s. The stable red item remains
`closure_bench`, which is a separate mutable-upvalue closure-call problem.

The honest cost is `table_array_access`: the previous branch often measured
around 0.029s to 0.030s, while the precise replay branch measured around
0.032s. Exit counts did not change. `-exit-stats` shows the table-array
benchmark is still dominated by `SetTable`, `NewTable`, and `GetField` exits,
not by the new `TableArrayLoad` miss path:

```
ExitTableExit: 408
  SetTable dominates
  NewTable remains visible
  GetField appears in top-level reporting
```

So this is not the optimization that makes table-array access faster. It is the
infrastructure that prevents the next table optimization from being forced into
a coarse safety gate.

## The Rule

The rule from this round is:

Do not disable an optimization because recovery is imprecise. Make recovery
precise.

That does not mean every precise recovery protocol is free. This one is not.
It adds cold code, table-exit metadata, a result-type check after fallback, and
a precise interpreter resume path for representation mismatch. But the
alternative was worse: a function-level mutation veto that would permanently
block typed table loads in exactly the programs we need to speed up.

Method JIT performance comes from keeping the common path narrow while making
the uncommon path correct enough that the common path is allowed to exist.
This round was about the second half of that sentence.
