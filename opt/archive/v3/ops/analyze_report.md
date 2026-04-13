# Analyze Report — R36

## Architecture Audit

**Full audit** (rounds_since_arch_audit = 2).

`arch_check.sh` results:
- **4 files at split threshold**: emit_dispatch.go (971 ⚠), graph_builder.go (955 ⚠), tier1_arith.go (903 ⚠), tier1_table.go (829 ⚠). None touched this round.
- **Test ratio**: 95% (18,872 test lines / 19,954 source lines) — up from 88% at last full audit (R28). 26 source files still lack test files.
- **Technical debt markers**: 2 (unchanged from R28).
- **Pipeline order**: unchanged since R28 — BuildGraph→Validate→TypeSpec→Intrinsic→TypeSpec→Inline→TypeSpec→ConstProp→LoadElim→DCE→RangeAnalysis→LICM→Validate→RegAlloc→Emit.
- **New since R28**: offset_check_test.go guards Table struct offsets at init. shape_new_test.go verifies Shape system. R35 added object_creation_dump_test.go (insn-count fixture).
- **Stale constant found**: `TableOffShape = 144` in value_layout.go overlaps `TableOffIntArray = 144`. Never used by JIT code. Will be removed in this round.

No new constraints discovered. `constraints.md` current.

## Gap Classification

| Category | Benchmarks | Total Gap vs Reference | Blocked? |
|----------|------------|----------------------|----------|
| allocation_heavy | object_creation (+49.35%), sort (+21.43%) | +0.387s wall-time | No (failures=1) |
| tier2_float_loop | (none drifting) | — | Yes (failures=2) |
| recursive_call | fib, ackermann, mutual_recursion | excluded from R7 (598bc1e-dominated) | Reset (failures=0) |
| field_access | (none drifting) | — | Reset (failures=0) |
| tier1_dispatch | (none drifting) | — | Reset (failures=0) |

Noise-level drifts (under 5%): closure_bench +3.70% (live run = reference 0.027s, noise in latest.json), table_array_access +3.19%, fannkuch +2.08%.

## Blocked Categories

- `tier2_float_loop`: category_failures=2. Blocked for at least 1 more round. R32+R33 both no_change/data-premise-error on LoopScalarPromotionPass.

## Active Initiatives

- `tier1-call-overhead.md`: Item 8 (fib regression from 598bc1e) still open. R29/R30 diagnostic+failed attempt. Not targeted this round — fib is excluded from R7.
- `tier2-float-loops.md`: Blocked by ceiling rule.
- `recursive-tier2-unlock.md`: Dormant.

## Initiative Retrospective

No active initiative is targeted this round. This is a standalone allocation_heavy round driven by R7 drift targeting rule.

## Selected Target

- **Category**: allocation_heavy
- **Initiative**: standalone
- **Reason**: R7 FAIL mandates targeting object_creation (+49.35%) or sort (+21.43%). Root cause identified by R35 bisect (commit 39b5ef3). Forward fix is surgical runtime surgery — high ROI with bounded risk.
- **Benchmarks**: object_creation (primary, -29.9% predicted), sort (secondary, -13.7% predicted)

## Architectural Insight

The regression is a **GC overhead design flaw**, not a codegen issue. When the Shape system was introduced (39b5ef3), it stored a `*Shape` GC pointer on every Table for object-model completeness. But the current implementation uses `shapeID` (uint32) for ALL hot-path operations — field IC validation, cache coherence. The `*Shape` pointer is dead weight: written by `setShape()`, never read by any production code path. This is analogous to V8 storing a `Map*` on every JSObject — but V8 has generational GC with write barriers that amortize the per-pointer cost. GScript uses Go's conservative stop-the-world GC, making each extra pointer proportionally more expensive.

The ScanGCRoots issue is a correctness-driven conservatism: scanning the full register file was the safe fix for JIT self-call registers. A high-water-mark tracker restores bounded scanning while preserving the correctness property that all JIT-touched registers are scanned.

Both fixes are cross-benchmark: any allocation-heavy workload benefits.

## Prior Art Research

### Knowledge Base

`opt/knowledge/r35-object-creation-regression.md` is the authoritative source. Covers:
- Bisect evidence chain (39b5ef3 identified as sole culprit)
- Diff analysis of both changes (shape pointer + ScanGCRoots)
- Proposed forward fixes (remove dead pointer + high-water-mark scan)
- Risk notes for sort and closure_bench

No web search needed — this is runtime surgery based on code-reading evidence, not a JIT optimization technique.

### Reference Source Findings

Not applicable. No external compiler source needed for this fix.

### Knowledge Base Update

No new knowledge doc needed — R35 doc is comprehensive and directly informs this round.

## Source Code Findings

### Files Read

1. **internal/runtime/table.go** (580 lines): `Table.shape *Shape` at line 50 (last field). `setShape()` at lines 375-386 writes both `t.shape` and `t.shapeID`. Called from 6 sites in `RawSetString`/`RawSetStringCached`. Production code never reads `t.shape` — only `t.shapeID`.

2. **internal/vm/vm.go** (280 lines): `ScanGCRoots()` at line 253 scans `0..len(vm.regs)`. `EnsureRegs()` at line 130 allocates `needed*2`, so scanned range can be 2x the high-water-mark. No `regHighWater` field exists yet.

3. **internal/jit/value_layout.go** (165 lines): `TableOffShape=144` at line 59 is stale — overlaps `TableOffIntArray=144`. Never used by JIT code (grep confirms single definition, no references). Init verifier at lines 131-155 checks arrayKind/intArray/floatArray/boolArray/keysDirty/shapeID — not shape.

### Diagnostic Data

From `opt/authoritative-context.json` (CONTEXT_GATHER production pipeline):
- object_creation IR: 0% instruction drift (1181/1572/208 = R35 baselines)
- 48-62% memory operations across all three hot functions
- Live run: 1.151s (consistent with latest.json 1.141s)
- sort: 1602 total insns, 42.6% memory. Live run: 0.050s (consistent)

### Actual Bottleneck (data-backed)

**GC overhead from two compounding runtime changes** [HIGH confidence — R35 bisect + code reading]:
1. ~800K tables × 1 dead GC pointer each = 800K extra pointers for Go GC to trace
2. ScanGCRoots scanning 2x-capacity register file = 50-100% excess scan per GC cycle
3. gcCompact triggers every 1M allocations — object_creation exceeds 2.4M allocations

## Plan Summary

Remove the dead `Table.shape *Shape` pointer (eliminates ~800K GC pointers per object_creation run) and bound ScanGCRoots to a high-water-mark tracker (halves the register scan range). Both changes are surgical runtime modifications — no JIT codegen changes. Expected: object_creation -29.9% (1.141→0.800), sort -13.7% (0.051→0.044). Key risk: GC overhead may be non-linear — removing one pointer may not recover a proportional fraction of the regression.
