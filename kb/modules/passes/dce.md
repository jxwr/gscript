---
module: passes/dce
description: Dead code elimination. Fixed-point removal of instructions whose results are unused and which have no observable side effects.
files:
  - path: internal/methodjit/pass_dce.go
  - path: internal/methodjit/pass_dce_test.go
last_verified: 2026-04-13
---

# DCE Pass

## Purpose

Remove SSA instructions whose result is never referenced and which carry no side effect. Runs to a fixed point so that removing one dead value (which decrements its operands' use counts) can cascade. Called late in the pipeline so all upstream passes (constprop, load-elim, intrinsic) have had a chance to create the dead instructions DCE collects.

## Public API

- `func DCEPass(fn *Function) (*Function, error)`
- `func hasSideEffect(instr *Instr) bool` — the canonical side-effect oracle used throughout the package (LICM, LoadElim also consult side-effect classification, though via their own whitelists)

## Invariants

- **MUST**: a value is removable iff `useCounts[instr.ID] == 0 && !hasSideEffect(instr)`.
- **MUST**: fixed-point iteration continues as long as at least one instruction was removed in the previous round (`for { ... if !removed { break } }`).
- **MUST**: `hasSideEffect` returns `true` for:
  - Control flow: `OpJump`, `OpBranch`, `OpReturn`
  - Stores: `OpStoreSlot`, `OpSetGlobal`, `OpSetUpval`
  - Table mutations: `OpSetTable`, `OpSetField`, `OpSetList`, `OpAppend`
  - Calls: `OpCall`, `OpSelf`
  - Guards: `OpGuardType`, `OpGuardNonNil`, `OpGuardTruthy`
  - For-loop control: `OpForPrep`, `OpForLoop`, `OpTForCall`, `OpTForLoop`
  - Closure creation: `OpClosure`, `OpClose`
  - Channel/goroutine: `OpGo`, `OpMakeChan`, `OpSend`, `OpRecv`
  - Phi nodes: `OpPhi` (kept because they participate in SSA structure)
- **MUST**: `OpNop` is NOT in the side-effect set — the redundant-guard-rewrite in LoadElim depends on DCE collecting those Nops.
- **MUST NOT**: remove an instruction with zero uses but a side effect (e.g. a `GuardType` whose result is unused but whose deopt trigger is load-bearing).
- **MUST NOT**: remove a phi even when it has zero uses — the phi-removal job belongs to `SimplifyPhisPass`, which has structural awareness.
- **MUST NOT**: skip blocks; every block's instruction list is rewritten per round.

## Hot paths

Every benchmark routes through DCE exactly once per Tier 2 compilation. The biggest wins are on:
- **Post-intrinsic**: collecting the `OpGetGlobal`/`OpGetField` chain left dead by `IntrinsicPass`.
- **Post-constprop**: collecting the now-dead operands of folded arithmetic.
- **Post-inline**: collecting the dead `OpReturn` / bridge values introduced by callee splicing.
- **Post-load-elim**: collecting the redundant `OpGetField` loads and `OpNop`s.

## Known gaps

- **No dead-store elimination.** A SetField whose stored value is never read is kept — it is classified as a side effect.
- **No unreachable-block removal.** Blocks with no predecessors (except `Entry`) are kept; the validator catches malformed ones but DCE does not collapse them.
- **No partial dead code elimination.** A value dead on some paths but live on others is always kept.
- **No dead-phi cleanup** beyond what SimplifyPhis already does — DCE defers to it entirely.

## Tests

- `pass_dce_test.go` — removable cases (unused arithmetic, ConstInt with no users), non-removable cases (every op in `hasSideEffect`), fixed-point cascading, and post-LoadElim `OpNop` collection.
