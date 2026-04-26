---
layout: default
title: "The Two Pointers That Met in the Middle"
permalink: /52-the-two-pointers-that-met-in-the-middle
---

# The Two Pointers That Met in the Middle

Fannkuch was the regression that did not fit the recent story.

The table work had been improving array-heavy code. The raw recursion work was
focused somewhere else. The full guard still said:

```text
fannkuch default: 0.062s
baseline:         0.049s
regression:       +26.5%
```

That is large enough to ignore noise. It needed a real explanation.

## The hot loop

The inner reversal loop is a classic two-pointer swap:

```text
lo := 1
hi := k
for lo < hi {
    t = perm[lo]
    perm[lo] = perm[hi]
    perm[hi] = t
    lo = lo + 1
    hi = hi - 1
}
```

The previous guarded-induction optimization helped forward loops like
`j += i`. It deliberately rejected plain decrementing induction variables,
because an upper guard like `i <= n` does not make `i - 1` safe.

That was correct, but fannkuch has a more precise shape. `hi` is not
decrementing alone. It is converging with `lo` under a strict `lo < hi` guard.

That comparison proves more than a generic upper bound.

If both operands are raw int48 values and `lo < hi`:

- `lo` cannot be `MaxInt48`, so `lo + 1` fits;
- `hi` cannot be `MinInt48`, so `hi - 1` fits.

The compiler does not need to know the full range of `k`. The strict comparison
between the two raw operands is enough to keep the `+1/-1` updates raw.

## The narrow rule

The new rule recognizes only this pattern:

```text
header:
  lo = Phi(initLo, lo + 1)
  hi = Phi(initHi, hi - 1)
  lo < hi
```

It marks only the `lo + 1` and `hi - 1` updates as `Int48Safe`.

It does not accept:

- `hi -= 2`;
- `lo <= hi`;
- unrelated decrementing loops;
- loops where the compared values are not the header phis.

That last part matters. This is not "decrementing loops are safe now." It is
"two raw integer pointers converging under a strict comparison make their
single-step updates safe."

That is why the change lives in range analysis and why the tests include both
the positive fannkuch shape and a `hi -= 2` negative case.

## The result

The independent branch measured the direct before/after:

```text
fannkuch default: 0.062s -> 0.051s
instruction count: 3615 -> 3432
code bytes:        14460 -> 13728
```

After integration on main, the 7-run regression guard reported:

```text
fannkuch default:           0.052s
fannkuch no-filter:         0.051s
table_array_access default: 0.042s
sort default:               0.050s
```

The old +26.5% fannkuch regression dropped to +6.1%, below the guard
threshold. It is still not LuaJIT:

```text
fannkuch JIT/LuaJIT: 2.60x
```

But the obvious self-inflicted damage is gone.

## The remaining gap

Fannkuch is still table and integer heavy. The next large win is unlikely to
come from another small range proof. It will come from keeping the permutation
array operations native and dense:

- fewer table guards per swap;
- better typed-array load/store code density;
- fewer boxed accumulator paths;
- more precise native table mutation gates for recursive and loop-heavy code.

This round fixed one representation mistake. It did not pretend to solve the
table problem.

Commit: `2f7f112 methodjit: keep converging swap indices raw`
