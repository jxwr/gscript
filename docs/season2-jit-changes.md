# Season 2 JIT Changes: Value 24B -> 8B (NaN-boxing)

## Overview

Changing `runtime.Value` from a 24-byte struct `{typ uint8, data uint64, ptr unsafe.Pointer}` to an 8-byte `uint64` (NaN-boxed) impacts every JIT codegen path that touches Value memory layout. This document catalogs every change needed.

NaN-boxing encoding (from `internal/nanbox/value.go`):
- **Float64**: raw IEEE 754 bits (NOT a qNaN with bit 50 set)
- **Tagged values**: bits 50-62 all 1 (qNaN), bit 63=1, bits 48-49 = type tag, bits 0-47 = 48-bit payload
  - `tag 00 = nil`, `tag 01 = bool`, `tag 10 = int48`, `tag 11 = pointer`
- **Tag constants**: `tagNil=0xFFFC`, `tagBool=0xFFFD`, `tagInt=0xFFFE`, `tagPtr=0xFFFF` (top 16 bits)
- **Masks**: `tagMask=0xFFFF000000000000`, `payloadMask=0x0000FFFFFFFFFFFF`

---

## 1. value_layout.go Constants

**File**: `internal/jit/value_layout.go`

### 1.1 ValueSize and field offsets (lines 21-32)

| Current | After |
|---------|-------|
| `ValueSize = 24` | `ValueSize = 8` |
| `OffsetTyp = 0` | **DELETE** |
| `OffsetData = 8` | **DELETE** |
| `OffsetPtr = 16` | **DELETE** |
| `OffsetIval = OffsetData` | **DELETE** |
| `OffsetPtrData = OffsetPtr` | **DELETE** |

**After code**: Value is a single uint64 at offset 0. No sub-field offsets.

```go
const (
    ValueSize = 8 // sizeof(nanbox.Value) = sizeof(uint64)
)
```

**New constants needed** (NaN-boxing bit manipulation):
```go
const (
    ValueSize = 8

    // NaN-boxing tags (top 16 bits)
    NB_TagMask     uint64 = 0xFFFF000000000000
    NB_PayloadMask uint64 = 0x0000FFFFFFFFFFFF
    NB_NanBits     uint64 = 0x7FFC000000000000  // bits 50-62
    NB_TagNil      uint64 = 0xFFFC000000000000
    NB_TagBool     uint64 = 0xFFFD000000000000
    NB_TagInt      uint64 = 0xFFFE000000000000
    NB_TagPtr      uint64 = 0xFFFF000000000000
)
```

**Difficulty**: Low (constant definitions only)

### 1.2 init() verification (lines 80-93)

**Current**: Verifies `unsafe.Sizeof`, `unsafe.Offsetof` for struct fields.
**After**: Verify `unsafe.Sizeof(nanbox.Value(0)) == 8`. No field offset checks needed.

**Difficulty**: Low

### 1.3 valueLayoutAccessor struct (lines 74-78)

**Current**: `struct { typ uint8; data uint64; ptr unsafe.Pointer }`
**After**: DELETE entirely. Value is just `uint64`.

**Difficulty**: Low

### 1.4 ValueOffset / FieldOffset helpers (lines 171-177)

**Current**:
```go
func ValueOffset(reg int) int { return reg * ValueSize }
func FieldOffset(reg, fieldOff int) int { return reg*ValueSize + fieldOff }
```
**After**: `FieldOffset` is meaningless -- delete. `ValueOffset` stays as `reg * 8`.

**Difficulty**: Low

### 1.5 regTypOffset / regIvalOffset / regFvalOffset (codegen.go lines 498-510)

**Current**:
```go
func regTypOffset(i int) int  { return i*ValueSize + OffsetTyp }
func regIvalOffset(i int) int { return i*ValueSize + OffsetIval }
func regFvalOffset(i int) int { return i*ValueSize + OffsetData }
```
**After**: DELETE all three. There is no typ sub-field, no data sub-field. The entire Value is at `i * 8`.

**Difficulty**: Low

### 1.6 TypeNil/TypeBool/TypeInt/TypeFloat constants (lines 63-71)

**Current**: Small integers (0-6) matching `runtime.ValueType`.
**After**: Replace with NaN-box tag values or keep as intermediate constants that map to tag manipulation. The type-check codegen changes fundamentally (see section 4).

**Difficulty**: Medium (must coordinate with all type check sites)

---

## 2. EmitMulValueSize

**File**: `internal/jit/value_layout.go` lines 183-201

