---
module: runtime.vm
description: VM core — register file, call stack, ExecContext, ScanGCRoots, execute loop entry.
files:
  - path: internal/vm/vm.go
  - path: internal/vm/compiler.go
  - path: internal/vm/opcode.go
  - path: internal/vm/proto.go
  - path: internal/vm/vm_jit_interface.go
last_verified: 2026-04-13
---

# Runtime VM Core

## Purpose

The VM is what runs at Tier 0 (interpreter) and what the JIT exits to on deopt. It owns the register file, call stack, global table, and GC roots. Every exit from Tier 1 or Tier 2 JIT re-enters the VM via an exit-code switch.

## Public API

- `func New(globals map[string]runtime.Value) *VM`
- `func (v *VM) Execute(proto *vm.FuncProto, args ...runtime.Value) ([]runtime.Value, error)`
- `func (v *VM) Regs() []runtime.Value` — current register file slice (alias, not copy)
- `func (v *VM) EnsureRegs(needed int)` — grow register file; 2× capacity policy
- `func (v *VM) ScanGCRoots(visit func(runtime.Value))` — GC entry point for live references
- `func (v *VM) Close()` — release resources

## Invariants

- **MUST**: the register file is a single flat `[]runtime.Value` slice. Multiple frames share it; each frame's register window is indexed by `base` offset.
- **MUST**: `Regs()` returns a live slice, NOT a copy. Callers must not retain across an `EnsureRegs` call (it may reallocate).
- **MUST**: `EnsureRegs` allocates at 2× current capacity when growing, keeping amortized cost low.
- **MUST**: every allocation-capable op point (NewTable, Closure, AppendSetList) is a potential GC trigger. GCRoots must be accurate at that point.
- **MUST**: `ScanGCRoots` visits every live reference reachable from the register file and the global table. Missing a live ref causes use-after-free under concurrent GC.
- **MUST NOT**: scan the entire `regs` slice unconditionally if only a prefix is live — doing so both costs perf (R35 object_creation regression) AND traces dead slots that may contain NaN-boxed non-pointers the GC must ignore.

## Hot paths

- **Allocation-heavy benchmarks**: `object_creation`, `sort`, `closure_bench`, `binary_trees` — GC cycles fire every ~1M allocations; `ScanGCRoots` cost is load-bearing for wall-time.
- **Recursive call**: every cross-tier transition touches `Regs()` + `EnsureRegs`. `ackermann`, `mutual_recursion`, `fib_recursive` exercise this path.

## Known gaps

- **ScanGCRoots scans `vm.regs[:len(vm.regs)]`** and `len(vm.regs)` defaults to 1024 — so shallow benchmarks see 1024-slot scans even when only ~50 slots are live. **Measured impact**: Rounds 1 and 2 tried two different approaches to shrink the scan range (len/cap split in `EnsureRegs`, then a smaller initial slice). Neither produced a wall-time improvement on `object_creation` or any other benchmark, and both caused catastrophic regressions on other benchmarks (Round 1 broke nothing but didn't help; Round 2 broke `fannkuch` 17× because the JIT's cached `RegsEnd` went stale after a slice-swap reallocation). GC scan overhead is not the dominant cost on these benchmarks. Do not try this again without a direct measurement proving the scan is on the critical path first.
- **No generational GC knob**: Go GC handles everything, including the script heap. The Go runtime's write barrier fires for every SetField on a pointer-bearing Table; unboxed field kinds (ArrayBool, ArrayFloat) sidestep this, but the generic path pays.
- **Per-proto FieldCache / GlobalValCache live in the proto itself** — they survive across VM instances. An old proto reused by a new VM carries stale cache entries; `engine.globalCacheGen` bumping invalidates GetGlobal caches but not FieldCache.
- **`RegsEnd` cache coherence** — the Tier 1 JIT caches `execCtx.RegsEnd = &regs[0] + len(regs)*8` at function entry via `tier1_manager.go:288`. `resyncRegs` refreshes it on exit-to-Go. But if `vm.regs` is REALLOCATED (not resliced) while the JIT is running — e.g. via a growth site that uses `newRegs := make(...); copy; vm.regs = newRegs` — the JIT's cached `RegsEnd` points into the freed old slice. This is a correctness footgun, not just a perf issue. Any change to `EnsureRegs` that changes allocation behaviour must preserve the `&regs[0]` pointer or be very careful about resync.

## Tests

- `vm_test.go` — execute loop correctness
- `frame_test.go` — call-stack unwinding
- `ensure_regs_test.go` — 2× growth + reference stability
- `gc_scan_test.go` — GCRoots accuracy across tier transitions
