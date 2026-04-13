---
cycle_id: 2026-04-12-gc-overhead-regression-fix
date: 2026-04-12
category: allocation_heavy
initiative: standalone

target:
  - benchmark: object_creation
    current_jit_s: 1.141
    reference_jit_s: 0.764
    expected_jit_s: 0.800
    expected_delta_pct: -29.9
    confidence: MEDIUM
    confidence_why: "R35 bisect confirmed 39b5ef3 as sole culprit (+49.35% drift). Fix removes the two identified overhead sources. MEDIUM not HIGH because (a) GC overhead is non-linear — removing one pointer per table and bounding scan range may not recover 100% of the regression, (b) there may be secondary GC effects from the Shape registry itself (sync.Map pressure) not addressed by this fix."
  - benchmark: sort
    current_jit_s: 0.051
    reference_jit_s: 0.042
    expected_jit_s: 0.044
    expected_delta_pct: -13.7
    confidence: MEDIUM
    confidence_why: "R35 knowledge doc attributes sort drift to ScanGCRoots change (sort uses integer-indexed GetTable/SetTable, not SetField/shapes). Fix 2 (bounded scan) directly addresses this. MEDIUM because sort allocates fewer tables than object_creation, so the shape pointer removal contributes less."

max_files: 3
max_source_loc: 80
max_commits: 2

assumptions:
  - id: A1
    claim: "Table.shape *Shape field is write-only in production — no hot path reads it. The JIT uses shapeID (uint32) exclusively via TableOffShapeID offset."
    type: derivable-from-code
    evidence: "grep '.shape' internal/ shows only table.go:380 (write in setShape) and shape_new_test.go (test reads). JIT accesses TableOffShapeID=140 not TableOffShape=144. TableOffShape constant is stale — overlaps TableOffIntArray."
    confidence: HIGH
    source: "grep of entire internal/ directory for '.shape' usage; internal/jit/value_layout.go:58-60 offset constants"
  - id: A2
    claim: "Removing Table.shape does not shift any JIT-visible struct offset because shape is the last field in the struct (after boolArray at offset 192+24=216)."
    type: derivable-from-code
    evidence: "internal/runtime/table.go:28-51 struct definition; internal/jit/value_layout.go:42-65 offset constants; init() verifier at value_layout.go:131-155 checks arrayKind/intArray/floatArray/boolArray/keysDirty/shapeID — all precede shape."
    confidence: HIGH
    source: "Direct reading of Table struct layout and JIT offset verification in value_layout.go init()"
  - id: A3
    claim: "ScanGCRoots currently scans len(vm.regs) which is up to 2x the high-water-mark due to EnsureRegs doubling. Bounding to high-water-mark reduces scan range."
    type: derivable-from-code
    evidence: "internal/vm/vm.go:253 scans 0..len(vm.regs); vm.go:130-137 EnsureRegs allocates needed*2 capacity."
    confidence: HIGH
    source: "Direct reading of vm.go ScanGCRoots and EnsureRegs functions"
  - id: A4
    claim: "The object_creation regression is 100% GC-driven — IR instruction counts are unchanged (0% drift on all 3 hot functions vs R35 fixture baselines)."
    type: cited-evidence
    evidence: "opt/authoritative-context.json#candidates[object_creation].observations[3]: 'IR quality and instruction counts match R35 baselines exactly (1181/1572/208 = 0% drift)'"
    confidence: HIGH
    source: "CONTEXT_GATHER production pipeline Diagnose() output"
  - id: A5
    claim: "Commit 39b5ef3 is the sole culprit for both object_creation +49.35% and sort +21.43% regressions."
    type: cited-evidence
    evidence: "opt/authoritative-context.json#bisect_candidates[0]; opt/knowledge/r35-object-creation-regression.md bisect output: 39b5ef3 median=1.084 (bad), 598bc1e median=0.745 (good)"
    confidence: HIGH
    source: "R35 git bisect run converged on 39b5ef3"
  - id: A6
    claim: "setShape can be refactored to compute shapeID via GetShapeID(skeys) without storing the *Shape pointer, preserving all cache validation behavior."
    type: derivable-from-code
    evidence: "internal/runtime/table.go:378-386 setShape calls GetShape(skeys), stores s, reads s.ID. GetShapeID(skeys) returns the same ID via GetShape().ID (internal/runtime/shape.go). Callers only need t.shapeID to be correct."
    confidence: HIGH
    source: "Reading setShape, GetShape, GetShapeID function implementations"

