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

- **ScanGCRoots scans the full register slice** (as of 2026-04-13). Post-R35 analysis identified this as ~25 percentage points of the `object_creation +49%` drift vs reference. A high-water-mark field in the VM would scan only `regs[:regHighWater]`, skipping the 2×-capacity tail.
- **No generational GC knob**: Go GC handles everything, including the script heap. The GO runtime's write barrier fires for every SetField on a pointer-bearing Table; unboxed field kinds (ArrayBool, ArrayFloat) sidestep this, but the generic path pays.
- **Per-proto FieldCache / GlobalValCache live in the proto itself** — they survive across VM instances. An old proto reused by a new VM carries stale cache entries; `engine.globalCacheGen` bumping invalidates GetGlobal caches but not FieldCache.

## Tests

- `vm_test.go` — execute loop correctness
- `frame_test.go` — call-stack unwinding
- `ensure_regs_test.go` — 2× growth + reference stability
- `gc_scan_test.go` — GCRoots accuracy across tier transitions
