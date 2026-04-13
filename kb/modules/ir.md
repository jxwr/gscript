---
module: ir
description: SSA intermediate representation — Function/Block/Value data structures, BuildGraph, IR printing and validation. The substrate all Tier 2 passes operate on.
files:
  - path: internal/methodjit/ir.go
  - path: internal/methodjit/ir_ops.go
  - path: internal/methodjit/graph_builder.go
  - path: internal/methodjit/graph_builder_ssa.go
  - path: internal/methodjit/validator.go
  - path: internal/methodjit/printer.go
last_verified: 2026-04-13
---

# IR — SSA Intermediate Representation

## Purpose

The middle layer between bytecode and ARM64. Every Tier 2 pass reads and writes `Function`. BuildGraph is the entry point; Emit is the exit point. Uses the Braun et al. 2013 "Simple and Efficient Construction of SSA Form" algorithm — single forward pass, lazy phi insertion, no dominance-frontier computation.

## Public API

- `func BuildGraph(proto *vm.FuncProto) *Function` — bytecode to SSA IR (`graph_builder.go`)
- `func Validate(fn *Function) []error` — structural invariant check; must return empty after every pass
- `func Print(fn *Function) string` — human-readable dump; used by Diagnose and diag harness
- `type Function struct` — entry block, all blocks (RPO), source proto, Int48Safe set, Globals hook, Unpromotable flag
- `type Block struct` — basic block with values + successors + predecessors
- `type Value struct` — single SSA value (op, operands, type, use count)

## Invariants

- **MUST**: every `Block` in `Function.Blocks` is reachable from `Entry` and appears in reverse postorder.
- **MUST**: every `Value` has a stable integer `ID` assigned by `Function.nextID`; IDs are not reused.
- **MUST**: phi operands list matches predecessor list position-for-position.
- **MUST**: after any pass, `Validate(fn)` returns no errors. `pipeline.go` runs Validate immediately after `BuildGraph` and again right before `AllocateRegisters`.
- **MUST**: `fn.Int48Safe` is only populated by `RangeAnalysisPass`; emitters read it to skip overflow checks on provably safe int arithmetic.
- **MUST NOT**: any pass creates a value with `Type == TypeFloat` whose operand chain includes NaN-boxed values unless a `GuardType TypeFloat` protects the chain.
- **MUST NOT**: BuildGraph silently drops `OP_CALL` with `B==0` (variadic). It sets `fn.Unpromotable = true` instead; `compileTier2Pipeline` refuses the promotion.

## Hot paths

Every benchmark that reaches Tier 2 compiles through `BuildGraph` exactly once per promoted proto. Hottest consumer: the pass pipeline in `tiering_manager.go:compileTier2Pipeline`.

Benchmarks that stress IR throughput (many small promoted functions): `method_dispatch`, `object_creation`, `closure_bench`.

## Known gaps

- `BuildGraph` does not model `OP_CALL B==0` (variadic top-tracker). See `Unpromotable` flag.
- No cross-block CSE in BuildGraph — ConstProp and LoadElim pick up some of this; a dedicated GCSE pass does not exist.
- `ValueID` reuse across passes is not supported; passes that need to attach metadata keep side tables keyed on ID.

## Tests

- `graph_builder_test.go` — BuildGraph coverage for each bytecode class, including the B==0 rejection path.
- `validator_test.go` — invariant checks (dangling values, phi mismatch, unreachable blocks).
- `interp_test.go` — IR interpreter correctness (`Interpret(fn, args)` matches `vm.Execute(proto, args)`) — the correctness oracle for all pass work.
