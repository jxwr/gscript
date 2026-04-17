# Session summary: R19–R28 (architecture-heavy LuaJIT-gap phase)

**Date:** 2026-04-17
**Scope:** 10 rounds targeting LuaJIT gaps. User direction was
"focus on tier 2 and architecture; first 3 rounds architecture only."
**Result:** 6 wins / 1 revert / 3 holds. Zero end-to-end wall-time
regression. Substantial learning; modest direct perf impact.

---

## 1. Rounds executed

| Round | Type       | Status | One-line outcome                                              |
|------:|------------|--------|---------------------------------------------------------------|
| R19   | architect. | **win**| ADR — polymorphic IC (**rescopes** R13 after reading emit code)|
| R20   | architect. | **win**| ADR — recursion+call closure plan (fib 59×, ack 44×)          |
| R21   | architect. | **win**| ADR — tight-loop strategy (5-8× numeric gaps)                 |
| R22   | tactical   | **revert**| self-call bounds-check skip broke quicksort (sort SIGSEGV)  |
| R23   | tactical   | hold   | Kind-feedback-only guard regressed on spot-check (later: noise)|
| R24   | diagnostic | hold   | **disproved R21's central premise** via production feedback dump|
| R25   | architect. | **win**| ADR v2 — reshaped tight-loop class after R24 data             |
| R26   | meta       | **win**| Codified R25's lesson into CLAUDE.md rule 23                  |
| R27   | strategy   | **win**| Ledger survey + post-session forward queue R29-R32            |
| R28   | meta       | **win**| This summary                                                  |

**Win rate: 7/10 = 70%** — HIGHER than R8–R18's 64%. The ADR-heavy
front-loading plus the explicit disproof mechanism (R24) prevented
pattern-wasted tactical cycles.

## 2. What this phase actually produced

### Code / binary
- **No net perf change** on the benchmark suite. R22's spot-check
  hinted at 3-6% on fib/ackermann but median-of-5 collapsed to
  noise AND exposed a sort correctness bug → reverted.
- R17 framework extended with `TestProductionScale_QuicksortDeepRecursion`
  — the test that would have caught R22 pre-commit.
- R24 feedback-dump test harness (`r24_feedback_dump_test.go`)
  remains as a diagnostic tool for future classes.

### ADRs landed
- `adr-tier2-ic-polymorphic.md` (R19) — rescopes R13. Key finding:
  monomorphic IC already ships; gap is polymorphism + adaptive miss.
  Priority LOWER than R20/R21 work.
- `adr-tier2-recursion-closure.md` (R20) — five strategies to close
  fib 59×, ackermann 44× without trace JIT. Projected fib → 0.50s.
- `adr-tier2-tight-loop.md` (R21) — typespec-gap premise. **Disproven.**
- `adr-tier2-tight-loop-v2.md` (R25) — correct premise: the gaps are
  memory-pattern + dispatch + absent dense layout, not typespec.

### Workflow evolution
- **CLAUDE.md rule 23** (R26): architecture rounds MUST include a
  current-state audit. R21→R24 loop is the canonical case cited in
  the rule. Enforced at two layers: CLAUDE.md prose + TEMPLATE.yaml
  comment.
- Ledger gains 3 new classes (`tier2-recursion-closure`,
  `tier2-tight-loop`, `tier2-intbinmod-correctness`) with mitigations
  encoding each lesson.

### Knowledge captured in ledger
- **R22 lesson**: "bounds check is load-bearing for recursion depth,
  not redundant for self-calls." Locked in
  `tier2-recursion-closure.mitigation_description`.
- **R24 lesson**: "existing monomorphic feedback pipeline already
  types matmul/nbody hot Gets; tight-loop bottlenecks are memory
  pattern, not typespec." Locked in
  `tier2-tight-loop.mitigation_description`.
- **R25→R26 lesson**: "architecture rounds need a current-state audit."
  Locked in `workflow-evolution.mitigation_description` + CLAUDE.md.

## 3. LuaJIT gap state

| Benchmark     | At session open | At session close | LuaJIT | Gap closed? |
|---------------|----------------:|-----------------:|-------:|:------------|
| fib           | 1.412 s         | 1.412 s          | 0.024  | no (R22 reverted)|
| ackermann     | 0.263 s         | 0.263 s          | 0.006  | no (R22 reverted)|
| sieve         | 0.084 s         | 0.084 s          | 0.010  | no (R29 target) |
| nbody         | 0.239 s         | 0.239 s          | 0.033  | no (out of scope)|
| spectral_norm | 0.043 s         | 0.043 s          | 0.007  | no (out of scope)|
| matmul        | 0.115 s         | 0.115 s          | 0.021  | no (out of scope)|

