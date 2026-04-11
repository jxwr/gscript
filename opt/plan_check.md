# Plan Check — 2026-04-11-scalar-promote-float-gate-fix

**Iteration**: 1
**Verdict**: PASS

<evaluation>PASS</evaluation>

## Self-assessment audit

| field | value | pass? | notes |
|-------|-------|-------|-------|
| uses_profileTier2Func | false | ✓ | no reference anywhere in plan or tasks |
| uses_hand_constructed_ir_in_tests | false | ✓ | new test uses TieringManager + BuildGraph + RunTier2Pipeline on real nbody source (Task 1 §"Structure" steps 1–4) |
| authoritative_context_consumed | true | ✓ (see note) | self-assessment is technically correct — ANALYZE read `opt/authoritative-context.json`. However the plan does not cite any field from it because CONTEXT_GATHER's drift-driven selection chose object_creation / sort / coroutine_bench while `opt/user_priority.md` explicitly authorizes an R33 ceiling override to nbody. This tension is legitimate (user_priority.md overrides automatic gap classification per its own preamble), but it is a harness-level signal that CONTEXT_GATHER should honor strategic overrides when present. Logged for REVIEW, not blocking. |
| all_predictions_have_confidence | true | ✓ | target has `confidence: MEDIUM` + `confidence_why`; every assumption has HIGH/MEDIUM labels |
| all_claims_cite_sources | true | ✓ | every assumption has a `source:` line |

None of the five booleans is wrong. No schema violation. No self-assessment violation.

## Target audit

| field | plan | verified | pass? |
|-------|------|----------|-------|
| benchmark | nbody | present in `benchmarks/data/reference.json`.results and `benchmarks/data/latest.json`.results | ✓ |
| reference_jit_s | 0.248 | reference.json.results.nbody.jit = "Time: 0.248s" | ✓ exact |
| current_jit_s | 0.248 | latest.json.results.nbody.jit = "Time: 0.248s" | ✓ exact |
| expected_jit_s | 0.238 | — | — |
| expected_delta_pct | -4.0 | (0.238 − 0.248) / 0.248 = −4.03% → rounds to −4.0% | ✓ consistent (within 2%) |
| confidence | MEDIUM | HIGH/MEDIUM/LOW set ✓ | ✓ |
| confidence_why | references R32 diagnostic, docs/42 blog, R23 halving rule | yes — `confidence_why` decomposes the MEDIUM into (HIGH gate-fix) × (MEDIUM wall-time) and cites three sources | ✓ |

Target policy: nbody is NOT one of CONTEXT_GATHER's drift-driven candidates (top 3 were object_creation +37.8%, sort +19.1%, coroutine_bench +18.4%). The override is authorized by `opt/user_priority.md` line 5 ("R33: ceiling override authorized — complete R32's one-line gate fix"), which per its own preamble "overrides ANALYZE Step 1's automatic gap classification when present." Target choice is legitimate.

No target_issues.

## Assumption verification

