# GScript Shape System Design

**Date:** 2026-03-27
**Status:** Design Phase
**Goal:** Enable V8-style hidden class optimization for GScript JIT (20-50% speedup on field access)

---

## Executive Summary

GScript has a partial shape system (hash-based `shapeID` + VM inline cache) but lacks:
1. **Shape objects** with field metadata (currently just a hash)
2. **Transition tree** for O(1) property addition
3. **JIT shape guards** (compilation relies on VM cache)
4. **Stability tracking** (no const field optimization)

This design adds a proper Shape registry with transition support while maintaining backward compatibility with existing `shapeID`.

---

## Current Implementation Analysis

### What Exists Today

**File:** `internal/runtime/table.go`, `internal/runtime/shape.go`

```go
// Current Table fields related to shapes
type Table struct {
    shapeID    uint32 // hash-based identifier from GetShapeID()
    skeys     []string // ordered field names
    svals     []Value  // parallel values
    // ...
}

// Current shape registry (hash-based)
var (
    shapeCounter uint32 = 0
    shapeRegistry sync.Map  // NUL-joined skeys → uint32
)

func GetShapeID(skeys []string) uint32 {
    if len(skeys) == 0 {
        return 0  // special "no shape" sentinel
    }
    key := strings.Join(skeys, "\x00")
    if id, ok := shapeRegistry.Load(key); ok {
        return id.(uint32)
    }
    newID := atomic.AddUint32(&shapeCounter, 1)
    actual, _ := shapeRegistry.LoadOrStore(key, newID)
    return actual.(uint32)
}
```

**VM Inline Cache:**
```go
type FieldCacheEntry struct {
    FieldIdx int    // cached index into skeys/svals
    ShapeID  uint32 // shapeID when cache was populated
}

func (t *Table) RawGetStringCached(key string, cache *FieldCacheEntry) Value {
    // Fast path: shapeID match → direct array access
    if t.shapeID != 0 && cache.ShapeID == t.shapeID && idx >= 0 && idx < len(t.svals) {
        return t.svals[cache.FieldIdx]
    }
    // Cache miss: linear scan + update cache
    // ...
}
```

**Capabilities:**
- Tables with identical field order share the same `shapeID`
- VM can skip string comparison when shapeID matches
- Works across different tables (e.g., all nbody body objects)

**Limitations:**
- `shapeID` is just a hash, no metadata attached
- No transition tree → `GetShapeID` recomputed on every modification
- JIT doesn't emit shape guards → relies on VM cache
- No field offset information (only index in skeys array)
- No stability tracking → no const field optimization

---

## Proposed Enhancement: Shape Transition System

### Core Data Structures

```go
// Shape represents a table's hidden class (field layout metadata).
// Shapes are immutable and globally shared across all tables.
type Shape struct {
    ID        uint32            // Unique identifier (backward compatible with Table.shapeID)
    FieldKeys []string          // Canonical ordered field names
    FieldMap  map[string]int    // Field name → index in FieldKeys
    Transitions map[string]*Shape // Property name → target shape (one-level cache)
    Stable    bool             // No further transitions possible (all fields locked)
    Version    uint32           // Incremented on deprecation events (future: constness tracking)
}

// ShapeRegistry manages global shape instances.
type ShapeRegistry struct {
    mu    sync.RWMutex
    byID  map[uint32]*Shape      // ID → Shape (for JIT guard validation)
    byKey map[string]*Shape     // NUL-joined keys → Shape (for lookup)
    nextID uint32               // Next ID to assign
}

// Global shape registry singleton
var globalShapeRegistry = &ShapeRegistry{
    byID:  make(map[uint32]*Shape),
    byKey: make(map[string]*Shape),
    nextID: 1,  // ID 0 reserved for "no shape" sentinel
}
```

### API Design

