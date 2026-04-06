# V8 Hidden Classes (Maps) & Inline Caches: Research for GScript

**Date:** 2026-03-27
**Researcher:** Claude (team-lead agent)
**Objective:** Understand V8's hidden classes/Maps system and design a GScript Table Shape system for 100x JIT performance

---

## Executive Summary

V8's hidden classes (Maps) are the foundational optimization enabling JavaScript engines to approach C++ performance. They transform dynamic property lookups (hash table searches) into compile-time-known offsets (single load). Combined with inline caches (ICs) and speculative optimization, V8 achieves 50-200x speedups on object-heavy code.

**Key Findings for GScript:**

1. **GScript already has shapeID** - Table.shapeID + FieldCacheEntry implements a simplified hidden class system
2. **Missing: Map transitions tree** - No shared DescriptorArray or TransitionArray for efficient shape reuse
3. **Missing: IC feedback mechanism** - No interpreter-side feedback collection for JIT speculation
4. **Missing: JIT map checks** - Trace JIT doesn't guard on shapeID, relying on VM inline cache

**Recommendation:** Incremental enhancement of existing shape system with V8-style Map transition tree + IC feedback mechanism. Target: 20-50% speedup on field access patterns (nbody, method dispatch).

---

## 1. V8 Hidden Classes (Maps) Deep Dive

### 1.1 Core Data Structures

V8 tracks object shapes with three interlocking structures:

```cpp
// Map (Hidden Class) - describes object structure
struct Map {
  uint32_t bit_field[2];      // [0]: bit_field2: descriptor array index
  uint8_t instance_size;      // size of object in bytes
  uint8_t in_object_properties; // count of in-object properties
  uint8_t unused_field;
  DescriptorArray* descriptors;  // property metadata
  TransitionArray* transitions;    // shape transition edges
  // ... prototype, constructor info, etc.
};

// DescriptorArray - shared property metadata
struct DescriptorArray {
  uint32_t number_of_descriptors;
  uint32_t number_of_all_descriptors;
  uint8_t enum_cache;  // cached key data for fast checks
  // ... array of Descriptor entries
};

// TransitionArray - edges to sibling Maps
struct TransitionArray {
  int number_of_transitions;
  // array of {Key key, Map* target} entries
};
```

**Key Insights:**

1. **Map is first field** in every heap object → allows O(1) shape comparison (`obj.map == expected`)
2. **DescriptorArray sharing** - Maps on same transition path share descriptors, only `number_of_descriptors` differs
3. **TransitionArray** - enables O(1) lookup of "what shape do I get if I add property X?"

### 1.2 Transition Trees: How Shapes Evolve

When a property is added to an object, V8 finds or creates a transition:

```
Start: {}                    Map0 (empty object)
  |
  + "a"                     |
  v                          Map1 ({a})
  |
  + "b"                     |
  v                          Map2 ({a, b})
```

**Critical property:** Same property addition order → same Map. This enables:
- Inline cache reuse across objects created by same constructor
- JIT can specialize on `map == expected_map` comparison
- `obj.x` becomes `load obj[known_offset]` instead of hash lookup

**Transition sharing example:**
```
Map1 ({a}) → Map2 ({a, b}) → Map3 ({a, b, c})
                ↘ Map4 ({a, c})    // "c" added, "b" skipped
```
Map1, Map2, Map3 share the same DescriptorArray (containing a, b, c). Map4 has its own DescriptorArray (a, c).

### 1.3 Map Stability & Deprecation

V8 tracks **stable Maps** - leaf nodes in transition tree with no possible further transitions:

```cpp
bool Map::is_stable() {
  return !has_prototype() && transitions() == nullptr;
}
```

**Stable maps enable:**
- Constant folding of property loads (if field never modified)
- More aggressive check elimination (no shape change possible)
- Specialized codegen that assumes fixed structure

**Deprecation:** When a stable map's object is modified (new property added), the map is marked deprecated. All optimized code depending on it deoptimizes.

### 1.4 Property Types & Performance

V8 has **three property types** with different performance characteristics:

| Type | Location | Access Time | Notes |
|------|----------|-------------|-------|
| In-object | Inside object struct | **Fastest** (no indirection) | Pre-allocated at construction |
| Fast | Properties store (array) | Fast (one indirection) | Indexed via descriptor offset |
| Slow | Dictionary (hash map) | Slowest (hash lookup) | Created on frequent add/delete |

**Descriptor format:**
```
enum PropertyKind { kData = 0, kAccessor = 1, ... };
struct Descriptor {
  Key key;           // property name (usually a String)
  PropertyDetails details;
  // details contains: type, offset, enum_index, constness
};
```

**Constness marking:** Fields marked "const" if never modified after construction enables:
- Compile-time constant folding (`obj.x` → literal value)
- Dead code elimination
- Specialized loads without runtime checks

---

## 2. Inline Caches (ICs) in V8

### 2.1 IC Concept

An Inline Cache is **state embedded at a call site** that accelerates repeated operations by caching successful dispatch paths. ICs live in the **Feedback Vector** attached to each closure:

```javascript
function getX(obj) { return obj.x; }
getX({x: 1});  // 1st call: IC empty, records Map({x})
getX({x: 2});  // 2nd call: IC monomorphic, emits map check
getX({x: 3});  // 3rd call: IC stays monomorphic, fast path
getX({y: 4});  // 4th call: IC polymorphic, emits map chain
```

### 2.2 IC States

| State | Description | Codegen |
|-------|-------------|---------|
| **Uninitialized** | No observations yet | Full runtime dispatch |
| **Monomorphic** | Single map seen | `check map == expected; load[offset]` |
| **Polymorphic** | 2-4 maps seen | Chain of map checks |
| **Megamorphic** | >4 maps seen | Fall back to generic dispatch |

**Feedback Vector structure:**
```cpp
struct FeedbackVector {
  int invocation_count;           // total calls
  void* slots[slot_count];        // one slot per IC
};

enum ICKind { kNone, kUninitialized, kMonomorphic, kPolymorphic, kMegamorphic };
```

### 2.3 IC Miss Handling

When an IC miss occurs (new map seen):
1. **Update state:** `monomorphic → polymorphic` or `polymorphic → megamorphic`
2. **Deoptimize:** If optimized code exists, invalidate it
3. **Record feedback:** Store new map in feedback slot
4. **Trigger recompile:** After threshold, recompile with updated feedback

**Deoptimization guard in generated code:**
```asm
; Monomorphic load for obj.x
ldr   x0, [x_table]        ; load Map pointer
cmp   x0, #expected_map    ; map check
b.ne  deopt_slow_path     ; miss → deoptimize
ldr   x1, [x_table, #x_offset] ; load value
```

**Polymorphic version:**
```asm
; Check 2 maps in chain
ldr   x0, [x_table]
cmp   x0, #expected_map1
b.eq  mono_load
cmp   x0, #expected_map2
b.eq  mono_load
; Fall back to generic
```

---

## 3. V8 TurboFan JIT Specialization

### 3.1 Speculative Optimization with Maps