**Current**: Switch on ValueSize; for 24 uses ADD+LSL (2 instructions).
**After**: ValueSize=8, single `LSL rd, rn, #3` (1 instruction). The case 8 path already exists.

### Call sites (16 total):

| File | Line | Context |
|------|------|---------|
| `codegen.go` | 3391 | GETFIELD: `i * ValueSize` for svals array |
| `codegen.go` | 3526 | SETFIELD: `i * ValueSize` for svals array |
| `codegen.go` | 3687 | GETTABLE (mixed): `key * ValueSize` for array |
| `codegen.go` | 3894 | SETTABLE (mixed): `key * ValueSize` for array |
| `codegen.go` | 4566 | CALL return: `RetBase * ValueSize` |
| `ssa_codegen.go` | 833 | LOAD_ARRAY (TypeInt, mixed fallback) |
| `ssa_codegen.go` | 893 | LOAD_ARRAY (TypeFloat, mixed fallback) |
| `ssa_codegen.go` | 918 | LOAD_ARRAY (generic) |
| `ssa_codegen.go` | 1037 | STORE_ARRAY (generic key*ValueSize) |
| `ssa_codegen.go` | 1081 | STORE_ARRAY (float, mixed fallback) |
| `ssa_codegen.go` | 1132 | STORE_ARRAY (bool, mixed fallback) |
| `ssa_codegen.go` | 1150 | STORE_ARRAY (unknown type, mixed fallback) |
| `trace_compile.go` | 882 | emitTrGetField: `i * ValueSize` |
| `trace_compile.go` | 945 | emitTrGetTable: `key * ValueSize` |
| `trace_compile.go` | 1037 | emitTrSetField: `i * ValueSize` |
| `trace_compile.go` | 1113 | emitTrSetTable: `key * ValueSize` |

**Change**: No code change needed -- the `case 8` already exists. But the scratch register parameter becomes unused (can simplify API).

**Difficulty**: Low (automatic, just verify)

---

## 3. Value Load/Store (Multi-word Copy -> Single Word)

### Pattern: `for w := 0; w < ValueSize/8; w++ { LDR+STR }` loops

Currently copies 3 words (24 bytes). After NaN-boxing, copies 1 word (8 bytes). The loop degenerates to a single LDR+STR pair.

### 3.1 codegen.go

| Line | Function | Context | After |
|------|----------|---------|-------|
| 722-725 | `copyValue` | Copy Value reg-to-reg (hardcoded 4 iterations!) | Single LDR+STR |
| 736-738 | `copyRKValue` | Copy Value from constants (hardcoded 4 iterations!) | Single LDR+STR |
| 3396 | `emitGetField` | Copy svals[i] to R(A) | Single LDR+STR |
| 3534 | `emitSetField` | Copy RK(C) const to svals[i] | Single LDR+STR |
| 3547 | `emitSetField` | Copy RK(C) reg to svals[i] | Single LDR+STR |
| 3691 | `emitGetTable` | Copy array[key] to R(A) | Single LDR+STR |
| 3901 | `emitSetTable` | Copy RK(C) const to array[key] | Single LDR+STR |
| 3913 | `emitSetTable` | Copy RK(C) reg to array[key] | Single LDR+STR |
| 4576-4582 | `emitCall` (result copy) | Hardcoded 3x LDR+STR | Single LDR+STR |

**NOTE**: Lines 722 and 736 use `for i := 0; i < 4; i++` (hardcoded 4, not `ValueSize/8`). This is a bug even today (ValueSize=24 means 3 words, not 4). With NaN-boxing, change to 1.

### 3.2 ssa_codegen.go

| Line | Function | Context |
|------|----------|---------|
| 706 | `SSA_LOAD_GLOBAL` | Copy constant to register |
| 920 | `SSA_LOAD_ARRAY` (generic) | Copy array element to dst |
| 1039 | `SSA_STORE_ARRAY` (generic) | Copy val to array element |
| 1084 | `SSA_STORE_ARRAY` (float, mixed) | Copy val to array |
| 1135 | `SSA_STORE_ARRAY` (bool, mixed) | Copy val to array |
| 1153 | `SSA_STORE_ARRAY` (unknown, mixed) | Copy val to array |
| 1193 | `SSA_GETFIELD` | Copy svals[idx] to dst |
| 1228 | `SSA_SETFIELD` | Copy val to svals[idx] |
| 1468 | `SSA_MOVE` (unknown type) | Full Value copy |

### 3.3 trace_compile.go

