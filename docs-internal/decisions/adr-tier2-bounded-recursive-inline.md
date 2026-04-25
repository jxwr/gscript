# ADR: Tier 2 bounded recursive inlining (fib/ackermann)

**Status:** Proposed (R31 architecture round, 2026-04-17)
**Scope:** close fib (59×) and ackermann (43×) LuaJIT gaps inside the
method-JIT substrate.
**Related:** R20 ADR (recursion closure plan — 5 strategies), R22 revert,
`adr-no-trace-jit.md`, `internal/methodjit/tiering_manager.go` inline
pass, `pass_inline*.go`.

---

## 1. Current state audit (per rule 23)

### 1.1 Existing inline pass capabilities

`tiering_manager.go` lines 355-380: `isInlineCandidate` allows inlining
only when:
- Callee code size ≤ 80 bytecodes (`inlineMaxCalleeSize`).
- Callee is **NOT recursive** (`isRecursive` check).
- Callee name ≠ caller name (explicit self-call check).
- Callee has NO loops.

**This pass explicitly blocks recursive callees.** fib and ackermann
are never inlined. Every call goes through the full ~78 ns native BLR
sequence.

### 1.2 Per-call cost breakdown (from R20 audit)

~40 insns × 2 ns/insn = 78 ns/call on fib. At 18M calls for fib(35):
~1.4 s wall time. Matches measured.

### 1.3 What LuaJIT does

LuaJIT specializes fib(35)'s execution end-to-end at the substrate
layer, producing ~5 insns per "iteration" (no per-call dispatch).
~20M iterations × 1 ns = 0.024 s. Matches observed.

The method-JIT equivalent (per `adr-no-trace-jit.md`) is
**static bounded recursive inlining**: at compile time, unroll the
recursion N levels deep.

### 1.4 What bounded inlining would do

fib:
```
fib(n):
  if n<2: return n
  return fib(n-1) + fib(n-2)
```

Depth-2 inline (unroll one level):
```
fib(n):
  if n<2: return n
  a = n-1; b = n-2
  // inline fib(a):
  if a<2: x = a
  else: x = fib(a-1) + fib(a-2)         ← still recursive
  // inline fib(b):
  if b<2: y = b
  else: y = fib(b-1) + fib(b-2)         ← still recursive
  return x + y
```

Call tree: fib(n) → {fib(n-2), fib(n-3), fib(n-3), fib(n-4)}.
Eliminates 1 call per 4 ≈ 25% of calls. fib wall time 1.4 → 1.05 s.

Depth-3 inline: eliminates 7 of 8 calls per level → 87% reduction.
fib 1.4 → 0.18 s. **Gap 59× → ~7.5×**.

Tradeoffs:
- **Code size**: depth-K inline expands body by ~2^K. fib at ~10
  bytecodes, depth-3 = ~80, fits within inlineMaxCalleeSize (currently 80).
  Depth-4 = ~160, exceeds budget.
- **Compile time**: O(2^K) expansion; negligible for K≤3.
- **I-cache**: expanded body may evict hot code; measure via diag.

### 1.5 Summary

**Gap is exclusively call overhead × call count.** Inlining reduces
call count. Depth-3 is the sweet spot for fib/ackermann.

**NOT ALREADY DONE**: inline pass explicitly excludes recursive callees.

## 2. Decision

Introduce **bounded recursive inlining** as a new mode of the existing
inline pass:

1. Detection: callee is self-recursive, has no loops, fits a recursive-
   inline size budget (separate from non-recursive budget).
2. Expansion: unroll the callee body N times, replacing recursive
   calls at level i with the callee's body again until level N; at
   level N, leave the call intact (base case fallback).
3. Depth budget: start at **K=2**, measure, increase to 3 if win ≥ 20%
   without regressing other benchmarks.
4. Size budget: `inlineMaxRecursiveSize = 160` (vs 80 for non-recursive).

### 2.1 Why this is method-JIT-shape, not a recording substrate

Static unrolling at compile time based on the function's IR shape.
Same code shape every time; no runtime path recording or trace cache.
Substrate is locked by `adr-no-trace-jit.md`.

### 2.2 Safety

Unrolling is semantics-preserving by construction (the unrolled body
is a textual substitution of `fib(x)` with `fib(x)`'s body, with
arguments bound). Correctness reduces to:
- Argument substitution correctness (already tested in existing
  non-recursive inline).
- SSA renumbering (already handled by graph builder's variable
  scoping).

Tests: add fib(n) for several n to production-scale regression tests
(seed case). Verify output matches for n=1..20.

## 3. Projected impact

| Benchmark | Current | Depth-2 | Depth-3 | LuaJIT | Depth-3 gap |
|-----------|--------:|--------:|--------:|-------:|------------:|
| fib       | 1.429   | 1.07    | 0.18    | 0.024  | 7.5× |
| ackermann | 0.261   | 0.20    | 0.033   | 0.006  | 5.5× |
| mutual_recursion | 0.184 | 0.184 | 0.184 | 0.004 | (不适用: mutual, not self) |

ackermann projection caveat: ackermann is `ack(m,n)` which recurses
via `ack(m-1, ack(m, n-1))`. Depth-3 unrolling with nested calls is
more complex than fib. Conservative estimate: depth-2 gives 25-30%
on ackermann.

mutual_recursion benefits require CROSS-FUNCTION inlining (F calls G,
G calls F). Out of scope for this ADR; filed as separate forward
class.

## 4. Non-goals

- Mutual recursion inlining. Separate class (needs inter-proc analysis).
- Unbounded unrolling. Depth cap is load-bearing for code size + compile time.
- Tail-call optimization (that's R20's step 3.4, separate).

## 5. Implementation staging

| Round | Step | Priority |
|------:|------|----------|
| R36   | Implement bounded recursive inline pass (depth-2, K=2) | HIGH |
| R37   | Integrate with existing inline pass; tests + regression suite | HIGH |
| R38   | If R36-R37 win, extend to depth-3 for fib-class functions | Depends on R36 result |

## 6. Composition with R22 revert

R22 attempted to skip the Tier 2 bounds check on self-calls and
broke quicksort at depth 16. Bounded recursive inlining is
ORTHOGONAL: it reduces the NUMBER of calls by unrolling, but each
call that remains still goes through the full (safe) BLR sequence.
No bounds-check-skip involved. **R22's failure mode does not apply
to this ADR.**

## 7. Decision outcome

**Accepted.** Priority HIGH within the R29-R38 phase. R36 implements
depth-2; extends to depth-3 if measurement supports it.

Projected gap closure per this ADR:
- fib: 59× → 7.5× (5-8× absolute improvement)
- ackermann: 43× → 30× (depth-2) or better (if depth-3 feasible)

Not LuaJIT parity but SUBSTANTIAL closure within method-JIT scope,
which is what the user asked for ("超过 luajit" requires multiple
ADRs; this gets fib/ack most of the way).
