# ADR: Tier 2 recursion and call-heavy closure plan

**Status:** Proposed (R20 architecture round, 2026-04-17)
**Scope:** close the fib (59×) and ackermann (44×) LuaJIT gaps inside
the method-JIT substrate.
**Related:** `internal/methodjit/emit_call_native.go`,
`docs-internal/architecture/overview.md`, `adr-no-trace-jit.md`,
R5 Tier 0 gate, R6 revert.

---

## 1. The problem

| Benchmark   | GScript JIT | LuaJIT | Gap   | Call count | ns/call (GScript) |
|-------------|-------------|--------|-------|-----------:|------------------:|
| fib(35)     | 1.412 s     | 0.024s | 59×   | ~18 M      | ~78 ns            |
| ackermann   | 0.263 s     | 0.006s | 44×   | ~33 M      | ~8 ns             |
| mutual_recursion | 0.186 s| 0.005s| 37×   | ~47 M      | ~4 ns             |

Both benchmarks are **call-cost-bound**. The Tier 2 native-BLR
sequence in `emit_call_native.go` is 13 steps with ~50 instructions
per call. At M4's ~6 GHz-equivalent (6-8 wide), 50 insns = ~10 ns
uncached but ~2 ns through the IC stream cache.

The ADR proposes what we can do within the method-JIT substrate to
close most of this gap. (Substrate choice is locked by
`adr-no-trace-jit.md`.)

## 2. Anatomy of the 78 ns/call on fib

From `emit_call_native.go` steps 1-13 (paraphrased):

| Step | Work                                         | ~ns on M4 |
|-----:|----------------------------------------------|----------:|
| 1    | Store fn-value + args to regs[]              | 2         |
| 2    | Spill live SSA regs (2-3 on fib)             | 3         |
| 3    | NativeCallDepth check                        | 1         |
| 3b   | Type-check closure (LSR+CMP)                 | 1         |
| 3c   | Sub-type check (== VMClosure)                | 1         |
| 4    | Extract ptr → Proto → DirectEntryPtr + CBZ   | 2         |
| 5    | Bounds check: calleeMaxStack fits            | 2         |
| 6    | Bump CallCount + re-tier check               | 2         |
| 7    | Save caller state on stack (5×STP, 64 bytes) | 6         |
| 8    | Copy nArgs to callee window                  | 2         |
| 9    | Set callee regs pointer, consts, CallMode    | 4         |
| 9b   | Increment NativeCallDepth                    | 1         |
| 9c   | BLR to callee entry                          | 2         |
| 10   | Restore caller state                         | 6         |
| 11   | Check exit code                              | 1         |
| 12   | Reload live SSA regs                         | 3         |
| 13   | Store result to SSA home                     | 1         |
| **total** |                                        | **~40**   |

Add ~20 ns for the callee's own prologue (check args, set up regs,
etc.) and ~15 ns for caller/callee cache pressure → **~75-80 ns**.
Matches the measured 78 ns.

LuaJIT's trace-inlined equivalent: a loop body with an integer
subtract + int add + compare + branch back. **~2-4 ns per "call."**

## 3. Strategies in priority order

Each is estimated on fib-class (self-recursive int → int) benchmarks.

### 3.1 Self-call specialization (**HIGH value, LOW cost**)

**Claim:** When a Tier 2 emit encounters an `OpCall` whose target is
provably the SAME Proto as the current function (detected via
feedback or syntactic self-reference), ~80% of the call sequence
becomes dead weight.

Specifically, for a self-call:
- Step 3b/3c (closure type check): the closure IS `self`; proto is
  known. Skip.
- Step 4 (DirectEntryPtr): emit a direct BLR to the current function's
  entry label instead of load-and-check.
- Step 5 (bounds check): callee stack size IS self's stack size, which
  the compiler already knows. Skip.
- Step 6 (CallCount bump): still needed for re-tier; but can be batched.

**Savings:** ~15-20 ns per self-call. On fib: 78 ns → 58 ns ≈ 25% win.
Projected fib wall time: 1.412s × 0.75 = **1.06 s** (vs LuaJIT 0.024s,
still 44× gap — we have more work below).

Pre-flight: microbench showing self-call path ≥ 2× faster than generic.

**Implementation cost:** small. One new emit path in
`emit_call_native.go`, feedback flag for "always self-call."

### 3.2 Arg/return register-pass specialization (**HIGH value, MODERATE cost**)

**Claim:** Steps 1, 8, 12, 13 (storing args to regs[], copying to
callee window, reloading SSA regs, storing result to home) exist
because the method-JIT calling convention is "args live in `regs[]`."
Pipe args through ARM64 parameter registers (X1, X2, X3, ...)
when TypeFeedback says args are monomorphic int/float.

Specifically for fib:
- fib(n) → fib(n-1) + fib(n-2)
- arg `n` is always int. Pass in X1 directly. Callee receives X1 as
  its `n` parameter without reg[slot] round-trip.
- Result is always int. Return in X0 without boxing + store-to-home
  round-trip.

**Savings:** ~10-15 ns per call. Combined with 3.1: ~30 ns savings →
fib 78 → 48 ns ≈ 38% total win. Projected: **0.87 s**.

**Implementation cost:** moderate. Need:
- Typed-calling-convention variant of `emit_call_native.go`.
- Callee prologue variant that expects X1, X2, ... as unboxed.
- Both caller and callee must agree on the convention (via
  `FuncProto.CallingConvention` flag set at Tier 2 compile).

