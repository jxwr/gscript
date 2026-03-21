---
layout: default
title: "Eight Bytes That Change Everything"
permalink: /11-eight-bytes-that-change-everything
---

# Eight Bytes That Change Everything

*March 2026 --- Beyond LuaJIT, Post #11*

## Season 2 Begins

This post marks a turning point. Everything before this --- ten blog posts, dozens of optimizations, two JIT compilers --- was Season 1. We pushed GScript from an interpreter to a tracing JIT that beats LuaJIT on fib and matches it on function calls. We learned SSA, register allocation, cold code splitting, sub-trace calling. But Season 1 hit a wall that no amount of code generation could break through.

The wall is 24 bytes.

In [Post #10](10-the-last-thirty-percent), the scoreboard told a clear story. Two categories of benchmarks exist:

**Compute-heavy (competitive):** fib 21% faster than LuaJIT, fn_calls at parity, ackermann 1.6x gap, mandelbrot 2.7x gap. These benchmarks operate on integers and floats in registers. The JIT generates tight ARM64.

**Table-heavy (catastrophic):** matmul 51x, spectral_norm 83x, nbody 64x, method_dispatch 80x. Every table operation moves 24 bytes per Value. Every array element is 24 bytes. Every field read is 24 bytes. The gap is not in the instructions we generate --- it is in the data we move.

LuaJIT's TValue is 8 bytes. Ours was 24. Three times the memory bandwidth. Three times the cache pressure. Three times the GC load. No JIT optimization can close a 3x data overhead.

So we changed the data.

## The 24-Byte Value: An Autopsy

Before Season 2, every GScript value was a 24-byte struct:

```
type Value struct {
    typ  ValueType       // offset 0:  1 byte + 7 padding
    data uint64          // offset 8:  int/float/bool payload
    ptr  unsafe.Pointer  // offset 16: GC-visible pointer for ref types
}
```

`typ` is a 1-byte enum (nil, bool, int, float, string, table, function, ...) that wastes 7 bytes of padding. `data` holds the 8-byte scalar payload (an `int64` or `float64` bit pattern). `ptr` holds a GC-visible pointer for reference types (tables, strings, closures). The pointer field *must* be `unsafe.Pointer` --- not `uint64` --- because Go's garbage collector only follows typed pointer fields. If we stored the pointer in `uint64`, the GC would not see it, and the object would be collected while still in use.

This design is safe and straightforward. It is also a performance disaster for anything that touches tables.

Consider matmul's inner loop. The matrix is a table-of-tables, each row a `[]Value`. Reading `C[i][j]` means:

1. Load the row pointer from `C.array[i]` --- 24 bytes read, extract `ptr` at offset 16.
2. Load the element from `row.array[j]` --- another 24 bytes read, extract `data` at offset 8.
3. For a 300x300 matrix, the array part alone is 300 * 300 * 24 = 2.16 MB.

LuaJIT does the same in 300 * 300 * 8 = 720 KB. That is 1.44 MB less data through the cache hierarchy. On Apple M4 with 128 KB L1 data cache and 16 MB L2, GScript's matrix does not fit in L1; LuaJIT's does. The difference cascades: L1 misses (3-5 cycles each), L2 misses for larger matrices, TLB misses for multi-megabyte allocations.

And that is just the read path. The write path is worse. Storing a value in the old layout required three separate writes:

```asm
// Store IntValue to R(A) (24-byte layout):
STR   payload, [regRegs, slot*24 + 8]    // write data field
MOV   X0, #TypeInt
STRB  X0, [regRegs, slot*24 + 0]         // write type tag
STR   XZR, [regRegs, slot*24 + 16]       // clear ptr field
```

Three stores. Three cache line touches. Three opportunities for the store buffer to stall.

We tried shrinking to 16 bytes in Post #9. It failed. The fundamental problem: Go's GC cannot see pointers hidden in non-pointer fields. If `ptr` becomes a `uint64`, the GC collects the objects. If `data` becomes an `unsafe.Pointer`, storing an integer `42` in it violates Go's pointer rules and crashes the GC scanner.

The only way to get to 8 bytes is to give up on Go's GC seeing our pointers --- and accept the consequences.

## NaN-Boxing: The IEEE 754 Hack

The technique is called NaN-boxing. V8 uses a variant. JavaScriptCore uses it. LuaJIT uses it. SpiderMonkey used it for years. The idea exploits an accident of the IEEE 754 floating-point standard.

A `float64` is 64 bits: 1 sign, 11 exponent, 52 mantissa. A value is NaN (Not a Number) when all 11 exponent bits are 1 and the mantissa is nonzero. A *quiet* NaN (qNaN) additionally has the highest mantissa bit (bit 51) set. This means any 64-bit value with bits 51-62 all set to 1 is a valid qNaN --- and the remaining 51 bits of mantissa plus the sign bit are "free." Hardware floating-point operations produce only one specific NaN pattern (`0x7FF8000000000000`). The other 2^52 NaN patterns are ours to fill with whatever we want.

GScript's NaN-boxing layout:

```
Bit layout of an 8-byte NaN-boxed Value (uint64):

 63  62       52 51 50 49 48 47           0
  S  EEEEEEEEEEE  Q  D  TT  PPPP...PPPPP
  ^  ^^^^^^^^^^^  ^  ^  ^^  ^^^^^^^^^^^^
  |  exponent     |  |  |   payload (48 bits)
  sign            |  |  tag (2 bits: 00=nil, 01=bool, 10=int, 11=ptr)
                  |  discriminator (1=tagged, 0=canonical NaN)
                  quiet NaN bit

Float64 (normal):
  Any bit pattern where bits 50-62 are NOT all 1.
  Stored directly --- the uint64 IS the float64 bits.

Tagged (non-float):
  Bits 50-62 all 1, sign bit = 1.
  Bits 48-49 = 2-bit type tag.
  Bits 0-47  = 48-bit payload.
```

The constant definitions:

```
tagNil  = 0xFFFC_0000_0000_0000   // sign=1, tag=00
tagBool = 0xFFFD_0000_0000_0000   // sign=1, tag=01
tagInt  = 0xFFFE_0000_0000_0000   // sign=1, tag=10
tagPtr  = 0xFFFF_0000_0000_0000   // sign=1, tag=11
```

The type check is a single bit operation. To determine if a value is a float:

```go
func (v Value) IsFloat() bool {
    return uint64(v) & 0x7FFC000000000000 != 0x7FFC000000000000
}
```

If bits 50-62 are not all set, the value is a float. Period. One AND, one compare. To check for a specific tagged type:

```go
func (v Value) IsInt() bool {
    return uint64(v) & 0xFFFF000000000000 == 0xFFFE000000000000
}
```

Shift the top 16 bits down, compare with a 16-bit constant. Two instructions on ARM64.

### Integer Range

The 48-bit payload gives us signed integers from -140,737,488,355,327 to +140,737,488,355,327. That is 140 trillion --- more than enough for loop counters, array indices, and virtually every scripting use case. For comparison, LuaJIT's NaN-boxed integers are 32-bit (only 2 billion).

When an integer exceeds the 48-bit range, it is promoted to `float64`:

```go
func FromInt(i int64) Value {
    if i > maxInt48 || i < minInt48 {
        return FromFloat64(float64(i))
    }
    return Value(tagInt | (uint64(i) & payloadMask))
}
```

The promotion loses precision for integers above 2^53, but this matches Lua semantics and the precision loss is irrelevant for practical scripting code.

### NaN Canonicalization

There is one subtlety. What if a floating-point operation produces a NaN whose bit pattern collides with our tag space? For example, a hardware-generated NaN with bits 50-62 all set would look like a tagged value.

In practice, hardware only produces the canonical NaN (`0x7FF8000000000000`), which has bit 50 = 0 and therefore never collides. But for defense-in-depth, the constructor canonicalizes:

```go
func FromFloat64(f float64) Value {
    bits := math.Float64bits(f)
    if bits & nanBits == nanBits {
        return Value(canonicalNaN) // 0x7FF8000000000000
    }
    return Value(bits)
}
```

This check costs one AND and one conditional branch --- negligible for float-heavy code, and the branch is almost never taken.

### Pointer Sub-Types

Four tags (nil, bool, int, pointer) are not enough to distinguish Table from String from Closure from Coroutine from Channel. All are pointers. The solution: steal 4 bits from the 48-bit pointer payload to encode a sub-type.

macOS ARM64 pointers use about 41 bits. We can safely use bits 44-47 for the sub-type, leaving 44 bits for the address --- enough for 16 TB of address space.

```
Pointer Value layout (within the 48-bit payload):

 47      44 43                 0
  SSSS      AAAA...AAAA
  ^^^^      ^^^^^^^^^^^^
  sub-type  44-bit address

  sub-type 0 = Table
  sub-type 1 = String
  sub-type 2 = Closure
  sub-type 3 = GoFunction
  sub-type 4 = Coroutine
  sub-type 5 = Channel
```

Extracting a table pointer from a NaN-boxed value costs one AND instruction:

```go
func (v Value) ptrPayload() unsafe.Pointer {
    return unsafe.Pointer(uintptr(uint64(v) & ptrAddrMask))
}
```

## The GC Problem (And the Temporary Solution)

NaN-boxed pointers live inside `uint64` values. Go's garbage collector cannot see them. If we store a `*Table` pointer as `uint64(tagPtr | uintptr(tablePtr))`, the GC has no idea the table is still in use. The next GC cycle collects it. The program crashes.

The correct long-term solution is a custom heap: allocate all GScript objects via `mmap`, manage them with a custom mark-sweep collector, and never let Go's GC touch them. This is what LuaJIT, V8, and SpiderMonkey do. It is also a substantial project on its own --- Season 2.2.

For Season 2.1 (now), we use a simple but effective stopgap: a global root map.

```go
var (
    gcRootsMu sync.Mutex
    gcRoots   = make(map[uintptr]any, 256)
)

func keepAlive(p unsafe.Pointer, obj any) {
    gcRootsMu.Lock()
    gcRoots[uintptr(p)] = obj
    gcRootsMu.Unlock()
}
```

Every time a pointer is stored in a NaN-boxed Value, the original Go object is also stored in `gcRoots`. The map values are `any` (interface), which Go's GC *can* see. The GC follows the interface to the object, marks it alive, and does not collect it.

The map is intentionally never cleaned. Values accumulate for the lifetime of the program. For benchmark durations (under 2 seconds), the leaked memory is negligible. For a production runtime, this would be unacceptable --- hence the custom heap in Season 2.2.

The mutex adds synchronization overhead on every allocation. But the overhead is per-allocation, not per-access. Reading a table field (the hot path) does not touch `gcRoots`. Creating a new table (the cold path) does.

This is the correct engineering trade-off for a migration: get the data layout right first, fix the GC later.

## Implementation: The Three Layers

The NaN-boxing migration touched three layers: the standalone `nanbox` package, the runtime `value.go` rewrite, and the JIT codegen adaptation.

### Layer 1: The nanbox Package

The `internal/nanbox/` package was built in isolation, fully tested before anything else changed. 27 test functions covering:

- Float roundtrip (normal, negative zero, subnormal, infinity, NaN)
- NaN canonicalization (10 exotic NaN patterns that could collide with tag space)
- Integer roundtrip (including boundary values, sign extension, overflow promotion)
- Boolean and nil roundtrip
- Pointer roundtrip (multiple allocations, null pointer)
- Truthiness semantics
- Cross-type discrimination (every type check is exclusive)
- 1,000,000 random float fuzzing (no misclassification)
- Bit-pattern verification (specific constants match design)
- Size verification: `unsafe.Sizeof(Value(0)) == 8`

The fuzz test was critical. One million random `uint64` bit patterns, each converted to `float64` and back through `FromFloat64`. Every single one must classify as `IsFloat()` and roundtrip correctly (or canonicalize if NaN). This catches the edge case that killed the 16-byte experiment: a float bit pattern that accidentally looks like a tagged value.

### Layer 2: The Runtime Rewrite

`runtime/value.go` was rewritten from scratch. The old 24-byte struct was replaced by:

```go
type Value uint64
```

One line. Every method on Value was reimplemented in terms of NaN-box bit operations. The public API was preserved: `IntValue(42)`, `v.IsInt()`, `v.Int()`, `v.String()` --- all behave identically. But internally, everything changed.

The pointer constructors gained sub-type encoding:

```go
func TableValue(t *Table) Value {
    if t == nil {
        return Value(valNil)
    }
    p := unsafe.Pointer(t)
    keepAlive(p, t)
    return Value(tagPtr | ptrSubTable | (uint64(uintptr(p)) & ptrAddrMask))
}
```

The `keepAlive` call is the GC safety net. The sub-type bits (`ptrSubTable = 0 << 44`) encode the pointer kind without a dereference.

The type accessors became pointer-sub-type aware:

```go
func (v Value) IsTable() bool {
    return uint64(v)&tagMask == tagPtr && v.ptrSubType() == ptrSubTable
}
```

Two checks: is it a pointer? Is it the right kind of pointer? For the hot path (table field access where the JIT has already verified the type), the second check is skipped.

### Layer 3: The JIT Adaptation

This was the hardest part. 210+ code locations across four files, 2,337 lines added, 1,020 lines removed. The changes fell into six categories:

**1. Value load/store: 3 words to 1 word.**

Before (24-byte Value, copying R(B) to R(A)):

```asm
LDR  X0, [regRegs, B*24 + 0]     // load word 0 (typ + padding)
STR  X0, [regRegs, A*24 + 0]     // store word 0
LDR  X0, [regRegs, B*24 + 8]     // load word 1 (data)
STR  X0, [regRegs, A*24 + 8]     // store word 1
LDR  X0, [regRegs, B*24 + 16]    // load word 2 (ptr)
STR  X0, [regRegs, A*24 + 16]    // store word 2
```

After (8-byte NaN-boxed Value):

```asm
LDR  X0, [regRegs, B*8]          // load entire Value
STR  X0, [regRegs, A*8]          // store entire Value
```

Six instructions down to two. 27 multi-word copy sites across the codebase, all reduced to single LDR+STR pairs.

**2. Type checking: byte load to bit shift.**

Before (24-byte layout):

```asm
LDRB W0, [regRegs, slot*24 + 0]  // load 1-byte type tag
CMP  W0, #2                      // TypeInt = 2
B.NE guard_fail
```

After (NaN-boxing):

```asm
LDR  X0, [regRegs, slot*8]       // load full Value
LSR  X1, X0, #48                 // shift top 16 bits to bottom
MOV  X2, #0xFFFE                 // tagInt >> 48
CMP  X1, X2
B.NE guard_fail
```

The instruction count is slightly higher (5 vs 3), but the Value is already loaded for use --- the LDR is shared with the subsequent unbox operation. In practice, the type check and unbox together went from 4 instructions (LDRB + CMP + B.NE + LDR data) to 4 instructions (LDR + LSR + CMP + B.NE), with the loaded value already in X0 for the unbox. Net instruction count: approximately equal. Net memory traffic: 8 bytes instead of 24. A clear win.

**3. Integer box/unbox.**

Before (24-byte, storing int result):

```asm
STR   X0, [regRegs, slot*24 + 8]    // store data
MOV   W1, #2
STRB  W1, [regRegs, slot*24 + 0]    // store type tag
STR   XZR, [regRegs, slot*24 + 16]  // clear ptr field
```

After (NaN-boxing):

```asm
LSL   X0, X0, #16                // clear top 16 bits (mask to 48)
LSR   X0, X0, #16
ORR   X0, X0, tagIntReg          // set int tag (pre-loaded in X register)
STR   X0, [regRegs, slot*8]      // single store
```

For unboxing, ARM64's `SBFX` (Signed Bit-Field Extract) sign-extends a 48-bit integer to 64 bits in a single instruction:

```asm
LDR   X0, [regRegs, slot*8]      // load NaN-boxed Value
SBFX  X0, X0, #0, #48            // sign-extend bits 0-47 to 64 bits
```

**4. Float handling: the big simplification.**

This is where NaN-boxing truly shines. In the old layout, a float was stored in the `data` field at offset 8:

```asm
// Old: load float from Value
FLDRd D0, [regRegs, slot*24 + 8]    // load from .data offset
```

```asm
// Old: store float to Value
FSTRd D0, [regRegs, slot*24 + 8]    // store to .data offset
MOV   W0, #3                        // TypeFloat
STRB  W0, [regRegs, slot*24 + 0]    // store type tag
STR   XZR, [regRegs, slot*24 + 16]  // clear ptr field
```

In NaN-boxing, a float *is* the raw uint64 bits. No tag, no offset, no type write:

```asm
// New: load float from Value
FLDRd D0, [regRegs, slot*8]         // the Value IS the float bits

// New: store float to Value
FSTRd D0, [regRegs, slot*8]         // just write the bits
```

Three instructions down to one for float stores. For mandelbrot, which is 100% float arithmetic in the inner loop, this is a significant reduction in instruction count.

**5. Pointer extraction.**

Before:

```asm
LDR  X0, [regRegs, slot*24 + 16]    // load .ptr field directly
```

After:

```asm
LDR  X0, [regRegs, slot*8]          // load NaN-boxed Value
AND  X0, X0, ptrAddrMaskReg         // extract 44-bit address
```

One extra AND instruction, but one fewer memory access (8 bytes instead of 24 bytes loaded). The AND is a single-cycle ALU operation; the memory bandwidth saved is worth far more.

**6. Table array access: the bandwidth revolution.**

Before (24B per element):

```asm
// Load array[i]: compute offset = i * 24
ADD  X0, X1, X1, LSL #1      // X0 = i * 3
LSL  X0, X0, #3               // X0 = i * 24
ADD  X0, arrayBase, X0        // address of element
LDR  X2, [X0, #8]             // load .data (the actual value)
LDRB W3, [X0, #0]             // load .typ (for type check)
```

After (8B per element):

```asm
// Load array[i]: compute offset = i * 8
LDR  X2, [arrayBase, X1, LSL #3]   // single indexed load
```

One instruction. The `LSL #3` (shift left by 3 = multiply by 8) fits in the addressing mode --- the index multiplication is free. For a 300x300 matrix access in matmul's inner loop (90,000 iterations), this is the difference between 5 instructions per access and 1. The cache footprint drops from 2.16 MB to 720 KB. This is where the table-heavy benchmarks will see the most dramatic improvement.

## The Numbers

The nanbox package: 248 lines of code, 783 lines of tests, 27 test functions, all passing. `unsafe.Sizeof(Value(0)) == 8` verified at compile time.

The runtime rewrite: `value.go` went from a 24-byte struct with 9 type constants to a `uint64` typedef with NaN-box bit operations, pointer sub-type encoding, and the `gcRoots` safety net.

The JIT adaptation: 210+ modified code locations. `value_layout.go` grew from offset constants to a full NaN-boxing codegen helper library (`EmitBoxInt`, `EmitUnboxInt`, `EmitCheckTagShr48`, `EmitExtractPtr`, `EmitBoxNil`, `EmitBoxBool`). The `ValueSize` constant changed from 24 to 8, and `EmitMulValueSize` simplified from a two-instruction `ADD+LSL` sequence to a single `LSL #3`.

The total diff: 2,337 lines added, 1,020 removed, across 12 files.

## Benchmark Results

[BENCHMARK_DATA]

## What Season 2 Means

Season 1 was about making the JIT generate good code. We succeeded: for pure computation, GScript's ARM64 output is competitive with LuaJIT.

Season 2 is about making the runtime move less data. NaN-boxing is the foundation --- the 24B-to-8B shrink that makes everything downstream possible. But it is only the foundation. The gcRoots map is a temporary fix. Go's GC still scans the Go heap. String allocation still creates Go objects. Table internals still use Go slices.

The roadmap:

**Season 2.1 (done):** NaN-boxing. Value becomes `uint64`. JIT codegen adapted. GC safety via `gcRoots` map. Correctness verified.

**Season 2.2 (next):** Custom heap. All GScript objects (tables, strings, closures) allocated via `mmap`. Bump allocator with size-class arenas. ~2-5ns per allocation instead of Go's ~25-50ns.

**Season 2.3:** Custom mark-sweep GC. Incremental tri-color marking. Write barriers. The `gcRoots` map is removed. Go's GC sees no GScript pointers --- zero pause time for GScript workloads.

**Season 2.4:** JIT integration with the custom heap. Table field loads become direct pointer dereferences into mmap'd memory. No Go runtime calls, no interface assertions, no map lookups.

Each phase builds on the previous. NaN-boxing is the prerequisite for all of them --- you cannot put objects on a custom heap if your Value type still has Go-visible pointer fields.

## The Bet

Season 1 proved that register-level performance is solvable. A tracing JIT generating ARM64 can match LuaJIT on compute-heavy code.

Season 2 bets that data-level performance is also solvable. NaN-boxing eliminates the 3x memory overhead. The custom heap eliminates the GC overhead. Together, they should bring the table-heavy benchmarks from 50-80x behind LuaJIT to 5-10x behind --- and with further JIT improvements (inline field caching, type-specialized table ops), within striking distance.

The 8-byte Value is the foundation of that bet. Everything else follows from it.

---

*[Beyond LuaJIT](./) --- a series about building a JIT compiler from scratch.*