| Line | Function | Context |
|------|----------|---------|
| 330 | `emitTrMove` | Copy full Value B to A |
| 347 | `emitTrLoadK` | Copy constant to register |
| 356 | `emitTrLoadNil` | Zero-fill multiple Values |
| 887 | `emitTrGetField` | Copy svals[i] to R(A) |
| 949 | `emitTrGetTable` | Copy array[key] to R(A) |
| 1042 | `emitTrSetField` | Copy RK(C) to svals[i] |
| 1117 | `emitTrSetTable` | Copy value to array[key] |
| 1225 | `emitTrSelfCall` | Nil-fill remaining results |

**Total**: 27 multi-word copy sites, all become single LDR+STR.

**Difficulty**: Low (mechanical, but many sites -- easy to miss one)

---

## 4. Type Checking (LDRB + CMP -> Bit Manipulation)

This is the most complex change. Currently, type checking is:
```asm
LDRB Xn, [base, slot*24 + 0]   // load 1-byte typ field
CMP  Wn, #TypeInt              // compare with type constant
B.NE side_exit
```

With NaN-boxing, the type is encoded in the top bits of the Value itself:
```asm
LDR  Xn, [base, slot*8]        // load full 8-byte Value
// For IsFloat: AND + CMP with nanBits
// For IsInt:   LSR #48, CMP with 0xFFFE (or AND tagMask, CMP tagInt)
// For IsTable: AND tagMask, CMP tagPtr (then further distinguish table vs string vs function)
```

**Key problem**: NaN-boxing only has 4 tags (nil, bool, int, pointer). Table/String/Function/Coroutine/Channel all share the `pointer` tag. Distinguishing them requires loading the pointed-to object and checking its type -- or using a secondary type tag stored in the payload bits.

### All type-check sites:

#### codegen.go (LDRB + CMP pattern)

| Line | Context | Type Checked |
|------|---------|--------------|
| 514-516 | `loadRegTyp` | Generic type load helper |
| 608 | `loadRKTyp` | Constant type load |
| 3304 | `emitGetField` | TypeTable check |
| 3621 | `emitGetTable` | TypeInt key check |
| 3641 | `emitGetTable` | TypeInt key check (reg) |
| 3707 | GETTABLE int-array result | STRB TypeInt |
| 3721 | GETTABLE float-array result | STRB TypeFloat |
| 3739 | GETTABLE bool-array result | STRB TypeBool |
| 3805 | SETTABLE | Key TypeInt check |
| 3825 | SETTABLE | Key TypeInt check (reg) |

#### ssa_codegen.go (LDRB + CMP pattern)

| Line | Context | Type Checked |
|------|---------|--------------|
| 355 | Pre-loop GUARD_TYPE | Various (AuxInt) |
| 716 | In-loop SSA_GUARD_TYPE | Various |
| 725 | SSA_GUARD_TRUTHY | TypeNil/TypeBool |
| 730-744 | SSA_GUARD_TRUTHY body | TypeNil/TypeBool/data |
| 2488 | emitGuardTruthyWithContinuation | TypeNil/TypeBool |

#### trace_compile.go (LDRB + CMP pattern)

| Line | Context | Type Checked |
|------|---------|--------------|
| 551-552 | `emitTrEQ` | TypeInt on both operands |
| 560-561 | `emitTrEQ` | TypeString check |
| 667-672 | `emitTrLT` | TypeInt on both operands |
| 697-702 | `emitTrLE` | TypeInt on both operands |
| 728 | `emitTrTest` | Truthiness check |
| 743 | `emitTrTest` | TypeBool check |
| 813-816 | `emitTrGetField` | TypeTable |
| 914 | `emitTrSetField` | TypeTable |
| 979 | `emitTrSetField` | TypeTable (second) |
| 1066 | `emitTrSetTable` | TypeTable |
| 1078-1080 | `emitTrSetTable` | TypeInt key |
| 1254-1259 | `emitTrMod` | TypeInt on both |
| 1300-1302 | `emitTrUNM` | TypeInt |
| 1319-1321 | `emitTrLen` | TypeTable |
| 400 | `emitTrArithIntRA` | TypeInt guard (partial) |
| 412-414 | `emitTrArithIntRA` | TypeInt (non-allocated) |

**After code pattern** (example for TypeInt check):
```asm
LDR  Xn, [base, slot*8]         // load NaN-boxed Value
LSR  X0, Xn, #48                // extract top 16 bits
CMP  X0, #0xFFFE                // tagInt >> 48
B.NE side_exit
```

Or more efficiently using the full tag mask:
```asm
LDR  Xn, [base, slot*8]
AND  X0, Xn, #tagMask           // 0xFFFF000000000000
MOV  X1, #0xFFFE000000000000    // tagInt (need 2-3 instructions for 64-bit immediate)
CMP  X0, X1
B.NE side_exit
```