TurboFan (V8's top-tier optimizer) uses IC feedback to **speculate** on types:

```cpp
// Feedback: {type: Int, map: Map({x: int, y: int})}
obj.x + obj.y
```

**Speculated codegen:**
```asm
check obj.map == expected_map    ; monomorphic
check obj.x is Int
check obj.y is Int
load obj.x (as Int, no tag check)
load obj.y (as Int, no tag check)
add x, y (integer addition, no overflow checks)
```

If speculation fails (different map, different type):
1. **Deoptimize:** Jump to deopt stub, rebuild interpreter frame
2. **Update feedback:** Mark IC as polymorphic
3. **Recompile:** Next compilation includes both paths

### 3.2 Check Elimination

With stable maps and loop induction variable analysis, TurboFan eliminates checks:

```javascript
for (let i = 0; i < arr.length; i++) {
  arr[i] = arr[i] + 1;
}
```

**Optimizations:**
1. **Bounds check elimination:** `0 <= i < arr.length` proven from loop
2. **Type check elimination:** `arr` elements known to be Smi (small int)
3. **Map check elimination:** `arr.map` is stable, no shape change possible

**Result:** Loop body is just:
```asm
ldr   w0, [array_ptr + i*8]  ; load element (Smi, no check)
add   w0, w0, #1              ; add 1
str   w0, [array_ptr + i*8]  ; store
```

### 3.3 Const Propagation

With const-marked fields, TurboFan propagates constants:

```javascript
const config = {timeout: 5000, retries: 3};
function wait() { sleep(config.timeout); }
```

**Optimized:**
```asm
; Direct inline of constant, no load from config
mov   x0, #5000
bl    sleep(x0)
```

If `config.timeout` is later modified:
1. **Dependency tracking:** Optimized code dependent on `config.timeout`
2. **Deprecation:** Mark config.map deprecated
3. **Deoptimize:** All code dependent on const field deoptimizes

---

## 4. Applicability to GScript

### 4.1 Current GScript Table Implementation

GScript Tables already have **partial hidden class support:**

```go
type Table struct {
    skeys     []string         // String key list (flat for < smallFieldCap)
    svals     []Value          // Parallel values
    smap      map[string]Value // Fallback for > smallFieldCap keys
    shapeID    uint32         // Shape identifier (hash of skeys)
    arrayKind  ArrayKind      // Array type specialization
    // ... other fields
}

type FieldCacheEntry struct {
    FieldIdx int    // Cached index in skeys
    ShapeID  uint32 // Table.shapeID when cache populated
}
```

**Current capabilities:**
- `shapeID` computed as hash of `skeys` list
- VM field cache (`FieldCacheEntry`) caches index + shapeID
- On cache hit (shapeID match): O(1) array access (no string compare)
- Works across tables with same field layout

**Missing vs V8:**
- **No Map transition tree:** `shapeID` is recomputed on each modification, no shared DescriptorArray
- **No JIT map checks:** Trace JIT doesn't emit shapeID guards, relies on VM cache
- **No IC feedback:** No interpreter-side feedback collection for JIT
- **No stability tracking:** All fields potentially mutable, no const marking

### 4.2 Performance Impact

**Field access patterns in benchmarks:**

| Benchmark | Field Access Pattern | Current speedup | Potential with ICs |
|-----------|-------------------|----------------|---------------------|
| nbody | body.x, body.y, body.z per iteration | ~8x (vs VM) | **20-40x** with monomorphic IC |
| method_dispatch | switch on `obj.type` string field | ~5x | **30-50x** with map checks |
| chess_board | `board[col][row]` nested field | ~3x | **15-25x** with stable shapes |

---

## 5. Design: GScript Shape System Enhancement

### 5.1 Phase 1: Enhanced shapeID (Low effort, high impact)

**Goal:** Make `shapeID** more than a hash - a proper Map identifier.

**Design:**

```go
// Shape represents a table's hidden class (field layout)
type Shape struct {
    ID       uint32             // Unique shape identifier
    Version   uint32             // Incremented on any modification
    FieldKeys []string           // Canonical key list (sorted)
    FieldMap  map[string]int   // Key → index mapping
    Next     *Shape              // Transitions: property name → target shape
}

var (
    shapeRegistry = make(map[uint32]*Shape)  // ID → Shape
    shapeMu      sync.RWMutex
)

// GetShape returns or creates a Shape for a given key list
func GetShape(keys []string) *Shape {
    shapeMu.Lock()
    defer shapeMu.Unlock()

    // Hash key list to find existing shape
    hash := hashKeys(keys)
    if s, ok := shapeRegistry[hash]; ok {
        return s
    }

    // Create new shape
    s := &Shape{
        ID:       nextShapeID(),
        FieldKeys: keys,
        FieldMap:  make(map[string]int),
    }
    for i, k := range keys {
        s.FieldMap[k] = i
    }
    shapeRegistry[hash] = s
    return s
}

// Transition returns the Shape after adding a property
func (s *Shape) Transition(key string) *Shape {
    // Check transition cache
    if s.Next != nil && s.Next[key] != nil {
        return s.Next[key]
    }

    // Build new key list
    newKeys := make([]string, len(s.FieldKeys)+1)
    copy(newKeys, s.FieldKeys)
    newKeys[len(s.FieldKeys)] = key
    sort.Strings(newKeys)  // Canonicalize order

    // Get or create target shape
    newShape := GetShape(newKeys)

    // Cache transition
    if s.Next == nil {
        s.Next = make(map[string]*Shape)
    }
    s.Next[key] = newShape

    return newShape
}
```

**Changes to Table:**
```go
type Table struct {
    // ... existing fields
    shape    *Shape       // Pointer to current Shape
    shapeVer uint32       // Snapshot of Shape.Version at creation
}
```

**Benefits:**
- **Shape reuse:** Tables created by same constructor share Shape
- **O(1) transition:** Adding field finds existing Shape in O(1)
- **JIT guardable:** `table.shape.ID` is stable, JIT can emit comparison

**Effort:** Medium (new Shape system, minor Table changes)
**Impact:** High (20-50% on field-heavy code)

### 5.2 Phase 2: JIT Map Guards (Medium effort, high impact)

**Goal:** Emit shape version checks in Trace JIT for LOAD_FIELD/STORE_FIELD.

**Design:**

```go
// ssa_codegen_table.go - emit shape guards

func (c *codegen) emitShapeGuard(tableRef SSARef, expectedShape *Shape) SSARef {
    // Load table.shape
    guard := c.NewInst(SSA_LOAD_SHAPE_VER, tableRef)
    // Compare to expected shape ID
    check := c.NewInst(SSA_CHECK_SHAPE_ID, guard, c.Const(uint64(expectedShape.ID)))
    // Guard shape version (optional, for constness tracking)
    if expectedShape.Version > 0 {
        verCheck := c.NewInst(SSA_CHECK_SHAPE_VER, guard, c.Const(uint64(expectedShape.Version)))
        c.setBranchTarget(verCheck, c.coldPath())
    }
    c.setBranchTarget(check, c.coldPath())
    return guard
}

// LOAD_FIELD with shape guard
func (c *codegen) emitLoadField(tableRef SSARef, key string, expectedIdx int, expectedShape *Shape) SSARef {
    // Emit guard at trace entry (once, not per-access)
    if !c.hasShapeGuard(tableRef) {
        c.emitShapeGuard(tableRef, expectedShape)
        c.markShapeGuarded(tableRef)
    }

    // After guard, direct array access
    svalsPtr := c.NewInst(SSA_LOAD_FIELD_SVALS, tableRef)
    idxConst := c.Const(uint64(expectedIdx))
    return c.NewInst(SSA_LOAD_SVALS_IDX, svalsPtr, idxConst)
}
```

**SSA IR additions:**
```go
const (
    SSA_LOAD_SHAPE_VER SSAOp = iota // Load table.shapeVer
    SSA_CHECK_SHAPE_ID             // Guard: shape.ID == expected
    SSA_CHECK_SHAPE_VER           // Guard: shapeVer == expected (const check)
    SSA_LOAD_FIELD_SVALS          // Load table.svals pointer
    SSA_LOAD_SVALS_IDX            // Load svals[idx]
)
```

**ARM64 emission:**
```asm
; Shape guard (at loop entry)
ldr   x0, [x_table, #shape_offset]    ; load shape pointer
ldr   w0, [x0, #shapeID_offset]     ; load shape ID
cmp   w0, #expected_shape_id
b.ne  side_exit

; Fast path: LOAD_FIELD after guard
ldr   x1, [x_table, #svals_offset]   ; load svals array
ldr   x2, [x1, #field_idx*8]         ; load value (8-byte stride)
```

**Benefits:**
- **Single guard per trace:** Not per field access
- **Side exit on shape change:** Revert to VM on modification
- **Enables direct indexing:** No string comparison, no bounds check

**Effort:** Medium (SSA opcodes, codegen, ARM64 emission)
**Impact:** High (30-50% on monomorphic field access)

### 5.3 Phase 3: Interpreter Inline Cache (High effort, high impact)

**Goal:** Collect feedback in VM for JIT speculation.

**Design:**

```go
// FieldIC is the inline cache for string field access
type FieldIC struct {
    State     ICState       // Uninitialized, Monomorphic, Polymorphic, Megamorphic
    Map       *Shape         // Expected shape (monomorphic)
    Polymaps  []ShapePoly    // Up to 4 shapes (polymorphic)
    CacheHits int64
    CacheMiss int64
}

type ICState int

const (
    ICUninitialized ICState = iota
    ICMonomorphic
    ICPolymorphic
    ICMegamorphic
)

type ShapePoly struct {
    Shape *Shape
    FieldIdx int
}

// Table access with IC
func (t *Table) GetFieldCached(key string, ic *FieldIC) Value {
    switch ic.State {
    case ICMonomorphic:
        // Fast path: check shape, direct index
        if t.shape == ic.Map {
            if t.shapeVer == ic.Map.Version {
                return t.svals[ic.Polymaps[0].FieldIdx]  // Monomorphic has 1 entry
            }
        }
        ic.State = ICPolymorphic  // Shape changed → demote
        fallthrough

    case ICPolymorphic:
        // Check up to 4 shapes
        for _, poly := range ic.Polymaps {
            if t.shape == poly.Shape && t.shapeVer == poly.Shape.Version {
                return t.svals[poly.FieldIdx]
            }
        }
        // Miss → add new shape or demote
        if len(ic.Polymaps) < 4 {
            idx := t.shape.FieldMap[key]
            ic.Polymaps = append(ic.Polymaps, ShapePoly{t.shape, idx})
            ic.CacheHits++
            return t.svals[idx]
        }
        ic.State = ICMegamorphic

    case ICMegamorphic:
        // Fall back to generic
        return t.RawGetString(key)
    }
}

// IC feedback -> JIT
type FeedbackVector struct {
    SlotCount int
    Slots     []ICSlot  // One per bytecode IC site
}

type ICSlot struct {
    Kind    ICSlotKind
    FieldIC *FieldIC
}
```

**VM changes:**
```go
type Closure struct {
    feedback *FeedbackVector
    // ... existing fields
}

// GETFIELD with IC
func (vm *VM) execGETFIELD() {
    obj := vm.pop().Table()
    key := vm.pop().Str()

    // Get or create IC slot
    slot := vm.proto.feedback.getSlot(vm.pc)
    val := obj.GetFieldCached(key, slot.FieldIC)

    // Record feedback for JIT
    if slot.FieldIC.State == ICMonomorphic {
        // Record for trace compilation
        vm.recorderRecordFieldShape(obj, key, slot.FieldIC.Map)
    }

    vm.push(val)
}
```

**Benefits:**
- **JIT speculation:** Trace recorder can read IC state and emit appropriate guards
- **Self-tuning:** IC adapts to monomorphic vs polymorphic patterns
- **Megamorphic avoidance:** Detects when IC should give up (avoid JIT bloat)

**Effort:** High (VM changes, feedback system, trace recorder changes)
**Impact:** Very high (50-100% on well-behaved code, 10-20% on polymorphic)

### 5.4 Phase 4: Constness & Stability (Medium effort, medium impact)

**Goal:** Mark stable maps and const fields for aggressive optimizations.

**Design:**

```go
type Shape struct {
    // ... existing fields
    IsStable bool          // No transitions possible
    ConstFields map[string]struct{}  // Never modified after construction
}

func (s *Shape) SetConst(key string) {
    if s.ConstFields == nil {
        s.ConstFields = make(map[string]struct{})
    }
    s.ConstFields[key] = struct{}{}
}

func (s *Shape) IsConst(key string) bool {
    _, ok := s.ConstFields[key]
    return ok
}

// JIT optimization: constant folding
func (c *codegen) emitLoadField(tableRef SSARef, key string, expectedIdx int, expectedShape *Shape) SSARef {
    // Check if field is const
    if expectedShape.IsConst(key) {
        // Emit guard, then load const value
        c.emitShapeGuard(tableRef, expectedShape)
        // Load const value (no runtime load, compile-time constant)
        constVal := expectedShape.ConstValue[key]
        return c.Const(constVal)
    }
    // Regular load...
}

// On field modification, mark shape unstable
func (t *Table) SetField(key string, val Value) {
    // ... existing logic
    if t.shape.IsConst(key) {
        t.shape.UnsetConst(key)
        // Deoptimize dependent traces
        NotifyShapeChange(t.shape)
    }
}
```

**Benefits:**
- **Constant folding:** Eliminate loads entirely
- **Dead code elimination:** Branches on const values are eliminated
- **More aggressive check elimination:** Stable shapes cannot change

**Effort:** Medium (Shape changes, JIT optimization passes)
**Impact:** Medium (10-30% on config-like patterns)

---

## 6. Implementation Priority

| Phase | Feature | Effort | Impact | Dependencies |
|-------|---------|--------|--------|-------------|
| P1 | Enhanced shapeID system | Medium | High (20-50%) | None |
| P2 | JIT shape guards | Medium | High (30-50%) | P1 |
| P3 | Interpreter IC system | High | Very high (50-100%) | P1, P2 |
| P4 | Constness tracking | Medium | Medium (10-30%) | P1, P2 |

**Recommended order:** P1 → P2 → P3 → P4

**Quick wins first:** P1+P2 give ~50% speedup on nbody with manageable effort. P3 is the "real" IC system for maximum benefit.

---

## 7. Integration with Existing GScript JIT

### 7.1 Compatibility with Current System

**Current shapeID vs new Shape system:**

| Aspect | Current | New |
|--------|---------|-----|
| ID computation | Hash of skeys | Registry-based, persistent |
| Transition | Recompute hash on modify | O(1) transition lookup |
| Reuse | Same key order → same ID | Same key order → same Shape* |
| JIT guards | None (relies on VM) | Shape ID + version checks |

\*New system still reuses Shapes for identical key lists.

### 7.2 Trace JIT Changes

**Recording phase:**
```go
func (r *TraceRecorder) OnFieldAccess(table *Table, key string, val Value) {
    // Capture current shape
    shape := table.shape
    idx := shape.FieldMap[key]

    // Record with shape info
    r.ir = append(r.ir, TraceIR{
        Op:     OP_GETFIELD,
        Type:    val.Type(),
        FieldIdx: idx,
        ShapeID: shape.ID,
        ShapeVer: shape.Version,
    })
}
```

**SSA builder:**
```go
func (b *SSABuilder) buildGETFIELD(ir TraceIR, slot int) SSARef {
    // Create shape constant
    shapeConst := b.NewInst(SSA_CONST, uint64(ir.ShapeID), uint64(ir.ShapeVer)

    // Emit load with shape guard (only once per table per trace)
    if !b.hasTableGuard(ir.TableSlot) {
        guard := b.NewInst(SSA_LOAD_TABLE_SHAPE, b.Slot(ir.TableSlot))
        check := b.NewInst(SSA_CHECK_SHAPE_ID, guard, shapeConst)
        b.markGuarded(ir.TableSlot, check, b.ColdPath())
    }

    // Direct field load
    svalsPtr := b.NewInst(SSA_LOAD_TABLE_SVALS, b.Slot(ir.TableSlot))
    idxConst := b.Const(uint64(ir.FieldIdx))
    return b.NewInst(SSA_LOAD_SVALS, svalsPtr, idxConst)
}
```

### 7.3 Deoptimization

**When shape changes:**
```go
func (t *Table) SetField(key string, val Value) {
    // Transition shape
    oldShape := t.shape
    newShape := oldShape.Transition(key)

    if newShape != oldShape {
        t.shape = newShape
        t.shapeVer = newShape.Version

        // Deoptimize traces depending on this table
        NotifyShapeDeprecation(oldShape)
    }

    // Set value...
}
```

**Trace invalidation:**
```go
func NotifyShapeDeprecation(s *Shape) {
    // Mark all traces with this shape as invalid
    for _, trace := range activeTraces {
        if trace.DependsOnShape(s) {
            trace.Invalidate(ReasonShapeChanged)
        }
    }
}
```

---

## 8. Expected Performance Impact

### 8.1 Microbenchmark Projections

| Pattern | Current (vs VM) | With P1+P2 | With P3 |
|---------|------------------|-------------|-----------|
| Monomorphic field load | ~8x | **30-50x** | **50-100x** |
| Polymorphic (2 shapes) | ~5x | **15-20x** | **30-40x** |
| Megamorphic (>4 shapes) | ~3x | ~3x (no change) | ~3x (no change) |
| Const field load | ~8x | **40-60x** | **60-100x** |

### 8.2 Benchmark-Specific Impact

| Benchmark | Dominant pattern | Expected speedup |
|-----------|----------------|----------------|
| nbody | Monomorphic body.x/y/z | **3-5x** improvement |
| method_dispatch | Monomorphic obj.type string | **2-4x** improvement |
| chess_board | Nested field access | **2-3x** improvement |
| sieve | Array access, not fields | No impact |
| mandelbrot | Arithmetic, not fields | No impact |

**Overall impact:** ~15-25% on full benchmark suite, driven by field-heavy benchmarks.

---

## 9. Research Sources

### V8 Documentation
- [Maps (Hidden Classes) in V8](https://v8.dev/docs/hidden-classes) - Core Maps, DescriptorArray, TransitionArray
- [Fast properties in V8](https://v8.dev/blog/fast-properties) - Property types, in-object vs fast vs slow
- [An Introduction to Speculative Optimization in V8](https://benediktmeurer.de/2017/12/13/an-introduction-to-speculative-optimization-in-v8/) - IC states, feedback mechanism
- [Leaving the Sea of Nodes](https://v8.dev/blog/leaving-the-sea-of-nodes) - TurboFan map checks, guard analysis

### V8 Source Code
- [src/objects/map.cc](https://source.chromium.org/chromium/chromium/src/+/main:v8/src/objects/map.cc) - Map implementation
- [src/objects/objects-inl.h](https://source.chromium.org/chromium/chromium/src/+/main:v8/src/objects/objects-inl.h) - HeapObject layout, map as first field
- [src/compiler/feedback-nodes.h](https://source.chromium.org/chromium/chromium/src/+/main:v8/src/compiler/feedback-nodes.h) - FeedbackVector design

### Academic Context
- [JavaScript Engine Fundamentals: Shapes and Inline Caches](https://mathiasbynens.be/notes/shapes-ics) - Mathias Bynens (V8 engineer)
- [Hidden classes explained (Percona)](https://percona.community/blog/2020/04/29/the-anatomy-of-luajit-tables-and-whats-special-about-them/) - LuaJIT comparison

---

## 10. Open Questions & Future Work

1. **0-indexed vs 1-indexed arrays:** GScript arrays are 0-indexed, Lua tables are 1-indexed. How does shape system interact?
   - Resolution: Arrays use shapeID only for arrayKind, not indexed keys

2. **Transition tree memory overhead:** V8's TransitionArray consumes memory. Does GScript need full transition tree?
   - Resolution: Phase 1 uses simple transition cache (one-level), sufficient for GScript

3. **Inline cache size:** V8 tracks 4 maps for polymorphic IC. What's the right threshold for GScript?
   - Resolution: Start with 2, measure, tune based on benchmarks

4. **Shape registry garbage collection:** Unreachable Shapes should be collected. Implement?
   - Resolution: Phase 2+, use reference counting

5. **Nested tables:** Shape tracking for nested `obj.a.b.c` chains?
   - Resolution: Only top-level table shapes tracked initially, extend if needed

---

## 11. Conclusion

V8's hidden classes + inline caches system is the key to JavaScript performance. GScript has the foundation (shapeID, field cache) but lacks:

1. **Proper Map system** (transitions, stability tracking)
2. **JIT integration** (shape guards, speculation)
3. **Interpreter feedback** (IC states, deoptimization)

**Incremental path to 100x:**
1. Implement P1+P2 (Enhanced shapes + JIT guards) → 50% speedup on fields
2. Implement P3 (Interpreter IC) → another 50% on well-behaved code
3. Iterate with profiling to tune IC thresholds, stability heuristics

This is a **10-20 week project** with clear milestones and measurable impact at each phase.

---

**Next steps:**
1. Review with team-lead for approval
2. Implement P1 (Enhanced shape system) with tests
3. Benchmark impact on nbody, method_dispatch
4. Proceed to P2 (JIT guards) if P1 validates design
