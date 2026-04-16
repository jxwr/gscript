---
module: runtime.gc
description: Garbage collection integration. Go GC is the substrate — GScript objects are Go heap objects. Write barriers, ScanGCRoots, and the allocation_heavy ceiling.
files:
  - path: internal/vm/vm.go
  - path: internal/runtime/table.go
  - path: internal/runtime/shape.go
last_verified: 2026-04-17
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

- **Go GC cannot be bypassed** for pointer-bearing objects. Per-benchmark ceiling on `object_creation`, `sort`, `binary_trees` is set by Go's STW + tracing overhead, not by the JIT's codegen.
- ~~**No custom bump allocator**~~ — **partially closed in R9 + R14**. `Heap.tableSlab` (R9) bump-allocates `*Table` structs from a []Table backing; `Heap.stringSlab` (R14) bump-allocates `[]string` sub-slices for `skeys`. Both are Go-heap-backed (interior pointers are scanned by Go GC). Residual mallocgc per NEWTABLE is only the `array` slice header (already arena-backed via `DefaultHeap.AllocValues` — mmap, non-GC). R9 shipped −6.3% object_creation, −11.7% binary_trees; R14 predicted another 3-10%.
- **Write barrier is per-store**. `SetField` on a pointer slot fires a barrier every time. The native-kind fast paths (`ArrayBool`, `ArrayFloat`) only help when the whole table is typed — mixed tables still go through the generic barrier-hitting path.
- ~~**Binary trees JIT slower than VM**~~ — closed in Round 5 via a `shouldStayTier0` gate in `func_profile.go`. Small (≤25 bytecodes), no-loop, allocation-heavy, call-having functions now skip Tier 1 compilation entirely. binary_trees 1.997s → 1.570s (−21.4%). Compounded to 1.391s (−30.3%) after R9. Root cause was Tier 1's exit-resume cost on NEWTABLE dominating the native-template win for tiny recursive allocators; R9 further reduces the per-NEWTABLE mallocgc.

## Measured non-gaps (tested and found to be either incorrect narratives or below the noise floor)

- **~~Pre-existing `shape *Shape` GC pointer costs 25pp of `object_creation +42%` drift~~** — this was the R35 knowledge doc's claim. Round 1 removed the field and saw **zero wall-time movement** on `object_creation`. The field is a structural oddity, not a measurable cost on these benchmarks. See `kb/modules/runtime/table.md` Known gaps.
- **~~ScanGCRoots full-slice scan costs 25pp~~** — Rounds 1 and 2 both tried to shrink the scan range. Neither moved `object_creation` wall-time. Round 2's small-initial-slice variant caused a `fannkuch` 17× regression due to stale JIT `RegsEnd` cache. GC scan is NOT the dominant cost on these benchmarks. See `kb/modules/runtime/vm.md` Known gaps for the correctness footgun.
- **~~The `object_creation +42%` drift vs `reference.json` is a closable regression~~** — updated after R9 + R14. R9's tableSlab closed 6.3% of the drift (1.057 → 0.990s); R14's stringSlab is expected to close another 3-10%. The remaining drift (~25% vs reference) is tractable via the same runtime-allocation-path class but with diminishing returns per round.

## Tests

- `vm/gc_scan_test.go` — root enumeration correctness
- `runtime/table_test.go` — write barrier invariants
- Benchmarks: `object_creation.gs`, `sort.gs`, `closure_bench.gs` — any regression here typically traces back to a write-barrier or ScanGCRoots change
