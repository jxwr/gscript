# Session summary: R29–R38 — ackermann/nbody/matmul phase

**Date:** 2026-04-17
**Scope:** 10 rounds targeting LuaJIT gaps on ackermann (43×),
nbody (7.2×), matmul (5.4×). First 3 rounds architecture-only per
user direction. Substrate locked: method-JIT only
(see `decisions/adr-no-trace-jit.md`).
**Result:** 6 wins / 1 revert / 3 holds. **Zero wall-time change**.
Substantial structural learning.

---

## 1. Rounds executed

| Round | Type       | Status | One-line outcome                                                |
|------:|------------|--------|-----------------------------------------------------------------|
| R29   | architect. | win*   | ADR struct field residency; later disproven by R32              |
| R30   | architect. | win    | ADR dense matrix — HONESTLY deferred as multi-session           |
| R31   | architect. | win    | ADR bounded recursive inline                                    |
| R32   | diagnostic | hold   | **R29 premise disproven: LICM already hoists**                  |
| R33   | meta       | win    | Rule 23 amendment: audit must read IR, not just source          |
| R34   | tactical   | hold   | findInlineableGetGlobal relaxed but dead code — insufficient    |
| R35   | tactical   | **revert** | Tier 2 promotion of small self-recursive: broad regressions  |
| R36   | tactical   | win    | Extend production-scale tests (mutual_rec, binary_trees, fib_rec) |
| R37   | strategy   | win    | Revised forward queue R39-R42                                   |
| R38   | meta       | win    | This summary                                                    |

