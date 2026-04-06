# Analyze Report — Round 14

> Date: 2026-04-06
> Cycle ID: 2026-04-06-table-access-bypass

## Architecture Audit

Quick read (rounds_since_arch_audit=1 < 2). `bash scripts/arch_check.sh` results:

- `emit_dispatch.go` 961 lines -- approaching limit (no changes planned this round)
- `graph_builder.go` 939 lines -- approaching limit (no changes planned this round)
- `emit_table.go` 872 lines -- grew from round 13 native array kinds; this round modifies existing functions, not adding new ones
- Source: 43 files, 17562 lines. Test ratio: 81% (14309 test lines)
- 1 TODO/HACK marker (emit_call.go:24, pre-existing)
- No new constraints beyond what's in constraints.md

**No constraint changes needed.** This round touches only emit_table.go (872 lines, within budget).

## Gap Classification

| Category | Benchmarks | Total Gap vs LuaJIT | Blocked? |
|----------|------------|---------------------|----------|
| recursive_call | fib (57x), ackermann (43.7x), mutual_recursion (32.8x), method_dispatch (huge) | ~1.86s | YES (ceiling=2) |
| tier2_float_loop | mandelbrot (6.7x), spectral_norm (41.9x), nbody (19.3x), matmul (42.8x) | ~2.27s | No (failures=1) |
| field_access | sieve (15.5x), fannkuch (3.5x), table_field/array (N/A) | ~0.22s | No (failures=0) |
| allocation_heavy | binary_trees (N/A), object_creation (regression) | N/A | No (failures=0) |
| other | sort (4.9x), sum_primes (2x) | ~0.05s | No |

## Blocked Categories
- `recursive_call` (ceiling=2): Tier 2 is net-negative for recursive functions. Needs native recursive BLR or Tier 1 specialization.

## Active Initiatives
- `tier2-float-loops.md` (paused): B3 peephole items exhausted. Next step requires feedback collection or deeper arch changes (deopt frame descriptors, loop-exit boxing).
- `recursive-tier2-unlock.md` (paused/blocked): category_failures=2.

## Selected Target

- **Category**: field_access
- **Initiative**: standalone
- **Reason**: Zero failures in this category. Sieve shows 15.5x gap despite round 13 native ArrayBool paths. Diagnostic data reveals massive per-access overhead (57 insns, only 9% compute). Two emit-level optimizations (key bypass + value bypass) are zero-risk and save 12+ insns per table access. tier2_float_loop has larger absolute gap but is paused/needing architectural changes.
- **Benchmarks**: sieve (primary), fannkuch (secondary)

## Prior Art Research

### Web Search Findings
- **V8 TurboFan**: Uses loop peeling + LoadElimination to hoist CheckMaps out of loops. JSNativeContextSpecialization specializes element access based on ElementAccessFeedback. SimplifiedLowering converts tagged ops to untagged machine ops.
- **LuaJIT**: ABC (Array Bounds Check Elimination) via range analysis. Array base pointer kept in register. Trace JIT naturally eliminates per-access dispatch by recording only the taken path.
- **.NET CLR**: Loop cloning creates fast/slow paths. x64 JIT hoists bounds check to pre-header; if it passes, all range checks in loop body eliminated.
- **Common pattern**: All production compilers prove safety once at loop entry, not per-access.

### Reference Source Findings
- V8 `src/compiler/load-elimination.cc`: Tracks maps/elements kind across nodes, kills redundant CheckMaps
- V8 `src/compiler/simplified-lowering.cc`: PROPAGATE/RETYPE/LOWER phases for representation selection
- LuaJIT `lj_asm.c`: Backward register allocation with PHI shuffle, snapshot-based deopt restoration

### Knowledge Base Update
No new knowledge base entry needed — findings are emit-level optimizations, not new techniques.

## Source Code Findings

### Files Read
- `emit_table.go:378-510` (emitGetTableNative): Full dispatch sequence for GetTable
- `emit_table.go:610-770` (emitSetTableNative): Full dispatch sequence for SetTable
- `emit_reg.go:140-160` (resolveValueNB): Boxes rawIntRegs values before returning
- `regalloc.go:1-280` (AllocateRegisters): Carried map, invariant carry, phi pre-allocation
- `pass_licm.go:473-505` (canHoistOp): Whitelist of hoistable ops (GetTable/SetTable not included)
- `ir_ops.go:62-64`: OpNewTable/GetTable/SetTable definitions

### Diagnostic Data

**Sieve function Tier 2 IR** (19 blocks, 15 regs, 1 spill slot):
- Init loop (B2/B1): SetTable with ConstBool(true)
- Outer sieve (B4): MulInt + Le comparison (Le is generic, not LeInt — v22 is TypeAny)
- Inner marking (B7/B8): SetTable + AddInt + Le comparison — 57 insns/iter
- Counting (B13/B11/B12): GetTable + GuardTruthy + AddInt — ~57 insns/iter

**Inner marking loop instruction breakdown (57 insns/iter):**

| Category | Count | % |
|----------|-------|---|
| Actual compute (CMP, ADD, STR) | 5 | 9% |
| Table access overhead (type/meta/kind) | 15 | 26% |
| NaN-boxing overhead (box/unbox round trips) | 14 | 25% |
| Bounds check | 5 | 9% |
| Overflow check | 3 | 5% |
| Phi slot loads/stores | 10 | 18% |
| Control flow + dead | 5 | 9% |

### Actual Bottleneck (data-backed)

**The key box-unbox round trip is the cheapest win.** In the SetTable emit path:
```
644: UBFX  X1, X23, #0, #48     // unbox from rawIntRegs
648: ORR   X1, X1, X24           // re-box as int
64c: LSR   X2, X1, #48           // check tag (guaranteed to be 0xFFFE)
650: MOV   X3, #0xfffe
654: CMP   X2, X3
658: B.NE  exit                  // never taken
65c: SBFX  X1, X1, #0, #48      // unbox again
```
7 instructions that are 100% wasted when the emitter knows the key is a raw int.

**The constant bool value path is the second win.** For `is_prime[j] = false`:
- `resolveValueNB` loads the pre-computed NaN-boxed false value
- Then checks its tag (known to be bool)
- Then extracts the payload (known to be 0)
- Then converts 0 -> byte 1 (false)
All of this can be replaced with `MOVimm16 X4, #1` (1 instruction).

## Plan Summary

Emit-level optimization: eliminate redundant NaN-boxing round trips for integer table keys and compile-time-known bool/int values in SetTable. Two changes to `emit_table.go`, no IR changes, no new ops. Expected 10-12% sieve improvement (0.186s -> 0.165-0.170s), calibrated for superscalar ARM64. Zero correctness risk — emit path only changes the instruction sequence for cases where the input is already known-typed.

**Future directions identified** (not this round):
1. **Table pointer guard hoisting**: New IR op (OpCheckTablePtr) to hoist table type check + metatable check to pre-header via LICM. Saves 10+ insns per access. Requires emit_dispatch.go split first.
2. **Le/Lt specialization for TypeAny operands**: v22 (n parameter) stays TypeAny → Le doesn't specialize to LeInt → fused compare+branch doesn't fire. Needs parameter type guards or feedback.
3. **Phi slot reload elimination**: After SetTable fast path, phis are unnecessarily reloaded. Needs split fast/slow path at emit level or IR-level exit-point tracking.
