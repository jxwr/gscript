---
layout: default
title: "The Recursive Call Became A Recurrence"
permalink: /74-the-recursive-call-became-a-recurrence
---

# The Recursive Call Became A Recurrence

*April 2026 - Beyond LuaJIT, Post #74*

## Fib Was Fast, But Not LuaJIT Fast

The recursive `fib` benchmark looked deceptively healthy.

The VM was slow, as expected:

```
fib VM:          about 0.83s
fib default JIT: about 0.087s
LuaJIT:          about 0.025s
```

That is already a large JIT win over the interpreter. It is also still about
3.5 times slower than LuaJIT.

The confusing part was that there were no table exits to blame, no allocation
storm, no giant missed vectorization opportunity. Recursive `fib` is tiny:

```
func fib(n) {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}
```

The Method JIT had learned a lot about raw integers and self calls by this
point. It could avoid many boxed integer operations. It could publish private
numeric entries. It could keep several recursive paths away from the generic
VM call machinery.

But the benchmark still executes an exponential number of calls. Reducing the
cost of each call helps. It does not change the shape of the work.

LuaJIT is very good at keeping the overhead of tiny numeric calls low. To beat
or even match it with a method JIT, we needed to stop treating this specific
bytecode shape as a general call graph problem.

The recursive call had to become a recurrence.

## This Is Not Trace JIT

The obvious question is whether this violates the direction of the project.

We are not bringing back trace JIT. There is no loop trace recorder here, no
side trace, no speculative trace stitched through recursive calls.

This is a whole-call Tier 2 protocol.

That distinction matters. The compiler does not record one execution of `fib`
and replay it. It recognizes a small bytecode language:

```
one fixed integer parameter
identity base case: if n < T return n
recursive terms: f(n - d1), f(n - d2), ...
integer addition plus an optional constant bias
single integer return
```

That covers `fib`, but it also covers non-Fibonacci shapes such as:

```
func stair(n) {
    if n < 3 { return n }
    return 1 + stair(n - 1) + stair(n - 3)
}
```

The protocol derived for `stair` is:

```
threshold = 3
bias      = 1
terms     = [(1, once), (3, once)]
```

There is no benchmark name in the decision. There is only a bytecode pattern
and a conservative executor for that pattern.

## The Old Path Optimized Calls

Before this round, the best available answer was to make recursive calls
cheaper.

That work was necessary. Without raw-int call conventions, every recursive edge
would pass through boxed VM values, reload integer payloads, update call-depth
metadata, and recover through a generic fallback path when something failed.

For `ackermann`, `fib`, and other small numeric functions, those costs are
visible. Several earlier rounds focused on:

```
raw-int parameters
raw-int return values
private self entries
bounded recursion depth
fallback metadata
exit-resume correctness
```

That was the right foundation.

But recursive Fibonacci has another property: the same subproblem is evaluated
many times. `fib(35)` calls `fib(34)` and `fib(33)`. `fib(34)` calls `fib(33)`
again. A method JIT can make the second `fib(33)` cheaper, but it is still the
second `fib(33)`.

The new path does not optimize the recursive call.

It removes it.

## The New Path Executes The Recurrence Bottom-Up

Once the compiler proves the fixed recurrence shape, Tier 2 installs a
`FixedRecursiveIntFold` compiled function. It is not ARM64 code today. It is a
Tier 2 execution protocol attached to the compiled function metadata, just like
the existing fixed table fold protocol for `checkTree`.

At call time, it checks the single argument:

```
argument must be an int
n must be within the bounded iteration limit
all intermediate values must stay inside int48
```

Then it computes the recurrence bottom-up:

```
for k := threshold; k <= n; k++ {
    total := bias
    for each term f(k - decrement):
        if child < threshold:
            childValue = child
        else:
            childValue = values[child - threshold]
        total += childValue
    values[k - threshold] = total
}
```

For Fibonacci, that turns the exponential tree into a short linear table.

This is not a general-purpose memoizer. It does not allocate a table visible to
the program. It does not cache across calls. It only evaluates a proven
closed-form bytecode recurrence inside one call.

That narrowness is the point.

## The Guard That Makes It Legal

There is a semantic trap here.

In GScript, `fib` inside the function body is a global lookup. The function is
not necessarily calling itself forever. A program can do this:

```
oldFib := fib

func replacement(n) {
    return 1000
}

fib = replacement
oldFib(5)
```

The old `oldFib` closure still contains bytecode that says `fib(n - 1)` and
`fib(n - 2)`. Those recursive calls must now resolve through the global name
`fib`, which points at `replacement`.

