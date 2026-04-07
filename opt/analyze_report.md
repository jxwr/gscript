# Analyze Report — Round 21

> Date: 2026-04-07
> Cycle ID: 2026-04-07-nbody-typing-diagnostic

## Architecture Audit

**Full audit** (rounds_since_arch_audit=2). Key findings:

- **emit_table.go split RESOLVED** (Round 19): Now emit_table_field.go (341) + emit_table_array.go (692). ✅
- **emit_dispatch.go 971 lines** ⚠ CRITICAL (grew 2 from R19). No changes planned this round.
- **graph_builder.go 955 lines** ⚠ CRITICAL (grew 16 from R19). Minor read-only changes for diagnostic.
- **tier1_table.go 829 lines** ⚠ NEW threshold crossing. No changes planned.
- **Self-call optimization landed** (Round 20, commits db2431f+e39cac0+b094383): Tier 1 proto comparison + BL direct for self-calls. 32-byte frame vs 64-byte. CallCount increment restored.
- **Native GetGlobal in Tier 2** (Round 20, commit 6bb9209): Inline value cache with generation-based invalidation.
- **LICM GetGlobal hoisting** (Round 20, commit 7cb0a54): GetGlobal added to canHoistOp whitelist.
- **Ackermann +137% regression** (Round 20): Self-call proto comparison adds ~13 insns to every Tier 1 call site. For ackermann's 67M calls: unacceptable overhead.
- **pass_load_elim.go: 94 lines** — S2L forwarding already landed. Room for growth.
- **27 source files lack test files** (up from 24 at R19).

Updated: `docs-internal/architecture/constraints.md` — Tier 1 Self-Call section, file sizes, table access dedup status, test coverage.

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (4.7x), ackermann (87x), mutual_recursion (68x) | 0.978s | **BLOCKED** (failures=2) |
| tier2_float_loop | nbody (7.5x), spectral (5.3x), matmul (5.2x), mandelbrot (0.96x!) | 0.392s | No (failures=0) |
| field_access | sieve (6.5x), sort (3.8x), fannkuch (2.3x) | 0.139s | No (failures=1) |
| gofunction_overhead | method_dispatch (regression) | 0.119s | No (failures=0) |
| allocation_heavy | binary_trees, object_creation (regressions) | N/A | No (failures=0) |

**mandelbrot now BEATS LuaJIT** (0.064s vs 0.067s). First benchmark at parity.

## Blocked Categories
- `recursive_call` (category_failures=2): Tier 2 net-negative for recursive functions.

## Active Initiatives
- `opt/initiatives/tier2-float-loops.md` — active (R20 improved: nbody -49%, fib -90%)
- `opt/initiatives/recursive-tier2-unlock.md` — paused (blocked)

## Selected Target

- **Category**: tier2_float_loop
- **Initiative**: opt/initiatives/tier2-float-loops.md
- **Reason**: (1) 0 failures, safe. (2) nbody at 7.5x has largest absolute gap (0.246s). (3) Round 20 cut nbody from 0.555s to 0.284s — momentum. (4) Diagnostic found critical typing gap.
- **Benchmarks**: nbody (primary), matmul/spectral_norm (secondary)

## Architectural Insight

The key question is whether the feedback pipeline delivers typed GetField results to nbody's Tier 2 compilation. A diagnostic test (without Tier 1 feedback) showed 29/31 arithmetic ops as generic — but in production, Tier 1 collects feedback first. The graph builder code for inserting GuardType after GetField exists (`graph_builder.go:669-676`). Whether it actually fires for nbody in production determines whether the bottleneck is untyped arithmetic (huge fix: -30-50%) or field access overhead (medium fix: -10-15%).

## Prior Art Research

### Web Search Findings
- V8 field-sensitive alias analysis in loops: `ComputeLoopState` kills only specific (object, field). GScript already matches.
- ARM64 M-series: L1D 3-cycle hit, FSQRT 13-cycle latency, 2 loads/cycle throughput.

### Reference Source Findings
- V8 `load-elimination.cc:1363`: field-sensitive kill in loop body scan
- V8 `load-elimination.cc:786`: CheckMaps elimination propagation

### Knowledge Base Update
Created `opt/knowledge/nbody-field-hoisting.md` (152 lines).

## Source Code Findings

### Files Read
- `pass_licm.go:174-248`: Per-field alias check. hasLoopCall blocks all GetField hoisting.
- `pass_load_elim.go` (94 lines): Block-local CSE + S2L forwarding. Complete.
- `graph_builder.go:655-677`: GetField feedback → GuardType insertion. Code present and correct.
- `pass_intrinsic.go`: math.sqrt → OpSqrt. Working for nbody.
- `tier1_call.go:120-200`: Self-call detection. Proto comparison on every call.

### Diagnostic Data

**nbody advance() through full Tier 2 pipeline (no Tier 1 feedback):**

```
INTRINSIC: math.sqrt → OpSqrt ✅
LICM: hasCall=false for j-loop ✅
LICM: 4 GetField hoisted (bi.x, bi.y, bi.z, bi.mass) ✅
TYPE: 4 typed / 44 untyped (inner loop) ⚠️
ARITH: 2 specialized / 29 generic ⚠️
CODE: 620 ARM64 insns (Tier 2), 10 spills
```

CAVEAT: No Tier 1 feedback in this compilation. Production codegen may be typed.

### Actual Bottleneck

**Scenario A (feedback broken):** 29 generic arith × ~10 insns overhead = ~290 insns/iter wasted. ~230ms of nbody's 0.284s.
**Scenario B (feedback working):** Field access overhead: 10 GetField + 6 SetField + 1 GetTable × ~5-15 insns each = ~150-250 insns/iter. Shape checks dominate.

## Plan Summary

Diagnostic-first round. Task 1: production-accurate diagnostic (TieringManager path). Task 2: fix confirmed bottleneck. Task 3 (bonus): ackermann self-call regression fix. Conservative: verify before optimizing.
