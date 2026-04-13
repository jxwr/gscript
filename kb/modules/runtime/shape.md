---
module: runtime.shape
description: Hidden-class descriptors for string-keyed table fields. shapeID uint32 identity + transition graph.
files:
  - path: internal/runtime/shape.go
last_verified: 2026-04-13
---

# Runtime Shape

## Purpose

V8-style hidden classes for tables. Every distinct set of string-keyed fields gets a `*Shape`; assigning the same key sequence to two different tables puts them on the same shape, unlocking inline caching. `shapeID uint32` is the compact identifier used by Tier 1 inline caches and Tier 2 guards.

## Public API

- `type Shape struct` — field-to-index map + transition table
- `func GetShapeID(fields []string) uint32` — canonical shape ID for a given field list
- `func LookupShapeByID(id uint32) *Shape` — reverse lookup from ID to struct
- `func (s *Shape) Transition(fieldName string) *Shape` — shape after adding a new field
- `func (s *Shape) FieldIndex(name string) (int, bool)` — offset into `Table.svals`
- `type ShapeRegistry struct` — global intern table (package-level singleton)

## Invariants

- **MUST**: shape IDs are stable across the VM lifetime. Once `GetShapeID(["x","y"])` returns 42, it always returns 42.
- **MUST**: two tables with the same field sequence share the same `shapeID` AND the same `*Shape`. This is enforced by the `ShapeRegistry` intern.
- **MUST**: `Shape.FieldIndex` is O(1) — a `map[string]int` inside `Shape`.
- **MUST**: transitions are cached — `s.Transition("foo")` is O(1) on the second call via an internal map on `*Shape`.
- **MUST NOT**: create a `*Shape` outside the registry. The registry is the single source of truth; direct struct literals break ID stability.
- **MUST NOT**: mutate a `*Shape` after registration. Shapes are immutable values keyed by ID.

## Hot paths

- Every `SetField` on a string-keyed table potentially triggers a shape transition (first set of that key from the current shape).
- Tier 1 inline cache hit: compares `table.shapeID` (uint32 load) against cached uint32 — no `*Shape` deref.
- Tier 2 `GuardShape`: CMP+B.NE on the uint32.

## Known gaps

- **No polymorphic shape support.** Inline caches are single-entry; a site that sees two shapes invalidates and re-fills on every call.
- **No shape eviction.** The registry grows monotonically. A pathological program that generates unique shapes forever would leak shape memory.
- **`Table.shape *Shape` field is dead** — see `kb/modules/runtime/table.md` Known gaps. Nothing in production reads it; it costs one pointer per table for GC.

## Tests

- `runtime/shape_test.go` — ID stability, transitions, registry interning
- `runtime/shape_new_test.go` — transition caching
- `methodjit/emit_table_field_test.go` — Tier 2 `GuardShape` + inline cache lowering
