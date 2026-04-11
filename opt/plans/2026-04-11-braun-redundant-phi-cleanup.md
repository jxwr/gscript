# R31 Plan: Braun Algorithm 5 Redundant-Phi Cleanup

**Cycle ID**: `2026-04-11-braun-redundant-phi-cleanup`
**Category**: `field_access`
**Target**: `sieve` (7.7× LuaJIT → predicted 7.0×)
**Initiative**: standalone (unlocks LICM for later rounds)
**Budget**: 1 Coder task, ≤300 LOC (~60 impl + ~80 test + ~20 pipeline wiring)

## Problem (from diagnostic)

`opt/diagnostics/r31-sieve.md` captured real Tier 2 IR + ARM64 disasm
for sieve's inner j-loop (2.58M iter × 46 insns). Three self-referential
phis for loop-invariant values (table `v77`, step `v78`, n `v34`) survive
graph construction and force per-iteration spill-slot traffic at the
back-edge:

- `0x7a0 str x21, [x26, #0x118]`  (v77 table self-copy)
- `0x7a4 str x22, [x26, #0x120]`  (v34 n self-copy)
- `0x78c ldr + 0x790 sbfx + 0x794 ubfx + 0x798 orr + 0x79c str` (v78 round-trip)
- `0x744 ldr + 0x748 sbfx`  (v78 reload mid-iter)
- `0x570 sbfx x1, x22`  (redecode n every iter)

Root cause (per Braun et al. 2013, §3.2 and cross-checked with Cornell
CS6120 notes): these phis form a **redundant-phi SCC** that the
existing `tryRemoveTrivialPhi` in `graph_builder_ssa.go:95` cannot
collapse because none of them is *individually* trivial — each
references another phi in the SCC, not itself. Braun's own fix is
**Algorithm 5 (removeRedundantPhis)**: run Tarjan SCC over the
phi-induced subgraph; any SCC whose outer-operand set has cardinality
1 collapses to that single value. Production compilers (LLVM
`SimplifyCFG`, V8 `CommonOperatorReducer::ReducePhi`, SpiderMonkey
`EliminatePhis`) all ship this pass as a post-construction safety net.

## Why this is Target #1

1. **Precondition for LICM**: LICM (`pass_licm.go:224`) explicitly skips
   phis. v77's def is a phi inside the loop body, so LICM cannot reason
   about hoisting SetTable validation whose operand is v77. After SCC
   collapse, v77 uses become v74 (outer-i-loop carrier defined outside
   B7), and `canHoistOp(OpGetTable)` + alias check in
   `pass_licm.go:253-263` immediately apply. This single pass *unlocks*
   a full round of downstream LICM gains we haven't been able to reach.
2. **Surgical and safe**: textbook algorithm, small blast radius, caught
   by `Validate` if broken. No regalloc, no emitter changes.
3. **User priority**: `opt/user_priority.md` mandates field_access +
   sieve first. This plan addresses that directive directly.

## Task 1 (single Coder): `pass_simplify_phis.go` — **DONE** (commit c375913)

**Status**: implemented, committed, full package test gate green.
- `pass_simplify_phis.go` (226 LOC) — Tarjan SCC over phi-subgraph, replacement map with path-compressed resolve, handles chains across SCCs.
- `pass_simplify_phis_test.go` (450 LOC) — 6 tests (self-ref, 2-phi SCC, non-redundant negative, sieve-shaped 3-phi nested, nil guard, no-phis no-op). All green.
- `pipeline.go` — wired at two sites in both `RunTier2Pipeline` and `NewTier2Pipeline`.
- `go test ./internal/methodjit/... -short -count=1 -timeout 120s` clean.

Original spec below (kept for retrospective).

---


**Files touched**:
- `internal/methodjit/pass_simplify_phis.go` (new, ~60 LOC)
- `internal/methodjit/pass_simplify_phis_test.go` (new, ~120 LOC)
- `internal/methodjit/pipeline.go` (wire pass in, ~6 LOC at 2 sites)

**Spec** (TDD — write tests FIRST, must fail before impl):

### Test 1 — self-referential phi, reducible (must collapse)

Hand-build `Function` with:
```
B0 entry:   const v0=42; Jump B1
B1 (seal 2 preds):
    v1 = Phi(B0:v0, B2:v1)     ; self-ref
    Jump B2
B2:         Jump B1
```
Call `SimplifyPhisPass(fn)`. Assert:
- `len(B1.Instrs)` drops by 1 (phi removed)
- Any prior use of `v1` now refers to `v0` (via `instr.Args`)
- `Validate(fn)` returns no errors

### Test 2 — redundant SCC (3 phis, 1 outer value)

Hand-build the sieve pattern:
```
B0: const v0=42; Jump B1
B1 (hdr, 2 preds):
    v1 = Phi(B0:v0, B3:v2)
    Jump B2
B2: Jump B3
B3: v2 = Phi(B1:v1, B2:v1)  ; references v1, which references v2 through B3
    Branch(cond) B1, B4
B4: use(v1); use(v2); Return
```
After `SimplifyPhisPass`: both phis collapse to v0. `use(v1)` and
`use(v2)` args now point to v0. Tarjan runs in topological order so the
entire SCC resolves in one pass.