**Design decision needed**: How to emit the 64-bit tag constants efficiently. Options:
1. Pre-load tag constants into reserved registers (X20-X24 etc.)
2. Use MOVZ+MOVK sequences (2-3 instructions per constant)
3. Use LSR #48 and compare with 16-bit values (cheapest: 1 instruction to extract, 1 to compare)

**Recommended**: Option 3. `LSR X0, Xn, #48; CMP W0, #0xFFFE` -- 2 instructions total, no constants needed.

**Float check** is different (inverted logic):
```asm
LDR  Xn, [base, slot*8]
// Float if bits 50-62 are NOT all 1
// i.e., (v & nanBits) != nanBits
AND  X0, Xn, #nanBits           // need to emit 0x7FFC000000000000
CMP  X0, X1                      // X1 = nanBits (pre-loaded)
B.EQ not_float                   // all tag bits set = tagged value, not float
```

**Truthiness check** (NaN-boxing):
```asm
// Truthy = not nil and not false
// Nil  = 0xFFFC000000000000
// False = 0xFFFD000000000000
LDR  Xn, [base, slot*8]
MOV  X0, #0xFFFC000000000000    // valNil
CMP  Xn, X0
B.EQ  falsy_exit
MOV  X0, #0xFFFD000000000000    // valFalse (tagBool | 0)
CMP  Xn, X0
B.EQ  falsy_exit
```

**Total**: ~35 type-check sites across 3 files.

**Difficulty**: High (most complex change, touches the most codegen patterns, needs design decisions about tag constant loading)

---

## 5. UNBOX_INT / UNBOX_FLOAT (Extract Payload)

### Current pattern (LDR from OffsetData):
```asm
LDR  Xn, [base, slot*24 + 8]    // load .data field (the int64/float64 bits)
```

### After: UNBOX_INT (NaN-boxed int48)
```asm
LDR  Xn, [base, slot*8]         // load full Value
AND  Xn, Xn, #payloadMask       // extract bottom 48 bits
// Sign-extend: if bit 47 is set, fill top 16 bits with 1s
SBFX Xn, Xn, #0, #48           // sign-extend bits 0-47 to 64 bits
```
`SBFX Xn, Xn, #0, #48` does the sign extension in a single ARM64 instruction.

**Key concern**: Current int is 64-bit. NaN-boxed int is 48-bit. Values outside [-2^47, 2^47-1] must be promoted to float. This affects the entire VM, not just JIT.

### After: UNBOX_FLOAT (NaN-boxed float64)
```asm
LDR  Xn, [base, slot*8]         // load full Value = raw float64 bits
FMOV Dn, Xn                     // move to FP register directly
```
**Huge win**: No offset addition needed. The Value IS the float bits. 1 instruction instead of 1.

### All unbox sites:

#### ssa_codegen.go (SSA_UNBOX_INT)

| Line | Context |
|------|---------|
| 1238 | `SSA_UNBOX_INT`: `LDR dstReg, regRegs, slot*ValueSize+OffsetData` |
| 395 | Pre-loop int load: `LDR armReg, regRegs, slot*ValueSize+OffsetData` |

#### ssa_codegen.go (SSA_UNBOX_FLOAT / float loads)

| Line | Context |
|------|---------|
| 410 | Pre-loop ref-level float: `FLDRd dreg, regRegs, slot*ValueSize+OffsetData` |
| 420 | Pre-loop slot-level float: `FLDRd dreg, regRegs, slot*ValueSize+OffsetData` |
| 1980 | `resolveFloatRef` memory load: `FLDRd scratch, regRegs, slot*ValueSize+OffsetData` |
| 1994 | `resolveFloatRef` fallback: `FLDRd scratch, regRegs, s*ValueSize+OffsetData` |
| 2004 | `resolveFloatRef` LOAD_SLOT: `FLDRd scratch, regRegs, s*ValueSize+OffsetData` |

#### trace_compile.go (data field loads)

