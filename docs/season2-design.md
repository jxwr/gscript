# Season 2: NaN-Boxing + Custom Memory Management

**Status**: Design
**Date**: 2026-03-21
**Goal**: Shrink `Value` from 24B to 8B, eliminate Go GC as bottleneck for table-heavy workloads.

---

## Table of Contents

1. [Problem Statement](#1-problem-statement)
2. [NaN-Boxing Encoding Scheme](#2-nan-boxing-encoding-scheme)
3. [Memory Management Strategy](#3-memory-management-strategy)
4. [Custom GC Design](#4-custom-gc-design)
5. [Migration Path](#5-migration-path)
6. [Impact Analysis](#6-impact-analysis)
7. [Expected Performance Gains](#7-expected-performance-gains)
8. [Risk Assessment](#8-risk-assessment)
9. [Work Breakdown](#9-work-breakdown)

---

## 1. Problem Statement

### Current State

GScript's `Value` is a 24-byte tagged union:

```
type Value struct {
    typ  ValueType       // offset 0:  1 byte + 7 padding
    data uint64          // offset 8:  int/float/bool payload
    ptr  unsafe.Pointer  // offset 16: GC-visible pointer for ref types
}
```

LuaJIT's `TValue` is an 8-byte NaN-boxed `uint64`. Every value -- number, boolean, nil, pointer -- fits in 8 bytes.

### Why This Matters

The 3x size difference creates cascading performance problems:

| Problem | Mechanism | Benchmarks Affected |
|---------|-----------|-------------------|
| **Memory bandwidth** | Array of 300 Values = 7,200B vs 2,400B. 3x more cache misses on every table scan. | matmul (51x), spectral_norm (83x) |
| **GC pressure** | Every `[]Value` slice contains `unsafe.Pointer` fields. Go GC must scan every element for pointer liveness. A 300x300 matrix = 90,000 Values = 90,000 pointer fields to scan. | binary_trees (7.4x), matmul, nbody |
| **Table struct overhead** | `Table.array` is `[]Value` (24B/element). `Table.svals` is `[]Value`. String-keyed field access carries 24B per value. | nbody (64x), method_dispatch (80x) |
| **Register spill cost** | JIT spills/reloads 24B per Value (3 stores/loads). NaN-boxed = 1 store/load. | All JIT'd benchmarks |
| **Allocation rate** | `StringValue()` allocates a `*string` on Go heap per string construction. Every new Table allocates `[]Value`. | string_bench, closure_bench |

### The Gap (Current vs LuaJIT)

| Benchmark | Current Gap | Root Cause |
|-----------|-------------|------------|
| matmul(300) | **51x** | 3-level nested loop, 2D table-of-tables, 24B array read/write |
| spectral_norm(500) | **83x** | Nested loops, array indexing, function calls with table args |
| nbody(500K) | **64x** | Field access (`.x`, `.y`, `.z`, `.vx`, ...) on body tables, 24B per field |
| method_dispatch(100K) | **80x** | Object pattern: table field read/write + function calls |
| fannkuch(9) | **31x** | Permutation arrays, heavy integer array manipulation |
| binary_trees(15) | **7.4x** | Deep recursion, massive table allocation, GC pressure |

All these benchmarks are **table-heavy**. The compute-heavy benchmarks (fib, ackermann, mandelbrot) where JIT operates on unboxed int/float registers are already at or near LuaJIT parity.

---

## 2. NaN-Boxing Encoding Scheme

### IEEE 754 Background

A `float64` is 64 bits: 1 sign + 11 exponent + 52 mantissa.

NaN occurs when all 11 exponent bits are 1 and the mantissa is non-zero. A **quiet NaN** (qNaN) additionally has the highest mantissa bit (bit 51) set. This means any `uint64` with bits 51-62 all set to 1 is a qNaN, and bits 0-50 (51 bits) are free payload. We also have the sign bit (bit 63) available for tagging.

**Key insight**: There are 2^51 * 2 - 2 possible quiet NaN bit patterns, but actual floating-point operations only produce a single canonical NaN (`0x7FF8000000000000`). The rest of the NaN space is ours to use.

### GScript NaN-Boxing Layout

```
Bit layout of a NaN-boxed Value (uint64):

63  62       52 51 50 49 48 47                          0
 S  EEEEEEEEEEE Q  T  T  T  PPPPPPPPPPPPPPPPPPPPPPPPPPPP...P
 ^  ^           ^  ^        ^
 |  exponent    |  tag(3b)  payload (48 bits)
 sign           quiet NaN bit

Double (normal IEEE 754 float64):
  Any bit pattern where bits 52-62 are NOT all 1, OR mantissa is 0.
  i.e., the value is NOT a NaN.

Tagged value (non-double):
  Bits 51-62 = all 1 (quiet NaN indicator)
  Bit 50 = 1 (distinguish from canonical NaN / HW-generated NaN)
  Bits 48-49 + bit 63 = 3-bit type tag (8 possible types)
  Bits 0-47 = 48-bit payload
```

### Encoding Table

```
Type        | Bit 63 | Bits 50-62    | Bits 48-49 | Bits 0-47 (payload)
------------|--------|---------------|------------|--------------------
float64     | (any)  | NOT all-1s    | (any)      | (IEEE 754 mantissa)
nil         | 1      | 1111111111111 | 00         | 000...0
false       | 1      | 1111111111111 | 01         | 000...0
true        | 1      | 1111111111111 | 01         | 000...1
int48       | 1      | 1111111111111 | 10         | 48-bit signed int
pointer     | 1      | 1111111111111 | 11         | 48-bit pointer
```

**Constant definitions** (as uint64):

```go
const (
    // NaN-box marker: bits 50-62 all set = quiet NaN with bit 50
    nanBits   uint64 = 0x7FFC000000000000  // bits 50-62 = 1

    // Tag base: sign bit + NaN bits
    tagBase   uint64 = 0xFFFC000000000000  // sign=1 + bits 50-62=1

    // Type tags (3 bits: sign + bits 48-49)
    tagNil    uint64 = 0xFFFC000000000000  // sign=1, tag=00
    tagBool   uint64 = 0xFFFD000000000000  // sign=1, tag=01
    tagInt    uint64 = 0xFFFE000000000000  // sign=1, tag=10
    tagPtr    uint64 = 0xFFFF000000000000  // sign=1, tag=11

    // Masks
    tagMask     uint64 = 0xFFFF000000000000  // top 16 bits
    payloadMask uint64 = 0x0000FFFFFFFFFFFF  // bottom 48 bits

    // Special values
    valNil    uint64 = tagNil                // nil
    valFalse  uint64 = tagBool               // false (payload=0)
    valTrue   uint64 = tagBool | 1           // true (payload=1)
)
```

### Type Checking (Fast Paths)

```go
type Value uint64

func (v Value) IsFloat() bool  { return v & nanBits != nanBits }
// ANY value where bits 50-62 are not all 1 is a valid float64.
// This single check distinguishes float from all tagged types.

func (v Value) IsNil() bool    { return uint64(v) == valNil }
func (v Value) IsBool() bool   { return uint64(v) & tagMask == tagBool }
func (v Value) IsInt() bool    { return uint64(v) & tagMask == tagInt }
func (v Value) IsPtr() bool    { return uint64(v) & tagMask == tagPtr }
func (v Value) IsNumber() bool { return v.IsFloat() || v.IsInt() }
func (v Value) Truthy() bool   { return uint64(v) != valNil && uint64(v) != valFalse }
```

### Value Extraction

```go
func (v Value) Float() float64 {
    return math.Float64frombits(uint64(v))
}

func (v Value) Int() int64 {
    // Sign-extend 48-bit integer to 64-bit
    raw := uint64(v) & payloadMask
    if raw & (1 << 47) != 0 {
        return int64(raw | 0xFFFF000000000000) // sign extend
    }
    return int64(raw)
}

func (v Value) Bool() bool {
    return uint64(v) & 1 != 0
}

func (v Value) Ptr() unsafe.Pointer {
    return unsafe.Pointer(uintptr(uint64(v) & payloadMask))
}
```

### Value Construction

```go
func FloatValue(f float64) Value {
    bits := math.Float64bits(f)
    // Safety check: if a float64 bit pattern happens to have bits 50-62 all set,
    // it would collide with our tag space. In practice this never happens because:
    //   - IEEE 754 hardware NaN has bit 50 = 0 (e.g., 0x7FF8000000000001)
    //   - -Inf (0xFFF0...) has bits 50-51 = 0
    //   - No normal/subnormal float has exponent bits all 1
    // We canonicalize anyway for defense-in-depth.
    if bits & nanBits == nanBits {
        return Value(math.Float64bits(math.NaN())) // Go's canonical NaN (bit 50 = 0)
    }
    return Value(bits)
}

func IntValue(i int64) Value {
    return Value(tagInt | (uint64(i) & payloadMask))
}

func BoolValue(b bool) Value {
    if b { return Value(valTrue) }
    return Value(valFalse)
}

func NilValue() Value {
    return Value(valNil)
}

func PtrValue(p unsafe.Pointer) Value {
    return Value(tagPtr | uint64(uintptr(p)))
}
```

### Integer Range

48-bit signed integer range: **-140,737,488,355,328 to +140,737,488,355,327** (~140 trillion).

This is sufficient for virtually all scripting use cases. For comparison, Lua 5.3 integers are 64-bit, but LuaJIT uses 32-bit integers in NaN-boxed mode.

GScript currently uses `int64`. With 48-bit NaN-boxing, we handle the common case (loop indices, array indices, counters, flags) in 48 bits. For the rare case where a user needs full 64-bit range, we can either:
- **Option A**: Promote to float64 (loses precision beyond 2^53 but matches Lua semantics)
- **Option B**: Box into a heap-allocated int64 (like LuaJIT's cdata)
- **Recommended**: Option A -- promote to float64. This matches LuaJIT behavior and keeps the common path fast. Add a runtime check: if int overflow would truncate, fall back to float64.

### Pointer Type Discrimination

A single `tagPtr` covers all GC'd object types (Table, Closure, String, Coroutine, Channel). We need to distinguish them. Two approaches:

**Approach A: Object Header Tag** (Recommended)

Every heap-allocated GScript object starts with a common header:

```go
type GCHeader struct {
    Mark  uint8  // GC mark bits
    OType uint8  // object type: OTypeTable, OTypeClosure, OTypeString, ...
}
```

To determine the type of a pointer value, dereference and read `OType`. This costs one memory load but keeps `Value` at exactly 8 bytes.

**Approach B: Steal More Tag Bits**

Use 4 tag bits instead of 2, encoding object sub-type in the tag. This reduces pointer payload to 47 bits. Feasible on current macOS ARM64 (pointers use ~41 bits), but riskier for future compatibility.

**Decision**: Approach A. The extra dereference for type discrimination is only needed in slow paths (type assertions, `type()` function, error messages). Hot paths (arithmetic, table indexing) already know the type from the operation context (e.g., GETTABLE always expects a Table at R(B)).

### Pointer Width Safety

Verified experimentally on macOS ARM64 (Apple Silicon):
- **Heap pointers**: 41 bits needed (e.g., `0x000001400019ceb0`)
- **mmap pointers**: 33 bits needed (e.g., `0x0000000103148000`)
- **47-bit max**: `0x00007FFFFFFFFFFF` = 128 TB

macOS ARM64 currently uses well under 47 bits. Even with future 48-bit VA support, we have `payloadMask` covering the full 48 bits, and our tag starts at bit 48. With mmap-based custom allocation, we control the address range and can guarantee pointers fit in 47 bits.

---

## 3. Memory Management Strategy

### The Core Problem

NaN-boxed pointers are stored as `uint64`. Go's GC scans memory for `unsafe.Pointer` and `*T` fields to determine liveness. A `uint64` field is invisible to the GC. If we store a `*Table` pointer inside a `uint64` (our NaN-boxed Value), the GC cannot see it and will collect the Table.

### Strategy Selection

| Strategy | Pros | Cons | Verdict |
|----------|------|------|---------|
| **A: mmap custom heap** | Full control, zero GC overhead, no CGO cost | Must implement our own GC, complex memory management | **Selected** |
| B: CGO + C malloc | GC-invisible, standard allocator | CGO call overhead (~100ns/call), complex build | Rejected |
| C: Go heap + KeepAlive | Simple, Go GC handles collection | Still 24B Value for GC visibility, defeats purpose | Rejected |
| D: Go arena (experimental) | Go-native, decent perf | API unstable, removed in future Go, still scanned | Rejected |

### Architecture: mmap-Based Custom Heap

```
+--------------------------------------------------------------+
|                    GScript Runtime                             |
+--------------------------------------------------------------+
|                                                                |
|  VM registers: []uint64  (NaN-boxed Values)                  |
|  Globals:      []uint64  (NaN-boxed Values)                  |
|  Call stack:   []CallFrame                                    |
|                                                                |
|  All pointers in uint64 are invisible to Go GC.              |
|  Go GC does NOT manage GScript objects.                       |
|                                                                |
+--------------------------------------------------------------+
         |  points into
         v
+--------------------------------------------------------------+
|                    GScript Heap (mmap'd)                      |
+--------------------------------------------------------------+
|  Region 0:  [GCHeader|Table data...] [GCHeader|Table...]     |
|  Region 1:  [GCHeader|Closure...]    [GCHeader|String...]    |
|  Region 2:  [GCHeader|Coroutine...]                          |
|  ...                                                          |
+--------------------------------------------------------------+
|  Managed by GScript GC (mark-sweep), NOT Go GC               |
|  Allocated via bump allocator within mmap'd pages             |
|  Freed by GScript sweep phase                                 |
+--------------------------------------------------------------+
```

### Heap Allocator Design

**Arena-style bump allocator** with size-class segregation:

```go
type Heap struct {
    // Size-class arenas (power-of-2 sizes)
    small  [8]*Arena  // 32B, 64B, 128B, 256B, 512B, 1KB, 2KB, 4KB
    large  *LargeAlloc // > 4KB objects (linked list of mmap'd blocks)

    // Statistics
    totalAlloc  uint64
    totalFreed  uint64
    numGC       int
}

type Arena struct {
    // Current allocation page
    page    []byte          // mmap'd memory (e.g., 1MB chunk)
    cursor  uintptr         // bump pointer (next free offset)
    limit   uintptr         // end of current page

    // Free list for recycled objects (after GC sweep)
    freeList unsafe.Pointer // singly-linked free list

    // All pages (for sweep)
    pages   [][]byte

    sizeClass int           // object size for this arena
}
```

**Allocation fast path** (hot, inlined):

```go
func (a *Arena) Alloc() unsafe.Pointer {
    // Try free list first (O(1))
    if a.freeList != nil {
        p := a.freeList
        a.freeList = *(*unsafe.Pointer)(p)
        return p
    }
    // Bump allocate (O(1))
    if a.cursor + uintptr(a.sizeClass) <= a.limit {
        p := unsafe.Pointer(a.cursor)
        a.cursor += uintptr(a.sizeClass)
        return p
    }
    // Slow path: allocate new page
    return a.allocSlow()
}
```

### Object Layout

All GScript heap objects share a common header for GC and type identification:

```go
// GCHeader is at the start of every heap object (8 bytes)
type GCHeader struct {
    Mark    uint8   // 0=white, 1=gray, 2=black (tri-color)
    OType   uint8   // object type tag
    Flags   uint16  // reserved (concurrent flag, etc.)
    _pad    uint32  // alignment padding
}

const (
    OTypeTable     uint8 = 1
    OTypeClosure   uint8 = 2
    OTypeString    uint8 = 3
    OTypeCoroutine uint8 = 4
    OTypeChannel   uint8 = 5
    OTypeUpvalue   uint8 = 6
)
```

**Table layout on custom heap** (all in one flat allocation):

```
+----------+--------+--------+--------+--------+--------+
| GCHeader | NArr   | NCap   | NHash  | HCap   | meta   |
|  8 bytes | 4B     | 4B     | 4B     | 4B     | 8B(ptr)|
+----------+--------+--------+--------+--------+--------+
| array part: uint64[NCap]                               |
| (NaN-boxed Values, inline, no separate allocation)     |
+--------------------------------------------------------+
| hash part: Entry[HCap]                                 |
| (key-value pairs for string/mixed keys, inline)        |
+--------------------------------------------------------+
```

This is dramatically different from the current Table which has 7+ separate slice/map allocations. The flat layout means:
- **One allocation per table** (vs 3-7 allocations currently)
- **Cache-friendly**: array and hash data are adjacent in memory
- **Zero GC scan overhead**: everything is in the mmap heap

**String layout**:

```
+----------+------+--------+
| GCHeader | Len  | Data[] |
|  8 bytes | 8B   | Len B  |
+----------+------+--------+
```

Strings are immutable and stored inline. The `Value` pointer points to the `GCHeader`. Hash for string interning can be stored in `Flags` or an additional field.

### Go/GScript Boundary

At the boundary between Go code and GScript objects, we need conversion:

```go
// Go world: runtime.Table (Go-managed, for stdlib functions)
// GScript world: *GCTable (mmap-managed, for VM/JIT)

// Stdlib functions receive Values and need to work with tables.
// Two options:
//   1. Stdlib operates directly on GCTable via unsafe pointer arithmetic
//   2. Stdlib converts to Go-native types at the boundary

// Recommended: Thin wrapper API that operates on GCTable directly
func TableRawGetInt(v Value, key int64) Value {
    tbl := (*GCTable)(v.Ptr())
    // Direct memory access into mmap'd table
    ...
}
```

---

## 4. Custom GC Design

### Why We Need Our Own GC

Objects on the mmap'd heap are invisible to Go's GC. Without our own GC, memory is never freed. GScript programs that create tables in loops (matmul, binary_trees) would OOM immediately.

### Algorithm: Incremental Tri-Color Mark-Sweep

LuaJIT uses an incremental, non-moving, mark-sweep collector. We follow the same approach.

**Phase 1: Mark**

```
Root set:
  - VM registers ([]uint64): scan for pointer-tagged Values
  - Global variables ([]uint64): scan for pointer-tagged Values
  - Call stack upvalues: scan Upvalue.val (now uint64)
  - Open upvalues list
  - String metatable

Algorithm (tri-color):
  1. Mark all roots GRAY, push to gray stack
  2. Pop GRAY objects, mark BLACK:
     - Table: scan array part (each uint64, check for tagPtr),
              scan hash part, scan metatable pointer
     - Closure: scan upvalues, scan environment
     - Coroutine: scan its register file, call stack
  3. Repeat until gray stack is empty
```

**Phase 2: Sweep**

```
For each Arena page:
  Walk through objects at fixed-size intervals:
    If object is WHITE (unmarked) → add to free list
    If object is BLACK → reset to WHITE for next cycle
```

### GC Triggering

```go
const (
    gcThreshold    = 1 << 20  // 1MB: trigger first GC
    gcGrowthFactor = 2        // trigger when heap doubles
)

func (h *Heap) maybeGC() {
    if h.totalAlloc - h.totalFreed > h.nextGCThreshold {
        h.collectGarbage()
        h.nextGCThreshold = (h.totalAlloc - h.totalFreed) * gcGrowthFactor
    }
}
```

### Incremental Scheduling

To avoid stop-the-world pauses, the GC runs incrementally:

```
Each N allocations (e.g., N=100):
  Run K mark steps (e.g., K=20 objects marked)

When mark phase complete:
  Run sweep (can also be incremental, sweeping one page at a time)
```

**Write barrier**: When a BLACK object is mutated to point to a WHITE object, we must mark the target GRAY. This is needed for table field assignment.

```go
func (h *Heap) writeBarrier(parent, child unsafe.Pointer) {
    if h.gcPhase == gcMarking {
        parentHeader := (*GCHeader)(parent)
        childHeader := (*GCHeader)(child)
        if parentHeader.Mark == markBlack && childHeader.Mark == markWhite {
            childHeader.Mark = markGray
            h.grayStack = append(h.grayStack, child)
        }
    }
}
```

### Finalization

Go objects that wrap external resources (file handles in stdlib, HTTP connections) need cleanup. The GScript GC must support finalizers for these cases. Implementation: maintain a list of objects with registered finalizers; call them during sweep.

---

## 5. Migration Path

### Strategy: Parallel Implementation with Adapter Layer

A big-bang rewrite (replace `Value` everywhere at once) is too risky. Instead, we build the NaN-boxed system alongside the existing one and migrate incrementally.

### Phase S2.0: Foundation (No behavior change)

**New package**: `internal/nanbox/`

Build the NaN-boxing primitives and custom heap in isolation, fully tested:

```
internal/nanbox/
    value.go          // NaN-boxed Value type (uint64) + constructors + accessors
    value_test.go     // Exhaustive encoding/decoding tests
    heap.go           // mmap arena allocator
    heap_test.go      // Allocation/deallocation tests
    gc.go             // Mark-sweep collector
    gc_test.go        // GC correctness tests (roots, reachability)
    table.go          // NaN-boxed Table (flat layout on custom heap)
    table_test.go     // Table get/set/iteration tests
    string.go         // Interned string on custom heap
    closure.go        // Closure object on custom heap
```

**Exit criteria**: All unit tests pass. Micro-benchmarks show expected perf characteristics (8B Value, O(1) alloc, correct GC collection).

### Phase S2.1: VM Registers as uint64

Change the VM register file from `[]runtime.Value` to `[]uint64`:

```go
// Before:
type VM struct {
    regs []runtime.Value  // 24B per register
}

// After:
type VM struct {
    regs []uint64  // 8B per register (NaN-boxed)
}
```

This is the smallest change that validates end-to-end integration:
- Bytecode interpreter operates on `[]uint64` directly
- At Go function call boundaries, convert `uint64` <-> `runtime.Value`
- Table operations still use the existing `runtime.Table` (with adapter)
- JIT value_layout.go changes `ValueSize` from 24 to 8

**Adapter layer**:

```go
// Convert between old Value and new NaN-boxed uint64
func ValueToNanBox(v runtime.Value) uint64 { ... }
func NanBoxToValue(n uint64) runtime.Value { ... }

// Stdlib wrapper: converts args at boundary
func wrapStdlib(fn func([]runtime.Value) ([]runtime.Value, error)) func([]uint64) ([]uint64, error) {
    return func(args []uint64) ([]uint64, error) {
        oldArgs := make([]runtime.Value, len(args))
        for i, a := range args {
            oldArgs[i] = NanBoxToValue(a)
        }
        results, err := fn(oldArgs)
        if err != nil {
            return nil, err
        }
        newResults := make([]uint64, len(results))
        for i, r := range results {
            newResults[i] = ValueToNanBox(r)
        }
        return newResults, nil
    }
}
```

**Key concern**: During this phase, Go's GC still manages Tables, Closures, etc. The NaN-boxed `uint64` registers hide pointers from the GC. We need a **shadow root set** -- a `[]unsafe.Pointer` that mirrors all pointer Values in registers, keeping them alive for Go's GC:

```go
type VM struct {
    regs    []uint64           // NaN-boxed registers (primary)
    gcRoots []unsafe.Pointer   // shadow: keeps ptr-tagged values alive for Go GC
}
```

This is temporary scaffolding, removed when objects move to the custom heap.

**Exit criteria**: All integration tests pass. VM benchmarks show 3x improvement in register read/write bandwidth. No GC crashes.

### Phase S2.2: Custom Heap Tables

Migrate Table allocation to the mmap custom heap:

1. New `GCTable` struct allocated on custom heap
2. `table.go` operations work on `GCTable` via `unsafe.Pointer`
3. Custom GC collects unreachable tables
4. `runtime.Table` becomes a thin Go wrapper around `GCTable` for stdlib

**This is the hardest phase.** The Table struct has the most complex layout and the most callers (every stdlib function that creates/reads tables).

**Exit criteria**: matmul, nbody, spectral_norm run correctly with custom-heap Tables. GC correctly collects tables. No memory leaks. Benchmark improvement visible.

### Phase S2.3: Custom Heap Strings, Closures, Upvalues

Migrate remaining GC-managed types to the custom heap:

- Strings: interned on custom heap, Value stores pointer directly
- Closures: allocated on custom heap, upvalue list inline
- Upvalues: allocated on custom heap
- Coroutines: remain on Go heap (they use Go goroutines internally)
- Channels: remain on Go heap (they wrap `chan Value`)

**After this phase**: The shadow root set (`gcRoots`) can be removed. All pointer-tagged Values point into the mmap heap. Go GC has nothing to scan.

**Exit criteria**: Full benchmark suite passes. No gcRoots needed. GScript GC handles all object lifetime.

### Phase S2.4: JIT NaN-Boxing

Update the JIT codegen for 8-byte Values:

```
Before (24B Value):
  LDR X0, [regRegs, #offset+8]    // load data field
  STRB W0, [regRegs, #offset]     // store type tag
  STR X0, [regRegs, #offset+8]    // store data
  STR XZR, [regRegs, #offset+16]  // clear ptr

After (8B Value):
  LDR X0, [regRegs, #offset]      // load NaN-boxed value
  STR X0, [regRegs, #offset]      // store NaN-boxed value
```

Key changes:
- `ValueSize` = 8 (was 24)
- `ValueOffset(reg)` = `reg * 8` (was `reg * 24`)
- `EmitMulValueSize()` = single LSL #3 (was ADD+LSL combo)
- Type guards compare tag bits of a uint64, not load a byte
- Unbox int: AND mask + sign-extend (was just load data field)
- Unbox float: no-op (the uint64 IS the float64 bits)
- Box int: OR with `tagInt` constant
- Box float: NaN canonicalization check
- Table field access: single 8B load/store (was 24B)

**Exit criteria**: JIT produces correct results. mandelbrot, sieve, fib benchmarks match interpreter. Table-heavy benchmarks show major improvement.

### Phase S2.5: Stdlib Migration

Remove the adapter layer. Stdlib functions operate directly on `uint64` (NaN-boxed) Values:

```go
// Before:
func mathSqrt(args []runtime.Value) ([]runtime.Value, error) {
    x := args[0].Number()
    return []runtime.Value{runtime.FloatValue(math.Sqrt(x))}, nil
}

// After:
func mathSqrt(args []uint64) ([]uint64, error) {
    v := nanbox.Value(args[0])
    x := v.Number()
    return []uint64{uint64(nanbox.FloatValue(math.Sqrt(x)))}, nil
}
```

This touches ~600 function signatures across ~33 stdlib files. It is tedious but mechanical -- a good candidate for scripted transformation.

**Exit criteria**: All stdlib tests pass. No conversion overhead at Go/GScript boundary.

---

## 6. Impact Analysis

### Files Affected

| Component | Files | Nature of Change |
|-----------|-------|-----------------|
| **runtime/value.go** | 1 | Complete rewrite: Value becomes `uint64` |
| **runtime/table.go** | 1 | Major rewrite: GCTable on custom heap |
| **runtime/closure.go** | 1 | Rewrite: GCClosure on custom heap |
| **runtime/environment.go** | 1 | Rewrite: Upvalue uses NaN-boxed Values |
| **runtime/coroutine.go** | 1 | Moderate: Value fields become uint64 |
| **runtime/channel.go** | 1 | Moderate: chan Value -> chan uint64 |
| **runtime/stdlib_*.go** | 33 | Mechanical: Value -> uint64 function signatures |
| **vm/vm.go** | 1 | Major: register file, all opcodes |
| **vm/compiler.go** | 1 | Moderate: constant pool encoding |
| **vm/proto.go** | 1 | Moderate: Constants []Value -> []uint64 |
| **jit/value_layout.go** | 1 | Rewrite: ValueSize=8, new offsets |
| **jit/ssa_codegen.go** | 1 | Major: all load/store/guard codegen |
| **jit/ssa.go** | 1 | Moderate: SSA type system alignment |
| **jit/trace.go** | 1 | Moderate: TraceIR with NaN-boxed values |
| **jit/executor.go** | 1 | Moderate: execution interface |
| **jit/codegen.go** | 1 | Major: method JIT codegen |
| **gscript/\*.go** | 5 | Moderate: public API, VM wrapper |
| **tests/\*.go** | 5+ | Update test expectations |
| **NEW: nanbox/\*.go** | 6+ | New package: value, heap, gc, table, string, closure |
| **Total** | ~60 files | |

### Unchanged Components

- Lexer, Parser, AST (no Value involvement)
- JIT assembler (ARM64 instruction encoding unchanged)
- JIT SSA optimization passes (constant hoisting, CSE, FMA fusion)
- JIT register allocator (slot-based allocation unchanged)
- Benchmark scripts and suite files (.gs, .lua)

---

## 7. Expected Performance Gains

### Analytical Model

Three primary gain sources:

**1. Memory bandwidth (3x reduction)**

`[]Value` array reads/writes go from 24B to 8B per element. For cache-line aligned access, 8 values fit in a 64B cache line (vs 2.67 values before).

**2. GC elimination**

Go's GC currently scans every `[]Value` slice for pointer fields. With NaN-boxing on custom heap, Go GC sees zero pointer fields in the register file and table arrays. GC pause time drops to near-zero for GScript workloads.

**3. Allocation speed**

Bump allocation on mmap'd arena: ~2-5ns per object (single pointer increment).
Go heap allocation: ~25-50ns per object (with GC write barriers, size class lookup).

### Per-Benchmark Projections

| Benchmark | Current | Expected After | Reasoning |
|-----------|---------|---------------|-----------|
| **matmul(300)** | 1.120s (51x) | ~0.15-0.25s (7-11x) | Inner loop: 2D array indexing. 3x bandwidth + zero GC. JIT can emit 8B loads. |
| **spectral_norm(500)** | 0.660s (83x) | ~0.08-0.15s (10-19x) | Array indexing + function calls. Massive bandwidth win. |
| **nbody(500K)** | 2.376s (64x) | ~0.20-0.40s (5-11x) | Field access (.x,.y,.z,.vx,...) = 8B loads. Flat table layout = cache friendly. |
| **method_dispatch(100K)** | 0.080s (80x) | ~0.01-0.02s (10-20x) | new_point creates table (1 bump alloc), field reads = 8B. |
| **fannkuch(9)** | 0.588s (31x) | ~0.08-0.15s (4-8x) | Integer array permutation. 8B per element. |
| **binary_trees(15)** | 1.255s (7.4x) | ~0.30-0.50s (2-3x) | Table allocation: 1 bump alloc. GC: mark-sweep handles tree structure. |
| **sort(50K)** | 0.158s (13x) | ~0.03-0.05s (3-4x) | Array read/write + comparisons. |
| **sieve(1M)** | 0.080s (7.3x) | ~0.03-0.05s (3-5x) | Already uses ArrayBool (1B/element). Minor benefit. |
| **fib(35)** | 0.026s (~1x) | 0.026s (~1x) | Pure computation, already at parity. No table ops. |
| **mandelbrot(1000)** | 0.155s (2.7x) | 0.155s (2.7x) | Pure float computation. NaN-boxing won't help (separate JIT improvements needed). |

### Aggregate Projection

Table-heavy benchmarks should improve from **50-83x gap** to **5-20x gap**, a **4-10x improvement**. Combined with future JIT improvements (function inlining for table ops, type-specialized field access), reaching **2-5x of LuaJIT** on these benchmarks is realistic.

Reaching full LuaJIT parity on table-heavy benchmarks requires additional work beyond NaN-boxing:
- Function inlining in trace JIT (eliminate call overhead for `A(i,j)` in spectral_norm)
- Inline field caching in JIT (eliminate table lookup overhead)
- Object shape / hidden class optimization (as in V8)

---

## 8. Risk Assessment

### Risk 1: Go GC Interaction (HIGH)

**Risk**: Hiding pointers from Go's GC in uint64 values causes use-after-free or memory corruption.

**Mitigation**:
- Phase S2.1 uses shadow root set to keep Go-managed objects alive
- Phase S2.2+ moves objects to mmap heap (Go GC not involved)
- Extensive testing with GOGC=1 (aggressive GC) during development
- AddressSanitizer-equivalent checks in debug builds

### Risk 2: Custom GC Correctness (HIGH)

**Risk**: Mark-sweep GC misses reachable objects or fails to collect dead ones. Memory leaks or use-after-free.

**Mitigation**:
- Start with simple stop-the-world mark-sweep (correct first, incremental later)
- Comprehensive GC test suite: cyclic references, weak tables, finalizers, stress tests
- GC verification mode: double-check that all freed objects are truly unreachable
- Conservative approach: if in doubt, don't collect

### Risk 3: Integer Range Truncation (MEDIUM)

**Risk**: 48-bit integers silently truncate values that exceed the range, producing incorrect results.

**Mitigation**:
- Runtime overflow check on IntValue(): if value exceeds 48-bit range, promote to float64
- Log warning in debug mode
- Benchmark suite validates all results match interpreter

### Risk 4: NaN Canonicalization (MEDIUM)

**Risk**: Float operations produce NaN values that collide with our tag bits, causing type confusion.

**Mitigation**:
- `FloatValue()` canonicalizes all NaN outputs to `0x7FF8000000000000`
- `IsFloat()` check is `bits & nanBits != nanBits` -- only our tagged values have ALL of bits 50-62 set
- Hardware NaN is `0x7FF8000000000000` which has bit 50 = 0, so it never collides with our tags (which require bit 50 = 1)

### Risk 5: Stdlib Migration Volume (MEDIUM)

**Risk**: 33 stdlib files, ~600 function signatures to update. Risk of subtle conversion bugs.

**Mitigation**:
- Adapter layer (Phase S2.1-S2.3) allows gradual migration
- Automated code transformation where possible
- Existing test suite catches conversion errors
- Migrate and test one stdlib module at a time

### Risk 6: Platform Portability (LOW)

**Risk**: NaN-boxing assumes 48-bit pointer width. Future ARM64 with 52-bit VA would break.

**Mitigation**:
- macOS ARM64 currently uses 41 bits maximum
- Custom mmap heap: we control allocation address range
- Can use `MAP_FIXED` or address hints to ensure addresses stay within 47 bits
- Linux ARM64 52-bit VA is opt-in (requires `mmap` hint > 48 bits)
- If ever needed: reduce to 47-bit payload, use 1 extra tag bit

### Risk 7: Debugging Difficulty (MEDIUM)

**Risk**: Raw uint64 values are unreadable in debugger. Memory corruption in mmap heap is harder to diagnose than Go heap issues.

**Mitigation**:
- `Value.String()` method for human-readable display
- Debug dump functions: `DumpRegisters()`, `DumpTable()`, `DumpHeap()`
- GC verification mode with full heap walks
- Observation-driven debugging (Rule 2 from CLAUDE.md)

---

## 9. Work Breakdown

The work is organized in strict dependency order. Each phase has clear entry/exit criteria.

### Phase S2.0: Foundation

**Scope**: New `internal/nanbox/` package with NaN-boxed Value, custom heap, and GC.

| Task | Description |
|------|-------------|
| S2.0.1 | NaN-boxed `Value` type: constructors, accessors, type checks |
| S2.0.2 | Value encoding exhaustive tests: roundtrip all types, edge cases (NaN, Inf, max int) |
| S2.0.3 | mmap arena allocator: size-class arenas, bump allocation, free list |
| S2.0.4 | GCHeader + object types: Table, Closure, String |
| S2.0.5 | Mark-sweep GC: root scanning, mark phase, sweep phase |
| S2.0.6 | GC tests: basic collection, cyclic refs, stress tests |
| S2.0.7 | NaN-boxed Table: flat layout, array part, hash part |
| S2.0.8 | NaN-boxed String: interned, immutable, on custom heap |
| S2.0.9 | Micro-benchmarks: alloc throughput, table get/set, GC pause time |

### Phase S2.1: VM Registers

**Scope**: VM register file becomes `[]uint64`. Adapter layer at boundaries.

| Task | Description |
|------|-------------|
| S2.1.1 | Change `VM.regs` from `[]runtime.Value` to `[]uint64` |
| S2.1.2 | Shadow root set for Go GC visibility |
| S2.1.3 | Update all bytecode ops in `vm.go` to use NaN-boxed operations |
| S2.1.4 | Adapter layer: `ValueToNanBox` / `NanBoxToValue` at stdlib boundary |
| S2.1.5 | Update constants pool (`FuncProto.Constants`) to `[]uint64` |
| S2.1.6 | Integration tests: full benchmark suite passes |

### Phase S2.2: Custom Heap Tables

**Scope**: Table objects allocated on custom heap.

| Task | Description |
|------|-------------|
| S2.2.1 | `GCTable` struct on custom heap with flat array+hash layout |
| S2.2.2 | Table operations (RawGet, RawSet, RawGetInt, RawGetString, etc.) |
| S2.2.3 | Table iteration (Next, rebuildKeys) |
| S2.2.4 | Metatable support |
| S2.2.5 | Remove shadow root set for table pointers (GScript GC manages) |
| S2.2.6 | Stdlib table operations: table.insert, table.remove, etc. |
| S2.2.7 | Integration tests + correctness verification |

### Phase S2.3: Remaining Types

**Scope**: Strings, closures, upvalues on custom heap.

| Task | Description |
|------|-------------|
| S2.3.1 | GCString on custom heap |
| S2.3.2 | GCClosure + GCUpvalue on custom heap |
| S2.3.3 | Remove shadow root set entirely |
| S2.3.4 | String interning (optional, perf optimization) |
| S2.3.5 | Integration tests |

### Phase S2.4: JIT Update

**Scope**: JIT codegen for 8-byte NaN-boxed Values.

| Task | Description |
|------|-------------|
| S2.4.1 | `value_layout.go`: ValueSize=8, new offset constants |
| S2.4.2 | SSA codegen: type guards via tag bit comparison |
| S2.4.3 | SSA codegen: unbox/box via bitwise ops |
| S2.4.4 | SSA codegen: table array load/store at 8B stride |
| S2.4.5 | SSA codegen: table field load/store |
| S2.4.6 | Method JIT: update for 8B Values |
| S2.4.7 | Trace recorder: capture NaN-boxed types |
| S2.4.8 | JIT integration tests + benchmark verification |

### Phase S2.5: Stdlib Cleanup

**Scope**: Remove adapter layer, native NaN-boxed stdlib.

| Task | Description |
|------|-------------|
| S2.5.1 | Update GoFunction signature to use NaN-boxed Values |
| S2.5.2 | Migrate stdlib files (batch: math, string, table, io, ...) |
| S2.5.3 | Update public API (`gscript/` package) |
| S2.5.4 | Remove all conversion scaffolding |
| S2.5.5 | Final benchmark run + blog post |

### Dependency Graph

```
S2.0 (Foundation)
  |
  v
S2.1 (VM Registers)
  |
  +---> S2.2 (Custom Heap Tables)
  |       |
  |       v
  |     S2.3 (Remaining Types)
  |       |
  +-------+
  |
  v
S2.4 (JIT Update) -- depends on S2.1 at minimum, S2.2 for table codegen
  |
  v
S2.5 (Stdlib Cleanup) -- depends on everything above
```

---

## Appendix A: LuaJIT Comparison

| Feature | LuaJIT | GScript (Planned) |
|---------|--------|------------------|
| Value size | 8B (NaN-boxed) | 8B (NaN-boxed) |
| Integer width | 32-bit (in NaN payload) | 48-bit (in NaN payload) |
| GC | Incremental mark-sweep, C heap | Incremental mark-sweep, mmap heap |
| Table layout | C struct, array+hash inline | Flat mmap'd, array+hash inline |
| String interning | Yes (hash+chain) | Planned (Phase S2.3.4) |
| Write barrier | Explicit (setgcref macros) | Explicit (writeBarrier function) |
| Memory allocator | Custom (lj_alloc, dlmalloc-based) | Custom (arena bump + free list) |

## Appendix B: Why Not `runtime.KeepAlive` (Option C)

The naive approach: store NaN-boxed pointers in `uint64` registers, but keep a parallel `[]unsafe.Pointer` to prevent Go GC from collecting the objects.

Problems:
1. **Still pays GC scan cost**: Go GC must scan the `[]unsafe.Pointer` slice. For a program with 100K live objects, this is ~100K pointer dereferences per GC cycle. The whole point of NaN-boxing is to eliminate GC overhead.
2. **Synchronization overhead**: Every table/string allocation must update the root set. Every GC cycle must scan it.
3. **Doesn't help with table layout**: Tables are still Go structs with multiple slice/map allocations. The flat cache-friendly layout requires custom allocation.
4. **Temporary value**: Only useful as a transitional strategy (Phase S2.1), not as a final solution.

## Appendix C: Encoding Verification

Critical invariants that must hold:

```
1. FloatValue(f).Float() == f   for all non-NaN f
2. FloatValue(NaN).Float() is NaN
3. IntValue(i).Int() == i       for all |i| < 2^47
4. BoolValue(true).Bool() == true
5. BoolValue(false).Bool() == false
6. NilValue().IsNil() == true
7. PtrValue(p).Ptr() == p      for all p with uintptr(p) < 2^48
8. IsFloat(FloatValue(f)) == true for all non-NaN f
9. IsFloat(IntValue(i)) == false
10. IsFloat(PtrValue(p)) == false
11. No valid float64 collides with any tagged value
```

Invariant 11 is guaranteed by: tagged values have bits 50-62 ALL set to 1 (quiet NaN with bit 50). The only float64 values with bits 50-62 all set are NaN values. Our `FloatValue()` constructor canonicalizes NaN, so no float64 stored in our system has bits 50-62 all set. Therefore no collision is possible.
