---
module: passes/inline
description: Monomorphic function inliner with a bytecode-size budget and bounded recursion. Resolves globals via the InlineConfig map; splices callee blocks into the caller's CFG.
files:
  - path: internal/methodjit/pass_inline.go
  - path: internal/methodjit/pass_inline_test.go
last_verified: 2026-04-13
---

# Inline Pass

## Purpose

Replace eligible `OpCall` instructions with the callee's SSA body, eliminating call-exit overhead and enabling cross-function constant propagation, type specialization, and LICM. The pass is monomorphic (callee must resolve statically), size-bounded (default 40 bytecodes), and runs to a fixpoint so newly exposed calls in a freshly inlined body can themselves be inlined.

## Public API

- `type InlineConfig struct { Globals map[string]*vm.FuncProto; MaxSize int; MaxRecursion int }`
- `func InlinePassWith(config InlineConfig) PassFunc` — returns a closure implementing the pass contract
- `const inlineMaxIterations = 5` — hard cap on fixpoint rounds

## Invariants

- **MUST**: callee resolution requires `OpCall.Args[0].Def == OpGetGlobal` and the global name to appear in `config.Globals`. Anything else (closures from `OpClosure`, locals, upvalues) is not inlined.
- **MUST**: callees with `len(proto.Code) > config.MaxSize` are refused, even if otherwise eligible. Default `MaxSize` is 40 (set by `Tier2PipelineOpts`), internal default is 30.
- **MUST**: recursive or mutually-recursive callees are gated by `config.MaxRecursion` per caller; non-recursive callees are never gated. Production uses `MaxRecursion = 2`.
- **MUST**: each inlining round calls `BuildGraph(calleeProto)` fresh to produce a callee IR whose value IDs are renumbered to avoid collisions with the caller.
- **MUST**: single-block callees go through `inlineTrivial` (flat splice); multi-block callees go through `inlineMultiBlock` (CFG surgery — caller block is split at the call site, callee blocks are inserted, control merges into a fresh merge block).
- **MUST**: after any inlining happens, `relinkValueDefs(fn)` is called to rewire placeholder `Value.Def` pointers so downstream passes see live `*Instr`s.
- **MUST**: the fixpoint driver stops when a round inlines nothing OR `inlineMaxIterations = 5` is reached.
- **MUST**: `SimplifyPhis` and `TypeSpecialize` run immediately after inline in `RunTier2Pipeline` because the spliced blocks introduce new phis and restore generic ops that need specialization.
- **MUST NOT**: inline a call that hits `fn.Globals == nil && config.Globals == nil` (e.g. test harness with no global map).
- **MUST NOT**: mutate caller blocks through the unsnapshotted `fn.Blocks` slice during a round — `inlineCalls` snapshots `origBlocks` before iterating.

## Hot paths

- `method_dispatch` — many small object methods; inlining is the only way to strip call overhead.
- `fibonacci_iterative` — tiny helper predicates fold away after inlining + ConstProp.
- `closure_bench` — callee bodies are single-block leaves and inline trivially.

## Known gaps

- **Globals only.** Closures captured in upvalues or locals are never inlined, even if monomorphic at the call site.
- **No polymorphic inline cache.** A call site that sometimes targets function A and sometimes B is not inlined at all (and no IC fallback).
- **Size budget is bytecode-count only.** A 39-bytecode callee with a complex inner loop may inline while a 41-bytecode callee with trivial body does not. No cost model.
- **Call-in-loop exclusion** is enforced elsewhere: `hasCallInLoop(fn)` in `tiering_manager.go` refuses the whole Tier 2 promotion if a call remains in a loop after inlining. This is a hard backstop, not a feature of the pass.
- **No devirtualization** of method calls — `OpSelf` is never inlined.

## Tests

- `pass_inline_test.go` — fixpoint convergence, recursive gating, size budget rejection, trivial vs multi-block splice, relinking of `Value.Def`.
