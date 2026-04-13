---
module: runtime.gc
description: Garbage collection integration. Go GC is the substrate — GScript objects are Go heap objects. Write barriers, ScanGCRoots, and the allocation_heavy ceiling.
files:
  - path: internal/vm/vm.go
  - path: internal/runtime/table.go
  - path: internal/runtime/shape.go
last_verified: 2026-04-13
---

# Runtime GC

## Purpose

GScript does not have a custom garbage collector. Every `Table`, `VMClosure`, string, and runtime object is a Go heap object traced by Go's GC. The JIT communicates with the GC through two contracts: `ScanGCRoots` enumerates live references, and pointer-bearing stores hit Go's write barrier automatically.

## Public API

(This module has no public Go API of its own — it describes emergent behavior of `runtime.Table`, `runtime.Value`, and `vm.ScanGCRoots`.)

- `func (v *VM) ScanGCRoots(visit func(runtime.Value))` — authoritative live-root enumerator
- `type runtime.Value uint64` — NaN-boxed. `IsPointer() bool` + `Pointer() unsafe.Pointer` for tracing. Non-pointer NaN-box variants (int, bool, nil, float) are skipped.
- `type runtime.Table struct` — holds `shape *Shape`, `svals []runtime.Value`, `nvals []float64`, `bvals []bool`, native-kind arrays. Every field that can hold a GC pointer is traced.

## Invariants

- **MUST**: every field in `Table` / `VMClosure` / other runtime objects that can hold a pointer is a Go pointer type (not a `uintptr`). This guarantees Go GC traces it.
- **MUST**: unboxed native kinds (`ArrayBool`, `ArrayFloat`, `ArrayInt`) do NOT hold GC pointers in their payload. They skip write barriers entirely for the primitive slot write.
- **MUST**: `ScanGCRoots` enumerates **all** live references reachable from the VM. A missed reference causes use-after-free under concurrent GC — manifests as SIGSEGV deep in compacted-object traversal.
- **MUST NOT**: store a `*Table` or `*VMClosure` pointer in a field that Go's GC does not trace (e.g., `uintptr`, NaN-boxed int slot) without a parallel Go pointer keeping it alive.
- **MUST NOT**: add fields to `Table` that are never read by production code. Write-only pointer fields still cost a write-barrier and a trace visit on every GC cycle — this is the `shape *Shape` field's exact failure mode in R35 (+25 pp of object_creation regression).

## Hot paths

- **Allocation-heavy benchmarks**: `object_creation` (~800k tables), `sort` (~N-log-N allocations), `closure_bench` (many closures), `binary_trees` (recursive tree construction). Every allocation may trigger a GC cycle; cycle cost scales with root count.

## Known gaps (allocation_heavy ceiling)

- **Go GC cannot be bypassed** for pointer-bearing objects. Per-benchmark ceiling on `object_creation`, `sort`, `binary_trees` is set by Go's STW + tracing overhead, not by the JIT's codegen. The memory-level win is to reduce pointer count per object (every write-only field is pure cost).
- **No custom bump allocator**. A Tier-2-only bump allocator for short-lived Tables would be a large-scope architectural change (requires escape analysis) — candidate for a global architecture round.
- **Write barrier is per-store**. `SetField` on a pointer slot fires a barrier every time. The native-kind fast paths (`ArrayBool`, `ArrayFloat`) only help when the whole table is typed — mixed tables still go through the generic barrier-hitting path.
- **ScanGCRoots is currently full-slice**, not high-water-mark. See `kb/modules/runtime/vm.md` Known gaps. Fix is ~20 lines.

## Tests

- `vm/gc_scan_test.go` — root enumeration correctness
- `runtime/table_test.go` — write barrier invariants
- Benchmarks: `object_creation.gs`, `sort.gs`, `closure_bench.gs` — any regression here typically traces back to a write-barrier or ScanGCRoots change