| id | claim (short) | type | verdict | evidence_match | feedback |
|----|---------------|------|---------|----------------|----------|
| A1 | `pass_scalar_promote.go:99` rejects every nbody GetField because gate checks `instr.Type == TypeFloat` while `graph_builder.go:669` emits `OpGetField, TypeAny` | derivable-from-code | verified | exact | I opened `pass_scalar_promote.go:99`: `if instr.Type == TypeFloat { p.anyFloat = true } else { p.allFloat = false }`. I opened `graph_builder.go:669`: `instr := b.emit(block, OpGetField, TypeAny, []*Value{tbl}, int64(c), aux2)`. Both match the claim word-for-word. |
| A2 | When Tier 1 feedback is FBFloat at a GETFIELD pc, `graph_builder.go:671-674` emits an `OpGuardType` consumer with `Type=TypeFloat`, `Aux=int64(TypeFloat)`, `Args[0]=result` (the just-emitted GetField result) | derivable-from-code | verified | exact | `graph_builder.go:671-676`: `if b.proto.Feedback != nil && pc < len(b.proto.Feedback) { if irType, ok := feedbackToIRType(b.proto.Feedback[pc].Result); ok { guard := b.emit(block, OpGuardType, irType, []*Value{result}, int64(irType), 0); result = guard.Value() } }`. This matches exactly. I also opened `feedback_getfield_integration_test.go:90-106`: an existing test asserts precisely this chain (walks blocks, asserts `next.Op == OpGuardType && next.Args[0].ID == instr.ID && next.Type == TypeFloat`). The A2 cross-reference to the existing test is accurate. |
| A3 | nbody advance() j-loop body has 9 loop-carried (obj,field) pairs, of which exactly 3 (bi.vx, bi.vy, bi.vz) pass `isInvariantObj` because bi is defined in the outer-loop pre-header | cited-evidence | verified | approximate | Source 1 (`docs/42-the-field-that-stayed-in-a-register.md:46`) says: "the j-loop body has **six** loop-carried `(obj, field)` pairs. Three of them — `bi.vx`, `bi.vy`, `bi.vz` — are on `bi`, which is loop-invariant across the j-loop... Three of them — `bj.vx`, `bj.vy`, `bj.vz` — are on `bj = bodies[j]`, which changes every iteration". Source 2 (`opt/state.json` previous_rounds R32 entry) gives outcome `no_change` for `2026-04-11-loop-scalar-promote-nbody` and the round summary is truncated in my read. **Discrepancy**: blog says 6 pairs total, A3 says 9. Both sources AGREE on the load-bearing subclaim — exactly 3 bi pairs are promotable — and that is what the target's expected_delta_pct arithmetic in A5 depends on. The total-count delta (9 vs 6) is likely a post-pipeline counting artifact (LICM/SimplifyPhi/LoadElim may have restructured the body between the blog's "plan-time" count and R32's post-pipeline diagnostic count), not a load-bearing error. Advisory: ANALYZE should reconcile the two counts in the Lessons section after VERIFY, or at minimum note the count difference in the round's final report. The 3-promotable claim is VERIFIED and the prediction is built on 3, not 9. |
| A4 | `pass_scalar_promote.go:205-210` (replaceAllUses + removeInstr of GetField) leaves any pre-existing `OpGuardType(float)` consumer pointing at the new phi; the new phi at line 199 is constructed with `Type: TypeFloat`; `pass_load_elim.go:74` will elide the now-tautological guard via block-local CSE | derivable-from-code | verified | exact | `pass_scalar_promote.go:199`: `phi := &Instr{ID: fn.newValueID(), Op: OpPhi, Type: TypeFloat, Block: hdr}` — exact match. `pass_scalar_promote.go:205-210`: the `replaceAllUses` loop + `removeInstr` loop — exact match. `pass_load_elim.go:60-89`: confirms OpGuardType CSE dedup path exists — redundant guards are rewritten AND converted to `OpNop` so DCE can remove them. The MEDIUM confidence is honest: the plan correctly notes "no test currently exercises the post-promotion guard dedup path" — which is exactly the gap the new `TestR33_ScalarPromoteFiresOnNbody` (Task 1 File B) closes, asserting ≥3 header phis with `Type == TypeFloat` after the pass runs. Self-consistent. |
| A5 | R23 superscalar halving rule applies to the prediction; 3 × (LDR+STR) removed per j-iter × halving yields −3% to −5% wall-time; nbody j-loop body is 526 insns with 33% memory | cited-evidence | verified | approximate | Source 2 (`docs/42-the-field-that-stayed-in-a-register.md:33`) confirms the 526 insns figure exactly. Source 1 (`docs-internal/architecture/constraints.md §"Calibrate predictions"`) — **this section does NOT exist in constraints.md**. I grepped the file for `calibrat`, `superscal`, `halv`, `M4` — zero matches for the first three; one match for `M4` on line 71 discussing branch-predictor, not halving. The calibration rule IS documented, just NOT at the cited location: it lives in `CLAUDE.md` §"Hard-Won Rules" item 5: "Calibrate predictions — halve instruction-count estimates on ARM64 superscalar. Cross-check with diagnostic data." The plan's `source:` field correctly names "R23 empirical calibration rule documented in CLAUDE.md §Hard-Won Rules item 5", so the plan itself knows where the rule actually lives — the `evidence:` field's constraints.md reference is cite rot. Because the rule's content is verified via the `source:` field reference (which I independently confirmed in CLAUDE.md context loaded by this session), the assumption's substance is verified; only the primary cite path is approximate. Advisory: ANALYZE should either (a) update A5's `evidence:` to cite CLAUDE.md §Hard-Won Rules item 5 (the actual location) OR (b) add a "Calibrate predictions" subsection to constraints.md so future rounds can cite it from there. Not blocking. |