```go
// GetShape returns or creates a Shape for the given field name list.
// If the shape exists, returns the cached instance (shape sharing).
// If not, creates a new Shape and adds to registry.
func GetShape(fieldNames []string) *Shape {
    if len(fieldNames) == 0 {
        return nil  // Empty table has no shape
    }

    key := shapeKey(fieldNames)
    globalShapeRegistry.mu.RLock()
    if s, ok := globalShapeRegistry.byKey[key]; ok {
        globalShapeRegistry.mu.RUnlock()
        return s
    }
    globalShapeRegistry.mu.RUnlock()

    // Create new shape
    globalShapeRegistry.mu.Lock()
    defer globalShapeRegistry.mu.Unlock()

    // Double-check after acquiring write lock
    if s, ok := globalShapeRegistry.byKey[key]; ok {
        return s
    }

    s := &Shape{
        ID:        globalShapeRegistry.nextID,
        FieldKeys: fieldNames,
        FieldMap:  make(map[string]int),
        Transitions: make(map[string]*Shape),
        Stable:    false,
        Version:    1,
    }
    for i, name := range fieldNames {
        s.FieldMap[name] = i
    }

    globalShapeRegistry.byID[s.ID] = s
    globalShapeRegistry.byKey[key] = s
    globalShapeRegistry.nextID++

    return s
}

// Transition returns the Shape after adding a property to this Shape.
// Uses transition cache for O(1) lookup.
// Returns the new Shape (which may be this Shape if property already exists).
func (s *Shape) Transition(property string) *Shape {
    // Check transition cache
    if next, ok := s.Transitions[property]; ok {
        return next
    }

    // Build new key list (preserving existing order, appending new property)
    // Note: V8 sorts keys, but GScript preserves insertion order (important!)
    newKeys := make([]string, len(s.FieldKeys)+1)
    copy(newKeys, s.FieldKeys)
    newKeys[len(s.FieldKeys)] = property

    // Get or create target shape
    newShape := GetShape(newKeys)

    // Cache transition
    s.Transitions[property] = newShape

    return newShape
}

// GetFieldIndex returns the index of a field in this Shape, or -1 if not found.
// This replaces the current Table.FieldIndex() with O(1) lookup.
func (s *Shape) GetFieldIndex(name string) int {
    if idx, ok := s.FieldMap[name]; ok {
        return idx
    }
    return -1
}

// shapeKey creates a canonical string key for shape lookup.
func shapeKey(names []string) string {
    return strings.Join(names, "\x00")
}

// LookupShapeByID retrieves a Shape by its ID (for JIT guard validation).
func LookupShapeByID(id uint32) *Shape {
    globalShapeRegistry.mu.RLock()
    defer globalShapeRegistry.mu.RUnlock()
    return globalShapeRegistry.byID[id]
}
```

### Table Integration

```go
// Table enhancements to use Shape system.
type Table struct {
    // ... existing fields ...
    shape    *Shape   // Pointer to current Shape (replaces shapeID field)
}

// Note: We keep shapeID for backward compatibility with FieldCacheEntry
// but add a getter that reads from shape if available.
func (t *Table) ShapeID() uint32 {
    if t.shape != nil {
        return t.shape.ID
    }
    return 0
}

// GetFieldIndex optimized to use Shape.FieldMap (O(1) vs O(n) scan).
func (t *Table) GetFieldIndex(key string) int {
    if t.shape != nil {
        return t.shape.GetFieldIndex(key)
    }
    // Fallback to linear scan for backward compatibility
    for i, k := range t.skeys {
        if k == key {
            return i
        }
    }
    return -1
}

// RawSetString enhanced to use Shape transitions.
func (t *Table) RawSetString(key string, val Value) {
    if t.mu != nil {
        t.mu.Lock()
        defer t.mu.Unlock()
    }
    t.keysDirty = true

    // Check if key exists (using Shape or fallback)
    fieldIdx := t.GetFieldIndex(key)
    if fieldIdx >= 0 {
        // Update existing field
        if val.IsNil() {
            // Delete field: need to transition to shape without this key
            if t.shape != nil {
                // Build new keys list without the deleted key
                newKeys := make([]string, 0, len(t.shape.FieldKeys)-1)
                for _, k := range t.shape.FieldKeys {
                    if k != key {
                        newKeys = append(newKeys, k)
                    }
                }
                t.shape = GetShape(newKeys)
            }
            // Update skeys/svals (existing logic)
            last := len(t.skeys) - 1
            t.skeys[fieldIdx] = t.skeys[last]
            t.svals[fieldIdx] = t.svals[last]
            t.skeys = t.skeys[:last]
            t.svals = t.svals[:last]
        } else {
            // Update value: no shape change
            t.svals[fieldIdx] = val
        }
    } else {
        // Add new field: transition shape
        if t.shape != nil {
            t.shape = t.shape.Transition(key)
        } else {
            // First field: create initial shape
            t.shape = GetShape([]string{key})
        }

        // Add to skeys/svals
        if len(t.skeys) < smallFieldCap {
            t.skeys = append(t.skeys, key)
            arenaAppendValue(DefaultHeap, &t.svals, val)
        } else {
            // Promote to map (existing logic)
            t.smap = make(map[string]Value, len(t.skeys)+1)
            for i, k := range t.skeys {
                t.smap[k] = t.svals[i]
            }
            t.smap[key] = val
            t.skeys = nil
            t.svals = nil
            t.shape = nil  // Can't use shape system with smap
        }
    }
}
```

