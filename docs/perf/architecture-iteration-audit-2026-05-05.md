---
title: "Architecture Iteration Audit"
date: 2026-05-05
---

# Architecture Iteration Audit

This note records the current engineering bottleneck after the native
coroutine and actors-dispatch rounds. It is intentionally an iteration guide,
not a redesign proposal.

## Current Finding

Performance iteration is now limited more by cross-layer coupling than by a
single missing peephole. Small changes often cross VM execution, Tier 1,
Tier 2, native call protocol, exit/resume, feedback, and runtime table
representation. That makes local changes hard to price before benchmarking.

The most recent examples:

- `producer_consumer_pipeline` improved materially after a runtime payload and
  native coroutine switch path, but the work had to coordinate VM coroutine
  state, Tier 1 continuation entries, and Method JIT counters.
- `actors_dispatch_mutation` still has a large LuaJIT gap even with low exit
  counts. Profiling showed the cost sits in the Tier 2 native call protocol
  and generated code body, not simply in exit frequency.
- A generic dynamic-call proto-fact experiment skipped some call protocol
  traffic, but measured only a small improvement. That is a signal to improve
  observability before adding more protocol branches.

## Friction Points

### `methodjit` Owns Too Many Roles

`internal/methodjit` contains IR, passes, register allocation, ARM64 emission,
Tier 1, Tier 2, tiering policy, exit handling, diagnostics, and benchmark-era
protocols. Several files are large enough that small changes require broad
context:

- `tiering_manager.go`: tier policy, compile orchestration, execution, gates
- `emit_call_native.go`: dynamic call IC, direct entries, context save/restore,
  native-call exits, tail calls, numeric and typed self-call protocols
- `emit_compile.go`: code layout, emit context, direct entries, numeric pass,
  resume entries
- `emit_table_array.go`: array lowering, typed stores, table kernels, lookup
  cache emission

This is not primarily a line-count problem. The issue is that protocol
semantics are embedded in emitter control flow rather than represented as
separate contracts.

### `ExecContext` Is A Shared Protocol Bag

`ExecContext` currently carries baseline exits, Tier 2 exits, native callee
resume state, global caches, coroutine and call state, register bounds, and
debug counters. It works, but it makes protocol changes risky: every added
field or branch has to be audited against multiple entry modes.

Near-term action should be small extraction, such as typed descriptor builders
or helpers around native-call exit fields. Avoid a wholesale context redesign.

### Pipeline Ordering Is Hard To Reason About

`RunTier2Pipeline` is a production-accurate manual chain. That is good for
parity, but the pass order has become hard to inspect:

- several passes run multiple times;
- remarks are attached manually after selected passes;
- the correct insertion point for a new pass is often discovered by trial;
- synthetic pass tests can pass while production IR never reaches the pass.

Near-term action should add production pipeline observability: stage labels,
pass timing, and changed/unchanged summaries in warm dumps. Do not reorder
passes as part of the first cleanup.

### Structural Kernels Are Useful But Distort Priorities

The VM has structural whole-call and driver-loop kernels. They are guarded by
bytecode/shape facts rather than benchmark names, so they are not necessarily
case-specific hacks. Still, their presence can make the benchmark table look
healthier than the general optimizer really is.

Future work should report kernel coverage separately from general Tier 2 wins.

## Allowed Cleanup Scope

These changes are low risk and should be parallelizable:

- add diagnostics that explain wall-time cost by protocol or IR/code range;
- add pass timing and stage summaries to the production warm dump;
- extract tiny helper functions for call/exit protocol setup;
- add production-pipeline assertion tests for new passes;
- document tiering decisions and protocol invariants close to the code.

## Deferred Scope

These are too broad for the current performance iteration:

- splitting `internal/methodjit` into many packages;
- redesigning `ExecContext`;
- replacing the Tier 2 pipeline builder;
- rewriting native call ABI;
- removing structural kernels before equivalent generic mechanisms exist.

## Next Iteration Rule

Before implementing another performance optimization, collect one of:

- warm dump showing the exact IR/code path to be changed;
- pprof or JIT PC mapping that identifies a protocol/code range cost;
- benchmark evidence that the target is stable under scaled timing;
- a production-pipeline test proving the new pass fires on real warmed IR.

If none of those exists, add diagnostics first.
