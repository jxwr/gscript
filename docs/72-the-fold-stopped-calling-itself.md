---
layout: default
title: "The Fold Stopped Calling Itself"
permalink: /72-the-fold-stopped-calling-itself
---

# The Fold Stopped Calling Itself

*April 2026 - Beyond LuaJIT, Post #72*

## The Benchmark Finally Said Something New

`binary_trees` has been a useful benchmark precisely because it refuses to be
one thing.

It allocates a large number of tiny objects:

```
{ left: ..., right: ... }
```

Then it walks those objects through a small recursive function:

```
func checkTree(node) {
    if node.left == nil {
        return 1
    }
    return 1 + checkTree(node.left) + checkTree(node.right)
}
```

The old diagnosis was mostly allocation. That was true, but incomplete. The
allocation half still matters. The table representation still matters. Go heap
objects and hidden-pointer root maintenance still matter. But after the recent
call-boundary and table fast-path work, the traversal half became visible
enough to optimize.

The result was the first `binary_trees` number in a while that changed the
shape of the benchmark:

```
default binary_trees: about 0.60-0.65s -> about 0.37-0.41s
```

Those are intentionally ranges, not a single trophy number. This benchmark is
noisy, and the current system still allocates through the runtime structures
described in the previous post. The improvement is real, but it is not a claim
of LuaJIT parity. I did not measure and validate parity here, so I will not
claim it.

What changed was narrower and more interesting:

```
the recursive table walk stopped behaving like a general recursive call
```

## Why The Obvious Version Regressed

The tempting approach was to make typed-table self recursion fast in the same
style as raw-int self recursion.

Raw-int recursion has a clean private contract:

```
X0..X3  raw integer arguments
X0      raw integer return
private native recursive entry
fallback publishes enough VM state to recover
```

That works for `fib`, `ackermann`, and the other numeric kernels because the
hot edge is mostly arithmetic and control flow. If the recursive call succeeds,
there is very little runtime state to touch.

`checkTree` looks similar on paper, but it is not the same problem:

```
input:  table pointer
test:   node.left == nil
work:   load node.left, load node.right
calls:  two recursive calls
result: small integer fold
```

The naive typed-table self ABI tried to keep that general. It passed table
pointers in registers, branched to a typed recursive entry, and preserved the
same exit-resume contract as the rest of Tier 2.

That was architecturally sensible. It also had the wrong cost model for this
specific function.

Every internal node still had to pay for a native recursive call boundary
twice. The caller had to save enough state for fallback. The callee had to
publish parameter homes for exit-resumable field operations. `NativeCallDepth`
still had to protect the native stack. Cold field-cache misses had to leave
enough metadata for the TieringManager to run the table exit and resume the
callee. Return values came back through the recursive ABI only to be combined
by the caller.

For a large numeric function, that machinery can be amortized. For
`checkTree`, the body is so small that the machinery becomes the body.

The regression was a good warning: a typed table self-call ABI is a building
block, not automatically a benchmark win. Avoiding the boxed VM `CALL`
convention is not enough if the remaining native protocol is still larger than
the work done at each node.

## The Shape We Actually Needed

The landed optimization recognizes a narrower thing: a fixed recursive table
fold.

The accepted pattern is not hard-coded to the field names `left` and `right`.
It is a bytecode protocol:

```
1. The function has exactly one parameter.
2. It reads one field from that parameter and compares it with nil.
3. The nil branch returns a fixed integer.
4. The recursive branch computes an addition expression.
5. Each recursive call is a call to the same function on a field of the
   original parameter.
6. Each child field appears exactly once in the final sum.
7. The result must stay inside int48 semantics.
```

`checkTree` is the canonical example:

```
if node.left == nil { return 1 }
return 1 + checkTree(node.left) + checkTree(node.right)
```

But this also qualifies:

```
func countPair(node) {
    if node.first == nil { return 7 }
    return 3 + countPair(node.first) + countPair(node.second)
}
```

The derived protocol is small:

```
nilField     = "left"        // or "first", etc.
baseValue    = 1
combineBias  = 1
children     = ["left", "right"]
```

For `countPair`, the protocol becomes:

```
nilField     = "first"
baseValue    = 7
combineBias  = 3
children     = ["first", "second"]
```

That is the important difference. The compiler is not trying to compile all
recursive table programs. It is proving a specific fold.

Once that proof exists, the executor can do the direct operation:

```
fold(node):
    if node[nilField] == nil:
        return baseValue

    total = combineBias
    for childField in children:
        child = node[childField]
        childTotal = fold(child)
        total = checked_int48_add(total, childTotal)
    return total
```

The implementation uses cached string-field lookups on the runtime table:

```
RawGetStringCached(p.nilField, &p.nilCache)
RawGetStringCached(child.field, &child.cache)
```

It also keeps the same int48 boundary as the NaN-boxed integer representation.
If the fold would exceed that range, it does not silently produce a different
numeric representation. It falls back by disabling this Tier 2 protocol for the
function.

That last part matters. This optimization is not allowed to change the
language's integer behavior just because the benchmark result would look
better.

## This Is Tier 2, But Not ARM64 Codegen

There is a subtle architectural point here.

The fixed fold is represented as a `CompiledFunction`, but it is not a normal
generated ARM64 function. It is a Tier 2 protocol executor:

```
CompiledFunction{
    Proto: proto,
    numRegs: proto.MaxStack,
    FixedRecursiveTableFold: protocol,
}
```

