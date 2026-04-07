# Analyze Report — Round 22

> Date: 2026-04-07
> Cycle ID: 2026-04-07-float-param-guard

## Architecture Audit

Quick read (rounds_since_arch_audit = 1). No new issues beyond existing flags.

arch_check.sh flags:
- ⚠ emit_dispatch.go: 971 lines (29 from limit)
- ⚠ graph_builder.go: 955 lines (45 from limit)
- ⚠ tier1_table.go: 829 lines (29 over 800 threshold)
- 27 source files lack test files (unchanged from R21)

**This round's plan does NOT touch any flagged files.** All changes in pass_typespec.go
(402 lines), pass_load_elim.go (95 lines), pass_licm.go (562 lines).

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| tier2_float_loop | nbody (0.227s), matmul (0.098s), spectral (0.039s), fannkuch (0.026s), mandelbrot (0.006s), sum_primes (0.002s) | 0.398s | No (failures=0) |
| field_access | sieve (0.079s), sort (0.030s) | 0.109s | No (failures=1) |
| gofunction_overhead | method_dispatch (0.100s) | 0.100s | No |
| recursive_call | fib (0.116s), ackermann (0.589s), mutual_recursion (0.232s) | 0.937s | **BLOCKED (ceiling=2)** |
| allocation_heavy | binary_trees, object_creation | N/A (no LuaJIT data) | No |

## Blocked Categories
- `recursive_call` (ceiling=2): needs native recursive BLR or Tier 1 specialization

## Active Initiatives
- `tier2-float-loops.md` (paused): remaining phases 6/9/10 + this round adds Phase 12
- `recursive-tier2-unlock.md` (paused, BLOCKED): waiting for net-positive Tier 2 recursion

## Selected Target

- **Category**: tier2_float_loop
- **Initiative**: opt/initiatives/tier2-float-loops.md (Phase 12: Parameter Type Specialization)
- **Reason**: Largest non-blocked gap (0.398s total). Diagnostic revealed concrete, fixable
  root cause: 7 generic ops in nbody from untyped `dt` parameter. Additionally, GuardType CSE
  and LICM whitelist gaps identified as secondary wins.
- **Benchmarks**: nbody (primary), matmul/spectral/broad (secondary)

## Architectural Insight

The remaining nbody gap (7.7x vs LuaJIT) is dominated by **per-access validation overhead**.
Every GetField/GetTable in the inner j-loop re-validates table type, shape, and kind — all
loop-invariant properties. The Method JIT's block-local validation (shapeVerified, tableVerified)
helps within a block but is cleared at loop back-edges.

However, the IMMEDIATE bottleneck is simpler: function parameters have type `:any` because
there's no parameter type feedback. TypeSpecialize Phase 0 only detects int-like params
(used with ConstInt). Float-like params (used with float operands) are not detected. This
blocks specialization for ALL arithmetic involving parameters — a design gap, not a per-benchmark
hack.

The architectural fix (cross-block shape propagation, IR-level guard splitting) remains the
long-term path to closing the 7.7x gap. This round addresses the typing pipeline gap first.

## Prior Art Research

### Web Search Findings
Not needed — knowledge base already has comprehensive research on parameter typing from V8
(CheckFloat64, SpeculativeNumberOps), LuaJIT (recording-time type capture), and SpiderMonkey
(CacheIR → GuardTo(Double)).

### Reference Source Findings
V8 TurboFan: `JSCallReducer` inserts type checks for parameters based on call-site feedback.
GScript lacks call-site feedback but can use use-site inference as workaround.

### Knowledge Base Update
No new knowledge files needed. Existing `feedback-typed-loads.md` and
`cross-block-load-elim.md` already cover the relevant techniques.

## Source Code Findings

### Files Read
1. **pass_typespec.go** (402 lines): Phase 0 `insertParamGuards` only detects int-like params
   (ConstInt pairing). Float-like params ignored. Phase 1 correctly infers types from GuardType
   and propagates through phis. Phase 2 `specialize()` requires both operands typed — blocks
   specialization for Mul(any, float) and Add(float, any).

2. **pass_load_elim.go** (95 lines): Handles GetField CSE and SetField invalidation. S2L
   forwarding already implemented (records stored value after SetField). Does NOT track GuardType
   — redundant guards on same value pass through untouched.

3. **pass_licm.go** (562 lines): `canHoistOp` whitelist missing OpSqrt and OpGetTable. GetField
   hoisting works correctly with alias checking (setFields, hasLoopCall). GetTable could use
   identical alias pattern.

4. **graph_builder.go** (lines 620-676): Inserts GuardType after BOTH GetTable AND GetField when
   feedback is monomorphic. This means matmul's production codegen likely HAS typed arithmetic —
   the static diagnostic was misleading.

### Diagnostic Data

Production diagnostic (TestDiag_NbodyProduction):
- **33 typed arithmetic ops** (confirmed: feedback pipeline works end-to-end)
- **7 generic arithmetic ops** (all involving `v0 = LoadSlot slot[0]` — the `dt` parameter)
  - 1 × Div (inner j-loop) — `mag = dt / (dsq * distance)`
  - 3 × Mul (position update) — `dt * b.vx`, `dt * b.vy`, `dt * b.vz`
  - 3 × Add (position update) — `b.x + result`, `b.y + result`, `b.z + result`
- **24 GuardType nodes** — every GetField result type-checked
- **Redundant GuardType**: bj.mass (v48) guarded 3 times for same TypeFloat
- **LICM hoisting confirmed**: bi.x/y/z/mass hoisted to j-loop preheader (4 instructions)

### Actual Bottleneck (data-backed)

**Primary**: `dt` parameter as `:any` blocks 7/40 arithmetic ops from specializing. The
TypeSpecialize pass has no mechanism to detect float-like parameters.

**Secondary**: Redundant GuardType instructions waste ~30M instructions per benchmark run.

**Tertiary**: OpSqrt not LICM-hoistable (no current benchmark impact but general gap).

**Structural (not addressable this round)**: Per-GetField/GetTable validation overhead accounts
for ~85% of inner loop time. Closing the 7.7x gap requires cross-block shape propagation or
IR-level guard splitting (future rounds).

## Plan Summary

Extend TypeSpecialize's parameter guard mechanism to detect and guard float-like parameters
(those used in arithmetic with float-typed operands). This eliminates all 7 generic ops in
nbody's advance(). Also: GuardType CSE in LoadElim, and OpSqrt + OpGetTable in LICM whitelist.
Expected nbody improvement: −2-6% (conservative 2-3%, optimistic 4-6% from branch elimination
benefits on M4). All changes in IR passes — no emitter changes, no file size risk.