| Line | Context |
|------|---------|
| 110 | Prologue: load allocated regs `LDR armReg, regRegs, vmReg*ValueSize+OffsetData` |
| 128-149 | OP_MOVE/LOADINT/LOADK/LOADBOOL: reload after memory write |
| 257 | Write-back in loop |
| 341 | emitTrLoadInt: `STR X0, regRegs, dst+OffsetData` |
| 371 | emitTrLoadBool: `STR X0, regRegs, dst+OffsetData` |
| 397 | emitTrArithIntRA: store result `STR dstReg, regRegs, dst+OffsetData` |
| 415, 425 | emitTrArithIntRA: load B/C data |
| 445-446 | emitTrArithIntRA: store result |
| 470-471 | emitTrArithFloat: load B/C data |
| 484-485 | emitTrArithFloat: store result |
| 491-496 | emitTrForPrep: load/store idx data |
| 500-514 | emitTrForLoop: load/store idx, step, limit, loop var |
| 569-570 | emitTrEQ: load int data |
| 674-675 | emitTrLT: load int data |
| 704-705 | emitTrLE: load int data |
| 751, 764-790 | emitTrTest / emitTrNot: load/store data |
| 933 | emitTrGetTable: load key data |
| 1081 | emitTrSetTable: load key data |
| 1140-1161 | emitTrIntrinsic: load/store int data |
| 1217-1219 | emitTrSelfCall: store result data+typ |
| 1262-1263, 1282-1283 | emitTrMod: load/store data |
| 1304, 1307-1308 | emitTrUNM: load/store data |

#### codegen.go (data field loads via helpers)

| Line | Context |
|------|---------|
| 632 | `loadRKIval`: `LDR dst, regConsts, constIdx*ValueSize+OffsetIval` |
| 643 | `loadRKFval`: `FLDRd dst, regConsts, constIdx*ValueSize+OffsetData` |
| 3705 | GETTABLE int result: `STR X4, regRegs, a*ValueSize+OffsetData` |
| 3719 | GETTABLE float result: `STR X4, regRegs, a*ValueSize+OffsetData` |
| 3737 | GETTABLE bool result: `STR X4, regRegs, a*ValueSize+OffsetData` |
| 3952-3961 | SETTABLE int store: `LDR X4, ..., valDataOff` |
| 3999-4008 | SETTABLE float store: `LDR X4, ..., valDataOff` |
| 4048-4057 | SETTABLE bool store: `LDR X4, ..., valDataOff` |

**After code**:
- Int unbox: `LDR Xn, [base, slot*8]; SBFX Xn, Xn, #0, #48`
- Float unbox: `LDR Xn, [base, slot*8]; FMOV Dn, Xn` (or just `FLDRd Dn, [base, slot*8]` directly)

**Difficulty**: Medium (many sites, but pattern is consistent; int48 sign-extension is the tricky part)

---

## 6. BOX / Store-back (Write Type + Data -> Write Single uint64)

### Current pattern (3 writes):
```asm
// Store IntValue to R(A):
STR   payload, [regRegs, slot*24 + 8]   // data = int value
MOV   X0, #TypeInt                       // type tag
STRB  X0, [regRegs, slot*24 + 0]        // typ = TypeInt
STR   XZR, [regRegs, slot*24 + 16]      // ptr = nil (for scalars)
```

### After (1 write):
```asm
// Store NaN-boxed IntValue to R(A):
// value = tagInt | (payload & payloadMask)
AND   X0, payload, #payloadMask          // mask to 48 bits
ORR   X0, X0, tagIntReg                  // set tag bits (tagIntReg pre-loaded)
STR   X0, [regRegs, slot*8]             // single 8-byte write
```

For float:
```asm
// Float is stored directly as its IEEE 754 bits (no tag needed):
STR   Xn, [regRegs, slot*8]             // single 8-byte write (float bits)
// or: FSTRd Dn, [regRegs, slot*8]
```

### All box/store-back sites:

#### codegen.go

| Line | What's Boxed | Writes |
|------|-------------|--------|
| 3705-3708 | IntValue (GETTABLE int-array) | STR data + STRB typ + STR ptr(nil) |
| 3719-3722 | FloatValue (GETTABLE float-array) | STR data + STRB typ + STR ptr(nil) |
| 3737-3740 | BoolValue (GETTABLE bool-array) | STR data + STRB typ + STR ptr(nil) |

#### ssa_codegen.go