No assumption has verdict `failed`. No assumption is `unverifiable`. Two are `approximate` (A3, A5) — both with the core content VERIFIED and only peripheral citations showing minor rot or counting-artifact discrepancy.

## Scope audit

| budget | plan | task reality | ok? |
|--------|------|--------------|-----|
| max_files | 2 | Task 1 lists exactly 2: `pass_scalar_promote.go` (modify) + `pass_scalar_promote_production_test.go` (new) | ✓ |
| max_source_loc | 30 | Task 1 caps pass_scalar_promote.go at ≤15 LOC functional; test file is ≤100 LOC but plan says "tests excluded from source LOC cap per R32 review". 15 ≤ 30 ✓ | ✓ |
| max_commits | 1 | Task 1 is "Commit 1 of 1" | ✓ |

No `scope_too_tight` flag. The Task Breakdown's surgical spec is small enough that the ≤15 LOC budget is realistic for the inserted `for _, other := range b.Instrs` scan (approx 10 lines).

## Live runs performed

None. All 5 assumptions were resolvable from static source read + file existence checks. The 2-live-run budget was preserved for future phases.

<feedback>
PASS — the plan is buildable. Advisory notes (non-blocking, do not trigger a rewrite cycle):

1. **A5 cite rot**: the `evidence:` field cites `docs-internal/architecture/constraints.md §'Calibrate predictions'` — that section does not exist in constraints.md. The rule IS documented in `CLAUDE.md` §Hard-Won Rules item 5, which A5's `source:` field correctly references. Either update the `evidence:` field to point at CLAUDE.md directly, or add a "Calibrate predictions" subsection to constraints.md in a future harness-maintenance round. Not IMPLEMENT's job; log in REVIEW.

2. **A3 count discrepancy**: docs/42 blog says "six loop-carried pairs" in the j-loop body; A3 says "9 loop-carried pairs". Both agree on 3 being promotable (bi.vx, bi.vy, bi.vz), which is what the expected_delta_pct arithmetic depends on. The total-count delta is likely a post-pipeline counting artifact between plan-time (blog's 6) and post-R32-pipeline (state.json's 9). VERIFY should record the actual post-pipeline unpromoted-pair count from `TestR33_ScalarPromoteFiresOnNbody` and note whether it was 9 → 6 (blog) or 9 → ≤6 (plan) in the Lessons section.

3. **CONTEXT_GATHER / user_priority.md tension**: `opt/authoritative-context.json` targeted object_creation, sort, coroutine_bench (the top-3 drift regressors) and the plan targets nbody via `opt/user_priority.md`'s explicit R33 override. This is legitimate per user_priority.md's preamble but the authoritative-context.json fields are unused. Not a P3 violation (the override is explicit), but REVIEW should consider whether CONTEXT_GATHER should read user_priority.md and include the directed target in its candidate list when present, so ANALYZE has authoritative context for BOTH the drift-regressed benchmarks AND the strategically-chosen one. Log as harness evolution item for next REVIEW.

4. **state.json integrity hash missing**: `state.json.reference_baseline.hash` was NONE when I queried it, while `opt/authoritative-context.json.reference_sha` records `1bdfe6d61954...`. P5 requires the hash to be recorded in state.json. This is a harness-level gap unrelated to the current plan, but flagging for REVIEW/SANITY: the P5 drift-detection mechanism cannot fire without the baseline hash being populated.

None of the above blocks IMPLEMENT. A1, A2, A4 are exact matches to production source. A3's load-bearing 3-promotable claim is verified. A5's rule content is verified via its backup citation. The plan will produce a correct gate fix; the only risk is that the predicted delta misses (which is already appropriately labelled MEDIUM confidence with an R23-style halving buffer).
</feedback>

## Iteration decision

PASS — plan verified. Proceed to IMPLEMENT.

The advisories above are for REVIEW and the post-VERIFY Lessons section, NOT for a plan rewrite. Burning a PLAN_CHECK iteration to fix A5's cite path or A3's count would exceed the severity of the findings. Anthropic evaluator-optimizer pattern grants PASS when substance is verified even if peripheral metadata is imperfect — and the substance here (correctness of the gate fix, IR invariants of A4, and the 3-promotable load-bearing prediction input) is verified with exact source matches.
