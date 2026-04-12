# R35: object_creation Regression Root Cause

## Root Cause

Commit `39b5ef34` ("fix: Shape system, GC scan all regs, split table.go, fix empty-loop test") introduced a +41% regression in the object_creation benchmark (reference 0.764s at `a388f782` to ~1.07-1.15s at HEAD). The commit made two changes that compound to create the regression: (1) the Shape system rewrite added a `shape *Shape` GC pointer field to every `Table` struct, increasing GC scan work proportionally to object allocation rate, and (2) `ScanGCRoots` was changed to scan the entire `vm.regs` slice instead of capping at `frames[-1].base + maxStack`, increasing per-GC-cycle cost. Both changes were correctness fixes (the GC scan cap missed JIT self-call registers, causing SIGSEGV), so the commit cannot be reverted.

## Evidence Chain

- **Bisect output** (verbatim):
```
bisect-witness: median=1.070 threshold=0.802   [a224669 — bad]
bisect-witness: median=1.065 threshold=0.802   [236730a — bad]
bisect-witness: median=1.084 threshold=0.802   [39b5ef3 — bad]
bisect-witness: median=0.745 threshold=0.802   [598bc1e — good]
39b5ef34c9ba58f9e804d0f643412abf8b570e04 is the first bad commit
```
- **Reference time**: 0.764s (from `benchmarks/data/reference.json`, frozen at `a388f782`)
- **Pre-bisect sanity**: HEAD median=1.077s (exit 1, bad), `a388f782` median=0.743s (exit 0, good)
- **Culprit commit**: `39b5ef34` — "fix: Shape system, GC scan all regs, split table.go, fix empty-loop test"
- **Parent commit**: `598bc1e` (self-call DirectEntryPtr check) — confirmed good (median=0.745s)

## Culprit Diff Analysis

The commit touches 5 files (`internal/runtime/shape.go`, `internal/runtime/table.go`, `internal/runtime/table_int.go`, `internal/vm/vm.go`, `tests/trace_exec_test.go`). Two changes create the performance regression:

### Change 1: `Table.shape *Shape` field (table.go:50)

A `shape *Shape` field was added to the `Table` struct (file: `internal/runtime/table.go:50`). Every call to `RawSetString` or `RawSetStringCached` that appends a new key now calls `t.setShape(t.skeys)` (table.go:378-386), which calls `GetShape()` → `getOrCreateShape()`. The hot-path cost is similar to the old `GetShapeID()` (both do `strings.Join` + `sync.Map.Load` on cache hit), but the `*Shape` pointer is a GC-visible reference that Go's garbage collector must trace for every live table.

In the object_creation benchmark:
- `new_vec3(x,y,z)` creates a fresh table + 3 `SetField` calls per invocation
- `create_and_sum` calls `new_vec3` 200,000x, `transform_chain` calls it 500,000x, `complex_objects` creates 100,000 tables with 10 fields each
- Total: ~800K table objects, each now carrying an extra GC pointer

The authoritative context observations confirm this is the hot path:
- `new_vec3` IR: 1 NewTable + 3 SetField, 208 total insns, 129 memory insns (62% memory-bound)
- `create_and_sum`: 813 insns, 466 memory (57.3%)
- All 3 SetField ops in `new_vec3` go through table-exit (shape guard fails on fresh tables with shapeID=0), so `setShape` is called 3x per `new_vec3` invocation

### Change 2: ScanGCRoots uncapped register scan (vm.go:253)

Before this commit, `ScanGCRoots` scanned only `vm.regs[:frames[-1].base + maxStack]`. After, it scans `vm.regs[:len(vm.regs)]`. The register file grows via `EnsureRegs(needed)` which doubles capacity (`make(needed*2)`), so the scanned range can be 2x larger than necessary. With GC compaction triggered every 1M allocations (`gcCompactInterval = 1 << 20` at `internal/runtime/value.go:120`), and object_creation performing ~2.4M+ table+field allocations, this increases GC overhead by scanning unused register slots.

### Compounding effect

The two changes compound: more GC pointers per table (Change 1) + more register slots scanned per GC cycle (Change 2) = multiplicative GC overhead increase on allocation-heavy benchmarks.

## Proposed R36 Forward Fix

Both changes are correctness fixes and cannot be reverted. The forward fix should surgically reduce the overhead:

1. **Remove `shape *Shape` from Table struct** (`internal/runtime/table.go:50`): The `shape` pointer is write-only — nothing in the current codebase reads `Table.shape` on a hot path (the JIT uses `shapeID` directly via `TableOffShapeID`). Removing it eliminates the extra GC pointer per table. Keep `setShape` but make it only set `shapeID = GetShapeID(skeys)` (the old behavior). If `LookupShapeByID` is needed later, it remains available.

2. **Restore bounded ScanGCRoots** (`internal/vm/vm.go:253`): Instead of scanning all registers, track the high-water mark of register usage (`vm.regHighWater`) updated in `EnsureRegs` and the JIT self-call path, then scan `vm.regs[:vm.regHighWater]`. This preserves correctness (JIT self-call registers are included) while avoiding scanning the 2x-capacity unused tail.

3. **Alternative for SetField hot path**: Use `Shape.Transition()` instead of `getOrCreateShape` for incremental field additions — `Transition` skips the `strings.Join` entirely by caching child shapes. This would change `setShape` to: `t.shape = t.shape.Transition(newKey); t.shapeID = t.shape.ID`. But this requires keeping the `shape` pointer, conflicting with fix 1. The tradeoff depends on whether the GC pointer cost or the `strings.Join` cost dominates — needs measurement.

## Risk Notes

- **sort benchmark** (+16.7% drift): `sort` uses `quicksort` which does `GetTable`/`SetTable` (integer-indexed), not `SetField`. The same commit split `table_int.go` from `table.go`, but the extracted code is identical. The sort regression is more likely from the ScanGCRoots change (sort creates arrays via RawSetInt which triggers GC). Same fix 2 applies.
- **closure_bench** (+11.1% drift): Closures don't create tables, but they allocate upvalue objects which go through gcLog. ScanGCRoots scanning all registers increases GC cycle cost even for non-table benchmarks. Fix 2 alone may recover closure_bench.
- **Correctness constraint**: The original bug (SIGSEGV from JIT self-call registers missed by GC) is a crash, not a performance issue. Any fix must provably scan all JIT self-call registers. The high-water-mark approach is safe because `EnsureRegs` is always called before any register write.

## Reproducibility

```bash
# Exact commands to reproduce the bisect
cp scripts/bisect_object_creation.sh /tmp/bisect_object_creation.sh
chmod +x /tmp/bisect_object_creation.sh
git stash --include-untracked
git bisect start b3d8824 a388f782
git bisect run /tmp/bisect_object_creation.sh
# Expected output: 39b5ef34c9ba58f9e804d0f643412abf8b570e04 is the first bad commit
git bisect reset
git stash pop
```
