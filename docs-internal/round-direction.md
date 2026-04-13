---
round: 0 (dry-run)
date: 2026-04-13
diag_snapshot: diag/summary.md
---

# Round Direction

## Evidence

Top drifters vs frozen `benchmarks/data/reference.json`:

| Benchmark | Reference | Latest | Drift | Status |
|-----------|----------:|-------:|------:|:-------|
| `object_creation` | 0.764s | 1.141s | **+49.35%** | FAIL |
| `sort` | 0.042s | 0.051s | **+21.43%** | FAIL |
| `closure_bench` | 0.027s | 0.028s | +3.70% | FLAG |
| `table_array_access` | 0.094s | 0.097s | +3.19% | FLAG |
| `fannkuch` | 0.048s | 0.049s | +2.08% | FLAG |

All other benchmarks within ±1%. Histogram anomalies: 15 of 22 benchmarks show >50% memory-ops in their hottest proto — broad store/load dominance consistent with the baked-in GC-trace overhead.

Hottest-proto shape for `object_creation/<main>`: 5468 insns, 1297 loads, 1933 stores, 574 branches. The inline `new_vec3` leaf shows 208 insns with 44 loads + 85 stores for a function whose IR is one `NewTable` + three `SetField` — 129 memory ops for 3 field writes.

## Q1 — Global architecture?

**Candidate exists, but not the right target for Round 1.**

The histogram anomalies + GC ceiling (`kb/modules/runtime/gc.md` Known gaps) point to a long-term Q1: **Tier-2-only bump allocator for short-lived Tables**. Would require escape analysis, a new allocation tier, and a custom GC contract — multi-round, significant scope, user discussion required.

Not now because the current regression isn't caused by the ceiling — it's a module-level bug masquerading as a ceiling problem.

**Status: tabled.** Revisit after module-level fix validates the real ceiling position.

## Q2 — Module boundary / algorithm?

**YES — this is the target.**

Two concrete, documented fixes in the KB:

### Fix A: remove dead `shape *Shape` field from `runtime.Table`

`kb/modules/runtime/table.md` Known gaps (lines 54-57):

> The `shape *Shape` field is write-only (as of 2026-04-13). The JIT never reads the pointer — it only reads `shapeID uint32`. This field costs one traced pointer per table and is the primary contributor to the `object_creation +49%` drift vs `reference.json`. Removing the field and rewriting `applyShape` / `clearShape` to compute `shapeID` directly via `GetShapeID(skeys)` is a ~3-file, ~80 LOC forward fix.

**Grep verification**: no production code reads `t.shape` (only reads `t.shapeID`). Tier 1 inline cache at `tier1_table.go`, Tier 2 emit at `emit_table_field.go` — both load the uint32, not the pointer. The dead pointer is `internal/runtime/table.go:50`.

**Expected impact**: removes 1 traced Go pointer per Table. `object_creation` allocates ~800k tables per run; each GC cycle visits one less pointer × 800k = significant trace work.

### Fix B: high-water-mark `ScanGCRoots`

`kb/modules/runtime/vm.md` Known gaps:

> ScanGCRoots scans the full register slice (as of 2026-04-13). Post-R35 analysis identified this as ~25 percentage points of the `object_creation +49%` drift vs reference. A high-water-mark field in the VM would scan only `regs[:regHighWater]`, skipping the 2×-capacity tail.

**Grep verification**: `EnsureRegs` allocates at 2× capacity policy (`kb/modules/runtime/vm.md` invariant). `ScanGCRoots` walks `v.regs` unconditionally. Adding `regHighWater int` to the VM and updating it inside `EnsureRegs` is mechanical.

**Expected impact**: removes trace of the 2×-capacity tail. Tail can be ~16k entries × (NaN-box filter check) × (GC cycles). Complements Fix A.

### Scope

- **Files touched**: `internal/runtime/table.go`, `internal/vm/vm.go`, plus tests.
- **LOC**: ~80 source + ~50 test (per knowledge doc).
- **Risk**: both are forward fixes to correctness commits (39b5ef3), neither weakens a correctness invariant. Coverage: existing `runtime/table_test.go`, `vm/gc_scan_test.go`.

## Q3 — Local optimization?

Tabled. Q2 is the correct level for the current evidence. Local pass/emit work would be premature until the regression is closed.

## Prediction (calibrated, halved per CLAUDE.md rule 8)

- `object_creation`: 1.141s → ~0.80s (−30%), lower bound −20%. HIGH confidence — bisect already identified 39b5ef3 as the root cause and both fixes are mechanical undos of specific sub-changes.
- `sort`: 0.051s → ~0.045s (−12%). MEDIUM confidence — shares the ScanGCRoots cost but has less table allocation pressure.
- `closure_bench`, `table_array_access`, `fannkuch`: likely drop below flag threshold. LOW confidence on specific magnitudes — these were already borderline.
- Other benchmarks: zero change expected. LOW concern.

## Round 1 plan sketch

(Not to be executed in Round 0 — this is the forward-looking target for the first real v4 round.)

1. Task 0: write a failing test — `TestObjectCreation_RegHighWater` asserts ScanGCRoots visits only the high-water-mark prefix, and a `TestTable_NoDeadShapePointer` regression test ensuring `Table.shape *Shape` field is absent.
2. Task 1: Fix A — delete `shape *Shape` field; rewrite `applyShape`, `clearShape`, `LookupShapeByID` consumers. Verify no grep hits for `\.shape` on `*Table` receivers outside archived code.
3. Task 2: Fix B — add `regHighWater` to VM struct, update `EnsureRegs` to track max write index, change `ScanGCRoots` slice bound.
4. Verify: `bash scripts/diag.sh object_creation sort closure_bench table_array_access`; drift must close to within flag threshold.
5. KB update: remove the two "Known gap" entries from `runtime/table.md` and `runtime/vm.md`/`runtime/gc.md`, bump `last_verified` date on those cards.

## Dry-run outcome

Round 0 was infrastructure validation, not an optimization round. No production code changed. `scripts/round.sh --no-bench` prep took ~30 seconds end-to-end (L1 index 3s + diag 25s + kb_check <1s). This document was produced in under 15 minutes from the `round.sh` completion banner — meeting the Round 0 success criterion #4 in `docs-internal/workflow-v4-plan.md`.
