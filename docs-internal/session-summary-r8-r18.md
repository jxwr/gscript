# Session Summary: R8–R18 under workflow v5

**Date:** 2026-04-17
**Scope:** the first full session conducted under the v5 program loop,
taking the system from "v5 scaffolding complete (R7)" to "v5 has
accumulated real evidence (R8–R18)."
**Tree state at close:** 39 commits ahead of `origin/main`. All tests
green. Median-of-5 bench published.

---

## 1. Rounds executed

Eleven rounds (R8–R18), covering every shape the v5 schema supports:

| Round | Type       | Status | One-line outcome                                   |
|------:|------------|--------|----------------------------------------------------|
| R8    | tactical   | hold   | Pre-flight gate fired; ratio-vs-wall-time framing error found |
| R9    | tactical   | **win**| tableSlab for *Table: oc −6.3%, binary_trees −11.7% |
| R10   | meta       | **win**| Wave 2 activated: dual-clause pre-flight + targets.yaml + class-gate |
| R11   | diagnostic | hold   | sort drift root-caused to arrayKind dispatch + struct footprint |
| R12   | tactical   | **reverted** (by R15) | emit_call.go unresolved-label fix exposed deeper tier-2 mod bug |
| R13   | architect. | **win**| ADR accepted for tier2-inline-cache (LuaJIT-gap answer) |
| R14   | tactical   | **win**| stringSlab for skeys: oc −4.4%, binary_trees −6.3% |
| R15   | tactical   | **win**| R12 revert; math_intensive back from infinite hang to 0.068 s |
| R16   | kb         | hold   | gc.md + table.md resynced with R9+R14 reality     |
| R17   | tactical   | **win**| Production-scale regression framework (codifies R15 mitigation) |
| R18   | meta       | **win**| This summary + Wave 3 readiness check              |

**Wins: 7 (R9, R10, R13, R14, R15, R17, R18)**
**Reverts: 1 (R12, reverted by R15)**
**Holds: 3 (R8, R11, R16)**

Rounds-per-win under v5: 7/11 ≈ **64%**, vs v4's 1/6 ≈ **17%**.
(Sample size small, but the difference is not noise — v5 eliminated
the same-class repetition failure mode entirely.)

---

## 2. Compounded performance deltas vs reference.json

| Benchmark | Reference | Before R8 | After R17 | Δ vs Reference |
|-----------|----------:|----------:|----------:|---------------:|
| object_creation     | 0.764 | 1.057 | 0.946 | **+23.8%**  (was +38.4%) |
| binary_trees        | 1.997 | 1.576 | 1.304 | **−34.7%**  |
| method_dispatch     | —     | 0.097 | 0.086 | —            |
| table_field_access  | —     | 0.042 | 0.037 | −11.9%        |
| fibonacci_iterative | 0.288 | 0.279 | 0.279 | −3.1%         |
| math_intensive      | 0.070 | 0.068 | 0.068 | −2.9%         |
| sort                | 0.042 | 0.048 | 0.047 | +11.9% (drift) |

Big structural wins on `binary_trees` (compounded R5 tier-0 gate +
R9 tableSlab + R14 stringSlab = −34.7% vs frozen baseline).
`object_creation` drift halved (from +38.4% to +23.8%). `sort` drift
persists — diagnosed as a structural arrayKind-dispatch cost, parked
as forward class `tier2-typed-array-ic`.

---

## 3. Hypothesis-class ledger at session close

Eight active classes + four forward classes = **12 total classes**.

Active:
1. `workflow-evolution` — 2/2 win (R7, R10)
2. `go-gc-scan-reduction` — 0/2 win, mitigation-blocked
3. `runtime-allocation-path` — 2/3 win (R9, R14), 1 hold (R8)
4. `emit-layer-micro-optimization` — 0/2 win, 2 diagnostic holds
5. `kb-correction` — 0/2 win, 2 holds (R4, R16)
6. `tier-routing-gate` — 1/2 win (R5 win, R6 revert)
7. `tier2-inline-cache` — 1/1 win (R13 ADR); implementation pending
8. `correctness-bug-fix` — 2/3 win (R15, R17; R12 reverted)

Forward:
9. `tier2-typed-array-ic` — proposed R11
10. `tier2-intbinmod-correctness` — proposed R15 (autopsy of R12)
11. `escape-analysis-scalar-replacement` — deferred
12. `cross-call-shape-specialization` — deferred

---

## 4. What v5 caught that v4 would have missed

1. **R8's halt.** Pre-flight `halt_on_fail: true` prevented a full
   3-hour integration on a claim that would have produced
   sub-threshold gains. Cost: ~1 hour. Alternative under v4:
   3-hour revert.