| Line | What's Boxed | Context |
|------|-------------|---------|
| 509+511 | TypeInt (inner loop spill) | STR data + STRB typ |
| 595+597 | TypeInt (inner escape spill) | STR data + STRB typ |
| 615 | TypeInt (count slot) | STR data (typ already set) |
| 620 | TypeInt (count memory) | LDR+ADD+STR data |
| 793+795 | TypeInt (LOAD_ARRAY int result) | STR data + STRB typ |
| 819+821 | TypeInt (LOAD_ARRAY bool->int) | STR data + STRB typ |
| 849+851 | TypeInt (LOAD_ARRAY mixed, int) | STR data + STRB typ |
| 879+881 | TypeInt (LOAD_ARRAY int-array) | STR data + STRB typ |
| 904+906 | TypeInt (LOAD_ARRAY float-to-int) | STR data + STRB typ |
| 963-966 | TypeInt (STORE_ARRAY spill) | STR data + STRB typ + STR ptr |
| 1389+1391 | TypeFloat (CONST_FLOAT) | FSTRd data + STRB typ |
| 1399+1401 | TypeBool (CONST_BOOL) | STR data + STRB typ |
| 1448+1450 | TypeFloat (MOVE float) | FSTRd data + STRB typ |
| 1489+1491 | TypeFloat (INTRINSIC sqrt) | FSTRd data + STRB typ |
| 1499+1501 | TypeInt (INTRINSIC bxor) | STR data + STRB typ |
| 1508+1510 | TypeInt (INTRINSIC band) | STR data + STRB typ |
| 1529+1533 | TypeInt (FORLOOP idx) | STR data + STRB typ |
| 1547+1551 | TypeInt (FORLOOP A+3) | STR data + STRB typ |
| 1561+1565 | TypeInt (FORLOOP step) | STR data + STRB typ |
| 1652-1656 | TypeInt (spillIfNotAllocated) | STR data + STRB typ |
| 1719-1723 | TypeInt (emitSlotStoreBack) | STR data + STRB typ |
| 1727-1731 | TypeInt (forloop A3 alias) | STR data + STRB typ |
| 1740-1744 | TypeFloat (emitSlotStoreBack) | FSTRd data + STRB typ |
| 1761+1764 | TypeFloat (ref-level spill) | FSTRd data + STRB typ |
| 2036 | TypeFloat (storeFloatRefResult) | FSTRd data only |
| 2175 | TypeFloat (storeFloatRefResult) | FSTRd data only |
| 2281 | TypeFloat (fwd float store) | FSTRd data + STRB typ |
| 2301+2303 | TypeFloat (CONST_FLOAT fwd) | FSTRd data + STRB typ |

#### trace_compile.go

| Line | What's Boxed | Context |
|------|-------------|---------|
| 338-341 | TypeInt (LOADINT) | STRB typ + STR data |
| 364-371 | TypeBool (LOADBOOL) | STRB typ + STR data |
| 396-400 | TypeInt (arith result) | STR data + STRB typ |
| 445-446 | TypeInt (arith result) | STR data |
| 484-487 | TypeInt (arith writeback) | STR data + MOV typ + STRB typ |
| 496 | TypeInt (FORPREP) | STR data |
| 506-511 | TypeInt (FORLOOP: idx, loop var) | STR data + STRB typ |
| 1143-1145 | TypeInt (INTRINSIC bxor) | STR data + STRB typ |
| 1152-1154 | TypeInt (INTRINSIC band) | STR data + STRB typ |
| 1161-1163 | TypeInt (INTRINSIC bor) | STR data + STRB typ |
| 1217-1219 | TypeInt (self-call result) | STR data + STRB typ |
| 1282-1285 | TypeInt (MOD result) | STR data + STRB typ |
| 1307-1310 | TypeInt (UNM result) | STR data + STRB typ |
| 1331-1332 | TypeInt (LEN result) | STR data |

**After**: Each 2-3 instruction box sequence becomes:
- **Int**: `AND payload, payload, #payloadMask; ORR val, payload, tagIntReg; STR val` (3 instructions, down from 3)
- **Float**: `STR float_bits` (1 instruction, down from 2-3). Major win because floats are unboxed in NaN-boxing.
- **Bool**: `MOV val, #tagBool_true/false; STR val` (2 instructions, down from 3)
- **Nil**: `MOV val, #tagNil; STR val` (2 instructions, down from 3 words of zero)

**Difficulty**: Medium (many sites, but pattern replacement is mechanical; float is a big simplification)

---

## 7. Pointer/Table Loading (OffsetPtrData)

### Current pattern:
```asm
LDR  X0, [regRegs, slot*24 + 16]   // load .ptr field (table/string/function pointer)
```

### After: Pointer from NaN-box
```asm
LDR  X0, [base, slot*8]            // load full Value
AND  X0, X0, #payloadMask          // extract 48-bit pointer
```
The 48-bit pointer is the physical address (all current platforms use <= 48-bit virtual addresses).

### All OffsetPtrData sites:

#### codegen.go

