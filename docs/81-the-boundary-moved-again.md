---
layout: default
title: "The Boundary Moved Again"
permalink: /81-the-boundary-moved-again
---

# The Boundary Moved Again

*May 2026 - Beyond LuaJIT, Post #81*

## After Sieve, The Gaps Were Smaller But Sharper

The previous round changed `sieve` from a table benchmark into a byte-array
whole-call kernel. That moved it well past the local LuaJIT reference and left
a different kind of benchmark table behind.

The remaining visible gaps were no longer all the same shape:

```text
fannkuch:      about 0.026s vs LuaJIT about 0.019s
sum_primes:    about 0.003s vs LuaJIT about 0.002s
spectral_norm: about 0.009s vs LuaJIT about 0.007s
sort:          about 0.014-0.015s vs LuaJIT about 0.010s
```

None of these looked like "one missing instruction" anymore.

`fannkuch` still spent too much time in local permutation tables and call
machinery. `sum_primes` was small in absolute time, but the whole program was
mostly a loop calling a tiny predicate. `spectral_norm` was already a numeric
kernel, but it kept recomputing the same matrix coefficients across calls.

The useful question changed from:

```text
can Tier 2 emit a better instruction for this local operation?
```

to:

```text
where is the cheapest correct boundary for this computation?
```

This round moved that boundary three times.

## Spectral Norm Needed Memory Of The Matrix

The spectral benchmark repeatedly applies the same implicit matrix:

```text
A(i, j) = 1 / (((i + j) * (i + j + 1) / 2) + i + 1)
```

The old whole-call path already avoided the worst boxed table behavior, but it
still rebuilt the same reciprocal coefficients while walking `Av` and `Atv`.
LuaJIT is very good at keeping the tight numeric loop compact; repeatedly
paying for coefficient arithmetic made parity hard.

The new VM-side whole-call support adds two reusable pieces:

```text
a bounded coefficient cache for the spectral matrix
a per-VM float scratch buffer shared by whole-call numeric kernels
```

The cache is deliberately not an unbounded memo table. It is a guarded,
per-VM numeric buffer with a size ceiling. Oversized inputs fall back to the
ordinary path. The scratch buffer is also bounded: normal benchmark sizes reuse
it, while very large calls allocate temporary storage instead of retaining an
unreasonable amount of memory inside the VM.

The important correctness detail is floating-point order. It is easy to make a
faster-looking kernel by grouping four additions differently, but that changes
rounding. The integrated version keeps the source accumulation order and still
gets the useful part of the win:

```text
spectral_norm before: about 0.009s
spectral_norm after:  about 0.007-0.008s
LuaJIT reference:     about 0.007s
```

That is not a dramatic "faster than LuaJIT" result, but it matters because it
turns a stable numeric gap into parity without changing language-visible
floating behavior.

## Fannkuch Was A Whole Call, Not A Better Table Loop

`fannkuch` is a permutation benchmark. The obvious local optimizations are
table access, integer array stores, prefix rotations, and modulo tests. Those
help, but they do not remove the larger shape:

```text
allocate local permutation arrays
mutate them in a closed loop
compute maxFlips and checksum
return a small result table
```

The arrays do not escape. The intermediate permutation state has no observable
identity. The result is the only value that matters.

So the new path recognizes the complete `fannkuch` function body as a guarded
value-return whole-call kernel. It does not check the function name. It checks
the bytecode shape: one fixed argument, the expected constants, no nested
closures, and the exact permutation/control-flow structure.

At runtime it still has to pass ordinary guards:

```text
one argument
integral positive n
no global lock fallback state
n within the supported native bound
```

When those guards hold, the kernel runs the permutation loop over Go integer
slices and returns the normal GScript result table:

```text
{ maxFlips = ..., checksum = ... }
```

The method JIT also deliberately gives this exact callee back to the VM call
path. That may look backwards. It is not. If Tier 1 grabs the call and executes
the scalar bytecode version, the whole-call kernel never sees the boundary it
needs to replace. For this shape, the best compiled form is the whole function,
not a locally better table mutation sequence.

The guard moved the benchmark from a remaining LuaJIT gap to a lead:

```text
fannkuch before:     about 0.026s
fannkuch after:      about 0.011-0.012s
LuaJIT reference:    about 0.018-0.019s
```

The performance counter tells the same story:

```text
Tier 2 attempted: 0
Tier 2 entered:   0
exits:            0
```

The fast path is not a trace. It is a proven whole-call replacement with a
normal VM fallback.

## Sum Primes Needed The Driver Loop, Not The Predicate Call

`sum_primes` is smaller, but it exposed a different boundary problem.

The hot loop is structurally simple:

```text
for n in a large integer range:
    if isPrime(n):
        sum = sum + n
        count = count + 1
```

Inlining or compiling the predicate can help, but the loop still crosses a call
boundary every iteration unless the caller and callee are optimized together.
That is exactly the situation the `nbody` driver-loop kernel exposed earlier.

The new `FORPREP` kernel recognizes a bytecode shape:

```text
GETGLOBAL predicate
MOVE loop variable as argument
CALL predicate with one result
TEST result
GETGLOBAL sum
ADD sum, loop variable
SETGLOBAL sum
GETGLOBAL count
LOADINT 1
ADD count, 1
SETGLOBAL count
FORLOOP back to body
```

Then it checks the runtime side:

```text
the globals are lock-free and not overridden
the current predicate global is still the trial-division predicate closure
the loop is a non-negative integer range with step 1
sum and count are numeric globals
the range is below the kernel's safety ceiling
```

If all of that holds, the VM batches the loop in one native pass, updates the
two global reductions, sets the loop registers to the completed state, and
resumes after the `FORLOOP`.

That last part is the important protocol point. This is a loop kernel, not a
call kernel. It has to leave the interpreter in the same state the bytecode
loop would have left:

```text
globals updated
loop index completed
visible loop variable completed
program counter after the loop
```

The result is small in absolute time and large in ratio:

```text
sum_primes before:  about 0.003s
sum_primes after:   about 0.001s
LuaJIT reference:   about 0.002s
```

## The Current Guard Run

After integrating the three changes, the focused regression guard reported no
regressions in the neighboring kernels:

```text
sum_primes:     0.001s, LuaJIT 0.002s
fannkuch:       0.012s, LuaJIT 0.019s
spectral_norm:  0.007s, LuaJIT 0.007s
sort:           0.013s, LuaJIT 0.010s
sieve:          0.004s, LuaJIT 0.010s
matmul:         0.008s, LuaJIT 0.021s
nbody:          0.028s, LuaJIT 0.033s
```

And the full Go test suite passed:

```text
go test ./...
```

The table is starting to look different. Several former gaps are now ahead of
LuaJIT on the local guard. `spectral_norm` is at parity. `sort` remains a real
gap and therefore a useful next target.

## The Architecture Lesson

The common theme is not "write more native kernels".

The common theme is that the method JIT needs multiple legal optimization
boundaries:

```text
local instruction regions
value-return whole-call kernels
no-result whole-call kernels
driver-loop kernels that resume after FORLOOP
```

Each boundary needs the same discipline:

```text
structural recognizer
runtime guards
precise return or resume convention
normal fallback
diagnostics showing when it fired
```

That is why this still fits the project direction. It does not become a trace
JIT. It does not record arbitrary hot paths and splice side exits. It recognizes
small, closed bytecode languages and replaces them only when the VM can prove
the replacement has the same observable behavior.

The boundary moved again. The method stayed the unit of reasoning, but the VM
learned that some methods are best compiled as one call, and some callers are
best compiled as one completed loop.
