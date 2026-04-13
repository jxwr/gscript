---
module: passes/type_specialize
description: Forward type propagation + generic-to-specialized op rewrite. The core of speculative numeric optimization; runs three times in the Tier 2 pipeline.
files:
  - path: internal/methodjit/pass_typespec.go
  - path: internal/methodjit/pass_typespec_test.go
last_verified: 2026-04-13
---

# TypeSpecialize Pass

## Purpose

Replace type-generic IR ops (`OpAdd`, `OpLt`, `OpUnm`, ...) with type-specialized variants (`OpAddInt`, `OpLtFloat`, `OpNegInt`, ...) when operand types can be proven statically. Uses SSA-local forward type inference, iterating to a fixed point over phis. Also inserts speculative `OpGuardType` on parameters used in numeric contexts so downstream code can specialize parameter-dependent arithmetic.

## Public API

- `func TypeSpecializePass(fn *Function) (*Function, error)`

## Invariants

- **MUST**: the pass runs at least three times in production — after `SimplifyPhis`, after `Intrinsic`, and after `Inline`. Omitting any of these runs leaves generic ops in the IR and regresses inlined / post-intrinsic code.
- **MUST**: every instruction whose inferred type becomes known has `instr.Type` written before the pass returns (`specialize` loop at pass end).
- **MUST**: `OpAdd/Sub/Mul/Mod` with both operands `TypeInt` rewrite to `OpAddInt/SubInt/MulInt/ModInt`; both operands numeric (at least one float) rewrite to the `*Float` variant.
- **MUST**: `OpDiv` always infers `TypeFloat` (Lua semantics — integer division is a separate `OpFloorDiv`).
- **MUST**: `OpPhi` type agreement uses a "skip unknown" rule so loop-carried phis can converge: the first known arg seeds, subsequent agreeing args confirm, int+float widens to float, any other mismatch yields unknown.
- **MUST**: parameter guards inserted by `insertParamGuards` / `insertFloatParamGuards` use `OpGuardType` with the target type in `Aux`; the emitter reads `Aux` as the expected type.
- **MUST**: fixed-point propagation is capped at 10 iterations (`for pass := 0; changed && pass < 10`); deeper nests rely on the second/third pipeline run to reach convergence.
- **MUST NOT**: specialize an op whose operand types are `TypeUnknown` — the generic op remains and the emitter handles it.
- **MUST NOT**: overwrite an existing `instr.Type` that is already concrete with `TypeUnknown`.

## Hot paths

Every numeric benchmark routes through this pass:
- `fibonacci_iterative`, `sieve`, `fannkuch` — int specialization of loop-carried counters
- `nbody`, `spectral_norm`, `mandelbrot` — float specialization with parameter guards (without guards, `2.0 * size / size` in spectral_norm stays generic)
- `partial_sums` — mixed int/float widening via the phi rule

## Known gaps

- **No feedback vector usage.** Inference is pure SSA-local. Interpreter feedback (`vm.FeedbackVector`) is not consulted — callers whose feedback shows 100% int still need a `GuardType` inserted speculatively.
- **No call return-type inference.** `OpCall` always yields `TypeUnknown`, so arithmetic on a call result is never specialized (inlining is the only way out).
- **No union types.** An int-or-nil phi collapses to `TypeUnknown`, not to a tagged union.
- **String concat (`..`)** is not specialized — remains `OpConcat`.

## Tests

- `pass_typespec_test.go` — covers constant propagation to types, phi convergence, parameter-guard insertion, int+float widening.