Composability check: the MIXED case (self-call uses typed CC, cross-
call uses generic) is safe because callee inspects its own CC flag.

### 3.3 Shallow recursion inlining (**MEDIUM value, MODERATE cost**)

**Claim:** For tiny self-recursive functions (≤ 20 bytecodes, no
loops, 1-2 self-call sites), inline the recursive body 2-4 levels
deep at Tier 2 compile time. Adds one compare-and-branch at each
inlined level.

For fib specifically:
```
fib(n):
  if n < 2: return n
  return fib(n-1) + fib(n-2)
```

Inlined 2 deep:
```
fib(n):
  if n < 2: return n
  a = n - 1
  if a < 2: x = a
  else:
      aa = a - 1
      if aa < 2: x1 = aa
      else: x1 = fib(aa-1) + fib(aa-2)   ; recursive tail
      ab = a - 2
      if ab < 2: x2 = ab
      else: x2 = fib(ab-1) + fib(ab-2)
      x = x1 + x2
  b = n - 2
  ... same as above
  return x + y
```

Tree of recursion halves per inline level. 2-level inline eliminates
3 out of every 4 calls; 3-level eliminates 7/8. Diminishing returns
vs code-size growth (2^N).

**Savings:** 2-level inline → ~40% of calls eliminated. At ~50 ns/call
saved × 18M × 0.5 = 450 ms savings on fib. Projected fib: **0.85 s**.

Combined with 3.1 + 3.2: **0.50-0.60 s** ≈ 20× LuaJIT gap (still 20×
but closing from 59×).

**Implementation cost:** moderate. Need:
- Inline-candidate detector at Tier 2 compile.
- Bytecode cloner / inliner with alpha-renaming.
- Depth budget controller (cap inline depth at 3).
- Risk: code bloat destroys I-cache residency.

### 3.4 Tail-call optimization (**LOW value for fib, HIGH for ackermann**)

**Claim:** When the last op before return is a call whose result is
returned directly, replace the BLR with a jump (B) after argument
shuffling. Reuses the caller's frame.

fib is NOT tail-recursive. ackermann IS partially:
```
ack(m,n):
  if m == 0: return n + 1
  if n == 0: return ack(m-1, 1)       ; tail call!
  return ack(m-1, ack(m, n-1))         ; NOT tail (outer needs inner)
```

About 50% of ackermann's calls are tail-recursive. TCO converts them
from BLR (with stack save/restore) to B (with arg-shuffle only).

**Savings:** ~30-40 ns per tail call. ack has ~33M calls; ~16M are
tail. 16M × 35 ns = 560 ms savings on ack. Projected: **0.263 × 0.5 =
0.13 s**. Still 22× LuaJIT gap.

**Implementation cost:** low. Detection is local; emit changes small.

### 3.5 Integer-only fast-path for arith + compare (**MEDIUM value**)

**Claim:** `n < 2`, `n - 1`, `a + b` in fib all currently go through
the full type-dispatch emit (NaN-box check, float fallback). With
feedback saying n is always int, emit the integer comparison/subtract
directly — 1 insn instead of 3-5.

Actually GScript's `tier1_int_analysis.go` already does this at Tier 1
via `int48Safe` detection. Check if Tier 2 preserves it.

**Savings:** 10-20% of per-call work. Will measure.

**Implementation cost:** may already be done; confirm in R22+.

## 4. Composed projection

If R22-R25 land 3.1 + 3.2 + 3.3 + 3.4 + 3.5:

| Benchmark | Current | +3.1 | +3.2 | +3.3 | +3.4 | Final | LuaJIT | Gap |
|-----------|--------:|-----:|-----:|-----:|-----:|------:|-------:|----:|
| fib       | 1.412   | 1.06 | 0.87 | 0.50 | 0.50 | **0.50** | 0.024 | 21× |
| ackermann | 0.263   | 0.20 | 0.16 | 0.14 | 0.08 | **0.08** | 0.006 | 13× |

Gap closes 59× → 21× on fib, 44× → 13× on ackermann. Not LuaJIT parity,
but a 3× absolute win. Further closure must come from cross-call EA,
larger inline budgets, or whole-program speculation inside the
method-JIT substrate (per `adr-no-trace-jit.md`).

## 5. Non-goals

- **Polymorphic inline cache for calls.** Already handled by R19 IC
  ADR for field/table ops; calls are a different structural case.
- **Changing the calling convention for ALL calls.** Only self-call
  and monomorphic-shape cross-calls use the typed CC; mixed stays
  on the generic path.

## 6. Implementation staging

| Round | Step | Priority | Prereqs            |
|------:|------|:---------|:-------------------|
| R22   | 3.1 self-call specialization | HIGH | none |
| R23   | 3.2 typed calling convention | HIGH | R22 |
| R24   | 3.4 tail-call optimization   | MEDIUM | R22 |
| R25   | 3.3 shallow recursion inline | MEDIUM | R22, R23 |

R26+ reassesses after measured wins. If 3.1+3.2 beat projections,
R25's inline may be deferred. If they undershoot, escalate.

## 7. Decision

**Accepted.** R22 takes step 3.1 (self-call specialization) first:
highest win/cost ratio, smallest blast radius, pre-flight-friendly.
Reassess scope at R26 after R22-R25 tactical evidence.
