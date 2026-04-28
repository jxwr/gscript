---
layout: default
title: "The Result Stayed Raw"
permalink: /68-the-result-stayed-raw
---

# The Result Stayed Raw

*April 2026 - Beyond LuaJIT, Post #68*

## Where We Left Off

In [Post #67](67-the-closure-was-already-a-pointer), the VM closure call path
stopped asking the generic pointer API a question the value tag had already
answered. That was a runtime dispatch fix. It made `binary_trees` and
`closure_bench` less dependent on interface reconstruction.

The next round moved back into the compiler.

The target was still the same uncomfortable set of benchmarks:

```
fib_recursive
ackermann
matmul
spectral_norm
sort
binary_trees
```

They were not all blocked on the same thing. That is what made this round
useful. A method JIT can lose to LuaJIT in several different ways at once:

```
raw recursive result gets boxed too early
typed table feedback is lost after lowering
duplicate numeric expressions survive in the hot block
closure allocation pays for generality on every creation
```

None of those are a trace JIT versus method JIT question. They are contract
questions. Once a compiler has learned a fact, where is that fact allowed to
live, and who is responsible for preserving it after the graph changes?

This round added four small contracts.

## Self Calls Are Still Integer Calls

`fib_recursive` exposed a strange shape.

The compiler already recognized raw-int self-recursive functions well enough to
emit a private numeric entry. The callee could take raw integer parameters and
return a raw integer in the native path. But the SSA call instruction itself
still looked like a generic boxed result in one important place.

That matters for this expression:

```
return fib(n - 1) + fib(n - 2)
```

If the two self calls produce unknown boxed values, the final addition stays
generic. The native self-call may be fast, but the caller immediately pays the
boxed arithmetic path again.

The fix is deliberately narrow:

```
if call is a static self call
if arity is exact
if the function's specialized ABI is raw-int -> raw-int
if all actual arguments are TypeInt
then the call instruction result is TypeInt
```

That does not invent a new ABI. It only lets the existing ABI fact survive in
the SSA graph. The downstream arithmetic can then become:

```
OpAddInt
```

instead of:

```
OpAdd
```

The benchmark movement was small but real:

```
fib_recursive:
  before: about 0.665s
  after:  about 0.606s in the focused guard
```

The important part is not that this single patch made recursion competitive
with LuaJIT. It did not. The important part is that the raw return convention
now reaches its consumers. A raw call result that immediately becomes boxed
again is not a raw call convention. It is only half a convention.

## The ABI Became Metadata

The next cleanup was about making the raw-int self-recursive ABI explicit.

Before this round, the raw self path was a pile of repeated local knowledge:

```
the proto is eligible
the entry takes N raw integer params
the params correspond to slots 0..N-1
the return is a raw integer
fallback must be able to rebuild the boxed VM call frame
numeric exits must publish ctx.Regs before Go observes the context
```

That is too much to keep as folklore in the emitter.

The patch adds `RawIntSelfABI` metadata to the compiled function:

```
Eligible
NumParams
ParamSlots
Return
RejectWhy
```

This is not a benchmark-specific ackermann knob. It is a compact form of the
same specialized ABI analysis, reduced to the exact facts the emitter and tests
need. The code generator can now say:

```
compiled function has a private raw-int self ABI
```

instead of rediscovering the shape at each call site.

The same patch also tightens the raw self-call success path. Successful raw
self recursion no longer publishes `ctx.Regs` on every layer just because a
fallback might happen later. The rule becomes:

```
success path:
  keep caller register base lazy

fallback or numeric exit:
  publish the current caller base before Go handles the context
```

That distinction is the beginning of the "register-only success path,
metadata-backed fallback path" design we need for the next raw ABI step.

The current speedup is modest:

```
ackermann:
  focused guard stayed around 0.015s to 0.016s
```

But the architectural win is bigger than the number. The ABI now has a named
record. That record is where liveness, fallback materialization, return
representation, and exit-resume metadata can converge instead of being patched
into each benchmark path independently.

## Lowering Must Not Forget Types

`matmul` exposed a different loss of information.

The table-array optimizer already split a monomorphic `GetTable` into:

```
TableArrayHeader
TableArrayLen
TableArrayData
TableArrayLoad
```

That was the right shape for LICM and load elimination. The stable header, len,
and data facts can be shared or hoisted. But the split had a hidden cost:

```
GetTable.Aux2 held the feedback kind
TableArrayLoad.Aux did not feed TypeSpec in the same way
```

So an inner matmul expression could still look like this after lowering:

```
TableArrayLoad -> Mul(any) -> Add(any)
```

The array load was known by feedback to be float, but the later arithmetic did
not reliably inherit that fact after the lowering pass moved the kind field.

The fix has two parts.

First, `TableArrayLower` stamps scalar loads from monomorphic array kinds:

```
FBKindInt   -> TypeInt
FBKindFloat -> TypeFloat
FBKindBool  -> TypeBool
```

`FBKindMixed` deliberately does not become a scalar type. A mixed array can
hold tables, closures, numbers, strings, or anything else.

Second, the pipeline runs a narrow post-lowering TypeSpec pass over the values
affected by `TableArrayLoad`. This pass is intentionally not the full TypeSpec
pipeline. At this point, overflow boxing has already made some decisions. The
post-lowering pass only specializes the graph reachable from typed array loads.
It does not insert a new wave of unrelated guards.

The target shape becomes:

```
TableArrayLoad : float
MulFloat
AddFloat
```

The immediate wall-time movement was still limited:

```
matmul:
  after this round: about 0.095s
  LuaJIT:           about 0.022s
```

That is not good enough. But it changes the next problem. If the IR now has
typed float loads and typed float arithmetic, then the remaining matmul gap is
less about type recovery and more about successful-path code generation:

```
row table reuse
array header/data residency
bounds checks
loop register pressure
call/frame overhead around helper paths
```

That is the direction the worker agents are now attacking.

## Pure Numeric Values Can Be Reused

`spectral_norm` was not an exit storm.

Its exit count was low:

```
spectral_norm exits: about 52
```

That means the fallback path is not the main problem. The successful Tier 2
path is simply doing too much work.

One obvious pattern in the dump was duplicated typed arithmetic inside a basic
block, especially after inlining:

```
i + j
i + j
```

For generic boxed arithmetic, common subexpression elimination is complicated:
side effects, conversions, fallback, and deopt metadata all matter. For typed
numeric SSA operations inside a single block, the rule can be much smaller:

```
same op
same type
same aux fields
same SSA arguments
no side-effect boundary crossed
=> reuse the earlier result
```

The new block-local CSE handles pure typed numeric operations such as:

```
OpAddInt, OpSubInt, OpMulInt
OpAddFloat, OpSubFloat, OpMulFloat, OpDivFloat
OpNumToFloat, OpSqrt, OpFMA
typed comparisons
```

It clears across side-effecting instructions and stays block-local. That keeps
the first version conservative. The goal is not to build a full global value
numbering pass in one step. The goal is to remove duplicate arithmetic that
the current pipeline is already producing in the same block.

The focused guard moved:

```
spectral_norm:
  before: about 0.025s
  after:  about 0.022s to 0.023s
```

Still not LuaJIT. But now the benchmark is closer, and the pass is general
enough to help any typed numeric hot block, not just spectral norm.

## Closure Allocation Without Breaking Ptr()

There was also a tempting closure allocation patch on the table.

The fast version avoided `ifaceRoots` entirely for VM closures by creating a
closure value that could only be decoded through `VMClosurePointer()`. That is
fast, but it changes a compatibility property:

```
Value.Ptr() no longer reconstructs the original *vm.Closure
```

For the internal hot paths, that is probably fine. They already use
`VMClosurePointer()`. But it is still a wider behavioral change than this round
needed.

So the merged version keeps the old value representation:

```
runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)
```

That preserves `Ptr()` reconstruction.

The optimization instead lives in closure allocation itself. Most closures have
zero or one upvalue. The old constructor pattern always allocated the closure
and then allocated a separate upvalue slice:

```
cl := &Closure{
    Proto: proto,
    Upvalues: make([]*Upvalue, len(proto.Upvalues)),
}
```

The new helper backs the one-upvalue slice with storage inside the closure:

```
type Closure struct {
    Proto         *FuncProto
    Upvalues      []*Upvalue
    inlineUpvalue [1]*Upvalue
}
```

`vm.NewClosure` then chooses:

```
0 upvalues: no slice allocation
1 upvalue:  use inlineUpvalue[:1]
2+ upvalues: allocate a normal slice
```

The VM, Tier 1 closure exit, Tier 2 op-exit closure fallback, and method JIT
IR interpreter now route through that helper. `closeUpvalues` also gets a
zero-open-upvalues fast return.

Focused guard:

```
closure_bench:
  about 0.028s -> 0.027s

binary_trees:
  full guard before this patch: about 0.803s
  focused guard after patch:    about 0.775s

nbody:
  stayed around 0.065s
```

This is intentionally smaller than the faster unsafe closure-value patch. It
buys some allocation relief without changing the public value reconstruction
contract. The more aggressive version can still come later, but it needs a
cleaner compatibility story.

## The Current Board

After these patches, a three-run full guard looked like this:

```
fib                  0.088s  vs LuaJIT 0.026s
ackermann            0.016s  vs LuaJIT 0.006s
matmul               0.096s  vs LuaJIT 0.022s
spectral_norm        0.022s  vs LuaJIT 0.008s
nbody                0.064s  vs LuaJIT 0.035s
fannkuch             0.042s  vs LuaJIT 0.020s
sort                 0.046s  vs LuaJIT 0.011s
fibonacci_iterative  0.025s
math_intensive       0.055s
object_creation      0.004s
```

The gap is still real.

The useful part of this round is that several facts now survive longer:

```
self-recursive raw-int calls keep raw-int result type
raw self ABI facts are recorded as metadata
typed array loads keep their element type after lowering
pure typed numeric expressions can be reused in-block
small closure allocation no longer always needs a second heap object
```

That changes the next optimization frontier.

For `ackermann` and `fib_recursive`, the next work is not another ad-hoc raw
call fast path. It is the full raw-int self-recursive protocol:

```
register liveness
exit-resume publication
fallback frame materialization
raw return convention
```

For `matmul`, the next work is native success-path quality now that typed float
loads are visible:

```
row table residency
array data pointer reuse
address calculation
bounds check structure
float register pressure
```

For `sort`, simply admitting recursive quicksort to Tier 2 was already tested
and was slower. The problem is not the gate by itself. The problem is making
the recursive table-mutation success path profitable.

That is the shape of the next round.

The method JIT does not need to become a trace JIT to keep closing the gap.
But it does need to stop losing facts at pass boundaries.

That is what this round fixed.
