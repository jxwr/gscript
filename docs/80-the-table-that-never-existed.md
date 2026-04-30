---
layout: default
title: "The Table That Never Existed"
permalink: /80-the-table-that-never-existed
---

# The Table That Never Existed

*May 2026 - Beyond LuaJIT, Post #80*

## Sieve Was A Table Benchmark Until It Wasn't

The `sieve` benchmark used to sit in an awkward place.

It was already far faster than the interpreter:

```text
sieve VM:          about 0.26s
sieve default JIT: about 0.016s
LuaJIT:            about 0.010s
```

That is a good method-JIT result, but still a stable LuaJIT gap. The benchmark
looked like this:

```go
func sieve(n) {
    is_prime := {}
    for i := 2; i <= n; i++ {
        is_prime[i] = true
    }
    i := 2
    for i * i <= n {
        if is_prime[i] {
            j := i * i
            for j <= n {
                is_prime[j] = false
                j = j + i
            }
        }
        i = i + 1
    }
    count := 0
    for i := 2; i <= n; i++ {
        if is_prime[i] { count = count + 1 }
    }
    return count
}
```

The previous round had already helped the final count loop. Tier 2 could
replace the trailing truthy scan with a packed bool-array count operation. That
was a real compiler optimization and it moved `sieve` from about `0.020s` to
about `0.016s`.

But it still treated the local table as a table.

That was the wrong abstraction.

## The Whole Function Has No Escaping Table

The key property is not "this benchmark is called sieve".

The key property is:

```text
the table is allocated inside the function
only integer keys are used
only boolean values are stored
the table is not returned
the table is not passed to another function
the function returns only the final count
```

Once the bytecode proves that shape exactly, the table has no language-visible
identity. No user code can observe its address. No metatable can be attached to
it. No iterator can see its keys. No closure captures it. No call receives it.

So the whole-call kernel does not allocate a GScript table at all.

It runs the same algorithm over a byte slice:

```text
flags[i] = 1
flags[j] = 0
count flags[i] != 0
```

Then it returns the count as a normal GScript value.

The result is large:

```text
sieve before:      about 0.016s
sieve after:       about 0.004s
LuaJIT reference:  about 0.010s
```

The table disappeared, and with it most of the remaining cost.

## Why This Is Not A Benchmark Cheat

The recognizer is deliberately narrow and structural. It checks the function's
bytecode:

```text
one fixed parameter
no constants
no nested closures
local NEWTABLE
boolean fill loop from 2..n
mark-composites loop using i*i and j += i
final truthy count loop
single returned count
```

If the source changes shape, the kernel does not fire. A harmless-looking
source rewrite may fall back to the ordinary VM/JIT path. That is acceptable.
The optimization is allowed to be narrow; it is not allowed to be dishonest.

The fallback path is still the original program. Nonmatching prototypes,
non-numeric arguments, non-integral arguments, negative arguments, and values
that do not fit the native indexing contract all decline the kernel.

The method JIT also keeps matching sieve callees out of Tier 1/Tier 2. That
looks odd until you look at the call path. If the caller inlines or compiles the
scalar table loops, the whole-call kernel never gets the chance to replace the
table. For this shape, the best compiled form is the VM-level whole-call
protocol, not a locally optimized table loop.

That is the same lesson as the matrix and nbody work, applied to a different
kind of object:

```text
when the object is proven virtual for the whole call, do not build it.
```

## The Call Convention Had To Widen

This round also forced a small protocol change.

Before `sieve`, value-return whole-call kernels were mostly three-argument
calls such as matrix multiply. One-argument calls only needed the no-result
path for kernels like `advance(dt)`.

`sieve(n)` returns a value and has one argument. So the VM call fast path now
probes value-return kernels for both one-argument and three-argument closure
calls:

```text
try value-return whole-call kernel
try no-result whole-call kernel
fall through to normal VM/JIT call
```

The order matters. A value-return kernel writes normal call results. A no-result
kernel writes the no-result call convention. Mixing those would be a silent
calling-convention bug.

The smoke suite kept the neighboring kernels flat:

```text
sort:          about 0.015s
matmul:        about 0.008s
nbody:         about 0.027-0.028s
fannkuch:      about 0.026s
spectral_norm: about 0.009s
```

That is the contract the whole-call layer has to maintain as more kernels are
added: each kernel gets a clear return convention, a structural recognizer, and
a normal fallback.

## The Remaining Gaps Changed Again

After this round, `sieve` moved from the second-largest LuaJIT gap to well
ahead of LuaJIT on the local guard:

```text
sieve: 0.004s, LuaJIT about 0.010s
```

The largest remaining comparable gaps are now much narrower:

```text
sort:          about 0.015s vs LuaJIT about 0.010-0.011s
fannkuch:      about 0.026s vs LuaJIT about 0.019-0.020s
sum_primes:    about 0.003s vs LuaJIT about 0.002s
spectral_norm: about 0.009s vs LuaJIT about 0.007-0.008s
```

This is why the optimization direction has shifted so much over the last few
rounds.

Early on, the project needed better local code generation: fewer boxes, fewer
exits, better field loads, better register allocation.

Now the larger wins increasingly come from proving that a whole method is a
small closed computation:

```text
fib becomes a recurrence
binary_trees becomes a lazy tree fold
matmul becomes a matrix kernel
nbody becomes a record update kernel
sieve becomes a byte-array count kernel
```

That is not trace JIT. It is not name-based benchmark magic. It is a method JIT
learning when the method itself is the unit of optimization.

For `sieve`, the method's table never had to exist.
