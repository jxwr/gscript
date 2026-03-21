---
layout: default
title: "The Box/Unbox Toll"
permalink: /12-the-box-unbox-toll
---

# The Box/Unbox Toll

*March 2026 --- Beyond LuaJIT, Post #12*

## The Hangover

[Post #11](11-eight-bytes-that-change-everything) ended on a high note: NaN-boxing shrunk every Value from 24 bytes to 8. Sieve got 3.2x faster. The table-heavy future looked bright.

But the compute benchmarks told a different story. FibRecursive regressed 41%. Ackermann regressed 16%. HeavyLoop regressed 48%. The 8-byte Value was paying a tax on every integer operation --- a tax that didn't exist in the old 24-byte layout.

The tax is box/unbox.

## The Problem

In the old 24-byte layout, an integer lived at a fixed offset:

```asm
LDR  X0, [regRegs, slot*24 + 8]    // load .data field: 1 instruction
STR  X0, [regRegs, slot*24 + 8]    // store: 1 instruction, type tag already set
```

In NaN-boxing, the integer is encoded in the value itself. Reading it requires sign-extending the 48-bit payload. Writing it requires masking and OR-ing the tag:

```asm
// Unbox (read): extract 48-bit signed int
LDR   X0, [regRegs, slot*8]
SBFX  X0, X0, #0, #48              // sign-extend bits 0-47

// Box (write): create NaN-boxed int
MOVZ  X1, #0xFFFE, LSL #48         // load tag constant
UBFX  X0, X0, #0, #48              // mask to 48 bits
ORR   X0, X0, X1                   // set tag
STR   X0, [regRegs, slot*8]        // store
```

Three extra instructions per write. One extra per read. For a benchmark like `fib(35)` with 14.9 million recursive calls, each touching 2-3 integer values per call, that's ~100 million wasted instructions.

## Seven Optimizations

We attacked the problem from every angle. The audit identified 7 optimization targets across 3 tiers (method JIT, trace JIT, VM interpreter).

### 1. Pinned Tag Register (X24)

The int tag constant `0xFFFE000000000000` was being reloaded on every box operation. We pinned it in X24 at function entry, saving one `MOVZ` instruction per box. Applied across all three JIT tiers.

Before (3 instructions):
```asm
MOVZ  scratch, #0xFFFE, LSL #48    // load tag
UBFX  dst, src, #0, #48            // mask
ORR   dst, dst, scratch             // set tag
```

After (2 instructions):
```asm
UBFX  dst, src, #0, #48            // mask
ORR   dst, dst, X24                 // set tag (X24 = pinned constant)
```

### 2. UBFX for Pointer Extraction

Extracting a 44-bit pointer from a NaN-boxed value required loading a 64-bit mask constant:

Before (4 instructions):
```asm
MOVZ  scratch, #0xFFFF              // 3 instructions for 0x00000FFFFFFFFFFF
MOVK  scratch, #0xFFFF, LSL #16
MOVK  scratch, #0x0FFF, LSL #32
AND   dst, src, scratch
```

After (1 instruction):
```asm
UBFX  dst, src, #0, #44             // extract bits 0-43
```

One instruction. Applied at 19 call sites across method JIT, trace JIT, and SSA codegen.

### 3. Trace FORLOOP Register Pinning

The trace JIT's FORLOOP was loading, unboxing, computing, boxing, and storing every iteration --- 14 instructions for what should be 3:

Before:
```asm
LDR   X0, [regs, idxOff]       // load idx
SBFX  X0, X0, #0, #48          // unbox
LDR   X1, [regs, stepOff]      // load step
SBFX  X1, X1, #0, #48          // unbox
ADD   X0, X0, X1               // compute
UBFX  X2, X0, #0, #48          // box: mask
ORR   X2, X2, X24              // box: tag
STR   X2, [regs, idxOff]       // store idx
STR   X2, [regs, loopVarOff]   // store loop var
LDR   X1, [regs, limitOff]     // load limit
SBFX  X1, X1, #0, #48          // unbox
CMP   X0, X1                   // compare
B.GT  loop_done                 // exit
```

After (when idx/step/limit are register-allocated):
```asm
ADD   idxReg, idxReg, stepReg  // idx += step
CMP   idxReg, limitReg         // compare
B.GT  loop_done                // exit
```

Plus a memory writeback for correctness (other ops still read from memory), but the core loop drops from 14 to ~6 instructions.

### 4. Trace Arithmetic: Register-to-Register

When both operands of an ADD/SUB/MUL are register-allocated, the trace now computes directly in registers instead of loading from memory.

### 5. Branchless Integer Sign Extension

The `Int()` and `RawInt()` methods used a branch for sign extension:

```go
raw := uint64(v) & payloadMask
if raw & (1<<47) != 0 {
    return int64(raw | 0xFFFF000000000000)
}
return int64(raw)
```

Replaced with branchless arithmetic shift:

```go
return int64(uint64(v) << 16) >> 16
```

One line. The Go compiler turns this into `LSL + ASR` --- two instructions, zero branches. For the VM dispatch loop, which calls `Int()` on every arithmetic operation, eliminating the branch prediction overhead matters.

### 6. Unchecked SetInt for FORLOOP

The VM's FORLOOP counter is guaranteed to be in the 48-bit range (it's initialized from a valid integer and incremented by a valid integer). The range check in `SetInt` is wasted work:

```go
// Before: checks every iteration
func (v *Value) SetInt(i int64) {
    if i > maxInt48 || i < minInt48 { ... }
    *v = Value(tagInt | (uint64(i) & payloadMask))
}

// After: FORLOOP uses unchecked version
func (v *Value) SetIntUnchecked(i int64) {
    *v = Value(tagInt | (uint64(i) & payloadMask))
}
```

### 7. AddNums/SubNums/MulNums Fast Path

The arithmetic helper functions were calling `v.Int()` (value receiver, copies) and `IntValue()` (range check). Changed to `v.RawInt()` (pointer receiver, branchless) and `v.SetInt()` (in-place mutation):

```go
// Before
*dst = IntValue(a.Int() + b.Int())

// After
dst.SetInt(a.RawInt() + b.RawInt())
```

## Bug Fix: NaN-Boxing Zero Value

During this work, we discovered that the NaN-boxing migration had introduced a subtle bug: Go's zero value for `uint64` is `0`, which corresponds to `float64(0.0)` in IEEE 754 --- not nil (`0xFFFC000000000000`).

Every `make([]Value, n)` in the codebase created arrays where uninitialized slots appeared as `0.0` instead of nil. The Chess AI benchmark crashed because `board[key]` returned `0.0` for empty squares, passed the `!= nil` check, and then attempted to index a number.

Fix: `MakeNilSlice(n)` helper that pre-fills with `NilValue()`. Applied to table expansion and VM register initialization.

## Results

All measurements on Apple M4 Max (under moderate system load):

| Benchmark | Before | After | Improvement |
|-----------|--------|-------|-------------|
| JIT FibRecursive(20) | 47.9us | 23.9us | **50% faster** |
| JIT Ackermann(3,4) | 40.2us | 21.9us | **46% faster** |
| JIT FibIterative(30) | 284ns | 172ns | **39% faster** |
| JIT FunctionCalls(10K) | 4.1us | 2.6us | **38% faster** |
| JIT HeavyLoop | 38.1us | 25.0us | **34% faster** |
| VM FibRecursive(20) | 1669us | 821us | **51% faster** |
| VM HeavyLoop | 2201us | 1000us | **55% faster** |

The NaN-boxing regression is fully recovered and then some. FibRecursive is now faster than pre-NaN-boxing (23.9us vs 19.4us pre-NaN, vs 47.9us post-NaN), back in striking distance of LuaJIT (25us).

## What's Next

The box/unbox toll is paid. Season 2.1 --- the NaN-boxing foundation --- is complete. The runtime moves 8 bytes per Value, the JIT generates tight box/unbox sequences, and the VM dispatch loop is branchless on the hot path.

Season 2.2 is the custom heap: `mmap`-based arena allocation for Tables, Strings, and Closures. The `gcRoots` safety net (which keeps every allocated pointer alive forever) must be replaced with a proper mark-sweep collector. This is where the table-heavy benchmarks --- matmul at 40x, spectral_norm at 85x, nbody at 57x --- will finally start closing the gap.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