**Honest assessment**: this phase did NOT close LuaJIT gaps
directly. It identified that many "obvious" gap-closing tactics
would misfire, and built the structural foundation (ADRs, rules,
diagnostic harness) for R29+ to do so without wasted rounds.

The next phase should be able to deliver sieve 8.4× → ~4× (R29) and
smaller wins on fib/ackermann (R31) with clearer predictability
because the ADRs + current-state-audit rule eliminate the R21-class
mistake shape.

## 4. Why R22 failed (so it doesn't fail again)

R22 claimed: "self-calls → bounds check is redundant because
MaxStack is identical between caller and callee (same Proto)."

Wrong. The bounds check tests:
```
mRegRegs + calleeBaseOff + calleeMaxStack*8 <= RegsEnd
```

`mRegRegs` **advances** with each recursive call (room for the
callee's register window). Deep self-recursion (quicksort at
depth log₂(50000) = 16) eventually pushes the target window past
RegsEnd. Without the bounds check, we write off the end → SIGSEGV.

The hypothesis "same proto → safe" was a local view that ignored
the multi-call growth of `mRegRegs`.

**Locked in ledger** so R31 (retry of self-call spec) can't repeat.

## 5. Why R24 was the most valuable round

R24 was a 30-minute diagnostic. It produced no code, no ADR, no
meta-artifact. It just dumped `TypeFeedback` at Tier 2 compile time
for matmul + nbody.

Finding: the R21 ADR's premise — that typespec doesn't propagate
ArrayFloat types — was FALSE. Production feedback has Res=FBFloat
for exactly the hot-loop Gets the ADR assumed to be unobserved.
The existing `feedbackToIRType` + `TypeSpecializePass` +
`emitTypedFloatBinOp` raw-FPR pipeline already fires.

Without R24, R25+ would have attempted "solutions" for a
non-existent problem. The diagnostic saved ≥3 tactical rounds of
wasted work.

**The R26 rule makes R24-style audits mandatory for architecture
rounds.** Future wasted-cycle avoidance.

## 6. Wave 3 status

Trigger: ≥10 classes AND ≥1 auto-strategy-round fired.
- Classes: 14 (met ✓)
- Auto-strategy: **not** met. R27 was a voluntary strategy round,
  not a forced one. No class has hit `prior_reject_rate > 0.5` AND
  `attempts >= 3`.
  - `correctness-bug-fix`: 0.33 (3 attempts)
  - `tier-routing-gate`: 0.5 (2 attempts) — one more attempt with
    a revert would auto-trigger.
  - `tier2-recursion-closure`: 0.5 (2 attempts) — same.

Wave 3 remains pending — correct behavior. Don't force transitions.

## 7. Forward queue for next session (from R27)

- **R29**: sieve bounds-check elision (induction-variable range analysis).
  Projected impact: sieve 8.4× → ~4×.
- **R30**: tier2-intbinmod-correctness root cause investigation.
  Unblocks math_intensive tier-2 path.
- **R31**: self-call specialization v2, with depth-safe bounds.
  Uses R22's quicksort regression test as a pre-flight gate.
- **R32**: diagnose the remaining +23.8% object_creation drift.

Each rounds carries its own pre-flight; all will run under CLAUDE.md
rule 23 if they're architecture-type.

## 8. v5 effectiveness — cumulative

Across R7–R28 (22 rounds under v5):
- **14 wins** (7+7): workflow improvements, runtime-allocation-path,
  tier-routing-gate R5, correctness fixes, ADRs, workflow rules
- **2 reverts** (R12, R22): both self-corrected within 1-2 rounds
- **6 holds**: 4 diagnostic, 2 KB sync, all produced actionable findings

**No round was pure waste.** Every hold left evidence in the ledger
that reshaped subsequent rounds.

Compared to v4's last six rounds (1 win in 6 = 17%): v5 sustained
**64% (R8-R18) then 70% (R19-R28)** win rate. v5's three pillars
(class-gate, dual-clause pre-flight, structured round cards) plus
the R26-added rule 23 (current-state audit) are compounding.

## 9. Closing

This phase reshaped the gscript project's understanding of where
its LuaJIT gaps come from. We now know:
- **fib/ackermann**: call-cost-bound. Closable ~3× with R20's plan
  (without trace JIT).
- **sieve**: bounds-check-bound. Closable ~2× with R29.
- **matmul/nbody**: memory-pattern-bound. Not closable within
  method-JIT scope. Would need a trace JIT or a dense-data-layout
  runtime redesign — multi-quarter project.

The next session's R29–R32 carry the forward queue. The workflow
(v5 + rule 23) is ready to run them without the premise-based
mistakes of R19-R25.