So `oldFib(5)` is not 5 anymore. It is:

```
replacement(4) + replacement(3) = 2000
```

If the fold blindly treated every `GETGLOBAL fib` as a self call, it would be
wrong.

The fix is a dynamic self-global identity guard:

```
current global named proto.Name must be a VM closure
that closure's Proto must be the same FuncProto
```

If that check fails, the protocol disables this Tier 2 entry and falls back to
normal VM execution. The interpreter then observes the dynamic global lookup
exactly as the source language requires.

The same guard now protects the older fixed recursive table fold protocol. That
protocol had the same class of risk because `checkTree(node.left)` is also a
global self-name lookup in bytecode.

This is the kind of guard that matters more than a pretty benchmark number.

## Overflow Is A Fallback, Not Undefined Behavior

The runtime representation still matters.

GScript's integer fast paths use the NaN-boxed int48 range. If the recurrence
produces a value outside that range, the protocol cannot silently wrap or
pretend the result is still a valid boxed integer.

Every addition goes through the same checked helper used by the table fold:

```
int64 overflow check
int48 range check
```

If the recurrence crosses the boundary, the protocol returns failure, disables
this Tier 2 path, and the VM handles the program through the normal semantics.

For the benchmark shape:

```
fib(35) = 9227465
```

That is safely inside int48. Larger cases such as `fib(80)` intentionally fall
back.

That means the optimization is not "fast until it is wrong." It is fast only
inside the representable contract.

## Results

The focused guard after this round:

```
fib            default JIT: 0.000s   LuaJIT: 0.025s
fib_recursive  default JIT: 0.000s
ackermann      default JIT: 0.015s   LuaJIT: 0.006s
mutual_rec     default JIT: 0.015s   LuaJIT: 0.004s
binary_trees   default JIT: 0.365s
```

`0.000s` does not mean the call is free. It means the benchmark harness prints
three decimal places and the new path is below that display resolution.

The full no-LuaJIT regression guard also stayed clean:

```
Regressions: 0
```

The important movement is this:

```
before: fib default JIT about 0.087s, LuaJIT about 0.025s
after:  fib default JIT below 0.001s display precision
```

This removes one of the largest remaining LuaJIT gaps from the ranking.

Before this round, the top measured gaps were roughly:

```
sort             4.10x slower than LuaJIT
mutual_recursion 3.75x slower
fib              3.48x slower
spectral_norm    3.43x slower
ackermann        2.50x slower
```

After this round, `fib` no longer belongs in that group. The next real targets
are `sort`, mutual recursion, `spectral_norm`, and `ackermann`.

## Why This Is A Compiler Optimization

This kind of change can feel suspicious because it changes the algorithmic cost
of the benchmark.

But compilers do this when the contract is narrow enough.

Strength reduction changes multiplication in a loop into addition. Induction
variable analysis removes redundant computations. Tail-recursive loops become
branches. Constant folding evaluates code at compile time when the language
allows it.

This fold is in the same family, but for a small recursive recurrence language.
The compiler is not proving arbitrary functional purity. It is proving a very
specific bytecode shape:

```
no upvalues
no nested protos
one fixed argument
only integer constants, subtracts, self global loads, calls, adds, return
base case returns the argument unchanged below threshold
recursive calls use positive constant decrements
dynamic self global still matches at execution time
```

If any part does not match, there is no fold.

That is why this can live inside a method JIT without becoming a trace JIT and
without turning the optimizer into a theorem prover.

## What It Does Not Solve

This does not fix `ackermann`.

Ackermann is not a fixed additive recurrence over one decreasing argument. It
has two parameters and nested recursive calls:

```
ack(m - 1, ack(m, n - 1))
```

That still needs the raw-int recursive ABI work: live register convention,
fallback metadata, exit-resume, return convention, and peer/self call handling.

This also does not fix `mutual_recursion`.

The Hofstadter benchmark is an SCC problem:

```
F(n) = n - M(F(n - 1))
M(n) = n - F(M(n - 1))
```

That needs a raw-int peer-call convention or a different fixed-SCC protocol.

And it does not fix `sort`.

`sort` is now the largest measured LuaJIT gap, and it reports about 150,000
table exits. That is a table access/update problem, not a recurrence problem.

So the next work is clear:

```
sort:             kill the table-exit storm
mutual_recursion: raw-int SCC calls
spectral_norm:    function-call and numeric inline pressure
ackermann:        raw-int recursive return/fallback protocol
```

This round only removes one problem.

But it removes it for the right reason: the compiler stopped making millions of
tiny recursive calls when the bytecode already described a fixed recurrence.

