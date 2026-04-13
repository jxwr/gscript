---
module: feedback
description: Per-proto FeedbackVector — Tier 0 interpreter + Tier 1 baseline JIT collect type, shape, and array-kind observations; Tier 2 BuildGraph + TypeSpecialize consume them to insert GuardType and specialize ops.
files:
  - path: internal/vm/feedback.go
  - path: internal/vm/vm.go
  - path: internal/vm/proto.go
  - path: internal/methodjit/tier1_table.go
  - path: internal/methodjit/graph_builder.go
  - path: internal/methodjit/pass_typespec.go
  - path: internal/runtime/table.go
last_verified: 2026-04-13
---

# Feedback — Type Observations Across Tiers

## Purpose

Tier 0 (interpreter) and Tier 1 (baseline JIT) collect per-bytecode-PC type observations into `proto.Feedback`. Tier 2 reads the feedback at IR-build time to insert `OpGuardType` after feedback-typed ops and at `TypeSpecialize` time to rewrite generic ops into typed variants (`OpAdd`→`OpAddInt`/`OpAddFloat`, `OpGetTable`→typed-array dispatch). This is the V8-style "Tier-1-collects, Tier-2-reads" contract that lets the optimizer specialize on observed types without a static type system.

## Public API

- `type FeedbackType uint8` — monotonic lattice `FBUnobserved → {FBInt,FBFloat,FBString,FBBool,FBTable,FBFunction} → FBAny`.
- `type TypeFeedback struct { Left, Right, Result FeedbackType; Kind uint8 }` — per-PC entry, 4 bytes. `Left/Right/Result` cover operand and result types; `Kind` encodes observed `runtime.ArrayKind` for GetTable/SetTable.
- `type FeedbackVector []TypeFeedback` — `len == len(proto.Code)`, one entry per bytecode instruction.
- `func NewFeedbackVector(codeLen int) FeedbackVector`
- `func (ft *FeedbackType) Observe(vt runtime.ValueType)` — monotonic widen; `Any` is terminal.
- `func (tf *TypeFeedback) ObserveKind(arrayKind uint8)` — monotonic widen for `Kind`; `FBKindPolymorphic` (0xFF) is terminal.
- `func (p *FuncProto) EnsureFeedback() FeedbackVector` — lazy allocator in `internal/vm/proto.go`; called by `TieringManager` before Tier 1 / Tier 2 compile.
- `feedbackToIRType(fb vm.FeedbackType) (Type, bool)` — in `graph_builder.go:943`, maps `FBInt/FBFloat/FBTable` to IR `Type` for guard insertion.

## Invariants

