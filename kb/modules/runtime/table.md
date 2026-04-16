---
module: runtime.table
description: Table data type — associative array with typed native fast paths (ArrayInt / ArrayFloat / ArrayBool) and hidden-class shape-tracked string keys.
files:
  - path: internal/runtime/table.go
  - path: internal/runtime/shape.go
last_verified: 2026-04-17
---

# Runtime Table

## Purpose

The universal object/array type. One `Table` value backs every `{}` literal, every array, every object. Tables have four specialized parts:

1. **Array** — 0-indexed `[]Value` for sequential integer keys.
2. **Typed array** — `ArrayKind` selects `intArray []int64`, `floatArray []float64`, or `boolArray []byte` (1 byte per bool, zero GC pointers). Entered when array grows homogeneously.
3. **Small string-keyed fields** — flat `skeys []string` + `svals []Value` slices, shape-tracked via `shapeID uint32`. Used for ≤`smallFieldCap` (12) keys.
4. **Maps** — `imap` (integer keys out of range), `smap` (>12 string keys), `hash` (everything else: float/bool/table keys).

Metatable support via `metatable *Table`. Concurrency opt-in via `SetConcurrent(true)` installing a `sync.RWMutex`.

## Public API

- `type Table struct` — the struct itself (see `internal/runtime/table.go:28`)
- `type ArrayKind uint8` — `ArrayMixed | ArrayInt | ArrayFloat | ArrayBool`
- `func NewTable() *Table` — bump-allocates the `*Table` from `Heap.tableSlab` (R9).
- `func NewTableSized(arrayHint, hashHint int) *Table` — bump-allocates the `*Table` struct from `Heap.tableSlab` (R9); for `hashHint > 0`, `skeys` comes from `Heap.stringSlab` (R14) instead of `make([]string, 0, hashHint)`.
- `func (t *Table) GetField(key string) Value` — string-keyed lookup; Tier 1 inline cache bypasses this
- `func (t *Table) SetField(key string, v Value)` — shape-transition on first set; fires write barrier on pointer-bearing slots
- `func (t *Table) GetIndex(i int64) Value` / `SetIndex(i int64, v Value)` — array-part access; native fast paths dispatch on ArrayKind
- `func (t *Table) SetConcurrent(on bool)`
- `func (t *Table) SetMetatable(mt *Table)`

## Invariants

- **MUST**: `shapeID uint32` is the authoritative identifier for the string-keyed fields' shape. Tier 1 inline caches and Tier 2 GuardShape both read `shapeID`, never the `shape *Shape` pointer.
- **MUST**: `ArrayKind` transitions are monotonic — a typed array never degrades back to `ArrayMixed` within a single transition step; it either stays or bifurcates into the mixed path and copies.
- **MUST**: every pointer field in `Table` (`imap`, `skeys`-slot entries that are pointer-typed, `smap`, `hash`, `metatable`, `keys`, `shape`) is traced by Go's GC on every cycle.
- **MUST NOT**: store a `shapeID` in one place and a `*Shape` in another without a write that keeps them consistent. `applyShape` / `clearShape` are the only sanctioned mutators.
- **MUST NOT**: add fields to `Table` that production code never reads. Write-only fields cost GC trace visits + write barriers on every mutation (see `kb/modules/runtime/gc.md` — the `shape *Shape` pointer is currently the exact anti-pattern).

## Hot paths

- `object_creation`: ~800k tables, each with 3 field sets → exercises shape transitions + write barriers.
- `sieve`: `ArrayBool` native path — R15 optimization took this from ~0.23s to ~0.18s (−25%).
- `matmul`: 2D `ArrayFloat` table-of-tables → R16 optimization (−80%).
- `nbody`: `ArrayFloat` via Table for vector math.
- `method_dispatch`: many small string-keyed tables → `smallFieldCap=12` threshold matters.

## Known gaps

- **`shape *Shape` field is write-only** — the JIT reads only `shapeID uint32`. This field costs one traced Go pointer per table, which is a structural oddity. **Measured impact**: Round 1 removed the field and re-ran benchmarks; `object_creation` wall-time did NOT close (1.086s → 1.158s, within noise). The field's GC-trace cost is real but below the noise floor on all measured benchmarks. Removing it is a correctness cleanup, not a performance win. Do not promise wall-time from this.
- **No transition stability for typed arrays**: writing a mixed value to an `ArrayFloat` table bifurcates to `ArrayMixed` and copies — no deoptimization record, no path back.
- **`smallFieldCap` is hard-coded to 12**. No profile-driven tuning.
- **SetTable's 4-way arrayKind dispatch** is ~8 ARM64 insns of runtime type switching before any actual store, repeated on every hot-loop store. See `kb/modules/emit/table.md` for the emit-layer view. R13 ADR (`docs-internal/decisions/adr-tier2-inline-cache.md`) accepts per-PC inline caches as the structural answer; implementation is staged across R14+ tactical rounds. Per-site ArrayKind caching is the forward class `tier2-typed-array-ic`.

## Tests

- `runtime/table_test.go` — field get/set, shape transition
- `runtime/shape_test.go` — shape registry + ID stability
- `runtime/shape_new_test.go` — transition behavior
- `methodjit/emit_table_typed_test.go` — Tier 2 typed array fast paths parity with Tier 1