---

## JIT Integration: Shape Guards

### Phase 1: Recording (Capture Shape Information)

```go
// TraceRecorder enhanced to capture Shape metadata.
type TraceRecorder struct {
    // ... existing fields ...
    tableShapes map[int]*Shape  // slot → shape at recording time
}

func (r *TraceRecorder) OnFieldAccess(table *Table, key string, val Value) {
    slot := r.currentSlot()

    // Capture shape and field index at recording time
    if table.shape != nil {
        r.tableShapes[slot] = table.shape
        fieldIdx := table.shape.GetFieldIndex(key)

        r.ir = append(r.ir, TraceIR{
            Op:       OP_GETFIELD,
            Type:      val.Type(),
            FieldIdx:  fieldIdx,
            ShapeID:   table.shape.ID,  // for guard emission
            TableSlot: slot,
        })
    } else {
        // Fallback: record without shape info (use legacy path)
        r.ir = append(r.ir, TraceIR{
            Op:       OP_GETFIELD,
            Type:      val.Type(),
            FieldIdx:  table.FieldIndex(key),  // O(n) fallback
            ShapeID:   0,
            TableSlot: slot,
        })
    }
}
```

### Phase 2: SSA Builder (Emit Shape Guards)

```go
// New SSA opcodes for shape-based field access
const (
    SSA_LOAD_TABLE_SHAPE SSAOp = iota  // Load table.shape pointer
    SSA_CHECK_SHAPE_ID              // Guard: shape.ID == expected
    SSA_LOAD_FIELD_BY_IDX          // Load svals[fieldIdx] (no key lookup)
)

// SSA builder: emit shape guard at trace entry (once per table)
func (b *SSABuilder) buildGETFIELD(ir TraceIR, slot int) SSARef {
    // Skip shape system if not available
    if ir.ShapeID == 0 {
        // Fallback to legacy path (string comparison)
        key := b.Const(uint64(b.strToOffset(ir.KeyStr)))
        table := b.Slot(ir.TableSlot)
        return b.NewInst(SSA_GETFIELD_LEGACY, table, key)
    }

    // Emit shape guard (only once per table per trace)
    if !b.hasShapeGuard(ir.TableSlot) {
        tableRef := b.Slot(ir.TableSlot)

        // Load shape pointer
        shapePtr := b.NewInst(SSA_LOAD_TABLE_SHAPE, tableRef)

        // Load shape ID (offset 0 in Shape struct)
        shapeID := b.NewInst(SSA_LOAD_I32, shapePtr, b.Const(0))

        // Guard: shape ID matches expected
        expectedID := b.Const(uint64(ir.ShapeID))
        guard := b.NewInst(SSA_CHECK_SHAPE_ID, shapeID, expectedID)
        b.setBranchTarget(guard, b.ColdPath())

        b.markShapeGuarded(ir.TableSlot)
    }

    // Direct field access by index (no string comparison)
    tableRef := b.Slot(ir.TableSlot)
    svalsPtr := b.NewInst(SSA_LOAD_SVALS_PTR, tableRef)  // Offset 16 in Table struct
    idxConst := b.Const(uint64(ir.FieldIdx))

    // Load from svals[idx] (8-byte stride)
    return b.NewInst(SSA_LOAD_SVALS_IDX, svalsPtr, idxConst)
}
```

### Phase 3: ARM64 Code Generation

```arm64
; Shape guard at loop entry
Lshape_guard_table0:
    LDR  X0, [X_table, #shape_offset]      ; Load table.shape (Shape*)
    LDR  W0, [X0, #id_offset]           ; Load shape.ID (offset 0)
    CMP  W0, #expected_shape_id            ; Compare to expected
    B.NE Lside_exit                        ; Bail on mismatch

; Fast path: LOAD_FIELD by index
Lload_field:
    LDR  X1, [X_table, #svals_offset]    ; Load svals pointer
    LDR  X2, [X1, #field_idx*8]         ; Load value (8-byte stride)
    ; Continue with value in X2
```

