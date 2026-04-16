# Workflow v5 — reflection after R8

**Status:** written 2026-04-17, end of R8 (first non-meta round under v5).
**Scope:** what v5 added, what it caught, where it still leaks.

---

## 1. What v5 was supposed to do (from ADR + R7)

v4's pathology: 1 win in 6 rounds, because nothing read across rounds.
R1→R2 repeated `go-gc-scan-reduction` with a different knob on the same
wrong premise. R6 repeated `tier-routing-gate` without consulting R5's
composability constraint. In both cases the signal existed in prose
notes that nobody re-read.

v5's three pillars:
1. **Hypothesis-class lookup at Step 3.** Grep `program/ledger.yaml`,
   forbid a class with `prior_reject_rate > 0.5` and `attempts ≥ 3`
   without an explicit `mitigation_description`.
2. **Structured round cards** (`rounds/NNN.yaml`) — schema-enforced,
   so the ledger is mechanically updatable.
3. **Pre-flight microbench gate** (Wave 3; opt-in earlier) — cheap
   evidence before the expensive integration.

## 2. What R8 demonstrated

R8 is the first real-code round under v5. Outcome = `hold`, not `win`,
but it self-corrected in under an hour instead of after a full
three-hour implementation.

**Signals v5 caught:**

- **The pre-flight gate fired as designed.** R8's card declared
  `acceptance: bump_per_alloc_ns ≤ current/5.0` + `halt_on_fail: true`.
  Microbench measured 3.17×. Gate fired. No production code landed.
  Had this been v4, implementation would have proceeded on a hunch and
  likely been reverted for noise-level wall-time effect — because the
  per-alloc delta in isolation doesn't tell you the wall-time delta.
- **Class promotion was mechanical.** `runtime-allocation-path` moved
  from `forward_classes` → `classes{}` with one edit block. The ledger
  update schema made this a trivial step rather than a prose write-up.
- **Revert autopsy schema forced clarity.** `revert_class:
  data-premise-error` + `revert_prevention` on the round card make the
  lesson grep-computable: R9 can be blocked from repeating the
  per-alloc-ratio framing without a written mitigation.

**Signal v5 did NOT catch:**

- **Pre-flight acceptance criterion quality.** v5 enforces that a
  criterion EXISTS and fires correctly. It does not evaluate whether
  the criterion is the RIGHT criterion. In R8, the per-alloc ratio was
  well-measured but wall-time-irrelevant. The halt was formally correct
  but cost a round to learn that the written gate was asking the wrong
  question.

This is a gap. A better gate would require the pre-flight to include
both a per-op AND a wall-time-estimate clause:

```yaml
acceptance:
  per_op_ratio: >= 2.0            # minimum per-op speedup
  wall_time_estimate: >= 10%      # per-op savings × est. op count >= X% of target
```

R9 should introduce this convention into `rounds/TEMPLATE.yaml` or as a
Wave 2 item in the ledger.

## 3. v4 pathologies that did NOT recur

- **R1→R2 class repetition:** would have been blocked at Step 3 under
  v5 (prior_reject_rate=1.0 for go-gc-scan-reduction after R1). R8 did
  consult the ledger; runtime-allocation-path was genuinely new.
- **R6 composability miss:** R8 explicitly addressed the
  tier-routing-gate composability constraint in the direction card's
  `counterfactual_check` ("The bump path is NOT a routing gate — it's
  a runtime allocator swap. Caller BLR cost is unchanged.").

## 4. What this implies for R9+

1. **R9 retries runtime-allocation-path with a CORRECTED pre-flight:**
   acceptance stated in absolute wall-time terms, not per-alloc ratio.
   The ledger's `mitigation_description` for runtime-allocation-path
   now requires this — class_gate at R9's Step 3 must cite it.
2. **Wave 2 trigger proximity.** v5 enters Wave 2 when:
   - ≥ 3 rounds run under v5 (R7 + R8, so 1 so far excluding meta)
   - ledger has ≥ 6 classes (we have 6: workflow-evolution,
     go-gc-scan-reduction, emit-layer-micro-optimization,
     kb-correction, tier-routing-gate, runtime-allocation-path)
   - at least one round consulted the ledger (R8 did)
   So we're ONE tactical round away from the Wave 2 boundary. R9 or
   R10 should trigger a meta-round that formalizes Wave 2 artifacts
   (`program/targets.yaml`, per-target budgets, the dual-clause
   acceptance schema above).
3. **Acceptance-criterion gap is the next meta improvement.** R8
   proved the structural gates work. The next level is whether the
   CONTENT of each gate is load-bearing.

## 5. Open questions for R9–R18

- Does runtime-allocation-path actually deliver ~20% on object_creation
  when integrated (not just in microbench)? If yes, can it extend to
  Table array slice allocation for methods like
  `SetArrayField`-on-growth?
- Is there a way to detect acceptance-criterion quality BEFORE the
  round halts on it? (candidate: Wave 2 requires a second reviewer
  pass on the pre-flight clause — currently impossible with a single
  agent; possibly an LLM self-review step.)
- R3's structural conclusion ("per-PC cached monomorphic dispatch is
  required to close the sieve 8.8× gap") is still unacted. Is that a
  proper class (`tier2-inline-cache` or similar) deserving a
  forward_classes entry?

---

**Bottom line:** v5 is holding. One round of real data (R8) shows the
pre-flight gate + class register prevent cheap failure modes that v4
allowed. The next evolution is criterion-quality, not criterion-
existence.