prior_art:
  - system: "R35 diagnostic round"
    reference: "opt/knowledge/r35-object-creation-regression.md"
    applicability: "Direct root cause analysis. Proposed fixes 1 and 2 are the basis for this plan."
    citation: "opt/knowledge/r35-object-creation-regression.md lines 50-56"
  - system: "V8 Hidden Classes"
    reference: "V8 uses Map objects (hidden classes) as GC-visible pointers on every JSObject. V8 mitigates GC cost via generational GC + write barriers. GScript uses Go's conservative GC which scans all pointers equally."
    applicability: "V8's approach confirms that storing a shape pointer is standard for field IC. But GScript's Go GC makes the per-object pointer cost higher than in V8's managed heap."
    citation: "V8 design documentation (general knowledge, not a specific file — confidence: MEDIUM)"

failure_signals:
  - condition: "object_creation delta < -15% vs current (i.e., still above 0.970s)"
    action: "investigate secondary GC overhead — sync.Map pressure in shape registry, gcCompact interval tuning"
  - condition: "any non-excluded benchmark regresses > 3% vs reference"
    action: "revert, root-cause the cross-contamination"
  - condition: "TestDeepRecursionRegression or any correctness test fails"
    action: "hard revert (correctness gate)"
  - condition: "offset_check_test.go panics at init"
    action: "struct layout shifted — fix offsets in value_layout.go"

self_assessment:
  uses_profileTier2Func: false
  uses_hand_constructed_ir_in_tests: false
  authoritative_context_consumed: true
  all_predictions_have_confidence: true
  all_claims_cite_sources: true

---

# Optimization Plan: Remove GC overhead from Shape system regression (R36)

## Overview

Commit 39b5ef3 introduced a +49% regression on object_creation and +21% on sort by adding two sources of GC overhead: (1) a write-only `shape *Shape` pointer on every Table struct that Go's GC must trace, and (2) uncapping ScanGCRoots to scan the full 2x-capacity register file instead of the active window. Both were correctness fixes (preventing SIGSEGV from GC missing JIT self-call registers). This plan surgically removes the overhead while preserving correctness: remove the dead pointer, bound the scan with a high-water-mark tracker. [A4, A5]

## Root Cause Analysis

The regression is 100% GC-driven, not codegen-driven [A4]. IR instruction counts for all three hot functions (`create_and_sum`=1181, `transform_chain`=1572, `new_vec3`=208) are bit-identical to the R35 fixture baseline. The two overhead sources compound:

1. **Dead `shape *Shape` pointer** [A1]: Every Table carries a `*Shape` GC pointer that is written by `setShape()` (table.go:380) but never read by any production code path. The JIT field cache validates against `shapeID` (uint32) via `TableOffShapeID`=140, not the pointer. With ~800K table allocations in object_creation, this adds ~800K traced GC pointers per benchmark run.

2. **Uncapped ScanGCRoots** [A3]: `vm.go:253` scans `vm.regs[:len(vm.regs)]`. `EnsureRegs` (vm.go:130) allocates `needed*2` capacity, so the scanned range can be 2x larger than necessary. With GC compaction triggered every 1M allocations, the excess scanning adds measurable overhead on allocation-heavy benchmarks.

## Approach

### Fix 1: Remove `Table.shape *Shape` field

- **Delete** the `shape *Shape` field from `Table` struct (table.go:50)
- **Refactor** `setShape()` to compute `shapeID` directly: `t.shapeID = GetShapeID(skeys)` for non-nil skeys, `t.shapeID = 0` for nil/empty
- **Delete** the stale `TableOffShape = 144` constant from `value_layout.go:59` (it overlaps `TableOffIntArray` — clearly wrong)
- **Update** `shape_new_test.go` to assert via `t.ShapeID()` accessor instead of `t.shape` pointer

This is safe because [A1] confirms no production code reads `Table.shape`, and [A2] confirms removing the last field doesn't shift any JIT-visible offset.

### Fix 2: Bounded ScanGCRoots via high-water-mark