- **MUST**: `Feedback[pc]` is monotonic. Once `FBAny`, stays `FBAny`. Once `FBKindPolymorphic` (0xFF), stays polymorphic. This prevents deopt-reopt cycles when a site sees a megamorphic pattern.
- **MUST**: feedback is per `FuncProto`, not per closure. All closures sharing a proto share feedback.
- **MUST**: `EnsureFeedback` is called by `TieringManager` before Tier 1 compilation in `tier1_compile.go` so the baseline JIT has a feedback buffer to stamp. Grep: `proto.EnsureFeedback()` appears at `tiering_manager.go:149,173,221,627`.
- **MUST**: Tier 0 interpreter writes feedback in-line in the opcode dispatch loop (`internal/vm/vm.go`) for every type-relevant op — `OP_ADD`, `OP_SUB`, `OP_MUL`, `OP_DIV`, `OP_GETTABLE`, `OP_SETTABLE`, `OP_GETFIELD`, `OP_SETFIELD`, `OP_SELF`. Grep: `frame.closure.Proto.Feedback != nil`.
- **MUST**: Tier 1 baseline JIT writes feedback via `emitBaselineFeedbackResult` / `emitBaselineFeedbackResultFromValue` in `tier1_table.go`. These emit ARM64 code that loads `ExecContext.BaselineFeedbackPtr` (`&proto.Feedback[0]`), checks the current slot, and monotonically widens. Grep: `emitBaselineFeedbackResult(asm, pc, ...)`.
- **MUST**: Tier 1 encodes feedback at `pc*4 + 2` for `Result` byte (offset of `TypeFeedback.Result` in the 4-byte struct). Grep: `fbResultOff := pc*4 + 2` in `tier1_table.go`.
- **MUST**: `Kind` feedback is observed by `ObserveKind` which stores `arrayKind + 1` (so 0 means unobserved, 1..4 means concrete `ArrayMixed..ArrayBool`, 0xFF means polymorphic). Grep: `encoded := arrayKind + 1`.
- **MUST**: Tier 2 `BuildGraph` consumes feedback only when `proto.Feedback != nil && pc < len(proto.Feedback)`. Grep: `if b.proto.Feedback != nil && pc < len(b.proto.Feedback)` in `graph_builder.go:622,630,647,671`.
- **MUST**: `OpGuardType` is inserted directly after `OpGetField` and `OpGetTable` when `feedbackToIRType(Feedback[pc].Result)` returns a monomorphic type. The guard's `Aux` field is the IR `Type` value; the SSA result threads through the guard.
- **MUST**: `Kind` feedback is passed as `Aux2` into `OpGetTable` / `OpSetTable` — the emitter's kind-specialized dispatch reads it (see `kb/modules/emit/table.md`).
- **MUST**: `BuildGraph` only emits a GuardType when feedback is monomorphic and matches one of `{FBInt, FBFloat, FBTable}`. `FBString`, `FBBool`, `FBFunction`, and `FBAny` do not drive guard insertion. Grep: `feedbackToIRType` in `graph_builder.go:943`.
- **MUST**: `TypeSpecialize` propagates the post-`GuardType` type forward through the IR, rewriting `OpAdd` → `OpAddInt`/`OpAddFloat`, `OpLt` → `OpLtInt`/`OpLtFloat`, etc.
- **MUST**: `FieldCache` is a parallel structure (`[]runtime.FieldCacheEntry`) indexed by PC and populated by Tier 0 / Tier 1 on GETFIELD/SETFIELD. `BuildGraph` reads `proto.FieldCache[pc]` to inject shape IDs into `OpGetField`/`OpSetField` as `Aux2 = (shapeID<<32) | fieldIndex`. This is distinct from `Feedback` but same collection pattern.
- **MUST NOT**: narrow feedback once widened. `Observe` never transitions from a concrete type back to `FBUnobserved`.
- **MUST NOT**: rely on feedback being populated — `BuildGraph` must tolerate `proto.Feedback == nil` (function never executed before promotion, e.g., tests that promote directly).
- **MUST NOT**: trust feedback past a deopt — after deopt, the interpreter continues collecting, and the next Tier 2 compile will see updated feedback.

## Hot paths

- `nbody`, `spectral_norm`, `mandelbrot` — GETFIELD on body/complex structs; `FBFloat` feedback + `FieldCache` shape entry → native shape-guarded `svals` load with FPR-resident result.
- `sieve` — SETTABLE on an ArrayBool sieve; `FBKindBool` feedback drives the kind-specialized dispatch to the `boolArrayLabel` path without the 4-way cascade.
- Any loop over a homogeneous integer array — `FBKindInt` + `FBInt` Result feedback makes the whole inner loop raw-int on GPRs.

## Known gaps

- **No call-site feedback**: `Feedback[pc].Left` is written for `OpCall` but `pass_inline.go` does not currently read it. Tracked in `pass_inline.go` header comment: "future versions will also use FeedbackVector data from the interpreter; this pass currently only uses static call graph analysis".
- **4 bytes per PC is tight**: only Left/Right/Result/Kind. No count, no site-polymorphism history — once an op hits two distinct types it goes straight to `FBAny`.
- **No per-callee profile**: feedback is per-proto, so a polymorphic helper called from many sites aggregates observations into `FBAny` even when each individual call site is monomorphic.
- **Tier 1 does not observe operand types for arithmetic**: only Tier 0 interpreter observes `Left`/`Right` for `OP_ADD`/`OP_SUB`/`OP_MUL`. Tier 1 native arithmetic bypasses feedback collection, so a function that jumps straight to Tier 1 without interpreter warm-up gets no operand feedback.
- **No shape polymorphism**: `FieldCache` stores exactly one `ShapeID` per PC. Sites that see multiple shapes thrash the cache.

## Tests

- `feedback_test.go` (in `internal/vm`) — `Observe` / `ObserveKind` monotonicity
- `feedback_getfield_integration_test.go` — end-to-end interpreter warm-up → BuildGraph → TypeSpecialize cascade
- `graph_builder_test.go` — BuildGraph feedback consumption and `OpGuardType` insertion
- `pass_typespec_test.go` — TypeSpecialize rewrites driven by post-guard types

## See also

- `kb/modules/emit/guard.md` — where guards get lowered to ARM64
- `kb/modules/emit/table.md` — where `Kind` feedback drives kind-specialized dispatch
- `kb/modules/passes/typespec.md` — the rewrite pass that consumes guarded types
- `kb/modules/tier1.md` — Tier 1's feedback-writing native paths
