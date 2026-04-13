---
module: emit.overview
description: ARM64 code emission layer. Per-IR-op emitters, fused compare+branch, deopt exit-resume, two entry points per function.
files:
  - path: internal/methodjit/emit.go
  - path: internal/methodjit/emit_compile.go
  - path: internal/methodjit/emit_dispatch.go
  - path: internal/methodjit/emit_execute.go
  - path: internal/methodjit/emit_reg.go
  - path: internal/methodjit/emit_op_exit.go
last_verified: 2026-04-13
---

# Emit — ARM64 Code Generation (Overview)

## Purpose

Turn SSA IR + RegAllocation into a single mmap'd ARM64 code region. Each IR op has one or more emit functions; fused shapes (cmp+branch, load+guard, store+barrier) are matched in `emit_dispatch.go`. This layer decides "how does a computation actually run on M4".

**Historical note**: most of GScript's wall-time wins (R15–R22) came from this layer, not from new IR passes. Emit is where target-specific optimization lives.

## Public API

- `func Compile(fn *Function, alloc *RegAllocation) (*CompiledFunction, error)`
- `type CompiledFunction struct` — mmap'd code + resume addresses + direct entry offset + num regs used
- `func (cf *CompiledFunction) Free()` — unmap

Internal per-op emitters (`emit_*.go`): `emitAdd`, `emitMul`, `emitGetField`, `emitCallNative`, etc. Each is a method on the emit state struct that writes ARM64 instruction words.

## Invariants

- **MUST**: every emitted function has two entry points — normal (saves X19–X28+FP+LR, 128-byte frame) for Execute() entry, and direct (16-byte frame, reloads fixed regs from ctx) for BLR callers.
- **MUST**: every register live across a `Call` is spilled to its regalloc slot before the BLR and reloaded afterward.
- **MUST**: `Int48Safe` values skip the `SBFX+CMP+B.NE` overflow check (3 saved instructions per op). The emitter reads `fn.Int48Safe` directly.
- **MUST**: type guards lower to a single `CMP+B.NE exit_label` sequence; a contiguous run of guards can share flags if no intervening op overwrites NZCV.
- **MUST**: exit-resume ops (NewTable, Concat, Len, Closure, etc.) write the target PC into `ctx.ExitResumePC` and exit with code 6. The execute loop dispatches to the right resume handler.
- **MUST NOT**: emit absolute addresses that survive into a parity comparison — all runtime-resolved addresses (mmap base, constant pool pointers, global proto pointers) are part of the expected parity-test diff. See `TestDiag_ProductionParity_*`.
- **MUST NOT**: use a register from the GPR pool (`X20/X23/X28`) without consulting the RegAllocation; the allocator does not inspect emit-side scratch use.

## Hot paths

- **Arithmetic**: `emit_arith.go` for Add/Sub/Mul/Div + Int/Float specializations. Fused compare+branch in `emit_fused_branch.go` — R11 win.
- **Tables**: `emit_table_field.go` (inline field cache), `emit_table_array.go` (native array kinds, R15 win for sieve −18–25%).
- **Calls**: `emit_call_native.go` + `emit_call_exit.go` — native BLR with spill/reload, fallback to exit-resume.
- **Globals**: `emit_global.go` — native value cache dispatch (R17 win, nbody −49%).
- **Loops**: `emit_loop.go` — FORPREP/FORLOOP with GPR counter carry + fused compare (R11).
- **Deopt**: `emit_op_exit.go` — all exit-resume sites share a common lowering that flushes dirty regs and stamps the resume PC.

## Known gaps

- **Peephole is ad-hoc**: fusion is done at emit time via dispatch lookups, not a dedicated peephole pass. Missed fusion opportunities (e.g., `CBZ` for zero-compare) exist.
- **No scheduling**: instructions are emitted in block order; a real scheduler could further exploit M4's 6–8-wide pipeline.
- **Shape guards are re-emitted per use**: a dedup pass exists at the IR level (`GuardType` CSE), but same-shape guards at different PCs still each emit a CMP+B.NE.

## Tests

- `emit_test.go`, `emit_ops_test.go` — per-op emission correctness against an interpreter oracle
- `emit_tier2_correctness_test.go` — end-to-end Tier 2 correctness for every op
- `emit_fused_branch_test.go`, `emit_int_counter_test.go` — R11 win preservation
- `emit_table_test.go`, `emit_table_typed_test.go` — R15/R16 native fast-path preservation

## See also

- `kb/modules/emit/table.md` — native array kinds, inline field cache
- `kb/modules/emit/call.md` — native BLR, self-call, exit-resume
- `kb/modules/emit/global.md` — GETGLOBAL value cache
- `kb/modules/emit/guard.md` — guard lowering, CSE, hoisting
- `kb/modules/emit/arith.md` — int48 overflow elision, fused compare+branch