**Offset constants (from Go struct layout):**
```go
// These are verified by TableShapeIDOffset() and TableFieldOffsets()
const (
    shape_offset   = unsafe.Offsetof((Table{}).shape)  // ≈ 184 bytes
    id_offset     = 0                                 // Shape.ID is first field
    svals_offset  = unsafe.Offsetof((Table{}).svals) // ≈ 56 bytes
)
```

---

## Phase-by-Phase Implementation Plan

### Phase 1A: Shape Registry (Week 1)

**Files to create/modify:**
- `internal/runtime/shape.go` (modify: add Shape struct, GetShape, Transition)
- `internal/runtime/table.go` (modify: add `shape *Shape` field)

**Changes:**
1. Define `Shape` struct with ID, FieldKeys, FieldMap, Transitions
2. Implement `ShapeRegistry` singleton
3. Implement `GetShape(fieldNames []string) *Shape`
4. Implement `Shape.Transition(property) *Shape`
5. Add `Table.shape *Shape` field (keep `shapeID` for compatibility)
6. Update `Table.ShapeID()` to return `t.shape.ID` or compute hash

**Tests:**
- Test shape sharing: multiple tables with same fields get same Shape instance
- Test transition: adding property returns correct new shape
- Test transition cache: second call to same Transition returns cached result
- Test lookup by ID: `LookupShapeByID` returns correct Shape

**Expected Impact:** No performance change yet, but foundation for future optimizations.

---

### Phase 1B: Table Integration (Week 1)

**Files to modify:**
- `internal/runtime/table.go` (modify: RawSetString, GetFieldIndex)

**Changes:**
1. Update `GetFieldIndex(key string) int` to use `Shape.FieldMap` (O(1))
2. Update `RawSetString(key, val Value)` to call `Shape.Transition()` on add
3. On field deletion: build new key list without deleted key, call `GetShape()`
4. Ensure skeys/svals are kept in sync with Shape.FieldKeys

**Tests:**
- Test field add: shape transitions correctly
- Test field update: no shape change for existing field
- Test field delete: shape changes to remove field
- Test GetFieldIndex: O(1) vs O(n) (benchmark comparison)

**Expected Impact:** 20-30% faster field index lookup on repeated access.

---

### Phase 1C: JIT Shape Guards (Week 2)

**Files to modify:**
- `internal/jit/recorder.go` (modify: OnFieldAccess to capture shape)
- `internal/jit/ssa_builder.go` (modify: buildGETFIELD, buildSETFIELD)
- `internal/jit/ssa_emit.go` (modify: add SSA opcodes, emit guards)
- `internal/jit/codegen_arm64.go` (modify: emit shape guard ARM64)

**Changes:**
1. Add `SSA_LOAD_TABLE_SHAPE`, `SSA_CHECK_SHAPE_ID` opcodes
2. Add `tableShapes map[int]*Shape` to TraceRecorder
3. Emit shape guard at trace entry (once per table)
4. Replace string comparison in GETFIELD with direct svals[idx] access
5. ARM64: emit LDR for shape, CMP for guard, LDR for field value

**Tests:**
- Test trace compiles with shape guard
- Test guard failure: side-exit on shape mismatch
- Test JIT correctness: compare trace output vs interpreter (multiple shape patterns)
- Benchmark: field access speedup vs legacy path

**Expected Impact:** 20-50% speedup on monomorphic field access patterns (nbody, method_dispatch).

---

## Memory and Performance Analysis

### Memory Overhead

**Per Shape:**
- FieldKeys slice: 16 bytes per key (string header) + content
- FieldMap map: 8 bytes per entry overhead + key/value
- Transitions map: Only for shapes that are transitioned from
- Total: ~64-128 bytes per unique shape

**Number of Shapes:**
- nbody benchmark: 1 shape (all body objects have same fields)
- method_dispatch: 3-5 shapes (different object types)
- Real-world: 10-50 shapes typical, <100 for large codebases

**Total Overhead:** ~6-12 KB for typical program, acceptable for JIT gains.

### Performance Comparison

