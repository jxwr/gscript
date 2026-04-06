# Native Table Access for Typed Array Kinds

> Last Updated: 2026-04-06 | Round: 13 (analysis)

## Problem

GScript's Go runtime promotes tables to typed backing stores based on the values stored:
- `ArrayMixed (0)` — `[]Value` (default, handles any type)
- `ArrayInt (1)` — `[]int64` (all integer values)
- `ArrayFloat (2)` — `[]float64` (all float values)
- `ArrayBool (3)` — `[]byte` (1 byte per bool: 0=nil, 1=false, 2=true)

The Tier 2 JIT emitter (`emit_table.go`) only implements native ARM64 fast paths for ArrayMixed and ArrayInt. ArrayFloat and ArrayBool fall through to exit-resume on EVERY access.

**Impact**: Any benchmark using boolean or float arrays (sieve, spectral_norm inner tables, etc.) runs all table ops through the Go runtime despite being at Tier 2.

## Encoding Details

### ArrayBool (`[]byte`)
- Offset: `TableOffBoolArray = 192` (data ptr), len at `+8`
- Byte encoding: `0 = nil/unset`, `1 = false`, `2 = true`
- Read: load byte → branch on 0/1/2 → produce NaN-boxed nil/false/true
- Write: check value is bool → convert to byte → store
- NaN-box constants: nil=`0xFFFC000000000000`, false=`0xFFFD000000000000`, true=`0xFFFD000000000001`

### ArrayFloat (`[]float64`)
- Offset: `TableOffFloatArray = 168` (data ptr), len at `+8`
- Read: load 8 bytes (raw float64 bits) → these ARE the NaN-boxed Value (no conversion)
- Write: check value is float → store raw 8 bytes
- Simplest of all kinds — float64 bits = NaN-boxed representation

### ArrayInt (`[]int64`, already implemented)
- Offset: `TableOffIntArray = 144`, len at `TableOffIntArrayLen = 152`
- Read: load int64 → `EmitBoxIntFast` (UBFX+ORR with int tag)
- Write: check value is int → SBFX extract → store int64

## Emitter Dispatch (current)

```go
// emit_table.go:421-424 (GetTable), 607-611 (SetTable)
asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
asm.CMPimm(jit.X2, jit.AKInt)       // 1
asm.BCond(jit.CondEQ, intArrayLabel) // jump to int path
asm.CBNZ(jit.X2, deoptLabel)        // anything else → EXIT-RESUME
```

Only Mixed(0) falls through to the mixed fast path. Int(1) branches to int path. Float(2) and Bool(3) both hit `CBNZ → deoptLabel`.

## Fix

Extend the dispatch chain:
```
LDRB X2, [X0, #TableOffArrayKind]
CMP  X2, #3       // AKBool
B.EQ boolLabel
CMP  X2, #2       // AKFloat
B.EQ floatLabel
CMP  X2, #1       // AKInt
B.EQ intLabel
CBNZ X2, deopt    // anything else
// Mixed(0) falls through
```

Each new kind needs: bounds check + kind-specific load/store + NaN-box conversion.

## Cross-Engine Comparison

- **LuaJIT**: Lua tables don't have typed backing stores. Array part is always `TValue[]` (NaN-boxed). LuaJIT optimizes via SCEV bounds hoisting + type guard hoisting, not array kind specialization.
- **V8**: JSArrays have "element kinds" (PACKED_SMI, PACKED_DOUBLE, etc.). TurboFan specializes load/store based on element kind feedback. GScript's ArrayKind is analogous.
- **GScript advantage**: ArrayBool uses 1 byte per element (vs 8 bytes for NaN-boxed), so 8x better cache density for boolean arrays. ArrayFloat avoids GC scanning (no pointer-containing Values). These are real wins IF the JIT can access them natively.
