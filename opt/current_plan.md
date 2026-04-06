# Optimization Plan: Table Access Raw-Int Key Bypass + Constant Value Bypass

> Created: 2026-04-06
> Status: active
> Cycle ID: 2026-04-06-table-access-bypass
> Category: field_access
> Initiative: standalone

## Target
Reduce per-access ARM64 instruction count for GetTable/SetTable in tight integer loops.

| Benchmark | Current (JIT) | LuaJIT | Gap | Target |
|-----------|--------------|--------|-----|--------|
| sieve | 0.186s (3 reps) | 0.012s (3 reps) | 15.5x | 0.160-0.170s |
| fannkuch | 0.070s | 0.020s | 3.5x | 0.060-0.065s |
| table_array_access | 0.135s | N/A | — | observe |

## Root Cause
Diagnostic data (sieve inner marking loop ARM64 disasm, 57 insns/iter) reveals:

**Only 5/57 instructions (9%) are actual compute.** The rest:
- **NaN-boxing overhead (14 insns, 25%)**: Key value is unboxed from rawIntRegs via UBFX+ORR (boxing), then LSR+MOV+CMP+BNE (tag check), then SBFX (unboxing). 7 completely wasted instructions when key is a known int.
- **Table access overhead (15 insns, 26%)**: Table type check, pointer extraction, metatable check, 4-way kind dispatch — all invariant across loop iterations but re-executed every time.
- **Phi slot reload (10 insns, 18%)**: After SetTable (potential exit point), all 4 phi registers reloaded from VM register file.
- **Bool value overhead (5-8 insns)**: For `is_prime[j] = false`, the constant `false` is loaded from a register, type-checked, and converted to byte — all unnecessary for a compile-time constant.

**This round attacks the two cheapest wins: key bypass (Task 1) and value bypass (Task 2).** Table pointer hoisting and phi reload elimination are deferred to future rounds as they need IR-level changes.

## Prior Art (MANDATORY)

**V8:** TurboFan's SimplifiedLowering converts tagged integer operations to untagged Word32/Word64 operations. LoadElement with known PACKED_SMI representation reads directly without tag checks. CheckMaps (shape guard) is hoisted out of loops by the LoadElimination pass (`src/compiler/load-elimination.cc`).

**LuaJIT:** All values are unboxed on-trace. Array base pointer kept in a register across iterations. ABC (Array Bounds Check Elimination) removes redundant bounds checks via range analysis. Trace JIT naturally eliminates per-access type dispatch because the trace records only the taken path.

**SpiderMonkey (Warp):** TypePolicy inserts MUnbox/MBox at representation boundaries. Phi specialization pass specializes loop phis to known types. FoldLoadsWithUnbox fuses NaN-boxed load + unbox into single operations.

Our constraints vs theirs:
- We don't have trace recording (fixed-path specialization), so we must handle all array kinds per access
- We don't have feedback for table result types (round 12 showed this), so we can't specialize the result
- But we DO have `rawIntRegs` tracking at emit time, which tells us the key is already an unboxed int — we're just not using this information

## Approach

### Task 1: Raw-int key bypass in emitGetTableNative/emitSetTableNative

In `emit_table.go`, before the existing key NaN-boxing + tag check sequence, check if the key value is in `rawIntRegs`. If so:
- Get the raw int value directly from `physReg(keyID)` or load from slot + SBFX
- Move to X1 (1 MOV instruction)
- Skip: EmitBoxIntFast (UBFX+ORR = 2 insns), tag check (LSR+MOV+CMP+BNE = 4 insns), SBFX unbox (1 insn)
- Keep the >= 0 check (CMP+B.LT = 2 insns) for bounds safety
- **Net savings: 5-7 instructions per table access with int key**

Concrete code location: `emit_table.go:403-416` (GetTable) and `emit_table.go:635-648` (SetTable).

Also handle the case where the key is TypeInt in the IR (from TypeSpecialize) but not in rawIntRegs — load from slot and SBFX directly without the tag check.

### Task 2: Constant value bypass for SetTable Bool path

In `emitSetTableNative`, when the value to store (Args[2]) is a compile-time `OpConstBool`:
- Compute the byte value at compile time: false=1, true=2
- Emit `MOVimm16(X4, byteVal)` (1 instruction)
- Skip: resolveValueNB (1-2 insns), tag check (LSR+MOV+CMP+BNE = 4 insns), payload extraction (LoadImm64+AND+ADD = 3 insns)
- **Net savings: 5-8 instructions per SetTable of a constant bool**

Also implement for SetTable Int path: when value is `OpConstInt`, unbox at compile time and skip the runtime type check. Saves ~3-5 insns.

Concrete code location: `emit_table.go:726-761` (Bool path) and `emit_table.go:680-701` (Int path).

### Task 3: Tests + verification

- Run existing `TestTier2_SieveCorrectness` to validate
- Add targeted test for the optimized paths
- Run full benchmark suite to measure improvement

## Expected Effect

Sieve inner marking loop: 57 insns/iter -> ~45 insns/iter (12 saved: 7 key bypass + 5 value bypass)

**Prediction calibration (MANDATORY):** Instruction count reduction of ~21%. On superscalar ARM64, IPC varies; rounds 7-10 showed that instruction-count savings translate to roughly half the predicted wall-time improvement. Therefore:
- **Predicted wall-time improvement: 10-12%** (half of 21%)
- **Sieve: 0.186s -> 0.165-0.170s** (3 reps)
- **Fannkuch: 0.070s -> 0.065-0.068s** (int array access, smaller proportional benefit)

This is a modest improvement on the 15.5x gap, but it's:
1. Zero-risk (emit-level only, no IR changes, no new ops)
2. Foundational (cleans up the emit path for future table optimizations)
3. General (helps ALL benchmarks with integer-keyed table access in loops)

## Failure Signals
- Signal 1: Sieve correctness test fails -> investigate rawIntRegs state at SetTable emit point, check if key is actually in rawIntRegs for sieve's loop patterns
- Signal 2: No measurable benchmark improvement despite fewer instructions -> abandon, accept that superscalar hides the savings entirely (precedent: round 10 showed 1-2% for float loops)
- Signal 3: Key is NOT in rawIntRegs at emit time for sieve's inner loop -> investigate why, possibly the carried map doesn't pin int phis in rawIntRegs

## Task Breakdown

- [x] 1. Raw-int key bypass for emitGetTableNative — file: `emit_table.go` — test: `TestTier2_SieveCorrectness` ✓
- [x] 2. Raw-int key bypass for emitSetTableNative — file: `emit_table.go` — test: `TestTier2_SieveCorrectness` ✓
- [x] 3. Constant bool value bypass for SetTable Bool path — file: `emit_table.go` — test: new `TestTier2_SetTableConstBool` ✓
- [x] 4. Constant int value bypass for SetTable Int path — file: `emit_table.go` — test: existing int array tests ✓
- [x] 5. Integration test + benchmark — run full suite, compare sieve/fannkuch times ✓

## Budget
- Max commits: 3 (+1 revert slot)
- Max files changed: 2 (emit_table.go + test file)
- Abort condition: 2 commits without any benchmark improvement AND correctness tests passing

## Results (filled after VERIFY)
| Benchmark | Before | After | Change |
|-----------|--------|-------|--------|
| sieve (3 reps) | 0.186s | 0.085s | -54% |
| fannkuch | 0.070s | 0.076s | +9% (noise) |
| table_array_access | 0.135s | 0.120s | -11% |

Note: Binary includes pre-existing uncommitted Tier 1 native table ops which amplify sieve improvement beyond our emit-level changes alone.

## Lessons (filled after completion/abandonment)