| Operation | Current | With Shape System | Speedup |
|-----------|---------|------------------|----------|
| Field index lookup | O(n) string scan | O(1) map lookup | 2-10x |
| Shape transition | O(n) hash computation | O(1) cache lookup | 2-5x |
| JIT field load | String compare + scan | Guard + direct access | 2-5x |
| Cache hit path | shapeID check + array access | shapeID check + array access | Same |

---

## Backward Compatibility

### Existing FieldCacheEntry

No changes needed — `FieldCacheEntry.ShapeID` continues to work with `Table.ShapeID()`.

### Legacy Tables Without Shape

Tables created before this change will have `t.shape == nil`. Code falls back to:
- Linear scan in `GetFieldIndex()`
- Hash computation for shapeID
- Legacy JIT path without shape guards

**Migration Path:** New tables use Shape system; old tables continue working.

---

## Future Extensions (Beyond Phase 1)

### Constness Tracking (Phase 4 from SYNTHESIS)

```go
type Shape struct {
    // ... existing fields ...
    ConstFields map[string]struct{}  // Never modified after construction
}

func (s *Shape) MarkConst(key string) {
    if s.ConstFields == nil {
        s.ConstFields = make(map[string]struct{})
    }
    s.ConstFields[key] = struct{}{}
}

// JIT: constant folding for const fields
func (c *codegen) emitLoadField(tableRef SSARef, key string) SSARef {
    if s.IsConst(key) {
        constVal := s.ConstValue[key]
        return c.Const(constVal)  // Emit literal, no load
    }
    // Regular load...
}
```

### Stability Tracking

```go
type Shape struct {
    Stable bool  // No transitions possible
}

func (s *Shape) MarkStable() {
    s.Stable = true
    // Enable more aggressive optimizations (no need for guard refresh)
}
```

### Polymorphic Inline Caches (Phase 3 from SYNTHESIS)

```go
type ICState int

const (
    ICUninitialized ICState = iota
    ICMonomorphic
    ICPolymorphic
    ICMegamorphic
)

type FieldIC struct {
    State     ICState
    Shapes    []*Shape  // Up to 4 shapes for polymorphic
    FieldIdxs []int     // Parallel field indices
}
```

---

## Risk Assessment

### Risk 1: Memory Bloat

**Concern:** Many unique shapes created by dynamic property patterns.

**Mitigation:**
- Shape sharing via registry (same field order = same shape)
- Consider shape GC in future (reference counting)

### Risk 2: Shape Transition Incorrectness

**Concern:** Shape transitions produce wrong field order for deletion patterns.

**Mitigation:**
- Unit tests for all CRUD operations
- Property order preservation tests (insertion order matters for iteration)

### Risk 3: JIT Guard Bugs

**Concern:** Shape guard fails to side-exit, corrupting state.

**Mitigation:**
- Built-in observability: dump shape info on guard failure
- Correctness tests: run all benchmarks with JIT vs interpreter comparison
- Start with eager deopt: immediately bail on guard failure

### Risk 4: Backward Compatibility Break

**Concern:** Existing code depends on `shapeID` behavior.

**Mitigation:**
- Keep `Table.shapeID uint32` field
- Add `ShapeID()` getter that reads from shape if available
- Existing `FieldCacheEntry` works unchanged

---

## Success Criteria

1. **Shape Registry:**
   - Shapes are shared across tables with identical field order
   - Transition cache provides O(1) lookup
   - No memory leaks (test with 100k tables)

2. **Table Integration:**
   - `GetFieldIndex` is O(1) (benchmark: < 10ns vs 100ns current)
   - Field add/delete transitions shapes correctly
   - skeys/svals stay in sync with Shape.FieldKeys

3. **JIT Guards:**
   - Trace JIT emits shape guard for GETFIELD/SETFIELD
   - Guard failure causes correct side-exit
   - JIT output matches interpreter (all existing tests pass)
   - nbody benchmark: +20-50% speedup

4. **Backward Compatibility:**
   - All existing benchmarks pass without modification
   - Legacy tables (shape = nil) work correctly
   - FieldCacheEntry continues to function

---

## References

- V8 Hidden Classes Research: `docs/research/v8-hidden-classes.md`
- Research Synthesis: `docs/research/SYNTHESIS.md`
- Current Implementation: `internal/runtime/shape.go`, `internal/runtime/table.go`