- **Add** `regHighWater int` field to VM struct (vm.go, after `regs`)
- **Update** `EnsureRegs()` to set `vm.regHighWater = max(vm.regHighWater, needed)`
- **Update** `ScanGCRoots()` to scan `vm.regs[:vm.regHighWater]` instead of `vm.regs[:len(vm.regs)]`

Correctness argument: `EnsureRegs` is called before any register write (including JIT self-call register window advancement). The high-water-mark captures the maximum register index ever written. Scanning up to this mark includes all JIT self-call registers (the original correctness concern) while excluding the unused 2x-capacity tail.

## Task Breakdown

### Task 0 (pre-flight, orchestrator) — none needed
No infrastructure issues to fix. The offset_check init verifier covers arrayKind/intArray/floatArray/boolArray/keysDirty/shapeID — all unaffected by this change.

### Task 1 — Remove dead shape pointer + bound ScanGCRoots

**Files** (3 total):
- `internal/runtime/table.go` — remove `shape *Shape` field (line 50), refactor `setShape()` (lines 375-386)
- `internal/vm/vm.go` — add `regHighWater int` field, update `EnsureRegs()` (lines 130-137), update `ScanGCRoots()` (line 253)
- `internal/jit/value_layout.go` — delete stale `TableOffShape = 144` constant (line 59)

**Test updates** (excluded from source LOC cap):
- `internal/runtime/shape_new_test.go` — replace `t.shape` reads with `t.ShapeID()` + `LookupShapeByID()` assertions

**Algorithm** (pseudocode):
```
# table.go:setShape refactor
func (t *Table) setShape(skeys []string):
    if len(skeys) == 0:
        t.shapeID = 0
        return
    t.shapeID = GetShapeID(skeys)

# vm.go:EnsureRegs update
func (vm *VM) EnsureRegs(needed int):
    if needed > vm.regHighWater:
        vm.regHighWater = needed
    if needed > len(vm.regs):
        newRegs = MakeNilSlice(needed * 2)
        copy(newRegs, vm.regs)
        vm.regs = newRegs
    return vm.regs

# vm.go:ScanGCRoots update
- for i := 0; i < len(vm.regs); i++
+ for i := 0; i < vm.regHighWater; i++
```

**What NOT to touch**:
- Do NOT remove `GetShape()` or the Shape type — they are used by `GetShapeID()` and `LookupShapeByID()`
- Do NOT change `shapeID` computation logic — only remove the pointer storage
- Do NOT modify any JIT codegen (emit_*.go, tier1_*.go) — this is runtime-only
- Do NOT change `gcCompactInterval` or GC tuning constants
- Do NOT modify the Transition/FieldMap infrastructure in shape.go

**Test plan**:
```bash
# Unit tests for shape system
go test ./internal/runtime/ -run TestShape -v
# Full methodjit package (catches offset panics at init + all JIT tests)
go test ./internal/methodjit/ -v -count=1
# Integration: run the target benchmark
go build -o /tmp/gscript_r36 ./cmd/gscript/
timeout 60s /tmp/gscript_r36 -jit benchmarks/suite/object_creation.gs
timeout 60s /tmp/gscript_r36 -jit benchmarks/suite/sort.gs
```

**Scope**: ≤3 files, ≤80 source LOC (refactoring setShape ~10 lines, adding regHighWater ~8 lines, ScanGCRoots ~2 lines, deleting shape field ~1 line, deleting stale constant ~1 line). Test file changes excluded per R22 rule.

## Integration Test

```bash
go build -o /tmp/gscript_r36 ./cmd/gscript/
timeout 60s /tmp/gscript_r36 -jit benchmarks/suite/object_creation.gs
timeout 60s /tmp/gscript_r36 -jit benchmarks/suite/sort.gs
```

## Pre-plan checklist
- [x] No broken tooling found by Diagnose() or arch_check — no fix Task needed
- [x] No files >800 lines touched by this plan (table.go ~580 lines, vm.go ~280 lines, value_layout.go ~165 lines)
- [x] known-issues.md object_creation entry (line 618-622) is in this plan's category — this round addresses it
- [x] Plan does NOT add or edit pass_*.go — no production-pipeline diagnostic test required

## Results (filled after VERIFY)
| Benchmark | Reference | Before | After | Change | Expected | Met? |
|-----------|-----------|--------|-------|--------|----------|------|

## Lessons (filled after completion/abandonment)

## Plan Check Feedback (populated by plan_check on rewrite cycles only)