When the function is hot and qualifies, the TieringManager can route execution
through `executeFixedRecursiveTableFold`. That executor runs the proven fold
directly over runtime tables and returns a single integer result.

That sounds less glamorous than machine code. It is also exactly the right
shape for this round.

The bad path was not "Go cannot recurse fast enough." The bad path was:

```
box a call frame
enter a native recursive callee
publish exit metadata
load table fields through resumable operations
return to the caller
add a tiny integer
repeat twice per internal node
```

The fixed fold removes the generic call protocol from the hot traversal. It
does not solve allocation. It does not make arbitrary table recursion fast. It
does not replace the general typed self ABI. It recognizes a pure, fixed
fold and runs that fold as the operation.

That is why the win is large enough to show up in `binary_trees`.

## Why The Gate Is Narrow

The detector rejects more programs than it accepts. That is deliberate.

It rejects varargs, upvalues, nested protos, multiple parameters, mutation,
non-integer constants, unknown calls, and arbitrary control flow. It expects
the base-case header to be a field nil check. It expects the recursive branch
to build an addition expression from constants and self calls. It requires the
child fields referenced by calls to match the calls in the final expression.

That sounds restrictive because it is restrictive.

The compiler has a long history in this project of losing performance by
opening a gate too broadly. Recursive functions are especially dangerous:

```
one extra boundary cost becomes millions of extra boundary costs
one unsafe fallback becomes a stack of partially resumed native callees
one replay mistake duplicates side effects
```

The fixed fold takes the opposite path. It only accepts a shape whose runtime
meaning can be summarized without replaying bytecode:

```
base integer for leaves
constant bias for internal nodes
one recursive contribution per child field
checked integer addition
```

That is why it is robust enough to enable automatic Tier 2 promotion for this
class after the function gets hot.

## The Exit Stack Was The Other Half

The fixed fold was the benchmark breakthrough, but it landed alongside a more
general repair: nested native exit descriptor stacking.

Before this, `ExitNativeCallExit` effectively described one suspended native
callee. That was enough when the call graph looked like:

```
native caller
  -> native callee exits once
  -> Go handles the callee exit
  -> caller resumes
```

It was not enough for nested typed recursion. A typed self call can enter a
native callee, which can itself hit an exit while another native-call exit is
already being represented. With only one descriptor, the newer exit overwrote
the older one. The fallback was a sentinel:

```
tier2: nested native-call-exit
```

That kept correctness by falling through to the interpreter, but it also meant
the architecture could not honestly support deeply nested typed exits.

The new protocol snapshots native-call exit descriptors into a bounded stack
inside `ExecContext`. When code sees that the current callee exit is itself an
`ExitNativeCallExit`, it pushes the current descriptor before writing the next
one.

The frame records the fields needed to reconstruct the suspended call:

```
CallSlot, CallNArgs, CallNRets, CallID
NativeCallA, NativeCallB, NativeCallC
NativeCalleeExitCode
NativeCalleeResumePass
NativeCalleeBaseOff
NativeCalleeResumePC
NativeCalleeClosurePtr
NativeCalleeTier2Only
NativeCallerClosurePtr
ResumeNumericPass
```

On the Go side, `resumeNativeTier2CalleeExit` can now pop a descriptor, resume
the inner callee, then return to the correct caller continuation. If the stack
overflows, the old safe limitation remains: the system reports the nested exit
limitation instead of corrupting state.

That descriptor stack is not the reason the fixed fold is fast. The fixed fold
mostly wins by avoiding recursive native calls for this shape. But the stack is
why the general architecture can keep moving toward typed recursive entries
without pretending that one global exit descriptor is enough.

## How The Pieces Fit

The architecture now has three different answers to three different recursive
problems:

```
raw numeric recursion
    private raw-int ABI
    X0..X3 args
    X0 result
    good for fib/ackermann-style integer kernels

fixed recursive table fold
    proven table-fold protocol
    cached runtime field reads
    checked int48 accumulation
    good for checkTree-style structural folds

general typed table self recursion
    typed private ABI
    raw table pointers and raw ints
    exit-resumable native frames
    needs descriptor stacking for nested exits
```

The mistake would be to force all three through one abstraction.

`fib` wants raw arithmetic and cheap self calls. `checkTree` wants the compiler
to notice that the recursive calls are just a fold over fields. Future table
kernels may still need the general typed ABI because they are not pure folds.
Those kernels need correct nested exit handling before they can be optimized
aggressively.

This split also explains why the fixed fold does not make `binary_trees`
"done." The benchmark still includes tree construction. `makeTree` allocates
the objects. The fixed fold speeds up the traversal, not the allocator, table
layout, or hidden-pointer root machinery.

That is why the honest result is:

```
binary_trees is much faster
the allocation architecture is still visible
LuaJIT parity is not claimed here
```

## The Lesson

The breakthrough was not that typed table recursion became universally fast.
The first naive version showed the opposite: a general typed-table recursive
ABI can regress when the recursive body is smaller than the ABI protocol.

The breakthrough was recognizing when the recursive program is not really a
call problem anymore.

For `checkTree`, the recursive call tree is a fixed reduction over a fixed
object shape. Once the compiler proves that, the runtime can execute the fold
directly, keep field caches local to the protocol, preserve int48 semantics,
and avoid paying a native call boundary at every node.

The nested exit descriptor stack is the complementary architectural fix. It
does not over-market the current win. It makes the next class of typed native
recursion possible without relying on a single overwritten exit record.

That is the useful kind of progress: one narrow benchmark win, one broader
correctness mechanism, and a clearer line between the two.