| Line | Context |
|------|---------|
| 3308 | GETFIELD: load *Table from R(B).ptr |
| 3333 | GETFIELD: load string ptr from Constants[C].ptr |
| 3445 | SETFIELD: load *Table from R(A).ptr |
| 3468 | SETFIELD: load string ptr from RK(B) |
| 3602 | GETTABLE: load *Table from R(B).ptr |
| 3786 | SETTABLE: load *Table from R(A).ptr |

#### ssa_codegen.go

| Line | Context |
|------|---------|
| 761 | LOAD_ARRAY: load *Table |
| 966 | STORE_ARRAY: clear ptr field (STR XZR) |
| 971 | STORE_ARRAY: load *Table |
| 1176 | SSA_GETFIELD: load *Table |
| 1212 | SSA_SETFIELD: load *Table |

#### trace_compile.go

| Line | Context |
|------|---------|
| 587-588 | emitTrEQ string: load string ptrs |
| 819 | emitTrGetField: load *Table |
| 837 | emitTrGetField: load string key ptr |
| 920 | emitTrSetField: load *Table |
| 984 | emitTrSetField: load *Table (second) |
| 999 | emitTrSetField: load string key from constants |
| 1070 | emitTrSetTable: load *Table |
| 1324 | emitTrLen: load *Table |

**After**: Every `LDR X0, [base, offset + OffsetPtrData]` becomes:
```asm
LDR  X0, [base, slot*8]
AND  X0, X0, #payloadMask
```

**Difficulty**: Medium (pointer extraction needs payloadMask; must ensure all platforms have 48-bit VA)

---

## 8. Table Struct Offsets

**File**: `internal/jit/value_layout.go` lines 34-48

The Table struct itself doesn't change size (its fields are Go slices, maps, etc.). But the `[]Value` slices inside Table (array, svals) now have 8-byte elements instead of 24-byte elements.

**What changes**:
- `TableOffArray` (offset 8) etc. -- **unchanged** (slice header is always ptr+len+cap = 24 bytes)
- Array element stride: 24 -> 8 bytes (handled by `EmitMulValueSize`)
- svals element stride: 24 -> 8 bytes (handled by `EmitMulValueSize`)

**However**: If the Table struct itself contains `[]nanbox.Value` instead of `[]runtime.Value`, the struct layout may change because field sizes/alignment could shift. Need to verify Table struct field offsets after the Value type change.

**Difficulty**: Low-Medium (offsets auto-adjust if Table struct changes; init() verifies at startup)

---

## 9. trRKBase Helper

**File**: `internal/jit/trace_compile.go` lines 1362-1367

**Current**:
```go
func trRKBase(idx int) (int, Reg) {
    if idx >= vm.RKBit {
        return (idx - vm.RKBit) * ValueSize, regConsts
    }
    return idx * ValueSize, regRegs
}
```

**After**: Same code, but ValueSize=8 makes the offsets 3x smaller. No code change needed (ValueSize constant handles it).

**Difficulty**: Low (automatic)

---

## 10. SSA Store-back with Type Tag

**File**: `internal/jit/ssa_codegen.go` lines 1710-1734 (`emitSlotStoreBack`)

**Current**: Writes OffsetData + OffsetTyp separately.
**After**: Must construct NaN-boxed value and write as single uint64.

```go
// Current:
asm.STR(armReg, regRegs, off+OffsetData)
asm.MOVimm16(X0, TypeInt)
asm.STRB(X0, regRegs, off+OffsetTyp)

// After:
// armReg contains the raw int64. Must box it:
asm.AND(X0, armReg, payloadMaskReg)   // mask to 48 bits
asm.ORR(X0, X0, tagIntReg)            // set int tag
asm.STR(X0, regRegs, off)             // single write
```

**Design decision**: Need reserved registers for `payloadMaskReg` and `tagIntReg` (or emit them inline each time).

**Recommendation**: Reserve X23 = payloadMask, X24 = tagInt for the most common case. Load other tags (tagFloat is not needed -- floats are stored raw, tagBool/tagNil are rare) inline.

**Difficulty**: Medium (register pressure increases; affects register allocation strategy)

---

## 11. Float Register Handling (Major Simplification)

**Current**: Float values stored in `.data` field need `FLDRd Dn, [base, slot*24+8]` to load.
**After**: Float values ARE the raw bits, so `FLDRd Dn, [base, slot*8]` loads directly (no offset addition for OffsetData).

But there's a subtlety: **you must verify the Value IS a float before using FMOV/FLDRd**, because a NaN-boxed int loaded as float64 would be a signaling NaN.

For type-specialized SSA paths (where guards have already verified the type), this is safe and gives a clean 1-instruction unbox.

**Difficulty**: Low (simplification)

---

## 12. Constant Pool Access