### Test 3 — non-redundant (2 outer values → must NOT collapse)

```
B0: v0=1; Jump B2
B1: v1=2; Jump B2
B2: v2 = Phi(B0:v0, B1:v1)
```
After pass: phi unchanged; `Validate(fn)` returns no errors.

### Test 4 — sieve-shaped regression fixture

Build a `Function` that mimics the sieve structure from the diagnostic
(B7 inner-header with 3 self-phis for table/step/n). Run the pass.
Assert those 3 phis are removed and their uses now reference the outer
B4/B17 values. This is the fixture that binds the plan to the reported
bottleneck.

### Implementation sketch (reference only; Coder writes their own)

```go
// pass_simplify_phis.go
package methodjit

// SimplifyPhisPass removes redundant phi SCCs per Braun et al. 2013
// Algorithm 5. Catches self-referential phis and redundant-phi cycles
// that survive graph construction (e.g. nested loop headers where the
// builder's one-shot tryRemoveTrivialPhi cannot resolve an SCC).
func SimplifyPhisPass(fn *Function) (*Function, error) {
    if fn == nil { return fn, nil }

    // 1. Collect all phi instructions.
    // 2. Build phi→phi edge set (phi.Args whose Def is also a phi).
    // 3. Tarjan SCC over phi subgraph.
    // 4. For each SCC in reverse topological order:
    //      outer := union(arg for phi in SCC where arg.Def not in SCC
    //                                         and arg.Def not already collapsed)
    //      if len(outer) == 1: replace all uses of every phi in SCC with outer,
    //                          delete phis from their blocks.
    //      else: leave SCC alone (legitimately multi-valued).
    // 5. Return fn. Validate is run by pipeline.
    ...
}
```

**Pipeline wiring** (`pipeline.go`):
- Insert `SimplifyPhisPass` as the **first step** of `RunTier2Pipeline`
  (before `TypeSpecializePass` at line 283). Reason: collapsing
  redundant phis early gives TypeSpec/ConstProp cleaner IR to work
  with and ensures LICM sees post-cleanup phis.
- Insert a **second call** immediately after `InlinePassWith` at line
  307, *before* the post-inline `TypeSpecializePass`. Inlining
  creates new merge blocks and can re-introduce redundant phis.
- Mirror in `NewTier2Pipeline()` diagnostic pipeline at lines 349+.

## Non-goals (explicit)

- **No LICM changes this round.** The hoisting of the SetTable
  validation tower is a *follow-on* round that consumes the unlocked
  IR. This round proves the SCC collapse works and measures the raw
  spill-slot reduction from eliminated phi-copies.
- **No regalloc or emitter changes.** Trivial phi cleanup is
  IR-level only.
- **No cross-phase work.** `graph_builder_ssa.go::tryRemoveTrivialPhi`
  stays as is — we add a global post-construction pass instead of
  patching the incremental builder.

## Prediction (calibrated)

From diagnostic §5: ~10 insns/iter removed × 2.58M iter = 25.8M insns.
At Apple M4 ~3 insn/cycle steady-state × 4 GHz, pure compute savings
≈ 2.1 ms. Wall-time prediction uses the research agent's M4 store-port
analysis: on store-bound spill loops the effective savings land in the
**8–12% band** (well below the naïve 22% from raw insn-count scaling).

**sieve wall-time**: 0.085 s → **0.075–0.078 s** (−8% to −12%)
**LuaJIT ratio**: 7.7× → **7.0×**

Secondary benchmarks with similar nested-loop patterns (matmul,
spectral_norm, nbody) may see 1–3% collateral from SCC cleanup in
their inner headers — not counted in the primary prediction.

## Scope creep tripwires

- If the Coder sub-agent needs to touch **any** file outside
  `pass_simplify_phis*.go` and `pipeline.go`, stop and escalate.
- If `Validate(fn)` ever returns errors after the pass, stop. The
  expected bug class is "dangling Value pointer after SCC collapse"
  — dump the offending phi SCC and fix in-place rather than expanding
  scope.
- If `go test ./internal/methodjit/...` (full package, not curated
  subset — R30 lesson) shows any new failure, stop. Do not paper over
  with exception lists.

## Definition of done

1. `pass_simplify_phis.go` + test file committed with all 4 tests
   green.
2. `pipeline.go` wires the pass at both insertion points.
3. `go test ./internal/methodjit/...` passes cleanly (full package).
4. `TestProfile_Sieve` re-dumped after landing: the 3 self-phis for
   v77/v78/v34 are gone from the IR; the hot loop at 0x570–0x7ac
   loses the `str x21/x22` self-copies and v78 ldr/re-box block.
5. Benchmark suite (median-of-5) with sieve improvement ≥ 5%
   (floor), ideally 8–12% (target). If between floor and target,
   still counts as improved and closes the cycle.

## Evaluator checklist

