# Optimization Plan: Native ArrayBool/ArrayFloat Table Access

> Created: 2026-04-06
> Status: active
> Cycle ID: 2026-04-06-native-array-kinds
> Category: field_access
> Initiative: standalone

## Target
What benchmark(s) are we trying to improve, and by how much?

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| sieve | 0.239s (3 reps) | 0.011s | 21.7x | 0.10–0.15s |
| table_array_access | 0.133s | N/A | — | may improve |
| fannkuch | 0.069s | 0.020s | 3.5x | may improve if uses typed arrays |

Primary target: **sieve**. Secondary: any benchmark using bool/float typed arrays.

## Root Cause
The Go runtime promotes tables to typed backing stores (ArrayBool=3 for boolean values, ArrayFloat=2 for float values). The Tier 2 emitter's `emitGetTableNative` and `emitSetTableNative` functions ONLY handle ArrayMixed(0) and ArrayInt(1). ArrayBool and ArrayFloat both fail the dispatch check and fall through to exit-resume:

```asm
LDRB X2, [X0, #TableOffArrayKind]
CMPimm X2, #1        // AKInt
B.EQ intArrayLabel
CBNZ X2, deoptLabel  // ArrayBool(3), ArrayFloat(2) → EXIT-RESUME
```

For sieve, the `is_prime` table stores only booleans → ArrayBool(3). **Every single GETTABLE and SETTABLE exits to Go**. Approximately 6–7 million exit-resume round-trips per sieve(1M) call. Each round-trip costs: register spill → return to Go → `executeTableExit` → `RawGet`/`RawSet` → resume lookup → jump back to JIT.

This is why sieve is only 1.05x vs VM — the JIT runs all table ops through Go with added transition overhead.

## Prior Art (MANDATORY)

**V8:** JSArrays have "element kinds" (PACKED_SMI, PACKED_DOUBLE, HOLEY_ELEMENTS, etc.). TurboFan specializes load/store codegen per element kind. `CheckMaps` guards the kind; downstream code uses unboxed representations. GScript's ArrayKind is directly analogous.

**LuaJIT:** Lua tables don't have typed backing stores (array part is always `TValue[]`). LuaJIT's advantage comes from SCEV-based bounds hoisting + type guard hoisting + copy-substitution loop opt, not from array specialization. However, LuaJIT's C-based table runtime is inherently faster than GScript's Go-based one.

**SpiderMonkey (if relevant):** Warp/IonMonkey has typed array specialization for TypedArrays (Int32Array, Float64Array), not for generic arrays.

Our constraints vs theirs:
- We already have the typed backing stores in the Go runtime — the JIT just doesn't know about them
- V8's element kinds are more granular (PACKED vs HOLEY) — we don't need that complexity
- Unlike LuaJIT, we CAN benefit from specialized loads (1 byte for bool vs 8 bytes for Value)

## Approach
Concrete implementation plan:

1. **Add missing offset constants** in `internal/jit/value_layout.go`:
   - `TableOffFloatArrayLen = 176` (168 + 8)
   - `TableOffBoolArrayLen = 200` (192 + 8)

2. **Extend arrayKind dispatch** in `emitGetTableNative` and `emitSetTableNative` (`emit_table.go`):
   - Current: Mixed(0) fall-through, Int(1) branch, else deopt
   - New: Mixed(0) fall-through, Int(1) branch, Float(2) branch, Bool(3) branch, else deopt

3. **ArrayBool fast path** (per `opt/knowledge/array-kind-table-access.md`):
   - GetTable: bounds check → LDRB from boolArray → convert byte to NaN-boxed (0→nil, 1→false, 2→true)
   - SetTable: check value is bool/nil → convert to byte → bounds check → STRB to boolArray

4. **ArrayFloat fast path**:
   - GetTable: bounds check → LDR (8 bytes) from floatArray → raw float64 bits = NaN-boxed Value (no conversion needed!)
   - SetTable: check value is float → bounds check → STR (8 bytes) to floatArray

## Expected Effect
Quantified predictions for specific benchmarks.

**sieve**: 0.239s → 0.10–0.15s (30–50% improvement)

**Prediction calibration (MANDATORY):** This is NOT an instruction-count-based estimate. The optimization eliminates Go function call overhead (exit-resume round-trips), not ARM64 instructions. Each exit-resume costs ~10–50ns; sieve has ~6–7M exits per call × 2 Tier 2 calls = ~12–14M exits. At 15ns average per exit, that's ~180–210ms of exit-resume overhead out of 239ms total. Eliminating this overhead is binary (either exits or doesn't), so confidence is higher than instruction-count estimates. The residual ~30–60ms is actual compute (ARM64 fast path for type checks + bounds checks + loop overhead).

Previous round overestimation pattern (halving for superscalar) does NOT apply here — exit-resume overhead is dominated by Go function call boundary latency, not pipeline effects.

## Failure Signals
What would tell us this approach is wrong? Be specific:
- Signal 1: sieve's `is_prime` table is NOT ArrayBool at runtime (table demotes to Mixed due to nil writes) → action: run diagnostic test to verify arrayKind at runtime, check if `is_prime[j] = false` demotes from ArrayBool
- Signal 2: sieve improvement < 10% after fix → action: profile to determine where time is spent (table growth in initialization loop? loop overhead? type checks?)
- Signal 3: Any Tier 2 correctness regression (bool value corruption) → action: revert, debug byte encoding

## Task Breakdown
Each task = one Coder sub-agent invocation.

- [x] 1. **TDD + offset constants** — Write failing tests for ArrayBool and ArrayFloat GetTable/SetTable at Tier 2. Add `TableOffFloatArrayLen` and `TableOffBoolArrayLen` to `value_layout.go`. — file(s): `emit_table_typed_test.go` (new), `value_layout.go` — test: `TestTier2_GetTableArrayBool`, `TestTier2_SetTableArrayBool`, `TestTier2_GetTableArrayFloat`, `TestTier2_SetTableArrayFloat`
- [x] 2. **ArrayBool fast path** — Add ArrayBool read/write paths to `emitGetTableNative` and `emitSetTableNative`. Extend arrayKind dispatch chain. — file(s): `emit_table.go` — test: tests from Task 1
- [ ] 3. **ArrayFloat fast path** — Add ArrayFloat read/write paths to `emitGetTableNative` and `emitSetTableNative`. — file(s): `emit_table.go` — test: tests from Task 1
- [ ] 4. **Integration test + benchmark** — Run full test suite + sieve benchmark. Compare against latest.json baseline. — test: `go test ./internal/methodjit/ -timeout 120s` + manual sieve benchmark

## Budget
- Max commits: 4 functional (+1 revert slot)
- Max files changed: 4 (emit_table.go, value_layout.go, new test file, potentially emit_dispatch.go)
- Abort condition: if sieve's `is_prime` table is ArrayMixed at runtime (not ArrayBool), the entire plan is invalid → diagnostic only

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|

## Lessons (filled after completion/abandonment)
What worked, what didn't, what to remember for next time.
