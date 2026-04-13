---
module: tier1
description: Baseline JIT. 1:1 bytecode → ARM64 templates, no IR, no optimization. Inline field caches, native BLR, OSR counter.
files:
  - path: internal/methodjit/tier1_manager.go
  - path: internal/methodjit/tier1_compile.go
  - path: internal/methodjit/tier1_call.go
  - path: internal/methodjit/tier1_table.go
  - path: internal/methodjit/tier1_arith.go
  - path: internal/methodjit/tier1_control.go
  - path: internal/methodjit/tier1_handlers.go
last_verified: 2026-04-13
---

# Tier 1 — Baseline JIT

## Purpose

Run every function natively on first call. No IR, no optimization, no analysis. Each bytecode compiles to a short ARM64 template; unsupported bytecodes use exit-resume (exit to Go, run the op in the VM, resume the JIT at the next PC).

## Public API

- `func CompileBaseline(proto *vm.FuncProto) (*BaselineFunc, error)`
- `type BaselineFunc struct` — compiled function with two entry points (normal + direct) and per-PC resume stubs
- `type BaselineJITEngine` — implements `vm.MethodJITEngine` for standalone baseline mode

## Invariants

- **MUST**: two entry points per function — normal (96-byte frame, X19–X28+FP+LR saved, used by `Execute()`) and direct (16-byte frame, only FP+LR, used by native BLR).
- **MUST**: every value lives NaN-boxed in its VM register slot. Tier 1 never unboxes into FPRs.
- **MUST**: `GETFIELD`/`SETFIELD` use per-PC inline caches in `proto.FieldCache[pc]`, shape-guarded by `shapeID`.
- **MUST**: `GETGLOBAL` uses a per-PC value cache in `proto.GlobalValCache[pc]` with generation-based invalidation (`engine.globalCacheGen`).
- **MUST**: `CALL` emits the native BLR sequence (~18 insns) that increments callee's `CallCount` and falls to slow path at `Tier2Threshold` — this is the smart-tiering entry point.
- **MUST**: `FORLOOP` decrements `ctx.OSRCounter` on back-edge; at zero, exit with `ExitOSR` (code 9) so TieringManager can compile Tier 2.
- **MUST NOT**: emit any code that depends on SSA IR. Tier 1 sees only `vm.FuncProto` bytecode.

## Hot paths

Call-heavy benchmarks (`ackermann`, `fib_recursive`, `mutual_recursion`, `method_dispatch`) stay in Tier 1 because the smart tiering policy treats "calls only, no loops" as better served by Tier 1's native BLR (~10 ns) than Tier 2 BLR (~15–20 ns).

Hottest sites:
- `tier1_call.go` — native BLR call sequence, self-call optimization, CallCount increment
- `tier1_table.go` — inline field cache hit path (GETFIELD, SETFIELD), native array/bool/float fast paths
- `tier1_global.go` — GETGLOBAL value cache hit path
- `tier1_dispatch.go` — top-level bytecode-to-template dispatch

## Known gaps

- No intra-function CSE; each GETFIELD/GETGLOBAL re-checks its cache independently even when the same pc fires twice in a basic block.
- No FPR residency; every float op pays NaN-box unbox + re-box.
- Inline cache is per-PC, not per-shape — polymorphic sites thrash.

## Tests

- `tier1_arith_test.go`, `tier1_control_test.go`, `tier1_table_test.go` — per-category template coverage
- `tier1_fib_dump_test.go`, `tier1_ack_dump_test.go` — golden instruction dumps for recursive fixtures
- `tier1_call_regression_test.go` — catches self-call / BLR fallback regressions
- `tier1_selfcall_lazyflush_test.go` — lazy context flush correctness
