# ADR — Call-path reduction arc (R147–R156)

**Status**: open, arc in flight.
**Date**: 2026-04-19
**Supersedes**: none (complements `adr-tier2-recursion-closure.md`).

## Context

Post-R146 gap snapshot (Σ of `gap_factor` across benchmarks that have a
LuaJIT reference, grouped by the optimization lever that would most help
each benchmark):

| Cluster | Benchmarks | Σ gap_factor |
|---------|-----------|-------------:|
| A. call-path reduction | ackermann 75.3, mutual_recursion 47.5, fib 35.1 (+ fib_recursive, method_dispatch n/a) | **157.9** |
| B. IC / shape-guard    | sieve 7.82 (+ table_field, binary_trees, object_creation n/a) | 7.82 |
| C. LICM / range        | nbody 7.17, spectral_norm 5.63, sort 4.73, spectral_dense 3.57, matmul_dense 1.59, fannkuch 2.30, sum_primes 2.00 | ~27 |
| D. string/concat       | string_bench n/a | — |
| E. closure/upvalue     | closure_bench, coroutine_bench n/a | — |
| F. parity              | mandelbrot 1.00 | 1.00 |

Cluster A is **5.7× larger** than the next-biggest cluster. Three of the
top-4 widest gaps live there.

R145 proved that simply opening the Tier 2 gate for ack/mut does not
help: ack regressed +64.6% when promoted, because Tier 2's caller-side
native call path is ~50 insns/call vs Tier 1's ~26 insns/call on the
same self-call. The call-path emit itself is the next lever — not the
promotion decision.

## Decision

Open a 10-round arc (R147–R156) targeting Cluster A as the **primary**
attack, with Cluster C held as a contingent fallback.

### Primary plan (R147–R152)

Land a **static-self-call fast path** in `emit_call_native.go` —
a dedicated emit branch for the pattern

```
callee_proto == caller_proto  ∧
callee is a statically-resolved self-call (already detected by
   R132's `staticallyCallsOnlySelf` / R40's `irHasSelfCall`)
```

Under this predicate, the following per-call work is **provably
redundant** and must be skipped:

| Work | Rationale for skipping | Est. insns |
|------|-----------------------|-----------:|
| IC cached-closure compare | Callee is fixed at compile time | 2 |
| VMClosure subtype guard | Compile-time constant | 2 |
| MaxStack bounds check | `callee.MaxStack == caller.MaxStack` | 3 |
| CallCount increment | Already at Tier 2; counter is dead | 3 |
| ClosurePtr scratch save/restore | Same proto → same closure ptr | 2 |
| Constants pointer save/restore | Same proto → same constants | 2 |
| GlobalCache pointer save/restore | Same proto → same cache | 2 |
| **Total** | | **~16** |

Additional savings contingent on R153 (regalloc symmetry) and R154
(lightweight 16-byte self-entry):

- **Spill/reload round-trip** for args already in destination physRegs
  (Plan B / R153): ~8–12 insns depending on live register count.
- **Thin prologue**: 16-byte frame instead of 128-byte for proven
  self-entry (Plan C / R154 = R145 re-attempt, now cheaper because
  caller-side is thinner): ~10 insns amortized.

Total budget: bring ack Tier 2 from 0.452s (R146) to **≤ 0.28s**
(beating ack Tier 1's 0.271s baseline) and fib Tier 2 from 0.912s
to **≤ 0.60s** by R152.

### Sub-round allocation

| R | Type | Deliverable |
|---|------|------------|
| R147 | architecture | This ADR. No code. |
| R148 | architecture | Current-state audit: IR read for fib/ack/mut, per-insn cost list of `emit_call_native.go`, confirm the skippable-insn budget. |
| R149 | tactical | Skip IC + closure type guard for static self-call. |
| R150 | tactical | Skip MaxStack + CallCount inc. |
| R151 | tactical | Collapse Closure/Constants/GlobalCache scratch. |
| R152 | tactical+verify | Median-of-5 measurement; halt gate. |
| R153 | tactical | Regalloc symmetric self-call (Plan B) — conditional on R152 pass. |
| R154 | tactical | Lightweight 16-byte t2_self_entry (Plan C) — conditional on R153 pass. |
| R155 | tactical | Extend `staticallyCallsOnlyCluster` for mutual recursion (F↔M). |
| R156 | meta | Arc close. Full bench run, luajit_gap updates, ledger counters, memory note. |

### Halt conditions (trigger pivot to Cluster C)

**Halt and pivot** to Cluster C (LICM / range analysis on nbody +
spectral_norm + sieve) at R152 if ANY of:

1. **Cost-model violation** — R149+R150+R151 combined savings measured
   at less than 60% of the predicted 16-insn total on fib.
2. **Null wall-time movement** — fib stays within ±1.5% of R146's
   0.912s across 5-sample median despite shipped code (implies the
   benchmark is dominated by something outside emit_call_native —
   probably body emit or epilogue BRV).
3. **Ack still regressed** — ack Tier 2 > 0.40s after R151 (i.e., <10%
   improvement on the R145 regression).
4. **tier2-recursion-closure class exceeds prior_reject_rate 0.5** —
   currently 0.25; one more revert in the class tips the balance and
   triggers rule 21 mitigation.

Under any halt, R153–R156 reallocate to Cluster C: measure legacy
nbody (7.17×) vs dense nbody (5.44×), identify the delta, land one
LICM/range-analysis improvement + one loop-body hoist per round.

### Why not pivot earlier?

R132 is the single largest BENCH-WIN on record (-39% fib). That came
from a one-line gate change unblocking numeric-conv for fib. R145 is
the most recent revert. The pattern is unambiguous: the call-path IS
the bottleneck; the question is only whether we can make the Tier 2
emit pay off, not whether the target is right.

## Consequences

**If the arc lands cleanly (all of R149–R155)**:
- fib gap: 35.1× → ~25× (estimated -30% wall time from 0.912 → 0.64).
- ack gap: 75.3× → ~45× (0.452 → 0.27; back to Tier 1 parity, then
  below via Plan C).
- mut gap: 47.5× → ~30× once R155's cluster detection lands.
- The emit_call_native.go static-self-call fast path becomes a
  permanent subsystem; `staticallyCallsOnlySelf` graduates from a
  promotion gate to a code-specialization gate.

**If halt at R152**: R149–R151 code stays in tree (they are
architecturally sound regardless of aggregate win). R153–R156 pivot
documented in a separate ADR amending this one.

**If halt at R153**: Plans A ships, Plans B + C deferred.

## References

- R132 / R137 / R144 / R145 round cards.
- `adr-tier2-recursion-closure.md` — class background.
- `program/ledger.yaml` → `tier2-recursion-closure` entry.
- User memory: `project_r87_r102_arc_final.md`,
  `project_call_opt_arc_final.md`,
  `project_tier2_promotion_blocker.md`.