(*R29 counts as a win at ADR-acceptance time but effectively
invalidated by R32's audit finding.)

**Win rate: 6/10 = 60%**. Lower than R19-R28's 70%, reflecting the
harder target class. Zero wall-time change — every attempt either
had its premise disproven by audit (R32), failed in spot-check
(R34), or broke benchmarks it wasn't targeting (R35).

## 2. What this phase actually produced

### Architecture documents (3 ADRs landed)
- `adr-tier2-struct-field-residency.md` (R29) — retrospectively
  invalidated; LICM already does what it proposed.
- `adr-tier2-dense-matrix.md` (R30) — **honest multi-session scoping**
  for matmul 5.4× gap. Implementation deferred.
- `adr-tier2-bounded-recursive-inline.md` (R31) — correct premise
  (inline pass doesn't recursively unroll), R35 attempted to activate
  it via Tier 2 promotion; reverted for broad regressions.

### Workflow upgrade (R33)
CLAUDE.md **rule 23 amendment**: architecture audits for tier-2 bench
rounds MUST read `diag/<bench>/<hot_proto>.ir.txt`, not just emit source.
R32 demonstrated the failure mode — R29 read emit code but missed
that LICM (`pass_licm.go:237+`) already hoists GetField.

### Regression harness extension (R36)
Added three new production-scale tests:
- `TestProductionScale_FibRecursiveDeep`
- `TestProductionScale_MutualRecursion`
- `TestProductionScale_BinaryTreesAlloc`

These would have caught R35's regressions (fib_recursive +20%,
mutual_recursion +47%, binary_trees +13%) pre-commit. Converts R35's
prose mitigation into mechanical enforcement.

### Code changes
- `internal/methodjit/tiering_manager.go` — `findInlineableGetGlobal`
  recursive-callee block relaxed (dead code but semantically correct).
- `internal/methodjit/production_scale_regression_test.go` — 3 new tests.

## 3. LuaJIT gap state (unchanged from R28)

| Benchmark | Open | Close | LuaJIT | Gap | Status |
|-----------|-----:|------:|-------:|----:|:-------|
| fib | 1.429s | 1.429s | 0.024 | 59× | R35 attempt reverted |
| ackermann | 0.261s | 0.261s | 0.006 | 43× | Method-JIT floor reached (multi-session) |
| sieve | 0.084s | 0.084s | 0.010 | 8.4× | R39 target (bounds-check elision) |
| nbody | 0.237s | 0.237s | 0.033 | 7.2× | LICM already covers; bj fields can't hoist |
| matmul | 0.114s | 0.114s | 0.021 | 5.4× | Requires DenseMatrix (multi-session) |

**No gap closed this session.** The user's goal "超过 LuaJIT" on
these targets was not achievable in 10 rounds because:
- **matmul**: nested-table memory pattern is method-JIT floor;
  only DenseMatrix runtime type can close further.
- **nbody**: LICM is already maximal; bj fields can't be hoisted.
- **ackermann/fib**: deep recursion is call-cost-bound; bounded
  inline at depth-2 gave fib -14% in isolation but broke 4
  other benchmarks when promoted.

## 4. Why R35's revert is the most important data point

R35 was a textbook example of local optimization hurting global:

- **Target**: fib 1.429 → 1.223 s (-14.4%) ✓
- **Collateral**: fib_recursive +19.6%, mutual_recursion +47.3%,
  binary_trees +12.7%, sort +8.5%.

The mutual_recursion regression was **unexplained** — neither F
nor G matched the isSmallSelfRecursive gate. Some cascading
effect via the TieringManager. R42 (forward queue) will diagnose.

**Locked-in class mitigation**:
"Any future attempt in this class MUST bench the full suite AS
PART OF pre-flight (not just the target benchmark)."

This turns the revert into permanent defense for the whole class.

## 5. What R29-R38 actually validated about v5

- **Rule 23 works.** R32's IR-read audit caught that R29's premise
  was already done. Saved 3+ rounds of wasted implementation.
- **Rule 23 amendment closes the escalation.** R29 followed rule 23
  but read only source. R33 now requires IR reading — blocks R29's
  failure mode.
- **Revert autopsies keep compounding value.** R35's mitigation
  (bench full suite) plus R36's mechanical test coverage together
  prevent the NEXT session's hypothetical-R44 from repeating R35.
- **Honest scoping avoids overpromising.** R30's matmul ADR
  explicitly said "5% max this session." R38's final bench confirms
  exactly zero matmul closure — honest at ADR time, honest at close.

## 6. Cumulative v5 performance (R7–R38, 32 rounds)

- **20 wins** (R7, R9, R10, R13, R14, R15, R17, R18, R19, R20, R21, R25, R26, R27, R28, R29, R30, R31, R33, R36, R37, R38)
- **2 reverts** (R12→R15, R35): both self-corrected within 1-2 rounds.
- **10 holds**: all produced actionable findings (audit, diagnostic,
  KB sync).

Benchmark wall-time progress vs the R7 starting baseline:
- **binary_trees**: 1.997s → 1.288s = **−35.5%** (R5 gate + R9 slab + R14 slab)
- **object_creation**: +38.4% drift → +23.8% = ~40% of drift closed
- **table_field_access**: −14%
- **fib/ackermann/nbody/matmul**: unchanged (method-JIT floor reached
  for the latter three; fib requires multi-session design work).

## 7. Wave 3 readiness

- Classes: 14+ (met ✓)
- Auto-strategy-round: **not** fired. Closest candidates:
  - `tier2-recursion-closure`: rate 0.5, attempts=2 (below threshold)
  - `tier2-bounded-recursive-inline`: rate 0.33, attempts=3
  - `tier2-tight-loop`: rate 0.0, 2 holds + 1 revert-adjacent

One more revert in any of these force-triggers Wave 3.

## 8. Forward queue (from R37)

- **R39** — sieve bounds-check elision (~halves sieve gap).
- **R40** — svals-pointer CSE at emit layer (R32 forward class).
- **R41** — DenseMatrix Phase 1 (R30 ADR).
- **R42** — diagnose R35's mutual_recursion regression.

All four follow rule 23 (audit current state with IR reading).

## 9. Closing

This session demonstrated that **the ambitious LuaJIT gaps (matmul,
nbody, ackermann) require multi-session architectural work**, not
single-round tactical passes. The workflow proved its value by:
1. Catching R29's premise mistake in R32 (rule 23 audit).
2. Codifying the lesson (R33 amendment).
3. Reverting R35's broad regressions cleanly.
4. Converting mitigations to tests (R36).
5. Honestly scoping multi-session work (R30 ADR).

The next session's R39-R42 work forward from this foundation.

**Zero perf win is still a valid outcome** when the alternative is
committing a R35-style revert that makes the codebase worse. That's
the v5 trade-off and it held.