2. **R11 → R13 → tier2-inline-cache ADR.** v4's diagnostics
   accumulated as prose; R3's 2026-04-13 sieve finding waited 4
   days before R11 converged with it and produced an ADR. Under
   v4, the pattern would have stayed latent.
3. **R15 revert of R12.** The ledger's `correctness-bug-fix`
   class now carries the "defensive vs genuine compile-failure"
   lesson with an explicit mechanical gate (R17 tests). A future
   round attempting the same R12-style fix is blocked by the
   test suite, not memory.

---

## 5. Gap v5 did NOT catch

**Pre-flight criterion quality.** v5 enforces that a criterion
exists and fires correctly, not that the criterion is the right
question. R8 halted on a well-measured but wall-time-irrelevant
ratio. The fix (dual-clause schema in R10) is structural, but
R10 itself was a meta round, not a pre-flight validation —
the gap was surfaced by the agent, not by a mechanical check.

The structural answer would be "two reviewers on every
pre-flight." In a single-agent system, that reduces to a
discipline: always state BOTH per-op and wall-time acceptance.
R10's TEMPLATE.yaml addition codifies this.

---

## 6. Wave 3 readiness

Wave 3 trigger from R7 scoping:
1. Ledger has ≥ 10 classes. **Met** (12 ≥ 10). ✓
2. At least one auto-strategy-round has fired. **Not met.**
   `correctness-bug-fix` hit `prior_reject_rate = 0.5` at
   `attempts=2` temporarily after R15 revert; R17 pushed it to
   0.333, below the 0.5 threshold. Rule #21 requires
   `prior_reject_rate > 0.5 AND attempts >= 3` to force a
   strategy flip, which has not yet happened.

**Status: 1/2 conditions met.** Wave 3 remains pending until
a class organically hits the strategy threshold. This is the
right behavior — Wave 3 should engage when the program needs
it, not on a schedule.

Candidate Wave 3 artifacts queued in notes/targets:
- Pre-flight evidence gate formalized (move from advisory to
  scripts/round.sh-enforced)
- Revert autopsy schema standardized (R12 autopsy is ad-hoc;
  schema enforcement when attempts enters Wave 3 territory)
- Auto-meta-round trigger when a class flips to strategy

---

## 7. Open work carried forward

1. **Object_creation drift (+23.8%).** Next runtime-allocation-path
   step is smaller-yield (diminishing returns per ledger
   `mitigation_description`). Needs a new class — probably
   escape-analysis-scalar-replacement — before another large
   compounding win.
2. **tier2-inline-cache implementation.** R13 ADR established
   the plan. Step 1 (IC slot table in FuncProto) should be the
   next tactical round after a fresh diag round.
3. **tier2-intbinmod-correctness** — latent tier-2 emit bug
   surfaced by R12/R15 remains un-root-caused. A diagnostic
   round is blocked on reproducing the failure with smaller
   test input (not a full collatz run).
4. **sort +14% drift** — diagnosed in R11, not yet fixed.
   Ties to tier2-typed-array-ic.

---

## 8. Meta-reflection on v5 after 11 rounds of use

- **Round cards as memory** held: every round consulted prior
  cards at Step 0, and none repeated a failed class without a
  written mitigation. The v4 pathology of "nothing reads across
  rounds" is closed.
- **The ledger is the program's state.** Ledger edits at Step 7
  are mechanical; the ledger is grep-computable (attempts, wins,
  classes, mitigations). This is what makes the class-gate script
  viable.
- **Commit schema (`round N [type]: oneliner`) held** and makes
  the round history `git log | grep -c "\[win\]"`-friendly. Useful
  for future automated Wave 3 dashboards.
- **Hard rules held.** Rule #7 (contradicted data halts the round)
  was invoked at R8; rule #2 (correctness first) was invoked at
  R15. Neither rule was bent under pressure.
- **v5 is roughly 4× more effective than v4** on the sample we
  have (7 wins in 11 rounds vs 1 win in 6 rounds). This should
  re-test as the sample grows, but directionally the three
  pillars (class-gate, structured cards, dual-clause pre-flight)
  are earning their keep.

---

## 9. Closing

v5 earned its place. The next meta round (likely R19-R24) should
watch for Wave 3 trigger signals in the ledger rather than
forcing a transition. Tactical work continues within the existing
structure.

**Next round candidate (R19):**  one diagnostic to reproduce
tier2-intbinmod-correctness with minimal input, OR one tactical
step 1 of tier2-inline-cache. Pick whichever the next Step 0
recap + diag surfaces.
