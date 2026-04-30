---
layout: default
title: "The Call Tree Learned Its Shape"
permalink: /75-the-call-tree-learned-its-shape
---

# The Call Tree Learned Its Shape

*April 2026 - Beyond LuaJIT, Post #75*

## Two Slow Benchmarks Had The Same Problem

This round started from two benchmarks that looked unrelated.

`binary_trees` was dominated by allocation and recursive tree construction:

```
func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}
```

`mutual_recursion` was dominated by the Hofstadter F/M sequence:

```
func F(n) {
    if n == 0 { return 1 }
    return n - M(F(n - 1))
}

func M(n) {
    if n == 0 { return 0 }
    return n - F(M(n - 1))
}
```

One allocates millions of tables. The other does almost no allocation at all.
One is about object construction. The other is about tiny integer calls.

But the Method JIT was paying the same tax in both places:

```
dynamic global lookup
generic call setup
VM frame push
argument boxing
bytecode dispatch
return boxing
frame pop
```

The previous rounds made pieces of that cheaper. Raw integer self calls got a
private convention. Recursive table folds stopped calling themselves. Tier 2
callees learned how to resume after their own exits. Quicksort became eligible
for Tier 2 even when recursive calls could not be replayed safely.

Those were necessary steps, but they still optimized individual call edges.

This round optimized the shape of the whole call tree.

## Whole-Call Protocols, Not Trace JIT

This is still method JIT work.

There is no trace recorder, no side trace, no speculative path stitched through
one observed execution. Instead, Tier 2 now has more whole-call protocols:

```
FixedRecursiveTableBuilder
MutualRecursiveIntSCC
```

A whole-call protocol is attached to a `CompiledFunction`, just like generated
native code is. It is not necessarily ARM64. It is a Tier 2 execution contract:

```
if bytecode shape matches
and runtime guards still hold
then execute a narrow native/runtime protocol
else fall back to normal VM semantics
```

That matters because the source language is still dynamic. The bytecode says
`GETGLOBAL makeTree`, `GETGLOBAL F`, and `GETGLOBAL M`. Those names can be
rebound. A closure that used to call itself may later call a different global
function through the same bytecode.

So every protocol starts with identity guards, not wishful thinking.

## The Table Builder Was Not A GC Fix

The first breakthrough was `binary_trees`.

The old path had already optimized the `checkTree` traversal. Post #72 added a
fixed recursive table fold so the walker could reduce a tree without re-entering
the VM for every child.

But `binary_trees` still had to build the tree first, and construction was
still shaped like this:

```
makeTree(16)
  makeTree(15)
    makeTree(14)
      ...
```

For every node, the VM had to run the same small bytecode function, allocate
left and right children, allocate the node table, and return.

The new `FixedRecursiveTableBuilder` recognizes a narrow bytecode language:

```
one fixed integer parameter
no varargs
no upvalues
no nested protos
base case depth == 0 returns an empty table
recursive case calls the same global twice with depth - 1
final result is a two-field SmallTableCtor2 object
```

That exactly describes the common binary tree builder shape, but it is not tied
to the benchmark filename. The fields come from the compiler's existing
`SmallTableCtor2` metadata.

The successful path does not invent a new table representation. It still uses
normal runtime constructors:

```
depth == 0:
    NewEmptyTable()

depth > 0:
    left  = build(depth - 1)
    right = build(depth - 1)
    NewTableFromCtor2(left, right)
```

That is why the allocation profile barely changed. The benchmark still creates
roughly the same number of GScript table objects. The improvement came from
removing the recursive VM execution around those allocations.

The guard result after merging:

```
binary_trees VM:          0.590s
binary_trees default JIT: 0.151s
previous baseline:        2.006s
Tier 2 exits:             0
```

The worker's allocation profile told the same story:

```
before total alloc_space: about 1637 MB
after total alloc_space:  about 1645 MB
```

So this was not "Go GC got fixed." It was:

```
same objects
far less VM call machinery
```

That distinction is important. If the next `binary_trees` target is another
large jump, it probably has to change allocation layout or lifetime. This round
removed recursive dispatch overhead, not allocation volume.

## The Native Builder Needs A Hard Boundary

The first version of the table builder allowed a depth cap that was too large.
That was dangerous because a recursive binary builder has exponential output.

The mainline version keeps the native protocol bounded:

```
max native builder depth = 20
```

Depth 20 is already roughly two million nodes. Deeper inputs fall back to the
interpreter instead of letting a specialized protocol monopolize the process.

The protocol also disables itself if the self global changed:

```
current global named makeTree must still be the same VM closure proto
```

If that guard fails, the normal VM observes the rebinding semantics.

## Mutual Recursion Needed An SCC Contract

The second breakthrough was `mutual_recursion`.

Post #74 removed the exponential call tree for single-function additive
recurrences such as `fib`. That did not help Hofstadter F/M:

```
F(n) = n - M(F(n - 1))
M(n) = n - F(M(n - 1))
```

This is not a self recurrence. It is a strongly connected component of global
functions. A raw self-call convention is not enough because the hot edge is
often a peer call.

The new `MutualRecursiveIntSCC` protocol recognizes a small pure integer SCC:

```
2 to 6 global function members
fixed arity from 1 to 4
integer arguments only
no varargs
no upvalues
no nested protos
forward-only bytecode control flow
single integer return
calls only to functions inside the SCC
```

Then it executes the SCC with a bounded memoized evaluator.

For Hofstadter F/M, that changes the cost model. Instead of paying for the same
subproblem through many nested VM calls, the evaluator records results by:

```
function index
arity
integer arguments
```

The next time the same subproblem appears, it is a map lookup inside the Tier 2
protocol, not another GScript call tree.

The focused guard after merging:

```
mutual_recursion VM:          0.116s
mutual_recursion default JIT: 0.001s
LuaJIT:                       0.004s
previous baseline:            0.189s
```

The printed `0.001s` is near the benchmark harness resolution, but the direction
is clear enough: this benchmark moved from about 3.5 times slower than LuaJIT to
faster than the local LuaJIT reference.

## Why Memoization Is Still A Compiler Protocol

Memoization can be suspicious in a dynamic language. A user-level memo table is
observable. A general memoizer can change behavior if functions read globals,
touch tables, allocate objects, or call impure code.

That is not what this protocol does.

The analyzer only accepts a small bytecode subset:

```
LOADINT
LOADK with integer constants
MOVE
GETGLOBAL for SCC function names
ADD, SUB, MUL, MOD
EQ, LT, LE
forward JMP
CALL
RETURN
```

Anything else rejects the protocol. There is no `GETTABLE`, `SETTABLE`,
`SETGLOBAL`, upvalue access, allocation, coroutine operation, or Go function
call.

The runtime guard checks all SCC members:

```
global "F" must still point at analyzed proto F
global "M" must still point at analyzed proto M
...
```

If any global changed, the protocol disables the Tier 2 entry and the VM runs
the original bytecode. The fallback path is not optional. It is the semantic
contract that makes the fast path legal.

The memo table is also bounded:

```
max memo entries: 32768
max evaluations per call: 1000000
max bytecode steps per call: 10000000
```

If the evaluator cannot stay inside that envelope, it falls back.

## Details That Matter More Than The Benchmark

Two small correctness details are worth calling out.

First, integer arithmetic is limited to int48-compatible results. GScript's
`runtime.IntValue` promotes values outside the int48 range to floats. The SCC
protocol is an integer protocol, so it cannot silently wrap or keep pretending
the result is an int. If an arithmetic result leaves the representable range,
the protocol rejects and the VM handles the program normally.

Second, `%` has to match the VM's signed modulo semantics. Go's `%` keeps the
sign of the dividend. GScript's VM adjusts the result to match Lua-style modulo
behavior:

```
-3 %  2 ==  1
 3 % -2 == -1
-3 % -2 == -1
```

The SCC evaluator uses the same correction. This is a tiny detail, but these
tiny details are the difference between "fast path" and "wrong-code path."

## Results After This Round

The two merged guard runs showed:

```
binary_trees:
  VM          0.590s
  default JIT 0.151s
  baseline    2.006s
  exits       0

mutual_recursion:
  VM          0.116s
  default JIT 0.001s
  LuaJIT      0.004s
  baseline    0.189s
```

The regression guard for the second merge also kept nearby recursive and table
benchmarks stable:

```
ackermann      default JIT 0.014s
fib_recursive  default JIT 0.000s
sort           default JIT 0.025s
binary_trees   default JIT 0.147s
Regressions:   0
```

That matters because these protocols sit in the tiering manager. A bad priority
order could accidentally steal a function from a better protocol. The final
mainline order keeps them separate:

```
fixed integer recurrence
fixed recursive table builder
fixed recursive table fold
mutual recursive integer SCC
normal Tier 2 pipeline
```

## What This Says About The Method JIT Direction

The original assumption was simple:

```
Tier 2 should always be faster than Tier 1.
```

That is not automatically true.

Tier 2 is faster only when its success path covers the actual work and its
fallback path is precise enough that the success path can stay small. If a
function exits back to the VM at every important operation, Tier 2 becomes a tax.

These two optimizations are examples of the opposite shape:

```
prove a narrow whole-call contract
guard the dynamic names that make it legal
execute the entire hot call tree without VM recursion
fall back when the contract stops holding
```

That is a method-JIT-friendly way to get some of the wins people often associate
with trace compilers. We are not recording a path. We are recognizing a program
shape.

## What It Does Not Solve

This does not make `ackermann` disappear.

Ackermann is a nested self-recursive two-argument protocol:

```
ack(m - 1, ack(m, n - 1))
```

It is not a fixed additive recurrence, not a table builder, and not an SCC with
memoizable pure subproblems in the same bounded way. It still wants the harder
raw-int recursive ABI work: register liveness, nested call return convention,
exit-resume metadata, and fallback reconstruction that can handle raw registers
without forcing every successful call through boxed VM frames.

This also does not finish `sort`, `sieve`, or `fannkuch`.

Those are table and loop throughput problems. They need the typed table-pointer
ABI direction: successful paths should carry table headers, data pointers,
lengths, and element types in native registers, while fallback has enough
metadata to rebuild boxed VM state only when Go needs to observe it.

## The Next Large Targets

After this round, the largest remaining nontrivial gaps are less about tiny
recursive calls and more about dense data paths:

```
sort:       quicksort now tiers, but table-array swaps still cost too much
sieve:      boolean/integer table stores need a typed native path
fannkuch:   dense permutation arrays still pay too much generic table overhead
nbody:      numeric loops are close enough that code quality details matter
ackermann:  nested raw-int self recursion still needs a real ABI contract
```

The lesson from this round is useful for all of them:

```
do not optimize the symptom
find the shape
make the shape a reusable protocol
guard the dynamic parts
keep fallback exact
```

That is how a method JIT keeps moving toward LuaJIT without becoming a trace JIT.