- [ ] Pass skipped when `fn == nil` or no phis present
- [ ] Handles single self-referential phi (Test 1 fixture)
- [ ] Handles redundant 2-phi SCC (Test 2 fixture)
- [ ] Handles redundant 3-phi SCC (Test 4 sieve fixture)
- [ ] Leaves legitimate multi-value phis alone (Test 3 fixture)
- [ ] `Validate(fn)` clean after pass
- [ ] No panics on deeply-nested SCC (stress test optional)
- [ ] Replaces uses in ALL instructions (including terminators' Aux
      fields if any — check regalloc after pass to confirm)
- [ ] Removes deleted phis from `block.Instrs` AND from any
      builder-side bookkeeping (`block.defs`, `block.incomplete`)

## Results (filled by VERIFY)

| Benchmark | Before | After | Change | Expected | Met? |
|-----------|--------|-------|--------|----------|------|
| sieve | 0.085s | 0.084s | -1.2% | -8% to -12% | no (below 5% floor) |
| fib | 1.437s | 1.410s | -1.9% | — | noise |
| fib_recursive | 14.400s | 14.120s | -1.9% | — | noise |
| mandelbrot | 0.063s | 0.062s | -1.6% | — | noise |
| ackermann | 0.274s | 0.267s | -2.6% | — | noise |
| matmul | 0.119s | 0.119s | 0% | — | noise |
| spectral_norm | 0.045s | 0.045s | 0% | — | noise |
| nbody | 0.251s | 0.248s | -1.2% | — | noise |
| fannkuch | 0.049s | 0.049s | 0% | — | noise |
| sort | 0.050s | 0.050s | 0% | — | noise |
| sum_primes | 0.004s | 0.004s | 0% | — | noise |
| mutual_recursion | 0.194s | 0.190s | -2.1% | — | noise |
| method_dispatch | 0.103s | 0.101s | -1.9% | — | noise |
| closure_bench | 0.027s | 0.027s | 0% | — | noise |
| string_bench | 0.031s | 0.031s | 0% | — | noise |
| binary_trees | 2.036s | 2.013s | -1.1% | — | noise |
| table_field_access | 0.042s | 0.043s | +2.4% | — | noise |
| table_array_access | 0.095s | 0.094s | -1.1% | — | noise |
| coroutine_bench | 17.183s | 19.278s | +12.2% | — | high-variance, ignored (prior rounds) |
| fibonacci_iterative | 0.295s | 0.295s | 0% | — | noise |
| math_intensive | 0.068s | 0.070s | +2.9% | — | noise |
| object_creation | 1.089s | 1.068s | -1.9% | — | noise |

### Test Status
- `./internal/methodjit/...` PASS (1.494s)
- `./internal/vm/...` PASS (0.304s)
- Full package, both suites green.

### Evaluator Findings
- VERDICT: pass (all 9 checklist items pass)
- Minor notes: (a) Test 4 comment labels inner phis as a "3-phi SCC" but they are three independent self-referential singleton SCCs — correct Tarjan behavior, just mislabeled in the test comment. (b) `resolve()` lacks write-back path compression but is functionally correct at realistic chain depths.

### Regressions (≥5%)
- None. coroutine_bench +12.2% is known high-variance and ignored per R29 precedent.

## Lessons

- **The predicted redundant-phi SCCs were never in the production IR.** The R31 diagnostic at `opt/diagnostics/r31-sieve.md` read IR from the simplified `profileTier2Func` pipeline, which is known-stale (see `constraints.md` "Diagnostic test pipeline mismatch"). Production `compileTier2` runs a fuller pass order that already collapses these phis through `tryRemoveTrivialPhi` + downstream ConstProp/DCE. SimplifyPhisPass catches nothing on sieve — hence no wall-time movement.
- **Generic infra lands anyway.** SCC-based redundant-phi cleanup is textbook and small (226 LOC + 450 LOC tests). It's correctness-neutral, Validate-clean, and zero-overhead on functions with no phi SCCs — keep it as a safety net for future construction bugs or inliner-introduced SCCs.
- **R19 lesson repeats under a new hat.** Round 19 (table-kind specialize, field_access, no_change) predicted sieve wins from eliminating a predictable 4-way dispatch cascade; M4 branch predictor made the dispatch free. R31 predicted sieve wins from eliminating a predicted set of phi-driven spills; but those spills were already gone in production. Pattern: sieve's hot loop at production Tier 2 may already be close to the compute floor.
- **Diagnostic tool debt is the root of two wasted rounds (R19, R31).** `tier2_float_profile_test.go::profileTier2Func` must either (a) be deleted, (b) be rewritten to call `compileTier2()` end-to-end, or (c) be gated behind a `//go:build diagnostic_stale` tag with a warning. Next ANALYZE must treat diagnostics from this test as invalid.
- **Field_access ceiling now at 2.** Per the ceiling rule, field_access is temporarily deprioritized for the next few rounds; ANALYZE should pivot to tier1_dispatch depth-gated self-call (the R30 revert leaves this undone) or a different category entirely.