Constants are currently `[]runtime.Value`, each 24 bytes. After NaN-boxing, `[]nanbox.Value`, each 8 bytes.

All constant pool index calculations use `constIdx * ValueSize + OffsetXxx`. After:
- `constIdx * 8` for the full Value
- No sub-field offsets

Sites in codegen.go: lines 608, 632, 643, 732-733, 3333, 3468, 3533, 3546, 3621-3622, 3805-3806, 3900, 3952, 3999, 4048.
Sites in ssa_codegen.go: lines 703-704.
Sites in trace_compile.go: lines 345, 999, 1364.

**Difficulty**: Low (mechanical, ValueSize constant handles most of it)

---

## Summary Table

| Category | Files Affected | Sites | Difficulty | Instruction Savings |
|----------|---------------|-------|------------|---------------------|
| 1. Layout constants | value_layout.go | 1 | Low | N/A (infrastructure) |
| 2. EmitMulValueSize | value_layout.go | 16 calls | Low | 1 inst saved per call (2->1) |
| 3. Multi-word copy | codegen.go, ssa_codegen.go, trace_compile.go | 27 | Low | 4 inst saved per copy (6->2) |
| 4. Type checking | all 3 codegen files | ~35 | **High** | ~0 (different pattern, similar cost) |
| 5. Unbox int/float | all 3 codegen files | ~40 | Medium | Float: 0 saved (same); Int: +1 (SBFX) |
| 6. Box/store-back | all 3 codegen files | ~40 | Medium | Float: 2 saved (3->1); Int: 0-1 |
| 7. Pointer loading | all 3 codegen files | ~15 | Medium | +1 inst (AND mask) per ptr load |
| 8. Table offsets | value_layout.go | 1 | Low-Medium | N/A |
| 9. trRKBase | trace_compile.go | 1 | Low | Automatic |
| 10. Store-back | ssa_codegen.go | 8 | Medium | 1 saved per store (3->2) |
| 11. Float handling | all codegen | ~15 | Low | Simplification |
| 12. Constant pool | all codegen | ~15 | Low | Automatic |

**Total affected sites**: ~210+ individual code locations across 4 files.

---

## Recommended Implementation Order

1. **value_layout.go**: Change constants, add NaN-box constants, update init()
2. **New helper functions**: `emitTypeCheck(asm, reg, tagConst)`, `emitBoxInt(asm, dst, src)`, `emitBoxFloat(asm, dst, src)`, `emitUnboxInt(asm, dst, src)`, `emitExtractPtr(asm, dst, src)`
3. **trace_compile.go**: Simplest codegen, good for testing the new patterns
4. **codegen.go**: Method JIT (more complex, has CALL handling)
5. **ssa_codegen.go**: Most complex (register allocation, forwarding, store-back)
6. **Tests**: Update codegen_test.go, ssa_codegen_test.go, trace_compile_test.go

---

## Design Decisions Needed Before Implementation

1. **Reserved registers for tag constants**: Reserve X23/X24 for payloadMask/tagInt? Or load inline?
2. **Int48 overflow handling**: VM must promote int64 to float when out of 48-bit range. JIT guards must detect this.
3. **Pointer type discrimination**: NaN-boxing has one pointer tag for Table/String/Function/Coroutine/Channel. How to distinguish? Options:
   - (a) Store a type byte at the start of every heap object (like V8's Map pointer)
   - (b) Use different tag bits for different pointer types (uses more of the NaN payload space)
   - (c) Only use pointer tag for Table (the common case), box others specially
4. **String representation**: Currently strings live at OffsetPtrData as `*string`. In NaN-boxing, the pointer tag holds the address of the string header. Need to decide if string access adds a dereference layer.
5. **Guard relaxation for int48**: SSA guards currently check `typ == TypeInt`. With NaN-boxing, should we also guard that the int fits in 48 bits? Or handle overflow in the int arithmetic codegen?

---

## Key Risk: Register Pressure

Current JIT uses these callee-saved registers:
- X19 = TraceContext pointer
- X25 = self-call depth
- X26 = regRegs (register array base)
- X27 = regConsts (constant pool base)
- X28 = regCtx (JIT context)

If we reserve X23/X24 for NaN-boxing constants (payloadMask, tagInt), that's 7 callee-saved registers used, leaving X20-X22 for value allocation. This is tight but workable since the SSA allocator primarily uses X0-X15 (caller-saved, spilled across calls).

Alternative: Use the `LSR #48 + CMP imm16` pattern to avoid needing a pre-loaded tagMask register entirely. This is 2 instructions but zero register pressure.
